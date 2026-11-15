package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"
)

// deployDir is the observability stack, from this package's directory.
var deployDir = filepath.Join("..", "..", "deploy", "observability")

// Spec 021 AC13: every metric the dashboard queries is one the gateway exposes, so a
// renamed metric fails here and not as an empty panel; and every panel has a query.
func TestDashboard_QueriesOnlyExposedMetrics(t *testing.T) {
	b, err := os.ReadFile(filepath.Join(deployDir, "grafana", "dashboards", "llm-gateway.json"))
	if err != nil {
		t.Fatal(err)
	}
	var dash struct {
		UID    string `json:"uid"`
		Panels []struct {
			Type, Title string
			Targets     []struct {
				Expr       string `json:"expr"`
				Datasource struct {
					UID string `json:"uid"`
				} `json:"datasource"`
			} `json:"targets"`
		} `json:"panels"`
	}
	if err := json.Unmarshal(b, &dash); err != nil {
		t.Fatalf("dashboard JSON: %v", err)
	}
	// The metrics a capturing gateway with the UI and proxy on exposes, after a stop so
	// the last-shutdown gauges are there too.
	dir := t.TempDir()
	up := jsonUpstream(t)
	g, _ := metricsGateway(t, dir, up)
	g.stop()
	_, url := metricsGateway(t, dir, up)
	exposed := scrape(t, url+"/metrics")

	name := regexp.MustCompile(`\b(gateway_[a-z_]+|process_[a-z_]+|go_[a-z_]+)\b`)
	groups := 0
	for _, p := range dash.Panels {
		if p.Type == "row" {
			groups++
			continue
		}
		if len(p.Targets) == 0 {
			t.Errorf("panel %q has no query", p.Title)
		}
		for _, tg := range p.Targets {
			if tg.Datasource.UID != "prometheus" {
				t.Errorf("panel %q queries datasource %q, want the provisioned prometheus", p.Title, tg.Datasource.UID)
			}
			for _, m := range name.FindAllString(tg.Expr, -1) {
				base := m
				for _, suf := range []string{"_bucket", "_count", "_sum"} {
					base = strings.TrimSuffix(base, suf)
				}
				if _, ok := exposed[base]; !ok {
					t.Errorf("panel %q queries %s, which the gateway does not expose", p.Title, m)
				}
			}
		}
	}
	if groups < 6 {
		t.Errorf("dashboard has %d rows, want one per metric group (6)", groups)
	}
}

// Spec 021 AC13: every port the stack publishes is on 127.0.0.1, and on Linux the
// services sharing the host's network bind loopback themselves.
func TestObservabilityStack_LoopbackOnly(t *testing.T) {
	type service struct {
		Ports       []string          `yaml:"ports"`
		NetworkMode string            `yaml:"network_mode"`
		Command     []string          `yaml:"command"`
		Environment map[string]string `yaml:"environment"`
	}
	read := func(name string) map[string]service {
		b, err := os.ReadFile(filepath.Join(deployDir, name))
		if err != nil {
			t.Fatal(err)
		}
		var f struct {
			Services map[string]service `yaml:"services"`
		}
		if err := yaml.Unmarshal(b, &f); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		return f.Services
	}
	base := read("compose.yml")
	for _, want := range []string{"prometheus", "grafana", "jaeger"} {
		if _, ok := base[want]; !ok {
			t.Errorf("compose.yml has no %s", want)
		}
	}
	for name, s := range base {
		if s.NetworkMode == "host" {
			t.Errorf("compose.yml: %s on the host network; only the Linux override may do that", name)
		}
		for _, p := range s.Ports {
			if !strings.HasPrefix(p, "127.0.0.1:") {
				t.Errorf("compose.yml: %s publishes %q, want 127.0.0.1 only", name, p)
			}
		}
	}
	for name, s := range read("compose.linux.yml") {
		if s.NetworkMode != "host" {
			continue
		}
		loopback := s.Environment["GF_SERVER_HTTP_ADDR"] == "127.0.0.1" ||
			slices.Contains(s.Command, "--web.listen-address=127.0.0.1:9090")
		if !loopback {
			t.Errorf("compose.linux.yml: %s is on the host network without binding 127.0.0.1", name)
		}
	}
}
