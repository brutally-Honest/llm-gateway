#!/bin/sh
# PreToolUse hook for the /run-feature subagents. Each agent file wires it in its
# own frontmatter, so it only runs while that agent is active.
#
#   agent-bash-guard.sh implementer  approves the git writes a task commit needs;
#                                    anything else falls through to normal prompts.
#   agent-bash-guard.sh reviewer     approves read-only commands and denies the rest,
#                                    so the reviewer's read-only rule is enforced.
#
# Needs jq. Without it, the implementer falls back to prompts and the reviewer is
# denied everything (fail closed).

role="$1"

decide() {
	# $1: allow | deny, $2: reason
	printf '{"hookSpecificOutput":{"hookEventName":"PreToolUse","permissionDecision":"%s","permissionDecisionReason":"%s"}}\n' "$1" "$2"
	exit 0
}

input=$(cat)

if ! command -v jq >/dev/null 2>&1; then
	[ "$role" = reviewer ] && decide deny "agent-bash-guard needs jq; install it to run the reviewer"
	exit 0
fi

cmd=$(printf '%s' "$input" | jq -r '.tool_input.command // empty')

# Only single, plain commands are ever approved. Chaining, pipes, redirects,
# substitutions and multi-line input fall through (implementer) or are denied
# (reviewer), so an approved prefix can't smuggle in a second command.
case "$cmd" in
	*';'* | *'&'* | *'|'* | *'>'* | *'<'* | *'`'* | *'$('* | *'
'*)
		[ "$role" = reviewer ] && decide deny "reviewer runs single read-only commands only"
		exit 0
		;;
esac

case "$role" in
implementer)
	case "$cmd" in
		'git add '* | 'git commit -F '* | 'git commit --amend -F '* | \
			'git notes add '* | 'git stash push '* | 'git restore '*)
			decide allow "implementer task commit"
			;;
	esac
	exit 0
	;;
reviewer)
	case "$cmd" in
		*--output* | *'-o '* | *-o=* | *-outputdir* | *-coverprofile* | *-cpuprofile* | \
			*-memprofile* | *-trace* | *-exec* | *-toolexec*)
			decide deny "reviewer may not write files or run other programs"
			;;
		'make verify' | 'git status' | 'git status '* | 'git diff' | 'git diff '* | \
			'git show '* | 'git log' | 'git log '* | 'git notes show '* | 'go test '*)
			decide allow "reviewer read-only command"
			;;
	esac
	decide deny "reviewer is read-only: git diff/show/log/status, git notes show, go test and make verify only"
	;;
*)
	exit 0
	;;
esac
