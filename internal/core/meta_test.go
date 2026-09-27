package core_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"testing"

	"github.com/brutally-honest/llm-gateway/internal/core"
)

func TestMeta_FromContext(t *testing.T) {
	if m := core.MetaFrom(context.Background()); m != nil {
		t.Fatalf("MetaFrom outside a request = %+v, want nil", m)
	}
	ctx, m := core.WithMeta(context.Background())
	if m == nil {
		t.Fatal("WithMeta returned a nil Meta")
	}
	if got := core.MetaFrom(ctx); got != m {
		t.Fatalf("MetaFrom = %p, want the Meta WithMeta returned (%p)", got, m)
	}
	// A nil Meta settles to nothing rather than panicking in a defer.
	var nilMeta *core.Meta
	nilMeta.Settle(ctx)
}

// failingBody yields some bytes, then err.
func failingBody(err error) io.ReadCloser {
	return io.NopCloser(io.MultiReader(bytes.NewReader([]byte("event: ping\n\n")), errReader{err}))
}

type errReader struct{ err error }

func (r errReader) Read([]byte) (int, error) { return 0, r.err }

func TestMeta_Settle(t *testing.T) {
	upstreamReset := errors.New("upstream reset")
	cases := []struct {
		name             string
		responseErr      error // what the response body fails with; nil reads to EOF
		requestErr       error // what the request body fails with; nil reads to EOF
		cancel           bool  // the client's context is gone when the handler ends
		wantAborted      bool
		wantDisconnected bool
	}{
		{name: "upstream read error, live context", responseErr: upstreamReset, wantAborted: true},
		{name: "client gone", cancel: true, wantDisconnected: true},
		{name: "both clean"},
		{name: "watcher error, cancelled context", responseErr: context.Canceled, cancel: true, wantDisconnected: true},
		{name: "request body error only, live context", requestErr: upstreamReset},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			ctx, m := core.WithMeta(ctx)

			res := io.NopCloser(bytes.NewReader([]byte("data: {}\n\n")))
			if tc.responseErr != nil {
				res = failingBody(tc.responseErr)
			}
			if _, err := io.ReadAll(core.WatchResponseBody(m, res)); !errors.Is(err, tc.responseErr) {
				t.Fatalf("reading response body: err = %v, want %v", err, tc.responseErr)
			}
			req := io.NopCloser(bytes.NewReader([]byte("{}")))
			if tc.requestErr != nil {
				req = failingBody(tc.requestErr)
			}
			if _, err := io.ReadAll(core.WatchRequestBody(m, req)); !errors.Is(err, tc.requestErr) {
				t.Fatalf("reading request body: err = %v, want %v", err, tc.requestErr)
			}
			if tc.cancel {
				cancel()
			}

			m.Settle(ctx)

			if m.UpstreamAborted != tc.wantAborted {
				t.Errorf("UpstreamAborted = %v, want %v", m.UpstreamAborted, tc.wantAborted)
			}
			if m.ClientDisconnected != tc.wantDisconnected {
				t.Errorf("ClientDisconnected = %v, want %v", m.ClientDisconnected, tc.wantDisconnected)
			}
		})
	}
}
