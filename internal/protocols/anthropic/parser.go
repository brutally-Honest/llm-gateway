package anthropic

import (
	"net/http"
	"strings"

	"github.com/brutally-honest/llm-gateway/internal/contentcoding"
	"github.com/brutally-honest/llm-gateway/internal/core"
)

// messagesPath is the one path the parser reads, as the exchange records it: the
// adapter's prefix already stripped.
const messagesPath = "/v1/messages"

// parser turns a captured POST /v1/messages exchange into canonical events. It reads
// only the stored copy, decoded through the shared content decoder.
type parser struct{}

// Parser is the adapter's parser; it satisfies core.ParsingAdapter.
func (Adapter) Parser() core.Parser { return parser{} }

// HashExcludedFields leaves cache_control out of block, system and tools hashes: it
// is a caching annotation the client moves between turns, not content.
func (parser) HashExcludedFields() []string { return []string{"cache_control"} }

// Parse reads one exchange. Anything but POST /v1/messages is skipped. Each body is
// decoded before it is read: an encoding the decoder doesn't know makes the parse
// unsupported_encoding, and a body cut short (truncated by the capture cap, or past
// the decode limit) makes it partial. A response the gateway wrote is not read.
func (parser) Parse(in core.ParseInput) core.ParseResult {
	if in.Method != http.MethodPost || in.Path != messagesPath {
		return core.ParseResult{Status: core.ParseSkipped}
	}
	reqBody, reqStatus := decodeBody(in.RequestHeader, in.RequestBody, in.RequestTruncated, in.DecodeLimit)
	status := reqStatus

	var events []core.Event
	if reqStatus != core.ParseUnsupportedEncoding {
		evs, err := parseRequest(reqBody)
		switch {
		case err == nil:
			events = evs
		case reqStatus == core.ParsePartial:
			// A body cut short is expected not to parse whole: partial, not failed,
			// with the events read before the cut.
			events = evs
		default:
			status = core.ParseFailed
		}
	}
	// A response the gateway wrote itself (its own error, or a bare 499 when the
	// client left first) is not the provider's: it has no response events, and above
	// all no error event, since the exchange's flags already say how it ended.
	if in.GatewayResponse {
		return core.ParseResult{Status: status, Events: events}
	}
	respBody, respStatus := decodeBody(in.ResponseHeader, in.ResponseBody, in.ResponseTruncated, in.DecodeLimit)
	status = worse(status, respStatus)
	// A 2xx event stream is reassembled; anything else, an error status included, is
	// read as JSON.
	if respStatus != core.ParseUnsupportedEncoding {
		cut := respStatus == core.ParsePartial
		var evs []core.Event
		var st core.ParseStatus
		if in.Stream && in.Status >= 200 && in.Status <= 299 {
			evs, st = parseStream(in.Status, respBody, cut)
		} else {
			evs, st = parseJSONResponse(in.Status, respBody, cut)
		}
		events = append(events, evs...)
		status = worse(status, st)
	}
	return core.ParseResult{Status: status, Events: events}
}

// decodeBody undoes the body's Content-Encoding and says what that leaves the parse:
// ok, partial when the body was truncated or cut short, or unsupported_encoding.
func decodeBody(h http.Header, body []byte, truncated bool, limit int64) ([]byte, core.ParseStatus) {
	out, st := contentcoding.Decode(strings.Join(h.Values("Content-Encoding"), ","), body, limit)
	switch {
	case st == contentcoding.Unsupported:
		return out, core.ParseUnsupportedEncoding
	case st == contentcoding.CutShort || truncated:
		return out, core.ParsePartial
	}
	return out, core.ParseOK
}

// worse is the status further from ok: ok, then partial, then unsupported_encoding,
// then failed.
func worse(a, b core.ParseStatus) core.ParseStatus {
	rank := map[core.ParseStatus]int{
		core.ParseOK:                  0,
		core.ParsePartial:             1,
		core.ParseUnsupportedEncoding: 2,
		core.ParseFailed:              3,
	}
	if rank[b] > rank[a] {
		return b
	}
	return a
}
