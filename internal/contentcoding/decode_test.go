package contentcoding_test

import (
	"bytes"
	"compress/flate"
	"compress/gzip"
	"compress/zlib"
	"fmt"
	"io"
	"math/rand/v2"
	"strings"
	"testing"

	"github.com/andybalholm/brotli"
	"github.com/klauspost/compress/zstd"

	"github.com/brutally-honest/llm-gateway/internal/contentcoding"
)

// sample is text that compresses, but not so well that half of a compressed copy
// decodes to nothing: truncation tests need a prefix to come out.
func sample() []byte {
	r := rand.New(rand.NewPCG(1, 2))
	words := []string{"alpha", "bravo", "charlie", "delta", "echo", "foxtrot", "golf", "hotel"}
	var b strings.Builder
	for b.Len() < 256<<10 {
		fmt.Fprintf(&b, "%s %d ", words[r.IntN(len(words))], r.IntN(1_000_000))
	}
	return []byte(b.String())
}

func encode(t *testing.T, coding string, in []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	var w io.WriteCloser
	var err error
	switch coding {
	case "gzip":
		w = gzip.NewWriter(&buf)
	case "zlib":
		w = zlib.NewWriter(&buf)
	case "raw":
		w, err = flate.NewWriter(&buf, flate.DefaultCompression)
	case "br":
		w = brotli.NewWriter(&buf)
	case "zstd":
		w, err = zstd.NewWriter(&buf)
	default:
		t.Fatalf("no test encoder for %q", coding)
	}
	if err != nil {
		t.Fatalf("encoder %s: %v", coding, err)
	}
	if _, err := w.Write(in); err != nil {
		t.Fatalf("encode %s: %v", coding, err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("close %s: %v", coding, err)
	}
	return buf.Bytes()
}

func TestDecode(t *testing.T) {
	plain := sample()
	const bigLimit = 1 << 30

	gz := encode(t, "gzip", plain)
	br := encode(t, "br", plain)

	tests := []struct {
		name     string
		encoding string
		body     []byte
		limit    int64
		want     []byte // exact output; nil when prefix is set
		prefix   bool   // output must be a non-empty strict prefix of plain
		status   contentcoding.Status
	}{
		{name: "empty_header", encoding: "", body: plain, limit: bigLimit, want: plain, status: contentcoding.Complete},
		{name: "identity", encoding: "identity", body: plain, limit: bigLimit, want: plain, status: contentcoding.Complete},
		{name: "gzip", encoding: "gzip", body: gz, limit: bigLimit, want: plain, status: contentcoding.Complete},
		{name: "gzip_case_and_space", encoding: " GZip ", body: gz, limit: bigLimit, want: plain, status: contentcoding.Complete},
		{name: "deflate_zlib", encoding: "deflate", body: encode(t, "zlib", plain), limit: bigLimit, want: plain, status: contentcoding.Complete},
		{name: "deflate_raw", encoding: "deflate", body: encode(t, "raw", plain), limit: bigLimit, want: plain, status: contentcoding.Complete},
		{name: "br", encoding: "br", body: br, limit: bigLimit, want: plain, status: contentcoding.Complete},
		{name: "zstd", encoding: "zstd", body: encode(t, "zstd", plain), limit: bigLimit, want: plain, status: contentcoding.Complete},
		{
			// Applied gzip first, then br: decoded br first, then gzip.
			name: "gzip_br_stacked", encoding: "gzip, br", body: encode(t, "br", gz),
			limit: bigLimit, want: plain, status: contentcoding.Complete,
		},
		{name: "unknown", encoding: "compress", body: gz, limit: bigLimit, want: gz, status: contentcoding.Unsupported},
		{name: "unknown_in_stack", encoding: "gzip, x-custom", body: gz, limit: bigLimit, want: gz, status: contentcoding.Unsupported},
		{name: "truncated_gzip", encoding: "gzip", body: gz[:len(gz)/2], limit: bigLimit, prefix: true, status: contentcoding.CutShort},
		{name: "truncated_br", encoding: "br", body: br[:len(br)/2], limit: bigLimit, prefix: true, status: contentcoding.CutShort},
		{name: "identity_over_limit", encoding: "identity", body: plain, limit: 1000, want: plain[:1000], status: contentcoding.CutShort},
		{name: "exactly_limit", encoding: "gzip", body: gz, limit: int64(len(plain)), want: plain, status: contentcoding.Complete},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, status := contentcoding.Decode(tc.encoding, tc.body, tc.limit)
			if status != tc.status {
				t.Errorf("status = %v, want %v", status, tc.status)
			}
			if tc.prefix {
				if len(got) == 0 || len(got) >= len(plain) || !bytes.HasPrefix(plain, got) {
					t.Errorf("got %d bytes, want a non-empty strict prefix of the %d-byte input", len(got), len(plain))
				}
				return
			}
			if !bytes.Equal(got, tc.want) {
				t.Errorf("got %d bytes, want %d bytes (equal: false)", len(got), len(tc.want))
			}
		})
	}

	t.Run("TestDecode_CapsOutput", func(t *testing.T) {
		const limit = 4096
		bomb := bytes.Repeat([]byte{'a'}, 8<<20)
		for _, coding := range []string{"gzip", "br", "zstd"} {
			t.Run(coding, func(t *testing.T) {
				got, status := contentcoding.Decode(coding, encode(t, coding, bomb), limit)
				if status != contentcoding.CutShort {
					t.Errorf("status = %v, want CutShort", status)
				}
				if !bytes.Equal(got, bomb[:limit]) {
					t.Errorf("got %d bytes, want exactly the first %d", len(got), limit)
				}
			})
		}
	})
}
