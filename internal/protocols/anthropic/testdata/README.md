# Golden fixture

- Recorded: 2026-09-26, model `claude-sonnet-5`, Claude Code subscription login,
  prompt "Say hello in five words." (claude -p).
- How: a throwaway recording proxy (not committed) at ANTHROPIC_BASE_URL, forwarding to
  api.anthropic.com with `Accept-Encoding: identity`; body saved byte-exact.
- `stream.headers`: status line, then one `Name: value` per line, sorted.
- Scrubbed: the org-ID and workspace-ID headers (`anthropic-*-id`), `traceresponse`,
  `cf-*`, `set-cookie` and hop-by-hop headers dropped; `request-id` set to `req_scrubbed`.
  Checked for keys, auth tokens, UUIDs, emails and account names.
- The data lines keep Anthropic's trailing space padding; the replay test relies on it
  staying byte-exact.