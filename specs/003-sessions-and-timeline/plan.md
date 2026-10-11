---
feature: 003-sessions-and-timeline
status: approved
version: 1
approved: 2026-10-11T03:34:10Z
approved-by: human
sha256: dae8057d996b8adf140ab61b5514520f403ae90080c6327f3b26315207232bf7
fingerprint: ef486a396a618b8458a57e11d453330b33a05a9a25a8d9795e3c1557a9b1d688
spec-fingerprint: 68cb3d068f27d522de3ee67141de9cf03e1897541d0c21cea129bd00686dd1e9
---
# Plan — 003-sessions-and-timeline

## Summary
The client profile may return an opaque session key from a request's headers. Core validates
it and capture stores it in a new `exchanges.session_key` column (migration step 2, indexed).
A new pure package `internal/timeline` reads canonical events and exchange columns through
`store.Reader`, de-duplicates resent history by message content hash, and renders text or JSON.
`gateway sessions`, `gateway timeline` and `gateway backfill-sessions` are thin commands beside `dump`.

## Context
- Contracts: `internal/core/adapter.go` (`Profile`, optional-interface pattern as `SecretDeclarer`/`ParsingAdapter` in `capture.go`), `registry.go` (`Identify`), `proxy.go` (`ServeHTTP` sets `Meta.Client`), `capture.go` (`Exchange`, `finishCapture`).
- Store: `internal/store/migrate.go` (append-only `migrations`, `Reader.checkSchema` already compares `len(migrations)`, giving AC23 for free), `exchange.go` (`SaveExchange`), `reader.go` (`ExchangeRow`, `Events`, `Content`).
- CLI: `cmd/gateway/dump.go` is the template (flag set with `ContinueOnError`, `OpenReader`, log line per failure on `d.stdout`, output built in memory first); dispatch is in `run.go`. `registered()` is the one place that imports profiles.
- Canonical hashes: `Canonicalize` (`canonical.go`) already gives a message's `content_hash` independent of index/source, so a response message and its resent copy hash equal. AC13 builds on this.
- Purity: `internal/core/purity_test.go` (`checkDir`, `names`, `coreOnly`).
- Commit scopes (PLAN §11): `proxy`, `store`, `claude-code`, `observability`, `server`, `docs`.

## Approach
Optional interface `core.SessionKeyer { SessionKey(http.Header) string }` on profiles. The proxy asks the matching profile once per request, validates the result (`core.ValidSessionKey`), and puts it on `Exchange`. Store persists it. A timeline `Build` function turns one session's exchanges plus events into a list of entries (exchange headers, messages, tool calls, results, errors, folded lines, markers); two renderers (text, JSON) print entries. Commands only parse flags, open the reader and print.

## Alternatives considered
### A1 — Optional `SessionKeyer` interface; key stored as a nullable column on `exchanges` (chosen)
- Pros: no change to the `Profile` contract other profiles implement; no new event kind (spec non-goal); a plain index serves grouping.
- Cons: the column is a second place beside events that carries session identity; backfill needs a store write path.

### A2 — Add `SessionKey` to the `Profile` interface itself
- Pros: one contract, no type assertion.
- Cons: every profile and test double must implement it; a profile with no notion of sessions returns a dummy.

### A3 — Derive sessions at read time from stored request headers (no migration)
- Pros: no schema change, no backfill command.
- Cons: the viewer would name a header (a client detail) or run profiles per row; no index, so listing scans every row's JSON; breaks "timeline reads only stored columns".

### A4 — Timeline De-dup by diffing against only the immediately previous exchange
- Pros: O(n), trivial.
- Cons: wrong for sub-agent interleaving (E6) and `/compact` (E12); the spec requires best match across all earlier exchanges.

## Design

**Session key contract (AC1, AC3, AC4, AC5).** `core/adapter.go` gains `SessionKeyer`. `Registry` gets an unexported `profileFor(req)` used by both `Identify` and a new `sessionKey(req)` handed to `newProxy` (the public `NewProxy` passes nil, so 001's callers are unchanged). `Proxy.ServeHTTP`, when capture is on, calls it on the inbound headers already in memory (no I/O, no blocking), inside a `recover` so a profile panic means no key. `core.ValidSessionKey(string) (reason string)` rejects empty, over 256 bytes, or any control character (C0, DEL, C1); on a rejected non-empty or empty key the proxy logs `session_key_rejected` with `request_id` and `reason` (`empty`, `too_long`, `control_character`), never the value, and stores no key. `Exchange.SessionKey` is set in `finishCapture` from a Meta field. Capture rules from 002 are unchanged: queue-full or memory drop discards the exchange as before and the request succeeds (E2). The key is opaque: core only measures and compares it.

**Store (AC1, AC6, AC7).** Migration step 2: `ALTER TABLE exchanges ADD COLUMN session_key TEXT;` and `CREATE INDEX exchanges_session ON exchanges(principal_id, client, session_key, started_at);`. Step 1 text is untouched, pinned by a hash test. `SaveExchange` writes `nullString(ex.SessionKey)`. `ExchangeRow` gains `SessionKey string json:"session_key,omitempty"` so `gateway dump` of an ungrouped exchange prints byte-identical JSON (AC3); `exchangeColumns`/`scanExchange` read it. Existing tests that assume `user_version` 1 are updated to `len(migrations)`.

**Reader queries (AC7-AC11, AC18-AC20).** New in `store/reader.go` (or `reader_sessions.go`): `SessionRows(limit int)` groups by `(principal_id, client, session_key)` where key is not NULL, ordered `MAX(started_at) DESC, principal_id, client, session_key` (deterministic top row for `-last`), limit 0 means all; `SessionsByKey(key)`; `SessionExchanges(principal, client, key)` ordered `started_at, request_id`; `UngroupedSummary()` (count, min, max); `UngroupedExchanges(limit)` newest first. All read-only on the existing `mode=ro` connection (AC22). Every query takes principal and client as part of the session identity; tenancy is therefore structural (AC7).

**Timeline package `internal/timeline` (AC12-AC17, AC26, AC27).** Depends on a small `Source` interface (`SessionExchanges`, `Events`, `Content`) satisfied by `*store.Reader`, so tests use an in-memory fake. `Build(src, session, opts)`:
1. Per exchange, in order, load events. Message list = `content_hash` of message events in seq order (history then response).
2. Folded line (no messages, AC15) when the exchange has no message event; folded with parse state and request id when parse is `failed`, `skipped` or `unsupported_encoding` (AC16). Folded exchanges are not match candidates. `partial` exchanges are built normally, each entry carrying `Partial` (AC16, E7); events with `partial=1` are marked too.
3. For every earlier non-folded exchange compute the longest common leading run (LCP). Best = max LCP; ties: the candidate whose own list length equals LCP, then the latest. LCP 0 means a new thread: show all messages, no marker (AC27, E6). Otherwise show `msgs[LCP:]`, and emit `history diverged` first when `len(best.list) > LCP` (AC14, E12). A repeated user message after the prefix is shown (E5).
4. Walk events in seq order. A message event is shown if its index in the list is at or past LCP. A `tool_call`/`tool_result` event belongs to the nearest preceding message event (the canonicalizer puts them after the message that references them), so it is shown iff that message is shown. `error` events are always shown; `usage` and `request` events are not shown (cost is Phase 4).
5. Text extraction: for `text` blocks the timeline reads the stored block content and prints its `text` string; other block types print as `[reasoning]`, `[media]`, `[unknown]`; tool input and result content are printed as stored JSON. Decided (Q1): one helper reads the block's `text` string and falls back to the raw JSON; 002's events are unchanged.
6. Per-entry limit (AC17): `DefaultEntryLimit = 2000` bytes, cut at a rune boundary, with `… [N bytes left out]`; `Options.Full` disables it.
The package contains no provider or client word (AC26): its purity check uses the full `names + coreOnly` list.

**Sessions list and rendering (AC8, AC9, AC21, E3, E11).** `timeline.ListSessions(src, limit)` returns rows with a preview: the first text of the first user message found by scanning the session's exchanges in order (empty when none, E3), cut to 80 runes. Renderers take a `*time.Location`: text uses `time.Local` as RFC 3339 with offset, JSON uses UTC (AC21). Text output escapes keys, previews and all C0/C1 controls except `\n` and `\t` in message bodies (E11, terminal injection from model text). JSON is one object per line with a `type` field per entry.

**Commands (AC8-AC11, AC18-AC23).** `cmd/gateway/viewer.go`: `runSessions` (`-n`, `-ungrouped`, `-json`, `-config`) and `runTimeline` (`-last`, `-full`, `-json`, `-config`, one positional key), dispatched from `run` beside `dump`. They reuse `loadConfig`, `captureDir` and the dump failure style (a log line on `d.stdout`, exit 1), build output in memory so a store error part-way prints nothing, and open `OpenReader` only (AC22). A refused schema reuses the reader's error text (AC23). `-n` also bounds `-ungrouped` (default 20, 0 all). Not-found exits 1 with reason `session not found` and no timeline output (AC19, E8). Decided (Q2): the reason is the same JSON log line on stdout that dump uses. Several matches print one headed timeline each (AC20).

**Backfill (AC24, AC25, E10).** `store.Store` gains `UnkeyedBatch(ctx, afterRowid, limit)` (request_id, client, request headers, rowid, where key is NULL) and `SetSessionKeys(ctx, []KeyAssignment) (filled int, err)`, one short transaction per batch of 500 on the writer connection: `UPDATE exchanges SET session_key = ? WHERE request_id = ? AND session_key IS NULL`, counting `RowsAffected`. The guard makes a rerun and a run beside a live gateway change nothing that already has a key; SQLite serialises writers (`busy_timeout` 5s). `cmd/gateway/backfill.go` maps client label to a registered `SessionKeyer` from `registered()`, validates with `core.ValidSessionKey`, and prints filled, left without key, skipped (no profile registered for the label, `unknown` included). It checks `gateway.db` exists before `store.Open`, so it never creates a store. No new dependency anywhere.

**Docs (spec Constraint).** `docs/decisions/0007-viewer-cli-first.md` records OQ-7; PLAN.md OQ-7 and the §8 Viewer row get "Decided" and a link. The ADR also notes that the text-block helper reads a wire-shaped field (`text`) and is to be revisited when the second protocol lands.

## AC coverage
| AC | Where | Verified by |
|----|-------|-------------|
| AC1 | `core/proxy.go`, `core/capture.go`, `store/exchange.go` | `TestProxy_SessionKeyReachesSink`, `TestSaveExchange_StoresSessionKey` |
| AC2 | `clients/claudecode/profile.go` | `TestProfile_SessionKeyFromHeader`, `TestRun_CapturesSessionKeyFromClaudeCodeHeader` |
| AC3 | `core/proxy.go`, `store/reader.go` | `TestProxy_NoKeyOrUnknownClientCapturedUngrouped`, `TestDump_UngroupedRowUnchanged` |
| AC4 | `core.ValidSessionKey`, `core/proxy.go` | `TestProxy_InvalidSessionKeyStoredUngrouped`, `TestValidSessionKey` |
| AC5 | `core/adapter.go`, `core/registry.go` | `TestProxy_TestOnlyProfileKeyNeedsNoCoreChange` |
| AC6 | `store/migrate.go` | `TestMigrate_SessionKeyStepAddsColumnAndIndex`, `TestMigrate_ReleasedStepUnchanged` |
| AC7 | `store/reader.go` | `TestReader_SessionsKeyedByPrincipalClientAndKey` |
| AC8 | `store/reader.go`, `timeline/sessions.go` | `TestReader_SessionsNewestActivityFirst`, `TestListSessions_RowsWithPreview`, `TestSessions_PrintsRows` |
| AC9 | `store/reader.go`, `cmd/gateway/viewer.go` | `TestReader_SessionsLimit`, `TestSessions_DefaultLimitTwentyAndN` |
| AC10 | `store/reader.go`, `viewer.go` | `TestReader_UngroupedSummary`, `TestSessions_EndsWithUngroupedRow` |
| AC11 | `store/reader.go`, `viewer.go` | `TestReader_UngroupedExchangesNewestFirst`, `TestSessions_UngroupedFlagListsExchanges` |
| AC12 | `timeline/build.go` | `TestBuild_OrdersExchangesByStartThenRequestID`, `TestBuild_EventOrderWithinExchange`, `TestTimeline_PrintsSession` |
| AC13 | `timeline/build.go` | `TestBuild_ShowsOnlyMessagesAfterSharedPrefix`, `TestBuild_TieBreaksPrefixEqualThenMostRecent` |
| AC14 | `timeline/build.go` | `TestBuild_RewriteMarksHistoryDiverged`, `TestBuild_NoMarkerWhenBestMatchIsPrefix` |
| AC15 | `timeline/build.go` | `TestBuild_FoldsExchangesWithoutMessages` |
| AC16 | `timeline/build.go` | `TestBuild_FoldsFailedSkippedUnsupported`, `TestBuild_PartialExchangeShowsEventsMarked` |
| AC17 | `timeline/build.go` | `TestBuild_CutsLongEntriesAndNotesBytes`, `TestBuild_FullKeepsEverything` |
| AC18 | `viewer.go` | `TestTimeline_LastPrintsTopSession` |
| AC19 | `viewer.go` | `TestTimeline_UnknownKeyFailsWithNoOutput` |
| AC20 | `viewer.go`, `store/reader.go` | `TestTimeline_SameKeyTwoClientsPrintsBoth` |
| AC21 | `timeline/render.go` | `TestRenderText_LocalZoneRFC3339`, `TestRenderJSON_UTC`, `TestTimeline_JSONFlag` |
| AC22 | `viewer.go` | `TestViewer_NeverWritesStore` |
| AC23 | `store/reader.go` (existing `checkSchema`) | `TestViewer_RefusesOlderAndNewerSchema` |
| AC24 | `store`, `cmd/gateway/backfill.go` | `TestBackfill_FillsFromStoredHeaders`, `TestBackfill_SkipsUnknownClientAndMissingProfile` |
| AC25 | `store.SetSessionKeys` | `TestSetSessionKeys_OnlyFillsNull`, `TestBackfill_SecondRunChangesNothing`, `TestBackfill_ConcurrentWithCapture` |
| AC26 | `core/purity_test.go` | `TestCore_NoProviderOrClientIdentifiers` (extended to `internal/timeline` and the viewer command files) |
| AC27 | `timeline/build.go` | `TestBuild_NewThreadWhenNoSharedMessage` |
| AC28 | manual | T004 captures the real session as a fixture and records the sub-agent finding; T011 replays it with `gateway timeline -last`, recorded in research.md |

## Test strategy
Real SQLite stores in `t.TempDir()` for store, reader, viewer-command and backfill tests (the 002 `dump_test.go` helpers: `captureOne`, `assertUnchanged`); an in-memory `Source` fake for timeline logic so message lists are written as short hash lists.
- Key: `TestProxy_SessionKeyReachesSink`, `TestProxy_NoKeyOrUnknownClientCapturedUngrouped`, `TestProxy_InvalidSessionKeyStoredUngrouped` (empty, 257 bytes, 256 accepted, control char, logged reason without value; E1), `TestProxy_SessionKeyWithDroppedCaptureStillSucceeds` (E2), `TestProxy_PanickingProfileMeansNoKey`.
- Migration: `TestMigrate_SessionKeyStepAddsColumnAndIndex` builds a v1 database from `migrations[0]`, inserts an exchange, opens with the new binary and checks version, NULL key and the index; `TestMigrate_ReleasedStepUnchanged` pins step 1's sha256.
- Timeline: one test per behaviour in the table, plus `TestBuild_RepeatedUserMessageShownAgain` (E5), `TestBuild_SubAgentInterleaveNoMarkers` (E6: P1, S1, P2, S2), `TestBuild_RewriteMarksHistoryDiverged` (E12), `TestBuild_ToolEventsOfHiddenMessagesAreHidden`, `TestBuild_ExchangesWithSameStartOrderByRequestID` (E4).
- Output: `TestRenderText_EscapesTerminalControls` (E11), `TestListSessions_EmptyPreviewForSideCallOnly` (E3).
- Commands: `TestSessions_EmptyStoreExitsZero`, `TestTimeline_LastOnEmptyStoreFails` (E8), `TestViewer_NeverWritesStore` (file states before/after, plus a read while a writer inserts for E9).
- Backfill: `TestBackfill_SkipsUnknownClientAndMissingProfile` (E10), `TestBackfill_InvalidKeyLeftWithoutKey`, `TestBackfill_StoreMissingFailsWithoutCreatingIt`.
- Real fixture: `TestBuild_RealSessionFixture` loads a redacted real Claude Code Task-tool session (T004) from `testdata/sessions/` at the repo root, outside `internal/timeline` so the purity walk does not see wire names. It is captured in a clean /tmp directory (no git repo, no project CLAUDE.md) and grepped for home path, username, email, hostname, `metadata.user_id` and ~/.claude/CLAUDE.md text, referenced content included; if the raw bodies hold anything identifying, only exchange rows, canonical events and referenced content are committed.
- Mutation-sensitive: tie-break order (E13), `>` versus `>=` for the marker, the `IS NULL` guard, principal/client in the grouping.

## Risks
- Wrong de-dup hides a message the user needs: bounded by `-full` not changing matching (it only lifts the per-entry cut) and by the marker on rewrites; tie-break tests pin the rules.
- The real-session fixture could leak identifying data (paths, user id, global CLAUDE.md in a system prompt): clean capture directory, the global CLAUDE.md moved aside during the capture and restored after, a grep review of bodies and referenced content, a reduced fixture if needed, and T004 stays NEEDS-HUMAN until the user confirms the review.
- Reading text from a block's stored content touches a field name that is wire-shaped (decided in Q1); limited to one helper with a fallback to printing the raw JSON.
- Backfill writes beside a live gateway: guarded update, small batches, 5s busy timeout; a failure leaves a consistent store and a rerun resumes.
- Migration on a large store: `ADD COLUMN` is O(1) in SQLite; the index build is a one-time scan at first start, inside the same `BEGIN IMMEDIATE`.
- O(n^2) matching on very long sessions: lists are hash slices, thousands of exchanges is milliseconds; no cap now.
- Control characters in the profile's header value reach logs: only the reason is logged.

## Open questions
- **Q1** — How does the timeline print the text of a text block, given block content is stored "as sent" and has no canonical text field? (recommended: read the `text` string of the stored block object, fall back to the raw JSON, in one helper; a canonical text field would change 002's events, which the spec forbids) → decided: read the `text` string of the stored block object, falling back to the raw JSON, in one helper; 002 events unchanged
- **Q2** — Where does `session not found` go, since `gateway dump` writes its failure log lines to stdout and AC19 says "nothing else to stdout"? (recommended: same JSON log line on stdout as dump, with no timeline output; adding a stderr to `deps` is a larger change and a second convention) → decided: the same JSON log line on stdout as dump, with no timeline output

## Changelog
- v1 (2026-10-11) — first draft; Q1 and Q2 decided
- v1 (2026-10-11) — revise: real-session fixture task (T004) added, AC28 replay moved to T011, ADR notes the wire-shaped text helper
- v1 (2026-10-11) — revise: fixture capture setup and privacy review added to T004, Test strategy and Risks
- v1 (2026-10-11) — revise: T004 moves the global CLAUDE.md aside during capture; an unfixable grep failure stops with an open question and no commit
