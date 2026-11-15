package config

import "net/url"

// Tracing is the tracing block (spec 021): OpenTelemetry spans for proxied calls,
// exported over OTLP/HTTP. Off by default.
type Tracing struct {
	Enabled bool
	// OTLPEndpoint is the collector's OTLP/HTTP base URL; spans go to <it>/v1/traces.
	// https anywhere, http on loopback only, like an upstream's base_url.
	OTLPEndpoint *url.URL
}

// tracingFile is the tracing block. Like fileConfig, its tags are the known keys.
type tracingFile struct {
	Enabled      *string `yaml:"enabled"`
	OTLPEndpoint *string `yaml:"otlp_endpoint"`
}

// defaultOTLPEndpoint is Jaeger's OTLP/HTTP port on loopback, as the compose stack in
// deploy/observability publishes it.
const defaultOTLPEndpoint = "http://127.0.0.1:4318"

// defaultTracing is tracing off, pointed at a local collector.
func defaultTracing() Tracing {
	u, _ := url.Parse(defaultOTLPEndpoint)
	return Tracing{OTLPEndpoint: u}
}

// tracingKey is the dotted key of one tracing field.
func tracingKey(field string) string {
	return "tracing." + field
}

// tracingSettings is one setting per tracing field.
func tracingSettings() []setting {
	return []setting{
		{tracingKey("enabled"), func(c *Config, v string) string {
			switch v {
			case "true":
				c.Tracing.Enabled = true
			case "false":
				c.Tracing.Enabled = false
			default:
				return reasonInvalidValue
			}
			return ""
		}},
		{tracingKey("otlp_endpoint"), func(c *Config, v string) string {
			u, ok := parseBaseURL(v)
			if !ok {
				return reasonInvalidURL
			}
			c.Tracing.OTLPEndpoint = u
			return ""
		}},
	}
}
