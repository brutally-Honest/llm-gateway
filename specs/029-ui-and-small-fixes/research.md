---
feature: 029-ui-and-small-fixes
---
# Research & decisions — 029-ui-and-small-fixes

## Open questions
None. The brief asked for a build without stopping; judgment calls are D-entries below.

## Findings (2026-11-22, main at b4b2d6c)
- Filters and search run in the browser: `internal/ui/web/filters.js` `filterSessions`
  narrows the loaded rows by kind (`all`, `look`, `active`, `hooks`) and a text match over
  title, client, session key, id, folder, full folder and models. `sessions-page.js` calls
  it on every redraw; `app.js` `load()` fetches `/api/sessions` with only `after`.
- `GET /api/sessions` already pages by keyset (`after`, `limit`, default 50, max 200,
  shared with `/api/ungrouped`). The order is last activity descending, then principal,
  client, session key (`sessionOrder` in `internal/store/reader_sessions.go`).
- The title, folder, models, needs-a-look count and `has_calls`/`has_hooks` come from each
  session's memo (digest cache), not from SQL, so most filters can only be tested after a
  summary is built.
- Active is decided in three places: `buildSummary` (`api_sessions.go:151`), `isActive`
  (`api_view.go:75`, used by the view and its cache check at `api_view.go:52`) and the
  stream's `Inactive` flag (`hub.go:722`).

## Decisions
- **D1** The cursor keeps its existing name, `after` / `next_after`, the same as
  `/api/ungrouped` and the timeline, rather than a new `cursor` key: one paging vocabulary
  across the API, and the page's `loadMore` already reads `next_after`.
- **D2** Filters and search are tested in Go on the built summary, not in SQL. The fields
  they read (title, folder, models, needs-a-look, has_calls) exist only in the memo, and
  testing the summary means `kind=active` uses the very `active` field the row shows, so
  the B12 fix applies to the filter with no second copy of the rule. Search is a
  `strings.Contains` over lowercased text, so there is nothing to escape (E6).
  Thesis: one predicate, one source of truth. Antithesis: SQL prefilters (session key,
  client, `last` for active) would scan fewer rows. Synthesis: Go-side for now; local
  stores hold hundreds to low thousands of sessions and the digest cache makes repeat
  scans cheap. Flip: stores with tens of thousands of sessions or slow cold digests —
  then add SQL prefilters for the SQL-able parts.
- **D3** A filtered or searched request tests at most 500 rows (10 × the 50 limit) and
  returns `next_after` at the last row it tested, so each request stays within the store
  timeout and no row is skipped. `items` may then be short or empty while `next_after` is
  set; `scanned` says how many rows were tested.
- **D4** The maximum limit for `/api/sessions` drops to 50 (the brief); `/api/ungrouped`
  keeps 200 because the page reads its first 200 for the "N ungrouped" link.
- **D5** `total` counts every session ignoring the filter, so the page can tell an empty
  store from a filter that matches nothing. A matched total is not offered (non-goal).
- **D6** The list state lives in the hash query (`#/?kind=…&q=…`) because routing is
  hash-based; the cursor is not kept, so reload starts at the first page.
- **D7** Fix 6 removed nine `exchangeJSON` fields that only `fillDrawer` filled:
  `side_call`, `prev_id`, `thread_new`, `messages`, `messages_unavailable`, `carried`,
  `new_count`, `response`, `response_tool_call_ids`. The JS hits for `messages`,
  `response` and `carried` read the call-detail payload (`call-dialog.js` `d.saw`,
  `d.response`), not the exchange JSON; `exchange-detail.js` reads none of them.
  `timeline.ExchangeDetail.ResponseToolCallIDs` (`internal/timeline/exchange.go:33`) is
  left in place: it is a timeline type and outside this fix.
- **D8** Fix 1: the Preview/Raw toggle is `.mode .seg` (built by `textBlock` in
  `story.js`, so the session page's toggles change too). Its padding goes from `2px 9px`
  to `3px 9px`, the call panel's `.btn.sm`. Its font size (11.5px against 12px) is left as
  it is: the brief asked for padding.
- **D9** Fix 5: E10 is tested on the failed chip (`chip-live.test.js`): open its panel,
  apply a live rebuild with a second failure, and the panel stays open with both rows and
  the chip reads "✗ 2 failed". The test passed first time, so no bug was found; a probe
  expecting the old count failed as it should.
