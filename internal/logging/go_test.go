package logging

import (
	"bytes"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"
)

// syncBuffer is a bytes.Buffer safe to write from the helper's goroutine and read from
// the test's.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// wait returns what ch delivers, failing the test if nothing arrives in time.
func wait(t *testing.T, ch <-chan error) error {
	t.Helper()
	select {
	case err := <-ch:
		return err
	case <-time.After(5 * time.Second):
		t.Fatal("Go delivered nothing within 5s")
		return nil
	}
}

type sentinelPanic struct{ secret string }

func TestGo_RecoversAndLogsJSON(t *testing.T) {
	const sentinel = "sk-sentinel-go-panic-value"
	var out, errOut syncBuffer
	log := newLogger(&out, &errOut, "info")

	ch := Go(log, "worker", func() error {
		panic(sentinelPanic{secret: sentinel})
	})
	if err := wait(t, ch); !errors.Is(err, ErrPanicked) {
		t.Fatalf("Go delivered %v, want ErrPanicked", err)
	}

	// Reaching this line is the proof the test process kept running.
	got := lines(t, out.String())
	if len(got) != 1 {
		t.Fatalf("got %d lines, want exactly 1\n%s", len(got), out.String())
	}
	line := got[0]
	if line["level"] != "error" {
		t.Errorf("level = %v, want error", line["level"])
	}
	if line["goroutine"] != "worker" {
		t.Errorf("goroutine = %v, want worker", line["goroutine"])
	}
	if line["panic_type"] != "logging.sentinelPanic" {
		t.Errorf("panic_type = %v, want logging.sentinelPanic", line["panic_type"])
	}
	if stack, _ := line["stack"].(string); !strings.Contains(stack, "TestGo_RecoversAndLogsJSON") {
		t.Errorf("stack does not show the panicking function:\n%s", stack)
	}
	if _, ok := line["panic"]; ok {
		t.Errorf("line has a panic value field: %v", line)
	}
	for name, s := range map[string]string{"stdout": out.String(), "stderr": errOut.String()} {
		if strings.Contains(s, sentinel) {
			t.Errorf("sentinel panic value appears on %s:\n%s", name, s)
		}
	}
	if errOut.String() != "" {
		t.Errorf("stderr is not empty: %q", errOut.String())
	}
}

func TestGo_DeliversResult(t *testing.T) {
	var out syncBuffer
	log := newLogger(&out, &out, "info")

	if err := wait(t, Go(log, "ok", func() error { return nil })); err != nil {
		t.Errorf("nil result delivered as %v", err)
	}
	want := errors.New("serve failed")
	if err := wait(t, Go(log, "fails", func() error { return want })); !errors.Is(err, want) {
		t.Errorf("delivered %v, want %v", err, want)
	}
	if out.String() != "" {
		t.Errorf("a goroutine that returned logged something: %s", out.String())
	}
}

func TestGo_ChannelIsBuffered(t *testing.T) {
	var out syncBuffer
	log := newLogger(&out, &out, "info")

	done := make(chan struct{})
	ch := Go(log, "unread", func() error {
		defer close(done)
		return nil
	})
	if cap(ch) < 1 {
		t.Fatalf("channel capacity = %d, want at least 1 so the goroutine never blocks", cap(ch))
	}
	<-done
	_ = wait(t, ch)
}
