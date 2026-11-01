package main

import (
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/brutally-honest/llm-gateway/internal/clients/claudecode"
)

func hooksPrint(t *testing.T, env map[string]string, args ...string) (int, string) {
	t.Helper()
	var out syncBuffer
	code := run(t.Context(), deps{
		args: append([]string{"hooks", "print"}, args...),
		lookupEnv: func(n string) (string, bool) {
			v, ok := env[n]
			return v, ok
		},
		stdout: &out,
		listen: nil, // hooks print never binds
	})
	return code, out.String()
}

type snippet struct {
	Hooks map[string][]struct {
		Hooks []map[string]any `json:"hooks"`
	} `json:"hooks"`
}

func parseSnippet(t *testing.T, out string) snippet {
	t.Helper()
	var s snippet
	if err := json.Unmarshal([]byte(out), &s); err != nil {
		t.Fatalf("not valid JSON: %v\n%s", err, out)
	}
	return s
}

func TestHooksPrint_SnippetCoversEveryEvent(t *testing.T) {
	code, out := hooksPrint(t, nil)
	if code != 0 {
		t.Fatalf("exit %d", code)
	}
	s := parseSnippet(t, out)
	if want := claudecode.HookSnippet("127.0.0.1:7197"); out != string(want) {
		t.Errorf("output differs from the profile snippet for the default addr\n%s", out)
	}
	if len(s.Hooks) == 0 {
		t.Fatal("no events")
	}
	for ev, g := range s.Hooks {
		if len(g) != 1 || len(g[0].Hooks) != 1 || g[0].Hooks[0]["type"] != "http" {
			t.Errorf("event %s malformed: %v", ev, g)
		}
	}
}

func TestHooksPrint_UsesListenAddr(t *testing.T) {
	_, out := hooksPrint(t, map[string]string{"GATEWAY_LISTEN_ADDR": "127.0.0.1:4242"})
	want := "http://127.0.0.1:4242/_gateway/hooks/" + claudecode.Name
	hooks := parseSnippet(t, out).Hooks
	if len(hooks) == 0 {
		t.Fatal("no events")
	}
	for ev, g := range hooks {
		if g[0].Hooks[0]["url"] != want {
			t.Errorf("%s url = %v, want %s", ev, g[0].Hooks[0]["url"], want)
		}
	}
}

func TestHooksPrint_SnippetIsValidJSONNoDecision(t *testing.T) {
	_, out := hooksPrint(t, nil)
	var generic map[string]any
	if err := json.Unmarshal([]byte(out), &generic); err != nil {
		t.Fatal(err)
	}
	if len(generic) != 1 {
		t.Errorf("top-level keys = %v, want only hooks", generic)
	}
	for _, bad := range []string{"decision", "permissionDecision", "\"continue\"", "\"command\""} {
		if strings.Contains(out, bad) {
			t.Errorf("snippet contains %s", bad)
		}
	}
	for ev, g := range parseSnippet(t, out).Hooks {
		if g[0].Hooks[0]["async"] != true {
			t.Errorf("%s hook is not async", ev)
		}
	}
}

func TestHooksPrint_WritesNoFileExitsZero(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	code, out := hooksPrint(t, map[string]string{"HOME": dir})
	if code != 0 || out == "" {
		t.Fatalf("exit %d, out %q", code, out)
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 0 {
		t.Errorf("files written: %v", entries)
	}
}
