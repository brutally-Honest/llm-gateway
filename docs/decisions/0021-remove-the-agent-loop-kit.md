---
status: accepted
date: 2026-11-22
---

# 0021 — Remove the agent-loop kit

## Context
The agent-loop kit (brutally-Honest/agent-loop) ran each change through spec, plan and
tasks files, approval hashes, a loop script and guard hooks. Specs 003-017 were built
with it. From 018 on, changes were built without it: a short `spec.md` and `research.md`,
built without stopping, with judgment calls recorded as D-entries.

## Decision
Remove the kit from this repo (spec 031) and keep the `spec.md` and `research.md` flow.
The kit may be reintroduced once its problems are fixed upstream.

The approach was right: written specs, recorded decisions, gated steps. The execution was
messy. The kit was a prototype and not fully working. It was slow: its tests added about
a minute to every `make verify`, locally and on both CI runners, and its guard hook ran
before every tool call. It blocked too often, and the stops for approvals made the
experience bad.

## Alternatives
- **Fix the kit in place in this repo.** Rejected: fixes belong upstream, in
  brutally-Honest/agent-loop.
- **Keep it installed but unused.** Rejected: it costs about a minute per push and a hook
  on every tool call.

## Consequences
- Specs 003-017 keep their kit frontmatter as history, and nothing checks it.
- `make verify` and CI got faster; the numbers are in
  `specs/031-remove-clutter/research.md`.
- To bring the kit back, install it fresh from brutally-Honest/agent-loop once it is
  fixed, using the settings recorded in `specs/031-remove-clutter/research.md`.
