// Package claudecode is the Claude Code client profile. In this phase it only detects
// the client; it reads no session or agent IDs.
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
