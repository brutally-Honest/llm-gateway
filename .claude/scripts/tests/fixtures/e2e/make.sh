#!/usr/bin/env bash
# make.sh — builds the e2e fixture: a tiny repo whose feature 001-e2e has an approved spec,
# plan and tasks, saved as repo.bundle next to this script.
#
# Approvals are human-only, so this takes three steps:
#   1. make.sh prepare          -> creates a scratch repo with the drafts and prints its path
#   2. you run the approval script there on the spec and the plan (prepare prints the commands)
#   3. make.sh export <path>    -> checks the approvals and writes repo.bundle
#
# The bundle keeps the exact commits, so the approval records stay valid when the test
# clones it. Rebuild it only when the fixture's specs change.
set -euo pipefail
HERE=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
KIT=$(cd "$HERE/../../../.." && pwd)   # the real .claude/

prepare() {
	local d
	d=$(mktemp -d "${TMPDIR:-/tmp}/al-e2e-fixture.XXXXXX")
	cd "$d"
	git init -q -b main
	git config user.name fixture
	git config user.email fixture@example.invalid
	git config commit.gpgsign false
	mkdir -p .claude
	cp -R "$KIT/scripts" "$KIT/hooks" "$KIT/templates" "$KIT/agents" .claude/
	rm -rf .claude/scripts/tests
	cp "$KIT/loop.conf" .claude/loop.conf
	printf '.agent-loop/\n' > .gitignore
	git add .gitignore .claude
	git commit -q -m "chore(repo): seed the e2e fixture repo"
	git switch -q -c feat/001-e2e
	cp -R "$HERE/src/specs" .
	git add specs
	git commit -q -m "docs(001-e2e): draft spec, plan and tasks"
	echo "Scratch repo: $d"
	echo "Now run, in your own terminal:"
	echo "  cd $d && .claude/scripts/approve.sh spec && .claude/scripts/approve.sh plan"
	echo "If the plan approval leaves tasks.md as a draft, also run: .claude/scripts/approve.sh tasks"
	echo "Then: $HERE/make.sh export $d"
}

export_bundle() {
	local d=$1 out
	[ -d "$d/.git" ] || { echo "not a repo: $d" >&2; exit 1; }
	for k in spec plan tasks; do
		out=$(cd "$d" && .claude/scripts/loop.sh check "$k" 2>&1) || { echo "$k does not pass its checks: $out" >&2; exit 1; }
		grep -q '^status: approved' "$d/specs/001-e2e/$k.md" || { echo "$k.md is not approved yet" >&2; exit 1; }
	done
	[ -z "$(git -C "$d" status --porcelain)" ] || { echo "the scratch repo has uncommitted changes" >&2; exit 1; }
	git -C "$d" bundle create "$HERE/repo.bundle" main feat/001-e2e
	echo "Wrote $HERE/repo.bundle"
}

case ${1:-} in
	prepare) prepare ;;
	export) export_bundle "${2:?usage: make.sh export <scratch repo path>}" ;;
	*) echo "usage: make.sh prepare | make.sh export <scratch repo path>" >&2; exit 1 ;;
esac
