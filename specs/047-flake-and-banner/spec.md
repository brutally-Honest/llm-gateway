---
feature: 047-flake-and-banner
kind: fix
created: 2026-11-26
branch: fix/047-flake-and-banner
---

## Problem
- **F1** Tests fail with "TempDir RemoveAll cleanup: directory not empty" (seen in
  `TestSessionsPage_BadParams` on PR #74's macOS run; 11 failures in 400 runs of the affected
  tests on clean main, 045 D24). A background writer (likely 030's disk-cache writer or 043's
  background builds) still writes into the test's folder after the test ends.
- **U11** 045's degraded banner cannot be dismissed until a restart, so one failed write
  leaves it up all day.
- **U12** (045 D26) The banner's text is rewritten every 15 s, so screen readers may announce
  it again each time.

## Goal
- **F1** Name the writer. If the gateway's own shutdown does not wait for it, fix that in
  product code (shutdown waits for or cancels it before returning); otherwise fix the test
  helpers. No sleeps, retries or skipped cleanup.
- **U11** The banner can be dismissed; it comes back only when the lost count goes up (or the
  gateway restarted, research D1).
  Dismissal is per tab (sessionStorage), no server change.
- **U12** The banner's text and live region change only when its counts change.

## Non-goals
- No change to `/healthz`, `/api/health`, metrics or `gateway doctor`.
- No other flakes; no other banner behaviour.

## Acceptance criteria
- **AC1** The reproduction (affected tests, `-race -count=200`, under load) fails before the
  fix and passes 3 rounds of `-count=200` after it.
- **AC2** Shutdown still waits for real work: the writer is waited for or cancelled before
  shutdown returns, never dropped silently mid-write.
- **AC3** The banner has a dismiss button; once dismissed it stays hidden in that tab while
  the lost count is unchanged and shows again as soon as it rises (webtests).
- **AC4** 15 s of polls with unchanged counts make no DOM change to the banner (webtest).

## Edge cases
- Gateway restart after dismissal (new `since`, count from 0): any loss after it shows the
  banner, even below the dismissed count (research D1; corrected 2026-11-26, review round 1).
- sessionStorage unavailable (throws): dismissal still works for the page's lifetime.
- Status goes ok then degraded again with a higher count: banner shows.
