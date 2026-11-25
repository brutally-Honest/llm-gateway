---
feature: 043-fast-rows-first
kind: fix
created: 2026-11-25
branch: fix/043-fast-rows-first
---

## Problem
030's view cache is keyed by the gateway executable, so it is empty after every install, and
new users start with an empty cache. With an empty cache `/api/sessions` builds every row's
full facts before it answers: on a copy of the real store page 1 takes 4.3 s and page 2 hits
the 5 s store timeout (504); filters take about 3 s (research, step 1).

## Goal
Every `/api/sessions` page and filter answers at once and never times out, even with an empty
cache. Rows show the columns that are cheap to read straight away; the heavy facts fill in as
soon as they are built, the rows on the page being viewed first.

### Row split
| Cheap, at once (SQL only) | Heavy, filled in when built |
|---|---|
| start time (`first_at`), `last_at` | title |
| status: active (clock and last activity), hooks only | status: failed |
| folder (first hook naming one, one indexed query per page) | model |
| client, session key, id | Work: prompts, calls, side calls, tool calls, failed tools |
| exchange count, effect counts (not shown alone) | Changes (needs the command count from the view) |
| store version of the row | Time (clock span from the view) |
| | Tokens and cost |
| | Needs a look |

## Non-goals
- A session-summary table in the store (after v1, with the data-model ADR).
- Live updates of an open session (040).
- Changing what is stored.
- The SQL floor of the session list query on large stores (030 D29).

## Acceptance criteria
- **AC1** With an empty view cache (memory and disk), `/api/sessions` pages 1, 2 and 5 answer
  without waiting for any session build. A row whose facts are not built carries
  `"pending": true` and only the cheap fields; it never carries a heavy field (absent, not
  zero).
- **AC2** A row whose memo is in memory (any version, as today) or on disk at the row's
  current key is served with its facts, never pending. A warm restart serves full rows, as 030.
- **AC3** Missing facts are built in the background, at most a fixed number at a time; the
  rows of the most recent list request go first, in row order; the background pass (030 AC7)
  waits while such work is queued. A build that a request waits for (session page, live
  update) is not starved.
- **AC4** `kind=active` and `kind=hooks` are decided from the cheap fields and are exact. For
  `kind=look` and `q`, a row is tested on its known facts; a `q` that matches a cheap field
  (client, key, id, folder) includes the row even while it is pending. A row that cannot be
  decided yet is left out and counted: the answer carries `checking`, the number of scanned
  rows still being checked, and their builds are queued. No filtered answer times out.
- **AC5** Filled-in facts reach the page by a follow-up fetch of the page on screen while it
  holds pending rows or `checking > 0`, and by the existing `session` stream events. A row is
  never replaced by one of an older store version.
- **AC6** In the browser, a pending row renders at once: cheap cells filled, every heavy cell
  a quiet placeholder of fixed width, so nothing moves when the facts arrive. A filtered list
  with `checking > 0` shows "N sessions still being checked" until it is 0.
- **AC7** Measured on the store copy and the 3x store with an empty cache: every page and
  filter answers in under 200 ms and never 504; all facts of the viewed page fill in within
  5 s on the copy. Warm-cache numbers do not get worse.

## Edge cases
- **E1** A build fails or times out: the row stays pending; the next request queues it again;
  the browser stops its follow-up fetches after a bounded time and the placeholder stays.
- **E2** The store moves while a build runs: the built memo carries the version it read; a
  newer live event wins over a late fill-in (version guard, AC5).
- **E3** A memo evicted from memory and absent on disk: the row is pending again, never shown
  with a different session's or version's facts.
- **E4** A filtered page after the first while rows are still being checked: page boundaries
  are recomputed from page 1 on each follow-up fetch, so no row is shown twice or skipped.
- **E5** A hooks-only session is never pending for its status; its title and Work still are.
- **E6** No stream connected: the follow-up fetch alone fills the page in.
