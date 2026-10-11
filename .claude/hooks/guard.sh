#!/usr/bin/env bash
# guard.sh — PreToolUse hook (Bash, Edit, Write, MultiEdit, NotebookEdit, Read, Agent).
#
# Opt-in enforcement. The role comes from the hook input's agent_type (the subagent's
# name; "main" when absent):
#
#   everyone      approve.sh is human-only, and nobody writes approval fields
#                 (status: approved, sha256, fingerprint, ...) into specs/ files.
#   main session  free — unless it holds a run flag (.agent-loop/<f>/lock with its session
#                 id): then it orchestrates only (no code edits, no git writes, loop.sh calls).
#   other agents  (Explore, general-purpose, your own agents) treated like the main session
#                 outside a run: free, bar the two rules above.
#   kit agents    always constrained, run or no run:
#     reviewer, spec-critic          read-only: no edits; single allow-listed read commands.
#     planner, impact-analyst    read-only except their own files (plan.md + tasks.md /
#                 changes/CR-*.md), and the planner only once the spec is approved.
#     implementer, quick-builder     code anywhere except specs/ (bar research.md), .claude/,
#                 PROTECTED_GLOBS; git writes only in the exact shapes a task commit needs.
#     all of them: no .claude/ or .agent-loop/ writes, no reading secrets (.env*, keys).
#
# Decisions: deny (blocked, reason shown to Claude), allow (only for the exact shapes
# below), or no output (normal permission flow). Needs jq: without it every call is
# denied, so a missing jq can't silently switch the policy off.
set -u
input=$(cat)

emit() { jq -cn --arg d "$1" --arg r "agent-loop: $2" '{hookSpecificOutput:{hookEventName:"PreToolUse",permissionDecision:$d,permissionDecisionReason:$r}}'; exit 0; }
deny() { emit deny "$*"; }
allow() { emit allow "$*"; }
pass() { exit 0; }

if ! command -v jq >/dev/null 2>&1; then
	printf '%s\n' '{"hookSpecificOutput":{"hookEventName":"PreToolUse","permissionDecision":"deny","permissionDecisionReason":"agent-loop guard needs jq on PATH. Install jq (or remove the agent-loop hooks from .claude/settings.json)."}}'
	exit 0
fi

tool=$(jq -r '.tool_name // ""' <<<"$input")
role=$(jq -r '.agent_type // "main"' <<<"$input")
cwd=$(jq -r '.cwd // ""' <<<"$input")
sid=$(jq -r '.session_id // ""' <<<"$input")

case $role in
	implementer | quick-builder) class=builder ;;
	reviewer | spec-critic) class=reader ;;
	planner | impact-analyst) class=writer ;;
	main) class=main ;;
	*) class=other ;;   # built-in or your own agents: free outside the kit's rules for everyone
esac
kit() { case $class in builder | reader | writer) return 0 ;; esac; return 1; }

[ -n "$cwd" ] && cd "$cwd" 2>/dev/null
git rev-parse --show-toplevel >/dev/null 2>&1 || pass   # not a git repo: no policy
# shellcheck source=../scripts/lib.sh
. "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/../scripts/lib.sh"
al_init
[ -n "${AGENT_LOOP_DEBUG:-}" ] && { mkdir -p "$STATE_ROOT"; printf '%s guard %s\n' "$(date -u +%H:%M:%S)" "$input" >> "$STATE_ROOT/hook-debug.log"; }

# the main session orchestrates a run only while it holds that run's flag
locked=0
if [ "$class" = main ] && [ -n "$sid" ] && [ -d "$STATE_ROOT" ]; then
	for l in "$STATE_ROOT"/*/lock; do
		[ -f "$l" ] || continue
		read -r lsid _ < "$l" || true
		[ "$lsid" = "$sid" ] && locked=1
	done
fi
paused_now=0
for pf in "$STATE_ROOT"/*/paused; do [ -f "$pf" ] && grep -q '^mode=now' "$pf" && paused_now=1; done
if [ $paused_now = 1 ] && kit; then
	deny "agent-loop is paused (the user typed pause now). Stop here: make no more changes and end your turn with a final message whose first line is PAUSED, then one line on what is unfinished. Your uncommitted work stays in the tree."
fi
RUNMSG="this session is running an agent-loop build: it only dispatches agents and runs loop.sh. To work normally, interrupt with Esc (or type any message) — the run pauses; /al-resume continues it."

secret() { # path -> 0 if it looks like a secret file
	local b=${1##*/}
	case $b in .env | .env.* | *.pem | *.key | id_rsa* | id_ed25519*) return 0 ;; esac
	return 1
}

# ------------------------------------------------------------------------------- Read
if [ "$tool" = Read ]; then
	kit || pass
	p=$(jq -r '.tool_input.file_path // ""' <<<"$input")
	secret "$p" && deny "$role may not read secret files (${p##*/}). If the task needs a value from it, say so in your report."
	pass
fi

# ------------------------------------------------------------------------------ Agent
if [ "$tool" = Agent ] || [ "$tool" = Task ]; then
	if [ $paused_now = 1 ]; then
		case $(jq -r '.tool_input.subagent_type // ""' <<<"$input") in
			implementer | quick-builder | reviewer | planner | impact-analyst | spec-critic)
				deny "agent-loop is paused (pause now): don't dispatch. Run .claude/scripts/loop.sh next — it answers ACTION pause." ;;
		esac
	fi
	pass
fi

# ------------------------------------------------------------------------------- Bash
if [ "$tool" = Bash ]; then
	cmd=$(jq -r '.tool_input.command // ""' <<<"$input")
	# accept ./.claude/... and /abs/repo/.claude/... spellings of the kit scripts
	case $cmd in "./.claude/"*) cmd=${cmd#./} ;; "$REPO/.claude/"*) cmd=${cmd#"$REPO"/} ;; esac
	has() { printf '%s' "$cmd" | grep -Eq -- "$1"; }
	# the command with harmless redirections removed (2>&1, >/dev/null), for write detection
	clean=$(printf '%s' "$cmd" | sed -E 's#[0-9]*>&[0-9]+##g; s#[0-9]*>>?[[:space:]]*/dev/null##g')
	writes() { printf '%s' "$clean" | grep -Eq '>|(^|[^[:alnum:]_.-])(rm|mv|cp|tee|truncate|dd|chmod|chown|ln|install|rsync|touch|patch)([^[:alnum:]_-]|$)|sed[^|;&]*[[:space:]]-[a-zA-Z]*i|perl[^|;&]*[[:space:]]-[a-zA-Z]*i|(python|python3|node|ruby)[[:space:]]+-[ce]'; }
	single() { ! printf '%s' "$clean" | grep -Eq '[;&|<>`]|\$\(' && [ "$(printf '%s' "$cmd" | wc -l | tr -d ' ')" = 0 ]; }
	loopsh() { has '^\.claude/scripts/loop\.sh( |$)'; }

	has 'approve\.sh' && deny "approve.sh is human-only — the user types /al-approve (or runs it in a terminal)."
	has '(^|[^[:alnum:]_-])git[[:space:]].*commit.*(: approve |: auto-approve )' \
		&& deny "approval commits are made by approve.sh only — the user types /al-approve."

	GIT_WRITE='push|reset|rebase|checkout|switch|merge|tag|worktree|config|update-ref|filter-branch|filter-repo|clean|cherry-pick|revert|remote|fetch|pull|gc|prune|replace|reflog|submodule|am|apply|notes'

	if ! kit; then
		[ $locked = 1 ] || pass
		loopsh && single && allow "loop.sh $(printf '%s' "$cmd" | awk '{ print $2 }')"
		has "(^|[^[:alnum:]_-])git([[:space:]]+(-[^[:space:]]+|[^[:space:]-][^[:space:]]*=[^[:space:]]*))*[[:space:]]+($GIT_WRITE|add|commit|restore|stash|rm|mv)([[:space:]]|\$)" && deny "$RUNMSG"
		writes && deny "$RUNMSG"
		pass
	fi

	# --- kit agents from here on
	set -f
	for w in $(printf '%s' "$cmd" | sed "s/[\"'=<>|;&()]/ /g"); do
		secret "$w" && deny "$role may not read secret files (${w##*/}). If the task needs a value from it, say so in your report."
	done
	set +f
	# a mention of .claude/ or .agent-loop/ only counts when it isn't a loop.sh call or the commit-message scratch files
	kitrest=$(printf '%s' "$cmd" | sed -E 's#(\./|/[^[:space:]]*/)?\.claude/scripts/loop\.sh##g')
	staterest=$(printf '%s' "$cmd" | sed -E 's#\.agent-loop/(commit-msg|note|scratch/[^[:space:]]*)##g')
	if printf '%s' "$kitrest" | grep -q '\.claude/' && writes; then deny "the agent-loop kit under .claude/ is not edited by agents."; fi
	if printf '%s' "$staterest" | grep -q '\.agent-loop' && writes; then deny ".agent-loop/ holds loop state; only loop.sh writes it."; fi

	# loop.sh is the loop's own tool: approve well-formed single calls per role, whatever the spelling
	if [ "$class" = reader ] && has '^\.claude/scripts/loop\.sh verify([[:space:]]|$)'; then
		deny "reviewers don't run verify — loop.sh already did; review-info prints its result (VERIFY line)."
	fi
	if loopsh && single; then
		sub=$(printf '%s' "$cmd" | awk '{ print $2 }')
		case $class:$sub in
			builder:test | builder:status | builder:check | builder:task | builder:findings | builder:post-check | builder:review-info | builder:impact | builder:resolve | builder:lineage | builder:report | builder:config)
				allow "loop.sh $sub" ;;
		esac
	fi

	gitsub() { # the git subcommand of a single command, skipping global options
		set -f; set -- $cmd; set +f
		[ "${1:-}" = git ] || return 0
		shift
		while [ $# -gt 0 ]; do
			case $1 in -C | -c | --git-dir | --work-tree | --namespace) shift 2 ;; -*) shift ;; *) printf '%s' "$1"; return 0 ;; esac
		done
	}

	case $class in
	reader | writer)
		single || deny "$role runs single read-only commands only (no ; & | < > \` \$( or newlines)."
		has '(^|[[:space:]])(-exec|-execdir|-delete|-ok|-okdir|-fprint|-fprint0|-fls|-fprintf|--output|-o|-coverprofile|-cpuprofile|-memprofile|-trace|-toolexec|-outputdir)([[:space:]=]|$)' \
			&& deny "$role may not write files or run other programs (flag not allowed)."
		if has '^git([[:space:]]+(-C[[:space:]]+[^[:space:]]+|--no-pager))*[[:space:]]+(status|diff|show|log|blame|ls-files|ls-tree|rev-parse|rev-list|merge-base|cat-file|grep|shortlog|describe|notes show)([[:space:]]|$)' \
			|| has '^git branch( (-a|-r|-v|-vv|--all|--list|--remotes|--show-current|--contains|--merged|--no-merged)( [^-][^[:space:]]*)?)*$' \
			|| has '^\.claude/scripts/loop\.sh (status|check|task|findings|review-info|test|impact|resolve|lineage|report|config)([[:space:]]|$)' \
			|| has '^(ls|cat|head|tail|wc|grep|rg|find|tree|stat|file|diff|du|pwd|which|echo)([[:space:]]|$)'; then
			allow "$role read-only command"
		fi
		[ -n "$READONLY_EXTRA_CMDS" ] && has "^($READONLY_EXTRA_CMDS)([[:space:]]|\$)" && allow "$role read-only command (READONLY_EXTRA_CMDS)"
		deny "$role is read-only: git status/diff/show/log/blame/ls-files, loop.sh status|check|task|review-info|test|impact|config, ls/cat/grep/rg/find only."
		;;
	builder)
		has '(^|[^[:alnum:]_-])git[[:space:]].*(--git-dir|--work-tree)|(^|[^[:alnum:]_-])git[[:space:]]+-C[[:space:]]' && deny "$role may not point git at another directory."
		has "(^|[^[:alnum:]_-])git([[:space:]]+(-[^[:space:]]+|[^[:space:]-][^[:space:]]*=[^[:space:]]*))*[[:space:]]+($GIT_WRITE)([[:space:]]|\$)" && deny "$role may not run that git command (push/reset/checkout/switch/rebase/merge/tag/config/... are the human's)."
		has '(^|[^[:alnum:]_-])git[[:space:]]+branch([[:space:]]+[^[:space:]]+)*[[:space:]]+(-[a-zA-Z]*[dDmMcCf][a-zA-Z]*|--delete|--move|--copy|--force)([[:space:]]|$)' && deny "$role may not delete or move branches."
		has '(^|[^[:alnum:]_-])git[[:space:]]+stash[[:space:]]+(drop|clear|pop|apply|branch)' && deny "$role may only 'git stash push' (never drop/pop/apply)."
		has '(^|[^[:alnum:]_-])git[[:space:]]+commit([[:space:]]+[^[:space:]]+)*[[:space:]]+(--no-verify|-[a-zA-Z]*n[a-zA-Z]*)([[:space:]]|$)' && deny "$role may not skip git hooks (--no-verify / -n)."
		has '(^|[^[:alnum:]_-])git[[:space:]]+add([[:space:]]+[^[:space:]]+)*[[:space:]]+(-[a-zA-Z]*[Afu][a-zA-Z]*|--all|--force|--update|\.|\./|:/|\*)([[:space:]]|$)' && deny "$role must 'git add' explicit paths (no -A, ., -f, -u)."
		has '^\.claude/scripts/loop\.sh (start|next|log|stop|finish|unlock|new|cr-new|gate|pause|answer|accept)([[:space:]]|$)' && deny "that loop.sh command belongs to the orchestrator, not to $role."
		has '^\.claude/scripts/loop\.sh verify([[:space:]]|$)' && deny "the script runs the full verify once per task after you report DONE — run targeted tests instead: .claude/scripts/loop.sh test <args> (or the repo's test command for the packages you touched)."
		has '(^|[^[:alnum:]_-])rm[[:space:]]+(-[a-zA-Z]*[rR][a-zA-Z]*[[:space:]]+)*(/|~|\$HOME|\.\.?)([[:space:]]|/?$)' && deny "$role may not delete the repo, home or root."
		if single; then
			case $(gitsub) in
				add) allow "task commit: git add <paths>" ;;
				commit)
					has '^git commit( --amend)? -F \.agent-loop/commit-msg$' && allow "task commit"
					has '^git commit --amend --no-edit$' && allow "task commit (amend)"
					deny "commit with: git commit -F .agent-loop/commit-msg   (or --amend -F / --amend --no-edit). Write the message, with its trailers, to .agent-loop/commit-msg first."
					;;
				restore) has '^git restore( --staged| --worktree| --source=HEAD)*( [^-][^[:space:]]*)+$' && allow "discard own change" ;;
				stash) has "^git stash push -u -m (\"[^\"]*\"|'[^']*')\$" && allow "stash a blocked attempt" ;;
			esac
		else
			has '(^|[^[:alnum:]_-])git[[:space:]]+(add|commit|restore|stash)([[:space:]]|$)' && deny "run git add/commit/restore/stash as a single command (no chaining), so the guard can check it."
		fi
		pass
		;;
	esac
	pass
fi

# ----------------------------------------------------------------- Edit / Write / ...
case $tool in Edit | Write | MultiEdit | NotebookEdit) ;; *) pass ;; esac

paths=$(jq -r '[.tool_input | .. | objects | (.file_path?, .notebook_path?) | strings] | unique | .[]' <<<"$input")
content=$(jq -r '[.tool_input | .. | objects | (.content?, .new_string?, .file_text?, .new_code?, .new_source?) | strings] | join("\n")' <<<"$input")
removed=$(jq -r '[.tool_input | .. | objects | .old_string? | strings] | join("\n")' <<<"$input")

APPROVAL_FIELDS='^(status:[[:space:]]*approved[[:space:]]*$|(approved|approved-by|sha256|fingerprint|spec-fingerprint|plan-sha256|plan-fingerprint|applied|spec-applied|plan-applied):[[:space:]]*[^[:space:]])'

sets_approval() { # file -> 0 if this write ADDS or CHANGES an approval field (keeping existing ones is fine)
	local before new l
	if [ "$tool" = Write ]; then
		before=""; [ -f "$1" ] && before=$(awk 'NR == 1 && $0 != "---" { exit } NR > 1 && /^---[[:space:]]*$/ { exit } { print }' "$1")
		new=$(printf '%s\n' "$content" | awk 'NR == 1 && $0 != "---" { exit } NR > 1 && /^---[[:space:]]*$/ { exit } { print }')
	else
		before=$removed; new=$content
	fi
	while IFS= read -r l; do
		[ -n "$l" ] || continue
		printf '%s\n' "$before" | grep -qxF -- "$l" || return 0
	done <<EOF
$(printf '%s\n' "$new" | sed 's/[[:space:]]*$//' | grep -E "$APPROVAL_FIELDS")
EOF
	return 1
}

check_file() { # rel path
	local rel=$1 f sub name st
	case $rel in */../* | ../* | */..) kit && deny "use a normalised path (no '..'): $rel" ;; esac

	case $rel in
	"$SPECS_DIR"/*/*.md | "$SPECS_DIR"/*/changes/*.md)
		sets_approval "$REPO/$rel" \
			&& deny "only the user approves: leave status: approved and the approval stamps under it as they are. The user types /al-approve."
		;;
	esac

	if ! kit; then
		if [ $locked = 1 ]; then
			case $rel in "$SPECS_DIR"/*/research.md) return 0 ;; esac
			deny "$RUNMSG"
		fi
		return 0
	fi

	# --- kit agents from here on
	secret "$rel" && deny "$role may not touch secret files ($rel)."
	case $rel in
	.claude/*) deny "agents never edit .claude/ ($rel)." ;;
	.agent-loop/commit-msg | .agent-loop/note | .agent-loop/scratch/*)
		case $class in reader | writer) deny "$role is read-only." ;; esac
		return 0 ;;
	.agent-loop/*) deny ".agent-loop/ holds loop state; only loop.sh writes it." ;;
	esac

	case $rel in
	"$SPECS_DIR"/*/*)
		sub=${rel#"$SPECS_DIR"/}; F=${sub%%/*}; name=${sub#*/}
		f="$REPO/$rel"
		case $name in
			spec.md | plan.md | tasks.md | brief.md | changes/CR-*.md)
				if [ -f "$f" ]; then
					st=$(fm_get "$f" status)
					[ "$st" = approved ] && deny "$rel is approved. Agents don't change approved files; the user changes them (/al-change)."
				fi ;;
		esac
		case $class in reader) deny "$role is read-only." ;; esac
		case $name in
			research.md) return 0 ;;
			spec.md | brief.md) deny "only the main session writes $name (with the user, via /al-spec or /al-quick)." ;;
			plan.md)
				case $role in
					planner) chain_errors spec >/dev/null || deny "spec.md of $F is not approved — the planner stops here. The user must /al-approve spec first." ;;
					*) deny "only the planner writes plan.md." ;;
				esac ;;
			tasks.md)
				case $role in
					planner) chain_errors spec >/dev/null || deny "spec.md of $F is not approved — the planner stops here. The user must /al-approve spec first." ;;
					*) deny "only the planner writes tasks.md." ;;
				esac ;;
			changes/CR-*.md) [ "$role" = impact-analyst ] || deny "only the impact-analyst writes change requests." ;;
			*) [ "$role" = planner ] || deny "$role may only write research.md under $SPECS_DIR/." ;;
		esac
		return 0 ;;
	"$SPECS_DIR"/*) deny "$role may not write $rel." ;;
	esac

	# ordinary repository files
	case $class in
		reader | writer) deny "$role is read-only outside its own spec file." ;;
		builder) match_globs "$rel" "$PROTECTED_GLOBS" && deny "$rel is protected (loop.conf PROTECTED_GLOBS) — report it as BLOCKED if the task needs it." ;;
	esac
	return 0
}

while IFS= read -r p; do
	[ -n "$p" ] || continue
	case $p in /*) ;; *) p="$cwd/$p" ;; esac
	case $p in
		"$REPO"/*) check_file "${p#"$REPO"/}" ;;
		*) case $class in reader | writer) deny "$role may not write outside the repository." ;; esac ;;
	esac
done <<EOF
$paths
EOF
pass
