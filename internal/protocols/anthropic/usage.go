package anthropic

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"strings"

	"github.com/brutally-honest/llm-gateway/internal/contentcoding"
	"github.com/brutally-honest/llm-gateway/internal/core"
)

const (
	// maxSSEEvent caps the one event the scanner buffers; a bigger event is skipped.
	maxSSEEvent = 1 << 20
	// maxJSONBody caps the decoded non-stream body kept to read its usage.
	maxJSONBody = 4 << 20
)

// UsageReader reads the tokens a response used. Non-2xx responses do no work.
func (Adapter) UsageReader(status int, h http.Header, stream bool) core.UsageReader {
	if status < 200 || status > 299 {
		return noUsage{}
	}
	r := &usageReader{stream: stream}
	if stream {
		r.scan = &sseScanner{}
		r.decoded = r.scan
	} else {
		r.body = &cappedBuffer{}
		r.decoded = r.body
	}
	if enc := strings.Join(h.Values("Content-Encoding"), ","); strings.TrimSpace(enc) != "" {
		r.feeder = contentcoding.NewFeeder(enc, func(src io.Reader) {
			_, _ = io.Copy(r.decoded, src)
		}, contentcoding.DefaultMaxPending)
	}
	return r
}

type noUsage struct{}

func (noUsage) Write([]byte)          {}
func (noUsage) Tokens() (int64, bool) { return 0, false }

type usageReader struct {
	stream  bool
	feeder  *contentcoding.Feeder
	decoded io.Writer
	scan    *sseScanner
	body    *cappedBuffer
}

func (r *usageReader) Write(p []byte) {
	if r.feeder != nil {
		_, _ = r.feeder.Write(p)
		return
	}
	_, _ = r.decoded.Write(p)
}

func (r *usageReader) Tokens() (int64, bool) {
	if r.feeder != nil {
		switch r.feeder.Close() {
		case contentcoding.Unsupported, contentcoding.Overflowed:
			return 0, false
		}
	}
	if r.stream {
		return tokensOf(r.scan.usage)
	}
	if r.body.overflowed {
		return 0, false
	}
	resp, _, _ := readResponse(r.body.buf.Bytes()) // a cut body keeps the usage read before it
	var fields map[string]json.RawMessage
	if present(resp.Usage) == nil || json.Unmarshal(resp.Usage, &fields) != nil {
		return 0, false
	}
	return tokensOf(fields)
}

// tokensOf is input + cache-write + output; ok is false when none of the three was
// reported.
func tokensOf(usage map[string]json.RawMessage) (int64, bool) {
	var total int64
	var seen bool
	for _, k := range []string{"input_tokens", "cache_creation_input_tokens", "output_tokens"} {
		var n int64
		if json.Unmarshal(usage[k], &n) == nil {
			total += n
			seen = true
		}
	}
	return total, seen
}

// cappedBuffer keeps up to maxJSONBody bytes and notes when more arrived.
type cappedBuffer struct {
	buf        bytes.Buffer
	overflowed bool
}

func (c *cappedBuffer) Write(p []byte) (int, error) {
	if room := maxJSONBody - c.buf.Len(); len(p) > room {
		c.overflowed = true
		p = p[:room]
	}
	c.buf.Write(p)
	return len(p), nil
}

// sseScanner reads a decoded SSE stream a byte run at a time, keeping only the
// event in progress, and merges the usage of message_start and message_delta.
type sseScanner struct {
	line    []byte
	data    []byte
	name    string
	fields  bool // the event has a field
	hasData bool
	size    int
	skip    bool // the event is over maxSSEEvent: ignored to its end
	prevCR  bool
	usage   map[string]json.RawMessage
}

func (s *sseScanner) Write(p []byte) (int, error) {
	for _, c := range p {
		if s.prevCR {
			s.prevCR = false
			if c == '\n' {
				continue
			}
		}
		if c == '\n' || c == '\r' {
			s.prevCR = c == '\r'
			s.endLine()
			continue
		}
		if s.skip {
			continue
		}
		if s.size++; s.size > maxSSEEvent {
			s.skip, s.line, s.data = true, nil, nil
			continue
		}
		s.line = append(s.line, c)
	}
	return len(p), nil
}

func (s *sseScanner) endLine() {
	if len(s.line) == 0 {
		if s.fields && !s.skip {
			s.dispatch()
		}
		s.line, s.data, s.name = s.line[:0], nil, ""
		s.fields, s.hasData, s.size, s.skip = false, false, 0, false
		return
	}
	if s.skip {
		return
	}
	field, value, _ := bytes.Cut(s.line, []byte(":"))
	value = bytes.TrimPrefix(value, []byte(" "))
	switch string(field) {
	case "event":
		s.name, s.fields = string(value), true
	case "data":
		if s.hasData {
			s.data = append(s.data, '\n')
		}
		s.data = append(s.data, value...)
		s.hasData, s.fields = true, true
	}
	s.line = s.line[:0]
}

// dispatch merges the usage of a message_start or message_delta event.
func (s *sseScanner) dispatch() {
	var w struct {
		Type    string `json:"type"`
		Message *struct {
			Usage json.RawMessage `json:"usage"`
		} `json:"message"`
		Usage json.RawMessage `json:"usage"`
	}
	name := s.name
	if name == "" {
		var t struct {
			Type string `json:"type"`
		}
		if json.Unmarshal(s.data, &t) != nil {
			return
		}
		name = t.Type
	}
	if name != "message_start" && name != "message_delta" {
		return
	}
	if json.Unmarshal(s.data, &w) != nil {
		return
	}
	if w.Message != nil {
		s.usage = mergeUsage(s.usage, w.Message.Usage)
	}
	s.usage = mergeUsage(s.usage, w.Usage)
}
