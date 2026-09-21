package service

import (
	"encoding/json"
	"fmt"
	"sort"
	"time"

	"example.com/batch-092001-q007/internal/domain"
)

// MovementRequest 追加一条运输/展出事实。occurred_at 可由来源显式给出，
// 服务端保留该原始发生时间。
type MovementRequest struct {
	EventID        string              `json:"event_id"`
	Ref            string              `json:"ref"`
	ArtworkRef     string              `json:"artwork_ref"`
	PieceRef       string              `json:"piece_ref"`
	SlotRef        string              `json:"slot_ref"`
	Kind           domain.MovementKind `json:"kind"`
	OccurredAt     *time.Time          `json:"occurred_at"`
	Note           string              `json:"note"`
	SourceID       string              `json:"source_id"`
	SourceSequence int64               `json:"source_sequence"`
}

// DamageRequest 上报作品/组成件损坏。
type DamageRequest struct {
	EventID        string     `json:"event_id"`
	Ref            string     `json:"ref"` // 处置方案编号
	PieceRef       string     `json:"piece_ref"`
	ArtworkRef     string     `json:"artwork_ref"`
	Detail         string     `json:"detail"`
	OccurredAt     *time.Time `json:"occurred_at"`
	SourceID       string     `json:"source_id"`
	SourceSequence int64      `json:"source_sequence"`
}

// WithdrawalRequest 撤回权利声明。
type WithdrawalRequest struct {
	EventID        string     `json:"event_id"`
	Ref            string     `json:"ref"` // 处置方案编号
	GrantRef       string     `json:"grant_ref"`
	Detail         string     `json:"detail"`
	OccurredAt     *time.Time `json:"occurred_at"`
	SourceID       string     `json:"source_id"`
	SourceSequence int64      `json:"source_sequence"`
}

// VenueCloseRequest 关闭场地。
type VenueCloseRequest struct {
	EventID        string     `json:"event_id"`
	Ref            string     `json:"ref"` // 处置方案编号
	VenueRef       string     `json:"venue_ref"`
	Detail         string     `json:"detail"`
	OccurredAt     *time.Time `json:"occurred_at"`
	SourceID       string     `json:"source_id"`
	SourceSequence int64      `json:"source_sequence"`
}

// DeviceBreakRequest 上报设备故障。
type DeviceBreakRequest struct {
	EventID        string     `json:"event_id"`
	Ref            string     `json:"ref"` // 处置方案编号
	DeviceRef      string     `json:"device_ref"`
	Detail         string     `json:"detail"`
	OccurredAt     *time.Time `json:"occurred_at"`
	SourceID       string     `json:"source_id"`
	SourceSequence int64      `json:"source_sequence"`
}

// ResolutionRequest 标记处置方案已解决，留下审计说明。
type ResolutionRequest struct {
	EventID        string `json:"event_id"`
	Ref            string `json:"ref"`
	Resolution     string `json:"resolution"`
	SourceID       string `json:"source_id"`
	SourceSequence int64  `json:"source_sequence"`
}

func validMovementKind(k domain.MovementKind) bool {
	switch k {
	case domain.MovementShipped, domain.MovementDelivered, domain.MovementInstalled,
		domain.MovementOpened, domain.MovementClosed, domain.MovementReturned:
		return true
	}
	return false
}

// AppendMovement 追加已发生的运输/展出事实。该事实只增不改，
// 即使场次事后被取消或改期，记录仍保留在作品轨迹中。
func (c *Coordinator) AppendMovement(req MovementRequest) (*SubmitResult, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if r := c.alreadySubmitted(req.EventID); r != nil {
		return r, nil
	}
	if req.Ref == "" || req.ArtworkRef == "" {
		return nil, validationf("记录编号与作品编号不能为空")
	}
	if !validMovementKind(req.Kind) {
		return nil, validationf("未知运输/展出阶段 %q", req.Kind)
	}
	if c.state.artworks[req.ArtworkRef] == nil {
		return nil, validationf("作品 %s 尚未登记", req.ArtworkRef)
	}
	if req.PieceRef != "" {
		p := c.state.pieces[req.PieceRef]
		if p == nil || p.ArtworkRef != req.ArtworkRef {
			return nil, validationf("组成件 %s 不属于作品 %s", req.PieceRef, req.ArtworkRef)
		}
	}
	if req.SlotRef != "" {
		s := c.state.slots[req.SlotRef]
		if s == nil || s.ArtworkRef != req.ArtworkRef {
			return nil, validationf("场次 %s 不属于作品 %s", req.SlotRef, req.ArtworkRef)
		}
	}
	at := c.clk.Now()
	if req.OccurredAt != nil {
		at = req.OccurredAt.UTC() // 保留来源原始发生时间
	}
	m := domain.Movement{
		Ref:        req.Ref,
		ArtworkRef: req.ArtworkRef,
		PieceRef:   req.PieceRef,
		SlotRef:    req.SlotRef,
		Kind:       req.Kind,
		OccurredAt: at,
		Note:       req.Note,
	}
	return c.emitAt(req.EventID, req.SourceID, req.SourceSequence, req.ArtworkRef,
		domain.EvtMovementAppended, m, at)
}

// ReportDamage 记录损坏并生成处置方案：未来场次一律暂停以待检查、
// 撤出公开展示；检查结论未出之前风险保持未解决。
func (c *Coordinator) ReportDamage(req DamageRequest) (*SubmitResult, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if r := c.alreadySubmitted(req.EventID); r != nil {
		return r, nil
	}
	p := c.state.pieces[req.PieceRef]
	if p == nil {
		return nil, validationf("组成件 %s 尚未登记", req.PieceRef)
	}
	if req.ArtworkRef == "" {
		req.ArtworkRef = p.ArtworkRef
	}
	if p.ArtworkRef != req.ArtworkRef {
		return nil, validationf("组成件 %s 不属于作品 %s", req.PieceRef, req.ArtworkRef)
	}
	at := c.occurredAt(req.OccurredAt)
	dp := damagePayload{PieceRef: req.PieceRef, ArtworkRef: req.ArtworkRef, Detail: req.Detail, OccurredAt: at}
	first, err := c.emitAt(req.EventID, req.SourceID, req.SourceSequence, req.PieceRef,
		domain.EvtDamageReported, dp, at)
	if err != nil || first.Idempotent {
		return first, err
	}
	d := &domain.Disposition{
		Ref:          c.dispositionRef(req.Ref, "DAM", req.PieceRef),
		IncidentKind: domain.IncidentDamage,
		SubjectRef:   req.PieceRef,
		Detail:       req.Detail,
		OpenRisk:     true,
		CreatedAt:    at,
	}
	d.Actions = append(d.Actions, domain.Action{
		Kind:   domain.ActionHoldInspection,
		Detail: fmt.Sprintf("组成件 %s 暂停一切运输与安装，等待定损", req.PieceRef),
	})
	for _, s := range c.futureSlotsOfArtwork(req.ArtworkRef, at) {
		d.Actions = append(d.Actions, domain.Action{
			Kind:    domain.ActionPullFromDisplay,
			SlotRef: s.Ref,
			Detail:  fmt.Sprintf("场次 %s 在定损完成前不得公开展出该组成件", s.Ref),
		})
	}
	return c.emitDisposition(d)
}

// WithdrawRights 记录权利撤回并生成处置方案：受影响的未来场次撤出展示，
// 若存在仍满足授权的替代场地/设备窗口，则一并给出可替代方案。
func (c *Coordinator) WithdrawRights(req WithdrawalRequest) (*SubmitResult, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if r := c.alreadySubmitted(req.EventID); r != nil {
		return r, nil
	}
	g := c.state.grants[req.GrantRef]
	if g == nil {
		return nil, validationf("授权声明 %s 尚未登记", req.GrantRef)
	}
	at := c.occurredAt(req.OccurredAt)
	wp := withdrawalPayload{GrantRef: req.GrantRef, ArtworkRef: g.ArtworkRef, Detail: req.Detail, OccurredAt: at}
	first, err := c.emitAt(req.EventID, req.SourceID, req.SourceSequence, req.GrantRef,
		domain.EvtRightsWithdrawn, wp, at)
	if err != nil || first.Idempotent {
		return first, err
	}
	d := &domain.Disposition{
		Ref:          c.dispositionRef(req.Ref, "RGT", req.GrantRef),
		IncidentKind: domain.IncidentRightsRevoked,
		SubjectRef:   req.GrantRef,
		Detail:       req.Detail,
		OpenRisk:     true,
		CreatedAt:    at,
	}
	for _, s := range c.futureSlotsOfArtwork(g.ArtworkRef, at) {
		if c.slotRightsOK(s) {
			continue // 仍有其他有效声明覆盖
		}
		action := domain.Action{
			Kind:    domain.ActionPullFromDisplay,
			SlotRef: s.Ref,
			Detail:  fmt.Sprintf("授权撤回后场次 %s 已无有效展示权利", s.Ref),
		}
		if alt := c.findAlternative(g.ArtworkRef, s, ""); alt != nil {
			action.Alternate = alt
			action.Detail += "；已附可替代安排"
		}
		d.Actions = append(d.Actions, action)
	}
	if len(d.Actions) == 0 {
		d.Detail += "（撤回时无受影响的未来场次）"
	}
	return c.emitDisposition(d)
}

// CloseVenue 关闭场地并为受影响的未来场次生成迁移方案。
func (c *Coordinator) CloseVenue(req VenueCloseRequest) (*SubmitResult, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if r := c.alreadySubmitted(req.EventID); r != nil {
		return r, nil
	}
	v := c.state.venues[req.VenueRef]
	if v == nil {
		return nil, validationf("场地 %s 尚未登记", req.VenueRef)
	}
	at := c.occurredAt(req.OccurredAt)
	cp := closurePayload{VenueRef: req.VenueRef, Detail: req.Detail, OccurredAt: at}
	first, err := c.emitAt(req.EventID, req.SourceID, req.SourceSequence, req.VenueRef,
		domain.EvtVenueClosed, cp, at)
	if err != nil || first.Idempotent {
		return first, err
	}
	d := &domain.Disposition{
		Ref:          c.dispositionRef(req.Ref, "VEN", req.VenueRef),
		IncidentKind: domain.IncidentVenueClosed,
		SubjectRef:   req.VenueRef,
		Detail:       req.Detail,
		OpenRisk:     true,
		CreatedAt:    at,
	}
	for _, s := range c.futureSlotsAtVenue(req.VenueRef, at) {
		action := domain.Action{
			Kind:    domain.ActionRelocate,
			SlotRef: s.Ref,
			Detail:  fmt.Sprintf("场地关闭，场次 %s 须迁出", s.Ref),
		}
		if alt := c.findAlternative(s.ArtworkRef, s, req.VenueRef); alt != nil {
			action.Alternate = alt
		} else {
			d.OpenRisk = true
			action.Detail += "；暂无满足权利与占用约束的替代窗口"
		}
		d.Actions = append(d.Actions, action)
	}
	return c.emitDisposition(d)
}

// BreakDevice 记录设备故障并为占用该设备的未来场次生成换设备/迁移方案。
func (c *Coordinator) BreakDevice(req DeviceBreakRequest) (*SubmitResult, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if r := c.alreadySubmitted(req.EventID); r != nil {
		return r, nil
	}
	dv := c.state.devices[req.DeviceRef]
	if dv == nil {
		return nil, validationf("设备 %s 尚未登记", req.DeviceRef)
	}
	at := c.occurredAt(req.OccurredAt)
	bp := breakagePayload{DeviceRef: req.DeviceRef, Detail: req.Detail, OccurredAt: at}
	first, err := c.emitAt(req.EventID, req.SourceID, req.SourceSequence, req.DeviceRef,
		domain.EvtDeviceBroken, bp, at)
	if err != nil || first.Idempotent {
		return first, err
	}
	d := &domain.Disposition{
		Ref:          c.dispositionRef(req.Ref, "DEV", req.DeviceRef),
		IncidentKind: domain.IncidentDeviceBroken,
		SubjectRef:   req.DeviceRef,
		Detail:       req.Detail,
		OpenRisk:     false,
		CreatedAt:    at,
	}
	for _, s := range c.futureSlotsUsingDevice(req.DeviceRef, at) {
		action := domain.Action{Kind: domain.ActionSwapDevice, SlotRef: s.Ref}
		if repl := c.replacementDevice(dv, s); repl != "" {
			action.Detail = fmt.Sprintf("场次 %s 可在同场地改用设备 %s（%s）", s.Ref, repl, dv.Kind)
			action.Alternate = &domain.SlotSpec{
				VenueRef: s.VenueRef, Start: s.Start, End: s.End,
				Devices: replaceRef(s.Devices, req.DeviceRef, repl),
			}
		} else if alt := c.findAlternative(s.ArtworkRef, s, ""); alt != nil {
			action.Kind = domain.ActionRelocate
			action.Detail = fmt.Sprintf("场次 %s 同场地无备用 %s，可迁至替代场地", s.Ref, dv.Kind)
			action.Alternate = alt
		} else {
			action.Detail = fmt.Sprintf("场次 %s 无可用备用 %s 设备", s.Ref, dv.Kind)
			d.OpenRisk = true
		}
		d.Actions = append(d.Actions, action)
	}
	return c.emitDisposition(d)
}

// ResolveDisposition 标记处置方案关闭。审计链路上方案与解决说明都保留。
func (c *Coordinator) ResolveDisposition(req ResolutionRequest) (*SubmitResult, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if r := c.alreadySubmitted(req.EventID); r != nil {
		return r, nil
	}
	d := c.state.dispos[req.Ref]
	if d == nil {
		return nil, validationf("处置方案 %s 不存在", req.Ref)
	}
	if !d.OpenRisk {
		return nil, &ConflictError{Reasons: []string{fmt.Sprintf("处置方案 %s 已解决", req.Ref)}}
	}
	p, _ := json.Marshal(struct {
		Ref        string `json:"ref"`
		Resolution string `json:"resolution"`
	}{Ref: req.Ref, Resolution: req.Resolution})
	sum := sha256Sum(p)
	evt := domain.Event{
		SchemaVersion:  SchemaVersion,
		EventID:        req.EventID,
		SourceID:       req.SourceID,
		SourceSequence: req.SourceSequence,
		OccurredAt:     c.clk.Now(),
		SubjectRef:     req.Ref,
		Type:           domain.EvtDispositionResolved,
		Payload:        p,
		PayloadDigest:  sum,
	}
	return c.submit(evt)
}

func (c *Coordinator) emitDisposition(d *domain.Disposition) (*SubmitResult, error) {
	return c.emitAt("EVT-DISP-"+d.Ref, "", 0, d.SubjectRef,
		domain.EvtDispositionGenerated, d, d.CreatedAt)
}

func (c *Coordinator) dispositionRef(provided, prefix, subject string) string {
	if provided != "" {
		return provided
	}
	return fmt.Sprintf("%s-%s-%d", prefix, subject, c.clk.Now().UnixNano())
}

func (c *Coordinator) occurredAt(t *time.Time) time.Time {
	if t != nil {
		return t.UTC()
	}
	return c.clk.Now()
}

// futureSlotsOfArtwork 返回事件时刻之后仍在进行或尚未开始的未取消场次。
func (c *Coordinator) futureSlotsOfArtwork(artworkRef string, now time.Time) []*domain.Slot {
	var out []*domain.Slot
	for _, s := range c.state.slots {
		if s.ArtworkRef == artworkRef && s.Status != domain.SlotCancelled && s.End.After(now) {
			out = append(out, s)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Ref < out[j].Ref })
	return out
}

func (c *Coordinator) futureSlotsAtVenue(venueRef string, now time.Time) []*domain.Slot {
	var out []*domain.Slot
	for _, s := range c.state.slots {
		if s.VenueRef == venueRef && s.Status != domain.SlotCancelled && s.End.After(now) {
			out = append(out, s)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Ref < out[j].Ref })
	return out
}

func (c *Coordinator) futureSlotsUsingDevice(deviceRef string, now time.Time) []*domain.Slot {
	var out []*domain.Slot
	for _, s := range c.state.slots {
		if s.Status != domain.SlotCancelled && s.End.After(now) && s.HasDevice(deviceRef) {
			out = append(out, s)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Ref < out[j].Ref })
	return out
}

// replacementDevice 在同一场地寻找种类相同、未故障、时间窗内空闲的设备。
func (c *Coordinator) replacementDevice(dv *domain.Device, s *domain.Slot) string {
	var candidates []string
	for _, other := range c.state.devices {
		if other.Ref == dv.Ref || other.VenueRef != dv.VenueRef ||
			other.Kind != dv.Kind || other.Broken {
			continue
		}
		free := true
		for _, occ := range c.state.slots {
			if occ.Ref == s.Ref || occ.Status == domain.SlotCancelled || !occ.HasDevice(other.Ref) {
				continue
			}
			if domain.Overlaps(s.Start, s.End, occ.Start, occ.End) {
				free = false
				break
			}
		}
		if free {
			candidates = append(candidates, other.Ref)
		}
	}
	sort.Strings(candidates)
	if len(candidates) == 0 {
		return ""
	}
	return candidates[0]
}

func replaceRef(devs []string, old, new string) []string {
	out := make([]string, 0, len(devs))
	for _, d := range devs {
		if d == old {
			out = append(out, new)
		} else {
			out = append(out, d)
		}
	}
	return out
}

// slotRightsOK 以当前投影重新判断场次全窗口是否仍有有效授权。
func (c *Coordinator) slotRightsOK(s *domain.Slot) bool {
	art := c.state.artworks[s.ArtworkRef]
	venue := c.state.venues[s.VenueRef]
	if art == nil || venue == nil {
		return false
	}
	return len(c.checkRights(art, c.artworkPieces(art), venue, s.Start, s.End)) == 0
}

// findAlternative 在 excludeVenue 之外（为空则不限）寻找一个满足权利、
// 借展、场地媒介与设备需求、且时间窗内无占用冲突的替代安排。
func (c *Coordinator) findAlternative(artworkRef string, s *domain.Slot, excludeVenue string) *domain.SlotSpec {
	art := c.state.artworks[artworkRef]
	if art == nil {
		return nil
	}
	pieces := c.artworkPieces(art)
	neededKinds := map[string]bool{}
	for _, p := range pieces {
		for _, k := range p.RequiredDeviceKinds {
			neededKinds[k] = true
		}
	}
	var venueRefs []string
	for _, v := range c.state.venues {
		if v.Ref == excludeVenue || v.Closed {
			continue
		}
		venueRefs = append(venueRefs, v.Ref)
	}
	sort.Strings(venueRefs)
	for _, vRef := range venueRefs {
		// 先为所需设备种类选配时间窗内空闲的同场地设备，
		// 再带完整设备组合做一次不变量校验（权利、借展、占用等）。
		devs := c.freeDevicesForKinds(vRef, s, neededKinds)
		if len(devs) != len(neededKinds) {
			continue
		}
		if len(c.validateSlot(artworkRef, vRef, s.Start, s.End, devs, s.Ref)) == 0 {
			return &domain.SlotSpec{VenueRef: vRef, Start: s.Start, End: s.End, Devices: devs}
		}
	}
	return nil
}

// freeDevicesForKinds 在指定场地为所需设备种类各挑一台时间窗内空闲的设备。
func (c *Coordinator) freeDevicesForKinds(venueRef string, s *domain.Slot, kinds map[string]bool) []string {
	var out []string
	for kind := range kinds {
		var candidates []string
		for _, d := range c.state.devices {
			if d.VenueRef != venueRef || d.Kind != kind || d.Broken {
				continue
			}
			free := true
			for _, occ := range c.state.slots {
				if occ.Ref == s.Ref || occ.Status == domain.SlotCancelled || !occ.HasDevice(d.Ref) {
					continue
				}
				if domain.Overlaps(s.Start, s.End, occ.Start, occ.End) {
					free = false
					break
				}
			}
			if free {
				candidates = append(candidates, d.Ref)
			}
		}
		sort.Strings(candidates)
		if len(candidates) == 0 {
			return nil
		}
		out = append(out, candidates[0])
	}
	sort.Strings(out)
	return out
}
