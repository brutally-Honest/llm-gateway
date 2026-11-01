---
feature: 010-harness-hooks
status: approved
version: 1
approved: 2026-11-01T06:25:07Z
approved-by: human
sha256: 07ac03ac4cc9db3ed61feb670486a2bfeeecba6333e2a0ceb9380e241ea2d757
fingerprint: 86f6047061c5c7913adc0b29ada922c9d9a9dfd42946a59452e91097655ca2d7
spec-fingerprint: f36c6f56a3514dd080ce28e79e59a4c1293c8dae9194645dfcc116efa2bd72cd
---
# Plan — 010-harness-hooks

## Summary
Claude Code hook events are POSTed to `/_gateway/hooks/{client}` on the proxy listener. A
small handler guards the request (loopback Host, no browser headers), answers 200 empty
at once and hands the raw body to a separate bounded hook sink. Sink workers map the body
through the client profile into a canonical `harness_event` and store it (raw payload as
a content-addressed blob, mapped fields as columns of a new `hook_events` table). The
timeline package joins hook events to captured tool calls by two exact keys, computes
match states at read time, and the CLI, web UI and live hub show them.

## Context
- Ingest pattern: `internal/capture/sink.go` + `pipeline.go` (bounded queue, workers via
  `logging.Go`, atomic counters, `Counts`), `internal/core.Budget` for the memory limit.
- Store: `internal/store/migrate.go` (append migration 3), `store.go` `write()` (blobs
  first, then one tx), `content.go`; readers in `reader.go`, `reader_sessions.go`, `CtxReader`.
- Routing: `internal/server/server.go` (`New(log, mounts...)`, chi), `core/registry.go` Mount.
- Client profile: `internal/clients/claudecode/profile.go`; purity test `core/purity_test.go`.
- Config: `internal/config/capture.go` (settings table, `parsePositive`), `docs_test.go`.
- Read side: `internal/timeline/build.go` (`Source`, `Entry`, `Kind`), `render.go`,
  `sessions.go`; CLI `cmd/gateway/viewer.go`; UI `internal/ui/hub.go`, `api_timeline.go`,
  `api_sessions.go`, `entries.go`, `web/`. Wiring: `cmd/gateway/run.go` (`startCapture`, `stop`).
- Existing canonical-JSON form used for AC18: `internal/core/canonical.go`.

## Approach
New package `internal/hooks` holds the HTTP handler and the hook sink; it knows no client
name beyond the profile lookup. `core` gets the `HarnessEvent` type, harness-neutral phases
and an optional profile interface `HarnessMapper`. The Claude Code profile implements it
and is the only place naming Claude Code events. Mapped fields are stored as columns at
ingest; states are never stored, always derived from store rows by `internal/timeline`,
so arrival order cannot matter (AC21) and the process stays stateless.

## Alternatives considered
### A1 — Separate hook sink, map in the worker, store mapped columns (chosen)
- Pros: own queue/memory so a hook burst never drops LLM capture (E13); SQL-joinable columns; handler stays under 10 ms.
- Cons: a second sink to build and shut down; schema columns fixed at ingest time.

### A2 — Store raw only, map at read time
- Pros: mapping bugs fixable without a data change; no mapped columns.
- Cons: every timeline/session listing re-parses blobs; hooks-only sessions and joins need blob reads.

### A3 — Reuse `capture.Sink` with a generalized item type
- Pros: one queue implementation, less new code.
- Cons: shared queue breaks E13; couples exchange pipeline to hooks; invasive change to the most critical path.

## Design
**Core types (AC11, AC12, constraints).** `core/harness.go`: `HarnessEvent{Phase, SessionKey,
ToolCallID, ToolName, ToolInput, AgentID, AgentType, SourceName}` and `HarnessPhase` consts
(`pre_tool`, `permission_requested`, `permission_denied`, `post_tool`, `post_tool_failed`,
`tool_batch_done`, `prompt`, `session_start`, `session_end`, `turn_end`, `turn_failed`,
`agent_start`, `agent_end`, `notice`, `compact_start`, `compact_end`, `unknown`).
`HarnessMapper` = `MapHarness(body []byte) HarnessEvent` on a profile. `SourceName` is the
original event name as read, kept only for display/`unknown`; no Claude Code name appears
in code outside `internal/clients/claudecode/`, enforced by an extended purity test.
ADR 0012 records the kind. Not a `StoredEvent` of an exchange: hook events live in their own table.

**Mapping (AC11-AC14).** The profile parses the body leniently; non-JSON, empty or missing
`hook_event_name` yields phase `unknown` and whatever was readable. `session_id` is passed
through verbatim as `SessionKey` (joined to `exchanges.session_key` with SQL `=`, AC14).
Unknown `{client}` segment: sink uses no mapper, client `unknown`, phase `unknown` (AC13).
Caveat: core validates session keys for exchanges; a key it rejects leaves the exchange
ungrouped while the hook shows hooks-only (accepted, no fuzzy join).

**Handler (AC2, AC4-AC8).** `hooks.Handler` registered with `HandleFunc` on
`/_gateway/hooks/{client}` plus `r.Handle("/_gateway/*", 404)` in `server.New`, so the
reserved prefix is never forwarded. Order: method (non-POST: 405 + `Allow: POST`), then
guard (Host host part in `localhost`, `127.0.0.0/8`, `::1`; any `Origin` or `Sec-Fetch-Site`
header: 403; nothing stored). Then read at most `capture.max_body_bytes+1` bytes; over limit:
count oversize, log one line, 200. `capture.enabled=false`: count, 200. Else assign a
per-process monotonic seq and receive time, `Submit` without blocking; full queue or
budget refused: count and one log line, 200. The answer is always `200` with empty body.
Bodies and headers are never logged (credential rule); only the client segment, size, reason.
Handler is mounted regardless of capture, so AC8 can answer.

**Sink (AC6, AC9, E13).** `hooks.Sink` mirrors `capture.Sink`: `hooks.queue_size`,
`hooks.workers`, `hooks.memory_limit` (new config section, positive ints, defaults 256 /
1 / 32 MiB), `Close(ctx)` drain, `Counts{QueueFull, Oversize, CaptureOff, NotJSON,
StoreFailed}`. A worker maps (panic recovered, as capture's parser), writes via
`Store.SaveHook`, then calls `OnStored(HookStored{Principal, Client, SessionKey})`
(recovered; used by the hub, AC26). Principal comes from `core.LocalPrincipal` like exchanges.

**Store (AC10, AC20, AC21, E4, E5).** Migration 3: `hook_events(id, seq, principal_id,
client, session_key NULL, tool_use_id NULL, phase, source_name, tool_name, agent_id,
agent_type, input_hash NULL, payload_hash NOT NULL REFERENCES content, received_at)` with
indexes on `(principal_id, client, session_key, received_at, seq)` and `tool_use_id`. The
payload is stored verbatim via the existing `write()` (blob + content row, ADR 0005); the
tool input (if any) as a second content row. No header column exists, so nothing credential-
bearing can land. Identical payloads produce identical content hash but separate rows
(dedup is read-side). `store.Store` implements `hooks.Store`; `run.go` asserts it.
Reader: `HookEvents(principal, client, key)` ordered by `received_at, seq`; `SessionRows`
gains a union with hook-only keys and `SessionRow.HooksOnly`; ungrouped hook count.

**States (AC15-AC22).** `internal/timeline/harness.go`: given a session's exchange events
and hook rows. Dedup first (same payload hash: keep earliest; drop later if tool id present
or gap to the kept copy <= 1 s). Per captured `tool_call` id: collect hook phases by exact
id; `post_tool|post_tool_failed` present -> ran (+ `tool_result` captured => `asked+ran+
reported`, else `ran-not-reported`), outcome from the phase; else `permission_denied` with
id => `asked-not-run`/`denied`; else a later-exchange `tool_result` => `asked-not-run`/
`not run`; else `pending`. Session with zero hook rows => `no harness data` for every call.
Phase order within a call is by phase rank, not timestamp. `pre_tool` input vs `tool_call`
input compared via the canonical-JSON form => flag `input differs` and carry both inputs.
Hook rows naming an id with no captured call, or with no id, become hook-event entries
(`ran-not-asked-or-not-captured` for ids). Agent id/type copied onto entries and matched calls.

**Presentation (AC23-AC26).** `Entry` gains `State, Reason, Outcome, InputDiffers,
HookInput, AgentID, AgentType` and `KindHarness`; render and `--json` are built from them.
`ui` entries/mapEntries pass the same fields; web JS renders badges.

**Hub and streams (AC26), traced in `hub.go` / `stream.go`.** A stream's "last sent" record
is `sent map[request_id]hash`, filled by the connect snapshot and by `writeLive`; the hash is
sha256 of the marshalled entry list of one exchange. `Hub.dispatch` groups commits by session,
and `readLive` rebuilds the whole session timeline (`sessionExchanges`) and returns `All`
(every exchange, timeline order) plus `Exchanges` (only the committed ids). `writeLive` does
nothing unless `Exchanges` is non-empty; it then sends an `entries` event per committed
exchange and an `exchange` event (whole list) for each later exchange in `All` whose hash
differs from `sent`, never for earlier ones. A hook event has no committed exchange and can
change the state of an earlier exchange, so it does not fit: today it would read with
`ids=[""]`, match nothing and send nothing. → decided: a new trigger. `Hub.PublishHook` queues
a hook commit (session identity only); dispatch coalesces hook commits per session to at most
one re-read per 250 ms (trailing edge, so the last event always lands) and sends a `liveMsg`
with `All` and a `Refresh` flag. For a `Refresh` message `writeLive` compares every exchange
in `All` (no "after the first committed" cut-off) with `sent` and emits `exchange` events for
the ones that differ, with no `id:` line (row ids are exchange-only). Hook lines that match no
call travel as one pseudo-exchange (`request_id` `hooks`) in `All`, hashed the same way. The
global stream gets a refreshed summary, so a hooks-only session appears. A re-read failure
drops the streams (they resync by snapshot), as for exchanges.

**Install (AC27, AC28).** `claudecode.HookSnippet(addr)` builds the settings JSON (the only
place with event names); `gateway hooks print` loads config for `listen_addr`, writes to
stdout, no file, exit 0. Hook type, async and failure behaviour come from the T001 spike.
Docs in `docs/clients/`.

**Dependencies:** none new.

## AC coverage
| AC | Where | Verified by |
|----|-------|-------------|
| AC1 | `research.md` (T001) | manual spike; `loop.sh check` finds the recorded facts |
| AC2 | `hooks/handler.go`, `server/server.go` | TestHandler_LoopbackPostAnswers200Empty, TestHandler_AnswersBeforeStorageFinishes, TestServer_ReservedPrefixUnknownIs404NotForwarded |
| AC3 | `cmd/gateway` (`!race` test) | TestRun_HookLatencyP99HealthyAndStalled |
| AC4 | `hooks/handler.go` | TestHandler_NonLoopbackHost403, TestHandler_OriginOrSecFetchSite403 |
| AC5 | `hooks/handler.go` | TestHandler_NonPostIs405 |
| AC6 | `hooks/sink.go` | TestSink_QueueFullDropsCountsAndLogsOnce, TestHandler_FullQueueStillAnswers200 |
| AC7 | `hooks/handler.go` | TestHandler_OversizeBody200NotStoredCounted |
| AC8 | `hooks/handler.go`, `run.go` | TestHandler_CaptureOffCounts, TestRun_CaptureOffHooksAnswers200 |
| AC9 | `run.go` | TestRun_ShutdownLogsHookCounters |
| AC10 | `store/hooks.go`, `migrate.go` | TestSaveHook_PayloadByteForByte, TestSaveHook_StoresNoRequestHeaders |
| AC11 | `claudecode/harness.go`, `core/harness.go` | TestMapHarness_EachInScopeEvent, TestPurity_NoHarnessEventNamesOutsideProfile |
| AC12 | `claudecode/harness.go`, `hooks/sink.go` | TestMapHarness_UnknownEventKeepsName, TestMapHarness_NotJSONIsUnknown, TestSink_NotJSONStoredAndCounted |
| AC13 | `hooks/sink.go` | TestSink_UnknownClientStoredAsUnknown |
| AC14 | `store/hooks.go`, `timeline/harness.go` | TestSaveHook_SessionIDVerbatim, TestStates_SessionJoinIsExactString |
| AC15 | `timeline/harness.go` | TestStates_ToolJoinIsExactID |
| AC16 | `timeline/harness.go` | TestStates_AskedRanReported, TestStates_RanNotReported, TestStates_DeniedWithID, TestStates_NotRunByLaterResult, TestStates_Pending, TestStates_FailureOutcome |
| AC17 | `timeline/harness.go` | TestStates_NoHookEventsMeansNoHarnessData |
| AC18 | `timeline/harness.go` | TestStates_InputDiffersFlagged, TestStates_InputKeyOrderIsNotADifference |
| AC19 | `timeline/harness.go` | TestStates_UnmatchedHookShown |
| AC20 | `store/hooks.go`, `timeline/harness.go` | TestSaveHook_IdenticalPayloadsBothStored, TestStates_DuplicateShownOnce, TestStates_DuplicatesFarApartWithoutIDShownTwice |
| AC21 | `timeline/harness.go` | TestStates_ArrivalOrderDoesNotChangeStates, TestStates_SeededPermutationsGiveIdenticalStates |
| AC22 | `timeline/harness.go`, `ui/entries.go` | TestStates_AgentLabelsOnEventAndCall, TestRender_ShowsAgentLabels, TestAPITimeline_AgentLabels |
| AC23 | `timeline/render.go`, `cmd/gateway/viewer.go` | TestRender_ShowsStateAndInputDiffers, TestRender_UnmatchedHookLinesInTimeOrder, TestTimelineJSON_IncludesStatesAndHookEvents |
| AC24 | `ui/api_timeline.go`, `ui/entries.go`, `web/` | TestAPITimeline_StatesFlagsAndHookLines, TestWeb_ServesHarnessBadgeRendering |
| AC25 | `store/reader_sessions.go`, `timeline/sessions.go`, `ui/api_sessions.go` | TestSessionRows_HooksOnlySessionListed, TestSessions_HooksOnlyLabelled, TestAPISessions_HooksOnlyLabel |
| AC26 | `ui/hub.go`, `ui/stream.go`, `run.go` | TestHub_HookEventPushesSessionTimeline, TestHub_HookEventPushesSessionsList, TestHub_HookRefreshesCoalescePerSession, TestHub_HookRefreshCancelledWhenLastStreamCloses, TestHub_HookRefreshAndCommitInSameWindowSendEachChangeOnce |
| AC27 | `claudecode/snippet.go`, `cmd/gateway/hooks.go` | TestHooksPrint_SnippetCoversEveryEvent, TestHooksPrint_WritesNoFileExitsZero, TestHooksPrint_UsesListenAddr |
| AC28 | `docs/clients/` | none — docs; reviewed by reading (T015) |

## Test strategy
Behaviour tests per layer. Edge cases: E1 TestStates_PartialHookDataKeepsRemainingStates;
E2 TestMapHarness_EmptyAndNoEventName; E3 TestHandler_OversizeBody200NotStoredCounted;
E4 TestSaveHook_NoSessionStoredUngrouped, TestSessions_UngroupedShowsHookCount; E5
TestStates_DuplicateShownOnce; E6 TestStates_ArrivalOrderDoesNotChangeStates and
TestStates_SeededPermutationsGiveIdenticalStates (AC21, 100 shuffles, fixed seed); E7
TestStates_InputDiffersFlagged; E8 TestStates_DeniedWithoutIDIsNotRun; E9
TestSink_UnknownClientStoredAsUnknown; E10 TestHandler_NonLoopbackHost403; E11
TestStates_Pending; E12 TestMapHarness_ConcurrentSubagentsKeepOwnIDs; E13
TestRun_HookBurstDoesNotDropLLMCapture. Purity of the new package: TestPurity_HooksPackageImportsNoClientAndNamesNoHarnessEvent
(internal/hooks imports no `internal/clients/*` and names no Claude Code event). Hub
coalescing: TestHub_HookRefreshesCoalescePerSession, TestHub_HookRefreshSendsOnlyChangedExchanges, TestHub_HookRefreshCancelledWhenLastStreamCloses, TestHub_HookRefreshAndCommitInSameWindowSendEachChangeOnce.
Race: sink/Close concurrency under `-race`;
latency test is `//go:build !race`. Migration: TestMigrate_V2StoreUpgradesKeepsData.

## Risks
- Hook payload field names are assumed from docs; T001 verifies, and the mapper is tolerant (unknown -> `unknown`).
- Core session-key validation may differ from hook `session_id` shape: hook shows hooks-only; no fuzzy join. Limits damage to a label.
- Shared SQLite writer: a hook burst delays LLM writes (E13 known limit); mitigated by small hook queue/workers=1.
- Hub re-reads the whole session per hook event: coalesced to one re-read per session per 250 ms; hub queue/overflow logic drops streams, never blocks ingest.
- Web JS rendering is checked by string tests only; T012 carries a manual look.
- DNS rebinding / browser POST to the loopback port: Host + Origin/Sec-Fetch-Site checks, nothing stored on refusal.

## Open questions
None.

## Changelog
- v1 (2026-11-01) — first draft; revised before approval (hub trace, coalescing, T001 deps, permutation and purity tests)
