package service

import (
	"encoding/json"
	"fmt"
	"sort"
	"time"

	"example.com/batch-092001-q007/internal/domain"
)

// SlotRequest 创建或确认场次。时间为带偏移量的 ISO 8601 字符串反序列化
// 得到的绝对时刻；跨时区提交携带各自偏移量，落位时再换算为场地当地日期。
type SlotRequest struct {
	EventID        string    `json:"event_id"`
	Ref            string    `json:"ref"`
	ArtworkRef     string    `json:"artwork_ref"`
	VenueRef       string    `json:"venue_ref"`
	Start          time.Time `json:"start"`
	End            time.Time `json:"end"`
	Devices        []string  `json:"devices"`
	SourceID       string    `json:"source_id"`
	SourceSequence int64     `json:"source_sequence"`
}

// ConfirmRequest 确认场次；确认时重新执行全部不变量与安装前置项校验。
type ConfirmRequest struct {
	EventID        string `json:"event_id"`
	Ref            string `json:"ref"`
	SourceID       string `json:"source_id"`
	SourceSequence int64  `json:"source_sequence"`
}

// PlanSlot 计划一个新场次：占用、权利、借展与设备匹配在此刻即校验，
// 安装前置项在确认时校验。
func (c *Coordinator) PlanSlot(req SlotRequest) (*SubmitResult, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if r := c.alreadySubmitted(req.EventID); r != nil {
		return r, nil
	}
	if req.Ref == "" {
		return nil, validationf("场次编号不能为空")
	}
	if c.state.slots[req.Ref] != nil {
		return nil, &ConflictError{Reasons: []string{fmt.Sprintf("场次 %s 已存在，改期请使用专用命令", req.Ref)}}
	}
	if reasons := c.validateSlot(req.ArtworkRef, req.VenueRef, req.Start, req.End, req.Devices, ""); len(reasons) > 0 {
		return nil, &ConflictError{Reasons: reasons}
	}
	p := slotPayload{Ref: req.Ref, ArtworkRef: req.ArtworkRef, VenueRef: req.VenueRef,
		Start: req.Start.UTC(), End: req.End.UTC(), Devices: req.Devices}
	return c.emitAt(req.EventID, req.SourceID, req.SourceSequence, req.Ref,
		domain.EvtSlotPlanned, p, c.clk.Now())
}

// RescheduleSlot 改期未发生的场次。凡已有运输/展出事实（已发运、已交付、
// 已安装、已开幕等）或开始时刻已过的场次，拒绝改期——历史事实只能被
// 后续事件补充，不能被重排覆盖。
func (c *Coordinator) RescheduleSlot(req SlotRequest) (*SubmitResult, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if r := c.alreadySubmitted(req.EventID); r != nil {
		return r, nil
	}
	s := c.state.slots[req.Ref]
	if s == nil {
		return nil, validationf("场次 %s 不存在", req.Ref)
	}
	if s.Status == domain.SlotCancelled {
		return nil, &ConflictError{Reasons: []string{fmt.Sprintf("场次 %s 已取消，不能改期", req.Ref)}}
	}
	if len(c.state.mvBySlot[req.Ref]) > 0 {
		return nil, &ConflictError{Reasons: []string{
			fmt.Sprintf("场次 %s 已存在运输/展出记录，历史事实不可重排覆盖", req.Ref)}}
	}
	if !s.Start.After(c.clk.Now()) {
		return nil, &ConflictError{Reasons: []string{
			fmt.Sprintf("场次 %s 开始时刻 %s 已过，不能改期", req.Ref, s.Start.Format(time.RFC3339))}}
	}
	if reasons := c.validateSlot(req.ArtworkRef, req.VenueRef, req.Start, req.End, req.Devices, req.Ref); len(reasons) > 0 {
		return nil, &ConflictError{Reasons: reasons}
	}
	p := slotPayload{Ref: req.Ref, ArtworkRef: req.ArtworkRef, VenueRef: req.VenueRef,
		Start: req.Start.UTC(), End: req.End.UTC(), Devices: req.Devices}
	return c.emitAt(req.EventID, req.SourceID, req.SourceSequence, req.Ref,
		domain.EvtSlotRescheduled, p, c.clk.Now())
}

// ConfirmSlot 确认场次：在计划校验之外，再核对全部安装前置项已满足。
func (c *Coordinator) ConfirmSlot(req ConfirmRequest) (*SubmitResult, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if r := c.alreadySubmitted(req.EventID); r != nil {
		return r, nil
	}
	s := c.state.slots[req.Ref]
	if s == nil {
		return nil, validationf("场次 %s 不存在", req.Ref)
	}
	if s.Status == domain.SlotCancelled {
		return nil, &ConflictError{Reasons: []string{fmt.Sprintf("场次 %s 已取消", req.Ref)}}
	}
	var reasons []string
	reasons = append(reasons, c.validateSlot(s.ArtworkRef, s.VenueRef, s.Start, s.End, s.Devices, s.Ref)...)
	reasons = append(reasons, c.unsatisfiedPrereqs(req.Ref)...)
	if len(reasons) > 0 {
		return nil, &ConflictError{Reasons: reasons}
	}
	return c.emitAt(req.EventID, req.SourceID, req.SourceSequence, req.Ref,
		domain.EvtSlotConfirmed, refPayload{Ref: req.Ref}, c.clk.Now())
}

// CancelSlot 取消场次。取消不删除任何运输/展出事实，轨迹仍可查。
func (c *Coordinator) CancelSlot(req ConfirmRequest) (*SubmitResult, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if r := c.alreadySubmitted(req.EventID); r != nil {
		return r, nil
	}
	if c.state.slots[req.Ref] == nil {
		return nil, validationf("场次 %s 不存在", req.Ref)
	}
	return c.emitAt(req.EventID, req.SourceID, req.SourceSequence, req.Ref,
		domain.EvtSlotCancelled, refPayload{Ref: req.Ref}, c.clk.Now())
}

// validateSlot 返回场次违反的全部不变量；selfRef 为改期时自身编号，
// 重叠检测跳过自身。
func (c *Coordinator) validateSlot(artworkRef, venueRef string, start, end time.Time,
	deviceRefs []string, selfRef string) []string {
	var reasons []string
	art := c.state.artworks[artworkRef]
	if art == nil {
		return []string{fmt.Sprintf("作品 %s 尚未登记", artworkRef)}
	}
	venue := c.state.venues[venueRef]
	if venue == nil {
		return []string{fmt.Sprintf("场地 %s 尚未登记", venueRef)}
	}
	if !start.Before(end) {
		reasons = append(reasons, fmt.Sprintf("场次开始 %s 必须早于结束 %s",
			start.Format(time.RFC3339), end.Format(time.RFC3339)))
	}
	if venue.Closed {
		reasons = append(reasons, fmt.Sprintf("场地 %s 已关闭", venueRef))
	}

	pieces := c.artworkPieces(art)
	medias := artworkMedias(art, pieces)
	for _, m := range medias {
		if len(venue.Medias) > 0 && !domain.MediaAllowed(venue.Medias, m) {
			reasons = append(reasons, fmt.Sprintf("场地 %s 不承载媒介 %s", venueRef, m))
		}
	}
	for _, p := range pieces {
		if c.state.damaged[p.Ref] {
			reasons = append(reasons, fmt.Sprintf("组成件 %s 已报损，暂停展出", p.Ref))
		}
	}

	if start.Before(end) {
		reasons = append(reasons, c.checkRights(art, pieces, venue, start, end)...)
	}
	for _, l := range c.artworkLoans(artworkRef) {
		if end.After(l.MustReturnBy) {
			reasons = append(reasons, fmt.Sprintf(
				"场次结束 %s 晚于借展协议 %s 的撤展截止 %s",
				end.UTC().Format(time.RFC3339), l.Ref, l.MustReturnBy.Format(time.RFC3339)))
		}
	}

	// 同一作品（实体组成件）不允许在重叠时段出现在两个未取消场次。
	for _, other := range c.state.slots {
		if other.Ref == selfRef || other.Status == domain.SlotCancelled {
			continue
		}
		if other.ArtworkRef == artworkRef && domain.Overlaps(start, end, other.Start, other.End) {
			reasons = append(reasons, fmt.Sprintf("作品 %s 在场次 %s（%s~%s）已被占用",
				artworkRef, other.Ref, other.Start.Format(time.RFC3339), other.End.Format(time.RFC3339)))
		}
	}

	// 设备：存在性、归属、故障、种类匹配与重叠独占。
	seen := map[string]bool{}
	slotKinds := map[string]bool{}
	for _, dRef := range deviceRefs {
		if seen[dRef] {
			continue
		}
		seen[dRef] = true
		d := c.state.devices[dRef]
		if d == nil {
			reasons = append(reasons, fmt.Sprintf("设备 %s 尚未登记", dRef))
			continue
		}
		if d.VenueRef != venueRef {
			reasons = append(reasons, fmt.Sprintf("设备 %s 属于场地 %s，不能在 %s 使用", dRef, d.VenueRef, venueRef))
		}
		if d.Broken {
			reasons = append(reasons, fmt.Sprintf("设备 %s 已故障", dRef))
		}
		slotKinds[d.Kind] = true
		for _, other := range c.state.slots {
			if other.Ref == selfRef || other.Status == domain.SlotCancelled || !other.HasDevice(dRef) {
				continue
			}
			if domain.Overlaps(start, end, other.Start, other.End) {
				reasons = append(reasons, fmt.Sprintf("设备 %s 在场次 %s（%s~%s）已被占用",
					dRef, other.Ref, other.Start.Format(time.RFC3339), other.End.Format(time.RFC3339)))
			}
		}
	}
	for _, p := range pieces {
		for _, kind := range p.RequiredDeviceKinds {
			if !slotKinds[kind] {
				reasons = append(reasons, fmt.Sprintf("组成件 %s 需要 %s 类播放设备，场次未配备", p.Ref, kind))
			}
		}
	}
	return reasons
}

// checkRights 校验场次覆盖的每一个展馆当地日期内，每种媒介都存在
// 未撤回、覆盖该国、该媒介且日期窗口包含该日的授权声明。
func (c *Coordinator) checkRights(art *domain.Artwork, pieces []*domain.Piece,
	venue *domain.Venue, start, end time.Time) []string {
	var reasons []string
	loc, err := time.LoadLocation(venue.TZ)
	if err != nil {
		return []string{fmt.Sprintf("场地 %s 时区 %q 无法解析", venue.Ref, venue.TZ)}
	}
	days := localDaysBetween(start.In(loc), end.Add(-1).In(loc))
	for _, media := range artworkMedias(art, pieces) {
		var missing []domain.LocalDate
		for _, day := range days {
			ok := false
			for _, g := range c.artworkGrants(art.Ref) {
				if domain.GrantAllows(g, venue.CountryCode, media,
					midnightAt(day, loc), venue.TZ) {
					ok = true
					break
				}
			}
			if !ok {
				missing = append(missing, day)
			}
		}
		if len(missing) > 0 {
			reasons = append(reasons, fmt.Sprintf(
				"作品 %s 的媒介 %s 在 %s(%s) 以下当地日期无有效授权：%s",
				art.Ref, media, venue.Ref, venue.CountryCode, formatDays(missing)))
		}
	}
	return reasons
}

// unsatisfiedPrereqs 返回目标场次尚未满足的安装前置项。
func (c *Coordinator) unsatisfiedPrereqs(toSlotRef string) []string {
	var reasons []string
	for _, dep := range c.depsTo(toSlotRef) {
		from := c.state.slots[dep.FromSlotRef]
		if from == nil {
			reasons = append(reasons, fmt.Sprintf("前置项 %s 引用的场次 %s 不存在", dep.Ref, dep.FromSlotRef))
			continue
		}
		if from.Status == domain.SlotCancelled {
			reasons = append(reasons, fmt.Sprintf("前置项 %s 的前置场次 %s 已取消", dep.Ref, from.Ref))
			continue
		}
		if !c.phaseReached(from, dep.Requires) {
			reasons = append(reasons, fmt.Sprintf("安装前置项 %s：场次 %s 须先达到 %s 阶段",
				dep.Ref, from.Ref, dep.Requires))
		}
	}
	return reasons
}

// phaseReached 根据已追加的运输/展出记录判断场次是否达到某阶段。
func (c *Coordinator) phaseReached(s *domain.Slot, phase string) bool {
	has := func(kind domain.MovementKind) bool {
		for _, m := range c.state.mvBySlot[s.Ref] {
			if m.Kind == kind {
				return true
			}
		}
		return false
	}
	switch phase {
	case "delivered":
		return has(domain.MovementDelivered) || has(domain.MovementInstalled)
	case "installed":
		return has(domain.MovementInstalled)
	default:
		return false
	}
}

func (c *Coordinator) artworkPieces(art *domain.Artwork) []*domain.Piece {
	var out []*domain.Piece
	for _, p := range c.state.pieces {
		if p.ArtworkRef == art.Ref {
			out = append(out, p)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Ref < out[j].Ref })
	return out
}

func (c *Coordinator) artworkGrants(artworkRef string) []*domain.RightsGrant {
	var out []*domain.RightsGrant
	for _, g := range c.state.grants {
		if g.ArtworkRef == artworkRef {
			out = append(out, g)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Ref < out[j].Ref })
	return out
}

func (c *Coordinator) artworkLoans(artworkRef string) []*domain.LoanAgreement {
	var out []*domain.LoanAgreement
	for _, l := range c.state.loans {
		if l.ArtworkRef == artworkRef {
			out = append(out, l)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Ref < out[j].Ref })
	return out
}

func (c *Coordinator) depsTo(toSlotRef string) []*domain.InstallDependency {
	var out []*domain.InstallDependency
	for _, d := range c.state.deps {
		if d.ToSlotRef == toSlotRef {
			out = append(out, d)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Ref < out[j].Ref })
	return out
}

func artworkMedias(art *domain.Artwork, pieces []*domain.Piece) []domain.Media {
	seen := map[domain.Media]bool{}
	var out []domain.Media
	add := func(m domain.Media) {
		if m != "" && !seen[m] {
			seen[m] = true
			out = append(out, m)
		}
	}
	for _, p := range pieces {
		add(p.Media)
	}
	if len(pieces) == 0 {
		for _, m := range art.Medias {
			add(m)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

// localDaysBetween 返回 [from, to] 两个绝对时刻覆盖的当地日期序列（去重按日）。
func localDaysBetween(from, to time.Time) []domain.LocalDate {
	if to.Before(from) {
		to = from
	}
	cur := time.Date(from.Year(), from.Month(), from.Day(), 0, 0, 0, 0, from.Location())
	last := time.Date(to.Year(), to.Month(), to.Day(), 0, 0, 0, 0, to.Location())
	var days []domain.LocalDate
	for !cur.After(last) {
		days = append(days, domain.LocalDate{Year: cur.Year(), Month: cur.Month(), Day: cur.Day()})
		cur = cur.AddDate(0, 0, 1)
	}
	return days
}

func midnightAt(d domain.LocalDate, loc *time.Location) time.Time {
	return time.Date(d.Year, d.Month, d.Day, 12, 0, 0, 0, loc)
}

func formatDays(days []domain.LocalDate) string {
	if len(days) <= 5 {
		s := make([]string, len(days))
		for i, d := range days {
			s[i] = d.String()
		}
		return joinStrings(s, ", ")
	}
	return fmt.Sprintf("%s..%s（共%d天）", days[0], days[len(days)-1], len(days))
}

func joinStrings(s []string, sep string) string {
	out := ""
	for i, v := range s {
		if i > 0 {
			out += sep
		}
		out += v
	}
	return out
}

// emitAt 与 emit 相同，但允许显式指定 occurred_at——运输/展出事实必须
// 保留来源声明的发生时间，不能用服务端到达时间覆盖。
func (c *Coordinator) emitAt(eventID, sourceID string, seq int64, subject string,
	typ domain.EventType, payload any, occurredAt time.Time) (*SubmitResult, error) {
	raw, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("序列化载荷: %w", err)
	}
	sum := sha256Sum(raw)
	evt := domain.Event{
		SchemaVersion:  SchemaVersion,
		EventID:        eventID,
		SourceID:       sourceID,
		SourceSequence: seq,
		OccurredAt:     occurredAt.UTC(),
		SubjectRef:     subject,
		Type:           typ,
		Payload:        raw,
		PayloadDigest:  sum,
	}
	return c.submit(evt)
}
