# 043: Fast rows first

Covers [specs/043-fast-rows-first/spec.md](../../specs/043-fast-rows-first/spec.md):
with an empty view cache (right after an install), the sessions list shows its rows at once
and fills in their details as they are built. In Chrome, Lean and Stark, light and dark, at
1,440 and 2,560 px. Results are also recorded in that spec's `research.md`.

## Setup

1. Use a copy of a real store, taken only with the owner's OK. Never point a check at the
   running gateway (`llm-gateway.service`, ports 7195–7199).

2. Write a throwaway config with its own data folder. Every listener is on 17195–17199:
   API 17197, UI 17199, HTTP proxy 17198, SOCKS 17195, and a fake upstream on 17196.

3. Before each check, stop the gateway and delete `view-cache/` in its data folder, so the
   cache is empty as after an install. Then start it and open the UI within 2 s of the UI
   port accepting.

4. Stop the gateway and the browser, and delete the copy when done.

## Checks

Each check gives the expected result, then the result seen. Results are in "Result 2026-11-25".

### Rows at once

1.1 **Eight cases (Lean and Stark, light and dark, 1,440 and 2,560 px).** Open the list.
    Expected: rows show within half a second with start time, folder and status. Every other
    cell shows a quiet grey placeholder. Within about 5 s every row on the page is filled in.
    No column changes width while that happens. Most rows keep their height. A row that turns
    out to need a look gains that line.

### Page 2

1.2 **Next before fill-in.** Press Next as soon as page 1 shows.
    Expected: page 2's rows show at once with placeholders and fill in within 5 s. No error.
    The URL reads `page=2`.

### Filters

1.3 **Filters while building.** Click Needs a look, then Active, then Hooks only, then search
    "gateway".
    Expected: each answers at once. While sessions are still being built, Needs a look and the
    search show "N sessions still being checked" under the list, then the full result (the
    same rows a warm cache shows). Active and Hooks only are exact at once.

## Result 2026-11-25

Recorded from `research.md`, D20 and D21. The Claude in Chrome extension was not connected, so
all cases ran in a headless Chrome with a throwaway profile, driven over the DevTools protocol
on 127.0.0.1:9333 (the fallback 042 used).

- 1.1: the page was opened 50–75 ms after the UI port bound. Rows showed 185–250 ms after
  navigation, with 47–48 of 50 pending. All were filled in at 2.2–2.3 s. Column widths changed
  by 0 px in all 8 cases. At 1,440 px, 2 rows of 47 changed height (shorter rows,
  −19 and −26 px). At 2,560 px, 8–9 rows changed height (+14 px for a row that gained its
  "needs a look" line, −7 px for a shorter row).
- 1.2: page 2 showed 53–57 ms after Next, with all 20 rows pending. It was filled in at
  3.2–4.3 s. No error. The URL read `#/?page=2`. Columns 0 px. At 1,440 px, 2 rows changed
  height; at 2,560 px, 13 rows did, for the same reasons as 1.1.
- 1.3: Needs a look first showed 8 rows and "N sessions still being checked" (20 at the
  start), then all 20 at about 4.3 s. Active showed no session, because none was active.
  Hooks only showed 1 at once. The search showed 29 rows within 0.3 s. These match the
  warm-cache answers (20 and 29). Columns 0 px in every case.
- First pass, before the fix in `11a72e5`: columns moved up to 656 px, and pending rows were
  a line too tall (D20).

## Record

- Spec: [specs/043-fast-rows-first/spec.md](../../specs/043-fast-rows-first/spec.md).
  Results also in `specs/043-fast-rows-first/research.md`.
- Date: 2026-11-25. Decisions: D20 (layout), D21 (placeholder heights).
