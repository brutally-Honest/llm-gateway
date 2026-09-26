package conventions

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"go.yaml.in/yaml/v3"

	"github.com/brutally-honest/llm-gateway/internal/config"
)

// composeFile is the subset of docker-compose.yml this test reads.
type composeFile struct {
	Services map[string]struct {
		StopGracePeriod string `yaml:"stop_grace_period"`
	} `yaml:"services"`
}

// TestCompose_StopGracePeriodMatchesShutdownTimeout fails when the gateway service's
// stop_grace_period is missing or shorter than the default shutdown_timeout, so Docker
// never kills the process while it still drains in-flight streams (spec 001, AC43).
func TestCompose_StopGracePeriodMatchesShutdownTimeout(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "docker-compose.yml"))
	if err != nil {
		t.Fatal(err)
	}
	var compose composeFile
	if err := yaml.Unmarshal(raw, &compose); err != nil {
		t.Fatalf("parse docker-compose.yml: %v", err)
	}
	gateway, ok := compose.Services["gateway"]
	if !ok {
		t.Fatal("docker-compose.yml has no gateway service")
	}
	if gateway.StopGracePeriod == "" {
		t.Fatal("gateway service has no stop_grace_period")
	}
	grace, err := time.ParseDuration(gateway.StopGracePeriod)
	if err != nil {
		t.Fatalf("stop_grace_period %q: %v", gateway.StopGracePeriod, err)
	}
	if want := config.Defaults().ShutdownTimeout; grace < want {
		t.Errorf("stop_grace_period = %v, want at least the default shutdown_timeout %v", grace, want)
	}
}
