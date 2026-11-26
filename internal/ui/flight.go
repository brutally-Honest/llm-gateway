package ui

import (
	"context"
	"errors"
	"sync"
	"time"
)

// buildTimeout bounds one detached build or digest compute, so a wedged store cannot
// hold a session's builds (and every request waiting on them) forever. It is far above
// storeTimeout on purpose: a build that outlives its request is the case it exists for.
const buildTimeout = 2 * time.Minute

// errStopped ends a build that shutdown refused to start.
var errStopped = errors.New("ui: shutting down")

// flight counts the background goroutines a cache started, so shutdown can wait for them
// (spec 047 F1). They read the store through the reader, so a reader closed under one would
// leave a file behind. Its zero value is ready.
//
// Wait puts it in a stopping state first (spec 047 D16): begin then returns false and the
// caller starts nothing, so a handler still running after the shutdown's fallback close
// cannot add to the count while, or after, Wait runs.
type flight struct {
	mu       sync.Mutex
	n        int
	stopping bool
	idle     chan struct{} // closed when n is 0 and stopping; nil until Wait
}

// begin counts one goroutine and returns true; its caller calls end when it returns. It
// returns false once Wait has started, and then the caller must not start the goroutine.
func (f *flight) begin() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.stopping {
		return false
	}
	f.n++
	return true
}

func (f *flight) end() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.n--
	if f.n == 0 && f.idle != nil {
		close(f.idle)
		f.idle = nil
	}
}

// Wait stops new goroutines from being counted, then returns true when every counted one
// has returned, or false when ctx ends first. Cancel the cache's base context first: a
// goroutine mid-build stops at its next check, a finished one is not undone. With nothing
// counted it returns true at once, whatever ctx says.
func (f *flight) Wait(ctx context.Context) bool {
	f.mu.Lock()
	f.stopping = true
	if f.n == 0 {
		f.mu.Unlock()
		return true
	}
	if f.idle == nil {
		f.idle = make(chan struct{})
	}
	idle := f.idle
	f.mu.Unlock()
	select {
	case <-idle:
		return true
	case <-ctx.Done():
		return false
	}
}
