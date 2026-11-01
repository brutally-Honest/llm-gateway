package config_test

import (
	"slices"
	"testing"

	"github.com/brutally-honest/llm-gateway/internal/config"
)

// AC3: ui.enabled and ui.listen_addr apply from the file and from env, env beats the
// file, and an unknown key under ui is a config error.
func TestLoad_UIPrecedenceAndUnknownKey(t *testing.T) {
	cfg, _, err := config.Load(config.Options{Upstreams: specs, LookupEnv: envOf()})
	if err != nil {
		t.Fatal(err)
	}
	if want := (config.UI{Enabled: true, ListenAddr: "127.0.0.1:7199"}); cfg.UI != want {
		t.Errorf("UI defaults = %+v, want %+v", cfg.UI, want)
	}

	path := writeFile(t, "ui:\n  enabled: false\n  listen_addr: 127.0.0.2:7199\n")
	cfg, src, err := config.Load(config.Options{Path: path, Upstreams: specs, LookupEnv: envOf()})
	if err != nil {
		t.Fatal(err)
	}
	if want := (config.UI{Enabled: false, ListenAddr: "127.0.0.2:7199"}); cfg.UI != want {
		t.Errorf("file UI = %+v, want %+v", cfg.UI, want)
	}
	if len(src.EnvOverrides) != 0 {
		t.Errorf("EnvOverrides = %v, want none", src.EnvOverrides)
	}

	cfg, src, err = config.Load(config.Options{Path: path, Upstreams: specs, LookupEnv: envOf(
		"GATEWAY_UI_ENABLED", "true",
		"GATEWAY_UI_LISTEN_ADDR", "127.0.0.3:7200",
	)})
	if err != nil {
		t.Fatal(err)
	}
	if want := (config.UI{Enabled: true, ListenAddr: "127.0.0.3:7200"}); cfg.UI != want {
		t.Errorf("env UI = %+v, want %+v", cfg.UI, want)
	}
	overrides := slices.Clone(src.EnvOverrides)
	slices.Sort(overrides)
	if !slices.Equal(overrides, []string{"ui.enabled", "ui.listen_addr"}) {
		t.Errorf("EnvOverrides = %v, want [ui.enabled ui.listen_addr]", src.EnvOverrides)
	}

	e := loadError(t, config.Options{Path: writeFile(t, "ui:\n  nope: 1\n")})
	want := config.Error{Key: "ui.nope", Source: e.Source, Reason: "unknown key", Line: 2}
	if *e != want {
		t.Errorf("unknown key error = %+v, want %+v", *e, want)
	}

	e = loadError(t, config.Options{Path: writeFile(t, "ui:\n  enabled: maybe\n")})
	if e.Key != "ui.enabled" || e.Reason != "invalid value" {
		t.Errorf("invalid enabled error = %+v", *e)
	}
}

// AC4 and E16: a ui.listen_addr that is not loopback, or equals the proxy's
// listen_addr, is a config error naming ui.listen_addr, from the file and from env.
// Loopback hosts and distinct ports load.
func TestLoad_UIListenAddrMustBeLoopbackAndDistinct(t *testing.T) {
	refused := []struct{ value, reason string }{
		{"0.0.0.0:7199", "not loopback"},
		{"[::]:7199", "not loopback"},
		{"192.168.1.5:7199", "not loopback"},
		{"127.0.0.1:7197", "same as listen_addr"}, // the proxy's default address
		{"[::ffff:192.168.1.5]:7199", "not loopback"},
		{"localhost:7197", "same as listen_addr"}, // localhost resolves to 127.0.0.1 or ::1
	}
	for _, tc := range refused {
		t.Run("refused_"+tc.value, func(t *testing.T) {
			path := writeFile(t, "ui:\n  listen_addr: \""+tc.value+"\"\n")
			e := loadError(t, config.Options{Path: path})
			if e.Key != "ui.listen_addr" || e.Reason != tc.reason {
				t.Errorf("file error = %+v, want key ui.listen_addr, reason %q", *e, tc.reason)
			}
			e = loadError(t, config.Options{LookupEnv: envOf("GATEWAY_UI_LISTEN_ADDR", tc.value)})
			if e.Key != "ui.listen_addr" || e.Reason != tc.reason || e.Source != "GATEWAY_UI_LISTEN_ADDR" {
				t.Errorf("env error = %+v, want key ui.listen_addr, reason %q", *e, tc.reason)
			}
		})
	}

	t.Run("proxy_address_from_file", func(t *testing.T) {
		path := writeFile(t, "listen_addr: 127.0.0.1:7199\nui:\n  listen_addr: 127.0.0.1:7199\n")
		if e := loadError(t, config.Options{Path: path}); e.Key != "ui.listen_addr" || e.Reason != "same as listen_addr" {
			t.Errorf("error = %+v", *e)
		}
	})

	// A different loopback IP on the proxy's port is a different listener, so it loads.
	accepted := []string{"127.0.0.2:7199", "127.0.0.1:7199", "localhost:7199", "[::1]:7199", "[::1]:7197", "127.0.0.2:7197"}
	for _, v := range accepted {
		t.Run("accepted_"+v, func(t *testing.T) {
			path := writeFile(t, "ui:\n  listen_addr: \""+v+"\"\n")
			if _, _, err := config.Load(config.Options{Path: path, Upstreams: specs}); err != nil {
				t.Errorf("file: %v", err)
			}
			if _, _, err := config.Load(config.Options{Upstreams: specs, LookupEnv: envOf("GATEWAY_UI_LISTEN_ADDR", v)}); err != nil {
				t.Errorf("env: %v", err)
			}
		})
	}

	// The loopback and distinct rules apply only when the UI is on.
	if _, _, err := config.Load(config.Options{Upstreams: specs, LookupEnv: envOf(
		"GATEWAY_UI_ENABLED", "false",
		"GATEWAY_UI_LISTEN_ADDR", "0.0.0.0:7199",
	)}); err != nil {
		t.Errorf("disabled UI with a non-loopback address: %v", err)
	}
}
