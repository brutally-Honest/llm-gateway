package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/brutally-honest/llm-gateway/internal/startup"
)

// runGateway runs the gateway. Under its own login service a failed start is slowed
// down so KeepAlive (launchd) or Restart=on-failure (systemd) cannot turn it into a
// restart a second: the start's log lines are held until it serves, and if it fails
// first they are written at most once per startup.LogEvery for the same error, the
// failure is recorded for doctor, and the process waits startup.Wait before it exits
// (spec 025 AC5, research D7). A clean start and stop, and any run outside a service,
// are as before.
func runGateway(ctx context.Context, d deps) int {
	getpid := d.getpid
	if getpid == nil {
		getpid = os.Getpid
	}
	if !startup.UnderService(d.lookupEnv, getpid()) {
		return serve(ctx, d)
	}
	gate := &startGate{out: d.stdout}
	path, havePath := startup.Path(d.lookupEnv)
	d.stdout = gate
	d.started = func() {
		gate.release()
		if havePath {
			_ = startup.Clear(path)
		}
	}
	code := serve(ctx, d)
	held, holding := gate.close()
	if code == exitOK || !holding {
		return code // served (or stopped before serving): every line is already out
	}
	if ctx.Err() != nil {
		gate.write(held) // stopped by a signal while starting: not a crash loop
		return code
	}
	now := time.Now
	if d.now != nil {
		now = d.now
	}
	text, key := startupError(held)
	var rec startup.Record
	if havePath {
		rec, _ = startup.Read(path)
	}
	t := now()
	if rec.Key != key || t.Sub(rec.LoggedAt) >= startup.LogEvery || !havePath {
		gate.write(held)
		rec.LoggedAt = t
	}
	rec.Error, rec.Key, rec.FailedAt = text, key, t
	if havePath {
		_ = startup.Write(path, rec)
	}
	sleep := d.sleep
	if sleep == nil {
		sleep = sleepCtx
	}
	sleep(ctx, startup.Wait)
	return code
}

// sleepCtx waits for d, or until ctx is done: a stop during the wait exits at once.
func sleepCtx(ctx context.Context, d time.Duration) {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
	case <-t.C:
	}
}

// startupError is the held output's error as one line of text, and a key that is the
// same for the same error on every start: the error lines without their time and
// caller. Output with no JSON error line (a flag error is plain text) is keyed by its
// first line.
func startupError(out []byte) (text, key string) {
	var texts, keys []string
	for line := range strings.SplitSeq(strings.TrimSpace(string(out)), "\n") {
		var m map[string]any
		if json.Unmarshal([]byte(line), &m) != nil || m["level"] != "error" {
			continue
		}
		delete(m, "ts")
		delete(m, "caller")
		k, _ := json.Marshal(m) // map keys marshal sorted
		keys = append(keys, string(k))
		texts = append(texts, describe(m))
	}
	if len(keys) == 0 {
		first, _, _ := strings.Cut(strings.TrimSpace(string(out)), "\n")
		return first, first
	}
	return strings.Join(texts, "; "), strings.Join(keys, "\n")
}

// describe is an error line as text: its message, then its fields in key order.
func describe(m map[string]any) string {
	msg, _ := m["msg"].(string)
	var names []string
	for k := range m {
		if k != "msg" && k != "level" && k != "logger" {
			names = append(names, k)
		}
	}
	slices.Sort(names)
	var parts []string
	for _, k := range names {
		parts = append(parts, fmt.Sprintf("%s=%v", k, m[k]))
	}
	if len(parts) == 0 {
		return msg
	}
	return msg + " (" + strings.Join(parts, ", ") + ")"
}

// startGate holds what the gateway writes while it starts, until release lets it
// through. A start that writes more than maxHeld is let through at once: the hold is
// for the few lines of a failed start, never for a long one.
type startGate struct {
	mu       sync.Mutex
	out      io.Writer
	held     bytes.Buffer
	released bool
}

const maxHeld = 1 << 20

func (g *startGate) Write(p []byte) (int, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.released {
		return g.out.Write(p)
	}
	g.held.Write(p)
	if g.held.Len() > maxHeld {
		g.flushLocked()
	}
	return len(p), nil
}

func (g *startGate) release() {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.flushLocked()
}

func (g *startGate) flushLocked() {
	if !g.released {
		_, _ = g.out.Write(g.held.Bytes())
		g.held.Reset()
		g.released = true
	}
}

// close ends the hold and returns what is still held, and false if the gate was already
// released (the gateway served); later writes go straight through.
func (g *startGate) close() ([]byte, bool) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.released {
		return nil, false
	}
	g.released = true
	return bytes.Clone(g.held.Bytes()), true
}

// write writes held output that close returned.
func (g *startGate) write(p []byte) { _, _ = g.out.Write(p) }
