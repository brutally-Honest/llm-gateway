---
feature: 029-ui-and-small-fixes
kind: fix
created: 2026-11-22
branch: fix/029-ui-and-small-fixes
---
# UI and small fixes

## Problem
- The Preview and Raw buttons in the call panel have less padding than the panel's other
  buttons of their size.
- The sessions list loads 50 rows, then filters and searches only the rows already loaded
  (`internal/ui/web/filters.js`): a filter can miss sessions that were never loaded, and the
  filter and search are lost on reload or Back.
- Loading states show empty space or a line of text instead of a skeleton.
- B12 (`docs/manual-checks/macos-v1.md`): a session known only from one `SessionEnd` hook
  shows as active for up to five minutes.
- Spec 017 E10 (live updates while a chip's panel is open) has no test.
- `fillDrawer` in `internal/ui/api_exchange.go` still fills drawer fields the page no longer
  reads.

## Goal
Six fixes:
1. Preview and Raw get the padding of the panel's other buttons of their size.
2. The sessions list pages on the server, 50 rows at a time, with filter and search done on
   the server and the list state kept in the URL.
3. Skeleton loaders while data loads.
4. B12: a hooks-only session whose only hook event is `SessionEnd` is never active.
5. The spec 017 E10 test.
6. `fillDrawer` and the fields only it fills are removed from the exchange API.

## Non-goals
- Paging the ungrouped list or the session timeline (they already page).
- Hiding B12 sessions from the list (only their active flag changes).
- A matched-row total for a filtered list (it would mean testing every session on every
  request).
- Putting loaded pages (the cursor) in the URL: a reload starts at the first page.

## API contract: `GET /api/sessions`

### Query parameters
| Key | Values | Absent | Invalid → 400 `bad_request` |
|---|---|---|---|
| `limit` | integer ≥ 1 | 50 | not an integer, or < 1. Above 50 is clamped to 50 |
| `after` | the cursor: an opaque `next_after` from an earlier response | first page | not a sessions cursor (bad base64, bad JSON, unknown field, another list's token) |
| `kind` | `all`, `look`, `active`, `hooks` | `all` | any other value, or an empty value |
| `q` | search text, at most 200 bytes after trimming | no search | longer than 200 bytes |

Unknown parameters are ignored. A repeated parameter uses its first value.

- `kind=look`: `list.needs_look > 0`. `kind=active`: the row's `active` field (so B12 applies
  to the filter too). `kind=hooks`: `hooks_only`, or `has_hooks` with no `has_calls`. These
  are the same tests `filters.js` ran in the browser.
- `q` is trimmed; empty after trimming is no search. It matches case-insensitively as a
  plain substring (no wildcards, no SQL) against the row's title, client, session key, id,
  folder, full folder and each model.
- `kind` and `q` together: a row must pass both.

### Response (200)
```json
{
  "items": [ /* session summaries, as today, newest last activity first */ ],
  "next_after": "opaque cursor" | null,
  "total": 123,
  "scanned": 50,
  "unassigned_connections": 0
}
```
- `items`: at most `limit` rows that pass the filter and search, in the store's order
  (last activity descending, then principal, client, session key).
- `next_after`: the position of the last row the server tested on this request; `null` when
  no row is left to test. The client asks again with it while it is not null.
- With a filter or search, a request tests at most 500 rows (10 × the maximum limit), and
  starts no new batch of rows once it has spent 2 s scanning (the first batch always
  runs), so `items` may hold fewer than `limit` rows, even none, while `next_after` is set.
- `total`: every session in the store, ignoring `kind` and `q`, so the page can tell "no
  sessions captured yet" from "no sessions match".
- `scanned`: how many rows this request tested.
- `unassigned_connections`: as today (omitted when 0).
- A cursor is a position in the order, not tied to a filter: reusing one with another
  `kind` or `q` is valid and continues from that position.

### URL state (the page)
The list lives at `#/`. Its state is in the hash query: `#/?kind=active&q=some%20text`.
- `kind` is left out when `all`, `q` when empty. An unknown `kind` in the URL reads as `all`.
- A filter click adds a history entry; typing in the search box replaces the current entry
  once typing settles, so Back steps through filter changes, not keystrokes.
- Reload, Back and a link to the URL restore the filter and the search; the page then
  loads the first page for them.

## Amendment (2026-11-22): page numbers, story bar spacing, long text blocks

Asked for after the first build, before the PR. Three more fixes:
7. Numbered pages replace Load more (hybrid, chosen by the human):
   - **No filter, no search:** a full pager `‹ 1 2 [3] 4 5 … 12 ›` that can jump to any
     page; the page count is `ceil(total / 50)`.
   - **Filter or search on:** numbered pages reached so far plus `›` while `next_after` is
     set (`‹ 1 2 [3] ›`); no total.
   - The list box has a fixed height and scrolls inside; the pager stays below it, so
     changing page never moves the pager. A page change scrolls the box to its top.
8. The session page's "Prompts and answers as Preview · Raw" bar has space below it, so it
   no longer touches the first turn card.
9. A long prompt or final answer in a turn card is capped in height and scrolls inside,
   with no layout shift (the scrollbar does not change the text's width) in Preview and in
   Raw; the card's copy button stays where it is.

### Contract change: `offset`
| Key | Values | Absent | Invalid → 400 `bad_request` |
|---|---|---|---|
| `offset` | integer ≥ 0, rows to skip | 0 | not an integer, < 0, or sent with `after`, a `kind` other than `all`, or a non-empty `q` |

- `offset` is for the unfiltered list only: page k is `offset = (k − 1) × 50`. An offset at
  or past `total` gives `items` empty and `next_after` null. The response shape is
  unchanged (`next_after` is still set when rows follow).
- A filtered list pages with `after` only, as before.

### URL state change
- `page` joins the hash query: `#/?kind=active&q=x&page=3`; left out on page 1. Not a
  positive integer → page 1.
- Unfiltered: reload, Back and a link open page `page` directly (one request).
- Filtered: positions are not in the URL, so opening `page=N` walks pages 1 … N in order;
  if the pages run out first, the last page reached is shown and the URL says so.
- A page click adds a history entry; a filter or search change goes back to page 1.

## Acceptance criteria
- **AC1** Preview and Raw have the same padding as the call panel's other buttons of their
  size.
- **AC2** `GET /api/sessions` follows the contract above: default and maximum limit 50,
  cursor paging with no skipped or repeated rows across pages, `kind` and `q` on the server,
  `total` and `scanned` in the response, 400 for each invalid parameter.
- **AC3** The sessions page loads 50 rows at a time, sends `kind` and `q` to the server, and
  keeps them in the URL; reload and Back restore them.
- **AC4** A skeleton shows while the sessions list, the session page and the call panel
  load, and is replaced by the content or the error.
- **AC5** A hooks-only session whose only hook event is `SessionEnd` is not active, in the
  list (`active`, and so `kind=active`) and in the session view (`isActive` and the view
  cache check).
- **AC6** A webtest covers 017 E10.
- **AC7** `fillDrawer` is gone, with every response field only it filled; no JS read them.
- **AC8** The sessions list shows numbered pages as in the amendment (full pager unfiltered,
  pages reached plus `›` when filtered), in a fixed-height box with the pager below; `page`
  is in the URL and reload and Back restore it. Load more is gone. `offset` follows its
  contract (AC3's "50 rows at a time" now means one page of 50).
- **AC9** The story bar has space below it in both themes.
- **AC10** A long prompt or final answer scrolls inside a capped box, in Preview and Raw,
  with no layout shift and the copy button in place.

## Edge cases
- **E1** Empty store → `items` empty, `next_after` null, `total` 0.
- **E2** Exactly 50 matching rows → one page, `next_after` null; 51 → the second page has one.
- **E3** A cursor past the last row → `items` empty, `next_after` null.
- **E4** Rows arriving between page requests: a new session sorts before the cursor, so it
  is not repeated on a later page (the live stream shows it on the first page). A session
  whose last activity moves after it was paged past is not shown again by paging.
- **E5** A filtered request that tests 500 rows and finds none → `items` empty, `next_after`
  set; the page says nothing matched yet and keeps Load more.
- **E6** Search text with `%`, `_`, quotes or backslashes matches literally.
- **E7** Typing fast in the search box sends one request once typing settles; a response to
  an older query is dropped.
- **E9** `page` past the last page (unfiltered: `page=99` with 3 pages) → the last page,
  and the URL is corrected to it; `total` 0 → one empty page, no pager numbers.
- **E10** Sessions arriving while on page 2 or later: rows on screen update in place, the
  page count follows `total`, and the page does not shift under the reader. On page 1 a new
  session appears at the top and the page keeps 50 rows.
- **E8** A hooks-only session with `SessionEnd` and other hook events stays active while
  recent (only a `SessionEnd`-only session changes).
