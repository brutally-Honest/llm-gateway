---
feature: 043-fast-rows-first
---
# Research & decisions — 043-fast-rows-first

## Progress
- 2026-11-25 Step 1 (measure, decide), f71b54e: empty cache on main: copy page 1 4.33 s, page 2 504, filters 2.9-3.0 s; 3x page 1 4.57 s, page 2 504; row split, filter and fill-in decisions D1-D9.
- 2026-11-25 Step 2 (design review), aabed28: 2 P1, 6 P2, P3s and P4s; P1 and P2 fixed in the design (D10-D15, spec AC1-AC7, E1, E7).
- 2026-11-25 Step 3a (server, sonnet), (this commit): /api/sessions and the stream snapshot never wait for a build; pending rows, row_version, checking, build gate with list priority; 16 new tests; two cmd/gateway tests now wait for filled rows; make verify green.

## Measurement setup
- Store copy, taken 2026-11-25 05:51 +05:30: `sqlite3 ~/.local/share/llm-gateway/gateway.db
  ".backup ~/.cache/llm-gateway-043/gateway.db"` and `cp -a` of `blobs/`: 3,919 exchanges,
  12,716 hook events, 69 exchange sessions (70 rows: two pages of 50), 1.3 GB database,
  4.0 GB blobs.
- 3x store `~/.cache/llm-gateway-043/x3`: the copy plus two copies of every exchange, event,
  hook event and effect with `-x1`/`-x2` on the session key (and request id) and times moved
  20 and 40 days back, as 030 did: 11,757 exchanges, 38,148 hook events, 210 rows, 3.7 GB,
  blobs hard-linked.
- Throwaway gateway: `systemd-run --user --scope -p MemoryMax=4G env -i HOME=<scratch>/home
  <bin> -config <scratch>/gw.yaml`; API 127.0.0.1:17197, UI 17199, effects network off
  (17198/17195 configured), upstream 17196 (nothing listens); `capture.dir` on the copy.
  `llm-gateway.service` and ports 7195-7199 were never touched.
- Probe (scratch bash): one gateway start per request; "empty" deletes `view-cache/` before
  the start, "warm" keeps a cache filled by a previous start that waited for
  `gateway_ui_cache_rebuild_seconds`. The request is sent as soon as the UI port accepts;
  the time is curl's `time_total`. Page 2 is `offset=50`, page 5 `offset=200`; filters
  `kind=look|active|hooks`, search `q=gateway`.

## Step 1: before (main at a98455c)

| Request | Copy, empty | Copy, warm | 3x, empty | 3x, warm |
|---|---|---|---|---|
| Page 1 | 4,327 ms | 60 ms | 4,567 ms | 138 ms |
| Page 2 | **504** (5,000 ms) | 77 ms | **504** | 173 ms |
| Page 5 | 37 ms (empty page) | 34 ms | 3,073 ms | 119 ms |
| `kind=look` | 2,992 ms (8 of 20 found) | 101 ms | 3,305 ms (8 found) | 415 ms |
| `kind=active` | 2,914 ms | 98 ms | 3,295 ms | 526 ms |
| `kind=hooks` | 3,007 ms | 103 ms | 3,244 ms | 522 ms |
| `q=gateway` | 2,921 ms (16 of 29 found) | 104 ms | 3,279 ms (16 found) | 333 ms |

Empty-cache filters stop on the 2 s scan budget (030 D10) with part of the list unscanned, so
they also miss matches. Filling the 3x cache took the background pass 53 s.

SQL on 3x in the sqlite3 CLI: the session list query (GROUP BY over all exchanges and hook
events) 22 ms whatever its LIMIT; the folder of 50 sessions (first hook naming a
`work_dir`, on `hook_events_session`) 2 ms.

## Decisions
- **D1** (2026-11-25) Row split (spec Goal). Cheap is what the session list query, the version
  and effect-count reads and one folder query give: start, last activity, active (clock and
  last activity, as today), hooks only, client, key, exchange count, effect counts and the
  folder. The folder query is `sessionFolder`'s rule in SQL (first hook by received_at, seq,
  id with a non-empty `work_dir`) and its display goes through the same function the view
  uses (exported from `timeline`), so the cheap folder equals the built one.
- **D2** (2026-11-25) Title, model and the call count are heavy, though the brief offered them
  as cheap. The title is a generated title from a side call's response or the first prompt:
  content reads. The model comes from parsed usage (no `model` column on `exchanges`); doing it
  in SQL would copy the usage rules and could disagree. The exchange count is not the "N
  calls" the row shows (main calls, side calls apart), so showing it first would change a
  fact under the reader. The row shows the folder at once instead.
- **D3** (2026-11-25) Changes, Time and the failed status are heavy too: Changes joins effect
  counts (cheap) with the command count (view); Time is the view's clock span; failed needs
  the tool states. A pending row that is neither active nor hooks only shows a placeholder
  in the status cell, not the empty "ended" one, so no fact is implied.
- **D4** (2026-11-25) A row with a memo on disk at its current key is read synchronously
  (one effect-version read per page, then file reads), so a warm restart serves full rows and
  shows no flicker; only a miss in memory and on disk is pending. A memo in memory at an
  older version is served as today (labelled by its version, rebuilt behind).
- **D5** (2026-11-25) Filters while facts are missing. `active` and `hooks` need no facts:
  active is from the row, and the digest's `HasCalls` is "the session has an exchange", so
  "hooks" matches exactly the hooks-only rows. `look` and `q` test known facts; `q` first
  tests the cheap fields (client, key, id, folder). An undecided row is left out, counted in
  `checking`, and queued. Filtered scans read their rows in one list query (up to the scan
  cap) instead of 51-row batches, since the query costs the same at any LIMIT (22 ms on 3x).
- **D6** (2026-11-25) Fill-in reaches the page by a follow-up fetch of the page on screen,
  repeated about every second while it has pending rows or `checking > 0`, backing off and
  stopping after two minutes (E1). Thesis for the stream: per-row, instant, already wired.
  Antithesis: filters need a refetch anyway (only the server can say a row now matches), a
  fetch also works with the stream down, and arming a hub catch-up per pending row would add
  a goroutine and a read per row. Synthesis: the fetch is the one path that covers every
  case; `session` events still update held rows as today. What would flip it: a list so
  large that a once-a-second fetch costs too much, or a need for sub-second fill-in.
- **D7** (2026-11-25) Every row in a list answer carries its store `version`; the browser
  keeps a held row when an incoming one (fetch or event) has an older version (the Go
  `versionAfter` rule). A pending row carries the version read for the page, so it replaces
  older facts with placeholders rather than keep them.
- **D8** (2026-11-25) Build order: every memo compute goes through a gate of 2 concurrent
  builds (tuned in step 4). A waiter is picked by priority: computes a caller waits for
  (session page, live update) and the latest list request's rows rank above older requests,
  in row order; the background pass ranks last and does not start its next session (memo or
  view) while prioritized work is queued. The build timeout starts when a compute gets its
  slot. Today every compute starts its own goroutine at once, so an empty-cache page would
  start 50 builds together.
- **D9** (2026-11-25) The live hub and the session endpoint keep waiting for their memo as
  today (their own timeouts); only the list endpoint stops waiting.
- **D10** (2026-11-25, corrects D7 after review P1-1, P2-3) Two versions per row: `row_version`
  (new, the store version the cheap fields were read at, on every row) and `version` (as
  today, the version the facts were built at; absent on a pending row). `versionAfter` is
  "not componentwise older", so equal versions are not "after": the browser merges per part
  (spec AC5): cheap part unless the incoming `row_version` is strictly older; facts when the
  incoming row has facts not strictly older; a pending incoming row keeps held facts (the
  same rule as D4's older memo in memory, which the server also serves). A full page load
  replaces rows without the rule (restored store, E7). The browser read no `version` before,
  so nothing else changes meaning.
- **D11** (2026-11-25, review P1-2) The stream's opening snapshot (`readSnapshot`) uses the
  list's non-waiting path and ranks as list work; its pending rows reach `onSession` and go
  through the D10 merge. Otherwise an empty cache still fails the snapshot (D30 again).
- **D12** (2026-11-25, corrects D8 after review P2-4, P2-5, P2-8) Only list work (requests and
  snapshot) and the background pass go through the gate; a compute some caller waits for
  (session page, `readLive`, catch-up `waitFresh`) runs at once as today, and a waiter that
  joins a run still queued in the gate promotes it to run at once. So waiters keep their
  timeouts and busy live sessions cannot hold list slots. The pass is classed by caller,
  ranks last, and its timeout starts at its slot. The gate is one dispatcher with a priority
  queue, not a parked goroutine per run. Disk is checked before a run is queued. Slot count:
  start at `max(2, GOMAXPROCS/4)` (3 here) and set from step 4's measurement.
- **D13** (2026-11-25, review P2-6) Filtered paging: while `checking > 0` the browser does not
  correct the URL's page; one list fetch is in flight at a time (a sequence number drops late
  answers, `refetchHead` included); a follow-up fetch of filtered page n walks from page 1
  (n requests, n is small). Server-side filtered offsets were considered (the list query
  costs the same at any LIMIT) and left out: it changes the paging contract 042 tested.
- **D14** (2026-11-25, review P2-7) Filtered scans read their rows in one list query (D5) but
  look facts up row by row in batches of 51 (versions, effect counts, disk) and stop as soon
  as the page is full and a next row is known to exist. The 2 s scan budget (030 D10) stays
  as a guard; without builds it should not trigger.
- **D15** (2026-11-25, review P3) Pending rows have their own JSON shape (heavy keys absent);
  the browser decides "hooks only" from `hooks_only` for a pending row. The follow-up stops
  after two minutes with a "details not available yet" note and Retry; it is cancelled on
  page or filter change and when leaving the list, and paused while the tab is hidden.

### Design review (step 2, fresh opus)

| # | Finding | Done |
|---|---|---|
| P1-1 | Version guard drops the fill-in at equal versions, or wipes facts | D10 |
| P1-2 | Stream snapshot still waits (504 on empty cache) or bypasses the merge | D11 |
| P2-3 | One version for parts read at different versions | D10 (`row_version`) |
| P2-4 | Gate makes waiters time out and drops streams; join does not promote | D12 |
| P2-5 | Busy live sessions starve the list | D12 (waiters bypass the gate) |
| P2-6 | Filtered paging: URL corrected while checking, racing refetches | D13 |
| P2-7 | 500 rows and their disk memos read before the page fills | D14 |
| P2-8 | 5 s fill at 2 slots unproven | D12 (measured in step 4) |
| P3 | Pending shape, cheap folder wins, restored store, follow-up stop, goroutine per run, disk before gate, pass timeout | spec AC1, AC2, E1, E7; D12, D15 |
| P4 | D5 hooks rule holds; versionAfter total in practice; no deadlock; look+q; scan budget | checked; AC4, D14 |
