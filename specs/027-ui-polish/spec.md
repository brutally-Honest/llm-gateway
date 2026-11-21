---
feature: 027-ui-polish
kind: feat
status: draft
version: 1
created: 2026-11-21
supersedes:
branch: feat/027-ui-polish
---
# UI polish before v1

Styles, theming and small UI changes, each proposed by Karthik, shown in light and dark at
1,440 and 2,560 px, and accepted one at a time. Built outside the agent loop. One line per
accepted change; decisions are in `research.md`.

## Accepted changes
- **C1** — Sessions list: no Sources column, Work and Tools in one Work cell, status as a
  glyph alone in a narrow first column (word on hover and for screen readers).
- **C2** — Two styles, Lean (default) and Stark, picked with the theme in a header Settings
  dialog and remembered; one shared dark base (darker) and one shared light base, violet
  accent, red only for failures; Stark uses bundled Geist fonts (ADR 0020).
- **C3** — Call details as a full-height panel docked on the right: one line of facts, links
  to each section, one scroll with nothing scrolling inside it; context said once with a bar
  of its parts; carried-over messages folded; a failed call's health said first.
- **C4a** — Session page as chapters in one page scroll: a wider sticky turns list, tabs
  led by the story, a model chip per turn, prompt and answer cards with one Preview/Raw
  switch and a copy icon, file diffs expanding in place in Files, every fact said once,
  loaders while data loads, and an even scrollbar strip on both sides so nothing shifts.
- **C6a** — Lean restyled as neutral glass: near-black (#0a0a0b) or near-white page, green
  as its only accent, frosted cards, small corners, layered shadows and Geist; Stark keeps
  violet on the same dark page. Sessions list: the day's totals as tiles, a token bar per
  row, a whole row opens its session; an open dialog holds the page and closes on a click
  outside.
- **F1** — The saved theme and style apply before the first paint, so a refresh never shows
  the other theme for a moment.
- **C6b** — Hourly sparklines on the sessions and model calls tiles, from per-hour counts and
  peaks the server sends with the day's totals.
- **C7a** — Decluttered header and pages: a funnel logo that links home (also the tab icon),
  a round green or red stream dot, a larger gear, no key bar (shortcuts on the ? key), no
  header underlines, breadcrumbs that stop at the parent; no ended badge, lone model, empty
  cost lines, Model label, all-normal capture facts or all-unknown client column; folded
  headers; the call panel docked under a usable header, not greyed by its own dimming, with
  its stop reason said once; Lean's context bar telling cache read from input; settings
  opening without a ringed option; turn list marks as glyphs and no empty scrollbar strip.
- **C8** — A session's turns run newest first, in the turn list and the story alike.
- **C7b** — Sessions list trimmed: "today" said once in the heading, today's starts as times,
  status glyphs only for active, failed and hooks-only rows, no zero counts, no needs-a-look
  prefix, no API-eq. label or unknown cost line, one dash for a hooks-only row's tokens, a
  hooks-only title without its time, and the unassigned table's Label and Client columns only
  when they vary; Settings closes on a click outside; the turn list pins where it starts.
- **C9** — Each turn is one card: header, prompt, "How it got there" and final answer.
