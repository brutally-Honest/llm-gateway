package netproxy

import (
	"bufio"
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"go.uber.org/zap"

	"github.com/brutally-honest/llm-gateway/internal/core"
	"github.com/brutally-honest/llm-gateway/internal/logging"
)

// dialTimeout bounds a dial to a proxy target.
const dialTimeout = 10 * time.Second

// Proxy is the HTTP and SOCKS5 forward proxy. Every connection it carries is counted and
// recorded once when it ends; at shutdown the ones still open are recorded as closed
// early (E13).
type Proxy struct {
	rec       *Recorder
	log       *zap.Logger
	dial      func(ctx context.Context, network, addr string) (net.Conn, error)
	transport *http.Transport

	mu      sync.Mutex
	closed  bool
	servers []*http.Server
	lns     []net.Listener
	tunnels map[*tunnel]struct{}
	wg      sync.WaitGroup
}

// New returns a proxy that records through rec.
func New(rec *Recorder, log *zap.Logger) *Proxy {
	d := &net.Dialer{Timeout: dialTimeout, KeepAlive: 30 * time.Second}
	return &Proxy{
		rec: rec, log: log, dial: d.DialContext,
		// A plain http:// request is forwarded directly: never through another proxy,
		// this one included.
		transport: &http.Transport{
			Proxy: nil, DialContext: d.DialContext, DisableCompression: true,
			ResponseHeaderTimeout: 0, IdleConnTimeout: 90 * time.Second, MaxIdleConns: 32,
		},
		tunnels: map[*tunnel]struct{}{},
	}
}

// tunnel is one open connection: its effect so far and the conns to cut at shutdown.
type tunnel struct {
	effect   core.Effect
	up, down atomic.Int64
	conns    []net.Conn
	once     sync.Once
}

// open registers a connection to host:port; finish records it once.
func (p *Proxy) open(host string, port int, via, detail string) *tunnel {
	t := &tunnel{effect: core.Effect{At: time.Now(), Host: host, Port: port, Via: via, Detail: detail}}
	p.mu.Lock()
	p.tunnels[t] = struct{}{}
	p.mu.Unlock()
	return t
}

// finish records t with outcome, once, and forgets it.
func (p *Proxy) finish(t *tunnel, outcome core.NetOutcome) {
	t.once.Do(func() {
		p.mu.Lock()
		delete(p.tunnels, t)
		p.mu.Unlock()
		e := t.effect
		e.EndedAt = time.Now()
		e.BytesUp, e.BytesDown = t.up.Load(), t.down.Load()
		e.Outcome = outcome
		p.rec.Record(e)
	})
}

// dialOutcome is the outcome of a failed dial.
func dialOutcome(err error) core.NetOutcome {
	if errors.Is(err, syscall.ECONNREFUSED) {
		return core.NetRefused
	}
	return core.NetError
}

// splitHostPort is host and port of addr, with def when it has no port.
func splitHostPort(addr string, def int) (string, int) {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return strings.Trim(addr, "[]"), def
	}
	n, err := strconv.Atoi(port)
	if err != nil {
		return host, def
	}
	return host, n
}

// ListenHTTP starts the HTTP proxy on ln (AC8).
func (p *Proxy) ListenHTTP(ln net.Listener) {
	srv := &http.Server{Handler: p, ReadHeaderTimeout: 10 * time.Second, ErrorLog: logging.StdLog(p.log)}
	p.mu.Lock()
	p.servers = append(p.servers, srv)
	p.lns = append(p.lns, ln)
	p.mu.Unlock()
	logging.Go(p.log, "netproxy_http", func() error {
		err := srv.Serve(ln)
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	})
}

// ServeHTTP answers one proxy request: CONNECT opens a tunnel, an absolute-form request is
// forwarded, anything else is refused.
func (p *Proxy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch {
	case r.Method == http.MethodConnect:
		p.connect(w, r)
	case r.URL.IsAbs() && r.URL.Scheme == "http":
		p.forward(w, r)
	default:
		http.Error(w, "this is a forward proxy: send CONNECT or an absolute http:// URL", http.StatusBadRequest)
	}
}

// connect dials the target, answers 200 and pipes bytes both ways; a failed dial gets 502
// (E12). Nothing is decrypted.
func (p *Proxy) connect(w http.ResponseWriter, r *http.Request) {
	target := r.Host
	if target == "" {
		target = r.URL.Host
	}
	host, port := splitHostPort(target, 443)
	t := p.open(host, port, "connect", "")
	up, err := p.dial(r.Context(), "tcp", net.JoinHostPort(host, strconv.Itoa(port)))
	if err != nil {
		p.finish(t, dialOutcome(err))
		http.Error(w, "proxy could not reach the target", http.StatusBadGateway)
		return
	}
	hj, ok := w.(http.Hijacker)
	if !ok {
		_ = up.Close()
		p.finish(t, core.NetError)
		http.Error(w, "proxy cannot tunnel on this connection", http.StatusInternalServerError)
		return
	}
	client, buf, err := hj.Hijack()
	if err != nil {
		_ = up.Close()
		p.finish(t, core.NetError)
		return
	}
	if _, err := client.Write([]byte("HTTP/1.1 200 Connection Established\r\n\r\n")); err != nil {
		_ = client.Close()
		_ = up.Close()
		p.finish(t, core.NetError)
		return
	}
	p.pipe(t, client, buf.Reader, up)
}

// pipe copies client to upstream and back until either side ends, counting bytes, then
// records t as ok. pending is what the client sent that was already read into a buffer.
func (p *Proxy) pipe(t *tunnel, client net.Conn, pending *bufio.Reader, up net.Conn) {
	p.mu.Lock()
	closed := p.closed
	t.conns = []net.Conn{client, up}
	p.wg.Add(1)
	p.mu.Unlock()
	defer p.wg.Done()
	if closed {
		_ = client.Close()
		_ = up.Close()
		p.finish(t, core.NetClosedEarly)
		return
	}
	var src io.Reader = client
	if pending != nil && pending.Buffered() > 0 {
		src = io.MultiReader(io.LimitReader(pending, int64(pending.Buffered())), client)
	}
	toUp := logging.Go(p.log, "netproxy_pipe", func() error {
		_, _ = io.Copy(&counter{w: up, n: &t.up}, src) // a copy ends when either side closes
		closeWrite(up)
		return nil
	})
	_, _ = io.Copy(&counter{w: client, n: &t.down}, up)
	closeWrite(client)
	<-toUp
	_ = client.Close()
	_ = up.Close()
	p.finish(t, core.NetOK)
}

// closeWrite half-closes c when it can, so the other side sees EOF; else it closes it.
func closeWrite(c net.Conn) {
	if cw, ok := c.(interface{ CloseWrite() error }); ok {
		_ = cw.CloseWrite()
		return
	}
	_ = c.Close()
}

// counter counts the bytes written through it.
type counter struct {
	w io.Writer
	n *atomic.Int64
}

func (c *counter) Write(b []byte) (int, error) {
	n, err := c.w.Write(b)
	c.n.Add(int64(n))
	return n, err
}

// countingBody counts the bytes read through it.
type countingBody struct {
	io.ReadCloser
	n *atomic.Int64
}

func (c *countingBody) Read(b []byte) (int, error) {
	n, err := c.ReadCloser.Read(b)
	c.n.Add(int64(n))
	return n, err
}

// hopHeaders are removed in both directions; so are Proxy-* headers and any header the
// Connection header names. Proxy-Authorization is never forwarded or stored.
var hopHeaders = []string{"Connection", "Keep-Alive", "Te", "Trailer", "Transfer-Encoding", "Upgrade"}

func stripHop(h http.Header) {
	for _, v := range h.Values("Connection") {
		for _, name := range strings.Split(v, ",") {
			if name = strings.TrimSpace(name); name != "" {
				h.Del(name)
			}
		}
	}
	for _, name := range hopHeaders {
		h.Del(name)
	}
	for name := range h {
		if strings.HasPrefix(http.CanonicalHeaderKey(name), "Proxy-") {
			delete(h, name)
		}
	}
}

// urlDetail is method and URL without its query string or user info (K2).
func urlDetail(method string, u *url.URL) string {
	c := url.URL{Scheme: u.Scheme, Host: u.Host, Path: u.Path, RawPath: u.RawPath}
	return method + " " + c.String()
}

// forward sends a plain http:// request on directly and streams the response back.
func (p *Proxy) forward(w http.ResponseWriter, r *http.Request) {
	host, port := splitHostPort(r.URL.Host, 80)
	t := p.open(host, port, "http", urlDetail(r.Method, r.URL))
	out := r.Clone(r.Context())
	out.RequestURI = ""
	out.Header = r.Header.Clone()
	stripHop(out.Header)
	if r.Body != nil && r.Body != http.NoBody {
		out.Body = &countingBody{ReadCloser: r.Body, n: &t.up}
	}
	res, err := p.transport.RoundTrip(out)
	if err != nil {
		p.finish(t, dialOutcome(err))
		http.Error(w, "proxy could not reach the target", http.StatusBadGateway)
		return
	}
	defer func() { _ = res.Body.Close() }()
	stripHop(res.Header)
	for k, v := range res.Header {
		w.Header()[k] = v
	}
	w.WriteHeader(res.StatusCode)
	rc := http.NewResponseController(w)
	buf := make([]byte, 32<<10)
	outcome := core.NetOK
	for {
		n, rerr := res.Body.Read(buf)
		if n > 0 {
			if _, werr := w.Write(buf[:n]); werr != nil {
				outcome = core.NetError
				break
			}
			t.down.Add(int64(n))
			_ = rc.Flush()
		}
		if rerr != nil {
			if !errors.Is(rerr, io.EOF) {
				outcome = core.NetError
			}
			break
		}
	}
	p.finish(t, outcome)
}

// Shutdown closes the listeners, waits for open tunnels until ctx ends, then cuts the
// rest and records them as closed early (E13).
func (p *Proxy) Shutdown(ctx context.Context) {
	p.mu.Lock()
	p.closed = true
	servers, lns := p.servers, p.lns
	p.mu.Unlock()
	for _, ln := range lns {
		_ = ln.Close()
	}
	for _, s := range servers {
		_ = s.Shutdown(ctx)
	}
	done := logging.Go(p.log, "netproxy_wait", func() error {
		p.wg.Wait()
		return nil
	})
	select {
	case <-done:
	case <-ctx.Done():
	}
	p.mu.Lock()
	open := make([]*tunnel, 0, len(p.tunnels))
	for t := range p.tunnels {
		open = append(open, t)
	}
	p.mu.Unlock()
	for _, t := range open {
		p.finish(t, core.NetClosedEarly)
		p.mu.Lock()
		conns := t.conns
		p.mu.Unlock()
		for _, c := range conns {
			_ = c.Close()
		}
	}
	for _, s := range servers {
		_ = s.Close()
	}
}
