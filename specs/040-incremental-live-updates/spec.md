---
feature: 040-incremental-live-updates
kind: feat
created: 2026-11-25
branch: feat/040-incremental-live-updates
---

## Problem
When a call, hook event or parse lands in an open session, the session view stream
(`/api/sessions/{id}/stream?view=1`) rebuilds the whole session's timeline: `buildSession`
re-reads and re-parses every stored event of every call, history included
(`internal/ui/build_session.go` `buildSession`, called through `TimelineCache.get` from
`cachedView`). On the largest session of the store copy (670 calls, 2,056 hook events) a new
call reaches the view 2.53-2.71 s after its capture, the update costs 2.3 CPU-seconds over
capture's own 0.7, and the gateway's RSS goes from 116 MB to about 680 MB. 58 % of a build
is reading 468k event rows; it allocates 940 MB each time (research.md, Step 1).

## Goal
On the largest session of the store copy, a new call, hook event or effect shows up in an
open session view in under 500 ms after it is stored, an update costs under 0.5 CPU-seconds
beyond what capturing the call costs with no stream open, and an update adds under 300 MB
to the gateway's RSS. The view is always exactly what a full rebuild at the same store state
would produce.

## Non-goals
- Changing what is stored, or the store schema.
- The sessions list and its digests (043); a session-summary table.
- The first open of a session (cold build); it stays a full build.
- Making the session view itself, or effect attribution, incremental (D2).

## What changes the view, and the rule for each
A view is `BuildView(timeline build, hook rows, effects, labels, unassigned connections,
active)`. The timeline build is the expensive part; the rest costs about 85 ms on the
largest session and is always rebuilt whole. The incremental state is per session and holds,
per exchange in timeline order, what the full build derives from that exchange's own events
(message hashes, request message count, usage, folded or not, tool call ids and input hashes,
tool result ids, user texts, and its entries before hook states are applied) plus where it
was placed (shared prefix length, best earlier match, diverged marker, thread).

| Input | What it can change | Incremental rule |
|---|---|---|
| A new exchange row (call ended, stored before its parse) | its own entries; the thread, best match and entries of every exchange after it in timeline order; hook states and hook-line merge; call facts of every call (sub-agent and main flags are per thread) | derive the new exchange from its own events only; re-place it and every exchange after it in timeline order (best match, shared prefix, thread); an exchange whose placement is unchanged keeps its entries, one whose placement changed has its entries re-made from its own events; then the global passes |
| A parse landing on a stored row (row's parse field changes) | as above, from that exchange's position | re-derive that exchange; re-place from its position; global passes |
| An exchange committed out of start order (a long call ending after later calls started) | as above, from its position | same as a new row: re-place from its position, not from the end |
| A hook event | tool-call states and chips, input-differs, hook lines and their merge position, agent ids, hence sub-agent/main call facts | no exchange is re-derived or re-placed; the hook rows are re-read whole and the hook pass and call-facts pass run whole over the kept state |
| A file or network effect | effect labels (ADR 0015) of any turn, files/commands/sites lists, unassigned connections | the timeline build is untouched (as on main); the view is rebuilt whole from the cached build |
| Time passing (active flag flips) | status | view rebuilt whole (as on main) |

The global passes, run whole on every update from the kept state: the hook pass (tool-call
states, loose hook lines merged by time), call facts (threads, side calls, sub-agents, main),
the usage summary, and the lookup maps. Then `BuildView` runs whole, so a late hook or
effect re-attributes earlier turns exactly as a full build does.

Exchange changes are found by diffing a fresh snapshot of the session's exchange rows
against the state, never by replaying the hub's messages: the order commits arrive in, a
dropped stream message or a hub overflow cannot make the state drift (D3).

### Fallback: a full build when
- F1 there is no state for the session (first open, evicted, restart);
- F2 a row the state holds is missing from the snapshot (the store never deletes);
- F3 the state was built with a different cut or client rules;
- F4 the incremental update fails or panics (logged, then a full build);
- F5 more than half of the session's exchanges would need re-deriving from their events;
- F6 the state would exceed its size cap (D4): a full build, and no state is kept.
Every build counts as incremental or full with its reason; the counts are kept by the timeline
cache and the store-copy run records the rate.

## Acceptance criteria
- AC1 After every input (new row, parse landing, out-of-order commit, hook event, effect) the
  incremental build's entries, call facts, timeline facts, exchange map and row ids, and the
  session view built from them, are byte-for-byte equal to a full build at the same store
  state (the oracle test, `internal/ui`).
- AC2 The oracle fails when an incremental step is broken on purpose (two planted breaks,
  recorded in research.md).
- AC3 The oracle run over every session of the store copy, each session's inputs replayed in
  capture order, finds no difference; the sessions checked and the fallback rate are recorded.
- AC4 On the largest session of the copy, measured as in Step 1: capture (or hook post, or
  effect) to the next SSE `view` event under 500 ms; CPU per update under 0.5 s beyond the
  no-stream control; RSS rise per update under 300 MB.
- AC5 The incremental state lives in the timeline cache's entry for the session, is bounded
  by that cache's session count and byte budget, has its own per-session cap, and is dropped
  with the entry.
- AC6 The fallback reasons F1-F6 each have a test; each build is counted as incremental or
  full by reason.
- AC7 The docs that describe live updates say how fast they are now and that some changes
  still cause a full rebuild.
- AC8 UI check (docs/manual-checks/040-incremental-live-updates.md): Lean and Stark, light and
  dark, 1,440 and 2,560 px, largest session open, 20 calls plus hooks and effects replayed:
  each appears within 500 ms, nothing flickers or jumps, the page matches a fresh reload.

## Edge cases
- E1 Two calls committed in one dispatcher batch, or a build already running when the next
  commit lands: the cache's one-run-per-session queue serialises builds; each diffs its own
  snapshot, so the second catches up whatever the first missed.
- E2 A row seen before its parse: derived as the full build derives it (folded, no events);
  re-derived when the parse lands.
- E3 A call whose start is before calls already in the state: re-placed from its position;
  later calls' threads, prev ids and diverged markers can change.
- E4 A hook whose receive time is before the last exchange's start: merged into the middle of
  the timeline by the hook pass, as in a full build.
- E5 A hook naming a tool call made several calls back, or a tool result landing in a later
  call: state, chip and "ran-not-reported"/"asked-not-run" move on the earlier call.
- E6 A sub-agent's hooks arriving after its calls: call facts flip `sub`/`main` on earlier
  calls of that thread.
- E7 An effect landing after later calls, or before the hook that names its tool: labels
  move in turns already sent; the view stream sends only turns whose hash changed.
- E8 The session evicted from the timeline cache between two updates: F1, a full build.
- E9 A gateway restart: no state survives; the first open is a full build (the on-disk view
  cache of 030 is untouched and needs no change, D6).
- E10 A session over the state cap: every update is a full build, as on main.
