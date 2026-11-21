package logging

import (
	"bytes"
	"errors"
	"testing"

	"go.uber.org/zap"
)

// Plain writes one text line per entry with its fields in order, at the given level, and
// never JSON (spec 024 AC10).
func TestPlain_TextLines(t *testing.T) {
	var b bytes.Buffer
	l := Plain(&b, "gateway dump", "info").With(zap.String("path", "/x"))
	l.Debug("hidden")
	l.Error("dump failed", zap.String("reason", "store not found"), zap.Int("count", 2), zap.Error(errors.New("boom")))
	want := "gateway dump: dump failed (path: /x, reason: store not found, count: 2, error: boom)\n"
	if b.String() != want {
		t.Fatalf("got %q, want %q", b.String(), want)
	}
	b.Reset()
	Plain(&b, "gateway", "info").Info("usage")
	if b.String() != "gateway: usage\n" {
		t.Fatalf("got %q", b.String())
	}
}
