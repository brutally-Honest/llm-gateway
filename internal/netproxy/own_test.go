package netproxy_test

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync/atomic"
	"testing"
	"time"

	"go.uber.org/zap"

	"github.com/brutally-honest/llm-gateway/internal/core"
	"github.com/brutally-honest/llm-gateway/internal/netproxy"
	"github.com/brutally-honest/llm-gateway/internal/store"
)

// startOwn is start with own given as the gateway's own listener addresses.
func startOwn(t *testing.T, own ...string) (*fakeStore, string) {
	t.Helper()
	st := &fakeStore{got: make(chan core.Effect, 16)}
	rec := netproxy.NewRecorder(st, func() (store.SessionIdent, bool) { return session, true }, nil, zap.NewNop())
	rec.SkipTargets(own...)
	p := netproxy.New(rec, zap.NewNop())
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	p.ListenHTTP(ln)
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		p.Shutdown(ctx)
		rec.Close(ctx)
	})
	return st, ln.Addr().String()
}

func connectEcho(t *testing.T, proxy, target, msg string) {
	t.Helper()
	c, err := net.Dial("tcp", proxy)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fmt.Fprintf(c, "CONNECT %s HTTP/1.1\r\nHost: %s\r\n\r\n", target, target); err != nil {
		t.Fatal(err)
	}
	br := bufio.NewReader(c)
	res, err := http.ReadResponse(br, &http.Request{Method: http.MethodConnect})
	if err != nil || res.StatusCode != http.StatusOK {
		t.Fatalf("CONNECT %s = %v, %v", target, res, err)
	}
	roundTrip(t, c, br, msg)
}

// B4: a tunnel or plain request to one of the gateway's own listener addresses is still
// forwarded but is not recorded as a network effect; other loopback traffic, a user's own
// local service, is (spec 024 AC4).
func TestNetProxy_OwnListenersNotRecorded(t *testing.T) {
	own := echoServer(t)  // stands for the gateway's API listener
	user := echoServer(t) // a user's own local service
	var hits atomic.Int32
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		_, _ = io.WriteString(w, "ok")
	}))
	t.Cleanup(api.Close)
	apiAddr := api.Listener.Addr().String()
	_, ownPort, _ := net.SplitHostPort(own)
	st, proxy := startOwn(t, own, apiAddr)

	connectEcho(t, proxy, own, "to the gateway")                       // forwarded, not recorded
	connectEcho(t, proxy, net.JoinHostPort("localhost", ownPort), "x") // the same listener, spelled localhost

	pu, _ := url.Parse("http://" + proxy)
	client := &http.Client{Transport: &http.Transport{Proxy: http.ProxyURL(pu)}}
	res, err := client.Post("http://"+apiAddr+"/_gateway/hooks/some-client", "application/json", nil)
	if err != nil {
		t.Fatal(err)
	}
	_ = res.Body.Close()
	if res.StatusCode != http.StatusOK || hits.Load() != 1 {
		t.Fatalf("plain request to the gateway: status %d, hits %d; want it forwarded", res.StatusCode, hits.Load())
	}
	client.CloseIdleConnections()

	connectEcho(t, proxy, user, "to a local service") // recorded
	e := st.next(t)
	if net.JoinHostPort(e.Host, fmt.Sprint(e.Port)) != user {
		t.Fatalf("first effect is %s:%d, want only the user's service %s", e.Host, e.Port, user)
	}
	select {
	case extra := <-st.got:
		t.Fatalf("extra effect %s:%d", extra.Host, extra.Port)
	case <-time.After(100 * time.Millisecond):
	}
}
