package capture

import (
	"context"
	"sync"
	"sync/atomic"

	"go.uber.org/zap"

	"github.com/brutally-honest/llm-gateway/internal/core"
	"github.com/brutally-honest/llm-gateway/internal/logging"
)

// Config sizes the sink: capture.queue_size and capture.workers, both greater than 0
// (config rejects anything else).
type Config struct {
	QueueSize, Workers int
}

// Counts are the sink's drop and failure counts, logged in the shutdown line.
type Counts struct {
	// DroppedQueueFull is every exchange Submit refused: the queue was full, or the
	// sink was already closed.
	DroppedQueueFull int64
}

// Sink is the v1 CaptureSink: a bounded in-process queue drained by worker
// goroutines. Submit never blocks. Every method is safe for concurrent use.
type Sink struct {
	store Store

	// mu orders Submit against Close: Submit sends under the read lock, and Close
	// sets closed under the write lock before it closes queue, so no send ever
	// reaches a closed channel.
	mu     sync.RWMutex
	closed bool
	queue  chan *core.Exchange

	// ctx is the workers' context, and the one every store call gets. Close cancels
	// it when its own context runs out.
	ctx     context.Context
	cancel  context.CancelFunc
	workers []<-chan error

	droppedQueueFull atomic.Int64
	// abandoned counts exchanges a worker took off the queue after ctx was
	// cancelled and gave back unstored: they are undrained, like the ones left in it.
	abandoned atomic.Int64
}

var _ core.CaptureSink = (*Sink)(nil)

// NewSink makes the queue and starts cfg.Workers workers through logging.Go, each
// storing through st.
func NewSink(cfg Config, st Store, log *zap.Logger) *Sink {
	ctx, cancel := context.WithCancel(context.Background())
	s := &Sink{
		store:  st,
		queue:  make(chan *core.Exchange, cfg.QueueSize),
		ctx:    ctx,
		cancel: cancel,
	}
	for range cfg.Workers {
		s.workers = append(s.workers, logging.Go(log, "capture_worker", s.work))
	}
	return s
}

// Submit queues ex without waiting. When the queue is full or the sink is closed it
// releases ex's memory, counts the drop and returns false.
func (s *Sink) Submit(ex *core.Exchange) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if !s.closed {
		select {
		case s.queue <- ex:
			return true
		default:
		}
	}
	ex.Release()
	s.droppedQueueFull.Add(1)
	return false
}

// Close stops accepting, then lets the workers drain the queue until ctx is done. At
// that point it cancels the workers' context, which the store call in flight also
// gets, waits for each worker to finish its current item, and releases what is left
// in the queue. It returns how many exchanges were left unstored that way. Later
// calls return 0.
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
	for ex := range s.queue {
		ex.Release()
		n++
	}
	return int(n)
}

// Counts is a snapshot of the sink's counts.
func (s *Sink) Counts() Counts {
	return Counts{DroppedQueueFull: s.droppedQueueFull.Load()}
}

// work is one worker: it takes exchanges off the queue until the queue is closed and
// empty, or until the workers' context is cancelled. An exchange taken after the
// cancel is released unstored and counted as undrained.
func (s *Sink) work() error {
	for ex := range s.queue {
		if s.ctx.Err() != nil {
			ex.Release()
			s.abandoned.Add(1)
			return nil
		}
		s.process(ex)
	}
	return nil
}

// process stores one exchange and releases its memory, whatever the outcome.
func (s *Sink) process(ex *core.Exchange) {
	defer ex.Release()
	_ = s.store.SaveExchange(s.ctx, ex)
}
