package core

import (
	"context"
	"io"
	"net/http"
	"net/http/httputil"
	"time"

	"go.uber.org/zap"

	"github.com/brutally-honest/llm-gateway/internal/config"
)

// Test-only doors to the unexported body watchers, for package core_test. The proxy
// wraps the bodies itself; tests reach the same code through these.

func WatchRequestBody(m *Meta, rc io.ReadCloser) io.ReadCloser  { return m.watchRequestBody(rc) }
func WatchResponseBody(m *Meta, rc io.ReadCloser) io.ReadCloser { return m.watchResponseBody(rc) }
func RequestBodyErr(m *Meta) error                              { return m.requestBodyErr() }
func ResponseBodyErr(m *Meta) error                             { return m.responseBodyErr() }

// FlushInterval is the built ReverseProxy's flush interval.
func FlushInterval(p *Proxy) time.Duration { return p.rp.FlushInterval }

// Classify is the error handler's status and reason for err; ctx is the inbound
// request's context.
func Classify(ctx context.Context, m *Meta, err error) (int, string) { return classify(ctx, m, err) }

// Test-only doors to the capture tees and the per-exchange reservation.

type (
	Reservation = reservation
	TeeReader   = teeReader
	TeeWriter   = teeWriter
)

func NewReservation(b *Budget) *Reservation   { return newReservation(b) }
func ReservationHeld(r *Reservation) int64    { return r.heldBytes() }
func ReservationDropped(r *Reservation) bool  { return r.isDropped() }
func ReleaseReservation(r *Reservation)       { r.release() }
func SealTeeReader(t *TeeReader) (Body, bool) { return t.seal() }
func SealTeeWriter(t *TeeWriter) Body         { return t.seal() }

func NewTeeReader(rc io.ReadCloser, r *Reservation, maxBytes int64) *TeeReader {
	return newTeeReader(rc, r, maxBytes)
}

func NewTeeWriter(w http.ResponseWriter, r *Reservation, maxBytes int64) *TeeWriter {
	return newTeeWriter(w, r, maxBytes)
}

// TeeWriterResponse is the status and header snapshot the response tee recorded.
func TeeWriterResponse(t *TeeWriter) (int, http.Header) { return t.status, t.header }

func TeeRequestBody(pr *httputil.ProxyRequest, r *Reservation, maxBytes int64) *TeeReader {
	return teeRequestBody(pr, r, maxBytes)
}

// TeeReaderCopied and TeeWriterCopied are the bytes each tee's copy holds now.
func TeeReaderCopied(t *TeeReader) int64 { return t.copy.size() }
func TeeWriterCopied(t *TeeWriter) int64 { return t.copy.size() }

// Test-only doors to redaction.

func RedactHeader(h http.Header, secret []string) http.Header { return redactHeader(h, secret) }
func RedactQuery(raw string, secret []string) string          { return redactQuery(raw, secret) }

// NewCapturingProxy is NewProxy with capture on, for a test that must reach the
// proxy itself rather than go through a Registry.
func NewCapturingProxy(a Adapter, up config.Upstream, identify func(*http.Request) string, log *zap.Logger, c Capture) *Proxy {
	return newProxy(a, up, identify, log, &c)
}

// WrapTransport puts wrap around the proxy's transport, so a test can see the
// outgoing request as the transport gets it.
func WrapTransport(p *Proxy, wrap func(http.RoundTripper) http.RoundTripper) {
	p.rp.Transport = wrap(p.rp.Transport)
}
