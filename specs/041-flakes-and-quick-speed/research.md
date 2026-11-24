---
feature: 041-flakes-and-quick-speed
---
# Research & decisions — 041-flakes-and-quick-speed

## Progress
- 2026-11-24 Review round 1 matched to commits: P1 and both F3 P3s in e747de2, the S2 P3 in
  583fa60, the timeline S4 P3 uncommitted. The P4 S1 row said "fixed" but was not (D15).
- 2026-11-24 Timeline S4 P3 checked: the test fails without the change (canceled and
  wrapped deadline log at warn) and passes 20/20 with it; committed.
- 2026-11-24 Review round 2 (fresh opus, diff at c99a95b): no P1 to P3; three P4s left
  open (see Review round 2). This was the last review round.
- 2026-11-24 F1 contract check: 009 AC23 and AC24 allow the live resend (see F1 contract).
- 2026-11-24 Finished: spec marked done; `make verify` green twice (5.9 s each, Go test
  cache warm); no throwaway gateway on 17195-17199; `~/.cache/llm-gateway-041` deleted.

## Measurement setup

- Store copy: `sqlite3 ~/.local/share/llm-gateway/gateway.db ".backup ~/.cache/llm-gateway-041/gateway.db"`
  and `cp -a` of `blobs/`, taken 2026-11-23 19:45 +05:30. 3,853 exchanges, 1,653,093
  events, 1,589 MB database, 4.0 GB of blobs. 521 calls on the day measured (2026-11-23);
  the busiest day in the store is 2026-11-15 with 1,161.
- Throwaway gateway: built from the commit under test, started with
  `systemd-run --user --scope -p MemoryMax=6G <bin> -config <scratch>/gw.yaml`, with
  `capture.dir` on the copy, the proxy on 127.0.0.1:17197, the UI on 127.0.0.1:17199,
  effects network listeners off, and a fake Anthropic upstream (a Python SSE server) on
  127.0.0.1:17195. The running `llm-gateway.service` was never touched.
- "Largest call": `SLTZOTBK3XZDAGQ4F4CK2CYPLE`, 1,648 events, in a session of 543 calls.
- Burst count: headless Chrome opens the UI with `--log-net-log`, 20 captured calls go
  through the gateway in one Claude Code session (`claude-cli/` user agent and
  `x-claude-code-session-id`), then the netlog's distinct `/api/today` URL requests
  are counted, from page load to 8 s after the burst.

## Baseline (main, eca58ef)

| What | Command | Result |
|---|---|---|
| S1: first `GET /api/exchanges/{largest}` after a restart | `curl -w %{time_total} http://127.0.0.1:17199/api/exchanges/SLTZOTBK3XZDAGQ4F4CK2CYPLE`, first request after start | 2.82 s, 2.75 s, 2.71 s (three restarts); 9 ms once cached |
| S2: `/api/today?since=<today 00:00>` (517 calls), cold | first request after start | 0.214 s, 0.214 s |
| S2: same, warm | repeated | 0.200–0.205 s |
| S2: `/api/today?since=2026-11-15T00:00+05:30` (≈3,000 calls, more than any one day), cold | first after start | 2.88 s, 3.42 s |
| S2: same, warm | repeated | 2.86 s |
| S2: `/api/today` requests during 20 calls 1 s apart | `burst-count.sh base-gap1 1` | 22 (page load, then one per call, ≈1 s apart) |
| S2: same, 20 calls 0.2 s apart | `burst-count.sh base-gap0.2 0.2` | 3 |
| `make verify` | `systemd-run --user --scope -p MemoryMax=6G make verify` | 52.5 s, green; 15 of 22 Go packages from the test cache |

Index sizes on the copy (`SELECT name, SUM(pgsize) FROM dbstat GROUP BY name`):
events 1,068 MB, events_content 200 MB, content 103 MB, **events_tool_call 99 MB**,
**events_principal 96 MB**, exchanges 16 MB. The three `events` indexes together are
395 MB; the two this spec drops are 195 MB.

### Query plans (S3)

Every SQL string in `internal/store`'s non-test files was extracted with a go/types
constant-folding script (43 constants, including `exchanges(...)` tails and the version
`WITH` tails), plus 8 queries built at run time written out by hand (`SessionRows`,
`SessionPage`, `SessionPageAt`, `SessionCount`, the one-session lookups,
`UngroupedPage`, `UngroupedExchanges`). `EXPLAIN QUERY PLAN` ran on each against the copy
with every parameter NULL.

Result: **47 queries, 0 plans use `events_tool_call` or `events_principal`.** The only
read of `events` is `Reader.Events` (`internal/store/reader.go:284`):
`SEARCH events USING PRIMARY KEY (request_id=?)`. Outside `internal/store` only
`internal/ui/api_timeline_test.go` names the events table.

## F1: the stream replay flake

Evidence: `scratchpad/f1/event_order.md` and `instr_fail.txt` (session scratchpad, not kept).

Event order of the failure:
1. The test saves c01 and calls `hub.Publish("c01")`, which only queues the commit.
2. The test opens the session stream; it subscribes, then the snapshot sends c01 and the
   hooks at position Row=1.
3. The dispatcher handles c01 late. The stream now watches the session, so the hub reads
   and sends c01. The test had already saved c02, so the read is stale (store at ExCount 2,
   cached build at 1) and c01 goes to catch-up.
4. Catch-up rebuilds at ExCount 2 (Row=2, not behind the snapshot) and sends a live batch:
   `entries` for c01, then `exchange` events. The test, in round i=2, wanted only
   `exchange` events before c02's `entries`, and failed at `stream_test.go:1153`.

The stream is right by the 009 contract. AC24: "The snapshot is taken after the stream
is subscribed, so no exchange committed around the open is missed; one sent both in the
snapshot and live is replaced, never duplicated." E18: "an exchange already in the
timeline answer arrives again in the snapshot and replaces itself". The test's
expectation was wrong. The brief's suspicion was close: the catch-up only sends c01
because the hub dispatched c01's commit after the stream subscribed. Without c02 saved
first, c01 arrives as a plain live `entries` event, which fails the same way.

Fix (test only): the loop accepts exactly one `entries` event whose entries all belong to
c01, in any round. With a wider delay, c01 can land after c02's commit; a first version
that allowed it only in round 2 failed 27/100. A second c01 event, or an `entries` event
for any other exchange, still fails. "Resends at most the previous call" and
`total <= 19` are unchanged.

Rates (`ui.test` built with `go test -race -c`, each run inside
`systemd-run --user --scope -p MemoryMax=6G`):

| Code | Load | Command | Failures |
|---|---|---|---|
| main test, main product code | none, GOMAXPROCS=1 | `ui.test -test.run '^TestStream_ReplayOf20CallsResendsAtMostThePreviousCall$' -test.count=500 -test.v`, stopped at 51 runs | 0/51 |
| main test, 60 ms sleep at the top of `Hub.dispatch` (temporary) | GOMAXPROCS=1 | 5 shards × 4 `-test.count=1` | 20/20 |
| new test, same temporary sleep | GOMAXPROCS=1 | 3 rounds × 10 shards × `-test.count=10` | 0/300 |
| new test, product code unchanged | 10 busy loops, GOMAXPROCS=1 | 3 rounds × 10 shards × `-test.count=10` | 0/100, 0/100, 0/100 |
| new test, product code unchanged | 10 busy loops, GOMAXPROCS=4 | same | 0/100, 0/100, 0/100 |

The race was never lost on Linux without the sleep, so "before" means with the sleep:
20/20 before, 0/300 after. The sleep and the trace prints were removed;
`git diff --stat -- internal` showed only `internal/ui/stream_test.go`.

### F1 contract (2026-11-24 check of 75a8ca5)

75a8ca5 changed the test's expectation, so the spec 009 contract it relies on is quoted in
full here (`specs/009-web-ui/spec.md`):

> **AC24** — […] The snapshot is taken after the stream is subscribed, so no exchange
> committed around the open is missed; one sent both in the snapshot and live is replaced,
> never duplicated.

The live resend arrives as an `entries` event, not an `exchange` event. AC23 makes the two
equivalent:

> **AC23** — When `GET /api/sessions/{id}/stream` is open and the session gains a committed
> exchange, the system shall send one SSE event `entries` carrying that one exchange's full
> timeline entry list […]. The page applies it like an `exchange` event: it replaces every
> entry it holds for that exchange id.

The changelog for v2 adds: "The page applies exchange events as replace-by-exchange-id
and never ignores an id it already holds." So c01, sent in the snapshot and then once live
as `entries`, is allowed: the page replaces it and does not show it twice. The test still
fails on a second c01 event or on an `entries` event for any other exchange.

## F2: the rate-limit wall clock

`ratelimit.Middleware` took `time.Now`, and the `cmd/gateway` run tests start a real
gateway, so a `Record` and the next `Admit` could fall in different minute windows. Now
`Middleware(l, a, log, opts ...Option)` takes `WithClock(f)`. `cmd/gateway`'s `deps` has a
`rateClock` field, which `serve` passes on only when it is set; `main` never sets it. Every
gateway-starting rate-limit run test goes through `rlGateway`, which fixes the clock at
2001-02-03 04:05:30 UTC, mid-window.

The new `TestRun_RateLimitFixedClockAtMinuteBoundary` fixes the clock at 04:05:59.999. One
call over the token limit gets 200, the next gets 429, and `X-Gateway-Ratelimit-Reset`
must be `2001-02-03T04:06:00Z`. That reset value is only possible when the injected
clock reaches the limiter.

Wall-clock proof. `scratchpad/f2/boundary.py` launches `TestRun_TokensRecordedFromStream`
80 to 102 ms before a real minute boundary, in 3 ms steps, 6 launches a minute for 8
minutes. The first token Record comes about 80 ms after launch.

| Binary | Launches | Failures |
|---|---|---|
| main (`go test -race -c`, built before any edit) | 48 | 1 (82 ms before: `run_ratelimit_test.go:212: upstream saw 2 calls, want 1`) |
| this branch | 48 | 0 |

An earlier scan of main with other offsets caught one more failure, at 91 ms. The window
that fails is the gap between Record and the next Admit, about 1 to 10 ms, so the race is
rare even when aimed at.

## S1: first call details after a restart

`placeInSession` now reads `ViewCache.peek`, a new non-blocking read of the session's
last cached view. It never calls `latest`, `get` or `cachedView`, so it never starts or
waits for a build. The title and the #n are given only when that view was built at the
session's current build version (`buildKey`, the prefix of the view key, now shared with
`buildView`), which costs one grouped version query. Otherwise both are left out. Both
can change as a session grows:
- `readEntries` numbers calls in start order, so a call that starts earlier but commits
  later shifts every number after it;
- a generated title, or a late call that comes first, changes the title.

So an older view, even one that holds the call, could give a wrong value.

UI with the fields missing (no JS change needed). The call dialog reads
`/api/sessions/{id}/calls/{eid}`, not this endpoint, and is unaffected. The full exchange
page shows "Exchange" for "Call #n", the breadcrumb `Sessions › Session` (still linked),
and "this session" as the session link. These are accurate generic fallbacks, so step 7
is not run.

| Measure (gateway built at this commit, store copy) | Before | After |
|---|---|---|
| first `GET /api/exchanges/SLTZOTBK3XZDAGQ4F4CK2CYPLE` after a restart | 2.82, 2.75, 2.71 s | 0.0053, 0.0068, 0.0052 s (no title or #n) |
| the same after `GET /api/sessions/{id}/view` (2.84 s build) | 9 ms | 8 ms, title "Pending and completed tasks", #535 |

Tests changed:
- `TestExchange_SessionTitleCallNumberAndStatusWord` now warms the view first.
- `TestAPI_ExchangeDetailAnswersFromCachedBuildWithoutWaiting` used to require the detail
  of a call newer than the cached build to wait for the gated build. It now requires that
  call to answer within 1 s with no `call_n`.

Both behaviours are changed on purpose here. New tests:
- `TestExchange_NothingCachedAnswersWithoutBuilding`
- `TestExchange_CachedCurrentViewGivesTitleAndNumber`
- `TestExchange_OlderCachedViewGivesNoTitleOrNumber`

## S2: today's totals

Where the time went (temporary timing test on the copy, read-only; today = 561 calls,
since 11-15 = 2,217):

| Part | today | since 11-15 |
|---|---|---|
| `ExchangesSince` (full rows) | 22-30 ms | ≈100 ms |
| `SessionStats` (every event of every call) | ≈195 ms | 3.15-3.35 s |
| `HookSessionsSince`, `EffectTotalsSince`, `hourlyBuckets` | ≈2 ms each | ≈3 ms each |

`SessionStats` reads about 420 events per call, mostly message and tool_call rows, but
the totals need only the `request` and `usage` events.

Fix:
- `CallMemo` holds each parsed call's `timeline.CallUsage` by request ID, capped at
  50,000 entries and reset when full.
- `ExchangeStubsSince` lists the calls without headers or bodies. It searches
  `exchanges_started_at`, then sorts the request_id tie-break in a temp B-tree.
- A memo miss reads only the request and usage events (`CallEvents`, events primary key).
- Totals are summed again for each request from the stubs.

Why the memo is safe:
- `SaveParse` sets `parse` and inserts the events in one transaction, and a call's events
  are inserted once (primary key `request_id, seq`). So a call with `parse` set never
  changes, and only those are memoised. Unparsed calls are read again on every request.
- Price tables are static for the process.
- A new `since` lists its own stubs, so another day's totals are never served.
- `TestToday_MemoisedEqualsReference` compares the response bytes with the old
  computation, kept as a test-only reference, over a wide fixture and many `since`
  values. It runs on a cold memo, a warm one, and after a new call, a late parse and a
  new hook.

UI: `scheduleToday` fetches at once when no fetch ran in the last 5 s, otherwise when the
5 s are up. Fetches never overlap, and the last event of a burst is always followed by a
fetch that starts after it. Four node tests cover this; they fail on the old app.js.

| Measure (store copy, `curl -w %{time_total}`) | Before (main) | After |
|---|---|---|
| since today, cold first request after a restart | 0.214-0.24 s | 0.075-0.077 s |
| since today, repeated | 0.20-0.24 s | 0.011-0.012 s |
| since today, first request after a new call (`burst.sh 1 0`) | 0.235-0.245 s | 0.012-0.013 s |
| since 11-15, cold first request after a restart | 2.88-3.42 s | 0.49-0.57 s |
| since 11-15, repeated | 2.86-3.3 s | 0.023 s |
| since 11-15, first request after a new call | 3.1-3.3 s | 0.022 s |
| `/api/today` requests, 20 calls 1 s apart (`burst-count.sh … 1`) | 22 | 8: 3 at page load, then 10.2, 15.2, 20.2, 25.2, 30.2 s (last event ≈28 s) |
| same, 20 calls 0.2 s apart | 3 | 5: page load at 0, 0.15 and 5.1 s, then 10.1 and 15.1 s (burst 8-12.2 s) |

All are under 100 ms except the cold first request after a restart for since 11-15, a
range wider than any one day. That request fills the memo once (about 0.25 s of SQL for
2,200 calls).

## F3: CI output that keeps failures

CI runs the Go tests through `.github/scripts/go-test-ci.sh`. It writes
`go test -race -json` to `$RUNNER_TEMP/go-test.json`, then prints with jq (preinstalled on
both runners):
- go's own one-line summary for each package;
- for every failed test, and for every failed package with output of its own (a panic,
  a timeout, a build error), a `===== FAIL <pkg> <Test> =====` block holding every output
  line, each with its event timestamp.

It exits with go test's code. On Linux, `make verify` is split into `make lint`, the
script and `make web-test`; macOS runs the script and `make web-test`. Job and step names
are unchanged, and `make test` locally is unchanged.

Checks:
- A demo package (scratchpad `f3demo`, outside the repo) with a failing test, a failing
  subtest, a panic and a build error printed each in full with timestamps, exit 1.
- The whole repo through the script: exit 0, 22 lines for 22 packages, the same count as
  plain `go test`.
- shellcheck is clean. actionlint is not installed, so it was not run. The macOS path
  (bash 3.2, BSD tools) was checked by reading only; CI on this PR is its first real run.

## S3 and S4

- S3: migration step 6 runs `DROP INDEX IF EXISTS events_tool_call` and
  `DROP INDEX IF EXISTS events_principal`; no earlier step is edited and there is no
  VACUUM. `TestStore_MigratesFromEmpty` now checks both are absent.
  `TestOpen_MigratesStoreFromVersion5DroppingUnusedIndexes` builds a store with steps
  1-5 and `user_version = 5` (as main leaves it), with an event row carrying
  `tool_call_id`. Open must reach version 6 with both indexes gone, `events_content`
  kept and the row readable. When the step was replaced by `SELECT 1`, both tests failed.
- S4: `DigestCache.run` logs `ui digest compute failed` at debug when the error is
  `context.Canceled` or `context.DeadlineExceeded` (wrapped or not), and at warn
  otherwise. `TestDigestCache_ComputeFailureLevelFollowsCause` covers canceled, wrapped
  deadline and another error. When the condition was forced false, the first two cases
  failed.

## After (branch at c7b0333, same store copy)

The copy gained about 50 calls from the burst runs between the two measurements.

| What | Before (main) | After |
|---|---|---|
| S1: first `GET /api/exchanges/SLTZOTBK3XZDAGQ4F4CK2CYPLE` after a restart | 2.82, 2.75, 2.71 s | 0.0074, 0.0051, 0.0061 s |
| S2: since today, cold first request after a restart | 0.214, 0.214 s | 0.074 s |
| S2: since today, warm | 0.200-0.205 s | 0.011-0.012 s |
| S2: since today, first request after a new call | 0.235-0.245 s | 0.013 s |
| S2: since 11-15, cold | 2.88, 3.42 s | 0.55 s |
| S2: since 11-15, warm | 2.86 s | 0.021-0.023 s |
| S2: since 11-15, first request after a new call | 3.1-3.3 s | 0.024 s |
| S2: `/api/today` requests, 20 calls 1 s apart | 22 | 8 (page load 0, 0.16, 5.2 s; then every 5 s to 30.2 s) |
| S2: the same, 0.2 s apart | 3 | 5 (0, 0.15, 5.2, 10.2, 15.2 s) |
| S3: plans using `events_tool_call` / `events_principal` | 0 of 47 | 0 of 49 (the two new `reader_today.go` queries included; the other 47 plans unchanged) |
| S4: warn lines in five restarts' logs | (no `digest compute failed` line in the baseline runs either) | 0 |
| `make verify` | 52.5 s, 15 of 22 packages cached | 58.8 s with the test cache cleared (`go clean -testcache`); 6.9 s fully cached |

The S4 warning did not show in these quick restarts on either build, because no digest
compute was running at shutdown. AC7 rests on
`TestDigestCache_ComputeFailureLevelFollowsCause`.

Database size on the copy (`stat -c %s` after `PRAGMA wal_checkpoint(TRUNCATE)`):

| State | Bytes | Note |
|---|---|---|
| before (user_version 5) | 1,589,620,736 | 388,091 pages, 0 free |
| after migration 6 (user_version 6) | 1,589,620,736 | 47,570 free pages (≈195 MB) are reused by new writes; the file does not shrink |
| after a manual `VACUUM` (14.9 s) | 1,304,141,824 | −285.5 MB (−18 %): the two indexes plus the defragmentation VACUUM does |

## Review (round 1, a fresh opus subagent on `git diff main...HEAD` at 2564c22)

Tests the reviewer ran: store, ratelimit and ui with -race, the cmd/gateway rate-limit
tests, F1 and the S2 equivalence test with -count=5, and the web tests (220/220). All
pass. Points 1-3, 5, 6 and 9 found nothing: the F1 allowance is exactly AC24/E18, no
test can reach the wall clock, no assertion is weakened, the memo is exact, no SQL needs
the dropped indexes, and no rules are broken. On a synthetic 1.65 M-row WAL database,
step 6 held the write lock for 0.66 s.

| # | Finding | Action |
|---|---|---|
| P1 | F3: a test timeout (or a crash while a test runs) prints only `FAIL <pkg>`. test2json gives the timeout panic to the running test, which never gets a terminal action, so neither block printed it. | fixed in e747de2 (`go-test-ci`: tests with `run` and no pass/fail/skip in a failed package) |
| P3 | F3: a jq failure exits before printing anything | fixed in e747de2 (raw JSON printed, non-zero exit) |
| P3 | F3: the jq program in a heredoc inside `$( … )` may mis-parse on bash 3.2 | fixed in e747de2 (moved to `go-test-ci.jq`, run with `jq -f`) |
| P3 | S2: the page-load `loadToday()` does not set `todayBusy`, so it can overlap the first throttled fetch, and a late answer can overwrite newer totals | fixed in 583fa60 (`loadTodayNow` counts as in flight; node test `thePageLoadFetchAndAnEventNeverOverlapAndTheNewestAnswerWins` fails on the old app.js) |
| P3 | S4: `ui timeline build failed` (`timeline_cache.go:257`) warns on a shutdown cancel too | fixed (same rule as the digest; `TestTimelineCache_BuildFailureLevelFollowsCause`) |
| P4 | S4/D14: `DeadlineExceeded` at debug hides a real build timeout; `c.base.Err() != nil` would catch shutdown only | kept as the brief asks (D14) |
| P4 | S1: the fallback title "No prompt · client · start" takes its start from a range that includes effects, which are outside `buildKey`, so an older effect version can show an old start time | 2026-11-24: **open**. The row first said "fixed (full view key compared)", but `placeInSession` still compares only the `buildKey` prefix (D15) |

## Review (round 2, a fresh opus subagent on `git diff main...HEAD` at c99a95b)

No P1, P2 or P3. The reviewer confirmed that the round 1 fixes (e747de2, 583fa60, b1c7a05) are correct.
To check that each test proves its claim, the reviewer broke the code on a `git archive` copy and ran the test:
- The old F1 test failed 4/4 with a 60 ms dispatch sleep; the new one passed 18/18 with that sleep and 18/18 with a late catch-up sleep.
- The S1 test failed with the version gate disabled.
- The S2 equivalence test failed when unparsed calls were memoised.
- The new 583fa60 node test failed on the old app.js.
- The timeline log test failed on main's code.
- The CI script printed the timeout, panic and build-error demos in full.
- The S3 migration steps 1-5 are byte-identical to main.

| # | Finding | Action |
|---|---|---|
| P4 | `go-test-ci.jq`: `$done` is keyed by package and test, so with a hand-passed `-count=N` a test that passes once and then hangs gets no "did not finish" block. CI never passes `-count`. | open (not reachable in CI) |
| P4 | `store_test.go:146-152`: the index-absence check treats any query error as "dropped". `migrate_041_test.go` uses `count(*)` and is sound. | open |
| P4 | AC5: since 11-15, the cold first request after a restart is 0.49-0.57 s, so D2 reads as if both ranges met 100 ms | recorded as D16 |

## Decisions
- **D1** (2026-11-23) Number 041 as the brief says; 035 was planned for the speed fixes and
  is folded in here (spec header), so no 035 folder is created.
- **D2** (2026-11-23) "Busy day" for S2 is measured two ways: since today's midnight (517
  calls, the real case) and since 2026-11-15 (≈3,000 calls, a stand-in heavier than the
  busiest day's 1,161), because `since` always runs to now and a past day alone cannot
  be asked for. The < 100 ms target is checked on both.
- **D3** (2026-11-23) `make verify` times are wall time with Go's test cache as it was;
  the cache state is recorded beside each, since it changes the time more than the code.
- **D4** (2026-11-23) The S3 migration is one-way like every step: `migrate` refuses a
  database whose `user_version` is above the binary's step count, so after the new step
  a main-built gateway will not open the store. No down-migration is added: the step only
  drops two indexes, and rolling back means `CREATE INDEX` of both and
  `PRAGMA user_version = 5` by hand. Spec E6 corrected to say so (it first claimed an older
  binary still opens the store, which is wrong).
- **D5** (2026-11-23) F1 is fixed in the test, not the stream: by 009 AC24 and E18, a
  snapshotted exchange may also arrive live and replaces itself. No product behaviour
  changes. No deterministic test of the race was added: it depends on when the
  dispatcher goroutine runs, and no existing seam forces that without a sleep. The proof
  uses a temporary sleep in `Hub.dispatch`, removed before the commit.
- **D6** (2026-11-23) F1 runs were sharded (10 processes × `-count=10`) instead of one
  `-count=500`: each run takes about 5 s, so 500 in one process take about 40 minutes.
- **D7** (2026-11-23) F2's clock is a variadic `ratelimit.Option` plus a test-only `deps`
  field, not `deps.now`, which is the crash-loop backoff clock (spec 025). The
  `ratelimit.Middleware(l, a, log)` call stays valid and the seam matches the other
  test-only `deps` fields.
- **D8** (2026-11-23) The boundary test cannot compile against main, which has no clock
  option, so "fails on main" is shown at the wall clock (1/48 on main, 0/48 after). The
  in-repo test shows the clock is honoured through the fixed reset header. The
  `internal/ratelimit` unit tests already used a fixed clock and are unchanged.
- **D9** (2026-11-23) S1's source is the view cache, not `TimelineCache.peek`: a peeked
  build still needs `sessionView`, which reads effects, content and hooks from the store.
  Thesis: serve any cached view's title and #n (more often filled in on live sessions).
  Antithesis: the #n and the title both move as a session grows, and the brief forbids a
  wrong title. Synthesis: both fields only from a view at the current build version; on
  a busy live session they are often missing until the session page rebuilds the view.
  It would flip if views carried a stable call number (one that is never renumbered).
  The first version served the title from any view; it was gated in a follow-up before
  the commit.
- **D10** (2026-11-23) S2 uses a per-call memo, not one aggregate query and not a
  whole-response cache. Thesis for SQL: no Go state. Against: cost needs JSON parsing,
  the model, the protocol's price book and the start time, which would duplicate
  `internal/cost` in SQL. Thesis for a response cache keyed by (since, hours, store
  version): a repeat costs one version query. Against: parse landings have no cheap global
  version (events has no rowid), and the first request after a new call would recompute
  everything. The memo makes that case cheap, and repeats are already 11-23 ms.
  Flips if pricing moves into the database, or the stub list stops being cheap.
- **D11** (2026-11-23) The throttle fires at once on the first event of a burst
  (leading), then at most every 5 s with a guaranteed trailing fetch. The old code waited
  500 ms after the last event, so a quiet single event now shows its totals sooner, and a
  burst costs one fetch per 5 s.
- **D12** (2026-11-23) S2 is two commits, the Go totals then the JS throttle, after one
  `make verify` of the whole tree. Neither part's tests depend on the other.
- **D13** (2026-11-23) F3 uses jq over `go test -json`, not gotestsum, which would be a
  new dependency, nor a Go formatter in the repo, which would be code to keep for CI
  alone. jq ships on both GitHub runners. The JSON file stays on the runner and is not
  uploaded: an upload step would add an action.
- **D14** (2026-11-23) S4 logs a `DeadlineExceeded` (a compute past `buildTimeout`) at
  debug too, as the brief says. A slow build that times out while the gateway is not
  shutting down is then quiet at info level; its waiters still get the error.
- **D15** (2026-11-24) The resumed session found the review's P4 S1 fix missing:
  `placeInSession` checks `strings.HasPrefix(v.Version, buildKey(...))`, not the full view
  key with the effect count and max id. The resume brief asks for P1 to P3 to be closed, so
  this P4 stays open and is reported to the human rather than fixed here. Its effect is
  limited to the start time in the "No prompt" fallback title when effects changed since
  the view was built; the #n is not affected.
- **D16** (2026-11-24) Correction to D2: AC5's 100 ms is met for since today (cold
  0.074 s, warm 0.011 s) and for since 11-15 once warm (0.021-0.023 s). The since-11-15
  cold first request after a restart, a range wider than any one day, takes 0.49-0.57 s
  (down from 2.88-3.42 s). Here the memo is filled once, for about 2,200 calls. This is
  accepted because AC5's real case is one day, and the 030 warm-up is out of scope here.
