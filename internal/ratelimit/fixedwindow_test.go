package ratelimit

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/brutally-honest/llm-gateway/internal/config"
)

func cfgWith(rpm, tpm int64) config.RateLimit {
	return config.RateLimit{Enabled: true, Strategy: config.StrategyFixedWindowMemory,
		Keys: map[string]config.KeyLimit{"k": {RPM: rpm, TPM: tpm}}}
}

var t0 = time.Unix(6000, 0).UTC()

func TestFixedWindow_RejectsOverRPM(t *testing.T) {
	l := NewFixedWindowMemory(cfgWith(2, 0))
	for i := 0; i < 2; i++ {
		if d := l.Admit("k", t0); d.Outcome != Admitted {
			t.Fatalf("call %d: %+v", i, d)
		}
	}
	d := l.Admit("k", t0.Add(5*time.Second))
	if d.Outcome != Limited || d.Dimension != DimRequests || d.Limit != 2 || d.Remaining != 0 || !d.Reset.Equal(t0.Add(time.Minute)) {
		t.Fatalf("got %+v", d)
	}
}

func TestFixedWindow_RejectsAtTPM(t *testing.T) {
	l := NewFixedWindowMemory(cfgWith(0, 100))
	l.Admit("k", t0)
	l.Record("k", 100, t0)
	d := l.Admit("k", t0)
	if d.Outcome != Limited || d.Dimension != DimTokens || d.Limit != 100 {
		t.Fatalf("got %+v", d)
	}
}

func TestFixedWindow_NewWindowResetsCounts(t *testing.T) {
	l := NewFixedWindowMemory(cfgWith(1, 10))
	l.Admit("k", t0)
	l.Record("k", 10, t0)
	if l.Admit("k", t0).Outcome != Limited {
		t.Fatal("want limited")
	}
	if d := l.Admit("k", t0.Add(time.Minute)); d.Outcome != Admitted {
		t.Fatalf("got %+v", d)
	}
}

func TestFixedWindow_ZeroLimitUnlimited(t *testing.T) {
	l := NewFixedWindowMemory(cfgWith(0, 0))
	for i := 0; i < 1000; i++ {
		if l.Admit("k", t0).Outcome != Admitted {
			t.Fatal("limited")
		}
		l.Record("k", 1_000_000, t0)
	}
}

func TestFixedWindow_ConcurrentBurstAdmitsExactlyRemaining(t *testing.T) {
	l := NewFixedWindowMemory(cfgWith(10, 0))
	for i := 0; i < 4; i++ {
		l.Admit("k", t0)
	}
	var admitted, limited atomic.Int64
	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if l.Admit("k", t0).Outcome == Admitted {
				admitted.Add(1)
			} else {
				limited.Add(1)
			}
		}()
	}
	wg.Wait()
	if admitted.Load() != 6 || limited.Load() != 94 {
		t.Fatalf("admitted %d limited %d", admitted.Load(), limited.Load())
	}
}

func TestFixedWindow_OvershootRejectsUntilWindowEnds(t *testing.T) {
	l := NewFixedWindowMemory(cfgWith(0, 100))
	l.Record("k", 99, t0)
	if l.Admit("k", t0).Outcome != Admitted {
		t.Fatal("want admitted")
	}
	l.Record("k", 50_000, t0)
	d := l.Admit("k", t0.Add(30*time.Second))
	if d.Outcome != Limited || d.Remaining != 0 {
		t.Fatalf("got %+v", d)
	}
	if l.Admit("k", t0.Add(time.Minute)).Outcome != Admitted {
		t.Fatal("want admitted next window")
	}
}

func TestFixedWindow_StreamEndingInNextWindowCountsThere(t *testing.T) {
	l := NewFixedWindowMemory(cfgWith(0, 100))
	l.Admit("k", t0)
	l.Record("k", 100, t0.Add(61*time.Second))
	if l.Admit("k", t0.Add(30*time.Second+time.Minute)).Outcome != Limited {
		t.Fatal("tokens should count in the later window")
	}
	if l.Admit("k", t0.Add(2*time.Minute)).Outcome != Admitted {
		t.Fatal("want admitted after")
	}
}

func TestFixedWindow_UnknownKeyCreatesNoState(t *testing.T) {
	l := NewFixedWindowMemory(cfgWith(1, 1)).(*fixedWindow)
	if d := l.Admit("nope", t0); d.Outcome != UnknownKey {
		t.Fatalf("got %+v", d)
	}
	l.Record("nope", 5, t0)
	if len(l.entries) != 1 {
		t.Fatalf("entries grew: %d", len(l.entries))
	}
}

func TestNew_SelectsStrategy(t *testing.T) {
	if _, err := New(cfgWith(1, 1)); err != nil {
		t.Fatal(err)
	}
	if _, err := New(config.RateLimit{Strategy: "x"}); err == nil {
		t.Fatal("want error")
	}
}
