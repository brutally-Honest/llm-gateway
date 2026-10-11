// Package claudecode is the Claude Code client profile. It detects the client and reports
// the session key the client sends; it reads no agent IDs.
package claudecode

import (
	"net/http"
	"strings"
)

// Name is the client label logged for a Claude Code request.
const Name = "claude-code"

// Profile detects Claude Code. It satisfies core.Profile without importing core.
type Profile struct{}

// Name is logged as the request's client.
func (Profile) Name() string { return Name }

// Match is true when the User-Agent starts with "claude-cli/" or the request carries
// "x-app: cli".
func (Profile) Match(r *http.Request) bool {
	if strings.HasPrefix(r.Header.Get("User-Agent"), "claude-cli/") {
		return true
	}
	return r.Header.Get("X-App") == "cli"
}

// sessionHeader carries the Claude Code session id, one value per session.
const sessionHeader = "X-Claude-Code-Session-Id"

// SessionKey is the value of the session header; present is false when the request has
// no such header. Core validates the value; the profile passes it on unparsed.
func (Profile) SessionKey(h http.Header) (key string, present bool) {
	return h.Get(sessionHeader), len(h.Values(sessionHeader)) > 0
}
