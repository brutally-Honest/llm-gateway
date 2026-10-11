#!/usr/bin/env bash
# Bug 1: a task's base commit (.agent-loop/<f>/base.<id>) survives a restart when the task
# resumes an interrupted attempt, so a hand commit in between still counts as the task's
# work; any other start records HEAD.
set -u
. "$(dirname "${BASH_SOURCE[0]}")/helpers.sh"
mk_repo

F=001-demo
git switch -q -c "feat/$F"
mkdir -p "specs/$F"
cat > "specs/$F/tasks.md" <<'EOF'
---
feature: 001-demo
status: draft
version: 1
---
# Tasks — 001-demo

## Tasks

### T001 — Demo task
- Do: add a test
- Tests: TestDemo
- AC: AC1
- Commit: feat(demo): add a demo
- Depends: —
- Size: S
- Risk: low
EOF
git add specs && git commit -q -m "docs($F): add tasks"

# lib() runs a snippet with the kit's lib.sh loaded for feature $F
lib() { bash -c '. .claude/scripts/lib.sh; al_init; F='"$F"'; '"$1"; }
set_state() { lib "state_set T001 $1"; }
base() { lib 'sfile_get base.T001'; }

# --- (a) the 005 case: BLOCKED, hand commit, resume -> base unchanged, check sees it
b0=$(git rev-parse HEAD)
lib 'record_base T001'
set_state BLOCKED
mkdir -p internal/demo
printf 'package demo\n' > internal/demo/demo_test.go
git add internal && git commit -q -m "feat(demo): add a demo" -m "Task: T001" -m "Feature: $F"
lib 'record_base T001'

t "(a) BLOCKED task keeps its base across a hand commit"
assert_eq "$b0" "$(base)" "base.T001"

t "(a) after-task check sees the hand commit"
out=$(.claude/scripts/loop.sh post-check T001 2>&1)
assert_not_contains "$out" "no new commit since" "post-check"

t "(a) after-task check sees the test file"
assert_not_contains "$out" "no test file was added or changed" "post-check"

t "(a) STOPPED task keeps its base too"
set_state STOPPED
lib 'record_base T001'
assert_eq "$b0" "$(base)" "base.T001"

t "(a) IN-PROGRESS task keeps its base too"
set_state IN-PROGRESS
lib 'record_base T001'
assert_eq "$b0" "$(base)" "base.T001"

# --- (b) a task that starts again after it was done gets HEAD, not its old base
t "(b) a done (PASS) task restarting records HEAD"
set_state PASS
lib 'record_base T001'
assert_eq "$(git rev-parse HEAD)" "$(base)" "base.T001"

t "(b) a leftover base on a task with no resumable state records HEAD"
lib "sfile_set base.T001 $b0; printf '' > \"\$(sdir)/state\""
lib 'record_base T001'
assert_eq "$(git rev-parse HEAD)" "$(base)" "base.T001"

# --- (c) a stale base that is no longer an ancestor of HEAD is re-recorded
t "(c) BLOCKED task with a non-ancestor base records HEAD"
git switch -q -c side main
echo side > side.txt && git add side.txt && git commit -q -m "chore(repo): side"
stale=$(git rev-parse HEAD)
git switch -q "feat/$F"
lib "sfile_set base.T001 $stale"
set_state BLOCKED
lib 'record_base T001'
assert_eq "$(git rev-parse HEAD)" "$(base)" "base.T001"

# --- both places that record a base use the rule
t "pre_task and the answer path both call record_base"
n=$(grep -c 'record_base "$id"' .claude/scripts/loop.sh)
direct=$(grep -c 'sfile_set "base\.' .claude/scripts/loop.sh)
assert_eq "2 0" "$n $direct" "record_base calls / direct base writes"

done_tests
