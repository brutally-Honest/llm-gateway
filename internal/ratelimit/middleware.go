package ratelimit

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"go.uber.org/zap"

	"github.com/brutally-honest/llm-gateway/internal/core"
)

const gatewayKeyHeader = "X-Gateway-Key"

// Reasons logged with ratelimit_rejected.
const (
	reasonMissingKey = "missing_key"
	reasonUnknownKey = "unknown_key"
)

// Middleware gates the model calls of adapter a through l. A request that is not a model
// call, or an adapter that cannot tell, passes untouched. A model call needs a listed
// gateway key (403 otherwise) and room in its window (429 otherwise); neither is
// forwarded. An admitted call reports its tokens back to l when it settles.
func Middleware(l Limiter, a core.Adapter, log *zap.Logger) func(http.Handler) http.Handler {
	return middleware(l, a, log, time.Now)
}

func middleware(l Limiter, a core.Adapter, log *zap.Logger, clock func() time.Time) func(http.Handler) http.Handler {
	ra, ok := a.(core.RateLimitAdapter)
	return func(next http.Handler) http.Handler {
		if !ok {
			return next
		}
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			path := strings.TrimPrefix(r.URL.Path, a.Prefix())
			if !ra.IsModelCall(r.Method, path) {
				next.ServeHTTP(w, r)
				return
			}
			g := &gate{l: l, ra: ra, log: log, clock: clock, w: w, r: r}
			if g.admit() {
				next.ServeHTTP(w, r)
			}
		})
	}
}

type gate struct {
	l     Limiter
	ra    core.RateLimitAdapter
	log   *zap.Logger
	clock func() time.Time
	w     http.ResponseWriter
	r     *http.Request
}

// admit reports whether the call may go on; if not, the answer is already written.
func (g *gate) admit() bool {
	key := g.r.Header.Get(gatewayKeyHeader)
	if key == "" {
		g.deny(key, reasonMissingKey)
		return false
	}
	now := g.clock()
	d := g.l.Admit(key, now)
	switch d.Outcome {
	case Admitted:
		g.watchUsage(key)
		return true
	case Limited:
		g.limited(key, d, now)
		return false
	default:
		g.deny(key, reasonUnknownKey)
		return false
	}
}

// watchUsage asks core to report the response's usage, and records the tokens.
func (g *gate) watchUsage(key string) {
	m := core.MetaFrom(g.r.Context())
	if m == nil {
		return
	}
	reqID := m.RequestID
	m.OnUsage = func(u core.UsageResult) {
		if u.OK {
			g.l.Record(key, u.Tokens, g.clock())
			return
		}
		if u.Status >= 200 && u.Status < 300 {
			g.log.Warn("usage_missing", zap.String("request_id", reqID), zap.String("key", key))
		}
	}
}

func (g *gate) deny(key, reason string) {
	msg := "the gateway key header x-gateway-key is missing or not configured"
	ct, body := g.ra.LimitErrorBody(core.LimitPermission, msg)
	g.write(http.StatusForbidden, ct, body)
	g.logRejected(key, reason, 0)
}

func (g *gate) limited(key string, d Decision, now time.Time) {
	secs := retryAfterSeconds(d.Reset, now)
	msg := fmt.Sprintf("rate limit reached for key %q: %d %s per minute; retry in %d seconds", key, d.Limit, d.Dimension, secs)
	ct, body := g.ra.LimitErrorBody(core.LimitRateLimit, msg)
	h := g.w.Header()
	h.Set("Retry-After", strconv.FormatInt(secs, 10))
	h.Set("X-Gateway-Ratelimit", d.Dimension)
	h.Set("X-Gateway-Ratelimit-Limit", strconv.FormatInt(d.Limit, 10))
	h.Set("X-Gateway-Ratelimit-Remaining", strconv.FormatInt(max(d.Remaining, 0), 10))
	h.Set("X-Gateway-Ratelimit-Reset", d.Reset.UTC().Format(time.RFC3339))
	g.write(http.StatusTooManyRequests, ct, body)
	g.logRejected(key, d.Dimension, secs)
}

func (g *gate) write(status int, contentType string, body []byte) {
	h := g.w.Header()
	h.Set("Content-Type", contentType)
	h.Set("Content-Length", strconv.Itoa(len(body)))
	g.w.WriteHeader(status)
	_, _ = g.w.Write(body)
}

func (g *gate) logRejected(key, reason string, retryAfter int64) {
	reqID := ""
	if m := core.MetaFrom(g.r.Context()); m != nil {
		reqID = m.RequestID
	}
	fields := []zap.Field{zap.String("request_id", reqID), zap.String("key", key), zap.String("reason", reason)}
	if key == "" {
		fields[1] = zap.String("key", "absent")
	}
	if retryAfter > 0 {
		fields = append(fields, zap.Int64("retry_after", retryAfter))
	}
	g.log.Info("ratelimit_rejected", fields...)
}

// retryAfterSeconds is the whole seconds from now to reset, rounded up, at least 1.
func retryAfterSeconds(reset, now time.Time) int64 {
	d := reset.Sub(now)
	secs := int64((d + time.Second - 1) / time.Second)
	if d <= 0 || secs < 1 {
		return 1
	}
	return secs
}
