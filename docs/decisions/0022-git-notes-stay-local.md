---
status: accepted
date: 2026-11-26
---

# 0022 — Git notes stay local

## Context
AGENTS.md said notes are local by default, and that pushing them needs the human's
go-ahead. `make setup` also added a `+refs/notes/*:refs/notes/*` fetch refspec, so pushed
notes could be fetched back. The pre-push hook let `refs/notes` pushes through unchecked,
as [ADR 0001](0001-local-gate-instead-of-ci.md) says ("notes and tags are pushed
unchecked"). GitHub does not display notes, so a pushed note helps no reader. It only
changes a remote ref.

## Decision
Notes stay local and are never pushed.
- `make setup` no longer adds the notes fetch refspec. It keeps `notes.rewriteRef`, so
  notes survive a rebase.
- `.githooks/pre-push` rejects any push to `refs/notes/*`, deletions included.

This amends ADR 0001 on that one point. The rest of ADR 0001 still stands.

## Alternatives
- **A docs-only rule, with no hook check.** It is simpler. But the rule would rest on
  memory, and a rule the repo does not enforce tends to get broken.
- **Keep pushing notes, with the human's go-ahead each time.** It keeps notes on the
  remote. But GitHub shows nothing, so each push costs a remote-ref change for no reader.

## Consequences
- Notes exist only in the clone that wrote them. The PR description carries the
  essentials: the spec link and the acceptance-criteria evidence.
- A clone made before this change may still have a notes fetch refspec, with any
  destination (for example `refs/notes/*:refs/notes/remote/*`). Remove every notes fetch
  refspec with `git config --unset-all remote.origin.fetch '^\+?refs/notes/'`.
- Cost of reversing: remove the hook check and its test (`TestPrePush_RejectsNotes` in
  `test/githooks/prepush_test.go`), and add the refspec back in `make setup`.
