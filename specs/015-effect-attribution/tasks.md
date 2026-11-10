---
feature: 015-effect-attribution
status: done
version: 1
approved:
approved-by:
sha256:
fingerprint:
plan-fingerprint:
previous:
---
# Tasks — 015-effect-attribution

Built outside the agent-loop kit (research.md D1): one branch, feature tasks first, the seed
early, functional tests together near the end, `make verify` green after every commit, leaks
patched as found and logged as dated lines in research.md.

### T001 — Add the attribution labels and profile interface
- Do: `internal/core/effect.go`: `Attribution` and its five values, `AttributionRules`
  (`NamesTarget`, `HarnessHost`, `IsAgentTool`), `WindowFrom`/`WindowTo` on `Effect`;
  `docs/decisions/0015-effect-attribution-at-read-time.md` (label set, A1 vs A2, flip
  condition).
- Tests: none — types and an ADR; tested from T009 on
- AC: none — foundation for AC1–AC12
- Commit: feat(capture): add the effect attribution labels
- Depends: —
- Size: S
- Risk: low

### T002 — Seed every attribution label
- Do: `docs/manual-checks/seed-effects.mjs`: the attribution turn from plan.md (named file
  and connection, only one running, two parallel calls, a sub-agent overlapping a main-thread
  call, an edit between Stop and the next prompt, a harness CONNECT); print the expected label
  per effect.
- Tests: none — a seed script; run once against the current build (labels absent until T007)
- AC: AC3–AC9 (manual)
- Commit: docs(docs): seed every effect attribution label
- Depends: T001
- Size: S
- Risk: low

### T003 — Record harness hosts and hook skew from fresh data
- Do: after the store is cleared (part A), one real Claude Code session (edit, a Bash script
  writing files, `curl https://example.com`, a WebFetch, parallel reads, a sub-agent); from
  the API, list every connection host with its time, and for each Bash `curl` the gap between
  its PreToolUse receive time and the connection start, and between the connection end and
  the PostToolUse; record both in research.md and set the start host list and `skew`.
- Tests: none — research
- AC: AC3, AC5 (inputs)
- Commit: docs(docs): record harness hosts and hook skew for 015
- Depends: —
- Size: S
- Risk: medium (the default skew and host list come from here)

### T004 — Store checkpoint windows and hook working folders
- Do: one migration (`effects.window_from`, `effects.window_to`, `hook_events.work_dir`);
  `insertEffects`, `effectSelect`, `EffectRow`, `SaveHook`, `HookRow`; `LastHookBefore`;
  `effects.attribution.skew` config block with env override and `config.example.yaml`.
- Tests: none new — covered by T009
- AC: AC1, AC2 (storage)
- Commit: feat(store): store checkpoint windows and hook working folders
- Depends: T001
- Size: S
- Risk: low

### T005 — Give each file change its checkpoint window
- Do: `internal/effects/runner.go`, `checkpoint.go`: start and end times per checkpoint,
  `prevStart` kept in memory after every checkpoint, the last-hook fallback on load (E7).
- Tests: none new — covered by T009
- AC: AC1
- Commit: feat(capture): give each file change its checkpoint window
- Depends: T004
- Size: S
- Risk: medium (runner timing; 014's one-running-one-dirty rule must hold)

### T006 — Add claude code attribution rules
- Do: `internal/clients/claudecode/attribution.go`: `NamesTarget` (write, read, search, fetch
  and Bash token rules), `HarnessHost` (exact and dot-suffix entries, list from T003),
  `IsAgentTool`; compile-time check against `core.AttributionRules`.
- Tests: none new — covered by T009
- AC: AC5, AC6
- Commit: feat(claude-code): add effect attribution rules
- Depends: T001, T003
- Size: S
- Risk: low

### T007 — Label effects when serving them
- Do: `internal/effects/attribute.go` (spans, candidates, labels, per-request input cache);
  `internal/ui/api_effects.go`: `attribution` on the session list and the unassigned list.
- Tests: none new — covered by T009
- AC: AC3–AC11
- Commit: feat(ui): label effects with the tool call that caused them
- Depends: T004, T005, T006
- Size: M
- Risk: high (the core of the feature)

### T008 — Show the attribution on effects rows
- Do: `web/effects.js` `attributionText`; `timeline-page.js` and the unassigned page add one
  text cell per row; `#n` from `callNumbers` matched by tool-call id.
- Tests: none new — covered by T009 and T010
- AC: AC12
- Commit: feat(ui): show the attribution on effects rows
- Depends: T007
- Size: S
- Risk: low

### T009 — Add the effect attribution functional tests
- Do: `TestCheckpoint_StoresWindow`, `TestAttribute_Labels`, `TestClaudeCode_NamesTarget`,
  `TestEffectsAPI_Attribution`, `effects.test.js: attributionText`. Break one rule in
  `attribute.go` (several → only one), see `TestAttribute_Labels` fail, restore.
- Tests: the five above
- AC: AC1–AC12
- Commit: test(capture): add effect attribution functional tests
- Depends: T008
- Size: M
- Risk: low

### T010 — Add the attribution manual check
- Do: `docs/manual-checks/015-attribution.md` (seed in a fresh data folder; every label with
  glyph + word and `#n Name`; `several` lists its candidates; unassigned page shows `harness`;
  no layout change against 014; a real Claude Code session); run it with `/ui-check`; record
  the result in research.md.
- Tests: none new — manual
- AC: AC12 (manual), AC3–AC9 end to end
- Commit: docs(docs): add the effect attribution manual check
- Depends: T002, T009
- Size: S
- Risk: low
- Manual: docs/manual-checks/015-attribution.md
