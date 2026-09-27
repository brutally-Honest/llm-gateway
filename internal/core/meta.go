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
	Protocol           string
	Client             string
	Auth               AuthKind
	Stream             bool
	TTFB               time.Duration
	HasTTFB            bool
	GatewayError       string
	ClientDisconnected bool
	UpstreamAborted    bool

	start   time.Time // when the proxy handler started; TTFB is measured from it
	reqBody *requestWatcher
	resBody *responseWatcher
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
func (m *Meta) Settle(ctx context.Context) {
	if m == nil {
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
}

func (w *requestWatcher) Read(p []byte) (int, error) {
	n, err := w.rc.Read(p)
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
}

func (w *responseWatcher) Read(p []byte) (int, error) {
	n, err := w.rc.Read(p)
	if err != nil && !errors.Is(err, io.EOF) && w.err == nil {
		w.err = err
	}
	return n, err
}

func (w *responseWatcher) Close() error { return w.rc.Close() }
