package ui

import (
	"context"
	"sync"
	"time"

	"go.uber.org/zap"

	"github.com/brutally-honest/llm-gateway/internal/logging"
)

// buildTimeout bounds one detached build or digest compute, so a wedged store cannot
// hold a session's builds (and every request waiting on them) forever. It is far above
// storeTimeout on purpose: a build that outlives its request is the case it exists for.
const buildTimeout = 2 * time.Minute

// flight counts the background goroutines a cache started, so shutdown can wait for them
// (spec 047 F1). They read the store through the reader, so a reader closed under one would
// leave a file behind. Its zero value is ready.
type flight struct{ wg sync.WaitGroup }

// begin counts one goroutine; its caller calls end when it returns. A goroutine started
// from inside a counted one is counted before its parent ends, so the count never rises
// from zero while Wait runs, provided no request is still being served.
func (f *flight) begin() { f.wg.Add(1) }
func (f *flight) end()   { f.wg.Done() }

// Wait returns true when every counted goroutine has returned, or false when ctx ends
// first. Cancel the cache's base context first: a goroutine mid-build stops at its next
// check, a finished one is not undone.
func (f *flight) Wait(ctx context.Context) bool {
	done := make(chan struct{})
	logging.Go(zap.NewNop(), "ui_flight_wait", func() error { f.wg.Wait(); close(done); return nil })
	select {
	case <-done:
		return true
	case <-ctx.Done():
		return false
	}
}
