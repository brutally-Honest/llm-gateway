package ui

import (
	"context"
	"fmt"
	"sync"
	"time"

	"go.uber.org/zap"

	"github.com/brutally-honest/llm-gateway/internal/logging"
)

// buildTimeout bounds one detached build, so a wedged store cannot hold a flight (and
// every request waiting on it) forever. It is far above storeTimeout on purpose: a
// build that outlives its request is the case the flight exists for.
const buildTimeout = 2 * time.Minute

// flight is one running build and the callers waiting on it.
type flight[V any] struct {
	done chan struct{}
	val  V
	err  error
}

// flightGroup runs at most one build per key at a time (a hand-written singleflight).
// A build runs on the group's own context, not a request's, so it finishes, and its
// caller can store the value, after the request that started it has timed out. A
// failed build is shared only by the callers waiting on it; the next call starts anew.
type flightGroup[K comparable, V any] struct {
	base context.Context
	log  *zap.Logger
	mu   sync.Mutex
	m    map[K]*flight[V]
}

func newFlightGroup[K comparable, V any](base context.Context, log *zap.Logger) *flightGroup[K, V] {
	if base == nil {
		base = context.Background()
	}
	if log == nil {
		log = zap.NewNop()
	}
	return &flightGroup[K, V]{base: base, log: log, m: map[K]*flight[V]{}}
}

// do returns fn's result for k, sharing one run among concurrent callers. It stops
// waiting when ctx ends, but the run goes on.
func (g *flightGroup[K, V]) do(ctx context.Context, k K, fn func(context.Context) (V, error)) (V, error) {
	g.mu.Lock()
	f, running := g.m[k]
	if !running {
		f = &flight[V]{done: make(chan struct{})}
		g.m[k] = f
		logging.Go(g.log, "ui_build", func() error { g.run(k, f, fn); return nil })
	}
	g.mu.Unlock()
	select {
	case <-f.done:
		return f.val, f.err
	case <-ctx.Done():
		var zero V
		return zero, ctx.Err()
	}
}

func (g *flightGroup[K, V]) run(k K, f *flight[V], fn func(context.Context) (V, error)) {
	ctx, cancel := context.WithTimeout(g.base, buildTimeout)
	defer cancel()
	defer func() {
		if r := recover(); r != nil {
			logging.LogPanic(g.log, "ui build panicked", r)
			f.err = fmt.Errorf("ui: build panicked: %v", r)
		}
		g.mu.Lock()
		delete(g.m, k)
		g.mu.Unlock()
		close(f.done)
	}()
	f.val, f.err = fn(ctx)
}
