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

## Q7 — Is the decoded size of a captured body capped?
- Status: answered     Level: technical
- Blocks / shapes: nothing yet (tasks.md not written); shapes the plan's content
  decoder and worker memory
- Context: 2026-09-28, drafting plan.md. `capture.memory_limit` bounds the tee buffers
  and queued exchanges. Decoding happens later, in the worker, on a copy the budget
  doesn't cover. A compressed body can inflate far past `capture.max_body_bytes`
  (SSE text compresses well; a hostile or broken upstream could send a bomb). The
  spec sets no limit on decoded output.
- Question: should the shared decoder stop at a size limit, and if so which one
  (`capture.max_body_bytes`, a multiple of it, or a new constant), and is a body cut
  there `partial`?
- How to resolve: owner decision.
- Answer: open.
- Outcome: the plan's decoder signature takes the limit, or the risk is accepted in
  the plan's Risks.
- 2026-09-28 (owner): `contentcoding.Decode` caps decoded output at
  `capture.max_body_bytes`, passed in by the caller; no new config. Output past the
  cap is dropped and the result is `CutShort`, so the exchange is `partial`.
- 2026-09-28 Outcome (→ plan, → spec): the decoder takes the limit and its task is
  unblocked; a `TestDecode` subtest covers the cap (no AC change). The plan's Risks
  state the worst-case unbudgeted worker memory as `workers × 4 × max_body_bytes`
  (about 256 MiB at the defaults). The spec's Content decoding section gains one
  clause for the cap.

## Q8 — Which new parser fixtures can be recorded from real traffic?
- Status: answered     Level: flow
- Blocks / shapes: nothing yet (tasks.md not written); shapes AC32–AC36 fixtures
- Context: 2026-09-28, drafting plan.md. The spec asks for new scrubbed fixtures: a
  tool-use turn, a server-tool turn, a non-streaming response and an upstream error.
  001's golden stream was recorded through a throwaway proxy with a Claude Code
  subscription login (001 Q10); no API key is assumed (001 Q4). AC34 needs one
  streamed turn holding a `server_tool_use`, its `web_search_tool_result` and a
  client `tool_use` together, which Claude Code may never send in one turn. A
  non-streamed `/v1/messages` call, a `429` and an SSE `error` event can't be
  produced on demand through Claude Code either.
- Question: for each fixture, is it recorded (through Claude Code, or a direct API
  call with a key), or built by hand from a recorded one following Anthropic's
  documented shapes, with its README saying so?
- How to resolve: owner decision; try a recording session first.
- Answer: open.
- Outcome: the plan's Fixtures section names the source of each file.
- 2026-09-28 (owner): recorded from Claude Code: `tool_turn`, `tool_order` and
  `server_tool` (Claude Code's WebSearch is the API's server tool). Hand-built from
  Anthropic's documented shapes: `non_streaming`, `error/response_429.json` and
  `error/stream_error.sse`. Every fixture's README entry says `recorded` or
  `synthetic`. The OAuth token is never extracted to call the API directly.
- 2026-09-28 Outcome (→ plan): the Fixtures table names each source. Two things to
  check while recording, and to bring back to the owner if they fail, not to
  paper over: AC34 wants a `server_tool_use`, its result and a client `tool_use` in
  one streamed turn, and Claude Code's WebSearch may send the server tool in a
  request of its own; AC33 wants a message of text, `tool_use`, text, and a model
  turn usually stops at its last `tool_use`.
- 2026-09-28 (owner): AC33's fixture may be synthetic, hand-built from recorded
  blocks, because it tests the parser's ordering, not upstream behaviour. AC34's may
  be assembled: the recorded WebSearch request's `server_tool_use` and
  `web_search_tool_result`, plus a client `tool_use` spliced in from `tool_turn`; its
  README says "synthetic (assembled from recorded blocks)". Outcome (→ plan): the
  fixture table says so.

## Q9 — Can `gateway dump` read a WAL database without writing a file?
- Status: answered     Level: technical
- Blocks / shapes: nothing yet (tasks.md not written); shapes AC57 and the dump
  command's open path
- Context: 2026-09-28, drafting plan.md. The spec says dump never writes a file. The
  store runs in WAL mode. SQLite's docs say a read-only connection to a WAL database
  can be opened if the `-wal` and `-shm` files exist or can be created; after a clean
  shutdown SQLite removes them, so a `mode=ro` open may create them. `immutable=1`
  avoids that but is only safe while nothing writes.
- Question: with the chosen driver, does a `mode=ro` open of a stopped gateway's
  database create `-wal` or `-shm`? If so, is `immutable=1` when no `-wal` exists an
  acceptable fallback?
- How to resolve: a throwaway program against the chosen driver, in both states
  (gateway running, gateway stopped).
- Answer: open.
- Outcome: the plan's dump open path, and possibly AC57's wording (→ spec).
- 2026-09-28 (owner): stays open until the spike runs. Decision rule, recorded now:
  `immutable=1` is rejected, since it gives torn reads while the gateway writes. If a
  `mode=ro` open creates `-wal` or `-shm`, the spec's dump rule becomes "never
  writes `gateway.db` or a blob", and AC57 is unchanged.
- 2026-09-28 Answer (spike):
  Spike: a throwaway module outside the repo (`go mod init spike`,
  `go get modernc.org/sqlite@latest` → v1.59.0, SQLite 3.53.4, go1.25.4, Linux amd64),
  `go run .`; not committed. The writer DSN is
  `file:<db>?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)&_pragma=synchronous(NORMAL)`
  with `SetMaxOpenConns(1)`; the reader DSN is `file:<db>?mode=ro`.
  - Stopped gateway: after the writer's clean close only `gateway.db` is left. A
    `mode=ro` open and `SELECT` succeed (2 rows) and create `gateway.db-wal` and
    `gateway.db-shm`, which stay after the reader closes. `gateway.db`'s mtime is
    unchanged. The created files get the database's mode (`0600`, pre-created as the
    store will); `-wal` is 0 bytes and `-shm` 32 KiB.
  - Running gateway (writer still open): `mode=ro` reads the committed rows,
    including rows still in the `-wal`, and sees a later insert by the writer. No new
    file is created; `gateway.db`'s mtime is unchanged; the writer is not blocked.
- 2026-09-28 Outcome (→ spec, → plan): the decision rule fires. The spec's dump rule
  becomes "never writes `gateway.db` or a blob"; SQLite's `-wal` and `-shm` may be
  created next to a stopped gateway's database. AC57 is unchanged: a fresh directory
  stays empty because dump checks for `gateway.db` before opening, and the
  database's mtime doesn't change. `immutable=1` stays rejected.

## Q10 — Does the driver fail a write after the database file is deleted?
- Status: answered     Level: technical
- Blocks / shapes: nothing yet (tasks.md not written); shapes AC25 `db_deleted` and
  AC62, and the driver choice
- Context: 2026-09-28, drafting plan.md. On Linux an unlinked file stays writable
  through an open descriptor, so a deleted `gateway.db` could keep taking writes
  silently. SQLite's unix VFS reports `SQLITE_READONLY_DBMOVED` for that case.
  `modernc.org/sqlite` uses SQLite's unix VFS; `ncruces/go-sqlite3` replaces it with
  its own Go VFS.
- Question: with `modernc.org/sqlite`, does a write after `gateway.db` is deleted
  fail, so the worker logs `capture_failed` with `stage: store`?
- How to resolve: a throwaway program, before the store task.
- Answer: open.
- Outcome: if it doesn't fail, the store checks the file's identity itself before
  each write transaction (→ plan), or AC25 is reshaped (→ spec).
- 2026-09-28 (owner): stays open until the spike runs. Fallback, recorded now: if a
  write after unlink doesn't fail, the store compares `gateway.db`'s inode (device and
  inode number) with the one it opened before each write transaction, and treats a
  mismatch or a missing file as a store failure. AC25 is unchanged either way.
- 2026-09-28 Answer (spike, same module as Q9):
  Spike: a throwaway module outside the repo (`go mod init spike`,
  `go get modernc.org/sqlite@latest` → v1.59.0, SQLite 3.53.4, go1.25.4, Linux amd64),
  `go run .`; not committed. The writer DSN is
  `file:<db>?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)&_pragma=synchronous(NORMAL)`
  with `SetMaxOpenConns(1)`; the reader DSN is `file:<db>?mode=ro`.
  - After one write, `gateway.db` is unlinked; `-wal` and `-shm` remain. `BEGIN`,
    `INSERT` and `COMMIT` all return no error, and so does a second autocommit
    `INSERT`. No `SQLITE_READONLY_DBMOVED` or any other error: writes go on silently
    into the unlinked file.
- 2026-09-28 Outcome (→ plan): the fallback fires. The store records `gateway.db`'s
  device and inode at open and compares them with a fresh `os.Stat` before each write
  transaction; a mismatch or a missing file is a store failure (AC25 `db_deleted`).
  The plan's dependency row no longer credits the driver with detecting this. AC25
  is unchanged.

## Q11 — What does the inode check do when `capture.dir` is `chmod 000`?
- Status: answered     Level: technical
- Blocks / shapes: T10 (the store's inode check), T30 (AC62's expectation)
- Context: 2026-09-28, drafting tasks.md. The Q10 fallback compares `gateway.db`'s
  device and inode with a fresh `os.Stat` before each write transaction, and treats a
  mismatch or a missing file as a store failure. After `chmod 000` on `capture.dir`,
  `os.Stat` of `gateway.db` fails with permission denied, since the directory can no
  longer be searched. The plan's AC62 row and Risks entry say inline-only exchanges
  may still be stored after `chmod 000`, which assumes the check passes. If a stat
  error counts as a missing file, every write fails at once instead, and the one log
  line says "deleted or replaced" for a permission change.
- Question: is any `os.Stat` error a store failure (and if so, should the line read
  "deleted, replaced or unreadable"), or only `ENOENT` and a changed inode, with other
  stat errors left to the write itself?
- How to resolve: owner decision.
- Answer: open.
- Outcome: the plan's inode-check paragraph, AC62 row and Risks entry, and T10 and T30
  (→ plan).
- 2026-09-28 (owner): option (b). Only a missing file (`ENOENT`) or a changed device
  or inode puts the store into the permanent "deleted or replaced" state with its one
  error line. Any other stat error, such as permission denied after `chmod 000`, does
  not: it is logged once as a warning, `store file unreadable: <err>`, with the path,
  and the write itself decides.
- 2026-09-28 Outcome (→ plan): the plan's inode-check paragraph says so; the AC62 row
  and Risks entry stay as written. T10 gains the subtest
  `stat_permission_denied_not_deleted`, and T30's expectation follows.

## Q12 — How does the manual smoke read the peak `capture.memory_limit` usage?
- Status: answered     Level: flow
- Blocks / shapes: T29 (AC61 evidence); the spec's open question on the
  `max_body_bytes` and `memory_limit` defaults
- Context: 2026-09-28, drafting tasks.md. The spec's open question asks to "log the
  peak `capture.memory_limit` usage" during the manual smoke. The plan's `Budget` has
  `InUse` only, and the `capture stopped` line logs `undrained` and the drop and
  failure counts. Nothing reports a peak.
- Question: should `Budget` track its peak and the `capture stopped` line log it (for
  example `memory_peak_bytes`), or is the peak read another way (process RSS)?
- How to resolve: owner decision.
- Answer: open.
- Outcome: T6 and T16 gain the field if chosen (→ plan), and T29's evidence names it.
- 2026-09-28 (owner): accepted as suggested. `Budget` tracks its peak with an atomic
  compare-and-swap max and exposes `Peak()`; the `capture stopped` line logs
  `memory_peak_bytes`.
- 2026-09-28 Outcome (→ plan): the plan's Memory section, Interfaces and shutdown
  line gain it; T6 and T16 build it, and T29's evidence includes the value. The
  spec's open question on the size defaults is answered by T29's evidence, and stays
  open in the spec until then.

## Q13 — Is 001's `TestAccessLog_UpstreamAbortedField/completed` flaky under load?
- Status: open     Level: flow
- Blocks / shapes: nothing in 002 (found during T13; a 001 test)
- Context: 2026-09-29, T13. One `make verify` run failed
  `TestAccessLog_UpstreamAbortedField/completed` in `internal/core` with
  `client_disconnected = true, want absent`; the next run passed, and 30 runs of the
  test alone under `-race` passed. T13 touched only the leak check's stack filter in
  that package. The `completed` case reads the response with `gw.raw`, which closes
  the connection as soon as the body is read, so the client may be gone before the
  handler settles and the gateway may then record a disconnect.
- Question: is this a race in the test (closing before the access line is decided),
  or in 001's disconnect detection on a finished response?
- How to resolve: reproduce under load (`go test -race -count=200 -cpu 1,4`), then
  decide whether the test or `Meta.Settle` changes.
- Answer: open.
- Outcome: a `fix/` branch for 001 if it is the proxy; a test fix otherwise.

## Q14 — Does renaming SSE event names in 001's test data break "001 tests unchanged"?
- Status: answered     Level: flow
- Blocks / shapes: T15 (AC58); the spec's Do line on 001's fidelity
- Context: 2026-09-29, T15 review escalation. The purity check reads every file under
  `internal/core`, test files included. Four 001 tests in `internal/core/proxy_test.go`
  used `message_start` and `message_stop` as SSE event names; commit `1120d42` renamed
  them to `first` and `last` so AC58 passes.
- Question: spec.md:363 (001 tests unchanged) conflicts with AC58 (core's banned-word
  check covers Anthropic event names, and test files are included).
- How to resolve: owner decision.
- Answer: (a). "Unchanged" means the assertions, not the test data.
- Why: the line exists to catch capture changing proxy behaviour, which the assertions
  cover. The reviewer confirmed all four renamed tests
  (`TestProxy_StreamsSSEWithoutBuffering`, `TestProxy_ClientDisconnectCancelsUpstream`,
  `TestProxy_UpstreamDiesMidStreamAbortsClient`, `TestAccessLog_UpstreamAbortedField`)
  keep every assertion and still fail on buffering, altering or cutting the stream.
  Narrowing AC58 would let provider names into protocol-agnostic core.
- Outcome (→ spec): spec.md's Do line now reads "every 001 test passes with its
  assertions unchanged, with capture on; test data may be renamed where AC58 requires
  it." `1120d42` stands.
- 2026-10-02 (owner, 002 ESCALATE review, A1 on `036abdb`, spec.md:367-368): the
  "assertions unchanged" wording is accepted. Precondition checked the same day:
  `git diff main...HEAD -- internal/core/proxy_test.go` is `1120d42` only, and it
  renames four SSE event-name string literals (`message_start`/`message_stop` to
  `first`/`last`); no assertion line changed.

## Q15 — How does `startGateway` keep the default `capture.dir` out of the real data dir?
- Status: answered     Level: flow
- Blocks / shapes: T16 (the test data-dir guard; the spec's Do line on the default
  data dir, AC5)
- Context: 2026-09-29, T16. The task line has `startGateway` put
  `GATEWAY_CAPTURE_DIR=<t.TempDir()>` in the env map it hands `run`, and proves the
  guard by dropping that line and watching `TestMain` fail. Every `GATEWAY_*` variable
  lands in the startup line's `env_overrides`, and 001's `TestRun_StartupLine` and
  `TestRun_DefaultsWhenNoConfig` assert that field, so the helper as written changes
  what 001's assertions see (spec, Do: assertions unchanged). In-process tests also
  read env from the map, not the process, so dropping the helper's line makes `run`
  exit `2` on an unresolvable default instead of writing anywhere `TestMain` looks.
- Question: what does the helper set instead, and what proves the guard?
- How to resolve: implementer, within the plan's intent; reviewed at T16.
- Answer: the helper sets `XDG_DATA_HOME=<t.TempDir()>` unless the test sets
  `GATEWAY_CAPTURE_DIR`, `XDG_DATA_HOME` or `HOME` itself, so the default resolves
  into a temp dir and `env_overrides` is untouched. `binary_test.go` still puts
  `GATEWAY_CAPTURE_DIR` in the child's environment, which is a real process. The guard
  is proved twice: dropping the child's `GATEWAY_CAPTURE_DIR` makes `TestMain` fail
  naming `<tmp>/llm-gateway`, and dropping the helper's `XDG_DATA_HOME` fails 10 001
  `run` tests.
- Why: it keeps 001's assertions unchanged, and `TestMain` still catches the one path
  that reads the real process environment.
- Outcome (→ plan): the Testing strategy's data-dir bullets say so; T16's line points
  here. The T16 reviewer judged the approach acceptable (2026-09-29).

## Q16 — Which task proves the event half of AC22?
- Status: answered     Level: flow
- Blocks / shapes: T16, T20 (AC22)
- Context: 2026-09-29, T16 review. AC22 says every stored exchange and event has
  `principal_id: local`. At T16 the Anthropic adapter has no parser, so every exchange
  is parsed `skipped` and has no events: `TestCapture_PrincipalLocal` looped over zero
  events and passed, and nothing would make it start checking once T20 adds the parser.
- Question: how is the event half proved, and by which task?
- How to resolve: implementer; tasks.md only (the spec is unchanged).
- Answer: T16 proves the exchange half and pins the messages exchange at parse
  `skipped` with no events, with a request body of one user message. When T20's
  parser lands that pin fails, and T20 turns it into parse `ok` with at least one
  event, each `principal_id: local`. T16 now supports AC22; T20 completes it.
- Why: the event half can't be proved without a parser, and a pin that fails on
  arrival is the only way to keep the test from passing on zero events after T20.
- Outcome (→ tasks): T16's line says `supports AC22`; T20's line gains the
  `TestCapture_PrincipalLocal` change and AC22; the AC table's AC22 row lists T20.

## Q17 — How is AC16 proved when the suite runs as root?
- Status: answered     Level: flow
- Blocks / shapes: T16 (AC16)
- Context: 2026-09-29, T16 review. `TestStore_OpenFailsFast` in `cmd/gateway` made
  `capture.dir`'s parent `0500` and skipped under root, which ignores directory
  permissions. As root (containers, some CI) the test reported success with AC16
  unproven, and `AGENTS.md` says not to skip tests. `internal/store`'s
  `unwritable_dir` subtest (T10, `5201167`) skips the same way; that is outside T16.
- Question: what makes an uncreatable `capture.dir` for any user?
- How to resolve: implementer.
- Answer: a `capture.dir` whose parent is a regular file. `mkdir` fails with
  `ENOTDIR` for every user, root included, so the test through `run` (subtest
  `uncreatable_dir`) checks exit `1`, one line naming the path with reason
  `not a directory`, nothing created and nothing bound, with no skip. The mapping of
  every other reason, `permission denied` included, is proved in subtest
  `fixed_reasons` on built errors, which don't depend on the user.
- Why: it proves AC16 wherever the suite runs; a real `EACCES` through `run` can't be
  had as root without dropping privileges.
- Outcome (→ tasks): T16's line names the two subtests. `internal/store`'s root skip
  stays for its own task to settle.
- 2026-10-02 (owner, 002 ESCALATE review, fix 3): `internal/store`'s
  `unwritable_dir` subtest drops `skipIfRoot` and uses the same answer: the store
  path sits beneath a regular file (`<tmp>/afile/store`), so creating it fails with
  `ENOTDIR` for every user, root included. The task is T34.

## Q18 — What do AC27 and AC28 require of `Proxy-Authorization`, which 001 strips?
- Status: answered     Level: flow
- Blocks / shapes: T18 (AC27, AC28)
- Context: 2026-09-29, T18. AC27 wants a sentinel in `Proxy-Authorization` to appear
  nowhere in the store *and* the header's name to be present with `[REDACTED]`. AC28
  wants "the same sentinels" to reach upstream and the client byte-identical. But
  001's spec (Forwarding, Request and Response; AC8) makes every `Proxy-*` header
  hop-by-hop, and 002's Do line keeps 001's assertions unchanged. In the code,
  `rewrite` strips `Proxy-*` before `teeRequest` snapshots the request headers "as
  sent upstream" (plan, Capture flow), and ReverseProxy plus `modifyResponse` strip
  them from the response before the response tee sees its header. So a
  `Proxy-Authorization` sentinel never reaches upstream (AC28 as written fails) and
  its name is never in the stored copy in either direction (AC27's "names present"
  fails). Core's redaction list still covers it, as `TestRedact` (T7) proves on a
  built header.
- Question: which does the spec mean?
  (a) AC27 and AC28 exclude hop-by-hop headers: `Proxy-Authorization`'s sentinel is
  checked absent from the store and the logs, and neither its forwarding nor its
  stored name is asserted (spec edit to both ACs' wording);
  (b) the request snapshot is taken from the inbound headers before hop-by-hop
  stripping, so `Proxy-Authorization` is stored as `[REDACTED]` (plan and core
  change; the stored headers would no longer be "as sent upstream"), with AC28
  excluding hop-by-hop;
  (c) something else.
- How to resolve: owner decision; the answer edits spec.md (and plan.md for (b)).
- Answer: 2026-09-30, owner: (a). AC27 and AC28 cover only headers that are forwarded
  upstream; hop-by-hop headers, every `Proxy-*` header included, are left out. For a
  `Proxy-Authorization` sentinel the test only checks that it never appears in the
  store or the logs. No code changes.
- Why: stored headers are defined as what was sent upstream, and 001 already strips
  `Proxy-*`, so the secret is never forwarded; core's redaction list still covers it,
  as T7's `TestRedact` proves. Option (b) would redefine stored headers and still need
  AC28 edited.
- Trade-off: the store has no record that the client sent the header. A client-side
  header view is possible future scope.
- Outcome (→ spec, tasks): AC27 and AC28 reworded; T18's line matches them.
- 2026-10-02 (owner, 002 ESCALATE review, A2 on `83bf467`, spec.md:505-513): (a) is
  accepted. Capture snapshots the outbound request after 001's hop-by-hop stripping,
  core's redaction list always covers `Proxy-Authorization`, and
  `TestRedact_AuthHeaders` proves the sentinel never reaches the store or the logs.
  The spec is re-approved with this wording.

## Q19 — Where do the response's id and model go in the canonical `message` event?
- Status: answered     Level: flow
- Blocks / shapes: T22 (T21)
- Context: 2026-09-30, T22 escalation. The spec says `message_start` "gives the id,
  model and initial usage", but the canonical `message` event has no field for the id
  or the model, so both T21 (JSON) and T22 (SSE) drop them; the plan noted they stay
  in the raw body.
- Question: does the canonical `message` event carry the response id and model?
  (a) no: they stay only in the raw body;
  (b) yes: the event gains provider-neutral `response_id` and `model` fields.
- How to resolve: owner decision; the answer edits spec.md, plan.md and tasks.md.
- Answer: 2026-09-30, owner: (b). The response `message` event gains `response_id` and
  `model`, filled by the shared builder on both the JSON and SSE paths. No code in
  this change; the T22 fix round does it.
- Why: the response model is the model that actually answered, and cost tracking
  prices on it; the response id is the key for matching resent assistant blocks (Q3).
  Both are provider-neutral (OpenAI has them too), so AC58 holds.
- Rejected: (a), since keeping them only in the raw body forces every later consumer
  to re-parse provider JSON.
- Trade-off: it changes core's canonical event. The fields live in `events.payload`,
  so the store schema is unchanged.
- Outcome (→ spec, plan, tasks): the spec's `message` event lists both fields; the
  plan's payload, `MessageEvent` and T22 details replace the "dropped" note; T22's line
  covers filling them with a stream-vs-JSON equality test.
- 2026-10-02 (owner, 002 ESCALATE review, A3 on `918085a`, spec.md:196-199 and
  238-240): (b) is accepted; `response_id` and `model` are provider-neutral. The spec
  is re-approved with these fields.

## Q20 — How are T23's assembled fixtures built when the task line's sources don't fit?
- Status: answered     Level: flow
- Blocks / shapes: T23 (AC33, AC34)
- Context: 2026-10-01, T23 review. The task line says `tool_order` is built "from
  `tool_turn`'s blocks" and that each block keeps its bytes unchanged. `tool_turn`'s
  recorded answer is a lone `tool_use` at index 0, with no `text` block, so text,
  `tool_use`, text can't come from it alone. The stream parser orders blocks by their
  top-level `index`, so recorded index values can't be kept either: text before a
  `tool_use` at index 0 is impossible, and in `server_tool` `tool_turn`'s `tool_use`
  (index 0) collides with `websearch.sse`'s `server_tool_use` (index 0).
- Question: where do `tool_order`'s text blocks come from, and may block events be
  edited to fit the new arrangement?
- How to resolve: implementer, within Q8's owner answer ("hand-built from recorded
  blocks"; `server_tool` "assembled" and spliced); reviewed at T23.
- Answer: `tool_order`'s two `text` blocks are `websearch.sse`'s recorded text blocks
  3 and 7, around `tool_turn`'s `tool_use`, in `tool_turn`'s envelope. `server_tool`
  keeps `websearch.sse`'s envelope (`stop_reason: end_turn` as recorded). In both,
  each block event is byte-identical to its recording except its top-level `"index"`,
  renumbered 0, 1, 2. Every other byte of every block event is unchanged.
- Why: Q8 allows both fixtures to be built from recorded blocks; the blocks still
  come only from recordings, and the index is the one field the arrangement itself
  defines. The READMEs of both fixtures say so.
- Rejected: hand-writing text blocks (not recorded); keeping recorded index values
  (impossible order and a collision, above); `tool_turn`'s `message_delta` in
  `server_tool` (mixes two recordings' usage).
- Trade-off: "bytes unchanged" now means "except the top-level `index`", and
  `server_tool`'s `stop_reason: end_turn` doesn't match a turn ending in a client
  `tool_use`; the parser does not read it for tool mapping.
- Outcome (→ plan, tasks): plan.md's "Details fixed in T23" bullet points here; T23's
  line links Q20.

## Q21 — Does the hard rule on credentials cover `Proxy-Authorization`?
- Status: answered     Level: flow
- Blocks / shapes: nothing in 002's tasks; `AGENTS.md` hard rules, `PLAN.md` §5
- Context: 2026-10-02, 002 ESCALATE review, finding B. `a3c1e39` narrowed the hard
  rule at `AGENTS.md:30` and `PLAN.md:99` from "secrets" to "credentials — auth
  headers and keys", to match OQ-5 and ADR 0005. The reviewer flagged the narrowing
  as a change to a hard rule, which only the owner can approve.
- Question: is the narrower rule approved, and does "auth headers" include
  `Proxy-Authorization`, which 001 strips before capture sees it (Q18)?
- How to resolve: owner decision.
- Answer: 2026-10-02, owner: the narrower rule is approved; it matches OQ-5 and ADR
  0005. Its wording names `Proxy-Authorization` as an auth header, so the rule
  doesn't depend on 001's stripping to hold.
- Outcome (→ AGENTS.md, PLAN.md): both lines name `Proxy-Authorization` among the
  auth headers.

## Q22 — What parse status does a request copy sealed early get?
- Status: answered     Level: flow
- Blocks / shapes: T32, T33
- Context: 2026-10-02, 002 ESCALATE review, fixes 1 and 2. Two gaps against the
  tolerance rule (spec, Anthropic parser): `parseRequest` reads the body with one
  `json.Unmarshal`, so a request body cut at `max_body_bytes` yields no events at
  all, not the events before the cut; and the pipeline never tells the parser about
  `request_incomplete`, so a request copy sealed before its end (upstream answered
  early, or a 502/504 before upstream read the body) parses as malformed: `failed`.
  `TestParse_GatewayMadeResponseStoredWithoutError/upstream_unreachable` asserted no
  parse status, so it passed on `failed`.
- Question: is a request copy sealed early `partial` or `failed`, and how far does a
  cut request body parse?
- How to resolve: owner decision.
- Answer: 2026-10-02, owner: (1) the request body is read token by token, so a body
  cut short emits the request event and one message event per message complete
  before the cut, and the parse is `partial`; (2) the pipeline passes
  `request_incomplete` to the parser beside the truncation flag, and such an
  exchange is `partial`, not `failed`.
- Why: both are bodies cut short, which the tolerance rule already treats as
  `partial`; `failed` is for a body malformed on its own.
- Outcome (→ spec, tasks): the spec's tolerance bullet names `request_incomplete`
  beside `truncated`; T32 and T33 do the work.

## Spec re-approval, 2026-10-02
- The owner re-approved `spec.md` after the 002 ESCALATE review: A1 (Q14), A2 (Q18)
  and A3 (Q19) accepted, and the tolerance edit from Q22. Q3 and Q13 stay open.
