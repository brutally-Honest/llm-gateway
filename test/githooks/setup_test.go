package githooks

import (
	"strings"
	"testing"
)

const notesRefspec = "+refs/notes/*:refs/notes/*"

// AC22: a second `make setup` leaves git config unchanged.
func TestSetupGit_Idempotent(t *testing.T) {
	w := newWorld(t)

	w.makeReal("setup-git")
	first := w.git("config", "--local", "--list")
	w.makeReal("setup-git")
	second := w.git("config", "--local", "--list")

	if first != second {
		t.Errorf("git config changed on the second run\nfirst:\n%s\nsecond:\n%s", first, second)
	}

	for key, value := range map[string]string{
		"core.hooksPath":   ".githooks",
		"notes.rewriteRef": "refs/notes/commits",
	} {
		if got := strings.TrimSpace(w.git("config", "--local", key)); got != value {
			t.Errorf("%s = %q, want %q", key, got, value)
		}
	}

	// Notes stay local: setup never adds a fetch refspec for them.
	fetch := strings.Split(strings.TrimSpace(w.git("config", "--get-all", "remote.origin.fetch")), "\n")
	n := 0
	for _, refspec := range fetch {
		if refspec == notesRefspec {
			n++
		}
	}
	if n != 0 {
		t.Errorf("notes refspec appears %d times in remote.origin.fetch, want 0: %q", n, fetch)
	}
}
