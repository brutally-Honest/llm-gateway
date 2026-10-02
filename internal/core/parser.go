package core

import "net/http"

// SecretDeclarer is an optional Adapter extension: the protocol's own secrets, which
// are redacted on top of the ones core always redacts.
type SecretDeclarer interface {
	// SecretHeaders are header names whose values are secrets.
	SecretHeaders() []string
	// SecretQueryParams are query parameters whose values are secrets.
	SecretQueryParams() []string
}

// ParsingAdapter is an optional Adapter extension: a parser that turns a stored
// exchange into canonical events. An adapter without one is still captured, with its
// parse status skipped.
type ParsingAdapter interface {
	Parser() Parser
}

// ParseStatus is how far a parser got with one exchange.
type ParseStatus string

// The parse statuses.
const (
	// ParseOK means every event the exchange holds was parsed.
	ParseOK ParseStatus = "ok"
	// ParsePartial means some events were parsed, but not all: a body was cut short
	// or part of it could not be read.
	ParsePartial ParseStatus = "partial"
	// ParseSkipped means no parser applies to the exchange.
	ParseSkipped ParseStatus = "skipped"
	// ParseUnsupportedEncoding means a body's content coding could not be decoded.
	ParseUnsupportedEncoding ParseStatus = "unsupported_encoding"
	// ParseFailed means the parser could not read the exchange at all.
	ParseFailed ParseStatus = "failed"
)

// ParseInput is one finished exchange as a parser sees it: redacted headers and the
// captured bodies, still content-encoded.
type ParseInput struct {
	Method, Path, Query                 string
	Status                              int
	RequestHeader, ResponseHeader       http.Header
	RequestBody, ResponseBody           []byte // still content-encoded
	RequestTruncated, ResponseTruncated bool
	// RequestIncomplete means the request copy was sealed before the body's end: a
	// request cut short, like a truncated one.
	RequestIncomplete bool
	Stream            bool
	// GatewayResponse means upstream sent no response: no status, no header, no body.
	// The gateway wrote the response itself, because it made an error (the exchange's
	// gateway_error) or the client left before upstream answered (a bare 499). Status
	// and ResponseBody are then the gateway's, never the provider's.
	GatewayResponse bool
	// DecodeLimit caps each decoded body (capture.max_body_bytes); the parser passes
	// it to the content decoder.
	DecodeLimit int64
}

// ParseResult is what a parser made of one exchange.
type ParseResult struct {
	Status ParseStatus
	Events []Event
}

// Parser turns one stored exchange into canonical events. Core decides nothing
// about any wire format: it calls the parser and stores what comes back.
type Parser interface {
	// Parse reads one exchange. It never fails the request; a problem is a status.
	Parse(in ParseInput) ParseResult
	// HashExcludedFields are wire-only keys left out of content hashes, removed from
	// the top level of a block, a system entry or a tool definition only.
	HashExcludedFields() []string
}
