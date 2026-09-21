// Package domain 保存国际影像展陈协调器的稳定事实模型。
//
// 外部主体一律使用不含真实身份信息的稳定引用编号（ART-/PIECE-/RGR-/
// LOAN-/VEN-/DEV-/SLOT-/EVENT- 等）；时间在边界上为带偏移量的 ISO 8601
// 字符串，入库后统一为 time.Time；材料本体不入库，只保存受控引用与
// sha256 摘要。
package domain

import "time"

// Media 标识组成件的受控媒介类型。
type Media string

const (
	MediaPhoto    Media = "photo"    // 照片
	MediaVideo    Media = "video"    // 短视频
	MediaBook     Media = "book"     // 手工书
	MediaMoving   Media = "moving"   // 实验影像
	MediaLightbox Media = "lightbox" // 灯箱等实体装裱件
)

// TransportCondition 描述组成件运输要求，供安装依赖与处置方案引用。
type TransportCondition string

const (
	TransportNormal    TransportCondition = "normal"
	TransportFragile   TransportCondition = "fragile"   // 易碎
	TransportClimate   TransportCondition = "climate"   // 恒温恒湿
	TransportDark      TransportCondition = "dark"      // 避光
	TransportHandCarry TransportCondition = "handcarry" // 仅专人手提
)

// Artwork 是作品（逻辑作品）与其组成件清单。
type Artwork struct {
	Ref       string    `json:"ref"`
	SchoolRef string    `json:"school_ref,omitempty"` // 提交院校的稳定引用
	Title     string    `json:"title,omitempty"`      // 脱敏标题，可为空
	Medias    []Media   `json:"medias,omitempty"`     // 作品涵盖的媒介集合
	Pieces    []*Piece  `json:"pieces,omitempty"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// Piece 是可独立运输、占用与安装的实体组成件。
type Piece struct {
	Ref                 string             `json:"ref"`
	ArtworkRef          string             `json:"artwork_ref"`
	Media               Media              `json:"media"`
	MaterialRef         string             `json:"material_ref"` // 受控材料引用
	Digest              string             `json:"digest"`       // sha256:<hex>
	Transport           TransportCondition `json:"transport"`
	RequiredDeviceKinds []string           `json:"required_device_kinds,omitempty"` // 播放设备种类
}

// RightsGrant 是权利人就公开放映/展出作出的授权声明。
type RightsGrant struct {
	Ref        string `json:"ref"`
	ArtworkRef string `json:"artwork_ref"`
	// AllowedCountries 为空表示不限地域；否则仅允许在所列国家代码内公开。
	AllowedCountries []string `json:"allowed_countries,omitempty"`
	// AllowedMedia 为空表示不限媒介；否则作品仅允许以所列媒介公开。
	AllowedMedia []Media `json:"allowed_media,omitempty"`
	// ValidFrom/ValidTo 以展馆当地时区解释，ValidTo 为闭区间端点。
	ValidFrom   LocalDate `json:"valid_from"`
	ValidTo     LocalDate `json:"valid_to"`
	Withdrawn   bool      `json:"withdrawn"`
	WithdrawnAt time.Time `json:"withdrawn_at,omitempty"`
	RecordedAt  time.Time `json:"recorded_at"`
}

// LoanAgreement 是借展协议：撤展时间与运输/展出约束。
type LoanAgreement struct {
	Ref        string `json:"ref"`
	ArtworkRef string `json:"artwork_ref"`
	// MustReturnBy 为借展撤展截止时刻（带偏移量），场次必须在此之前闭场。
	MustReturnBy time.Time `json:"must_return_by"`
	Notes        string    `json:"notes,omitempty"`
	RecordedAt   time.Time `json:"recorded_at"`
}

// Venue 是展厅、街巷或院落。CountryCode 决定版权地域判定。
type Venue struct {
	Ref         string `json:"ref"`
	Name        string `json:"name,omitempty"`
	CountryCode string `json:"country_code"`
	// TZ 是展馆当地时区的 IANA 名称（如 Asia/Shanghai）。
	TZ        string    `json:"tz"`
	Medias    []Media   `json:"medias,omitempty"` // 场地可承载的媒介
	Closed    bool      `json:"closed"`
	ClosedAt  time.Time `json:"closed_at,omitempty"`
	UpdatedAt time.Time `json:"updated_at"`
}

// Device 是可被场次独占占用的播放/展示设备。
type Device struct {
	Ref       string    `json:"ref"`
	VenueRef  string    `json:"venue_ref"`
	Kind      string    `json:"kind"` // projector / screen / player ...
	Broken    bool      `json:"broken"`
	BrokenAt  time.Time `json:"broken_at,omitempty"`
	UpdatedAt time.Time `json:"updated_at"`
}

// InstallDependency 是安装前置项：目标场次须等待前置场次完成对应阶段。
type InstallDependency struct {
	Ref         string `json:"ref"`
	ArtworkRef  string `json:"artwork_ref"`
	FromSlotRef string `json:"from_slot_ref"`
	ToSlotRef   string `json:"to_slot_ref"`
	// Requires 为前置场次必须达到的阶段：delivered 或 installed。
	Requires string `json:"requires"`
}

// SlotStatus 是场次确认状态。
type SlotStatus string

const (
	SlotPlanned   SlotStatus = "planned"
	SlotConfirmed SlotStatus = "confirmed"
	SlotCancelled SlotStatus = "cancelled"
)

// Slot 是作品在某场地某时段的一次展陈安排。
type Slot struct {
	Ref        string `json:"ref"`
	ArtworkRef string `json:"artwork_ref"`
	VenueRef   string `json:"venue_ref"`
	// Start/End 为绝对时刻；半开区间 [Start, End)。
	Start  time.Time  `json:"start"`
	End    time.Time  `json:"end"`
	Status SlotStatus `json:"status"`
	// Devices 为该场次独占占用的设备引用。
	Devices   []string  `json:"devices,omitempty"`
	Version   int       `json:"version"`
	UpdatedAt time.Time `json:"updated_at"`
}

// HasDevice 报告场次是否占用指定设备。
func (s *Slot) HasDevice(deviceRef string) bool {
	for _, d := range s.Devices {
		if d == deviceRef {
			return true
		}
	}
	return false
}

// MovementKind 标识运输/展出过程记录的种类。
type MovementKind string

const (
	MovementShipped   MovementKind = "shipped"
	MovementDelivered MovementKind = "delivered"
	MovementInstalled MovementKind = "installed"
	MovementOpened    MovementKind = "opened"
	MovementClosed    MovementKind = "closed"
	MovementReturned  MovementKind = "returned"
)

// Movement 是已发生且不可重排覆盖的运输/展出记录。
type Movement struct {
	Ref        string       `json:"ref"`
	ArtworkRef string       `json:"artwork_ref"`
	PieceRef   string       `json:"piece_ref,omitempty"` // 为空表示整批
	SlotRef    string       `json:"slot_ref,omitempty"`
	Kind       MovementKind `json:"kind"`
	OccurredAt time.Time    `json:"occurred_at"`
	Note       string       `json:"note,omitempty"`
}

// IncidentKind 标识触发处置方案的事件种类。
type IncidentKind string

const (
	IncidentDamage        IncidentKind = "damage"         // 作品损坏
	IncidentRightsRevoked IncidentKind = "rights_revoked" // 权利撤回
	IncidentVenueClosed   IncidentKind = "venue_closed"   // 场地关闭
	IncidentDeviceBroken  IncidentKind = "device_broken"  // 设备故障
)

// ActionKind 标识处置动作种类。
type ActionKind string

const (
	ActionHoldInspection  ActionKind = "hold_for_inspection" // 暂停以待检查
	ActionPullFromDisplay ActionKind = "pull_from_display"   // 撤出公开展示
	ActionRelocate        ActionKind = "relocate"            // 迁移至替代场次
	ActionSwapDevice      ActionKind = "swap_device"         // 更换设备
	ActionSuspend         ActionKind = "suspend_install"     // 暂停安装
)

// Action 是处置方案中的单个动作。
type Action struct {
	Kind      ActionKind `json:"kind"`
	SlotRef   string     `json:"slot_ref,omitempty"`
	Detail    string     `json:"detail,omitempty"`
	Alternate *SlotSpec  `json:"alternate,omitempty"` // 可替代安排
}

// SlotSpec 是尚未落编号的替代场次规格（用于处置方案与冲突建议）。
type SlotSpec struct {
	VenueRef string    `json:"venue_ref"`
	Start    time.Time `json:"start"`
	End      time.Time `json:"end"`
	Devices  []string  `json:"devices,omitempty"`
}

// Disposition 是由损坏/撤回/闭厅等事件生成的可审计处置方案。
type Disposition struct {
	Ref          string       `json:"ref"`
	IncidentKind IncidentKind `json:"incident_kind"`
	SubjectRef   string       `json:"subject_ref"` // 作品/场地/设备引用
	Detail       string       `json:"detail,omitempty"`
	Actions      []Action     `json:"actions"`
	OpenRisk     bool         `json:"open_risk"` // 是否仍有未解决风险
	CreatedAt    time.Time    `json:"created_at"`
	ResolvedAt   time.Time    `json:"resolved_at,omitempty"`
	Resolution   string       `json:"resolution,omitempty"`
}

// TrackEntry 是作品完整展陈轨迹中的一行。
type TrackEntry struct {
	At      time.Time `json:"at"`
	Kind    string    `json:"kind"` // movement / slot / disposition
	Ref     string    `json:"ref"`
	Summary string    `json:"summary"`
	SlotRef string    `json:"slot_ref,omitempty"`
}

// VenueBrief 是“某场地某时段”的协调简报。
type VenueBrief struct {
	VenueRef    string          `json:"venue_ref"`
	WindowStart time.Time       `json:"window_start"`
	WindowEnd   time.Time       `json:"window_end"`
	Slots       []SlotBrief     `json:"slots"`
	Prereqs     []PrereqView    `json:"prerequisites"`
	Risks       []RiskView      `json:"open_risks"`
	Alternates  []AlternateView `json:"alternatives"`
}

// SlotBrief 是简报中的场次行。
type SlotBrief struct {
	SlotRef    string     `json:"slot_ref"`
	ArtworkRef string     `json:"artwork_ref"`
	Start      time.Time  `json:"start"`
	End        time.Time  `json:"end"`
	Status     SlotStatus `json:"status"`
}

// PrereqView 是安装前置项视图。
type PrereqView struct {
	ToSlotRef      string `json:"to_slot_ref"`
	FromSlotRef    string `json:"from_slot_ref"`
	Requires       string `json:"requires"`
	Satisfied      bool   `json:"satisfied"`
	BlockingReason string `json:"blocking_reason,omitempty"`
}

// RiskView 是未解决风险视图。
type RiskView struct {
	Severity string `json:"severity"` // error / warning
	Subject  string `json:"subject"`
	SlotRef  string `json:"slot_ref,omitempty"`
	Message  string `json:"message"`
}

// AlternateView 是可替代方案视图。
type AlternateView struct {
	ForSlotRef string   `json:"for_slot_ref"`
	Reason     string   `json:"reason"`
	Spec       SlotSpec `json:"spec"`
}
