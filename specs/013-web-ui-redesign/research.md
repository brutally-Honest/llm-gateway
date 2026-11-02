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
- D15 (2026-11-02) — The server computes one chip per tool call from state and outcome and exposes it on each tool call; the digest's tool_states counts those chips; list and timeline read that field and neither derives it — T003 counted raw states, so a failed tool showed as ok in the list. This supersedes the plan's browser-side chip derivation; implemented out of task order, before T011, on the user's instruction.
<!-- - D1 (date) — <decision> — why -->

## Notes
- 2026-11-02 — Follow-up, out of scope for 013: update `gateway sessions` and other CLI output to use the side-call rule (D13), which also removes the error-count difference in E23.
- 2026-11-02 — Session-grouping check, read-only against the local store (~/.local/share/llm-gateway): all 106 /v1/messages exchanges carry X-Claude-Code-Session-Id and it equals the stored session_key every time. Quota, title and main calls share one id and list as one session (for example 50bb7cb8: 429 quota, title, then 4 main calls = 6 exchanges). No capture grouping bug, so nothing to log as one. The mockup's separate "quota" row and "4 calls" for that session do not match the store.
- 2026-11-02 — Correction to D1's reason: a generated title is stored, in the title-generation call's response (`{"title":"Python 20-second foreground sleep"}`). The earlier statement that no title is stored was wrong; no working directory was checked again. D1's choice stands until the user changes it.
- 2026-11-02 — The quota probe is a 429 in 11 of the 13 sessions that have model calls, so counting side calls in errors means most sessions show at least 1 error and match Has errors.
- 2026-11-02 — Ungrouped holds only HEAD /api/hello (22 exchanges).
- 2026-11-02 — Mockup contrast, computed from its CSS: the key bar's level label (white on dark-theme accent #58a6ff) is 2.53:1 and fails AC34's 4.5:1. The chip and muted pairs checked pass; several light-theme pairs only just pass (warn 4.52, ok and accent 4.56).
- 2026-11-02 — The mockup lives outside the repo at ~/Downloads/gateway-ui-v4.html.
- 2026-11-02 — Correction to the line above: the user copied the mockup to specs/013-web-ui-redesign/gateway-ui-v4.html, and the spec now points there.
- 2026-11-02 — Temporary deviation from .claude/skills/al-implement/LOOP.md, approved by the user: the implementer prompts for T010–T016 add "Run go test ./internal/core -run TestCore_NoProviderOrClientIdentifiers before committing." LOOP.md says to pass only its listed prompts. T005 and T009 each went red on this purity test after committing, because implementers ran narrower tests. The deviation holds until the kit runs the purity test per task.
- 2026-11-02 — Correction to the line above: for T011-T016 the prompt line is now "Run .claude/scripts/loop.sh verify before committing.", replacing the purity test line, also a temporary deviation from LOOP.md approved by the user. T010 went red on lint, which the purity line does not run.

## Build policy for T011-T016 and the branch review
Set by the user on 2026-11-02.
1. Mechanical failures (lint, gofmt, test-fixture wording, failing tests in the task's own code) get the normal fix round automatically, no stop. Fix rules still apply: never split or encode words, never touch the purity list or test, never change an existing test's expectation without logging why in this file.
2. Spec or AC conflicts: don't stop. Implement the spec as written, log the conflict and a proposal under "Decisions for Karthik", and continue.
3. Plan drift: plan.md's chip text is stale (CR-001's eight edits were never applied). For chips, the code is the source of truth: the server computes `chip`, the browser only displays it. Don't stop for plan drift; log each one under "Plan fixes pending".
4. Stop only for: a fix round that fails twice, anything that would change storage or the CLI, or a destructive git operation.
5. Stop once before the branch review with one batch: commits, "Decisions for Karthik" and "Plan fixes pending".

## Decisions for Karthik
- 2026-11-02 — T005: the exchange API's `new_count` counts only the request's messages, while the timeline's `call.new` also counts the reply, so the two can differ by one and AC23 could disagree with other views. Proposal: count the same way in both.
- 2026-11-02 — T004: call facts cause later exchanges to be resent after a late commit, and the expected event counts changed in three stream tests (CommitOutOfRowIDOrderStillReachesPage, LiveOutOfOrderInsertsAtSortedPosition, LiveConvergesWithReloadAndCLI). The added live traffic is unmeasured. Proposal: confirm the behaviour in the branch review.
- 2026-11-02 — T006: ungrouped exchanges count toward today's calls and tokens but not sessions; the spec does not say. They are only HEAD /api/hello today, so no effect yet. Proposal: keep.
- 2026-11-02 — T008: the new turn, chip and stats tests passed on their first run, so they were never seen failing. Proposal: mutation-check them in the branch review.
- 2026-11-02 — T010: `createSessionsPage` DOM code has no tests, only pure functions do, so the manual check is its only cover; the ungrouped link reads the first 200 and shows "N+" beyond that, so it is not always exact. Proposal: keep both, and cover the page in the manual check.
- 2026-11-02 — T003: the digest reads content more than once per session; T016's memo only helps on a cache hit. Proposal: confirm with the T016 benchmark.

- 2026-11-02 — T011: AC16 folds reminders behind "+N reminders" in the browser, but D13 says the browser never classifies by message text and the API has no reminder field, so `splitReminders` in model.js matches `<system-reminder>` blocks on a copy of the prompt text. Proposal: have the server (ClientRules) return the prompt split into body and reminders, and drop the browser regex.
- 2026-11-02 — T011: the call link `#n · duration` points at `#/sessions/{id}/call/{exchangeId}`, which reloads the page until T013 adds the drawer; hook lines are placed on the card holding their tool, else by start time. Proposal: keep.
- 2026-11-02 — T012: the context line is drawn as one column per main-thread call, not a connected line (no SVG under the DOM stub and the CSP forbids inline styles, so heights are set through the CSSOM). The AC35 guard bans `%` in JS, so heights are in rem. Show options render after the toolbar in the DOM (CSS places the rail left) because existing tests take the first `input` as the filter box; `h` toggles the Hooks inline option. Proposal: keep, or turn the columns into an SVG polyline later.

## Plan fixes pending
- 2026-11-02 — CR-001's eight edits were approved but never applied to plan.md: Summary, Approach, a new "Tool call chips" Design paragraph, Session digest, Web structure, AC coverage (`TestChips_*` under AC1 and AC18, `hasGapsMatchesHooksOnlySession` under AC6, AC13 row rewritten), Test strategy, and Changelog. Exact text: changes/CR-001.md. The loop shows CR-001 as applied, which is wrong.
- 2026-11-02 — plan.md's AC13 row names `sixStatesMapToGlyphAndText`; the test is now `sixChipsMapToGlyphAndText`.
- 2026-11-02 — T011: web_test.go guards named the removed session-view.js, model-chip.js and the ToolCard badge code (`pendingBadgeShown(call, result)`); they now name timeline-page.js, `chipNode(row.chip)`, `row.agent` and model.js. webtest/tool-card.test.js dropped "the pending badge follows pendingBadgeShown's answer" (the badge is now the server's chip, tested in chips.test.js) and its three hook-section tests now call `toolRowNode` with the same expectations. `pendingBadgeShown` in entries.js stays, still tested in entries.test.js, but nothing imports it now.
