package service

import (
	"strings"
	"testing"
	"time"

	"example.com/batch-092001-q007/internal/domain"
)

func apiCode(t *testing.T, err error) string {
	t.Helper()
	if err == nil {
		return ""
	}
	ae := asAPIError(err)
	return ae.Code
}

// ---------- 批量清单与幂等 ----------

func TestBatchIdempotentReplay(t *testing.T) {
	f := newFixture(t)

	// 用与首次相同的 batch_ref 重传，但改动作品标题（内容已变）。
	resp2, err := f.svc.SubmitBatch(BatchRequest{
		BatchRef: "B-001", SchoolRef: "SXAU",
		Works: []WorkInput{{
			Ref: "W-1", Title: "被改动的标题", ArtistRef: "ART-1", Medium: domain.MediumPhoto,
			Rights: []RightInput{{Ref: "R-1", HolderRef: "H-1", Countries: []string{"CN"}, Media: []string{domain.MediumPhoto}}},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !resp2.Replayed {
		t.Fatal("同 batch_ref 重传必须识别为重放")
	}
	if !resp2.DigestChanged {
		t.Fatal("内容变化时应提示 digest_changed")
	}
	if len(f.svc.Store().ListWorks()) != 3 {
		t.Fatalf("幂等重放不得改动事实库，作品数=%d 应为 3", len(f.svc.Store().ListWorks()))
	}
	if w, _ := f.svc.Store().GetWork("W-1"); w.Title != "古城墙组照" {
		t.Fatalf("首次受理内容不得被重传覆盖，标题=%q", w.Title)
	}
	if len(f.svc.Store().AllEvents()) != 1 {
		t.Fatalf("重放不得重复追加台账事件，事件数=%d 应为 1", len(f.svc.Store().AllEvents()))
	}
}

func TestBatchValidation(t *testing.T) {
	f := newFixture(t)
	cases := []struct {
		name string
		in   BatchRequest
		code string
	}{
		{
			name: "缺少 batch_ref", code: "invalid_batch",
			in: BatchRequest{SchoolRef: "X", Works: []WorkInput{{Ref: "W"}}},
		},
		{
			name: "空清单", code: "invalid_batch",
			in: BatchRequest{BatchRef: "B", SchoolRef: "X"},
		},
		{
			name: "非法媒介", code: "invalid_medium",
			in: BatchRequest{BatchRef: "B2", SchoolRef: "X", Works: []WorkInput{
				{Ref: "W9", Title: "t", ArtistRef: "a", Medium: "sculpture"},
			}},
		},
		{
			name: "组成件 form 非法", code: "invalid_form",
			in: BatchRequest{BatchRef: "B3", SchoolRef: "X", Works: []WorkInput{
				{
					Ref: "W9", Title: "t", ArtistRef: "a", Medium: domain.MediumPhoto,
					Components: []ComponentInput{{Ref: "C", Form: "ghost"}},
				},
			}},
		},
		{
			name: "借展窗口倒置", code: "invalid_loan",
			in: BatchRequest{BatchRef: "B4", SchoolRef: "X", Works: []WorkInput{
				{
					Ref: "W9", Title: "t", ArtistRef: "a", Medium: domain.MediumPhoto,
					Loan: &LoanInput{Ref: "L", Start: time.Now(), End: time.Now().Add(-time.Hour)},
				},
			}},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := f.svc.SubmitBatch(tc.in)
			if got := apiCode(t, err); got != tc.code {
				t.Fatalf("错误码=%q 期望 %q (%v)", got, tc.code, err)
			}
		})
	}
}

// ---------- 跨时区落位 ----------

func TestSlotLocalizesToVenueTimezone(t *testing.T) {
	f := newFixture(t)
	// 以 +02:00 的挂钟提交同一瞬时，平遥（+08）应为 6 小时后。
	start := time.Date(2026, 9, 19, 2, 0, 0, 0, time.FixedZone("CEST", 2*3600))
	end := start.Add(3 * time.Hour)
	v := f.propose(t, SlotRequest{
		VenueRef: "V-BEIJING", WorkRef: "W-1", Start: start, End: end,
		Equipment: []string{"MON-01"},
	})
	if v.Slot.LocalStart != "2026-09-19T08:00:00" {
		t.Fatalf("当地挂钟落位错误: %s", v.Slot.LocalStart)
	}
	if v.Slot.Timezone != "Asia/Shanghai" {
		t.Fatalf("时区标签=%q", v.Slot.Timezone)
	}
}

// ---------- 占用冲突 ----------

func TestDoubleBookingRejected(t *testing.T) {
	f := newFixture(t)
	s1, e1 := sep19Window()
	mid := s1.Add(24 * time.Hour)

	// 第一条：W-1 在 V-PING 使用 MON-01。
	f.confirmProposed(t, SlotRequest{
		VenueRef: "V-PING", WorkRef: "W-1", Start: s1, End: e1, Equipment: []string{"MON-01"},
	})

	// 同一实体作品 W-1 重叠排期（带上满足场地条件的设备，隔离出占用冲突）→ 实体件冲突。
	_, err := f.svc.ProposeSlot(SlotRequest{
		VenueRef: "V-PING", WorkRef: "W-1", Start: mid, End: e1.Add(24 * time.Hour),
		Equipment: []string{"PROJ-4K"},
	})
	if got := apiCode(t, err); got != "double_booked" {
		t.Fatalf("同作品重叠排期错误码=%q", got)
	}

	// 不同作品但同一台设备 MON-01 重叠 → 设备冲突。
	_, err = f.svc.ProposeSlot(SlotRequest{
		VenueRef: "V-PING", WorkRef: "W-3", Start: mid, End: e1, Equipment: []string{"MON-01"},
	})
	if got := apiCode(t, err); got != "double_booked" {
		t.Fatalf("设备重叠排期错误码=%q", got)
	}

	// 首尾相接（半开区间）不冲突：W-3 在 e1 起使用 MON-01 应通过。
	v := f.propose(t, SlotRequest{
		VenueRef: "V-PING", WorkRef: "W-3", Start: e1, End: e1.Add(48 * time.Hour),
		Equipment: []string{"MON-01"},
	})
	if v.Slot.Start.Before(e1) || !v.Slot.Start.Equal(e1) {
		t.Fatal("相邻时段应被允许")
	}
}

// ---------- 展示限制：地域 / 媒介 / 日期 ----------

func TestPublicDisplayRestrictions(t *testing.T) {
	f := newFixture(t)
	s1, e1 := sep19Window()

	// W-1 权利仅限 CN：排到 FR 场地 → 地域拒绝（V-FR 同时不接受照片，两个问题都应上报）。
	_, err := f.svc.ProposeSlot(SlotRequest{
		VenueRef: "V-FR", WorkRef: "W-1", Start: s1, End: e1,
	})
	msg := err.Error()
	if !strings.Contains(msg, "right_denied") || !strings.Contains(msg, "venue_medium") {
		t.Fatalf("应同时报告地域与媒介限制，实际: %v", err)
	}

	// 新增一个法国但接受照片的场地，隔离地域规则。
	if err := f.addVenue(VenueInput{
		Ref: "V-FR2", Name: "巴黎摄影空间", Country: "FR", Timezone: "Europe/Paris",
		AcceptedMedia: []string{domain.MediumPhoto},
	}); err != nil {
		t.Fatal(err)
	}
	_, err = f.svc.ProposeSlot(SlotRequest{VenueRef: "V-FR2", WorkRef: "W-1", Start: s1, End: e1})
	if !strings.Contains(apiCode(t, err)+err.Error(), "right_denied") {
		t.Fatalf("CN-only 权利在 FR 场地应被拒绝，实际: %v", err)
	}

	// 日期限制：新作品 W-4 的声明在 9-18 截止，9-19 开展应被拒绝。
	_, err = f.svc.SubmitBatch(BatchRequest{
		BatchRef: "B-002", SchoolRef: "SXAU",
		Works: []WorkInput{{
			Ref: "W-4", Title: "限期作品", ArtistRef: "ART-4", Medium: domain.MediumPhoto,
			Components: []ComponentInput{{Ref: "C-4A", Form: domain.FormPhysical, TransportClass: "framed"}},
			Rights: []RightInput{{
				Ref: "R-4", HolderRef: "H-4", Countries: []string{domain.CountryAll},
				Media:    []string{domain.MediumPhoto},
				NotAfter: ptrTime(time.Date(2026, 9, 18, 16, 0, 0, 0, time.UTC)),
			}},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = f.svc.ProposeSlot(SlotRequest{VenueRef: "V-BEIJING", WorkRef: "W-4", Start: s1, End: e1})
	if !strings.Contains(err.Error(), "right_denied") {
		t.Fatalf("超出权利日期窗口应拒绝，实际: %v", err)
	}

	// 借展撤展时间：W-1 借展 10-10 截止，排到 10-20 应拒绝。
	_, err = f.svc.ProposeSlot(SlotRequest{
		VenueRef: "V-BEIJING", WorkRef: "W-1",
		Start:     time.Date(2026, 10, 18, 0, 0, 0, 0, time.UTC),
		End:       time.Date(2026, 10, 20, 0, 0, 0, 0, time.UTC),
		Equipment: []string{"MON-01"},
	})
	if !strings.Contains(err.Error(), "loan_window") {
		t.Fatalf("超出撤展时间应拒绝，实际: %v", err)
	}
}

func TestVenueConditionsAndPlayback(t *testing.T) {
	f := newFixture(t)
	s1, e1 := sep19Window()

	// V-PING 需要 darkroom：不分配设备 → 场地条件失败。
	_, err := f.svc.ProposeSlot(SlotRequest{VenueRef: "V-PING", WorkRef: "W-1", Start: s1, End: e1})
	if !strings.Contains(err.Error(), "venue_requirement") {
		t.Fatalf("未满足场地条件应拒绝，实际: %v", err)
	}

	// W-2 需要 4k/hdr：分配只支持 1080p 的 MON-01 → 播放规格失败。
	_, err = f.svc.ProposeSlot(SlotRequest{
		VenueRef: "V-BEIJING", WorkRef: "W-2", Start: s1, End: e1, Equipment: []string{"MON-01"},
	})
	if !strings.Contains(err.Error(), "playback_mismatch") {
		t.Fatalf("播放规格不匹配应拒绝，实际: %v", err)
	}

	// 正确组合（PROJ-4K 提供 darkroom 与 4k/hdr）应通过。
	v := f.propose(t, SlotRequest{
		VenueRef: "V-PING", WorkRef: "W-2", Start: s1, End: e1, Equipment: []string{"PROJ-4K"},
	})
	// 数字作品没有运输前置项，但应有试播前置项。
	kinds := map[string]bool{}
	for _, p := range v.Prereqs {
		kinds[p.Kind] = true
	}
	if kinds[domain.PrereqTransport] {
		t.Fatal("数字组成件不应生成运输到场前置项")
	}
	if !kinds[domain.PrereqPlayback] || !kinds[domain.PrereqInsurance] ||
		!kinds[domain.PrereqPermit] || !kinds[domain.PrereqTeam] {
		t.Fatalf("前置项不完整: %v", kinds)
	}
}

// ---------- 确认门控与前置项事件 ----------

func TestConfirmRequiresPrerequisites(t *testing.T) {
	f := newFixture(t)
	s1, e1 := sep19Window()
	v := f.propose(t, SlotRequest{
		VenueRef: "V-PING", WorkRef: "W-1", Start: s1, End: e1, Equipment: []string{"MON-01"},
	})

	if _, err := f.svc.ConfirmSlot(v.Slot.Ref); apiCode(t, err) != "prerequisites_open" {
		t.Fatalf("前置项未解决时确认应被拦截，实际: %v", err)
	}

	f.satisfyPrereqs(t, v.Slot.Ref, "C-1A")
	confirmed, err := f.svc.ConfirmSlot(v.Slot.Ref)
	if err != nil {
		t.Fatal(err)
	}
	if confirmed.Slot.Status != domain.SlotConfirmed {
		t.Fatalf("状态=%q", confirmed.Slot.Status)
	}
	// 重复确认幂等。
	if _, err := f.svc.ConfirmSlot(v.Slot.Ref); err != nil {
		t.Fatalf("重复确认应幂等，实际 %v", err)
	}
}

// ---------- 临时换场与连带事项 ----------

func TestRescheduleCascadeAndImmutability(t *testing.T) {
	f := newFixture(t)
	s1, e1 := sep19Window()
	v := f.confirmProposed(t, SlotRequest{
		VenueRef: "V-PING", WorkRef: "W-1", Start: s1, End: e1, Equipment: []string{"MON-01"},
	})

	newStart := s1.Add(24 * time.Hour)
	newEnd := e1.Add(24 * time.Hour)
	view, err := f.svc.RescheduleSlot(v.Slot.Ref, RescheduleRequest{
		Start: newStart, End: newEnd, Reason: "街巷临时施工",
	})
	if err != nil {
		t.Fatal(err)
	}
	old, _ := f.svc.Store().GetSlot(v.Slot.Ref)
	if old.Status != domain.SlotSuperseded || old.SupersededBy != view.Slot.Ref {
		t.Fatalf("旧排期应标记 superseded，实际 %+v", old)
	}
	orders := f.svc.Store().ListChangeOrders()
	if len(orders) != 1 {
		t.Fatalf("换场单数量=%d", len(orders))
	}
	kinds := map[string]bool{}
	for _, item := range orders[0].Items {
		kinds[item.Kind] = true
		if item.Status != domain.ChangeOpen {
			t.Fatalf("连带事项初始应未闭环: %+v", item)
		}
	}
	for _, k := range []string{domain.ChangeInsurance, domain.ChangeTeam, domain.ChangePermit} {
		if !kinds[k] {
			t.Fatalf("换场缺少连带事项 %s", k)
		}
	}
	// 已到场组成件的运输前置项应继承为已解决，新排期仍需重新保险/许可/班组。
	var openKinds []string
	for _, p := range view.Prereqs {
		if p.Status == domain.PrereqOpen {
			openKinds = append(openKinds, p.Kind)
		}
	}
	joined := strings.Join(openKinds, ",")
	for _, k := range []string{domain.PrereqInsurance, domain.PrereqPermit, domain.PrereqTeam} {
		if !strings.Contains(joined, k) {
			t.Fatalf("换场后 %s 前置项应重新打开，实际开放: %s", k, joined)
		}
	}

	// 换场到不接受照片媒介的 V-FR（且权利仅限 CN）应被规则拦截，旧排期不受影响。
	_, err = f.svc.RescheduleSlot(view.Slot.Ref, RescheduleRequest{
		VenueRef: "V-FR", Start: newStart, End: newEnd, Equipment: []string{"MON-01"},
	})
	if apiCode(t, err) != "slot_validation_failed" || !strings.Contains(err.Error(), "venue_medium") {
		t.Fatalf("换场到媒介不符的场地应被拦截，实际: %v", err)
	}
	if cur, _ := f.svc.Store().GetSlot(view.Slot.Ref); cur.Status != domain.SlotConfirmed {
		t.Fatalf("被拒绝的换场不得改动新排期，状态=%q", cur.Status)
	}
}

func TestPastSlotCannotBeRescheduled(t *testing.T) {
	f := newFixture(t)
	s1, e1 := sep19Window()
	v := f.confirmProposed(t, SlotRequest{
		VenueRef: "V-PING", WorkRef: "W-1", Start: s1, End: e1, Equipment: []string{"MON-01"},
	})
	// 时钟推进到展出开始之后：运输与展出已发生，不得重排覆盖。
	f.svc.WithClock(func() time.Time { return s1.Add(time.Hour) })
	_, err := f.svc.RescheduleSlot(v.Slot.Ref, RescheduleRequest{
		Start: s1.Add(48 * time.Hour), End: e1.Add(48 * time.Hour),
	})
	if apiCode(t, err) != "past_immutable" {
		t.Fatalf("已开始排期错误码=%q (%v)", apiCode(t, err), err)
	}
	if _, err := f.svc.CancelSlot(v.Slot.Ref, CancelRequest{Reason: "x"}); apiCode(t, err) != "past_immutable" {
		t.Fatalf("已开始排期不应被取消，错误码=%q", apiCode(t, err))
	}
}

// ---------- 事件台账：幂等、迟到序号、保留原始时间 ----------

func TestEventLedgerSemantics(t *testing.T) {
	f := newFixture(t)
	s1, e1 := sep19Window()
	v := f.propose(t, SlotRequest{
		VenueRef: "V-PING", WorkRef: "W-1", Start: s1, End: e1, Equipment: []string{"MON-01"},
	})

	occurred := testClock().Add(5 * time.Hour)
	req := EventRequest{
		EventID: "EVT-INS-X1", Source: "ops", SourceSequence: 5,
		OccurredAt: occurred, SubjectRef: v.Slot.Ref, Type: EvtInsuranceBound,
		Payload: marshalPayload(map[string]string{"slot_ref": v.Slot.Ref, "ref": "POL-1"}),
	}
	res, err := f.svc.IngestEvent(req)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Effect.ResolvedPrereqs) != 1 {
		t.Fatalf("保险事件应解决 1 个前置项，实际 %d", len(res.Effect.ResolvedPrereqs))
	}
	// 同一 event_id 重放：幂等、不重复执行副作用。
	res2, err := f.svc.IngestEvent(req)
	if err != nil {
		t.Fatal(err)
	}
	if !res2.Duplicate {
		t.Fatal("同 event_id 必须识别为重复")
	}
	// 迟到的更小序号：仍受理并按其原始 occurred_at 落账。
	earlier := occurred.Add(-2 * time.Hour)
	res3, err := f.svc.IngestEvent(EventRequest{
		EventID: "EVT-TEAM-X2", Source: "ops", SourceSequence: 3,
		OccurredAt: earlier, SubjectRef: v.Slot.Ref, Type: EvtTeamAssigned,
		Payload: marshalPayload(map[string]string{"slot_ref": v.Slot.Ref}),
	})
	if err != nil {
		t.Fatal(err)
	}
	if !res3.StaleSeq {
		t.Fatal("更小来源序号应标记 stale_sequence")
	}
	if !res3.OccurredAt.Equal(earlier) {
		t.Fatal("原始 occurred_at 不得被到达时间覆盖")
	}
	// 未知事件类型拒绝。
	_, err = f.svc.IngestEvent(EventRequest{
		EventID: "EVT-BAD", Source: "ops", SourceSequence: 6,
		OccurredAt: testClock(), SubjectRef: "x", Type: "explode",
	})
	if apiCode(t, err) != "unknown_event_type" {
		t.Fatalf("未知类型错误码=%q", apiCode(t, err))
	}

	// 自带摘要与负载不一致时拒绝入账。
	_, err = f.svc.IngestEvent(EventRequest{
		EventID: "EVT-TAMPER", Source: "ops", SourceSequence: 7,
		OccurredAt: testClock(), SubjectRef: v.Slot.Ref, Type: EvtTeamAssigned,
		PayloadDigest: "sha256:0000000000000000000000000000000000000000000000000000000000000000",
		Payload:       marshalPayload(map[string]string{"slot_ref": v.Slot.Ref}),
	})
	if apiCode(t, err) != "digest_mismatch" {
		t.Fatalf("摘要不一致错误码=%q (%v)", apiCode(t, err), err)
	}
}

// ---------- 损坏 / 撤回 / 关闭处置方案 ----------

func TestDamageOpensAuditablePlan(t *testing.T) {
	f := newFixture(t)
	s1, e1 := sep19Window()
	v := f.confirmProposed(t, SlotRequest{
		VenueRef: "V-PING", WorkRef: "W-1", Start: s1, End: e1, Equipment: []string{"MON-01"},
	})

	res := f.ingest(t, EventRequest{
		EventID: "EVT-DMG-1", Source: "venue-ops", SourceSequence: 1,
		OccurredAt: testClock().Add(24 * time.Hour), SubjectRef: "C-1A",
		Type:    EvtComponentDamaged,
		Payload: marshalPayload(map[string]string{"component_ref": "C-1A", "note": "布展落架磕碰"}),
	})
	plan, ok := f.svc.Store().GetPlan(res.Effect.PlanRef)
	if !ok {
		t.Fatal("应生成处置方案")
	}
	if plan.Trigger != domain.TriggerDamage || len(plan.AffectedSlots) != 1 ||
		plan.AffectedSlots[0] != v.Slot.Ref {
		t.Fatalf("损坏方案关联错误: %+v", plan)
	}
	var hasVenueAlt bool
	for _, a := range plan.Alternatives {
		if a.Kind == "venue" && a.Ref == "V-BEIJING" {
			hasVenueAlt = true
		}
	}
	if !hasVenueAlt {
		t.Fatalf("应给出可承接的备选场地 V-BEIJING，实际: %+v", plan.Alternatives)
	}
	// 损坏件不得再进入新排期。
	_, err := f.svc.ProposeSlot(SlotRequest{
		VenueRef: "V-BEIJING", WorkRef: "W-1",
		Start: e1, End: e1.Add(48 * time.Hour), Equipment: []string{"MON-01"},
	})
	if !strings.Contains(err.Error(), "component_damaged") {
		t.Fatalf("损坏件应阻止新排期，实际: %v", err)
	}
	// 执行全部步骤后方案归档。
	var execs []StepExecution
	for _, st := range plan.Steps {
		execs = append(execs, StepExecution{Seq: st.Seq, Status: domain.PlanExecuted})
	}
	updated, err := f.svc.ExecutePlan(plan.Ref, ExecutePlanRequest{Steps: execs})
	if err != nil {
		t.Fatal(err)
	}
	if updated.Status != domain.PlanExecuted {
		t.Fatalf("方案状态=%q", updated.Status)
	}
}

func TestWithdrawalRespectsRemainingRights(t *testing.T) {
	f := newFixture(t)
	s1, e1 := sep19Window()

	// W-5 持有两条声明：R-5b 全球，R-5a 仅 CN。
	_, err := f.svc.SubmitBatch(BatchRequest{
		BatchRef: "B-005", SchoolRef: "SXAU",
		Works: []WorkInput{{
			Ref: "W-5", Title: "双重授权作品", ArtistRef: "ART-5", Medium: domain.MediumPhoto,
			Components: []ComponentInput{{Ref: "C-5A", Form: domain.FormPhysical, TransportClass: "framed"}},
			Rights: []RightInput{
				{Ref: "R-5A", HolderRef: "H-5", Countries: []string{"CN"}, Media: []string{domain.MediumPhoto}},
				{Ref: "R-5B", HolderRef: "H-5", Countries: []string{domain.CountryAll}, Media: []string{domain.MediumPhoto}},
			},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	f.confirmProposed(t, SlotRequest{
		VenueRef: "V-PING", WorkRef: "W-5", Start: s1, End: e1, Equipment: []string{"MON-01"},
	})

	revoke := func(ref, evt string, seq int64) EventResponse {
		return f.ingest(t, EventRequest{
			EventID: evt, Source: "rights", SourceSequence: seq,
			OccurredAt: testClock().Add(time.Duration(seq) * time.Hour), SubjectRef: ref,
			Type: EvtRightRevoked, Payload: marshalPayload(map[string]string{"right_ref": ref, "reason": "作者要求"}),
		})
	}
	// 撤回 CN 声明后，全球声明仍覆盖 → 无受影响排期。
	r1 := revoke("R-5A", "EVT-REV-5A", 1)
	if p, _ := f.svc.Store().GetPlan(r1.Effect.PlanRef); len(p.AffectedSlots) != 0 {
		t.Fatalf("仍有覆盖声明时不应影响排期，实际 %d 条", len(p.AffectedSlots))
	}
	// 再撤回全球声明 → 排期失去权利覆盖。
	r2 := revoke("R-5B", "EVT-REV-5B", 2)
	p, _ := f.svc.Store().GetPlan(r2.Effect.PlanRef)
	if len(p.AffectedSlots) != 1 {
		t.Fatalf("全部声明撤回后应有 1 条受影响排期，实际 %d", len(p.AffectedSlots))
	}
	// 已结束的历史排期不受撤回影响：直接补录一条 9-1~9-5 的历史 confirmed 记录。
	f.svc.Store().PutSlot(domain.Slot{
		Ref: "SLOT-HIST-1", VenueRef: "V-BEIJING", WorkRef: "W-3",
		Start:    time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC),
		End:      time.Date(2026, 9, 5, 0, 0, 0, 0, time.UTC),
		Timezone: "Asia/Shanghai", Status: domain.SlotConfirmed, Components: []string{"C-3A"},
		CreatedAt: time.Date(2026, 8, 20, 0, 0, 0, 0, time.UTC),
	})
	f.ingest(t, EventRequest{
		EventID: "EVT-REV-3", Source: "rights", SourceSequence: 3,
		OccurredAt: testClock(), SubjectRef: "R-3", Type: EvtRightRevoked,
		Payload: marshalPayload(map[string]string{"right_ref": "R-3", "reason": "x"}),
	})
	p3, _ := f.svc.Store().GetPlan(f.ingest(t, EventRequest{
		EventID: "EVT-REV-3B", Source: "rights", SourceSequence: 4,
		OccurredAt: testClock(), SubjectRef: "R-3", Type: EvtRightRevoked,
		Payload: marshalPayload(map[string]string{"right_ref": "R-3"}),
	}).Effect.PlanRef)
	if len(p3.AffectedSlots) != 0 {
		t.Fatalf("已结束排期不应被撤回波及，实际 %d 条", len(p3.AffectedSlots))
	}
}

func TestVenueClosurePlan(t *testing.T) {
	f := newFixture(t)
	s1, e1 := sep19Window()
	v1 := f.confirmProposed(t, SlotRequest{
		VenueRef: "V-PING", WorkRef: "W-1", Start: s1, End: e1, Equipment: []string{"MON-01"},
	})
	v2 := f.confirmProposed(t, SlotRequest{
		VenueRef: "V-BEIJING", WorkRef: "W-3", Start: s1, End: e1,
	})

	res := f.ingest(t, EventRequest{
		EventID: "EVT-CLOSE-1", Source: "gov", SourceSequence: 1,
		OccurredAt: testClock().Add(48 * time.Hour), SubjectRef: "V-PING",
		Type:    EvtVenueClosed,
		Payload: marshalPayload(map[string]string{"venue_ref": "V-PING", "reason": "古建修缮"}),
	})
	plan, _ := f.svc.Store().GetPlan(res.Effect.PlanRef)
	if plan.Trigger != domain.TriggerClosure || len(plan.AffectedSlots) != 1 ||
		plan.AffectedSlots[0] != v1.Slot.Ref {
		t.Fatalf("关闭方案只应波及 V-PING 的排期: %+v", plan.AffectedSlots)
	}
	// 已关闭场地拒绝一切新排期；其他场地不受影响（v2 仍可查）。
	if _, err := f.svc.ProposeSlot(SlotRequest{
		VenueRef: "V-PING", WorkRef: "W-3", Start: e1, End: e1.Add(48 * time.Hour),
	}); !strings.Contains(err.Error(), "venue_closed") {
		t.Fatalf("关闭场地应拒绝新排期，实际: %v", err)
	}
	// 其他场地的既有排期不受影响。
	if other, ok := f.svc.Store().GetSlot(v2.Slot.Ref); !ok || other.Status != domain.SlotConfirmed {
		t.Fatalf("V-BEIJING 排期不应受 V-PING 关闭影响: %+v", other)
	}
}

// ---------- 轨迹与场地时段视图 ----------

func TestWorkTimeline(t *testing.T) {
	f := newFixture(t)
	s1, e1 := sep19Window()
	v := f.confirmProposed(t, SlotRequest{
		VenueRef: "V-PING", WorkRef: "W-1", Start: s1, End: e1, Equipment: []string{"MON-01"},
	})
	tl, err := f.svc.WorkTimeline("W-1")
	if err != nil {
		t.Fatal(err)
	}
	f.svc.RescheduleSlot(v.Slot.Ref, RescheduleRequest{Start: s1.Add(24 * time.Hour), End: e1.Add(24 * time.Hour)})
	f.ingest(t, EventRequest{
		EventID: "EVT-DMG-T", Source: "ops", SourceSequence: 1,
		OccurredAt: testClock().Add(72 * time.Hour), SubjectRef: "C-1A",
		Type:    EvtComponentDamaged,
		Payload: marshalPayload(map[string]string{"component_ref": "C-1A"}),
	})

	tl, err = f.svc.WorkTimeline("W-1")
	if err != nil {
		t.Fatal(err)
	}
	cats := map[string]int{}
	for _, en := range tl.Entries {
		cats[en.Category]++
	}
	if cats["event:batch_submitted"] == 0 {
		t.Fatal("轨迹应包含院校批次入库事件")
	}
	if cats["event:component_damaged"] == 0 || cats["plan:damage"] == 0 {
		t.Fatalf("轨迹应包含损坏事件与处置方案: %v", cats)
	}
	if cats["slot:confirmed"] != 1 || cats["slot:superseded"] != 1 {
		t.Fatalf("轨迹应保留新（confirmed）旧（superseded）两条排期: %v", cats)
	}
	if cats["event:slot_rescheduled"] == 0 {
		t.Fatalf("轨迹应包含换场生命周期事件: %v", cats)
	}
}

func TestVenueWindowView(t *testing.T) {
	f := newFixture(t)
	s1, e1 := sep19Window()
	f.confirmProposed(t, SlotRequest{
		VenueRef: "V-PING", WorkRef: "W-1", Start: s1, End: e1, Equipment: []string{"MON-01"},
	})
	f.ingest(t, EventRequest{
		EventID: "EVT-DMG-W", Source: "ops", SourceSequence: 1,
		OccurredAt: testClock().Add(24 * time.Hour), SubjectRef: "C-1A",
		Type:    EvtComponentDamaged,
		Payload: marshalPayload(map[string]string{"component_ref": "C-1A"}),
	})

	view, err := f.svc.VenueWindow("V-PING",
		time.Date(2026, 9, 19, 0, 0, 0, 0, time.UTC),
		time.Date(2026, 9, 22, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	if view.LocalStart != "2026-09-19T08:00:00" {
		t.Fatalf("窗口应按平遥当地落位: %s", view.LocalStart)
	}
	if len(view.Slots) != 1 {
		t.Fatalf("窗内排期数=%d", len(view.Slots))
	}
	brief := view.Slots[0]
	if len(brief.Prerequisites) == 0 {
		t.Fatal("应列出安装前置项")
	}
	var codes []string
	for _, r := range brief.Risks {
		codes = append(codes, r.Code)
	}
	if !strings.Contains(strings.Join(codes, ","), "component_damaged") {
		t.Fatalf("应列出损坏未解决风险: %v", codes)
	}
	var hasAlt bool
	for _, a := range brief.Alternatives {
		if a.Ref == "V-BEIJING" {
			hasAlt = true
		}
	}
	if !hasAlt {
		t.Fatalf("应列出可替代场地: %+v", brief.Alternatives)
	}
}
