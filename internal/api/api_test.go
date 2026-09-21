package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"example.com/batch-092001-q007/internal/domain"
	"example.com/batch-092001-q007/internal/service"
	"example.com/batch-092001-q007/internal/store"
)

type testServer struct {
	svc *service.Service
	srv *httptest.Server
}

func setup(t *testing.T) *testServer {
	t.Helper()
	st := store.New()
	svc := service.New(st)
	srv := httptest.NewServer(New(svc).Handler())
	t.Cleanup(srv.Close)
	return &testServer{svc: svc, srv: srv}
}

func (ts *testServer) do(t *testing.T, method, path string, body any) (int, map[string]any) {
	t.Helper()
	var reader *bytes.Reader
	if body != nil {
		raw, _ := json.Marshal(body)
		reader = bytes.NewReader(raw)
	} else {
		reader = bytes.NewReader(nil)
	}
	req, _ := http.NewRequest(method, ts.srv.URL+path, reader)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var decoded map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&decoded)
	return resp.StatusCode, decoded
}

func seedBase(t *testing.T, ts *testServer) {
	t.Helper()
	if code, _ := ts.do(t, "POST", "/v1/venues", map[string]any{
		"ref": "V1", "name": "平遥院落", "country": "CN", "timezone": "Asia/Shanghai",
		"accepted_media": []string{"*"}, "requirements": []string{"darkroom"},
	}); code != 200 {
		t.Fatalf("register venue: %d", code)
	}
	if code, _ := ts.do(t, "POST", "/v1/equipment", map[string]any{
		"ref": "EQ1", "kind": "projector",
		"playback_profiles": []string{"4k/hdr"}, "provides": []string{"darkroom"},
	}); code != 200 {
		t.Fatalf("register equipment: %d", code)
	}
}

func TestHealth(t *testing.T) {
	ts := setup(t)
	code, body := ts.do(t, "GET", "/health", nil)
	if code != 200 || body["status"] != "ok" {
		t.Fatalf("health: %d %v", code, body)
	}
}

func TestEndToEndFlow(t *testing.T) {
	ts := setup(t)
	// 固定在大展前，使 9-19 的排期属于"未来"，可以确认与换场。
	fixedNow := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	ts.svc.WithClock(func() time.Time { return fixedNow })
	seedBase(t, ts)

	// 1) 院校批量清单。
	batch := map[string]any{
		"batch_ref": "B-1", "school_ref": "SXAU",
		"occurred_at": "2026-09-01T09:00:00+08:00",
		"works": []map[string]any{{
			"ref": "W1", "title": "组照", "artist_ref": "A1", "medium": "photo",
			"components": []map[string]any{
				{"ref": "C1", "form": "physical", "medium": "photo", "transport_class": "framed", "sha256": "sha256:x"},
				{"ref": "C2", "form": "digital", "medium": "video", "playback_profile": "4k/hdr", "sha256": "sha256:y"},
			},
			"rights": []map[string]any{
				{"ref": "R1", "holder_ref": "H1", "countries": []string{"CN"}, "media": []string{"photo"}},
			},
			"loan": map[string]any{
				"ref": "L1", "lender_ref": "A1",
				"start": "2026-09-01T00:00:00+08:00", "end": "2026-10-31T00:00:00+08:00",
			},
		}},
	}
	code, body := ts.do(t, "POST", "/v1/batches", batch)
	if code != 200 {
		t.Fatalf("submit batch: %d %v", code, body)
	}
	// 重放同一批次 → 幂等。
	code, body2 := ts.do(t, "POST", "/v1/batches", batch)
	if code != 200 || body2["replayed"] != true {
		t.Fatalf("batch replay: %d %v", code, body2)
	}

	// 2) 提议排期。
	slotReq := map[string]any{
		"venue_ref": "V1", "work_ref": "W1",
		"start": "2026-09-19T10:00:00+08:00", "end": "2026-09-22T18:00:00+08:00",
		"equipment": []string{"EQ1"},
	}
	code, body = ts.do(t, "POST", "/v1/slots", slotReq)
	if code != 200 {
		t.Fatalf("propose slot: %d %v", code, body)
	}
	slot := body["slot"].(map[string]any)
	slotRef := slot["ref"].(string)
	if slot["local_start"] != "2026-09-19T10:00:00" {
		t.Fatalf("localization: %v", slot["local_start"])
	}

	// 3) 未满足前置项 → 确认被 409 拦截。
	if code, body = ts.do(t, "POST", "/v1/slots/"+slotRef+"/confirm", nil); code != 409 {
		t.Fatalf("confirm without prereqs: code=%d body=%v", code, body)
	}

	// 4) 事件驱动满足前置项。
	events := []map[string]any{
		{"event_id": "E1", "source": "log", "source_sequence": 1,
			"occurred_at": "2026-09-15T09:00:00+08:00", "subject_ref": "C1",
			"type": "component_arrived", "payload": map[string]string{"component_ref": "C1"}},
		{"event_id": "E2", "source": "ops", "source_sequence": 1,
			"occurred_at": "2026-09-15T09:00:00+08:00", "subject_ref": slotRef,
			"type": "insurance_bound", "payload": map[string]string{"slot_ref": slotRef, "ref": "POL-1"}},
		{"event_id": "E3", "source": "ops", "source_sequence": 2,
			"occurred_at": "2026-09-15T09:00:00+08:00", "subject_ref": slotRef,
			"type": "install_team_assigned", "payload": map[string]string{"slot_ref": slotRef}},
		{"event_id": "E4", "source": "gov", "source_sequence": 1,
			"occurred_at": "2026-09-15T09:00:00+08:00", "subject_ref": slotRef,
			"type": "public_permit_granted", "payload": map[string]string{"slot_ref": slotRef}},
		{"event_id": "E5", "source": "ops", "source_sequence": 3,
			"occurred_at": "2026-09-15T09:00:00+08:00", "subject_ref": slotRef,
			"type": "playback_checked", "payload": map[string]string{"slot_ref": slotRef}},
	}
	for _, ev := range events {
		if code, body = ts.do(t, "POST", "/v1/events", ev); code != 200 {
			t.Fatalf("ingest %s: %d %v", ev["event_id"], code, body)
		}
	}
	// 重复事件 → 幂等。
	if code, body = ts.do(t, "POST", "/v1/events", events[0]); code != 200 || body["duplicate"] != true {
		t.Fatalf("duplicate event: %d %v", code, body)
	}

	// 5) 确认成功。
	if code, body = ts.do(t, "POST", "/v1/slots/"+slotRef+"/confirm", nil); code != 200 {
		t.Fatalf("confirm: %d %v", code, body)
	}

	// 6) 设备双重占用 → 409。
	code, body = ts.do(t, "POST", "/v1/batches", map[string]any{
		"batch_ref": "B-2", "school_ref": "SXAU",
		"works": []map[string]any{{
			"ref": "W2", "title": "手工书", "artist_ref": "A2", "medium": "book",
			"components": []map[string]any{{"ref": "C3", "form": "physical", "transport_class": "rare"}},
			"rights": []map[string]any{{
				"ref": "R2", "holder_ref": "H2", "countries": []string{"*"}, "media": []string{"book"},
			}},
		}},
	})
	if code != 200 {
		t.Fatalf("batch 2: %d %v", code, body)
	}
	code, body = ts.do(t, "POST", "/v1/slots", map[string]any{
		"venue_ref": "V1", "work_ref": "W2",
		"start": "2026-09-20T10:00:00+08:00", "end": "2026-09-21T18:00:00+08:00",
		"equipment": []string{"EQ1"},
	})
	if code != 409 || !strings.Contains(asString(body["error"]), "重复占用") {
		t.Fatalf("double booking should 409: %d %v", code, body)
	}

	// 7) 作品轨迹。
	code, body = ts.do(t, "GET", "/v1/works/W1/timeline", nil)
	if code != 200 {
		t.Fatalf("timeline: %d %v", code, body)
	}
	entries := body["entries"].([]any)
	if len(entries) < 5 {
		t.Fatalf("轨迹条目过少: %d", len(entries))
	}

	// 8) 场地时段视图。
	code, body = ts.do(t, "GET",
		"/v1/venues/V1/window?start=2026-09-19T00:00:00%2B08:00&end=2026-09-23T00:00:00%2B08:00", nil)
	if code != 200 {
		t.Fatalf("venue window: %d %v", code, body)
	}
	slots := body["slots"].([]any)
	if len(slots) != 1 {
		t.Fatalf("窗内排期数=%d", len(slots))
	}
	brief := slots[0].(map[string]any)
	if len(brief["prerequisites"].([]any)) == 0 {
		t.Fatal("应返回安装前置项")
	}

	// 9) 换场：连带保险/班组/许可事项。
	code, body = ts.do(t, "POST", "/v1/slots/"+slotRef+"/reschedule", map[string]any{
		"start":  "2026-09-23T10:00:00+08:00",
		"end":    "2026-09-25T18:00:00+08:00",
		"reason": "临时活动占用院落",
	})
	if code != 200 {
		t.Fatalf("reschedule: %d %v", code, body)
	}
	code, body = ts.do(t, "GET", "/v1/change-orders", nil)
	if code != 200 || len(body["change_orders"].([]any)) != 1 {
		t.Fatalf("change orders: %d %v", code, body)
	}
	items := body["change_orders"].([]any)[0].(map[string]any)["items"].([]any)
	if len(items) != 3 {
		t.Fatalf("连带事项数=%d", len(items))
	}

	// 10) 损坏事件 → 处置方案可查询。
	code, body = ts.do(t, "POST", "/v1/events", map[string]any{
		"event_id": "E6", "source": "ops", "source_sequence": 4,
		"occurred_at": time.Now().Format(time.RFC3339), "subject_ref": "C1",
		"type":    "component_damaged",
		"payload": map[string]string{"component_ref": "C1", "note": "磕碰"},
	})
	if code != 200 {
		t.Fatalf("damage event: %d %v", code, body)
	}
	planRef := body["effect"].(map[string]any)["plan_ref"].(string)
	code, body = ts.do(t, "GET", "/v1/plans/"+planRef, nil)
	if code != 200 {
		t.Fatalf("get plan: %d %v", code, body)
	}
	if body["trigger"] != domain.TriggerDamage {
		t.Fatalf("plan trigger=%v", body["trigger"])
	}
}

func TestBadJSON(t *testing.T) {
	ts := setup(t)
	req, _ := http.NewRequest("POST", ts.srv.URL+"/v1/venues", strings.NewReader("{not json"))
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != 400 {
		t.Fatalf("bad json status=%d", resp.StatusCode)
	}
}

func TestUnknownPath(t *testing.T) {
	ts := setup(t)
	resp, err := http.Get(ts.srv.URL + "/v1/works/NOPE/timeline")
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != 404 {
		t.Fatalf("missing work status=%d", resp.StatusCode)
	}
}

func asString(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	if m, ok := v.(map[string]any); ok {
		if msg, ok := m["message"].(string); ok {
			return msg
		}
	}
	return ""
}
