---
feature: 003-sessions-and-timeline
status: done
version: 2
approved: 2026-10-11T06:13:21Z
approved-by: human
sha256: 68ded52f294d8dc6aa2e1eeba379f92079ad7c09d717a489993741c76c7056a0
fingerprint: 1284f4cf54429e25fb8fc075f8a2bfa37ea2c8586ba2547e39de1f2513100c22
plan-fingerprint: ef486a396a618b8458a57e11d453330b33a05a9a25a8d9795e3c1557a9b1d688
previous: 0fe04e7
---
# Tasks — 003-sessions-and-timeline

<!-- One block per task, in build order. Each task = one commit that leaves verify green.
     Tasks not started yet stay editable after approval (reorder, split, merge, retitle);
     done tasks are frozen. Progress lives in .agent-loop/ and git (see: loop.sh status).
     Size: S|M|L picks the model, Risk: high forces a review; Model:, Review: skip|always,
     Verify: targeted|full are optional overrides.

### T001 — <imperative title>
- Do: <what changes, which packages/files>
- Tests: <named behaviour tests: the happy path + the spec's edge cases it touches>   (or: none — <reason>)
- AC: AC1, AC2   (or: none — <reason>; in amendments: AC2 (rework), AC4 (remove))
- Commit: feat(scope): <subject>
- Depends: —   (or: T00n, earlier tasks only)
- Size: S   (S | M | L)
- Risk: low   (low | high)
- Manual: <only for a check a human must run by hand; makes the task NEEDS-HUMAN>
-->

## Tasks

### T001 — Add the session key contract to core capture
- Do: `internal/core`: optional `SessionKeyer` interface (adapter.go), `ValidSessionKey` (empty, >256 bytes, control chars), `Registry.profileFor` shared by `Identify` and a new `sessionKey`, passed to `newProxy` (`NewProxy` passes nil); `Proxy.ServeHTTP` reads the key under `recover`, logs `session_key_rejected` with request id and reason only, sets a Meta field; `Exchange.SessionKey` set in `finishCapture`.
- Tests: TestProxy_SessionKeyReachesSink (AC1); TestProxy_NoKeyOrUnknownClientCapturedUngrouped (AC3); TestProxy_InvalidSessionKeyStoredUngrouped (AC4, E1: empty, 257 bytes, 256 accepted, control char, log has reason not value); TestValidSessionKey (AC4); TestProxy_TestOnlyProfileKeyNeedsNoCoreChange (AC5); TestProxy_SessionKeyWithDroppedCaptureStillSucceeds (E2); TestProxy_PanickingProfileMeansNoKey.
- AC: AC1, AC3, AC4, AC5
- Commit: feat(proxy): read an opaque session key from the client profile
- Depends: —
- Size: M
- Risk: high

### T002 — Store the session key in a new migration step
- Do: `internal/store`: migration 2 (`ADD COLUMN session_key TEXT`, index on principal, client, key, started_at); `SaveExchange` writes the key; `ExchangeRow.SessionKey` (`omitempty`) read by `scanExchange`; update existing tests that assume `user_version` 1.
- Tests: TestMigrate_SessionKeyStepAddsColumnAndIndex (AC6); TestMigrate_ReleasedStepUnchanged (AC6); TestSaveExchange_StoresSessionKey (AC1); TestSaveExchange_NoKeyStoresNull (AC3); TestDump_UngroupedRowUnchanged (AC3).
- AC: AC1, AC3, AC6
- Commit: feat(store): add a session key column to exchanges
- Depends: T001
- Size: M
- Risk: high

### T003 — Report the session key from the Claude Code profile
- Do: `internal/clients/claudecode`: `SessionKey(http.Header)` returns the `x-claude-code-session-id` value; end-to-end capture through `run` in `cmd/gateway`.
- Tests: TestProfile_SessionKeyFromHeader (AC2); TestProfile_NoHeaderNoKey (AC2); TestRun_CapturesSessionKeyFromClaudeCodeHeader (AC2); TestRun_UnknownClientCapturedUngrouped (AC3).
- AC: AC2, AC3
- Commit: feat(claude-code): report the session id header as the session key
- Depends: T002
- Size: S
- Risk: low

### T004 — Capture a real Claude Code session as a fixture
- Do: with the T001-T003 gateway, capture a real Claude Code session on a subscription login that uses the Task tool. Run it in a fresh directory under /tmp that holds only a few harmless files, is not a git repo and has no project CLAUDE.md; the prompt must only touch those files. Save the store under `testdata/sessions/claude-code-task/` at the repo root, with no auth headers or keys (002's redaction applies; check by grep). Before committing, grep the stored bodies, events and every content blob they reference (a hashed system prompt included) for the home path, username, email, hostname, `metadata.user_id` and any ~/.claude/CLAUDE.md text. If anything identifying is in the raw request or response bodies, commit only what TestBuild_RealSessionFixture needs (exchange rows, canonical events and the content they reference) instead of the full bodies, and say so in research.md. The referenced content must pass the same grep. If the grep still fails on content the fixture needs (exchange rows, canonical events or content they reference), stop: add `- **Qn** (open) T004 — <question> — options: A) … B) …` to research.md under `## Open questions` and do not commit. Record in research.md whether the sub-agent sent the same `x-claude-code-session-id` as the parent.
- Tests: none — fixture for T006; the loading test is TestBuild_RealSessionFixture there
- AC: none — fixture and research finding; AC28 is closed by T011
- Commit: test(observability): add a captured claude code session fixture
- Depends: T003
- Size: S
- Risk: low
- Manual: NEEDS-HUMAN until the user confirms in research.md that the privacy review above found nothing identifying (or that only the reduced fixture was kept). Steps: before the session run `mv ~/.claude/CLAUDE.md ~/.claude/CLAUDE.md.aside`; capture in the /tmp directory; right after the session ends run `mv ~/.claude/CLAUDE.md.aside ~/.claude/CLAUDE.md`; copy to the fixture path, run the greps, note the findings and the sub-agent session-id result in research.md.

### T005 — Add session queries to the store reader
- Do: `internal/store` reader: `SessionRows(limit)`, `SessionsByKey`, `SessionExchanges(principal, client, key)` ordered by start then request id, `UngroupedSummary`, `UngroupedExchanges(limit)`; read-only.
- Tests: TestReader_SessionsKeyedByPrincipalClientAndKey (AC7); TestReader_SessionsNewestActivityFirst (AC8); TestReader_SessionsLimit (AC9: n and 0 = all); TestReader_UngroupedSummary (AC10); TestReader_UngroupedExchangesNewestFirst (AC11); TestReader_SessionExchangesTieOrdersByRequestID (E4); TestReader_SessionsByKeyAcrossClients (AC20); TestReader_SessionsEmptyStore (E8).
- AC: AC7, AC8, AC9, AC10, AC11, AC20
- Commit: feat(store): query sessions and ungrouped exchanges
- Depends: T002
- Size: M
- Risk: high

### T006 — Build the de-duplicated timeline
- Do: new `internal/timeline`; add a `TestBuild_RealSessionFixture` that loads the T004 fixture through `store.OpenReader` (the fixture lives outside `internal/timeline`, which the purity test scans), and use it where it fits (ordering, de-dup, tool events); (`Source` interface, entry types, `Build`, `Options{Full}`): ordering, best-match shared prefix with tie-breaks, `history diverged`, new threads, folded exchanges, partial marking, tool events following their message, per-entry cut; extend `core/purity_test.go` to walk `../timeline` with the full word list.
- Tests: TestBuild_RealSessionFixture (AC12, AC13, AC27: loads the T004 fixture; also asserts its request events carry a `system_hash` with no matching content in the store, and that `Build`, default and `Full`, still returns every exchange and event of the session without error, research.md D12); TestBuild_OrdersExchangesByStartThenRequestID (AC12, E4); TestBuild_EventOrderWithinExchange (AC12); TestBuild_ShowsOnlyMessagesAfterSharedPrefix (AC13); TestBuild_RepeatedUserMessageShownAgain (E5); TestBuild_TieBreaksPrefixEqualThenMostRecent (E13); TestBuild_RewriteMarksHistoryDiverged (AC14, E12); TestBuild_NoMarkerWhenBestMatchIsPrefix (AC14); TestBuild_NewThreadWhenNoSharedMessage (AC27); TestBuild_SubAgentInterleaveNoMarkers (E6); TestBuild_FoldsExchangesWithoutMessages (AC15, E3); TestBuild_FoldsFailedSkippedUnsupported (AC16); TestBuild_PartialExchangeShowsEventsMarked (AC16, E7); TestBuild_ToolEventsOfHiddenMessagesAreHidden (AC13); TestBuild_CutsLongEntriesAndNotesBytes (AC17); TestBuild_FullKeepsEverything (AC17); TestCore_NoProviderOrClientIdentifiers (AC26).
- AC: AC12, AC13, AC14, AC15, AC16, AC17, AC26, AC27
- Commit: feat(observability): build a de-duplicated session timeline
- Depends: T004, T005
- Size: L
- Risk: high

### T007 — List sessions and render text and JSON
- Do: `internal/timeline`: `ListSessions(src, limit)` with first-user-prompt preview (80 runes), text and JSON renderers taking a `*time.Location`, terminal-control escaping for keys, previews and bodies; one helper reads a text block's `text` string, falling back to the raw JSON (Q1).
- Tests: TestListSessions_RowsWithPreview (AC8); TestListSessions_EmptyPreviewForSideCallOnly (E3); TestRenderText_LocalZoneRFC3339 (AC21); TestRenderJSON_UTC (AC21); TestRenderText_EscapesTerminalControls (E11); TestRenderText_FoldedAndMarkerLines (AC14, AC15, AC16); TestBlockText_TextFieldElseRawJSON (AC8, AC12); TestRender_RealSessionFixtureWithoutSystemContent (AC12, AC21: renders the T004 fixture timeline as text and JSON with the system content absent; must not fail and must not print the system prompt, D12).
- AC: AC8, AC21
- Commit: feat(observability): render sessions and timelines as text or json
- Depends: T006
- Size: M
- Risk: low

### T008 — Add the gateway sessions and timeline commands
- Do: `cmd/gateway/viewer.go` (`runSessions`, `runTimeline`) dispatched from `run.go`; flags `-n`, `-ungrouped`, `-json`, `-last`, `-full`, `-config`; reader-only; extend the purity test to the viewer command file.
- Tests: TestSessions_PrintsRows (AC8); TestSessions_DefaultLimitTwentyAndN (AC9); TestSessions_EndsWithUngroupedRow (AC10); TestSessions_UngroupedFlagListsExchanges (AC11); TestSessions_EmptyStoreExitsZero (E8); TestTimeline_PrintsSession (AC12); TestTimeline_LastPrintsTopSession (AC18); TestTimeline_LastOnEmptyStoreFails (E8, AC19); TestTimeline_UnknownKeyFailsWithNoOutput (AC19); TestTimeline_SameKeyTwoClientsPrintsBoth (AC20); TestTimeline_JSONFlag (AC21); TestViewer_NeverWritesStore (AC22, E9); TestViewer_RefusesOlderAndNewerSchema (AC23); TestViewer_BadArguments; TestCore_NoProviderOrClientIdentifiers (AC26).
- AC: AC8, AC9, AC10, AC11, AC12, AC18, AC19, AC20, AC21, AC22, AC23, AC26
- Commit: feat(server): add the sessions and timeline commands
- Depends: T003, T007
- Size: M
- Risk: low

### T009 — Add gateway backfill-sessions
- Do: `store.Store`: `UnkeyedBatch` and `SetSessionKeys` (guarded `WHERE session_key IS NULL`, batches); `cmd/gateway/backfill.go`: label-to-`SessionKeyer` map from `registered()`, validation, counts, store existence check before open; dispatched from `run.go`.
- Tests: TestSetSessionKeys_OnlyFillsNull (AC25); TestBackfill_FillsFromStoredHeaders (AC24); TestBackfill_SkipsUnknownClientAndMissingProfile (AC24, E10); TestBackfill_InvalidKeyLeftWithoutKey (AC24); TestBackfill_SecondRunChangesNothing (AC25); TestBackfill_ConcurrentWithCapture (AC25); TestBackfill_StoreMissingFailsWithoutCreatingIt.
- AC: AC24, AC25
- Commit: feat(store): backfill session keys on stored exchanges
- Depends: T003
- Size: M
- Risk: high

### T010 — Record the viewer decision
- Do: add `docs/decisions/0007-viewer-cli-first.md`; mark PLAN.md OQ-7 and the §8 Viewer row decided with a link. The ADR also records that the text-block helper reads a wire-shaped field (`text` of the stored block) and is to be revisited when the second protocol lands.
- Tests: none — documentation only; `make verify` stays green.
- AC: none — spec Constraint (ADR and PLAN update), no AC
- Commit: docs(repo): record the cli-first viewer decision
- Depends: —
- Size: S
- Risk: low

### T011 — Replay the captured real session end to end
- Do: replay the session captured in T004 with `gateway timeline -last` against its store and record the result in research.md.
- Tests: none — manual check
- AC: AC28
- Commit: docs(003-sessions-and-timeline): record the real-session replay result
- Depends: T008, T009
- Size: S
- Risk: low
- Manual: run `gateway timeline -last` on the T004 session (Task-tool use); confirm prompts, tool calls, tool results and responses each appear once, in order, and note the result in research.md.

## Changelog
- v1 (2026-10-11) — generated from the approved plan
- v1 (2026-10-11) — revise: new manual fixture task T004; later tasks renumbered T005-T011; ADR task notes the wire-shaped text helper; AC28 replay is T011
- v1 (2026-10-11) — revise: T004 captures in a clean /tmp directory, gets a privacy review (greps incl. referenced content) before commit, and stays NEEDS-HUMAN until the user confirms it in research.md
- v1 (2026-10-11) — revise: T004 moves ~/.claude/CLAUDE.md aside during the capture and restores it after; a failed grep on needed content stops with an open question and no commit
- v2 (2026-10-11) — reopened: CR-001: Real-session fixture test also covers missing system content
- v2 (2026-10-11) — CR-001: T006 TestBuild_RealSessionFixture also checks Build with system content absent; T007 gains TestRender_RealSessionFixtureWithoutSystemContent
