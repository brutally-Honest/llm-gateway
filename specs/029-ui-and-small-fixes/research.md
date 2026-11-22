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
- **D10** Server half: the filtered scan reads batches of at least 51 rows
  (`max(limit+1, 51)`, capped by what is left of the 500): each `SessionPage` call runs
  the session GROUP BY over the whole store, so batches of `limit+1` with `limit=1` could
  run it about 250 times in one request. When a scan stops on the last row of a full
  batch, it reads one more row to learn whether `next_after` is needed, so a null cursor
  always means the end.
- **D11** `kind` values are case-sensitive (`ALL` is a 400); a cursor is decoded as before.
  `total` comes from `SessionCount`, the session union wrapped in `COUNT(*)`, placed in
  `internal/store/reader_ctx.go` beside `SessionPage`. `/api/sessions` gets its own response
  type, so `/api/ungrouped` keeps its old shape.
- **D12** (2026-11-22) Correction to D3: a 500-row cap alone is not enough. Under
  `make verify` (race detector), testing 500 cold rows took over the 5 s store timeout and
  returned 504, and real sessions are heavier. The scan also stops before a new batch once
  it has spent `scanBudget` (2 s) in this request; the first batch always runs, and
  `next_after` stays at the last tested row. `scanCap` and `scanBudget` are package vars so
  tests can lower them: the cap test runs at 50 rows, since 600 rows still timed out under
  `-race`.
- **D13** The repo's purity test (`internal/core/purity_test.go`) forbids client names in
  `internal/ui`, and "cursor" is one. Go identifiers, comments and tests say "after" or
  "position" instead; spec.md keeps the word "cursor" for the contract, since specs are
  not checked.
- **D14** B12: the rule lives once, in `isActive(last, endOnly, now)` (`api_view.go`),
  used by `buildSummary` (so `kind=active` follows), `buildView` and the view cache check.
  The fact comes from SQL for the list (`SessionRow.EndOnly`: the hooks-only branch's
  `MIN(phase = core.PhaseSessionEnd)`) and from the loaded hooks for the view
  (`endOnlyHooks`). The canonical phase `session_end` is used, not Claude Code's
  `SessionEnd`, so no client name enters `internal/ui` or the store. `hub.go:722` is left
  alone: it sets `Inactive` only for sessions with exchanges, which B12 sessions never
  have. The session is not hidden from the list (non-goal).
- **D15** Browser paging keeps the page's existing "Load more" button (the pattern the
  list already used), with no `limit` sent. Each query or page change bumps a generation
  number and older answers are dropped (E7); Load more is disabled while it fetches and
  shows three skeleton rows.
- **D16** A sessions→sessions hash change is applied in place (`listSync` in `app.js`, the
  twin of the session page's `sessionSync`): the page is not rebuilt, the stream keeps
  running and the search box keeps focus. Search text is trimmed before it goes into the
  URL. An unknown, empty or wrong-case `kind` in the URL reads as `all`.
- **D17** Live updates in a filtered list: a row already shown is updated in place; an
  unseen session triggers a debounced (500 ms) refetch of page 1 for the current kind and
  q, merged by id. The browser does not copy the server's filter rules, so a shown row
  that stops matching stays until the next page-1 load.
- **D18** Skeletons: the session page (`setStatus` "Loading the session…") and the call
  panel ("Loading call…") already had them, so only app-level tests were added there. New
  ones: Load more's rows. `filterSessions` and the "filtering N loaded" note are removed
  with their tests; `filters.js` keeps `FILTERS` and gains `normalizeKind`.

## UI check (2026-11-22, branch at 8da69fe)

Throwaway gateway (`bin/gateway`, `config.example.yaml` copy, ports 17295–17299, fresh data
folder in the session scratchpad), seeded with fictional data only: 130 hooks-only sessions
(`UserPromptSubmit` with made-up folders `/srv/fx029-none/{alpha-shop,…}`), one
`SessionEnd`-only session (B12), one `SessionStart`+`SessionEnd` session (E8),
`seed-call-kinds.mjs` (plain, streamed, long, errored calls) and two `seed-calls.mjs --calls 6`
runs. 135 sessions in all. `seed-session.mjs` was not used because it makes a git repo under
the home folder.

API, every page walked: unfiltered 135 rows in 3 pages, no repeats; `kind=active` 134 (the
end-only session left out), `kind=hooks` 132, `kind=look` 1 (the errored call),
`q=gamma` 26, `kind=hooks&q=gamma` 26, `q=%_` 0, `kind=active&limit=1` 134 rows over 134
pages with no repeats. `kind=bogus`, `limit=0` and a 201-byte `q` → 400. Empty store
before seeding → `{"items":[],"next_after":null,"total":0,"scanned":0}`.

Chrome, at 2,560 px and 1,440 px, light and dark:
- Paging: 50 → 100 → 135 rows, unique ids, Load more gone at the end; during each fetch
  three skeleton rows, "Loading more sessions…", button disabled.
- Each filter (All, Needs a look, Active, Hooks only) sets the hash and shows the server's
  rows; with fetch slowed in the page, a filter change shows six skeleton rows and
  "Loading sessions…", then the rows.
- Search typed in the box: one request after typing settles, `#/?q=gamma` via replaceState
  (history length unchanged), focus kept. Filter plus search: `#/?kind=active&q=gamma`,
  26 rows.
- Back restored Active + "gamma"; reload of `#/?kind=hooks&q=beta` restored both (26 rows).
- No match: "no sessions match".
- B12: the end-only row shows ◇ "hooks only", not ●; the start+end row shows ● active.
- Session page: skeleton ("Loading turns…") with fetch slowed, then the page. Call panel:
  skeleton ("Loading call…"), then the call; calls open and step with Previous/Next, with
  no console errors, without the removed fields.
- Preview and Raw: padding 3px 9px, as `.btn.sm`; they look balanced in their box in both
  themes. They are 23.8 px tall against 26.6 px for Previous/Next, from the 11.5 px font
  and the box border (D8 leaves the font alone).
- No horizontal scroll at 1,440 px.

Not seen in the browser: the "No matches in the newest N sessions yet" state, which needs
more than 500 sessions or a 2 s scan (covered by `app-list.test.js` and
`TestSessionsPage_ScanCap`/`ScanBudgetStopsEarly`). Nothing looked off.
At 14:11, past the five-minute window, `kind=active` returned 0 of 135 rows.

## Review (round 1, 2026-11-22, fresh opus reviewer on `git diff main...HEAD`)

No P1. Cursor logic, SQL, the removed exchange fields and B12 were found correct.
- **P2** Load more racing the page-1 refresh on Back could append a deep page after a
  fresh page 1 and move `next_after` past the rows in between, so they were never shown
  (`app.js` showSessions held-rows branch, `loadSessions`). Reproduced by the reviewer's
  probe test. → fixed: an answer applies only if the list is still at its position; test
  added.
- **P3** A filtered list refetched page 1 after every lull of a busy, non-matching
  session: one full filtered scan per event burst. → fixed: refetch throttled to one per
  5 s, and skipped when the event's session is older than page 1's oldest row.
- **P3** The 2 s budget did not count the batch about to start. → fixed: no new batch
  when elapsed + the last batch's duration passes the budget.
- **P3** The search box had no byte limit, so a long paste showed a 400 in place of the
  list. → fixed: q is capped at 200 UTF-8 bytes on a character boundary, typed or from
  the URL.
- **P3** D2's "digest cache makes repeat scans cheap" holds only up to the cache size
  (512 sessions). → recorded as D19, not built.
- **P4** search fields joined with "\n" could match across fields → fixed (each field
  tested alone). `sessionSelect` back to `const` → fixed if the constant allows.
  `SessionCount` on every page, shell links dropping the filter, no
  `docs/manual-checks/029-*.md`, a stale comment in `api_exchange_cache_test.go` → left
  as they are (P4, outside the review-fix scope).
- Test gaps named: AC1 has no automated test (CSS, checked by hand); E4's "a paged-past
  session whose last activity moves" has no test.

## Decisions (continued)
- **D19** (2026-11-22) Correction to D2's flip condition: the digest cache holds 512
  sessions (`digest_cache.go`), so above that a filtered walk recomputes cold digests and
  evicts the ones page 1 and the stream use. The flip point is "more sessions than the
  digest cache", not "tens of thousands". SQL prefilters (session key, client, `last` for
  `kind=active`) or a larger cache are the follow-up; neither is in 029.
- **D20** (2026-11-22) Page numbers (amendment, fix 7): the human chose the hybrid.
  Thesis: an unfiltered list knows `total`, so a full pager with jumps costs one SQL
  `OFFSET` and no summaries for skipped rows. Antithesis: one paging model for both modes
  (Prev/Next everywhere) is simpler and never needs `offset`; a full pager in both modes
  would need a filtered match count, a scan of every session per request, which breaks the
  2 s budget (D12). Synthesis: `offset` for the unfiltered list, `after` for filtered pages
  with the pages reached numbered. Flip: if filtered lists get a cheap match count (SQL
  prefilters, D19), the filtered pager can become a full one too.
- **D21** A filtered `page=N` in the URL is reached by walking pages 1 … N, since positions
  are not kept in the URL (D6); each request is bounded by the scan budget, so the walk is
  slow only for deep pages of a rare match.
- **D22** Long text (fix 9): the cap sits on `.chapter .tx > .tx-b` (24rem, as
  `.effect-body`), so Raw grows inside the same box and gets no nested scroll; the copy
  button lives in `.tx-h`, outside it. `scrollbar-gutter: stable` keeps the text width
  fixed when the scrollbar appears. Cost: with classic scrollbars (Linux, Windows) a short
  block keeps an empty gutter inside its 16px right padding. Story bar (fix 8): 16px below,
  as `.sesstabs .tabp`'s top margin.
- **D23** The sessions box height is `calc(100vh - 320px)`, min 360px, measured in Chrome:
  box top 263px + 14px gap + 27px pager + 16px below it, at 1,440×900 and 2,560 wide. The
  first estimate (`100vh - 20rem`) put the pager below the window: the root font is 14px,
  so 20rem was 280px. If the page head grows (tiles wrapping on a narrow window) the pager
  can drop below the fold; the box still scrolls and the page scrolls to it.
- **D24** The current page is filled (`--on-accent` on `--accent-strong`), the pair
  `contrast.test.js` already checks. The first tries were too faint (panel2 on panel, 0.09
  against 0.05 white in dark) or failed 4.5:1 (accent-strong text on panel2: 3.36 base dark,
  4.29 base light, 4.43 lean light); the test pair added for the second try was removed
  with it.

## UI check, amendment (2026-11-22, throwaway gateway rebuilt, 435 fictional sessions)

300 more hooks-only sessions (`fx029-more-NNN`, made-up folders `/srv/fx029-none/zeta-N`)
were added for 9 pages. Chrome, 1,440×900 and 2,560×1,400, light and dark:
- Unfiltered pager `‹ 1 2 3 … 9 ›`; 3, 5, 1 and 9 each one request; `page=99` lands on
  page 9 (35 rows) and the URL reads `page=9`; the box scrolls to its top on a page change;
  Back steps 5 → 3 → 1. The pager's bottom is at 740 of 757 (1,440) and 1,240 of 1,257
  (2,560), so it is in view without scrolling; the box keeps its height on every page.
- Filtered (`kind=hooks`): `‹ [1] ›` → `‹ 1 [2] ›` → `‹ 1 2 [3] ›`, and the numbers stay
  after going back to 1; `q=zeta-3` (43 rows) shows no pager; a link to
  `kind=hooks&q=more&page=4` walks to page 4.
- Current page: filled accent, plain in both themes (after D24).
- Story bar: 16px between it and the first turn card, light and dark.
- Long blocks: the 60-line prompt and answer of the "very long call" turn are 336px boxes
  that scroll inside, in Preview and in Raw, with the same 1,009px text width as short
  blocks (42px, unchanged) — no width or height jump on the switch; copy stays in the
  header. On this Linux Chrome a short block keeps a 15px empty gutter at its right (D22);
  it reads as a wider right margin, nothing visible.
- No console errors.

## Review (round 2, 2026-11-22, fresh opus reviewer; the last round)

No P1. Offset path, E9, stale-answer guards, URL state, CSS tokens and agnosticism were
found correct; the reviewer's probes are in the session scratchpad.
- **P2** A filtered page-1 refetch renewed page 2's start but kept stale starts for pages
  3+, so a jump 1 → 3 after a new matching session skipped a row. → fixed: a fetched
  `next_after` that differs from the stored start drops every later start.
- **P3** On unfiltered page 2+ a held row with new activity re-sorted to the top, against
  E10. → fixed: replaced at its index; the test now uses a newer `last_at` on a middle row.
- **P3** Focus was lost when the clicked arrow became disabled (› onto the last page). →
  fixed: focus goes to the current page.
- **P3** The unfiltered total refresh read a whole page, and the unassigned list was read
  on every page change. → fixed: `limit=1` for the total; unassigned only on page 1 or
  when its count changed.
- **P3** A filtered `page=N` walk makes N requests with no cap → left as D21 accepts it.
- **P4** A markdown code block nested a scroller inside the capped body → fixed;
  `copyButtonStaysInHeaderOutsideScrollBox` passed without the code → made to build a
  block and check where Copy sits. Offset paging can repeat one row on the next page
  after an insert → by design (D20).
