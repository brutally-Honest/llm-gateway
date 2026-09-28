package core

import (
	"sync"
	"sync/atomic"
)

// Budget is the process-wide memory bound on captured bytes: in-flight tee copies and
// queued exchanges share it (capture.memory_limit). Every method is safe for
// concurrent use.
type Budget struct {
	limit int64
	inUse atomic.Int64
	peak  atomic.Int64
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
// again, and each discards its chunks the next time it is touched.
type reservation struct {
	budget  *Budget
	mu      sync.Mutex
	held    int64
	dropped bool
}

func newReservation(b *Budget) *reservation {
	return &reservation{budget: b}
}

// reserve takes n bytes for the exchange. It returns false once the exchange is
// dropped, whether by this call or an earlier one.
func (r *reservation) reserve(n int64) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.dropped {
		return false
	}
	if r.budget.TryReserve(n) {
		r.held += n
		return true
	}
	r.budget.Release(r.held)
	r.held = 0
	r.dropped = true
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
