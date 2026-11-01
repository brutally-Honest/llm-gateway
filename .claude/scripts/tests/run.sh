#!/usr/bin/env bash
# run.sh — runs the agent-loop kit's shell tests: every test_*.sh in this folder, each in
# its own process. Exit 0 = all green.
#
# Usage: .claude/scripts/tests/run.sh [test_name.sh ...]
#
# Each test builds a throwaway git repo (helpers.sh mk_repo), so the real repo's
# .agent-loop/ is never read or written; this runner also checks that it didn't change.
# Every run also lints every .sh under .claude/ with "shellcheck -S warning" (it must be
# installed; SHELLCHECK names another binary): any finding fails the run.
set -u
HERE=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
KIT=$(cd "$HERE/../.." && pwd)   # .claude/
REAL_STATE=$(cd "$HERE/../../.." && pwd)/.agent-loop
SC=${SHELLCHECK:-shellcheck}

command -v "$SC" >/dev/null 2>&1 || {
	echo "FAIL: shellcheck is not installed, and the kit's tests lint every script with it." >&2
	echo "  do this: install shellcheck (apt install shellcheck, or brew install shellcheck), then run again" >&2
	exit 1
}
# the kit's scripts; session worktrees and install backups are other copies, not the kit
scripts=()
while IFS= read -r f; do scripts+=("$f"); done <<EOF
$(find "$KIT" -type f -name '*.sh' -not -path "$KIT/worktrees/*" -not -path "$KIT/agent-loop-backup-*" | sort)
EOF

snapshot() { # a listing of the real .agent-loop/ with each file's checksum
	[ -d "$REAL_STATE" ] || { echo "absent"; return; }
	find "$REAL_STATE" -type f -exec cksum {} + 2>/dev/null | sort
}

before=$(snapshot)
if [ $# -gt 0 ]; then tests=("$@"); else tests=("$HERE"/test_*.sh); fi
failed=0
printf 'shellcheck -S warning (%s scripts under .claude/)\n' "${#scripts[@]}"
if "$SC" -S warning "${scripts[@]}"; then echo "  ok   no findings"
else failed=$((failed + 1)); echo "  -> shellcheck FAILED"; fi
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
