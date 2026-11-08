# 013: web UI redesign (T015)

Covers AC33 (every action has a mouse and a keyboard path through real `<button>` or
`<a>` elements, icon-only controls labelled), AC34 (4.5:1 text contrast in both themes, and
no status by colour alone), the drawer's focus and live behaviour (AC26, AC28, E11) and the
2,000-call render budget. The automated parts are `TestWeb_ActionsAreButtonsOrLinks` in
`internal/ui/web_test.go` and `allTextPairsMeet4_5InBothThemes` in
`internal/ui/webtest/contrast.test.js`. This check covers what they cannot see.

## Setup

From the repo root, with a scratch data directory so the real store is not touched:

```sh
XDG_DATA_HOME=/tmp/ui013-data make run      # in the background
```

Open <http://127.0.0.1:7199>. Use a session with traffic: run `claude` through the proxy as
in [010-hooks-ui.md](010-hooks-ui.md), or send requests to `http://127.0.0.1:7197/anthropic`.

Steps 13 to 15 need one session with 2,000 captured calls. Stop the gateway if it is running,
then start it against the seed's stub upstream, on the scratch data directory only:

```sh
XDG_DATA_HOME=/tmp/ui013-data \
  GATEWAY_UPSTREAMS_ANTHROPIC_BASE_URL=http://127.0.0.1:7196 make run   # in the background
node docs/manual-checks/seed-calls.mjs --live                          # leave it running
```

A clean run takes about a minute and prints `seeded 2000 calls` once all of them are captured.
If the UI answers 504 to the seed's capture poll, the seed stops early and exits 1, at about
1,500 to 2,000 calls. The 504 means the UI's 5 s store timeout ran out while the session list
was built cold, so the gateway is up and only slow; run the seed again on a fresh data
directory. `--live` then adds a call every 2 s, which keeps the session active and gives steps
14 and 15 their live traffic. Stop it with Ctrl-C after step 15. With
the setting above, the stub answers every call and nothing reaches a provider. Without it,
the first call goes to the configured upstream with a fake key, and the seed stops there. A
turn is 10 calls, most with a tool use, and one call in 50 is an error. The session is the
newest one on the Sessions page.

For step 13's numbers, `node docs/manual-checks/cold-open.mjs` cold-opens that session in
headless Chrome (fresh profile, no extensions) and prints, per run, the time to the first
turn card, the time until the card count settles, the cards, the DOM nodes and the list
rebuilds. Run it while `--live` is sending: only an active session opens its live stream.

Use the keyboard only for steps 1 to 7. Do not touch the mouse there.

## Steps

| # | Do | Expected |
|---|----|----------|
| 1 | Load `#/`. Press Tab from the top of the page until focus leaves it. | Every control takes focus in a visible order: the theme buttons, the session filter and each session's link. Each focused control shows a focus outline. |
| 2 | On the sessions list, press ↑ and ↓, then Enter on a session. Press `/`, type a word, then Esc. | ↑/↓ move the selection, Enter opens the session. `/` puts the cursor in the filter box and typing goes there, not to a shortcut. Esc leaves the box, and the shortcuts work again. |
| 3 | On the turns page, press `j` and `k`, `e` (next error), `t` (next tool), `h` (hooks), then `/` and Esc. | Each key moves or toggles as the key bar says. The key bar shows the level TURNS with position and the shortcuts for it. |
| 4 | With focus on the turns page, press Enter. | The drawer opens on the first call of the current turn. Focus moves to the drawer's title. |
| 5 | In the drawer, Tab through ↑ previous, ↓ next and Close. Use ↑ and ↓ to step calls, then `t`. | Tab reaches each button with a visible outline. Each step changes the call and the URL. `t` closes the drawer and returns to the turn, focus included. |
| 6 | Open a call again with Enter, then press Esc. | The drawer closes and focus returns to the element that opened it (the call link or the turn heading). |
| 7 | Press Backspace on the turns page, then open `#/ungrouped` from the sessions page with Tab and Enter. | Backspace goes back to the sessions list. The ungrouped page shows the key bar at level SESSIONS with only Backspace to Sessions. |
| 8 | Open DevTools, then the Accessibility tree. Inspect the theme buttons, the drawer's Close and ↑/↓ buttons, the copy buttons and the status chips. | Every button and link has a name. Icon-only controls carry an aria label such as "Close call". A chip's label starts with its word, for example "failed", and carries the full state. |
| 9 | Repeat steps 1 to 6 with the mouse only: click each session, turn, call link, rail item, theme button, previous, next and Close. | Every action done by key also works by click, and each click target is a real button or link. |
| 10 | Pick Light, then Dark, in the header. Reload. Repeat steps 3 and 4 in each theme. | The chosen theme applies at once and survives a reload. Text, chips, error marks, the drawer and the key bar stay readable in both themes. |
| 11 | Contrast spot check in both themes with DevTools' colour contrast readout. Check the chip text, muted text, error text, drawer section headings and the key bar's level label. | Each text pair is at least 4.5:1 against its background. The automated test covers the token pairs; this confirms the rendered result. |
| 12 | With the rendering option that emulates no colour (grayscale) in DevTools, look at every status in both themes: the chips, error marks on turns and rail items, error counts, and the duration strip's error bars. | Every status reads without colour: a glyph, a word or a count. The duration strip's error bars are colour only today (their words are in the hover text), so a bar that cannot be told apart is logged as a bug in `research.md`. |
| 13 | Open the 2,000-call session. In the console, note `performance.now()` just before the page's data request and just after the turns list is painted (or use the Performance panel from request to first paint). | The page shows all calls, with no cap, within 2 s of the data arriving. Scrolling and the turns page stay responsive. |
| 14 | With the 2,000-call session open and live traffic arriving, open the drawer on a call in the middle. Let new calls arrive. Scroll the drawer, then press ↓ on the drawer. | The drawer keeps its call, its focus and its scroll position while the list behind it updates. A pending chip changes in place to its final chip. The list does not auto-scroll while the drawer is open. |
| 15 | With the drawer open on a call, let a live batch rebuild the list, then press Esc. | The opener is gone, so focus lands on the current turn's heading, not on the page body (E11). |
| 16 | Paste a drawer URL (`#/sessions/{id}/call/{exchangeId}`) into a new tab and press Esc. | The drawer closes by replacing the URL with the session URL. Back does not return to the drawer. |

## Result

Record for each step: pass, or the step number and what you saw. A failed step is a bug in
this feature; log it in `specs/013-web-ui-redesign/research.md` rather than changing the
expectation.

## Cleanup

Stop the seed and the gateway, then remove the scratch data directory `/tmp/ui013-data`.
