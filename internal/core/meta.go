package core

import (
	"context"
	"errors"
	"io"
	"sync/atomic"
	"time"
)

// Meta is what the proxy learns about one request, for the access log. It lives in
// the request context: the access log creates it with WithMeta before the handler
// chain and reads it after. The proxy's hooks run on the handler's goroutine, so the
// exported fields need no lock; the one value written from another goroutine, the
// request-body error, sits in an atomic inside its watcher.
type Meta struct {
	// RequestID is the gateway's ID for the request, set by the access log.
	RequestID string
	// PrincipalID is who sent the request, set by the proxy.
	PrincipalID string

	Protocol           string
	Client             string
	Auth               AuthKind
	Stream             bool
	TTFB               time.Duration
	HasTTFB            bool
	GatewayError       string
	ClientDisconnected bool
	UpstreamAborted    bool
	// Status is the final status the proxy answered with: upstream's, or the one its
	// error handler wrote. 0 until one is known.
	Status int
	// Capture is what became of the exchange's capture: CaptureQueued,
	// CaptureDroppedQueueFull, CaptureDroppedMemory or CaptureOff. It is decided in
	// Settle, before the access line is written, and is "" off the proxy.
	Capture string
	// OnUsage, when set before the proxy runs, is called once from Settle with what
	// the response reported. Core sets nothing here: a middleware that counts tokens
	// does. Unset, the response is not observed at all.
	OnUsage func(UsageResult)

	start      time.Time // when the proxy handler started; TTFB is measured from it
	dispatched time.Time // when the call was handed to the transport; zero if never
	reqBody    *requestWatcher
	resBody    *responseWatcher
	sessionKey string        // validated; "" when the request has none
	usage      *usageWriter  // nil when OnUsage is unset or the adapter is not a RateLimitAdapter
	capture    *captureState // nil when capture is off or already finished
	complete   bool          // the response body hit a clean EOF and all of it reached the client
	trace      *callTrace    // nil when tracing is off (spec 021)
}

type metaKey struct{}

// WithMeta returns a context holding a new, empty Meta, and that Meta.
func WithMeta(ctx context.Context) (context.Context, *Meta) {
	m := &Meta{}
	return context.WithValue(ctx, metaKey{}, m), m
}

// MetaFrom returns the request's Meta, or nil outside a request that has one.
func MetaFrom(ctx context.Context) *Meta {
	m, _ := ctx.Value(metaKey{}).(*Meta)
	return m
}

// Settle decides the two abort flags once the handler is done; ctx is the inbound
// request's context. If the response body failed to read while the client was still
// there, upstream cut the response: UpstreamAborted. Otherwise, a cancelled context
// means the client left: ClientDisconnected. When the client leaves, the outbound
// read fails with context.Canceled too, which is why the inbound context decides.
// Then it finishes the exchange's capture, so Capture is set when Settle returns, and
// reports the response's usage to OnUsage, once.
func (m *Meta) Settle(ctx context.Context) {
	if m == nil {
		return
	}
	m.settleAbort(ctx)
	m.finishCapture()
	m.finishUsage()
	m.trace.finish(m)
	m.trace = nil
}

// requestBytes is how many request body bytes were read and sent upstream.
func (m *Meta) requestBytes() int64 {
	if m.reqBody == nil {
		return 0
	}
	return m.reqBody.n.Load()
}

// responseBytes is how many response body bytes were read from upstream.
func (m *Meta) responseBytes() int64 {
	if m.resBody == nil {
		return 0
	}
	return m.resBody.n
}

func (m *Meta) settleAbort(ctx context.Context) {
	// A response that reached the client whole is never a disconnect or an abort: the
	// client may close before Settle runs, and net/http then cancels ctx.
	if m.complete {
		return
	}
	// The gateway's shutdown cut it (spec 019): neither side ended it.
	if cutByShutdown(ctx) {
		m.GatewayError = reasonShutdown
		return
	}
	if m.responseBodyErr() != nil && ctx.Err() == nil {
		m.UpstreamAborted = true
		return
	}
	if ctx.Err() != nil {
		m.ClientDisconnected = true
	}
}

// Overhead is the time from the proxy taking the call to handing it to the transport,
// what the gateway itself adds before upstream sees it (spec 021). False when the call
// was never sent.
func (m *Meta) Overhead() (time.Duration, bool) {
	if m == nil || m.start.IsZero() || m.dispatched.IsZero() {
		return 0, false
	}
	return m.dispatched.Sub(m.start), true
}

// Elapsed is the time since the proxy took the call; 0 off the proxy.
func (m *Meta) Elapsed() time.Duration {
	if m == nil || m.start.IsZero() {
		return 0
	}
	return time.Since(m.start)
}

// markResponseComplete records that the response was forwarded whole: the upstream
// body reached a clean EOF. The caller vouches that the proxy wrote it without error.
func (m *Meta) markResponseComplete() {
	if m.resBody != nil && m.resBody.eof && m.resBody.err == nil {
		m.complete = true
	}
}

// watchRequestBody wraps the outbound request body so a failure reading the client's
// body can be told apart from an upstream failure.
func (m *Meta) watchRequestBody(rc io.ReadCloser) io.ReadCloser {
	m.reqBody = &requestWatcher{rc: rc}
	return m.reqBody
}

// watchResponseBody wraps the upstream response body so an upstream failure after
// headers can be told apart from a client that left.
func (m *Meta) watchResponseBody(rc io.ReadCloser) io.ReadCloser {
	m.resBody = &responseWatcher{rc: rc}
	return m.resBody
}

// requestBodyErr is the first error reading the request body, or nil. The value is
// for classification only and is never logged.
func (m *Meta) requestBodyErr() error {
	if m.reqBody == nil {
		return nil
	}
	if p := m.reqBody.err.Load(); p != nil {
		return *p
	}
	return nil
}

// responseBodyErr is the first error reading the response body, or nil. The value is
// for classification only and is never logged.
func (m *Meta) responseBodyErr() error {
	if m.resBody == nil {
		return nil
	}
	return m.resBody.err
}

// requestWatcher passes every Read and Close straight through, changing no byte and
// buffering nothing, and keeps the first read error that is not io.EOF. The transport
// reads the request body on its own goroutine, so the error is atomic.
type requestWatcher struct {
	rc  io.ReadCloser
	err atomic.Pointer[error]
	n   atomic.Int64 // bytes read, for the trace (spec 021)
}

func (w *requestWatcher) Read(p []byte) (int, error) {
	n, err := w.rc.Read(p)
	w.n.Add(int64(n))
	if err != nil && !errors.Is(err, io.EOF) {
		w.err.CompareAndSwap(nil, &err)
	}
	return n, err
}

func (w *requestWatcher) Close() error { return w.rc.Close() }

// responseWatcher is requestWatcher for the response body, which is read on the
// handler's goroutine, so the error needs no atomic.
type responseWatcher struct {
	rc  io.ReadCloser
	err error
	eof bool  // a Read returned io.EOF
	n   int64 // bytes read, for the trace (spec 021)
}

func (w *responseWatcher) Read(p []byte) (int, error) {
	n, err := w.rc.Read(p)
	w.n += int64(n)
	if err == io.EOF {
		w.eof = true
	} else if err != nil && w.err == nil {
		w.err = err
	}
	return n, err
}

func (w *responseWatcher) Close() error { return w.rc.Close() }
