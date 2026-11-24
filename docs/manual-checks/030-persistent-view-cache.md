# 030: Persistent view cache

Covers [specs/030-persistent-view-cache/spec.md](../../specs/030-persistent-view-cache/spec.md):
the gateway serves the sessions list and a session's page at once after a restart, from a cache
on disk. In Chrome, Lean and Stark, light and dark, at 1,440 and 2,560 px. Results are also
recorded in that spec's `research.md`.

## Setup

1. Use a copy of a real store, taken only with the owner's OK. Never point a check at the
   running gateway (`llm-gateway.service`, ports 7195–7199).

2. Write a throwaway config with its own data folder. Every listener is on 17195–17199:
   API 17197, UI 17199, HTTP proxy 17198, SOCKS 17195, and a fake upstream on 17196.

3. Start it once and wait until the background pass is done: `gateway_ui_cache_rebuild_seconds`
   must be set on the API's `/metrics`. Then stop it. The view cache is now filled.

4. Run the checks in Chrome. Stop the gateway and delete the copy when they are done.

## Checks

Each check gives the expected result, then the result seen. Results are in "Result 2026-11-24".

### Restart

1.1 **Page 2 at once after a restart.** Restart the gateway. Within 2 s of the UI port
    accepting, open the UI and press Next straight away.
    Expected: page 1 shows without a wait, page 2 shows with no error, and the pager reads
    "Page 2 of N".

### Pixel match with main

2.1 **Same pages as main (8 cases).** For each of Lean and Stark, light and dark, at 1,440 and
    2,560 px: the sessions list page 1 and the largest session's page. Take the same screenshots
    on the build before this change (main) on the same store.
    Expected: each pair is pixel-identical (`compare -metric AE` gives 0), and the page text is
    the same in both builds.

### Folder deleted while running

3.1 **Cache folder removed.** While the gateway runs, delete `<capture.dir>/view-cache/`.
    Expected: the list keeps answering. Opening a session rebuilds it and recreates the folder.
    The next restart rebuilds every entry.

## Result 2026-11-24

Recorded from `research.md`, "Step 5: after" and D31.

- 1.1: the tab opened 0.24 s after the port bound. Page 1 data arrived 132 ms after navigation.
  Next showed page 2 in 11 ms, with no error.
- 2.1: 16 screenshot pairs, all pixel-identical (`compare -metric AE` = 0). The 1,440 px cases
  ran in a headless Chrome with a throwaway profile, because the extension window would not
  resize (D31). The page text was identical at both widths.
- 3.1: pages kept answering from memory (43–50 ms). The largest view rebuilt in 2.0 s and
  recreated `view-cache/<build>/v/`. The next start's pass rebuilt all 70 memos and 70 views.

## Record

- Spec: [specs/030-persistent-view-cache/spec.md](../../specs/030-persistent-view-cache/spec.md).
  Results also in `specs/030-persistent-view-cache/research.md`.
- Date: 2026-11-24. Decision for the 1,440 px runs: D31.
