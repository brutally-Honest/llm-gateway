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

# done_tests — exits non-zero when any check failed
done_tests() {
	[ "$T_FAILS" = 0 ] || exit 1
	exit 0
}
