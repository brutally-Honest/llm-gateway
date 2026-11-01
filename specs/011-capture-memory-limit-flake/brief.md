---
feature: 011-capture-memory-limit-flake
kind: fix
mode: quick
status: approved
version: 1
created: 2026-11-01
branch: fix/011-capture-memory-limit-flake
approved: 2026-11-01T09:03:24Z
approved-by: human
sha256: 41859a9ea8e0353ad96f3989f3a33e7632e827cfa241529af908dc26c6643943
fingerprint: f4f9c91f90113c5250c904a181aa5d4cb956d7bf6201313b6f7b9df3d1149614
---
# Capture memory limit flake

## Change
`TestCapture_MemoryLimitDrops/crosses_limit_mid_stream` (internal/core/capture_test.go:147) flakes under full-suite load with `Peak = 0`.
Repro: when the gateway's copy loop falls behind, the five 20-byte SSE events coalesce into one upstream Read, so the tee's only
TryReserve asks for 100 bytes against a 50-byte budget and nothing is reserved. Expected: the test always sees a reservation
below the limit before the crossing; actual: it depends on read timing. The tee is correct; the test must force the first event through alone.

## Acceptance
- **AC1** — When the upstream sends all five events in a single Write, the old `crosses_limit_mid_stream` shall fail with `Peak = 0` on every run (cause confirmed before the fix; that temporary change is not committed).
- **AC2** — When the upstream sends event 0, flushes and blocks until the test has read exactly 20 bytes of the response with `io.ReadFull` and released it (closing the release channel through a `sync.Once` shared with `t.Cleanup`, so a second close can't panic), `crosses_limit_mid_stream` shall read the rest, assert first+rest equals `sent`, assert `20 <= budget.Peak() <= 50`, and pass.
- **AC3** — When run as `go test -race -count=200 -run 'TestCapture_MemoryLimitDrops/crosses_limit_mid_stream' ./internal/core`, the fixed test shall pass every run, and `go test -race -count=20 ./internal/core` shall pass for the whole package; its other assertions (whole stream to client, dropped_memory, no sink exchange, InUse restored, Dropped = 1) stay unchanged.

## Out of scope
- No production code: the tee, Budget and copy loop stay as they are.
- `streamEvents` and its other caller (capture_test.go:314) stay unchanged.

## Approach
Only `internal/core/capture_test.go`. Add a gated upstream handler (send event 0, flush, wait on a `release` channel, then send events 1-4)
used only by `crosses_limit_mid_stream`; the test reads exactly 20 bytes with `io.ReadFull`, closes `release`, reads the rest and checks
the concatenation against `sent`. Release the channel in `t.Cleanup` too, so a failure never hangs the upstream.

## Steps
- **S1** — Confirm the cause: temporarily make `streamEvents` write all five events in one Write, run the old test with `-count=20` and record that every run fails with `Peak = 0`; revert — Tests: TestCapture_MemoryLimitDrops/crosses_limit_mid_stream (expected red, not committed)
- **S2** — Gate the upstream; close the release channel only through a `sync.Once` used by both the test body and `t.Cleanup`; read the first 20 bytes with `io.ReadFull`, release, read the rest, assert first+rest equals `sent` (the whole-stream check stays); assert `budget.Peak() >= 20` and `budget.Peak() <= 50` — Tests: TestCapture_MemoryLimitDrops/crosses_limit_mid_stream, run `go test -race -count=200 -run 'TestCapture_MemoryLimitDrops/crosses_limit_mid_stream' ./internal/core`, then the package with `go test -race -count=20 ./internal/core`

## Changelog
- v1 (2026-11-01) — first draft
