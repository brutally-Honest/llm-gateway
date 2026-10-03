package config_test

import (
	"slices"
	"testing"

	"github.com/brutally-honest/llm-gateway/internal/config"
)

// captureKeys are the capture.* keys that hold a size or a count, with the env var of
// each.
var captureKeys = []struct{ key, env string }{
	{"capture.queue_size", "GATEWAY_CAPTURE_QUEUE_SIZE"},
	{"capture.workers", "GATEWAY_CAPTURE_WORKERS"},
	{"capture.max_body_bytes", "GATEWAY_CAPTURE_MAX_BODY_BYTES"},
	{"capture.memory_limit", "GATEWAY_CAPTURE_MEMORY_LIMIT"},
}

// AC1: the six capture keys have the spec's defaults, and DefaultCaptureDir resolves
// the default directory from the injected environment only.
func TestLoad_CaptureDefaults(t *testing.T) {
	cfg, _, err := config.Load(config.Options{Upstreams: specs, LookupEnv: envOf()})
	if err != nil {
		t.Fatal(err)
	}
	want := config.Capture{
		Enabled:      true,
		Dir:          "",
		QueueSize:    256,
		Workers:      2,
		MaxBodyBytes: 32 << 20,
		MemoryLimit:  256 << 20,
	}
	if cfg.Capture != want {
		t.Errorf("Capture = %+v, want %+v", cfg.Capture, want)
	}
	if config.Defaults().Capture != want {
		t.Errorf("Defaults().Capture = %+v, want %+v", config.Defaults().Capture, want)
	}

	cases := []struct {
		name string
		env  []string
		want string
		ok   bool
	}{
		{"xdg_absolute", []string{"XDG_DATA_HOME", "/data/xdg", "HOME", "/home/u"}, "/data/xdg/llm-gateway", true},
		{"relative_xdg_ignored", []string{"XDG_DATA_HOME", "rel/xdg", "HOME", "/home/u"}, "/home/u/.local/share/llm-gateway", true},
		{"empty_xdg_ignored", []string{"XDG_DATA_HOME", "", "HOME", "/home/u"}, "/home/u/.local/share/llm-gateway", true},
		{"home_only", []string{"HOME", "/home/u"}, "/home/u/.local/share/llm-gateway", true},
		{"relative_home_unresolvable", []string{"HOME", "rel/home"}, "", false},
		{"neither", nil, "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := config.DefaultCaptureDir(envOf(tc.env...))
			if got != tc.want || ok != tc.ok {
				t.Errorf("DefaultCaptureDir() = %q, %v; want %q, %v", got, ok, tc.want, tc.ok)
			}
		})
	}
	// A relative XDG_DATA_HOME never makes loading fail: the default is resolved
	// outside Load, and Load reads only GATEWAY_* vars.
	t.Run("relative_xdg_load", func(t *testing.T) {
		if _, _, err := config.Load(config.Options{Upstreams: specs, LookupEnv: envOf("XDG_DATA_HOME", "rel/xdg")}); err != nil {
			t.Errorf("Load() error = %v", err)
		}
	})
}

// AC2: each GATEWAY_CAPTURE_* var beats the file, and env_overrides names the key.
func TestLoad_CaptureEnvOverridesFile(t *testing.T) {
	file := "capture:\n  enabled: true\n  dir: /file/dir\n  queue_size: 1\n  workers: 1\n  max_body_bytes: 1\n  memory_limit: 1\n"
	cases := []struct {
		key, envName, value string
		check               func(config.Capture) bool
	}{
		{"capture.enabled", "GATEWAY_CAPTURE_ENABLED", "false", func(c config.Capture) bool { return !c.Enabled }},
		{"capture.dir", "GATEWAY_CAPTURE_DIR", "/env/dir", func(c config.Capture) bool { return c.Dir == "/env/dir" }},
		{"capture.queue_size", "GATEWAY_CAPTURE_QUEUE_SIZE", "7", func(c config.Capture) bool { return c.QueueSize == 7 }},
		{"capture.workers", "GATEWAY_CAPTURE_WORKERS", "8", func(c config.Capture) bool { return c.Workers == 8 }},
		{"capture.max_body_bytes", "GATEWAY_CAPTURE_MAX_BODY_BYTES", "9", func(c config.Capture) bool { return c.MaxBodyBytes == 9 }},
		{"capture.memory_limit", "GATEWAY_CAPTURE_MEMORY_LIMIT", "10", func(c config.Capture) bool { return c.MemoryLimit == 10 }},
	}
	for _, tc := range cases {
		t.Run(tc.key, func(t *testing.T) {
			cfg, src, err := config.Load(config.Options{Path: writeFile(t, file), Upstreams: specs, LookupEnv: envOf(tc.envName, tc.value)})
			if err != nil {
				t.Fatal(err)
			}
			if !tc.check(cfg.Capture) {
				t.Errorf("env did not win: %+v", cfg.Capture)
			}
			if want := []string{tc.key}; !slices.Equal(src.EnvOverrides, want) {
				t.Errorf("EnvOverrides = %v, want %v", src.EnvOverrides, want)
			}
		})
	}
	// The file alone is what the six keys hold when env is silent.
	cfg, _, err := config.Load(config.Options{Path: writeFile(t, file), Upstreams: specs})
	if err != nil {
		t.Fatal(err)
	}
	want := config.Capture{Enabled: true, Dir: "/file/dir", QueueSize: 1, Workers: 1, MaxBodyBytes: 1, MemoryLimit: 1}
	if cfg.Capture != want {
		t.Errorf("file values not applied: %+v, want %+v", cfg.Capture, want)
	}
	// A large size fits: sizes are int64.
	cfg, _, err = config.Load(config.Options{Upstreams: specs, LookupEnv: envOf("GATEWAY_CAPTURE_MAX_BODY_BYTES", "8589934592")})
	if err != nil || cfg.Capture.MaxBodyBytes != 8<<30 {
		t.Errorf("8 GiB: MaxBodyBytes = %d, err = %v", cfg.Capture.MaxBodyBytes, err)
	}
}

// AC3: every invalid capture value fails with "invalid value", naming the key and the
// source (and the line, from the file).
func TestLoad_InvalidCaptureValues(t *testing.T) {
	check := func(t *testing.T, key, envName, field, value string) {
		t.Helper()
		path := writeFile(t, "capture:\n  "+field+": \""+value+"\"\n")
		want := config.Error{Key: key, Source: path, Reason: "invalid value", Line: 2}
		if e := loadError(t, config.Options{Path: path}); *e != want {
			t.Errorf("file %s=%q: error = %+v, want %+v", field, value, *e, want)
		}
		want = config.Error{Key: key, Source: envName, Reason: "invalid value"}
		if e := loadError(t, config.Options{LookupEnv: envOf(envName, value)}); *e != want {
			t.Errorf("env %s=%q: error = %+v, want %+v", field, value, *e, want)
		}
	}
	for _, k := range captureKeys {
		field := k.key[len("capture."):]
		for name, v := range map[string]string{"zero": "0", "negative": "-1", "not_a_number": "12abc"} {
			t.Run(k.key+"/"+name, func(t *testing.T) { check(t, k.key, k.env, field, v) })
		}
	}
	for _, v := range []string{"yes", "1", "TRUE", "on"} {
		t.Run("not_a_bool", func(t *testing.T) { check(t, "capture.enabled", "GATEWAY_CAPTURE_ENABLED", "enabled", v) })
	}
	for _, v := range []string{"rel/dir", "./dir", "~/captures"} {
		t.Run("relative_dir", func(t *testing.T) { check(t, "capture.dir", "GATEWAY_CAPTURE_DIR", "dir", v) })
	}
	// An empty dir can only come from the file: an empty env var is unset.
	t.Run("empty_dir_in_file", func(t *testing.T) {
		path := writeFile(t, "capture:\n  dir: \"\"\n")
		want := config.Error{Key: "capture.dir", Source: path, Reason: "invalid value", Line: 2}
		if e := loadError(t, config.Options{Path: path}); *e != want {
			t.Errorf("error = %+v, want %+v", *e, want)
		}
	})
}

// The capture block is parsed like upstreams: a mapping of known scalar keys.
func TestLoad_CaptureBlockShape(t *testing.T) {
	cases := []struct {
		name, file, key, reason string
		line                    int
	}{
		{"unknown_key", "capture:\n  nope: 1\n", "capture.nope", "unknown key", 2},
		{"not_a_mapping", "capture: 5\n", "capture", "invalid type", 1},
		{"nested_value", "capture:\n  workers:\n    x: 1\n", "capture.workers", "invalid type", 2},
		{"duplicate_key", "capture:\n  workers: 1\n  workers: 2\n", "capture.workers", "duplicate key", 3},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path := writeFile(t, tc.file)
			want := config.Error{Key: tc.key, Source: path, Reason: tc.reason, Line: tc.line}
			if e := loadError(t, config.Options{Path: path}); *e != want {
				t.Errorf("error = %+v, want %+v", *e, want)
			}
		})
	}
	// A null block or field sets nothing.
	for _, file := range []string{"capture:\n", "capture: ~\n", "capture:\n  workers:\n"} {
		cfg, _, err := config.Load(config.Options{Path: writeFile(t, file), Upstreams: specs})
		if err != nil {
			t.Errorf("%q: %v", file, err)
			continue
		}
		if cfg.Capture != config.Defaults().Capture {
			t.Errorf("%q: Capture = %+v, want defaults", file, cfg.Capture)
		}
	}
}
