package config

import (
	"sort"
	"strconv"
)

// StrategyFixedWindowMemory is the only limiter strategy so far.
const StrategyFixedWindowMemory = "fixed_window_memory"

// RateLimit is the rate limiter's settings (spec 006, Config).
type RateLimit struct {
	Enabled  bool
	Strategy string
	Keys     map[string]KeyLimit // by gateway key; file-only
}

// KeyLimit is one gateway key's limits per minute. 0 means no limit on that axis.
type KeyLimit struct {
	RPM int64
	TPM int64
}

// keyFile is one ratelimit.keys.<name> block. Its tags are the known keys.
type keyFile struct {
	RPM *string `yaml:"rpm"`
	TPM *string `yaml:"tpm"`
}

// ratelimitFile is the ratelimit block. Its tags are the known keys.
type ratelimitFile struct {
	Enabled  *string            `yaml:"enabled"`
	Strategy *string            `yaml:"strategy"`
	Keys     map[string]keyFile `yaml:"keys"`
}

func defaultRateLimit() RateLimit {
	return RateLimit{Strategy: StrategyFixedWindowMemory}
}

func ratelimitKey(field string) string { return "ratelimit." + field }

func ratelimitKeyField(name, field string) string {
	return "ratelimit.keys." + name + "." + field
}

// ratelimitSettings is one setting per scalar field; the keys block is file-only.
func ratelimitSettings() []setting {
	return []setting{
		{ratelimitKey("enabled"), func(c *Config, v string) string {
			switch v {
			case "true":
				c.RateLimit.Enabled = true
			case "false":
				c.RateLimit.Enabled = false
			default:
				return reasonInvalidValue
			}
			return ""
		}},
		{ratelimitKey("strategy"), func(c *Config, v string) string {
			c.RateLimit.Strategy = v
			return ""
		}},
	}
}

// parseLimit is a base-10 integer >= 0.
func parseLimit(v *string) (int64, bool) {
	if v == nil {
		return 0, true
	}
	n, err := strconv.ParseInt(*v, 10, 64)
	return n, err == nil && n >= 0
}

// applyKeys converts the file's keys block, failing on a bad limit or an empty name.
func applyKeys(c *Config, path string, raw map[string]keyFile, lines map[string]int) error {
	names := make([]string, 0, len(raw))
	for name := range raw {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		if name == "" {
			return &Error{Key: "ratelimit.keys", Source: path, Reason: reasonInvalidValue, Line: lines["ratelimit.keys."]}
		}
		kf := raw[name]
		rpm, ok := parseLimit(kf.RPM)
		if !ok {
			k := ratelimitKeyField(name, "rpm")
			return &Error{Key: k, Source: path, Reason: reasonInvalidValue, Line: lines[k]}
		}
		tpm, ok := parseLimit(kf.TPM)
		if !ok {
			k := ratelimitKeyField(name, "tpm")
			return &Error{Key: k, Source: path, Reason: reasonInvalidValue, Line: lines[k]}
		}
		if c.RateLimit.Keys == nil {
			c.RateLimit.Keys = map[string]KeyLimit{}
		}
		c.RateLimit.Keys[name] = KeyLimit{RPM: rpm, TPM: tpm}
	}
	return nil
}

// validateRateLimit runs after file and env are applied.
func validateRateLimit(c *Config, path string) error {
	source := path
	if source == "" {
		source = "default"
	}
	if c.RateLimit.Strategy != StrategyFixedWindowMemory {
		return &Error{Key: "ratelimit.strategy", Source: source, Reason: reasonInvalidValue}
	}
	if c.RateLimit.Enabled && len(c.RateLimit.Keys) == 0 {
		return &Error{Key: "ratelimit.keys", Source: source, Reason: reasonInvalidValue}
	}
	return nil
}
