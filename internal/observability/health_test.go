package observability_test

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/brutally-honest/llm-gateway/internal/observability"
)

func TestHealthOf(t *testing.T) {
	since := time.Date(2026, 11, 26, 10, 0, 0, 0, time.UTC)
	cases := []struct {
		name   string
		loss   observability.Loss
		status string
		lost   int64
		reason string
	}{
		{"ok", observability.Loss{}, "ok", 0, ""},
		{"hooks", observability.Loss{HookEventsDropped: 2}, "degraded", 2, "hook events dropped"},
		{"captures", observability.Loss{CapturesDropped: 3}, "degraded", 3, "captures dropped"},
		{"store", observability.Loss{StoreWriteFailures: 1}, "degraded", 1, "store writes failed"},
		{"blob", observability.Loss{BlobWriteFailures: 4}, "degraded", 4, "blob writes failed"},
		{"all", observability.Loss{1, 2, 3, 4}, "degraded", 10,
			"hook events dropped, captures dropped, store writes failed, blob writes failed"},
		{"order", observability.Loss{BlobWriteFailures: 1, HookEventsDropped: 1}, "degraded", 2,
			"hook events dropped, blob writes failed"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			h := observability.HealthOf(c.loss, since)
			if h.Status != c.status || h.Lost != c.lost || h.Reason != c.reason || !h.Since.Equal(since) {
				t.Fatalf("got %+v", h)
			}
			if c.loss.Total() != c.lost {
				t.Fatalf("Total = %d", c.loss.Total())
			}
		})
	}
}

func TestHealthJSONShape(t *testing.T) {
	since := time.Date(2026, 11, 26, 10, 0, 0, 0, time.UTC)
	b, _ := json.Marshal(observability.HealthOf(observability.Loss{}, since))
	want := `{"status":"ok","since":"2026-11-26T10:00:00Z","lost":0,"counts":{"hook_events_dropped":0,"captures_dropped":0,"store_write_failures":0,"blob_write_failures":0}}`
	if string(b) != want {
		t.Fatalf("got %s", b)
	}
}
