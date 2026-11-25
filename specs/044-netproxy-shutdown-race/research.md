---
feature: 044-netproxy-shutdown-race
---
# Research & decisions — 044-netproxy-shutdown-race

## Progress
- 2026-11-25 Branch made from main at 80b5563; spec written.
- 2026-11-25 Fix and test committed in 6f63ea1; make verify green.

## Findings
- `grep -n 'wg\.' internal/netproxy/*.go`: the only `wg.Add` is in `pipe()`
  (`http.go:191`); the only `wg.Wait` is in `Shutdown()` (`http.go:338`).
- `socks5.go:123` calls `p.pipe(t, c, nil, up)`, so the SOCKS5 path shares the fix.

## Decisions
- **D1** Fix the single `wg.Add` in `pipe()` rather than adding a guard in each caller:
  both HTTP CONNECT and SOCKS5 funnel through it, so one place holds the rule.
- **D2** The test drives real listeners (HTTP CONNECT and SOCKS5, 48 tunnels released
  together) while `Shutdown` runs with a 20 ms ctx, instead of calling `pipe()` directly
  from an in-package test: it exercises the real Add/Wait interleaving and reproduced
  the race on the first iteration of `-race -count=200`.
- **D3** The test asserts closed-early effects equal accepted tunnels. A tunnel still
  dialing when the sweep runs is recorded closed early by the sweep (`finish` runs once
  per tunnel); its conns are cut later by `pipe()`'s closed branch, so the count holds.

## Evidence
- Before the fix: `go test -race -count=200 -run '^TestNetProxy_ShutdownRacesNewTunnels$'
  ./internal/netproxy` failed on the first iteration with `WARNING: DATA RACE`: write in
  `Shutdown.func1` (`http.go:338`, `wg.Wait`) against a read in `pipe()` (`http.go:191`,
  `wg.Add`) reached from `socks()` (`socks5.go:123`).
- After the fix: the same command `ok` (1.3 s);
  `go test -race -count=200 -run '^TestRun_OwnListenerTrafficNotRecorded$' ./cmd/gateway`
  `ok` (13.1 s); `make verify` green. All runs under
  `systemd-run --user --scope -p MemoryMax=4G`.
