# 047 research

## Progress
- 2026-11-26 spec and research created on fix/047-flake-and-banner
- 2026-11-26 F1 reproduced, cause found, fixed and committed (fix(ui))
- 2026-11-26 U11 dismissible banner built and committed (fix(ui))

## Decisions
- **D1** 2026-11-26 Dismissal stores `{since, lost}` from the answer shown when dismissed
  (sessionStorage, in-memory fallback). The banner stays hidden while `since` is the same and
  `lost` is not greater; it shows again when `lost` rises or `since` differs (a restart: its
  count restarts at 0, so any loss after it is new even if lower than the dismissed count).
  Keying on `lost` alone would hide a restart's first losses, breaking "never hide a new loss".
  `since` is already in /api/health (process start), so no server change.
- **D2** 2026-11-26 `docs/observability.md` says the banner "cannot be dismissed"; it is
  updated with U11. That is docs, not a change to /healthz, /api/health, metrics or doctor.
- **D3** 2026-11-26 F1 reproduced before any change, on Linux, in `systemd-run --user --scope -q
  -p MemoryMax=4G`, 12 cores, test binary built with `go test -c -race ./internal/ui`, run as
  `<bin> -test.run '^(TestSessionsPage_BadParams|TestAPI_AfterFromOtherListIs400)$'
  -test.count=200`. Failures per 200 runs: default GOMAXPROCS 6, 6, 6 (three rounds);
  GOMAXPROCS=4 1; GOMAXPROCS=4 with 8 `while :; do :; done` loops 1; GOMAXPROCS=1 0; GOMAXPROCS=1
  with 8 loops 0; default with 8 loops 0. So the heaviest variation that fails reliably is the
  plain default (full parallelism); busy loops did not raise the rate. All fail with "TempDir
  RemoveAll cleanup: directory not empty"; a probe that listed the folder after the failed
  RemoveAll found only `gateway.db-shm`, a SQLite shared-memory file created by a reader
  connection opened after the test's reader was closed. Logs are in the 047 scratchpad.
- **D4** 2026-11-26 F1 writer and cause. Not the disk cache (030): these tests run with no
  `Deps.Disk`. The writers are the goroutines the UI starts off the request and nobody waits
  for: the digest cache's `ui_build` runs and the `ui_queue_undecided` goroutine that
  `queueDeferred` starts after a list request (both read through the `store.Reader`), plus,
  by the same pattern, the timeline cache's `ui_build` and the `ui_view_job` runner. A counter
  around them showed one queue goroutine and two builds still in flight when the test
  ended. `sql.DB.Close` does not wait for a connection in use, so the test's `r.Close()` and
  `s.Close()` return while a build still holds a reader connection; SQLite then (re)creates
  `gateway.db-shm` after, or while, `t.TempDir`'s RemoveAll runs. The gateway's own shutdown
  had the same gap: `captureRun.close` cancelled nothing and waited for none of them before
  `reader.Close()` and `store.Close()` (the base context was cancelled by the signal, but
  nobody waited for the goroutines to return; on the `serve failed` path it was not cancelled
  at all). Owner of the writers: `internal/ui` (scope ui); the wait is called from
  `cmd/gateway` shutdown.
- **D5** 2026-11-26 F1 fix, in product code. `internal/ui` gets a `flight` counter (a
  WaitGroup with `Wait(ctx) bool`) embedded in `DigestCache`, `TimelineCache` and the view
  jobs (`ViewCache.Wait`), counting the `ui_build`, `ui_queue_undecided` and `ui_view_job`
  goroutines; `DiskCache.Wait(ctx)` waits for the writer, which already stops on its context
  and writes each file as temp then rename, so the write in hand ends whole and the rest of
  the queue is dropped, never torn. `cmd/gateway`: `startCaptureWith` runs the UI's background
  work on a child context it can cancel (`captureRun.stopBG`, also passed to the handler as
  `Deps.Ctx`), and `close` calls `stopUIWork` right after the hub closes and before the sink,
  store and reader: cancel, then wait for views, digests, timelines and the disk writer within
  the shutdown's own budget. Test helpers (`testHandler` in api_sessions_test.go, the disk
  env's restart) build the caches on `t.Context()` and register a cleanup after the reader and
  the store (so it runs first) that waits for them. Rejected: (a) fix only the test helpers,
  since the product has the same gap at shutdown and AC2 asks for the wait there; (b) sleep or
  retry the removal, forbidden and hides the race; (c) make `reader.Close` wait for in-use
  connections, a wider change to the store with no owner for the goroutines; (d) one `Close()`
  on `Deps`/`Handler`, which returns `http.Handler` and would change its API for every caller.
  Not covered, noted: `ui.Prefill` and the hub's goroutines also read through the reader;
  Prefill stops on the same cancelled context but is not waited for, and none of them
  reproduced here.
- **D6** 2026-11-26 F1 tests. `TestShutdown_WaitsForARunningBuildBeforeTheFolderGoes` holds a
  build past the cancel, requires `Wait` to report not-done while it runs, releases it, and
  requires its late write to exist, `Wait` to report done and the folder removable. Probe:
  with `flight.Wait` temporarily returning true at once the test failed ("Wait returned while a
  build was still running"); restored. `TestShutdown_DiskWriterStopsAfterWait` checks no write
  or temp file after the disk writer's `Wait`; its probe (Wait returning true at once) did not
  fail it, since the writer stops fast, so it guards the shape, not the wait; the first test is
  the proof for the wait itself.
- **D7** 2026-11-26 F1 after the fix, same commands and binary build: default GOMAXPROCS
  `-race -count=200`, 3 separate rounds: 0, 0, 0 failures (before: 6, 6, 6); GOMAXPROCS=4 with
  8 busy loops: 0 (before 1); one more round on the final build: 0. `make verify` passes
  (run under the same systemd-run scope). The first verify run failed
  `TestNoBareGoStatements` on the `go` statement in `flight.Wait`; it now uses `logging.Go`.
- **D8** 2026-11-26 U11 button: a real `<button type="button">` showing "×", with aria-label
  "dismiss capture health banner" and a title; styled like the existing `.health-link` (no box,
  `--bad`, bold, same in both styles) with the existing focus outline token. No new colour. The
  old DOM assertion "no close button" (045 D4) is replaced by one that requires this button,
  since U11 reverses that decision.
- **D9** 2026-11-26 U11 storage: the dismissal is written to sessionStorage (`llmgw.healthDismissed`)
  and to an in-memory copy; a read takes the stored value, or the memory copy when storage is
  empty or throws. So a dismissal holds for the page's lifetime even when storage is blocked.
  Accessing `globalThis.sessionStorage` itself is guarded, since a blocked browser throws there.
- **D10** 2026-11-26 U11 stale dismissal: when a degraded answer is shown past the stored
  dismissal (new `since`, or a higher `lost`), `setHealth` clears the stored dismissal at once.
  An ok answer leaves it alone (D1). The decision is the pure `bannerVisible(h, dismissed)`.
- **D11** 2026-11-26 `docs/manual-checks/045-failures-visible.md` 1.5 still says the banner
  "cannot be dismissed". It is left as is: it is 045's dated record of what was checked then.
  The current behaviour is in `docs/observability.md` and in this spec.
