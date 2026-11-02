---
feature: 010-harness-hooks
---
# Research & decisions — 010-harness-hooks

## Open questions
<!-- Agents add a question here when they are blocked; the loop stops until you answer.
     Answer by changing "(open)" to "(answered)" and writing the answer underneath:
- **Q1** (open) T003 — <question> — options: A) … B) …
-->
- **Q2** (answered) T025 — make verify is red on the finished branch: TestPurity_NoHarnessEventNamesOutsideProfile fails because internal/ui/hub_hook_test.go (added in e09ef0e) names the Claude Code events "PreToolUse" and "PostToolUse"; the fix touches a test file outside T025's commit, so T025 cannot change it — options: A) T025 fix round edits internal/ui/hub_hook_test.go to build the event names from the claudecode profile constants, B) add a new chore task before T025 that does the same edit and reruns verify, C) add a narrow exemption for _test.go files in internal/ui to purity_test.go (weakens the rule for tests, needs a human decision)
  - **Answer:** None of 1–3. Use neutral names: build the test's hook events with a test-only mapper and made-up event names mapped to the pre and post phases, the way the internal/hooks tests and T012's UI tests already do (evt-post etc.). Don't import the claudecode profile into internal/ui tests: that would couple the UI to one client, which the agnosticism rule exists to prevent. Don't add an exemption. Make the edit in a fix round of the task whose commit added the test (e09ef0e), since hub_hook_test.go is in its scope. Then run make verify. (2026-11-02)

## Decisions
<!-- - D1 (date) — <decision> — why -->
- D1 (2026-11-01) — Hook type chosen: `http` for `gateway hooks print` — loopback POST needs no
  shell/curl, hook-down is non-fatal for both types (see N1), and bodies arrive as the raw JSON the
  gateway must store byte-for-byte (AC10). `command` hooks remain a documented fallback.

## Notes
- N1 (2026-11-01, T001 spike, AC1) — Claude Code 2.1.316, `claude -p` through a throwaway
  Python loopback listener that proxied /v1/messages to api.anthropic.com (logging
  `x-claude-code-session-id`) and logged hook POSTs. Not committed. Findings:
  - `tool_use_id`: present on PreToolUse and PostToolUse (all 16 Pre / matching Post observed,
    `toolu_...` ids). It equals the id of the `tool_use` block in the API response stream, so the
    join key holds.
  - `session_id` in the hook body equals `x-claude-code-session-id` byte-for-byte (observed on
    every request in every run). Both join keys pass; no BLOCKED.
  - Hook body fields: common `session_id, transcript_path, cwd, prompt_id, effort, permission_mode,
    hook_event_name, tool_name, tool_input, tool_use_id`; PostToolUse adds `tool_response,
    duration_ms`. `agent_id` / `agent_type` were absent in these main-thread runs (no sub-agent
    was spawned); the mapper must treat them as optional and they remain unverified here.
  - Latency: both types add only a few ms. Pre-to-Post gaps for the same tool (Read/Bash echo)
    were 40-60 ms for http, command and async variants alike, with the one 155-171 ms outlier being
    the first Bash call in every run. Hook POSTs arrived 6-8 ms apart when both types were
    configured. No measurable difference in total session time beyond run-to-run noise (11.8-15.7 s).
  - Gateway/endpoint down (connection refused on a dead port): http and command hooks both failed
    open; tool calls ran and the session finished normally (`is_error` false) for both types.
  - Async: `"async": true` was accepted on both http and command hooks; events still arrived and the
    session completed. No ordering guarantee was observed to change; AC21 (order-independence) covers it.
  - PermissionDenied: NOT observed. In `-p` default mode a disallowed Bash call produced a
    PreToolUse and a `permission_denials` entry (with `tool_use_id`) but no PermissionDenied hook;
    `--permission-mode auto` ran the command with no denial. Whether PermissionDenied carries
    `tool_use_id` is therefore unconfirmed; the mapper should read it as optional. Human to confirm
    in an interactive auto-mode session (task Manual line).
  - `input differs` (AC18): 0 of 16 PreToolUse events differed from the API-stream tool input
    (parsed JSON equality) with no other hook modifying input, so the flag should be rare.

## Human decisions
- 2026-11-01 T001 manual check passed: interactive auto-mode session on 2.1.316: SubagentStart/Stop and tool events inside the sub-agent carry agent_id and agent_type; sub-agent tool events carry their own tool_use_id; SubagentStart/Stop carry no tool_use_id; main-thread events have no agent_id; all events share one session_id; background sub-agent: the parent Agent call PostToolUse fires before the sub-agent tools run, so SubagentStop is the real end; PermissionDenied not observed because the model declined without calling the tool, so denied falls back to not run per AC16
- 2026-11-01 T007 accepted as it is (it was STOPPED scope): user-requested scope extension: TestCore_NoProviderOrClientIdentifiers in internal/core/purity_test.go now also scans non-test .go files in internal/hooks with the existing event-name list; verified a planted "PreToolUse" in internal/hooks/zz_probe.go fails it, the same probe in a _test.go file passes it, and make verify is green with the probe removed
- 2026-11-01 T009 accepted as it is (it was STOPPED scope): user-requested: Claude Code hook fixture moved to internal/clients/claudecode/testdata so no event names live under cmd/ (purity rule); cmd/gateway test reads it from there; AC9 fixed in the same round: with capture off, shutdown logs a hook ingest stopped line with the hook counters, covered by TestRun_CaptureOffLogsHookCounters
