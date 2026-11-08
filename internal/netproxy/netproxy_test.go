package netproxy_test

import (
	"bufio"
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"go.uber.org/zap"

	"github.com/brutally-honest/llm-gateway/internal/core"
	"github.com/brutally-honest/llm-gateway/internal/netproxy"
	"github.com/brutally-honest/llm-gateway/internal/store"
)

// fakeStore hands every saved effect to a channel.
type fakeStore struct{ got chan core.Effect }

func (f *fakeStore) SaveEffects(_ context.Context, effects []core.Effect, _ [][]byte) error {
	for _, e := range effects {
		f.got <- e
	}
	return nil
}

func (f *fakeStore) next(t *testing.T) core.Effect {
	t.Helper()
	select {
	case e := <-f.got:
		return e
	case <-time.After(5 * time.Second):
		t.Fatal("no effect recorded")
		return core.Effect{}
	}
}

var session = store.SessionIdent{Principal: "local", Client: "client-a", Key: "s1"}

// start runs a proxy linked to session on two loopback listeners.
func start(t *testing.T) (*fakeStore, string, string) {
	t.Helper()
	st := &fakeStore{got: make(chan core.Effect, 16)}
	rec := netproxy.NewRecorder(st, func() (store.SessionIdent, bool) { return session, true }, nil, zap.NewNop())
	p := netproxy.New(rec, zap.NewNop())
	httpLn, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	socksLn, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	p.ListenHTTP(httpLn)
	p.ListenSOCKS(socksLn)
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		p.Shutdown(ctx)
		rec.Close(ctx)
	})
	return st, httpLn.Addr().String(), socksLn.Addr().String()
}

// echoServer echoes every byte back until the client closes.
func echoServer(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				_, _ = io.Copy(c, c)
				_ = c.Close()
			}()
		}
	}()
	return ln.Addr().String()
}

// roundTrip writes msg on c, reads it back and closes c.
func roundTrip(t *testing.T, c net.Conn, r io.Reader, msg string) {
	t.Helper()
	if _, err := c.Write([]byte(msg)); err != nil {
		t.Fatal(err)
	}
	back := make([]byte, len(msg))
	if _, err := io.ReadFull(r, back); err != nil || string(back) != msg {
		t.Fatalf("echo = %q, %v", back, err)
	}
	_ = c.Close()
}

// AC8: a CONNECT tunnel forwards bytes unchanged and is stored once with its host, port,
// bytes each way and outcome, linked to the active session.
func TestNetProxy_ConnectCountsBytes(t *testing.T) {
	st, httpAddr, _ := start(t)
	target := echoServer(t)
	c, err := net.Dial("tcp", httpAddr)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fmt.Fprintf(c, "CONNECT %s HTTP/1.1\r\nHost: %s\r\n\r\n", target, target); err != nil {
		t.Fatal(err)
	}
	br := bufio.NewReader(c)
	res, err := http.ReadResponse(br, &http.Request{Method: http.MethodConnect})
	if err != nil || res.StatusCode != http.StatusOK {
		t.Fatalf("CONNECT = %v, %v", res, err)
	}
	roundTrip(t, c, br, "hello, tunnel")

	e := st.next(t)
	host, port, _ := net.SplitHostPort(target)
	if e.Kind != core.EffectNetConn || e.Host != host || fmt.Sprint(e.Port) != port || e.Via != "connect" {
		t.Errorf("effect = %+v", e)
	}
	if e.BytesUp != 13 || e.BytesDown != 13 || e.Outcome != core.NetOK || e.EndedAt.Before(e.At) {
		t.Errorf("bytes %d/%d outcome %q, want 13/13 ok", e.BytesUp, e.BytesDown, e.Outcome)
	}
	if e.SessionKey != session.Key {
		t.Errorf("session = %q, want %q", e.SessionKey, session.Key)
	}

	// An unreachable target is a 502 and a refused effect (E12).
	dead, _ := net.Listen("tcp", "127.0.0.1:0")
	deadAddr := dead.Addr().String()
	_ = dead.Close()
	c2, err := net.Dial("tcp", httpAddr)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = c2.Close() }()
	if _, err := fmt.Fprintf(c2, "CONNECT %s HTTP/1.1\r\nHost: %s\r\n\r\n", deadAddr, deadAddr); err != nil {
		t.Fatal(err)
	}
	res, err = http.ReadResponse(bufio.NewReader(c2), &http.Request{Method: http.MethodConnect})
	if err != nil || res.StatusCode != http.StatusBadGateway {
		t.Fatalf("CONNECT to a closed port = %v, %v; want 502", res, err)
	}
	if e := st.next(t); e.Outcome != core.NetRefused {
		t.Errorf("closed port outcome = %q, want refused", e.Outcome)
	}
}

// AC8, K2: a plain http:// request is forwarded with its query string, but stored with
// method and URL without it; Proxy-Authorization is neither forwarded nor stored.
func TestNetProxy_PlainHTTPRecordsURL(t *testing.T) {
	st, httpAddr, _ := start(t)
	seen := make(chan http.Header, 1)
	var query string
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		query = r.URL.RawQuery
		seen <- r.Header.Clone()
		_, _ = io.WriteString(w, "page body")
	}))
	t.Cleanup(up.Close)
	proxyURL, _ := url.Parse("http://" + httpAddr)
	client := &http.Client{Transport: &http.Transport{Proxy: http.ProxyURL(proxyURL)}}
	req, _ := http.NewRequest(http.MethodGet, up.URL+"/docs/page?token=s3cret", nil)
	req.Header.Set("Proxy-Authorization", "Basic c2VjcmV0LXByb3h5")
	res, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(res.Body)
	_ = res.Body.Close()
	if string(body) != "page body" || query != "token=s3cret" {
		t.Fatalf("body %q, upstream query %q", body, query)
	}
	if h := <-seen; h.Get("Proxy-Authorization") != "" {
		t.Errorf("Proxy-Authorization forwarded: %q", h.Get("Proxy-Authorization"))
	}

	e := st.next(t)
	if want := "GET " + up.URL + "/docs/page"; e.Detail != want || e.Via != "http" {
		t.Errorf("detail = %q via %q, want %q via http", e.Detail, e.Via, want)
	}
	if all := fmt.Sprintf("%+v", e); strings.Contains(all, "s3cret") || strings.Contains(all, "c2VjcmV0LXByb3h5") {
		t.Errorf("stored effect holds the query or the proxy credential: %s", all)
	}
	if e.BytesDown != int64(len("page body")) || e.Outcome != core.NetOK {
		t.Errorf("bytes down %d outcome %q", e.BytesDown, e.Outcome)
	}
}

// AC9: SOCKS5 with no authentication and CONNECT tunnels bytes and stores the same fields.
func TestNetProxy_Socks5Connect(t *testing.T) {
	st, _, socksAddr := start(t)
	target := echoServer(t)
	host, portText, _ := net.SplitHostPort(target)
	c, err := net.Dial("tcp", socksAddr)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.Write([]byte{5, 1, 0}); err != nil {
		t.Fatal(err)
	}
	greet := make([]byte, 2)
	if _, err := io.ReadFull(c, greet); err != nil || greet[1] != 0 {
		t.Fatalf("greeting = %v, %v", greet, err)
	}
	port, err := strconv.Atoi(portText)
	if err != nil {
		t.Fatal(err)
	}
	req := []byte{5, 1, 0, 3, byte(len(host))}
	req = append(req, host...)
	req = binary.BigEndian.AppendUint16(req, uint16(port))
	if _, err := c.Write(req); err != nil {
		t.Fatal(err)
	}
	rep := make([]byte, 10)
	if _, err := io.ReadFull(c, rep); err != nil || rep[1] != 0 {
		t.Fatalf("reply = %v, %v", rep, err)
	}
	roundTrip(t, c, c, "socks bytes")

	e := st.next(t)
	if e.Host != host || e.Port != port || e.Via != "socks" || e.BytesUp != 11 || e.BytesDown != 11 || e.Outcome != core.NetOK {
		t.Errorf("effect = %+v", e)
	}
}
