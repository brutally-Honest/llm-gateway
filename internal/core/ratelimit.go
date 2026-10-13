package core

import "net/http"

// LimitErrorKind is which gateway-created error an adapter is asked to word.
type LimitErrorKind int

// The limit error kinds.
const (
	// LimitPermission is a call refused for want of a valid gateway key.
	LimitPermission LimitErrorKind = iota
	// LimitRateLimit is a call refused for a rate limit.
	LimitRateLimit
)

// UsageReader watches the bytes of one forwarded response and reports the tokens the
// provider said the call used. It is a side copy: Write must never block or fail.
type UsageReader interface {
	// Write is fed the response bytes exactly as sent to the client.
	Write(p []byte)
	// Tokens finishes the reader and reports input + cache-write + output tokens
	// (cache reads excluded). ok is false when no usage was seen or it cannot be
	// trusted. It is called once, after the last Write.
	Tokens() (n int64, ok bool)
}

// PanickedReader is an optional UsageReader extension for a reader whose decoder
// panicked and was recovered. Panicked reports the panic value's type; core logs it
// once and records no usage for that response.
type PanickedReader interface {
	Panicked() (panicType string, ok bool)
}

// RateLimitAdapter is an optional Adapter extension: what the rate limiter needs to
// know about a protocol.
type RateLimitAdapter interface {
	// IsModelCall reports whether the request is a model call, the only kind that is
	// keyed, counted and limited. path has the adapter's prefix stripped.
	IsModelCall(method, path string) bool
	// LimitErrorBody is the protocol's error envelope for a gateway limit error.
	LimitErrorBody(kind LimitErrorKind, message string) (contentType string, body []byte)
	// UsageReader returns a reader for the response about to be sent.
	UsageReader(status int, h http.Header, stream bool) UsageReader
}
