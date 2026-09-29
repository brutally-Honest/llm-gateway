package core_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"strings"
	"sync"
	"testing"

	"github.com/brutally-honest/llm-gateway/internal/core"
)

func TestBudget_ReserveRelease(t *testing.T) {
	t.Run("limit", func(t *testing.T) {
		b := core.NewBudget(10)
		if !b.TryReserve(10) {
			t.Fatal("TryReserve(10) at limit 10 refused")
		}
		if b.TryReserve(1) {
			t.Fatal("TryReserve(1) past the limit accepted")
		}
		b.Release(5)
		if !b.TryReserve(5) {
			t.Fatal("TryReserve(5) after Release(5) refused")
		}
		if got := b.InUse(); got != 10 {
			t.Fatalf("InUse = %d, want 10", got)
		}
		if b.TryReserve(11) {
			t.Fatal("a reservation larger than the limit was accepted")
		}
	})

	t.Run("concurrent", func(t *testing.T) {
		const limit, goroutines, rounds = 64, 32, 500
		b := core.NewBudget(limit)
		var wg sync.WaitGroup
		for range goroutines {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for i := range rounds {
					n := int64(i%7 + 1)
					if b.TryReserve(n) {
						if in := b.InUse(); in > limit {
							t.Errorf("InUse = %d, over the limit %d", in, limit)
						}
						b.Release(n)
					}
				}
			}()
		}
		wg.Wait()
		if got := b.InUse(); got != 0 {
			t.Fatalf("InUse after every release = %d, want 0", got)
		}
		if p := b.Peak(); p > limit || p == 0 {
			t.Fatalf("Peak = %d, want in (0, %d]", p, limit)
		}
	})
}

func TestBudget_Peak(t *testing.T) {
	b := core.NewBudget(100)
	if b.Peak() != 0 {
		t.Fatalf("Peak of a fresh budget = %d, want 0", b.Peak())
	}
	b.TryReserve(3)
	b.TryReserve(4)
	if got := b.Peak(); got != 7 {
		t.Fatalf("Peak = %d, want 7", got)
	}
	b.Release(7)
	if got := b.Peak(); got != 7 {
		t.Fatalf("Peak after Release = %d, want 7 (it never drops)", got)
	}
	b.TryReserve(2)
	if got := b.Peak(); got != 7 {
		t.Fatalf("Peak after a smaller reservation = %d, want 7", got)
	}
	b.TryReserve(50)
	if got := b.Peak(); got != 52 {
		t.Fatalf("Peak = %d, want 52", got)
	}
	if b.TryReserve(49) {
		t.Fatal("a refused reservation was accepted")
	}
	if got := b.Peak(); got != 52 {
		t.Fatalf("Peak after a refused reservation = %d, want 52", got)
	}
}

// chunkReader returns its parts one Read at a time, then io.EOF.
type chunkReader struct {
	parts []string
}

func (c *chunkReader) Read(p []byte) (int, error) {
	if len(c.parts) == 0 {
		return 0, io.EOF
	}
	n := copy(p, c.parts[0])
	c.parts[0] = c.parts[0][n:]
	if c.parts[0] == "" {
		c.parts = c.parts[1:]
	}
	return n, nil
}

func (c *chunkReader) Close() error { return nil }

func newChunkReader(parts ...string) *chunkReader {
	return &chunkReader{parts: append([]string(nil), parts...)}
}

func TestTeeReader_CopiesAndCaps(t *testing.T) {
	t.Run("copies", func(t *testing.T) {
		b := core.NewBudget(1 << 20)
		res := core.NewReservation(b)
		tr := core.NewTeeReader(newChunkReader("hello ", "wire ", "bytes"), res, 1<<10)
		got, err := io.ReadAll(tr)
		if err != nil || string(got) != "hello wire bytes" {
			t.Fatalf("forwarded %q, %v", got, err)
		}
		body, _ := core.SealTeeReader(tr)
		if string(body.Bytes()) != "hello wire bytes" || body.Size != 16 || body.Truncated {
			t.Fatalf("copy = %q size %d truncated %v", body.Bytes(), body.Size, body.Truncated)
		}
		if len(body.Chunks) != 3 {
			t.Fatalf("copy has %d chunks, want one per Read (3)", len(body.Chunks))
		}
		if b.InUse() != 16 || core.ReservationHeld(res) != 16 {
			t.Fatalf("InUse = %d, held = %d, want 16", b.InUse(), core.ReservationHeld(res))
		}
		core.ReleaseReservation(res)
		core.ReleaseReservation(res)
		if b.InUse() != 0 {
			t.Fatalf("InUse after release = %d, want 0", b.InUse())
		}
	})

	t.Run("caps", func(t *testing.T) {
		b := core.NewBudget(1 << 20)
		res := core.NewReservation(b)
		tr := core.NewTeeReader(newChunkReader("abc", "defgh", "ijkl"), res, 5)
		got, _ := io.ReadAll(tr)
		if string(got) != "abcdefghijkl" {
			t.Fatalf("forwarded %q, want the whole body", got)
		}
		body, _ := core.SealTeeReader(tr)
		if string(body.Bytes()) != "abcde" || body.Size != 5 || !body.Truncated {
			t.Fatalf("copy = %q size %d truncated %v, want abcde 5 true", body.Bytes(), body.Size, body.Truncated)
		}
		if b.InUse() != 5 {
			t.Fatalf("InUse = %d, want only the capped 5", b.InUse())
		}
		if core.ReservationDropped(res) {
			t.Fatal("a truncated body was dropped")
		}
	})

	t.Run("exactly_at_cap", func(t *testing.T) {
		res := core.NewReservation(core.NewBudget(1 << 20))
		tr := core.NewTeeReader(newChunkReader("abc", "de"), res, 5)
		_, _ = io.ReadAll(tr)
		body, _ := core.SealTeeReader(tr)
		if string(body.Bytes()) != "abcde" || body.Truncated {
			t.Fatalf("copy = %q truncated %v, want abcde false", body.Bytes(), body.Truncated)
		}
	})

	t.Run("refused_drops", func(t *testing.T) {
		b := core.NewBudget(4)
		res := core.NewReservation(b)
		tr := core.NewTeeReader(newChunkReader("abc", "defgh", "ij"), res, 1<<10)
		got, _ := io.ReadAll(tr)
		if string(got) != "abcdefghij" {
			t.Fatalf("forwarded %q, want the whole body", got)
		}
		body, _ := core.SealTeeReader(tr)
		if len(body.Chunks) != 0 || body.Size != 0 {
			t.Fatalf("a dropped copy kept %d bytes", body.Size)
		}
		if !core.ReservationDropped(res) {
			t.Fatal("a refused reservation did not mark the exchange dropped")
		}
		if b.InUse() != 0 || core.ReservationHeld(res) != 0 {
			t.Fatalf("InUse = %d, held = %d after a drop, want 0", b.InUse(), core.ReservationHeld(res))
		}
	})

	t.Run("refusal_releases_both_tees", func(t *testing.T) {
		b := core.NewBudget(8)
		res := core.NewReservation(b)
		rec := httptest.NewRecorder()
		tw := core.NewTeeWriter(rec, res, 1<<10)
		_, _ = tw.Write([]byte("12345"))
		if b.InUse() != 5 {
			t.Fatalf("InUse = %d, want 5", b.InUse())
		}
		tr := core.NewTeeReader(newChunkReader("abcdef"), res, 1<<10)
		_, _ = io.ReadAll(tr)
		if b.InUse() != 0 {
			t.Fatalf("InUse = %d after the request tee was refused, want 0", b.InUse())
		}
		_, _ = tw.Write([]byte("678"))
		if rec.Body.String() != "12345678" {
			t.Fatalf("client got %q, want every byte", rec.Body.String())
		}
		if body := core.SealTeeWriter(tw); body.Size != 0 || len(body.Chunks) != 0 {
			t.Fatalf("response copy kept %d bytes after the exchange was dropped", body.Size)
		}
		if b.InUse() != 0 {
			t.Fatalf("InUse = %d, want 0", b.InUse())
		}
	})
}

func TestTeeReader_SealRecordsEOF(t *testing.T) {
	t.Run("sealed_before_eof", func(t *testing.T) {
		res := core.NewReservation(core.NewBudget(1 << 20))
		tr := core.NewTeeReader(newChunkReader("first", "second"), res, 1<<10)
		p := make([]byte, 64)
		if n, err := tr.Read(p); err != nil || string(p[:n]) != "first" {
			t.Fatalf("Read = %q, %v", p[:n], err)
		}
		body, complete := core.SealTeeReader(tr)
		if complete {
			t.Fatal("sealed before EOF, but reported complete")
		}
		if string(body.Bytes()) != "first" {
			t.Fatalf("copy = %q, want first", body.Bytes())
		}
		rest, err := io.ReadAll(tr)
		if err != nil || string(rest) != "second" {
			t.Fatalf("read after the seal = %q, %v; want it passed through", rest, err)
		}
		if body, _ := core.SealTeeReader(tr); string(body.Bytes()) != "first" {
			t.Fatalf("a read after the seal was copied: %q", body.Bytes())
		}
	})

	t.Run("sealed_after_eof", func(t *testing.T) {
		res := core.NewReservation(core.NewBudget(1 << 20))
		tr := core.NewTeeReader(newChunkReader("all"), res, 1<<10)
		_, _ = io.ReadAll(tr)
		if _, complete := core.SealTeeReader(tr); !complete {
			t.Fatal("sealed after EOF, but reported incomplete")
		}
	})

	t.Run("eof_seen_past_cap", func(t *testing.T) {
		res := core.NewReservation(core.NewBudget(1 << 20))
		tr := core.NewTeeReader(newChunkReader("abcdef"), res, 2)
		_, _ = io.ReadAll(tr)
		body, complete := core.SealTeeReader(tr)
		if !complete || !body.Truncated {
			t.Fatalf("complete %v truncated %v, want true true", complete, body.Truncated)
		}
	})

	t.Run("read_error_not_eof", func(t *testing.T) {
		res := core.NewReservation(core.NewBudget(1 << 20))
		boom := errors.New("boom")
		tr := core.NewTeeReader(io.NopCloser(io.MultiReader(strings.NewReader("ab"), errReader{boom})), res, 1<<10)
		if _, err := io.ReadAll(tr); !errors.Is(err, boom) {
			t.Fatalf("err = %v, want the reader's error passed through", err)
		}
		if _, complete := core.SealTeeReader(tr); complete {
			t.Fatal("a body that failed before EOF reported complete")
		}
	})
}

// flushRecorder is a ResponseWriter whose only way to Flush is its own method, so a
// wrapper hiding it cannot be flushed without Unwrap.
type flushRecorder struct {
	http.ResponseWriter
	flushed int
}

func (f *flushRecorder) Flush() { f.flushed++ }

func TestTeeWriter_Unwrap(t *testing.T) {
	inner := &flushRecorder{ResponseWriter: httptest.NewRecorder()}
	tw := core.NewTeeWriter(inner, core.NewReservation(core.NewBudget(1<<20)), 1<<10)
	if tw.Unwrap() != http.ResponseWriter(inner) {
		t.Fatal("Unwrap does not return the inner writer")
	}
	if err := http.NewResponseController(tw).Flush(); err != nil {
		t.Fatalf("Flush through the tee: %v", err)
	}
	if inner.flushed != 1 {
		t.Fatalf("inner flushed %d times, want 1", inner.flushed)
	}
}

func TestTeeWriter_CopiesStatusAndHeaders(t *testing.T) {
	rec := httptest.NewRecorder()
	b := core.NewBudget(1 << 20)
	tw := core.NewTeeWriter(rec, core.NewReservation(b), 4)
	tw.Header().Set("Content-Type", "text/event-stream")
	tw.WriteHeader(http.StatusTeapot)
	tw.Header().Set("X-After", "late")
	_, _ = tw.Write([]byte("ab"))
	_, _ = tw.Write([]byte("cdef"))
	if rec.Body.String() != "abcdef" || rec.Code != http.StatusTeapot {
		t.Fatalf("client got %d %q", rec.Code, rec.Body.String())
	}
	status, header := core.TeeWriterResponse(tw)
	if status != http.StatusTeapot {
		t.Fatalf("recorded status %d, want 418", status)
	}
	if header.Get("Content-Type") != "text/event-stream" || header.Get("X-After") != "" {
		t.Fatalf("header snapshot = %v, want the headers as sent", header)
	}
	body := core.SealTeeWriter(tw)
	if string(body.Bytes()) != "abcd" || !body.Truncated {
		t.Fatalf("copy = %q truncated %v, want abcd true", body.Bytes(), body.Truncated)
	}

	t.Run("implicit_200", func(t *testing.T) {
		tw := core.NewTeeWriter(httptest.NewRecorder(), core.NewReservation(b), 4)
		_, _ = tw.Write([]byte("x"))
		if status, _ := core.TeeWriterResponse(tw); status != http.StatusOK {
			t.Fatalf("status %d, want 200", status)
		}
	})
}

func TestCapture_OutgoingRequestHasNoGetBody(t *testing.T) {
	in := httptest.NewRequest(http.MethodPost, "/t/v1/x", strings.NewReader("request body"))
	in.GetBody = nil // as on a server's inbound request
	out := in.Clone(context.Background())
	pr := &httputil.ProxyRequest{In: in, Out: out}
	res := core.NewReservation(core.NewBudget(1 << 20))

	tr := core.TeeRequestBody(pr, res, 1<<10)
	if tr == nil {
		t.Fatal("a request with a body got no tee")
	}
	if pr.Out.GetBody != nil {
		t.Fatal("the outgoing request has a GetBody: the transport could replay the body")
	}
	got, _ := io.ReadAll(pr.Out.Body)
	if !bytes.Equal(got, []byte("request body")) {
		t.Fatalf("forwarded %q", got)
	}
	if body, complete := core.SealTeeReader(tr); string(body.Bytes()) != "request body" || !complete {
		t.Fatalf("copy = %q complete %v", body.Bytes(), complete)
	}

	t.Run("no_body", func(t *testing.T) {
		out := in.Clone(context.Background())
		out.Body = nil
		pr := &httputil.ProxyRequest{In: in, Out: out}
		if core.TeeRequestBody(pr, res, 1<<10) != nil || pr.Out.Body != nil {
			t.Fatal("a request with no body got a tee")
		}
		out.Body = http.NoBody
		if core.TeeRequestBody(pr, res, 1<<10) != nil || pr.Out.Body != http.NoBody {
			t.Fatal("http.NoBody was wrapped, which would change how the transport frames it")
		}
	})
	t.Run("through_proxy", outgoingRequestHasNoGetBodyThroughProxy)
}

func TestReservation_RefusalFreesOtherTeeAtOnce(t *testing.T) {
	t.Run("response_refused_after_request_read", func(t *testing.T) {
		b := core.NewBudget(8)
		res := core.NewReservation(b)
		tr := core.NewTeeReader(newChunkReader("abcdef"), res, 1<<10)
		_, _ = io.ReadAll(tr)
		if got := core.TeeReaderCopied(tr); got != 6 {
			t.Fatalf("request copy holds %d bytes, want 6", got)
		}
		tw := core.NewTeeWriter(httptest.NewRecorder(), res, 1<<10)
		_, _ = tw.Write([]byte("xyz"))
		if !core.ReservationDropped(res) {
			t.Fatal("the refused response chunk did not drop the exchange")
		}
		if got := core.TeeReaderCopied(tr); got != 0 {
			t.Fatalf("request copy still holds %d bytes after the drop, before any seal", got)
		}
		if b.InUse() != 0 {
			t.Fatalf("InUse = %d, want 0", b.InUse())
		}
	})

	t.Run("request_refused_after_response_write", func(t *testing.T) {
		b := core.NewBudget(8)
		res := core.NewReservation(b)
		tw := core.NewTeeWriter(httptest.NewRecorder(), res, 1<<10)
		_, _ = tw.Write([]byte("12345"))
		tr := core.NewTeeReader(newChunkReader("abcdef"), res, 1<<10)
		_, _ = io.ReadAll(tr)
		if got := core.TeeWriterCopied(tw); got != 0 {
			t.Fatalf("response copy still holds %d bytes after the drop, before any write or seal", got)
		}
	})
}

// endlessReader returns one byte per Read, forever.
type endlessReader struct{}

func (endlessReader) Read(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	p[0] = 'x'
	return 1, nil
}

func (endlessReader) Close() error { return nil }

func TestTeeReader_ConcurrentReadAndSeal(t *testing.T) {
	b := core.NewBudget(1 << 10)
	res := core.NewReservation(b)
	tr := core.NewTeeReader(endlessReader{}, res, 1<<20)
	tw := core.NewTeeWriter(httptest.NewRecorder(), res, 1<<20)

	var wg sync.WaitGroup
	stop := make(chan struct{})
	wg.Add(2)
	go func() { // the transport's goroutine
		defer wg.Done()
		p := make([]byte, 1)
		for {
			select {
			case <-stop:
				return
			default:
				_, _ = tr.Read(p)
			}
		}
	}()
	go func() { // the handler's goroutine, writing until the budget refuses
		defer wg.Done()
		for i := 0; i < 2000; i++ {
			_, _ = tw.Write([]byte("y"))
		}
		_ = core.SealTeeWriter(tw)
	}()
	for i := 0; i < 100; i++ {
		_, _ = core.SealTeeReader(tr)
	}
	close(stop)
	wg.Wait()
	if !core.ReservationDropped(res) {
		t.Fatal("the budget never refused, so the drop path was not exercised")
	}
	if b.InUse() != 0 || core.TeeReaderCopied(tr) != 0 || core.TeeWriterCopied(tw) != 0 {
		t.Fatalf("after the drop: InUse %d, request copy %d, response copy %d; want 0",
			b.InUse(), core.TeeReaderCopied(tr), core.TeeWriterCopied(tw))
	}
}
