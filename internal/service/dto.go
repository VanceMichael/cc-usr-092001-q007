package service

import (
	"encoding/json"
	"time"

	"example.com/batch-092001-q007/internal/domain"
)

// ---------- 目录登记 ----------

type VenueInput struct {
	Ref           string   `json:"ref"`
	Name          string   `json:"name"`
	Country       string   `json:"country"`
	Timezone      string   `json:"timezone"`
	AcceptedMedia []string `json:"accepted_media"`
	Requirements  []string `json:"requirements"`
}

type EquipmentInput struct {
	Ref              string   `json:"ref"`
	Kind             string   `json:"kind"`
	PlaybackProfiles []string `json:"playback_profiles"`
	Provides         []string `json:"provides"`
	TestedProfiles   []string `json:"tested_profiles"`
	Notes            string   `json:"notes"`
}

// ---------- 院校批量清单 ----------

type ComponentInput struct {
	Ref             string `json:"ref"`
	Form            string `json:"form"`
	Medium          string `json:"medium"`
	TransportClass  string `json:"transport_class"`
	PlaybackProfile string `json:"playback_profile"`
	Sha256          string `json:"sha256"`
	Notes           string `json:"notes"`
}

type RightInput struct {
	Ref          string     `json:"ref"`
	HolderRef    string     `json:"holder_ref"`
	Countries    []string   `json:"countries"`
	Media        []string   `json:"media"`
	NotBefore    *time.Time `json:"not_before,omitempty"`
	NotAfter     *time.Time `json:"not_after,omitempty"`
	StatementSha string     `json:"statement_sha"`
}

type LoanInput struct {
	Ref        string    `json:"ref"`
	LenderRef  string    `json:"lender_ref"`
	Start      time.Time `json:"start"`
	End        time.Time `json:"end"`
	Conditions []string  `json:"conditions"`
}

type WorkInput struct {
	Ref        string           `json:"ref"`
	Title      string           `json:"title"`
	ArtistRef  string           `json:"artist_ref"`
	Medium     string           `json:"medium"`
	Components []ComponentInput `json:"components"`
	Rights     []RightInput     `json:"rights"`
	Loan       *LoanInput       `json:"loan,omitempty"`
}

type BatchRequest struct {
	BatchRef   string      `json:"batch_ref"`
	SchoolRef  string      `json:"school_ref"`
	OccurredAt time.Time   `json:"occurred_at"` // 院校提交时刻，保留其原始偏移量
	Works      []WorkInput `json:"works"`
}

type BatchResponse struct {
	Source        string    `json:"source"`
	BatchRef      string    `json:"batch_ref"`
	Digest        string    `json:"digest"`
	Replayed      bool      `json:"replayed"`
	DigestChanged bool      `json:"digest_changed,omitempty"` // 重传内容与首次不同：仍幂等，仅提示
	AcceptedWorks []string  `json:"accepted_works"`
	EventIDs      []string  `json:"event_ids"`
	ReceivedAt    time.Time `json:"received_at"`
}

// ---------- 通用事件 ----------

type EventRequest struct {
	SchemaVersion  string    `json:"schema_version,omitempty"`
	EventID        string    `json:"event_id"`
	Source         string    `json:"source"`
	SourceSequence int64     `json:"source_sequence"`
	OccurredAt     time.Time `json:"occurred_at"`
	SubjectRef     string    `json:"subject_ref"`
	Type           string    `json:"type"`
	// PayloadDigest 可选；提供时必须与服务端对 payload 计算的 sha256 一致，否则拒绝。
	PayloadDigest string          `json:"payload_digest,omitempty"`
	Payload       json.RawMessage `json:"payload"`
}

// ---------- 排期 ----------

type SlotRequest struct {
	VenueRef   string    `json:"venue_ref"`
	WorkRef    string    `json:"work_ref"`
	Start      time.Time `json:"start"`
	End        time.Time `json:"end"`
	Components []string  `json:"components"` // 空表示该作品全部实体组成件
	Equipment  []string  `json:"equipment"`
}

type SlotView struct {
	Slot      domain.Slot              `json:"slot"`
	VenueName string                   `json:"venue_name"`
	WorkTitle string                   `json:"work_title"`
	Issues    []domain.ValidationIssue `json:"issues,omitempty"`
	Prereqs   []domain.Prerequisite    `json:"prereqs,omitempty"`
	Occupancy []OccupancyNote          `json:"occupancy,omitempty"`
}

type OccupancyNote struct {
	Kind       string `json:"kind"` // component | equipment
	SubjectRef string `json:"subject_ref"`
	SlotRef    string `json:"slot_ref"`
	Message    string `json:"message"`
}

type RescheduleRequest struct {
	VenueRef  string    `json:"venue_ref,omitempty"` // 空表示沿用原场地
	Start     time.Time `json:"start"`
	End       time.Time `json:"end"`
	Equipment []string  `json:"equipment,omitempty"` // 空表示沿用原设备
	Reason    string    `json:"reason"`
}

type CancelRequest struct {
	Reason string `json:"reason"`
}

// ---------- 处置方案 ----------

type StepExecution struct {
	Seq    int    `json:"seq"`
	Status string `json:"status"`
	Note   string `json:"note,omitempty"`
}

type ExecutePlanRequest struct {
	Steps []StepExecution `json:"steps"`
}

// ---------- 场地时段视图 ----------

type Risk struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	Subject string `json:"subject,omitempty"`
}

type SlotBrief struct {
	Ref           string                `json:"ref"`
	WorkRef       string                `json:"work_ref"`
	WorkTitle     string                `json:"work_title"`
	Start         time.Time             `json:"start"`
	End           time.Time             `json:"end"`
	LocalStart    string                `json:"local_start"`
	LocalEnd      string                `json:"local_end"`
	Status        string                `json:"status"`
	Prerequisites []domain.Prerequisite `json:"prerequisites"`
	Risks         []Risk                `json:"risks"`
	Alternatives  []domain.Alternative  `json:"alternatives"`
}

type VenueWindowView struct {
	Venue       domain.Venue `json:"venue"`
	WindowStart time.Time    `json:"window_start"`
	WindowEnd   time.Time    `json:"window_end"`
	LocalStart  string       `json:"local_start"`
	LocalEnd    string       `json:"local_end"`
	Slots       []SlotBrief  `json:"slots"`
	Risks       []Risk       `json:"risks"` // 场地级风险（如关闭）
}
