package ui

import (
	"container/heap"
	"context"
	"runtime"

	"github.com/brutally-honest/llm-gateway/internal/store"
)

// gateSlots is how many gated memo computes run at once (spec 043 D12). A cache takes it
// when it is made, so a test or a tuning sets it before that.
var gateSlots = max(2, runtime.GOMAXPROCS(0)/4)

const (
	classBackground = iota // the background pass
	classList              // a list request or a stream snapshot
)

// gatePrio is where a gated run ranks: list work above the background pass; among list
// work the newest request first, and within a request the earlier row first; then the
// order they were queued.
type gatePrio struct {
	class int
	tick  uint64 // the list request's number; larger is newer
	pos   int    // the row's position in its request
	seq   uint64 // queue order, set by the gate
}

func (p *gatePrio) clone() *gatePrio {
	if p == nil {
		return nil
	}
	c := *p
	return &c
}

// above reports whether p ranks strictly above o, ignoring the queue order.
func (p gatePrio) above(o gatePrio) bool {
	if p.class != o.class {
		return p.class > o.class
	}
	if p.tick != o.tick {
		return p.tick > o.tick
	}
	return p.pos < o.pos
}

func (p gatePrio) before(o gatePrio) bool {
	if p.above(o) {
		return true
	}
	if o.above(p) {
		return false
	}
	return p.seq < o.seq
}

// gateState is the gate's queue and slots. One dispatcher, dispatchLocked, starts queued
// runs as slots free up; no goroutine waits per queued run. Guarded by the cache's fmu.
type gateState struct {
	slots int
	q     runHeap
	// inSlot holds the runs that hold a slot.
	inSlot map[*digestRun]struct{}
	seq    uint64
	// changed is closed, and replaced, whenever the queue or the slots change.
	changed chan struct{}
}

func (g *gateState) nextSeq() uint64 { g.seq++; return g.seq }

func (g *gateState) notify() {
	close(g.changed)
	g.changed = make(chan struct{})
}

// remove takes a queued run out of the queue.
func (g *gateState) remove(r *digestRun) {
	if r.queued {
		heap.Remove(&g.q, r.qi)
	}
}

// listWork reports whether list work is queued or running in a slot.
func (g *gateState) listWork() bool {
	for _, r := range g.q {
		if r.prio != nil && r.prio.class == classList {
			return true
		}
	}
	for r := range g.inSlot {
		if r.prio != nil && r.prio.class == classList {
			return true
		}
	}
	return false
}

// dispatchLocked starts the highest-ranked queued runs while slots are free. fmu is held.
func (c *DigestCache) dispatchLocked() {
	g := &c.gate
	for len(g.q) > 0 && len(g.inSlot) < g.slots {
		r := heap.Pop(&g.q).(*digestRun)
		r.inSlot = true
		g.inSlot[r] = struct{}{}
		c.launch(r.id, r)
	}
	g.notify()
}

// runHeap is the queue, best rank first; it keeps each run's index and queued flag.
type runHeap []*digestRun

func (h runHeap) Len() int           { return len(h) }
func (h runHeap) Less(i, j int) bool { return h[i].prio.before(*h[j].prio) }
func (h runHeap) Swap(i, j int) {
	h[i], h[j] = h[j], h[i]
	h[i].qi, h[j].qi = i, j
}
func (h *runHeap) Push(x any) {
	r := x.(*digestRun)
	r.qi, r.queued = len(*h), true
	*h = append(*h, r)
}
func (h *runHeap) Pop() any {
	old := *h
	r := old[len(old)-1]
	old[len(old)-1] = nil
	*h = old[:len(old)-1]
	r.queued = false
	return r
}

// newListTick numbers one list request or stream snapshot; its rows rank above every
// earlier request's.
func (c *DigestCache) newListTick() uint64 { return c.ticks.Add(1) }

// queue asks for the memo at ver without waiting: it joins or creates the run for it,
// ranked by gp in the build gate, and returns. A run already queued is raised to gp when
// gp ranks above it.
func (c *DigestCache) queue(id store.SessionIdent, ver store.Version, gp gatePrio,
	compute func(context.Context) (digestMemo, error)) {
	c.fmu.Lock()
	c.joinLocked(id, ver, &gp, compute)
	c.fmu.Unlock()
}

// getBackground is get for the background pass: the run it starts or joins ranks last in
// the gate. It waits for the result; the run's own timeout starts at its slot.
func (c *DigestCache) getBackground(ctx context.Context, id store.SessionIdent, ver store.Version,
	compute func(context.Context) (digestMemo, error)) (digestHit, error) {
	if e, ok := c.lookup(id, ver); ok {
		return e.hit(), nil
	}
	c.fmu.Lock()
	r := c.joinLocked(id, ver, &gatePrio{class: classBackground}, compute)
	c.fmu.Unlock()
	return c.wait(ctx, r)
}

// waitListIdle returns when no list work is queued or running in the gate, or ctx ends.
func (c *DigestCache) waitListIdle(ctx context.Context) {
	for {
		c.fmu.Lock()
		busy, ch := c.gate.listWork(), c.gate.changed
		c.fmu.Unlock()
		if !busy {
			return
		}
		select {
		case <-ch:
		case <-ctx.Done():
			return
		}
	}
}

// putFromDisk keeps a memo read from disk in memory; it is not written back.
func (c *DigestCache) putFromDisk(id store.SessionIdent, ver store.Version, m digestMemo) {
	m.fromDisk = true
	c.store(&digestEntry{id: id, ver: ver, memo: cloneMemo(m)})
}

// memoryMemo returns a copy of the session's memo in memory, whatever its version, and
// the version it was computed at. ok is false when memory holds none.
func (c *DigestCache) memoryMemo(id store.SessionIdent, ver store.Version) (digestMemo, store.Version, bool) {
	if e, ok := c.lookup(id, ver); ok {
		return cloneMemo(e.memo), e.ver, true
	}
	if e, ok := c.peek(id); ok {
		return cloneMemo(e.memo), e.ver, true
	}
	return digestMemo{}, store.Version{}, false
}
