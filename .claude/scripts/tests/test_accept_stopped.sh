#!/usr/bin/env bash
# Fix 3: accept works on a STOPPED task as well as an ESCALATED one. For STOPPED a reason
# is required. The reason is committed to research.md (## Human decisions) by a kit
# commit, so it outlives the gitignored loop state.
set -u
. "$(dirname "${BASH_SOURCE[0]}")/helpers.sh"
mk_e2e
R=specs/001-e2e/research.md
state() { awk -F '\t' '$1 == "T001" { s = $2 } END { print s }' .agent-loop/001-e2e/state; }

$L start --session accept >/dev/null
$L next >/dev/null
# the task names tests but commits none: it fails twice and stops at the limit (FIX_ROUNDS=1)
task_commit "feat(demo): add the greet function" "demo/demo.go=package demo"
own=$(git rev-parse HEAD)
$L log T001 implementer 'DONE T001 x' >/dev/null
$L log T001 implementer 'DONE T001 x' >/dev/null
head=$(git rev-parse HEAD)

t "the task is STOPPED"
assert_eq STOPPED "$(state)" "state"

t "accepting a STOPPED task without a reason is refused"
out=$($L accept T001 2>&1); rc=$?
assert_eq "1 STOPPED $head" "$rc $(state) $(git rev-parse HEAD)" "exit, state, HEAD ($out)"

t "when the decision can't be committed, nothing is accepted and research.md is as it was"
printf -- '- your unsaved note\n' >> "$R"; before=$(cat "$R")
mkdir -p .git/reject && printf '#!/bin/sh\nexit 1\n' > .git/reject/commit-msg && chmod +x .git/reject/commit-msg
git config core.hooksPath .git/reject
out=$($L accept T001 'x' 2>&1); rc=$?
git config --unset core.hooksPath
assert_eq "1 STOPPED $head" "$rc $(state) $(git rev-parse HEAD)" "exit, state, HEAD ($out)"
assert_eq "$before" "$(cat "$R")" "research.md"
git checkout -q -- "$R"

t "accepting it with a reason passes it"
assert_eq "ACTION next" "$($L accept T001 'tests are in the next task' | last)" "accept"
assert_eq PASS "$(state)" "state"

t "the reason is committed to research.md under Human decisions"
assert_eq "## Human decisions|- $(date -u +%Y-%m-%d) T001 accepted as it is (it was STOPPED fix-limit): tests are in the next task" \
	"$(sed -n '/^## Human decisions$/,$p' "$R" | sed '/^$/d' | paste -sd'|' -)" "research.md"

t "by a kit commit holding research.md alone"
assert_eq "accept|$R" "$(git log -1 --format='%(trailers:key=Kit,valueonly)' | sed '/^$/d')|$(git show --name-only --format= HEAD | sed '/^$/d')" "Kit trailer | files"

t "the task's own commit is recorded for it"
assert_eq "$own T001" "$(cat .agent-loop/001-e2e/commits)" "commits map"

t "the build moves on instead of running T001 again"
assert_contains "$($L next | last)" "ACTION review BRANCH" "next"

t "a task that is neither STOPPED nor ESCALATED can't be accepted"
out=$($L accept T001 'again' 2>&1); rc=$?
assert_eq 1 "$rc" "exit ($out)"

done_tests
