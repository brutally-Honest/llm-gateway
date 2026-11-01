# helpers.sh — shared setup and assertions for the kit's shell tests. Sourced, never run.
#
# Every test works in a throwaway git repo under a temp dir, with a copy of the kit, so
# the scripts resolve REPO, .claude/ and .agent-loop/ inside it and never touch the real
# repo's state.

KIT_SRC=$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)   # the real .claude/

T_FAILS=0
T_NAME=""

fail() { printf '  FAIL %s: %s\n' "$T_NAME" "$*" >&2; T_FAILS=$((T_FAILS + 1)); }
pass() { printf '  ok   %s\n' "$T_NAME"; }

# t <name> — starts a named check; end it with ok_if or explicit fail/pass
t() { T_NAME=$1; }
assert_eq() { # want got [what]
	if [ "$1" = "$2" ]; then pass; else fail "${3:-value}: want '$1', got '$2'"; fi
}
assert_contains() { # haystack needle [what]
	case $1 in *"$2"*) pass ;; *) fail "${3:-output} lacks '$2': $1" ;; esac
}
assert_not_contains() { # haystack needle [what]
	case $1 in *"$2"*) fail "${3:-output} contains '$2': $1" ;; *) pass ;; esac
}

# mk_repo — creates a temp git repo holding a copy of the kit, cds into it, and sets
# TMP_REPO. Removed on exit.
mk_repo() {
	TMP_REPO=$(mktemp -d "${TMPDIR:-/tmp}/al-test.XXXXXX") || exit 1
	trap 'rm -rf "$TMP_REPO"' EXIT
	cd "$TMP_REPO" || exit 1
	git init -q -b main
	git config user.name test
	git config user.email test@example.invalid
	git config commit.gpgsign false
	mkdir -p .claude
	cp -R "$KIT_SRC/scripts" "$KIT_SRC/hooks" "$KIT_SRC/templates" "$KIT_SRC/agents" .claude/
	rm -rf .claude/scripts/tests
	cp "$KIT_SRC/loop.conf" .claude/loop.conf
	printf '.agent-loop/\n' > .gitignore
	git add .gitignore .claude
	git commit -q -m "chore(repo): seed test repo"
}

# mk_e2e [loop.conf lines...] — clones the e2e fixture (feature 001-e2e, approved, one task
# T001) into a temp dir, swaps in the kit under test, appends the given loop.conf lines
# (VERIFY_CMD="true" and an empty TASK_CHECK_CMD first, so checks pass unless a line says
# otherwise), cds into it and
# sets TMP_REPO and L (the loop script). Removed on exit.
mk_e2e() {
	local bundle="$KIT_SRC/scripts/tests/fixtures/e2e/repo.bundle" line
	[ -f "$bundle" ] || { echo "  FAIL missing $bundle — build it with fixtures/e2e/make.sh" >&2; exit 1; }
	TMP_REPO=$(mktemp -d "${TMPDIR:-/tmp}/al-e2e.XXXXXX") || exit 1
	E2E_TMPS="${E2E_TMPS:-} $TMP_REPO"   # a test may build several; all go on exit
	# shellcheck disable=SC2064
	trap "rm -rf $E2E_TMPS" EXIT
	git clone -q -b feat/001-e2e "$bundle" "$TMP_REPO"
	cd "$TMP_REPO" || exit 1
	git config user.name test
	git config user.email test@example.invalid
	git config commit.gpgsign false
	git branch -q main origin/main
	git remote remove origin
	rm -rf .claude/scripts .claude/hooks .claude/templates .claude/agents
	cp -R "$KIT_SRC/scripts" "$KIT_SRC/hooks" "$KIT_SRC/templates" "$KIT_SRC/agents" .claude/
	rm -rf .claude/scripts/tests
	cp "$KIT_SRC/loop.conf" .claude/loop.conf
	printf '\nVERIFY_CMD="true"\nTASK_CHECK_CMD=""\n' >> .claude/loop.conf
	for line in "$@"; do printf '%s\n' "$line" >> .claude/loop.conf; done
	git add .claude && git commit -q -m "chore(repo): use the kit under test"
	L=.claude/scripts/loop.sh
}

# task_commit <message subject> <path=content>... — writes the files and commits them as
# T001 of 001-e2e (with both trailers)
task_commit() {
	local subj=$1 kv p; shift
	for kv in "$@"; do p=${kv%%=*}; mkdir -p "$(dirname "$p")"; printf '%s\n' "${kv#*=}" > "$p"; git add -- "$p"; done
	git commit -q -m "$subj" -m "Task: T001
Feature: 001-e2e"
}

last() { tail -1; }

# done_tests — exits non-zero when any check failed
done_tests() {
	[ "$T_FAILS" = 0 ] || exit 1
	exit 0
}
