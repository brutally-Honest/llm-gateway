#!/usr/bin/env bash
# run_verify keeps verify's output out of .agent-loop/ until verify exits, so a
# VERIFY_CMD that runs the kit's own tests (whose runner fails when .agent-loop/
# changes during the run) goes green under the loop, and the log still lands at VLOG.
set -u
. "$(dirname "${BASH_SOURCE[0]}")/helpers.sh"

mk_repo
mkdir -p .claude/scripts/tests
cp "$KIT_SRC/scripts/tests/run.sh" "$KIT_SRC/scripts/tests/helpers.sh" .claude/scripts/tests/
printf '#!/usr/bin/env bash\necho fake kit test\n' > .claude/scripts/tests/test_fake.sh
printf 'VERIFY_CMD="bash .claude/scripts/tests/run.sh"\n' >> .claude/loop.conf
git add .claude && git commit -q -m "chore(repo): run the kit runner as verify"

t "verify that runs the kit runner goes green"
out=$(bash .claude/scripts/loop.sh verify 2>&1); rc=$?
assert_eq 0 "$rc" "exit code ($out)"

t "verify goes green again with a log already in .agent-loop/"
out=$(bash .claude/scripts/loop.sh verify 2>&1); rc=$?
assert_eq 0 "$rc" "exit code ($out)"

t "verify's output lands at VLOG"
assert_contains "$(cat .agent-loop/verify.log 2>&1)" "all kit tests passed" "verify.log"

done_tests
