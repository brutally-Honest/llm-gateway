package conventions

import (
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"go.yaml.in/yaml/v3"

	"github.com/brutally-honest/llm-gateway/internal/config"
)

// composeFile is the subset of docker-compose.yml these tests read.
type composeFile struct {
	Services map[string]struct {
		StopGracePeriod string            `yaml:"stop_grace_period"`
		Environment     map[string]string `yaml:"environment"`
		Volumes         []composeMount    `yaml:"volumes"`
	} `yaml:"services"`
	Volumes map[string]any `yaml:"volumes"`
}

// composeMount is one entry of a service's volumes list, in either the short
// ("source:target[:mode]") or the long (type, source, target) syntax.
type composeMount struct {
	Type   string `yaml:"type"`
	Source string `yaml:"source"`
	Target string `yaml:"target"`
}

// UnmarshalYAML accepts both compose mount syntaxes.
func (m *composeMount) UnmarshalYAML(n *yaml.Node) error {
	if n.Kind == yaml.ScalarNode {
		parts := strings.Split(n.Value, ":")
		if len(parts) < 2 {
			// A bare target is an anonymous volume: no source.
			m.Type, m.Target = "volume", parts[0]
			return nil
		}
		m.Source, m.Target = parts[0], parts[1]
		m.Type = "volume"
		if strings.HasPrefix(m.Source, "/") || strings.HasPrefix(m.Source, ".") || strings.HasPrefix(m.Source, "~") {
			m.Type = "bind"
		}
		return nil
	}
	type plain composeMount
	if err := n.Decode((*plain)(m)); err != nil {
		return fmt.Errorf("volume entry: %w", err)
	}
	return nil
}

// readCompose parses the repository's docker-compose.yml.
func readCompose(t *testing.T) composeFile {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "..", "docker-compose.yml"))
	if err != nil {
		t.Fatal(err)
	}
	var compose composeFile
	if err := yaml.Unmarshal(raw, &compose); err != nil {
		t.Fatalf("parse docker-compose.yml: %v", err)
	}
	return compose
}

// TestCompose_StopGracePeriodMatchesShutdownTimeout fails when the gateway service's
// stop_grace_period is missing or shorter than the default shutdown_timeout, so Docker
// never kills the process while it still drains in-flight streams (spec 001, AC43).
func TestCompose_StopGracePeriodMatchesShutdownTimeout(t *testing.T) {
	compose := readCompose(t)
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

// TestCompose_CaptureDirOnVolume fails unless the gateway service sets
// GATEWAY_CAPTURE_DIR to an absolute path that is the mount target of a named volume
// declared at the top level, so captures outlive the container (spec 002, Config).
func TestCompose_CaptureDirOnVolume(t *testing.T) {
	compose := readCompose(t)
	gateway, ok := compose.Services["gateway"]
	if !ok {
		t.Fatal("docker-compose.yml has no gateway service")
	}
	dir, ok := gateway.Environment["GATEWAY_CAPTURE_DIR"]
	if !ok || dir == "" {
		t.Fatal("gateway service does not set GATEWAY_CAPTURE_DIR")
	}
	if !path.IsAbs(dir) {
		t.Fatalf("GATEWAY_CAPTURE_DIR = %q, want an absolute path", dir)
	}
	for _, m := range gateway.Volumes {
		if path.Clean(m.Target) != path.Clean(dir) {
			continue
		}
		if m.Type != "volume" || m.Source == "" {
			t.Fatalf("mount at %s is %s with source %q, want a named volume", dir, m.Type, m.Source)
		}
		if _, declared := compose.Volumes[m.Source]; !declared {
			t.Fatalf("volume %q mounted at %s is not declared under top-level volumes", m.Source, dir)
		}
		return
	}
	t.Fatalf("no volume is mounted at GATEWAY_CAPTURE_DIR %s", dir)
}
