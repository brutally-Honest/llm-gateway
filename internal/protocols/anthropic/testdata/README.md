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
- `stream.events.golden.json` (spec 002): the canonical events the parser makes of
  `stream.sse` answering the replay test's request (regenerate with `go test
  ./internal/protocols/anthropic -run TestParse_GoldenStream -update`, then review the
  diff).
# Synthetic fixtures (spec 002, research Q8)

Built by hand on 2026-09-30 from Anthropic's documented Messages API shapes, not
recorded: none of these can be produced on demand through Claude Code. No identifiers
to scrub; message and tool IDs are made up, and `request_id` is `req_scrubbed`.

- `non_streaming/request.json`, `non_streaming/response.json`: `synthetic`. A
  non-streamed `POST /v1/messages` request (model `claude-sonnet-5`, one `Read` tool,
  a `cache_control` on the system entry) and its JSON response: a `text` block, then a
  `tool_use`, `stop_reason: tool_use`, and usage with a `cache_creation` breakdown.
  `non_streaming/events.golden.json` is the canonical events the parser makes of the
  pair (regenerate with `go test ./internal/protocols/anthropic -run
  TestParse_NonStreaming -update`, then review the diff).
- `non_streaming/response.sse`: `synthetic`. The streamed equivalent of
  `non_streaming/response.json`: the same text and `tool_use` blocks (the tool input
  split over `input_json_delta` events), stop reason and usage, as SSE.
- `error/response_429.json`: `synthetic`. The error body of a `429`, type
  `rate_limit_error`.
- `error/stream_error.sse`: `synthetic`. A stream that starts a text block and ends
  with an `error` event, type `overloaded_error`, with no `message_stop`.
