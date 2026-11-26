---
feature: 045-failures-visible
kind: fix
created: 2026-11-26
branch: fix/045-failures-visible
---

This spec covers what was planned as 037 (missing blobs break reads) and 038 (silent
capture loss). Both are built here as one change.

## Problem
From the audit:
- FAIL-1: one missing or unreadable blob makes /api/sessions return 500 for every
  session, and the body endpoint returns 500 and logs nothing.
- FAIL-3: a blob write failure drops the whole call row, so the call disappears
  instead of showing up without its body.
- REL-3, FAIL-2: capture can be failing or dropping while /healthz, /api/health and
  the UI all say ok.
- CON-2, PERF-9: a burst of more than 256 hook events drops the extra ones and still
  answers 200, with no metric.

An observability tool that loses data without saying so is the worst failure it can have.

## Goal
Nothing is lost silently. A missing body shows as "content unavailable" where it
belongs and never breaks anything else; a call whose body could not be stored is kept
with its metadata; every drop or store failure is counted, exported as a metric,
reported by /healthz and /api/health as degraded with counts, and shown in the UI as a
persistent banner.

## Non-goals
- Retention.
- The data-model change (no new tables or columns).
- Repairing a damaged store.

## Acceptance criteria
- **AC1** A missing or unreadable blob becomes a per-block "[content unavailable]"
  placeholder in timeline, digest and view builds; /api/sessions answers 200 and other
  sessions are untouched.
- **AC2** The body endpoint answers 404 `body_not_stored` for a missing or unreadable
  blob and logs one line naming the hash.
- **AC3** A blob write failure keeps the call row with its metadata; its body reads as
  content unavailable; the failure is counted.
- **AC4** Hook events dropped, captures dropped, store write failures and blob write
  failures are each counted and exported as Prometheus counters.
- **AC5** /healthz and /api/health report `status: degraded` with the counts (format in
  research D3); /healthz stays 200 while the gateway proxies, so the claude shell
  function never bypasses a gateway that is only degraded.
- **AC6** `gateway doctor` shows the degraded state with its counts.
- **AC7** The UI header shows a persistent banner "capture degraded: N lost since
  start", a short reason and a link to the details, in both styles and both themes,
  following the clearing rule (research D4).
- **AC8** docs/observability.md and the troubleshooting part of docs/install.md say what
  degraded means, where it shows and what to do about each counter.

## Edge cases
- A blob deleted after its row was written; a blob made unreadable (mode 000).
- A blob file whose bytes do not decode, or decode to the wrong size.
- The same hash written again after an earlier blob write failed (it must heal).
- A store-read deadline is not "unavailable": it still fails the request as before.
- A burst of 1,000 hook events against a queue of 256.
- Counters read while many workers add to them.
