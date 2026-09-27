// Package config loads the gateway's settings: built-in defaults, then an optional
// YAML file, then GATEWAY_* env vars (ADR 0002). It is pure: no logging and no
// network. Errors name the key and where it came from, never its value.
package config

import (
	"fmt"
	"net"
	"strconv"
	"strings"
	"time"
)

// Config is the loaded, validated configuration.
type Config struct {
	ListenAddr      string
	LogLevel        string // one of debug, info, warn, error (lower-cased)
	ShutdownTimeout time.Duration
	Upstreams       map[string]Upstream // by adapter name; nil when Options.Upstreams is empty
}

// Options says where Load looks.
type Options struct {
	Path        string // -config; "" = look for DefaultPath
	DefaultPath string // "config.yaml"
	LookupEnv   func(string) (string, bool)
	Upstreams   []UpstreamSpec // the registered adapters; one upstreams.<name> block each
}

// Source says where the loaded values came from, for the startup line.
type Source struct {
	File         string   // "" means defaults
	EnvOverrides []string // key names, never values; empty, not nil, when none
}

// Error is every error Load returns. It holds no parser error, so it is safe to log
// field by field.
type Error struct {
	Key    string // "" for file-level errors (malformed yaml)
	Source string // file path or env var name
	Reason string // fixed text: "unknown key", "invalid duration", ...
	Line   int    // 0 if not applicable
}

// Error is built only from the fields, so it never contains a value.
func (e *Error) Error() string {
	var b strings.Builder
	b.WriteString("config: ")
	b.WriteString(e.Source)
	if e.Line > 0 {
		fmt.Fprintf(&b, ":%d", e.Line)
	}
	if e.Key != "" {
		b.WriteString(": ")
		b.WriteString(e.Key)
	}
	b.WriteString(": ")
	b.WriteString(e.Reason)
	return b.String()
}

// The fixed reasons an Error can carry.
const (
	reasonFileNotFound      = "file not found"
	reasonCannotRead        = "cannot read file"
	reasonMalformedYAML     = "malformed yaml"
	reasonNotMapping        = "not a mapping"
	reasonUnknownKey        = "unknown key"
	reasonDuplicateKey      = "duplicate key"
	reasonInvalidType       = "invalid type"
	reasonInvalidConfig     = "invalid config"
	reasonMultipleDocuments = "multiple documents"
	reasonInvalidDuration   = "invalid duration"
	reasonInvalidLevel      = "invalid level"
	reasonInvalidAddress    = "invalid address"
	reasonInvalidURL        = "invalid url"
)

// Defaults is the configuration with no file and no env.
func Defaults() Config {
	return Config{
		ListenAddr:      "127.0.0.1:7197",
		LogLevel:        "info",
		ShutdownTimeout: 10 * time.Minute,
	}
}

// setting validates one key's raw value and stores it. apply returns a reason, or ""
// if the value is valid.
type setting struct {
	key   string
	apply func(c *Config, v string) string
}

// settingsFor lists the settings in the order they are applied: the flat ones, then
// each spec's upstream fields. Every key fileValues returns has one.
func settingsFor(specs []UpstreamSpec) []setting {
	s := []setting{
		{"listen_addr", setListenAddr},
		{"log_level", setLogLevel},
		{"shutdown_timeout", setShutdownTimeout},
	}
	for _, spec := range specs {
		s = append(s, upstreamSettings(spec.Name)...)
	}
	return s
}

// envNameReplacer maps the characters of a dotted key that an env var cannot hold.
var envNameReplacer = strings.NewReplacer(".", "_", "-", "_")

// envName is the env var that overrides key: "upstreams.anthropic.base_url" is
// GATEWAY_UPSTREAMS_ANTHROPIC_BASE_URL.
func envName(key string) string {
	return "GATEWAY_" + strings.ToUpper(envNameReplacer.Replace(key))
}

func setListenAddr(c *Config, v string) string {
	host, port, err := net.SplitHostPort(v)
	// An empty host binds every interface. That must be written out as 0.0.0.0 or
	// [::], so it is never the result of a missing host.
	if err != nil || host == "" {
		return reasonInvalidAddress
	}
	// Base 10 and 16 bits: an integer from 0 to 65535, so named ports are rejected.
	if _, err := strconv.ParseUint(port, 10, 16); err != nil {
		return reasonInvalidAddress
	}
	c.ListenAddr = v
	return ""
}

func setLogLevel(c *Config, v string) string {
	level := strings.ToLower(v)
	switch level {
	case "debug", "info", "warn", "error":
		c.LogLevel = level
		return ""
	}
	return reasonInvalidLevel
}

// parseDuration is Go duration syntax, and must be > 0: a zero timeout would make every
// wait an immediate timeout.
func parseDuration(v string) (time.Duration, bool) {
	d, err := time.ParseDuration(v)
	return d, err == nil && d > 0
}

func setShutdownTimeout(c *Config, v string) string {
	d, ok := parseDuration(v)
	if !ok {
		return reasonInvalidDuration
	}
	c.ShutdownTimeout = d
	return ""
}
