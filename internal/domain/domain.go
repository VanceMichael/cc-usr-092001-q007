// Package domain 保存国际影像展陈协调器的稳定事实与纯业务规则。
//
// 约定：
//   - 外部主体（摄影师、院校、权利人、保险方等）只以脱敏引用编号出现；
//   - 所有时间均为带偏移量的时间，跨时区提交统一换算到展馆 IANA 时区落位；
//   - 材料本体不入库，只保存受控引用与 sha256 摘要；
//   - 时段一律按半开区间 [Start, End) 处理，相邻时段不算重叠。
package domain

import "time"

// 媒介受控值。
const (
	MediumPhoto   = "photo"
	MediumVideo   = "video"
	MediumBook    = "book"
	MediumExpFilm = "experimental_film"
	MediumAll     = "*" // 权利声明中的通配媒介
)

// 组成件形态：实体件参与占用冲突，数字件不参与。
const (
	FormPhysical = "physical"
	FormDigital  = "digital"
)

// 地域通配值。
const CountryAll = "ALL"

// 排期与处置状态。
const (
	SlotProposed   = "proposed"
	SlotConfirmed  = "confirmed"
	SlotCancelled  = "cancelled"
	SlotSuperseded = "superseded"

	PrereqOpen     = "open"
	PrereqResolved = "resolved"

	PlanOpen     = "open"
	PlanExecuted = "executed"

	ChangeOpen     = "open"
	ChangeResolved = "resolved"
)

// Work 是参展作品的稳定事实。组成件单独存放，此处只保留引用。
type Work struct {
	Ref        string   `json:"ref"`
	Title      string   `json:"title"`
	ArtistRef  string   `json:"artist_ref"`
	SchoolRef  string   `json:"school_ref"`
	Medium     string   `json:"medium"`
	Components []string `json:"components"`
}

// Component 是作品的组成件：实体相框图、手工书、装置或数字播放文件。
type Component struct {
	Ref             string `json:"ref"`
	WorkRef         string `json:"work_ref"`
	Form            string `json:"form"`
	Medium          string `json:"medium"`
	TransportClass  string `json:"transport_class"` // 受控运输条件引用
	PlaybackProfile string `json:"playback_profile,omitempty"`
	Sha256          string `json:"sha256,omitempty"`
	Notes           string `json:"notes,omitempty"`

	// 运行态事实。
	Arrived   bool       `json:"arrived"`
	ArrivedAt *time.Time `json:"arrived_at,omitempty"`
	Damaged   bool       `json:"damaged"`
	DamagedAt *time.Time `json:"damaged_at,omitempty"`
}

// Right 是权利人就作品公开展示作出的授权声明。
type Right struct {
	Ref          string   `json:"ref"`
	WorkRef      string   `json:"work_ref"`
	HolderRef    string   `json:"holder_ref"`
	Countries    []string `json:"countries"` // CountryAll 或国家/地区代码
	Media        []string `json:"media"`     // MediumAll 或受控媒介
	StatementSha string   `json:"statement_sha,omitempty"`

	NotBefore *time.Time `json:"not_before,omitempty"`
	NotAfter  *time.Time `json:"not_after,omitempty"`

	Revoked   bool       `json:"revoked"`
	RevokedAt *time.Time `json:"revoked_at,omitempty"`
	Reason    string     `json:"reason,omitempty"`
}

// AllowsAt 判断声明是否在给定时刻、地域与媒介上允许公开展示。
func (r Right) AllowsAt(country, medium string, at time.Time) bool {
	if r.Revoked {
		return false
	}
	if !contains(r.Countries, CountryAll) && !contains(r.Countries, country) {
		return false
	}
	if !contains(r.Media, MediumAll) && !contains(r.Media, medium) {
		return false
	}
	if r.NotBefore != nil && at.Before(*r.NotBefore) {
		return false
	}
	if r.NotAfter != nil && !at.Before(*r.NotAfter) {
		return false
	}
	return true
}

// CoversInterval 判断声明是否覆盖整个半开展出区间。
func (r Right) CoversInterval(country, medium string, start, end time.Time) bool {
	return r.AllowsAt(country, medium, start) && r.AllowsAt(country, medium, end.Add(-time.Nanosecond))
}

// Loan 是借展协议中与排期相关的事实。
type Loan struct {
	Ref        string    `json:"ref"`
	WorkRef    string    `json:"work_ref"`
	LenderRef  string    `json:"lender_ref"`
	Start      time.Time `json:"start"`
	End        time.Time `json:"end"` // 撤展时间：作品必须在此之前撤还
	Conditions []string  `json:"conditions,omitempty"`
}

// Venue 是古城中的展厅、街巷或院落。
type Venue struct {
	Ref           string   `json:"ref"`
	Name          string   `json:"name"`
	Country       string   `json:"country"`
	Timezone      string   `json:"timezone"` // IANA 名称，如 Asia/Shanghai
	AcceptedMedia []string `json:"accepted_media"`
	Requirements  []string `json:"requirements"` // 必须由分配设备满足的受控场地条件

	Closed    bool       `json:"closed"`
	ClosedAt  *time.Time `json:"closed_at,omitempty"`
	CloseFrom *time.Time `json:"close_from,omitempty"`
}

// Equipment 是可调配的播放或布展设备。
type Equipment struct {
	Ref              string   `json:"ref"`
	Kind             string   `json:"kind"`
	PlaybackProfiles []string `json:"playback_profiles,omitempty"`
	Provides         []string `json:"provides,omitempty"` // 可满足的场地条件
	TestedProfiles   []string `json:"tested_profiles,omitempty"`
	Notes            string   `json:"notes,omitempty"`
}

// Slot 是作品在某场地某时段的展陈排期。
// Start/End 的瞬时值用于冲突计算，Location 固定为展馆时区；
// LocalStart/LocalEnd 保留展馆当地挂钟表示，用于展示与跨时区落位核对。
type Slot struct {
	Ref        string    `json:"ref"`
	VenueRef   string    `json:"venue_ref"`
	WorkRef    string    `json:"work_ref"`
	Start      time.Time `json:"start"`
	End        time.Time `json:"end"`
	Timezone   string    `json:"timezone"`
	LocalStart string    `json:"local_start"`
	LocalEnd   string    `json:"local_end"`
	Status     string    `json:"status"`
	Components []string  `json:"components"`
	Equipment  []string  `json:"equipment"`

	CreatedAt    time.Time  `json:"created_at"`
	ConfirmedAt  *time.Time `json:"confirmed_at,omitempty"`
	SupersededBy string     `json:"superseded_by,omitempty"`
	Supersedes   string     `json:"supersedes,omitempty"`
	CancelReason string     `json:"cancel_reason,omitempty"`
}

// Prerequisite 是排期确认时生成的安装前置项。
type Prerequisite struct {
	Ref        string     `json:"ref"`
	SlotRef    string     `json:"slot_ref"`
	Kind       string     `json:"kind"`
	SubjectRef string     `json:"subject_ref"`
	RequiredBy time.Time  `json:"required_by"`
	Status     string     `json:"status"`
	Detail     string     `json:"detail,omitempty"`
	ResolvedBy string     `json:"resolved_by,omitempty"` // 台账事件 ID
	ResolvedAt *time.Time `json:"resolved_at,omitempty"`
	DependsOn  []string   `json:"depends_on,omitempty"`
}

// 前置项类型。
const (
	PrereqTransport = "transport_arrival"
	PrereqInsurance = "insurance"
	PrereqPermit    = "public_permit"
	PrereqTeam      = "install_team"
	PrereqPlayback  = "playback_check"
)

// PlanStep 是处置方案中的一个可审计步骤。
type PlanStep struct {
	Seq         int        `json:"seq"`
	Kind        string     `json:"kind"`
	Description string     `json:"description"`
	Deadline    *time.Time `json:"deadline,omitempty"`
	Status      string     `json:"status"`
}

// Alternative 是处置方案或场地视图中给出的可替代安排。
type Alternative struct {
	Kind    string `json:"kind"` // venue | work | equipment
	Ref     string `json:"ref"`
	Detail  string `json:"detail,omitempty"`
	SlotRef string `json:"slot_ref,omitempty"`
}

// Plan 是作品损坏、权利撤回或场地关闭触发的可审计处置方案。
type Plan struct {
	Ref           string        `json:"ref"`
	Trigger       string        `json:"trigger"` // damage | withdrawal | closure
	SubjectRef    string        `json:"subject_ref"`
	AffectedSlots []string      `json:"affected_slots"`
	Status        string        `json:"status"`
	CreatedAt     time.Time     `json:"created_at"`
	TriggerEvent  string        `json:"trigger_event"`
	Steps         []PlanStep    `json:"steps"`
	Alternatives  []Alternative `json:"alternatives,omitempty"`
	Evidence      []string      `json:"evidence,omitempty"`
	Notes         string        `json:"notes,omitempty"`
}

// 处置触发类型。
const (
	TriggerDamage     = "damage"
	TriggerWithdrawal = "withdrawal"
	TriggerClosure    = "closure"
)

// ChangeItem 是临时换场连带产生、需要外部方处理的事项。
type ChangeItem struct {
	Kind   string `json:"kind"` // insurance | install_team | public_permit
	Status string `json:"status"`
	Detail string `json:"detail"`
	Ref    string `json:"ref,omitempty"` // 原保单/班组/许可引用
}

// ChangeOrder 记录一次对已确认未来排期的换场或取消及其连带影响。
type ChangeOrder struct {
	Ref        string       `json:"ref"`
	SlotRef    string       `json:"slot_ref"`
	Action     string       `json:"action"` // reschedule | cancel
	Reason     string       `json:"reason"`
	NewSlotRef string       `json:"new_slot_ref,omitempty"`
	Items      []ChangeItem `json:"items"`
	CreatedAt  time.Time    `json:"created_at"`
}

// 换场连带事项类型。
const (
	ChangeInsurance = "insurance"
	ChangeTeam      = "install_team"
	ChangePermit    = "public_permit"
)

// Event 是只追加台账中的交换事件，信封与 contracts/event.example.json 对齐。
type Event struct {
	SchemaVersion  string    `json:"schema_version"`
	EventID        string    `json:"event_id"`
	Source         string    `json:"source"`
	SourceSequence int64     `json:"source_sequence"`
	OccurredAt     time.Time `json:"occurred_at"`
	ReceivedAt     time.Time `json:"received_at"`
	SubjectRef     string    `json:"subject_ref"`
	Type           string    `json:"type"`
	PayloadDigest  string    `json:"payload_digest"`
	Payload        []byte    `json:"payload,omitempty"`
}

func contains(list []string, v string) bool {
	for _, item := range list {
		if item == v {
			return true
		}
	}
	return false
}
