package netproxy_test

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"go.uber.org/zap"

	"github.com/brutally-honest/llm-gateway/internal/core"
	"github.com/brutally-honest/llm-gateway/internal/netproxy"
	"github.com/brutally-honest/llm-gateway/internal/store"
)

// socksOpen opens a SOCKS5 tunnel to target on c and reports whether the proxy accepted it.
func socksOpen(c net.Conn, host string, port int) bool {
	if _, err := c.Write([]byte{5, 1, 0}); err != nil {
		return false
	}
	greet := make([]byte, 2)
	if _, err := io.ReadFull(c, greet); err != nil {
		return false
	}
	req := []byte{5, 1, 0, 3, byte(len(host))}
	req = append(req, host...)
	req = append(req, byte(port>>8), byte(port))
	if _, err := c.Write(req); err != nil {
		return false
	}
	rep := make([]byte, 10)
	_, err := io.ReadFull(c, rep)
	return err == nil && rep[1] == 0
}

// Tunnels that reach pipe() while Shutdown sets closed and waits must not join the
// WaitGroup (a positive Add racing Wait on a zero counter), and each is recorded once.
func TestNetProxy_ShutdownRacesNewTunnels(t *testing.T) {
	var (
		mu   sync.Mutex
		outs []core.Effect
	)
	st := storeFunc(func(effects []core.Effect) {
		mu.Lock()
		outs = append(outs, effects...)
		mu.Unlock()
	})
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
	target := echoServer(t)
	host, portText, _ := net.SplitHostPort(target)
	var port int
	_, _ = fmt.Sscan(portText, &port)

	const n = 48
	var opened atomic.Int64
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			if i%2 == 0 {
				c, err := net.Dial("tcp", httpLn.Addr().String())
				if err != nil {
					return
				}
				defer func() { _ = c.Close() }()
				_ = c.SetDeadline(time.Now().Add(3 * time.Second))
				if _, err := fmt.Fprintf(c, "CONNECT %s HTTP/1.1\r\nHost: %s\r\n\r\n", target, target); err != nil {
					return
				}
				br := bufio.NewReader(c)
				res, err := http.ReadResponse(br, &http.Request{Method: http.MethodConnect})
				if err != nil || res.StatusCode != http.StatusOK {
					return
				}
				opened.Add(1)
				_, _ = io.Copy(io.Discard, br) // until the proxy cuts the tunnel
				return
			}
			c, err := net.Dial("tcp", socksLn.Addr().String())
			if err != nil {
				return
			}
			defer func() { _ = c.Close() }()
			_ = c.SetDeadline(time.Now().Add(3 * time.Second))
			if !socksOpen(c, host, port) {
				return
			}
			opened.Add(1)
			_, _ = io.Copy(io.Discard, c)
		}()
	}
	close(start)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	p.Shutdown(ctx)
	wg.Wait()
	rec.Close(context.Background())

	mu.Lock()
	defer mu.Unlock()
	early := 0
	for _, e := range outs {
		if e.Outcome == core.NetClosedEarly {
			early++
		}
	}
	// Every accepted tunnel stays open until the proxy cuts it, so each is recorded
	// once as closed early; none is lost and none is recorded twice.
	if early != int(opened.Load()) {
		t.Errorf("closed early = %d, accepted tunnels = %d", early, opened.Load())
	}
	if len(outs) > n {
		t.Errorf("recorded %d effects for %d tunnels", len(outs), n)
	}
}

// storeFunc is a Store that hands every batch to f.
type storeFunc func([]core.Effect)

func (f storeFunc) SaveEffects(_ context.Context, effects []core.Effect, _ [][]byte) error {
	f(effects)
	return nil
}
