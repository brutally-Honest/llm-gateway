---
feature: 014-file-and-network-effects
status: draft
version: 1
approved:
approved-by:
sha256:
fingerprint:
plan-fingerprint:
previous:
---
# Tasks — 014-file-and-network-effects

Built outside the agent-loop kit (research.md D1): one branch, tasks in this order
(foundation → files → network → check), `make verify` (lint included) after every task,
functional tests only, everything checked together in T012.

### T001 — Add the effect kind and the harness working folder
- Do: `internal/core/`: `Effect`, its kinds and fields, the optional `EffectRules` profile
  interface (`ReadTarget`, `ReadContent`, `WritePaths`, `ExtraDirs`), and `WorkDir` on
  `HarnessEvent`; extend the core purity test's scan if it needs the new file; write
  `docs/decisions/0014-effect-kind-and-no-root-sensors.md` (own table like ADR 0012, checkpoint
  and proxy sensors, why not OS watchers or Docker).
- Tests: none — types and an ADR; used and tested from T003 on
- AC: none — foundation for AC1–AC15
- Commit: feat(capture): add the effect kind and the harness working folder
- Depends: —
- Size: S
- Risk: low

### T002 — Add the effects config block
- Do: `internal/config/effects.go` with the block from plan.md (defaults, `GATEWAY_*` env
  overrides, loopback and distinct-address checks, absolute `extra_dirs`,
  `snapshot_retention`); document it in `config.example.yaml`, with the comment that `.env` is
  left out of `ignore` on purpose.
- Tests: TestLoad_EffectsDefaults
- AC: AC4, AC12 (config side)
- Commit: feat(config): add the effects config block
- Depends: T001
- Size: S
- Risk: low

### T003 — Add the effects table and effect versions
- Do: one new migration (`effects`, its index and `effect_snapshots` from plan.md);
  `internal/store/effects.go`: `SaveEffects`, `SaveCheckpoint` (effects and snapshot rows in one
  transaction), `LoadSnapshot`, `DeleteSnapshot`, `SweepSnapshots`, `ActiveSessions`,
  `ListEffects`, `ListUnassigned`, `EffectVersions` and `EffectCounts` with the
  `SessionVersions` CTE-join pattern; `SessionVersions` untouched.
- Tests: TestEffects_SessionVersionUnchanged (save effects for a session, its
  `SessionVersions` value is equal before and after, its `EffectVersions` value moved)
- AC: AC13
- Commit: feat(store): add the effects table and effect versions
- Depends: T001
- Size: M
- Risk: low

### T004 — Serve effects, content and diffs with a live event
- Do: `internal/effects/diff.go` (Myers line diff, 2,000-line cap); `internal/ui/api_effects.go`
  (`/api/sessions/{id}/effects`, `/api/effects/{id}/content`, `/api/effects/{id}/diff`,
  `/api/effects/unassigned`); `EffectCounts` merged into the fresh part of `buildSummary` and
  `unassigned_connections` on `GET /api/sessions`; hub `effects` event, at most one per session
  per 500 ms, never touching the timeline cache.
- Tests: TestEffectsAPI_ListAndDiff (seeded file effects come back in id order with
  `after` paging; a two-version file gives the expected diff; a binary effect's diff is a 404
  with a reason)
- AC: AC13, AC15
- Commit: feat(ui): serve effects, content and diffs with a live event
- Depends: T003
- Size: M
- Risk: low

### T005 — Show effects per turn and on the sessions list
- Do: `web/effects.js` (`placeEffects`, glyph + word helpers); `timeline-page.js` folded row per
  turn, expanding to files then connections, coverage line; `drawer-sections.js` "Effect"
  section loading `/diff` or `/content` on open; `sessions-page.js` Effects column and the
  unassigned link; `route.js` `#/effects/unassigned` page; live `effects` event fetches
  `after=<last id>`.
- Tests: effects.test.js: placesEffectsInTurns (effects before the first prompt, inside turn 1,
  at a turn boundary, after the last turn)
- AC: AC14, AC15
- Commit: feat(ui): show effects per turn and on the sessions list
- Depends: T004
- Size: M
- Risk: low

### T006 — Map the working folder and the effect rules
- Do: `internal/clients/claudecode/harness.go` sets `WorkDir` from `cwd`;
  `internal/clients/claudecode/effects.go` implements `EffectRules`: Read/Grep/Glob read targets
  with line range or pattern, `tool_response` content from a Read payload, Write/Edit/MultiEdit/
  NotebookEdit write paths, `additionalDirectories` from `~/.claude/settings.json`,
  `<cwd>/.claude/settings.json` and `<cwd>/.claude/settings.local.json`.
- Tests: TestClaudeCode_EffectRules (fixture payloads in `testdata/` for each tool, a missing
  settings file, a settings file with two extra folders)
- AC: AC6, AC7
- Commit: feat(claude-code): map the working folder and effect rules
- Depends: T001
- Size: S
- Risk: low

### T007 — Store read effects from hook events
- Do: `hooks.HookStored` gains `ID` and the mapped `core.HarnessEvent`;
  `internal/effects/reads.go` stores a `file_read` effect for each post-tool event the
  profile's `ReadTarget` accepts; wire it to `OnStored` in `cmd/gateway/run.go` behind
  `effects.enabled`.
- Tests: none new — the mapping is covered by T006's fixture test; the stored row by manual
  step 5
- AC: AC7
- Commit: feat(capture): store read effects from hook events
- Depends: T003, T006
- Size: S
- Risk: low

### T008 — Run file checkpoints on hook events
- Do: `internal/effects/`: `runner.go` (per-session cache loaded from `effect_snapshots`, one
  running and one queued, evicted from memory after 1 h without a hook, snapshot deleted at
  session end, `SweepSnapshots` at startup and hourly, active-session set seeded from
  `ActiveSessions` for T009), `roots.go` (start folder,
  `ExtraDirs`, `extra_dirs`, learned roots, rejected roots), `git.go` (`--no-optional-locks`
  status, ignored-files listing, `show HEAD:<path>`, timeouts), `scan.go`, `checkpoint.go`
  (baseline, compare, copies, before version, one `SaveCheckpoint` per checkpoint); wire to
  `OnStored`.
- Tests: TestCheckpoint_GitRepoChanges, TestCheckpoint_PlainFolder,
  TestCheckpoint_GitIgnoredEnvSeen, TestCheckpoint_RestartKeepsSnapshot,
  TestRoots_LearnedAndRejected,
  TestRunner_BurstRunsOneFollowUp (slow fake checkpoint, 50 signals, exactly 2 runs; break the
  dirty flag, see it fail, restore)
- AC: AC1, AC2, AC3, AC4, AC5, AC6
- Commit: feat(capture): run file checkpoints on hook events
- Depends: T003, T006
- Size: L
- Risk: high

### T009 — Add the HTTP and SOCKS5 forward proxy listeners
- Do: `internal/netproxy/`: `http.go` (CONNECT tunnels, plain HTTP forwarding with a
  proxy-less transport, header stripping, query string dropped), `socks5.go` (RFC 1928,
  no-auth CONNECT only), `record.go` (bounded queue, one store worker, drop counter, session
  link from the runner's active set); start and stop both listeners in `cmd/gateway/run.go`,
  open tunnels stored as `closed early` at shutdown, counters in the shutdown log line.
- Tests: TestNetProxy_ConnectCountsBytes (loopback echo server, known byte counts, outcome
  `ok`), TestNetProxy_PlainHTTPRecordsURL (no query string stored, no `Proxy-Authorization`
  forwarded or stored), TestNetProxy_Socks5Connect
- AC: AC8, AC9, AC10
- Commit: feat(proxy): add http and socks5 forward proxy listeners
- Depends: T003, T008
- Size: L
- Risk: high

### T010 — Skip the gateway's own proxy for upstream calls
- Do: `internal/core/proxy.go` wraps `http.ProxyFromEnvironment`; a proxy URL whose host:port
  is one of the gateway's proxy listeners gives a direct connection.
- Tests: TestUpstream_SkipsOwnProxyListener
- AC: AC11
- Commit: fix(proxy): skip the gateway's own proxy for upstream calls
- Depends: T009
- Size: S
- Risk: low

### T011 — Print the env and sandbox blocks in hooks print
- Do: `claudecode.SettingsDoc` (hooks + `env` + optional `sandbox`), used by
  `cmd/gateway/hooks.go`; with network effects disabled the output is byte-for-byte today's.
- Tests: TestHooksPrint_SettingsDoc (network off equals today's output; network on with
  sandbox on and off; SOCKS5 off leaves out `socksProxyPort`)
- AC: AC12
- Commit: feat(claude-code): print the env and sandbox blocks in hooks print
- Depends: T002
- Size: S
- Risk: low

### T012 — Add the effects setup, seed and manual check
- Do: "Files and network (014)" section in `docs/clients/claude-code.md` (build, config, save
  the printed settings file, systemd and launchd login service, the `claude` function with its
  health-check fallback, remove old hooks from `~/.claude/settings.json`);
  `docs/manual-checks/seed-effects.mjs`; `docs/manual-checks/014-effects.md`; README status.
- Tests: none new — docs and a seed script; `make verify` stays green
- AC: AC10, AC14, AC15 (manual)
- Commit: docs(claude-code): add the effects setup, seed and manual check
- Depends: T005, T007, T008, T009, T010, T011
- Size: M
- Risk: low
- Manual: docs/manual-checks/014-effects.md
