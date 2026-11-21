package main

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/brutally-honest/llm-gateway/internal/startup"
)

// backoff is a fake clock and sleep for the crash-loop backoff (spec 025 AC5).
type backoff struct {
	mu     sync.Mutex
	at     time.Time
	sleeps []time.Duration
}

func newBackoff() *backoff {
	return &backoff{at: time.Date(2026, 11, 21, 8, 0, 0, 0, time.UTC)}
}

func (b *backoff) now() time.Time { b.mu.Lock(); defer b.mu.Unlock(); return b.at }

func (b *backoff) sleep(_ context.Context, d time.Duration) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.sleeps = append(b.sleeps, d)
}

func (b *backoff) advance(d time.Duration) { b.mu.Lock(); defer b.mu.Unlock(); b.at = b.at.Add(d) }

func (b *backoff) slept() []time.Duration {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]time.Duration(nil), b.sleeps...)
}

// systemdEnv is the environment systemd gives the gateway's own unit: INVOCATION_ID and
// SYSTEMD_EXEC_PID set to this process, with the startup record under state.
func systemdEnv(state string, extra map[string]string) map[string]string {
	env := map[string]string{
		"INVOCATION_ID":    "0123456789abcdef",
		"SYSTEMD_EXEC_PID": strconv.Itoa(os.Getpid()),
		"XDG_STATE_HOME":   state,
	}
	for k, v := range extra {
		env[k] = v
	}
	return env
}

// badListen makes the start fail before bind with an invalid config line.
var badListen = map[string]string{"GATEWAY_LISTEN_ADDR": "not-an-address"}

func recordAt(t *testing.T, state string) startup.Record {
	t.Helper()
	r, err := startup.Read(filepath.Join(state, "llm-gateway", "startup-error.json"))
	if err != nil {
		t.Fatal(err)
	}
	return r
}

// Spec 025 AC5: a start that fails under the gateway's own service waits startup.Wait
// before it exits, logs the error, and records it for doctor.
func TestRun_ServiceStartFailureWaits(t *testing.T) {
	inTempDir(t, nil)
	state, b := t.TempDir(), newBackoff()
	g := startGateway(t, options{env: systemdEnv(state, badListen), now: b.now, sleep: b.sleep})
	line := failsBeforeBind(t, g, exitConfig)
	if line["msg"] != "invalid config" || line["key"] != "listen_addr" {
		t.Errorf("line = %v", line)
	}
	if got := b.slept(); len(got) != 1 || got[0] != startup.Wait {
		t.Fatalf("slept %v, want one %v", got, startup.Wait)
	}
	r := recordAt(t, state)
	if !strings.Contains(r.Error, "invalid config") || !strings.Contains(r.Error, "key=listen_addr") ||
		!r.FailedAt.Equal(b.now()) || !r.LoggedAt.Equal(b.now()) {
		t.Fatalf("record = %+v", r)
	}
}

// Spec 025 AC5: the same startup error is logged at most once per startup.LogEvery; every
// failed start still waits and updates the record. A different error is logged at once.
func TestRun_ServiceStartFailureLogsOncePerInterval(t *testing.T) {
	inTempDir(t, nil)
	state, b := t.TempDir(), newBackoff()
	start := func(extra map[string]string) *gateway {
		g := startGateway(t, options{env: systemdEnv(state, extra), now: b.now, sleep: b.sleep})
		if code := g.wait(); code == exitOK {
			t.Fatalf("exit %d, want a failed start", code)
		}
		return g
	}
	first := b.now()
	start(badListen)
	b.advance(time.Minute)
	if g := start(badListen); g.stdout.String() != "" {
		t.Fatalf("the same error logged again within %v:\n%s", startup.LogEvery, g.stdout)
	}
	r := recordAt(t, state)
	if !r.LoggedAt.Equal(first) || !r.FailedAt.Equal(b.now()) {
		t.Fatalf("record = %+v, want logged at %v, failed at %v", r, first, b.now())
	}
	if g := start(map[string]string{"GATEWAY_LOG_LEVEL": "loud"}); !strings.Contains(g.stdout.String(), "log_level") {
		t.Fatalf("a different error was not logged:\n%s", g.stdout)
	}
	b.advance(time.Minute)
	start(badListen) // logged: the last logged error was another one
	b.advance(startup.LogEvery)
	if g := start(badListen); !strings.Contains(g.stdout.String(), "listen_addr") {
		t.Fatalf("not logged again after %v:\n%s", startup.LogEvery, g.stdout)
	}
	if got := b.slept(); len(got) != 5 {
		t.Fatalf("slept %d times, want 5 (one per failed start)", len(got))
	}
}

// Spec 025 AC5: outside the gateway's own service a failed start exits at once and
// logs every time. A terminal can inherit INVOCATION_ID with another SYSTEMD_EXEC_PID,
// or XPC_SERVICE_NAME of another job; neither counts. launchd's own label does.
func TestRun_StartFailureOutsideServiceExitsAtOnce(t *testing.T) {
	inTempDir(t, nil)
	state := t.TempDir()
	for name, env := range map[string]map[string]string{
		"plain":            {},
		"inherited unit":   {"INVOCATION_ID": "x", "SYSTEMD_EXEC_PID": strconv.Itoa(os.Getpid() + 1)},
		"another xpc job":  {"XPC_SERVICE_NAME": "application.com.apple.Terminal.1"},
		"xpc service zero": {"XPC_SERVICE_NAME": "0"},
	} {
		t.Run(name, func(t *testing.T) {
			b := newBackoff()
			env["XDG_STATE_HOME"] = state
			env["GATEWAY_LISTEN_ADDR"] = badListen["GATEWAY_LISTEN_ADDR"]
			g := startGateway(t, options{env: env, now: b.now, sleep: b.sleep})
			failsBeforeBind(t, g, exitConfig)
			if got := b.slept(); len(got) != 0 {
				t.Fatalf("slept %v outside the service", got)
			}
		})
	}
	if _, err := os.Stat(filepath.Join(state, "llm-gateway", "startup-error.json")); !os.IsNotExist(err) {
		t.Fatalf("a record was written outside the service: %v", err)
	}

	b := newBackoff()
	env := map[string]string{"XPC_SERVICE_NAME": "dev.llm-gateway", "XDG_STATE_HOME": t.TempDir()}
	env["GATEWAY_LISTEN_ADDR"] = badListen["GATEWAY_LISTEN_ADDR"]
	failsBeforeBind(t, startGateway(t, options{env: env, now: b.now, sleep: b.sleep}), exitConfig)
	if got := b.slept(); len(got) != 1 {
		t.Fatalf("launchd: slept %v, want one wait", got)
	}
}

// Spec 025 AC5: under the service a clean start and stop stay immediate: the start's
// lines come out as soon as it serves, nothing waits, and an old record is cleared.
func TestRun_ServiceCleanStartAndStopDoNotWait(t *testing.T) {
	inTempDir(t, nil)
	state, b := t.TempDir(), newBackoff()
	path := filepath.Join(state, "llm-gateway", "startup-error.json")
	if err := startup.Write(path, startup.Record{Error: "old", Key: "old", FailedAt: b.now()}); err != nil {
		t.Fatal(err)
	}
	g := startGateway(t, options{env: systemdEnv(state, nil), now: b.now, sleep: b.sleep})
	g.waitLine("gateway started")
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("record not cleared after a start: %v", err)
	}
	began := time.Now()
	if code := g.stop(); code != exitOK {
		t.Fatalf("exit %d", code)
	}
	if took := time.Since(began); took > 2*time.Second {
		t.Fatalf("stop took %v", took)
	}
	g.waitLine("gateway stopped")
	if got := b.slept(); len(got) != 0 {
		t.Fatalf("slept %v on a clean start and stop", got)
	}
}

// Spec 025 AC5: a stop during the wait ends it at once, so a reinstall's restart of a
// failing gateway is not held for startup.Wait.
func TestSleepCtxEndsOnStop(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	go func() { time.Sleep(20 * time.Millisecond); cancel() }()
	began := time.Now()
	sleepCtx(ctx, startup.Wait)
	if took := time.Since(began); took > time.Second {
		t.Fatalf("sleep took %v after the stop", took)
	}
}
