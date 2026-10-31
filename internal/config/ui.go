package config

import (
	"net"
	"strconv"
	"strings"
)

// UI is the web UI's listener settings (spec 009, Config). It has its own listener,
// separate from the proxy.
type UI struct {
	Enabled    bool
	ListenAddr string // host:port; must be loopback and differ from listen_addr
}

// uiFile is the ui block. Like fileConfig, its tags are the known keys.
type uiFile struct {
	Enabled    *string `yaml:"enabled"`
	ListenAddr *string `yaml:"listen_addr"`
}

// uiListenKey is the dotted key of ui.listen_addr, the key validateUI names.
const uiListenKey = "ui.listen_addr"

// defaultUI is the UI on loopback port 7199, enabled.
func defaultUI() UI {
	return UI{Enabled: true, ListenAddr: "127.0.0.1:7199"}
}

// uiKey is the dotted key of one ui field.
func uiKey(field string) string {
	return "ui." + field
}

// uiSettings is one setting per ui field.
func uiSettings() []setting {
	return []setting{
		{uiKey("enabled"), func(c *Config, v string) string {
			switch v {
			case "true":
				c.UI.Enabled = true
			case "false":
				c.UI.Enabled = false
			default:
				return reasonInvalidValue
			}
			return ""
		}},
		// The address is checked for shape here; loopback and distinctness are checked
		// by validateUI once file and env are both applied.
		{uiListenKey, func(c *Config, v string) string {
			if !validAddress(v) {
				return reasonInvalidAddress
			}
			c.UI.ListenAddr = v
			return ""
		}},
	}
}

// loopbackHost is localhost or an IP in 127.0.0.0/8 or ::1. It never resolves a name.
func loopbackHost(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// sameListen reports whether two host:port addresses name the same listener. Ports are
// compared as numbers, hosts in canonical form. No name is looked up: localhost is
// taken as the two loopback addresses it is set to resolve to, 127.0.0.1 and ::1.
func sameListen(a, b string) bool {
	ha, pa, errA := net.SplitHostPort(a)
	hb, pb, errB := net.SplitHostPort(b)
	if errA != nil || errB != nil {
		return false
	}
	na, errA := strconv.ParseUint(pa, 10, 16)
	nb, errB := strconv.ParseUint(pb, 10, 16)
	if errA != nil || errB != nil || na != nb {
		return false
	}
	ca, cb := canonicalHost(ha), canonicalHost(hb)
	return ca == cb || localhostIs(ca, cb) || localhostIs(cb, ca)
}

// localhostIs reports whether name is localhost and other is one of the addresses it
// resolves to.
func localhostIs(name, other string) bool {
	return name == "localhost" && (other == "127.0.0.1" || other == "::1")
}

// canonicalHost lower-cases a host, and writes an IP in its canonical form.
func canonicalHost(host string) string {
	if ip := net.ParseIP(host); ip != nil {
		return ip.String()
	}
	return strings.ToLower(host)
}

// validateUI runs after file and env are applied. It checks nothing when the UI is off.
// origin says where ui.listen_addr came from, for the error's source.
func validateUI(c *Config, origin string) error {
	if !c.UI.Enabled {
		return nil
	}
	if origin == "" {
		origin = "default"
	}
	host, _, err := net.SplitHostPort(c.UI.ListenAddr)
	if err != nil {
		return &Error{Key: uiListenKey, Source: origin, Reason: reasonInvalidAddress}
	}
	if !loopbackHost(host) {
		return &Error{Key: uiListenKey, Source: origin, Reason: reasonNotLoopback}
	}
	if sameListen(c.UI.ListenAddr, c.ListenAddr) {
		return &Error{Key: uiListenKey, Source: origin, Reason: reasonSameAsListen}
	}
	return nil
}
