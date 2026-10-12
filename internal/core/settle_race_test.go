package core_test

import (
	"net"
	"net/http"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/brutally-honest/llm-gateway/internal/core"
	"github.com/brutally-honest/llm-gateway/internal/logging"
	"github.com/brutally-honest/llm-gateway/internal/server"
)

// AC1: the client reads the whole response and closes before Meta.Settle runs, so
// net/http has cancelled the inbound context by then. The handler between the access
// log and the proxy holds the request open until that happens, which makes the race
// deterministic. The access line must carry neither flag.
func TestAccessLog_CompletedThenClientClosesBeforeSettle(t *testing.T) {
	checkNoLeaksAtEnd(t)
	const path = "/t/v1/stream"
	logs := &syncBuffer{}
	log := logging.New(logs, "debug")
	a := testAdapter{}
	p := core.NewProxy(a, upstreamConfig(newUpstream(t, nil).URL), identifyWith(), log)
	cancelled := make(chan bool, 1)
	holding := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p.ServeHTTP(w, r)
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
		select {
		case <-r.Context().Done():
			cancelled <- true
		case <-time.After(5 * time.Second):
			cancelled <- false
		}
	})
	srv := server.New(log, func(r chi.Router) { r.Handle(a.Prefix()+"/*", holding) })
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go func() { _ = srv.Serve(ln) }()
	t.Cleanup(func() { _ = srv.Close() })
	gw := &gateway{addr: ln.Addr().String(), url: "http://" + ln.Addr().String(), logs: logs}

	gw.raw(t, rawRequest(http.MethodGet, path, "gateway.local", nil, nil, ""))
	if !<-cancelled {
		t.Fatal("inbound context was not cancelled within 5s of the client closing")
	}

	line := gw.accessLine(t, path)
	for _, field := range []string{"client_disconnected", "upstream_aborted"} {
		if got, ok := line[field]; ok {
			t.Errorf("%s = %v, want absent", field, got)
		}
	}
}
