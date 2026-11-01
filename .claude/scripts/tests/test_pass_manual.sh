#!/usr/bin/env bash
# Fix 4: loop.sh pass-manual <ID> '<reason>' records that you ran a NEEDS-HUMAN task's
# manual check and it passed. The task becomes PASS, the reason is committed to
# research.md, and the report stops saying needs-human. Human-only, like the approvals.
set -u
. "$(dirname "${BASH_SOURCE[0]}")/helpers.sh"
mk_e2e 'REVIEW="none"'
R=specs/001-e2e/research.md
state() { awk -F '\t' '$1 == "T001" { s = $2 } END { print s }' .agent-loop/001-e2e/state; }
decision() { # command role -> allow | deny | pass
	local out
	out=$(jq -cn --arg c "$1" --arg r "$2" --arg d "$PWD" \
		'{tool_name:"Bash", agent_type:$r, cwd:$d, session_id:"test", tool_input:{command:$c}}' | .claude/hooks/guard.sh)
	[ -n "$out" ] || { echo pass; return; }
	jq -r '.hookSpecificOutput.permissionDecision' <<<"$out"
}

$L start --session manual >/dev/null
$L next >/dev/null
task_commit "feat(demo): add the greet function" "demo/demo.go=package demo" "demo/demo_test.go=package demo"
own=$(git rev-parse HEAD)

t "a task can only pass by hand once it is NEEDS-HUMAN"
out=$($L pass-manual T001 'M1 passed' 2>&1); rc=$?
assert_eq 1 "$rc" "exit ($out)"

$L log T001 implementer 'NEEDS-HUMAN T001 M1' >/dev/null
t "the report says the manual check is still to do"
assert_contains "$($L report)" "do the manual checks for T001" "report"

t "a reason is required"
out=$($L pass-manual T001 2>&1); rc=$?
assert_eq "1 NEEDS-HUMAN" "$rc $(state)" "exit, state ($out)"

t "pass-manual passes the task"
out=$($L pass-manual T001 'M1 passed, see research.md'); rc=$?
assert_eq "0 PASS" "$rc $(state)" "exit, state ($out)"

t "the reason is committed to research.md by a kit commit"
assert_contains "$(sed -n '/^## Human decisions$/,$p' "$R")" "- $(date -u +%Y-%m-%d) T001 manual check passed: M1 passed, see research.md" "research.md"
assert_eq "pass-manual|$R" "$(git log -1 --format='%(trailers:key=Kit,valueonly)' | sed '/^$/d')|$(git show --name-only --format= HEAD | sed '/^$/d')" "Kit trailer | files"

t "the task's own commit is recorded for it"
assert_eq "$own T001" "$(cat .agent-loop/001-e2e/commits)" "commits map"

t "the report shows it passed, and no longer asks for the manual check"
rep=$($L report)
assert_contains "$rep" "manual check passed: M1 passed, see research.md" "report"
assert_not_contains "$rep" "NEEDS-HUMAN" "report"
assert_not_contains "$rep" "do the manual checks" "report"

t "the guard keeps it human-only: denied to the main session and to every agent"
got=""
for role in main implementer reviewer planner general-purpose; do
	got="$got $(decision '.claude/scripts/loop.sh pass-manual T001 ok' "$role")"
done
assert_eq " deny deny deny deny deny" "$got" "guard decisions"

t "settings.json denies it as a permission rule too"
assert_eq 1 "$(jq '[.permissions.deny[] | select(. == "Bash(*pass-manual*)")] | length' "$KIT_SRC/settings.json")" "deny rule"

done_tests
