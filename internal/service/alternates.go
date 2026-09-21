package service

import (
	"fmt"
	"sort"
	"time"

	"example.com/batch-092001-q007/internal/domain"
)

// coveredByOtherRights 判断除 excludeRight 外，是否仍有有效声明完整覆盖区间。
func (s *Service) coveredByOtherRights(workRef, excludeRight string, venue domain.Venue, start, end time.Time) bool {
	work, ok := s.store.GetWork(workRef)
	if !ok {
		return false
	}
	for _, r := range s.store.ActiveRightsOf(workRef) {
		if r.Ref == excludeRight {
			continue
		}
		if r.CoversInterval(venue.Country, work.Medium, start, end) {
			return true
		}
	}
	return false
}

// alternativesForSlots 为受影响排期生成可替代场地、同院校同媒介替代作品与空闲替代设备。
// 结果去重，供处置方案与场地时段视图共用。
func (s *Service) alternativesForSlots(affected []domain.Slot) []domain.Alternative {
	var out []domain.Alternative
	seen := map[string]bool{}
	add := func(a domain.Alternative) {
		key := a.Kind + "|" + a.Ref
		if seen[key] {
			return
		}
		seen[key] = true
		out = append(out, a)
	}

	for _, sl := range affected {
		work, _ := s.store.GetWork(sl.WorkRef)
		venue, _ := s.store.GetVenue(sl.VenueRef)

		// 替代场地：开放、接受该媒介、权利在其国家覆盖原时段。
		for _, v := range s.store.ListVenues() {
			if v.Ref == sl.VenueRef || v.Closed {
				continue
			}
			if !containsStr(v.AcceptedMedia, domain.MediumAll) && !containsStr(v.AcceptedMedia, work.Medium) {
				continue
			}
			if !s.rightsCover(work.Ref, v.Country, sl.Start, sl.End) {
				continue
			}
			add(domain.Alternative{
				Kind: "venue", Ref: v.Ref, SlotRef: sl.Ref,
				Detail: fmt.Sprintf("%s（%s，%s）可承接作品 %s", v.Name, v.Country, v.Timezone, work.Ref),
			})
		}

		// 替代作品：同院校、同媒介、组成件无损坏、权利覆盖现场地国家及时段、且同期无排期。
		for _, w := range s.store.ListWorks() {
			if w.Ref == sl.WorkRef || w.SchoolRef != work.SchoolRef || w.Medium != work.Medium {
				continue
			}
			damaged := false
			for _, c := range s.store.ComponentsOf(w.Ref) {
				if c.Damaged {
					damaged = true
					break
				}
			}
			if damaged || !s.rightsCover(w.Ref, venue.Country, sl.Start, sl.End) {
				continue
			}
			if s.workOccupied(w.Ref, sl.Start, sl.End, "") {
				continue
			}
			add(domain.Alternative{
				Kind: "work", Ref: w.Ref, SlotRef: sl.Ref,
				Detail: fmt.Sprintf("同院校替代作品《%s》（%s）", w.Title, w.Ref),
			})
		}

		// 替代设备：与原设备能力相交、且原时段空闲。
		for _, eqRef := range sl.Equipment {
			orig, ok := s.store.GetEquipment(eqRef)
			if !ok {
				continue
			}
			for _, cand := range s.store.ListEquipment() {
				if cand.Ref == eqRef || !capabilitiesIntersect(orig, cand) {
					continue
				}
				if s.equipmentOccupied(cand.Ref, sl.Start, sl.End, sl.Ref) {
					continue
				}
				add(domain.Alternative{
					Kind: "equipment", Ref: cand.Ref, SlotRef: sl.Ref,
					Detail: fmt.Sprintf("可替换 %s 的设备 %s（%s）", eqRef, cand.Ref, cand.Kind),
				})
			}
		}
	}

	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Kind != out[j].Kind {
			return out[i].Kind < out[j].Kind
		}
		if out[i].SlotRef != out[j].SlotRef {
			return out[i].SlotRef < out[j].SlotRef
		}
		return out[i].Ref < out[j].Ref
	})
	return out
}

func (s *Service) rightsCover(workRef, country string, start, end time.Time) bool {
	work, ok := s.store.GetWork(workRef)
	if !ok {
		return false
	}
	for _, r := range s.store.ActiveRightsOf(workRef) {
		if r.CoversInterval(country, work.Medium, start, end) {
			return true
		}
	}
	return false
}

// workOccupied 判断作品的实体组成件在窗口内是否已被其他排期占用。
func (s *Service) workOccupied(workRef string, start, end time.Time, excludeSlot string) bool {
	for _, other := range s.store.ActiveSlots() {
		if other.WorkRef != workRef || other.Ref == excludeSlot {
			continue
		}
		if domain.Overlaps(start, end, other.Start, other.End) {
			return true
		}
	}
	return false
}

// equipmentOccupied 判断设备在窗口内是否已被其他排期占用。
func (s *Service) equipmentOccupied(eqRef string, start, end time.Time, excludeSlot string) bool {
	for _, other := range s.store.ActiveSlots() {
		if other.Ref == excludeSlot {
			continue
		}
		if !containsStr(other.Equipment, eqRef) {
			continue
		}
		if domain.Overlaps(start, end, other.Start, other.End) {
			return true
		}
	}
	return false
}

func capabilitiesIntersect(a, b domain.Equipment) bool {
	for _, p := range a.PlaybackProfiles {
		if containsStr(b.PlaybackProfiles, p) {
			return true
		}
	}
	for _, p := range a.Provides {
		if containsStr(b.Provides, p) {
			return true
		}
	}
	return a.Kind == b.Kind
}

func containsStr(list []string, v string) bool {
	for _, x := range list {
		if x == v {
			return true
		}
	}
	return false
}
