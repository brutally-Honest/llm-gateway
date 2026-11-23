---
feature: 041-flakes-and-quick-speed
---
# Research & decisions — 041-flakes-and-quick-speed

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
