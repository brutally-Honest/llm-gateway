package config_test

import (
	"slices"
	"testing"

	"github.com/brutally-honest/llm-gateway/internal/config"
)

// hooksKeys are the hooks.* keys, with the env var of each.
var hooksKeys = []struct{ key, env string }{
	{"hooks.queue_size", "GATEWAY_HOOKS_QUEUE_SIZE"},
	{"hooks.workers", "GATEWAY_HOOKS_WORKERS"},
	{"hooks.memory_limit", "GATEWAY_HOOKS_MEMORY_LIMIT"},
}

// AC6 (E13): the hooks block has its own defaults, separate from capture's: a queue of
// 256, one worker and 32 MiB of memory.
func TestLoad_HooksDefaults(t *testing.T) {
	cfg, _, err := config.Load(config.Options{Upstreams: specs, LookupEnv: envOf()})
	if err != nil {
		t.Fatal(err)
	}
	want := config.Hooks{QueueSize: 256, Workers: 1, MemoryLimit: 32 << 20}
	if cfg.Hooks != want {
		t.Errorf("Hooks = %+v, want %+v", cfg.Hooks, want)
	}
	if config.Defaults().Hooks != want {
		t.Errorf("Defaults().Hooks = %+v, want %+v", config.Defaults().Hooks, want)
	}
}

// AC6 (E13): each hooks.* key applies from the file, and a GATEWAY_HOOKS_* var beats the
// file and is named in env_overrides.
func TestLoad_HooksFromFileAndEnv(t *testing.T) {
	path := writeFile(t, "hooks:\n  queue_size: 64\n  workers: 3\n  memory_limit: 1048576\n")
	cfg, src, err := config.Load(config.Options{Path: path, Upstreams: specs, LookupEnv: envOf()})
	if err != nil {
		t.Fatal(err)
	}
	if want := (config.Hooks{QueueSize: 64, Workers: 3, MemoryLimit: 1 << 20}); cfg.Hooks != want {
		t.Errorf("file Hooks = %+v, want %+v", cfg.Hooks, want)
	}
	if len(src.EnvOverrides) != 0 {
		t.Errorf("EnvOverrides = %v, want none", src.EnvOverrides)
	}

	cfg, src, err = config.Load(config.Options{Path: path, Upstreams: specs, LookupEnv: envOf(
		"GATEWAY_HOOKS_QUEUE_SIZE", "7",
		"GATEWAY_HOOKS_WORKERS", "8",
		"GATEWAY_HOOKS_MEMORY_LIMIT", "9",
	)})
	if err != nil {
		t.Fatal(err)
	}
	if want := (config.Hooks{QueueSize: 7, Workers: 8, MemoryLimit: 9}); cfg.Hooks != want {
		t.Errorf("env Hooks = %+v, want %+v", cfg.Hooks, want)
	}
	overrides := slices.Clone(src.EnvOverrides)
	slices.Sort(overrides)
	want := []string{"hooks.memory_limit", "hooks.queue_size", "hooks.workers"}
	if !slices.Equal(overrides, want) {
		t.Errorf("EnvOverrides = %v, want %v", src.EnvOverrides, want)
	}

	e := loadError(t, config.Options{Path: writeFile(t, "hooks:\n  nope: 1\n")})
	if e.Key != "hooks.nope" || e.Reason != "unknown key" || e.Line != 2 {
		t.Errorf("unknown key error = %+v", *e)
	}
}

// AC6 (E13): zero, negative and non-numeric values are rejected, from the file and from
// env, naming the key and never the value.
func TestLoad_HooksRejectsNonPositive(t *testing.T) {
	for _, k := range hooksKeys {
		field := k.key[len("hooks."):]
		for name, v := range map[string]string{"zero": "0", "negative": "-1", "not_a_number": "12abc"} {
			t.Run(k.key+"/"+name, func(t *testing.T) {
				path := writeFile(t, "hooks:\n  "+field+": \""+v+"\"\n")
				want := config.Error{Key: k.key, Source: path, Reason: "invalid value", Line: 2}
				if e := loadError(t, config.Options{Path: path}); *e != want {
					t.Errorf("file %s=%q: error = %+v, want %+v", field, v, *e, want)
				}
				want = config.Error{Key: k.key, Source: k.env, Reason: "invalid value"}
				if e := loadError(t, config.Options{LookupEnv: envOf(k.env, v)}); *e != want {
					t.Errorf("env %s=%q: error = %+v, want %+v", field, v, *e, want)
				}
			})
		}
	}
}
