#!/usr/bin/env bash
# Fix 5: TASK_CHECK_CMD (loop.conf) runs after every task whose full verify is deferred,
# so a cheap must-hold check (this repo: the core purity test) never waits for the
# every-N verify. Red is a post-task failure, like a red verify.
set -u
. "$(dirname "${BASH_SOURCE[0]}")/helpers.sh"

t "this repo's loop.conf runs the core purity test after every task"
conf=$(bash -c '. "$1"; printf %s "$TASK_CHECK_CMD"' _ "$KIT_SRC/loop.conf")
assert_contains "$conf" "TestCore_NoProviderOrClientIdentifiers" "TASK_CHECK_CMD"

mk_e2e 'VERIFY="every-3"' 'TASK_CHECK_CMD="echo purity broke; false"'
$L start --session check >/dev/null
$L next >/dev/null
task_commit "feat(demo): add the greet function" "demo/demo.go=package demo" "demo/demo_test.go=package demo"

t "with verify deferred, a red task check fails the task"
out=$($L log T001 implementer 'DONE T001 x')
assert_eq "ACTION fix T001 post-task 1/1 model=haiku" "$(printf '%s\n' "$out" | last)" "log"

t "the findings name the command and show its output"
f=$($L findings T001)
assert_contains "$f" "task check is red (echo purity broke; false)" "findings"
assert_contains "$f" "purity broke" "findings"

t "its output is kept in .agent-loop/<f>/task-check.log"
assert_contains "$(cat .agent-loop/001-e2e/task-check.log)" "purity broke" "task-check.log"

t "a green task check lets the task pass, and is logged"
mk_e2e 'VERIFY="every-3"' 'TASK_CHECK_CMD="true"'
$L start --session check >/dev/null; $L next >/dev/null
task_commit "feat(demo): add the greet function" "demo/demo.go=package demo" "demo/demo_test.go=package demo"
assert_contains "$($L log T001 implementer 'DONE T001 x')" "T001 passes" "log"
assert_contains "$(cat .agent-loop/001-e2e/run.log)" "TASK-CHECK green after T001" "run.log"

t "with VERIFY=task the full verify covers it: the task check doesn't run"
mk_e2e 'VERIFY="task"' 'TASK_CHECK_CMD="false"'
$L start --session check >/dev/null; $L next >/dev/null
task_commit "feat(demo): add the greet function" "demo/demo.go=package demo" "demo/demo_test.go=package demo"
assert_contains "$($L log T001 implementer 'DONE T001 x')" "T001 passes" "log"

t "a task check that changes the tree is red"
mk_e2e 'VERIFY="every-3"' 'TASK_CHECK_CMD="touch stray-file"'
$L start --session check >/dev/null; $L next >/dev/null
task_commit "feat(demo): add the greet function" "demo/demo.go=package demo" "demo/demo_test.go=package demo"
assert_contains "$($L log T001 implementer 'DONE T001 x')" "task check is red" "log"

done_tests
