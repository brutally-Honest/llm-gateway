package config_test

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/brutally-honest/llm-gateway/internal/config"
)

// specs is the one upstream these tests register. The name is a config key, not
// something the config package knows about.
var specs = []config.UpstreamSpec{{Name: "anthropic", DefaultBaseURL: "https://api.anthropic.com"}}

func writeFile(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func envOf(pairs ...string) func(string) (string, bool) {
	m := map[string]string{}
	for i := 0; i+1 < len(pairs); i += 2 {
		m[pairs[i]] = pairs[i+1]
	}
	return func(name string) (string, bool) {
		v, ok := m[name]
		return v, ok
	}
}

func loadError(t *testing.T, opts config.Options) *config.Error {
	t.Helper()
	opts.Upstreams = specs
	_, _, err := config.Load(opts)
	var e *config.Error
	if !errors.As(err, &e) {
		t.Fatalf("Load() error = %v, want a *config.Error", err)
	}
	return e
}

// AC1: with no file and no env, the anthropic upstream has its defaults.
func TestLoad_UpstreamDefaults(t *testing.T) {
	cfg, _, err := config.Load(config.Options{Upstreams: specs, LookupEnv: envOf()})
	if err != nil {
		t.Fatal(err)
	}
	up, ok := cfg.Upstreams["anthropic"]
	if !ok || len(cfg.Upstreams) != 1 {
		t.Fatalf("Upstreams = %+v, want exactly anthropic", cfg.Upstreams)
	}
	if up.BaseURL == nil || up.BaseURL.String() != "https://api.anthropic.com" {
		t.Errorf("BaseURL = %v, want https://api.anthropic.com", up.BaseURL)
	}
	if up.ConnectTimeout != 10*time.Second || up.TLSHandshakeTimeout != 10*time.Second || up.ResponseHeaderTimeout != 10*time.Minute {
		t.Errorf("timeouts = %v %v %v, want 10s 10s 10m", up.ConnectTimeout, up.TLSHandshakeTimeout, up.ResponseHeaderTimeout)
	}
	if cfg.ShutdownTimeout != 10*time.Minute {
		t.Errorf("ShutdownTimeout = %v, want 10m", cfg.ShutdownTimeout)
	}
}

// AC2: each env var beats the file, and env_overrides names the key.
func TestLoad_UpstreamEnvOverridesFile(t *testing.T) {
	file := "upstreams:\n  anthropic:\n    base_url: https://file.example\n    connect_timeout: 1s\n    tls_handshake_timeout: 2s\n    response_header_timeout: 3s\n"
	cases := []struct {
		key, envName, value string
		check               func(config.Upstream) bool
	}{
		{"upstreams.anthropic.base_url", "GATEWAY_UPSTREAMS_ANTHROPIC_BASE_URL", "https://env.example", func(u config.Upstream) bool { return u.BaseURL.String() == "https://env.example" }},
		{"upstreams.anthropic.connect_timeout", "GATEWAY_UPSTREAMS_ANTHROPIC_CONNECT_TIMEOUT", "4s", func(u config.Upstream) bool { return u.ConnectTimeout == 4*time.Second }},
		{"upstreams.anthropic.tls_handshake_timeout", "GATEWAY_UPSTREAMS_ANTHROPIC_TLS_HANDSHAKE_TIMEOUT", "5s", func(u config.Upstream) bool { return u.TLSHandshakeTimeout == 5*time.Second }},
		{"upstreams.anthropic.response_header_timeout", "GATEWAY_UPSTREAMS_ANTHROPIC_RESPONSE_HEADER_TIMEOUT", "6s", func(u config.Upstream) bool { return u.ResponseHeaderTimeout == 6*time.Second }},
	}
	for _, tc := range cases {
		t.Run(tc.key, func(t *testing.T) {
			cfg, src, err := config.Load(config.Options{Path: writeFile(t, file), Upstreams: specs, LookupEnv: envOf(tc.envName, tc.value)})
			if err != nil {
				t.Fatal(err)
			}
			if !tc.check(cfg.Upstreams["anthropic"]) {
				t.Errorf("env did not win: %+v", cfg.Upstreams["anthropic"])
			}
			if want := []string{tc.key}; !slices.Equal(src.EnvOverrides, want) {
				t.Errorf("EnvOverrides = %v, want %v", src.EnvOverrides, want)
			}
		})
	}
	// The file alone is what the four keys hold when env is silent.
	cfg, _, err := config.Load(config.Options{Path: writeFile(t, file), Upstreams: specs})
	if err != nil {
		t.Fatal(err)
	}
	up := cfg.Upstreams["anthropic"]
	if up.BaseURL.String() != "https://file.example" || up.ConnectTimeout != time.Second || up.TLSHandshakeTimeout != 2*time.Second || up.ResponseHeaderTimeout != 3*time.Second {
		t.Errorf("file values not applied: %+v", up)
	}
}

// AC5: three timeouts, three invalid values, from the file and from env.
func TestLoad_InvalidUpstreamDuration(t *testing.T) {
	envNames := map[string]string{
		"connect_timeout":         "GATEWAY_UPSTREAMS_ANTHROPIC_CONNECT_TIMEOUT",
		"tls_handshake_timeout":   "GATEWAY_UPSTREAMS_ANTHROPIC_TLS_HANDSHAKE_TIMEOUT",
		"response_header_timeout": "GATEWAY_UPSTREAMS_ANTHROPIC_RESPONSE_HEADER_TIMEOUT",
	}
	for field, name := range envNames {
		key := "upstreams.anthropic." + field
		for _, v := range []string{"abc", "0s", "-5s"} {
			path := writeFile(t, "upstreams:\n  anthropic:\n    "+field+": \""+v+"\"\n")
			want := config.Error{Key: key, Source: path, Reason: "invalid duration", Line: 3}
			if e := loadError(t, config.Options{Path: path}); *e != want {
				t.Errorf("file %s=%q: error = %+v, want %+v", field, v, *e, want)
			}
			want = config.Error{Key: key, Source: name, Reason: "invalid duration"}
			if e := loadError(t, config.Options{LookupEnv: envOf(name, v)}); *e != want {
				t.Errorf("env %s=%q: error = %+v, want %+v", field, v, *e, want)
			}
		}
	}
}

// AC6: an unknown field or an unknown upstream name is an unknown key with its path.
func TestLoad_UnknownUpstreamKey(t *testing.T) {
	path := writeFile(t, "upstreams:\n  anthropic:\n    nope: x\n")
	want := config.Error{Key: "upstreams.anthropic.nope", Source: path, Reason: "unknown key", Line: 3}
	if e := loadError(t, config.Options{Path: path}); *e != want {
		t.Errorf("error = %+v, want %+v", *e, want)
	}
	path = writeFile(t, "log_level: info\nupstreams:\n  other:\n    base_url: https://x.example\n")
	want = config.Error{Key: "upstreams.other", Source: path, Reason: "unknown key", Line: 3}
	if e := loadError(t, config.Options{Path: path}); *e != want {
		t.Errorf("error = %+v, want %+v", *e, want)
	}
}

// AC7: the example file, with its upstreams block, loads as the defaults.
func TestLoad_ExampleFileHasUpstreamDefaults(t *testing.T) {
	b, err := os.ReadFile(filepath.Join("..", "..", "config.example.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	path := writeFile(t, string(b))
	got, _, err := config.Load(config.Options{Path: path, Upstreams: specs})
	if err != nil {
		t.Fatal(err)
	}
	want, _, err := config.Load(config.Options{Upstreams: specs})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("example file = %+v, want defaults %+v", got, want)
	}
	if _, ok := got.Upstreams["anthropic"]; !ok {
		t.Errorf("example file loaded no anthropic upstream")
	}
}

// AC3: a base_url that is not a plain https (or loopback http) origin is rejected with
// the fixed reason, naming the key and source, never the value.
func TestLoad_InvalidUpstreamURL(t *testing.T) {
	const sentinel = "s3ntinel-v4lue"
	const key = "upstreams.anthropic.base_url"
	const envName = "GATEWAY_UPSTREAMS_ANTHROPIC_BASE_URL"
	values := map[string][]string{
		"relative":       {"/" + sentinel, sentinel, "//" + sentinel},
		"empty host":     {"https://", "https:///" + sentinel},
		"userinfo":       {"https://" + sentinel + "@host.example"},
		"query":          {"https://host.example/" + sentinel + "?a=b", "https://host.example?"},
		"fragment":       {"https://host.example/" + sentinel + "#f", "https://host.example#"},
		"scheme":         {"ftp://" + sentinel + ".example", "ws://" + sentinel + ".example"},
		"http non-local": {"http://" + sentinel + ".example", "http://10.0.0.1:8080", "http://localhost.example"},
	}
	for name, vs := range values {
		for _, v := range vs {
			t.Run(name, func(t *testing.T) {
				path := writeFile(t, "upstreams:\n  anthropic:\n    base_url: \""+v+"\"\n")
				want := config.Error{Key: key, Source: path, Reason: "invalid url", Line: 3}
				e := loadError(t, config.Options{Path: path})
				if *e != want {
					t.Errorf("file %q: error = %+v, want %+v", v, *e, want)
				}
				want = config.Error{Key: key, Source: envName, Reason: "invalid url"}
				e = loadError(t, config.Options{LookupEnv: envOf(envName, v)})
				if *e != want {
					t.Errorf("env %q: error = %+v, want %+v", v, *e, want)
				}
				if s := e.Error() + e.Key + e.Source + e.Reason; strings.Contains(s, sentinel) {
					t.Errorf("error contains the value: %q", s)
				}
			})
		}
	}
}

// AC4: https anywhere, and http only to a loopback host, are accepted.
func TestLoad_UpstreamURLAccepted(t *testing.T) {
	for _, v := range []string{
		"https://api.anthropic.com", "https://host/api",
		"http://127.0.0.1:8080", "http://localhost:8080", "http://[::1]:8080",
	} {
		cfg, _, err := config.Load(config.Options{Upstreams: specs, LookupEnv: envOf("GATEWAY_UPSTREAMS_ANTHROPIC_BASE_URL", v)})
		if err != nil {
			t.Errorf("%q: %v", v, err)
			continue
		}
		if got := cfg.Upstreams["anthropic"].BaseURL.String(); got != v {
			t.Errorf("BaseURL = %q, want %q", got, v)
		}
	}
}
