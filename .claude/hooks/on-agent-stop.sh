#!/usr/bin/env bash
# on-agent-stop.sh — SubagentStop hook: the output contract of every loop agent.
#
# An agent cannot finish until its final message has the agreed first line AND the
# repository is in the state that line claims. Otherwise the stop is blocked and the
# agent gets the exact problem to fix (up to 3 times; then it is let go, the failure is
# logged, and loop.sh stops the run on the next step).
#
#   implementer / quick-builder  DONE|BLOCKED|NEEDS-HUMAN <task> ...  + post-task checks
#   reviewer                     PASS|FIX|ESCALATE (alone on line 1) + numbered findings for FIX
#   planner                      PLAN-DRAFTED <questions> <tasks>  + plan.md and tasks.md pass the draft checks
#   impact-analyst               CR-DRAFTED CR-nnn + the change request passes the checks
#   spec-critic                  GAPS <n> | CLEAN
set -u
input=$(cat)
command -v jq >/dev/null 2>&1 || exit 0
role=$(jq -r '.agent_type // ""' <<<"$input")
aid=$(jq -r '.agent_id // "unknown"' <<<"$input")
cwd=$(jq -r '.cwd // ""' <<<"$input")
msg=$(jq -r '.last_assistant_message // ""' <<<"$input")

case $role in implementer | quick-builder | reviewer | planner | impact-analyst | spec-critic) ;; *) exit 0 ;; esac
[ -n "$cwd" ] && cd "$cwd" 2>/dev/null
git rev-parse --show-toplevel >/dev/null 2>&1 || exit 0
HOOKS=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
SCRIPTS="$HOOKS/../scripts"
# shellcheck source=../scripts/lib.sh
. "$SCRIPTS/lib.sh"
# shellcheck source=../scripts/validate.sh
. "$SCRIPTS/validate.sh"
al_init
[ -n "${AGENT_LOOP_DEBUG:-}" ] && { mkdir -p "$STATE_ROOT"; printf '%s agent-stop %s\n' "$(date -u +%H:%M:%S)" "$input" >> "$STATE_ROOT/hook-debug.log"; }
F=$( (resolve_feature && printf '%s' "$F") 2>/dev/null ) || F=""
[ -n "$F" ] || exit 0   # used outside a feature branch: no contract to enforce

first=$(printf '%s\n' "$msg" | awk 'NF { print; exit }' | sed -E 's/^[[:space:]>#*`_]+//; s/[*`_[:space:]]+$//')
# "pause now": the agent was told to stop mid-task; its report is not held to the contract
if [ -f "$STATE_ROOT/$F/paused" ] && grep -q '^mode=now' "$STATE_ROOT/$F/paused"; then exit 0; fi
rdir="$STATE_ROOT/$F/hook-retries"; mkdir -p "$rdir"
rfile="$rdir/$aid"
n=$(cat "$rfile" 2>/dev/null || echo 0)

block() {
	n=$((n + 1)); echo "$n" > "$rfile"
	if [ "$n" -gt 3 ]; then
		log_event "CONTRACT $role $aid gave up after 3 blocked stops: $(printf '%s' "$1" | head -1)"
		exit 0
	fi
	jq -cn --arg r "agent-loop contract ($role): $1" \
		'{decision:"block", reason:$r, hookSpecificOutput:{hookEventName:"SubagentStop", additionalContext:$r}}'
	exit 0
}
ok() { rm -f "$rfile"; exit 0; }

case $role in
implementer | quick-builder)
	printf '%s' "$first" | grep -Eq '^(DONE|BLOCKED|NEEDS-HUMAN) (T[0-9]{3}|Q)( [A-Za-z0-9?]+)?$' \
		|| block "your final message must start with exactly one plain line: 'DONE <task> <sha>', 'BLOCKED <task> <Qn>' or 'NEEDS-HUMAN <task> <sha|none>'. It started with: '$first'"
	set -f; set -- $first; set +f
	verdict=$1 id=$2 third=${3:-}
	if [ "$role" = quick-builder ] && [ "$id" != Q ]; then block "the quick-builder works on unit Q: 'DONE Q <sha>'"; fi
	if [ "$role" = implementer ] && [ "$id" = Q ]; then block "the implementer works on tasks (Tnnn), not Q"; fi
	case $verdict in
		DONE)
			out=$(bash "$SCRIPTS/loop.sh" post-check "$id" 2>&1) \
				|| block "you reported DONE but the post-task checks fail. Fix these, then finish again:
$out" ;;
		BLOCKED)
			printf '%s' "$third" | grep -Eq '^Q[0-9]+$' || block "BLOCKED needs the question id: 'BLOCKED $id Qn'"
			open_questions | grep -qx "$third" \
				|| block "$third is not in research.md as '- **$third** (open) $id — <question>' under '## Open questions'"
			tree_clean || block "leave the tree clean: commit research.md alone (docs commit), then stash your attempt: git stash push -u -m \"$id blocked on $third\"" ;;
		NEEDS-HUMAN)
			tree_clean || block "leave the tree clean: commit your code with the task trailers (verify green), or stash it" ;;
	esac
	ok ;;
reviewer)
	printf '%s' "$first" | grep -Eq '^(PASS|FIX|ESCALATE)$' \
		|| block "your final message must start with a line that is exactly PASS, FIX or ESCALATE (nothing else on that line). It started with: '$first'"
	case $first in
		FIX) printf '%s\n' "$msg" | grep -Eq '^[[:space:]]*[0-9]+[.)][[:space:]]' || block "FIX needs numbered must-fix findings: '1. path/to/file.go:42 — what is wrong and why'" ;;
		ESCALATE) [ "$(printf '%s\n' "$msg" | grep -c '[^[:space:]]')" -ge 2 ] || block "ESCALATE must say what the human has to decide" ;;
	esac
	ok ;;
planner)
	printf '%s' "$first" | grep -Eq '^PLAN-DRAFTED [0-9]+ [0-9]+$' || block "your final message must start with 'PLAN-DRAFTED <undecided open questions> <number of tasks>'. It started with: '$first'"
	if [ "$(art_state "$(art plan)")" != approved ]; then
		out=$(check_plan "$(art plan)" draft) || block "plan.md fails the checks. Fix, then finish again:
$out"
	fi
	out=$(check_tasks "$(art tasks)" draft) || block "tasks.md fails the checks. Fix, then finish again:
$out"
	ok ;;
impact-analyst)
	printf '%s' "$first" | grep -Eq '^CR-DRAFTED CR-[0-9]{3}$' || block "your final message must start with 'CR-DRAFTED CR-nnn'. It started with: '$first'"
	cr=${first#CR-DRAFTED }
	out=$(check_change "$SPECS_DIR/$F/changes/$cr.md") || block "$cr fails the checks. Fix, then finish again:
$out"
	ok ;;
spec-critic)
	printf '%s' "$first" | grep -Eq '^(GAPS [0-9]+|CLEAN)$' || block "your final message must start with 'GAPS <n>' or 'CLEAN'. It started with: '$first'"
	ok ;;
esac
exit 0
