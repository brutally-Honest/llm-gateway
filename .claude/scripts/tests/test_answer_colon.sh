#!/usr/bin/env bash
# Fix 8a: /al-answer takes "Q2:" (and "Q2,") as well as "Q2".
set -u
. "$(dirname "${BASH_SOURCE[0]}")/helpers.sh"
mk_e2e
R=specs/001-e2e/research.md
sed -i 's/^## Open questions$/## Open questions\n- **Q1** (open) T001 — which greeting? — options: A) hello B) hi\n- **Q2** (open) T001 — which name? — options: A) world B) you/' "$R"
git commit -q -m "docs(001-e2e): ask q1 and q2" -- "$R"

t "Q1: is read as Q1"
out=$($L answer Q1: hello 2>&1); rc=$?
assert_eq 0 "$rc" "exit ($out)"
assert_contains "$(grep -F '**Q1**' "$R")" "(answered)" "research.md"

t "the answer text keeps no stray colon"
assert_contains "$(cat "$R")" "  - **Answer:** hello (" "research.md"

t "Q2, is read as Q2"
out=$($L answer 'Q2,' world 2>&1); rc=$?
assert_eq 0 "$rc" "exit ($out)"

t "anything else is still refused"
out=$($L answer Qx yes 2>&1); rc=$?
assert_eq 1 "$rc" "exit ($out)"

done_tests
