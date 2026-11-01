#!/usr/bin/env bash
# A hook whose working directory can't be entered: guard.sh fails closed (denies the call
# and says why), the other hooks do nothing and exit 0.
set -u
. "$(dirname "${BASH_SOURCE[0]}")/helpers.sh"
mk_repo
GONE="$TMP_REPO/no-such-dir"

guard() { # tool role -> the hook's JSON output
	jq -cn --arg t "$1" --arg r "$2" --arg d "$GONE" \
		'{tool_name:$t, agent_type:$r, cwd:$d, session_id:"test", tool_input:{command:"ls", file_path:"x"}}' | .claude/hooks/guard.sh
}

for tool in Bash Read Edit; do
	for role in main implementer; do
		t "guard denies $tool for $role when it can't cd to the cwd"
		out=$(guard "$tool" "$role")
		assert_eq deny "$(jq -r '.hookSpecificOutput.permissionDecision' <<<"$out" 2>/dev/null)" "decision ($out)"
	done
done

t "the guard's reason says why"
assert_contains "$(jq -r '.hookSpecificOutput.permissionDecisionReason' <<<"$(guard Bash main)")" "can't enter the working directory $GONE" "reason"

t "the guard still works from a directory that exists"
out=$(jq -cn --arg d "$TMP_REPO" '{tool_name:"Bash", agent_type:"main", cwd:$d, session_id:"test", tool_input:{command:"ls"}}' | .claude/hooks/guard.sh)
assert_eq "" "$out" "guard output"

quiet() { # hook json -> "<exit code>|<output>"
	local out rc
	out=$(printf '%s' "$2" | ".claude/hooks/$1" 2>&1); rc=$?
	printf '%s|%s' "$rc" "$out"
}
t "on-agent-stop exits 0 quietly"
assert_eq "0|" "$(quiet on-agent-stop.sh "$(jq -cn --arg d "$GONE" '{agent_type:"implementer", cwd:$d, last_assistant_message:"DONE T001 x"}')")" "exit|output"
t "on-command exits 0 quietly"
assert_eq "0|" "$(quiet on-command.sh "$(jq -cn --arg d "$GONE" '{command_name:"al-status", command_args:"", cwd:$d, session_id:"test"}')")" "exit|output"
t "on-prompt exits 0 quietly"
assert_eq "0|" "$(quiet on-prompt.sh "$(jq -cn --arg d "$GONE" '{prompt:"hi", cwd:$d, session_id:"test"}')")" "exit|output"

done_tests
