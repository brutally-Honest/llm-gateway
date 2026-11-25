---
feature: 040-incremental-live-updates
---
# Research & decisions — 040-incremental-live-updates

## Progress
- 2026-11-25 Step 1 (measure, design), (this commit): baseline on main: call to view 2,534-2,712 ms, 3.0-3.2 CPU-s per call (0.7-0.9 of it capture), RSS 116 to ~680 MB; hook 2,268-2,298 ms; effect 750-786 ms; full build 1.8 s, 940 MB allocated; rules per input and fallbacks F1-F6 in spec.md; D1-D11.
- 2026-11-25 Step 1 commit f800e3b.
- 2026-11-25 Step 2 (design review, fresh opus), 8cedafc: 0 P1, 4 P2, 7 P3, 5 P4; P2s fixed in the design (D12-D15), P3s D16-D22, P4s folded into spec.md.
- 2026-11-25 Step 3 (oracle test, sonnet), f43d1c8: `internal/ui/incremental_oracle_test.go`, S1-S7 plus 20 random seeds (25-32 steps each), skipped until step 4; with the stub pointed at a full build every case passes, with one entry's text altered it fails at S1 step 0; a neutral test client (purity check); `buildSessionInc` stub and `buildInfo` in build_session.go.
- 2026-11-25 Step 4.1 (incremental build, sonnet), dac7ad8: `timeline.State` with per-exchange records, placement split (body key, header), snapshot diff, fallbacks F1-F6 counted by the timeline cache; view streams get a one-slot dirty signal, hooks and effects poke them at once; oracle enabled and green; planted breaks (no re-place after insertion; reused hook pass) both fail it; in process on the largest session: update 44-52 ms (append), 95-100 ms (insertion), state ~31 MB est.; quick live run: hook 291-300 ms, effect 241-244 ms, call ~950 ms from response end, first call after open a full build (cache evicted by 030's background pass, D30).
- 2026-11-25 Step 4.1b (D30, sonnet), e1bf8a7: a background-pass build is stored only into a free slot or over its own entry; 3 tests (2 fail without it); live probe: call stored to view 291-367 ms, 0.99-1.08 CPU-s per call (capture alone 0.69-0.87), RSS ~230 MB, rise 66-81 MB; hook 287-325 ms, effect 241 ms.
- 2026-11-25 Step 4.2 (store oracle, sonnet), 2cf864a: 70 sessions, 26,467 inputs replayed in capture order (row, parse, hook, effect), 7,039 full comparisons, 0 differences; builds 26,324 incremental, 70 full:no_state (first build), 73 full:large_delta (0.28 % real fallbacks); a planted break (body kept when the body key changed) is caught at session a1d57aac step 280; 485 s, peak 1.26 GB.
- 2026-11-25 Step 4.3 (persistent cache, sonnet 3): not run (D6); the measured restart case is main's.
- 2026-11-25 Step 4.4 (docs, haiku), 4d1e3a0: README live-updates bullet (about a third of a second, full rebuild on first open, eviction or a large burst); roadmap moves 040 to Done.
- 2026-11-25 Step 5a (measure, HEAD 4d1e3a0, no commit of its own; recorded here): call stored to view 303-341 ms, 0.25-0.35 CPU-s over the control, RSS ~210-270 MB with a rise of 51-117 MB; hook 299-301 ms, 0.21-0.26 CPU-s; effect 247-249 ms; all AC4 targets met (table under Step 5).
- 2026-11-25 Step 5b (UI check, haiku doc), (this commit): Chrome extension not connected, so a headed Chrome with a throwaway profile over DevTools (port 9333); 8 cases (Lean/Stark, dark/light, 1,440/2,560), 25 inputs each (20 calls, 3 hooks, 2 effects): every input on the page 265-408 ms after being stored, 0 px movement of a mid-list turn, 0 layout shift, no turn re-created, page equal to a reload (2,879 lines); docs/manual-checks/040-incremental-live-updates.md.

## Measurement setup
- Store copy, taken 2026-11-25 19:19 +05:30: `sqlite3 ~/.local/share/llm-gateway/gateway.db
  ".backup ~/.cache/llm-gateway-040/gateway.db"` and `cp -a` of `blobs/`: 3,919 exchanges,
  12,716 hook events, 6,971 effects, 1.3 GB database, 4.0 GB blobs. `pristine.db` beside it
  is restored before every gateway run, so each run starts from the same state.
- Largest session `local/claude-code/2fff4047-…`: 667 calls in the store (670 rows in the
  build), 2,056 hook events, 4,146 timeline entries, at most 1,533 events per call.
- Throwaway gateway: `systemd-run --user --scope -p MemoryMax=4G env -i HOME=~/.cache/llm-gateway-040/home
  <bin> -config <scratch>/gw.yaml`; proxy 17197, UI 17199, HTTP proxy 17198, SOCKS 17195,
  a fake upstream on 17196 answering a short streamed Anthropic reply; `capture.dir` is the
  copy. `llm-gateway.service` and ports 7195-7199 are never touched.
- Probe (scratch Go program): opens `/api/sessions/{id}/stream?view=1` on the largest
  session, waits for the first `view` event, then sends one input at a time and times from
  the end of the input (proxied response fully read, hook POST answered, proxied GET
  answered) to the next `view` event. CPU is the gateway's utime+stime from `/proc` from the
  input until 500 ms of quiet; memory is VmRSS sampled every 10 ms, the rise over the value
  before the input. Inputs: `x` replays that session's last request (8.8 MB, 606 messages)
  with its session header; `h` posts a PreToolUse hook for a new tool id with `cwd` under the
  throwaway HOME; `e` sends a GET through the HTTP proxy 17198, which the effects runner
  assigns to the one active session.
- Control: the same inputs with no stream open (`-nostream`), to separate capture's own cost.
- In-process profile (a `probe040`-tagged test kept in the scratch folder, never committed):
  `buildSession` then `sessionView` of the largest session on the copy, three times.

## Step 1: baseline (main at fd0a0f8)

| Input | View after | CPU (process) | RSS before | RSS rise |
|---|---|---|---|---|
| open (first view, cold) | 1,952-1,982 ms | | | |
| call 1 | 2,662 ms | 3.03 s | 116 MB | 516 MB |
| calls 2-5 | 2,534-2,712 ms | 2.99-3.21 s | 631-683 MB | 0-133 MB |
| hook 1-3 | 2,268-2,298 ms | 2.40-2.43 s | 665-672 MB | 2-32 MB |
| effect 1-2 | 750-786 ms | 0.12-0.14 s | 639-670 MB | 31-45 MB |
| control: call, no stream | | 0.69-0.87 s | 100-163 MB | 9-58 MB |
| control: hook, no stream | | 0.00-0.01 s | | |

So a call's view update costs about 2.3 CPU-seconds beyond its capture, and the first update
leaves the process about 520 MB larger for good.

In process (largest session): `buildSession` 1.76-1.87 s, 940 MB allocated per build,
`sessionView` 72-86 ms (38 MB allocated), view JSON 1.9 MB in 5 ms. CPU profile of a build:
58 % in `store.(*Reader).eventsCtx` (SQLite rows of 468k events), then GC and allocation;
`timeline.bestMatch` 3 %. Other pieces: `SessionSnapshot` 35 ms (670 rows), `HookEvents`
6.5 ms (2,056 rows), the last call's events 2.2 ms (1,207 rows), `estimateSize` 35 ms
(marshals the whole build on every store into the cache).

Fixed waits on the live path: a commit pokes view streams at once and the stream waits
`viewDebounce` (150 ms); a hook pokes them after `hookRefreshEvery` (250 ms) plus the debounce;
an effect after `effectsEvery` (500 ms) plus the debounce. The hook and effect waits alone
are 400 and 650 ms (D7).

The session page opens one stream only, the view stream (`app.js` `openStream(…,
"sessions/<id>/stream?view=1")` after `stopStream`), so the hub reads no summary or digest
for it; the digest work of the global stream is 043's and out of scope.

## Step 5: after (HEAD 4d1e3a0, same copy, harness and inputs as Step 1)

The probe also times from when the call is stored (its parse visible in the copy, polled every
5 ms through the sqlite3 CLI) to the `view` event; that is the Goal's "after it is stored".

| Input | Stored to view | Response end to view | CPU (process) | CPU over control | RSS before | RSS rise |
|---|---|---|---|---|---|---|
| open (first view, cold) | | 2,334 ms | | | | |
| calls 1-5 | 303-341 ms | 953-1,210 ms | 1.05-1.21 s | 0.25-0.35 s | 211-267 MB | 51-117 MB |
| hooks 1-3 | | 299-301 ms | 0.21-0.26 s | 0.21-0.26 s | 242-252 MB | 1-2 MB |
| effects 1-2 | | 247-249 ms | 0.17-0.18 s | | 215-217 MB | 0-2 MB |
| control: call, no stream | | | 0.75-0.97 s | | 110-171 MB | 8-57 MB |

Before (Step 1): call 2,534-2,712 ms from response end, about 2.3 CPU-s over the control, RSS
116 MB to about 680 MB; hook 2,268-2,298 ms, 2.4 CPU-s; effect 750-786 ms.

A call's time from the end of its response to the view is still about 1 s: capture parses an
8.8 MB request (about 0.7 s) before it stores the call, and that pipeline is unchanged (Goal
counts from the store, D1). The first open is still a full build (non-goal).

## Decisions
- **D1** (2026-11-25) CPU per update is the gateway process's CPU from the input to quiet,
  minus the same input's CPU with no stream open (capture, parse and store of an 8.8 MB
  request cost 0.7-0.9 s by themselves and are not the view's). Both numbers are recorded.
- **D2** (2026-11-25) Only the timeline build is incremental; the view, effect attribution
  (ADR 0015) and every list are rebuilt whole from the build on each update.
  Thesis: the build is 1.8 s of the 1.9 s and is the part that re-reads history; the view is
  72-86 ms on the largest session, so rebuilding it keeps late hooks and effects (which can
  re-label any earlier turn) exact by construction, with no rule per effect kind.
  Antithesis: an incremental view (append a turn, patch a label) would cut the 85 ms too and
  scale to sessions ten times larger. Synthesis: not now; the view is a pure function of
  in-memory inputs, its cost is a fifth of the budget, and per-effect re-attribution rules are
  where drift would hide. Flip: a session whose view alone passes about 250 ms.
- **D3** (2026-11-25) An update diffs a fresh snapshot of the session's exchange rows against
  the state (by request id, row equality and order), instead of applying the hub's commit
  messages. Thesis: commits arrive out of start order, two can share a dispatcher batch, a
  build can run while another commit lands, and the hub drops all streams on overflow; a
  snapshot diff is right whatever happened in between. Antithesis: applying known commit ids
  skips the 35 ms snapshot read. Synthesis: the snapshot read is the only store read that
  scales with the session's call count, and it is small next to the budget. Flip: sessions
  with tens of thousands of calls, where the snapshot read itself would need a delta query.
- **D4** (2026-11-25) The state lives in the timeline cache's entry for the session
  (`cachedBuild`), immutable once stored like the rest of the entry: an update builds a new
  state that shares the old one's per-exchange records and copies only slices and maps. It is
  bounded by the cache's existing bounds (4 sessions, 512 MB estimated) and counted in the
  entry's size, and capped per session at 128 MB estimated (F6: no state kept, every update a
  full build). It is dropped when the entry is evicted or replaced by a full build without
  state. Thesis: the cache already serialises builds per session, holds the latest build,
  and is what the view reads; one owner means one eviction rule and no second LRU.
  Antithesis: a hub-owned per-stream state would live exactly as long as an open stream and
  never be evicted by an unrelated session's open. Synthesis: the cache; the 4-session LRU is
  "open and recently viewed", and an eviction costs one full build (F1). Flip: users who keep
  more than four live sessions open at once; then a pin for sessions with an open view
  stream.
- **D5** (2026-11-25) Content-addressed memos (text by content hash and role, the input
  comparison of a hook against a tool call by the two input hashes) are shared by all states
  of one session behind a mutex: a value keyed by a content hash never goes stale. Everything
  else in the state is copy-on-write.
- **D6** (2026-11-25) 030's persistent cache needs no change, so step 4's third subagent is
  not run. It stores digests and views (views only of inactive sessions, 030 D18) under an
  exact (store version, effect version) key and the build identity; it never stores a
  timeline build. An incremental build equals a full build at the same version (AC1), so a
  view built from it is a valid entry for its key. A restart after live updates starts with no
  state, exactly like main: an active session has no view on disk either way (D18), and an
  inactive one is warm from disk as before.
- **D7** (2026-11-25) Hook events and effects poke the session's view streams at once, on the
  leading edge, instead of after the 250 ms hook window or the 500 ms effects window; the
  view stream's own 150 ms debounce still gathers a burst into one rebuild, and the windows
  still pace the entries streams and global effects events. Thesis: with 400 and 650 ms of
  fixed wait no hook or effect can show within the 500 ms the UI check asks. Antithesis: the
  windows bound rebuild rate under hook bursts. Synthesis: the debounce bounds a view stream
  to one rebuild per 150 ms, and an incremental rebuild is cheap enough for that. Flip: an
  update cost that stays above about 150 ms of CPU, which would keep a core busy during
  bursts.
- **D8** (2026-11-25) The cache's size estimate of an entry must not marshal the whole build
  on every store (35 ms, 31 MB on the largest session): it is computed from the state's own
  running estimate in O(changed) or O(n) without encoding, under 5 ms on the largest session,
  with the factor documented next to the old one.
- **D9** (2026-11-25) Hook rows are re-read whole on every build (6.5 ms for 2,056 rows) and the
  hook pass and call-facts pass run whole over the kept per-exchange data: a hook can change
  any tool call's state and any thread's sub-agent flag, and a whole pass over in-memory data
  is cheaper than tracking which.
- **D10** (2026-11-25) The full build stays the production fallback and the oracle's
  reference. Both paths share the per-exchange derivation, placement and hook pass as single
  functions in `internal/timeline` (one home per rule); only the driver that decides what to
  reuse differs, and that driver is what the oracle tests.
- **D11** (2026-11-25) Only the `parse` column of an exchange row changes after insert
  (`store.SaveParse`), and events are written in the same transaction. The diff still
  compares whole rows, so any other change also re-derives that exchange.
- **D12** (2026-11-25, review P2-1) The incremental driver lives in `TimelineCache.run`: the
  cache's build function becomes `func(ctx, prev *cachedBuild) (builtSession, error)`, `run`
  hands it `peek(id)`'s entry, and every caller (`timelineBuild`, `api_timeline.go`) passes the
  same function, so whether a run is incremental never depends on which caller queued it last.
- **D13** (2026-11-25, review P2-2) Placement is split: the body key (shared prefix length,
  diverged marker: `len(best.hashes) > shared`) decides the entries' body; the header fields
  (Thread, PrevID, Messages, Carried) are patched on a copy of the first entry. Entries are
  re-made from events only when the body key changes, so a late call that opens a new thread
  renumbers later threads without re-reading their events. F5 counts re-reads from events
  only, never header patches.
- **D14** (2026-11-25, review P2-3) A view subscriber gets a one-slot dirty signal: a poke is a
  non-blocking send to it and never drops the stream, so a burst of hook or effect pokes
  during a slow build (any fallback) leaves one pending rebuild. Pokes are sent outside
  `h.mu`. With that, D7 (leading-edge pokes) stands.
- **D15** (2026-11-25, review P2-4) Correction to D5: the memos (text by hash and role; hook
  input against call input by the two hashes and the cut) live in the state, are counted in
  its running size estimate (D8) and in the F6 cap, and are mutated only inside the session's
  serialised build run, so they need no mutex; a new state takes the old one's memo and the
  old state is never a build base again. A content read that fails with anything but not-found
  is never memoised. F3 drops them.
- **D16** (2026-11-25, review P3-5) Row equality in the snapshot diff is explicit: every scalar
  field, `TTFBNS` dereferenced, headers compared by value. A test asserts an unchanged
  snapshot re-derives zero exchanges.
- **D17** (2026-11-25, review P3-6) The interner is append-only and lives in the state, so kept
  hashes stay shared and the state stays small; F6's 128 MB cap is checked against the
  largest session's measured state in step 4 and moved if needed (new D-entry).
- **D18** (2026-11-25, review P3-7) Not changed: a run is labelled with the version its caller
  read, which can be older than the snapshot the build reads. It is the same on main, it only
  costs a wasted store or one extra rebuild, and the next update's snapshot diff corrects the
  state whatever the label. Flip: a measured rebuild loop caused by it.
- **D19** (2026-11-25, review P3-8) F4 replaces the cache entry with the full build; the
  incremental path never mutates its base state, and the hook pass always gets a fresh
  concatenation of entries.
- **D20** (2026-11-25, review P3-9) `cachedBuild` records whether it is a full or incremental
  build; the on-disk view cache stores only views built from a full build, so a drift (should
  one ever slip past the oracle) never survives a restart. An inactive session gets its disk
  view on its next full build, as on main after an eviction.
- **D21** (2026-11-25, review P3-10) Unassigned connections keep main's behaviour: they do not
  poke session view streams; the next rebuild of the view shows them. Out of scope.
- **D22** (2026-11-25, review P3-11) AC4 is measured with the session page's view stream as the
  only stream, which is how the session page runs; a sessions list open in another tab adds
  the digest catch-up, which is 043's (non-goal).
- **D23** (2026-11-25, review P4s) The unused usage summary is dropped from the global passes;
  the per-exchange tool data mirrors the hook pass's input exactly (spec); the best match is an
  index; a hooks-only session is a valid empty state with a test; `buildView` keeps reading
  hooks itself (as on main).
- **D24** (2026-11-25, step 4.1) Only a full build that keeps a state (`buildSession`) takes call
  facts' user texts from the state's records; `buildFrom` (the digest path) keeps its
  events-based path. The oracle compares two runs of the same derivation for user texts.
- **D25** (2026-11-25, step 4.1) The input comparison memo stores a result only when both blocks
  were read; a not-stored block is read again on each build, so a blob that lands late never
  leaves a stale answer (stricter than D15).
- **D26** (2026-11-25, step 4.1) F5 is `2*count > n` over changed rows plus body re-reads, checked
  before reading events and again after placement. Tiny sessions fall back often (a hooks-only
  state meeting its first call is `large_delta`); those full builds are cheap.
- **D27** (2026-11-25, step 4.1) An over-cap session remembers `too_large` on its build and every
  update reports it without retrying; `failed` logs at Warn with the error, other fallbacks at
  Debug, `no_state` not at all.
- **D28** (2026-11-25, step 4.1) The entry size estimate sums entries, facts, calls, rows and the
  state without encoding (0.3 ms on the largest session, was 35 ms); `sizeFactor` 1.5, against
  1.45 measured as retained heap over the sum (49 MB retained for build and state).
- **D29** (2026-11-25, step 4.1) A test that replaces `buildSessionFn` with a fake gets no state,
  so its cache never goes incremental; only the real full build seeds a state.
- **D30** (2026-11-25, step 4.1 live run) 030's background pass (`api_sessions.go`, views of
  inactive sessions written to disk) builds through the same 4-session timeline cache, and on
  a fresh view cache (every upgrade, every new build identity) it evicts the live session's
  entry, so the next update is a full build (2.8 s measured). Its builds must not displace
  cached entries: a background build is stored only into a free slot, never by evicting.
  Thesis: the LRU should hold what users look at. Antithesis: pin sessions with an open view
  stream instead, which also protects them from other users' opens. Synthesis: the
  background-pass rule is the narrow fix for the measured case; pinning waits for D4's flip
  (more than four live sessions open).
- **D31** (2026-11-25, step 4.2) The store oracle is `ui.RunStoreOracle` behind the `storeoracle`
  build tag, driven by `cmd/gateway/store_oracle_test.go` (same tag, env
  `GATEWAY_STORE_ORACLE_DIR`): the purity check forbids client names under `internal/ui`, and
  the real client rules live in `cmd/gateway`. It replays read-only through a time-travel view of
  the copy, never writing to it. It compares in full after every input for sessions of up to 150
  calls, and for larger ones every 20th input, each of the last 40 and the final state; every
  input goes through the incremental build. Unassigned connections pass through unfiltered,
  the same for both builds.
