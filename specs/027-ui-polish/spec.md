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
