# 040: Incremental live updates, manual check

Covers the incremental live update path in [specs/040-incremental-live-updates/research.md](../../specs/040-incremental-live-updates/research.md).
An open session page shows new model calls, hook events and effects within half a second of
them being stored, without flicker or jumps, and ends up identical to a fresh reload. In
Chrome, Lean and Stark, light and dark, at 1,440 and 2,560 px. Results are also recorded in
that spec's `research.md`.

## Setup

1. Use a copy of the real store, taken only with the owner's OK. Never point a check at the
   running gateway (`llm-gateway.service`, ports 7195–7199).

   ```sh
   mkdir -p ~/.cache/llm-gateway-040
   sqlite3 ~/.local/share/llm-gateway/gateway.db ".backup ~/.cache/llm-gateway-040/gateway.db"
   cp -a ~/.local/share/llm-gateway/blobs ~/.cache/llm-gateway-040/
   ```

2. Write a throwaway config with its own HOME and config folder. Every listener is on
   17195–17199: API 17197, UI 17199, HTTP proxy 17198, SOCKS 17195, and a fake upstream on
   17196 that answers a short streamed reply.

3. Start the throwaway gateway inside a memory-capped scope, so the real service is not
   touched:

   ```sh
   systemd-run --user --scope -p MemoryMax=4G <throwaway gateway command and config>
   ```

4. Browser: Chrome 156 in a headed window with its own throwaway profile, driven over the
   DevTools protocol on port 9333 by a scratch script. Set the viewport with a device-metrics
   override.

5. The check uses the largest session: 672 calls after the check's own calls, 28 turns. Open
   it at `#/sessions/<id>`. Send one call first so the session is active and the page opens
   its live stream.

6. Cleanup, when the checks are done: stop Chrome, the throwaway gateway and the fake
   upstream, and delete the copy. Nothing may listen on 17195–17199 or 9333.

   ```sh
   rm -rf ~/.cache/llm-gateway-040
   ```

## Widths and themes

Every case ran in all 8 combinations: Lean and Stark × dark and light × 1,440×900 and
2,560×1,265.

## Steps per case

Each case follows the same steps. The page is scrolled so a turn in the middle of the list
is at the top of the window.

1. Set the theme and style, open the session page, and wait for the first view.

2. Send 25 inputs one at a time, in this order: `x x x x h x x x x e x x x x h x x x x e x x x x h`
   (x = model call, h = hook event, e = effect):
   - 20 model calls: the session's last request, replayed with its session header.
   - 3 hook events: PreToolUse, each with a new tool id.
   - 2 effects: an HTTP request through the effects proxy.

3. For each input, record:
   - Time from the input being stored (a call: its parse visible in the database; a hook or
     effect: its POST answered) to the first change of the page after the next live view event.
   - Movement of the mid-list turn, in px.
   - Layout shift, from PerformanceObserver `layout-shift` entries without recent input.
   - Turn elements removed and not put back.

4. Take the page's text, reload, take the text again, and compare line by line. Relative
   times are masked.

## Expected

- Every input appears in under 500 ms.
- 0 px movement of the mid-list turn.
- 0 layout shift.
- No turn is re-created.
- The page equals the reload.

## Result 2026-11-25

Branch `feat/040-incremental-live-updates`, HEAD `4d1e3a0`. Times are in ms, from the stored
input to the page change.

| Case | Calls (ms) | Hooks (ms) | Effects (ms) | Max move | Layout shift | Turns re-created | Same as reload |
|------|-----------|-----------|-------------|----------|--------------|------------------|----------------|
| Lean Dark 1,440 | 323-398 | 330-355 | 276-282 | 0 px | 0 | 0 | yes (2,879 lines) |
| Lean Dark 2,560 | 311-378 | 314-379 | 268-274 | 0 px | 0 | 0 | yes |
| Lean Light 1,440 | 323-371 | 323-335 | 289-305 | 0 px | 0 | 0 | yes |
| Lean Light 2,560 | 320-383 | 329-334 | 274-315 | 0 px | 0 | 0 | yes |
| Stark Dark 1,440 | 323-408 | 336 | 274-306 | 0 px | 0 | 0 | yes |
| Stark Dark 2,560 | 326-380 | 324-363 | 273-274 | 0 px | 0 | 0 | yes |
| Stark Light 1,440 | 316-365 | 323-365 | 265-267 | 0 px | 0 | 0 | yes |
| Stark Light 2,560 | 310-371 | 323-346 | 285-296 | 0 px | 0 | 0 | yes |

All 200 inputs (160 calls, 24 hooks, 16 effects) appeared 265–408 ms after being stored.
Nothing moved or flickered, and every case matched its reload.

Before 040, a call took about 2.6 s and a hook about 2.3 s on this session.

## Cleanup record

Chrome, the throwaway gateway and the fake upstream were stopped. Nothing listens on
17195–17199 or 9333.

## Record

- Spec notes: [specs/040-incremental-live-updates/research.md](../../specs/040-incremental-live-updates/research.md).
- Commits on the branch: f43d1c8 (oracle, skipped), dac7ad8 (incremental live updates),
  e1bf8a7 (background builds keep live sessions), 2cf864a (replay every stored session through
  the oracle), 4d1e3a0 (docs).
- Date: 2026-11-25.
