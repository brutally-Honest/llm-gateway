package core_test

import (
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/brutally-honest/llm-gateway/internal/core"
	"github.com/brutally-honest/llm-gateway/internal/logging"
	"github.com/brutally-honest/llm-gateway/internal/server"
)

// sessionProfile is a test-only client that reports X-Test-Session as its session
// key. Registered from the test, it needs no change in core (AC5).
type sessionProfile struct{ panics bool }

func (sessionProfile) Name() string               { return "session-client" }
func (sessionProfile) Match(r *http.Request) bool { return r.Header.Get("X-Session-Client") != "" }
func (s sessionProfile) SessionKey(h http.Header) (string, bool) {
	if s.panics {
		panic("profile bug")
	}
	return h.Get("X-Test-Session"), len(h.Values("X-Test-Session")) > 0
}

func startSessionGateway(t *testing.T, base *url.URL, sink core.CaptureSink, p core.Profile) *gateway {
	t.Helper()
	logs := &syncBuffer{}
	log := logging.New(logs, "debug")
	reg := core.NewRegistry(log, core.WithCapture(core.Capture{Sink: sink, Budget: core.NewBudget(1 << 20), MaxBodyBytes: 1 << 20}))
	reg.AddProfile(testProfile{})
	reg.AddProfile(p)
	reg.AddAdapter(testAdapter{}, upstreamConfig(base))
	return serveOnLoopback(t, logs, server.New(log, reg.Mount))
}

func sendSession(t *testing.T, g *gateway, client, session string) int {
	t.Helper()
	return sendSessionHeader(t, g, client, session, session != "")
}

// sendSessionHeader sets X-Test-Session only when withHeader, so "" can be present.
func sendSessionHeader(t *testing.T, g *gateway, client, session string, withHeader bool) int {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, g.url+"/t/v1/x", strings.NewReader("hi"))
	if err != nil {
		t.Fatal(err)
	}
	if client != "" {
		req.Header.Set(client, "1")
	}
	if withHeader {
		req.Header.Set("X-Test-Session", session)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = io.Copy(io.Discard, res.Body)
	_ = res.Body.Close()
	return res.StatusCode
}

func okUpstream(t *testing.T) *upstream {
	return newUpstream(t, func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, "ok") })
}

func TestProxy_SessionKeyReachesSink(t *testing.T) {
	checkNoLeaksAtEnd(t)
	sink := &recordingSink{}
	g := startSessionGateway(t, okUpstream(t).URL, sink, sessionProfile{})
	sendSession(t, g, "X-Session-Client", "Opaque/Key:1")
	ex := sink.wait(t, 1)[0]
	defer ex.Release()
	if ex.SessionKey != "Opaque/Key:1" || ex.Client != "session-client" {
		t.Errorf("session key %q, client %q; want the key unparsed", ex.SessionKey, ex.Client)
	}
}

func TestProxy_NoKeyOrUnknownClientCapturedUngrouped(t *testing.T) {
	checkNoLeaksAtEnd(t)
	sink := &recordingSink{}
	g := startSessionGateway(t, okUpstream(t).URL, sink, sessionProfile{})
	// the test profile is no SessionKeyer; the second request matches nothing
	if got := sendSession(t, g, "X-Test-Client", "abc"); got != http.StatusOK {
		t.Fatal(got)
	}
	if got := sendSession(t, g, "", "abc"); got != http.StatusOK {
		t.Fatal(got)
	}
	for _, ex := range sink.wait(t, 2) {
		if ex.SessionKey != "" {
			t.Errorf("client %q has a session key %q, want none", ex.Client, ex.SessionKey)
		}
		ex.Release()
	}
	if strings.Contains(g.logs.String(), "session_key_rejected") {
		t.Error("a profile with nothing to report logged a rejection")
	}
}

func TestValidSessionKey(t *testing.T) {
	for _, c := range []struct{ in, want string }{
		{"", "empty"},
		{strings.Repeat("a", 256), ""},
		{strings.Repeat("a", 257), "too_long"},
		{"a\x00b", "control_character"},
		{"a\nb", "control_character"},
		{"a\x7fb", "control_character"},
		{"a\u0085b", "control_character"},
		{"naïve-密钥", ""},
		{" spaces ok ", ""},
	} {
		if got := core.ValidSessionKey(c.in); got != c.want {
			t.Errorf("ValidSessionKey(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestProxy_InvalidSessionKeyStoredUngrouped(t *testing.T) {
	checkNoLeaksAtEnd(t)
	for _, c := range []struct {
		name, val, reason string
		kept              bool
	}{
		{"empty", "", "empty", false},
		{"too long", strings.Repeat("k", 257), "too_long", false},
		{"256 accepted", strings.Repeat("k", 256), "", true},
		{"control", "sec\u0085ret-value", "control_character", false},
	} {
		t.Run(c.name, func(t *testing.T) {
			sink := &recordingSink{}
			g := startSessionGateway(t, okUpstream(t).URL, sink, sessionProfile{})
			if got := sendSessionHeader(t, g, "X-Session-Client", c.val, true); got != http.StatusOK {
				t.Fatalf("status %d", got)
			}
			ex := sink.wait(t, 1)[0]
			defer ex.Release()
			if (ex.SessionKey != "") != c.kept {
				t.Errorf("stored session key %q, kept = %v", ex.SessionKey, c.kept)
			}
			logs := g.logs.String()
			if c.reason == "" {
				if strings.Contains(logs, "session_key_rejected") {
					t.Error("a valid key was logged as rejected")
				}
				return
			}
			if !strings.Contains(logs, "session_key_rejected") || !strings.Contains(logs, `"reason":"`+c.reason+`"`) ||
				!strings.Contains(logs, `"request_id"`) {
				t.Errorf("log lacks the rejection and its reason %q: %s", c.reason, logs)
			}
			if c.val != "" && (strings.Contains(logs, c.val) || strings.Contains(logs, "ret-value")) {
				t.Error("the key's value reached the log")
			}
		})
	}
}

func TestProxy_AbsentSessionHeaderLogsNothing(t *testing.T) {
	checkNoLeaksAtEnd(t)
	sink := &recordingSink{}
	g := startSessionGateway(t, okUpstream(t).URL, sink, sessionProfile{})
	if got := sendSession(t, g, "X-Session-Client", ""); got != http.StatusOK {
		t.Fatalf("status %d", got)
	}
	ex := sink.wait(t, 1)[0]
	defer ex.Release()
	if ex.SessionKey != "" || ex.Client != "session-client" {
		t.Errorf("client %q, session key %q; want session-client and none", ex.Client, ex.SessionKey)
	}
	if strings.Contains(g.logs.String(), "session_key_rejected") {
		t.Errorf("an absent header was logged as rejected: %s", g.logs.String())
	}
}

func TestProxy_TestOnlyProfileKeyNeedsNoCoreChange(t *testing.T) {
	checkNoLeaksAtEnd(t)
	sink := &recordingSink{}
	g := startSessionGateway(t, okUpstream(t).URL, sink, sessionProfile{})
	sendSession(t, g, "X-Session-Client", "s-1")
	sendSession(t, g, "X-Session-Client", "s-1")
	sendSession(t, g, "X-Session-Client", "s-2")
	got := map[string]int{}
	for _, ex := range sink.wait(t, 3) {
		got[ex.SessionKey]++
		ex.Release()
	}
	if got["s-1"] != 2 || got["s-2"] != 1 {
		t.Errorf("session keys = %v, want s-1 twice and s-2 once", got)
	}
}

func TestProxy_SessionKeyWithDroppedCaptureStillSucceeds(t *testing.T) {
	checkNoLeaksAtEnd(t)
	sink := &recordingSink{refuse: true}
	g := startSessionGateway(t, okUpstream(t).URL, sink, sessionProfile{})
	if got := sendSession(t, g, "X-Session-Client", "s-1"); got != http.StatusOK {
		t.Fatalf("status %d, want 200 with the capture dropped", got)
	}
	line := g.accessLine(t, "/t/v1/x")
	if line["capture"] != core.CaptureDroppedQueueFull {
		t.Errorf("capture = %v, want %s", line["capture"], core.CaptureDroppedQueueFull)
	}
}

func TestProxy_PanickingProfileMeansNoKey(t *testing.T) {
	checkNoLeaksAtEnd(t)
	sink := &recordingSink{}
	g := startSessionGateway(t, okUpstream(t).URL, sink, sessionProfile{panics: true})
	if got := sendSession(t, g, "X-Session-Client", "s-1"); got != http.StatusOK {
		t.Fatalf("status %d, want 200", got)
	}
	ex := sink.wait(t, 1)[0]
	defer ex.Release()
	if ex.SessionKey != "" {
		t.Errorf("session key %q after a panic, want none", ex.SessionKey)
	}
}
