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
