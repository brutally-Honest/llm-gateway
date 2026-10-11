#!/usr/bin/env bash
# The harness itself: mk_repo isolates the kit in a temp repo, so the scripts' REPO and
# state root resolve there and not in the real repo.
set -u
. "$(dirname "${BASH_SOURCE[0]}")/helpers.sh"
real_repo=$(cd "$KIT_SRC/.." && pwd)
mk_repo

t "temp repo is not the real repo"
assert_eq "$TMP_REPO" "$(git rev-parse --show-toplevel)" "toplevel"

t "kit copy present without the tests"
[ -x .claude/scripts/loop.sh ] && [ ! -e .claude/scripts/tests ] && pass || fail "kit copy wrong"

t "state root resolves inside the temp repo"
got=$(bash -c '. .claude/scripts/lib.sh; al_init; printf %s "$STATE_ROOT"')
assert_eq "$TMP_REPO/.agent-loop" "$got" "STATE_ROOT"

t "state root is not the real one"
assert_not_contains "$got" "$real_repo/.agent-loop" "STATE_ROOT"

done_tests
