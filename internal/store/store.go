// Package store 是协调器的内存事实库与只追加事件台账。
//
// 生产部署可在同一接口后替换为持久化实现；当前实现用互斥锁保证并发安全，
// 事件一经追加不可修改（只提供读取索引，不提供删除/改写入口）。
package store

import (
	"sort"
	"sync"
	"time"

	"example.com/batch-092001-q007/internal/domain"
)

// BatchRecord 记录某个来源的某份批量清单的首次受理结果。
// 同一 (source, batch_ref) 的重传直接返回该记录，实现幂等。
type BatchRecord struct {
	Key        string    `json:"key"`
	Source     string    `json:"source"`
	BatchRef   string    `json:"batch_ref"`
	Digest     string    `json:"digest"`
	EventIDs   []string  `json:"event_ids"`
	Works      []string  `json:"works"`
	ReceivedAt time.Time `json:"received_at"`
}

// Store 保存全部业务事实。
type Store struct {
	mu sync.RWMutex

	works      map[string]domain.Work
	components map[string]domain.Component
	rights     map[string]domain.Right
	loans      map[string]domain.Loan
	venues     map[string]domain.Venue
	equipment  map[string]domain.Equipment
	slots      map[string]domain.Slot
	prereqs    map[string]domain.Prerequisite
	plans      map[string]domain.Plan
	changes    map[string]domain.ChangeOrder

	// events 按追加顺序保存；eventByID 用于幂等去重；subjectIndex 指向 events 下标。
	events       []domain.Event
	eventByID    map[string]int
	subjectIndex map[string][]int
	maxSequence  map[string]int64

	batches   map[string]BatchRecord
	sourceSeq map[string]int64

	counters map[string]int64
}

// New 创建空事实库。
func New() *Store {
	return &Store{
		works:        map[string]domain.Work{},
		components:   map[string]domain.Component{},
		rights:       map[string]domain.Right{},
		loans:        map[string]domain.Loan{},
		venues:       map[string]domain.Venue{},
		equipment:    map[string]domain.Equipment{},
		slots:        map[string]domain.Slot{},
		prereqs:      map[string]domain.Prerequisite{},
		plans:        map[string]domain.Plan{},
		changes:      map[string]domain.ChangeOrder{},
		eventByID:    map[string]int{},
		subjectIndex: map[string][]int{},
		maxSequence:  map[string]int64{},
		batches:      map[string]BatchRecord{},
		sourceSeq:    map[string]int64{},
		counters:     map[string]int64{},
	}
}

// NextID 生成带前缀的稳定编号。
func (s *Store) NextID(prefix string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.counters[prefix]++
	return prefix + "-" + itoa(s.counters[prefix])
}

// ---------- 作品与组成件 ----------

func (s *Store) PutWork(w domain.Work) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.works[w.Ref] = w
}

func (s *Store) GetWork(ref string) (domain.Work, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	w, ok := s.works[ref]
	return w, ok
}

func (s *Store) ListWorks() []domain.Work {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]domain.Work, 0, len(s.works))
	for _, w := range s.works {
		out = append(out, w)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Ref < out[j].Ref })
	return out
}

func (s *Store) PutComponent(c domain.Component) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.components[c.Ref] = c
	if w, ok := s.works[c.WorkRef]; ok {
		if !contains(w.Components, c.Ref) {
			w.Components = append(w.Components, c.Ref)
			s.works[c.WorkRef] = w
		}
	}
}

func (s *Store) GetComponent(ref string) (domain.Component, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	c, ok := s.components[ref]
	return c, ok
}

// MutateComponent 在锁内读取-修改组成件。
func (s *Store) MutateComponent(ref string, fn func(*domain.Component)) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	c, ok := s.components[ref]
	if !ok {
		return false
	}
	fn(&c)
	s.components[ref] = c
	return true
}

func (s *Store) ComponentsOf(workRef string) []domain.Component {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var out []domain.Component
	for _, c := range s.components {
		if c.WorkRef == workRef {
			out = append(out, c)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Ref < out[j].Ref })
	return out
}

// ---------- 权利 / 借展 ----------

func (s *Store) PutRight(r domain.Right) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.rights[r.Ref] = r
}

func (s *Store) GetRight(ref string) (domain.Right, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	r, ok := s.rights[ref]
	return r, ok
}

func (s *Store) MutateRight(ref string, fn func(*domain.Right)) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	r, ok := s.rights[ref]
	if !ok {
		return false
	}
	fn(&r)
	s.rights[ref] = r
	return true
}

// RightsOf 返回某作品当前有效的全部声明（含已撤回，撤回状态在结构内体现）。
func (s *Store) RightsOf(workRef string) []domain.Right {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var out []domain.Right
	for _, r := range s.rights {
		if r.WorkRef == workRef {
			out = append(out, r)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Ref < out[j].Ref })
	return out
}

// ActiveRightsOf 仅返回未撤回声明。
func (s *Store) ActiveRightsOf(workRef string) []domain.Right {
	var out []domain.Right
	for _, r := range s.RightsOf(workRef) {
		if !r.Revoked {
			out = append(out, r)
		}
	}
	return out
}

func (s *Store) PutLoan(l domain.Loan) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.loans[l.Ref] = l
}

// LoanOf 返回作品的借展协议；一件作品当前模型下只有一份。
func (s *Store) LoanOf(workRef string) (*domain.Loan, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, l := range s.loans {
		if l.WorkRef == workRef {
			copy := l
			return &copy, true
		}
	}
	return nil, false
}

// ---------- 场地 / 设备 ----------

func (s *Store) PutVenue(v domain.Venue) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.venues[v.Ref] = v
}

func (s *Store) GetVenue(ref string) (domain.Venue, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	v, ok := s.venues[ref]
	return v, ok
}

func (s *Store) MutateVenue(ref string, fn func(*domain.Venue)) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	v, ok := s.venues[ref]
	if !ok {
		return false
	}
	fn(&v)
	s.venues[ref] = v
	return true
}

func (s *Store) ListVenues() []domain.Venue {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]domain.Venue, 0, len(s.venues))
	for _, v := range s.venues {
		out = append(out, v)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Ref < out[j].Ref })
	return out
}

func (s *Store) PutEquipment(e domain.Equipment) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.equipment[e.Ref] = e
}

func (s *Store) GetEquipment(ref string) (domain.Equipment, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	e, ok := s.equipment[ref]
	return e, ok
}

func (s *Store) ListEquipment() []domain.Equipment {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]domain.Equipment, 0, len(s.equipment))
	for _, e := range s.equipment {
		out = append(out, e)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Ref < out[j].Ref })
	return out
}

// ---------- 排期 ----------

func (s *Store) PutSlot(sl domain.Slot) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.slots[sl.Ref] = sl
}

func (s *Store) GetSlot(ref string) (domain.Slot, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	sl, ok := s.slots[ref]
	return sl, ok
}

// ActiveSlots 返回未取消、未被替换的排期（含已提议与已确认）。
func (s *Store) ActiveSlots() []domain.Slot {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var out []domain.Slot
	for _, sl := range s.slots {
		if sl.Status == domain.SlotProposed || sl.Status == domain.SlotConfirmed {
			out = append(out, sl)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Ref < out[j].Ref })
	return out
}

func (s *Store) SlotsForWork(workRef string) []domain.Slot {
	var out []domain.Slot
	for _, sl := range s.ActiveSlots() {
		if sl.WorkRef == workRef {
			out = append(out, sl)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Start.Before(out[j].Start) })
	return out
}

func (s *Store) SlotsForVenue(venueRef string) []domain.Slot {
	var out []domain.Slot
	for _, sl := range s.ActiveSlots() {
		if sl.VenueRef == venueRef {
			out = append(out, sl)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Start.Before(out[j].Start) })
	return out
}

// AllSlotsForWork 含已取消/已被替换的历史排期，用于轨迹查询。
func (s *Store) AllSlotsForWork(workRef string) []domain.Slot {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var out []domain.Slot
	for _, sl := range s.slots {
		if sl.WorkRef == workRef {
			out = append(out, sl)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Start.Before(out[j].Start) })
	return out
}

// ---------- 前置项 ----------

func (s *Store) PutPrerequisite(p domain.Prerequisite) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.prereqs[p.Ref] = p
}

func (s *Store) GetPrerequisite(ref string) (domain.Prerequisite, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	p, ok := s.prereqs[ref]
	return p, ok
}

func (s *Store) MutatePrerequisite(ref string, fn func(*domain.Prerequisite)) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	p, ok := s.prereqs[ref]
	if !ok {
		return false
	}
	fn(&p)
	s.prereqs[ref] = p
	return true
}

func (s *Store) PrereqsForSlot(slotRef string) []domain.Prerequisite {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var out []domain.Prerequisite
	for _, p := range s.prereqs {
		if p.SlotRef == slotRef {
			out = append(out, p)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].RequiredBy.Equal(out[j].RequiredBy) {
			return out[i].Ref < out[j].Ref
		}
		return out[i].RequiredBy.Before(out[j].RequiredBy)
	})
	return out
}

// ---------- 处置方案 / 换场单 ----------

func (s *Store) PutPlan(p domain.Plan) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.plans[p.Ref] = p
}

func (s *Store) GetPlan(ref string) (domain.Plan, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	p, ok := s.plans[ref]
	return p, ok
}

func (s *Store) MutatePlan(ref string, fn func(*domain.Plan)) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	p, ok := s.plans[ref]
	if !ok {
		return false
	}
	fn(&p)
	s.plans[ref] = p
	return true
}

// PlansForSubject 返回某主体（作品/场地）的处置方案，按创建时间排序。
func (s *Store) PlansForSubject(subjectRef string) []domain.Plan {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var out []domain.Plan
	for _, p := range s.plans {
		if p.SubjectRef == subjectRef {
			out = append(out, p)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.Before(out[j].CreatedAt) })
	return out
}

func (s *Store) PutChangeOrder(c domain.ChangeOrder) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.changes[c.Ref] = c
}

func (s *Store) ListChangeOrders() []domain.ChangeOrder {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]domain.ChangeOrder, 0, len(s.changes))
	for _, c := range s.changes {
		out = append(out, c)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.Before(out[j].CreatedAt) })
	return out
}

// ---------- 事件台账（只追加） ----------

// AppendEventResult 说明追加结果。Duplicate 为 true 时 Event 是此前已受理的同一事件。
type AppendEventResult struct {
	Event     domain.Event
	Duplicate bool
	StaleSeq  bool // 来源序号小于已知最大序号（迟到事件），仍按原始 occurred_at 落账
}

// AppendEvent 把事件追加进台账。event_id 重复时原样返回既有记录，绝不覆盖。
func (s *Store) AppendEvent(ev domain.Event) AppendEventResult {
	s.mu.Lock()
	defer s.mu.Unlock()

	if idx, ok := s.eventByID[ev.EventID]; ok {
		return AppendEventResult{Event: s.events[idx], Duplicate: true}
	}

	max := s.maxSequence[ev.Source]
	stale := ev.SourceSequence < max
	if ev.SourceSequence > max {
		s.maxSequence[ev.Source] = ev.SourceSequence
	}

	idx := len(s.events)
	s.events = append(s.events, ev)
	s.eventByID[ev.EventID] = idx
	s.subjectIndex[ev.SubjectRef] = append(s.subjectIndex[ev.SubjectRef], idx)
	return AppendEventResult{Event: ev, StaleSeq: stale}
}

// EventsForSubject 按原始发生时间（其次来源序号）返回某主体的全部台账事件。
func (s *Store) EventsForSubject(subjectRef string) []domain.Event {
	s.mu.RLock()
	defer s.mu.RUnlock()
	idxs := s.subjectIndex[subjectRef]
	out := make([]domain.Event, 0, len(idxs))
	for _, i := range idxs {
		out = append(out, s.events[i])
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].OccurredAt.Equal(out[j].OccurredAt) {
			if out[i].Source == out[j].Source {
				return out[i].SourceSequence < out[j].SourceSequence
			}
			return out[i].EventID < out[j].EventID
		}
		return out[i].OccurredAt.Before(out[j].OccurredAt)
	})
	return out
}

// AllEvents 按追加顺序返回全部事件。
func (s *Store) AllEvents() []domain.Event {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]domain.Event, len(s.events))
	copy(out, s.events)
	return out
}

// ---------- 批量清单幂等 ----------

// BatchKey 构造批量清单的幂等键。
func BatchKey(source, batchRef string) string { return source + "::" + batchRef }

func (s *Store) GetBatch(source, batchRef string) (BatchRecord, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	r, ok := s.batches[BatchKey(source, batchRef)]
	return r, ok
}

func (s *Store) SaveBatch(r BatchRecord) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.batches[r.Key] = r
}

// NextSourceSequence 返回某来源的下一个受理序号（每份清单/每个事件各占一个序号）。
func (s *Store) NextSourceSequence(source string) int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sourceSeq[source]++
	return s.sourceSeq[source]
}

// ---------- 小工具 ----------

func contains(list []string, v string) bool {
	for _, item := range list {
		if item == v {
			return true
		}
	}
	return false
}

func itoa(n int64) string {
	if n == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}
