---
feature: 033-docs-correctness
kind: chore
created: 2026-11-22
branch: chore/033-docs-correctness
---
# Docs correctness

## Problem
Several docs describe a main that no longer exists: removed commands and tooling (the
agent-loop kit, `gateway sessions`, `gateway timeline`, `backfill-sessions`), the UI as it was
before 027 and 029, stale statuses, and rules that v1 does not follow (a stateless process).
Many spec and ADR statuses were never moved past `draft` or `proposed`.

## Goal
Every existing doc says what main actually does. Code is the source of truth. Wrong or stale
claims are fixed, and the listed spec and ADR statuses are set.

## Non-goals
- No new docs (README rewrite, getting-started, what-gets-recorded, architecture).
- No rewording for style.
- No code changes.
- Nothing under `docs/manual-checks/`.
- Spec and ADR bodies are history: only the listed status changes and dated notes.

## Acceptance criteria
- **AC1** — Every tracked `.md` outside `specs/` and `docs/manual-checks/`, and
  `config.example.yaml`, is assigned to one group in `research.md` and checked against the
  fact sheet and the code.
- **AC2** — Each changed claim is true on main, with the code that proves it recorded.
- **AC3** — Specs 018–027 are `done` with the note "Built without the agent-loop kit."; specs
  007, 010, 011, 012, 028, 029, 031, 032 are `done`; ADRs 0008, 0017, 0018 are `accepted`,
  0017 with the dated note "Widened to optional profile interfaces."
- **AC4** — Every relative link in the changed files resolves to an existing file.
- **AC5** — Outside `specs/` and `docs/manual-checks/`, no `loop.sh`, `/al-`, `kit-test`,
  `plan.md`, `tasks.md`, `gateway sessions`, `gateway timeline`, `backfill-sessions`,
  `Dockerfile` or `docker-compose.yml` remains, except in ADR 0021, ADR history lines and
  the kit notes in AGENTS.md and the roadmap.
- **AC6** — `make verify` is green before every commit.

## Edge cases
- **E1** — A doc fits no group → group 4 (AC1).
- **E2** — A spec has no `status:` line (029, 031, 032) or keeps it in `brief.md` (007, 011,
  012) → the status is set where the frontmatter lives (AC3).
- **E3** — A claim is true but phrased badly → left alone (Non-goals).
