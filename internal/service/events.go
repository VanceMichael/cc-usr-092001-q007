package service

import (
	"encoding/json"
	"fmt"
	"time"

	"example.com/batch-092001-q007/internal/domain"
)

// 受支持的运营事件类型。
const (
	EvtComponentArrived = "component_arrived"
	EvtComponentDamaged = "component_damaged"
	EvtRightRevoked     = "right_revoked"
	EvtVenueClosed      = "venue_closed"
	EvtInsuranceBound   = "insurance_bound"
	EvtPermitGranted    = "public_permit_granted"
	EvtTeamAssigned     = "install_team_assigned"
	EvtPlaybackChecked  = "playback_checked"
)

// EventEffect 描述事件受理后产生的副作用，供调用方核对。
type EventEffect struct {
	ResolvedPrereqs []string `json:"resolved_prereqs,omitempty"`
	PlanRef         string   `json:"plan_ref,omitempty"`
	MarkedArrived   string   `json:"marked_arrived,omitempty"`
	Note            string   `json:"note,omitempty"`
}

// EventResponse 是事件受理结果；Duplicate 表示 event_id 已存在，副作用不会重复执行。
type EventResponse struct {
	EventID    string      `json:"event_id"`
	Duplicate  bool        `json:"duplicate"`
	StaleSeq   bool        `json:"stale_sequence,omitempty"`
	OccurredAt time.Time   `json:"occurred_at"`
	ReceivedAt time.Time   `json:"received_at"`
	Effect     EventEffect `json:"effect"`
}

type componentArrivedPayload struct {
	ComponentRef string `json:"component_ref"`
}

type componentDamagedPayload struct {
	ComponentRef string `json:"component_ref"`
	Note         string `json:"note"`
}

type rightRevokedPayload struct {
	RightRef string `json:"right_ref"`
	Reason   string `json:"reason"`
}

type venueClosedPayload struct {
	VenueRef string     `json:"venue_ref"`
	From     *time.Time `json:"from,omitempty"`
	Reason   string     `json:"reason"`
}

type slotRefPayload struct {
	SlotRef string `json:"slot_ref"`
	Ref     string `json:"ref"` // 保单/许可/班组编号
}

// IngestEvent 受理外部运营事件：先做主体校验，再追加只增台账，最后执行副作用。
// 事件的原始 occurred_at 原样保留；event_id 重复时幂等返回。
func (s *Service) IngestEvent(in EventRequest) (EventResponse, error) {
	if in.EventID == "" || in.Source == "" || in.SubjectRef == "" || in.Type == "" {
		return EventResponse{}, badRequest("invalid_event", "event_id/source/subject_ref/type 为必填")
	}
	if in.OccurredAt.IsZero() {
		return EventResponse{}, badRequest("invalid_event", "occurred_at 为必填，须携带偏移量")
	}
	if in.SourceSequence <= 0 {
		return EventResponse{}, badRequest("invalid_event", "source_sequence 必须为正整数")
	}

	// 如外部带来摘要，必须与服务端重算一致；不一致说明传输内容被改动，拒绝入账。
	digest := domain.DigestBytes(in.Payload)
	if in.PayloadDigest != "" && in.PayloadDigest != digest {
		return EventResponse{}, badRequest("digest_mismatch",
			"payload_digest 与负载不一致：声明 %s，实算 %s", in.PayloadDigest, digest)
	}

	// 先校验副作用所需主体存在，避免台账出现无法执行的残缺事件。
	if err := s.prevalidateEvent(in); err != nil {
		return EventResponse{}, err
	}

	ev := domain.Event{
		SchemaVersion:  orDefault(in.SchemaVersion, "1"),
		EventID:        in.EventID,
		Source:         in.Source,
		SourceSequence: in.SourceSequence,
		OccurredAt:     in.OccurredAt,
		ReceivedAt:     s.now(),
		SubjectRef:     in.SubjectRef,
		Type:           in.Type,
		PayloadDigest:  digest,
		Payload:        append([]byte(nil), in.Payload...),
	}
	res := s.store.AppendEvent(ev)
	if res.Duplicate {
		return EventResponse{
			EventID: res.Event.EventID, Duplicate: true,
			OccurredAt: res.Event.OccurredAt, ReceivedAt: res.Event.ReceivedAt,
			Effect: EventEffect{Note: "事件已受理过，本次为重放，副作用未重复执行"},
		}, nil
	}

	effect, err := s.applyEvent(in)
	if err != nil {
		// 副作用理论上已被 prevalidate 覆盖；兜底返回错误，事件仍留在台账中。
		return EventResponse{}, fmt.Errorf("事件 %s 已入账但副作用失败: %w", in.EventID, err)
	}
	return EventResponse{
		EventID: ev.EventID, StaleSeq: res.StaleSeq,
		OccurredAt: ev.OccurredAt, ReceivedAt: ev.ReceivedAt, Effect: effect,
	}, nil
}

func (s *Service) prevalidateEvent(in EventRequest) error {
	switch in.Type {
	case EvtComponentArrived, EvtComponentDamaged:
		var p struct {
			ComponentRef string `json:"component_ref"`
		}
		if err := decodePayload(in.Payload, &p); err != nil || p.ComponentRef == "" {
			return badRequest("invalid_payload", "%s 事件需要 component_ref", in.Type)
		}
		if _, ok := s.store.GetComponent(p.ComponentRef); !ok {
			return notFound("组成件 %s 不存在", p.ComponentRef)
		}
	case EvtRightRevoked:
		var p rightRevokedPayload
		if err := decodePayload(in.Payload, &p); err != nil || p.RightRef == "" {
			return badRequest("invalid_payload", "right_revoked 事件需要 right_ref")
		}
		if _, ok := s.store.GetRight(p.RightRef); !ok {
			return notFound("权利声明 %s 不存在", p.RightRef)
		}
	case EvtVenueClosed:
		var p venueClosedPayload
		if err := decodePayload(in.Payload, &p); err != nil || p.VenueRef == "" {
			return badRequest("invalid_payload", "venue_closed 事件需要 venue_ref")
		}
		if _, ok := s.store.GetVenue(p.VenueRef); !ok {
			return notFound("场地 %s 不存在", p.VenueRef)
		}
	case EvtInsuranceBound, EvtPermitGranted, EvtTeamAssigned, EvtPlaybackChecked:
		var p slotRefPayload
		if err := decodePayload(in.Payload, &p); err != nil || p.SlotRef == "" {
			return badRequest("invalid_payload", "%s 事件需要 slot_ref", in.Type)
		}
		if _, ok := s.store.GetSlot(p.SlotRef); !ok {
			return notFound("排期 %s 不存在", p.SlotRef)
		}
	default:
		return badRequest("unknown_event_type", "不支持的事件类型 %q", in.Type)
	}
	return nil
}

func (s *Service) applyEvent(in EventRequest) (EventEffect, error) {
	switch in.Type {
	case EvtComponentArrived:
		var p componentArrivedPayload
		_ = decodePayload(in.Payload, &p)
		at := in.OccurredAt
		s.store.MutateComponent(p.ComponentRef, func(c *domain.Component) {
			c.Arrived = true
			c.ArrivedAt = &at
		})
		resolved := s.resolvePrereqsBySubject(domain.PrereqTransport, p.ComponentRef, in.EventID, at)
		return EventEffect{MarkedArrived: p.ComponentRef, ResolvedPrereqs: resolved}, nil

	case EvtComponentDamaged:
		var p componentDamagedPayload
		_ = decodePayload(in.Payload, &p)
		at := in.OccurredAt
		s.store.MutateComponent(p.ComponentRef, func(c *domain.Component) {
			c.Damaged = true
			c.DamagedAt = &at
		})
		planRef, affected := s.openDamagePlan(p.ComponentRef, p.Note, in.EventID, at)
		return EventEffect{PlanRef: planRef, ResolvedPrereqs: nil,
			Note: fmt.Sprintf("已生成损坏处置方案，受影响排期 %d 条", affected)}, nil

	case EvtRightRevoked:
		var p rightRevokedPayload
		_ = decodePayload(in.Payload, &p)
		at := in.OccurredAt
		s.store.MutateRight(p.RightRef, func(r *domain.Right) {
			r.Revoked = true
			r.RevokedAt = &at
			r.Reason = p.Reason
		})
		r, _ := s.store.GetRight(p.RightRef)
		planRef, affected := s.openWithdrawalPlan(r, p.Reason, in.EventID, at)
		return EventEffect{PlanRef: planRef,
			Note: fmt.Sprintf("已生成权利撤回处置方案，受影响排期 %d 条", affected)}, nil

	case EvtVenueClosed:
		var p venueClosedPayload
		_ = decodePayload(in.Payload, &p)
		at := in.OccurredAt
		s.store.MutateVenue(p.VenueRef, func(v *domain.Venue) {
			v.Closed = true
			v.ClosedAt = &at
			v.CloseFrom = p.From
		})
		planRef, affected := s.openClosurePlan(p.VenueRef, p.Reason, in.EventID, at)
		return EventEffect{PlanRef: planRef,
			Note: fmt.Sprintf("已生成场地关闭处置方案，受影响排期 %d 条", affected)}, nil

	case EvtInsuranceBound:
		var p slotRefPayload
		_ = decodePayload(in.Payload, &p)
		resolved := s.resolveSlotPrereqs(domain.PrereqInsurance, p.SlotRef, in.EventID, in.OccurredAt)
		return EventEffect{ResolvedPrereqs: resolved}, nil

	case EvtPermitGranted:
		var p slotRefPayload
		_ = decodePayload(in.Payload, &p)
		resolved := s.resolveSlotPrereqs(domain.PrereqPermit, p.SlotRef, in.EventID, in.OccurredAt)
		return EventEffect{ResolvedPrereqs: resolved}, nil

	case EvtTeamAssigned:
		var p slotRefPayload
		_ = decodePayload(in.Payload, &p)
		resolved := s.resolveSlotPrereqs(domain.PrereqTeam, p.SlotRef, in.EventID, in.OccurredAt)
		return EventEffect{ResolvedPrereqs: resolved}, nil

	case EvtPlaybackChecked:
		var p slotRefPayload
		_ = decodePayload(in.Payload, &p)
		resolved := s.resolveSlotPrereqs(domain.PrereqPlayback, p.SlotRef, in.EventID, in.OccurredAt)
		return EventEffect{ResolvedPrereqs: resolved}, nil
	}
	return EventEffect{}, nil
}

// resolveSlotPrereqs 将某排期上指定类型的未决前置项标记为已解决。
func (s *Service) resolveSlotPrereqs(kind, slotRef, eventID string, at time.Time) []string {
	var resolved []string
	for _, p := range s.store.PrereqsForSlot(slotRef) {
		if p.Kind == kind && p.Status == domain.PrereqOpen {
			s.store.MutatePrerequisite(p.Ref, func(pp *domain.Prerequisite) {
				pp.Status = domain.PrereqResolved
				pp.ResolvedBy = eventID
				pp.ResolvedAt = &at
			})
			resolved = append(resolved, p.Ref)
		}
	}
	return resolved
}

// resolvePrereqsBySubject 按主体（如组成件）解决前置项，用于运输到场事件。
func (s *Service) resolvePrereqsBySubject(kind, subjectRef, eventID string, at time.Time) []string {
	var resolved []string
	for _, sl := range s.store.ActiveSlots() {
		for _, p := range s.store.PrereqsForSlot(sl.Ref) {
			if p.Kind == kind && p.SubjectRef == subjectRef && p.Status == domain.PrereqOpen {
				s.store.MutatePrerequisite(p.Ref, func(pp *domain.Prerequisite) {
					pp.Status = domain.PrereqResolved
					pp.ResolvedBy = eventID
					pp.ResolvedAt = &at
				})
				resolved = append(resolved, p.Ref)
			}
		}
	}
	return resolved
}

// recordSystemEvent 把协调器自身产生的生命周期事实（提议/确认/换场/取消）追加进同一本台账，
// 使作品轨迹与外部事件使用同一条只追加链路。
func (s *Service) recordSystemEvent(eventType, subjectRef string, payload any) string {
	raw := mustJSON(payload)
	ev := domain.Event{
		SchemaVersion: "1", EventID: s.store.NextID("EVT"), Source: "coordinator",
		SourceSequence: s.store.NextSourceSequence("coordinator"),
		OccurredAt:     s.now(), ReceivedAt: s.now(),
		SubjectRef: subjectRef, Type: eventType,
		PayloadDigest: domain.DigestBytes(raw), Payload: raw,
	}
	s.store.AppendEvent(ev)
	return ev.EventID
}

// marshalPayload 仅供测试与导出使用。
func marshalPayload(v any) json.RawMessage { return mustJSON(v) }
