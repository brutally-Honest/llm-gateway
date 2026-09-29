package capture_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/brutally-honest/llm-gateway/internal/capture"
)

// A Submit racing or following Close returns false, releases the exchange and never
// panics: no send ever reaches the closed queue.
func TestSink_SubmitAfterCloseReturnsFalse(t *testing.T) {
	t.Run("after_close", func(t *testing.T) {
		checkNoLeaksAtEnd(t)
		exs, budget := capturedExchanges(t, 4)
		log, _ := newLogger()
		st := &recordingStore{}
		sink := capture.NewSink(capture.Config{QueueSize: 8, Workers: 2}, st, log)
		if n := closeSink(sink); n != 0 {
			t.Errorf("Close = %d, want 0 on an empty queue", n)
		}
		for i, ex := range exs {
			if sink.Submit(ex) {
				t.Errorf("Submit %d after Close = true, want false", i)
			}
		}
		if got := budget.InUse(); got != 0 {
			t.Errorf("InUse = %d, want 0: a refused exchange must be released", got)
		}
		if got := sink.Counts().DroppedQueueFull; got != int64(len(exs)) {
			t.Errorf("DroppedQueueFull = %d, want %d", got, len(exs))
		}
		if n := st.count(); n != 0 {
			t.Errorf("the store got %d exchanges, want none", n)
		}
		if n := closeSink(sink); n != 0 {
			t.Errorf("a second Close = %d, want 0", n)
		}
	})

	t.Run("racing_close", func(t *testing.T) {
		checkNoLeaksAtEnd(t)
		const submitters, each = 8, 8
		exs, budget := capturedExchanges(t, submitters*each)
		log, _ := newLogger()
		st := &failingStore{}
		sink := capture.NewSink(capture.Config{QueueSize: 4, Workers: 2}, st, log)

		var (
			wg       sync.WaitGroup
			start    = make(chan struct{})
			mu       sync.Mutex
			accepted int
		)
		for i := range submitters {
			wg.Add(1)
			go func(batch int) {
				defer wg.Done()
				<-start
				for _, ex := range exs[batch*each : (batch+1)*each] {
					if sink.Submit(ex) {
						mu.Lock()
						accepted++
						mu.Unlock()
					}
				}
			}(i)
		}
		undrained := make(chan int, 1)
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			undrained <- sink.Close(ctx)
		}()
		close(start)
		wg.Wait()

		if sink.Submit(exs[0]) {
			t.Error("Submit after Close returned = true, want false")
		}
		stored := int(st.calls.Load())
		if n := <-undrained; stored+n != accepted {
			t.Errorf("stored %d + undrained %d, want the %d accepted", stored, n, accepted)
		}
		refused := len(exs) - accepted
		if got := sink.Counts().DroppedQueueFull; got != int64(refused)+1 {
			t.Errorf("DroppedQueueFull = %d, want %d", got, refused+1)
		}
		if got := budget.InUse(); got != 0 {
			t.Errorf("InUse = %d, want 0: every exchange, stored, failed or refused, is released", got)
		}
	})
}

// Close drains until its context is done, then cancels the store call in flight and
// counts what is left in the queue, releasing all of it.
func TestSink_CloseCountsUndrained(t *testing.T) {
	t.Run("drained_in_time", func(t *testing.T) {
		checkNoLeaksAtEnd(t)
		exs, budget := capturedExchanges(t, 5)
		log, _ := newLogger()
		st := &recordingStore{}
		sink := capture.NewSink(capture.Config{QueueSize: 8, Workers: 2}, st, log)
		for i, ex := range exs {
			if !sink.Submit(ex) {
				t.Fatalf("Submit %d = false with room in the queue", i)
			}
		}
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if n := sink.Close(ctx); n != 0 {
			t.Errorf("Close = %d, want 0", n)
		}
		if n := st.count(); n != len(exs) {
			t.Errorf("stored %d, want all %d", n, len(exs))
		}
		if got := budget.InUse(); got != 0 {
			t.Errorf("InUse = %d, want 0", got)
		}
	})

	t.Run("deadline_passes", func(t *testing.T) {
		checkNoLeaksAtEnd(t)
		exs, budget := capturedExchanges(t, 5)
		log, _ := newLogger()
		st := newBlockingStore()
		sink := capture.NewSink(capture.Config{QueueSize: 4, Workers: 1}, st, log)
		if !sink.Submit(exs[0]) {
			t.Fatal("first Submit = false")
		}
		st.waitEntered(t, 1)
		for i, ex := range exs[1:] {
			if !sink.Submit(ex) {
				t.Fatalf("Submit %d = false with room in the queue", i+1)
			}
		}

		ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
		defer cancel()
		start := time.Now()
		if n := sink.Close(ctx); n != 4 {
			t.Errorf("Close = %d, want the 4 left in the queue", n)
		}
		if took := time.Since(start); took > time.Second {
			t.Errorf("Close took %v past a 50ms deadline", took)
		}
		errs := st.cancelled()
		if len(errs) != 1 || !errors.Is(errs[0], context.Canceled) {
			t.Errorf("store calls ended with %v, want the one in flight cancelled", errs)
		}
		if got := budget.InUse(); got != 0 {
			t.Errorf("InUse = %d, want 0: the in-flight and the undrained exchanges are released", got)
		}
		if got := sink.Counts().DroppedQueueFull; got != 0 {
			t.Errorf("DroppedQueueFull = %d, want 0: nothing was refused", got)
		}
	})
}
