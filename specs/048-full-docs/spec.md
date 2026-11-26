---
feature: 048-full-docs
kind: chore
created: 2026-11-26
branch: chore/048-full-docs
status: done
---

## Problem
033 fixed only wrong claims. The README still reads as a project log, there is no page for a
non-technical user, no single page on what is and is not recorded, no architecture or UI
page that matches main after 027-047, and workflow rules are split between AGENTS.md and
PLAN.md with PLAN.md set to win.

## Goal
The docs are rewritten to match the code and UI on main today, every claim checked against
the code. AGENTS.md is the one home for workflow rules; PLAN.md keeps only the plan.

## Non-goals
- No code, config or test changes.
- No screenshots (the release PR adds them from seeded fictional data); the README leaves a
  place for them.
- No investigation of the AppArmor / bwrap sandbox issue (after v1); it is only documented.
- Spec and ADR bodies stay history: only status lines, broken links and pointers change.

## Acceptance criteria
- **AC1** `docs/what-gets-recorded.md` covers what is recorded, what bypasses the gateway,
  several accounts, several sessions, the Node version floor and `NODE_USE_ENV_PROXY`, the
  AppArmor/bwrap note, privacy and disk growth.
- **AC2** `docs/architecture.md` covers components, ports, store, capture, the view cache,
  live updates, shutdown order and the degraded health state; the UI is described as it is
  after 027, 029, 042, 043, 045, 046 and 047.
- **AC3** `README.md` is rewritten and `docs/getting-started.md` exists for non-technical
  users; every command in them exists in the code.
- **AC4** AGENTS.md holds every workflow rule; PLAN.md no longer says it overrides AGENTS.md
  and points to it; no file in the repo says otherwise.
- **AC5** Remaining docs are swept for stale claims; spec and ADR status lines follow the rule;
  every relative link resolves.
- **AC6** A fresh review checks at least 40 claims against the code and finds no P1-P3 left.
- **AC7** No text or numbers from the owner's real data are in the repo.

## Edge cases
- A rule in PLAN.md conflicts with AGENTS.md: PLAN.md's wording wins, logged as a D-entry.
- A claim cannot be checked against the code: the doc words it as such, and research.md says
  so.
