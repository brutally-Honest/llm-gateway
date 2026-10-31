// Package ratelimit limits model calls per gateway key. It knows nothing of HTTP.
package ratelimit

import (
	"fmt"
	"time"

	"github.com/brutally-honest/llm-gateway/internal/config"
)

// Outcome is what Admit decided.
type Outcome int

const (
	Admitted Outcome = iota
	UnknownKey
	Limited
)

// Dimensions a Decision can name.
const (
	DimRequests = "requests"
	DimTokens   = "tokens"
)

// Decision is the result of Admit. For Limited, Dimension, Limit, Remaining and Reset
// describe the exhausted limit; Remaining is clamped at 0. For Admitted, Reset is the
// end of the current window.
type Decision struct {
	Outcome   Outcome
	Dimension string
	Limit     int64
	Remaining int64
	Reset     time.Time
}

// Limiter admits or rejects calls per gateway key.
type Limiter interface {
	// Admit decides whether key may make a call now, and counts it if so.
	Admit(key string, now time.Time) Decision
	// Record adds tokens used by a finished call to key's current window.
	// An unknown key is ignored.
	Record(key string, tokens int64, now time.Time)
}

// New returns the limiter named by cfg.Strategy.
func New(cfg config.RateLimit) (Limiter, error) {
	switch cfg.Strategy {
	case config.StrategyFixedWindowMemory:
		return NewFixedWindowMemory(cfg), nil
	default:
		return nil, fmt.Errorf("ratelimit: unknown strategy %q", cfg.Strategy)
	}
}
