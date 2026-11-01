#!/usr/bin/env bash
# Fix 6: a fix round may change only the files the task's own commits had changed when the
# round was asked for, plus test files in those files' folders. Anything else fails the
# check and stops the task — it never passes and never gets another round.
set -u
. "$(dirname "${BASH_SOURCE[0]}")/helpers.sh"
mk_e2e 'FIX_ROUNDS=2' 'REVIEW="every"'
amend() { local kv p; for kv in "$@"; do p=${kv%%=*}; mkdir -p "$(dirname "$p")"; printf '%s\n' "${kv#*=}" > "$p"; git add -- "$p"; done; git commit -q --amend --no-edit; }
scope_err() { $L post-check T001 2>&1 | grep -c 'outside the scope of its fix rounds'; }

$L start --session scope >/dev/null
$L next >/dev/null
task_commit "feat(demo): add the greet function" "demo/demo.go=package demo" "demo/demo_test.go=package demo"
$L log T001 implementer "DONE T001 x" >/dev/null
t "the reviewer's FIX asks for round 1"
assert_eq "ACTION fix T001 review 1/2 model=haiku" "$($L log T001 reviewer 'FIX' | last)" "log"
start=$(git rev-parse HEAD)

t "round 1 may edit the task's files"
amend "demo/demo.go=package demo // fixed" "demo/demo_test.go=package demo // more"
assert_eq 0 "$(scope_err)" "scope problems"

t "round 1 may add a test file in the folder of a task file"
amend "demo/extra_test.go=package demo"
assert_eq 0 "$(scope_err)" "scope problems"
git reset -q --hard "$start"

t "round 1 may not add a non-test file, even in the same folder"
amend "demo/helper.go=package demo"
assert_eq 1 "$(scope_err)" "scope problems"
git reset -q --hard "$start"

t "round 1 may not add a test file in another folder"
amend "other/other_test.go=package other"
assert_eq 1 "$(scope_err)" "scope problems"
git reset -q --hard "$start"

t "round 1 may not touch a file elsewhere, even in a new commit"
task_commit "fix(demo): touch elsewhere" "other/other.go=package other"
assert_contains "$($L post-check T001 2>&1)" "changed other/other.go — outside the scope of its fix rounds" "post-check"

t "logging that round stops the task instead of passing or asking for another round"
assert_eq "ACTION stop scope T001" "$($L log T001 implementer 'DONE T001 x' | last)" "log"
assert_eq "STOPPED scope" "$(awk -F '\t' '$1 == "T001" { s = $2 " " $3 } END { print s }' .agent-loop/001-e2e/state)" "state"

done_tests
