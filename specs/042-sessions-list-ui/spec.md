---
feature: 042-sessions-list-ui
kind: fix
created: 2026-11-24
branch: fix/042-sessions-list-ui
status: done
---

## Problem
Seen on a real store in Chrome, in the Lean and Stark styles:

- **P1**: the sessions table's header row stays pinned while the list scrolls (029's
  fixed-height list box), but rows scroll over it and the text overlaps. Two causes: every
  row is `position: relative` (for its stretched title link), so rows paint over the
  `z-index: auto` sticky header in both styles; and Lean's header background is the
  see-through `--panel`.
- **P2**: right after a restart, page 2 showed "Loading sessions…", then "the store did not
  answer in time" (the 5 s store timeout, a 504), and the pager disappeared, so there was
  no way back or to retry.
- **P3**: scrolling the sessions list and the page feels laggy. The profile blames live
  events: each `session` event redraws the whole page (all 50 rows re-inserted, unchanged
  attributes rewritten, the today tiles rebuilt), which forces a full table layout during
  the scroll (research "Step 1").

## Goal
The pinned header is readable in both styles and themes, a failed page can be retried or
left with the pager, and scrolling stays smooth while live events arrive.

## Non-goals
- Making page 2 or the warm-up fast (030).
- Changing the API or the server.
- Lean's glass (`backdrop-filter`) GPU cost: measured, not blamed (research D5).

## Acceptance criteria
- **AC1** (P1): the header cells of the sessions table have an opaque background from the
  existing theme tokens and paint above the rows, in Lean and Stark, dark and light, with
  a border or shadow under them. A webtest fails if the header background resolves to a
  see-through colour in any style and theme, or if the header does not stack above rows.
- **AC2** (P2): when a page fails to load, the pager stays visible and working, the error
  shows inside the list box with a Retry button that asks for the same page again, and
  Previous and Next still work. The URL keeps the page. Webtests: an error on page 2 keeps
  the pager; Retry re-requests page 2; Previous goes to page 1.
- **AC3** (P3): a live event that changes one row touches only that row's changed nodes,
  moves only rows whose place changed, and leaves the today tiles alone when the totals
  did not change. On the store copy, with live traffic and the CPU slowed 4×, scrolling the
  list shows no long task over 50 ms and a steady frame rate (research numbers before and
  after).
- **AC4**: nothing else changes: same API, same columns, same pager position.

## Edge cases
- **E1** (P1): a selected or hovered row scrolled under the header must not show through.
- **E2** (P2): an error on page 1 has no Previous; Retry still works and Next works when
  the total says more pages exist.
- **E3** (P2): Retry while the earlier request is still pending; the older answer is
  dropped (listGen), the newer one shown.
- **E4** (P2): a filtered list errors; where page n+1 starts is unknown, so Next is
  disabled and Previous and Retry work.
- **E5** (P3): a live event moves a session to the top of page 1; the focused title link
  keeps its focus (029 F1).
