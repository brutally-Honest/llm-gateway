#!/usr/bin/env bash
# approve.sh — HUMAN-ONLY approval stamp (and reopen).
#
# Claude never runs this. You trigger it by typing /al-approve (or /al-change, which may reopen)
# in Claude Code — a UserPromptExpansion hook runs it from your keystroke — or you run it
# in your own terminal. guard.sh denies it to every agent and to the main session, and
# settings.json denies it as a permission rule.
#
#   approve.sh                         approve whatever is waiting (auto)
#   approve.sh spec|plan|tasks|brief   approve that artifact of the current feature
#   approve.sh change [CR-nnn]         accept a change request (reopens what it changes)
#   approve.sh reopen spec|plan|tasks|brief --reason "why"   reopen an approved artifact as a draft
#   add a feature id (012 or 012-slug) to act on a feature other than the branch's
#
# Approving = validate (refuse on any error) -> stamp status/approved/fingerprint into the
# frontmatter -> reopen downstream artifacts the change invalidates -> commit specs/<f>/
# with the contract fingerprints in the commit body (that record is what makes it approved).
# If the commit fails (e.g. a git hook rejects it) every file is restored.
set -uo pipefail
HERE=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
# shellcheck source=lib.sh
. "$HERE/lib.sh"
# shellcheck source=validate.sh
. "$HERE/validate.sh"
al_init

target="" rtarget="" by=human reason="" feat="" cr=""
while [ $# -gt 0 ]; do
	case $1 in
		--by) by=${2:-}; shift ;;
		--reason) reason=${2:-}; shift ;;
		CR-[0-9]*) cr=$1 ;;
		spec | plan | tasks | brief | change | auto | reopen)
			if [ -z "$target" ]; then target=$1
			elif [ "$target" = reopen ] && [ -z "$rtarget" ]; then rtarget=$1
			else die "'$1' is one word too many.
  do this: /al-approve spec|plan|tasks|brief|change   (or just /al-approve)"; fi ;;
		-*) die "unknown option $1.
  do this: /al-approve spec|plan|tasks|brief|change   (or just /al-approve)" ;;
		*) feat=$1 ;;
	esac
	shift
done
target=${target:-auto}
case $by in human | auto) ;; *) die "--by must be human or auto" ;; esac
resolve_feature "$feat"

br=$(current_branch) || die "you're on a detached HEAD, so there's nowhere to commit the approval.
  do this: git switch <the feature's branch>"
[ "$br" != "$(base_branch)" ] || die "you're on $br; approvals are committed on the feature's branch.
  do this: git switch <the feature's branch>, then /al-approve again"
expect=$(fm_get "$(art spec)" branch)$(fm_get "$(art brief)" branch)
if [ -n "$expect" ]; then
	[ "$br" = "$expect" ] || die "$F lives on branch $expect, but you're on $br.
  do this: git switch $expect"
else
	case $br in *"$F"*) ;; *) die "$F's branch isn't checked out (you're on $br).
  do this: git switch <$F's branch>" ;; esac
fi

SNAP=""
snapshot() { SNAP=$(mktemp -d "${TMPDIR:-/tmp}/al-snap.XXXXXX") && cp -Rp "$SPECS_DIR/$F/." "$SNAP/"; }
restore() { [ -n "$SNAP" ] || return 0; git reset -q -- "$SPECS_DIR/$F" 2>/dev/null; cp -Rp "$SNAP/." "$SPECS_DIR/$F/"; rm -rf "$SNAP"; SNAP=""; }
done_snap() { [ -n "$SNAP" ] && rm -rf "$SNAP"; SNAP=""; }

commit_docs() { # subject body
	local out
	git add -- "$SPECS_DIR/$F" || return 1
	out=$(git commit -q -m "$1" -m "$2" -- "$SPECS_DIR/$F" 2>&1) || { printf '%s\n' "$out"; return 1; }
}

finish_commit() { # subject body
	local out
	if ! out=$(commit_docs "$1" "$2"); then
		restore
		die "the commit failed, so nothing was approved (your files are as they were):
$out"
	fi
	done_snap
}

stamp() { # file by [key value]...
	local f=$1 who=$2; shift 2
	fm_set "$f" status approved
	fm_set "$f" approved "$(now)"
	fm_set "$f" approved-by "$who"
	while [ $# -ge 2 ]; do fm_set "$f" "$1" "$2"; shift 2; done
	fm_set "$f" fingerprint "$(contract_fp "$f")"
	fm_set "$f" sha256 "$(body_hash "$f")"
}

rec() { printf 'fingerprint %s %s\n' "$(art_kind "$1")" "$(contract_fp "$1")"; }   # the approval record (commit body)

add_changelog() { # file line -> appended to '## Changelog' (created if missing)
	local f=$1 tmp
	tmp=$(mktemp "${TMPDIR:-/tmp}/al.XXXXXX") || return 1
	L="$2" awk '
		/^## / { if (inlog && !done) { print ENVIRON["L"]; done = 1 } inlog = (tolower($0) ~ /^## changelog/) }
		{ print }
		END { if (!done) { if (!inlog) { print ""; print "## Changelog" } print ENVIRON["L"] } }' "$f" > "$tmp" && cat "$tmp" > "$f"
	rm -f "$tmp"
}

reopen_file() { # file reason -> prints one line
	local f=$1 why=$2 ver new prev k
	ver=$(fm_get "$f" version); ver=${ver:-1}; new=$((ver + 1))
	prev=$(approval_sha "$f"); [ -n "$prev" ] || prev=$(git log -1 --format=%H -- "$f")
	fm_set "$f" status draft
	fm_set "$f" version "$new"
	fm_set "$f" previous "$(git rev-parse --short "$prev")"
	for k in approved approved-by sha256 fingerprint spec-fingerprint plan-sha256 plan-fingerprint; do
		[ -n "$(fm_get "$f" "$k")" ] && fm_set "$f" "$k" ""
	done
	add_changelog "$f" "- v$new ($(today)) — reopened: $why"
	echo "REOPENED ${f##*/} v$new (draft) — $why"
}

ready_or_die() { # file
	local f=$1 st
	st=$(art_state "$f")
	case $st in
		draft | unproven) ;;
		missing) die "$(art_why "$f" missing)" ;;
		approved) die "${f##*/} is already approved — nothing to do.
  do this: .claude/scripts/loop.sh status   (shows the next step)" ;;
		*) die "$(art_why "$f" "$st")" ;;
	esac
}

pending_crs_where() { # key -> approved CRs whose <key> is still empty
	local c
	for c in $(cr_files); do
		[ "$(fm_get "$c" status)" = approved ] && [ -z "$(fm_get "$c" "$1")" ] && echo "$c"
	done
	return 0
}

approve_spec() {
	local f p t c e ver fp casc=""
	f=$(art spec); ready_or_die "$f"
	e=$(check_spec "$f" approve) || die "spec.md is not ready to approve:
$e"
	for c in $(pending_crs_where spec-applied); do
		[ "$(fm_get "$c" scope)" = spec ] || continue
		e=$(check_cr_applied "$c" "$f") || die "spec.md does not apply $(basename "$c" .md) yet:
$e"
	done
	snapshot
	ver=$(fm_get "$f" version); ver=${ver:-1}
	fp=$(contract_fp "$f")
	stamp "$f" human
	for c in $(pending_crs_where spec-applied); do [ "$(fm_get "$c" scope)" = spec ] && fm_set "$c" spec-applied "v$ver"; done
	p=$(art plan); t=$(art tasks)
	# the planner revises plan and tasks together, so a contract change reopens both
	if [ "$(art_state "$p")" = approved ] && [ "$(link_rec "$p" spec-fingerprint)" != "$fp" ]; then
		casc="$casc$(reopen_file "$p" "spec v$ver changed its contract sections")
"
		[ "$(fm_get "$t" status)" = approved ] && casc="$casc$(reopen_file "$t" "spec v$ver changed its contract sections")
"
	fi
	finish_commit "docs($F): approve spec v$ver" "$(rec "$f")
sha256 $(fm_get "$f" sha256)"
	echo "APPROVED spec v$ver of $F ($(git rev-parse --short HEAD))"
	[ -n "$casc" ] && printf '%s' "$casc"
	if [ -n "$casc" ]; then echo "NEXT /al-plan — the planner revises the reopened plan and tasks for spec v$ver, then /al-approve plan"
	elif [ "$(art_state "$p")" = approved ]; then echo "NEXT plan.md and tasks.md stay valid (the contract sections did not change) — /al-implement"
	else echo "NEXT /al-plan"; fi
}

approve_plan() { # stamps the plan; with AUTO_APPROVE_TASKS=on also a valid tasks.md, in the same commit
	local f s t c e ver tver casc="" tnote="" subj body n pfp
	is_quick && die "$F is a /al-quick feature — it has no plan.
  do this: /al-approve brief"
	e=$(chain_errors spec) || die "the spec must be approved before the plan:
  $e"
	f=$(art plan); ready_or_die "$f"
	e=$(check_plan "$f" approve) || die "plan.md is not ready to approve:
$e"
	snapshot
	ver=$(fm_get "$f" version); ver=${ver:-1}
	s=$(art spec)
	stamp "$f" human spec-fingerprint "$(contract_fp "$s")"
	pfp=$(contract_fp "$f")
	body="$(rec "$f")
spec-fingerprint $(contract_fp "$s")"
	for c in $(pending_crs_where plan-applied); do [ "$(fm_get "$c" scope)" = plan ] && fm_set "$c" plan-applied "v$ver"; done
	t=$(art tasks)
	if [ "$(art_state "$t")" = approved ] && [ "$(link_rec "$t" plan-fingerprint)" != "$pfp" ]; then
		casc=$(reopen_file "$t" "plan v$ver changed")
	fi
	subj="docs($F): approve plan v$ver"
	case $(art_state "$t") in
		missing) tnote="NEXT tasks.md is missing — /al-plan has the planner write it, then /al-approve tasks" ;;
		draft | unproven)
			if [ "$(cfg AUTO_APPROVE_TASKS)" != on ]; then
				tnote="NEXT review $t, then /al-approve tasks (AUTO_APPROVE_TASKS=off)"
			elif e=$(check_tasks "$t"); then
				tver=$(fm_get "$t" version); tver=${tver:-1}
				stamp "$t" human plan-fingerprint "$pfp"
				for c in $(pending_crs_where applied); do fm_set "$c" applied "tasks v$tver"; done
				subj="$subj + tasks v$tver"
				body="$body
$(rec "$t")
plan-fingerprint $pfp"
				n=$(task_ids "$t" | grep -c .)
				tnote="APPROVED tasks v$tver of $F — $n tasks
NEXT /al-implement"
			else
				tnote="tasks.md stays a draft — it does not pass the checks yet:
$e
NEXT fix tasks.md (by hand, or /al-plan), then /al-approve tasks"
			fi ;;
		approved) tnote="NEXT /al-implement" ;;
		*) tnote="NEXT $(art_why "$t" "$(art_state "$t")")" ;;
	esac
	finish_commit "$subj" "$body"
	echo "APPROVED plan v$ver of $F ($(git rev-parse --short HEAD))"
	[ -n "$casc" ] && echo "$casc"
	printf '%s\n' "$tnote"
}

approve_tasks() {
	local f p c e ver subj n st prev pfp
	is_quick && die "$F is a /al-quick feature — it has no tasks.md.
  do this: /al-approve brief"
	e=$(chain_errors plan) || die "the plan must be approved before the tasks:
  $e"
	f=$(art tasks); st=$(art_state "$f")
	if [ "$st" = changed ] || [ "$st" = invalid ]; then
		# an approved tasks.md you edited: done tasks must be untouched, then it's re-approved as the next version
		prev=$(approval_sha "$f")
		e=$(tasks_problems "$f" "$prev") || die "tasks.md can't be approved as it is:
$e"
		ver=$(fm_get "$f" version); ver=$(( ${ver:-1} + 1 ))
		snapshot
		fm_set "$f" version "$ver"; fm_set "$f" previous "$(git rev-parse --short "$prev")"
	else
		ready_or_die "$f"
		e=$(check_tasks "$f") || die "tasks.md is not ready to approve:
$e"
		snapshot
		ver=$(fm_get "$f" version); ver=${ver:-1}
	fi
	p=$(art plan); pfp=$(contract_fp "$p")
	stamp "$f" "$by" plan-fingerprint "$pfp"
	for c in $(pending_crs_where applied); do fm_set "$c" applied "tasks v$ver"; done
	subj="docs($F): approve tasks v$ver"; [ "$by" = auto ] && subj="docs($F): auto-approve tasks v$ver"
	finish_commit "$subj" "$(rec "$f")
plan-fingerprint $pfp"
	n=$(task_ids "$f" | grep -c .)
	echo "APPROVED tasks v$ver of $F by $by — $n tasks ($(git rev-parse --short HEAD))"
	echo "NEXT /al-implement"
}

approve_brief() {
	local f c e ver
	is_quick || die "$F has no brief.md (it is a full feature).
  do this: /al-approve spec, /al-approve plan"
	f=$(art brief); ready_or_die "$f"
	e=$(check_brief "$f") || die "brief.md is not ready to approve:
$e"
	for c in $(pending_crs_where applied); do
		e=$(check_cr_applied "$c" "$f") || die "brief.md does not apply $(basename "$c" .md) yet:
$e"
	done
	snapshot
	ver=$(fm_get "$f" version); ver=${ver:-1}
	stamp "$f" human
	for c in $(pending_crs_where applied); do fm_set "$c" applied "brief v$ver"; done
	finish_commit "docs($F): approve brief v$ver" "$(rec "$f")"
	echo "APPROVED brief v$ver of $F ($(git rev-parse --short HEAD))"
	echo "NEXT the build starts now (the /al-implement loop)"
}

approve_change() {
	local c name e scope title casc=""
	if [ -n "$cr" ]; then c="$SPECS_DIR/$F/changes/$cr.md"
	else c=$(for x in $(cr_files); do [ "$(fm_get "$x" status)" = draft ] && echo "$x"; done | tail -1); fi
	[ -n "$c" ] && [ -f "$c" ] || die "there is no change request waiting.
  do this: /al-change <what you want changed>"
	name=$(basename "$c" .md)
	ready_or_die "$c"
	e=$(check_change "$c") || die "$name is not ready to approve:
$e"
	scope=$(fm_get "$c" scope)
	title=$(doc "$c" | head -1 | sed -E 's/^#[[:space:]]*//; s/^CR-[0-9]+[[:space:]]*(—|-|:)?[[:space:]]*//')
	snapshot
	stamp "$c" human
	case $scope in
		spec)
			if is_quick; then casc=$(reopen_file "$(art brief)" "$name: $title")
			else casc=$(reopen_file "$(art spec)" "$name: $title"); fi ;;
		plan)
			is_quick && { restore; die "quick features have no plan — use scope spec"; }
			casc=$(reopen_file "$(art plan)" "$name: $title")
			[ "$(fm_get "$(art tasks)" status)" = approved ] && casc="$casc
$(reopen_file "$(art tasks)" "$name: $title")" ;;
		tasks) is_quick && { restore; die "quick features have no tasks — use scope spec"; }; casc=$(reopen_file "$(art tasks)" "$name: $title") ;;
	esac
	finish_commit "docs($F): approve $name ($scope)" "$title

$(rec "$c")"
	echo "APPROVED change $name of $F ($(git rev-parse --short HEAD)) — scope $scope"
	echo "$casc"
	case $scope in
		spec)
			if is_quick; then echo "NEXT apply the Delta of $name to brief.md exactly (strike removed ACs, never delete), run 'loop.sh check brief', then /al-approve brief"
			else echo "NEXT apply the Delta of $name to spec.md exactly (strike removed ACs, never delete), run 'loop.sh check spec', then /al-approve spec — plan and tasks reopen automatically if the contract changed"; fi ;;
		plan) echo "NEXT /al-plan (amend mode, guided by $name) — the planner revises plan.md and tasks.md — then /al-approve plan" ;;
		tasks) echo "NEXT /al-plan (the planner revises tasks.md for $name), then /al-approve tasks" ;;
	esac
}

reopen_cmd() {
	local f st ver
	case $rtarget in spec | plan | tasks | brief) ;; *) die "say what to reopen.
  do this: approve.sh reopen spec|plan|tasks|brief --reason \"why\"" ;; esac
	[ -n "$reason" ] || die "a reason is required (it goes in the changelog).
  do this: approve.sh reopen $rtarget --reason \"why\""
	f=$(art "$rtarget"); [ -f "$f" ] || die "there is no $rtarget.md in $F"
	st=$(fm_get "$f" status)
	[ "$st" = approved ] || die "$rtarget.md is already a draft — edit it, then /al-approve $rtarget"
	snapshot
	reopen_file "$f" "$reason"
	[ "$rtarget" = plan ] && [ "$(fm_get "$(art tasks)" status)" = approved ] && reopen_file "$(art tasks)" "$reason (the planner revises plan and tasks together)"
	ver=$(fm_get "$f" version)
	finish_commit "docs($F): reopen $rtarget v$ver" "$reason"
	echo "NEXT edit $f, then /al-approve $rtarget"
}

if [ "$target" = auto ]; then
	if is_quick; then [ "$(art_state "$(art brief)")" = draft ] && target=brief
	else
		for x in $(cr_files); do [ "$(fm_get "$x" status)" = draft ] && target=change; done
		if [ "$target" = auto ]; then
			for x in spec plan tasks; do
				case $(art_state "$(art "$x")") in draft | unproven) target=$x; break ;; esac
			done
		fi
	fi
	[ "$target" != auto ] || die "nothing in $F is waiting for your approval.
  do this: .claude/scripts/loop.sh status   (shows the next step)"
fi

case $target in
	spec) approve_spec ;;
	plan) approve_plan ;;
	tasks) approve_tasks ;;
	brief) approve_brief ;;
	change) approve_change ;;
	reopen) reopen_cmd ;;
esac
