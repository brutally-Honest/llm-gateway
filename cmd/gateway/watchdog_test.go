package main

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"runtime/debug"
	"strings"
	"testing"
	"time"
)

// defaultWatchdog is how long a test that starts a gateway may run before the
// watchdog ends the binary. The slowest tests take a few seconds.
const defaultWatchdog = 60 * time.Second

// watchdogChildEnv makes TestMain run hangUnderWatchdog instead of the tests, in the
// binary that TestWatchdog_FiresOnAHungTest re-executes.
const watchdogChildEnv = "GATEWAY_TEST_WATCHDOG_CHILD"

// watchdog fails the whole test binary d after the call, unless the test finishes
// first. A hung test then ends in seconds with every goroutine's stack in the log,
// not after the go test timeout. t.Fatal cannot do this: it must run on the test
// goroutine, which is the one that is stuck. Spec 036, D6.
func watchdog(t testing.TB, d time.Duration) *time.Timer {
	t.Helper()
	timer := armWatchdog(t.Name(), d)
	t.Cleanup(func() { timer.Stop() })
	return timer
}

func armWatchdog(name string, d time.Duration) *time.Timer {
	return time.AfterFunc(d, func() {
		debug.SetTraceback("all")
		panic(fmt.Sprintf("watchdog: %s still running after %s", name, d))
	})
}

// hangUnderWatchdog is the re-executed child of TestWatchdog_FiresOnAHungTest: it arms
// a short watchdog and blocks forever, as a hung test would.
func hangUnderWatchdog() {
	armWatchdog("hungChild", 200*time.Millisecond)
	select {}
}

// The watchdog kills a hung test binary quickly and prints the stacks.
func TestWatchdog_FiresOnAHungTest(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, os.Args[0])
	cmd.Env = append(os.Environ(), watchdogChildEnv+"=1")
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	start := time.Now()
	err := cmd.Run()
	took := time.Since(start)
	if ctx.Err() != nil {
		t.Fatalf("child still running after 20s\n%s", out.String())
	}
	if err == nil {
		t.Fatalf("child exited 0, want failure\n%s", out.String())
	}
	if took > 10*time.Second {
		t.Errorf("child took %s to fail, want a few seconds", took)
	}
	s := out.String()
	if !strings.Contains(s, "watchdog: hungChild still running after 200ms") {
		t.Errorf("no watchdog message\n%s", s)
	}
	if n := strings.Count(s, "goroutine "); n < 2 {
		t.Errorf("%d goroutine stacks, want every goroutine's\n%s", n, s)
	}
	if !strings.Contains(s, "hangUnderWatchdog") {
		t.Errorf("the blocked goroutine's frame is missing\n%s", s)
	}
}

// A test that finishes stops its timer: Stop reports false because the cleanup
// already stopped it (with an hour to go, it cannot have fired).
func TestWatchdog_StoppedWhenTheTestEnds(t *testing.T) {
	var timer *time.Timer
	t.Run("inner", func(t *testing.T) { timer = watchdog(t, time.Hour) })
	if timer.Stop() {
		t.Error("watchdog still armed after its test ended")
	}
}
