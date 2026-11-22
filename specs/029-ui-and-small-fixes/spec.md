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
- With a filter or search, a request tests at most 500 rows (10 × the maximum limit), so
  `items` may hold fewer than `limit` rows, even none, while `next_after` is set.
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
- **E8** A hooks-only session with `SessionEnd` and other hook events stays active while
  recent (only a `SessionEnd`-only session changes).
