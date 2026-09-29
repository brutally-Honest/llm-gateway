package core

import (
	"net/http"
	"net/http/httputil"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// Budget is the process-wide memory bound on captured bytes: in-flight tee copies and
// queued exchanges share it (capture.memory_limit). Every method is safe for
// concurrent use.
type Budget struct {
	limit   int64
	inUse   atomic.Int64
	peak    atomic.Int64
	dropped atomic.Int64
}

// NewBudget returns a Budget that holds at most limit bytes at once.
func NewBudget(limit int64) *Budget {
	return &Budget{limit: limit}
}

// TryReserve takes n bytes from the budget, or takes nothing and returns false when
// that would cross the limit. A successful reservation raises the peak.
func (b *Budget) TryReserve(n int64) bool {
	for {
		cur := b.inUse.Load()
		next := cur + n
		if next > b.limit {
			return false
		}
		if b.inUse.CompareAndSwap(cur, next) {
			b.raisePeak(next)
			return true
		}
	}
}

// raisePeak sets the peak to v if v is higher: a compare-and-swap max (research Q12).
func (b *Budget) raisePeak(v int64) {
	for {
		p := b.peak.Load()
		if v <= p || b.peak.CompareAndSwap(p, v) {
			return
		}
	}
}

// Release gives n reserved bytes back.
func (b *Budget) Release(n int64) {
	b.inUse.Add(-n)
}

// InUse is the number of bytes reserved now.
func (b *Budget) InUse() int64 {
	return b.inUse.Load()
}

// Peak is the highest InUse seen since the budget was made. Release never lowers it;
// it is logged as memory_peak_bytes at shutdown.
func (b *Budget) Peak() int64 {
	return b.peak.Load()
}

// Dropped is the number of exchanges dropped because the budget refused one of their
// chunks (dropped_memory). The sink never sees them, so they are counted here; it is
// logged with the sink's counts at shutdown.
func (b *Budget) Dropped() int64 {
	return b.dropped.Load()
}

// Body is one captured body: the bytes as they crossed the wire, still
// content-encoded, as one chunk per Read or Write.
type Body struct {
	Chunks    [][]byte
	Size      int64
	Truncated bool // bytes past capture.max_body_bytes were forwarded but not copied
}

// Bytes joins the chunks. It allocates a second copy, so it is called in a capture
// worker, never on the request path.
func (b Body) Bytes() []byte {
	out := make([]byte, 0, b.Size)
	for _, c := range b.Chunks {
		out = append(out, c...)
	}
	return out
}

// reservation is one exchange's share of the Budget, held by both its tees. The
// request tee reserves on the transport's goroutine and the response tee on the
// handler's, so it holds a mutex. When the budget refuses a chunk, everything the
// exchange holds goes back at once and the exchange is dropped: neither tee copies
// again, and both copies let go of their chunks there and then.
type reservation struct {
	budget  *Budget
	mu      sync.Mutex
	held    int64
	dropped bool
	copies  []*bodyCopy
}

func newReservation(b *Budget) *reservation {
	return &reservation{budget: b}
}

// attach registers a tee's copy, so a drop can clear it.
func (r *reservation) attach(c *bodyCopy) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.copies = append(r.copies, c)
}

// reserve takes n bytes for from, the copy asking. It returns false once the
// exchange is dropped, whether by this call or an earlier one. The call that drops
// it clears every other attached copy before returning; from, whose lock its caller
// holds, discards its own. The other copies are cleared after r.mu is released, and
// only one call ever drops, so no two locks are waited on in opposite orders.
func (r *reservation) reserve(n int64, from *bodyCopy) bool {
	r.mu.Lock()
	if r.dropped {
		r.mu.Unlock()
		return false
	}
	if r.budget.TryReserve(n) {
		r.held += n
		r.mu.Unlock()
		return true
	}
	r.budget.Release(r.held)
	r.held = 0
	r.dropped = true
	others := r.copies
	r.mu.Unlock()
	for _, c := range others {
		if c != from {
			c.clear()
		}
	}
	return false
}

// isDropped reports whether the budget refused one of the exchange's chunks.
func (r *reservation) isDropped() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.dropped
}

// release gives back everything the exchange holds. It is idempotent.
func (r *reservation) release() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.budget.Release(r.held)
	r.held = 0
}

// heldBytes is what the exchange holds now.
func (r *reservation) heldBytes() int64 {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.held
}

// Capture turns capture on for every proxy a Registry builds. Sink takes the
// finished exchanges, Budget bounds the bytes held in flight and in the queue, and
// MaxBodyBytes caps each copied body (capture.max_body_bytes). A nil Principal is
// LocalPrincipal.
type Capture struct {
	Sink         CaptureSink
	Budget       *Budget
	MaxBodyBytes int64
	Principal    PrincipalResolver
}

// CaptureSink takes finished, redacted exchanges. Submit never blocks. It owns ex
// either way: on false (queue full or closed) it has already released ex's memory.
type CaptureSink interface {
	Submit(ex *Exchange) bool
}

// What became of an exchange's capture, logged as the access line's capture field.
const (
	CaptureQueued           = "queued"
	CaptureDroppedQueueFull = "dropped_queue_full"
	CaptureDroppedMemory    = "dropped_memory"
	CaptureOff              = "off"
)

// Exchange is one proxied request as captured: the record the spec's Exchange record
// lists, already redacted. It holds its bodies' share of the Budget until Release.
type Exchange struct {
	RequestID, PrincipalID, Protocol, Client string
	Auth                                     AuthKind
	Method, Path, Query                      string      // Path has the prefix stripped; Query is redacted
	RequestHeader, ResponseHeader            http.Header // redacted clones
	Status                                   int
	Start, End                               time.Time
	TTFB                                     time.Duration
	HasTTFB                                  bool

	Stream, ClientDisconnected, UpstreamAborted bool
	RequestIncomplete                           bool // the request copy was sealed before the body's EOF
	GatewayError                                string

	Request, Response Body
	Parser            Parser   // nil: parse skipped
	Excluded          []string // the parser's HashExcludedFields

	res *reservation
}

// Release gives the exchange's bytes back to the Budget. It is idempotent, and the
// worker calls it once the exchange is stored, whatever the outcome.
func (ex *Exchange) Release() {
	if ex.res != nil {
		ex.res.release()
	}
}

// captureConfig is what one proxy needs to capture: the Capture, and what its adapter
// declares through the optional SecretDeclarer and ParsingAdapter.
type captureConfig struct {
	Capture
	prefix        string
	secretHeaders []string
	secretQuery   []string
	parser        Parser
	excluded      []string
}

// newCaptureConfig reads adapter a's optional declarations once, when its proxy is
// built. A nil c means capture is off.
func newCaptureConfig(a Adapter, c *Capture) *captureConfig {
	if c == nil {
		return nil
	}
	cfg := &captureConfig{Capture: *c, prefix: a.Prefix()}
	if cfg.Principal == nil {
		cfg.Principal = LocalPrincipal{}
	}
	if d, ok := a.(SecretDeclarer); ok {
		cfg.secretHeaders = slices.Clone(d.SecretHeaders())
		cfg.secretQuery = slices.Clone(d.SecretQueryParams())
	}
	if pa, ok := a.(ParsingAdapter); ok {
		if p := pa.Parser(); p != nil {
			cfg.parser = p
			cfg.excluded = slices.Clone(p.HashExcludedFields())
		}
	}
	return cfg
}

// captureState is one exchange's capture in flight, held by its Meta: both tees,
// their shared reservation, and what the request looked like.
type captureState struct {
	cfg       *captureConfig
	res       *reservation
	reqTee    *teeReader // nil when the request has no body
	resTee    *teeWriter
	reqHeader http.Header // as sent upstream, before redaction
	method    string
	path      string // prefix stripped
	rawQuery  string // as sent, before redaction
}

// startCapture begins m's capture and returns the writer the proxy must write
// through: w itself when capture is off, else the response tee around it.
func (cfg *captureConfig) startCapture(m *Meta, w http.ResponseWriter, r *http.Request) http.ResponseWriter {
	if cfg == nil {
		m.Capture = CaptureOff
		return w
	}
	res := newReservation(cfg.Budget)
	tw := newTeeWriter(w, res, cfg.MaxBodyBytes)
	m.capture = &captureState{
		cfg:      cfg,
		res:      res,
		resTee:   tw,
		method:   r.Method,
		path:     strings.TrimPrefix(r.URL.Path, cfg.prefix),
		rawQuery: r.URL.RawQuery,
	}
	return tw
}

// teeRequest wraps the outbound body in the request tee and snapshots the headers
// as they go upstream. Rewrite calls it after 001's five steps and before the body
// watcher, so the body is watcher(tee(body)).
func teeRequest(pr *httputil.ProxyRequest) {
	m := MetaFrom(pr.Out.Context())
	if m == nil || m.capture == nil {
		return
	}
	c := m.capture
	c.reqHeader = pr.Out.Header.Clone()
	c.reqTee = teeRequestBody(pr, c.res, c.cfg.MaxBodyBytes)
}

// finishCapture seals both tees, builds the redacted Exchange and submits it, and
// sets m.Capture to what became of it. It runs from Settle, once the abort flags are
// decided, on the handler's goroutine. Nothing here waits: Submit never blocks.
func (m *Meta) finishCapture() {
	c := m.capture
	if c == nil {
		return
	}
	m.capture = nil
	var reqBody Body
	complete := true
	if c.reqTee != nil {
		reqBody, complete = c.reqTee.seal()
	}
	resBody := c.resTee.seal()
	// Both tees are sealed, so nothing reserves any more and a drop is final.
	if c.res.isDropped() {
		c.res.release()
		c.cfg.Budget.dropped.Add(1)
		m.Capture = CaptureDroppedMemory
		return
	}
	status := c.resTee.status
	if status == 0 {
		status = m.Status
	}
	ex := &Exchange{
		RequestID:          m.RequestID,
		PrincipalID:        m.PrincipalID,
		Protocol:           m.Protocol,
		Client:             m.Client,
		Auth:               m.Auth,
		Method:             c.method,
		Path:               c.path,
		Query:              redactQuery(c.rawQuery, c.cfg.secretQuery),
		RequestHeader:      redactHeader(c.reqHeader, c.cfg.secretHeaders),
		ResponseHeader:     redactHeader(c.resTee.header, c.cfg.secretHeaders),
		Status:             status,
		Start:              m.start,
		End:                time.Now(),
		TTFB:               m.TTFB,
		HasTTFB:            m.HasTTFB,
		Stream:             m.Stream,
		ClientDisconnected: m.ClientDisconnected,
		UpstreamAborted:    m.UpstreamAborted,
		RequestIncomplete:  !complete,
		GatewayError:       m.GatewayError,
		Request:            reqBody,
		Response:           resBody,
		Parser:             c.cfg.parser,
		Excluded:           slices.Clone(c.cfg.excluded),
		res:                c.res,
	}
	if c.cfg.Sink.Submit(ex) {
		m.Capture = CaptureQueued
		return
	}
	ex.Release() // the sink has released it already; Release is idempotent
	m.Capture = CaptureDroppedQueueFull
}
