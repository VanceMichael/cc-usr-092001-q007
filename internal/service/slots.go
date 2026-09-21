package service

import (
	"fmt"
	"sort"
	"time"

	"example.com/batch-092001-q007/internal/domain"
)

// 前置项相对开展时刻的提前量。
var prereqLead = map[string]time.Duration{
	domain.PrereqTransport: 48 * time.Hour,     // 实体件到场
	domain.PrereqInsurance: 72 * time.Hour,     // 保单生效
	domain.PrereqPermit:    7 * 24 * time.Hour, // 公开展示许可
	domain.PrereqTeam:      24 * time.Hour,     // 安装班组到位
	domain.PrereqPlayback:  24 * time.Hour,     // 现场试播通过
}

// ProposeSlot 提议一条排期：执行全部静态规则与占用冲突检查，通过后落为 proposed，
// 并生成安装前置项。排期时间按展馆时区落位，保留当地挂钟表示。
func (s *Service) ProposeSlot(in SlotRequest) (SlotView, error) {
	cx, err := s.buildContext(in)
	if err != nil {
		return SlotView{}, err
	}

	start, end, localStart, localEnd, err := s.localizeWindow(cx.Venue, in.Start, in.End)
	if err != nil {
		return SlotView{}, err
	}

	if issues := domain.ValidateSlot(domain.SlotContext{
		Work: cx.Work, Venue: cx.Venue, Loan: cx.Loan,
		Components: cx.Components, Equipment: cx.Equipment, Rights: cx.Rights,
	}, start, end); len(issues) > 0 {
		return SlotView{}, conflict("slot_validation_failed", "排期未通过校验: %s", joinIssues(issues))
	}

	if occ := s.findOccupancy(cx.Components, cx.Equipment, start, end, ""); len(occ) > 0 {
		return SlotView{}, conflict("double_booked", "%s: %s", domain.ErrOverlap, joinOccupancy(occ))
	}

	now := s.now()
	sl := domain.Slot{
		Ref: s.store.NextID("SLOT"), VenueRef: in.VenueRef, WorkRef: in.WorkRef,
		Start: start, End: end, Timezone: cx.Venue.Timezone,
		LocalStart: localStart, LocalEnd: localEnd,
		Status: domain.SlotProposed, Components: componentRefs(cx.Components),
		Equipment: append([]string(nil), in.Equipment...), CreatedAt: now,
	}
	s.store.PutSlot(sl)
	s.generatePrerequisites(sl, cx)
	s.recordSystemEvent("slot_proposed", sl.WorkRef, map[string]any{
		"slot_ref": sl.Ref, "venue_ref": sl.VenueRef,
		"local_start": sl.LocalStart, "local_end": sl.LocalEnd,
	})

	return s.slotView(sl.Ref), nil
}

// ConfirmSlot 确认排期：重新执行规则与冲突检查（权利可能已被撤回、场地可能已关闭），
// 并要求全部安装前置项已解决。重复确认同一排期为幂等操作。
func (s *Service) ConfirmSlot(slotRef string) (SlotView, error) {
	sl, ok := s.store.GetSlot(slotRef)
	if !ok {
		return SlotView{}, notFound("排期 %s 不存在", slotRef)
	}
	if sl.Status == domain.SlotConfirmed {
		return s.slotView(slotRef), nil
	}
	if sl.Status != domain.SlotProposed {
		return SlotView{}, conflict("slot_not_confirmable", "排期状态为 %s，不能确认", sl.Status)
	}

	if err := s.revalidate(sl); err != nil {
		return SlotView{}, err
	}
	if open := s.openPrereqRefs(slotRef); len(open) > 0 {
		return SlotView{}, conflict("prerequisites_open", "尚有 %d 项安装前置项未解决: %v", len(open), open)
	}

	now := s.now()
	s.store.PutSlot(func() domain.Slot {
		sl.Status = domain.SlotConfirmed
		sl.ConfirmedAt = &now
		return sl
	}())
	s.recordSystemEvent("slot_confirmed", sl.WorkRef, map[string]any{
		"slot_ref": sl.Ref, "venue_ref": sl.VenueRef, "confirmed_at": now,
	})
	return s.slotView(slotRef), nil
}

// RescheduleSlot 对已确认的未来排期临时换场。
// 已开始（运输到场、展出已发生）的排期不可重排覆盖，只能走关闭/损坏处置方案；
// 换场会把旧排期标记为 superseded（记录保留可审计），并产生保险、安装团队、
// 公开展示许可的连带换场事项。
func (s *Service) RescheduleSlot(slotRef string, req RescheduleRequest) (SlotView, error) {
	old, ok := s.store.GetSlot(slotRef)
	if !ok {
		return SlotView{}, notFound("排期 %s 不存在", slotRef)
	}
	if old.Status != domain.SlotConfirmed {
		return SlotView{}, conflict("slot_not_reschedulable", "只有已确认排期可以换场，当前状态 %s", old.Status)
	}
	now := s.now()
	if domain.SlotHasStarted(old, now) {
		return SlotView{}, conflict("past_immutable", "%s: 排期 %s 已开始", domain.ErrPastImmutable, slotRef)
	}

	newReq := SlotRequest{
		VenueRef: req.VenueRef, WorkRef: old.WorkRef, Start: req.Start, End: req.End,
		Components: old.Components, Equipment: req.Equipment,
	}
	if newReq.VenueRef == "" {
		newReq.VenueRef = old.VenueRef
	}
	if len(newReq.Equipment) == 0 {
		newReq.Equipment = append([]string(nil), old.Equipment...)
	}

	cx, err := s.buildContext(newReq)
	if err != nil {
		return SlotView{}, err
	}
	start, end, localStart, localEnd, err := s.localizeWindow(cx.Venue, req.Start, req.End)
	if err != nil {
		return SlotView{}, err
	}
	if issues := domain.ValidateSlot(domain.SlotContext{
		Work: cx.Work, Venue: cx.Venue, Loan: cx.Loan,
		Components: cx.Components, Equipment: cx.Equipment, Rights: cx.Rights,
	}, start, end); len(issues) > 0 {
		return SlotView{}, conflict("slot_validation_failed", "换场未通过校验: %s", joinIssues(issues))
	}
	if occ := s.findOccupancy(cx.Components, cx.Equipment, start, end, old.Ref); len(occ) > 0 {
		return SlotView{}, conflict("double_booked", "%s: %s", domain.ErrOverlap, joinOccupancy(occ))
	}

	// 落新排期（旧排期此时仍在活跃集合中，因此冲突检查必须排除旧排期——上面已排除）。
	newSlot := domain.Slot{
		Ref: s.store.NextID("SLOT"), VenueRef: newReq.VenueRef, WorkRef: old.WorkRef,
		Start: start, End: end, Timezone: cx.Venue.Timezone,
		LocalStart: localStart, LocalEnd: localEnd,
		Status: domain.SlotConfirmed, Components: componentRefs(cx.Components),
		Equipment: append([]string(nil), newReq.Equipment...),
		CreatedAt: now, ConfirmedAt: &now, Supersedes: old.Ref,
	}
	s.store.PutSlot(newSlot)
	s.generatePrerequisites(newSlot, cx)
	// 已到场实体件的运输前置项直接继承为已解决。
	s.carryArrivedTransport(newSlot)

	old.Status = domain.SlotSuperseded
	old.SupersededBy = newSlot.Ref
	s.store.PutSlot(old)

	change := domain.ChangeOrder{
		Ref: s.store.NextID("CHG"), SlotRef: old.Ref, Action: "reschedule",
		Reason: req.Reason, NewSlotRef: newSlot.Ref, CreatedAt: now,
		Items: s.buildChangeItems(old, newSlot),
	}
	s.store.PutChangeOrder(change)
	s.recordSystemEvent("slot_rescheduled", old.WorkRef, map[string]any{
		"old_slot_ref": old.Ref, "new_slot_ref": newSlot.Ref,
		"change_ref": change.Ref, "reason": req.Reason, "items": change.Items,
	})

	view := s.slotView(newSlot.Ref)
	view.Occupancy = append(view.Occupancy, OccupancyNote{
		Kind: "change_order", SubjectRef: change.Ref, SlotRef: old.Ref,
		Message: fmt.Sprintf("换场单 %s 含 %d 项连带事项（保险/安装团队/公开展示许可）", change.Ref, len(change.Items)),
	})
	return view, nil
}

// CancelSlot 取消尚未开始的排期并产生连带事项；已开始的排期不可取消，
// 须通过场地关闭等处置方案处理。
func (s *Service) CancelSlot(slotRef string, req CancelRequest) (domain.ChangeOrder, error) {
	sl, ok := s.store.GetSlot(slotRef)
	if !ok {
		return domain.ChangeOrder{}, notFound("排期 %s 不存在", slotRef)
	}
	if sl.Status != domain.SlotConfirmed && sl.Status != domain.SlotProposed {
		return domain.ChangeOrder{}, conflict("slot_not_cancellable", "排期状态 %s，不能取消", sl.Status)
	}
	now := s.now()
	if sl.Status == domain.SlotConfirmed && domain.SlotHasStarted(sl, now) {
		return domain.ChangeOrder{}, conflict("past_immutable", "%s: 排期 %s 已开始", domain.ErrPastImmutable, slotRef)
	}
	sl.Status = domain.SlotCancelled
	sl.CancelReason = req.Reason
	s.store.PutSlot(sl)

	change := domain.ChangeOrder{
		Ref: s.store.NextID("CHG"), SlotRef: sl.Ref, Action: "cancel",
		Reason: req.Reason, CreatedAt: now,
		Items: s.buildCancellationItems(sl),
	}
	s.store.PutChangeOrder(change)
	s.recordSystemEvent("slot_cancelled", sl.WorkRef, map[string]any{
		"slot_ref": sl.Ref, "change_ref": change.Ref, "reason": req.Reason, "items": change.Items,
	})
	return change, nil
}

// ---------- 内部辅助 ----------

type slotContext struct {
	Work       domain.Work
	Venue      domain.Venue
	Loan       *domain.Loan
	Components []domain.Component
	Equipment  []domain.Equipment
	Rights     []domain.Right
}

func (s *Service) buildContext(in SlotRequest) (slotContext, error) {
	if in.VenueRef == "" || in.WorkRef == "" {
		return slotContext{}, badRequest("invalid_slot", "venue_ref 与 work_ref 为必填")
	}
	work, ok := s.store.GetWork(in.WorkRef)
	if !ok {
		return slotContext{}, notFound("作品 %s 不存在", in.WorkRef)
	}
	venue, ok := s.store.GetVenue(in.VenueRef)
	if !ok {
		return slotContext{}, notFound("场地 %s 不存在", in.VenueRef)
	}

	comps := s.store.ComponentsOf(in.WorkRef)
	var picked []domain.Component
	if len(in.Components) == 0 {
		// 默认占用该作品全部实体组成件（数字文件不产生实体占用，但仍登记到排期）。
		picked = append(picked, comps...)
	} else {
		byRef := map[string]domain.Component{}
		for _, c := range comps {
			byRef[c.Ref] = c
		}
		for _, ref := range in.Components {
			c, ok := byRef[ref]
			if !ok {
				return slotContext{}, badRequest("component_mismatch", "%s: %s 不属于作品 %s",
					domain.ErrComponentInUse, ref, in.WorkRef)
			}
			picked = append(picked, c)
		}
	}

	var eqs []domain.Equipment
	for _, ref := range in.Equipment {
		e, ok := s.store.GetEquipment(ref)
		if !ok {
			return slotContext{}, notFound("设备 %s 不存在", ref)
		}
		eqs = append(eqs, e)
	}

	loan, _ := s.store.LoanOf(in.WorkRef)
	return slotContext{
		Work: work, Venue: venue, Loan: loan,
		Components: picked, Equipment: eqs, Rights: s.store.RightsOf(in.WorkRef),
	}, nil
}

func (s *Service) localizeWindow(venue domain.Venue, start, end time.Time) (time.Time, time.Time, string, string, error) {
	loc, err := time.LoadLocation(venue.Timezone)
	if err != nil {
		return time.Time{}, time.Time{}, "", "", badRequest("invalid_timezone", "场地时区 %q 无法解析", venue.Timezone)
	}
	ls, le := start.In(loc), end.In(loc)
	if !le.After(ls) {
		return time.Time{}, time.Time{}, "", "", badRequest("bad_interval", "%s", domain.ErrBadInterval)
	}
	return ls, le, domain.LocalClock(ls), domain.LocalClock(le), nil
}

func (s *Service) findOccupancy(comps []domain.Component, eqs []domain.Equipment, start, end time.Time, excludeSlot string) []OccupancyNote {
	var notes []OccupancyNote
	physical := map[string]bool{}
	for _, c := range comps {
		if c.Form == domain.FormPhysical {
			physical[c.Ref] = true
		}
	}
	for _, other := range s.store.ActiveSlots() {
		if other.Ref == excludeSlot {
			continue
		}
		if !domain.Overlaps(start, end, other.Start, other.End) {
			continue
		}
		for _, ref := range other.Components {
			if physical[ref] {
				notes = append(notes, OccupancyNote{
					Kind: "component", SubjectRef: ref, SlotRef: other.Ref,
					Message: fmt.Sprintf("实体组成件 %s 在重叠时段已被排期 %s 占用", ref, other.Ref),
				})
			}
		}
		for _, ref := range other.Equipment {
			for _, want := range eqs {
				if want.Ref == ref {
					notes = append(notes, OccupancyNote{
						Kind: "equipment", SubjectRef: ref, SlotRef: other.Ref,
						Message: fmt.Sprintf("设备 %s 在重叠时段已被排期 %s 占用", ref, other.Ref),
					})
				}
			}
		}
	}
	return notes
}

func (s *Service) revalidate(sl domain.Slot) error {
	work, _ := s.store.GetWork(sl.WorkRef)
	venue, _ := s.store.GetVenue(sl.VenueRef)
	var comps []domain.Component
	for _, ref := range sl.Components {
		if c, ok := s.store.GetComponent(ref); ok {
			comps = append(comps, c)
		}
	}
	var eqs []domain.Equipment
	for _, ref := range sl.Equipment {
		if e, ok := s.store.GetEquipment(ref); ok {
			eqs = append(eqs, e)
		}
	}
	loan, _ := s.store.LoanOf(sl.WorkRef)
	if issues := domain.ValidateSlot(domain.SlotContext{
		Work: work, Venue: venue, Loan: loan, Components: comps,
		Equipment: eqs, Rights: s.store.RightsOf(sl.WorkRef),
	}, sl.Start, sl.End); len(issues) > 0 {
		return conflict("slot_validation_failed", "排期当前不再满足规则: %s", joinIssues(issues))
	}
	if occ := s.findOccupancy(comps, eqs, sl.Start, sl.End, sl.Ref); len(occ) > 0 {
		return conflict("double_booked", "%s: %s", domain.ErrOverlap, joinOccupancy(occ))
	}
	return nil
}

// generatePrerequisites 为排期生成安装前置项：
// 运输到场（逐实体件）、保险、公开展示许可、安装团队、数字件现场试播（依赖班组前置项）。
func (s *Service) generatePrerequisites(sl domain.Slot, cx slotContext) {
	add := func(kind, subject, detail string, requiredBy time.Time, dependsOn []string) string {
		p := domain.Prerequisite{
			Ref: s.store.NextID("PRE"), SlotRef: sl.Ref, Kind: kind, SubjectRef: subject,
			RequiredBy: requiredBy, Status: domain.PrereqOpen, Detail: detail, DependsOn: dependsOn,
		}
		s.store.PutPrerequisite(p)
		return p.Ref
	}

	var playbackSubjects []string
	for _, c := range cx.Components {
		if c.Form == domain.FormPhysical {
			add(domain.PrereqTransport, c.Ref,
				fmt.Sprintf("组成件 %s（运输条件 %s）须到场", c.Ref, c.TransportClass),
				sl.Start.Add(-prereqLead[domain.PrereqTransport]), nil)
		}
		if c.Form == domain.FormDigital && c.PlaybackProfile != "" {
			playbackSubjects = append(playbackSubjects, c.Ref)
		}
	}
	add(domain.PrereqInsurance, sl.WorkRef, "按运输条件与展出时段生效的展品保险",
		sl.Start.Add(-prereqLead[domain.PrereqInsurance]), nil)
	teamRef := add(domain.PrereqTeam, "team:"+sl.Ref, "安装班组按场地条件完成布展",
		sl.Start.Add(-prereqLead[domain.PrereqTeam]), nil)
	add(domain.PrereqPermit, sl.VenueRef,
		fmt.Sprintf("%s 场地 %s 时段公开展示许可", sl.VenueRef, sl.LocalStart),
		sl.Start.Add(-prereqLead[domain.PrereqPermit]), nil)
	for _, cRef := range playbackSubjects {
		add(domain.PrereqPlayback, cRef,
			fmt.Sprintf("组成件 %s 在分配设备上完成现场试播", cRef),
			sl.Start.Add(-prereqLead[domain.PrereqPlayback]), []string{teamRef})
	}
}

// carryArrivedTransport 换场后把已到场实体件的运输前置项标记为已解决。
func (s *Service) carryArrivedTransport(sl domain.Slot) {
	at := s.now()
	for _, p := range s.store.PrereqsForSlot(sl.Ref) {
		if p.Kind != domain.PrereqTransport {
			continue
		}
		if c, ok := s.store.GetComponent(p.SubjectRef); ok && c.Arrived {
			s.store.MutatePrerequisite(p.Ref, func(pp *domain.Prerequisite) {
				pp.Status = domain.PrereqResolved
				pp.ResolvedBy = "carried:arrival"
				pp.ResolvedAt = &at
				pp.Detail += "（换场继承：组成件已到场）"
			})
		}
	}
}

func (s *Service) buildChangeItems(old, new domain.Slot) []domain.ChangeItem {
	items := []domain.ChangeItem{
		{Kind: domain.ChangeInsurance, Status: domain.ChangeOpen, Ref: s.resolvedPrereqRef(old.Ref, domain.PrereqInsurance),
			Detail: "保单须按新场地/新时段批改；运输条件变化时同步重新核保"},
		{Kind: domain.ChangeTeam, Status: domain.ChangeOpen, Ref: s.resolvedPrereqRef(old.Ref, domain.PrereqTeam),
			Detail: "安装班组排班连带调整，新排期前置项已重新生成"},
	}
	permitDetail := "公开展示许可须按新日期重新确认"
	if old.VenueRef != new.VenueRef {
		permitDetail = "场地变更：公开展示许可须按新场地（" + new.VenueRef + "）重新申请"
	}
	items = append(items, domain.ChangeItem{
		Kind: domain.ChangePermit, Status: domain.ChangeOpen,
		Ref: s.resolvedPrereqRef(old.Ref, domain.PrereqPermit), Detail: permitDetail,
	})
	return items
}

func (s *Service) buildCancellationItems(sl domain.Slot) []domain.ChangeItem {
	return []domain.ChangeItem{
		{Kind: domain.ChangeInsurance, Status: domain.ChangeOpen,
			Ref: s.resolvedPrereqRef(sl.Ref, domain.PrereqInsurance), Detail: "排期取消：按保单条款解除或终止保险"},
		{Kind: domain.ChangeTeam, Status: domain.ChangeOpen,
			Ref: s.resolvedPrereqRef(sl.Ref, domain.PrereqTeam), Detail: "排期取消：释放安装班组排班"},
		{Kind: domain.ChangePermit, Status: domain.ChangeOpen,
			Ref: s.resolvedPrereqRef(sl.Ref, domain.PrereqPermit), Detail: "排期取消：撤回/注销对应公开展示许可"},
	}
}

func (s *Service) resolvedPrereqRef(slotRef, kind string) string {
	for _, p := range s.store.PrereqsForSlot(slotRef) {
		if p.Kind == kind && p.Status == domain.PrereqResolved {
			return p.ResolvedBy
		}
	}
	return ""
}

func (s *Service) openPrereqRefs(slotRef string) []string {
	var out []string
	for _, p := range s.store.PrereqsForSlot(slotRef) {
		if p.Status == domain.PrereqOpen {
			out = append(out, p.Ref)
		}
	}
	sort.Strings(out)
	return out
}

func (s *Service) slotView(ref string) SlotView {
	sl, ok := s.store.GetSlot(ref)
	if !ok {
		return SlotView{}
	}
	v := SlotView{Slot: sl, Prereqs: s.store.PrereqsForSlot(ref)}
	if venue, ok := s.store.GetVenue(sl.VenueRef); ok {
		v.VenueName = venue.Name
	}
	if work, ok := s.store.GetWork(sl.WorkRef); ok {
		v.WorkTitle = work.Title
	}
	return v
}

func componentRefs(comps []domain.Component) []string {
	out := make([]string, 0, len(comps))
	for _, c := range comps {
		out = append(out, c.Ref)
	}
	return out
}

func joinIssues(issues []domain.ValidationIssue) string {
	out := ""
	for i, is := range issues {
		if i > 0 {
			out += "；"
		}
		out += "[" + is.Code + "] " + is.Message
	}
	return out
}

func joinOccupancy(notes []OccupancyNote) string {
	out := ""
	for i, n := range notes {
		if i > 0 {
			out += "；"
		}
		out += n.Message
	}
	return out
}
