package contentcoding_test

import (
	"bytes"
	"io"
	"runtime"
	"testing"
	"testing/iotest"
	"time"

	"github.com/brutally-honest/llm-gateway/internal/contentcoding"
)

func TestNewReader_StreamsEachCoding(t *testing.T) {
	plain := sample()
	gz := encode(t, "gzip", plain)
	cases := []struct {
		name, encoding string
		body           []byte
	}{
		{"gzip", "gzip", gz},
		{"deflate_zlib", "deflate", encode(t, "zlib", plain)},
		{"deflate_raw", "deflate", encode(t, "raw", plain)},
		{"br", "br", encode(t, "br", plain)},
		{"zstd", "zstd", encode(t, "zstd", plain)},
		{"stacked", "gzip, br", encode(t, "br", gz)},
		{"identity", "identity", plain},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// One byte at a time: the decoder must work on a trickling stream.
			r, st := contentcoding.NewReader(tc.encoding, iotest.OneByteReader(bytes.NewReader(tc.body)))
			if st != contentcoding.Complete {
				t.Fatalf("status = %v", st)
			}
			got, err := io.ReadAll(r)
			if err != nil {
				t.Fatalf("read: %v", err)
			}
			_ = r.Close()
			if !bytes.Equal(got, plain) {
				t.Errorf("decoded %d bytes, want %d", len(got), len(plain))
			}
		})
	}
}

func TestNewReader_UnknownCodingUnsupported(t *testing.T) {
	for _, enc := range []string{"compress", "gzip, x-custom"} {
		r, st := contentcoding.NewReader(enc, bytes.NewReader(nil))
		if st != contentcoding.Unsupported || r != nil {
			t.Errorf("%q: got reader=%v status=%v", enc, r, st)
		}
	}
}

func TestFeeder_WriteNeverBlocksOnSlowSink(t *testing.T) {
	plain := sample()
	gz := encode(t, "gzip", plain)
	release := make(chan struct{})
	var got []byte
	f := contentcoding.NewFeeder("gzip", func(r io.Reader) {
		<-release // a sink that is stuck
		got, _ = io.ReadAll(r)
	}, contentcoding.DefaultMaxPending)

	start := time.Now()
	for i := 0; i < len(gz); i += 100 {
		_, _ = f.Write(gz[i:min(i+100, len(gz))])
	}
	if d := time.Since(start); d > 2*time.Second {
		t.Fatalf("writes took %v with a stuck sink", d)
	}
	close(release)
	if st := f.Close(); st != contentcoding.Complete {
		t.Fatalf("status = %v", st)
	}
	if !bytes.Equal(got, plain) {
		t.Errorf("decoded %d bytes, want %d", len(got), len(plain))
	}
}

func TestFeeder_OverflowMarksOverflowedAndDrops(t *testing.T) {
	release := make(chan struct{})
	f := contentcoding.NewFeeder("identity", func(r io.Reader) {
		<-release
		_, _ = io.Copy(io.Discard, r)
	}, 1024)
	chunk := make([]byte, 512)
	for range 10 {
		if n, err := f.Write(chunk); n != len(chunk) || err != nil {
			t.Fatalf("Write = %d, %v", n, err)
		}
	}
	close(release)
	if st := f.Close(); st != contentcoding.Overflowed {
		t.Errorf("status = %v, want overflowed", st)
	}
}

func TestFeeder_BombStopsAtOutputCap(t *testing.T) {
	bomb := encode(t, "gzip", bytes.Repeat([]byte{'a'}, contentcoding.MaxDecoded+(8<<20)))
	var total int64
	f := contentcoding.NewFeeder("gzip", func(r io.Reader) {
		total, _ = io.Copy(io.Discard, r)
	}, 4<<20)
	_, _ = f.Write(bomb)
	if st := f.Close(); st != contentcoding.CutShort {
		t.Errorf("status = %v, want cut_short", st)
	}
	if total != contentcoding.MaxDecoded {
		t.Errorf("sink saw %d bytes, want %d", total, contentcoding.MaxDecoded)
	}
}

func TestFeeder_CloseDrainsNoLeak(t *testing.T) {
	before := runtime.NumGoroutine()
	plain := sample()
	for _, enc := range []string{"gzip", "zstd", "nope"} {
		body := plain
		if enc != "nope" {
			body = encode(t, enc, plain)
		}
		// The sink reads nothing: the feeder must still drain and finish.
		f := contentcoding.NewFeeder(enc, func(io.Reader) {}, contentcoding.DefaultMaxPending)
		_, _ = f.Write(body)
		f.Close()
		f.Close() // idempotent
	}
	deadline := time.Now().Add(2 * time.Second)
	for runtime.NumGoroutine() > before && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if after := runtime.NumGoroutine(); after > before {
		t.Errorf("goroutines: %d before, %d after", before, after)
	}
}

func TestFeeder_UnsupportedReported(t *testing.T) {
	called := false
	f := contentcoding.NewFeeder("x-custom", func(io.Reader) { called = true }, 1024)
	_, _ = f.Write([]byte("abc"))
	if st := f.Close(); st != contentcoding.Unsupported || called {
		t.Errorf("status = %v, sink called = %v", st, called)
	}
}

func TestFeeder_TruncatedInputReportsCutShort(t *testing.T) {
	plain := sample()
	gz := encode(t, "gzip", plain)
	var got []byte
	f := contentcoding.NewFeeder("gzip", func(r io.Reader) { got, _ = io.ReadAll(r) }, contentcoding.DefaultMaxPending)
	_, _ = f.Write(gz[:len(gz)/2])
	if st := f.Close(); st != contentcoding.CutShort {
		t.Errorf("status = %v, want cut_short", st)
	}
	if len(got) == 0 || !bytes.HasPrefix(plain, got) {
		t.Errorf("got %d bytes, want a non-empty prefix of the input", len(got))
	}
}
