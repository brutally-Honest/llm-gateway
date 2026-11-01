#!/usr/bin/env bash
# Every kit agent carries the rule against getting around a guard, hook or check, word for
# word, and the reviewer checks commits and runs for it.
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

t "the reviewer checks for getting around the checks, as ESCALATE"
rv=$(sed -n '/^## Check, in order/,/^## Verdict/p' "$KIT_SRC/agents/reviewer.md")
assert_contains "$rv" "**Getting around the checks**" "reviewer check list"
assert_contains "$rv" "Any of it is ESCALATE." "reviewer check list"

t "the reviewer's ESCALATE covers a check got around"
assert_contains "$(grep '^- \*\*ESCALATE\*\*' "$KIT_SRC/agents/reviewer.md")" "a check was weakened or got around" "verdict"

done_tests
