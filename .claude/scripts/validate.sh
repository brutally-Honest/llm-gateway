#!/usr/bin/env bash
# validate.sh — deterministic format and consistency checks. Sourced after lib.sh.
# Every check_* prints one "  - problem" line per issue and returns 1 if there is any.
# approve.sh refuses to stamp an artifact whose check fails; hooks use the same checks
# to stop an agent from finishing with a malformed file.

_e=0
err() { printf '  - %s\n' "$*"; _e=1; }
wrn() { printf '  ! warning: %s\n' "$*"; }   # reported, never a refusal

_markers() { # file -> one error per unresolved marker (comments are ignored)
	local m
	m=$(doc "$1" | grep -E 'NEEDS CLARIFICATION|\[ASSUMED\]|(^|[^A-Za-z])(TBD|TODO)([^A-Za-z]|$)|‹' | head -5)
	[ -z "$m" ] && return 0
	printf '%s\n' "$m" | while IFS= read -r l; do printf '  - unresolved marker: %s\n' "$l"; done
	_e=1
}

_need_sections() { # file title...
	local f=$1 s; shift
	for s in "$@"; do
		[ -n "$(section "$f" "$s")" ] || err "section '## $s' is missing or empty"
	done
}

_ac_text() { # file -> acceptance section of a spec or brief
	case $1 in *brief.md) section "$1" "Acceptance" ;; *) section "$1" "Acceptance criteria" ;; esac
}

_old_ac_text() { # file rev -> acceptance section of the file at rev
	local h="Acceptance criteria"
	case $1 in *brief.md) h="Acceptance" ;; esac
	git show "$2:$1" 2>/dev/null | strip_fm | strip_noise | sec "$h"
}

_shall() { # file ids...
	local f=$1 id line; shift
	for id in "$@"; do
		line=$(_ac_text "$f" | ac_line "$id")
		printf '%s' "$line" | grep -qiE 'shall|must' \
			|| err "$id must state an observable result: 'When <trigger>, the system shall <result>'"
	done
}

_ids_kept() { # file -> every AC id of the previous approved version still exists (active or struck)
	local f=$1 prev old cur id
	prev=$(fm_get "$f" previous); [ -n "$prev" ] || return 0
	old=$(_old_ac_text "$f" "$prev")
	cur=$(_ac_text "$f")
	for id in $( { printf '%s\n' "$old" | ac_active; printf '%s\n' "$old" | ac_struck; } | sort -u); do
		{ printf '%s\n' "$cur" | ac_active; printf '%s\n' "$cur" | ac_struck; } | grep -qx "$id" \
			|| err "$id existed in the approved version ($prev) — never delete an AC, strike it: - ~~**$id**~~ — removed in vN (CR-xxx): reason"
	done
}

check_spec() { # file [draft|approve]
	local f=$1 mode=${2:-approve} txt ids struck dup
	_e=0
	[ -f "$f" ] || { err "$f does not exist"; return 1; }
	# only Goal and the ACs are required; every other section (yours included) is optional and kept
	_need_sections "$f" "Goal" "Acceptance criteria"
	txt=$(_ac_text "$f")
	ids=$(printf '%s\n' "$txt" | ac_active)
	struck=$(printf '%s\n' "$txt" | ac_struck)
	[ -n "$ids" ] || err "no acceptance criteria — add lines like: - **AC1** — When <trigger>, the system shall <result>."
	dup=$(printf '%s\n' $ids $struck | sort | uniq -d | tr '\n' ' ')
	[ -z "${dup// /}" ] || err "duplicate AC ids: $dup"
	# shellcheck disable=SC2086
	_shall "$f" $ids
	_ids_kept "$f"
	[ "$mode" = draft ] || _markers "$f"
	return $_e
}

check_plan() { # file [draft|approve]
	local f=$1 mode=${2:-approve} alts n chosen bad spec ids all cov id lines undecided max
	_e=0
	[ -f "$f" ] || { err "$f does not exist"; return 1; }
	_need_sections "$f" "Summary" "Approach" "Alternatives considered" "Design" "AC coverage" "Test strategy" "Risks"
	alts=$(section "$f" "Alternatives considered")
	n=$(printf '%s\n' "$alts" | grep -c '^### ')
	[ "$n" -ge 2 ] || err "list at least 2 real alternatives as '### A1 — <name>' subsections (found $n)"
	chosen=$(printf '%s\n' "$alts" | grep -c '^### .*(chosen)')
	[ "$chosen" = 1 ] || err "mark exactly one alternative '(chosen)' in its heading (found $chosen)"
	bad=$(printf '%s\n' "$alts" | awk '
		/^### / { if (name != "" && !(p && c)) print name; name = $0; p = 0; c = 0; next }
		{ l = tolower($0) }
		l ~ /^[[:space:]]*[-*]?[[:space:]]*\**pros\**:/ { p = 1 }
		l ~ /^[[:space:]]*[-*]?[[:space:]]*\**cons\**:/ { c = 1 }
		END { if (name != "" && !(p && c)) print name }')
	[ -z "$bad" ] || err "each alternative needs a 'Pros:' and a 'Cons:' line — missing in: $(printf '%s' "$bad" | tr '\n' ';')"
	spec=$(art spec)
	ids=$(_ac_text "$spec" | ac_active)
	all=$( { _ac_text "$spec" | ac_active; _ac_text "$spec" | ac_struck; } )
	cov=$(section "$f" "AC coverage" | ac_refs)
	for id in $ids; do in_list "$id" "$cov" || err "'## AC coverage' does not mention $id"; done
	for id in $(doc "$f" | ac_refs); do in_list "$id" "$all" || err "plan mentions $id, which is not in spec.md"; done
	lines=$(fm_body "$f" | wc -l | tr -d ' ')
	max=$(cfg PLAN_MAX_LINES)
	[ "$lines" -le "$max" ] || wrn "plan is $lines lines, over PLAN_MAX_LINES=$max: summarise and point at code"
	section "$f" "Test strategy" | grep -qE '[0-9]+ ?%' && err "'## Test strategy' sets a coverage percentage — name the behaviours and edge cases to test instead"
	if [ "$mode" != draft ]; then
		undecided=$(section "$f" "Open questions" | grep -E '^[[:space:]]*[-*][[:space:]]*(\*\*)?Q[0-9]+' | grep -v 'decided:')
		if [ -n "$undecided" ]; then
			printf '%s\n' "$undecided" | while IFS= read -r l; do printf '  - open question not decided (add "→ decided: <answer>"): %s\n' "$l"; done
			_e=1
		fi
		_markers "$f"
	fi
	return $_e
}

check_tasks() { # file [draft|approve]
	local f=$1 mode=${2:-approve} spec ids all body tids dup t blk v acs a covered seen x dones opens prev prevtxt ptids maxprev old new c delta line verb id
	_e=0
	[ -f "$f" ] || { err "$f does not exist"; return 1; }
	spec=$(art spec)
	ids=$(_ac_text "$spec" | ac_active)
	all=$( { _ac_text "$spec" | ac_active; _ac_text "$spec" | ac_struck; } )
	body=$(doc "$f")
	tids=$(printf '%s\n' "$body" | task_ids_stdin)
	[ -n "$tids" ] || { err "no tasks — add '### T001 — <title>' blocks under '## Tasks'"; return 1; }
	dup=$(printf '%s\n' $tids | sort | uniq -d | tr '\n' ' ')
	[ -z "${dup// /}" ] || err "duplicate task ids: $dup"
	covered="" seen=""
	for t in $tids; do
		blk=$(printf '%s\n' "$body" | task_block_stdin "$t")
		[ -n "$(printf '%s\n' "$blk" | head -1 | sed -E 's/^### T[0-9]+[[:space:]]*(—|-|:)?[[:space:]]*//')" ] || err "$t has no title"
		for x in Do Tests AC Commit; do
			[ -n "$(printf '%s\n' "$blk" | field_stdin "$x")" ] || err "$t is missing '- $x:'"
		done
		acs=$(printf '%s\n' "$blk" | field_stdin AC)
		case $acs in
			[Nn]one*) ;;
			*)
				[ -n "$(printf '%s' "$acs" | ac_refs)" ] || err "$t: '- AC:' must list AC ids, or say 'none — <reason>'"
				for a in $(printf '%s' "$acs" | ac_refs); do
					if printf '%s' "$acs" | grep -qE "$a[[:space:]]*\(remove\)"; then
						in_list "$a" "$all" || err "$t: $a is not in spec.md"
					else
						in_list "$a" "$ids" || err "$t: $a is not an active AC in spec.md"
						covered="$covered $a"
					fi
				done ;;
		esac
		if printf '%s\n' "$blk" | grep -qE '^[[:space:]]*- Manual:'; then
			case $(printf '%s\n' "$blk" | field_stdin Manual) in
				'' | '—' | '-' | '–' | [Nn]one* | [Nn]/[Aa]) err "$t: drop the '- Manual:' line unless a human must check something by hand (it makes the task NEEDS-HUMAN)" ;;
			esac
		fi
		v=$(printf '%s\n' "$blk" | field_stdin Tests)
		printf '%s' "$v" | grep -qiE '[0-9]+ ?%|coverage' && err "$t: '- Tests:' must name behaviours (happy path, edge cases), not coverage"
		_opt_field "$t" "$blk" Size "S M L"
		_opt_field "$t" "$blk" Risk "low high"
		_opt_field "$t" "$blk" Review "skip always"
		_opt_field "$t" "$blk" Verify "targeted full"
		_opt_field "$t" "$blk" Model "haiku sonnet opus"
		v=$(printf '%s\n' "$blk" | field_stdin Commit | tr -d '`')
		[ -z "$v" ] || printf '%s' "$v" | grep -qE '^[a-z]+(\([^)]+\))?!?: .+' || err "$t: '- Commit:' must be a conventional subject, e.g. 'feat(api): add rate limit'"
		for x in $(printf '%s\n' "$blk" | field_stdin Depends | grep -oE 'T[0-9][0-9][0-9]'); do
			in_list "$x" "$seen" || err "$t depends on $x, which is not an earlier task"
		done
		seen="$seen
$t"
	done
	for a in $ids; do printf '%s\n' $covered | grep -qx "$a" || err "$a is not covered by any task"; done

	dones=$(done_tasks)
	opens=""
	for t in $tids; do in_list "$t" "$dones" || opens="$opens $t"; done

	prev=$(fm_get "$f" previous)
	if [ -n "$prev" ]; then
		prevtxt=$(git show "$prev:$f" 2>/dev/null | strip_fm | strip_noise)
		ptids=$(printf '%s\n' "$prevtxt" | task_ids_stdin)
		maxprev=$(printf '%s\n' $ptids | sort | tail -1)
		for t in $dones; do
			in_list "$t" "$ptids" || continue
			old=$(printf '%s\n' "$prevtxt" | task_block_stdin "$t")
			new=$(printf '%s\n' "$body" | task_block_stdin "$t")
			[ "$old" = "$new" ] || err "$t is done — keep its block exactly as approved in $prev; put rework in a new task"
		done
		for t in $tids; do
			in_list "$t" "$ptids" && continue
			[ "$t" \> "$maxprev" ] || err "$t reuses an id; new tasks must be numbered after $maxprev"
		done
	fi

	for c in $(cr_files); do
		[ "$(fm_get "$c" status)" = approved ] && [ -z "$(fm_get "$c" applied)" ] || continue
		delta=$(section "$c" "Delta")
		while IFS= read -r line; do
			verb=$(printf '%s' "$line" | grep -oE 'ADDED|MODIFIED|REMOVED' | head -1)
			id=$(printf '%s' "$line" | grep -oE 'AC[0-9]+' | head -1)
			[ -n "$verb" ] && [ -n "$id" ] || continue
			_cr_task_cover "$f" "$body" "$verb" "$id" "$opens" "$dones" "${c##*/}"
		done <<EOF
$delta
EOF
	done

	[ "$mode" = draft ] || _markers "$f"
	return $_e
}

_opt_field() { # task block field "valid values" — optional fields must hold a known value when present
	local v
	v=$(printf '%s\n' "$2" | field_stdin "$3")
	[ -n "$v" ] || return 0
	case " $4 " in *" $v "*) ;; *) err "$1: '- $3: $v' — use one of: $4" ;; esac
}

_cr_task_cover() { # file body verb id opens dones crname
	local body=$2 verb=$3 id=$4 opens=$5 dones=$6 cr=${7%.md} t acs okrm=0 okadd=0 builtrm=0
	for t in $opens; do
		acs=$(printf '%s\n' "$body" | task_block_stdin "$t" | field_stdin AC)
		if printf '%s' "$acs" | grep -qE "$id[[:space:]]*\(remove\)"; then okrm=1
		elif printf '%s' "$acs" | ac_refs | grep -qx "$id"; then
			okadd=1
			[ "$verb" = REMOVED ] && err "$cr removes $id but open task $t still builds it — drop it or mark '$id (remove)'"
		fi
	done
	for t in $dones; do
		acs=$(printf '%s\n' "$body" | task_block_stdin "$t" | field_stdin AC)
		printf '%s' "$acs" | ac_refs | grep -qx "$id" && builtrm=1
	done
	case $verb in
		ADDED | MODIFIED) [ $okadd = 1 ] || err "$cr $verb $id, but no open task covers it — add a task with '- AC: $id' (rework if it changes done work)" ;;
		REMOVED) if [ $builtrm = 1 ] && [ $okrm = 0 ]; then err "$cr removes $id, which done tasks built — add a task with '- AC: $id (remove)' to take it out"; fi ;;
	esac
}

check_brief() { # file
	local f=$1 ids n steps ns
	_e=0
	[ -f "$f" ] || { err "$f does not exist"; return 1; }
	_need_sections "$f" "Change" "Acceptance"
	ids=$(_ac_text "$f" | ac_active)
	n=$(printf '%s\n' $ids | grep -c .)
	[ "$n" -ge 1 ] || err "no acceptance criteria — add lines like: - **AC1** — When <trigger>, the system shall <result>."
	[ "$n" -le "$QUICK_MAX_ACS" ] || err "too big for /al-quick: $n acceptance criteria (max $QUICK_MAX_ACS) — use /al-spec for this one"
	# shellcheck disable=SC2086
	_shall "$f" $ids
	_ids_kept "$f"
	steps=$(section "$f" "Steps" | grep -E '^[[:space:]]*[-*][[:space:]]+(\*\*)?S[0-9]+')
	ns=$(printf '%s\n' "$steps" | grep -c .)
	[ "$ns" -le "$QUICK_MAX_STEPS" ] || err "too big for /al-quick: $ns steps (max $QUICK_MAX_STEPS) — use /al-spec for this one"
	printf '%s\n' "$steps" | grep -v 'Tests:' | grep -q . && err "every step needs 'Tests: <named behaviour tests>' (or 'Tests: none — <reason>')"
	_markers "$f"
	return $_e
}

check_change() { # file
	local f=$1 name scope class delta bad nac target ids all line verb id t acs dones impact base
	_e=0
	[ -f "$f" ] || { err "$f does not exist"; return 1; }
	name=${f##*/}; name=${name%.md}
	[ "$(fm_get "$f" cr)" = "$name" ] || err "frontmatter 'cr:' must be $name"
	scope=$(fm_get "$f" scope); class=$(fm_get "$f" class)
	case $scope in spec | plan | tasks) ;; *) err "frontmatter 'scope:' must be spec, plan or tasks (got '$scope')" ;; esac
	case $class in clarification | scope-change | approach-change | task-only | reconcile) ;;
		*) err "frontmatter 'class:' must be clarification|scope-change|approach-change|task-only|reconcile (got '$class')" ;; esac
	_need_sections "$f" "Request" "Delta" "Impact" "Recommendation"
	delta=$(section "$f" "Delta")
	bad=$(printf '%s\n' "$delta" | grep -vE '^[[:space:]]*[-*][[:space:]]+((ADDED|MODIFIED|REMOVED)[[:space:]]+AC[0-9]+|none)' | grep .)
	[ -z "$bad" ] && : || err "Delta lines must be '- ADDED|MODIFIED|REMOVED ACn — …' or '- none'; bad: $(printf '%s' "$bad" | head -1)"
	nac=$(printf '%s\n' "$delta" | grep -cE '(ADDED|MODIFIED|REMOVED)[[:space:]]+AC[0-9]+')
	if [ "$nac" -gt 0 ] && [ "$scope" != spec ]; then err "the Delta changes acceptance criteria, so scope must be 'spec'"; fi
	if [ "$nac" = 0 ] && [ "$scope" = spec ] && [ "$class" != clarification ]; then err "scope 'spec' needs AC lines in the Delta (or class 'clarification')"; fi
	if is_quick; then target=$(art brief); else target=$(art spec); fi
	# the Delta is relative to the approved version: with /al-change --adopt the file already holds the edit
	if [ "$(art_state "$target")" = changed ]; then
		base=$(_old_ac_text "$target" "$(approval_sha "$target")")
	else
		base=$(_ac_text "$target")
	fi
	ids=$(printf '%s\n' "$base" | ac_active)
	all=$( { printf '%s\n' "$base" | ac_active; printf '%s\n' "$base" | ac_struck; } )
	dones=$(done_tasks)
	impact=$(section "$f" "Impact")
	while IFS= read -r line; do
		verb=$(printf '%s' "$line" | grep -oE 'ADDED|MODIFIED|REMOVED' | head -1)
		id=$(printf '%s' "$line" | grep -oE 'AC[0-9]+' | head -1)
		[ -n "$verb" ] && [ -n "$id" ] || continue
		case $verb in
			ADDED) in_list "$id" "$all" && err "ADDED $id: that id already exists — use the next unused number" ;;
			*) in_list "$id" "$ids" || err "$verb $id: no active $id in ${target##*/}" ;;
		esac
		if [ "$verb" != ADDED ] && [ -f "$(art tasks)" ]; then
			for t in $dones; do
				acs=$(task_field "$(art tasks)" "$t" AC)
				printf '%s' "$acs" | ac_refs | grep -qx "$id" || continue
				printf '%s\n' "$impact" | grep -q "$t" || err "done task $t implements $id — list it in '## Impact' with its handling"
			done
		fi
	done <<EOF
$delta
EOF
	_markers "$f"
	return $_e
}

check_cr_applied() { # cr-file target-file -> did the target apply the CR's AC delta?
	local c=$1 f=$2 prev cur old line verb id
	_e=0
	prev=$(fm_get "$f" previous)
	cur=$(_ac_text "$f")
	old=""; [ -n "$prev" ] && old=$(_old_ac_text "$f" "$prev")
	while IFS= read -r line; do
		verb=$(printf '%s' "$line" | grep -oE 'ADDED|MODIFIED|REMOVED' | head -1)
		id=$(printf '%s' "$line" | grep -oE 'AC[0-9]+' | head -1)
		[ -n "$verb" ] && [ -n "$id" ] || continue
		case $verb in
			ADDED) printf '%s\n' "$cur" | ac_active | grep -qx "$id" || err "${c##*/} adds $id, but ${f##*/} has no active $id" ;;
			REMOVED) printf '%s\n' "$cur" | ac_struck | grep -qx "$id" || err "${c##*/} removes $id — strike it in ${f##*/}: - ~~**$id**~~ — removed in vN (${c##*/}): reason" ;;
			MODIFIED)
				if ! printf '%s\n' "$cur" | ac_active | grep -qx "$id"; then err "${c##*/} modifies $id, but ${f##*/} has no active $id"
				elif [ "$(printf '%s\n' "$cur" | ac_line "$id")" = "$(printf '%s\n' "$old" | ac_line "$id")" ]; then err "${c##*/} modifies $id, but its text in ${f##*/} is unchanged"
				fi ;;
		esac
	done <<EOF
$(section "$c" "Delta")
EOF
	return $_e
}
