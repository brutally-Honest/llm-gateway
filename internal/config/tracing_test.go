package config

import "testing"

// Spec 021 AC10: tracing turns on from the file or env, with an OTLP/HTTP endpoint.
func TestLoad_Tracing(t *testing.T) {
	path := writeConfig(t, "tracing:\n  enabled: true\n  otlp_endpoint: https://collector.example:4318\n")
	cfg, _, err := Load(Options{Path: path})
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.Tracing.Enabled || cfg.Tracing.OTLPEndpoint.String() != "https://collector.example:4318" {
		t.Errorf("tracing = %+v, want on at https://collector.example:4318", cfg.Tracing)
	}
	cfg, src, err := Load(Options{LookupEnv: env("GATEWAY_TRACING_ENABLED", "true",
		"GATEWAY_TRACING_OTLP_ENDPOINT", "http://localhost:14318")})
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.Tracing.Enabled || cfg.Tracing.OTLPEndpoint.String() != "http://localhost:14318" {
		t.Errorf("tracing from env = %+v", cfg.Tracing)
	}
	if len(src.EnvOverrides) != 2 {
		t.Errorf("env overrides = %v, want both tracing keys", src.EnvOverrides)
	}
}

// Spec 021 E8: a bad endpoint or value is a config error naming the key, never the value.
func TestLoad_TracingInvalid(t *testing.T) {
	for _, c := range []struct{ yaml, key, reason string }{
		{"tracing:\n  otlp_endpoint: http://10.0.0.5:4318\n", "tracing.otlp_endpoint", reasonInvalidURL},
		{"tracing:\n  otlp_endpoint: grpc://127.0.0.1:4317\n", "tracing.otlp_endpoint", reasonInvalidURL},
		{"tracing:\n  otlp_endpoint: not a url\n", "tracing.otlp_endpoint", reasonInvalidURL},
		{"tracing:\n  enabled: yes-please\n", "tracing.enabled", reasonInvalidValue},
		{"tracing:\n  sample_ratio: 1\n", "tracing.sample_ratio", reasonUnknownKey},
		{"tracing: 3\n", "tracing", reasonInvalidType},
	} {
		e := loadErr(t, Options{Path: writeConfig(t, c.yaml)})
		if e.Key != c.key || e.Reason != c.reason {
			t.Errorf("%q: error = %+v, want %s %q", c.yaml, e, c.key, c.reason)
		}
	}
}
