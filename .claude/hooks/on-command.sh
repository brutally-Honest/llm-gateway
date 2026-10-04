#!/usr/bin/env bash
# on-command.sh — UserPromptExpansion hook for the workflow commands you type.
#
# Fires only when YOU type the command (UserPromptExpansion never fires for Claude's own
# tool calls, and every workflow skill sets disable-model-invocation), before Claude sees it:
#   /al-approve ...             runs approve.sh with your arguments. Refused -> the command is blocked
#                               and you see why; approved -> approve.sh's output goes to Claude's context.
#   /al-plan                    runs `loop.sh gate plan`        } a failed gate blocks the command with
#   /al-implement, /al-resume   runs `loop.sh gate implement`   } its reason, so no agent starts.
#   /al-change                  runs `loop.sh gate change`; when nothing is built yet it reopens the
#                               artifact right here (approve.sh reopen — your keystroke covers it).
#   /al-fix                     runs `loop.sh gate fix`
#   /al-answer Qn <text>        records your answer (loop.sh answer)
#   /al-status [--config]       runs `loop.sh status` (and `config`)
#   /al-pause [now]             pauses the build of this session (loop.sh pause)
# Kit commands all start with al- (al-spec and al-quick need no hook), so a skill of yours
# named e.g. "fix" never reaches this hook's logic.
# The skills run the same gates again as a backstop.
set -u
input=$(cat)
command -v jq >/dev/null 2>&1 || exit 0
name=$(jq -r '.command_name // ""' <<<"$input")
args=$(jq -r '.command_args // ""' <<<"$input")
cwd=$(jq -r '.cwd // ""' <<<"$input")
sid=$(jq -r '.session_id // ""' <<<"$input")
name=${name##*:}; name=${name##*/}
case $name in al-approve | al-plan | al-implement | al-resume | al-change | al-fix | al-answer | al-status | al-pause) ;; *) exit 0 ;; esac
name=${name#al-}
[ -n "$cwd" ] && cd "$cwd" 2>/dev/null
git rev-parse --show-toplevel >/dev/null 2>&1 || exit 0
[ -n "${AGENT_LOOP_DEBUG:-}" ] && { r=$(git rev-parse --show-toplevel 2>/dev/null) && mkdir -p "$r/.agent-loop" && printf '%s on-command %s\n' "$(date -u +%H:%M:%S)" "$input" >> "$r/.agent-loop/hook-debug.log"; }

scripts="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/../scripts"
L="$scripts/loop.sh"

block() { jq -cn --arg r "$1" '{decision:"block", reason:$r}'; exit 0; }
context() { jq -cn --arg c "$1" '{hookSpecificOutput:{hookEventName:"UserPromptExpansion", additionalContext:$c}}'; exit 0; }
field() { printf '%s\n' "$1" | awk -v k="$2" '$1 == k { $1 = ""; sub(/^ /, ""); print; exit }'; }

set -f
case $name in
approve)
	# shellcheck disable=SC2086
	out=$("$scripts/approve.sh" $args 2>&1) || block "Not approved. approve.sh said:
$out"
	context "approve.sh output (the user's /al-approve was applied by the hook):
$out" ;;
plan)
	out=$("$L" gate plan 2>&1) || block "/al-$name stopped by its gate — nothing ran:
$out" ;;
implement | resume)
	# shellcheck disable=SC2086
	out=$("$L" gate implement $args 2>&1) || block "/al-$name stopped by its gate — nothing ran:
$out" ;;
status)
	out=$("$L" status 2>&1)
	case " $args " in *" --config "*) out="$out

$("$L" config 2>&1)" ;; esac
	context "agent-loop status, produced by the hook — show it to the user exactly as it is:
$out" ;;
pause)
	now=""; case " $args " in *" now "* | *" --now "*) now=--now ;; esac
	if [ -n "$sid" ]; then out=$("$L" pause --session "$sid" $now 2>&1); else out=$("$L" pause $now 2>&1); fi || block "$out"
	context "agent-loop pause, applied by the hook: $out" ;;
change)
	# shellcheck disable=SC2086
	out=$("$L" gate change $args 2>&1) || block "/al-$name stopped by its gate — nothing changed:
$out"
	[ -n "$sid" ] && "$L" pause --session "$sid" >/dev/null 2>&1
	if [ "$(field "$out" MODE)" = reopen ]; then
		what=$(field "$out" TARGET)
		req=$(printf '%s\n' "$args" | sed -E 's/(^|[[:space:]])--(adopt|reconcile)([[:space:]]|$)/ /g; s/^[[:space:]]*(spec|plan|tasks)[[:space:]]+//; s/^[[:space:]]+//; s/[[:space:]]+$//')
		rout=$("$scripts/approve.sh" reopen "$what" --reason "${req:-changed with /al-change}" 2>&1) || block "Nothing was reopened. approve.sh said:
$rout"
		out="$out
$rout"
	fi
	context "/al-change gate (applied by the hook from the user's keystroke):
$out" ;;
fix)
	out=$("$L" gate fix 2>&1) || block "/al-fix stopped by its gate — nothing changed:
$out"
	context "/al-fix gate:
$out" ;;
answer)
	# shellcheck disable=SC2086
	out=$("$L" answer $args 2>&1) || block "The answer was not recorded:
$out"
	context "loop.sh answer, applied by the hook from the user's keystroke:
$out" ;;
esac
exit 0
