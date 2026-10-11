#!/usr/bin/env bash
# add-fix rollback: when the tasks.md commit fails, add-fix leaves tasks.md and the tree as
# they were and exits non-zero; when it succeeds, the commit has a fitting subject and the
# full bug text as its body.
#
# add-fix refuses an unapproved tasks.md, and approvals are made by approve.sh only, so
# this test loads cmd_add_fix from loop.sh with the approval lookups stubbed to "approved".
set -u
. "$(dirname "${BASH_SOURCE[0]}")/helpers.sh"
mk_repo

F=005-session-token-usage
git switch -q -c "feat/$F"
mkdir -p "specs/$F"
cat > "specs/$F/tasks.md" <<'EOF'
---
feature: 005-session-token-usage
status: approved
version: 1
---
# Tasks — 005-session-token-usage

## Tasks

### T001 — Demo task
- Do: add a demo
- Tests: TestDemo
- AC: AC1
- Commit: feat(demo): add a demo
- Depends: —
- Size: S
- Risk: low
EOF
git add specs && git commit -q -m "docs($F): add tasks"
sed -n '/^cmd_add_fix() {/,/^}/p' .claude/scripts/loop.sh > add_fix.sh
printf 'add_fix.sh\n' >> .git/info/exclude

BUG='In multi-session `gateway timeline -json`, session header rows carry `usage: null` and `calls: 0`, which reads as "no model calls" (AC12) and contradicts each session'"'"'s `usage_total` line. Remove the usage/calls keys from header rows; totals live only in usage_total. Regression test: a two-session timeline -json where header rows have no usage key and each usage_total matches its session.'

add_fix() { # bug -> runs cmd_add_fix with approvals stubbed
	bash -c '. .claude/scripts/lib.sh; . .claude/scripts/validate.sh; al_init
		art_state() { echo approved; }
		approval_sha() { git rev-parse HEAD; }
		tasks_problems() { return 0; }
		. ./add_fix.sh
		cmd_add_fix "$1"' _ "$1"
}

before=$(cat "specs/$F/tasks.md")
head_before=$(git rev-parse HEAD)

# --- the commit fails: a commit-msg hook that rejects everything
mkdir -p .git/hooks
printf '#!/bin/sh\necho rejected by test hook >&2\nexit 1\n' > .git/hooks/commit-msg
chmod +x .git/hooks/commit-msg

out=$(add_fix "$BUG" 2>&1); rc=$?

t "add-fix exits non-zero when the commit fails"
[ "$rc" != 0 ] && pass || fail "exit code 0: $out"

t "add-fix says nothing was added and why"
assert_contains "$out" "nothing was added" "add-fix output"

t "tasks.md is unchanged"
assert_eq "$before" "$(cat "specs/$F/tasks.md")" "tasks.md"

t "the tree is clean"
assert_eq "" "$(git status --porcelain)" "git status"

t "no commit was made"
assert_eq "$head_before" "$(git rev-parse HEAD)" "HEAD"

# --- the commit succeeds: subject fits, body carries the full text
rm -f .git/hooks/commit-msg
out=$(add_fix "$BUG" 2>&1); rc=$?

t "add-fix succeeds without the rejecting hook"
assert_eq 0 "$rc" "exit code ($out)"

t "the tasks.md commit subject fits 72 bytes"
subject=$(git log -1 --format=%s)
[ "$(printf '%s' "$subject" | LC_ALL=C wc -c)" -le 72 ] && pass || fail "subject too long: $subject"

t "the tasks.md commit body is the full bug text"
assert_eq "$BUG" "$(git log -1 --format=%b | sed '/^$/d')" "commit body"

t "the new task's Do: line carries the full bug text"
assert_contains "$(cat "specs/$F/tasks.md")" "- Do: reproduce and fix: $BUG" "tasks.md"

t "the new task's Commit: line fits 72 bytes"
line=$(grep '^- Commit: fix(' "specs/$F/tasks.md"); line=${line#- Commit: }
[ "$(printf '%s' "$line" | LC_ALL=C wc -c)" -le 72 ] && pass || fail "Commit: line too long: $line"

done_tests
