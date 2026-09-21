package service

import (
	"time"

	"example.com/batch-092001-q007/internal/domain"
)

// 下列结构是事件 payload 的序列化形状，与领域对象解耦，
// 便于在不改动日志格式的前提下演进接口字段。

type slotPayload struct {
	Ref        string    `json:"ref"`
	ArtworkRef string    `json:"artwork_ref"`
	VenueRef   string    `json:"venue_ref"`
	Start      time.Time `json:"start"`
	End        time.Time `json:"end"`
	Devices    []string  `json:"devices"`
}

type refPayload struct {
	Ref string `json:"ref"`
}

type damagePayload struct {
	PieceRef   string    `json:"piece_ref"`
	ArtworkRef string    `json:"artwork_ref"`
	Detail     string    `json:"detail"`
	OccurredAt time.Time `json:"occurred_at"`
}

type withdrawalPayload struct {
	GrantRef   string    `json:"grant_ref"`
	ArtworkRef string    `json:"artwork_ref"`
	Detail     string    `json:"detail"`
	OccurredAt time.Time `json:"occurred_at"`
}

type closurePayload struct {
	VenueRef   string    `json:"venue_ref"`
	Detail     string    `json:"detail"`
	OccurredAt time.Time `json:"occurred_at"`
}

type breakagePayload struct {
	DeviceRef  string    `json:"device_ref"`
	Detail     string    `json:"detail"`
	OccurredAt time.Time `json:"occurred_at"`
}

// ---- 请求体 ----

// ArtworkRequest 登记一件作品。
type ArtworkRequest struct {
	EventID   string         `json:"event_id"`
	Ref       string         `json:"ref"`
	SchoolRef string         `json:"school_ref"`
	Title     string         `json:"title"`
	Medias    []domain.Media `json:"medias"`
	// SourceID/SourceSequence 用于外部来源有序提交。
	SourceID       string `json:"source_id"`
	SourceSequence int64  `json:"source_sequence"`
}

// PieceRequest 登记作品的一个组成件。
type PieceRequest struct {
	EventID             string                    `json:"event_id"`
	Ref                 string                    `json:"ref"`
	ArtworkRef          string                    `json:"artwork_ref"`
	Media               domain.Media              `json:"media"`
	MaterialRef         string                    `json:"material_ref"`
	Digest              string                    `json:"digest"`
	Transport           domain.TransportCondition `json:"transport"`
	RequiredDeviceKinds []string                  `json:"required_device_kinds"`
	SourceID            string                    `json:"source_id"`
	SourceSequence      int64                     `json:"source_sequence"`
	IdempotencyKey      string                    `json:"idempotency_key"`
	ManifestRef         string                    `json:"manifest_ref"`
}

// RightsRequest 记录权利人声明。
type RightsRequest struct {
	EventID          string         `json:"event_id"`
	Ref              string         `json:"ref"`
	ArtworkRef       string         `json:"artwork_ref"`
	AllowedCountries []string       `json:"allowed_countries"`
	AllowedMedia     []domain.Media `json:"allowed_media"`
	ValidFrom        string         `json:"valid_from"` // YYYY-MM-DD
	ValidTo          string         `json:"valid_to"`
	SourceID         string         `json:"source_id"`
	SourceSequence   int64          `json:"source_sequence"`
}

// LoanRequest 记录借展协议。
type LoanRequest struct {
	EventID        string    `json:"event_id"`
	Ref            string    `json:"ref"`
	ArtworkRef     string    `json:"artwork_ref"`
	MustReturnBy   time.Time `json:"must_return_by"`
	Notes          string    `json:"notes"`
	SourceID       string    `json:"source_id"`
	SourceSequence int64     `json:"source_sequence"`
}

// VenueRequest 登记场地。
type VenueRequest struct {
	EventID        string         `json:"event_id"`
	Ref            string         `json:"ref"`
	Name           string         `json:"name"`
	CountryCode    string         `json:"country_code"`
	TZ             string         `json:"tz"`
	Medias         []domain.Media `json:"medias"`
	SourceID       string         `json:"source_id"`
	SourceSequence int64          `json:"source_sequence"`
}

// DeviceRequest 登记可占用设备。
type DeviceRequest struct {
	EventID        string `json:"event_id"`
	Ref            string `json:"ref"`
	VenueRef       string `json:"venue_ref"`
	Kind           string `json:"kind"`
	SourceID       string `json:"source_id"`
	SourceSequence int64  `json:"source_sequence"`
}

// DependencyRequest 登记安装前置依赖。
type DependencyRequest struct {
	EventID        string `json:"event_id"`
	Ref            string `json:"ref"`
	ArtworkRef     string `json:"artwork_ref"`
	FromSlotRef    string `json:"from_slot_ref"`
	ToSlotRef      string `json:"to_slot_ref"`
	Requires       string `json:"requires"` // delivered | installed
	SourceID       string `json:"source_id"`
	SourceSequence int64  `json:"source_sequence"`
}
