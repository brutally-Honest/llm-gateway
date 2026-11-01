#!/usr/bin/env bash
# Every kit agent carries the rule against getting around a guard, hook or check, word for
# word, and the reviewer checks commits and runs for it. The orchestrator (LOOP.md and the
# /al-* skills that drive it) carries its own version.
set -u
. "$(dirname "${BASH_SOURCE[0]}")/helpers.sh"

RULE="Never get around a guard, hook or check by splitting, quoting, encoding or renaming strings, or by using another tool to perform the action that was blocked. Reading or editing a file with Read/Edit/Write is fine when only a Bash command naming it was blocked. Running a human-only command (approve, answer, change, pass-manual) is never fine by any route. If unsure, stop and ask."

n=0
for f in "$KIT_SRC"/agents/*.md; do
	n=$((n + 1))
	t "${f##*/} carries the no-workaround rule"
	grep -qF -- "$RULE" "$f" && pass || fail "the rule is missing or changed in ${f##*/}"
done

t "every kit agent was checked"
assert_eq 6 "$n" "agent files"

# the orchestrator's version: LOOP.md, and every /al-* skill that runs the loop or loop.sh answer
ORCH="Never get around a guard, hook or check by splitting, quoting, encoding or renaming strings, or by using another tool to perform the action that was blocked. Reading or editing a file with Read/Edit/Write is fine when only a Bash command naming it was blocked. approve, /al-change and pass-manual are human-only by any route. Run loop.sh answer only after the human has answered, and record their answer as given: if they picked an option, record that option's text, never your own rewording. Never write loop state or task status by hand. If unsure, stop and ask."
for f in al-implement/LOOP.md al-implement/SKILL.md al-resume/SKILL.md al-approve/SKILL.md al-fix/SKILL.md al-quick/SKILL.md al-answer/SKILL.md; do
	t "$f carries the orchestrator's no-workaround rule"
	grep -qF -- "$ORCH" "$KIT_SRC/skills/$f" && pass || fail "the rule is missing or changed in skills/$f"
done

t "every skill that hands over to LOOP.md carries the rule"
missing=""
for f in "$KIT_SRC"/skills/*/SKILL.md; do
	grep -q 'LOOP.md' "$f" && ! grep -qF -- "$ORCH" "$f" && missing="$missing ${f#"$KIT_SRC"/skills/}"
done
assert_eq "" "$missing" "skills without the rule"

t "the reviewer checks for getting around the checks, as ESCALATE"
rv=$(sed -n '/^## Check, in order/,/^## Verdict/p' "$KIT_SRC/agents/reviewer.md")
assert_contains "$rv" "**Getting around the checks**" "reviewer check list"
assert_contains "$rv" "Any of it is ESCALATE." "reviewer check list"

t "the reviewer's ESCALATE covers a check got around"
assert_contains "$(grep '^- \*\*ESCALATE\*\*' "$KIT_SRC/agents/reviewer.md")" "a check was weakened or got around" "verdict"

done_tests
