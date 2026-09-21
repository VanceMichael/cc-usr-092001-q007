package service

import (
	"fmt"
	"sort"
	"time"

	"example.com/batch-092001-q007/internal/domain"
)

// TimelineView 是作品编号下的完整展陈轨迹：台账事件、排期记录与处置方案
// 统一按时间排列。已经发生的事实带 immutable=true，任何重排都不会覆盖它们。
type TimelineView struct {
	WorkRef string              `json:"work_ref"`
	Title   string              `json:"title"`
	Entries []TimelineEntryView `json:"entries"`
}

type TimelineEntryView struct {
	At        time.Time `json:"at"`
	Category  string    `json:"category"`
	Ref       string    `json:"ref"`
	Summary   string    `json:"summary"`
	Immutable bool      `json:"immutable"`
}

// WorkTimeline 汇总作品的完整展陈轨迹。
func (s *Service) WorkTimeline(workRef string) (TimelineView, error) {
	work, ok := s.store.GetWork(workRef)
	if !ok {
		return TimelineView{}, notFound("作品 %s 不存在", workRef)
	}
	now := s.now()

	subjects := map[string]bool{workRef: true}
	for _, c := range s.store.ComponentsOf(workRef) {
		subjects[c.Ref] = true
	}
	for _, r := range s.store.RightsOf(workRef) {
		subjects[r.Ref] = true
	}

	var entries []TimelineEntryView

	// 1) 台账事件：主体直接关联，或院校批次清单中包含该作品。
	for _, ev := range s.store.AllEvents() {
		relevant := subjects[ev.SubjectRef]
		if !relevant && ev.Type == "batch_submitted" {
			relevant = batchPayloadContains(ev.Payload, workRef)
		}
		if !relevant {
			continue
		}
		entries = append(entries, TimelineEntryView{
			At: ev.OccurredAt, Category: "event:" + ev.Type, Ref: ev.EventID,
			Summary:   fmt.Sprintf("来源 %s 序号 %d 的事件（摘要 %s）", ev.Source, ev.SourceSequence, ev.PayloadDigest),
			Immutable: true, // 台账只追加，事件永不被覆盖
		})
	}

	// 2) 全部排期（含已取消、已被替换的历史记录）。
	for _, sl := range s.store.AllSlotsForWork(workRef) {
		immutable := sl.Status == domain.SlotCancelled || sl.Status == domain.SlotSuperseded ||
			domain.SlotHasStarted(sl, now)
		entries = append(entries, TimelineEntryView{
			At: sl.Start, Category: "slot:" + sl.Status, Ref: sl.Ref,
			Summary: fmt.Sprintf("场地 %s 当地 %s ~ %s（设备 %v，组成件 %d 件）",
				sl.VenueRef, sl.LocalStart, sl.LocalEnd, sl.Equipment, len(sl.Components)),
			Immutable: immutable,
		})
		if sl.SupersededBy != "" {
			entries[len(entries)-1].Summary += "，已由 " + sl.SupersededBy + " 换场"
		}
		if sl.CancelReason != "" {
			entries[len(entries)-1].Summary += "，取消原因：" + sl.CancelReason
		}
	}

	// 3) 处置方案：组成件损坏、权利撤回，以及波及本作品排期的场地关闭方案。
	slotRefs := map[string]bool{}
	for _, sl := range s.store.AllSlotsForWork(workRef) {
		slotRefs[sl.Ref] = true
	}
	planSeen := map[string]bool{}
	addPlan := func(p domain.Plan) {
		if planSeen[p.Ref] {
			return
		}
		planSeen[p.Ref] = true
		done, total := 0, len(p.Steps)
		for _, st := range p.Steps {
			if st.Status == domain.PlanExecuted || st.Status == "done" {
				done++
			}
		}
		entries = append(entries, TimelineEntryView{
			At: p.CreatedAt, Category: "plan:" + p.Trigger, Ref: p.Ref,
			Summary:   fmt.Sprintf("处置方案（%s）触发于事件 %s，步骤完成 %d/%d，受影响排期 %d 条", p.Trigger, p.TriggerEvent, done, total, len(p.AffectedSlots)),
			Immutable: p.Status == domain.PlanExecuted,
		})
	}
	for subject := range subjects {
		for _, p := range s.store.PlansForSubject(subject) {
			addPlan(p)
		}
	}
	for _, venue := range s.store.ListVenues() {
		for _, p := range s.store.PlansForSubject(venue.Ref) {
			for _, ref := range p.AffectedSlots {
				if slotRefs[ref] {
					addPlan(p)
					break
				}
			}
		}
	}

	sort.SliceStable(entries, func(i, j int) bool {
		if entries[i].At.Equal(entries[j].At) {
			return entries[i].Ref < entries[j].Ref
		}
		return entries[i].At.Before(entries[j].At)
	})
	return TimelineView{WorkRef: workRef, Title: work.Title, Entries: entries}, nil
}

// VenueWindow 列出某场地在某时间窗内的排期、每条排期的安装前置项、
// 未解决风险与可替代方案。窗口按展馆当地时间解释。
func (s *Service) VenueWindow(venueRef string, rawStart, rawEnd time.Time) (VenueWindowView, error) {
	venue, ok := s.store.GetVenue(venueRef)
	if !ok {
		return VenueWindowView{}, notFound("场地 %s 不存在", venueRef)
	}
	loc, err := time.LoadLocation(venue.Timezone)
	if err != nil {
		return VenueWindowView{}, badRequest("invalid_timezone", "场地时区 %q 无法解析", venue.Timezone)
	}
	start, end := rawStart.In(loc), rawEnd.In(loc)
	if !end.After(start) {
		return VenueWindowView{}, badRequest("bad_interval", "%s", domain.ErrBadInterval)
	}

	view := VenueWindowView{
		Venue: venue, WindowStart: start, WindowEnd: end,
		LocalStart: domain.LocalClock(start), LocalEnd: domain.LocalClock(end),
	}
	if venue.Closed {
		view.Risks = append(view.Risks, Risk{Code: "venue_closed", Subject: venue.Ref,
			Message: "场地已关闭，窗内排期须迁移或取消"})
	} else if venue.CloseFrom != nil && venue.CloseFrom.Before(end) {
		view.Risks = append(view.Risks, Risk{Code: "venue_closing", Subject: venue.Ref,
			Message: fmt.Sprintf("场地自 %s 起关闭", domain.LocalClock(venue.CloseFrom.In(loc)))})
	}

	var windowSlots []domain.Slot
	for _, sl := range s.store.SlotsForVenue(venueRef) {
		if domain.Overlaps(start, end, sl.Start, sl.End) {
			windowSlots = append(windowSlots, sl)
		}
	}
	allAlts := s.alternativesForSlots(windowSlots)
	now := s.now()

	for _, sl := range windowSlots {
		brief := SlotBrief{
			Ref: sl.Ref, WorkRef: sl.WorkRef, Start: sl.Start, End: sl.End,
			LocalStart: sl.LocalStart, LocalEnd: sl.LocalEnd, Status: sl.Status,
			Prerequisites: s.store.PrereqsForSlot(sl.Ref),
		}
		if w, ok := s.store.GetWork(sl.WorkRef); ok {
			brief.WorkTitle = w.Title
		}
		brief.Risks = append(brief.Risks, s.slotRisks(sl, now)...)
		for _, a := range allAlts {
			if a.SlotRef == sl.Ref {
				brief.Alternatives = append(brief.Alternatives, a)
			}
		}
		view.Slots = append(view.Slots, brief)
	}
	return view, nil
}

// slotRisks 评估单条排期当前的未解决风险（不修改任何状态）。
func (s *Service) slotRisks(sl domain.Slot, now time.Time) []Risk {
	var risks []Risk
	venue, _ := s.store.GetVenue(sl.VenueRef)

	for _, p := range s.store.PrereqsForSlot(sl.Ref) {
		if p.Status != domain.PrereqOpen {
			continue
		}
		code := "prerequisite_open"
		if p.RequiredBy.Before(now) {
			code = "prerequisite_overdue"
		}
		risks = append(risks, Risk{Code: code, Subject: p.Ref,
			Message: fmt.Sprintf("前置项 %s 未解决（截止 %s）：%s", p.Kind, domain.LocalClock(p.RequiredBy), p.Detail)})
	}

	if !s.rightsCover(sl.WorkRef, venue.Country, sl.Start, sl.End) {
		risks = append(risks, Risk{Code: "right_gap", Subject: sl.WorkRef,
			Message: "当前有效权利声明已不能完整覆盖该排期的地域/媒介/日期"})
	}
	if loan, ok := s.store.LoanOf(sl.WorkRef); ok && sl.End.After(loan.End) {
		risks = append(risks, Risk{Code: "loan_exceeded", Subject: loan.Ref,
			Message: fmt.Sprintf("排期结束晚于借展撤展时间 %s", domain.LocalClock(loan.End))})
	}
	for _, ref := range sl.Components {
		if c, ok := s.store.GetComponent(ref); ok && c.Damaged {
			risks = append(risks, Risk{Code: "component_damaged", Subject: c.Ref,
				Message: "组成件已损坏，须按处置方案处理"})
		}
	}
	for _, ch := range s.store.ListChangeOrders() {
		if ch.SlotRef != sl.Ref && ch.NewSlotRef != sl.Ref {
			continue
		}
		for _, item := range ch.Items {
			if item.Status == domain.ChangeOpen {
				risks = append(risks, Risk{Code: "change_item_open", Subject: ch.Ref,
					Message: fmt.Sprintf("换场单 %s 的连带事项 %s 未闭环：%s", ch.Ref, item.Kind, item.Detail)})
			}
		}
	}
	if sl.Status == domain.SlotProposed {
		risks = append(risks, Risk{Code: "unconfirmed", Subject: sl.Ref, Message: "排期仍为提议态，尚未确认"})
	}
	return risks
}

// batchPayloadContains 判断 batch_submitted 事件负载中是否包含某作品编号。
func batchPayloadContains(raw []byte, workRef string) bool {
	var works []struct {
		Ref string `json:"ref"`
	}
	if err := decodePayload(raw, &works); err != nil {
		return false
	}
	for _, w := range works {
		if w.Ref == workRef {
			return true
		}
	}
	return false
}
