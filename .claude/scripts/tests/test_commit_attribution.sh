#!/usr/bin/env bash
# Fix 1: the post-task check attributes commits by their trailers. A task's work is its
# "Task: <id>" commits; a kit commit (answer, approve, change, accept, ...) carries
# "Kit: <action>" and never counts against the task; anything else in the range fails.
set -u
. "$(dirname "${BASH_SOURCE[0]}")/helpers.sh"
mk_e2e
R=specs/001-e2e/research.md
kit_of() { git log -1 --format='%(trailers:key=Kit,valueonly)' "${1:-HEAD}" | sed '/^$/d'; }

# an open question to answer later, committed before the task starts
sed -i 's/^## Open questions$/## Open questions\n- **Q1** (open) T001 — which greeting? — options: A) hello B) hi/' "$R"
git commit -q -m "docs(001-e2e): ask q1" -- "$R"

$L start --session attr >/dev/null
$L next >/dev/null
task_commit "feat(demo): add the greet function" "demo/demo.go=package demo" "demo/demo_test.go=package demo"
own=$(git rev-parse HEAD)

t "answer commits with the trailer Kit: answer"
$L answer Q1 hello >/dev/null
assert_eq answer "$(kit_of)" "Kit trailer"

t "a kit commit in the task's range doesn't count against the task"
out=$($L post-check T001 2>&1); rc=$?
assert_eq 0 "$rc" "post-check exit ($out)"

t "a kit commit is known by its trailer, whatever its subject says"
mkdir -p specs/001-e2e/changes && printf 'note\n' > specs/001-e2e/changes/notes.md
git add specs/001-e2e/changes/notes.md
git commit -q -m "chore(anything): renamed kit command" -m "Kit: renamed-action"
out=$($L post-check T001 2>&1); rc=$?
assert_eq 0 "$rc" "post-check exit ($out)"

t "the same specs commit without the trailer fails"
printf 'more\n' >> specs/001-e2e/changes/notes.md
git commit -q -m "docs(001-e2e): answer q9" -- specs/001-e2e/changes/notes.md
bad=$(git rev-parse --short HEAD)
out=$($L post-check T001 2>&1)
assert_contains "$out" "commit $bad lacks the trailer 'Task: T001'" "post-check"
git reset -q --hard HEAD~1

t "a Kit-trailered commit that touches code outside specs/ fails"
printf 'package demo\n' > demo/other.go; git add demo/other.go
git commit -q -m "docs(001-e2e): answer q9" -m "Kit: answer"
bad=$(git rev-parse --short HEAD)
out=$($L post-check T001 2>&1)
assert_contains "$out" "kit commit $bad changes demo/other.go outside specs/001-e2e/" "post-check"
git reset -q --hard HEAD~1

t "an untrailered commit that touches code still fails"
printf 'package demo\n' > demo/stray.go; git add demo/stray.go
git commit -q -m "feat(demo): stray code"
bad=$(git rev-parse --short HEAD)
out=$($L post-check T001 2>&1)
assert_contains "$out" "commit $bad lacks the trailer 'Task: T001'" "post-check"
git reset -q --hard HEAD~1

t "the implementer's blocked-question commit (research.md, 'Blocked:' trailer) doesn't count"
printf -- '- **Q2** (answered) T001 — x\n' >> "$R"
git commit -q -m "docs(001-e2e): record q2 blocking t001" -m "Feature: 001-e2e
Blocked: T001 Q2" -- "$R"
out=$($L post-check T001 2>&1); rc=$?
assert_eq 0 "$rc" "post-check exit ($out)"

t "a 'Blocked:' commit that touches more than research.md fails"
printf 'package demo\n' > demo/blocked.go; git add demo/blocked.go
git commit -q -m "docs(001-e2e): record q3 blocking t001" -m "Feature: 001-e2e
Blocked: T001 Q3"
bad=$(git rev-parse --short HEAD)
out=$($L post-check T001 2>&1)
assert_contains "$out" "blocked-question commit $bad changes demo/blocked.go — it may only change specs/001-e2e/research.md" "post-check"
git reset -q --hard HEAD~1

t "logging DONE records only the task's own commit"
out=$($L log T001 implementer "DONE T001 $(git rev-parse --short HEAD)")
assert_eq "$own T001" "$(cat .agent-loop/001-e2e/commits)" "commits map ($out)"

t "add-fix commits with Kit: add-fix"
$L add-fix "the greeting is wrong" >/dev/null
assert_eq add-fix "$(kit_of)" "Kit trailer"

t "the research autocommit (before the next task) carries Kit: research"
printf -- '- a note\n' >> "$R"
$L next >/dev/null
assert_eq research "$(kit_of)" "Kit trailer ($(git log -1 --format=%s))"

t "reopen and approve commit with Kit: reopen and Kit: approve"
.claude/scripts/approve.sh reopen spec --reason "test" >/dev/null
r=$(kit_of)
.claude/scripts/approve.sh spec >/dev/null
assert_eq "reopen approve" "$r $(kit_of)" "Kit trailers"

done_tests
