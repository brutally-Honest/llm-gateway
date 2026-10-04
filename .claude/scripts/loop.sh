#!/usr/bin/env bash
# loop.sh — the deterministic half of the agent loop.
#
# The model dispatches agents; this script decides. Whether a task really finished,
# what runs next, how many fix rounds are left and when to stop are all decided here
# and printed as a final "ACTION ..." line for the orchestrator to follow.
#
# Usage: .claude/scripts/loop.sh <command> [args]   (run from the repo root)
#   status [feature]                  where things stand + the next step
#   config [TASK] | cfg KEY           every setting's effective value and where it came from
#   pause [--now]                     pause this repo's build (from any terminal); /al-resume continues
#   verify | test <args>              run VERIFY_CMD / TEST_CMD
#   impact <ACn...> | lineage | report | doctor | suggest-verify
#   check spec|plan|tasks|brief [--draft]  |  check change [CR-nnn]
# Used by the skills, hooks and agents:
#   new <kind> <slug> [--here|--worktree] [--quick] [--base REF] [--supersedes NNN]
#   gate plan|implement|change|fix   precondition check for a skill (exit 1 = stop)
#   start --session ID [flags] | next | log <ID> <agent> '<first line>' | stop '<reason>' | finish
#   task <ID> | findings <ID> | review-info <ID|BRANCH|Q> | post-check <ID>
#   answer <Qn> <text> | accept <ID> <reason> | dirty <ID> continue|discard|keep
#   add-fix <bug> | cr-new [--adopt] | resolve
set -uo pipefail
HERE=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
# shellcheck source=lib.sh
. "$HERE/lib.sh"
# shellcheck source=validate.sh
. "$HERE/validate.sh"
al_init

short() { git rev-parse --short "${1:-HEAD}" 2>/dev/null; }
soft_feature() { F=$( (resolve_feature "${1:-}" && printf '%s' "$F") 2>/dev/null ) && [ -n "$F" ]; }
VLOG() { if [ -n "${F:-}" ]; then ensure_state; printf '%s/verify.log' "$(sdir)"; else mkdir -p "$STATE_ROOT"; printf '%s/verify.log' "$STATE_ROOT"; fi; }

render() { # template dest   (uses F KIND TITLE SUP CR BRANCH); .claude/templates.local/<name> wins over the kit's
	local src="$AL_KIT_DIR/templates.local/$1"
	[ -f "$src" ] || src="$AL_KIT_DIR/templates/$1"
	[ -f "$src" ] || die "missing template $src"
	sed -e "s|{{FEATURE}}|$F|g" -e "s|{{KIND}}|${KIND:-}|g" -e "s|{{DATE}}|$(today)|g" -e "s|{{BRANCH}}|${BRANCH:-}|g" \
		-e "s|{{TITLE}}|${TITLE:-$F}|g" -e "s|{{SUPERSEDES}}|${SUP:-}|g" -e "s|{{CR}}|${CR:-}|g" -e 's/^\([a-z-]*\): $/\1:/' "$src" > "$2"
}

run_verify() { # [why] -> 0 = green. Records HEAD as green only when verify ran on a clean tree and left it clean.
	local log rc before
	log=$(VLOG)
	if [ -z "$VERIFY_CMD" ]; then { verify_unset_msg; echo; } > "$log"; return 1; fi # unset is red, never a silent green
	before=$(git status --porcelain)
	(bash -c "$VERIFY_CMD") > "$log" 2>&1; rc=$?
	if [ "$(git status --porcelain)" != "$before" ]; then
		printf '\nagent-loop: verify changed files in the working tree — verify must be read-only:\n%s\n' "$(git status --short)" >> "$log"
		rc=1
	fi
	if [ $rc = 0 ] && [ -z "$before" ] && [ -n "${F:-}" ]; then sfile_set green "$(git rev-parse HEAD)"; fi
	if [ -n "${F:-}" ] && [ -n "${1:-}" ]; then
		if [ $rc = 0 ]; then log_event "VERIFY green $1 at $(short)"; else log_event "VERIFY red $1 at $(short)"; fi
	fi
	return $rc
}

verify_due() { # id -> 0 when the script runs VERIFY_CMD right after this task
	local v n i t
	v=$(cfg VERIFY)
	[ "$v" = off ] && return 1
	[ "$1" = Q ] && return 0   # a quick change is one unit: its post-task verify is the final one
	case $(task_field "$(art tasks)" "$1" Verify | tr '[:upper:]' '[:lower:]') in full) return 0 ;; targeted) return 1 ;; esac
	case $v in
		task) return 0 ;;
		every-*)
			n=${v#every-}; i=0
			for t in $(task_ids "$(art tasks)"); do i=$((i + 1)); [ "$t" = "$1" ] && break; done
			[ $((i % n)) = 0 ] ;;
		*) return 1 ;;   # targeted | end: once, before the branch review
	esac
}

final_verify() { # -> 0 when HEAD is proven green (or VERIFY=off); else prints the ACTION that fixes it
	local last
	[ "$(cfg VERIFY)" = off ] && return 0
	[ "$(sfile_get green)" = "$(git rev-parse HEAD)" ] && return 0
	if run_verify "before finish"; then return 0; fi
	last=$(for t in $(task_ids "$(art tasks)"); do case $(state_get "$t") in PASS | NEEDS-HUMAN) echo "$t" ;; esac; done | tail -1)
	echo "verify is red on the finished branch ($VERIFY_CMD) — sending it back to $last:"
	tail -25 "$(VLOG)"
	sfile_set "findings.$last" "verify is red on the finished branch ($VERIFY_CMD):
$(tail -25 "$(VLOG)")"
	state_set "$last" IN-PROGRESS "final verify red"
	bump "$last" post-task
	return 1
}

autocommit_research() { # commit research.md when it is the only uncommitted change (your answers to Qn)
	local st r="$SPECS_DIR/$F/research.md"
	st=$(git status --porcelain); [ -n "$st" ] || return 0
	printf '%s\n' "$st" | grep -v " $r\$" | grep -q . && return 0
	git add -- "$r" && git commit -q -m "docs($F): update research notes" -- "$r" && echo "Committed your research.md changes."
}

draft_crs() { local c; for c in $(cr_files); do [ "$(fm_get "$c" status)" = draft ] && basename "$c" .md; done; return 0; }

# --- new ------------------------------------------------------------------------

next_number() {
	local max=0 n b
	b=$(base_branch)
	for n in $( {
		ls -1 "$SPECS_DIR" 2>/dev/null
		git ls-tree --name-only "$b" "$SPECS_DIR/" 2>/dev/null
		git for-each-ref --format='%(refname:short)' refs/heads
	} | awk '{ k = split($0, p, "/"); s = p[k]; if (s ~ /^[0-9][0-9][0-9]-/) print substr(s, 1, 3) }'); do
		n=$((10#$n)); [ "$n" -gt "$max" ] && max=$n
	done
	printf '%03d' $((max + 1))
}

cmd_new() {
	local kind="" slug="" wt=0 quick=0 here=0 base="" br target path d
	SUP=""
	while [ $# -gt 0 ]; do
		case $1 in
			--worktree) wt=1 ;; --quick) quick=1 ;; --here) here=1 ;;
			--base) base=${2:-}; shift ;;
			--supersedes) SUP=${2:-}; shift ;;
			-*) die "unknown flag $1" ;;
			*) if [ -z "$kind" ]; then kind=$1; elif [ -z "$slug" ]; then slug=$1; else die "unexpected argument '$1'"; fi ;;
		esac
		shift
	done
	case $kind in feat | fix | refactor | chore) ;; *) die "the kind of change must be feat, fix, refactor or chore (got '$kind').
  do this: pick one of those four" ;; esac
	printf '%s' "$slug" | grep -Eq '^[a-z0-9][a-z0-9-]{1,47}$' || die "the short name '$slug' won't work as a branch and folder name.
  do this: use 2-48 lowercase letters, digits and dashes, e.g. rate-limit"
	git ls-files --error-unmatch .claude/scripts/loop.sh >/dev/null 2>&1 \
		|| die "the agent-loop kit isn't committed yet, so a new branch wouldn't have it.
  do this: git add .claude .gitignore && git commit -m \"chore: add agent-loop kit\""
	base=${base:-$(base_branch)}
	git rev-parse --verify --quiet "$base^{commit}" >/dev/null || die "base '$base' not found"
	if [ -n "$SUP" ]; then
		d=$(git ls-tree --name-only "$base" "$SPECS_DIR/" 2>/dev/null | awk -v n="${SUP%%-*}" '{ k = split($0, p, "/"); if (index(p[k], n "-") == 1) { print p[k]; exit } }')
		[ -n "$d" ] || die "there is no feature $SUP merged into $base to supersede.
  do this: check the number (ls $SPECS_DIR on $base)"
		SUP=$d
	fi
	n=$(next_number); F="$n-$slug"; KIND=$kind
	TITLE=$(printf '%s' "$slug" | tr '-' ' '); TITLE="$(printf '%s' "${TITLE:0:1}" | tr '[:lower:]' '[:upper:]')${TITLE:1}"
	br="$kind/$F"
	if [ $here = 1 ]; then
		[ $wt = 0 ] || die "--here and --worktree don't go together: --here uses the branch you're on"
		br=$(current_branch) || die "--here needs a branch, but you're on a detached HEAD.
  do this: git switch -c <branch name>, then try again"
		[ "$br" != "$(base_branch)" ] || die "--here would put the feature on $br, the base branch.
  do this: git switch -c <branch name>, then try again (or leave out --here to get $kind/$F)"
		d=$( (resolve_feature && printf '%s' "$F") 2>/dev/null ) && die "branch $br already holds feature $d — one feature per branch.
  do this: git switch -c <another branch>, then try again"
		target=$REPO
	elif git show-ref --verify --quiet "refs/heads/$br"; then die "branch $br already exists.
  do this: git switch $br to work on it, or pick another short name"
	elif [ $wt = 1 ]; then
		path=".claude/worktrees/$F"
		git check-ignore -q "$path/x" || die ".claude/worktrees/ isn't gitignored, so the worktree would show up as changes.
  do this: echo '.claude/worktrees/' >> .gitignore && git commit -am \"chore: ignore worktrees\""
		git worktree add -q -b "$br" "$path" "$base" || die "git worktree add failed"
		target="$REPO/$path"
	else
		tree_clean || die "the working tree has uncommitted changes, and switching branches would carry them along.
  do this: commit or stash them — or use --here (stay on this branch) or --worktree"
		git switch -q -c "$br" "$base" || die "git switch -c $br $base failed"
		target=$REPO
	fi
	BRANCH=$br
	mkdir -p "$target/$SPECS_DIR/$F"
	if [ $quick = 1 ]; then render brief.md "$target/$SPECS_DIR/$F/brief.md"
	else render spec.md "$target/$SPECS_DIR/$F/spec.md"; fi
	render research.md "$target/$SPECS_DIR/$F/research.md"
	echo "FEATURE $F"
	echo "BRANCH $br"
	echo "DIR $SPECS_DIR/$F"
	[ $wt = 1 ] && echo "WORKTREE $target"
	return 0
}

# --- status ------------------------------------------------------------------------

art_line() { # name
	local f st v extra=""
	f=$(art "$1"); st=$(art_state "$f"); v=$(fm_get "$f" version)
	[ "$st" = missing ] || extra=" v${v:-1}"
	[ "$1" = plan ] && [ "$st" = draft ] && extra="$extra ($(section "$f" "Open questions" | grep -E '^[[:space:]]*[-*][[:space:]]*(\*\*)?Q[0-9]+' | grep -vc 'decided:') undecided questions)"
	printf '  %-9s %s%s\n' "$1.md" "$st" "$extra"
}

next_step() {
	local st q ce c
	if is_quick; then
		st=$(art_state "$(art brief)")
		case $st in
			draft) echo "review $(art brief), then /al-approve brief (it builds right after)" ;;
			approved) [ "$(state_get Q)" = PASS ] && echo "done — read the report (loop.sh report) and open the PR" || echo "/al-implement" ;;
			*) art_why "$(art brief)" "$st" ;;
		esac
		return
	fi
	c=$(draft_crs | head -1); [ -n "$c" ] && { echo "$c is waiting: /al-approve change (or edit it, or delete it)"; return; }
	st=$(art_state "$(art spec)")
	case $st in
		missing) echo "/al-spec <requirement>"; return ;;
		draft) echo "review $(art spec), then /al-approve spec (or keep refining with /al-spec)"; return ;;
		approved) ;;
		*) art_why "$(art spec)" "$st"; return ;;
	esac
	st=$(art_state "$(art plan)")
	case $st in
		missing) echo "/al-plan"; return ;;
		draft)
			if check_plan "$(art plan)" approve >/dev/null 2>&1 && check_tasks "$(art tasks)" >/dev/null 2>&1; then
				echo "review $(art plan) and tasks.md, then /al-approve plan (it approves the tasks too)"
			else echo "/al-plan to finish the draft plan (loop.sh check plan / check tasks list what's missing), then /al-approve plan"; fi
			return ;;
		approved) ;;
		*) art_why "$(art plan)" "$st"; return ;;
	esac
	ce=$(chain_errors plan) || { echo "$ce"; return; }
	st=$(art_state "$(art tasks)")
	[ "$st" = approved ] || { art_why "$(art tasks)" "$st"; return; }
	ce=$(chain_errors tasks) || { echo "$ce"; return; }
	q=$(open_questions | tr '\n' ' ')
	[ -n "$q" ] && { echo "answer $q: /al-answer <Qn> <your answer>, then /al-resume"; return; }
	for c in $(task_ids "$(art tasks)"); do
		case $(state_get "$c") in
			STOPPED) echo "$c hit the fix-round limit: its last attempt is committed on top of $(short "$(sfile_get "base.$c")"). Fix it by hand and /al-resume, or drop it (git reset --hard $(short "$(sfile_get "base.$c")")), edit the task in tasks.md, and /al-resume"; return ;;
			ESCALATED) echo "$c needs your decision (the reviewer's ESCALATE): /al-change the spec, or fix the code yourself; then /al-resume"; return ;;
		esac
	done
	for c in $(task_ids "$(art tasks)"); do
		case $(state_get "$c") in PASS | NEEDS-HUMAN) ;; *) in_list "$c" "$(done_tasks)" || { echo "/al-implement"; return; } ;; esac
	done
	local nh=""
	for c in $(task_ids "$(art tasks)"); do [ "$(state_get "$c")" = NEEDS-HUMAN ] && nh="$nh $c"; done
	st=$(state_get BRANCH); [ "$(cfg REVIEW)" = none ] && st=PASS
	case $st in
		PASS | FIX | ESCALATE | OFF) echo "done — read the report (loop.sh report)${nh:+, do the manual checks for$nh}, then open the PR" ;;
		*) echo "/al-implement (branch review pending)" ;;
	esac
}

cmd_status() {
	local id st c
	if ! soft_feature "${1:-}"; then
		echo "No feature on this branch ($(current_branch)). Start one: /al-spec <requirement>  or  /al-quick <small change>"
		return 0
	fi
	echo "Feature  $F  ($(fm_get "$(art spec)" kind)$(fm_get "$(art brief)" kind), branch $(current_branch), base $(base_branch))"
	if is_quick; then art_line brief; else
		art_line spec; art_line plan; art_line tasks
		for c in $(cr_files); do printf '  %-9s %s (scope %s)%s\n' "$(basename "$c" .md)" "$(art_state "$c")" "$(fm_get "$c" scope)" "$([ -n "$(fm_get "$c" applied)" ] && echo ", applied")"; done
	fi
	st=$(open_questions | tr '\n' ' '); [ -n "$st" ] && echo "  open questions: $st(research.md)"
	if [ -f "$(sdir)/paused" ]; then echo "Build: paused ($(cut -d' ' -f1 "$(sdir)/paused" | sed 's/mode=//')) — /al-resume continues"
	elif [ -f "$(sdir)/lock" ]; then echo "Build: running in session $(cut -d' ' -f1 "$(sdir)/lock") (type any message there to pause it)"; fi
	if is_quick; then
		[ -n "$(state_get Q)" ] && echo "Build: $(state_get Q)"
	elif [ -f "$(art tasks)" ]; then
		echo "Tasks"
		for id in $(task_ids "$(art tasks)"); do
			st=$(state_get "$id"); [ -n "$st" ] || { in_list "$id" "$(done_tasks)" && st=PASS || st=todo; }
			printf '  %s  %-12s %s\n' "$id" "$st" "$(task_title "$(art tasks)" "$id")"
		done
	fi
	echo "Next: $(next_step)"
}

# --- gates ------------------------------------------------------------------------

pending_crs() { local c; for c in $(cr_files); do [ "$(fm_get "$c" status)" = approved ] && [ -z "$(fm_get "$c" applied)" ] && basename "$c" .md; done; return 0; }

gate_plan() { # the planner writes plan.md and tasks.md; MODE tasks = the plan is approved, only tasks.md is open
	local e p t st tst mode=new
	calm_pause
	is_quick && die "$F is a /al-quick change: it has a brief, not a plan.
  do this: /al-approve brief (it builds right after), or /al-change to amend the brief"
	e=$(chain_errors spec) || die "the planner can't start yet:
  $e"
	p=$(art plan); t=$(art tasks); st=$(art_state "$p"); tst=$(art_state "$t")
	case $st in
		missing) render plan.md "$p" ;;
		draft) [ -n "$(fm_get "$p" previous)" ] && mode=amend ;;
		approved)
			case $tst in
				approved) die "plan.md and tasks.md are already approved.
  do this: /al-implement to build — or /al-change plan <what> to change them" ;;
				missing | draft) mode=tasks ;;
				*) die "$(art_why "$t" "$tst")" ;;
			esac ;;
		*) die "$(art_why "$p" "$st")" ;;
	esac
	[ -f "$t" ] || render tasks.md "$t"
	[ "$mode" = new ] && [ -n "$(fm_get "$t" previous)" ] && mode=amend
	echo "GATE plan: OK"
	echo "FEATURE $F"
	echo "MODE $mode"
	echo "MODEL $(cfg MODEL_PLANNER)"
	echo "DONE-TASKS $(done_tasks | tr '\n' ' ')"
	echo "CHANGE-REQUESTS $(pending_crs | tr '\n' ' ')"
}

gate_implement() {
	local e c br id st
	if is_quick; then e=$(chain_errors brief); else e=$(chain_errors tasks); fi || die "not ready to build:
$(printf '%s\n' "$e" | sed 's/^/  /')"
	c=$(draft_crs | head -1); [ -z "$c" ] || die "$c is waiting for your decision.
  do this: /al-approve change   (or edit it, or delete the file)"
	br=$(current_branch); [ "$br" != "$(base_branch)" ] || die "you're on $br, not on the feature's branch.
  do this: git switch <the feature's branch>"
	[ -n "$VERIFY_CMD" ] || die "$(verify_unset_msg)"
	autocommit_research >/dev/null
	if ! tree_clean; then
		c=$(interrupted_task)
		[ -n "$c" ] || die "the working tree has uncommitted changes: $(git status --short | head -5 | tr '\n' ' ')
  do this: commit or stash them, then /al-implement again"
		echo "NOTE $c was interrupted and left uncommitted work — you'll be asked: continue, discard or keep it"
	fi
	echo "GATE implement: OK"
	echo "FEATURE $F"
	echo "KIND $(fm_get "$(art spec)" kind)$(fm_get "$(art brief)" kind)"
	echo "VERIFY $VERIFY_CMD"
	echo "PROFILE $(cfg PROFILE)   REVIEW $(cfg REVIEW)   VERIFY $(cfg VERIFY)   FIX_ROUNDS $(cfg FIX_ROUNDS)"
	if is_quick; then echo "UNIT Q ($(state_get Q))"
	else
		for id in $(task_ids "$(art tasks)"); do
			st=$(state_get "$id"); [ -n "$st" ] || st=todo
			[ "$st" = PASS ] || echo "TASK $id $st — $(task_title "$(art tasks)" "$id")"
		done
	fi
}

built() { # 0 when any task (or Q) has been built: work exists that a change has to account for
	[ -n "$(done_tasks)" ] && return 0
	[ -f "$(sdir)/state" ] || return 1
	awk -F '\t' '{ s[$1] = $2 } END { for (k in s) if (k != "BRANCH" && s[k] ~ /^(IMPLEMENTED|PASS|NEEDS-HUMAN|STOPPED|ESCALATED)$/) f = 1; exit !f }' "$(sdir)/state"
}

merged() { # 0 when this feature's spec or brief is already on the base branch
	git cat-file -e "$(base_branch):$(art spec)" 2>/dev/null || git cat-file -e "$(base_branch):$(art brief)" 2>/dev/null
}

gate_change() { # [--adopt] [--reconcile] [spec|plan|tasks] <request> -> MODE edit | reopen | cr | adopt | reconcile
	local adopt=0 rec=0 what="" first=1 f st mode
	while [ $# -gt 0 ]; do
		case $1 in
			--adopt) adopt=1 ;;
			--reconcile) rec=1 ;;
			spec | plan | tasks) [ $first = 1 ] && what=$1; first=0 ;;
			*) first=0 ;;
		esac
		shift
	done
	if is_quick; then what=brief; else what=${what:-spec}; fi
	calm_pause
	merged && die "$F is already merged into $(base_branch), so it is history now.
  do this: /al-spec --supersedes ${F%%-*} <the change>   (a new feature that replaces it)"
	f=$(art "$what")
	[ -f "$f" ] || die "there is no $what.md in $F yet.
  do this: /al-plan"
	st=$(art_state "$f")
	case $st in
		draft) mode=edit ;;
		approved | changed)
			if [ $rec = 1 ]; then
				built || die "nothing is built yet, so there is no code to reconcile with the spec.
  do this: /al-change <what> (without --reconcile)"
				mode=reconcile
			elif built; then
				if [ $adopt = 1 ] || [ "$st" = changed ]; then mode=adopt; else mode=cr; fi
			else mode=reopen; fi ;;
		*) die "$(art_why "$f" "$st")" ;;
	esac
	if [ "$mode" = adopt ] && [ "$st" != changed ]; then
		die "${f##*/} has no edits since you approved it, so there is nothing to adopt.
  do this: edit it first, or /al-change <what> without --adopt"
	fi
	[ -f "$STATE_ROOT/$F/lock" ] && echo "NOTE a build is running for $F — it pauses now; /al-resume after the change"
	echo "GATE change: OK"
	echo "FEATURE $F"
	echo "TARGET $what"
	echo "FILE $f"
	echo "MODE $mode"
	echo "MODEL $(cfg MODEL_IMPACT)"
	echo "DONE-TASKS $(done_tasks | tr '\n' ' ')"
	echo "DRAFT-CR $(draft_crs | head -1)"
}

gate_fix() { # -> MODE task (add a fix task to this feature) | quick (a new fix/ branch from the base)
	if soft_feature; then
		if merged; then echo "GATE fix: OK"; echo "MODE quick"; echo "WHY $F is merged — the fix gets its own branch"; return 0; fi
		is_quick && die "$F is a /al-quick change: it has no task list to add a fix to.
  do this: /al-change <the fix>   (amends its brief)"
		echo "GATE fix: OK"; echo "FEATURE $F"; echo "MODE task"
		return 0
	fi
	echo "GATE fix: OK"; echo "MODE quick"; echo "WHY no feature on $(current_branch) — the fix gets its own branch"
}

cmd_gate() {
	local what=${1:-} feat="" flags=""; shift || true
	case $what in
		change | amend) resolve_feature; gate_change "$@"; return ;;
		fix) gate_fix; return ;;
	esac
	while [ $# -gt 0 ]; do
		case $1 in --*) flags="$flags $1 ${2:-}"; shift ;; *) feat=$1 ;; esac
		shift
	done
	resolve_feature "$feat"
	# shellcheck disable=SC2086
	if [ "$what" = implement ]; then parse_run_flags $flags; check_effective_cfg; fi
	case $what in
		plan | tasks) gate_plan ;; implement) gate_implement ;;
		*) die "usage: gate plan|implement|change|fix [feature]" ;;
	esac
}

cmd_check() {
	local what=${1:-} mode=approve f out rc
	shift || true
	[ "${1:-}" = --draft ] && { mode=draft; shift; }
	case $what in
		change)
			resolve_feature
			if [ -n "${1:-}" ]; then f="$SPECS_DIR/$F/changes/$1.md"; else f=$(for c in $(draft_crs); do echo "$SPECS_DIR/$F/changes/$c.md"; done | tail -1); fi
			[ -n "$f" ] || die "no draft change request; pass its id (CR-nnn)"
			out=$(check_change "$f"); rc=$? ;;
		spec | plan) resolve_feature "${1:-}"; f=$(art "$what"); out=$(check_"$what" "$f" "$mode"); rc=$? ;;
		tasks) resolve_feature "${1:-}"; f=$(art tasks); out=$(check_tasks "$f" "$mode"); rc=$? ;;
		brief) resolve_feature "${1:-}"; f=$(art brief); out=$(check_brief "$f"); rc=$? ;;
		*) die "usage: check spec|plan|tasks|brief [--draft] | check change [CR-nnn]" ;;
	esac
	if [ $rc = 0 ]; then echo "OK: $f passes the $mode checks"; [ -n "$out" ] && printf '%s\n' "$out"; else echo "NOT READY: $f"; printf '%s\n' "$out"; fi
	return $rc
}

# --- the loop ------------------------------------------------------------------------

pre_task() { # id -> 0 ok (base recorded) | 1 with STOP-REASON lines
	local id=$1 ce q
	autocommit_research >/dev/null
	tree_clean || { echo "STOP-REASON working tree not clean before $id:"; git status --short | head -10; return 1; }
	if is_quick; then ce=$(chain_errors brief); else ce=$(chain_errors tasks); fi \
		|| { echo "STOP-REASON approvals are not valid any more: $ce"; return 1; }
	q=$(draft_crs | head -1); [ -z "$q" ] || { echo "STOP-REASON $q is waiting for a decision"; return 1; }
	if [ "$(state_get "$id")" = BLOCKED ]; then
		q=$(state_detail "$id")
		if open_questions | grep -qx "$q"; then
			echo "STOP-REASON $q is still open in $(art research) — answer it (change '(open)' to '(answered)' and write the answer), then /al-implement"
			return 1
		fi
	fi
	# VERIFY=task proves every HEAD a task starts from (a commit of yours in between included);
	# the other policies verify less often on purpose, so a hand commit waits for their next run
	if [ "$(cfg VERIFY)" = task ] && [ "$(sfile_get green)" != "$(git rev-parse HEAD)" ]; then
		run_verify "before $id" || { echo "STOP-REASON verify is red at the start of $id — the previous change broke something:"; tail -20 "$(VLOG)"; return 1; }
	fi
	sfile_set "base.$id" "$(git rev-parse HEAD)"
	sfile_set "rounds.$id" 0
	rm -f "$(sdir)/findings.$id" "$(sdir)/watch.$id" "$(sdir)/reason.$id" "$(sdir)/end.$id"
	state_set "$id" IN-PROGRESS
	[ "$id" = Q ] || state_set BRANCH CLEARED
	log_event "$id pre-task ok base=$(short)"
	return 0
}

PE=0
pe() { printf '  - %s\n' "$*"; PE=1; }

post_task() { # id [quick|full] -> 0 ok | 1 + problems.  quick = no verify (used by the SubagentStop hook)
	local id=$1 mode=${2:-full} end base commits c msg p tests changed kind w="" skips ce
	PE=0
	base=$(sfile_get "base.$id"); [ -n "$base" ] || { pe "no base recorded for $id (pre-task never ran)"; return 1; }
	end=$(task_end "$id")
	tree_clean || pe "working tree not clean: $(git status --short | head -5 | tr '\n' ' ')"
	commits=$(git rev-list "$base..$end")
	[ -n "$commits" ] || pe "no new commit since $(short "$base")"
	if [ "$(cfg TRAILERS)" != off ]; then
		for c in $commits; do
			msg=$(git log -1 --format=%B "$c")
			printf '%s\n' "$msg" | grep -qx "Task: $id" || pe "commit $(short "$c") lacks the trailer 'Task: $id'"
			printf '%s\n' "$msg" | grep -qx "Feature: $F" || pe "commit $(short "$c") lacks the trailer 'Feature: $F'"
		done
	fi
	while IFS= read -r p; do
		[ -n "$p" ] || continue
		case $p in
			.claude/*) pe "changed $p — agents never touch the kit" ;;
			.agent-loop/*) pe "committed loop state $p" ;;
			"$SPECS_DIR"/*) [ "$p" = "$SPECS_DIR/$F/research.md" ] || pe "changed $p — under $SPECS_DIR/ only $SPECS_DIR/$F/research.md may change" ;;
		esac
		match_globs "$p" "$PROTECTED_GLOBS" && pe "changed protected file $p (loop.conf PROTECTED_GLOBS)"
		match_globs "$p" "$WATCHED_GLOBS" && w="${w}WATCH changed $p — reviewer: is this needed, and does it weaken a check?
"
	done <<EOF
$(git diff --name-only "$base" "$end")
EOF
	if is_quick; then tests=$(section "$(art brief)" "Steps" | grep 'Tests:' | grep -viE 'Tests:[[:space:]]*none')
	else tests=$(task_field "$(art tasks)" "$id" Tests); case $tests in [Nn]one* | '') tests="" ;; esac; fi
	if [ -n "$tests" ]; then
		changed=""
		while IFS= read -r p; do [ -n "$p" ] && match_globs "$p" "$TEST_GLOBS" && changed=1; done <<EOF
$(git diff --name-only --diff-filter=AM "$base" "$end")
EOF
		[ -n "$changed" ] || pe "the task names tests but no test file was added or changed (TEST_GLOBS in loop.conf)"
	fi
	while IFS= read -r p; do
		[ -n "$p" ] && match_globs "$p" "$TEST_GLOBS" && w="${w}WATCH deleted test file $p — reviewer: must be justified by the spec
"
	done <<EOF
$(git diff --name-only --diff-filter=D "$base" "$end")
EOF
	kind=$(fm_get "$(art spec)" kind)$(fm_get "$(art brief)" kind)
	if [ "$kind" = refactor ]; then
		while IFS= read -r p; do
			[ -n "$p" ] && match_globs "$p" "$TEST_GLOBS" && w="${w}WATCH refactor modified existing test $p — behaviour must not change
"
		done <<EOF
$(git diff --name-only --diff-filter=M "$base" "$end")
EOF
	fi
	skips=$(git diff "$base" "$end" -U0 | grep '^+' | grep -v '^+++' \
		| grep -E 't\.Skip\(|\.skip\(|(^|[^A-Za-z_])x(it|describe|test)\(|\.only\(|@Disabled|pytest\.mark\.skip|(it|test)\.todo\(|//[[:space:]]*nolint|eslint-disable' | head -5)
	[ -n "$skips" ] && w="${w}$(printf '%s\n' "$skips" | sed 's/^/WATCH added skip\/disable marker: /')
"
	if is_quick; then ce=$(chain_errors brief); else ce=$(chain_errors tasks); fi || pe "approved files no longer match their approval: $ce"
	if [ "$mode" = full ] && [ $PE = 0 ]; then
		if verify_due "$id"; then
			run_verify "after $id" || pe "verify is red ($VERIFY_CMD):
$(tail -25 "$(VLOG)")"
		else
			log_event "$id verify deferred (VERIFY=$(cfg VERIFY))"
		fi
	fi
	sfile_set "watch.$id" "$w"
	[ "$mode" = full ] && [ $PE = 0 ] && record_commits "$id" "$commits"
	[ -n "$w" ] && printf '%s' "$w"
	return $PE
}

record_commits() { # id "shas" -> .agent-loop/<f>/commits ("sha task" lines): the task<->commit map, trailers or not
	local m; m="$(sdir)/commits"; ensure_state
	{ [ -f "$m" ] && I="$1" awk '$2 != ENVIRON["I"]' "$m"; for c in $2; do printf '%s %s\n' "$c" "$1"; done; } > "$m.new" && mv "$m.new" "$m"
}

bump() { # id source -> prints ACTION
	local r max
	max=$(cfg FIX_ROUNDS)
	r=$(sfile_get "rounds.$1"); r=$(( ${r:-0} + 1 ))
	if [ "$r" -gt "$max" ]; then
		state_set "$1" STOPPED fix-limit
		log_event "$1 STOP fix-limit ($max fix rounds used)"
		echo "ACTION stop fix-limit $1"
	else
		sfile_set "rounds.$1" "$r"
		log_event "$1 fix round $r/$max ($2)"
		model_for "$1"
		echo "ACTION fix $1 $2 $r/$max model=$CV"
	fi
}

act_implement() { model_for "$1"; echo "ACTION implement $1 model=$CV"; }
act_review() { # every reviewer dispatch goes through here; REVIEW=none (from any layer) never reaches the echo
	if [ "$(cfg REVIEW)" = none ]; then
		log_event "BUG review of $1 requested while REVIEW=none — skipped"
		if [ "$1" = Q ]; then echo "ACTION finish"; else echo "ACTION next"; fi
		return 0
	fi
	if [ "$1" = BRANCH ]; then cfg_lookup MODEL_BRANCH_REVIEWER; else cfg_lookup MODEL_REVIEWER; fi
	echo "ACTION review $1 model=$CV"
}

task_review() { [ "$1" != Q ] && [ -f "$(art tasks)" ] && task_field "$(art tasks)" "$1" Review | tr '[:upper:]' '[:lower:]'; return 0; }

review_reason() { # id -> one line why; returns 0 when the task gets a reviewer
	local id=$1 pol tr
	pol=$(cfg REVIEW)
	[ "$pol" = none ] && { echo "skipped: reviews are off (REVIEW=none)"; return 1; }
	tr=$(task_review "$id")
	case $tr in
		always) echo "reviewed: the task says Review: always"; return 0 ;;
		skip) echo "skipped: the task says Review: skip"; return 1 ;;
	esac
	# a quick change has no branch review, so its one review stands in for it: only REVIEW=none skips it
	[ "$id" = Q ] && { echo "reviewed: a quick change's review is its branch review"; return 0; }
	case $pol in
		every) echo "reviewed: REVIEW=every"; return 0 ;;
		branch) echo "skipped: REVIEW=branch (the branch review covers it)"; return 1 ;;
	esac
	risk_reason "$id"
}

risk_reason() { # id -> REVIEW=risk: reviewed if high risk, big, watched, or touching REVIEW_GLOBS
	local id=$1 base n max w p globs
	if [ "$id" != Q ] && [ "$(task_field "$(art tasks)" "$id" Risk | tr '[:upper:]' '[:lower:]')" = high ]; then
		echo "reviewed: the task says Risk: high"; return 0
	fi
	base=$(sfile_get "base.$id")
	n=$(git diff --numstat "$base" "$(task_end "$id")" 2>/dev/null | awk '{ n += $1 + $2 } END { print n + 0 }')
	max=$(cfg REVIEW_LINES)
	[ "$n" -gt "$max" ] && { echo "reviewed: $n changed lines (REVIEW_LINES=$max)"; return 0; }
	w=$(sfile_get "watch.$id" | awk 'NF { print; exit }')
	[ -n "$w" ] && { echo "reviewed: ${w#WATCH }" | sed 's/ — reviewer:.*//'; return 0; }
	globs=$(cfg REVIEW_GLOBS)
	if [ -n "$globs" ]; then
		while IFS= read -r p; do
			[ -n "$p" ] && match_globs "$p" "$globs" && { echo "reviewed: touched $p (REVIEW_GLOBS)"; return 0; }
		done <<EOF
$(git diff --name-only "$base" "$(task_end "$id")" 2>/dev/null)
EOF
	fi
	echo "skipped: low risk ($n changed lines, no watched files)"
	return 1
}

after_implemented() { # id -> ACTION review, or PASS without a reviewer
	local why rc=0
	why=$(review_reason "$1") || rc=1
	sfile_set "reason.$1" "$why"
	if [ $rc = 0 ]; then
		log_event "$1 $why"; echo "$1 $why"; act_review "$1"
	else
		if [ "$1" = Q ] && [ "$(cfg REVIEW)" = none ]; then state_set Q PASS "reviews off"
		else state_set "$1" PASS "$(short)"; fi
		[ "$1" = Q ] || state_set BRANCH CLEARED
		log_event "$1 PASS without review — $why"
		echo "$1 passes without a review ($why)"
		if [ "$1" = Q ]; then echo "ACTION finish"; else echo "ACTION next"; fi
	fi
}

parse_run_flags() { # flags... -> RUN_CONF (KEY=value lines) or dies with the valid values
	local f v k m pair
	RUN_CONF=""
	_rc() { cfg_valid "$1" "$2" || die "--$3 '$2' is not valid — use one of: $(cfg_values "$1")"; RUN_CONF="$RUN_CONF$1=$2
"; }
	while [ $# -gt 0 ]; do
		f=$1; v=${2:-}
		case $f in
			--profile) _rc PROFILE "$v" profile ;;
			--review) _rc REVIEW "$v" review ;;
			--verify) _rc VERIFY "$v" verify ;;
			--fix-rounds) _rc FIX_ROUNDS "$v" fix-rounds ;;
			--mutation) _rc MUTATION "$v" mutation ;;
			--model)
				[ -n "$v" ] || die "--model needs a list, e.g. --model T002=haiku,implementer=sonnet,reviewer=opus"
				for pair in $(printf '%s' "$v" | tr ',' ' '); do
					k=${pair%%=*}; m=${pair#*=}
					[ "$k" != "$pair" ] || die "--model: '$pair' should be <task or agent>=<model>, e.g. T002=haiku"
					cfg_valid MODEL_IMPLEMENTER "$m" || die "--model $pair: model must be one of: haiku sonnet opus"
					case $k in
						T[0-9][0-9][0-9] | Q) RUN_CONF="${RUN_CONF}MODEL_$k=$m
" ;;
						implementer | reviewer | branch-reviewer | planner | quick | impact)
							RUN_CONF="${RUN_CONF}MODEL_$(printf '%s' "$k" | tr 'a-z-' 'A-Z_')=$m
" ;;
						*) die "--model $pair: '$k' is not a task id (T002) or an agent (implementer, reviewer, branch-reviewer, planner, quick, impact)" ;;
					esac
				done ;;
			*) die "unknown flag '$f' — /al-implement takes: --profile fast|balanced|strict, --review none|branch|risk|every, --verify targeted|task|every-N|end|off, --fix-rounds 0-3, --mutation off|risk|every, --model T002=haiku,reviewer=opus" ;;
		esac
		shift; [ $# -gt 0 ] && shift
	done
	return 0
}

check_effective_cfg() { # dies naming the first setting that is not valid, and where it was set
	local k
	for k in $CFG_KEYS; do
		cfg_lookup "$k"
		cfg_valid "$k" "$CV" || die "$k=$CV (set in: $CS) is not valid — use one of: $(cfg_values "$k")"
	done
}

cmd_config() { # [TASK]
	local t=${1:-} k v
	soft_feature || F=""
	echo "Settings${F:+ for $F} — first match wins: run flags > task fields > plan.md frontmatter > .claude/loop.conf > profile > kit default"
	for k in $CFG_KEYS; do cfg_lookup "$k"; printf '%s=%s (%s)\n' "$k" "$CV" "$CS"; done
	if [ -n "$t" ]; then
		[ -n "$F" ] || die "there's no feature on this branch, so there are no task settings to show.
  do this: git switch <the feature's branch>"
		if [ "$t" != Q ]; then task_block "$(art tasks)" "$t" | grep -q . || die "no task $t in $(art tasks)"; fi
		echo "Task $t"
		model_for "$t"; printf 'MODEL=%s (%s)\n' "$CV" "$CS"
		if [ "$t" != Q ]; then
			for k in Size Risk Review Verify; do
				v=$(task_field "$(art tasks)" "$t" "$k"); [ -n "$v" ] && printf '%s=%s (task)\n' "$(printf '%s' "$k" | tr '[:lower:]' '[:upper:]')" "$v"
			done
		fi
		printf 'REVIEWED=%s\n' "$(review_reason "$t")"
	fi
	return 0
}

cmd_start() {
	local sid="" feat="" out flags="" c
	while [ $# -gt 0 ]; do
		case $1 in
			--session) sid=${2:-}; shift ;;
			--*) flags="$flags $1 ${2:-}"; shift ;;
			*) feat=$1 ;;
		esac
		shift
	done
	resolve_feature "$feat"
	# shellcheck disable=SC2086
	out=$(parse_run_flags $flags 2>&1) || { printf '%s\n' "$out"; echo "ACTION stop flags"; exit 1; }
	out=$(check_effective_cfg 2>&1) || { printf '%s\n' "$out"; echo "ACTION stop config"; exit 1; }
	out=$(gate_implement 2>&1) || { printf '%s\n' "$out"; echo "ACTION stop gate"; exit 1; }
	ensure_state
	# shellcheck disable=SC2086
	parse_run_flags $flags
	if [ -n "$RUN_CONF" ]; then printf '%s' "$RUN_CONF" > "$(sdir)/run.conf"
	elif [ -f "$(sdir)/run.conf" ]; then echo "Using the flags of the interrupted run: $(tr '\n' ' ' < "$(sdir)/run.conf")"; fi
	case $sid in '' | *'$'* | *CLAUDE_SESSION_ID*) warn "no session id — the orchestrator write-lock is off for this run"; sid="" ;; esac
	ensure_state
	[ -f "$(sdir)/paused" ] && { log_event "RESUME ($(cut -d' ' -f1 "$(sdir)/paused"))"; rm -f "$(sdir)/paused"; }
	if [ -n "$sid" ]; then sfile_set lock "$sid $(now)"; fi
	log_event "RUN START head=$(short) session=${sid:-none}"
	if [ "$(cfg VERIFY)" = task ] && tree_clean && [ "$(sfile_get green)" != "$(git rev-parse HEAD)" ]; then
		echo "Running verify on HEAD: $VERIFY_CMD"
		if ! run_verify "before the first task"; then
			tail -30 "$(VLOG)"
			log_event "STOP verify red before the first task"
			rm -f "$(sdir)/lock"
			echo "verify is red on HEAD before any task ran — fix that first (log: $(VLOG))"
			echo "ACTION stop verify-red"
			exit 1
		fi
	fi
	printf '%s\n' "$out" | grep -E '^(TASK|UNIT) '
	if ! tree_clean; then
		c=$(interrupted_task)
		case $(state_detail "$c") in
			"restored after "*) echo "$c continues from its restored attempt"; model_for "$c"; echo "ACTION implement $c model=$CV"; return 0 ;;
		esac
		echo "$c was interrupted and left uncommitted work:"; git status --short | head -10 | sed 's/^/  /'
		echo "ACTION ask-dirty $c"
		return 0
	fi
	echo "ACTION next"
}

cmd_next() {
	local id st out
	resolve_feature
	if [ -f "$(sdir)/paused" ]; then
		release_lock; calm_pause; log_event "PAUSED at a task boundary"
		echo "Paused. /al-resume continues from here."; echo "ACTION pause"; return 0
	fi
	if is_quick; then
		st=$(state_get Q)
		case $st in
			PASS) echo "ACTION finish" ;;
			IMPLEMENTED) after_implemented Q ;;
			*) if out=$(pre_task Q); then act_implement Q; else printf '%s\n' "$out"; log_event "STOP pre-task Q"; echo "ACTION stop pre-task Q"; fi ;;
		esac
		return 0
	fi
	for id in $(task_ids "$(art tasks)"); do
		[ "$(state_get "$id")" = IMPLEMENTED ] && { echo "RESUME $id was implemented but not reviewed"; after_implemented "$id"; return 0; }
		if [ "$(state_get "$id")" = IN-PROGRESS ] && interrupted_with_commits "$id"; then
			echo "RESUME $id was interrupted after it committed — checking that work as if it had reported DONE"
			log_event "$id resumed with commits from before the pause"
			log_done "$id"; return 0
		fi
		if [ "$(state_get "$id")" = ESCALATED ]; then
			state_set "$id" IMPLEMENTED "re-review after your decision"
			if [ "$(cfg REVIEW)" = none ]; then
				echo "RESUME $id was escalated; reviews are off now (REVIEW=none), so it passes on the script's checks"
				after_implemented "$id"; return 0
			fi
			echo "RESUME $id was escalated — reviewing it again against the spec as it is now"
			log_event "$id re-review after the escalation"; act_review "$id"; return 0
		fi
	done
	for id in $(task_ids "$(art tasks)"); do
		st=$(state_get "$id")
		[ -z "$st" ] && in_list "$id" "$(done_tasks)" && continue
		case $st in PASS | NEEDS-HUMAN) continue ;; esac
		if out=$(pre_task "$id"); then act_implement "$id"
		else printf '%s\n' "$out"; log_event "STOP pre-task $id"; echo "ACTION stop pre-task $id"; fi
		return 0
	done
	final_verify || return 0
	[ "$(cfg REVIEW)" = none ] && { state_set BRANCH OFF; echo "ACTION finish"; return 0; }
	case $(state_get BRANCH) in PASS | FIX | ESCALATE) echo "ACTION finish" ;; *) act_review BRANCH ;; esac
}

cmd_log() {
	local id=${1:-} agent=${2:-} line=${3:-} verdict w2 w3 out q
	resolve_feature
	[ -n "$id" ] && [ -n "$agent" ] || die "usage: log <ID> <agent> '<first line of its final message>'"
	set -f; set -- $line; set +f
	verdict=${1:-}; w2=${2:-}; w3=${3:-}
	if pause_mode now; then
		log_event "$id $agent stopped by pause now: '$line'"; release_lock; calm_pause
		echo "Paused now: $id is left as it was; /al-resume decides what happens to its work."; echo "ACTION pause"; return 0
	fi
	case $agent in
		implementer | quick-builder)
			[ "$id" != BRANCH ] || die "BRANCH is reviewed, not implemented"
			if [ -n "$w2" ] && [ "$w2" != "$id" ]; then
				log_event "$id $agent CONTRACT reported '$line'"; echo "ACTION stop contract $id (agent reported another task: '$line')"; return 0
			fi
			case $(state_get "$id") in
				PASS | NEEDS-HUMAN | '') log_event "$id $agent CONTRACT result for a task that is not in progress"
					echo "ACTION stop contract $id ($id is not in progress — run loop.sh next to get the current action)"; return 0 ;;
			esac
			case $verdict in
				DONE) log_event "$id $agent DONE $(short)"; log_done "$id" ;;
				BLOCKED)
					q=$(printf '%s' "$w3" | grep -oE '^Q[0-9]+'); q=${q:-Q?}
					state_set "$id" BLOCKED "$q"; log_event "$id $agent BLOCKED $q"
					question_lines "$q"
					echo "ACTION ask $q $id" ;;
				NEEDS-HUMAN)
					tree_clean || { log_event "$id NEEDS-HUMAN with a dirty tree"; echo "ACTION stop dirty-tree $id"; return 0; }
					state_set "$id" NEEDS-HUMAN "$w3"; log_event "$id $agent NEEDS-HUMAN $w3"
					echo "ACTION next" ;;
				*) log_event "$id $agent CONTRACT '$line'"; echo "ACTION stop contract $id (first line was not DONE/BLOCKED/NEEDS-HUMAN)" ;;
			esac ;;
		reviewer)
			if [ "$id" = BRANCH ]; then
				case $verdict in PASS | FIX | ESCALATE) ;; *) verdict=UNCLEAR ;; esac
				state_set BRANCH "$verdict"; log_event "BRANCH reviewer $verdict"
				echo "ACTION finish"; return 0
			fi
			if [ "$verdict" != ESCALATE ] && [ "$(state_get "$id")" != IMPLEMENTED ]; then
				log_event "$id reviewer CONTRACT $verdict logged while $id is $(state_get "$id")"
				echo "ACTION stop contract $id (a review was logged for $id, but $id has not passed its post-task checks — it is $(state_get "$id"))"
				return 0
			fi
			case $verdict in
				PASS)
					state_set "$id" PASS "$(short)"; state_set BRANCH CLEARED
					log_event "$id reviewer PASS $(short)"
					if is_quick; then echo "ACTION finish"; else echo "ACTION next"; fi ;;
				FIX) log_event "$id reviewer FIX"; bump "$id" review ;;
				ESCALATE) state_set "$id" ESCALATED; log_event "$id reviewer ESCALATE"; echo "ACTION ask-escalate $id" ;;
				*) log_event "$id reviewer CONTRACT '$line'"; echo "ACTION stop contract $id (reviewer verdict was not PASS/FIX/ESCALATE)" ;;
			esac ;;
		*) die "agent must be implementer, quick-builder or reviewer" ;;
	esac
}

release_lock() { rm -f "$(sdir)/lock"; }

cmd_stop() { resolve_feature; log_event "STOP $*"; release_lock; cmd_report; }
cmd_finish() { resolve_feature; log_event "RUN END"; release_lock; cmd_report; rm -f "$(sdir)/run.conf"; }
log_done() { # id -> the script's checks on a reported (or resumed) DONE, then review or a fix round
	local id=$1 out
	if out=$(post_task "$id" full); then
		state_set "$id" IMPLEMENTED "$(short)"
		[ -n "$out" ] && printf '%s\n' "$out"
		after_implemented "$id"
	else
		sfile_set "findings.$id" "$out"
		echo "post-task checks failed for $id:"; printf '%s\n' "$out"
		log_event "$id post-task FAILED"
		bump "$id" post-task
	fi
}

pause_mode() { [ -f "$(sdir)/paused" ] && grep -q "^mode=$1" "$(sdir)/paused"; }   # graceful | now
calm_pause() { pause_mode now && sfile_set paused "mode=graceful $(now) (was now)"; return 0; }   # agents are back: stop denying them

interrupted_task() { # the task a pause or Esc left IN-PROGRESS (Q for quick), if any
	local id
	if is_quick; then [ "$(state_get Q)" = IN-PROGRESS ] && echo Q; return 0; fi
	for id in $(task_ids "$(art tasks)"); do [ "$(state_get "$id")" = IN-PROGRESS ] && { echo "$id"; return 0; }; done
	return 0
}
task_end() { local e; e=$(sfile_get "end.$1"); printf '%s\n' "${e:-HEAD}"; }   # where a task's commits end

interrupted_with_commits() { # id -> 0 when the interrupted task committed before the pause; its range then ends there
	local b e
	b=$(sfile_get "base.$1"); [ -n "$b" ] && tree_clean || return 1
	# your commits made while paused are yours, not the task's: the task's range ends where the pause found HEAD
	e=$(sfile_get pausehead)
	{ [ -n "$e" ] && git merge-base --is-ancestor "$b" "$e" 2>/dev/null; } || e=$(git rev-parse HEAD)
	[ -n "$(git rev-list "$b..$e" 2>/dev/null)" ] || return 1
	[ "$e" = "$(git rev-parse HEAD)" ] || sfile_set "end.$1" "$e"
	return 0
}

cmd_dirty() { # <ID> continue|discard|keep — what happens to the uncommitted work an interrupted task left behind
	local id=${1:-} how=${2:-}
	resolve_feature
	[ "$(state_get "$id")" = IN-PROGRESS ] || die "${id:-the task} was not interrupted."
	case $how in
		continue)
			log_event "$id continues from its uncommitted work"
			echo "$id continues: the implementer finishes the diff in the working tree"
			model_for "$id"; echo "ACTION implement $id model=$CV" ;;
		discard)
			git stash push -q -u -m "$id discarded on resume" || die "stashing the work failed"
			log_event "$id: uncommitted work discarded on resume (git stash: '$id discarded on resume')"
			echo "Stashed as '$id discarded on resume' (git stash list) — $id starts over"
			cmd_next ;;
		keep)
			log_event "$id: the user keeps its uncommitted work as their own change"
			release_lock
			echo "The work stays in the tree as your change. Commit it (or not) yourself; then /al-resume runs $id again from the new HEAD."
			echo "ACTION stop keep $id" ;;
		*) die "say what to do with $id's uncommitted work.
  do this: loop.sh dirty $id continue|discard|keep" ;;
	esac
}

cmd_pause() { # [--now] [--session ID] [feature] — the run stops at the next task boundary (--now: agents are stopped too)
	local mode=graceful sid="" feat="" l lsid fs=""
	while [ $# -gt 0 ]; do
		case $1 in --now) mode=now ;; --session) sid=${2:-}; shift ;; -*) die "unknown flag $1" ;; *) feat=$1 ;; esac
		shift
	done
	if [ -n "$sid" ]; then
		for l in "$STATE_ROOT"/*/lock; do
			[ -f "$l" ] || continue
			read -r lsid _ < "$l" || true
			[ "$lsid" = "$sid" ] && fs="$fs $(basename "$(dirname "$l")")"
		done
		[ -n "$fs" ] || { echo "no build is running in this session"; return 0; }
	else
		resolve_feature "$feat"; fs=$F
	fi
	for F in $fs; do
		sfile_set paused "mode=$mode $(now)"
		sfile_set pausehead "$(git rev-parse HEAD)"
		release_lock
		log_event "PAUSE requested ($mode)"
		echo "PAUSED $F ($mode) — /al-resume continues the build"
	done
}

cmd_unlock() { if soft_feature "${1:-}"; then release_lock; else rm -f "$STATE_ROOT"/*/lock; fi; echo "orchestrator lock released"; }

mutation_line() { # id -> whether the implementer runs a mutation check, and why
	local m; m=$(cfg MUTATION)
	case $m in
		every) echo "MUTATION required (MUTATION=every)" ;;
		off) echo "MUTATION skip (MUTATION=off)" ;;
		*)
			if [ "$1" != Q ] && [ "$(task_field "$(art tasks)" "$1" Risk | tr '[:upper:]' '[:lower:]')" = high ]; then echo "MUTATION required (Risk: high, MUTATION=risk)"
			else echo "MUTATION skip (MUTATION=risk and the task is not Risk: high)"; fi ;;
	esac
}

paragraphs_naming() { # file section "AC1|AC3" -> the section's paragraphs that mention one of the ids
	fm_body "$1" | awk '{ print } END { print "" }' | strip_comments | sec_raw "$2" \
		| R="$3" awk 'BEGIN { RS = ""; re = "(^|[^A-Za-z0-9])(" ENVIRON["R"] ")([^0-9]|$)" } $0 ~ re { print; print "" }'
}

cmd_task() { # the context pack: everything one task needs, so agents read more only when they must
	local id=${1:-} r b acs re a eids
	resolve_feature; [ -n "$id" ] || die "usage: task <ID>"
	r=$(sfile_get "rounds.$id")
	echo "FEATURE $F   KIND $(fm_get "$(art spec)" kind)$(fm_get "$(art brief)" kind)   ROUND ${r:-0}/$(cfg FIX_ROUNDS)"
	model_for "$id"
	echo "SETTINGS model=$CV   review=$(cfg REVIEW)   verify=$(cfg VERIFY) (the script runs it — you run targeted tests only)"
	mutation_line "$id"
	if [ "$id" = Q ]; then
		echo; echo "## The brief ($(art brief))"; doc "$(art brief)"
	else
		task_block "$(art tasks)" "$id" | grep -q . || die "no task $id in $(art tasks)"
		echo; echo "## Task"; task_block "$(art tasks)" "$id"
		acs=$(task_field "$(art tasks)" "$id" AC | ac_refs)
		re=$(printf '%s\n' $acs | paste -sd'|' -)
		echo; echo "## Its acceptance criteria ($(art spec))"
		for a in $acs; do _ac_text "$(art spec)" | ac_line "$a"; done
		echo; echo "## Edge cases that concern it"
		eids=$(task_block "$(art tasks)" "$id" | grep -oE 'E[0-9]+' | sort -u | paste -sd'|' -)
		section "$(art spec)" "Edge cases" | E="$eids" R="${re:-NONE}" awk '
			BEGIN { re = "(^|[^A-Za-z0-9])(" ENVIRON["R"] ")([^0-9]|$)"; if (ENVIRON["E"] != "") ee = "[*][*](" ENVIRON["E"] ")[*][*]" }
			$0 ~ re || (ee != "" && $0 ~ ee) { print }'
		if [ -f "$(art plan)" ]; then
			echo; echo "## Plan — approach"; section "$(art plan)" "Approach"
			if [ -n "$re" ]; then
				echo; echo "## Plan — design paragraphs that name its ACs"; paragraphs_naming "$(art plan)" "Design" "$re"
				echo "## Plan — AC coverage"; section "$(art plan)" "AC coverage" | grep -E "(^|[^A-Za-z0-9])($re)([^0-9]|$)"
			fi
		fi
	fi
	if [ -f "$(art research)" ]; then
		echo; echo "## Answered questions (research.md)"
		section "$(art research)" "Open questions" | awk '/^[[:space:]]*[-*][[:space:]]+\*\*Q[0-9]+\*\*/ { on = ($0 ~ /\(answered\)/) } on { print }'
	fi
	b=$(base_branch)
	echo; echo "## Recent task commits"
	git log "$b..HEAD" --grep='^Task: ' -3 --format='@@%h %s%n%b' 2>/dev/null \
		| awk '/^@@/ { print "  " substr($0, 3); want = 1; next } want && NF && $0 !~ /^[A-Za-z-]+: / { print "      " $0; want = 0 }'
	if [ "$(cfg TRAILERS)" = off ]; then
		echo; echo "COMMIT TRAILERS: off (TRAILERS=off) — write a normal commit message, no trailers"
	else
		echo; echo "COMMIT TRAILERS (required, as the last lines of every commit message):"
		echo "Task: $id"; echo "Feature: $F"
		[ "$id" = Q ] || echo "AC: $(task_field "$(art tasks)" "$id" AC)"
	fi
	if [ -n "$TEST_CMD" ]; then echo "TESTS run only the tests this task touches: .claude/scripts/loop.sh test <args>   ($TEST_CMD)"
	else echo "TESTS run only the tests this task touches, with the repo's test command scoped to the packages you change"; fi
}

cmd_post_check() { # the post-task contract without verify (agents self-check; the SubagentStop hook uses it)
	local id=${1:-}
	resolve_feature; [ -n "$id" ] || die "usage: post-check <ID>"
	if post_task "$id" quick; then echo "OK: $id meets the post-task contract (verify runs when the orchestrator logs DONE)"; return 0; fi
	return 1
}

cmd_findings() { local id=${1:-}; resolve_feature; [ -n "$id" ] || die "usage: findings <ID>"; sfile_get "findings.$id"; }

cmd_review_info() {
	local id=${1:-} base mb id2
	resolve_feature; [ -n "$id" ] || die "usage: review-info <ID|BRANCH|Q>"
	echo "FEATURE $F   KIND $(fm_get "$(art spec)" kind)$(fm_get "$(art brief)" kind)"
	case $id in
		BRANCH)
			mb=$(git merge-base "$(base_branch)" HEAD)
			echo "MODE branch — judge the whole branch against $(art spec) (every AC, scope both ways, checks on checks)"
			echo "RANGE $(short "$mb")..$(short)   (git diff $(short "$mb")..HEAD)"
			echo "TASKS"; for id2 in $(task_ids "$(art tasks)"); do printf '  %s %s\n' "$id2" "$(state_get "$id2")"; done
			git diff --stat "$mb" HEAD | tail -25
			git diff --name-only "$mb" HEAD | while IFS= read -r p; do match_globs "$p" "$WATCHED_GLOBS" && echo "WATCH changed $p"; done
			git diff --name-only --diff-filter=D "$mb" HEAD | while IFS= read -r p; do match_globs "$p" "$TEST_GLOBS" && echo "WATCH deleted test file $p"; done
			verify_line
			;;
		*)
			base=$(sfile_get "base.$id"); [ -n "$base" ] || die "no base recorded for $id"
			if [ "$id" = Q ]; then
				echo "MODE brief — the standard is $(art brief) (Change, Acceptance, Out of scope) plus AGENTS.md/CLAUDE.md"
			else
				echo "MODE task — the standard is $(art spec) plus AGENTS.md/CLAUDE.md. The block below is the CLAIM you check, not the standard:"
				task_block "$(art tasks)" "$id"
			fi
			echo "RANGE $(short "$base")..$(short "$(task_end "$id")")   (git diff $(short "$base")..$(short "$(task_end "$id")"))"
			echo "ROUND $(sfile_get "rounds.$id")/$(cfg FIX_ROUNDS)"
			git diff --stat "$base" "$(task_end "$id")" | tail -25
			sfile_get "watch.$id"
			verify_line
			;;
	esac
}

verify_line() { # the script's verify result for HEAD — reviewers report it, they never re-run it
	if [ "$(sfile_get green)" = "$(git rev-parse HEAD)" ]; then echo "VERIFY green at $(short) — run by loop.sh ($VERIFY_CMD); don't run it again"
	else echo "VERIFY not run at $(short) yet (VERIFY=$(cfg VERIFY)) — loop.sh runs it before the branch review; don't run it yourself"; fi
}

cmd_verify() {
	local rc
	soft_feature || F=""
	run_verify; rc=$?
	if [ $rc = 0 ]; then echo "verify: green ($VERIFY_CMD)"; else tail -40 "$(VLOG)"; echo "verify: RED (${VERIFY_CMD:-not set}) — full log: $(VLOG)"; fi
	return $rc
}

cmd_test() {
	[ -n "$TEST_CMD" ] || die "TEST_CMD is not set in .claude/loop.conf.
  do this: run the repo's own test command for the packages you touched"
	bash -c "$TEST_CMD"' "$@"' _ "$@"
}

cmd_impact() {
	local a t b
	resolve_feature; [ $# -gt 0 ] || die "usage: impact AC1 [AC2 ...]"
	b=$(base_branch)
	for a in "$@"; do
		echo "$a"
		if [ -f "$(art tasks)" ]; then
			for t in $(task_ids "$(art tasks)"); do
				task_field "$(art tasks)" "$t" AC | ac_refs | grep -qx "$a" || continue
				st=$(state_get "$t"); [ -n "$st" ] || { in_list "$t" "$(done_tasks)" && st=PASS || st=todo; }
				printf '  task %s %-12s %s\n' "$t" "$st" "$(task_title "$(art tasks)" "$t")"
			done
		fi
		{
			git log "$b..HEAD" --format='@@%h %s%n%B' | A="$a" awk '
				/^@@/ { c = substr($0, 3); next }
				/^AC:/ { n = split($0, x, /[^A-Za-z0-9]+/); for (i = 1; i <= n; i++) if (x[i] == ENVIRON["A"]) print "  commit " c }'
			# commits recorded by the loop (the only map when TRAILERS=off)
			if [ -f "$(sdir)/commits" ] && [ -f "$(art tasks)" ]; then
				while read -r c t; do
					task_field "$(art tasks)" "$t" AC | ac_refs | grep -qx "$a" && git log -1 --format="  commit %h %s" "$c" 2>/dev/null
				done < "$(sdir)/commits"
			fi
		} | sort -u
	done
}

cmd_cr_new() { # [--adopt] — with --adopt the Delta is filled from your edit of the approved spec (or brief)
	local n max=0 c d adopt=0 t sha delta file
	[ "${1:-}" = --adopt ] && adopt=1
	resolve_feature
	d=$(draft_crs | head -1)
	if [ -n "$d" ]; then echo "CR $d (existing draft — revise it)"; echo "FILE $SPECS_DIR/$F/changes/$d.md"; return 0; fi
	if [ $adopt = 1 ]; then
		if is_quick; then t=$(art brief); else t=$(art spec); fi
		[ "$(art_state "$t")" = changed ] || die "${t##*/} has no edits since you approved it, so there is nothing to adopt."
		sha=$(approval_sha "$t")
		delta=$(adopt_delta "$t" "$sha")
	fi
	mkdir -p "$SPECS_DIR/$F/changes"
	for c in $(cr_files); do n=${c##*/CR-}; n=${n%.md}; n=$((10#$n)); [ "$n" -gt "$max" ] && max=$n; done
	CR=$(printf 'CR-%03d' $((max + 1)))
	file="$SPECS_DIR/$F/changes/$CR.md"
	render cr.md "$file"
	if [ $adopt = 1 ]; then
		fm_set "$file" scope spec
		[ -n "$delta" ] || delta="- none"
		D="$delta" R="Adopt my edit of ${t##*/} (made after its approval in $(git rev-parse --short "$sha"))." awk '
			$0 == "## Delta" { print; print ENVIRON["D"]; next }
			$0 == "## Request" { print; print ENVIRON["R"]; next }
			{ print }' "$file" > "$file.new" && mv "$file.new" "$file"
		echo "ADOPTED the AC changes of ${t##*/} into the Delta:"
		printf '%s\n' "$delta" | sed 's/^/  /'
	fi
	echo "CR $CR"
	echo "FILE $file"
}

adopt_delta() { # file approval-sha -> Delta lines (ADDED / MODIFIED / REMOVED) for the AC edits since then
	local f=$1 sha=$2 h o n ids id ol nl
	case $f in *brief.md) h="Acceptance" ;; *) h="Acceptance criteria" ;; esac
	o=$(git show "$sha:$f" 2>/dev/null | strip_fm | strip_noise | sec "$h")
	n=$(doc "$f" | sec "$h")
	ids=$( { printf '%s\n' "$o" "$n" | ac_active; printf '%s\n' "$o" "$n" | ac_struck; } | sort -u | sort -t C -k 2n)
	actext() { sed -E 's/^[[:space:]]*[-*][[:space:]]+(~~)?\*\*AC[0-9]+\*\*(~~)?[[:space:]]*(—|-|:)?[[:space:]]*//'; }
	for id in $ids; do
		ol=$(printf '%s\n' "$o" | ac_active | grep -qx "$id" && printf '%s\n' "$o" | ac_line "$id")
		nl=$(printf '%s\n' "$n" | ac_line "$id")
		if [ -z "$ol" ]; then
			printf '%s\n' "$n" | ac_active | grep -qx "$id" && echo "- ADDED $id — $(printf '%s\n' "$nl" | actext)"
		elif [ -z "$nl" ] || printf '%s\n' "$n" | ac_struck | grep -qx "$id"; then
			echo "- REMOVED $id — removed in your edit (was: $(printf '%s\n' "$ol" | actext))"
		elif [ "$ol" != "$nl" ]; then
			echo "- MODIFIED $id — $(printf '%s\n' "$nl" | actext) (was: $(printf '%s\n' "$ol" | actext))"
		fi
	done
}

cmd_add_fix() { # <the bug, in words> -> a not-started task "Tnnn — fix: <bug>" placed before the first open task, committed
	local bug="$*" t sha ids max next first subj scope tmp
	resolve_feature
	is_quick && die "$F is a /al-quick change: it has no task list.
  do this: /al-change <the fix>"
	[ -n "$bug" ] || die "say what is broken.
  do this: /al-fix <what goes wrong, and when>"
	t=$(art tasks)
	[ "$(art_state "$t")" = approved ] || die "tasks.md isn't approved, so a fix task can't join it yet:
  $(art_why "$t" "$(art_state "$t")")"
	tree_clean || die "the working tree has uncommitted changes.
  do this: commit or stash them, then /al-fix again"
	sha=$(approval_sha "$t")
	ids=$( { task_ids "$t"; git show "$sha:$t" 2>/dev/null | strip_fm | strip_noise | task_ids_stdin; } | sort -u)
	max=$(printf '%s\n' $ids | sort | tail -1); max=${max#T}
	next=$(printf 'T%03d' $((10#${max:-0} + 1)))
	first=""
	for id in $(task_ids "$t"); do in_list "$id" "$(done_tasks)" || { first=$id; break; }; done
	scope=${F#[0-9][0-9][0-9]-}
	subj=$(printf '%s' "$bug" | tr '[:upper:]' '[:lower:]' | tr -c 'a-z0-9 .,-' ' ' | tr -s ' ' | sed 's/^ //; s/ $//' | cut -c1-60)
	BLK="### $next — fix: $bug
- Do: reproduce and fix: $bug
- Tests: a regression test that fails before the fix and passes after it ($bug)
- AC: none — bug fix: $bug
- Commit: fix($scope): $subj
- Depends: —
- Size: S
- Risk: low
" FIRST="$first" awk '
		function emit() { if (!done) { printf "%s\n", ENVIRON["BLK"]; done = 1 } }
		ENVIRON["FIRST"] != "" && index($0, "### " ENVIRON["FIRST"]) == 1 && substr($0, 9, 1) !~ /[0-9]/ { emit() }
		/^## / && intasks && !/^## Tasks/ { emit() }
		/^## Tasks/ { intasks = 1 }
		{ print }
		END { emit() }' "$t" > "$t.new" && mv "$t.new" "$t"
	if ! tmp=$(tasks_problems "$t" "$sha"); then
		git checkout -q -- "$t"
		die "the fix task would make tasks.md invalid, so nothing was added:
$tmp"
	fi
	git commit -q -m "docs($F): add $next — fix: $subj" -- "$t" || die "committing tasks.md failed"
	log_event "$next added by /al-fix: $bug"
	echo "ADDED $next — fix: $bug$( [ -n "$first" ] && echo " (runs before $first)")"
	if [ "$(cfg AUTO_APPROVE_TASKS)" = on ]; then echo "NEXT the build runs $next now"
	else echo "NEXT /al-approve tasks (AUTO_APPROVE_TASKS=off), then /al-implement"; fi
}

question_lines() { # Qn -> QUESTION <text> and OPTION <text> lines from research.md, for the question card
	section "$(art research)" "Open questions" | Q="$1" awk '
		index($0, "**" ENVIRON["Q"] "**") {
			s = $0; sub(/^[[:space:]]*[-*][[:space:]]+\*\*Q[0-9]+\*\*[[:space:]]*\([a-z]+\)[[:space:]]*/, "", s)
			i = index(s, "options:"); qs = s; os = ""
			if (i) { qs = substr(s, 1, i - 1); os = substr(s, i + 8) }
			sub(/[[:space:]—-]+$/, "", qs)
			print "QUESTION " qs
			n = split(" " os, parts, /[[:space:]]+[A-Z]\)[[:space:]]+/)
			for (j = 2; j <= n; j++) { t = parts[j]; sub(/[[:space:]]+$/, "", t); if (t != "") print "OPTION " t }
			exit
		}'
}

cmd_answer() { # Qn <answer> — record your answer, commit research.md, restore the blocked task's attempt
	local q=${1:-} txt r id ref
	resolve_feature
	shift || true; txt="$*"
	case $q in Q[0-9]*) ;; *) die "say which question and the answer.
  do this: /al-answer Q2 <your answer>" ;; esac
	[ -n "$txt" ] || die "the answer is missing.
  do this: /al-answer $q <your answer>"
	r=$(art research)
	open_questions | grep -qx "$q" || die "$q is not an open question in $r.
  do this: .claude/scripts/loop.sh status   (lists the open ones)"
	Q="$q" A="$txt ($(today))" awk '
		{ line = $0 }
		!incom && index(line, "**" ENVIRON["Q"] "**") && index(line, "(open)") && !done {
			sub(/\(open\)/, "(answered)", line); print line; print "  - **Answer:** " ENVIRON["A"]; done = 1; next }
		{ print }
		index(line, "<!--") && !index(line, "-->") { incom = 1 }
		index(line, "-->") { incom = 0 }' "$r" > "$r.new" && mv "$r.new" "$r"
	git commit -q -m "docs($F): answer $q" -m "$txt" -- "$r" || die "committing research.md failed"
	log_event "$q answered: $txt"
	id=$(awk -F '\t' -v q="$q" '{ s[$1] = $2; d[$1] = $3 } END { for (k in s) if (s[k] == "BLOCKED" && d[k] == q) print k }' "$(sdir)/state" 2>/dev/null | head -1)
	if [ -z "$id" ]; then echo "ANSWERED $q"; echo "ACTION next"; return 0; fi
	ref=$(git stash list --format='%gd%x09%s' | I="$id blocked on $q" awk -F '\t' 'index($2, ENVIRON["I"]) { print $1; exit }')
	if [ -n "$ref" ]; then
		git stash pop -q "$ref" || die "$id's earlier attempt ($ref) didn't apply cleanly onto HEAD.
  do this: resolve the conflict in the files git lists, then /al-resume"
	fi
	sfile_set "base.$id" "$(git rev-parse HEAD)"; sfile_set "rounds.$id" 0
	state_set "$id" IN-PROGRESS "restored after $q"
	log_event "$id resumes after $q${ref:+ with its stashed attempt}"
	echo "ANSWERED $q — $id continues${ref:+ from its earlier attempt (restored in the working tree)}"
	model_for "$id"; echo "ACTION implement $id model=$CV"
}

cmd_accept() { # Tn <reason> — you accept what the reviewer escalated; the task passes, the risk is on record
	local id=${1:-} why
	resolve_feature
	shift || true; why="$*"
	[ "$(state_get "$id")" = ESCALATED ] || die "${id:-the task} is not waiting for your decision."
	[ -n "$why" ] || why="accepted as is"
	state_set "$id" PASS "accepted by you: $why"; [ "$id" = Q ] || state_set BRANCH CLEARED
	log_event "$id ACCEPTED by the user despite the reviewer's ESCALATE: $why"
	if [ "$id" = Q ]; then echo "ACTION finish"; else echo "ACTION next"; fi
}

cmd_lineage() {
	local b s d
	resolve_feature "${1:-}"; b=$(base_branch)
	echo "$F"
	s=$(fm_get "$(art spec)" supersedes)
	while [ -n "$s" ]; do
		echo "  supersedes $s"
		s=$(git show "$b:$SPECS_DIR/$s/spec.md" 2>/dev/null | awk 'NR==1 && $0!="---"{exit} NR>1 && /^---/{exit} /^supersedes:/{sub(/^supersedes:[[:space:]]*/,""); print; exit}')
	done
	for d in $(git ls-tree --name-only "$b" "$SPECS_DIR/" 2>/dev/null); do
		git show "$b:$d/spec.md" 2>/dev/null | grep -q "^supersedes: $F\$" && echo "  superseded by ${d##*/}"
	done
	return 0
}

cmd_report() {
	local id st d q end title
	echo "Report — $F on $(current_branch) ($(now))"
	if is_quick; then
		echo "  Q  $(state_get Q) $(state_detail Q)  (fix rounds: $(sfile_get rounds.Q))"
		{ [ "$(cfg REVIEW)" = none ] || [ "$(state_detail Q)" = "reviews off" ]; } && echo "Reviews: off (REVIEW=none) — no review ran"
	else
		echo "Tasks"
		for id in $(task_ids "$(art tasks)"); do
			st=$(state_get "$id"); d=$(state_detail "$id"); title=$(task_title "$(art tasks)" "$id")
			[ -n "$st" ] || { in_list "$id" "$(done_tasks)" && st=PASS || st=todo; }
			case $st in
				NEEDS-HUMAN) printf '  %s  NEEDS-HUMAN  %s — check by hand: %s\n' "$id" "$title" "$(task_field "$(art tasks)" "$id" Manual)" ;;
				BLOCKED) printf '  %s  BLOCKED %s  %s — %s\n' "$id" "$d" "$title" "$(section "$(art research)" "Open questions" | grep -F "**$d**" | head -1)" ;;
				*) printf '  %s  %-12s %s %s(fix rounds: %s)%s\n' "$id" "$st" "$title" "${d:+$d }" "$(sfile_get "rounds.$id")" "$(r=$(sfile_get "reason.$id"); [ -n "$r" ] && printf ' — %s' "$r")" ;;
			esac
		done
		if [ "$(cfg REVIEW)" = none ] || [ "$(state_get BRANCH)" = OFF ]; then echo "Reviews: off (REVIEW=none) — no task or branch review ran"
		else echo "Branch review: $(state_get BRANCH | sed 's/^CLEARED$/not run since the last change/; s/^$/not run/')"; fi
	fi
	[ "$(cfg VERIFY)" = off ] && echo "Verify: OFF (VERIFY=off) — nothing proved the repo healthy; run .claude/scripts/loop.sh verify before you open the PR"
	end=$(grep -E ' (STOP|RUN END)' "$(sdir)/run.log" 2>/dev/null | tail -1)
	[ -n "$end" ] && echo "Run: $end"
	q=$(section "$(art research)" "Open questions" | grep '(open)')
	[ -n "$q" ] && { echo "Open questions:"; printf '%s\n' "$q" | sed 's/^/  /'; }
	echo "Next: $(next_step)"
	echo "Log: .agent-loop/$F/run.log"
}

cmd_doctor() {
	local ok=0
	chk() { if eval "$2" >/dev/null 2>&1; then echo "  ok    $1"; else echo "  FAIL  $1 — $3"; ok=1; fi; }
	echo "agent-loop doctor ($REPO)"
	chk "git" "command -v git" "install git"
	chk "jq (hooks need it)" "command -v jq" "install jq — without it the guard denies every Bash/Edit call"
	chk "sha256sum or shasum" "command -v sha256sum || command -v shasum" "install coreutils"
	chk "kit committed" "git ls-files --error-unmatch .claude/scripts/loop.sh" "git add .claude .gitignore && git commit"
	chk "hooks executable" "test -x .claude/hooks/guard.sh -a -x .claude/hooks/on-command.sh -a -x .claude/hooks/on-agent-stop.sh" "chmod +x .claude/hooks/*.sh .claude/scripts/*.sh"
	chk "settings.json wires the hooks" "jq -e '.hooks.PreToolUse and .hooks.UserPromptExpansion and .hooks.UserPromptSubmit and .hooks.SubagentStop' .claude/settings.json" "merge the kit's settings.json (install.sh does it)"
	chk ".agent-loop/ gitignored" "git check-ignore -q .agent-loop/x" "add '.agent-loop/' to .gitignore"
	chk ".claude/worktrees/ gitignored" "git check-ignore -q .claude/worktrees/x" "add '.claude/worktrees/' to .gitignore"
	if [ -n "$VERIFY_CMD" ]; then echo "  ok    VERIFY_CMD set"; else echo "  FAIL  $(verify_unset_msg)"; ok=1; fi
	if [ -f AGENTS.md ] || [ -f CLAUDE.md ]; then echo "  ok    AGENTS.md / CLAUDE.md present"; else echo "  warn  no AGENTS.md or CLAUDE.md — optional, but agents read it for conventions and hard rules"; fi
	echo "  base branch: $(base_branch)   verify: ${VERIFY_CMD:-<none>}"
	return $ok
}

cmd=${1:-help}; shift || true
case $cmd in
	new) cmd_new "$@" ;;
	status) cmd_status "$@" ;;
	config) cmd_config "$@" ;;
	cfg) soft_feature || F=""; [ -n "${1:-}" ] || die "usage: cfg <KEY>"; cfg "$1" ;;
	suggest-verify) suggest_verify ;;
	gate) cmd_gate "$@" ;;
	check) cmd_check "$@" ;;
	start) cmd_start "$@" ;;
	next) cmd_next "$@" ;;
	log) cmd_log "$@" ;;
	stop) cmd_stop "$@" ;;
	finish) cmd_finish "$@" ;;
	unlock) cmd_unlock "$@" ;;
	pause) cmd_pause "$@" ;;
	dirty) cmd_dirty "$@" ;;
	task) cmd_task "$@" ;;
	findings) cmd_findings "$@" ;;
	post-check) cmd_post_check "$@" ;;
	review-info) cmd_review_info "$@" ;;
	verify) cmd_verify "$@" ;;
	test) cmd_test "$@" ;;
	impact) cmd_impact "$@" ;;
	cr-new) cmd_cr_new "$@" ;;
	add-fix) cmd_add_fix "$@" ;;
	answer) cmd_answer "$@" ;;
	accept) cmd_accept "$@" ;;
	lineage) cmd_lineage "$@" ;;
	report) resolve_feature "${1:-}"; cmd_report ;;
	doctor) cmd_doctor ;;
	resolve) resolve_feature "${1:-}"; echo "$F" ;;
	help | -h | --help) sed -n '2,/^set -uo/p' "$0" | sed '$d; s/^# \{0,1\}//' ;;
	*) die "there is no loop.sh command '$cmd'.
  do this: .claude/scripts/loop.sh help" ;;
esac
