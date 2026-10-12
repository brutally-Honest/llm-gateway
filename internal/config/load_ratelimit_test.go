package config_test

import (
	"errors"
	"testing"

	"github.com/brutally-honest/llm-gateway/internal/config"
)

// AC1: rate limiting is off by default, with the fixed-window strategy and no keys.
func TestLoad_RateLimitDefaultsOff(t *testing.T) {
	cfg, _, err := config.Load(config.Options{Upstreams: specs, LookupEnv: envOf()})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.RateLimit.Enabled || cfg.RateLimit.Strategy != "fixed_window_memory" || len(cfg.RateLimit.Keys) != 0 {
		t.Errorf("RateLimit = %+v", cfg.RateLimit)
	}
}

func TestLoad_RateLimitFileAndEnv(t *testing.T) {
	file := "ratelimit:\n  keys:\n    alice:\n      rpm: 60\n      tpm: 1000\n    bob:\n      rpm: 5\n"
	cfg, src, err := config.Load(config.Options{
		Path: writeFile(t, file), Upstreams: specs,
		LookupEnv: envOf("GATEWAY_RATELIMIT_ENABLED", "true"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.RateLimit.Enabled || cfg.RateLimit.Strategy != "fixed_window_memory" {
		t.Errorf("RateLimit = %+v", cfg.RateLimit)
	}
	if got := cfg.RateLimit.Keys["alice"]; got != (config.KeyLimit{RPM: 60, TPM: 1000}) {
		t.Errorf("alice = %+v", got)
	}
	if got := cfg.RateLimit.Keys["bob"]; got != (config.KeyLimit{RPM: 5}) {
		t.Errorf("bob = %+v", got)
	}
	if len(src.EnvOverrides) != 1 || src.EnvOverrides[0] != "ratelimit.enabled" {
		t.Errorf("EnvOverrides = %v", src.EnvOverrides)
	}
}

// AC16: each invalid block fails Load with an *Error naming the field.
func TestLoad_RateLimitInvalid_NamesField(t *testing.T) {
	cases := []struct {
		name, file string
		env        []string
		field      string
	}{
		{"unknown_strategy", "ratelimit:\n  strategy: nope\n  keys:\n    a:\n      rpm: 1\n", nil, "ratelimit.strategy"},
		{"unknown_strategy_env", "", []string{"GATEWAY_RATELIMIT_STRATEGY", "nope"}, "ratelimit.strategy"},
		{"negative_rpm", "ratelimit:\n  keys:\n    a:\n      rpm: -1\n", nil, "ratelimit.keys.a.rpm"},
		{"non_integer_tpm", "ratelimit:\n  keys:\n    a:\n      tpm: 1.5\n", nil, "ratelimit.keys.a.tpm"},
		{"empty_key_name", "ratelimit:\n  keys:\n    \"\":\n      rpm: 1\n", nil, "ratelimit.keys"},
		{"enabled_no_keys", "ratelimit:\n  enabled: true\n", nil, "ratelimit.keys"},
		{"enabled_env_no_keys", "", []string{"GATEWAY_RATELIMIT_ENABLED", "true"}, "ratelimit.keys"},
		{"unknown_subkey", "ratelimit:\n  keys:\n    a:\n      rps: 1\n", nil, "ratelimit.keys.a.rps"},
		{"unknown_field", "ratelimit:\n  bogus: 1\n", nil, "ratelimit.bogus"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			opts := config.Options{Upstreams: specs, LookupEnv: envOf(tc.env...)}
			if tc.file != "" {
				opts.Path = writeFile(t, tc.file)
			}
			_, _, err := config.Load(opts)
			var ce *config.Error
			if !errors.As(err, &ce) || ce.Key != tc.field {
				t.Fatalf("err = %v, want *Error with field %q", err, tc.field)
			}
		})
	}
}
