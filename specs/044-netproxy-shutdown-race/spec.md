---
feature: 044-netproxy-shutdown-race
kind: fix
created: 2026-11-25
branch: fix/044-netproxy-shutdown-race
status: done
---

## Problem
main's CI run 45434990436 (80b5563) failed on Ubuntu under `-race` in
`cmd/gateway` `TestRun_OwnListenerTrafficNotRecorded`. In `internal/netproxy/http.go`,
`pipe()` calls `p.wg.Add(1)` even when `p.closed` is already true, while `Shutdown()`
may already be in `p.wg.Wait()` from a zero counter. A positive `Add` racing a `Wait` on
a zero counter is documented `sync.WaitGroup` misuse. The code dates from ef34021
(spec 014). The SOCKS5 path reaches the same `pipe()`.

## Goal
No tunnel joins the WaitGroup after `Shutdown` has set `closed`. A tunnel opened during
shutdown is still recorded as closed early, as today.

## Non-goals
- Changing what is recorded for a tunnel, or how long `Shutdown` waits.
- Other packages' WaitGroups.

## Acceptance criteria
1. `pipe()` calls `p.wg.Add(1)` only when `closed` is false, under the lock that reads it.
2. A tunnel reaching `pipe()` after `closed` is set has its conns closed and is recorded
   once as `closed_early`, without touching the WaitGroup.
3. Every `wg.Add` in `internal/netproxy` follows the same rule (HTTP CONNECT and SOCKS5
   both go through `pipe()`).
4. A test in `internal/netproxy` that opens tunnels while `Shutdown` runs reports the race
   under `-race` on main's code and passes with the fix.
5. `go test -race -count=200 -run '^TestRun_OwnListenerTrafficNotRecorded$' ./cmd/gateway`
   passes; `make verify` is green.

## Edge cases
- A tunnel that joined before `closed` is set: `Shutdown` still waits for it until ctx
  ends, then cuts it and records it as closed early.
- A tunnel registered in `p.tunnels` but rejected at `pipe()`: recorded once, never twice
  (`finish` is idempotent per tunnel).
