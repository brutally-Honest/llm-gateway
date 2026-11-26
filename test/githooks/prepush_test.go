package githooks

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// stubMakefile stands in for the real Makefile, so the hook's `make verify` in the
// work repo doesn't run these tests again.
const stubMakefile = "verify:\n\t@exit $${STUB_VERIFY_EXIT:-0}\n"

// newGatedWorld is a world after `make setup`: the hooks and the stub Makefile are
// committed, core.hooksPath points at them, and main is pushed.
func newGatedWorld(t *testing.T) *world {
	t.Helper()
	w := newWorld(t)
	for _, hook := range []string{"commit-msg", "pre-push"} {
		b, err := os.ReadFile(filepath.Join(w.root, ".githooks", hook))
		if err != nil {
			t.Fatal(err)
		}
		w.write(filepath.Join(".githooks", hook), string(b), 0o755)
	}
	w.write("Makefile", stubMakefile, 0o644)
	w.makeReal("setup-git")
	w.git("add", ".")
	w.git("commit", "-q", "-m", "chore(repo): add the gate")
	w.git("push", "-q", "origin", "main")
	return w
}

func (w *world) write(name, content string, mode os.FileMode) {
	w.t.Helper()
	path := filepath.Join(w.dir, name)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		w.t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), mode); err != nil {
		w.t.Fatal(err)
	}
}

// commit commits a change to file.txt. --no-verify lets a malformed message in, as
// `git commit --no-verify` would; the pre-push hook is what must catch it.
func (w *world) commit(msg string) {
	w.t.Helper()
	w.write("file.txt", msg+"\n", 0o644)
	w.git("add", "file.txt")
	w.git("commit", "-q", "--no-verify", "-m", msg)
}

// push runs `git push` with extra environment and returns its output and whether it
// succeeded. It does not fail the test.
func (w *world) push(env []string, args ...string) (string, bool) {
	w.t.Helper()
	cmd := exec.Command("git", append([]string{"push"}, args...)...)
	cmd.Dir = w.dir
	cmd.Env = append(append([]string(nil), w.env...), env...)
	out, err := cmd.CombinedOutput()
	return string(out), err == nil
}

// remoteSHA is the sha of ref on origin, or "" if origin doesn't have it.
func (w *world) remoteSHA(ref string) string {
	w.t.Helper()
	out := strings.Fields(w.git("ls-remote", "origin", ref))
	if len(out) == 0 {
		return ""
	}
	return out[0]
}

var verifyFails = []string{"STUB_VERIFY_EXIT=1"}

// AC20: each of these pushes is rejected, and origin is left as it was.
func TestPrePush_Rejects(t *testing.T) {
	cases := []struct {
		name string
		// arrange prepares the world and returns the push's env and args.
		arrange func(w *world) ([]string, []string)
		want    string // in the push output
	}{
		{
			name: "malformed_subject",
			arrange: func(w *world) ([]string, []string) {
				w.commit("fixed stuff")
				w.commit("feat(repo): add a valid commit on top")
				return nil, []string{"origin", "main"}
			},
			want: "subject does not match",
		},
		{
			name: "verify_fails",
			arrange: func(w *world) ([]string, []string) {
				w.commit("feat(repo): add a change")
				return verifyFails, []string{"origin", "main"}
			},
			want: "make verify failed",
		},
		{
			// cleanEnv strips these; they are set again here, as a pusher's shell
			// might export them.
			name: "verify_fails_despite_env_flags",
			arrange: func(w *world) ([]string, []string) {
				w.commit("feat(repo): add a change")
				env := append([]string{"MAKEFLAGS=i", "GNUMAKEFLAGS=-i", "GOFLAGS=-run=^$"}, verifyFails...)
				return env, []string{"origin", "main"}
			},
			want: "make verify failed",
		},
		{
			name: "dirty_tree",
			arrange: func(w *world) ([]string, []string) {
				w.commit("feat(repo): add a change")
				w.write("file.txt", "uncommitted\n", 0o644)
				return nil, []string{"origin", "main"}
			},
			want: "uncommitted changes",
		},
		{
			name: "sha_not_head",
			arrange: func(w *world) ([]string, []string) {
				w.git("branch", "side")
				w.commit("feat(repo): add a change")
				w.git("checkout", "-q", "side")
				return nil, []string{"origin", "main"}
			},
			want: "is not HEAD",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w := newGatedWorld(t)
			before := w.remoteSHA("refs/heads/main")

			env, args := tc.arrange(w)
			out, ok := w.push(env, args...)

			if ok {
				t.Fatalf("push succeeded, want it rejected\n%s", out)
			}
			if !strings.Contains(out, tc.want) {
				t.Errorf("push output does not contain %q\n%s", tc.want, out)
			}
			if after := w.remoteSHA("refs/heads/main"); after != before {
				t.Errorf("origin main moved from %s to %s", before, after)
			}
		})
	}
}

// Notes are local only (AGENTS.md, Git notes): a push to refs/notes/* is rejected
// before any other check, and so is its deletion.
func TestPrePush_RejectsNotes(t *testing.T) {
	const want = "is local only and is never pushed"

	t.Run("push", func(t *testing.T) {
		w := newGatedWorld(t)
		w.git("notes", "add", "-m", "a note", "HEAD")

		out, ok := w.push(nil, "origin", "refs/notes/commits")
		if ok {
			t.Fatalf("notes push succeeded, want it rejected\n%s", out)
		}
		if !strings.Contains(out, want) {
			t.Errorf("push output does not contain %q\n%s", want, out)
		}
		if sha := w.remoteSHA("refs/notes/commits"); sha != "" {
			t.Errorf("origin has refs/notes/commits at %s, want none", sha)
		}
	})

	t.Run("deletion", func(t *testing.T) {
		w := newGatedWorld(t)
		// Put a notes ref on origin directly; the hook is what is under test, not this setup.
		remote := strings.TrimSpace(w.git("remote", "get-url", "origin"))
		head := strings.TrimSpace(w.git("rev-parse", "HEAD"))
		w.run(w.dir, "git", "--git-dir", remote, "update-ref", "refs/notes/commits", head)

		out, ok := w.push(nil, "origin", ":refs/notes/commits")
		if ok {
			t.Fatalf("notes deletion succeeded, want it rejected\n%s", out)
		}
		if !strings.Contains(out, want) {
			t.Errorf("push output does not contain %q\n%s", want, out)
		}
		if sha := w.remoteSHA("refs/notes/commits"); sha != head {
			t.Errorf("origin refs/notes/commits = %q, want it left at %s", sha, head)
		}
	})
}

// AC21: each of these pushes is allowed. Deletions and tags push while `make verify`
// fails, which shows the hook doesn't check them at all.
func TestPrePush_Allows(t *testing.T) {
	t.Run("branch_deletion", func(t *testing.T) {
		w := newGatedWorld(t)
		w.git("push", "-q", "origin", "main:gone")

		if out, ok := w.push(verifyFails, "origin", "--delete", "gone"); !ok {
			t.Fatalf("deletion rejected\n%s", out)
		}
		if sha := w.remoteSHA("refs/heads/gone"); sha != "" {
			t.Errorf("origin still has gone at %s", sha)
		}
	})

	t.Run("clean_range", func(t *testing.T) {
		w := newGatedWorld(t)

		// A new branch: its range is everything origin doesn't have.
		w.git("checkout", "-q", "-b", "feat/next")
		w.commit("feat(repo): add a change")
		w.commit("fix(repo): correct the change")
		if out, ok := w.push(nil, "origin", "feat/next"); !ok {
			t.Fatalf("new branch rejected\n%s", out)
		}

		// An update: its range is <remote sha>..<local sha>.
		w.commit("docs(repo): describe the change")
		if out, ok := w.push(nil, "origin", "feat/next"); !ok {
			t.Fatalf("update rejected\n%s", out)
		}
		if got, want := w.remoteSHA("refs/heads/feat/next"), strings.TrimSpace(w.git("rev-parse", "HEAD")); got != want {
			t.Errorf("origin feat/next = %s, want %s", got, want)
		}
	})

	t.Run("tag", func(t *testing.T) {
		w := newGatedWorld(t)
		w.git("tag", "v0.0.1")

		if out, ok := w.push(verifyFails, "origin", "v0.0.1"); !ok {
			t.Fatalf("tag push rejected\n%s", out)
		}
		if w.remoteSHA("refs/tags/v0.0.1") == "" {
			t.Error("origin has no refs/tags/v0.0.1")
		}
	})
}
