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

# the research.md a new feature starts with is the kit's, not your draft: while it was never
# committed it goes in with the spec's approval, so it can't block the next branch switch
git checkout -q -- "$D/research.md"; rm -f "$D/notes-draft.md"
git switch -q -c second   # from here, so the kit under test stays checked out
N=$($L new feat second --here | awk '$1 == "FEATURE" { print $2 }')
S=specs/$N/spec.md
awk '{ print } NR > 1 && /^---$/ { exit }' "$S" > spec.tmp   # keep the frontmatter, new body
printf '# Second\n\n## Goal\nOne more thing works.\n\n## Acceptance criteria\n- **AC1** — When it runs, the system shall print ok.\n' >> spec.tmp
mv spec.tmp "$S"

t "the first spec approval also commits the never-committed research.md"
$A spec >/dev/null
assert_eq "specs/$N/research.md $S " "$(files_of)" "files in the approval commit"

t "and leaves the tree clean"
assert_eq "" "$(git status --porcelain)" "git status"

done_tests
