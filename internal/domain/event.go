package domain

import (
	"encoding/json"
	"time"
)

// EventType 是协调事件的受控类型。
type EventType string

const (
	// 登记类命令，payload 为对应领域对象的 JSON。
	EvtArtworkRegistered      EventType = "artwork_registered"
	EvtPieceRegistered        EventType = "piece_registered"
	EvtRightsRecorded         EventType = "rights_recorded"
	EvtLoanRecorded           EventType = "loan_recorded"
	EvtVenueRegistered        EventType = "venue_registered"
	EvtVenueClosed            EventType = "venue_closed"
	EvtDeviceRegistered       EventType = "device_registered"
	EvtDeviceBroken           EventType = "device_broken"
	EvtInstallDependencyAdded EventType = "install_dependency_added"
	// 排期类命令。
	EvtSlotPlanned     EventType = "slot_planned"
	EvtSlotConfirmed   EventType = "slot_confirmed"
	EvtSlotCancelled   EventType = "slot_cancelled"
	EvtSlotRescheduled EventType = "slot_rescheduled"
	// 运输/展出事实，只追加。
	EvtMovementAppended EventType = "movement_appended"
	// 风险与处置。
	EvtDamageReported       EventType = "damage_reported"
	EvtRightsWithdrawn      EventType = "rights_withdrawn"
	EvtDispositionGenerated EventType = "disposition_generated"
	EvtDispositionResolved  EventType = "disposition_resolved"
)

// Event 是追加日志中的标准信封（见 docs/domain.md）。
// occurred_at 保留来源原始发生时间，服务端绝不用到达时间覆盖；
// source_sequence 只在同一 source_id 内严格递增。
type Event struct {
	SchemaVersion  string          `json:"schema_version"`
	EventID        string          `json:"event_id"`
	SourceID       string          `json:"source_id,omitempty"`
	SourceSequence int64           `json:"source_sequence,omitempty"`
	OccurredAt     time.Time       `json:"occurred_at"`
	SubjectRef     string          `json:"subject_ref"`
	Type           EventType       `json:"type"`
	Payload        json.RawMessage `json:"payload"`
	PayloadDigest  string          `json:"payload_digest"`
	// IdempotencyKey 是院校批量重传等场景使用的自然键；
	// 同一键的重放返回首次结果，不产生第二条事件。
	IdempotencyKey string `json:"idempotency_key,omitempty"`
	// ManifestRef 标识批量清单，同一清单内组成件引用由稳定序号生成，
	// 重传时解析为相同标识，配合 event_id 实现批量幂等。
	ManifestRef string `json:"manifest_ref,omitempty"`
}
