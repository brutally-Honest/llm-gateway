---
feature: 049-notes-purge-cleanup
kind: chore
created: 2026-11-26
branch: chore/049-notes-purge-cleanup
status: draft
---

## Problem
Small leftovers after 048: the repo still sets up and documents pushing git notes, the
`gateway uninstall --purge` wording may not match what the code deletes, a UI comment says
the reconnect dot is amber when it is red, the "no telemetry" claim has no written list of
outbound destinations behind it, and the 048 round-2 fixes were never re-checked.

## Goal
Notes are local only everywhere; `--purge` help and docs say exactly what is deleted and
kept; the app.js comment matches the code; the outbound destinations are listed and the docs
agree with them; the 048 round-2 diff is checked and fixed.

## Non-goals
- No change to what `--purge` deletes, or to any UI behaviour.
- No change to `.git/config` of any clone; no git config run by this work.
- Spec and ADR bodies stay history.

## Acceptance criteria
- **AC1** When a contributor reads AGENTS.md or runs `make setup`, no notes fetch refspec is
  added and nothing in the repo pushes `refs/notes`; AGENTS.md says notes stay local.
- **AC2** When a user reads `gateway uninstall` help, docs/install.md or
  docs/getting-started.md, the system shall describe what `--purge` deletes and keeps exactly
  as the code does.
- **AC3** The comment above `liveText` in app.js names the colours the code shows.
- **AC4** research.md lists every outbound destination the binary can contact; no doc claims
  more than the code supports.
- **AC5** The 048 round-2 fix commit is checked for typos, broken relative links and
  contradictions; findings are fixed.
- **AC6** A fresh review finds no P1-P3 left (at most two rounds).

## Edge cases
- **E1** A test asserts the old notes behaviour → the test changes with the behaviour (AC1).
- **E2** A custom `capture.dir` or the macOS logs are not deleted by `--purge` → the help and
  docs say they are kept (AC2).
