package ratelimit

import (
	"sync"
	"time"

	"github.com/brutally-honest/llm-gateway/internal/config"
)

const windowSeconds = 60

// fixedWindow keeps per-key counts in memory. The key map is built once and read-only
// afterwards, so state is bounded by configured keys and an unknown key creates none.
type fixedWindow struct {
	entries map[string]*entry
}

type entry struct {
	rpm, tpm int64

	mu     sync.Mutex
	window int64
	reqs   int64
	toks   int64
}

// NewFixedWindowMemory returns the fixed-window in-memory limiter for cfg.Keys. The
// caller supplies time on every call, so tests control the clock.
func NewFixedWindowMemory(cfg config.RateLimit) Limiter {
	m := make(map[string]*entry, len(cfg.Keys))
	for k, l := range cfg.Keys {
		m[k] = &entry{rpm: l.RPM, tpm: l.TPM, window: -1}
	}
	return &fixedWindow{entries: m}
}

// roll resets the counts if now is in a newer window. e.mu must be held.
func (e *entry) roll(now time.Time) {
	if w := now.Unix() / windowSeconds; w > e.window {
		e.window, e.reqs, e.toks = w, 0, 0
	}
}

func (e *entry) reset() time.Time {
	return time.Unix((e.window+1)*windowSeconds, 0).UTC()
}

func clamp(n int64) int64 {
	if n < 0 {
		return 0
	}
	return n
}

func (f *fixedWindow) Admit(key string, now time.Time) Decision {
	e, ok := f.entries[key]
	if !ok {
		return Decision{Outcome: UnknownKey}
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	e.roll(now)
	reset := e.reset()
	if e.rpm > 0 && e.reqs >= e.rpm {
		return Decision{Outcome: Limited, Dimension: DimRequests, Limit: e.rpm, Remaining: clamp(e.rpm - e.reqs), Reset: reset}
	}
	if e.tpm > 0 && e.toks >= e.tpm {
		return Decision{Outcome: Limited, Dimension: DimTokens, Limit: e.tpm, Remaining: clamp(e.tpm - e.toks), Reset: reset}
	}
	e.reqs++
	return Decision{Outcome: Admitted, Reset: reset}
}

func (f *fixedWindow) Record(key string, tokens int64, now time.Time) {
	e, ok := f.entries[key]
	if !ok || tokens <= 0 {
		return
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	e.roll(now)
	e.toks += tokens
}
