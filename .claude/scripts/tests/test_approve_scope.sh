#!/usr/bin/env bash
# Fix 2: an approval commits only the files it wrote. Your other work in specs/<f>/ — a
# draft plan edit, a new file, research notes — stays uncommitted, as you left it.
set -u
. "$(dirname "${BASH_SOURCE[0]}")/helpers.sh"
mk_e2e
D=specs/001-e2e
A=.claude/scripts/approve.sh
files_of() { git show --name-only --format= HEAD | sed '/^$/d' | sort | tr '\n' ' '; }

$A reopen spec --reason "edit the goal" >/dev/null
printf '\nAn extra note.\n' >> "$D/plan.md"         # your unfinished edit of the plan
printf -- '- a research note\n' >> "$D/research.md"
printf 'draft\n' > "$D/notes-draft.md"               # a new file, never added

t "approving the spec commits spec.md only"
$A spec >/dev/null
assert_eq "$D/spec.md " "$(files_of)" "files in the approval commit"

t "your plan edit, research note and new file are left uncommitted"
assert_eq " M $D/plan.md| M $D/research.md|?? $D/notes-draft.md" "$(git status --porcelain -- "$D" | sort | paste -sd'|' -)" "git status"

git checkout -q -- "$D/plan.md"
t "reopening the plan commits plan.md and tasks.md only"
$A reopen plan --reason "revise" >/dev/null
assert_eq "$D/plan.md $D/tasks.md " "$(files_of)" "files in the reopen commit"
t "approving the plan (tasks auto-approved) commits plan.md and tasks.md only"
$A plan >/dev/null
assert_eq "$D/plan.md $D/tasks.md " "$(files_of)" "files in the approval commit"

t "research.md and the new file are still uncommitted"
assert_eq " M $D/research.md|?? $D/notes-draft.md" "$(git status --porcelain -- "$D" | sort | paste -sd'|' -)" "git status"

done_tests
