package core_test

import (
	"bytes"
	"errors"
	"io"
	"testing"

	"github.com/brutally-honest/llm-gateway/internal/core"
)

// step is one scripted Read: the bytes the source yields and the error it returns.
type step struct {
	data string
	err  error
}

// scripted is a source body that plays its steps in order and records the size of
// every buffer it was asked to fill and whether it was closed.
type scripted struct {
	steps    []step
	asked    []int
	closed   bool
	closeErr error
}

func (s *scripted) Read(p []byte) (int, error) {
	s.asked = append(s.asked, len(p))
	if len(s.steps) == 0 {
		return 0, io.EOF
	}
	st := s.steps[0]
	s.steps = s.steps[1:]
	return copy(p, st.data), st.err
}

func (s *scripted) Close() error {
	s.closed = true
	return s.closeErr
}

func TestWatcher_PassesBytesThrough(t *testing.T) {
	first := errors.New("first failure")
	second := errors.New("second failure")
	closeErr := errors.New("close failure")

	watchers := []struct {
		name  string
		wrap  func(*core.Meta, io.ReadCloser) io.ReadCloser
		err   func(*core.Meta) error
		other func(*core.Meta) error // the opposite side's error, which must stay nil
	}{
		{"request", core.WatchRequestBody, core.RequestBodyErr, core.ResponseBodyErr},
		{"response", core.WatchResponseBody, core.ResponseBodyErr, core.RequestBodyErr},
	}
	cases := []struct {
		name    string
		steps   []step
		reads   []int // buffer size the caller passes on each Read
		wantErr error // the error the watcher keeps
	}{
		{
			name:  "clean body ending in EOF keeps nothing",
			steps: []step{{"event: a\n", nil}, {"data: 1\n\n", nil}, {"", io.EOF}},
			reads: []int{16, 3, 64},
		},
		{
			name:  "data and EOF together keeps nothing",
			steps: []step{{"hello", io.EOF}},
			reads: []int{8},
		},
		{
			name:    "first error kept over a later one",
			steps:   []step{{"par", nil}, {"tial", first}, {"", second}, {"", io.EOF}},
			reads:   []int{3, 32, 32, 32},
			wantErr: first,
		},
		{
			name:    "error after EOF is still kept",
			steps:   []step{{"", io.EOF}, {"", first}},
			reads:   []int{4, 4},
			wantErr: first,
		},
	}
	for _, w := range watchers {
		for _, tc := range cases {
			t.Run(w.name+"/"+tc.name, func(t *testing.T) {
				// The reference plays the same script straight, without a watcher.
				ref := &scripted{steps: append([]step(nil), tc.steps...)}
				src := &scripted{steps: append([]step(nil), tc.steps...), closeErr: closeErr}
				_, m := core.WithMeta(t.Context())
				body := w.wrap(m, src)

				for i, size := range tc.reads {
					wantBuf := make([]byte, size)
					wantN, wantE := ref.Read(wantBuf)
					gotBuf := make([]byte, size)
					gotN, gotE := body.Read(gotBuf)
					if gotN != wantN || !bytes.Equal(gotBuf[:gotN], wantBuf[:wantN]) || !errors.Is(gotE, wantE) || (wantE == nil) != (gotE == nil) {
						t.Fatalf("read %d: got (%d, %q, %v), want (%d, %q, %v)",
							i, gotN, gotBuf[:gotN], gotE, wantN, wantBuf[:wantN], wantE)
					}
				}
				if len(src.asked) != len(tc.reads) {
					t.Fatalf("source read %d times, want %d (no read-ahead)", len(src.asked), len(tc.reads))
				}
				for i := range tc.reads {
					if src.asked[i] != tc.reads[i] {
						t.Errorf("read %d: source asked for %d bytes, caller asked for %d", i, src.asked[i], tc.reads[i])
					}
				}
				if got := w.err(m); !errors.Is(got, tc.wantErr) || (tc.wantErr == nil) != (got == nil) {
					t.Errorf("kept error = %v, want %v", got, tc.wantErr)
				}
				if got := w.other(m); got != nil {
					t.Errorf("the other side's error = %v, want nil", got)
				}
				if err := body.Close(); !errors.Is(err, closeErr) {
					t.Errorf("Close = %v, want the source's %v", err, closeErr)
				}
				if !src.closed {
					t.Error("Close did not reach the source")
				}
			})
		}
	}
}

// TestWatcher_RequestErrorAcrossGoroutines reads the request body on one goroutine, as
// the transport does, and reads the kept error on another; -race checks the handoff.
func TestWatcher_RequestErrorAcrossGoroutines(t *testing.T) {
	failure := errors.New("short body")
	_, m := core.WithMeta(t.Context())
	body := core.WatchRequestBody(m, &scripted{steps: []step{{"{", nil}, {"", failure}}})

	done := make(chan struct{})
	go func() {
		defer close(done)
		_, _ = io.ReadAll(body)
	}()
	for {
		select {
		case <-done:
			if got := core.RequestBodyErr(m); !errors.Is(got, failure) {
				t.Fatalf("kept error = %v, want %v", got, failure)
			}
			return
		default:
			_ = core.RequestBodyErr(m)
		}
	}
}
