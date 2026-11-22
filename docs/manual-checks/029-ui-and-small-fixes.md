# 029: UI and small fixes

Covers what the automated tests cannot see for
[specs/029-ui-and-small-fixes/spec.md](../../specs/029-ui-and-small-fixes/spec.md) (AC1–AC10,
E1–E10): the sessions list, the session page and the call panel in a real browser, at two
widths, light and dark.

The automated parts are:

- Go: `internal/ui/api_sessions_page_test.go` (`TestSessionsPage_*`: sizes, paging past the
  end, filter and search together, the scan cap and budget, literal search, bad parameters,
  the `offset` pages), `internal/ui/end_only_test.go` (`TestEndOnly_*`: B12 in the list and
  in the view), and `internal/ui/api_exchange_test.go` (`TestExchange_DrawerKeysAreGone`).
- Webtests in `internal/ui/webtest/`: `app-list.test.js` (paging, skeletons, filters, search,
  state in the URL), `sessions-page.test.js` (the pager and the list box), `route.test.js`
  (the hash query and Back), `story-layout.test.js` (the story bar gap, the long-text cap)
  and `chip-live.test.js` (spec 017 E10).

Record each check's seen result in the spec's `research.md` (the "UI check" sections), as
spec 017 does.

## Setup

1. From the repo root, build the branch and start a **throwaway** gateway on a fresh data
   folder and its own ports. It forwards model calls to the seed's stub upstream, so nothing
   reaches a provider. `mkdir` fails if the folder already exists, which keeps the data
   fresh:

   ```sh
   make build
   cp config.example.yaml /tmp/fx029-config.yaml
   mkdir /tmp/fx029-data
   GATEWAY_LISTEN_ADDR=127.0.0.1:17297 GATEWAY_UI_LISTEN_ADDR=127.0.0.1:17299 \
   GATEWAY_CAPTURE_DIR=/tmp/fx029-data \
   GATEWAY_EFFECTS_NETWORK_HTTP_PROXY_ADDR=127.0.0.1:17298 \
   GATEWAY_EFFECTS_NETWORK_SOCKS_PROXY_ADDR=127.0.0.1:17295 \
   GATEWAY_UPSTREAMS_ANTHROPIC_BASE_URL=http://127.0.0.1:17296 \
   bin/gateway -config /tmp/fx029-config.yaml     # in the background; leave it running
   ```

2. Check the empty store **now**, before seeding anything:

   ```sh
   curl -s 'http://127.0.0.1:17299/api/sessions'
   ```

   Expected: `{"items":[],"next_after":null,"total":0,"scanned":0}`. The list shows "No
   sessions captured yet."

3. Seed (node 22.12+). All of it is fictional: made-up folders under `/srv/fx029/`, no
   prompts from anyone. The result is 435 sessions in all. Each folder holds 86 or 87 of the 432 sessions, so a search for a folder name finds them. Do not use `seed-session.mjs`
   here: it makes a git repo under the home folder.

   ```sh
   GW=http://127.0.0.1:17297
   # one hook event as the client's hook endpoint receives it
   hook() {   # hook EVENT SESSION_ID CWD PROMPT
     curl -s -o /dev/null -X POST "$GW/_gateway/hooks/claude-code" \
       -H 'content-type: application/json' \
       -d "{\"hook_event_name\":\"$1\",\"session_id\":\"$2\",\"cwd\":\"$3\",\"transcript_path\":\"\",\"prompt\":\"$4\"}"
   }
   # 432 hooks-only sessions: one prompt hook each, no model call, in five folders
   folders="alpha-shop beta-notes gamma-api delta-cli epsilon-docs"
   for i in $(seq 1 432); do
     f=$(echo $folders | cut -d' ' -f$((i % 5 + 1)))
     hook UserPromptSubmit "fx029-none-$i" "/srv/fx029/$f" "fx029 prompt $i"
   done
   # B12: a session known only from one SessionEnd hook
   hook SessionEnd fx029-b12 /srv/fx029/b12 ""
   # E8: a session with SessionStart and SessionEnd
   hook SessionStart fx029-e8 /srv/fx029/e8 ""
   hook SessionEnd fx029-e8 /srv/fx029/e8 ""
   # plain, streamed, very long and errored calls, through the stub on 17296
   node docs/manual-checks/seed-call-kinds.mjs --gateway "$GW" \
     --ui http://127.0.0.1:17299 --stub-port 17296
   ```

4. Open `http://127.0.0.1:17299/`. Check every step at 1,440 px and at 2,560 px wide (confirm
   the width with `window.innerWidth`), in the light theme first, then switch the theme
   (header toggle) and check again in dark.

5. Cleanup, when the check is done:

   ```sh
   # stop the gateway (Ctrl-C in its terminal, or kill its process), then:
   rm -rf /tmp/fx029-data /tmp/fx029-config.yaml
   ```

## Widths and themes

- Widths: 1,440 px and 2,560 px. The amendment check used 1,440 × 900 and 2,560 × 1,400.
- Themes: light, then dark, each through the header toggle.
- Each step below is checked in both widths and both themes unless it says otherwise.

## Checks

Each check gives the expected result, then the result seen on 2026-11-22. All passed.

### Sessions list

1.1 **Pager (AC8, D25).** The list box is followed by a pager: `‹ Previous   Page N of M   Next ›`.
    Expected: the pager's two buttons and its label keep the same x positions on every page
    (Previous at 480 px, Next at 715 px at 2,560 px). Previous is disabled on page 1, Next on
    the last page. Seen: Next from page 1 to page 9 kept both buttons at those positions on
    every page and in a filtered list ("Page 2"). Previous was disabled on page 1, Next on
    page 9. The D25 check ran on ports 18295–18299, because the human's own check held
    17295–17299.

1.2 **Unfiltered paging (AC8).** Expected: pages 1, 3, 5 and 9 each cost one request, the
    URL reads `page=N` on page 2 and up, the list box scrolls to its top on a page change,
    and Back steps back through the pages. Seen (on the 435-session store, before D25): page
    3, 5, 1 and 9 each made one request; Back stepped 5 → 3 → 1; the box scrolled to its top
    on each change.

1.3 **Out-of-range page (E9).** Open `#/?page=99`. Expected: the last page is shown and the
    URL is corrected to its number. Seen: `page=99` landed on page 9 (35 rows) and the URL
    read `page=9`.

1.4 **Fixed box (AC8, D23).** Expected: the list box keeps its height on every page, and the
    pager's bottom edge is inside the visible window without scrolling the page. Seen: the
    pager's bottom was at 740 px of the 757 px visible height at 1,440 px wide, and at
    1,240 px of 1,257 px at 2,560 px wide. The box kept its height on every page.

1.5 **Filtered paging (AC3, AC8).** Expected: with a filter on, the pager reads `‹ Previous   Page N   Next ›` (no total); Next follows the server's next_after; opening `#/?kind=hooks&q=gamma&page=2` walks to page 2. Seen (on 2026-11-22, before D25): `‹ [1] ›` → `‹ 1 [2] ›` → `‹ 1 2 [3] ›`, and the numbers stayed after going back to 1; a 43-row search showed no pager; a `page=4` link walked to page 4. Seen (after D25): the filtered list showed "Page 2" with Previous and Next in the same places.

1.6 **Each filter (AC3).** Click All, Needs a look, Active and Hooks only in turn. Expected:
    each is marked pressed, the hash changes (`#/?kind=…`), and the list shows the server's
    rows for it. With fetch slowed, a filter change shows six skeleton rows and "Loading
    sessions…", then the rows. Seen: each filter set the hash and showed the server's rows;
    the slowed fetch showed six skeleton rows and "Loading sessions…" before them.

1.7 **Search (AC3, E7).** Type `gamma` in the search box and wait. Expected (about 87 rows, two pages): one request once
    typing settles, the hash reads `#/?q=gamma` through replaceState (the history length does
    not grow), and the box keeps focus. Seen (on the earlier 135-session store): one request after typing settled, `#/?q=gamma`,
    history length unchanged, focus kept.

1.8 **Filter plus search (AC3, AC2 via the browser).** With Hooks only and `gamma`, expected:
    `#/?kind=hooks&q=gamma`, the gamma-api rows only. Seen (on the earlier 135-session store, with Active): `#/?kind=active&q=gamma`, 26 rows.

1.9 **Reload and Back keep state (AC3).** Expected: Back restores the previous filter and
    search, and reloading `#/?kind=hooks&q=beta` restores both and loads the first page for
    them (the page position is not kept, D6). Seen (on the earlier 135-session store): Back restored Active and "gamma". Reloading
    `#/?kind=hooks&q=beta` restored both, with 26 rows.

1.10 **No match.** Type `zzz`. Expected: "no sessions match" in the list, and no skeletons
    left. Seen: "no sessions match".

1.11 **Skeletons (AC4).** With network throttled (or fetch slowed in the page), expected: a
    list load, a filter change and a page change (Next or Previous) show skeleton rows inside
    the list box, then the rows; the pager stays in place. Seen: a filter change showed six
    skeleton rows and "Loading sessions…" before the rows; before D25, each Load more fetch
    showed three skeleton rows.

1.12 **Paging to the end.** Expected: Next through every page reaches the last row with no repeated session, and Next is disabled on the last page. Seen: on the 435-session store, Next from page 1 to 9 ended on page 9 (35 rows) with Next disabled; on the earlier 135-session store, 50 → 100 → 135 rows, unique ids.

1.13 **B12 (AC5).** Expected: the B12 row (`fx029-b12`) shows `◇ hooks only`, not `● active`.
    The E8 row (`fx029-e8`, SessionStart and SessionEnd) shows `● active`. Seen: the
    end-only row showed ◇ "hooks only"; the start and end row showed ● active.

1.14 **Active empty after five minutes (AC5, E8).** Leave the seeded store alone for five
    minutes past the last hook, then filter Active. Expected: no row is active (the list is
    empty). Seen: at 14:11, past the five-minute window, `kind=active` returned 0 of 135 rows.

### Session page

2.1 **Story bar gap (AC9).** Expected: 16 px between the "Prompts and answers as Preview · Raw"
    bar and the first turn card, in both themes. Seen: 16 px in light and in dark.

2.2 **Long prompt and answer (AC10).** Open the "very long call" turn (the 60-line prompt and
    answer). Expected: each block is capped at 336 px and scrolls inside its box, in Preview and
    in Raw. The text width stays the same when the view switches (no width or height jump), and
    Copy stays in the card header. Seen: both blocks were 336 px boxes that scrolled inside, in
    Preview and in Raw. The text width was 1,009 px in both modes, the same as short blocks.
    Copy stayed in the header.

2.3 **Skeleton (AC4).** With the fetch slowed, expected: "Loading turns…" in the session page,
    then the page. Seen: "Loading turns…", then the page.

### Call panel

3.1 **Preview and Raw padding (AC1, D8).** Expected: Preview and Raw have padding `3px 9px`,
    the same as the panel's `.btn.sm` buttons, so they look balanced in their box. Their
    heights differ from Previous and Next (about 23.8 px against 26.6 px) because their font is
    still 11.5 px (D8 leaves the font alone); that is expected. Seen: both themes looked
    balanced, with the heights as recorded.

3.2 **Call steps and skeleton (AC4, AC10).** Open a call. Expected: "Loading call…", then the
    call. Calls step with Previous and Next. Seen: the skeleton showed, then the call; the
    steps worked.

3.3 **Removed fields (AC7).** Expected: the call panel opens and steps with no console
    errors, and no page code reads the nine removed exchange fields (D7). Seen: the call panel
    opened, stepped and showed no console errors, without the removed fields.

3.4 **No horizontal scroll.** Expected: no horizontal scroll at 1,440 px wide. Seen: none.

### Optional: API walk

The counts were recorded on the earlier 135-session store, with folders alpha-shop … epsilon-docs. With this recipe's 435 sessions the totals differ, and `kind=active` returns 0 once five minutes have passed. Read each response's `next_after` until it is `null`, and check that no `id` repeats.

```sh
API=http://127.0.0.1:17299/api/sessions
curl -s "$API"                              # 50 rows, next_after set
curl -s "$API?kind=active"                  # 134 rows: the end-only session left out
curl -s "$API?kind=hooks"                   # 132 rows
curl -s "$API?kind=look"                    # 1 row: the errored call
curl -s "$API?q=gamma"                      # 26 rows
curl -s "$API?kind=hooks&q=gamma"           # 26 rows
curl -s "$API?q=%_"                         # 0 rows: the search is literal
curl -s "$API?kind=active&limit=1"          # 134 rows over 134 pages, no repeats
curl -s "$API?kind=bogus"                   # 400
curl -s "$API?limit=0"                      # 400
curl -s "$API?q=$(printf 'a%.0s' $(seq 1 201))"   # 400: q is 201 bytes
```

## Fixed during the check

The UI check found three things and they were fixed on the branch:

- (a) The pager fell below the window. The box height is now `calc(100vh - 320px)`, with a
  minimum of 360 px, so the pager stays in view (D23).
- (b) The current page was nearly invisible in dark. It is now filled with `--on-accent` on
  `--accent-strong`, the pair `contrast.test.js` checks (D24). The numbered pager that showed
  it was later replaced by Previous / Next with a fixed-width label (D25).
- (c) A 500-row filtered scan timed out under the race detector, returning 504. The scan now
  stops before a new batch once it has spent 2 s in one request (D12).

## Known cost

On Linux and Windows, with classic scrollbars, a short prompt or answer block keeps an empty
15 px scrollbar strip at its right, inside its 16 px right padding (`scrollbar-gutter: stable`,
D22). It reads as a slightly wider right margin. It is the price of a text width that does not
shift when a long block gains a scrollbar.

## Record

- Date: 2026-11-22. Branch: `fix/029-ui-and-small-fixes`, checked at 8da69fe for the first pass
  and rebuilt for the amendment and D25 checks.
- Result: every check above passed in both widths and both themes. No console errors.
- Not seen in the browser: the "No matches in the newest N sessions yet" state. It needs more
  than 500 sessions or a 2 s scan; `app-list.test.js` and `TestSessionsPage_ScanCap` /
  `TestSessionsPage_ScanBudgetStopsEarly` cover it.
