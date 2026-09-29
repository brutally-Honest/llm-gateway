package anthropic_test

import (
	"reflect"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/brutally-honest/llm-gateway/internal/core"
)

// fidelityWrapper is the name every subtest of TestCapture_ForwardingUnchanged starts
// with. The gateway helper turns capture on for a test whose name starts with it.
const fidelityWrapper = "TestCapture_ForwardingUnchanged"

// recordingSink counts the exchanges it is handed and gives their memory back.
type recordingSink struct {
	mu sync.Mutex
	n  int
}

func (s *recordingSink) Submit(ex *core.Exchange) bool {
	ex.Release()
	s.mu.Lock()
	defer s.mu.Unlock()
	s.n++
	return true
}

// waitAtLeast fails t unless the sink receives n exchanges within 2s. Exchanges are
// submitted when the handler ends, which can be after the client has its response.
func (s *recordingSink) waitAtLeast(t *testing.T, n int) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for {
		s.mu.Lock()
		got := s.n
		s.mu.Unlock()
		if got >= n {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("sink holds %d exchanges, want at least %d", got, n)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// fidelityRun is the capture one wrapped 001 function runs with.
type fidelityRun struct {
	sink   *recordingSink
	budget *core.Budget
}

// fidelityRuns maps a wrapper subtest's name to its run.
var fidelityRuns = struct {
	mu   sync.Mutex
	runs map[string]*fidelityRun
}{runs: map[string]*fidelityRun{}}

// fidelityCapture is the capture for t when t runs under TestCapture_ForwardingUnchanged,
// at any depth of subtest, and ok false otherwise. The name test reaches nested
// subtests, which a registry keyed by *testing.T would not.
func fidelityCapture(t *testing.T) (c core.Capture, ok bool) {
	t.Helper()
	rest, under := strings.CutPrefix(t.Name(), fidelityWrapper+"/")
	if !under {
		return core.Capture{}, false
	}
	fn, _, _ := strings.Cut(rest, "/")
	fidelityRuns.mu.Lock()
	run := fidelityRuns.runs[fidelityWrapper+"/"+fn]
	fidelityRuns.mu.Unlock()
	if run == nil {
		t.Fatalf("no capture registered for %s", t.Name())
	}
	return core.Capture{Sink: run.sink, Budget: run.budget, MaxBodyBytes: 32 << 20}, true
}

// AC5: 001's forwarding, streaming and error tests for the Anthropic adapter pass
// with capture on. Each runs unchanged as a subtest; the helper sees the name and
// turns capture on, and the subtest then proves capture was on by the exchanges the
// sink received.
func TestCapture_ForwardingUnchanged(t *testing.T) {
	for _, fn := range []func(*testing.T){
		TestProxy_AnthropicRoutes,                   // 001 AC9
		TestProxy_UnknownPathUnderPrefixForwarded,   // 001 AC10
		TestProxy_HeadAPIHelloReturnsUpstreamStatus, // 001 AC13
		TestProxy_AuthHeadersForwardedUnchanged,     // 001 AC17
		TestProxy_GoldenAnthropicStreamReplay,       // 001 AC24
		TestProxy_UpstreamErrorsVerbatim,            // 001 AC26
		TestProxy_UpstreamUnreachable502,            // 001 AC27
		TestProxy_UpstreamTimeout504,                // 001 AC28
		TestProxy_ClientBodyEnvelope,                // 001 AC47
	} {
		t.Run(funcName(fn), func(t *testing.T) {
			run := &fidelityRun{sink: &recordingSink{}, budget: core.NewBudget(256 << 20)}
			fidelityRuns.mu.Lock()
			fidelityRuns.runs[t.Name()] = run
			fidelityRuns.mu.Unlock()
			t.Cleanup(func() {
				fidelityRuns.mu.Lock()
				delete(fidelityRuns.runs, t.Name())
				fidelityRuns.mu.Unlock()
			})

			fn(t)

			run.sink.waitAtLeast(t, 1)
		})
	}
}

// funcName is fn's name without its package path.
func funcName(fn func(*testing.T)) string {
	full := runtime.FuncForPC(reflect.ValueOf(fn).Pointer()).Name()
	return full[strings.LastIndex(full, ".")+1:]
}
