package observability

import (
	"context"
	"errors"
	"testing"
	"time"

	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"
)

// downExporter fails while down is true.
type downExporter struct{ down bool }

func (d *downExporter) ExportSpans(context.Context, []sdktrace.ReadOnlySpan) error {
	if d.down {
		return errors.New("dial tcp 127.0.0.1:4318: connect: connection refused")
	}
	return nil
}
func (d *downExporter) Shutdown(context.Context) error { return nil }

// Spec 021 research D16: a collector that stays down is logged once at once, then at most
// once a minute with the failed batch count, and once more when it is back.
func TestQuietExporter_RateLimitsFailures(t *testing.T) {
	core, logs := observer.New(zapcore.InfoLevel)
	clock := time.Unix(0, 0)
	inner := &downExporter{down: true}
	e := newQuietExporter(inner, zap.New(core), time.Minute, func() time.Time { return clock })
	export := func() {
		if err := e.ExportSpans(context.Background(), nil); err != nil {
			t.Fatalf("ExportSpans = %v, want nil (failures are logged, not returned)", err)
		}
	}
	// 3 minutes of failed batches every 5 s: 36 batches.
	for range 36 {
		export()
		clock = clock.Add(5 * time.Second)
	}
	warns := logs.FilterMessage("trace export failed").All()
	if len(warns) != 3 {
		t.Fatalf("warnings over 3 minutes = %d, want 3 (one at once, then one a minute)", len(warns))
	}
	if got := warns[1].ContextMap()["failed_batches"]; got != int64(13) {
		t.Errorf("second warning failed_batches = %v, want 13", got)
	}
	inner.down = false
	export()
	export()
	rec := logs.FilterMessage("trace export recovered").All()
	if len(rec) != 1 || rec[0].ContextMap()["failed_batches"] != int64(36) {
		t.Errorf("recovered lines = %v, want one with failed_batches 36", rec)
	}
	// Down again: the first failure is logged at once, not held back by the last warning.
	inner.down = true
	export()
	if n := len(logs.FilterMessage("trace export failed").All()); n != 4 {
		t.Errorf("warnings after a new outage = %d, want 4", n)
	}
}
