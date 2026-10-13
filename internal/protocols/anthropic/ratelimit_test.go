package anthropic_test

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"net/http"
	"os"
	"strings"
	"testing"

	"github.com/brutally-honest/llm-gateway/internal/core"
	"github.com/brutally-honest/llm-gateway/internal/protocols/anthropic"
)

func TestIsModelCall_OnlyMessagesPost(t *testing.T) {
	a := anthropic.Adapter{}
	cases := []struct {
		method, path string
		want         bool
	}{
		{"POST", "/v1/messages", true},
		{"POST", "/v1/messages/count_tokens", false},
		{"GET", "/v1/messages", false},
		{"GET", "/v1/models", false},
		{"HEAD", "/api/hello", false},
		{"POST", "/v1/messages/", false},
	}
	for _, c := range cases {
		if got := a.IsModelCall(c.method, c.path); got != c.want {
			t.Errorf("IsModelCall(%s, %s) = %v, want %v", c.method, c.path, got, c.want)
		}
	}
}

func TestLimitErrorBody_PermissionAndRateLimitTypes(t *testing.T) {
	a := anthropic.Adapter{}
	for kind, want := range map[core.LimitErrorKind]string{
		core.LimitPermission: "permission_error",
		core.LimitRateLimit:  "rate_limit_error",
	} {
		ct, body := a.LimitErrorBody(kind, "the message")
		if ct != "application/json" {
			t.Errorf("content type = %q", ct)
		}
		var env struct {
			Type  string
			Error struct{ Type, Message string }
		}
		if err := json.Unmarshal(body, &env); err != nil {
			t.Fatal(err)
		}
		if env.Type != "error" || env.Error.Type != want || env.Error.Message != "the message" {
			t.Errorf("kind %d: envelope = %+v, want type %s", kind, env, want)
		}
	}
}

// feed writes b to r in chunks of n bytes.
func feed(r core.UsageReader, b []byte, n int) {
	for len(b) > 0 {
		k := min(n, len(b))
		r.Write(b[:k])
		b = b[k:]
	}
}

func readTestdata(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func hdr(enc string) http.Header {
	h := http.Header{}
	if enc != "" {
		h.Set("Content-Encoding", enc)
	}
	return h
}

// parserTokens is the usage total the stream parser reports for the SSE body:
// input + cache write + output.
func parserTokens(t *testing.T, sse []byte) int64 {
	t.Helper()
	res := parser(t).Parse(core.ParseInput{
		Method: "POST", Path: "/v1/messages", Status: 200,
		RequestHeader:  http.Header{"Content-Type": {"application/json"}},
		ResponseHeader: http.Header{"Content-Type": {"text/event-stream"}},
		ResponseBody:   sse, Stream: true, DecodeLimit: decodeLimit,
	})
	for _, ev := range res.Events {
		if ev.Kind == core.KindUsage {
			var n int64
			for _, p := range []*int64{ev.Usage.InputTokens, ev.Usage.CacheWriteTokens, ev.Usage.OutputTokens} {
				if p != nil {
					n += *p
				}
			}
			return n
		}
	}
	t.Fatal("parser reported no usage")
	return 0
}

func TestUsageReader_StreamTokensMatchParser(t *testing.T) {
	sse := readTestdata(t, "stream.sse")
	want := parserTokens(t, sse)
	if want != 41656 {
		t.Fatalf("parser total = %d, want 41656", want)
	}
	for _, chunk := range []int{1, 7, len(sse)} {
		r := anthropic.Adapter{}.UsageReader(200, hdr(""), true)
		feed(r, sse, chunk)
		if n, ok := r.Tokens(); !ok || n != want {
			t.Errorf("chunk %d: Tokens() = %d, %v; want %d, true", chunk, n, ok, want)
		}
	}
}

func TestUsageReader_GzipSSEStream(t *testing.T) {
	assertCodedStream(t, "stream.sse.gz", "gzip")
}

func TestUsageReader_EachCodingSSEStream(t *testing.T) {
	assertCodedStream(t, "stream.sse.br", "br")
	assertCodedStream(t, "stream.sse.zst", "zstd")
	assertCodedStream(t, "stream.sse.deflate", "deflate")
}

func assertCodedStream(t *testing.T, file, enc string) {
	t.Helper()
	want := parserTokens(t, readTestdata(t, "stream.sse"))
	r := anthropic.Adapter{}.UsageReader(200, hdr(enc), true)
	feed(r, readTestdata(t, file), 5)
	if n, ok := r.Tokens(); !ok || n != want {
		t.Errorf("%s: Tokens() = %d, %v; want %d, true", enc, n, ok, want)
	}
}

func TestUsageReader_JSONBody(t *testing.T) {
	r := anthropic.Adapter{}.UsageReader(200, hdr(""), false)
	feed(r, readTestdata(t, "non_streaming/response.json"), 9)
	// 12 input + 0 cache write + 58 output; the 41642 cache reads are not counted.
	if n, ok := r.Tokens(); !ok || n != 70 {
		t.Errorf("Tokens() = %d, %v; want 70, true", n, ok)
	}
}

func TestUsageReader_GzipJSONBody(t *testing.T) {
	r := anthropic.Adapter{}.UsageReader(200, hdr("gzip"), false)
	feed(r, gzipped(t, readTestdata(t, "non_streaming/response.json")), 4)
	if n, ok := r.Tokens(); !ok || n != 70 {
		t.Errorf("Tokens() = %d, %v; want 70, true", n, ok)
	}
}

func TestUsageReader_CutGzipStreamStillReportsSeenUsage(t *testing.T) {
	sse := readTestdata(t, "stream.sse")
	// Cut right after message_start: its usage (2 + 41642 + output so far) is seen.
	end := bytes.Index(sse, []byte("\n\n")) + 2
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	_, _ = zw.Write(sse[:end])
	_ = zw.Flush() // no trailer: the stream is cut
	r := anthropic.Adapter{}.UsageReader(200, hdr("gzip"), true)
	feed(r, buf.Bytes(), 6)
	n, ok := r.Tokens()
	if !ok || n < 41644 || n >= 41656 {
		t.Errorf("Tokens() = %d, %v; want the message_start usage only", n, ok)
	}
}

func TestUsageReader_NoUsageReportsNotOK(t *testing.T) {
	for _, stream := range []bool{true, false} {
		r := anthropic.Adapter{}.UsageReader(200, hdr(""), stream)
		feed(r, []byte("event: ping\ndata: {\"type\":\"ping\"}\n\n"), 3)
		if n, ok := r.Tokens(); ok || n != 0 {
			t.Errorf("stream=%v: Tokens() = %d, %v; want 0, false", stream, n, ok)
		}
	}
}

func TestUsageReader_ErrorStatusNotOK(t *testing.T) {
	for _, status := range []int{400, 429, 500} {
		r := anthropic.Adapter{}.UsageReader(status, hdr(""), false)
		feed(r, readTestdata(t, "non_streaming/response.json"), 100)
		if n, ok := r.Tokens(); ok || n != 0 {
			t.Errorf("status %d: Tokens() = %d, %v; want 0, false", status, n, ok)
		}
	}
}

func TestUsageReader_OversizeJSONBodyNoTokens(t *testing.T) {
	r := anthropic.Adapter{}.UsageReader(200, hdr(""), false)
	r.Write([]byte(`{"pad":"` + strings.Repeat("x", 4<<20)))
	r.Write([]byte(`","usage":{"input_tokens":5,"output_tokens":5}}`))
	if n, ok := r.Tokens(); ok || n != 0 {
		t.Errorf("Tokens() = %d, %v; want 0, false", n, ok)
	}
}

func TestUsageReader_UnsupportedCodingNotOK(t *testing.T) {
	r := anthropic.Adapter{}.UsageReader(200, hdr("compress-x"), true)
	feed(r, readTestdata(t, "stream.sse"), 50)
	if n, ok := r.Tokens(); ok || n != 0 {
		t.Errorf("Tokens() = %d, %v; want 0, false", n, ok)
	}
}
