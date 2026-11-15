# Side-call request fixtures (spec 018)

Five request bodies of one Claude Code session, in order: a turn, an away summary, a turn
whose prompt quotes both markers, a prompt suggestion, a turn after it.

- Shape: copied on 2026-11-15 from real captured requests (claude-cli 2.1.325, model
  claude-opus-5-5): a main-thread call for the turns, an away summary and a prompt
  suggestion. Top-level fields, `system` blocks, `max_tokens`, `thinking`,
  `output_config`, `context_management` and `cache_control` are as captured.
- The last message of 02 and 04 is Claude Code's own text, whole and unchanged.
- Redacted: the 12,426-character system prompt (a placeholder), the tools (Bash and Read
  kept, descriptions cut to their first line), `metadata.user_id` (device and session ids
  replaced, the account id dropped), `safeguards` (paths, rules and identity dropped). The conversation is invented; a real
  one is hundreds of messages.
- 018 research.md, Notes, lists what tells each kind apart from a turn.
