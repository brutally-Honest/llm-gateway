#!/usr/bin/env bash
# Fix 7: a fix round counts only once an implementer has run it (its result is logged).
# Asking for a round again — e.g. running loop.sh next instead of dispatching — asks for
# the same round; it does not use one up.
set -u
. "$(dirname "${BASH_SOURCE[0]}")/helpers.sh"
mk_e2e 'FIX_ROUNDS=2'
rounds() { cat .agent-loop/001-e2e/rounds.T001; }

$L start --session rounds >/dev/null
$L next >/dev/null
# the task names tests but commits none, so the post-task check fails
task_commit "feat(demo): add the greet function" "demo/demo.go=package demo"

t "a failed DONE asks for fix round 1"
assert_eq "ACTION fix T001 post-task 1/2 model=haiku" "$($L log T001 implementer 'DONE T001 x' | last)" "log"

t "asking is not running: no round is used yet"
assert_eq 0 "$(rounds)" "rounds.T001"

t "the task pack tells the implementer which round it is in"
assert_contains "$($L task T001 | head -1)" "ROUND 1/2" "task pack"

t "running next instead of dispatching asks for the same round"
assert_eq "ACTION fix T001 post-task 1/2 model=haiku" "$($L next | last)" "next"

t "and again: still the same round, never the limit"
assert_eq "ACTION fix T001 post-task 1/2 model=haiku" "$($L next | last)" "next"
assert_eq 0 "$(rounds)" "rounds.T001"

t "the implementer's result for round 1 uses it; the next failure asks for round 2"
assert_eq "ACTION fix T001 post-task 2/2 model=haiku" "$($L log T001 implementer 'DONE T001 x' | last)" "log"
assert_eq 1 "$(rounds)" "rounds.T001"

t "round 2 run and failed: the task stops at the limit"
assert_eq "ACTION stop fix-limit T001" "$($L log T001 implementer 'DONE T001 x' | last)" "log"
assert_eq 2 "$(rounds)" "rounds.T001"

done_tests
