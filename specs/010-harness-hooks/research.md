---
feature: 010-harness-hooks
---
# Research & decisions — 010-harness-hooks

## Open questions
<!-- Agents add a question here when they are blocked; the loop stops until you answer.
     Answer by changing "(open)" to "(answered)" and writing the answer underneath:
- **Q1** (open) T003 — <question> — options: A) … B) …
-->

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
