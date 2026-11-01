// Package hooks ingests the lifecycle events a client's harness reports beside the
// exchanges. The sink queues each delivery, maps it through the client's profile and
// stores it. It knows no client: a profile hands it a core.HarnessMapper by name.
package hooks

import (
	"context"
	"encoding/json"
	"sync"
	"sync/atomic"
	"time"

	"go.uber.org/zap"

	"github.com/brutally-honest/llm-gateway/internal/core"
	"github.com/brutally-honest/llm-gateway/internal/logging"
	"github.com/brutally-honest/llm-gateway/internal/store"
)

// UnknownClient labels a delivery whose client names no profile with a mapper.
const UnknownClient = "unknown"

// Store is what the workers write through. It honours ctx, which the sink cancels
// when shutdown runs out of time.
type Store interface {
	SaveHook(ctx context.Context, h *store.HookRecord) error
}

// HookStored names a hook event that is stored. It carries no body.
type HookStored struct {
	Principal, Client, SessionKey string
}

// Config sizes the sink: hooks.queue_size, hooks.workers (both greater than 0) and
// hooks.memory_limit, the bytes queued bodies may hold together.
//
// Mappers finds a client's mapper by name; a nil result or a nil Mappers means the
// client is stored as unknown. OnStored, when set, is called on the worker after each
// successful save, so it must not block. A panic in it is recovered.
type Config struct {
	QueueSize, Workers int
	MemoryLimit        int64
	Mappers            func(client string) core.HarnessMapper
	OnStored           func(HookStored)
}

// Counts are the sink's drop and failure counts.
type Counts struct {
	// QueueFull is every delivery Submit refused: the queue was full, or the sink closed.
	QueueFull int64
	// Oversize is every delivery dropped because its body would cross the memory limit.
	Oversize int64
	// CaptureOff is every delivery answered but not stored because capture is off.
	CaptureOff int64
	// NotJSON is every stored delivery whose body is not valid JSON.
	NotJSON int64
	// StoreFailed is every SaveHook that failed.
	StoreFailed int64
}

type item struct {
	client     string
	body       []byte
	receivedAt time.Time
}

// Sink is a bounded queue of hook deliveries drained by workers. Submit never blocks.
// Every method is safe for concurrent use.
type Sink struct {
	store    Store
	log      *zap.Logger
	mappers  func(string) core.HarnessMapper
	onStored func(HookStored)
	budget   *core.Budget

	// mu orders Submit against Close, as in the capture sink.
	mu     sync.RWMutex
	closed bool
	queue  chan item

	ctx     context.Context
	cancel  context.CancelFunc
	workers []<-chan error

	queueFull   atomic.Int64
	oversize    atomic.Int64
	notJSON     atomic.Int64
	captureOff  atomic.Int64
	storeFailed atomic.Int64
	abandoned   atomic.Int64
}

// NewSink makes the queue and starts cfg.Workers workers through logging.Go.
func NewSink(cfg Config, st Store, log *zap.Logger) *Sink {
	ctx, cancel := context.WithCancel(context.Background())
	s := &Sink{
		store:    st,
		log:      log,
		mappers:  cfg.Mappers,
		onStored: cfg.OnStored,
		budget:   core.NewBudget(cfg.MemoryLimit),
		queue:    make(chan item, cfg.QueueSize),
		ctx:      ctx,
		cancel:   cancel,
	}
	for range cfg.Workers {
		s.workers = append(s.workers, logging.Go(log, "hooks_worker", s.work))
	}
	return s
}

// Submit queues one delivery without waiting; the sink owns body from here. It
// returns false, counting and logging one line, when the sink is closed, the queue is
// full or body would cross the memory limit.
func (s *Sink) Submit(client string, body []byte) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	n := int64(len(body))
	if !s.closed {
		if !s.budget.TryReserve(n) {
			s.oversize.Add(1)
			s.log.Warn("hook event dropped: memory limit", zap.String("client", client), zap.Int64("bytes", n))
			return false
		}
		select {
		case s.queue <- item{client: client, body: body, receivedAt: time.Now()}:
			return true
		default:
		}
		s.budget.Release(n)
	}
	s.queueFull.Add(1)
	s.log.Warn("hook event dropped: queue full", zap.String("client", client), zap.Int64("bytes", n))
	return false
}

// RecordOversize counts and logs one delivery the handler refused for its size.
func (s *Sink) RecordOversize(client string, limit int64) {
	s.oversize.Add(1)
	s.log.Warn("hook event dropped: body over limit", zap.String("client", client), zap.Int64("limit", limit))
}

// RecordCaptureOff counts one delivery answered but not stored because capture is off.
func (s *Sink) RecordCaptureOff() { s.captureOff.Add(1) }

// Close stops accepting, then lets the workers drain the queue until ctx is done, at
// which point it cancels the workers' context and returns how many deliveries were
// left unstored. Later calls return 0.
func (s *Sink) Close(ctx context.Context) (undrained int) {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return 0
	}
	s.closed = true
	close(s.queue)
	s.mu.Unlock()

	finished := 0
wait:
	for finished < len(s.workers) {
		select {
		case <-s.workers[finished]:
			finished++
		case <-ctx.Done():
			break wait
		}
	}
	s.cancel()
	for _, w := range s.workers[finished:] {
		<-w
	}
	n := s.abandoned.Load()
	for it := range s.queue {
		s.budget.Release(int64(len(it.body)))
		n++
	}
	return int(n)
}

// Counts is a snapshot of the sink's counts.
func (s *Sink) Counts() Counts {
	return Counts{
		QueueFull:   s.queueFull.Load(),
		Oversize:    s.oversize.Load(),
		CaptureOff:  s.captureOff.Load(),
		NotJSON:     s.notJSON.Load(),
		StoreFailed: s.storeFailed.Load(),
	}
}

func (s *Sink) work() error {
	for it := range s.queue {
		if s.ctx.Err() != nil {
			s.budget.Release(int64(len(it.body)))
			s.abandoned.Add(1)
			return nil
		}
		s.process(it)
	}
	return nil
}

func (s *Sink) process(it item) {
	defer s.budget.Release(int64(len(it.body)))
	client := UnknownClient
	var mapper core.HarnessMapper
	if s.mappers != nil {
		if mapper = s.mappers(it.client); mapper != nil {
			client = it.client
		}
	}
	if !json.Valid(it.body) {
		s.notJSON.Add(1)
	}
	ev := s.mapBody(mapper, it.body)
	rec := &store.HookRecord{
		PrincipalID: core.PrincipalLocal,
		Client:      client,
		ReceivedAt:  it.receivedAt,
		Payload:     it.body,
		Event:       ev,
	}
	if err := s.store.SaveHook(s.ctx, rec); err != nil {
		s.storeFailed.Add(1)
		s.log.Warn("hook event not stored", zap.String("client", client), zap.Error(err))
		return
	}
	s.notify(HookStored{Principal: rec.PrincipalID, Client: client, SessionKey: ev.SessionKey})
}

// mapBody maps body through m; no mapper, or a panic in it, gives an unknown event.
func (s *Sink) mapBody(m core.HarnessMapper, body []byte) (ev core.HarnessEvent) {
	unknown := core.HarnessEvent{Phase: core.PhaseUnknown}
	if m == nil {
		return unknown
	}
	defer func() {
		if v := recover(); v != nil {
			logging.LogPanic(s.log, "hook mapper panicked", v)
			ev = unknown
		}
	}()
	ev = m.MapHarness(body)
	if ev.Phase == "" {
		ev.Phase = core.PhaseUnknown
	}
	return ev
}

func (s *Sink) notify(h HookStored) {
	if s.onStored == nil {
		return
	}
	defer func() {
		if v := recover(); v != nil {
			logging.LogPanic(s.log, "hook OnStored panicked", v)
		}
	}()
	s.onStored(h)
}
