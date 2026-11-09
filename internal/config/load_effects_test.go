package config_test

import (
	"reflect"
	"testing"
	"time"

	"github.com/brutally-honest/llm-gateway/internal/config"
)

// AC4, AC12: the effects block's defaults, with .env left out of the ignore list on
// purpose; the file's lists and an env override apply on top; a proxy address that is
// not loopback, or that another listener uses, is refused.
func TestLoad_EffectsDefaults(t *testing.T) {
	want := config.Effects{
		Enabled: true,
		Files: config.EffectFiles{
			Enabled:           true,
			ExtraDirs:         []string{},
			Ignore:            []string{".git", "node_modules", "dist", "build", "target", ".venv", "venv", "__pycache__", ".next", ".cache"},
			MaxCopyBytes:      262144,
			GitTimeout:        10 * time.Second,
			SnapshotRetention: 168 * time.Hour,
		},
		Network: config.EffectNetwork{
			Enabled:        true,
			HTTPProxyAddr:  "127.0.0.1:7198",
			SocksProxyAddr: "127.0.0.1:7195",
			Sandbox:        false,
			ActiveWindow:   10 * time.Minute,
		},
	}
	cfg, _, err := config.Load(config.Options{Upstreams: specs, LookupEnv: envOf()})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(cfg.Effects, want) {
		t.Errorf("Effects = %+v, want %+v", cfg.Effects, want)
	}

	path := writeFile(t, "effects:\n  files:\n    extra_dirs: [/srv/a, /srv/b]\n    ignore: [.git, .env]\n  network:\n    socks_proxy_addr: \"\"\n")
	cfg, src, err := config.Load(config.Options{Path: path, Upstreams: specs, LookupEnv: envOf("GATEWAY_EFFECTS_NETWORK_SANDBOX", "true")})
	if err != nil {
		t.Fatal(err)
	}
	if got := cfg.Effects.Files.ExtraDirs; !reflect.DeepEqual(got, []string{"/srv/a", "/srv/b"}) {
		t.Errorf("ExtraDirs = %v", got)
	}
	if got := cfg.Effects.Files.Ignore; !reflect.DeepEqual(got, []string{".git", ".env"}) {
		t.Errorf("Ignore = %v; adding .env must be allowed", got)
	}
	if cfg.Effects.Network.SocksProxyAddr != "" || !cfg.Effects.Network.Sandbox {
		t.Errorf("Network = %+v, want SOCKS5 off and sandbox on", cfg.Effects.Network)
	}
	if !reflect.DeepEqual(src.EnvOverrides, []string{"effects.network.sandbox"}) {
		t.Errorf("EnvOverrides = %v", src.EnvOverrides)
	}

	for name, c := range map[string]struct{ env, key, reason string }{
		"relative extra dir": {"GATEWAY_EFFECTS_FILES_EXTRA_DIRS", "effects.files.extra_dirs", "invalid value"},
		"not loopback":       {"GATEWAY_EFFECTS_NETWORK_HTTP_PROXY_ADDR", "effects.network.http_proxy_addr", "not loopback"},
		"same as listen":     {"GATEWAY_EFFECTS_NETWORK_SOCKS_PROXY_ADDR", "effects.network.socks_proxy_addr", "same as another listener"},
	} {
		v := map[string]string{
			"relative extra dir": "rel/dir",
			"not loopback":       "10.0.0.1:7198",
			"same as listen":     "127.0.0.1:7197",
		}[name]
		e := loadError(t, config.Options{LookupEnv: envOf(c.env, v)})
		if e.Key != c.key || e.Reason != c.reason || e.Source != c.env {
			t.Errorf("%s: error = %+v", name, *e)
		}
	}
}
