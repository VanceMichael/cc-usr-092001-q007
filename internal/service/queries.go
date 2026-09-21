package service

import (
	"fmt"
	"sort"
	"time"

	"example.com/batch-092001-q007/internal/domain"
)

// ArtworkView 是作品登记信息及其组成件。
type ArtworkView struct {
	Artwork *domain.Artwork `json:"artwork"`
	Pieces  []*domain.Piece `json:"pieces"`
}

// GetArtwork 返回作品与其组成件。
func (c *Coordinator) GetArtwork(ref string) *ArtworkView {
	c.mu.Lock()
	defer c.mu.Unlock()
	art := c.state.artworks[ref]
	if art == nil {
		return nil
	}
	return &ArtworkView{Artwork: art, Pieces: c.artworkPieces(art)}
}

// ArtworkTrack 按时间合并作品的场次、运输/展出事实与处置方案，
// 给出完整展陈轨迹。已取消场次与历史事实同样保留。
func (c *Coordinator) ArtworkTrack(artworkRef string) []domain.TrackEntry {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.state.artworks[artworkRef] == nil {
		return nil
	}
	var entries []domain.TrackEntry

	slotRefs := map[string]bool{}
	for _, s := range c.state.slots {
		if s.ArtworkRef != artworkRef {
			continue
		}
		slotRefs[s.Ref] = true
		entries = append(entries, domain.TrackEntry{
			At:      s.Start,
			Kind:    "slot",
			Ref:     s.Ref,
			SlotRef: s.Ref,
			Summary: fmt.Sprintf("场次于 %s 场地 %s，状态 %s",
				s.VenueRef, s.Start.Format(time.RFC3339), s.Status),
		})
	}
	for _, m := range c.state.mvByArt[artworkRef] {
		entries = append(entries, domain.TrackEntry{
			At:      m.OccurredAt,
			Kind:    "movement",
			Ref:     m.Ref,
			SlotRef: m.SlotRef,
			Summary: fmt.Sprintf("阶段 %s%s", m.Kind, noteSuffix(m.Note)),
		})
	}
	for _, d := range c.state.dispos {
		if !c.dispositionTouchesArtwork(d, artworkRef, slotRefs) {
			continue
		}
		status := "未解决"
		if !d.OpenRisk {
			status = "已解决"
		}
		entries = append(entries, domain.TrackEntry{
			At:      d.CreatedAt,
			Kind:    "disposition",
			Ref:     d.Ref,
			Summary: fmt.Sprintf("处置方案（%s，%s）：%d 项动作", d.IncidentKind, status, len(d.Actions)),
		})
	}
	sort.SliceStable(entries, func(i, j int) bool {
		if !entries[i].At.Equal(entries[j].At) {
			return entries[i].At.Before(entries[j].At)
		}
		return entries[i].Ref < entries[j].Ref
	})
	return entries
}

func (c *Coordinator) dispositionTouchesArtwork(d *domain.Disposition, artworkRef string, slotRefs map[string]bool) bool {
	switch d.IncidentKind {
	case domain.IncidentDamage:
		if p := c.state.pieces[d.SubjectRef]; p != nil {
			return p.ArtworkRef == artworkRef
		}
	case domain.IncidentRightsRevoked:
		if g := c.state.grants[d.SubjectRef]; g != nil {
			return g.ArtworkRef == artworkRef
		}
	}
	for _, a := range d.Actions {
		if slotRefs[a.SlotRef] {
			return true
		}
	}
	return false
}

func noteSuffix(note string) string {
	if note == "" {
		return ""
	}
	return "：" + note
}

// VenueBrief 列出某场地在 [start,end) 时段内的场次、安装前置项、
// 未解决风险与可替代方案，供现场协调使用。
func (c *Coordinator) VenueBrief(venueRef string, start, end time.Time) (*domain.VenueBrief, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	venue := c.state.venues[venueRef]
	if venue == nil {
		return nil, validationf("场地 %s 尚未登记", venueRef)
	}
	if !start.Before(end) {
		return nil, validationf("时段开始必须早于结束")
	}
	brief := &domain.VenueBrief{
		VenueRef:    venueRef,
		WindowStart: start.UTC(),
		WindowEnd:   end.UTC(),
	}

	var inWindow []*domain.Slot
	for _, s := range c.state.slots {
		if s.VenueRef == venueRef && s.Status != domain.SlotCancelled &&
			domain.Overlaps(start, end, s.Start, s.End) {
			inWindow = append(inWindow, s)
		}
	}
	sort.Slice(inWindow, func(i, j int) bool {
		if !inWindow[i].Start.Equal(inWindow[j].Start) {
			return inWindow[i].Start.Before(inWindow[j].Start)
		}
		return inWindow[i].Ref < inWindow[j].Ref
	})
	inWindowSet := map[string]bool{}
	for _, s := range inWindow {
		inWindowSet[s.Ref] = true
		brief.Slots = append(brief.Slots, domain.SlotBrief{
			SlotRef:    s.Ref,
			ArtworkRef: s.ArtworkRef,
			Start:      s.Start,
			End:        s.End,
			Status:     s.Status,
		})
	}

	if venue.Closed {
		brief.Risks = append(brief.Risks, domain.RiskView{
			Severity: "error",
			Subject:  venueRef,
			Message:  "场地已关闭",
		})
	}

	for _, s := range inWindow {
		for _, reason := range c.validateSlot(s.ArtworkRef, s.VenueRef, s.Start, s.End, s.Devices, s.Ref) {
			brief.Risks = append(brief.Risks, domain.RiskView{
				Severity: "error",
				Subject:  s.ArtworkRef,
				SlotRef:  s.Ref,
				Message:  reason,
			})
		}
		if s.Status == domain.SlotPlanned {
			for _, reason := range c.unsatisfiedPrereqs(s.Ref) {
				brief.Risks = append(brief.Risks, domain.RiskView{
					Severity: "warning",
					Subject:  s.ArtworkRef,
					SlotRef:  s.Ref,
					Message:  reason,
				})
			}
		}
		for _, d := range c.state.dispos {
			if !d.OpenRisk {
				continue
			}
			for _, a := range d.Actions {
				if a.SlotRef == s.Ref {
					brief.Risks = append(brief.Risks, domain.RiskView{
						Severity: "warning",
						Subject:  d.SubjectRef,
						SlotRef:  s.Ref,
						Message:  fmt.Sprintf("未解决处置方案 %s（%s）", d.Ref, d.IncidentKind),
					})
					break
				}
			}
		}
	}

	// 安装前置项：包含目标场次落在窗口内的全部依赖。
	for _, dep := range c.state.deps {
		if !inWindowSet[dep.ToSlotRef] {
			continue
		}
		view := domain.PrereqView{
			ToSlotRef:   dep.ToSlotRef,
			FromSlotRef: dep.FromSlotRef,
			Requires:    dep.Requires,
		}
		from := c.state.slots[dep.FromSlotRef]
		switch {
		case from == nil:
			view.BlockingReason = "前置场次不存在"
		case from.Status == domain.SlotCancelled:
			view.BlockingReason = "前置场次已取消"
		case c.phaseReached(from, dep.Requires):
			view.Satisfied = true
		default:
			view.BlockingReason = fmt.Sprintf("前置场次尚未达到 %s 阶段", dep.Requires)
		}
		brief.Prereqs = append(brief.Prereqs, view)
	}
	sort.Slice(brief.Prereqs, func(i, j int) bool {
		if brief.Prereqs[i].ToSlotRef != brief.Prereqs[j].ToSlotRef {
			return brief.Prereqs[i].ToSlotRef < brief.Prereqs[j].ToSlotRef
		}
		return brief.Prereqs[i].FromSlotRef < brief.Prereqs[j].FromSlotRef
	})

	// 可替代方案：凡带风险的场次，尝试给出替代安排。
	riskySlots := map[string]string{}
	for _, r := range brief.Risks {
		if r.SlotRef != "" {
			riskySlots[r.SlotRef] = r.Message
		}
	}
	for _, s := range inWindow {
		reason, risky := riskySlots[s.Ref]
		if !risky {
			continue
		}
		alt := c.findAlternative(s.ArtworkRef, s, "")
		if alt == nil {
			continue
		}
		brief.Alternates = append(brief.Alternates, domain.AlternateView{
			ForSlotRef: s.Ref,
			Reason:     reason,
			Spec:       *alt,
		})
	}
	sort.Slice(brief.Alternates, func(i, j int) bool {
		return brief.Alternates[i].ForSlotRef < brief.Alternates[j].ForSlotRef
	})
	return brief, nil
}
