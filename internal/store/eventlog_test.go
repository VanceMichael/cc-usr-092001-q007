package store

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"example.com/batch-092001-q007/internal/domain"
)

func TestAppendReplayAndIdempotency(t *testing.T) {
	path := filepath.Join(t.TempDir(), "events.jsonl")
	var replayed []domain.Event
	log, err := Open(path, func(e domain.Event) error {
		replayed = append(replayed, e)
		return nil
	})
	if err != nil {
		t.Fatalf("open: %v", err)
	}

	evt := domain.Event{
		SchemaVersion: "1", EventID: "EVT-1", SourceID: "SCH-1", SourceSequence: 1,
		OccurredAt: time.Date(2026, 9, 21, 4, 0, 0, 0, time.UTC),
		SubjectRef: "ART-1", Type: domain.EvtArtworkRegistered, PayloadDigest: "sha256:x",
	}
	dup, err := log.Append(evt)
	if err != nil || dup {
		t.Fatalf("first append: dup=%v err=%v", dup, err)
	}
	// 同 event_id 再追加：返回 duplicate，不二次写盘。
	dup, err = log.Append(evt)
	if err != nil || !dup {
		t.Fatalf("second append: dup=%v err=%v", dup, err)
	}
	if err := log.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	// 重开：日志逐行重放，且 event_id 仍然去重。
	var again []domain.Event
	log2, err := Open(path, func(e domain.Event) error {
		again = append(again, e)
		return nil
	})
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer log2.Close()
	if len(again) != 1 || again[0].EventID != "EVT-1" {
		t.Fatalf("expected exactly one replayed event, got %+v", again)
	}
	if !log2.Has("EVT-1") {
		t.Fatalf("replayed event should be known")
	}
	dup, _ = log2.Append(evt)
	if !dup {
		t.Fatalf("append after replay should be duplicate")
	}
}

func TestReplayRejectsCorruptLine(t *testing.T) {
	path := filepath.Join(t.TempDir(), "events.jsonl")
	if err := os.WriteFile(path, []byte("{not json\n"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	if _, err := Open(path, func(domain.Event) error { return nil }); err == nil {
		t.Fatalf("corrupt log must fail startup replay")
	}
}

func TestInMemoryLogIsEphemeral(t *testing.T) {
	log, err := Open("", nil)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer log.Close()
	dup, err := log.Append(domain.Event{EventID: "EVT-MEM", Type: domain.EvtSlotPlanned})
	if err != nil || dup {
		t.Fatalf("mem append: dup=%v err=%v", dup, err)
	}
	snap, err := log.Snapshot()
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	if snap != nil {
		t.Fatalf("in-memory log should not expose file snapshot")
	}
	if !log.Has("EVT-MEM") {
		t.Fatalf("in-memory log still dedupes within process")
	}
}
