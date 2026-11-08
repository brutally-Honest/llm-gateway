package main

import (
	"encoding/json"
	"maps"
	"os"
	"strings"
	"testing"

	"github.com/brutally-honest/llm-gateway/internal/clients/claudecode"
)

// networkKey turns the network sensor on or off for hooks print (spec 014).
const networkKey = "GATEWAY_EFFECTS_NETWORK_ENABLED"

// hooksPrint runs `gateway hooks print`. The 010 tests read the hooks-only document,
// which spec 014 prints with network effects off, so the network sensor is off unless
// env sets it.
func hooksPrint(t *testing.T, env map[string]string, args ...string) (int, string) {
	t.Helper()
	var out syncBuffer
	code := run(t.Context(), deps{
		args: append([]string{"hooks", "print"}, args...),
		lookupEnv: func(n string) (string, bool) {
			v, ok := env[n]
			if !ok && n == networkKey {
				return "false", true
			}
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

// AC12: with network effects off, hooks print is byte for byte the hooks document of
// 010; with them on it adds the env block and, unless sandbox is false, a sandbox block
// pointing at the proxy ports, without socksProxyPort when SOCKS5 is off.
func TestHooksPrint_SettingsDoc(t *testing.T) {
	_, off := hooksPrint(t, nil)
	if want := string(claudecode.HookSnippet("127.0.0.1:7197")); off != want {
		t.Errorf("network off: output differs from the 010 hooks document\n%s", off)
	}

	type doc struct {
		Hooks   map[string]any    `json:"hooks"`
		Env     map[string]string `json:"env"`
		Sandbox *struct {
			Enabled bool           `json:"enabled"`
			Network map[string]int `json:"network"`
		} `json:"sandbox"`
	}
	parse := func(env map[string]string) doc {
		t.Helper()
		env[networkKey] = "true"
		code, out := hooksPrint(t, env)
		if code != 0 {
			t.Fatalf("exit %d", code)
		}
		var d doc
		if err := json.Unmarshal([]byte(out), &d); err != nil {
			t.Fatalf("not valid JSON: %v\n%s", err, out)
		}
		return d
	}
	on := parse(map[string]string{})
	wantEnv := map[string]string{
		"ANTHROPIC_BASE_URL": "http://127.0.0.1:7197/anthropic",
		"HTTPS_PROXY":        "http://127.0.0.1:7198",
		"HTTP_PROXY":         "http://127.0.0.1:7198",
		"NO_PROXY":           "127.0.0.1,localhost",
	}
	if len(on.Hooks) == 0 || !maps.Equal(on.Env, wantEnv) {
		t.Errorf("network on: hooks %d, env %v; want the hooks and %v", len(on.Hooks), on.Env, wantEnv)
	}
	if on.Sandbox == nil || !on.Sandbox.Enabled || !maps.Equal(on.Sandbox.Network, map[string]int{"httpProxyPort": 7198, "socksProxyPort": 7195}) {
		t.Errorf("network on: sandbox = %+v", on.Sandbox)
	}

	if d := parse(map[string]string{"GATEWAY_EFFECTS_NETWORK_SANDBOX": "false"}); d.Sandbox != nil || len(d.Env) != 4 {
		t.Errorf("sandbox off: sandbox %+v, env %v; want no sandbox block and the env block", d.Sandbox, d.Env)
	}

	dir := t.TempDir()
	path := dir + "/config.yaml"
	if err := os.WriteFile(path, []byte("effects:\n  network:\n    socks_proxy_addr: \"\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	env := map[string]string{networkKey: "true"}
	code, out := hooksPrint(t, env, "-config", path)
	var d doc
	if code != 0 || json.Unmarshal([]byte(out), &d) != nil {
		t.Fatalf("SOCKS5 off: exit %d\n%s", code, out)
	}
	if d.Sandbox == nil || !maps.Equal(d.Sandbox.Network, map[string]int{"httpProxyPort": 7198}) {
		t.Errorf("SOCKS5 off: sandbox = %+v, want httpProxyPort only", d.Sandbox)
	}
}
