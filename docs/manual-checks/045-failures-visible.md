# 045: Failures visible

Covers [specs/045-failures-visible/spec.md](../../specs/045-failures-visible/spec.md):
capture losses are counted and shown, and a missing blob shows as content unavailable
and breaks nothing else. In Chrome, Lean and Stark, light and dark, at 1,440 and 2,560 px.
Results are also recorded in that spec's `research.md`.

## Setup

1. Never point a check at the running gateway (`llm-gateway.service`, ports 7195–7199) or
   at the real HOME.

2. Write a throwaway config with its own data folder and its own HOME. API 17197, UI 17199,
   and a fake upstream on 17196 (`upstreams.anthropic.base_url: http://127.0.0.1:17196`).
   Effects are off. Run the gateway inside `systemd-run --user --scope -p MemoryMax=4G`,
   with HOME set to the throwaway folder.

3. Seed fictional data. The fake upstream answers every `/v1/messages` with a JSON message
   whose text is over 4 KiB. Send calls to `http://127.0.0.1:17197/anthropic/v1/messages`
   with `user-agent: claude-cli/…` and `x-claude-code-session-id`: three calls in two
   sessions.

4. When done, stop the gateway, the fake upstream and the browser, and delete the throwaway
   folder.

## Checks

Each check gives the expected result, then the result seen. Results are in "Result 2026-11-26".

### Capture loss

1.1 **Blob write failure.** Run `chmod -R a-w <data>/blobs`, then send one call in a new
    session.
    Expected: `/healthz` answers 200 with status degraded, lost 1, blob_write_failures 1,
    reason "blob writes failed". `gateway_blob_write_failures_total 1`. One "blob not stored"
    log line with the hash. The call is listed in `/api/sessions` with its metadata. Its body
    endpoint answers 404 body_not_stored and logs "body unavailable".

1.2 **Doctor.** Run `HOME=<throwaway> gateway doctor`, with the config copied to
    `<throwaway>/.config/llm-gateway/config.yaml`.
    Expected: the API listener passes. Then "warn Capture health 1 lost since …: blob writes
    failed (…counts…)", the fix line, and the link `…#check-capture-health`.

### Missing blob

1.3 **Missing blob.** Delete one raw body blob and one text-block blob.
    Expected: `/api/sessions` answers 200, and the other session's body still answers 200.
    The affected body answers 404 body_not_stored. After a rebuild (restart with `view-cache/`
    deleted, since deleting a file does not change the store version), the session view shows
    `"answer":"[content unavailable]"` and still answers 200.

### Hook flood

1.4 **Hook flood.** Post 1,000 hook events at once to `/_gateway/hooks/claude-code`.
    Expected: every answer is 200. Stored plus `gateway_hook_events_dropped_total{reason="queue_full"}`
    equals 1,000. Health counts the drop.

### Banner

1.5 **Eight cases (Lean and Stark, light and dark, 1,440 and 2,560 px).** Open the UI while
    capture is degraded.
    Expected: a one-line red banner under the header row: "capture degraded: N lost since
    start — reason details". Role `status`. The details link opens `/api/health` in a new tab
    (target _blank, rel noopener). Round corners in Lean, square and mono in Stark. No
    horizontal scroll. It cannot be dismissed.

## Result 2026-11-26

- The Claude in Chrome extension was not connected, so 1.5 ran in a headless Chrome with a
  throwaway profile, driven over the DevTools protocol on 127.0.0.1:9333 (as in 042 and 043).
- 1.1: `/healthz` 200 `{"status":"degraded",…,"lost":1,"counts":{…"blob_write_failures":1},"reason":"blob writes failed"}`.
  Metric 1, store failures 0. One "blob not stored" line. The call was listed (status 200,
  model claude-sonnet-5-5, usage 1200/900). Body endpoint 404 body_not_stored, with one
  "body unavailable" line. Only the response body failed: the request body and the text block
  had the same content as earlier calls, so their blobs already existed.
- 1.2: "warn Capture health 1 lost since 2026-11-26T00:36:37Z: blob writes failed (hook events
  dropped 0, captures dropped 0, store writes failed 0, blob writes failed 1)", then the fix
  line and the link `…/install.md#check-capture-health`. API and UI listeners pass. The other
  checks fail as expected in a throwaway HOME with nothing installed.
- 1.3: list 200 and the other body 200 after the raw body blob was deleted. The affected body
  404. After the text-block blob was deleted, the cached view kept the text until a rebuild.
  After a restart with `view-cache/` deleted, all four sessions (they share that text) answered
  200, and the view showed `"answer":"[content unavailable]"`.
- 1.4: with the default `hooks.queue_size` (256), all 1,000 were answered 200 and all 1,000
  were stored. The store kept up, so nothing was dropped. Restarted with `hooks.queue_size` 8
  and workers 1: 1,000 answered 200, 961 stored, `gateway_hook_events_dropped_total{reason="queue_full"}`
  39, 39 "hook event dropped: queue full" log lines, and `/healthz` 200 degraded, lost 39.
  961 + 39 = 1,000.
- 1.5: in all 8 cases the banner showed inside `header#shell-head` with "capture degraded:
  40 lost since start — hook events dropped, blob writes failed" and the details link (href
  `/api/health`, target _blank, rel noopener, role status). Lean: 4px radius, Geist. Stark:
  0px radius, Geist Mono. Colours: Lean light rgb(185,28,28) on rgba(220,38,38,.07). Lean dark
  rgb(248,113,113) on rgba(248,113,113,.11). Stark light rgb(196,46,56) on rgb(252,239,239).
  Stark dark rgb(239,107,112) on rgb(42,20,22). Width 1,362 px at 1,440 and 2,482 px at 2,560.
  No horizontal scroll.

## Record

- Spec: [specs/045-failures-visible/spec.md](../../specs/045-failures-visible/spec.md).
  Results also in `specs/045-failures-visible/research.md`.
- Date: 2026-11-26. Decisions: D3, D4, D10, D14, D18.
