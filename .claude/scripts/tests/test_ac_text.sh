#!/usr/bin/env bash
# Bug 5: an AC wrapped over several lines is compared and checked as a whole, so an edit
# made only on a continuation line is seen everywhere the kit compares AC text.
set -u
. "$(dirname "${BASH_SOURCE[0]}")/helpers.sh"
mk_repo
mkdir -p specs/001-demo/changes

# lib <snippet> runs it with lib.sh and validate.sh loaded
lib() { bash -c '. .claude/scripts/lib.sh; . .claude/scripts/validate.sh; al_init; '"$1"; }

spec() { # body of AC1's continuation line -> writes specs/001-demo/spec.md
	cat > specs/001-demo/spec.md <<EOF
---
feature: 001-demo
status: draft
version: 2
previous: ${PREV:-}
---
# Demo

## Goal
A demo.

## Acceptance criteria
- **AC1** — When a request arrives, the system
  $1
- **AC2** — When nothing happens, the system shall stay idle.
EOF
}

# --- ac_text itself
t "ac_text joins wrapped lines and stops at the next AC"
got=$(printf '%s\n' '- **AC1** — When x, the system' '  shall do y.' '- **AC2** — When z, the system shall w.' | lib 'ac_text AC1')
assert_eq "- **AC1** — When x, the system shall do y." "$got" "ac_text AC1"

t "ac_text gives the same text for different wrapping"
a=$(printf '%s\n' '- **AC1** — When x, the system shall do y.' | lib 'ac_text AC1')
b=$(printf '%s\n' '- **AC1** — When x,' '    the system   shall' '  do y.' | lib 'ac_text AC1')
assert_eq "$a" "$b" "rewrapped AC1"

t "ac_text stops at a heading"
got=$(printf '%s\n' '- **AC1** — When x, the system shall do y.' '## Edge cases' 'more' | lib 'ac_text AC1')
assert_eq "- **AC1** — When x, the system shall do y." "$got" "ac_text AC1"

t "ac_text reads a struck AC"
got=$(printf '%s\n' '- ~~**AC3**~~ — removed in v2 (CR-001):' '  no longer needed' | lib 'ac_text AC3')
assert_eq "- ~~**AC3**~~ — removed in v2 (CR-001): no longer needed" "$got" "ac_text AC3"

# --- validate.sh _shall: "shall" on a continuation line counts
t "_shall accepts an AC whose 'shall' is on its wrapped line"
spec "shall answer it."
out=$(lib '_e=0; _shall specs/001-demo/spec.md AC1; echo "rc=$_e"')
assert_contains "$out" "rc=0" "_shall"

t "_shall still rejects an AC with no 'shall' at all"
spec "answers it."
out=$(lib '_e=0; _shall specs/001-demo/spec.md AC1; echo "rc=$_e"')
assert_contains "$out" "must state an observable result" "_shall"

# --- validate.sh check_cr_applied: a continuation-line edit applies a MODIFIED delta
spec "shall answer it."
git add specs && git commit -q -m "docs(001-demo): spec v1"
PREV=$(git rev-parse HEAD)
cat > specs/001-demo/changes/CR-001.md <<'EOF'
---
cr: CR-001
---
# CR-001

## Delta
- MODIFIED AC1 — When a request arrives, the system shall answer it within 1s.
EOF

t "check_cr_applied sees an edit made only on the wrapped line"
spec "shall answer it within 1s."
out=$(lib '_e=0; check_cr_applied specs/001-demo/changes/CR-001.md specs/001-demo/spec.md; echo "rc=$_e"')
assert_not_contains "$out" "is unchanged" "check_cr_applied"

t "check_cr_applied still refuses when AC1 really is unchanged"
spec "shall answer it."
out=$(lib '_e=0; check_cr_applied specs/001-demo/changes/CR-001.md specs/001-demo/spec.md; echo "rc=$_e"')
assert_contains "$out" "is unchanged" "check_cr_applied"

# --- loop.sh adopt_delta: a continuation-line edit becomes a MODIFIED line with the full text
sed -n '/^adopt_delta() {/,/^}/p' .claude/scripts/loop.sh > adopt.sh
t "adopt_delta reports an edit made only on the wrapped line"
spec "shall answer it within 1s."
out=$(lib ". ./adopt.sh; adopt_delta specs/001-demo/spec.md $PREV")
assert_contains "$out" "- MODIFIED AC1 — When a request arrives, the system shall answer it within 1s. (was: When a request arrives, the system shall answer it.)" "adopt_delta"

t "adopt_delta is silent when nothing changed"
spec "shall answer it."
out=$(lib ". ./adopt.sh; adopt_delta specs/001-demo/spec.md $PREV")
assert_eq "" "$out" "adopt_delta"
rm -f adopt.sh

# --- lib.sh contract_changes: the message names the AC whose wrapped line changed
t "contract_changes names an AC changed only on its wrapped line"
spec "shall answer it within 1s."
out=$(lib "contract_changes specs/001-demo/spec.md $PREV")
assert_contains "$out" "AC1 changed" "contract_changes"

t "contract_changes does not name an AC that is unchanged"
spec "shall answer it."
sed -i.bak 's/^A demo\.$/A changed demo./' specs/001-demo/spec.md && rm -f specs/001-demo/spec.md.bak
out=$(lib "contract_changes specs/001-demo/spec.md $PREV")
assert_not_contains "$out" "AC1" "contract_changes"

# --- loop.sh task: the task pack shows a wrapped AC in full
git switch -q -c feat/001-demo
spec "shall answer it within 1s."
cat > specs/001-demo/tasks.md <<'EOF'
---
feature: 001-demo
status: draft
version: 1
---
# Tasks — 001-demo

## Tasks

### T001 — Demo task
- Do: answer requests
- Tests: TestDemo
- AC: AC1
- Commit: feat(demo): answer requests
- Depends: —
- Size: S
- Risk: low
EOF
t "the task pack shows a wrapped AC in full"
out=$(.claude/scripts/loop.sh task T001 2>&1)
assert_contains "$out" "- **AC1** — When a request arrives, the system shall answer it within 1s." "loop.sh task"

done_tests
