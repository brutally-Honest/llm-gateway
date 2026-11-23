---
feature: 041-flakes-and-quick-speed
kind: fix
created: 2026-11-23
branch: fix/041-flakes-and-quick-speed
---

This spec absorbs the planned 035 quick speed fixes (S1 to S4); there is no separate
035 spec.

## Problem
Three test flakes make CI unreliable, and four small speed problems slow the UI.

- **F1**: `internal/ui` `TestStream_ReplayOf20CallsResendsAtMostThePreviousCall` failed on
  main's macOS run 45104601139 (a560277) with `stream_test.go:1153: commit before c02:
  unexpected entries event`, in 0.13 s. Left out of scope in 036 (D3).
- **F2**: rate-limit tests depend on the wall-clock minute: the limiter's 60 s window can
  reset between counting tokens and the check (036 D8, roadmap Later).
- **F3**: one `cmd/gateway` failure in 3,800 macOS runs (036 D9), cause unknown, because
  CI's output was cut and the error lost.
- **S1** (PERF-10): the first call details after a restart wait about 3 s for a full
  session build just to fill the session title and the call number
  (`internal/ui/api_exchange.go`, `placeInSession`).
- **S2** (PERF-8): `/api/today` reads every call since midnight, and the UI refetches it
  after every burst of live events (`internal/ui/api_today.go`,
  `internal/ui/web/app.js` `scheduleToday`).
- **S3** (PERF-6): the indexes `events_tool_call` and `events_principal` take about 195 MB
  of the store and no query uses them.
- **S4**: `ui digest compute failed` is logged at warn for `context canceled` at every
  restart, which is a normal shutdown (`internal/ui/digest_cache.go`).

## Goal
F1 and F2 are fixed at their cause, F3 can be diagnosed next time it happens, and S1 to
S4 are fixed and measured before and after on a copy of a real store.

## Non-goals
- The persistent cache and warm-up (030), incremental builds (040).
- Dropping `events_content` (kept until garbage collection exists).
- No skip, retry, longer sleep or looser assertion anywhere.
- No product behaviour change for F1 to F3 unless the cause is in product code (then a
  D-entry says so).

## Acceptance criteria
- **AC1** (F1): research.md records the reproduced failure, the event order that caused it,
  and whether the test or the stream is wrong by the 009 hub contract. The reproduction
  fails before the fix and passes three rounds after.
- **AC2** (F2): the rate limiter takes an injectable clock that only tests set; production
  uses `time.Now`. Every rate-limit test uses a fixed clock. A test that sets the clock
  just before a minute boundary fails on main's code and passes after. The roadmap entry
  is removed.
- **AC3** (F3): when a test fails in CI, the log keeps its full failure text and
  timestamps; a passing run's log is not much longer. `make test` locally is unchanged
  and both job names stay.
- **AC4** (S1): `placeInSession` uses only an already-cached session view and never waits
  for a build; with none cached, the title and call number are left out. The first
  `GET /api/exchanges/{id}` after a restart answers in under 50 ms on the store copy.
- **AC5** (S2): `/api/today` answers in under 100 ms on the store copy, and the UI asks for
  it at most once per 5 s during a burst, with a final fetch after the burst ends.
- **AC6** (S3): a new migration step drops `events_tool_call` and `events_principal`; no
  existing step is edited; a store migrated by main still opens.
- **AC7** (S4): a digest compute ended by `context.Canceled` or
  `context.DeadlineExceeded` logs at debug; any other error still logs at warn.
- **AC8**: research.md has before and after numbers for S1 to S3, the store size before,
  after the migration and after a manual VACUUM, and both `make verify` times.

## Edge cases
- **E1** (F1): a session rebuild lands after a client's stream opens; the catch-up must
  not resend an entry the snapshot already holds, or the test must accept what the
  contract allows (AC1).
- **E2** (F2): a token count and the next call straddle a minute boundary (AC2).
- **E3** (S1): the cached view is older than the call; the call number must never be
  wrong, so it is left out rather than guessed (AC4).
- **E4** (S2): the last event of a burst arrives while a throttled fetch is pending; the
  totals must still be fetched once after it (AC5).
- **E5** (S2): the day changes; totals keyed to an older day must never be served
  (AC5).
- **E6** (S3): a store opened by an older binary after the migration still works; the
  dropped indexes are not needed by any query (AC6).
