#!/usr/bin/env bash
# Fix 8b: the report's Next: line follows the latest stop — not the first STOPPED task in
# task order, and not a generic /al-implement for stops that leave no task state.
set -u
. "$(dirname "${BASH_SOURCE[0]}")/helpers.sh"
mk_e2e
nextline() { printf '%s\n' "$1" | grep '^Next: '; }

t "a pre-task stop: Next names the task and why it couldn't start"
$L start --session next >/dev/null
printf 'x\n' > stray.txt
$L next >/dev/null
out=$($L stop 'ACTION stop pre-task T001')
assert_contains "$(nextline "$out")" "couldn't start T001: working tree not clean before T001" "report"
rm stray.txt

t "a later fix-limit stop replaces it"
$L start --session next >/dev/null
$L next >/dev/null
task_commit "feat(demo): add the greet function" "demo/demo.go=package demo"
$L log T001 implementer 'DONE T001 x' >/dev/null
$L log T001 implementer 'DONE T001 x' >/dev/null
out=$($L stop 'ACTION stop fix-limit T001')
assert_contains "$(nextline "$out")" "T001 hit the fix-round limit" "report"
assert_not_contains "$(nextline "$out")" "couldn't start" "report"

t "a newer stop on the same STOPPED task is the one Next describes"
$L start --session next >/dev/null
printf 'x\n' > stray.txt
$L next >/dev/null
out=$($L stop 'ACTION stop pre-task T001')
assert_contains "$(nextline "$out")" "couldn't start T001: working tree not clean" "report"
rm stray.txt

t "a contract stop says what broke and that resuming picks the task up"
out=$($L stop 'ACTION stop contract T001 (first line was not DONE/BLOCKED/NEEDS-HUMAN)')
assert_contains "$(nextline "$out")" "T001: an agent's report broke the loop's contract (first line was not DONE/BLOCKED/NEEDS-HUMAN)" "report"

t "once the task moves on (accepted), the old stop no longer drives Next"
$L accept T001 'checked by hand' >/dev/null
out=$($L report)
assert_not_contains "$(nextline "$out")" "contract" "report"
assert_not_contains "$(nextline "$out")" "T001" "report"

t "a new run clears the last stop"
$L start --session next >/dev/null
assert_eq "" "$(cat .agent-loop/001-e2e/laststop 2>/dev/null)" "laststop"

t "with two held tasks, Next describes the one that stopped last, not the first in task order"
$L add-fix "the greeting is wrong" >/dev/null   # T002, after T001
printf 'T001\tSTOPPED\tfix-limit\t2026-01-01T00:00:00Z\nT002\tESCALATED\t\t2026-01-01T00:01:00Z\n' >> .agent-loop/001-e2e/state
assert_contains "$($L status | grep '^Next: ')" "T002 needs your decision" "Next"

done_tests
