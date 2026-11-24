---
feature: 030-persistent-view-cache
---
# Research & decisions — 030-persistent-view-cache

## Progress
- 2026-11-24 Step 1 (baseline and skip prototype), scratch bbcd2e9 (local, never pushed): the skip makes builds ~1.8x faster with byte-identical output for all 70 sessions, but a cold build of the largest session stays ~1.7-2.3 s and a live update 2.6 s, so the cache carries the rest and live updates go to 040 (D4).
- 2026-11-24 Step 2 (design): spec.md and the design below; D5-D14. 3x store seeded and its baseline taken.
- 2026-11-24 Step 3 (design review, fresh opus): 3 P1, 4 P2, 4 P3, 4 P4; every P1-P3 folded into the spec and design except the labelled stale fallback (D17); D15-D22.
- 2026-11-24 Spec committed, 91ff92b: spec.md and research.md with steps 1-3; make verify green.
- 2026-11-24 Step 4 Sonnet 1 (skip), ecf1a1b: text memo by (hash, role) and per-pass hash interning; equivalence test fails when the key ignores role; store-copy dump of all 70 sessions byte-identical to main; make verify green.
- 2026-11-24 Step 4 Sonnet 2 (cache), c8638cb: disk cache, serve at once, background pass, new metrics; make verify green; smoke test on the copy: after a restart page 1 63 ms, page 2 51 ms, pass 0.10 s, warm-up metric 0.027 s; disk-served row, /view and /timeline of all 70 sessions byte-identical to main. With an empty cache page 2 still timed out (D27).
- 2026-11-24 Step 4 Sonnet 3 (041 D15), e93b938: placeInSession compares the full view key; the new test fails on the old prefix check; make verify green.
- 2026-11-24 Step 4 Haiku 4 (page-1 error), 3ccc10a: pager always shown on an error, Next on for an unfiltered unknown total; 244 web tests pass; make verify green.
- 2026-11-24 Step 4 Haiku 5 (docs), 1dbed32: install.md view-cache section and listeners paragraph, observability metrics row, roadmap (030 done, 040 next).
- 2026-11-24 Step 5 (measure, UI check): targets met on the copy; on 3x every page and filter answers with no 504, pages 109-211 ms (the list query's floor, D29); empty-cache page 2 still times out (D27, D30); folder deletion and a restored older store both rebuild; 16 screenshots pixel-identical to main.
- 2026-11-24 Step 6 review round 1 (fresh opus): 1 P1 (AC13, not code: D32), 2 P2, 1 P3, 7 P4; P2-2 fixed in 2786bbc (zone rules in the build id), P2-3 and P3-4 in 98d2727 (high-water covers a view's unassigned max; four tests).
- 2026-11-24 Step 6 review round 2 (fresh opus, last round): no P1 or P2; P3-1 (store-going-back check only at cache open) plus P4-1 and P4-3 handed to a fix.
- 2026-11-24 Step 6 round-2 fixes (resumed session; the killed subagent's work kept after review): P3-1 (D33) in af6769b, P4-1 in 6c9cbd4, P4-2 and P4-3 in 07f9d57; the identity test fails when the open-time identity check is disabled (go test -overlay probe); touched tests pass -race -count=20; make verify green.
- 2026-11-24 Step 6 re-measure (copy, after the key and high-water changes): page 1 57-61 ms, page 2 53-58 ms, page 5 27-28 ms, kind=look 75-81 ms, cold /view largest 34-36 ms, warm-up 0.025-0.026 s; empty cache unchanged (page 2 504, D30); table under Step 5.
- 2026-11-24 Step 7 (finish): spec status done; make verify green twice; throwaway gateway stopped, store copy and scratch/030-skip-proto deleted.

## Measurement setup
- Store copy, taken 2026-11-24 19:05 +05:30: `sqlite3 ... ".backup ~/.cache/llm-gateway-030/gateway.db"`
  and `cp -a` of `blobs/`: 3,919 exchanges, 1.66M events, 1.3 GB database, 4.0 GB blobs,
  70 sessions (two pages of 50). The largest session: 667 calls, 468,716 events, 183 MB of
  event payload, 2,686 distinct content hashes. A pristine copy of the database is restored
  after every run that sends calls.
- Throwaway gateway: `systemd-run --user --scope -p MemoryMax=5G env -i HOME=<scratch>/home
  ... <bin> --config <scratch>/config.yaml`; API 127.0.0.1:17197, UI 17199, HTTP proxy 17198,
  SOCKS 17195, a fake upstream on 17196 answering a short streamed reply; `capture.dir` on
  the copy. `llm-gateway.service` and ports 7195-7199 are never touched.
- Probe (scratch Go program): starts the gateway, polls until the UI port accepts, then
  times each request from send to full body. "Cold" is right after the port is bound;
  "warm" is after the `ui ready` log line plus 1 s. Page 2 is `offset=50`, page 5
  `offset=200` (empty on the copy: two pages only; it is measured on the 3x store in step 5).
  Filters are `kind=look|active|hooks`, search `q=gateway` (29 hits) and `q=zzzznomatch`.
- Live update: an open `/api/sessions/{id}/stream?view=1` on the largest session; the
  probe replays that session's last request body (8.8 MB, 606 messages) through the proxy
  with its session header and times from the end of the proxied response to the next
  `event: view`. Three calls, 2 s apart.
- Byte-for-byte check: every session's list row (from `/api/sessions`, minus the
  clock-dependent `active`), `/view` and the whole `/timeline` (all pages) dumped by both
  builds of the same store state and compared with `diff -r`. Both binaries carry the
  same version string.

## Step 1: baseline and prototype

The warm-up on this copy is 4.8-5.8 s, not the 15-31 s seen on the real gateway; the
real gateway also runs effects and live traffic, and its store is still growing.
Measured here, from the same copy and harness for both builds:

| Measure | Baseline (main) | Prototype (skip) |
|---|---|---|
| `gateway_ui_warmup_seconds` | 4.85 / 5.35 / 5.44 / 5.76 | 2.87 / 2.90 / 3.00 |
| Page 1, cold (sent at bind) | 5,483 ms | 3,039 ms |
| Page 2, cold | 504 at 5,000 ms | 504 at 5,000 ms |
| Page 2, warm | **504 at 5,000 ms** | 2,252 ms |
| Page 1, warm | 59 ms | 46 ms |
| `kind=look`, cold | 504 (10,755 ms) | 504 (7,899 ms) |
| `kind=active`, cold | 504 | 2,434 ms |
| `kind=hooks`, cold | 504 | 77 ms |
| `q=gateway`, cold | 3,003 ms | 79 ms |
| `q=zzzznomatch`, cold | 89 ms | 77 ms |
| Filters and search, warm | 83-87 ms | 77-84 ms |
| `/view` largest, cold (sent at bind, waits for warm-up) | 8,249 / 9,303 ms | 4,787 ms |
| `/view` largest, warm (in memory) | 18 ms | 16 ms |
| Live update, capture to SSE view | 4,293 / 4,057 / 4,612 ms | 2,694 / 2,550 / 2,587 ms |

Cold filters run in the order listed, so a later one finds memos the earlier ones built.

Profile of the largest session, in process on the copy (`computeMemo` then
`buildSession` then `sessionView`, twice):

| | computeMemo | buildSession | sessionView |
|---|---|---|---|
| Baseline | 6.56 s, 5.36 s | 3.01 s, 3.71 s | 78-87 ms |
| Prototype | 2.70 s, 2.31 s | 1.90 s, 1.66 s | 66-84 ms |

Baseline CPU: `timeline.textOf`/`userText` 39% (JSON decode of the message payload and
content reads, for every resent history message), `UserTextsOf` in `CallFacts` 26%,
`Reader.eventsCtx` 23%, `bestMatch`/`lcp` 16% (64-byte string compares). Prototype CPU:
`eventsCtx` 49-51%, mostly modernc `rows.Next`, `columnText` and the allocation of 183 MB of
payload per load of the largest session; `textOf` and `lcp` drop out of the top 30.

The prototype (scratch branch `scratch/030-skip-proto`, never pushed):
- a `timeline.TextMemo` a Source may implement; `textOf` asks it first, keyed by
  (content hash, role), and fills it after a successful decode;
- `ui.eventsOnce` implements it and interns content-hash strings per build, so `lcp`'s
  equal strings share a pointer and compare in one instruction.

Output: 210 files (70 sessions x list row, `/view`, full `/timeline`) byte-identical between
baseline and prototype.

### Baseline on the 3x store

`~/.cache/llm-gateway-030/x3`: the copy plus two copies of every exchange, event, hook event
and effect with `-x1`/`-x2` on the session key and times moved 20 and 40 days back, so the
copies fill pages 2-5 (11,757 exchanges, 4.98M events, 210 sessions, 3.7 GB database, blobs
hard-linked). Baseline binary:

| Measure | Baseline (main) |
|---|---|
| `gateway_ui_warmup_seconds` | 7.74 / 6.51 |
| Page 1, cold | 7,855 ms |
| Page 2 / page 5 / `limit=20&offset=80`, cold | 504 / 504 / 504 |
| Page 1, warm | 124 ms |
| Page 2, warm | **504** |
| Page 5, warm | 2,970 ms |
| `kind=look`, `active`, `hooks`, `q=gateway`, cold | 504 (look after 11,506 ms) |
| `q=zzzznomatch`, cold | 3,300 ms |
| All five filters and search, warm | **504** |

## Step 5: after

Same harness, final build (HEAD after the docs commit), `NEWGW=1` waits for the background
pass (`gateway_ui_cache_rebuild_seconds` set) instead of the old `ui ready` line.

| Target | Store copy, before | Store copy, after | 3x, before | 3x, after |
|---|---|---|---|---|
| Page 1 after restart (< 100 ms) | 5,483 ms | **64 ms** | 7,855 ms | 151 ms |
| Page 2 after restart (< 100 ms) | 504 | **55 ms** | 504 | 211 ms |
| Page 5 after restart (< 100 ms) | (none) | (none; 27 ms empty) | 504 | 153 ms |
| `gateway_ui_warmup_seconds` (< 1 s) | 4.85-5.76 | **0.026-0.032** | 6.51-7.74 | **0.032-0.034** |
| Background pass, warm cache | — | 0.10-0.14 s | — | 0.47-0.56 s |
| Background pass, empty cache | — | 12.9-16.9 s | — | 74.9 s |
| Cold filters and search (< 300 ms copy; no 504 on 3x) | 89 ms to 504 | **79-108 ms** | 504 (4 of 5) | **274-445 ms, no 504** |
| Cold `/view` largest (< 500 ms) | 8,249-9,303 ms | **44-52 ms** | — | **46 ms** |
| Cold `/calls/{eid}` largest (D19, not a target) | — | 1,806-2,600 ms | — | 2,363 ms |
| Live update (040) | 4,057-4,612 ms | 2,540-2,662 ms (one run 2,885-4,527) | — | — |
| Page 1 / page 2, empty cache (first start after an upgrade) | — | 2,952-3,880 ms / **504** | — | 504 / 504 (`limit=20&offset=80` 4,865 ms) |

Re-measured 2026-11-24 after the step-6 fixes (build af6769b, which changed the cache key and
the high-water check), copy only, three restarts with a warm cache. The probe of step 5 was
lost with its scratch folder, so this is curl's `time_total` sent right after the UI port
binds; no live calls were sent. The 3x store was not re-measured.

| Target | Step 5, copy | After review fixes, copy |
|---|---|---|
| Page 1 after restart | 64 ms | **57-61 ms** |
| Page 2 after restart | 55 ms | **53-58 ms** |
| Page 5 after restart | 27 ms (empty) | **27-28 ms** (empty) |
| Filter `kind=look`, cold | 79-108 ms (all filters) | **75-81 ms** |
| Cold `/view` largest | 44-52 ms | **34-36 ms** |
| `gateway_ui_warmup_seconds` | 0.026-0.032 | **0.025-0.026** |
| Background pass, warm cache | 0.10-0.14 s | 0.108-0.112 s |
| First start (new build id, empty cache): page 1 / page 2 / `kind=look` | 2,952-3,880 ms / 504 | 3,305 ms / 504 / 3,203 ms |

No "store is behind" or high-water warning on any restart: the identity check keeps an
unchanged store's cache.

On the 3x store the same pages warm in memory take 102-132 ms, and main's warm page 1
there took 124 ms: the floor is each request's full-store session list (D29).

Checks on the copy:
- Folder deleted while running: pages kept answering from memory (43-50 ms); the largest
  view rebuilt in 2.0 s and recreated `view-cache/<build>/v/`; the next start's pass rebuilt
  all 70 memos and 70 views.
- Restored older store: after live calls the high-water mark was exchange row 3,922;
  starting on the pristine copy (max row 3,919) logged "the store is behind the cache (an
  older copy was restored?); the cache of this build was deleted" and rebuilt. An earlier
  attempt, where no entry had been built on the three new rows, kept the cache, correctly.
- Disk: 15 MB (copy, 70 sessions), 44 MB (3x, 210 sessions).
- Old build folders stayed while younger than 10 minutes (D22).

UI check (`docs/manual-checks/030-persistent-view-cache.md`): the Claude in Chrome tab was
opened 0.24 s after the UI port bound; page 1 data arrived 132 ms after navigation; Next
showed page 2 of 2 in 11 ms, no error. The extension's window would not resize below
2,560 px, so the 1,440 and 2,560 px cases ran in a headless Chrome with a throwaway profile
over the DevTools protocol (D31): list page 1 and the largest session page, Lean and Stark,
light and dark, 16 screenshots from this build and 16 from main on the same store, all
pixel-identical (`compare -metric AE` = 0), and the page text identical at both widths.

## Design

What is cached, per session, in `<capture.dir>/view-cache/<build>/`:
- `m/<sid>` the memo: stats, digest, list-row facts (what `summarizeRows` needs).
- `v/<sid>` the view as `/view` serves it, with the session's last activity, end-only
  fact and the active flag it was built with.

`<sid>` is the hex sha256 of principal, client and key joined by NUL; the file itself names
the full identity, so a collision is a key mismatch, never a wrong hit. `<build>` is the
first 16 hex of sha256(format version, gateway version, sha256 of `/proc/self/exe`
(else `os.Executable()`), attribution skew, home folder, `time.Local` name). Beside the
build folders, `view-cache/<build>/high-water` holds the largest exchange rowid, hook id and
effect id any written entry was built on (AC8).

File format: one header line of JSON (`format`, `build`, `ident`, `key`, the body's
sha256, and for a view `last`, `end_only`, `active`, `from`, `to` and `unassigned_max`:
the activity range the view's unassigned connections were read over and the largest
unassigned `net_conn` effect id when it was built), then the body as JSON. A read checks
format, build, ident, key and checksum before decoding the body. Writes go to a temp file in
the same folder, then `rename`, so a reader sees the old file or the new one whole.

Key: `buildKey(store version) + effect count.max id`, the same string the in-memory view
cache uses as a view's `Version`; for a view, also `isActive(last, end_only, now) == active`
and no unassigned `net_conn` effect with `id > unassigned_max` and `at` in `[from, to]`
(one query on the effects primary key, `id > ?`), checked for in-memory views too (D15).

Read path:
- Memo: `DigestCache.get`/`latest` look in memory, then on disk for an exact hit (stored
  into memory), then fall back to the in-memory older memo or a compute, as today; the
  compute itself checks the disk first, so a request that joined it before the cache was
  consulted still gets the hit.
- View: `buildView` reads the store and effect versions first (it already does), then
  looks in the in-memory view cache and on disk by the current key before it asks the
  timeline cache for a build.

Write path: when a memo compute or a view build stores its result in memory, it also hands
it to the cache writer: one goroutine, a bounded queue, never blocking a request (the
background pass waits for room). Before writing, the writer re-reads the session's store
and effect versions; it writes only if they still equal the entry's key (AC5), raises the
high-water file first, then writes the entry. A memo compute reads the effect version
before it starts, alongside the store version its caller read before it. A view is handed
over only when its timeline build is at the version read for the request, the session is
not active, and no content read failed during the build (D16). Damaged files are deleted by
the writer, only if the file still has the checksum that was found bad.

Start-up: the executable is hashed and the high-water file checked against the store's
maxima before the UI serves (tens of ms); then the UI serves at once. A background pass
deletes other build folders and stray temp files idle for 10 minutes, then walks every session in
list order, one at a time: for each, versions are read in batches; a memo or view that
misses on disk is built through the normal paths (so it lands in memory and on disk and
coalesces with any request building the same session). It then enforces the size cap.

## Decisions
- **D1** (2026-11-24) Page 5 does not exist on the copy (70 sessions, 50 a page); it is
  measured on the 3x store in step 5, and on the copy `offset=200` (empty) and
  `limit=20&offset=80` (empty) answer in ~30 ms either way.
- **D2** (2026-11-24) The probe's "cold" requests are sent the moment the UI port accepts.
  On main the port is bound before `ui.Warm` ends and requests wait in the listen queue, so
  a cold time includes the rest of the warm-up; that is what a user sees.
- **D3** (2026-11-24) The skip memoises by (content hash, role). `core.Canonicalize` hashes a
  message as `{"role", "blocks": [{type, hash}]}`, so equal hashes mean equal role and
  blocks; `textOf` reads only those and content by hash, which never changes. A decode
  failure is not memoised (it belongs to the payload, not the hash), nor is a content-read
  error. Interning: the prototype rewrote `EventRow.ContentHash` in place; the real change
  interns inside each pass's `messageHashes` instead, so a Source's slices are never written.
- **D4** (2026-11-24) Step 1's rule: the prototype does not bring a cold build of the largest
  session under 500 ms (1.7-2.3 s) nor the live update (2.6 s). Kept, since it halves every
  build and is proven identical; the cache carries the start-up and cold-open targets, and
  the live update goes to 040. What is left is reading 469k event rows and 183 MB of payload
  per build through modernc; skipping those reads needs the events' payloads by first
  occurrence or an incremental build, both larger changes than this PR.
- **D5** (2026-11-24) Files, not a SQLite cache database. Thesis for SQLite: one file, atomic
  transactions, easy size accounting, the driver is already there. Antithesis: a second
  database to open, migrate and recover; a corrupt page loses every entry; an unlinked
  database keeps working on a ghost inode, so "delete the folder while running" would not
  rebuild until a restart; and its writes would share the driver's single-writer lock
  pattern with the store. Synthesis: one file per entry, temp-and-rename. Damage stays per
  entry, deletion is visible at once, and the format is two lines of JSON from the standard
  library. Flip: tens of thousands of sessions (inode and directory-scan cost), or entries
  needing transactions across sessions.
- **D6** (2026-11-24) JSON bodies, not gob. The view is already served as JSON and its struct
  has only exported, tagged fields, no custom marshalers and no `json:"-"`; a round-trip test
  (seeded sessions, `json(decode(json(v))) == json(v)`) guards it. The memo has unexported
  fields, so it gets a small DTO with its own round-trip test.
- **D7** (2026-11-24, hashing moved before serve by D21) The build identity hashes the executable, not only the version string:
  `make build` stamps `git describe --dirty`, and a plain `go build` stamps nothing, so two
  different binaries can share a version. Reading the binary once (~30 MB) runs in the
  background pass, before the cache is used; until then every cache read is a miss. The
  attribution skew joins it because `viewDeps` passes it into every view; client rules are
  code, covered by the hash.
- **D8** (2026-11-24) Exactness rule (AC5) instead of a snapshot read. The builds read in
  several transactions after the version was read, so the content can be newer than its
  key. Versions only grow in v1 (no deletes, no retention), so if the versions re-read at
  write time still equal the key, nothing changed in between and the content is exactly the
  key's state. The in-memory caches keep their behaviour (older-labelled content is fine
  there: it is never written down).
- **D9** (2026-11-24) No config key. The cache is disposable, bounded (AC9), and safe to
  delete; a switch would add config, env, docs and tests for no case the folder deletion
  does not cover.
- **D10** (2026-11-24, superseded by D20) Store going back (AC8): restoring an older `gateway.db` makes versions
  smaller, and later writes could bring a session back to a tuple a cached entry already
  has, with other content (rowids are reused after the max row goes). The background pass
  runs at start, before most new writes, and drops the whole build folder on the first entry
  found ahead of the store. Documented: delete `view-cache/` when restoring a backup.
- **D11** (2026-11-24) Metrics (AC10): `gateway_ui_warmup_seconds` keeps its name and now
  measures what it says in the doctor's terms, the time until the UI serves; the
  background pass gets its own gauge, and reads a counter by kind and result, so a cache
  that never hits is visible.
- **D12** (2026-11-24) The background pass builds views through the normal view path, a
  second load of each session beside its memo. Sharing one load between the memo and the
  view (which `computeMemo` already half-builds) would halve a full rebuild and the live
  update; it is left to 040 with the incremental build.
- **D13** (2026-11-24) Size (AC9): 32 MiB per view and 1 GiB for all views of a build.
  The copy's 70 views total 15 MB (largest 1.9 MB). Eviction deletes the oldest-written view
  files; an evicted view is rebuilt on its next open. Old build folders are deleted at start,
  so at most one build's entries live on disk.
- **D14** (2026-11-24) The memo key on disk is the full view key (store and effect version),
  stricter than the in-memory memo (store version only): row facts come from the view, which
  reads effects, and a disk entry lives across restarts.

### Design review (step 3, fresh opus)

| # | Finding | Done |
|---|---|---|
| P1-1 | Unassigned connections (`session_key IS NULL`, stored when they close, with their start time) shape a view's Sites but are in no key | Fixed in design: header `from`/`to`/`unassigned_max` and an `id > ?` check on every view hit, in memory too (D15, AC4) |
| P1-2 | `sessionView` turns content-read errors into nil, so a view built while the request deadline passed is degraded and would be cached for good | Fixed in design: record non-not-found errors, cache such a result nowhere (D16, AC5) |
| P1-3 | Misses still wait; the executable hash in the background makes every read at bind a miss; a joined compute skips the disk | Hash before serve (D21); disk checked inside the compute (AC7). The labelled stale disk fallback is rejected (D17) |
| P2-1 | AC8's in-pass detection races requests after a restored store | Replaced by a high-water file checked before any read (D20, AC8) |
| P2-2 | A key mismatch is the normal stale case, not damage; a request's delete can remove the writer's fresh file | Key/active mismatch is a plain miss; deletes go through the writer, checksum-guarded; warnings rate-limited (AC6) |
| P2-3 | Active sessions' AC5 re-read rarely matches, and each live update would write a ~2 MB view | Views of active sessions are not written to disk (D18) |
| P2-4 | "Every page" overclaims: call and tool drawers, `/timeline` stay cold | Goal narrowed to the sessions list and the session page; drawers measured cold in step 5 (D19) |
| P3-1 | HOME and `time.Local` shape folder and title | Added to the build identity (AC4) |
| P3-2 | A view keyed by an older `built.ver` must not be written | Written only when `built.ver` equals the version read (AC5) |
| P3-3 | `os.Executable()` can name a replaced binary | `/proc/self/exe` first (AC4) |
| P3-4 | Two gateways on one data folder delete each other's folders | Only build folders and temps idle 10 minutes are deleted (D22) |
| P4 | Background pass should wait on a full queue; evicted views rebuilt each start; memo files uncapped; guard `textOf` memo on message events and drop the second decode | First and last taken (AC5, step 4); the other two accepted as they are (D13, D5's flip) |

- **D15** (2026-11-24) Unassigned connections: a view reads `UnassignedConnsBetween(from, to)`,
  where `from`/`to` is the session's activity range. The header keeps that range and the
  largest unassigned `net_conn` id at build time (read before the build); a hit also needs
  `NOT EXISTS (session_key IS NULL AND kind = 'net_conn' AND id > unassigned_max AND at
  BETWEEN from AND to)`, which walks only effects newer than the build by primary key. The
  same check guards the in-memory view cache, which had the gap too.
- **D16** (2026-11-24) A build during which any content read failed for a reason other than
  not-found (a passed deadline, an I/O error) is served to its caller as today but stored in
  no cache, in memory or on disk, so a degraded view never outlives its request.
- **D17** (2026-11-24) The review proposed serving an older disk memo labelled with its
  version (as `latest` does in memory) so a cold page never waits. Thesis: the list already
  shows labelled older memos from memory and catches up live. Antithesis: the human's rule
  for this cache is that an entry is used only on an exact key match and never served stale;
  a disk entry can be days old, and a restart has no live catch-up stream for list pages
  that are not open. Synthesis: no fallback; exact hits only. A cold page after an upgrade
  builds on demand (about 3 s for 50 sessions of the copy, with the skip). Flip: if step 5
  shows a page 504 after an upgrade on the 3x store, revisit with the human.
- **D18** (2026-11-24) The view of a session active at build time is not written to disk: it
  changes every few seconds, its AC5 check rarely holds, each write is up to a few MB, and
  after a restart its active flag has usually flipped, so the entry would miss anyway. Its
  memo is written (small). Once idle, the next view build (an open, or the next start's
  background pass) writes it.
- **D19** (2026-11-24) The call and tool drawers and `/timeline` read the timeline build,
  which this PR does not persist (non-goal). After a restart they build on first use, about
  2 s for the largest session with the skip; step 5 measures `/calls/{eid}` cold on the copy
  and the 3x store.
- **D20** (2026-11-24) Store going back, replacing D10: a high-water file per build folder
  holds the largest exchange rowid, hook id and effect id of any written entry, raised
  before the entry is written. When the cache opens, before any read, three `MAX()` reads
  (primary keys, O(1)) are compared with it; any lower means a restored older store, and the
  build folder is deleted with one warning. A crash between the raise and the write leaves
  the mark high, which is safe.
- **D21** (2026-11-24) The executable is hashed before the UI serves, not in the background:
  until the build identity is known every read would miss, which is the whole start-up.
  ~30 MB of sha256 is tens of ms, well inside AC13's 1 s.
- **D22** (2026-11-24) One gateway per data folder is assumed (nothing enforces it). To stay
  correct if two run, the build id is in both the path and every header, and the start-up
  clean-up deletes only other build folders and temp files untouched for 10 minutes.
- **D23** (2026-11-24) `unassigned_max` is the global `MAX(id)` of `effects`, not of unassigned
  `net_conn` effects only: any connection stored later has a larger id either way, and the
  global maximum is an O(1) primary-key read. The check stays `id > unassigned_max AND
  session_key IS NULL AND kind = 'net_conn' AND at BETWEEN from AND to`.
- **D24** (2026-11-24) The memo's disk lookup sits inside the compute closure only, not also
  before the join: a request after a restart costs one goroutine hop more, and a request
  that joins an early compute still gets the disk hit. `latest` keeps serving an older
  in-memory memo while that compute (usually a disk hit) runs, as before 030; disk entries
  are only ever read at the exact key.
- **D25** (2026-11-24) A blob file missing from disk (`os.ErrNotExist`, unlike a content row
  that is not stored, `store.ErrNotFound`) counts as a content-read error, so a view or memo
  that met one is served but never cached. Missing-blob handling is a non-goal; this only
  keeps such a result from being frozen. The copy has none: all 70 views were written.
- **D26** (2026-11-24) The background pass waits between sessions until the writer queue is
  under half full (`WaitRoom`) instead of a blocking enqueue: its writes come from the
  compute and build goroutines, not from the pass itself. A cache-folder with entries but no
  high-water file is treated as damaged and deleted with a warning; an empty one is not.
  The end of the pass is visible in `gateway_ui_cache_rebuild_seconds`; no log line.
- **D27** (2026-11-24) With an empty cache (a first start after an upgrade), page 2 of the copy,
  requested while page 1 and the background pass build, still timed out at 5 s in the smoke
  test: its 20 memos each start their own compute next to the pass's. This is D17's flip
  case; step 5 measures it on the copy and the 3x store and decides.
- **D28** (2026-11-24) Page-1 error (AC12): the error box already carries Retry on every page;
  what page 1 lacked was the pager, hidden because an unknown total made it a "single"
  page. On an error the pager now always shows, and unfiltered with an unknown total its
  Next stays enabled (the app corrects a page past the last to the last), so a failing
  page 1 is not a dead end. A filtered list keeps Next off on an error (where page 2 starts
  is unknown, 042 E4).
- **D29** (2026-11-24) AC13's 100 ms for pages is met on the copy (55-64 ms) but not on the
  3x store (109-211 ms after a restart). Every list request runs the session list query
  (a GROUP BY over all exchanges and hook events, 22 ms in the sqlite3 CLI on 3x against 7 ms
  on the copy, slower through modernc) plus the version and effect-count reads; warm, from
  memory, the same pages take 102-132 ms, and main's warm page 1 took 124 ms. So the floor
  grows with the store, not the cache. Cutting it means a session summary kept by the store
  or an in-memory session list kept in step with writes: a store change (non-goal) or a new
  invalidation path. Left for a later PR and reported to the human.
- **D30** (2026-11-24) D17's flip case happened: with an empty cache (first start after an
  upgrade, or after deleting the folder) page 2 of the copy, and pages 1, 2 and 5 of the 3x
  store, still time out at 5 s while the pass and other requests build; page 1 of the copy
  builds in about 3-4 s. It no longer waits behind a warm-up, and every later restart
  answers at once. Making a cold page never time out needs either the labelled stale
  fallback D17 rejected under the human's "never stale" rule or rows that show without
  their facts until built; both are the human's call, so it is reported rather than decided.
- **D31** (2026-11-24) The 1,440 px cases could not run in the extension's Chrome (the window
  stays at the screen's 2,560 px; the UI refuses to be framed), so all widths ran in a
  headless Chrome with a throwaway profile driven over the DevTools protocol on 127.0.0.1:9333,
  the fallback the brief allows; the extension tab covered the restart-to-page-2 check.

### Code review round 1 (step 6, fresh opus)

| # | Finding | Done |
|---|---|---|
| P1-1 | AC13 not met on 3x pages and with an empty cache; D30 undecided | Not a code fix: AC13 carries a dated status line and stays open for the human (D32) |
| P2-2 | `time.Local.String()` is "Local" when TZ is unset, so a time-zone change keeps the build id and old-zone times are served | Fixed: zone rules fingerprint in the build id (see Progress) |
| P2-3 | The high-water mark leaves out a view's `unassigned_max` (global effect max), so a restore that drops trailing unassigned connections is not detected | Fixed: the mark takes the view's `unassigned_max` too (see Progress) |
| P3-4 | Tests missing: effect landing between the effect-version read and the data reads; a joined compute getting the disk hit; mark raised before the entry; home and zone in the build id | Added (see Progress) |
| P4-5 | A memo hit skips decoding a later corrupt payload with the same hash | Accepted (D3): hash and payload are written in one transaction; a corrupt payload under a good hash is store damage, out of scope |
| P4-6..11 | Damage count can wait for the next damage; temp files in the build root not cleaned; no fsync; rebuild gauge set with no disk cache; `textMemoOff` package var; a flipped JS assertion | Accepted as they are; the JS flip is stated in its commit note (D28) |

- **D32** (2026-11-24) AC13 is reported as partly unmet instead of being reworded: the
  copy meets every number; on 3x the pages miss 100 ms by the list query's floor (D29), and an
  empty cache can still time out a page (D30). Both need a choice that is the human's
  (a store-side session summary, or stale or fact-less rows). The PR says so.

### Code review round 2 (step 6, fresh opus, last round)

No P1 or P2. Both round-1 fixes judged correct and complete; their five tests pass `-race -count=10`.

| # | Finding | Done |
|---|---|---|
| P3-1 | AC8 runs only when the cache opens: a restored store, then a run with the UI off, can push the maxima back above the mark with reused rowids/ids | Fixed: the high-water file records the identity of the row at each maximum and the open checks it (D33) |
| P4-1 | The zone fingerprint misses zones that differ only in transition hours (Asia/Beirut vs Europe/Sofia) | Fixed: `$TZ` and the sha256 of `/etc/localtime` join the build id |
| P4-2 | The joining-request test relied on a sleep to join | Fixed: it waits until the second caller has joined |
| P4-3 | The `computeMemoFn` restore could race background computes at test cleanup | Fixed: restore registered first, so it runs last |

- **D33** (2026-11-24) Store going back, closing D20's gap: besides the largest exchange rowid,
  hook id and effect id, the high-water file keeps the identity of the row at each (its
  `request_id`, `received_at`, `at`). At open, each recorded row must still exist with the
  same identity, else the build folder is dropped. A store restored and then written to with
  the cache never opened reuses those ids for other rows, and is caught. An old-format mark
  is dropped like damage.
