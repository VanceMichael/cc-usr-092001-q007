// Package service 是展陈协调器的应用层：校验命令、把事实作为事件
// 追加到只追加日志，并在内存中维护用于排期与查询的投影。
//
// 所有写操作都遵循“先校验、后追加、再归约”：校验只读取投影，
// 归约函数 apply 不做业务判断，保证重放日志能逐字节还原状态。
package service

import (
	"encoding/json"
	"fmt"
	"sync"

	"example.com/batch-092001-q007/internal/clock"
	"example.com/batch-092001-q007/internal/domain"
	"example.com/batch-092001-q007/internal/store"
)

// SchemaVersion 是当前事件信封版本。
const SchemaVersion = "1"

// ConflictError 表示命令违反了排期或权利不变量。
type ConflictError struct{ Reasons []string }

func (e *ConflictError) Error() string {
	return fmt.Sprintf("冲突：%s", joinReasons(e.Reasons))
}

// ValidationError 表示请求本身不合法。
type ValidationError struct{ Msg string }

func (e *ValidationError) Error() string { return e.Msg }

func validationf(format string, args ...any) error {
	return &ValidationError{Msg: fmt.Sprintf(format, args...)}
}

// Coordinator 持有事件日志与当前投影。
type Coordinator struct {
	mu    sync.Mutex
	log   *store.EventLog
	clk   clock.Clock
	state *state
}

type state struct {
	artworks  map[string]*domain.Artwork
	pieces    map[string]*domain.Piece
	grants    map[string]*domain.RightsGrant
	loans     map[string]*domain.LoanAgreement
	venues    map[string]*domain.Venue
	devices   map[string]*domain.Device
	deps      map[string]*domain.InstallDependency
	slots     map[string]*domain.Slot
	dispos    map[string]*domain.Disposition
	damaged   map[string]bool // 损坏组成件
	mvBySlot  map[string][]*domain.Movement
	mvByPiece map[string][]*domain.Movement
	mvByArt   map[string][]*domain.Movement
	movements []*domain.Movement
	srcSeq    map[string]int64
	idem      map[string]string // 幂等键 -> event_id
}

func newState() *state {
	return &state{
		artworks:  map[string]*domain.Artwork{},
		pieces:    map[string]*domain.Piece{},
		grants:    map[string]*domain.RightsGrant{},
		loans:     map[string]*domain.LoanAgreement{},
		venues:    map[string]*domain.Venue{},
		devices:   map[string]*domain.Device{},
		deps:      map[string]*domain.InstallDependency{},
		slots:     map[string]*domain.Slot{},
		dispos:    map[string]*domain.Disposition{},
		damaged:   map[string]bool{},
		mvBySlot:  map[string][]*domain.Movement{},
		mvByPiece: map[string][]*domain.Movement{},
		mvByArt:   map[string][]*domain.Movement{},
		srcSeq:    map[string]int64{},
		idem:      map[string]string{},
	}
}

// New 打开日志并重放历史事件。path 为空时使用纯内存日志。
func New(path string, clk clock.Clock) (*Coordinator, error) {
	if clk == nil {
		clk = clock.System{}
	}
	c := &Coordinator{clk: clk, state: newState()}
	log, err := store.Open(path, func(evt domain.Event) error {
		return c.apply(evt)
	})
	if err != nil {
		return nil, err
	}
	c.log = log
	return c, nil
}

// Close 关闭底层日志。
func (c *Coordinator) Close() error { return c.log.Close() }

// SubmitResult 是一次成功提交的结果。
type SubmitResult struct {
	Event      domain.Event `json:"event"`
	Idempotent bool         `json:"idempotent"`
}

// alreadySubmitted 在命令做任何业务校验前处理 event_id 重放，
// 返回非空结果表示这是重试，调用方应直接返回。
func (c *Coordinator) alreadySubmitted(eventID string) *SubmitResult {
	if eventID != "" && c.log.Has(eventID) {
		return &SubmitResult{Event: domain.Event{EventID: eventID}, Idempotent: true}
	}
	return nil
}

// submit 是所有写操作的唯一入口：序列号与幂等键检查、追加、归约。
func (c *Coordinator) submit(evt domain.Event) (*SubmitResult, error) {
	if evt.SchemaVersion == "" {
		evt.SchemaVersion = SchemaVersion
	}
	if evt.EventID == "" {
		return nil, validationf("event_id 不能为空")
	}
	// 已提交事件的重放最先短路：即使当前投影已使其不再满足校验
	// （例如撤权后重传排期命令），也必须保持幂等，不得返回新冲突。
	if c.log.Has(evt.EventID) {
		return &SubmitResult{Event: evt, Idempotent: true}, nil
	}
	if evt.SourceID != "" {
		last := c.state.srcSeq[evt.SourceID]
		if evt.SourceSequence <= 0 {
			return nil, validationf("来源 %s 的 source_sequence 必须为正整数", evt.SourceID)
		}
		if last != 0 && evt.SourceSequence < last {
			return nil, &ConflictError{Reasons: []string{
				fmt.Sprintf("来源 %s 序号回退：已到 %d，收到 %d", evt.SourceID, last, evt.SourceSequence)}}
		}
		if last != 0 && evt.SourceSequence == last && !c.log.Has(evt.EventID) {
			return nil, &ConflictError{Reasons: []string{
				fmt.Sprintf("来源 %s 的序号 %d 已被另一事件占用", evt.SourceID, last)}}
		}
		if last != 0 && evt.SourceSequence > last+1 {
			return nil, &ConflictError{Reasons: []string{
				fmt.Sprintf("来源 %s 序号跳跃：已到 %d，收到 %d", evt.SourceID, last, evt.SourceSequence)}}
		}
		// seq == last 且 event_id 相同：重放，交由日志幂等处理。
	}
	if evt.IdempotencyKey != "" {
		if existing, ok := c.state.idem[evt.IdempotencyKey]; ok {
			// 同一自然键的重放（院校批量重传可能携带新 event_id）：
			// 返回首次提交的事件编号，不再追加、不改变状态。
			return &SubmitResult{Event: domain.Event{EventID: existing}, Idempotent: true}, nil
		}
	}
	dup, err := c.log.Append(evt)
	if err != nil {
		return nil, err
	}
	if dup {
		// 重放：event_id 已提交。直接返回，不再改变任何状态。
		return &SubmitResult{Event: evt, Idempotent: true}, nil
	}
	if err := c.apply(evt); err != nil {
		// 理论上不可达：落盘前已校验；返回错误以提示日志与投影不一致。
		return nil, fmt.Errorf("归约事件 %s 失败（日志已提交）：%w", evt.EventID, err)
	}
	return &SubmitResult{Event: evt}, nil
}

// apply 是无业务判断的归约函数，供在线提交与启动重放共用。
func (c *Coordinator) apply(evt domain.Event) error {
	c.state.srcSeq[evt.SourceID] = seqOr(c.state.srcSeq[evt.SourceID], evt.SourceSequence)
	if evt.IdempotencyKey != "" {
		c.state.idem[evt.IdempotencyKey] = evt.EventID
	}
	switch evt.Type {
	default:
		return validationf("未知事件类型 %q", evt.Type)
	case domain.EvtArtworkRegistered:
		var v domain.Artwork
		if err := json.Unmarshal(evt.Payload, &v); err != nil {
			return err
		}
		c.state.artworks[v.Ref] = &v
	case domain.EvtPieceRegistered:
		var v domain.Piece
		if err := json.Unmarshal(evt.Payload, &v); err != nil {
			return err
		}
		c.state.pieces[v.Ref] = &v
		if a := c.state.artworks[v.ArtworkRef]; a != nil {
			if !containsPiece(a.Pieces, v.Ref) {
				a.Pieces = append(a.Pieces, &v)
			}
		}
	case domain.EvtRightsRecorded:
		var v domain.RightsGrant
		if err := json.Unmarshal(evt.Payload, &v); err != nil {
			return err
		}
		c.state.grants[v.Ref] = &v
	case domain.EvtLoanRecorded:
		var v domain.LoanAgreement
		if err := json.Unmarshal(evt.Payload, &v); err != nil {
			return err
		}
		c.state.loans[v.Ref] = &v
	case domain.EvtVenueRegistered:
		var v domain.Venue
		if err := json.Unmarshal(evt.Payload, &v); err != nil {
			return err
		}
		c.state.venues[v.Ref] = &v
	case domain.EvtDeviceRegistered:
		var v domain.Device
		if err := json.Unmarshal(evt.Payload, &v); err != nil {
			return err
		}
		c.state.devices[v.Ref] = &v
	case domain.EvtInstallDependencyAdded:
		var v domain.InstallDependency
		if err := json.Unmarshal(evt.Payload, &v); err != nil {
			return err
		}
		c.state.deps[v.Ref] = &v
	case domain.EvtSlotPlanned, domain.EvtSlotRescheduled:
		var p slotPayload
		if err := json.Unmarshal(evt.Payload, &p); err != nil {
			return err
		}
		s := c.state.slots[p.Ref]
		if s == nil {
			s = &domain.Slot{Ref: p.Ref}
			c.state.slots[p.Ref] = s
		}
		s.ArtworkRef, s.VenueRef = p.ArtworkRef, p.VenueRef
		s.Start, s.End = p.Start, p.End
		s.Devices = p.Devices
		s.Status = domain.SlotPlanned
		s.Version++
		s.UpdatedAt = evt.OccurredAt
	case domain.EvtSlotConfirmed:
		var p refPayload
		if err := json.Unmarshal(evt.Payload, &p); err != nil {
			return err
		}
		if s := c.state.slots[p.Ref]; s != nil {
			s.Status = domain.SlotConfirmed
			s.UpdatedAt = evt.OccurredAt
		}
	case domain.EvtSlotCancelled:
		var p refPayload
		if err := json.Unmarshal(evt.Payload, &p); err != nil {
			return err
		}
		if s := c.state.slots[p.Ref]; s != nil {
			s.Status = domain.SlotCancelled
			s.UpdatedAt = evt.OccurredAt
		}
	case domain.EvtMovementAppended:
		var v domain.Movement
		if err := json.Unmarshal(evt.Payload, &v); err != nil {
			return err
		}
		c.state.movements = append(c.state.movements, &v)
		c.state.mvByArt[v.ArtworkRef] = append(c.state.mvByArt[v.ArtworkRef], &v)
		if v.PieceRef != "" {
			c.state.mvByPiece[v.PieceRef] = append(c.state.mvByPiece[v.PieceRef], &v)
		}
		if v.SlotRef != "" {
			c.state.mvBySlot[v.SlotRef] = append(c.state.mvBySlot[v.SlotRef], &v)
		}
	case domain.EvtDamageReported:
		var p damagePayload
		if err := json.Unmarshal(evt.Payload, &p); err != nil {
			return err
		}
		if p.PieceRef != "" {
			c.state.damaged[p.PieceRef] = true
		}
	case domain.EvtRightsWithdrawn:
		var p withdrawalPayload
		if err := json.Unmarshal(evt.Payload, &p); err != nil {
			return err
		}
		if g := c.state.grants[p.GrantRef]; g != nil {
			g.Withdrawn = true
			g.WithdrawnAt = evt.OccurredAt
		}
	case domain.EvtVenueClosed:
		var p closurePayload
		if err := json.Unmarshal(evt.Payload, &p); err != nil {
			return err
		}
		if v := c.state.venues[p.VenueRef]; v != nil {
			v.Closed = true
			v.ClosedAt = evt.OccurredAt
		}
	case domain.EvtDeviceBroken:
		var p breakagePayload
		if err := json.Unmarshal(evt.Payload, &p); err != nil {
			return err
		}
		if d := c.state.devices[p.DeviceRef]; d != nil {
			d.Broken = true
			d.BrokenAt = evt.OccurredAt
		}
	case domain.EvtDispositionGenerated:
		var v domain.Disposition
		if err := json.Unmarshal(evt.Payload, &v); err != nil {
			return err
		}
		c.state.dispos[v.Ref] = &v
	case domain.EvtDispositionResolved:
		var p struct {
			Ref        string `json:"ref"`
			Resolution string `json:"resolution"`
		}
		if err := json.Unmarshal(evt.Payload, &p); err != nil {
			return err
		}
		if d := c.state.dispos[p.Ref]; d != nil {
			d.OpenRisk = false
			d.ResolvedAt = evt.OccurredAt
			d.Resolution = p.Resolution
		}
	}
	return nil
}

func seqOr(last, incoming int64) int64 {
	if incoming > last {
		return incoming
	}
	return last
}

func containsPiece(pieces []*domain.Piece, ref string) bool {
	for _, p := range pieces {
		if p.Ref == ref {
			return true
		}
	}
	return false
}
