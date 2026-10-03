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

# Recorded fixtures (spec 002, T19)

Recorded 2026-10-01, model `claude-opus-5-5`, Claude Code `2.1.286` with a subscription login,
in a scratch directory `/tmp/t19/work` holding a three-line `notes.txt`. Method: 001's
throwaway proxy, `Accept-Encoding: identity`.

- `tool_turn/response.sse`, `tool_turn/response.headers`: `recorded`. The streamed answer
  to "How many lines are in notes.txt? Use the Read tool.", ending in a `Read` `tool_use`.
  Body unchanged. Headers: `anthropic-*-id`, `traceresponse`, `cf-*`, `set-cookie`,
  hop-by-hop, `content-encoding` and `content-length` dropped; `request-id` set to `req_scrubbed`.
- `tool_turn/next_request.json`: `recorded`, trimmed. The next `POST /v1/messages` body,
  carrying the `tool_result`. `metadata.user_id` scrubbed; system text, tool descriptions
  and every non-assistant text except the recorded prompt replaced with placeholders; tools
  cut to `Read`; UUIDs, home paths and the local username outside assistant messages
  replaced; the `safeguards` client context trimmed. Assistant messages unchanged.
- `server_tool/websearch.sse`: `recorded`. The streamed response to "Search the web for
  the latest Go release and answer in one line.", holding `server_tool_use` and
  `web_search_tool_result`. Body unchanged; `encrypted_content` is opaque provider data.

Checked for keys, auth tokens, UUIDs, emails, paths and account names.

# Assembled fixtures (spec 002, T23, research Q8)

Built 2026-10-01 from the recorded T19 files above, by owner decision (Q8): each block's
events keep their recorded bytes, and only their arrangement is synthetic. The one edit
is the top-level `"index":N` of each block event, renumbered so the blocks run 0, 1, 2
in the order below (the parser orders blocks by index). Nothing new to scrub.

- `tool_order/response.sse`: `synthetic`, hand-built from recorded blocks. The envelope
  (`message_start`, `message_delta` with `stop_reason: tool_use`, `message_stop`) and
  the `Read` `tool_use` block with its `ping` come from `tool_turn/response.sse`. A
  recorded turn stops at its last `tool_use`, and `tool_turn`'s answer has no text, so
  the two `text` blocks around it are `server_tool/websearch.sse`'s blocks 3 and 7
  (plain text, no citations). Order: text, `tool_use`, text.
- `server_tool/response.sse`: `synthetic (assembled from recorded blocks)`. The
  envelope (`message_start`, `message_delta` with `stop_reason: end_turn` as recorded,
  `message_stop`), the `server_tool_use` block with its `ping`, and its
  `web_search_tool_result` come from `server_tool/websearch.sse`; the client `Read`
  `tool_use` block is spliced in from `tool_turn/response.sse`. The thinking and text
  blocks that followed the search are left out.
