package core_test

import (
	"reflect"
	"runtime"
	"strings"
	"sync"
	"testing"

	"github.com/brutally-honest/llm-gateway/internal/core"
)

// fidelityWrapper is the name every subtest of TestCapture_ForwardingUnchanged starts
// with. The gateway helper turns capture on for a test whose name starts with it.
const fidelityWrapper = "TestCapture_ForwardingUnchanged"

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

// AC5: 001's forwarding, streaming, compression and error tests pass with capture on.
// Each runs unchanged as a subtest; the helper sees the name and turns capture on,
// and the subtest then proves capture was on by the exchanges the sink received.
func TestCapture_ForwardingUnchanged(t *testing.T) {
	for _, fn := range []func(*testing.T){
		TestProxy_ForwardsRequestVerbatim,           // 001 AC8
		TestProxy_BaseURLPathPrefixJoined,           // 001 AC12
		TestProxy_StripsHopByHopHeaders,             // 001 AC14
		TestProxy_SetsUpstreamHost,                  // 001 AC15
		TestProxy_AddsNoHeadersUpstream,             // 001 AC16
		TestProxy_ResponseVerbatim,                  // 001 AC18
		TestProxy_GatewayRequestIDWinsOnResponse,    // 001 AC19
		TestProxy_LargeBodyNotLimited,               // 001 AC20
		TestProxy_CompressionPassThrough,            // 001 AC21
		TestProxy_TransparentGzipDisabled,           // 001 AC22
		TestProxy_StreamsSSEWithoutBuffering,        // 001 AC23
		TestProxy_UpstreamErrorsVerbatim,            // 001 AC26
		TestProxy_GatewayErrorStatus,                // 001 AC27, core half
		TestProxy_NoRetry,                           // 001 AC29
		TestProxy_ClientDisconnectCancelsUpstream,   // 001 AC30
		TestProxy_UpstreamDiesMidStreamAbortsClient, // 001 AC31
		TestProxy_MalformedClientBody400,            // 001 AC47
		TestAccessLog_UpstreamAbortedField,          // 001 AC48
	} {
		name := funcName(fn)
		t.Run(name, func(t *testing.T) {
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

			run.sink.wait(t, 1)
		})
	}
}

// funcName is fn's name without its package path.
func funcName(fn func(*testing.T)) string {
	full := runtime.FuncForPC(reflect.ValueOf(fn).Pointer()).Name()
	return full[strings.LastIndex(full, ".")+1:]
}
