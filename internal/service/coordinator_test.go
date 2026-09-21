package service

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"example.com/batch-092001-q007/internal/clock"
	"example.com/batch-092001-q007/internal/domain"
)

// 固定“现在”：所有未来场次都安排在 2026-09-25 前后。
var testNow = time.Date(2026, 9, 21, 4, 0, 0, 0, time.UTC)

func shanghaiTime(y int, mo time.Month, d, h, min int) time.Time {
	loc, _ := time.LoadLocation("Asia/Shanghai")
	return time.Date(y, mo, d, h, min, 0, 0, loc)
}

func berlinTime(y int, mo time.Month, d, h, min int) time.Time {
	loc, _ := time.LoadLocation("Europe/Berlin")
	return time.Date(y, mo, d, h, min, 0, 0, loc)
}

func newCoord(t *testing.T, t0 time.Time) *Coordinator {
	t.Helper()
	c, err := New("", clock.Fixed{T: t0})
	if err != nil {
		t.Fatalf("new coordinator: %v", err)
	}
	t.Cleanup(func() { _ = c.Close() })
	return c
}

func must2[T any](v T, err error) T {
	if err != nil {
		panic(err)
	}
	return v
}

func isConflict(err error, frag string) bool {
	if err == nil {
		return false
	}
	c, ok := err.(*ConflictError)
	if !ok {
		return false
	}
	if frag == "" {
		return true
	}
	return strings.Contains(c.Error(), frag)
}

// world 是测试用基础数据：两件照片作品、三个国家的场地、两台投影仪。
type world struct {
	c *Coordinator
}

func setupWorld(t *testing.T, t0 time.Time) *world {
	t.Helper()
	c := newCoord(t, t0)
	w := &world{c: c}

	must2(c.RegisterArtwork(ArtworkRequest{EventID: "EVT-A1", Ref: "ART-1", SchoolRef: "SCH-1", Medias: []domain.Media{domain.MediaPhoto}}))
	must2(c.RegisterArtwork(ArtworkRequest{EventID: "EVT-A2", Ref: "ART-2", SchoolRef: "SCH-2", Medias: []domain.Media{domain.MediaPhoto}}))
	must2(c.RegisterPiece(PieceRequest{EventID: "EVT-P1A", Ref: "PIECE-1A", ArtworkRef: "ART-1",
		Media: domain.MediaPhoto, MaterialRef: "MAT/photo/archival", Digest: "sha256:aaa", Transport: domain.TransportClimate,
		RequiredDeviceKinds: []string{"projector"}}))
	must2(c.RegisterPiece(PieceRequest{EventID: "EVT-P2A", Ref: "PIECE-2A", ArtworkRef: "ART-2",
		Media: domain.MediaPhoto, Digest: "sha256:bbb"}))

	must2(c.RegisterVenue(VenueRequest{EventID: "EVT-V1", Ref: "VEN-PY", Name: "平遥古城一展厅",
		CountryCode: "CN", TZ: "Asia/Shanghai"}))
	must2(c.RegisterVenue(VenueRequest{EventID: "EVT-V2", Ref: "VEN-BER",
		CountryCode: "DE", TZ: "Europe/Berlin"}))
	must2(c.RegisterVenue(VenueRequest{EventID: "EVT-V3", Ref: "VEN-LA",
		CountryCode: "US", TZ: "America/Los_Angeles"}))

	must2(c.RecordRights(RightsRequest{EventID: "EVT-G1", Ref: "RGR-1", ArtworkRef: "ART-1",
		AllowedCountries: []string{"CN"}, AllowedMedia: []domain.Media{domain.MediaPhoto},
		ValidFrom: "2026-09-20", ValidTo: "2026-09-30"}))
	must2(c.RecordRights(RightsRequest{EventID: "EVT-G2", Ref: "RGR-2", ArtworkRef: "ART-1",
		AllowedCountries: []string{"US"}, AllowedMedia: []domain.Media{domain.MediaPhoto},
		ValidFrom: "2026-09-20", ValidTo: "2026-09-30"}))
	must2(c.RecordRights(RightsRequest{EventID: "EVT-G3", Ref: "RGR-3", ArtworkRef: "ART-2",
		AllowedMedia: []domain.Media{domain.MediaPhoto},
		ValidFrom:    "2026-09-20", ValidTo: "2026-09-30"}))

	must2(c.RecordLoan(LoanRequest{EventID: "EVT-L1", Ref: "LOAN-1", ArtworkRef: "ART-1",
		MustReturnBy: time.Date(2026, 10, 5, 0, 0, 0, 0, time.UTC)}))

	must2(c.RegisterDevice(DeviceRequest{EventID: "EVT-D1", Ref: "DEV-P1", VenueRef: "VEN-PY", Kind: "projector"}))
	must2(c.RegisterDevice(DeviceRequest{EventID: "EVT-D2", Ref: "DEV-P2", VenueRef: "VEN-PY", Kind: "projector"}))
	must2(c.RegisterDevice(DeviceRequest{EventID: "EVT-D3", Ref: "DEV-B1", VenueRef: "VEN-BER", Kind: "projector"}))
	must2(c.RegisterDevice(DeviceRequest{EventID: "EVT-D4", Ref: "DEV-L1", VenueRef: "VEN-LA", Kind: "projector"}))
	return w
}

func planSlotReq(ref, art, venue string, start, end time.Time, devs ...string) SlotRequest {
	return SlotRequest{EventID: "EVT-SLOT-" + ref, Ref: ref, ArtworkRef: art, VenueRef: venue,
		Start: start, End: end, Devices: devs}
}

func TestSlotDoubleBookingArtwork(t *testing.T) {
	w := setupWorld(t, testNow)
	c := w.c
	// 同一实体作品不能在重叠时段同时出现在平遥与柏林。
	must2(c.PlanSlot(planSlotReq("S1", "ART-2", "VEN-PY",
		shanghaiTime(2026, 9, 25, 10, 0), shanghaiTime(2026, 9, 25, 12, 0))))
	err := planErr(c, planSlotReq("S2", "ART-2", "VEN-BER",
		shanghaiTime(2026, 9, 25, 11, 0), shanghaiTime(2026, 9, 25, 13, 0)))
	if !isConflict(err, "已被占用") {
		t.Fatalf("expected artwork double-booking conflict, got %v", err)
	}
	// 背靠背（半开区间首尾相接）允许。
	must2(c.PlanSlot(planSlotReq("S3", "ART-2", "VEN-BER",
		shanghaiTime(2026, 9, 25, 12, 0), shanghaiTime(2026, 9, 25, 14, 0))))
}

func TestSlotDoubleBookingDevice(t *testing.T) {
	w := setupWorld(t, testNow)
	c := w.c
	must2(c.PlanSlot(planSlotReq("S1", "ART-1", "VEN-PY",
		shanghaiTime(2026, 9, 25, 10, 0), shanghaiTime(2026, 9, 25, 12, 0), "DEV-P1")))
	// ART-2 虽无占用，但同一投影仪 DEV-P1 不能重复占用。
	err := planErr(c, planSlotReq("S2", "ART-2", "VEN-PY",
		shanghaiTime(2026, 9, 25, 11, 0), shanghaiTime(2026, 9, 25, 13, 0), "DEV-P1"))
	if !isConflict(err, "DEV-P1") {
		t.Fatalf("expected device conflict, got %v", err)
	}
	// 另一台投影仪在同一时段可用。
	must2(c.PlanSlot(planSlotReq("S3", "ART-2", "VEN-PY",
		shanghaiTime(2026, 9, 25, 11, 0), shanghaiTime(2026, 9, 25, 13, 0), "DEV-P2")))
}

func TestRightsCountryRestriction(t *testing.T) {
	w := setupWorld(t, testNow)
	c := w.c
	// RGR-1 仅允许 CN：柏林（DE）场次必须拒绝。
	err := planErr(c, planSlotReq("S1", "ART-1", "VEN-BER",
		berlinTime(2026, 9, 25, 10, 0), berlinTime(2026, 9, 25, 12, 0)))
	if !isConflict(err, "无有效授权") {
		t.Fatalf("expected country rights conflict, got %v", err)
	}
	// 平遥（CN）同窗口允许。
	must2(c.PlanSlot(planSlotReq("S2", "ART-1", "VEN-PY",
		shanghaiTime(2026, 9, 25, 10, 0), shanghaiTime(2026, 9, 25, 12, 0), "DEV-P1")))
}

func TestRightsMediaRestriction(t *testing.T) {
	w := setupWorld(t, testNow)
	c := w.c
	must2(c.RegisterArtwork(ArtworkRequest{EventID: "EVT-A3", Ref: "ART-3"}))
	must2(c.RegisterPiece(PieceRequest{EventID: "EVT-P3V", Ref: "PIECE-3V", ArtworkRef: "ART-3",
		Media: domain.MediaVideo}))
	must2(c.RecordRights(RightsRequest{EventID: "EVT-G4", Ref: "RGR-4", ArtworkRef: "ART-3",
		AllowedMedia: []domain.Media{domain.MediaPhoto},
		ValidFrom:    "2026-09-20", ValidTo: "2026-09-30"}))
	err := planErr(c, planSlotReq("S1", "ART-3", "VEN-PY",
		shanghaiTime(2026, 9, 25, 10, 0), shanghaiTime(2026, 9, 25, 12, 0)))
	if !isConflict(err, "video") {
		t.Fatalf("expected media rights conflict, got %v", err)
	}
}

func TestRightsDateWindowUsesVenueLocalDate(t *testing.T) {
	w := setupWorld(t, testNow)
	c := w.c
	// 独立作品：授权仅到 2026-09-25（按展馆当地日历），无其他宽授权。
	must2(c.RegisterArtwork(ArtworkRequest{EventID: "EVT-A4", Ref: "ART-4", Medias: []domain.Media{domain.MediaPhoto}}))
	must2(c.RecordRights(RightsRequest{EventID: "EVT-G9", Ref: "RGR-9", ArtworkRef: "ART-4",
		ValidFrom: "2026-09-20", ValidTo: "2026-09-25"}))

	// UTC 9/25 16:30 —— 提交方按 UTC 偏移量提交；在上海已是 9/26 00:30，
	// 必须按平遥当地日期判定为超期，而不是按 UTC 日期放行。
	utcEvening := time.Date(2026, 9, 25, 16, 30, 0, 0, time.UTC)
	err := planErr(c, planSlotReq("S1", "ART-4", "VEN-PY",
		utcEvening, utcEvening.Add(2*time.Hour)))
	if !isConflict(err, "2026-09-26") {
		t.Fatalf("expected local-date rights conflict mentioning 2026-09-26, got %v", err)
	}
	// 同一物理瞬间在柏林仍是 9/25 傍晚，应当落位成功。
	must2(c.PlanSlot(planSlotReq("S2", "ART-4", "VEN-BER",
		utcEvening, utcEvening.Add(2*time.Hour))))
}

func TestLoanReturnDeadline(t *testing.T) {
	w := setupWorld(t, testNow)
	c := w.c
	err := planErr(c, planSlotReq("S1", "ART-1", "VEN-PY",
		shanghaiTime(2026, 10, 5, 10, 0), shanghaiTime(2026, 10, 5, 12, 0)))
	if !isConflict(err, "撤展截止") {
		t.Fatalf("expected loan deadline conflict, got %v", err)
	}
}

func TestInstallDependencyBlocksConfirmation(t *testing.T) {
	w := setupWorld(t, testNow)
	c := w.c
	// S-A 先展、S-B 背靠背接力，S-B 安装要求 S-A 的组成件已交付。
	must2(c.PlanSlot(planSlotReq("SA", "ART-2", "VEN-PY",
		shanghaiTime(2026, 9, 25, 8, 0), shanghaiTime(2026, 9, 25, 10, 0))))
	must2(c.PlanSlot(planSlotReq("SB", "ART-2", "VEN-BER",
		shanghaiTime(2026, 9, 25, 12, 0), shanghaiTime(2026, 9, 25, 14, 0))))
	must2(c.AddDependency(DependencyRequest{EventID: "EVT-DEP1", Ref: "DEP-1",
		ArtworkRef: "ART-2", FromSlotRef: "SA", ToSlotRef: "SB", Requires: "delivered"}))

	err := confirmErr(c, ConfirmRequest{EventID: "EVT-CF-B1", Ref: "SB"})
	if !isConflict(err, "delivered") {
		t.Fatalf("expected unsatisfied prerequisite, got %v", err)
	}
	must2(c.AppendMovement(MovementRequest{EventID: "EVT-M1", Ref: "MV-1", ArtworkRef: "ART-2",
		SlotRef: "SA", Kind: domain.MovementDelivered,
		OccurredAt: ptrTime(shanghaiTime(2026, 9, 25, 7, 0))}))
	must2(c.ConfirmSlot(ConfirmRequest{EventID: "EVT-CF-B2", Ref: "SB"}))
}

func TestIdempotentReplayAndSequence(t *testing.T) {
	c := newCoord(t, testNow)
	// 同一 event_id 重放返回幂等，不重复登记。
	req := ArtworkRequest{EventID: "EVT-DUP", Ref: "ART-X", SourceID: "school-1", SourceSequence: 1}
	r1 := must2(c.RegisterArtwork(req))
	r2 := must2(c.RegisterArtwork(req))
	if r1.Idempotent || !r2.Idempotent {
		t.Fatalf("expected first=false second=true, got %v %v", r1.Idempotent, r2.Idempotent)
	}

	// 序号跳跃拒绝。
	jump := ArtworkRequest{EventID: "EVT-JUMP", Ref: "ART-Y", SourceID: "school-1", SourceSequence: 3}
	if err := artErr(c, jump); !isConflict(err, "跳跃") {
		t.Fatalf("expected sequence gap conflict, got %v", err)
	}
	// 序号 2 正常。
	must2(c.RegisterArtwork(ArtworkRequest{EventID: "EVT-SEQ2", Ref: "ART-Y", SourceID: "school-1", SourceSequence: 2}))
	// 相同 event_id+序号重放幂等。
	must2(c.RegisterArtwork(ArtworkRequest{EventID: "EVT-SEQ2", Ref: "ART-Y", SourceID: "school-1", SourceSequence: 2}))
	// 相同序号不同事件拒绝。
	err := artErr(c, ArtworkRequest{EventID: "EVT-SEQ2B", Ref: "ART-Z", SourceID: "school-1", SourceSequence: 2})
	if !isConflict(err, "已被另一事件占用") {
		t.Fatalf("expected sequence-taken conflict, got %v", err)
	}
}

func TestManifestRetransmitIdempotent(t *testing.T) {
	c := newCoord(t, testNow)
	must2(c.RegisterArtwork(ArtworkRequest{EventID: "EVT-A9", Ref: "ART-9", SchoolRef: "SCH-9"}))
	first := PieceRequest{EventID: "EVT-PIECE-1", Ref: "PIECE-9-1", ArtworkRef: "ART-9",
		Media: domain.MediaBook, ManifestRef: "MANIFEST-9-20260921"}
	// 院校网络重试：重传携带全新 event_id，但同一清单+组成件自然键。
	retry := first
	retry.EventID = "EVT-PIECE-1-RETRY"
	must2(c.RegisterPiece(first))
	r2 := must2(c.RegisterPiece(retry))
	if !r2.Idempotent {
		t.Fatalf("retransmit should be idempotent, got %+v", r2)
	}
	if r2.Event.EventID != "EVT-PIECE-1" {
		t.Fatalf("replay should point to original event, got %s", r2.Event.EventID)
	}
	if len(c.GetArtwork("ART-9").Pieces) != 1 {
		t.Fatalf("expected exactly one piece after retransmit")
	}
}

func TestHistoricalMovementCannotBeRescheduled(t *testing.T) {
	w := setupWorld(t, testNow)
	c := w.c
	must2(c.PlanSlot(planSlotReq("S1", "ART-2", "VEN-PY",
		shanghaiTime(2026, 9, 25, 10, 0), shanghaiTime(2026, 9, 25, 12, 0))))
	must2(c.AppendMovement(MovementRequest{EventID: "EVT-M1", Ref: "MV-1", ArtworkRef: "ART-2",
		SlotRef: "S1", Kind: domain.MovementShipped,
		OccurredAt: ptrTime(shanghaiTime(2026, 9, 24, 9, 0))}))
	// 已有运输事实的场次不能改期覆盖。
	rsReq := planSlotReq("S1", "ART-2", "VEN-BER",
		shanghaiTime(2026, 9, 26, 10, 0), shanghaiTime(2026, 9, 26, 12, 0))
	rsReq.EventID = "EVT-RESCHED-S1"
	err := reschedErr(c, rsReq)
	if !isConflict(err, "不可重排覆盖") {
		t.Fatalf("expected immutable-history conflict, got %v", err)
	}
	// 无历史事实的未来场次可以改期。
	must2(c.PlanSlot(planSlotReq("S2", "ART-1", "VEN-PY",
		shanghaiTime(2026, 9, 27, 10, 0), shanghaiTime(2026, 9, 27, 12, 0), "DEV-P1")))
	must2(c.RescheduleSlot(planSlotReq("S2", "ART-1", "VEN-PY",
		shanghaiTime(2026, 9, 28, 10, 0), shanghaiTime(2026, 9, 28, 12, 0), "DEV-P1")))

	// 取消场次不抹掉运输事实：轨迹仍可查。
	must2(c.CancelSlot(ConfirmRequest{EventID: "EVT-X1", Ref: "S1"}))
	track := c.ArtworkTrack("ART-2")
	foundShipped := false
	for _, e := range track {
		if e.Kind == "movement" && strings.Contains(e.Summary, "shipped") {
			foundShipped = true
		}
	}
	if !foundShipped {
		t.Fatalf("historical movement missing from track after cancellation: %+v", track)
	}
}

func TestDamageGeneratesAuditableDisposition(t *testing.T) {
	w := setupWorld(t, testNow)
	c := w.c
	must2(c.PlanSlot(planSlotReq("S1", "ART-1", "VEN-PY",
		shanghaiTime(2026, 9, 25, 10, 0), shanghaiTime(2026, 9, 25, 12, 0), "DEV-P1")))
	res := must2(c.ReportDamage(DamageRequest{EventID: "EVT-DMG1", Ref: "DSP-DMG-1",
		PieceRef: "PIECE-1A", Detail: "运输箱受潮，相框变形"}))
	if res.Idempotent {
		t.Fatalf("first damage report must not be idempotent")
	}
	d := c.state.dispos["DSP-DMG-1"]
	if d == nil || !d.OpenRisk {
		t.Fatalf("expected open disposition, got %+v", d)
	}
	var pull bool
	for _, a := range d.Actions {
		if a.Kind == domain.ActionPullFromDisplay && a.SlotRef == "S1" {
			pull = true
		}
	}
	if !pull {
		t.Fatalf("expected pull-from-display for S1, got %+v", d.Actions)
	}
	// 损坏组成件不得进入新场次。
	err := planErr(c, planSlotReq("S2", "ART-1", "VEN-PY",
		shanghaiTime(2026, 9, 26, 10, 0), shanghaiTime(2026, 9, 26, 12, 0), "DEV-P2"))
	if !isConflict(err, "已报损") {
		t.Fatalf("expected damaged-piece conflict, got %v", err)
	}
	// 重复上报同一事件幂等，不产生第二份方案。
	must2(c.ReportDamage(DamageRequest{EventID: "EVT-DMG1", Ref: "DSP-DMG-1", PieceRef: "PIECE-1A"}))
	if len(c.state.dispos) != 1 {
		t.Fatalf("expected one disposition, got %d", len(c.state.dispos))
	}
	// 解决后审计字段闭合。
	must2(c.ResolveDisposition(ResolutionRequest{EventID: "EVT-RSLV1", Ref: "DSP-DMG-1",
		Resolution: "定损完成，保险理赔受理，作品撤展返还"}))
	if c.state.dispos["DSP-DMG-1"].OpenRisk {
		t.Fatalf("disposition should be resolved")
	}
}

func TestRightsWithdrawalBlocksAndSuggestsAlternative(t *testing.T) {
	w := setupWorld(t, testNow)
	c := w.c
	must2(c.PlanSlot(planSlotReq("S1", "ART-1", "VEN-PY",
		shanghaiTime(2026, 9, 25, 10, 0), shanghaiTime(2026, 9, 25, 12, 0), "DEV-P1")))
	must2(c.WithdrawRights(WithdrawalRequest{EventID: "EVT-WD1", Ref: "DSP-WD-1",
		GrantRef: "RGR-1", Detail: "权利人临时撤回中国地区授权"}))

	d := c.state.dispos["DSP-WD-1"]
	if d == nil {
		t.Fatalf("expected withdrawal disposition")
	}
	var altVenue string
	for _, a := range d.Actions {
		if a.SlotRef == "S1" && a.Alternate != nil {
			altVenue = a.Alternate.VenueRef
		}
	}
	if altVenue != "VEN-LA" {
		t.Fatalf("expected US alternative VEN-LA via RGR-2, got %q", altVenue)
	}
	// 撤回后原 CN 场次无法再确认。
	err := confirmErr(c, ConfirmRequest{EventID: "EVT-CF1", Ref: "S1"})
	if !isConflict(err, "无有效授权") {
		t.Fatalf("expected rights conflict after withdrawal, got %v", err)
	}
}

func TestVenueClosureRelocation(t *testing.T) {
	w := setupWorld(t, testNow)
	c := w.c
	must2(c.PlanSlot(planSlotReq("S1", "ART-2", "VEN-PY",
		shanghaiTime(2026, 9, 25, 10, 0), shanghaiTime(2026, 9, 25, 12, 0))))
	must2(c.CloseVenue(VenueCloseRequest{EventID: "EVT-VC1", Ref: "DSP-VC-1",
		VenueRef: "VEN-PY", Detail: "院落修缮临时关闭"}))
	d := c.state.dispos["DSP-VC-1"]
	var relocated string
	for _, a := range d.Actions {
		if a.SlotRef == "S1" && a.Alternate != nil {
			relocated = a.Alternate.VenueRef
		}
	}
	if relocated != "VEN-BER" {
		t.Fatalf("expected relocation alternative, got %q (actions=%+v)", relocated, d.Actions)
	}
	// 关闭后任何新排期都不能落在该场地。
	err := planErr(c, planSlotReq("S2", "ART-2", "VEN-PY",
		shanghaiTime(2026, 9, 26, 10, 0), shanghaiTime(2026, 9, 26, 12, 0)))
	if !isConflict(err, "已关闭") {
		t.Fatalf("expected closed venue conflict, got %v", err)
	}
}

func TestDeviceBreakageSuggestsSwap(t *testing.T) {
	w := setupWorld(t, testNow)
	c := w.c
	must2(c.PlanSlot(planSlotReq("S1", "ART-1", "VEN-PY",
		shanghaiTime(2026, 9, 25, 10, 0), shanghaiTime(2026, 9, 25, 12, 0), "DEV-P1")))
	must2(c.BreakDevice(DeviceBreakRequest{EventID: "EVT-DB1", Ref: "DSP-DB-1",
		DeviceRef: "DEV-P1", Detail: "投影仪灯泡烧毁"}))
	d := c.state.dispos["DSP-DB-1"]
	found := false
	for _, a := range d.Actions {
		if a.SlotRef == "S1" && a.Alternate != nil {
			for _, dev := range a.Alternate.Devices {
				if dev == "DEV-P2" {
					found = true
				}
			}
		}
	}
	if !found {
		t.Fatalf("expected swap onto DEV-P2, got %+v", d.Actions)
	}
}

func TestArtworkTrackAssemblesHistory(t *testing.T) {
	w := setupWorld(t, testNow)
	c := w.c
	must2(c.PlanSlot(planSlotReq("S1", "ART-2", "VEN-PY",
		shanghaiTime(2026, 9, 25, 10, 0), shanghaiTime(2026, 9, 25, 12, 0))))
	must2(c.AppendMovement(MovementRequest{EventID: "EVT-M1", Ref: "MV-1", ArtworkRef: "ART-2",
		SlotRef: "S1", Kind: domain.MovementShipped, OccurredAt: ptrTime(shanghaiTime(2026, 9, 24, 9, 0))}))
	must2(c.AppendMovement(MovementRequest{EventID: "EVT-M2", Ref: "MV-2", ArtworkRef: "ART-2",
		SlotRef: "S1", Kind: domain.MovementOpened, OccurredAt: ptrTime(shanghaiTime(2026, 9, 25, 10, 0))}))

	track := c.ArtworkTrack("ART-2")
	if len(track) != 3 {
		t.Fatalf("expected 3 track entries (2 movements + 1 slot), got %d: %+v", len(track), track)
	}
	if track[0].Kind != "movement" || track[0].Ref != "MV-1" {
		t.Fatalf("track not ordered by time: %+v", track[0])
	}
}

func TestVenueBriefReportsPrereqsRisksAlternatives(t *testing.T) {
	w := setupWorld(t, testNow)
	c := w.c
	// S-A 正常确认；S-B 依赖 S-A 交付，未满足 -> 风险与前置项。
	must2(c.PlanSlot(planSlotReq("SA", "ART-2", "VEN-PY",
		shanghaiTime(2026, 9, 25, 8, 0), shanghaiTime(2026, 9, 25, 10, 0))))
	must2(c.PlanSlot(planSlotReq("SB", "ART-1", "VEN-PY",
		shanghaiTime(2026, 9, 25, 10, 0), shanghaiTime(2026, 9, 25, 12, 0), "DEV-P1")))
	must2(c.AddDependency(DependencyRequest{EventID: "EVT-DEP1", Ref: "DEP-1",
		ArtworkRef: "ART-1", FromSlotRef: "SA", ToSlotRef: "SB", Requires: "delivered"}))

	winStart := shanghaiTime(2026, 9, 25, 0, 0)
	winEnd := shanghaiTime(2026, 9, 26, 0, 0)
	brief, err := c.VenueBrief("VEN-PY", winStart, winEnd)
	if err != nil {
		t.Fatalf("brief: %v", err)
	}
	if len(brief.Slots) != 2 || len(brief.Prereqs) != 1 {
		t.Fatalf("expected 2 slots and 1 prereq, got %+v", brief)
	}
	if brief.Prereqs[0].Satisfied {
		t.Fatalf("prereq should be unsatisfied before delivery")
	}
	var riskFound bool
	for _, r := range brief.Risks {
		if r.SlotRef == "SB" && strings.Contains(r.Message, "delivered") {
			riskFound = true
		}
	}
	if !riskFound {
		t.Fatalf("expected unsatisfied-prereq risk for SB, got %+v", brief.Risks)
	}

	// 交付后前置项满足、风险消失。
	must2(c.AppendMovement(MovementRequest{EventID: "EVT-M1", Ref: "MV-1", ArtworkRef: "ART-2",
		SlotRef: "SA", Kind: domain.MovementDelivered, OccurredAt: ptrTime(shanghaiTime(2026, 9, 25, 7, 0))}))
	brief2, _ := c.VenueBrief("VEN-PY", winStart, winEnd)
	if !brief2.Prereqs[0].Satisfied {
		t.Fatalf("prereq should be satisfied after delivery: %+v", brief2.Prereqs[0])
	}
	for _, r := range brief2.Risks {
		if strings.Contains(r.Message, "delivered") {
			t.Fatalf("prereq risk should clear: %+v", brief2.Risks)
		}
	}
}

func TestEventLogReplayRestoresStateAndImmutability(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "events.jsonl")

	c1, err := New(path, clock.Fixed{T: testNow})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	must2(c1.RegisterArtwork(ArtworkRequest{EventID: "EVT-A1", Ref: "ART-1"}))
	must2(c1.RegisterVenue(VenueRequest{EventID: "EVT-V1", Ref: "VEN-PY", CountryCode: "CN", TZ: "Asia/Shanghai"}))
	must2(c1.RecordRights(RightsRequest{EventID: "EVT-G1", Ref: "RGR-1", ArtworkRef: "ART-1",
		ValidFrom: "2026-09-20", ValidTo: "2026-09-30"}))
	must2(c1.RegisterPiece(PieceRequest{EventID: "EVT-P1", Ref: "PIECE-1", ArtworkRef: "ART-1",
		Media: domain.MediaPhoto}))
	must2(c1.PlanSlot(planSlotReq("S1", "ART-1", "VEN-PY",
		shanghaiTime(2026, 9, 25, 10, 0), shanghaiTime(2026, 9, 25, 12, 0))))
	must2(c1.AppendMovement(MovementRequest{EventID: "EVT-MV1", Ref: "MV-1", ArtworkRef: "ART-1",
		SlotRef: "S1", Kind: domain.MovementShipped,
		OccurredAt: ptrTime(shanghaiTime(2026, 9, 24, 9, 0))}))
	if err := c1.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	// 十天后重启：场次开始时刻已过，且已有运输事实——两条理由都禁止改期。
	later := testNow.Add(10 * 24 * time.Hour)
	c2, err := New(path, clock.Fixed{T: later})
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer c2.Close()
	if c2.GetArtwork("ART-1") == nil {
		t.Fatalf("artwork not restored from log")
	}
	track := c2.ArtworkTrack("ART-1")
	if len(track) != 2 {
		t.Fatalf("expected restored track with 2 entries, got %+v", track)
	}
	rsReq2 := planSlotReq("S1", "ART-1", "VEN-PY",
		shanghaiTime(2026, 10, 1, 10, 0), shanghaiTime(2026, 10, 1, 12, 0))
	rsReq2.EventID = "EVT-RESCHED-S1-LATE"
	_, err = c2.RescheduleSlot(rsReq2)
	if err == nil {
		t.Fatalf("past/history slot must not be reschedulable after restart")
	}
	// 已处理事件重放仍幂等。
	r := must2(c2.RegisterArtwork(ArtworkRequest{EventID: "EVT-A1", Ref: "ART-1"}))
	if !r.Idempotent {
		t.Fatalf("replayed event should be idempotent after restart")
	}
}

func ptrTime(t time.Time) *time.Time { return &t }

func planErr(c *Coordinator, req SlotRequest) error {
	_, err := c.PlanSlot(req)
	return err
}

func reschedErr(c *Coordinator, req SlotRequest) error {
	_, err := c.RescheduleSlot(req)
	return err
}

func confirmErr(c *Coordinator, req ConfirmRequest) error {
	_, err := c.ConfirmSlot(req)
	return err
}

func artErr(c *Coordinator, req ArtworkRequest) error {
	_, err := c.RegisterArtwork(req)
	return err
}
