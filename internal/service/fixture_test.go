package service

import (
	"testing"
	"time"

	"example.com/batch-092001-q007/internal/domain"
	"example.com/batch-092001-q007/internal/store"
)

// 固定的测试时钟：2026-09-10 12:00 UTC（大展前）。
func testClock() time.Time {
	return time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC)
}

// fixture 提供已铺好基础数据的服务：
//   - 场地 V-PING（平遥，+08）、V-BEIJING（北京，+08）、V-FR（巴黎，+02 仅视频）
//   - 设备 PROJ-4K（4K 投影）、MON-01（显示器，满足 climate_controlled? no—provides darkroom）
//   - 院校批次 SXAU-2026：作品 W-1（照片，实体相框）、W-2（视频，数字文件）、W-3（手工书）
type fixture struct {
	svc *Service
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	st := store.New()
	svc := New(st)
	svc.WithClock(func() time.Time { return testClock() })
	f := &fixture{svc: svc}

	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatalf("fixture setup: %v", err)
		}
	}
	must(f.addVenue(VenueInput{
		Ref: "V-PING", Name: "平遥古城一展厅", Country: "CN", Timezone: "Asia/Shanghai",
		AcceptedMedia: []string{domain.MediumAll}, Requirements: []string{"darkroom"},
	}))
	must(f.addVenue(VenueInput{
		Ref: "V-BEIJING", Name: "北京合作展厅", Country: "CN", Timezone: "Asia/Shanghai",
		AcceptedMedia: []string{domain.MediumAll}, Requirements: nil,
	}))
	must(f.addVenue(VenueInput{
		Ref: "V-FR", Name: "巴黎合作空间", Country: "FR", Timezone: "Europe/Paris",
		AcceptedMedia: []string{domain.MediumVideo}, Requirements: nil,
	}))
	must(f.addEquipment(EquipmentInput{
		Ref: "PROJ-4K", Kind: "projector", PlaybackProfiles: []string{"4k/hdr", "1080p"},
		Provides: []string{"darkroom", "power_3kw"}, TestedProfiles: []string{"4k/hdr"},
	}))
	must(f.addEquipment(EquipmentInput{
		Ref: "MON-01", Kind: "monitor", PlaybackProfiles: []string{"1080p"},
		Provides: []string{"darkroom"},
	}))

	batch := BatchRequest{
		BatchRef:  "B-001",
		SchoolRef: "SXAU",
		Works: []WorkInput{
			{
				Ref: "W-1", Title: "古城墙组照", ArtistRef: "ART-1", Medium: domain.MediumPhoto,
				Components: []ComponentInput{
					{Ref: "C-1A", Form: domain.FormPhysical, Medium: domain.MediumPhoto, TransportClass: "framed_fragile", Sha256: "sha256:frame-a"},
				},
				Rights: []RightInput{
					{Ref: "R-1", HolderRef: "H-1", Countries: []string{"CN"}, Media: []string{domain.MediumPhoto}},
				},
				Loan: &LoanInput{Ref: "L-1", LenderRef: "ART-1",
					Start: time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC),
					End:   time.Date(2026, 10, 10, 0, 0, 0, 0, time.UTC)},
			},
			{
				Ref: "W-2", Title: "北方实验短片", ArtistRef: "ART-2", Medium: domain.MediumVideo,
				Components: []ComponentInput{
					{Ref: "C-2A", Form: domain.FormDigital, Medium: domain.MediumVideo,
						PlaybackProfile: "4k/hdr", Sha256: "sha256:video-2"},
				},
				Rights: []RightInput{
					{Ref: "R-2", HolderRef: "H-2", Countries: []string{domain.CountryAll},
						Media: []string{domain.MediumVideo}},
				},
				Loan: &LoanInput{Ref: "L-2", LenderRef: "ART-2",
					Start: time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC),
					End:   time.Date(2026, 10, 10, 0, 0, 0, 0, time.UTC)},
			},
			{
				Ref: "W-3", Title: "手工书《窑》", ArtistRef: "ART-3", Medium: domain.MediumBook,
				Components: []ComponentInput{
					{Ref: "C-3A", Form: domain.FormPhysical, Medium: domain.MediumBook, TransportClass: "rare_book"},
				},
				Rights: []RightInput{
					{Ref: "R-3", HolderRef: "H-3", Countries: []string{domain.CountryAll},
						Media: []string{domain.MediumBook}},
				},
			},
		},
	}
	resp, err := svc.SubmitBatch(batch)
	must(err)
	if resp.Replayed {
		t.Fatal("首次清单不应是重放")
	}
	return f
}

func (f *fixture) addVenue(in VenueInput) error {
	_, err := f.svc.RegisterVenue(in)
	return err
}

func (f *fixture) addEquipment(in EquipmentInput) error {
	_, err := f.svc.RegisterEquipment(in)
	return err
}

// propose 快捷提议排期。
func (f *fixture) propose(t *testing.T, in SlotRequest) SlotView {
	t.Helper()
	v, err := f.svc.ProposeSlot(in)
	if err != nil {
		t.Fatalf("ProposeSlot(%s): %v", in.WorkRef, err)
	}
	return v
}

// satisfyPrereqs 用运营事件把排期全部前置项解决。
func (f *fixture) satisfyPrereqs(t *testing.T, slotRef string, arrivedComponents ...string) {
	t.Helper()
	n := 0
	for _, cRef := range arrivedComponents {
		n++
		f.ingest(t, EventRequest{
			EventID: "EVT-ARR-" + cRef + "-" + slotRef, Source: "logistics", SourceSequence: int64(n),
			OccurredAt: testClock().Add(time.Duration(n) * time.Hour), SubjectRef: cRef,
			Type: EvtComponentArrived, Payload: marshalPayload(map[string]string{"component_ref": cRef}),
		})
	}
	bind := func(kind, evtID string, n int) {
		f.ingest(t, EventRequest{
			EventID: evtID, Source: "ops", SourceSequence: int64(n),
			OccurredAt: testClock().Add(time.Duration(n) * time.Hour), SubjectRef: slotRef,
			Type: kind, Payload: marshalPayload(map[string]string{"slot_ref": slotRef, "ref": "DOC-" + kind}),
		})
	}
	bind(EvtInsuranceBound, "EVT-INS-"+slotRef, 90)
	bind(EvtTeamAssigned, "EVT-TEAM-"+slotRef, 91)
	bind(EvtPermitGranted, "EVT-PMT-"+slotRef, 92)
	bind(EvtPlaybackChecked, "EVT-PB-"+slotRef, 93)
}

func (f *fixture) ingest(t *testing.T, in EventRequest) EventResponse {
	t.Helper()
	res, err := f.svc.IngestEvent(in)
	if err != nil {
		t.Fatalf("IngestEvent(%s): %v", in.EventID, err)
	}
	return res
}

// confirmProposed 提议、满足前置项并确认一条排期，返回排期视图。
func (f *fixture) confirmProposed(t *testing.T, in SlotRequest) SlotView {
	t.Helper()
	v := f.propose(t, in)
	f.satisfyPrereqs(t, v.Slot.Ref, physicalRefs(v)...)
	confirmed, err := f.svc.ConfirmSlot(v.Slot.Ref)
	if err != nil {
		t.Fatalf("ConfirmSlot: %v", err)
	}
	return confirmed
}

func physicalRefs(v SlotView) []string {
	var out []string
	for _, p := range v.Prereqs {
		if p.Kind == domain.PrereqTransport {
			out = append(out, p.SubjectRef)
		}
	}
	return out
}

func sep19Window() (time.Time, time.Time) {
	// 2026-09-19 ~ 09-22（UTC，落位平遥为 09-19 全天）
	return time.Date(2026, 9, 18, 16, 0, 0, 0, time.UTC),
		time.Date(2026, 9, 21, 16, 0, 0, 0, time.UTC)
}
