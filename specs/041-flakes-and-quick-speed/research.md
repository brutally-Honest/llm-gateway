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

## Decisions
- **D1** (2026-11-23) Number 041 as the brief says; 035 was planned for the speed fixes and
  is folded in here (spec header), so no 035 folder is created.
- **D2** (2026-11-23) "Busy day" for S2 is measured two ways: since today's midnight (517
  calls, the real case) and since 2026-11-15 (≈3,000 calls, a stand-in heavier than the
  busiest day's 1,161), because `since` always runs to now and a past day alone cannot
  be asked for. The < 100 ms target is checked on both.
- **D3** (2026-11-23) `make verify` times are wall time with Go's test cache as it was;
  the cache state is recorded beside each, since it changes the time more than the code.
