---
feature: 042-sessions-list-ui
---
# Research & decisions — 042-sessions-list-ui

## Progress
- 2026-11-24 Step 1 (reproduce and profile): all three reproduced on the store copy; P3 profile blames live-event redraws (full table layout), not the nested scroller.

## Measurement setup
- Store copy: `sqlite3 ~/.local/share/llm-gateway/gateway.db ".backup ~/.cache/llm-gateway-042/gateway.db"`
  and `cp -a` of `blobs/`, taken 2026-11-24 07:31 +05:30: 3,918 exchanges, 1.3 GB database,
  4.0 GB blobs, 51 to 100 sessions (two pages).
- Throwaway gateway: built from the commit under test, `systemd-run --user --scope -p MemoryMax=6G
  <bin> -config <scratch>/gw.yaml`; proxy 127.0.0.1:17197, UI 127.0.0.1:17199, upstream
  127.0.0.1:17196 (nothing listens, so calls get a 502 and are still captured), effects
  network listeners off, `capture.dir` on the copy. `llm-gateway.service` was never touched.
- Browser: Google Chrome 156, a headed window with its own throwaway profile, driven over
  the DevTools protocol by a scratch script (D1). Viewports 1,440×900 and 2,560×1,265
  (029's sizes) by device-metrics override, scale 1. The display runs at 144 Hz, so an
  unhindered frame is 6.9 ms.
- Scroll: `Input.synthesizeScrollGesture`, mouse wheel, 1,500 px/s, down the whole list (or
  page) and back up. "List" scrolls with the pointer over the list box; "page" over the
  header area.
- Frame times: `requestAnimationFrame` intervals in the page during the scroll. Long tasks:
  renderer main-thread `ThreadControllerImpl::RunTask` over 50 ms in a Chrome trace
  (`devtools.timeline`, `...timeline.frame`, `cc`, `gpu`, `viz`, `blink`), cross-checked with
  a `longtask` PerformanceObserver. Dropped frames: the trace's `DroppedFrame` events.
- Live traffic (D2): a loop sends one call every ~0.25 s through the throwaway proxy, rotating
  over three Claude Code session keys, so the page gets `session` events as it does while
  a real session runs. "CPU 4×" is `Emulation.setCPUThrottlingRate` 4, standing in for a
  slower or busier machine (D2).

## Step 1: reproduction

### P1: the pinned header
After `scrollTop = 300` on the list, `document.elementFromPoint` at the centre of each header
cell returns the row's `a.stitle` (its `::after` stretches over the row), in all 8 cases
(Lean and Stark × dark and light × 1,440 and 2,560): rows paint over the header.

| Style, theme | Header background (computed) | Header on top |
|---|---|---|
| Lean dark | `rgba(255,255,255,0.06)` (`--panel`) | no |
| Lean light | `rgba(9,9,11,0.04)` (`--panel`) | no |
| Stark dark | `rgb(14,14,15)` (`--card`, opaque) | no |
| Stark light | `rgb(255,255,255)` (`--card`, opaque) | no |

Causes: `.sp-list tbody tr:has(a.stitle) { position: relative }` (027 C6a, the whole row
opens the session) makes every row a positioned box that paints after, so over, the
`position: sticky; z-index: auto` header cells; and Lean's `.sp-list th { background:
var(--panel) }` is see-through. Stark has only the first cause, which is why the opaque
background alone did not save it.

### P2: page 2 after a restart
Restart the throwaway gateway, open the list, press Next at once:

| t (s) | List box | Pager |
|---|---|---|
| 0.3 | rows of page 1 | shown, "Page 1 of 2" |
| 0.4 | "Loading sessions…" | shown, "Page 2 of 2", URL `#/?page=2` |
| 5.4 | "the store did not answer in time" | **hidden** |

`curl /api/sessions?offset=50` right after a restart: 504 after 5.000 s twice, then 200 in
3.04 s. The 504 is `storeTimeout` (5 s, `internal/ui/api_sessions.go:25`) on a cold
offset query, which is 030's. The UI side: `drawPager` sets `show = !state.error && ...`
(`internal/ui/web/sessions-page.js:578`), so any error hides the pager, and the list box
shows only the message, with nothing to retry.

### P3: scroll profile
DOM: 1,630 elements on the page with 50 rows (not large). Numbers are per scroll run
(down and back up).

Without live traffic, unthrottled (rAF fps, long tasks, main-thread busy %, dropped frames):

| Run | fps | p95 frame | max frame | long tasks | main busy | dropped |
|---|---|---|---|---|---|---|
| Lean 1,440 list | 143.9* | 7.0 | 8.1* | 0 | 8.2%* | 4* |
| Lean 1,440 page | 143.6 | 7.0 | 14.1 | 0 | 8.4% | 7 |
| Lean 2,560 list | 143.9 | 7.0 | 8.1 | 0 | 8.2% | 4 |
| Lean 2,560 page | 144.0 | 7.0 | 7.7 | 0 | 8.7% | 3 |
| Stark 1,440 list | 143.9 | 7.0 | 8.5 | 0 | 5.3% | 2 |
| Stark 1,440 page | 144.0 | 7.0 | 7.5 | 0 | 5.1% | 2 |
| Stark 2,560 list | 143.9 | 7.0 | 8.1 | 0 | 5.1% | 3 |
| Stark 2,560 page | 144.0 | 7.0 | 7.7 | 0 | 5.0% | 0 |

\* The first Lean 1,440 list run stalled (one 165 s frame, the window lost the
foreground); the row shows the 2,560 run's numbers in its place, and the 4× runs below
repeat Lean 1,440.

With live traffic:

| Run | fps | p95 | max | frames >33 ms | long tasks | main busy | dropped |
|---|---|---|---|---|---|---|---|
| Lean 1,440 list | 139.9 | 7.1 | 27.8 | 0 | 0 | 17.5% | 70 |
| Lean 2,560 list | 140.0 | 7.1 | 20.8 | 0 | 0 | 18.6% | 51 |
| Stark 1,440 list | 137.2 | 7.1 | 28.1 | 0 | 0 | 14.4% | 119 |
| Stark 2,560 list | 137.1 | 7.1 | 34.8 | 1 | 0 | 15.0% | 85 |
| Lean 1,440 page | 140.1 | 7.0 | 20.8 | 0 | 0 | 18.9% | 31 |
| Stark 1,440 page | 137.2 | 7.1 | 27.8 | 0 | 0 | 14.7% | 50 |

CPU 4×, 1,440:

| Run | fps | p95 | max | frames >33 ms | long tasks (max) | main busy | dropped |
|---|---|---|---|---|---|---|---|
| Lean list, no live | 143.7 | 7.0 | 13.9 | 0 | 0 | 23.3% | 12 |
| Stark list, no live | 143.7 | 7.0 | 20.8 | 0 | 0 | 10.9% | 7 |
| Lean page, no live | 142.9 | 7.0 | 34.7 | 1 | 0 | 22.4% | 10 |
| Stark page, no live | 144.0 | 7.0 | 7.7 | 0 | 0 | 11.2% | 3 |
| **Lean list, live** | **96.9** | 14.2 | 138.9 | 44 | **44 (101.7 ms)** | 56.6% | **556** |
| **Stark list, live** | **98.0** | 20.9 | 111.5 | 45 | **43 (76.8 ms)** | 46.8% | **558** |
| Lean page, live | 95.6 | 27.8 | 118.1 | 21 | 19 (86.9 ms) | 56.9% | 252 |
| Stark page, live | 98.9 | 27.8 | 90.3 | 23 | 20 (66.2 ms) | 45.9% | 246 |

Where the time goes (Stark 1,440 list, live, CPU 4×, main thread, ms per run): script
1,373 (max 22 per event), Layout 1,373 (max 44), style 648 (max 15), Paint 802. Without live
traffic the same run has script 223, Layout 4.6, style 2.2, Paint 3.

Attribution (unthrottled live trace, Stark 1,440 list): the SSE handler (`api.js:91`, which
calls app.js `onSession` → `store.set` → the page's full `redraw`) ran 84 times for 283 ms;
42 Layouts each dirtied 2,515 of 2,572 layout objects (the whole page) for 282 ms. A
MutationObserver over 10 s of live traffic (~40 events) counted:

| Region | Nodes added / removed | Attribute writes |
|---|---|---|
| Sessions `tbody` | 1,900 / 1,900 (all 50 rows re-inserted 38 times) | 6,240 |
| Today tiles | 546 / 546 (rebuilt every event) | 0 |
| Other | 78 / 78 | 390 |

So each `session` event: `drawRows` re-inserts every row when any row's place changed
(`body.replaceChildren(...want)`), `fillRow` rewrites `className`, `aria-selected`,
`hidden` and the token bar's `flex-grow` on every row even when unchanged, and `redraw`
rebuilds the today tiles and the footer. An auto-layout table relays every cell when its
rows are re-inserted, so each event costs a full table layout.

**Verdict (D4):** the scroll itself is cheap. Composited, it holds 144 fps with no long
tasks in both styles, even at CPU 4×. The lag is the live-event redraw landing during the
scroll: script, then a full-page style and layout. Not to blame: the nested scroller, the
pinned header, DOM size. Lean's glass (D5) roughly doubles GPU and compositor time
(GPU main 1,057 ms vs 454 ms in Stark for the 2,560 list run; Layerize 170 vs 9 ms; Paint 39
vs 0.7 ms) but adds no long task and no dropped frames on its own.

## Decisions
- **D1** 2026-11-24: The Claude in Chrome extension was not connected (two tries). Rather than
  stop, Chrome is driven over the DevTools protocol by a scratch script, in a headed window
  with its own throwaway profile (never the user's). This also gives real traces. The UI check
  (Step 5) uses the same driver; its screenshots stand in for watching.
- **D2** 2026-11-24: The profile includes live traffic, because the user's gateway gets
  `session` events while they read the list (their own Claude Code sessions run through it).
  The store copy alone gets none, and it scrolled smoothly. CPU 4× stands for a slower or
  busier machine, because this one runs at 144 Hz with headroom. The 50 ms long-task target
  is judged at CPU 4× with live traffic, the worst case measured.
- **D3** 2026-11-24: P1 is two causes (stacking and Lean's see-through token); the fix covers
  both. The header takes `--card-solid` (the opaque twin both styles define) and stacks above
  rows with a `z-index`. Lean's tint is kept by drawing `--panel` over that opaque base.
- **D4** 2026-11-24: P3 is blamed on the live-event redraw, so Step 4 fixes only that. The
  nested scroller is not to blame, so the list keeps scrolling inside its box (029 fix 7); the
  "scroll with the page" question does not arise.
- **D5** 2026-11-24: Lean's `backdrop-filter` costs GPU time but makes no long task or dropped
  frames on its own; it is a look 027 chose and is left alone here (non-goal).
- **D6** 2026-11-24: P2's 504 is the server's 5 s store timeout on a cold offset query. Making
  it fast is 030; this change only makes the error recoverable in the UI.
