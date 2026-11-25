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
  zero; its own JSON shape). Every row carries `row_version`, the store version its cheap
  fields were read at; a row with facts also carries `version`, the version its facts were
  built at (as today). The cheap folder always wins over a memo's folder.
- **AC2** A row whose memo is in memory (any version, as today) or on disk at the row's
  current key is served with its facts, never pending. Disk is checked before any build is
  queued. A warm restart serves full rows, as 030.
- **AC3** Missing facts of list rows and of the background pass go through a build gate with a
  fixed number of slots: the rows of the most recent list request (or stream snapshot) first,
  in row order, older requests after, the background pass last; the pass does not start its
  next session while list work is queued. A build some caller waits for (session page, live
  update, catch-up) bypasses the gate and promotes a queued run it joins, so it is never
  starved and never starves the list. Timeouts of gated builds start at the slot.
- **AC4** `kind=active` and `kind=hooks` are decided from the cheap fields and are exact. For
  `kind=look` and `q`, a row is tested on its known facts; a `q` that matches a cheap field
  (client, key, id, folder) includes the row even while it is pending (with `kind=look` too,
  the row stays undecided until its facts are known). A row that cannot be decided yet is
  left out and counted: the answer carries `checking`, the number of scanned rows still
  being checked, and their builds are queued. Facts are looked up row by row and the scan
  stops as soon as the page is full and it is known whether a next page exists. No filtered
  answer times out.
- **AC5** Filled-in facts reach the page by a follow-up fetch of the page on screen while it
  holds pending rows or `checking > 0`, and by `session` stream events, including the
  stream's opening snapshot, which uses the same non-waiting path. Merge rule for a held row:
  the cheap part is replaced unless the incoming `row_version` is strictly older; the facts
  are replaced when the incoming row has facts whose `version` is not strictly older, and
  kept when the incoming row is pending. A full page load (new page, new filter, Retry)
  replaces rows without the rule. One list fetch is in flight at a time.
- **AC6** In the browser, a pending row renders at once: cheap cells filled, every heavy cell
  a quiet placeholder of fixed width, so nothing moves when the facts arrive. A filtered list
  with `checking > 0` shows "N sessions still being checked" until it is 0. While
  `checking > 0` the URL's page is kept, not corrected to the last page found so far.
- **AC7** Measured on the store copy and the 3x store with an empty cache: every page and
  filter answers in under 200 ms and never 504; all facts of the viewed page fill in within
  5 s on the copy. Warm-cache numbers do not get worse.

## Edge cases
- **E1** A build fails or times out: the row stays pending; the next request queues it again;
  the browser stops its follow-up fetches after a bounded time, then says the details are
  not available yet and offers Retry. Follow-up fetches stop when the list is left or
  reloaded, and pause while the tab is hidden.
- **E2** The store moves while a build runs: the built memo carries the version it read; a
  newer live event wins over a late fill-in (version guard, AC5).
- **E3** A memo evicted from memory and absent on disk: the row is pending again, never shown
  with a different session's or version's facts.
- **E4** A filtered page after the first while rows are still being checked: page boundaries
  are recomputed from page 1 on each follow-up fetch, so no row is shown twice or skipped.
- **E5** A hooks-only session is never pending for its status; its title and Work still are.
- **E6** No stream connected: the follow-up fetch alone fills the page in.
- **E7** A store restored to an older copy: versions go back; a full page load replaces the
  rows without the merge rule, so nothing stays stuck.
