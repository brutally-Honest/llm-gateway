# 042: Sessions list header, page errors and scroll

Covers [specs/042-sessions-list-ui/spec.md](../../specs/042-sessions-list-ui/spec.md) (AC1–AC4,
E1–E5): the pinned header, a failed page, and scrolling under live events, in Chrome, Lean and
Stark, light and dark, at 1,440 and 2,560 px. Results are also recorded in that spec's
`research.md`.

The automated parts are:

- `internal/ui/webtest/sticky-header.test.js`: the header is opaque in every style and theme,
  stacks above the rows, and has a line under it.
- `internal/ui/webtest/app-list.test.js`: `anErrorOnPageTwoKeepsThePagerAndShowsRetryInTheBox`,
  `retryReRequestsTheSamePage`, `previousAfterAPageErrorGoesBackToPageOne`.
- `internal/ui/webtest/sessions-page.test.js`: `aPageErrorKeepsThePagerAndRetryCallsOnRetry`,
  `aFilteredPageErrorDisablesNext`, `retryFocusMovesToTheListBoxWhenTheSkeletonReplacesIt`,
  `aLiveUpdateOfOneRowLeavesTheOtherRowsInPlace`, `aRowMovedToTheTopIsTheOnlyRowMoved`,
  `anUnchangedSessionIsNotRefilled`, `todayTilesAreKeptWhenTheTotalsDidNotChange`,
  `theErrorBoxIsKeptAcrossRedrawsWithTheSameError`.

## Setup

1. Use a store with more than 50 sessions, so the list has two pages. Use a copy of a real
   store, taken only with the owner's OK, and delete the copy afterwards. Never point a check
   at the running gateway (`llm-gateway.service`, ports 7195–7199). Never run `gateway doctor`
   or `gateway install` against the real HOME.

   ```sh
   mkdir -p ~/.cache/llm-gateway-042
   sqlite3 ~/.local/share/llm-gateway/gateway.db ".backup ~/.cache/llm-gateway-042/gateway.db"
   cp -a ~/.local/share/llm-gateway/blobs ~/.cache/llm-gateway-042/
   ```

2. Write a throwaway config. Every listener is on 17195–17199. Replace `<you>` with your
   home folder name.

   ```yaml
   listen_addr: 127.0.0.1:17197
   upstreams:
     anthropic:
       base_url: http://127.0.0.1:17196   # nothing listens: calls get a 502 and are still captured
   capture:
     enabled: true
     dir: /home/<you>/.cache/llm-gateway-042
   ui:
     enabled: true
     listen_addr: 127.0.0.1:17199
   effects:
     enabled: true
     network:
       enabled: false
       http_proxy_addr: 127.0.0.1:17198
       socks_proxy_addr: 127.0.0.1:17195
   ```

   Start it, then stop it when the checks are done:

   ```sh
   make build
   systemd-run --user --scope --unit=gw042 -p MemoryMax=6G bin/gateway -config <that file>
   systemctl --user stop gw042.scope
   ```

3. Live traffic (for check 3.1). This loop sends one call every 0.25 s through the throwaway
   proxy and rotates over three Claude Code session ids. Stop it with Ctrl-C.

   ```sh
   i=0; while true; do i=$((i+1)); curl -s -o /dev/null -X POST http://127.0.0.1:17197/anthropic/v1/messages -H 'content-type: application/json' -H 'user-agent: claude-cli/2.0.0 (external, cli)' -H "x-claude-code-session-id: 042-live-$((i % 3))" -H 'anthropic-version: 2023-06-01' -d '{"model":"claude-opus-5-5","max_tokens":10,"messages":[{"role":"user","content":"probe"}]}'; sleep 0.25; done
   ```

4. Browser: Chrome 156 in a headed window with its own throwaway profile, driven over the
   DevTools protocol by a scratch script. The Claude in Chrome extension was not connected
   (research D1). The same checks can be done by hand: DevTools device toolbar at 1,440×900
   and 2,560×1,265, the Settings dialog for Lean and Stark and for light and dark, and the
   Performance panel with CPU 4× slowdown.

5. Cleanup, when the checks are done:

   ```sh
   rm -rf ~/.cache/llm-gateway-042
   ```

## Widths and themes

Every check ran in all 8 cases: Lean and Stark × dark and light × 1,440×900 and 2,560×1,265
(029's sizes), except where a check says otherwise.

## Checks

Each check gives the expected result, then the result seen. Results are from the run in
`research.md`.

### Header

1.1 **Header readable while scrolling (AC1, E1).** Scroll the list box down 300 px. Look at the
    header. In DevTools, run `document.elementFromPoint` at the centre of each header cell.
    Expected: the header is opaque, rows never show through, the topmost element at each header
    cell is the header cell, and a line separates the header from the rows.
    Seen before the fix (546fd7f): in all 8 cases the topmost element was the row's title link
    (`a.stitle`), and header text overlapped row text. Lean's header background was see-through
    (`rgba(255,255,255,0.06)` dark, `rgba(9,9,11,0.04)` light).
    Seen after the fix (4514898): in all 8 cases the header cell is on top. Backgrounds: Lean
    dark `rgb(24,24,27)`, Lean light `rgb(255,255,255)`, Stark dark `rgb(14,14,15)`, Stark light
    `rgb(255,255,255)`. Screenshots show a clean header with its line.

### Page errors

2.1 **A failed page is recoverable (AC2, E2).** Start the throwaway gateway cold and open the
    list. Press Next. Stop the gateway 1 s into page 2's load. Start it again and press Retry.
    Expected: the error shows inside the list box with a Retry button. The pager stays, with
    "Page 2 of 2", Previous enabled and Next disabled (last page). The URL keeps `#/?page=2`.
    Retry after the restart shows page 2.
    Seen (all 8 cases): the error was in the box with Retry, and the pager showed "Page 2 of 2",
    Previous enabled, Next disabled, URL `#/?page=2`. Retry after the restart showed page 2's
    23 rows.
    Note: the error text for a gateway stopped mid-request is the browser's raw "Failed to
    execute 'json' on 'Response': Unexpected end of JSON input" (from `api.js`, older than 042).
    It is left as a follow-up, not part of this spec.

2.2 **A cold page 2 timeout (P2 as reported).** Restart the gateway cold and press Next at once.
    Expected: after the server's 5 s store timeout, "the store did not answer in time" shows in
    the box with Retry, and the pager stays.
    Seen (Lean dark, 1,440): at 5.4 s the error and Retry showed, with the pager at "Page 2 of
    2". Previous went back to page 1 (50 rows, URL `#/`). Each Retry sent a new request. Retries
    1 and 2 (10 s apart) also timed out while the store warmed. Retry 3 showed the 23 rows in
    0.5 s (warm-up speed is 030, research D7). Before the fix, the pager disappeared at 5.4 s.

### Live scrolling

3.1 **Scrolling stays smooth under live events (AC3).** With the live traffic loop running, scroll
    the list (pointer over the box) and the page (pointer over the header area), down and back up
    at 1,500 px/s, with CPU 4× slowdown. Expected: no task over 50 ms while scrolling, and a
    steady frame rate.
    Seen at CPU 4×, 1,440, dark (before → after):
    - Lean list: 44 long tasks, up to 101.7 ms → 0. 96.9 → 141.0 fps. Dropped frames 556 → 55.
    - Stark list: 43 long tasks, up to 76.8 ms → 0. 98.0 → 139.7 fps. Dropped frames 558 → 87.
    - Lean page: 19 long tasks → 0. 95.6 → 141.5 fps.
    - Stark page: 20 long tasks → 0. 98.9 → 140.2 fps.
    Unthrottled with live traffic after the fix, in all 8 cases (both themes, both widths, list
    and page): 143.2–143.9 fps on a 144 Hz display, with no long task.

3.2 **Nothing else changed (AC4).** Expected: the same columns, pager position, filters and
    paging.
    Seen: the same columns and pager position in every screenshot. `make verify` is green
    (235 web tests).

## Record

- Spec: [specs/042-sessions-list-ui/spec.md](../../specs/042-sessions-list-ui/spec.md). Results
  also in `specs/042-sessions-list-ui/research.md`.
- Fix commits: 9c682a0 (header), 63abd3d (page errors), 4514898 (live redraws). Spec notes:
  531ce32.
