// Package core is the protocol-agnostic proxy. It knows no provider and no client:
// everything provider- or client-shaped is a value an Adapter or a Profile hands it.
package core

import "net/http"

// AuthKind is the kind of credential a request carries, never the credential itself.
type AuthKind string

// The auth kinds an adapter can report.
const (
	AuthNone   AuthKind = "none"
	AuthAPIKey AuthKind = "api_key"
	AuthBearer AuthKind = "bearer"
	AuthBoth   AuthKind = "both" // an API key and a bearer token are both present
)

// ClientUnknown labels a request that no registered profile matches. It is still
// proxied and captured.
const ClientUnknown = "unknown"

// Adapter is one wire protocol. Everything provider-shaped is a value it returns.
type Adapter interface {
	// Name is logged as the request's protocol.
	Name() string
	// Prefix is where the adapter is mounted: a leading slash and no trailing one.
	Prefix() string
	// DefaultBaseURL is the upstream's default. Config only checks that it parses
	// (url.Parse); the base_url rules for file and env values are not applied to it.
	DefaultBaseURL() string
	// AuthKind reports which kind of credential the request headers carry.
	AuthKind(h http.Header) AuthKind
	// ErrorBody is the protocol's error envelope for a gateway-created error.
	ErrorBody(reason string) (contentType string, body []byte)
}

// Profile is one client. It only detects the client in this phase.
type Profile interface {
	// Name is logged as the request's client.
	Name() string
	// Match reports whether the request came from this client.
	Match(r *http.Request) bool
}

// SessionKeyer is an optional Profile capability: it reports the opaque key a client
// uses to say which requests belong to one session. Core only measures and compares
// it; "" means the request carries none.
type SessionKeyer interface {
	SessionKey(h http.Header) string
}

// maxSessionKeyBytes is the longest session key core accepts.
const maxSessionKeyBytes = 256

// The reasons ValidSessionKey gives for a key it rejects.
const (
	SessionKeyEmpty            = "empty"
	SessionKeyTooLong          = "too_long"
	SessionKeyControlCharacter = "control_character"
)

// ValidSessionKey returns "" for a usable key, else why it is rejected: empty, over
// 256 bytes, or holding a control character (C0, DEL or C1).
func ValidSessionKey(key string) (reason string) {
	if key == "" {
		return SessionKeyEmpty
	}
	if len(key) > maxSessionKeyBytes {
		return SessionKeyTooLong
	}
	for _, r := range key {
		if r < 0x20 || (r >= 0x7f && r <= 0x9f) {
			return SessionKeyControlCharacter
		}
	}
	return ""
}
