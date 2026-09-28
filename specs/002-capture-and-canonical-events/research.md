---
status: draft
spec: ./spec.md
plan: ./plan.md
tasks: ./tasks.md
---

# 002 — Research

The feature's working memory: doubts, roadblocks and limits, with their answers.

What belongs here, when to escalate an answer to `spec.md`, `plan.md` or an ADR, and
the never-delete rule: `PLAN.md` §10 and `AGENTS.md`. Don't restate them here.

---

## Q1 — Does Claude Code ask for `gzip` or `br` on non-streaming `/v1/messages` calls?
- Status: answered     Level: flow
- Blocks / shapes: nothing yet (tasks.md not written); shapes the spec's `br` skip rule
  and AC41
- Context: 2026-09-27. The parser decodes only `identity` and `gzip` in the captured
  copy; any other `Content-Encoding` is stored raw and marked `unsupported_encoding`.
  The committed golden stream was recorded with `Accept-Encoding: identity` forced by
  the recording proxy, so it says nothing about what Claude Code itself sends.
- Question: which `Accept-Encoding` does Claude Code send on `/v1/messages`, streaming
  and not, and so which `Content-Encoding` does Anthropic answer with?
- How to resolve: log `Accept-Encoding` on real Claude Code `/v1/messages` requests
  through the gateway, in API-key and subscription mode.
- Answer: open.
- Outcome: if it's `br`, the skip becomes the common case, and decoding `br` for parsing
  needs a dependency and so an ADR.
- 2026-09-27 (owner): Answered by a manual mitmproxy capture:
  - Claude Code sends `Accept-Encoding: gzip, deflate, br, zstd`.
  - Anthropic answers a streamed `POST /v1/messages` with
    `Content-Type: text/event-stream` and `Content-Encoding: gzip`. Compressed SSE is the
    normal case, not an edge case.
  - `HEAD /api/hello` is answered with no `Content-Encoding`.
  - Decision: the parser decodes the captured copy for `identity`, `gzip`, `deflate`
    (zlib-wrapped and raw), `br` and `zstd`; `unsupported_encoding` covers anything
    else. Forwarded bytes are never touched. A truncated compressed body decodes as far
    as it can, and the exchange is `partial`.
- 2026-09-27 Outcome (→ spec): the Anthropic parser's Decoding rules are rewritten; ADR
  0004 adds `andybalholm/brotli` (pure Go), and `zstd` reuses the blob library; the
  "decoding `br`, `zstd` or `deflate`" out-of-scope line and the spec's gzip/br open
  question are removed. AC41 is replaced by AC43 `TestParse_ContentEncodings`; AC31
  `TestParse_GoldenStreamGzip` and the `gzip` subtest of AC41
  `TestParse_TruncatedStreamPartial` are added (ACs renumbered).

## Q2 — Which Anthropic content-block and delta types does the parser map?
- Status: answered     Level: technical
- Blocks / shapes: the spec's Anthropic parser `tool_call` / `tool_result` mapping, the
  stream delta rules; AC32, AC36, AC37
- Context: 2026-09-27. The spec maps provider-executed tools to `tool_call` /
  `tool_result` with `executed_by: provider`, and spells out stream reassembly per
  delta type. The type names had to be checked against Anthropic's current docs rather
  than recalled.
- Question: which block and delta type names exist today, and does every
  `*_tool_result` block carry a `tool_use_id`?
- Answer: checked on 2026-09-27 against:
  - https://platform.claude.com/docs/en/api/messages (request content block types):
    `text`, `image`, `document`, `search_result`, `thinking`, `redacted_thinking`,
    `tool_use`, `tool_result`, `server_tool_use`, `web_search_tool_result`,
    `web_fetch_tool_result`, `code_execution_tool_result`,
    `bash_code_execution_tool_result`, `text_editor_code_execution_tool_result`,
    `tool_search_tool_result`, `container_upload`. Every `*_tool_result` type listed
    has a `tool_use_id`.
  - https://platform.claude.com/docs/en/agents-and-tools/mcp-connector (beta, header
    `mcp-client-2025-11-20`): `mcp_tool_use` (`id`, `name`, `server_name`, `input`) and
    `mcp_tool_result` (`tool_use_id`, `is_error`, `content`).
  - https://platform.claude.com/docs/en/build-with-claude/streaming: deltas
    `text_delta` (`text`), `input_json_delta` (`partial_json`), `thinking_delta`
    (`thinking`), `signature_delta` (`signature`). A `server_tool_use` block streams
    its input as `input_json_delta`; its `web_search_tool_result` arrives whole in
    `content_block_start`. `message_delta` usage counts are cumulative.
  - https://platform.claude.com/docs/en/build-with-claude/citations: `citations_delta`,
    each carrying one citation to append to the current `text` block's `citations`.
- Outcome: no change needed. The spec's names and the `_tool_result` suffix rule match
  the docs. The code-execution, bash, text-editor and tool-search result types are
  covered by the suffix rule. `redacted_thinking`, `search_result` and
  `container_upload` fall under the existing block kinds or `unknown`, and are left to
  plan.md.
- 2026-09-27 (owner): correction: the mapping belongs in the spec, not plan.md.
  `redacted_thinking` → `reasoning` with `redacted: true`, its `data` kept (AC39
  `TestParse_RedactedThinkingKept`). `search_result` and `container_upload` → `unknown`
  in v1, stated under Out of scope. After renumbering, the ACs above are AC33, AC37 and
  AC38.

## Q3 — Does Claude Code resend an assistant message's blocks unchanged?
- Status: open     Level: flow
- Blocks / shapes: nothing yet (tasks.md not written); shapes AC45 and the spec's
  hashed-payload rule
- Context: 2026-09-27. The canonical hash makes turn N's reassembled assistant message
  dedup with its copy in turn N+1's request. That holds only if the harness resends
  the same content. Harnesses commonly add `cache_control` to recent blocks, and could
  drop or rewrite others (for example thinking blocks). The spec leaves wire-only
  annotations out of the hashed payload.
- Question: apart from `cache_control`, does Claude Code change an assistant message's
  blocks when it resends them?
- How to resolve: in the manual smoke test, compare turn N's response content with the
  matching message in turn N+1's request, byte for byte after canonical encoding.
- Answer: open.
- Outcome: if it rewrites content, AC45 needs reshaping (→ spec).
- 2026-09-27 (owner): how the exclusion is declared (the question itself stays open):
  the Anthropic adapter declares its hash-excluded fields (`cache_control`) through a
  `HashExcludedFields()`-style method on the parser seam, and core's canonical encoding
  applies that list without naming a wire field. The same exclusions apply to the new
  `system_hash` and `tools_hash`. AC45 is now AC48 after renumbering.
- 2026-09-28 (owner): Q3 stays open and doesn't block approval. It is answered by a new
  step in the manual Claude Code smoke (AC58): in a session with at least 3 turns, run
  `gateway dump` on turn N and N+1 and record whether turn N's assistant message and
  its resent copy share one content hash; that result is Q3's answer. AC48 is verified
  against fixtures only. If the hashes differ, a follow-up fix (not this spec) adjusts
  the canonical encoding or the adapter's hash exclusions.
- 2026-09-28: after the review-gap edits the ACs are renumbered. AC48 (fixture dedup)
  is now AC50, and the manual Claude Code smoke that answers this question is AC61.
  Q6's AC45 `TestParse_ReasoningRequestedFlag` is now AC46.
- 2026-09-28 (owner): hash exclusions now apply only to the top-level keys of a
  content block, a system entry or a tool definition, never inside tool input or tool
  result content (AC52). Related doubt for the same smoke: the Messages API allows
  `cache_control` on blocks nested in a `tool_result`'s content. If Claude Code puts
  it there, that resent `tool_result` would not dedup across turns. The AC61 step
  compares assistant messages; compare a resent `tool_result` too while there.

## Q4 — Why is Claude Code's `HEAD /api/hello` labelled `unknown`?
- Status: answered     Level: limit
- Blocks / shapes: the spec's Docs bullet (`docs/clients/claude-code.md`); AC45
- Context: 2026-09-27. 001 research Q1 found `HEAD /api/hello` carries
  `User-Agent: Bun/1.4.3` and no `X-App`, so the Claude Code profile (which matches
  `claude-cli/` or `x-app: cli`) labels it `unknown`. The Q1 mitmproxy capture above
  saw the same shape.
- Question: should the Claude Code profile also match this request?
- Answer: 2026-09-27 (owner): no. It sends `User-Agent: Bun/<version>` and no `x-app`,
  and stays `unknown` on purpose: matching on Bun would mislabel every other Bun app.
  Phase 4 can attribute it by timing (it opens an interactive session) if needed.
- Outcome (→ spec): the Docs bullet adds this to `docs/clients/claude-code.md` as a
  known limit. The profile is unchanged.
- 2026-09-28 Outcome correction (→ spec): the limit doesn't go in
  `docs/clients/claude-code.md`, which only points at PLAN.md §3's "Known client
  limits". Instead, that PLAN.md entry for Claude Code gains the line in the
  implementation PR, so the list keeps one home.

## Q5 — What is the canonical `tool_result`'s link to its `tool_call` called?
- Status: answered     Level: technical
- Blocks / shapes: nothing yet (tasks.md not written); shapes AC32, AC55 and the
  spec's `tool_result` event
- Context: 2026-09-28. AC55's purity denylist gains Anthropic wire names, `tool_use`
  and `tool_use_id` among them, and the check is a substring match over every file in
  `internal/core/`. The spec's `tool_result` event and AC32 spell the link field
  `tool_use_id`, the wire name. Core holds the canonical event types, so a canonical
  field of that name would fail AC55. The list must not be weakened to let it pass.
- Question: what does the canonical `tool_result` call the id of the `tool_call` it
  answers?
- How to resolve: owner decision. The obvious candidate is a canonical name such as
  `tool_call_id`, set from the wire `tool_use_id` by the Anthropic parser, but that is
  a naming choice, not something to guess.
- Answer: open.
- Outcome: the spec's `tool_result` event and AC32 name the canonical field and say
  which wire field sets it, the same way `cache_hints` and `reasoning_requested` do.
- 2026-09-28 (owner): the canonical `tool_result` links to its `tool_call` through
  `tool_call_id`. The Anthropic parser sets it from the wire's `tool_use_id`. The
  purity denylist is unchanged.
- 2026-09-28 Outcome (→ spec): the `tool_result` event description and AC32 name
  `tool_call_id`; the open question is removed from the spec.

## Q6 — Does `thinking: {type: "disabled"}` set `reasoning_requested`?
- Status: answered     Level: technical
- Blocks / shapes: nothing yet (tasks.md not written); shapes the spec's `request` event
- Context: 2026-09-28. The `request` event's `reasoning_requested: bool` replaces
  "whether `thinking` is present", and the spec sets it from a top-level `thinking`
  field. The Messages API also accepts `thinking` with `type: "disabled"`. Taken
  literally, "present" would then mark a request as asking for reasoning when it
  explicitly asks not to.
- Question: does a `thinking` field with `type: "disabled"` set `reasoning_requested`?
- How to resolve: owner decision; check the current `thinking` types against
  Anthropic's Messages API docs.
- Answer: open.
- Outcome: the spec's Anthropic parser line for `reasoning_requested` names the exact
  rule.
- 2026-09-28 (owner): `reasoning_requested` is true when the request has a top-level
  `thinking` field whose `type` is anything other than `disabled` (so `enabled`,
  `adaptive` and unknown future types are true). It is false when `thinking` is absent
  or has `type: "disabled"`.
- 2026-09-28 Outcome (→ spec): the Anthropic parser section states the rule, and new
  AC45 `TestParse_ReasoningRequestedFlag` covers it (subtests `absent`, `disabled`,
  `enabled`, `unknown_type`); the open question is removed from the spec. ACs from the
  old AC45 on shift by one: AC48 is now AC49, AC55 is AC56, AC58 is AC59.
