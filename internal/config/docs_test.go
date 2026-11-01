package config_test

import (
	"testing"

	"github.com/brutally-honest/llm-gateway/internal/config"
)

// AC53: config.example.yaml is a working starting point. It loads through config.Load with
// no error, and its ui block is the documented default.
func TestDocs_ConfigExampleParses(t *testing.T) {
	const example = "../../config.example.yaml"
	cfg, src, err := config.Load(config.Options{Path: example, Upstreams: specs, LookupEnv: envOf()})
	if err != nil {
		t.Fatalf("config.example.yaml does not load: %v", err)
	}
	if src.File != example {
		t.Errorf("Source.File = %q, want %q", src.File, example)
	}
	if want := config.Defaults().UI; cfg.UI != want {
		t.Errorf("example UI = %+v, want the documented default %+v", cfg.UI, want)
	}
}
