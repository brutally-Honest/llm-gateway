package startup

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func env(m map[string]string) func(string) (string, bool) {
	return func(k string) (string, bool) { v, ok := m[k]; return v, ok }
}

// Spec 025 AC5, research D7: only the gateway's own job counts as the service.
func TestUnderService(t *testing.T) {
	for _, c := range []struct {
		name string
		env  map[string]string
		want bool
	}{
		{"plain", map[string]string{}, false},
		{"launchd job", map[string]string{"XPC_SERVICE_NAME": "dev.llm-gateway"}, true},
		{"terminal under launchd", map[string]string{"XPC_SERVICE_NAME": "0"}, false},
		{"systemd unit", map[string]string{"INVOCATION_ID": "a", "SYSTEMD_EXEC_PID": "42"}, true},
		{"inherited from a unit", map[string]string{"INVOCATION_ID": "a", "SYSTEMD_EXEC_PID": "41"}, false},
		{"exec pid alone", map[string]string{"SYSTEMD_EXEC_PID": "42"}, false},
		{"invocation id alone", map[string]string{"INVOCATION_ID": "a"}, false},
	} {
		if got := UnderService(env(c.env), 42); got != c.want {
			t.Errorf("%s: UnderService = %v, want %v", c.name, got, c.want)
		}
	}
	if UnderService(nil, 42) {
		t.Error("nil lookup counted as a service")
	}
}

func TestPath(t *testing.T) {
	if p, ok := Path(env(map[string]string{"XDG_STATE_HOME": "/s", "HOME": "/h"})); !ok || p != "/s/llm-gateway/startup-error.json" {
		t.Errorf("xdg: %q %v", p, ok)
	}
	if p, ok := Path(env(map[string]string{"XDG_STATE_HOME": "rel", "HOME": "/h"})); !ok || p != "/h/.local/state/llm-gateway/startup-error.json" {
		t.Errorf("home: %q %v", p, ok)
	}
	if _, ok := Path(env(map[string]string{})); ok {
		t.Error("a path with no HOME")
	}
}

func TestRecordRoundTripAndRestarting(t *testing.T) {
	path := filepath.Join(t.TempDir(), "a", "startup-error.json")
	now := time.Date(2026, 11, 21, 8, 0, 0, 0, time.UTC)
	r := Record{Error: "invalid config (key=listen_addr)", Key: "k", LoggedAt: now, FailedAt: now}
	if err := Write(path, r); err != nil {
		t.Fatal(err)
	}
	got, err := Read(path)
	if err != nil || got.Error != r.Error || !got.FailedAt.Equal(now) {
		t.Fatalf("read %+v %v", got, err)
	}
	if st, _ := os.Stat(path); st.Mode().Perm() != 0o600 {
		t.Errorf("mode %v", st.Mode().Perm())
	}
	if !Restarting(got, now.Add(Wait+time.Second)) || Restarting(got, now.Add(time.Minute)) || Restarting(Record{}, now) {
		t.Error("Restarting window wrong")
	}
	if err := Clear(path); err != nil {
		t.Fatal(err)
	}
	if err := Clear(path); err != nil {
		t.Fatalf("a second clear: %v", err)
	}
	if _, err := Read(path); !os.IsNotExist(err) {
		t.Fatalf("read after clear: %v", err)
	}
}
