# Task-notification fixtures (spec 018, research D7)

One Claude Code session, in order: a typed prompt's hook and the call it starts (answered
with a tool call, so the turn stays open); two background tasks' notice hooks sent in the
same moment and the one call that answers both while the turn is open (answered with text,
so the turn ends); a third notice and the call that answers it after the turn ended; a typed prompt's hook that no
captured call answers (a real hooks-only turn).

- Shape: copied on 2026-11-15 from captured ones (claude-cli 2.1.325): the hooks are
  UserPromptSubmit payloads with `"prompt": "<task-notification>…"`; the answering call
  carries the notice as a `role: "system"` message after the tool result, opening with
  Claude Code's own "[SYSTEM NOTIFICATION - NOT USER INPUT]" text, whole and unchanged.
  Request bodies share the side_calls fixtures' redacted top level.
- Redacted: the session id (`SESSION`, set by the test), cwd, prompt id, the scratchpad
  folder; task ids, tool-use ids, output paths and summaries are invented. The tool call is
  the one `internal/protocols/anthropic/testdata/tool_turn/response.sse` answers with.
