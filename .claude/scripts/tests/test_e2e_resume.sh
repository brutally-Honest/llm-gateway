#!/usr/bin/env bash
# End to end, on a feature with real approvals (fixtures/e2e/repo.bundle): a task stops at
# the fix-round limit, you commit its work by hand, the build resumes, and the after-task
# check passes on that commit. This is the path that broke in 005 (bug 1).
set -u
. "$(dirname "${BASH_SOURCE[0]}")/helpers.sh"
BUNDLE="$KIT_SRC/scripts/tests/fixtures/e2e/repo.bundle"
[ -f "$BUNDLE" ] || { echo "  FAIL missing $BUNDLE — build it with fixtures/e2e/make.sh" >&2; exit 1; }

TMP_REPO=$(mktemp -d "${TMPDIR:-/tmp}/al-e2e.XXXXXX") || exit 1
trap 'rm -rf "$TMP_REPO"' EXIT
git clone -q -b feat/001-e2e "$BUNDLE" "$TMP_REPO"
cd "$TMP_REPO" || exit 1
git config user.name test
git config user.email test@example.invalid
git config commit.gpgsign false
git branch -q main origin/main   # the base branch, as in a real clone
git remote remove origin

# the kit under test replaces the one the fixture was approved with; verify always passes here
rm -rf .claude/scripts .claude/hooks .claude/templates .claude/agents
cp -R "$KIT_SRC/scripts" "$KIT_SRC/hooks" "$KIT_SRC/templates" "$KIT_SRC/agents" .claude/
rm -rf .claude/scripts/tests
cp "$KIT_SRC/loop.conf" .claude/loop.conf
printf '\nVERIFY_CMD="true"\nTASK_CHECK_CMD=""\n' >> .claude/loop.conf
git add .claude && git commit -q -m "chore(repo): use the kit under test"

L=.claude/scripts/loop.sh
last() { tail -1; }

t "the fixture's approvals hold"
out=$($L check tasks 2>&1)
assert_contains "$out" "passes the approve checks" "check tasks"

t "the build starts T001"
$L start --session e2e >/dev/null
assert_eq "ACTION implement T001 model=haiku" "$($L next | last)" "next"
base=$(cat .agent-loop/001-e2e/base.T001)

t "T001 stops at the fix-round limit when nothing is committed"
$L log T001 implementer 'DONE T001 0000000' >/dev/null
assert_eq "ACTION stop fix-limit T001" "$($L log T001 implementer 'DONE T001 0000000' | last)" "log"
$L stop 'ACTION stop fix-limit T001' >/dev/null

# you finish the task by hand
mkdir -p demo
printf 'package demo\n\nfunc Greet(name string) string { return "hello, " + name }\n' > demo/demo.go
printf 'package demo\n\nimport "testing"\n\nfunc TestGreet(t *testing.T) {}\n' > demo/demo_test.go
git add demo/demo.go demo/demo_test.go
git commit -q -m "feat(demo): add the greet function" -m "Task: T001
Feature: 001-e2e"

t "resuming goes back to T001"
$L start --session e2e >/dev/null
assert_eq "ACTION implement T001 model=haiku" "$($L next | last)" "next"

t "resuming keeps T001's base, so the hand commit is the task's work"
assert_eq "$base" "$(cat .agent-loop/001-e2e/base.T001)" "base.T001"

t "post-check passes cleanly on the hand commit"
out=$($L post-check T001 2>&1); rc=$?
assert_eq "0 OK: T001 meets the post-task contract (verify runs when the orchestrator logs DONE)" "$rc $out" "post-check"

t "logging DONE passes T001"
out=$($L log T001 implementer "DONE T001 $(git rev-parse --short HEAD)")
assert_contains "$out" "T001 passes" "log"

done_tests
