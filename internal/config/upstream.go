package config

import (
	"net"
	"net/url"
	"strings"
	"time"
)

// Upstream is one adapter's upstream: where it lives and how long to wait for it.
// There is deliberately no timeout for the request as a whole.
type Upstream struct {
	BaseURL               *url.URL
	ConnectTimeout        time.Duration
	TLSHandshakeTimeout   time.Duration
	ResponseHeaderTimeout time.Duration
}

// UpstreamSpec names one upstream and its default base URL. The adapters supply the
// list, so this package names no provider.
type UpstreamSpec struct {
	Name           string
	DefaultBaseURL string
}

// The fields under upstreams.<name>, in the order they are applied.
const (
	fieldBaseURL               = "base_url"
	fieldConnectTimeout        = "connect_timeout"
	fieldTLSHandshakeTimeout   = "tls_handshake_timeout"
	fieldResponseHeaderTimeout = "response_header_timeout"
)

const (
	defaultConnectTimeout        = 10 * time.Second
	defaultTLSHandshakeTimeout   = 10 * time.Second
	defaultResponseHeaderTimeout = 10 * time.Minute
)

// upstreamKey is the dotted key of one upstream field.
func upstreamKey(name, field string) string {
	return "upstreams." + name + "." + field
}

// defaultUpstream is the spec's upstream with no file and no env. It returns false if
// the spec's own default URL does not parse.
func defaultUpstream(spec UpstreamSpec) (Upstream, bool) {
	u, err := url.Parse(spec.DefaultBaseURL)
	if err != nil {
		return Upstream{}, false
	}
	return Upstream{
		BaseURL:               u,
		ConnectTimeout:        defaultConnectTimeout,
		TLSHandshakeTimeout:   defaultTLSHandshakeTimeout,
		ResponseHeaderTimeout: defaultResponseHeaderTimeout,
	}, true
}

// parseBaseURL parses and validates an upstream base URL. It returns false for
// anything that is not an absolute https URL (or http to a loopback host) with a host,
// no userinfo, no query and no fragment.
func parseBaseURL(v string) (*url.URL, bool) {
	// An empty query or fragment is swallowed by url.Parse, so look at the string.
	if strings.ContainsAny(v, "?#") {
		return nil, false
	}
	u, err := url.Parse(v)
	if err != nil || !u.IsAbs() || u.Hostname() == "" || u.User != nil {
		return nil, false
	}
	switch u.Scheme {
	case "https":
	case "http":
		host := u.Hostname()
		if host != "localhost" && !isLoopbackIP(host) {
			return nil, false
		}
	default:
		return nil, false
	}
	return u, true
}

func isLoopbackIP(host string) bool {
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// upstreamSettings is one setting per field of the named upstream.
func upstreamSettings(name string) []setting {
	duration := func(field string, set func(*Upstream, time.Duration)) setting {
		return setting{upstreamKey(name, field), func(c *Config, v string) string {
			d, ok := parseDuration(v)
			if !ok {
				return reasonInvalidDuration
			}
			up := c.Upstreams[name]
			set(&up, d)
			c.Upstreams[name] = up
			return ""
		}}
	}
	return []setting{
		{upstreamKey(name, fieldBaseURL), func(c *Config, v string) string {
			u, ok := parseBaseURL(v)
			if !ok {
				return reasonInvalidURL
			}
			up := c.Upstreams[name]
			up.BaseURL = u
			c.Upstreams[name] = up
			return ""
		}},
		duration(fieldConnectTimeout, func(u *Upstream, d time.Duration) { u.ConnectTimeout = d }),
		duration(fieldTLSHandshakeTimeout, func(u *Upstream, d time.Duration) { u.TLSHandshakeTimeout = d }),
		duration(fieldResponseHeaderTimeout, func(u *Upstream, d time.Duration) { u.ResponseHeaderTimeout = d }),
	}
}
