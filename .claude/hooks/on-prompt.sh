#!/usr/bin/env bash
# on-prompt.sh — UserPromptSubmit hook: a message you type pauses this session's build.
#
# While a build runs, the main session is the orchestrator and the guard keeps it from
# editing code. As soon as you type something that isn't a kit command, the run is
# paused (the current agent finishes its task; "pause now" stops agents at their next
# tool call) and the run flag is released, so Claude can do what you asked.
# /al-resume continues the build. A prompt typed while a turn is running reaches this
# hook at the orchestrator's next tool boundary; after Esc it reaches it at once.
set -u
input=$(cat)
command -v jq >/dev/null 2>&1 || exit 0
prompt=$(jq -r '.prompt // ""' <<<"$input")
sid=$(jq -r '.session_id // ""' <<<"$input")
cwd=$(jq -r '.cwd // ""' <<<"$input")
[ -n "$sid" ] || exit 0
[ -n "$cwd" ] && cd "$cwd" 2>/dev/null
r=$(git rev-parse --show-toplevel 2>/dev/null) || exit 0
[ -n "${AGENT_LOOP_DEBUG:-}" ] && { mkdir -p "$r/.agent-loop" && printf '%s on-prompt %s\n' "$(date -u +%H:%M:%S)" "$input" >> "$r/.agent-loop/hook-debug.log"; }

# not typed by the user: Claude Code delivers background-agent results and command output as
# prompts too (no field says so, the text does) — they must not pause the build
case $(printf '%s' "$prompt" | awk 'NF { sub(/^[[:space:]]+/, ""); print; exit }') in
	"<task-notification>"* | "<local-command-"* | "<command-name>"* | "<command-message>"* | "<system-reminder>"* | "<bash-input>"* | "<bash-stdout>"* | "<bash-stderr>"*) exit 0 ;;
esac

# kit commands (all of them start with /al-) manage the run themselves; anything else is a plain prompt
first=$(printf '%s' "$prompt" | awk 'NF { print $1; exit }')
case $first in
	/al-* | /*:al-*) exit 0 ;;
esac

held=0
for l in "$r"/.agent-loop/*/lock; do
	[ -f "$l" ] || continue
	read -r lsid _ < "$l" || true
	[ "$lsid" = "$sid" ] && held=1
done
[ $held = 1 ] || exit 0

now=""
case $(printf '%s' "$prompt" | tr '[:upper:]' '[:lower:]' | sed 's/[[:space:][:punct:]]*$//; s/^[[:space:]]*//') in
	"pause now" | "stop now" | stop) now=--now ;;
esac
out=$("$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/../scripts/loop.sh" pause --session "$sid" $now 2>&1)
jq -cn --arg c "agent-loop: the build was paused because the user sent a message ($out). If an agent is still working, wait for it, log its result with loop.sh log as usual, then stop dispatching: the next loop.sh call says ACTION pause. Then do what the user asked — you are no longer the orchestrator. /al-resume continues the build." \
	'{hookSpecificOutput:{hookEventName:"UserPromptSubmit", additionalContext:$c}}'
exit 0
