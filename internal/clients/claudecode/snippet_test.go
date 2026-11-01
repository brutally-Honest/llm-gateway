package claudecode

import (
	"encoding/json"
	"testing"
)

func TestHookSnippet_NamesEveryPhase(t *testing.T) {
	var s struct {
		Hooks map[string]json.RawMessage `json:"hooks"`
	}
	if err := json.Unmarshal(HookSnippet("127.0.0.1:1"), &s); err != nil {
		t.Fatal(err)
	}
	if len(s.Hooks) != len(phases) {
		t.Errorf("got %d events, want %d", len(s.Hooks), len(phases))
	}
	for name := range phases {
		if _, ok := s.Hooks[name]; !ok {
			t.Errorf("event %s missing", name)
		}
	}
}
