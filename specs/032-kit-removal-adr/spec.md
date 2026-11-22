---
feature: 032-kit-removal-adr
kind: chore
created: 2026-11-22
branch: chore/032-kit-removal-adr
---
# Record why the agent-loop kit was removed

## Problem
Spec 031 removed the agent-loop kit, but no ADR says why. ADR 0011 is superseded, and the
notes in `AGENTS.md` and `docs/roadmap.md` say the kit may come back without pointing to
the reasons it left.

## Goal
Record the removal as ADR 0021 (context, decision and reasons, alternatives,
consequences) and point ADR 0011, `AGENTS.md` and `docs/roadmap.md` to it.

## Non-goals
Docs only: no code, config, CI or Makefile change, no edit to other specs, and no other
wording change in the files that get a pointer.

## Acceptance criteria
`docs/decisions/0021-remove-the-agent-loop-kit.md` exists in the template's shape with
`status: accepted` and `date: 2026-11-22`; ADR 0011's superseded note, the `AGENTS.md`
kit line and the roadmap "Agent-loop kit" line each name ADR 0021; `make verify` is
green.
