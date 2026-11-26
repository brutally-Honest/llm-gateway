package observability

import (
	"strings"
	"time"
)

// Loss is what capture has lost since the process started (spec 045). Each field is a
// count read from the same atomics the Prometheus counters read, so a scrape and a
// health read agree.
type Loss struct {
	// HookEventsDropped is hook deliveries dropped: queue full, memory limit, body over
	// limit, body unreadable. Capture-off deliveries are not a loss.
	HookEventsDropped int64
	// CapturesDropped is exchanges and network effects dropped before storage.
	CapturesDropped int64
	// StoreWriteFailures is data writes the store failed.
	StoreWriteFailures int64
	// BlobWriteFailures is blob files the store could not write.
	BlobWriteFailures int64
}

// Total is the sum of the four counts.
func (l Loss) Total() int64 {
	return l.HookEventsDropped + l.CapturesDropped + l.StoreWriteFailures + l.BlobWriteFailures
}

// LossCounts is Loss as it is written in JSON.
type LossCounts struct {
	HookEventsDropped  int64 `json:"hook_events_dropped"`
	CapturesDropped    int64 `json:"captures_dropped"`
	StoreWriteFailures int64 `json:"store_write_failures"`
	BlobWriteFailures  int64 `json:"blob_write_failures"`
}

// Health is the body of GET /healthz and the fields of GET /api/health.
type Health struct {
	// Status is "ok", or "degraded" when anything was lost since Since.
	Status string `json:"status"`
	// Since is the process start; the counts are since then.
	Since  time.Time  `json:"since"`
	Lost   int64      `json:"lost"`
	Counts LossCounts `json:"counts"`
	// Reason names the kinds of loss, only when degraded.
	Reason string `json:"reason,omitempty"`
}

// HealthOf builds the Health for l. Degraded is sticky for the process: the counts only
// grow, so a loss stays visible until the gateway restarts.
func HealthOf(l Loss, since time.Time) Health {
	h := Health{
		Status: "ok",
		Since:  since.UTC(),
		Lost:   l.Total(),
		Counts: LossCounts(l),
	}
	if h.Lost == 0 {
		return h
	}
	h.Status = "degraded"
	var kinds []string
	for _, k := range []struct {
		n    int64
		name string
	}{
		{l.HookEventsDropped, "hook events dropped"},
		{l.CapturesDropped, "captures dropped"},
		{l.StoreWriteFailures, "store writes failed"},
		{l.BlobWriteFailures, "blob writes failed"},
	} {
		if k.n > 0 {
			kinds = append(kinds, k.name)
		}
	}
	h.Reason = strings.Join(kinds, ", ")
	return h
}
