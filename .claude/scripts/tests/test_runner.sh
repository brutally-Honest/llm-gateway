#!/usr/bin/env bash
# run.sh's guard on the real .agent-loop/: a missing folder that stays missing is
# unchanged; a folder that appears or changes during the tests fails the run.
set -u
. "$(dirname "${BASH_SOURCE[0]}")/helpers.sh"

ROOT=$(mktemp -d "${TMPDIR:-/tmp}/al-runner.XXXXXX") || exit 1
trap 'rm -rf "$ROOT"' EXIT
mkdir -p "$ROOT/.claude/scripts/tests"
cp "$KIT_SRC/scripts/tests/run.sh" "$KIT_SRC/scripts/tests/helpers.sh" "$ROOT/.claude/scripts/tests/"

fake_test() { # body -> the only test_*.sh in the fake runner
	rm -f "$ROOT/.claude/scripts/tests/"test_*.sh
	printf '#!/usr/bin/env bash\n%s\n' "$1" > "$ROOT/.claude/scripts/tests/test_fake.sh"
}

t "missing .agent-loop/ that stays missing passes"
fake_test 'exit 0'
out=$(bash "$ROOT/.claude/scripts/tests/run.sh" 2>&1); rc=$?
assert_eq 0 "$rc" "exit code ($out)"

t "existing .agent-loop/ left alone passes"
mkdir -p "$ROOT/.agent-loop/f"; echo base > "$ROOT/.agent-loop/f/base.T001"
out=$(bash "$ROOT/.claude/scripts/tests/run.sh" 2>&1); rc=$?
assert_eq 0 "$rc" "exit code ($out)"

t "a changed .agent-loop/ file fails"
fake_test "echo other > '$ROOT/.agent-loop/f/base.T001'"
out=$(bash "$ROOT/.claude/scripts/tests/run.sh" 2>&1); rc=$?
assert_contains "$out" "the real .agent-loop/ changed" "runner output (rc $rc)"

t "an .agent-loop/ created during the tests fails"
rm -rf "$ROOT/.agent-loop"
fake_test "mkdir -p '$ROOT/.agent-loop' && touch '$ROOT/.agent-loop/x'"
out=$(bash "$ROOT/.claude/scripts/tests/run.sh" 2>&1); rc=$?
assert_contains "$out" "the real .agent-loop/ changed" "runner output (rc $rc)"

# the run also lints every .sh under .claude/ with "shellcheck -S warning"
rm -rf "$ROOT/.agent-loop"
t "a shellcheck finding in any kit script fails the run"
mkdir -p "$ROOT/.claude/hooks"
printf '#!/usr/bin/env bash\ncd /tmp\necho hi\n' > "$ROOT/.claude/hooks/bad.sh"
fake_test 'exit 0'
out=$(bash "$ROOT/.claude/scripts/tests/run.sh" 2>&1); rc=$?
assert_eq 1 "$rc" "exit code ($out)"
assert_contains "$out" "SC2164" "runner output"

t "session worktrees and install backups under .claude/ are not linted"
mkdir -p "$ROOT/.claude/worktrees/w" "$ROOT/.claude/agent-loop-backup-1"
cp "$ROOT/.claude/hooks/bad.sh" "$ROOT/.claude/worktrees/w/"; mv "$ROOT/.claude/hooks/bad.sh" "$ROOT/.claude/agent-loop-backup-1/"
out=$(bash "$ROOT/.claude/scripts/tests/run.sh" 2>&1); rc=$?
assert_eq 0 "$rc" "exit code ($out)"

t "without shellcheck the run fails and says to install it"
out=$(SHELLCHECK=/no/such/shellcheck bash "$ROOT/.claude/scripts/tests/run.sh" 2>&1); rc=$?
assert_eq 1 "$rc" "exit code ($out)"
assert_contains "$out" "install shellcheck" "runner output"

done_tests
