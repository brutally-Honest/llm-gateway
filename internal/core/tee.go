package core

import (
	"errors"
	"io"
	"net/http"
	"net/http/httputil"
	"sync"
)

// bodyCopy is the copy half both tees share: it checks capture.max_body_bytes first,
// then reserves each chunk from the exchange's reservation before copying it, so the
// bytes held always equal the bytes reserved. Its mutex lets the reservation clear
// it from the other tee's goroutine when the exchange is dropped.
type bodyCopy struct {
	res *reservation
	max int64

	mu      sync.Mutex
	body    Body
	stopped bool // the cap was reached or the exchange was dropped
}

// newBodyCopy returns a copy attached to res.
func newBodyCopy(res *reservation, maxBytes int64) *bodyCopy {
	c := &bodyCopy{res: res, max: maxBytes}
	res.attach(c)
	return c
}

// add copies p as one chunk, up to the cap. It never fails: a refused reservation
// drops the exchange and the copy, and forwarding is not its concern.
func (c *bodyCopy) add(p []byte) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.stopped || len(p) == 0 {
		return
	}
	if c.res.isDropped() {
		c.discard()
		return
	}
	n := int64(len(p))
	if room := c.max - c.body.Size; n > room {
		n = room
		c.body.Truncated = true
		c.stopped = true
	}
	if n == 0 {
		return
	}
	if !c.res.reserve(n, c) {
		c.discard()
		return
	}
	chunk := make([]byte, n)
	copy(chunk, p)
	c.body.Chunks = append(c.body.Chunks, chunk)
	c.body.Size += n
}

// clear lets go of the chunks when the other tee's chunk dropped the exchange.
func (c *bodyCopy) clear() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.discard()
}

// discard lets go of the chunks of a dropped exchange; the reservation has already
// given their bytes back to the budget. The caller holds c.mu.
func (c *bodyCopy) discard() {
	c.body = Body{}
	c.stopped = true
}

// result is the copy as it stands, or an empty Body if the exchange was dropped.
func (c *bodyCopy) result() Body {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.res.isDropped() {
		c.discard()
	}
	return c.body
}

// size is the bytes the copy holds now.
func (c *bodyCopy) size() int64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.body.Size
}

// teeReader copies the outbound request body as the transport reads it: what each
// Read returns is what goes upstream. The transport reads on its own goroutine and
// the handler seals on its own, so a mutex guards the copy; the Read itself runs
// outside the lock, so a seal never waits on a blocked read.
type teeReader struct {
	rc io.ReadCloser

	mu     sync.Mutex
	copy   *bodyCopy
	sealed bool
	sawEOF bool
}

func newTeeReader(rc io.ReadCloser, res *reservation, maxBytes int64) *teeReader {
	return &teeReader{rc: rc, copy: newBodyCopy(res, maxBytes)}
}

// Read passes every byte and error through unchanged. After the seal it only passes
// through.
func (t *teeReader) Read(p []byte) (int, error) {
	n, err := t.rc.Read(p)
	t.mu.Lock()
	if !t.sealed {
		t.copy.add(p[:n])
		if errors.Is(err, io.EOF) {
			t.sawEOF = true
		}
	}
	t.mu.Unlock()
	return n, err
}

func (t *teeReader) Close() error { return t.rc.Close() }

// seal stops the copy and returns it, with complete reporting whether the body's
// io.EOF was read before the seal. An incomplete copy is the exchange's
// request_incomplete. Sealing again returns the same copy.
func (t *teeReader) seal() (body Body, complete bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.sealed = true
	return t.copy.result(), t.sawEOF
}

// teeRequestBody wraps the outbound body in a request tee and returns it, or returns
// nil when there is no body. It never sets GetBody: without one the transport cannot
// replay a body it has written, so the copy is the bytes read once (spec, Tee).
// http.NoBody is left alone, since a wrapped one would change how the transport
// frames the request.
func teeRequestBody(pr *httputil.ProxyRequest, res *reservation, maxBytes int64) *teeReader {
	if pr.Out.Body == nil || pr.Out.Body == http.NoBody {
		return nil
	}
	t := newTeeReader(pr.Out.Body, res, maxBytes)
	pr.Out.Body = t
	return t
}

// teeWriter copies the response body as it is written to the client. Each Write goes
// to the inner writer first and only what it accepted is copied, so the copy never
// delays a byte. It records the status and a header snapshot when the header goes
// out. It runs on the handler's goroutine only, as Meta does, so it needs no lock.
// Unwrap lets http.NewResponseController reach the inner writer's Flush.
type teeWriter struct {
	http.ResponseWriter

	copy        *bodyCopy
	wroteHeader bool
	status      int
	header      http.Header
	sealed      bool
}

func newTeeWriter(w http.ResponseWriter, res *reservation, maxBytes int64) *teeWriter {
	return &teeWriter{ResponseWriter: w, copy: newBodyCopy(res, maxBytes)}
}

// WriteHeader records the first final status and the headers sent with it. An
// informational 1xx is forwarded but not recorded: more headers follow it.
func (t *teeWriter) WriteHeader(code int) {
	if !t.wroteHeader && code >= http.StatusOK {
		t.record(code)
	}
	t.ResponseWriter.WriteHeader(code)
}

// record snapshots the status and headers, before the inner writer sends them.
func (t *teeWriter) record(code int) {
	t.wroteHeader = true
	t.status = code
	t.header = t.ResponseWriter.Header().Clone()
}

// Write forwards p, then copies what the inner writer accepted.
func (t *teeWriter) Write(p []byte) (int, error) {
	if !t.wroteHeader {
		t.record(http.StatusOK) // net/http's implicit WriteHeader(200)
	}
	n, err := t.ResponseWriter.Write(p)
	if !t.sealed {
		t.copy.add(p[:n])
	}
	return n, err
}

// Unwrap returns the inner writer, for http.NewResponseController.
func (t *teeWriter) Unwrap() http.ResponseWriter { return t.ResponseWriter }

// seal stops the copy and returns it. Sealing again returns the same copy.
func (t *teeWriter) seal() Body {
	t.sealed = true
	return t.copy.result()
}
