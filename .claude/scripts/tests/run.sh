#!/usr/bin/env bash
# run.sh — runs the agent-loop kit's shell tests: every test_*.sh in this folder, each in
# its own process. Exit 0 = all green.
#
# Usage: .claude/scripts/tests/run.sh [test_name.sh ...]
#
# Each test builds a throwaway git repo (helpers.sh mk_repo), so the real repo's
# .agent-loop/ is never read or written; this runner also checks that it didn't change.
set -u
HERE=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
REAL_STATE=$(cd "$HERE/../../.." && pwd)/.agent-loop

snapshot() { # a listing of the real .agent-loop/ with each file's checksum
	[ -d "$REAL_STATE" ] || { echo "absent"; return; }
	find "$REAL_STATE" -type f -exec cksum {} + 2>/dev/null | sort
}

before=$(snapshot)
if [ $# -gt 0 ]; then tests=("$@"); else tests=("$HERE"/test_*.sh); fi
failed=0
for f in "${tests[@]}"; do
	case $f in /*) ;; *) f="$HERE/$f" ;; esac
	printf '%s\n' "${f##*/}"
	bash "$f" || { failed=$((failed + 1)); printf '  -> %s FAILED\n' "${f##*/}"; }
done
if [ "$(snapshot)" != "$before" ]; then
	echo "FAIL: the real .agent-loop/ changed during the tests" >&2
	failed=$((failed + 1))
fi
if [ "$failed" = 0 ]; then echo "all kit tests passed"; else echo "$failed failed"; exit 1; fi
