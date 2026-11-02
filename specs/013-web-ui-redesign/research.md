---
feature: 013-web-ui-redesign
---
# Research & decisions — 013-web-ui-redesign

## Open questions
<!-- Agents add a question here when they are blocked; the loop stops until you answer.
     Answer by changing "(open)" to "(answered)" and writing the answer underneath:
- **Q1** (open) T003 — <question> — options: A) … B) …
-->

## Decisions
- D1 (2026-11-02) — Session title is the first prompt with reminders removed — no title or cwd is stored, and generated titles would add client-specific extraction.
- D2 (2026-11-02) — `#/ungrouped` and `#/exchanges/{id}` stay; Ungrouped gets a quiet link under the sessions table and the drawer gets its own URL — nothing captured becomes unreachable.
- D3 (2026-11-02) — "Previous call" means the earlier call in the same thread, using the timeline's existing message-hash match — literal session order gives false diffs around sub-agent calls.
- D4 (2026-11-02) — Today's totals count calls started today in browser-local time — sessions cross midnight.
- D5 (2026-11-02) — `ran-not-reported` shows `… pending`; `no harness data` gets a sixth, neutral chip `– no hooks` — the full state stays on hover.
- D6 (2026-11-02) — From the mockup, session filters and rail Show options are in; rate limit in Health and Copy as curl are out.
- D7 (2026-11-02) — A thread is main if it has a user prompt, unless hooks or a parent Agent call identify it as a sub-agent — this survives compaction and /clear; the accepted limit is that a sub-agent in a session with no hooks and no captured Agent call can show as a false turn.
- D8 (2026-11-02) — Calls before the first main prompt go in an unnumbered "before first prompt" card; `#/ungrouped` and `#/exchanges/{id}` show level SESSIONS in the key bar with only Backspace to Sessions.
- D9 (2026-11-02) — The Claude Code profile marks two side calls, the quota probe and title generation; they go in "before first prompt" or "side calls" cards, never start a turn or become the title, and still count in calls, errors and tokens; unrecognised calls follow D7 — chosen by the user after the quota gap in D7's rule.
- D10 (2026-11-02) — Replaces D1: the session title is the latest stored generated title, falling back to the first prompt with reminders stripped when there is none or it failed; rows update live — the user's choice once the stored title was found.
- D11 (2026-11-02) — Amends D9: side-call errors are left out of the Errors column and Has errors, and still show red in their card and drawer; side calls still count in calls, tokens and today's totals — the quota 429 would otherwise mark most sessions as errored.
- D12 (2026-11-02) — The context line plots main-thread calls only; side calls stay in the duration strip, drawn muted. E18 accepted as is.
- D13 (2026-11-02) — Side-call classification is computed server-side in the Claude Code profile and exposed in the API, never re-derived in the browser — the CLI can then adopt the same rule without duplicating it.
- D14 (2026-11-02) — Confirmed: the stats row's errors exclude side calls and its context is the last main-thread call's; the drawer's trend plots main-thread calls only.
<!-- - D1 (date) — <decision> — why -->

## Notes
- 2026-11-02 — Follow-up, out of scope for 013: update `gateway sessions` and other CLI output to use the side-call rule (D13), which also removes the error-count difference in E23.
- 2026-11-02 — Session-grouping check, read-only against the local store (~/.local/share/llm-gateway): all 106 /v1/messages exchanges carry X-Claude-Code-Session-Id and it equals the stored session_key every time. Quota, title and main calls share one id and list as one session (for example 50bb7cb8: 429 quota, title, then 4 main calls = 6 exchanges). No capture grouping bug, so nothing to log as one. The mockup's separate "quota" row and "4 calls" for that session do not match the store.
- 2026-11-02 — Correction to D1's reason: a generated title is stored, in the title-generation call's response (`{"title":"Python 20-second foreground sleep"}`). The earlier statement that no title is stored was wrong; no working directory was checked again. D1's choice stands until the user changes it.
- 2026-11-02 — The quota probe is a 429 in 11 of the 13 sessions that have model calls, so counting side calls in errors means most sessions show at least 1 error and match Has errors.
- 2026-11-02 — Ungrouped holds only HEAD /api/hello (22 exchanges).
- 2026-11-02 — Mockup contrast, computed from its CSS: the key bar's level label (white on dark-theme accent #58a6ff) is 2.53:1 and fails AC34's 4.5:1. The chip and muted pairs checked pass; several light-theme pairs only just pass (warn 4.52, ok and accent 4.56).
- 2026-11-02 — The mockup lives outside the repo at ~/Downloads/gateway-ui-v4.html.
