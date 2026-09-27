---
status: draft
branch: feat/002-capture-and-canonical-events
---

# 002 — Capture and canonical events

## Intent
Make the gateway remember what flows through it. Every exchange that 001 forwards, the
request and the response, streams included, is copied on the side, redacted of auth,
stored, and parsed into the canonical events from PLAN.md §2: `request`, `message`,
`tool_call`, `tool_result`, `usage`, `error`. From here on, nothing downstream
(sessions, timeline, cost) reads a provider's raw format.

This is priority 1, observability (PLAN.md §4), made real: 001 put the gateway on the
wire; 002 is the first thing it can show. It must not cost anything in 001's terms. A
client can't tell a capturing gateway from a direct connection, and a broken, slow or
missing store never fails or delays a request.

Done when (PLAN.md §7): every exchange is stored and parsed, and killing the store
doesn't break the client.

## Scope
- **Principal seam** (PLAN.md §2, "Scale").
  - Core gains a principal resolver behind one interface. Its only implementation
    returns `local`.
  - The resolved principal travels with the request, and every stored exchange and
    event carries its `principal_id`.
- **Tee.**
  - The proxy copies the request body as it is read towards upstream, and the response
    body as it is written to the client.
  - The copy never changes a forwarded byte, never waits, and never slows a flush.
  - Each copied body is capped at `capture.max_body_bytes`. Past the cap, the copy stops
    and the exchange is flagged `truncated`; forwarding carries on untouched.
  - The copy holds the bytes as they went over the wire, still encoded if
    `Content-Encoding` was set.
  - The request copy is the bytes read from the client body, once. That holds because
    the outgoing request has no `GetBody`, so the transport never replays a body it
    has written (001, research Q21). Capture must not add a `GetBody`.
- **Exchange record.** One per proxied request, keyed by the gateway's request ID:
  - `principal_id`, `protocol`, `client`, `auth` kind;
  - method, path (prefix stripped) and query;
  - request and response headers (redacted, below);
  - status; start time, TTFB and end time;
  - `stream`, `truncated`, `client_disconnected`, `upstream_aborted`, `gateway_error`;
  - the request and response bodies, by reference to their blobs.

  An exchange that ends badly (the client left, upstream aborted, the gateway made the
  error) is still recorded, with whatever bytes were seen and a flag saying how it
  ended.
- **Capture sink.**
  - Producers depend on a `CaptureSink` interface, never on its transport (PLAN.md §2).
  - The v1 sink is a bounded in-process queue (`capture.queue_size`) drained by
    `capture.workers` goroutines, started through 001's recovery helper.
  - When the queue is full, the exchange is dropped, not waited for. A drop is logged
    and counted, never silent.
  - The sink confirms PLAN.md §8's "Proposed" capture pipeline.
- **Memory bound.** In-flight tee buffers and queued exchanges share one budget,
  `capture.memory_limit`.
  - A stream's size is unknown at the start, so each tee reserves from the budget
    chunk by chunk.
  - When a chunk would cross the limit, that exchange's buffers are released at once
    and it is marked `dropped_memory` (logged and counted). Forwarding is unaffected.
  - `max_body_bytes` is checked first: a truncated body stops reserving, and it isn't
    dropped.
- **Redaction** of the captured copy, before anything is queued. The forwarded traffic
  is never touched.
  - **Headers:** the value of every auth-bearing header becomes `[REDACTED]` in both
    directions. The name is kept, so the record still shows which kind of auth was
    used.
    - Generic HTTP auth headers live in core: `Authorization`, `Proxy-Authorization`,
      `Cookie`, `Set-Cookie`.
    - Each adapter adds its own; Anthropic adds `x-api-key`.
  - **Query:** each adapter declares any query parameters that carry secrets, and their
    values are redacted the same way. Anthropic declares none; the seam exists for
    later adapters that put a key in the query.
  - **Bodies:** stored as sent. Secrets inside prompts and tool results are not masked
    (OQ-5, below).
- **Store** (OQ-1, below).
  - All access goes through a `Store` interface. Nothing outside the store package
    knows the engine.
  - **Index and events:** SQLite, in `<capture.dir>/gateway.db`, in WAL mode. The
    schema is created and migrated by the gateway at startup, with a recorded schema
    version.
  - **Writers and readers:**
    - All writes go through a single writer connection owned by the store. Workers
      may parse concurrently, but writes are serialized.
    - The store sets a busy timeout of 5 s, so a concurrent reader never turns into a
      capture failure.
    - Readers (`gateway dump`, tests) use separate read-only connections.
  - **Bodies and content:** content-addressed. Each item is keyed by its sha256 hash:
    - **Raw bodies:** the hash covers the stored bytes before zstd, i.e. the wire
      bytes. A gzip body is hashed as gzip.
    - **Parsed content** (blocks, tool inputs, tool results, the system and tools
      arrays): the hash covers a canonical encoding: decode, drop the fields the
      adapter declares excluded from the hash (Parser seam, below), then re-encode
      with sorted keys, in compact form, with numbers preserved
      (`json.Decoder.UseNumber`). Without it, an assistant message reassembled from
      turn N's stream and its resent copy in turn N+1's request serialize differently
      and never dedup. Core takes the exclusion list as given and names no wire field
      itself.
  - **Where content lives:**
    - Content up to 4 KiB (a constant in v1, not config) is stored inline in SQLite.
    - Larger content goes to a blob file, named by its hash
      (`<capture.dir>/blobs/ab/cdef…`) and stored zstd-compressed.
    - The rule is the same for raw bodies and parsed content. The dedup key is the
      hash either way, so where content lives doesn't affect dedup. Identical content
      is stored once.
    - A blob is written in full (temp file, then rename) before any row references
      it.
  - **Permissions:** the directory is `0700` and every file under it is `0600`.
  - **Startup:** if the store can't be opened or migrated at startup, the gateway fails
    fast with the reason and the path. It never starts silently without capture.
  - **At runtime:** after startup, every store failure (a locked, deleted or read-only
    database, a full disk) is logged and counted per exchange, and the request it
    belongs to is unaffected.
- **Content decoding** (research Q1). Decoding a captured body's `Content-Encoding` is
  one shared helper in a protocol-neutral package outside `internal/protocols/`. The
  Anthropic parser and `gateway dump` both use it, and neither has decode rules of its
  own.
  - It decodes `identity`, `gzip`, `deflate` (both zlib-wrapped and raw), `br` and
    `zstd`. Any other encoding is reported as unsupported, and the caller keeps the
    raw bytes.
  - A truncated compressed body decodes as far as it can, and the helper reports that
    it was cut short.
  - It only ever reads the captured copy. Forwarded bytes are never decoded.
  - Content coding is generic HTTP, so the helper knows no provider or wire format.
- **Parser seam.** An adapter may implement an optional parser that turns one stored
  exchange into canonical events. The core decides nothing about any wire format; it
  only calls the parser and stores what comes back.
  - The parser also declares the wire-only fields excluded from content hashes (a
    `HashExcludedFields()`-style method). Core applies that list in the canonical
    encoding and never spells a field name of its own.
- **Tool origin in the canonical model.** `tool_call` and `tool_result` carry
  `executed_by: client | provider`. `client` means the harness ran the tool. `provider`
  means the provider ran it on its own side. This is protocol-neutral; later
  protocols with hosted tools use `provider` too.
- **Message history per exchange.** Each exchange's `message` events cover the full
  request history, not only what is new since the last turn.
  - Content is deduplicated by hash, so the cost is small rows.
  - Phase 4 derives "new since last turn" by comparing hashes.
  - The process stays stateless: no parse depends on an earlier exchange.
- **System and tools as deduplicated content.** The `request` event carries
  `system_hash` and `tools_hash`. The system array and the tools array are each stored
  once by content hash, with the same canonical encoding and the same adapter-declared
  hash exclusions as message blocks.
  - Why: a bare "hi" request from Claude Code was 115 KB, almost all of it system
    prompt and tool definitions, resent on every turn. Stored by hash, that costs one
    copy per distinct version instead of one per turn.
  - A harness can change either mid-session (Claude Code does, behind its
    mid-conversation system and tool-change betas). A changed hash between two turns
    is how that becomes visible.
- **Anthropic parser.**
  - **Parses:** `POST /v1/messages`, both non-streaming JSON and SSE streams. Every
    other path is stored raw and marked `parse: skipped`.
  - **Decoding** (research Q1): real traffic is compressed. Claude Code sends
    `Accept-Encoding: gzip, deflate, br, zstd`, and Anthropic answers a streamed
    `POST /v1/messages` with `Content-Type: text/event-stream` and
    `Content-Encoding: gzip`. Compressed SSE is the normal case, not an edge case.
    - The parser decodes the captured copy through the shared helper (Content
      decoding, above).
    - An encoding the helper doesn't support is marked `parse: unsupported_encoding`,
      and the raw bytes stay stored.
    - A body the helper reports as cut short makes the exchange `partial`.
    - The committed golden `stream.sse` is identity-encoded (recorded with
      `Accept-Encoding: identity` forced, 001 research Q10). A test gzips it to cover
      the real case.
  - **Events:**
    - `request`: model, stream flag, `max_tokens`, `system_hash` and `tools_hash`,
      whether `system` and `tools` are present, the tool names offered, and two
      canonical presence flags:
      - `cache_hints: bool` is set when a `cache_control` field appears anywhere in
        the request body;
      - `reasoning_requested: bool` is true when the request body has a top-level
        `thinking` field whose `type` is anything other than `disabled` (so
        `enabled`, `adaptive` and unknown future types are true). It is false when
        `thinking` is absent or has `type: "disabled"` (research Q6).
    - `message`: one per message in the request (with its index) and one for the
      response's assistant message, which also carries `stop_reason`. Content blocks
      are typed `text`, `reasoning`, `media` or `unknown`. Each block's content is
      stored by hash, so a message resent on every turn is stored once.
      - `thinking` → `reasoning`.
      - `redacted_thinking` → `reasoning` with `redacted: true`, its opaque `data`
        kept as sent, since it is needed to replay the turn.
    - `tool_call`, with its id, name and input (stored by hash), and the raw block
      JSON:
      - `tool_use` → `executed_by: client`;
      - `server_tool_use` and `mcp_tool_use` → `executed_by: provider`.
    - `tool_result`, with `tool_call_id` (the canonical link to its `tool_call`, set
      from the wire's `tool_use_id`), `is_error`, the content (stored by hash) and the
      raw block JSON (research Q5):
      - `tool_result` → `executed_by: client`;
      - `mcp_tool_result`, and any block type ending in `_tool_result` that carries a
        `tool_use_id` (for example `web_search_tool_result`,
        `web_fetch_tool_result`) → `executed_by: provider`.
    - `usage`: the final input, output, cache-creation and cache-read token counts as
      the provider reported them, with any finer breakdown the provider sends kept as
      raw JSON.
    - `error`: an upstream error status and body mapped to `error` with the provider's
      error type and message, or a stream that ended with an `error` event.
  - **Streams:** SSE is reassembled into whole blocks. `ping` and unknown event types
    are skipped, not failed on.
    - **Deltas, per block index:**
      - `text_delta` is concatenated.
      - `input_json_delta.partial_json` is concatenated, and parsed **only at
        `content_block_stop`**.
      - `thinking_delta` is concatenated. `signature_delta` is stored on the
        reasoning block and kept, since it's what makes thinking replayable.
      - `citations_delta` is appended to the block's citations.
    - **Truncation:** if the stream ends before a block's `content_block_stop`, or the
      joined JSON is invalid, the block keeps its raw joined string and the exchange
      is `partial`, not `failed`.
    - **Message-level fields:** `message_start` gives the id, model and initial
      usage. `message_delta` gives `stop_reason` and updated usage.
    - **Usage:** for each counter, the last value the stream reported wins.
  - **Hashed payload:** a block's hash covers its content (text, reasoning text and
    signature, redacted reasoning data, tool input, tool result content). The adapter
    declares `cache_control` as its hash-excluded field through the parser seam, so it
    is left out of block, system and tools hashes alike. It stays in the raw body.
  - **Tolerance:**
    - An unknown content-block type, other than the tool types above, or an unknown
      field, becomes `unknown` with its raw JSON kept, never a parse failure.
    - A truncated or aborted stream parses as far as it goes, and the events are
      flagged `partial`.
  - **Per-exchange status:** each exchange records `parse`: `ok`, `partial`, `skipped`,
    `unsupported_encoding` or `failed`. A failure keeps the raw bodies and is logged
    and counted.
  - **Schema version:** every event carries a canonical schema version, so a later
    parser change can re-parse stored raw bodies.
- **Ordering.** Parsing runs in the capture worker, after the raw exchange is stored. A
  parse failure never loses the raw capture.
- **Shutdown,** in this order:
  1. The HTTP server shuts down (001 waits for in-flight streams). Exchanges that end
     during this step are still accepted into the queue.
  2. The sink stops accepting.
  3. The queue drains within whatever remains of `shutdown_timeout`.
  4. The store closes.

  Anything left undrained is counted in one final log line.
- **Config** (ADR 0002 rules):

  | Key | Env var | Default |
  |---|---|---|
  | `capture.enabled` | `GATEWAY_CAPTURE_ENABLED` | `true` |
  | `capture.dir` | `GATEWAY_CAPTURE_DIR` | `$XDG_DATA_HOME/llm-gateway`, else `~/.local/share/llm-gateway` |
  | `capture.queue_size` | `GATEWAY_CAPTURE_QUEUE_SIZE` | `256` |
  | `capture.workers` | `GATEWAY_CAPTURE_WORKERS` | `2` |
  | `capture.max_body_bytes` | `GATEWAY_CAPTURE_MAX_BODY_BYTES` | `33554432` (32 MiB) |
  | `capture.memory_limit` | `GATEWAY_CAPTURE_MEMORY_LIMIT` | `268435456` (256 MiB) |

  - Sizes and counts are integers greater than 0; anything else fails with
    `invalid value`.
  - `capture.dir` must be absolute. A relative path fails with `invalid value`, naming
    the key and source, so a second store never appears silently when the binary runs
    from another folder.
  - `capture.enabled: false` means no tee, no queue and no store. The gateway then
    proxies exactly as it does in 001.
  - `config.example.yaml` gains these keys at their defaults. `capture.dir`'s default
    depends on the environment, so it is shown commented out.
  - `docker-compose.yml` mounts a named volume and sets `GATEWAY_CAPTURE_DIR` to an
    absolute path on it.
- **Logs.**
  - The `request` access line gains `capture`: `queued`, `dropped_queue_full`,
    `dropped_memory` or `off`. The value is decided when the exchange ends, which
    001's deferred access line allows.
  - The worker logs `capture_failed`, with `request_id` and `stage` (`store` or
    `parse`), on a store or parse failure.
  - No log line ever carries a header value, a body or a query string (001).
- **ADRs.**
  - ADR 0004: capture store (OQ-1), covering SQLite plus zstd blob files and the two
    new dependencies: a pure-Go SQLite driver (no cgo) and a zstd library, plus
    `andybalholm/brotli` (pure Go) for decoding `br` in the shared content decoder. `zstd` decoding
    reuses the blob library. It records the trade-off: raw bodies are stored whole for fidelity and re-parsing, so raw
    request storage grows quadratically with session length, and only parsed content
    is deduplicated.
  - ADR 0005: secrets in captured content (OQ-5).
  - PLAN.md §8 and §9 are updated in the same PR.
- **`gateway dump`.** A read-only subcommand that shows one exchange.
  - **Forms:** `gateway dump <request-id>` and `gateway dump --last`.
  - **Output, in order:**
    1. the exchange row's fields;
    2. the request and response bodies, zstd-decompressed. Unless `--raw` is given,
       each body's `Content-Encoding` is also decoded through the shared helper
       (Content decoding, above), for all five encodings it supports. An unsupported
       encoding prints the raw bytes plus a one-line notice, and dump does not fail;
    3. the exchange's canonical events, as JSON lines, content hashes included.
  - **Read-only:** it opens the database read-only, never creates or migrates it, and
    never writes a file. A missing store or an unknown ID exits non-zero with the
    reason.
  - It finds the store through the same config sources as the gateway.
  - It knows no wire format: it prints stored bytes and canonical events only. Content
    decoding is generic HTTP, not provider logic.
  - `gateway` with any other positional argument still fails as it does in 001.
- **Docs.** A short `docs/capture.md` covers where the store lives, what's in it, what
  is and isn't redacted, and how to inspect it: with `gateway dump` first, and with the
  `sqlite3` CLI second, until the viewer exists.
  - PLAN.md §3's Claude Code "Known client limits" entry gains a line in the
    implementation PR (research Q4): Claude Code's `HEAD /api/hello` sends
    `User-Agent: Bun/<version>` and no `x-app`, so it is labelled `unknown`, on
    purpose. `docs/clients/claude-code.md` already points at that entry and is
    unchanged.

## Out of scope
- **Sessions:** session and agent-ID extraction (`x-claude-code-session-id`), grouping,
  the `session` event (Phase 4). The raw headers are stored, so Phase 4 can backfill.
- **Timeline and viewer:** listing, filtering, searching or any viewer over the store
  (Phase 4). `gateway dump` shows one exchange only.
- **Cost:** pricing, cost of any kind, token estimation or auditing (Phase 5). `usage`
  records only what the provider reported.
- **Masking secrets inside bodies,** and encryption at rest (OQ-5 deferred, ADR 0005).
- **Chunked or message-level dedup of raw bodies.** Raw bodies are stored whole; only
  parsed content is deduplicated. Raw request storage grows quadratically with
  session length; zstd absorbs most of it; retention is a later spec.
- **Retention:** pruning, compaction, size caps on the store, and cleanup of orphan
  blobs.
- **Other stores:** Postgres, MongoDB, an external queue or Kafka (the seams allow them
  later).
- **Parsers** for anything but `POST /v1/messages`, and every other protocol (003 and
  later).
- **Mapping `search_result` and `container_upload` blocks.** In v1 both become
  `unknown`, with their raw JSON kept.
- **Model or any body-derived field in the access log.** The access line is written
  before parsing, which is async.
- **Re-parse tooling** over stored raw bodies (the schema version makes it possible
  later).
- **Metrics:** Prometheus counters for drops and failures (Phase 9); log lines stand in
  until then.
- **Changing forwarded traffic** in any way.
- **Hot config reload.**

## Do
- Keep 001's fidelity intact. Every 001 test passes unchanged with capture on.
- Tee on the side: the forward path never waits on the capture copy, the queue, the
  store or the parser.
- Drop and count rather than block, and make every drop visible in a log line.
- Redact before queueing, so an unredacted secret never sits in the queue, the store
  or a log.
- Write a blob in full before its row, so a crash never leaves a row pointing at
  nothing. The accepted other side: a failed row insert can leave a blob with no row
  (an orphan), which retention cleans up later.
- Keep wire-format knowledge (`tool_use`, `content_block_delta`, `x-api-key`) in
  `internal/protocols/anthropic/`. Core holds only the canonical event types and the
  interfaces.
- Keep unknown content: an unknown block, field or event type is kept or skipped,
  never an error.
- Test the parser against the committed golden stream, plus new scrubbed fixtures for
  a tool-use turn, a server-tool turn, a non-streaming response and an upstream error.

## Don't
- **Don't buffer the forwarded stream** to capture it. The tee copies what's already
  been written; streams are sacred (PLAN.md §5).
- **Don't write to the store on the request goroutine.** Synchronous writes add
  storage latency to every token (PLAN.md §8).
- **Don't decompress or re-encode forwarded bytes.** Only the captured copy is decoded,
  and only for parsing and `gateway dump`.
- **Don't store an auth header value, anywhere,** including in blobs, event JSON or
  error text.
- **Don't let a parser's output shape leak provider names into core's event types.**
  The events are canonical, and 003 must produce the same ones from OpenAI.
- **Don't start the gateway with capture silently off** when the store fails to open.
  Fail fast; the user can set `capture.enabled: false` on purpose.
- **Don't use cgo.** It breaks the single static binary (PLAN.md §8, Packaging).
- **Don't add a dependency without an ADR.**

## Agnosticism check
- **Protocol:** the Anthropic adapter gains header and query redaction lists, a
  parser, and the parser's hash-excluded field list.
- **Client:** unchanged. Captured exchanges carry the client label from 001's profiles.
- **Provider:** unchanged.

`internal/core/` changes. That's justified: the tee, the `CaptureSink` interface, the
principal resolver, the redaction step, the parser interface and the canonical event
types are protocol-agnostic by construction, and PLAN.md §2 puts the event model at
the centre of core. The `Store` implementation lives outside core. Two tests keep it
honest:
- 001's AC34 purity test still passes, now also covers the new files, and its denylist
  gains the Anthropic wire names core must not spell (AC56).
- A test-only adapter with a test-only parser is captured, redacted and turned into
  canonical events with no change to core.

## Open questions resolved here
- OQ-1 (capture store) → `docs/decisions/0004-capture-store.md`: SQLite index and
  events, plus zstd-compressed content-addressed blob files for bodies.
- OQ-5 (secrets inside content) → `docs/decisions/0005-secrets-in-captured-content.md`:
  stored as-is on a trusted machine, with `0700`/`0600` permissions. Masking is a
  later opt-in.

## Acceptance criteria
Config
- **AC1** `TestLoad_CaptureDefaults` — the six `capture.*` keys have the defaults in the
  table, `capture.dir` included.
- **AC2** `TestLoad_CaptureEnvOverridesFile` — each `GATEWAY_CAPTURE_*` var overrides
  the file value.
- **AC3** `TestLoad_InvalidCaptureValues` — each fails with `invalid value`, naming the
  key and source. Subtests: `zero`, `negative` and `not_a_number` for each size and
  count, and `relative_dir` for `capture.dir`.
- **AC4** `TestLoad_ExampleFileIsDefaults` — still passes with the capture keys in
  `config.example.yaml`.

Fidelity under capture
- **AC5** `TestCapture_ForwardingUnchanged` — 001's forwarding, streaming, compression
  and error tests pass with capture enabled.
- **AC6** `TestCapture_StreamNotDelayed` — with a store that blocks forever, each SSE
  event still reaches the client before upstream sends the next.
- **AC7** `TestCapture_DisabledIsPassthrough` — with `capture.enabled: false`, no file
  is created under `capture.dir` and the access line has `capture: off`.
- **AC8** `TestCapture_OutgoingRequestHasNoGetBody` — with capture on, the upstream
  request's `GetBody` is `nil`. It guards against a later change that would enable
  replays, and with them double capture.

Capture and store
- **AC9** `TestCapture_ExchangeStored` — each exchange is stored with every field in the
  Scope list, and its bodies read back byte-identical to what went over the wire.
  Subtests: `streamed`, `non_streamed`.
- **AC10** `TestCapture_EncodedBodyStoredAsSent` — a gzip response is stored still
  compressed, byte-identical.
- **AC11** `TestCapture_BodyOverCapTruncated` — a body over `max_body_bytes` is
  forwarded whole. The stored copy is exactly the cap long and the exchange is flagged
  `truncated`.
- **AC12** `TestCapture_AbortedExchangesRecorded` — each produces an exchange with the
  matching flag and the bytes seen. Subtests: `client_disconnect`, `upstream_abort`,
  `gateway_502`.
- **AC13** `TestStore_BlobDedup` — two exchanges with identical request bodies store
  that body once.
- **AC14** `TestStore_BlobBeforeRow` — a failure between blob and row leaves no row
  pointing at a missing blob.
- **AC15** `TestStore_Permissions` — the store directory is `0700` and every file in it
  is `0600`.
- **AC16** `TestStore_OpenFailsFast` — an unwritable `capture.dir` at startup exits
  non-zero, naming the path and the reason.
- **AC17** `TestStore_MigratesFromEmpty` — a fresh directory is created and migrated,
  and the schema version is recorded.
- **AC18** `TestStore_SmallContentInline` — a 100-byte body is stored inline, and no
  blob file is created.
- **AC19** `TestStore_LargeContentBlob` — a body over 4 KiB is stored as a
  zstd-compressed blob file named by its hash.
- **AC20** `TestStore_ConcurrentWorkersNoBusyErrors` — with `capture.workers` = 8, 500
  exchanges give zero `capture_failed` lines and 500 stored exchanges.
- **AC21** `TestStore_OpenReaderDoesNotFailWrites` — a long read transaction on another
  connection produces no store failures.
- **AC22** `TestCapture_PrincipalLocal` — every stored exchange and event has
  `principal_id: local`.

Capture never blocks
- **AC23** `TestCapture_QueueFullDrops` — with the queue full, the request succeeds, the
  access line has `capture: dropped_queue_full`, and no request waits.
- **AC24** `TestCapture_MemoryLimitDrops` — the request is forwarded in full and the
  access line has `capture: dropped_memory`. Subtests:
  - `over_limit`: the budget is already spent when the exchange starts;
  - `crosses_limit_mid_stream`: the stream reaches the client whole, and the memory in
    use returns to its previous level.
- **AC25** `TestCapture_StoreDeadClientUnaffected` — requests succeed byte-identical,
  and each gets a `capture_failed` line with `stage: store`. Subtests: `db_deleted`,
  `db_read_only`, `write_fails`.
- **AC26** `TestCapture_ShutdownDrainsQueue` — subtests:
  - `queued_stored`: queued exchanges are stored before exit;
  - `stream_ends_during_server_shutdown`: a stream that ends while the HTTP server
    shuts down has its exchange stored;
  - `undrained_counted`: exchanges left when `shutdown_timeout` runs out are counted in
    one final line.

Redaction
- **AC27** `TestRedact_AuthHeaders` — sentinels in `Authorization`, `x-api-key`,
  `Cookie`, `Set-Cookie` and `Proxy-Authorization` appear nowhere in the database or in
  any decompressed blob. The header names are present with `[REDACTED]`.
- **AC28** `TestRedact_ForwardedTrafficUntouched` — the same sentinels still reach
  upstream and the client byte-identical.
- **AC29** `TestRedact_AdapterQueryParams` — a test adapter's declared secret query
  parameter is redacted in the store, and forwarded unchanged.

Anthropic parser
- **AC30** `TestParse_GoldenStream` — the committed `stream.sse` yields the expected
  `request`, `message` and `usage` events (golden JSON), with `parse: ok`.
- **AC31** `TestParse_GoldenStreamGzip` — the committed `stream.sse`, gzipped in the
  test and marked `Content-Encoding: gzip`, yields the same events as the
  identity-encoded golden.
- **AC32** `TestParse_ToolUseTurn` — a tool-use fixture yields `tool_call` events with
  id, name, input and `executed_by: client`, and the next request's `tool_result`
  events carry the matching `tool_call_id`, set from the wire's `tool_use_id`.
- **AC33** `TestParse_ServerToolUse` — a new scrubbed fixture: a streamed turn with one
  `server_tool_use`, its `web_search_tool_result`, and one client `tool_use`. It yields
  `executed_by: provider` for the server tool's call and result, `executed_by: client`
  for the client call, and the IDs line up.
- **AC34** `TestParse_NonStreaming` — a JSON response yields the same events as its
  streamed equivalent.
- **AC35** `TestParse_UpstreamError` — each yields an `error` event with the provider's
  type and message. Subtests: `status_429`, `sse_error_event`.
- **AC36** `TestParse_UnknownBlockAndEventKept` — an unknown content-block type becomes
  `unknown` with its raw JSON, and an unknown SSE event is skipped. Parse is `ok`.
- **AC37** `TestParse_ToolInputSplitAcrossDeltas` — a tool input split over several
  `input_json_delta` events is joined and parsed once, at `content_block_stop`.
- **AC38** `TestParse_ThinkingSignatureKept` — a reasoning block has its joined
  thinking text and the `signature_delta` signature.
- **AC39** `TestParse_RedactedThinkingKept` — a `redacted_thinking` block becomes a
  `reasoning` block with `redacted: true` and its `data` kept byte-identical.
- **AC40** `TestParse_UsageLastValueWins` — each usage counter holds the last value the
  stream reported.
- **AC41** `TestParse_TruncatedStreamPartial` — a stream cut mid-block yields events up
  to the cut, flagged `partial`. Subtests: `identity`, `gzip` (the cut falls inside the
  compressed body).
- **AC42** `TestParse_TruncatedToolInputPartial` — a stream cut inside a tool input
  keeps the raw joined string on the block, and the exchange is `partial`, not
  `failed`.
- **AC43** `TestParse_ContentEncodings` — a table test. Subtests `gzip`,
  `deflate_zlib`, `deflate_raw`, `br` and `zstd` are each decoded and parsed with
  `parse: ok`; `unknown_encoding` is stored raw and marked `unsupported_encoding`.
- **AC44** `TestParse_SystemChangeChangesHash` — two requests that differ only in
  `system` have different `system_hash` values and the same `tools_hash`; two that
  differ only in `cache_control` on the system array have the same `system_hash`.
- **AC45** `TestParse_ReasoningRequestedFlag` — `reasoning_requested` follows the
  request's top-level `thinking` field. Subtests: `absent` (false), `disabled` (false),
  `enabled` (true), `unknown_type` (true).
- **AC46** `TestParse_OtherPathsSkipped` — `count_tokens`, `/v1/models` and
  `HEAD /api/hello` are stored with `parse: skipped`.
- **AC47** `TestParse_FailureKeepsRaw` — a malformed body gives `parse: failed` and a
  `capture_failed` line with `stage: parse`, and the raw bodies are stored.
- **AC48** `TestStore_BlockContentDedup` — two requests resending the same message
  store its content once.
- **AC49** `TestStore_ResponseMessageDedupsWithNextRequest` — verified against fixtures:
  turn N's response message and its copy in turn N+1's request share one content hash
  and one stored copy. Whether real Claude Code resends the message unchanged is
  AC59's check (research Q3).
- **AC50** `TestStore_SystemAndToolsStoredOnce` — two turns with the same system and
  tools give one stored copy of each.
- **AC51** `TestFixtures_NoIdentifiers` — still passes over the new fixtures.

`gateway dump`
- **AC52** `TestDump_PrintsExchange` — prints the row's fields, both bodies and the
  canonical events as JSON lines, in that order. Subtests: `streamed`, `non_streamed`,
  `gzip` (decoded), `br` (decoded), `zstd` (decoded), `raw_flag` (left encoded),
  `unsupported_encoding` (raw bytes plus a one-line notice, exit zero).
- **AC53** `TestDump_Last` — `--last` prints the most recent exchange.
- **AC54** `TestDump_UnknownIDFails` — exits non-zero with the reason. Subtests:
  `unknown_id`, `missing_store`.
- **AC55** `TestDump_NeverWritesStore` — a fresh directory stays empty, and an existing
  database's mtime is unchanged.

Agnosticism
- **AC56** `TestCore_NoProviderOrClientIdentifiers` — still passes, covering the new
  core files. Its denylist, kept in the test, is extended beyond provider and client
  names with the Anthropic wire names core must not spell: block, delta and event
  type names, and the adapter's hash-excluded field. Canonical names are never on it.
- **AC57** `TestCore_TestParserNeedsNoCoreChange` — a test-only adapter and parser,
  registered from the test, are captured, redacted and turned into canonical events.

Logging and secrets
- **AC58** `TestCapture_SecretsNotLogged` — sentinels in auth headers, the query and the
  body appear in no log line, across a stored, a dropped and a failed capture.

Manual (evidence recorded in the PR)
- **AC59** `ManualSmoke_ClaudeCodeCaptured` — a real Claude Code session with a tool
  call, run through the gateway, shows in `gateway.db` one exchange per request, with
  `tool_call` and `tool_result` events that line up. No auth value appears, checked by
  `grep` over `gateway dump` output for every exchange. The PR records the session's
  turn count, the total raw body bytes, and the on-disk store size.
  - In a session with at least 3 turns, run `gateway dump` on turn N and turn N+1, and
    record whether turn N's assistant message and its resent copy in N+1 share one
    content hash. The result is recorded as research Q3's answer. If they don't, a
    follow-up fix (not this spec) adjusts the canonical encoding or the adapter's hash
    exclusions.
- **AC60** `ManualSmoke_KillStoreMidSession` — deleting or `chmod 000`-ing
  `capture.dir` mid-session leaves Claude Code working normally, with `capture_failed`
  lines in the log.
- **AC61** `ManualDocs_CapturePage` — `docs/capture.md` covers the location, contents,
  redaction scope, and inspection with `gateway dump` first and `sqlite3` second.

## Open questions
- [OPEN] Are the `capture.max_body_bytes` (32 MiB) and `memory_limit` (256 MiB)
  defaults right for real Claude Code sessions? Measure during the manual smoke test,
  and log the peak `capture.memory_limit` usage during it.
- [OPEN] Does Claude Code resend an assistant message's blocks unchanged, apart from
  the fields the Anthropic adapter excludes from the hash? Answered by AC59's manual
  smoke step (research Q3). It doesn't block approval; a mismatch is a follow-up fix,
  not a change to this spec.
