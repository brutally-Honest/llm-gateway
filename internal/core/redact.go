package core

import (
	"net/http"
	"net/url"
	"strings"
)

// Redacted replaces the value of every secret header and query parameter in a
// captured exchange.
const Redacted = "[REDACTED]"

// secretHeaders are the generic HTTP auth headers core always redacts, whatever the
// adapter declares.
var secretHeaders = []string{"Authorization", "Proxy-Authorization", "Cookie", "Set-Cookie"}

// redactHeader returns a clone of h in which every value of core's secret headers and
// of the names in secret is Redacted. Names are matched case-insensitively and kept,
// as is the number of values. h itself is never touched.
func redactHeader(h http.Header, secret []string) http.Header {
	out := h.Clone()
	for name, values := range out {
		if !isSecretHeader(name, secret) {
			continue
		}
		for i := range values {
			values[i] = Redacted
		}
	}
	return out
}

func isSecretHeader(name string, secret []string) bool {
	for _, s := range secretHeaders {
		if strings.EqualFold(name, s) {
			return true
		}
	}
	for _, s := range secret {
		if strings.EqualFold(name, s) {
			return true
		}
	}
	return false
}

// redactQuery returns the raw query with the value of every parameter whose
// unescaped name is in secret replaced by Redacted. It splits by hand on `&` and the
// first `=`, so every other byte, `;`, bad escapes, repeated and empty parameters
// included, stays as sent. A parameter with no `=` has no value and is kept.
func redactQuery(raw string, secret []string) string {
	if raw == "" || len(secret) == 0 {
		return raw
	}
	parts := strings.Split(raw, "&")
	changed := false
	for i, p := range parts {
		name, _, ok := strings.Cut(p, "=")
		if !ok || !isSecretParam(name, secret) {
			continue
		}
		parts[i] = name + "=" + Redacted
		changed = true
	}
	if !changed {
		return raw
	}
	return strings.Join(parts, "&")
}

// isSecretParam matches a raw parameter name against secret after unescaping it; a
// name that does not unescape is compared as sent. The match is case-sensitive, as
// query parameter names are.
func isSecretParam(raw string, secret []string) bool {
	name, err := url.QueryUnescape(raw)
	if err != nil {
		name = raw
	}
	for _, s := range secret {
		if name == s {
			return true
		}
	}
	return false
}
