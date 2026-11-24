---
feature: 030-persistent-view-cache
kind: feat
status: done
created: 2026-11-24
branch: feat/030-persistent-view-cache
---

## Problem
After every restart the UI port is bound but answers nothing until a warm-up of the
sessions list's first page ends (`cmd/gateway/run.go`, `ui.Warm`). Only page 1 is warmed,
so page 2, filters and search stay cold and hit the 5 s store timeout; opening a large
session cold takes seconds, and a live update rebuilds the whole session. On a copy of the
real store (70 sessions, 3,919 calls, 1.66M events), measured in `research.md`: page 1
right after a restart 5.5 s, page 2 a 504 even warm, three of five cold filters a 504,
the largest session (667 calls) 8.2-9.3 s cold, a live update 4.1-4.6 s. Most of the time
is Go decoding JSON and reading text of history that every call resends (469k events, only
2,686 distinct content hashes, in the largest session). Two leftovers: `placeInSession`
compares only the `buildKey` prefix (041 D15), and the error on the first page-1 load has
no Retry button or pager (042 covered only later pages).

## Goal
The UI answers at once after a restart, on every sessions-list page; opening a session
page, filtering and searching are fast on the real store, from a disposable on-disk copy of
per-session results that can always be rebuilt from the store.

## Non-goals
- Changing what the store holds (the data-model change needs an ADR, after v1).
- Retention, and handling of missing blobs.
- Making live updates incremental: that is 040 (D4).
- Persisting the timeline builds behind `/timeline`, `/tools/{tid}` and `/calls/{eid}`:
  those drawers stay cold after a restart (measured in step 5, D19).
- A config key for the cache (D9).

## Acceptance criteria
- **AC1** (skip) A session build decodes each message's text at most once per role per
  build: a message's text is memoised by its content hash, which covers its role and every
  block. Content-hash strings are interned per pass so history comparison is cheap. The
  `/view`, `/timeline` and list-row JSON of every seeded session is byte-identical with
  and without the memo (Go test), and was identical for all 70 sessions of the store copy.
- **AC2** (where) The cache lives in `<capture.dir>/view-cache/`, by default under
  `~/.local/share/llm-gateway`, which `gateway uninstall --purge` removes. Folders are
  0700, files 0600.
- **AC3** (what) For every session, two entries: its memo (stats, digest, list-row facts),
  and its view as `/view` serves it.
- **AC4** (key) An entry is used only when every part of its key equals the current one:
  the session's identity, its store version, its effect version, the build identity (cache
  format version, gateway version, a hash of the running executable read through
  `/proc/self/exe`, and what else shapes a view: the attribution skew, the home folder and
  the local time zone's name) and, for a view, its active flag at the current time and no
  unassigned connection stored since it was built inside its time range (D15). Anything
  else is a miss; an entry is never served stale, not even labelled as older (D17). A new
  build identity makes every old entry a miss; other build folders older than 10 minutes
  are deleted in the background.
- **AC5** (write) Entries are written off the request path by one writer goroutine, only
  when the session's store and effect versions read just before the write still equal the
  entry's key, and (for a view) the timeline build it came from was at the version read for
  the request, so an entry's content always equals the state its key names. A result built
  while any content read failed (other than not-found) is served but cached nowhere, in
  memory or on disk (D16). The view of an active session is not written to disk (D18).
  Capture never waits on the cache; a full queue drops a request-path write, while the
  background pass waits for room.
- **AC6** (damage) A missing file, or one whose key or active flag differs, is a plain
  miss. A file that cannot be read or parsed, fails its checksum, names an unknown format
  or another session is damaged: a miss, counted, deleted by the writer only if it is still
  the same file, and warned about at most once a minute with a count. The result is
  rebuilt. Deleting the whole folder while the gateway runs is safe: reads miss, the next
  writes recreate it.
- **AC7** (serve at once) The executable is hashed before the UI serves (tens of ms); the
  UI then serves as soon as it binds. Every list page, filter, search and view is served
  from memory, else from the cache on an exact hit, else built on demand; a compute also
  checks the cache before building, so a request that joined it early still gets a disk
  hit. A background pass, started after the UI serves, walks every session in list order
  and builds the entries that are missing or stale.
- **AC8** (store going back) A high-water file records the largest exchange rowid, hook id
  and effect id any written entry was built on; it is raised before the entry is written.
  When the cache opens, before any read, the store's maxima are compared with it: if any is
  lower, the store was replaced by an older copy, and the whole build folder is deleted
  with one warning.
- **AC9** (size) A view over 32 MiB of JSON is not cached. The view files of the build
  folder are kept under 1 GiB: past it the oldest-written are deleted. Memo files are not
  capped (about 2 KB each).
- **AC10** (metrics) `gateway_ui_warmup_seconds` is the time from start until the UI
  serves; `gateway_ui_cache_rebuild_seconds` is the last background pass's duration;
  `gateway_ui_view_cache_reads_total{kind,result}` counts reads (`memo`/`view`;
  `hit`, `miss`, `damaged`).
- **AC11** (041 D15) `placeInSession` uses a view only when its whole key, effect version
  included, equals the session's current one.
- **AC12** (page-1 error) When the first load of page 1 fails, the error box shows the same
  Retry button and pager as a later page's error (042).
- **AC13** (numbers, step 5) On the store copy after a restart: `/api/sessions` page 1,
  page 2 and page 5 under 100 ms each; `gateway_ui_warmup_seconds` under 1 s; cold filters
  and search under 300 ms; cold `/view` of the largest session under 500 ms. On a 3x
  store: the same pages under 100 ms and no 504 on any filter or search.
  Status 2026-11-24 (D29, D30, D32): met on the copy; on the 3x store no 504 on any filter or
  search, but pages take 109-211 ms after a restart (the list query's floor); and with an
  empty cache (first start after an upgrade) a page can still time out. Both are left open
  for the human, not reworded away.

## Edge cases
- First start after an upgrade or a format change: every entry misses; pages build on
  demand, without the listen-queue wait of the old warm-up, with the skip making them about
  twice as fast, and the background pass refills the cache. Page 1 of the copy builds in
  about 3 s; a page whose cold build passes 5 s can still time out (D17).
- A session changes while its entry is being built or written: the write is skipped
  (AC5) and the next read rebuilds.
- A session that was active at shutdown and is idle now: its view entry misses on the
  active flag and is rebuilt.
- Two builds of one session at once (request and background pass): they share the one
  compute the in-memory caches already coalesce; the writer may write twice, each write
  checked by AC5.
- A session in use when the gateway restarts gets a call: its entries miss on key, and its
  first list or view load builds it (about 2 s for the largest session).
- An unassigned connection (no session) is stored when it closes, with its start time, so
  it can land inside an ended session's range later: that view misses (D15).
- Two gateways on one data folder (unsupported): build folders are told apart by name and
  header; only folders idle for 10 minutes are deleted.
- The executable cannot be read for its hash: the persistent cache is off for that run,
  with one warning; the UI works as before from memory.
- A `capture.dir` outside the data folder: the cache follows the store, so `--purge`
  removes neither, as before.
