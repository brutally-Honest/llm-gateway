#!/usr/bin/env bash
# Bug 4: add-fix builds commit subjects that fit 72 bytes, cut at a word boundary, with the
# full bug text kept in the task's Do: line and the tasks.md commit body.
set -u
. "$(dirname "${BASH_SOURCE[0]}")/helpers.sh"
HOOK="$KIT_SRC/../.githooks/commit-msg"   # the repo's own subject check, run read-only
mk_repo

# the exact description from 005's T008
BUG='In multi-session `gateway timeline -json`, session header rows carry `usage: null` and `calls: 0`, which reads as "no model calls" (AC12) and contradicts each session'"'"'s `usage_total` line. Remove the usage/calls keys from header rows; totals live only in usage_total. Regression test: a two-session timeline -json where header rows have no usage key and each usage_total matches its session.'
# add-fix's own cleanup of the text (loop.sh cmd_add_fix)
SUBJ=$(printf '%s' "$BUG" | tr '[:upper:]' '[:lower:]' | tr -c 'a-z0-9 .,-' ' ' | tr -s ' ' | sed 's/^ //; s/ $//')

fit() { bash -c '. .claude/scripts/lib.sh; fit_subject "$1" "$2"' _ "$1" "$2"; }
bytes() { printf '%s' "$1" | LC_ALL=C wc -c | tr -d ' '; }
hook_ok() { # subject body -> 0 when the repo's commit-msg hook accepts it
	printf '%s\n\n%s\n' "$1" "$2" > msg.txt
	sh "$HOOK" msg.txt >/dev/null 2>&1; local rc=$?; rm -f msg.txt; return $rc
}

for prefix in "docs(005-session-token-usage): add T008 — fix: " "fix(session-token-usage): "; do
	s=$(fit "$prefix" "$SUBJ")
	summary=${s#"$prefix"}

	t "'$prefix…' fits 72 bytes"
	[ "$(bytes "$s")" -le 72 ] && pass || fail "$(bytes "$s") bytes: $s"

	t "'$prefix…' keeps the prefix and some words"
	case $s in "$prefix"?*) pass ;; *) fail "got: $s" ;; esac

	t "'$prefix…' cuts at a word boundary"
	case "$SUBJ " in "$summary "*) pass ;; *) fail "'$summary' is not whole leading words of the text" ;; esac

	t "'$prefix…' passes the repo's commit-msg hook with the full text as body"
	hook_ok "$s" "$BUG" && pass || fail "hook rejected: $s"
done

t "a short description is kept whole"
assert_eq "fix(demo): crash on empty input" "$(fit 'fix(demo): ' 'crash on empty input')"

t "trailing punctuation is dropped where the cut lands"
assert_eq "fix(demo): a, b" "$(fit 'fix(demo): ' 'a, b, ccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc')"

t "a first word too long to fit is cut, never left empty"
s=$(fit 'fix(demo): ' "$(printf 'x%.0s' $(seq 1 100))")
[ "$(bytes "$s")" -le 72 ] && [ "$s" != "fix(demo): " ] && pass || fail "got: $s"

t "add-fix uses fit_subject for both subjects and keeps the full text in the body"
src=$(sed -n '/^cmd_add_fix() {/,/^}/p' .claude/scripts/loop.sh)
n=$(printf '%s\n' "$src" | grep -c 'fit_subject')
body=$(printf '%s\n' "$src" | grep -c 'git commit -q -m "$(fit_subject "docs($F): add $next — fix: " "$subj")" -m "$bug" -- "$t" 2>&1')
do_line=$(printf '%s\n' "$src" | grep -c '^- Do: reproduce and fix: \$bug$')
assert_eq "2 1 1" "$n $body $do_line" "fit_subject calls / body / Do: line"

done_tests
