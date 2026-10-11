#!/usr/bin/env bash
# Bug 2: the guard lets an implementer commit with "cd <repo root> && git commit -F
# .agent-loop/commit-msg" as well as the bare command, and nothing looser.
set -u
. "$(dirname "${BASH_SOURCE[0]}")/helpers.sh"
mk_repo
ROOT=$(git rev-parse --show-toplevel)

decision() { # command [role] -> allow | deny | pass (no decision: normal permission flow)
	local out
	out=$(jq -cn --arg c "$1" --arg r "${2:-implementer}" --arg d "$ROOT" \
		'{tool_name:"Bash", agent_type:$r, cwd:$d, session_id:"test", tool_input:{command:$c}}' \
		| .claude/hooks/guard.sh)
	[ -n "$out" ] || { echo pass; return; }
	jq -r '.hookSpecificOutput.permissionDecision' <<<"$out"
}

t "bare task commit is allowed"
assert_eq allow "$(decision 'git commit -F .agent-loop/commit-msg')"

t "task commit from the literal repo root is allowed"
assert_eq allow "$(decision "cd $ROOT && git commit -F .agent-loop/commit-msg")"

t "cd to another directory stays blocked"
assert_eq deny "$(decision 'cd /tmp && git commit -F .agent-loop/commit-msg')"

t "a command chained after the commit stays blocked"
assert_eq deny "$(decision "cd $ROOT && git commit -F .agent-loop/commit-msg && git push")"

t "another message file stays blocked"
assert_eq deny "$(decision "cd $ROOT && git commit -F other-file")"

t "a ; instead of && stays blocked"
assert_eq deny "$(decision "cd $ROOT; git commit -F .agent-loop/commit-msg")"

t "a pipe after the commit stays blocked"
assert_eq deny "$(decision "cd $ROOT && git commit -F .agent-loop/commit-msg | tee x")"

t "a subdirectory of the root stays blocked"
assert_eq deny "$(decision "cd $ROOT/sub && git commit -F .agent-loop/commit-msg")"

t "the exception is for builders only: a reviewer stays blocked"
assert_eq deny "$(decision "cd $ROOT && git commit -F .agent-loop/commit-msg" reviewer)"

t "a quoted root path stays blocked"
assert_eq deny "$(decision "cd \"$ROOT\" && git commit -F .agent-loop/commit-msg")"

# the two fix-round amends
for amend in "git commit --amend -F .agent-loop/commit-msg" "git commit --amend --no-edit"; do
	t "'$amend' from the literal repo root is allowed"
	assert_eq allow "$(decision "cd $ROOT && $amend")"

	t "'$amend' chained with another command stays blocked"
	assert_eq deny "$(decision "cd $ROOT && $amend && git push")"

	t "'$amend' piped stays blocked"
	assert_eq deny "$(decision "cd $ROOT && $amend | tee x")"

	t "'$amend' after another cd path stays blocked"
	assert_eq deny "$(decision "cd /tmp && $amend")"

	t "'$amend' after a quoted root stays blocked"
	assert_eq deny "$(decision "cd \"$ROOT\" && $amend")"
done

t "implementer.md tells the agent to run the commit on its own"
assert_contains "$(cat .claude/agents/implementer.md)" "no cd, no &&, no other command" "implementer.md"

t "quick-builder.md tells the agent to run the commit on its own"
assert_contains "$(cat .claude/agents/quick-builder.md)" "no cd, no &&, no other command" "quick-builder.md"

done_tests
