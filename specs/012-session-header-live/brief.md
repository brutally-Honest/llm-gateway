---
feature: 012-session-header-live
kind: fix
mode: quick
status: approved
version: 1
created: 2026-11-02
branch: fix/012-session-header-live
approved: 2026-11-02T09:01:19Z
approved-by: human
sha256: 749fbb8f2e070e374807f1f87ae0c1464105e0a3a44b66a2db35367d862c3295
fingerprint: 1ddf2f8ae6059b69c2f38d47e475f06ebf7e322610d2264d3430432bcb52896d
---
# Session header live

## Change
Repro: open an active session in the web UI, then commit more exchanges. Expected: the header (time
range, exchange, model call and tool call counts, errors, tokens) follows the timeline and matches a
reload. Actual: it keeps its first values until a reload. The page already applies `session` events
(`liveEffect`), but the hub reads and sends summaries for global streams only, so a session stream
never carries one.

## Acceptance
- **AC1** — When an open session stream's session gains a committed exchange, the system shall send a `session` event with that session's updated summary (AC15 shape) before the batch's `entries` events.
- **AC2** — When a hook event of a watched session triggers a refresh, the system shall send the session stream the updated summary as a `session` event, so a hook-driven change reaches the header too.
- **AC3** — When a session stream opens or reopens, the system shall send the session's current summary as a `session` event before its snapshot, so a reconnect also refreshes the header; a stream on a session with no stored row sends no `session` event and still opens as it does today.
- **AC4** — When two more exchanges with a tool call commit on an open session stream, the last `session` event's counts, token totals and last time shall equal a fresh `GET /api/sessions/{id}`.
- **AC5** — When the check file is read, `docs/manual-checks/010-hooks-ui.md` shall list under `## Known issues` only: "The session header's active label does not flip to inactive while the page stays open on a quiet session; a reload refreshes it.", and its reload step shall compare the session header's counts, token totals and last time as well as tool cards, hook lines and entry counts.

## Out of scope
- Changing the `entries`, `exchange` or `hooks` events, their ids or their order, and the global stream.
- Flipping the `active` badge when a session goes quiet with no new event: nothing is sent then.
- The client: `liveEffect` already replaces the summary on a `session` event, so no web file should change.

## Approach
`internal/ui/hub.go`: `readAndSend` always reads the summary and passes it on the session streams' `liveMsg`, which `writeLive` already writes first. `readSnapshot` fills `Sessions` for a session stream, which `sendSnapshot` already writes first; `store.ErrNotFound` there means no summary. Tests in `internal/ui/stream_test.go` and `hub_hook_test.go`: `open` skips `session` events on session paths, the same pattern it uses for empty `hooks` events, and `openRaw` tests that count events take the new one explicitly.

## Steps
- **S1** — Send the summary on session streams, live, on hook refresh and in the snapshot; adjust the stream test helpers — Tests: TestLive_SessionHeaderMatchesReloadAfterCommits (AC1, AC4: open stream, two more exchanges with a tool call, compare with a fresh snapshot); TestHub_HookRefreshSendsSessionSummary (AC2); TestStream_SessionSnapshotStartsWithSummary (AC3); TestStream_SessionSnapshotWithoutRowSendsNoSummary (AC3)
- **S2** — In the manual check, replace the header known-issue line with the active-label line (keeping the `## Known issues` heading) and extend step 6 (reload) to compare the header's counts, token totals and last time — Tests: none — docs lines, checked by reading the file (AC5)

## Changelog
- v1 (2026-11-02) — first draft
