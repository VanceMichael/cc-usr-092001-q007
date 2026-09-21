package api_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"example.com/batch-092001-q007/internal/api"
	"example.com/batch-092001-q007/internal/clock"
	"example.com/batch-092001-q007/internal/service"
)

func TestHTTPConflictOnDoubleBooking(t *testing.T) {
	coord, err := service.New("", clock.Fixed{T: time.Date(2026, 9, 21, 4, 0, 0, 0, time.UTC)})
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	defer coord.Close()
	srv := httptest.NewServer(api.NewHandler(coord))
	defer srv.Close()

	post := func(t *testing.T, path string, body any) (int, map[string]any) {
		t.Helper()
		raw, _ := json.Marshal(body)
		resp, err := http.Post(srv.URL+path, "application/json", bytes.NewReader(raw))
		if err != nil {
			t.Fatalf("post: %v", err)
		}
		defer resp.Body.Close()
		var out map[string]any
		_ = json.NewDecoder(resp.Body).Decode(&out)
		return resp.StatusCode, out
	}

	must200 := func(t *testing.T, code int, where string) {
		t.Helper()
		if code != http.StatusOK {
			t.Fatalf("%s status = %d", where, code)
		}
	}

	c1, _ := post(t, "/v1/artworks", map[string]any{"event_id": "EVT-A1", "ref": "ART-1"})
	must200(t, c1, "artwork")
	c2, _ := post(t, "/v1/venues", map[string]any{
		"event_id": "EVT-V1", "ref": "VEN-PY", "country_code": "CN", "tz": "Asia/Shanghai",
	})
	must200(t, c2, "venue")
	c3, _ := post(t, "/v1/rights", map[string]any{
		"event_id": "EVT-G1", "ref": "RGR-1", "artwork_ref": "ART-1",
		"valid_from": "2026-09-20", "valid_to": "2026-09-30",
	})
	must200(t, c3, "rights")
	c4, _ := post(t, "/v1/pieces", map[string]any{
		"event_id": "EVT-P1", "ref": "PIECE-1", "artwork_ref": "ART-1", "media": "photo",
	})
	must200(t, c4, "piece")
	slot := map[string]any{
		"artwork_ref": "ART-1", "venue_ref": "VEN-PY",
		"start": "2026-09-25T10:00:00+08:00", "end": "2026-09-25T12:00:00+08:00",
	}
	first := map[string]any{}
	for k, v := range slot {
		first[k] = v
	}
	first["event_id"], first["ref"] = "EVT-S1", "S1"
	c5, _ := post(t, "/v1/slots", first)
	must200(t, c5, "first slot")

	second := map[string]any{}
	for k, v := range slot {
		second[k] = v
	}
	second["event_id"], second["ref"] = "EVT-S2", "S2"
	code2, body := post(t, "/v1/slots", second)
	if code2 != http.StatusConflict {
		t.Fatalf("overlapping same-artwork slot should be 409, got %d %v", code2, body)
	}
	errObj, _ := body["error"].(map[string]any)
	if errObj == nil || errObj["code"] != "conflict" {
		t.Fatalf("expected conflict error envelope, got %v", body)
	}

	// 同 event_id 重放：幂等 200。
	code3, _ := post(t, "/v1/slots", first)
	if code3 != http.StatusOK {
		t.Fatalf("idempotent replay should be 200, got %d", code3)
	}
}

func TestHTTPTrackAndBrief(t *testing.T) {
	coord, err := service.New("", clock.Fixed{T: time.Date(2026, 9, 21, 4, 0, 0, 0, time.UTC)})
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	defer coord.Close()
	srv := httptest.NewServer(api.NewHandler(coord))
	defer srv.Close()

	post := func(path string, body any) {
		raw, _ := json.Marshal(body)
		resp, err := http.Post(srv.URL+path, "application/json", bytes.NewReader(raw))
		if err != nil {
			t.Fatalf("post %s: %v", path, err)
		}
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("post %s status %d", path, resp.StatusCode)
		}
		resp.Body.Close()
	}
	post("/v1/artworks", map[string]any{"event_id": "EVT-A1", "ref": "ART-1"})
	post("/v1/venues", map[string]any{"event_id": "EVT-V1", "ref": "VEN-PY", "country_code": "CN", "tz": "Asia/Shanghai"})
	post("/v1/rights", map[string]any{"event_id": "EVT-G1", "ref": "RGR-1", "artwork_ref": "ART-1",
		"valid_from": "2026-09-20", "valid_to": "2026-09-30"})
	post("/v1/pieces", map[string]any{"event_id": "EVT-P1", "ref": "PIECE-1", "artwork_ref": "ART-1", "media": "photo"})
	post("/v1/slots", map[string]any{
		"event_id": "EVT-S1", "ref": "S1", "artwork_ref": "ART-1", "venue_ref": "VEN-PY",
		"start": "2026-09-25T10:00:00+08:00", "end": "2026-09-25T12:00:00+08:00",
	})
	post("/v1/movements", map[string]any{
		"event_id": "EVT-M1", "ref": "MV-1", "artwork_ref": "ART-1", "slot_ref": "S1",
		"kind": "shipped", "occurred_at": "2026-09-24T09:00:00+08:00",
	})

	resp, err := http.Get(srv.URL + "/v1/artworks/ART-1/track")
	if err != nil {
		t.Fatalf("get track: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("track status %d", resp.StatusCode)
	}
	var track struct {
		Track []map[string]any `json:"track"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&track); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(track.Track) != 2 {
		t.Fatalf("expected 2 track entries, got %d", len(track.Track))
	}

	resp2, err := http.Get(srv.URL + "/v1/venues/VEN-PY/brief?start=2026-09-25T00:00:00%2B08:00&end=2026-09-26T00:00:00%2B08:00")
	if err != nil {
		t.Fatalf("get brief: %v", err)
	}
	defer resp2.Body.Close()
	if resp2.StatusCode != http.StatusOK {
		t.Fatalf("brief status %d", resp2.StatusCode)
	}
}
