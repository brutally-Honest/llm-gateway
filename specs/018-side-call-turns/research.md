---
feature: 018-side-call-turns
---
# Research & decisions — 018-side-call-turns

## Open questions
<!-- Answer by changing "(open)" to "(answered)" and writing the answer underneath. -->

## Decisions
- D1 (2026-11-15) — Karthik: a fix branch built by hand, no agent-loop kit (no /al-*
  commands), so spec.md stays a draft with no approval stamps and there is no tasks.md.
- D2 (2026-11-15) — Evidence (below): both kinds send the same body as a real main-thread
  call (system prompt, 28 tools, max_tokens, thinking, metadata keys, output_config,
  context_management, safeguards). Only two things differ: the last message, and two
  `anthropic-beta` flags. The rule reads the last user text, as the quota and title rules
  already read user texts: the text is Claude Code's fixed instruction, the profile
  already gets it (`IsSideCall(userTexts)`), and the beta list changes with every Claude
  Code release and holds nothing that names the call's purpose. Rejected: matching
  `fallback-credit` / missing `afk-mode` (fragile, and the interface carries no headers).
- D3 (2026-11-15) — The marker must be the *last* user text, and only its start is
  matched ("The user stepped away and is coming back." / "[SUGGESTION MODE:"), so a real
  prompt that quotes either one later in its text, or a request whose history happens to
  carry one, stays a turn. In all 41 stored calls the marker is the request's last
  message; no later real call carries it in its history. Known limit: a prompt the user
  types starting with one of those exact openings is read as a side call.
- D4 (2026-11-15) — Names: "away summary" and "prompt suggestion", beside the existing
  "quota probe" and "title generation" (`SideCallName`).
- D5 (2026-11-15) — Cached views (AC5): the session-view cache (`ui.TimelineCache`) and the
  digest cache (`ui.digestCache`) live in memory only, keyed on the session's store version
  (exchange count, max row, parsed count, hook count); nothing about a view is stored on
  disk or in the browser (localStorage holds only the theme and the reply mode), and the
  API sends no ETag or max-age. A gateway restart empties both, so every session is rebuilt
  with the new rule on first open. There is no cache version to bump; none is added.

## Notes
### Evidence: the captured calls (2026-11-15, the real store, read-only)
Found by the content hash of each marker's user message:
- Away summaries: 30 calls across 9 sessions; prompt suggestions: 11 calls across 5
  sessions; 10 sessions in all. Every one is client `claude-code`, `POST /v1/messages?
  beta=true`, model `claude-opus-5-5`, 29 of 30 away and all 11 suggestions status 200 (one
  away summary 499, a client cancel).
- The marker is the last message of the request, a user message whose `content` is a plain
  string (no blocks, no `cache_control`):
  - away: `The user stepped away and is coming back. Recap in under 40 words, 1-2 plain
    sentences, no markdown. Lead with the overall goal and current task, then the one next
    action. …`
  - suggestion: `[SUGGESTION MODE: Suggest what the user might naturally type next into
    Claude Code.]` then about 1,400 characters of instructions, ending `Reply with ONLY the
    suggestion, no quotes or explanation.`
- Everything before it is the main thread's conversation as of the last answer (778
  messages for the latest away summary against 776 for the real call before it): the
  call is a fork of the main thread, made to reuse its prompt cache.
- Body fields, compared with the real call just before each (session 2fff4047, calls
  KM6H4SDU… and 6OHMGS5G…): identical keys (`context_management, max_tokens, messages,
  metadata, model, output_config, safeguards, stream, system, thinking, tools`);
  `max_tokens` 128000, `stream` true, `thinking` adaptive, `output_config.effort` medium;
  `system` the same three blocks (billing line `cc_version=2.1.325.a25; cc_entrypoint=cli`,
  the identity line, the 12,426-character prompt); 28 tools, same names; `metadata` holds
  `user_id` only; no `tool_choice`.
- Headers: the same names and values (`User-Agent: claude-cli/2.1.325 (external, cli)`,
  `X-App: cli`, `Anthropic-Version: 2023-06-01`, `X-Claude-Code-Session-Id` the session's),
  except `Anthropic-Beta`, where both side calls add `fallback-credit-2026-06-01` and drop
  `afk-mode-2026-01-31`.
- Replies: text only, never a tool call (2 away summaries also carry thinking).
- Effect today, session 9980399c (121 calls): 13 turns, of which turns 3, 8, 10 and 13 are
  away summaries and turn 12 a suggestion; its row counts 13 prompts, 119 main calls and 2
  side calls (quota probe, title generation).
