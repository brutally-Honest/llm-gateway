---
feature: 044-netproxy-shutdown-race
---
# Research & decisions — 044-netproxy-shutdown-race

## Progress
- 2026-11-25 Branch made from main at 80b5563; spec written.
- 2026-11-25 Fix and test committed in 6f63ea1; make verify green.
- 2026-11-25 Review round 1 (fresh opus, diff at 807c394): no P1; one P2, two P3s, P4 nits (see Review round 1). P2, the test P3 and the P4s fixed in 9439f1e. No second round (no P1).
- 2026-11-25 Finished: spec marked done; make verify green.

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
- **D4** 2026-11-25 Correction to D3: the count did not hold. `pipe()`'s closed branch
  closed the conns before recording, so the caller could close the recorder first and
  the effect was dropped (about 0.45% of `-race` runs). Fixed in 9439f1e by recording
  before cutting, as the sweep does. The assertion is now `early >= accepted`: a CONNECT
  swept while still dialing is recorded closed early but cut before its 200.
- **D5** The SOCKS handshake and dial path that Shutdown neither waits for nor cuts
  (review P3, present on main) is left out: the spec's non-goals exclude changing how
  long Shutdown waits. It is a candidate follow-up.

## Evidence
- Before the fix: `go test -race -count=200 -run '^TestNetProxy_ShutdownRacesNewTunnels$'
  ./internal/netproxy` failed on the first iteration with `WARNING: DATA RACE`: write in
  `Shutdown.func1` (`http.go:338`, `wg.Wait`) against a read in `pipe()` (`http.go:191`,
  `wg.Add`) reached from `socks()` (`socks5.go:123`).
- After the fix: the same command `ok` (1.3 s);
  `go test -race -count=200 -run '^TestRun_OwnListenerTrafficNotRecorded$' ./cmd/gateway`
  `ok` (13.1 s); `make verify` green. All runs under
  `systemd-run --user --scope -p MemoryMax=4G`.

## Review round 1
Fresh opus reviewer, `git diff main...HEAD` at 807c394.
- No P1. The race is closed on every path: the only `wg.Add` (in `pipe()`) and `wg.Wait`
  (in `Shutdown`) share `p.mu` ordering via `closed`; HTTP CONNECT and SOCKS5 both reach
  `pipe()`; the plain http:// path never touches `wg`. No tunnel is unrecorded, recorded
  twice or leaked; Shutdown still waits for joined tunnels until ctx ends.
- P2 test flake, closed branch cut before record: fixed in 9439f1e (D4).
- P3 test could fail the other way (CONNECT swept while dialing): fixed in 9439f1e (D4).
- P3 SOCKS handshake/dial not waited for or cut at shutdown (on main): left open (D5).
- P4 `fmt.Sscan`/ignored errors: fixed in 9439f1e. P4 no `opened > 0` assertion: counts
  logged instead, since asserting it would flake. P4 `len(outs) > n` cannot catch a
  double record alone: `finish` runs once per tunnel via `sync.Once`; left as is.
- After 9439f1e: the new test passed 1000/1000 under `-race`, still reported the race
  against main's `pipe()` via `-overlay`; `TestRun_OwnListenerTrafficNotRecorded`
  `-race -count=200` ok; `make verify` green.
