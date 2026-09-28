---
status: approved
spec: ./spec.md
plan: ./plan.md
research: ./research.md
---

# 002 — Tasks

Small, ordered, checkable steps. The agent ticks them as it goes. A task held up by a
query in `research.md` ends with `(blocked: Q2)`; once that query is answered it becomes
`(shaped: Q2)`. The link stays on the task line either way — the status lives in
`research.md`.

Each task is one commit. When it is done, `make verify` passes. Tests land in the same
task as the code they cover, and every task starts by writing the failing tests it
names. A test that passes on arrival (a guard over behaviour an earlier task built) is
proved by breaking the code for a moment and watching it fail, then reverting; never
committed broken. "Done" names the proof, and the ACs a task contributes to are listed
at the end of its line. An AC whose evidence spans tasks is listed on each of them.

Layout the tasks assume:
- `internal/core` tests are in package `core_test`, with 001's test adapter (`test`,
  `/t`) and test profile. Core never names a provider, so no core test does either.
- Every store lives in `t.TempDir()`. No test resolves or touches the default data
  dir (spec, Do); T16 adds the guard that proves it.
- Every capture test that ends in a cancel, an abort or a shutdown runs 001's leak
  check, extended in T13 to stacks through `internal/capture`.
- The Q9 and Q10 spikes are done (research, 2026-09-28) and have no task.

## Decisions and dependencies

- [ ] T1 — `docs/decisions/0004-capture-store.md` and
  `docs/decisions/0005-secrets-in-captured-content.md`, from `0000-template.md`,
  `status: proposed` (T27 flips them when PLAN.md changes).
  - 0004 records: SQLite index and events, plus zstd content-addressed blob files
    (OQ-1); the confirmation of PLAN.md §8's "Proposed" capture pipeline; the three
    dependencies with the version each is pinned at and its licence, and the rejected
    options (plan, New dependencies); the spike results the driver was verified against
    (Q9, Q10); the quadratic raw-storage trade-off. `modernc.org/sqlite` is pinned at
    v1.59.0, the spiked version; the other two at their current releases, looked up
    without adding them to `go.mod`.
  - 0005 records: secrets inside prompts and tool results are stored as-is on a trusted
    machine, protected by `0700`/`0600`; masking and encryption at rest are a later
    opt-in (OQ-5).
  - No `go get` here: each dependency is added by the task that first imports it, at the
    version 0004 pins (T3, T10).

  Done: both ADRs follow the template's four sections, `go.mod` is unchanged, and `make
  verify` passes. Commit: `docs(capture): decide the capture store and its dependencies`
  (no AC; spec ADRs) (shaped: Q1, Q9, Q10)

## Config

- [ ] T2 — `internal/config`: the `capture` block, parsed like `upstreams` (one level,
  scalars, unknown keys rejected). `Config.Capture{Enabled, Dir, QueueSize, Workers,
  MaxBodyBytes, MemoryLimit}` with the spec's defaults; `Dir` stays `""` unless set. New
  fixed reason `invalid value`: a bool other than `true`/`false`, an integer that
  doesn't parse or isn't `> 0`, a relative `capture.dir`. `DefaultCaptureDir(lookupEnv)`
  returns `$XDG_DATA_HOME/llm-gateway` when that is absolute, ignores a relative one,
  else `$HOME/.local/share/llm-gateway` (`HOME` through `lookupEnv`, never
  `os.UserHomeDir`), else not-ok. `config.example.yaml` gains the block at the defaults,
  each key commented with its env name, `dir:` commented out.
  `TestLoad_ErrorsNeverContainValue` gains a row for `invalid value`. Tests first, in
  `internal/config/load_capture_test.go`: `TestLoad_CaptureDefaults` (the six keys;
  `DefaultCaptureDir` subtests `xdg_absolute`, `relative_xdg_ignored`, `home_only`,
  `neither`), `TestLoad_CaptureEnvOverridesFile` (each `GATEWAY_CAPTURE_*`),
  `TestLoad_InvalidCaptureValues` (`zero`, `negative`, `not_a_number` per size and
  count, `not_a_bool`, `relative_dir`; key and source named). Done: those pass, and
  `TestLoad_ExampleFileIsDefaults` passes unchanged. Commit: `feat(config): add the
  capture settings` (AC1, AC2, AC3, AC4) (no research link)

## Content decoding

- [ ] T3 — `go get github.com/klauspost/compress@<version>` and
  `github.com/andybalholm/brotli@<version>`, at the versions ADR 0004 pins.
  `internal/contentcoding/decode.go`: `Decode(encoding, body, limit) ([]byte, Status)`
  with `Complete`, `CutShort`, `Unsupported`. The header value is split on commas and
  decoded in reverse; an unknown token is `Unsupported`. `identity`, `gzip`, `deflate`
  (zlib header detected, else raw), `br` (`andybalholm/brotli`), `zstd`
  (`klauspost/compress/zstd`). A read error part-way returns what was decoded with
  `CutShort`; output is read through `io.LimitReader(limit+1)` and anything past `limit`
  is dropped with `CutShort`. No provider name, no wire format. Tests first, in
  `decode_test.go`: `TestDecode` as a table (each coding round-trips; `gzip, br`
  stacked; `deflate_zlib`, `deflate_raw`; `unknown` is `Unsupported`; a truncated `gzip`
  and `br` give their prefix and `CutShort`; `TestDecode_CapsOutput`: a body inflating
  past `limit` gives exactly `limit` bytes and `CutShort`). Done: the table passes under
  `-race`. Commit: `feat(capture): add the shared content decoder` (supports AC44, AC54)
  (shaped: Q1, Q7)

## Core

- [ ] T4 — `internal/core/event.go` and `canonical.go`: the canonical event types
  exactly as the plan's Interfaces list them (`EventKind`, `Source`, `ExecutedBy`,
  `BlockType`, `Block`, `RequestEvent`, `MessageEvent`, `ToolCallEvent`,
  `ToolResultEvent`, `UsageEvent`, `ErrorEvent`, `Event`, `SchemaVersion = 1`), and
  `Canonicalize(events, excluded)`. Canonical form: decode with `UseNumber`, re-encode
  sorted, compact, `SetEscapeHTML(false)`, sha256 lowercase hex. Exclusions apply only
  to a block's top-level keys and to each system entry's and tool definition's top-level
  keys, never inside tool input or tool result content, nor to a tool event's raw JSON.
  A message's content is its role plus each block's type and hash in order; a
  `tool_call` block contributes `{id, name, input_hash}` and a `tool_result` block
  `{tool_call_id, is_error, content_hash}`; `stop_reason` is not part of it. Tests
  first, in `canonical_test.go`: `TestCanonicalize_KeyOrderAndNumbers`,
  `TestCanonicalize_HTMLKept`, `TestCanonicalize_ExclusionTopLevelOnly` (a nested
  excluded key in tool input changes the hash; a top-level one on a block doesn't),
  `TestCanonicalize_MessageHash` (same blocks, same hash; a `stop_reason` doesn't change
  it; a reordered block does), `TestCanonicalize_Seq` (events keep their order). Done:
  those pass. Commit: `feat(core): add canonical events and their encoding` (supports
  AC45, AC49, AC50, AC51, AC52) (shaped: Q5)
- [ ] T5 — `internal/core/parser.go` and `principal.go`: `Parser` (`Parse`,
  `HashExcludedFields`), `ParseInput` (with `DecodeLimit`), `ParseResult`, `ParseStatus`
  and its five values; the optional adapter interfaces `SecretDeclarer` and
  `ParsingAdapter`; `PrincipalResolver`, `LocalPrincipal`, `PrincipalLocal`. Nothing
  calls them yet. Tests first: `TestLocalPrincipal_AlwaysLocal`, and a compile-time
  assertion that the test adapter can implement both optional interfaces. Done: both
  pass, and 001's tests pass unchanged. Commit: `feat(core): add the parser seam and
  principal resolver` (supports AC22, AC59) (no research link)
- [ ] T6 — `internal/core/capture.go` (`Budget`, `Body`) and `tee.go`. `Budget`:
  `TryReserve`, `Release`, `InUse`, atomic, and `Peak()`, raised by a compare-and-swap
  max on each successful reservation (Q12). `teeReader`: copies each `Read`'s bytes as a
  chunk after reserving; checks `max_body_bytes` first and flags `truncated` at the cap;
  on a refused reservation releases everything and stops copying; a mutex guards it
  against the transport's goroutine; `seal()` stops copying and records whether `io.EOF`
  was seen, for `request_incomplete`. `teeWriter`: embeds `http.ResponseWriter`, copies
  each `Write` after the inner `Write` returns, records the status and a header snapshot
  at `WriteHeader`, and has `Unwrap()`. Neither sets `GetBody`. Not wired into the proxy
  yet. Tests first, in `tee_test.go`: `TestBudget_ReserveRelease` (under `-race`, many
  goroutines), `TestBudget_Peak` (the peak is the highest `InUse` reached, and never
  drops on `Release`), `TestTeeReader_CopiesAndCaps`, `TestTeeReader_SealRecordsEOF`
  (sealed before EOF reports incomplete; after EOF doesn't; a read after the seal is
  passed through, not copied), `TestTeeWriter_Unwrap`
  (`http.NewResponseController(tw).Flush()` reaches the inner flusher),
  `TestCapture_OutgoingRequestHasNoGetBody` (written here against a
  `httputil.ProxyRequest` built by hand; T8 reruns it through the proxy). Done: those
  pass under `-race`. Commit: `feat(core): add the capture tees and memory budget` (AC8,
  supports AC11, AC12, AC24, AC61) (shaped: Q12)
- [ ] T7 — `internal/core/redact.go`: header redaction (a clone; core's four,
  `Authorization`, `Proxy-Authorization`, `Cookie`, `Set-Cookie`, plus the adapter's
  `SecretHeaders()`; case-insensitive; value becomes `[REDACTED]`, name kept) and raw
  query redaction (split by hand on `&` and the first `=`; a parameter whose unescaped
  name is listed keeps its name and gets `[REDACTED]`; everything else byte-identical).
  Tests first, in `redact_test.go`: `TestRedact_HeaderClone` (the input header is
  untouched), `TestRedact_HeaderNamesKept`, `TestRedact_QueryKeepsBytes` (`;`, `%zz`,
  repeated and empty parameters byte-identical). Done: those pass. Commit: `feat(core):
  redact captured headers and query` (supports AC27, AC29) (no research link)
- [ ] T8 — Wire capture into the proxy. `Capture{Sink, Budget, MaxBodyBytes,
  Principal}`, `WithCapture`, `NewRegistry(log, opts...)` (001 callers unchanged);
  `CaptureSink` and `Exchange` (with `RequestIncomplete`, `Parser`, `Excluded`,
  idempotent `Release`). `Meta` gains `RequestID`, `PrincipalID`, `Status`, `Capture`.
  `ServeHTTP` sets the principal, wraps `w` in the `teeWriter`; `Rewrite` wraps the body
  as `watcher(tee(body))` and snapshots `pr.Out.Header`. `Settle` calls `finishCapture`:
  seal both tees, build the `Exchange`, redact, `Submit`, set `Meta.Capture` (`queued`,
  `dropped_queue_full`, `dropped_memory`, `off`). With no `WithCapture` the proxy is
  001's and `Capture` is `off`. `internal/server`: `accessLog` sets `Meta.RequestID`;
  `proxyFields` adds `capture`. Tests first, with a recording sink in
  `internal/core/capture_test.go`: `TestCapture_MemoryLimitDrops` (`over_limit`,
  `crosses_limit_mid_stream`: forwarded whole, `capture: dropped_memory`, `Budget.InUse`
  back to its prior value), `TestRedact_AdapterQueryParams` (a test adapter's secret
  query parameter redacted in the submitted exchange, forwarded unchanged),
  `TestCapture_ExchangeFields` (every `Exchange` field from a streamed and a
  non-streamed request, `principal_id: local`, `request_incomplete` when upstream
  answers before reading the body), `TestCapture_OutgoingRequestHasNoGetBody` (rerun
  through the proxy), `TestAccessLog_CaptureField` (`off` with no capture; no `capture`
  field off-proxy). Done: those pass under `-race`, and every 001 test passes unchanged.
  Commit: `feat(core): capture exchanges at the end of each request` (AC8, AC22, AC24,
  AC29, supports AC7, AC12) (no research link)
- [ ] T9 — The AC5 wrappers, test-only. `internal/core/capture_fidelity_test.go` and
  `internal/protocols/anthropic/capture_fidelity_test.go`:
  `TestCapture_ForwardingUnchanged` lists 001's forwarding, streaming, compression and
  error test functions that send a request through the proxy (001's AC8, AC12,
  AC14–AC24, AC26–AC31, AC47, AC48) and runs each as a subtest; the shared helper turns
  capture on (recording sink, real `Budget`) when `t.Name()` starts with
  `TestCapture_ForwardingUnchanged/`. After each function returns, its subtest asserts
  the recording sink received at least one exchange. Proved by renaming the wrapper for
  a moment: every subtest fails on the zero-exchange assertion; then reverted. Done:
  both wrappers pass under `-race`, and 001's test bodies are unchanged. Commit:
  `test(capture): run 001's proxy tests with capture on` (AC5) (no research link)

## Store

- [ ] T10 — `go get modernc.org/sqlite@v1.59.0`, the version ADR 0004 pins.
  `internal/store`: `Open(dir, log)`. `MkdirAll(dir, 0700)` then `Chmod(0700)`;
  `gateway.db` pre-created `O_CREATE|0600`; one writer handle, `SetMaxOpenConns(1)`,
  pragmas `journal_mode=WAL`, `busy_timeout=5000`, `synchronous=NORMAL`,
  `foreign_keys=ON`; migration 1 (the plan's `content`, `exchanges` with
  `request_incomplete`, `events` tables and indexes) inside `BEGIN IMMEDIATE`, `PRAGMA
  user_version` recorded; a newer database fails with `schema newer than gateway`. Every
  error names the path. The inode check (Q10, Q11): device and inode recorded at open, a
  fresh `os.Stat` before each write transaction.
  - A missing file (`ENOENT`) or a changed device or inode logs `store file deleted or
    replaced; restart the gateway to resume capture` with `path` once, marks the store
    failed for the rest of the run, and every later write returns an error at once. The
    store never recreates or reopens the database.
  - Any other stat error (permission denied after `chmod 000`) logs `store file
    unreadable: <err>` with `path` once per run as a warning, does not mark the store
    failed, and lets the write itself decide. Tests first, in
    `internal/store/store_test.go`:
  `TestStore_MigratesFromEmpty`, `TestStore_Permissions` (under umask `0022` and `0000`:
  directory `0700`, every file `0600`, `-wal` and `-shm` included),
  `TestStore_OpenFailsFast` at package level (an unwritable dir, a file in place of the
  dir, a newer schema: each errors with the path), `TestStore_DeletedDatabase` (after
  unlinking `gateway.db`, the next write fails, exactly one `store file deleted or
  replaced` line across several writes, no new `gateway.db`; subtest
  `stat_permission_denied_not_deleted`: after `chmod 000` on the directory, no `deleted
  or replaced` line, one `store file unreadable` warning, and an inline-only write still
  succeeds through the open handles). Done: those pass under `-race`. Commit:
  `feat(store): open and migrate the capture database` (AC15, AC16, AC17, supports AC25,
  AC62) (shaped: Q10, Q11)
- [ ] T11 — `internal/store`: content and writes. `SaveExchange` and `SaveParse` (the
  `capture.Store` shape from the plan; the interface itself lands in T13), each one
  transaction on the writer after its blobs are on disk. Content of 4096 bytes or less
  inline in `data`; larger zstd-compressed to `blobs/<hash[0:2]>/<hash[2:]>` via temp
  file in the target directory, `fsync`, rename; the row is inserted (`INSERT OR
  IGNORE`) after the blob exists, and an existing row skips the write. Raw bodies are
  hashed as captured (a gzip body as gzip). A fault hook between blob and row, exported
  through `export_test.go`. Tests first: `TestStore_BlobDedup`,
  `TestStore_BlobBeforeRow` (the hook fails the row: no row points at a missing blob;
  the orphan blob is allowed), `TestStore_SmallContentInline` (100 bytes, no blob file),
  `TestStore_LargeContentBlob` (over 4 KiB, a zstd file named by its hash that
  decompresses to the input), `TestStore_OpenReaderDoesNotFailWrites` (a long read
  transaction on a second connection, writes keep succeeding). Done: those pass under
  `-race`. Commit: `feat(store): store exchanges and content by hash` (AC13, AC14, AC18,
  AC19, AC21) (no research link)
- [ ] T12 — `internal/store`: `OpenReader(dir)` (checks `gateway.db` exists first, never
  creates it, `mode=ro`, never `immutable=1`), `Exchange(id)`, `Last()`, `Content(hash)`
  (inline or blob, decompressed), `Events(id)` in `seq` order, `ErrNotFound`. Tests
  first: `TestReader_MissingStoreNotCreated` (a fresh directory stays empty),
  `TestReader_StoppedStore` (reads everything; `gateway.db`'s mtime is unchanged;
  `-wal`/`-shm` may appear, per Q9), `TestReader_WhileWriterOpen` (sees committed
  writes, doesn't block the writer), `TestReader_UnknownID` (`ErrNotFound`). Done: those
  pass under `-race`. Commit: `feat(store): add the read-only reader` (supports AC56,
  AC57) (shaped: Q9)

## Capture pipeline

- [ ] T13 — `internal/capture/sink.go` and `store.go`: the `Store` interface, `Config`,
  `Sink` with `NewSink` (workers started through `logging.Go`), `Submit`, `Close`,
  `Counts`. `Submit` holds a read lock, checks `closed`, does a non-blocking send; full
  or closed returns `false` and releases the exchange. `Close` takes the write lock,
  sets `closed`, closes the channel, drains until the context's deadline, then cancels
  the workers' context (which the in-flight store call gets), counts and releases what
  is left, and returns that count. The worker loop here only calls `Store.SaveExchange`
  and `Release`; T14 adds the rest. 001's leak check learns `internal/capture`. Tests
  first, in `internal/capture/capture_test.go` and `sink_test.go`, with a blocking and a
  failing fake store and the core proxy under `server.New`:
  `TestCapture_StreamNotDelayed` (a store that never returns; each SSE event reaches the
  client before upstream sends the next), `TestCapture_QueueFullDrops` (`queue_size` 1,
  blocking store; the request succeeds, `capture: dropped_queue_full`, no request
  waits), `TestSink_SubmitAfterCloseReturnsFalse` (under `-race`: `Submit` racing and
  following `Close` returns `false`, releases, never panics),
  `TestSink_CloseCountsUndrained`. Done: those pass under `-race` with the leak check.
  Commit: `feat(capture): add the bounded capture queue` (AC6, AC23, supports AC26) (no
  research link)
- [ ] T14 — `internal/capture/pipeline.go`: per exchange, `SaveExchange` (on error:
  `capture_failed` with `request_id` and `stage: store`, counted, stop), then the
  exchange's parser (nil: `skipped`; a panic is recovered and becomes `failed`), then
  `core.Canonicalize` with the parser's exclusions, then `SaveParse` with the principal
  (on error: `stage: store`); a `failed` status logs `stage: parse`; `Release` always.
  Tests first: `TestStore_ConcurrentWorkersNoBusyErrors`
  (`internal/capture/sink_test.go`: 8 workers, real store, 500 exchanges, zero
  `capture_failed`, 500 rows), `TestCapture_StoreDeadClientUnaffected`
  (`internal/capture/capture_test.go`, real store and proxy: `db_deleted` with subtest
  `deleted_logged_once`, `db_read_only`, and `write_fails` with a failing fake; requests
  byte-identical, one `capture_failed` with `stage: store` each),
  `TestCapture_ParsePanicIsFailed`, and `TestCore_TestParserNeedsNoCoreChange`
  (`internal/core/capture_pipeline_test.go`: a test adapter and test parser, registered
  from the test, captured, redacted and turned into canonical events in a real store,
  with no edit to `internal/core`). Done: those pass under `-race`. Commit:
  `feat(capture): parse and store captured exchanges` (AC20, AC25, AC59) (shaped: Q10)
- [ ] T15 — `internal/core/purity_test.go`: `TestCore_NoProviderOrClientIdentifiers`
  gains the wire names in its denylist (`cache_control`, `tool_use`, `tool_use_id`,
  `content_block`, `message_delta`, `input_json_delta`, `thinking_delta`; `x-api-key` is
  already there) over `internal/core`, and walks `../capture`, `../store` and
  `../contentcoding` for the provider and client names only. Each directory must hold at
  least one checked file. Proved first against a throwaway file in each directory
  holding a forbidden word, then removed. Done: it passes on the tree. Commit:
  `test(core): extend the purity check to capture packages` (AC58) (no research link)

## Wiring

- [ ] T16 — `cmd/gateway/run.go`: after config load and before bind, when
  `capture.enabled`: resolve the directory (`Dir` or `DefaultCaptureDir`; neither gives
  `invalid value` for `capture.dir` from source `default`, exit `2`), `store.Open` (an
  error logs the path and a fixed reason and exits `1`), `capture.NewSink`,
  `core.WithCapture` with `LocalPrincipal`. `deps` gains `openStore` for tests. Shutdown
  after `srv.Shutdown`: `sink.Close` with what is left of the shutdown context,
  `store.Close`, one `capture stopped` line with `undrained`, the counts and
  `memory_peak_bytes` from `Budget.Peak()` (Q12). The test data-dir guard:
  `startGateway` puts `GATEWAY_CAPTURE_DIR=<t.TempDir()>` in the env map unless the test
  sets one; `binary_test.go` does the same for the child; `cmd/gateway/main_test.go`
  adds a `TestMain` pointing `HOME` and `XDG_DATA_HOME` at a temp dir and failing if
  `llm-gateway` appears under it. Tests first, in `cmd/gateway/run_capture_test.go`:
  `TestCapture_DisabledIsPassthrough` (no file under `capture.dir`, `capture: off`),
  `TestStore_OpenFailsFast` (through `run`: an unwritable dir exits non-zero, naming the
  path and reason, nothing bound), `TestCapture_PrincipalLocal` (every stored exchange
  and event has `principal_id: local`), `TestCapture_ShutdownDrainsQueue`
  (`queued_stored`, `stream_ends_during_server_shutdown`, `undrained_counted` with a
  slow store through `deps.openStore`; the line carries `memory_peak_bytes` above zero
  after a captured request), `TestRun_DefaultCaptureDirUnresolvable`. Proved for the
  guard: drop the helper's env line for a moment, see `TestMain` fail. Done: those pass,
  and every 001 `cmd/gateway` test passes with capture on and unchanged bodies. Commit:
  `feat(capture): open the store and drain it on shutdown` (AC5, AC7, AC16, AC22, AC26,
  supports AC61) (shaped: Q12)
- [ ] T17 — `cmd/gateway/run_capture_test.go`, end-to-end through `run` with a real
  store and a fake upstream (a guard over T8–T16, plus the fixes it finds):
  `TestCapture_ExchangeStored` (`streamed`, `non_streamed`: every Scope field of the
  exchange record, bodies byte-identical to the wire),
  `TestCapture_EncodedBodyStoredAsSent` (a gzip response stored compressed,
  byte-identical), `TestCapture_BodyOverCapTruncated` (forwarded whole, stored copy
  exactly the cap, `truncated`), `TestCapture_AbortedExchangesRecorded`
  (`client_disconnect`, `upstream_abort`, `gateway_502`, `request_read_after_seal`).
  Each is proved by breaking the matching code for a moment. Done: those pass under
  `-race` with the leak check. Commit: `test(capture): prove exchanges are stored end to
  end` (AC9, AC10, AC11, AC12) (no research link)
- [ ] T18 — `internal/protocols/anthropic/adapter.go` implements `SecretDeclarer`
  (`SecretHeaders() = [x-api-key]`, `SecretQueryParams()` empty). Tests first, in
  `cmd/gateway/run_capture_test.go` through `run`: `TestRedact_AuthHeaders` (sentinels
  in `Authorization`, `x-api-key`, `Cookie`, `Set-Cookie`, `Proxy-Authorization` appear
  nowhere in the database or any decompressed blob; names present with `[REDACTED]`),
  `TestRedact_ForwardedTrafficUntouched`, `TestCapture_SecretsNotLogged` (sentinels in
  auth headers, query and body in no log line across a stored, a dropped and a failed
  capture, via `deps.openStore`). Done: those pass. Commit: `feat(anthropic): declare
  the api key header as secret` (AC27, AC28, AC60) (no research link)

## Fixtures (human)

- [ ] T19 — Record the new Anthropic fixtures (needs-human). The implementer has no
  Claude Code login and must never extract the OAuth token. Nothing else in this spec is
  recorded; `non_streaming` and `error/*` are synthetic (T21).

  What the human runs:
  1. Start 001's throwaway recording proxy (plan 001 "Golden fixture"), extended to also
     save each request body, still forcing `Accept-Encoding: identity`, forwarding to
     `https://api.anthropic.com`.
  2. In a scratch directory holding `notes.txt` (three lines), run
     `ANTHROPIC_BASE_URL=http://127.0.0.1:<proxy port> claude` and send "How many lines
     are in notes.txt? Use the Read tool." Save that turn's streamed response as
     `testdata/tool_turn/response.sse` (with a `.headers` file as in 001) and the next
     `POST /v1/messages` request body, which carries the `tool_result`, as
     `testdata/tool_turn/next_request.json`.
  3. In the same session send "Search the web for the latest Go release and answer in
     one line." Save the WebSearch request's streamed response, which holds the
     `server_tool_use` and `web_search_tool_result` blocks, as
     `testdata/server_tool/websearch.sse`.
  4. Scrub as in 001 (UUIDs, keys, tokens, emails, `organization`, `account_uuid`,
     `org_id`, `request-id` set to `req_scrubbed`), and trim system prompts and tool
     descriptions in the request body to what the tests need; remove paths, user names
     and anything secret in tool results.
  5. Add a `testdata/README.md` entry per file: date, model, method, what was scrubbed,
     and `recorded`.
  6. `go test ./test/conventions/ -run TestFixtures_NoIdentifiers`.

  Evidence to paste into the PR: the three README entries, the test output, and `ls -l`
  of the new files. Commit (by the human): `test(anthropic): record the tool and web
  search fixtures` (supports AC32, AC34, AC50, AC53) (shaped: Q8) (needs-human)

## Anthropic parser

- [ ] T20 — `internal/protocols/anthropic/parser.go` and `request.go`. The adapter
  implements `ParsingAdapter`; `HashExcludedFields() = [cache_control]`. `Parse` returns
  `skipped` for anything but `POST /v1/messages`; decodes both bodies with
  `contentcoding.Decode` limited to `DecodeLimit` (`Unsupported` →
  `unsupported_encoding`, `CutShort` → `partial`). The request side: model, stream,
  `max_tokens`, system and tools as raw content, `has_system`, `has_tools`,
  `cache_hints` (any `cache_control` at any depth), `reasoning_requested` (a top-level
  `thinking` whose `type` isn't `disabled`), tool names; each message as a `message`
  event (`source: request_history`) with blocks typed `text`, `reasoning`
  (`redacted_thinking` with `redacted: true`), `media`, `tool_call`, `tool_result`,
  `unknown`, followed by its `tool_call` and `tool_result` events with `tool_call_id`
  from the wire `tool_use_id`, and `executed_by` per the spec. Unknown fields are kept
  in raw content. Tests first, in `parser_test.go` with request bodies built in the
  test: `TestParse_SystemChangeChangesHash`, `TestParse_ReasoningRequestedFlag`
  (`absent`, `disabled`, `enabled`, `unknown_type`), `TestParse_OtherPathsSkipped`
  (`count_tokens`, `/v1/models`, `HEAD /api/hello`). Done: those pass. Commit:
  `feat(anthropic): parse messages requests into canonical events` (AC45, AC46, AC47)
  (shaped: Q2, Q4, Q5, Q6, Q7)
- [ ] T21 — `internal/protocols/anthropic/response.go`: a non-streamed JSON response
  (the assistant message, `source: response`, with `stop_reason`, its tool events and
  `usage`), an upstream error status with a JSON error body (one `error` event with type
  and message), and the truncated-JSON path (a body flagged `truncated` keeps the
  complete messages and blocks before the cut and is `partial`, never `failed`;
  malformed without truncation is `failed`). New synthetic fixtures, built from
  Anthropic's documented shapes, each with a README entry saying `synthetic`:
  `testdata/non_streaming/request.json`, `response.json`,
  `testdata/error/response_429.json`, `testdata/error/stream_error.sse`. Tests first:
  `TestParse_NonStreaming` (its events equal those of the streamed equivalent; the SSE
  half lands in T22, so until then the comparison is against a golden JSON),
  `TestParse_UpstreamError` (`status_429`; `sse_error_event` added in T22),
  `TestParse_FailureKeepsRaw` (`internal/protocols/anthropic/pipeline_test.go`, through
  the real pipeline and store: `parse: failed`, `capture_failed` with `stage: parse`,
  raw bodies stored), and the AC11 subtest `non_streamed_json_parses_partial` in
  `cmd/gateway`. Done: those pass, and `TestFixtures_NoIdentifiers` passes. Commit:
  `feat(anthropic): parse json responses and upstream errors` (AC11, AC35, AC36, AC48)
  (shaped: Q2, Q8)
- [ ] T22 — `internal/protocols/anthropic/stream.go`: SSE reassembly per the plan
  (`text_delta` and `thinking_delta` joined, `partial_json` joined and parsed only at
  `content_block_stop`, `signature_delta` set, `citations_delta` appended;
  `message_start` and `message_delta` for id, model, `stop_reason` and usage, last value
  per counter wins; `ping` and unknown events skipped; an `error` event becomes `error`;
  a block with no stop or bad joined JSON keeps its raw string and makes the exchange
  `partial`). Tests first, against the committed golden `stream.sse` and small streams
  built in the test: `TestParse_GoldenStream` (golden JSON of the events, `parse: ok`),
  `TestParse_GoldenStreamGzip`, `TestParse_UnknownBlockAndEventKept`,
  `TestParse_ToolInputSplitAcrossDeltas`, `TestParse_ThinkingSignatureKept`,
  `TestParse_RedactedThinkingKept`, `TestParse_UsageLastValueWins`,
  `TestParse_TruncatedStreamPartial` (`identity`, `gzip`),
  `TestParse_TruncatedToolInputPartial`, `TestParse_ContentEncodings` (`gzip`,
  `deflate_zlib`, `deflate_raw`, `br`, `zstd`, `unknown_encoding`); T21's
  `TestParse_NonStreaming` now compares against the streamed equivalent, and
  `TestParse_UpstreamError` gains `sse_error_event` from `stream_error.sse`. Done: those
  pass under `-race`. Commit: `feat(anthropic): reassemble streamed responses` (AC30,
  AC31, AC35, AC36, AC37, AC38, AC39, AC40, AC41, AC42, AC43, AC44) (shaped: Q1, Q2, Q7)
## `gateway dump`

- [ ] T25 — `cmd/gateway/dump.go`: `run` hands `args[0] == "dump"` to `runDump`, whose
  flag set takes `-config`, `-raw`, `-last` and at most one request ID. It loads config
  as `run` does, resolves the directory, and opens `store.OpenReader`; a missing store
  or unknown ID exits `1` with the reason. Output: the exchange row as one JSON line;
  `== request body` and `== response body`, decoded through `contentcoding` with
  `capture.max_body_bytes` as the limit unless `-raw`, the header line naming what was
  decoded, an unsupported encoding printing raw bytes with a notice and exit `0`; `==
  events` and one JSON line per event, content hashes included. Any other positional
  argument to `gateway` still fails as in 001. Tests first, in
  `cmd/gateway/dump_test.go`: `TestDump_PrintsExchange` (`streamed`, `non_streamed`,
  `gzip`, `br`, `zstd`, `raw_flag`, `unsupported_encoding`), `TestDump_Last`,
  `TestDump_UnknownIDFails` (`unknown_id`, `missing_store`), `TestDump_NeverWritesStore`
  (a fresh directory stays empty; an existing database's mtime is unchanged, with the
  gateway stopped and running; no blob is written). Done: those pass under `-race`.
  Commit: `feat(capture): add the gateway dump command` (AC54, AC55, AC56, AC57)
  (shaped: Q7, Q9)

## Docs and deployment

- [ ] T26 — `docs/capture.md`: where the store lives (`capture.dir`, its default and the
  XDG rule), what is in it (exchanges, events, content inline or in blobs), what is and
  isn't redacted (auth headers and declared query parameters; bodies stored as-is, ADR
  0005), and how to inspect it: `gateway dump` first, then `sqlite3` with two example
  queries. It states the store-failure behaviour (deleted or replaced database, restart
  to resume). Done: the page covers AC63's four items, and `make verify` passes. Commit:
  `docs(capture): add the capture page` (AC63) (no research link)
- [ ] T27 — `PLAN.md` and ADR status, in one commit: §8 "Capture pipeline" becomes
  Decided (ADR 0004); §8 "Capture store" gets its choice and Decided (ADR 0004); §9
  marks OQ-1 and OQ-5 resolved with their ADR links; §3's Claude Code known-limits entry
  gains the line that `HEAD /api/hello` sends `User-Agent: Bun/<version>` and no
  `x-app`, so it is labelled `unknown` on purpose. ADRs 0004 and 0005 become `status:
  approved`. Done: `git diff` shows only those edits, and `make verify` passes. Commit:
  `docs(repo): record the capture decisions in the plan` (no AC; spec ADRs and Docs)
  (shaped: Q4)
- [ ] T28 — `docker-compose.yml` mounts a named volume at `/var/lib/llm-gateway` and
  sets `GATEWAY_CAPTURE_DIR=/var/lib/llm-gateway`; the `Dockerfile` copies an empty
  directory there `--chown=nonroot` from the build stage. Test first, in
  `test/conventions/compose_test.go`: `TestCompose_CaptureDirOnVolume` (the env value is
  absolute and is the mount path of a named volume). Done: it passes, and `docker
  compose config` parses the file if Docker is present (if not, say so in the note).
  Commit: `build(deploy): keep captures on a named volume` (no AC; spec Config) (no
  research link)

- [ ] T23 — Tool turns, against the recorded fixtures. Needs T19's `tool_turn` and
  `server_tool` files; if they are absent, report BLOCKED on T19 and never fake a
  recording. Build the two assembled fixtures from recorded blocks, each block's bytes
  unchanged and each README entry saying so: `testdata/tool_order/response.sse`
  (`synthetic`: text, `tool_use`, text, from `tool_turn`'s blocks) and
  `testdata/server_tool/response.sse` (`synthetic (assembled from recorded blocks)`: the
  `server_tool_use` and `web_search_tool_result` from `websearch.sse`, plus a client
  `tool_use` from `tool_turn`). Tests first: `TestParse_ToolUseTurn` (`tool_turn`:
  `tool_call` events with id, name, input, `executed_by: client`, `source: response`; in
  `next_request.json` the same calls as `source: request_history`, and `tool_result`
  events with the matching `tool_call_id`), `TestParse_MessageKeepsToolBlockOrder`
  (blocks `text`, `tool_call`, `text`, and the block's `tool_call_id` matches its
  event), `TestParse_ServerToolUse` (`executed_by: provider` for the server call and
  result, `client` for the client call, IDs line up). Done: those pass, and
  `TestFixtures_NoIdentifiers` passes over the new files. Commit: `feat(anthropic): map
  tool calls and results by origin` (AC32, AC33, AC34, AC53) (shaped: Q2, Q5, Q8)
- [ ] T24 — Dedup through the real pipeline and store, in
  `internal/protocols/anthropic/pipeline_test.go`. Needs T19's `tool_turn`. Tests first:
  `TestStore_BlockContentDedup` (two requests resending one message store its content
  once), `TestStore_ResponseMessageDedupsWithNextRequest` (`tool_turn`'s response
  message and its copy in `next_request.json` share one content hash and one stored
  copy), `TestStore_SystemAndToolsStoredOnce`,
  `TestStore_ExclusionNotAppliedInsideToolInput` (two tool inputs differing only in a
  nested `cache_control` get different hashes, both stored). A failure here is a bug in
  T4 or T20 to fix in this task, or, for AC50 on real data, a finding for Q3. Done:
  those pass. Commit: `test(anthropic): prove captured content deduplicates` (AC49,
  AC50, AC51, AC52) (shaped: Q8)

## Manual evidence (no commit; output goes into the PR)

- [ ] T29 — Claude Code smoke with capture (needs-human). Run after T1–T28 are ticked.
  The implementer has no Claude Code login.

  What the human runs:
  1. `make build`, then Terminal A: `GATEWAY_CAPTURE_DIR=/tmp/llmgw-cap bin/gateway 2>&1
     | tee /tmp/llmgw-smoke.log`.
  2. Terminal B, in a scratch directory with a few files:
     `ANTHROPIC_BASE_URL=http://127.0.0.1:7197/anthropic claude`. Hold one session of at
     least 3 turns, one of which uses a tool (for example "Read notes.txt"), then exit.
  3. `sqlite3 /tmp/llmgw-cap/gateway.db "select count(*) from exchanges"` and `grep -c
     '"msg":"request"' /tmp/llmgw-smoke.log` must match (one exchange per request).
  4. `sqlite3 /tmp/llmgw-cap/gateway.db "select kind, tool_call_id, source from events
     where kind in ('tool_call','tool_result') order by request_id, seq"`: each
     `tool_result`'s `tool_call_id` matches an earlier `tool_call`.
  5. For every exchange, `GATEWAY_CAPTURE_DIR=/tmp/llmgw-cap bin/gateway dump <id>` into
     `/tmp/llmgw-dump.txt`; then `grep -iE '^(authorization|x-api-key)|sk-ant-'
     /tmp/llmgw-dump.txt` shows only `[REDACTED]` values and no key.
  6. Q3: pick consecutive `/v1/messages` turns N and N+1 and compare turn N's `source:
     response` message `content_hash` with the matching `request_history` message in N+1
     (`select request_id, seq, source, content_hash from events where kind='message'
     order by request_id, seq`). Also compare one resent `tool_result`.
  7. Record the turn count, total raw body bytes (`select sum(c.size) from exchanges e
     join content c on c.hash in (e.request_body, e.response_body)`) and `du -sh
     /tmp/llmgw-cap`.
  8. Stop the gateway with Ctrl-C and copy `memory_peak_bytes` from the `capture
     stopped` line, next to `capture.max_body_bytes` (32 MiB) and `capture.memory_limit`
     (256 MiB).

  Evidence to paste into the PR: the outputs of steps 3–8. Then append a dated answer
  line under Q3 in `research.md` with step 6's result, and set its Status. If the hashes
  differ, a follow-up fix outside this spec adjusts the canonical encoding or the hash
  exclusions. Step 8's value answers the spec's open question on the `max_body_bytes`
  and `memory_limit` defaults: record it there as answered, with the number, in the PR
  that closes this task. (AC61) (shaped: Q3, Q8, Q12) (needs-human)
- [ ] T30 — Kill the store mid-session (needs-human). Same setup as T29, a fresh
  `GATEWAY_CAPTURE_DIR`.
  1. Mid-session, `rm /tmp/llmgw-cap2/gateway.db`, then send two more prompts. Expected:
     Claude Code answers normally; the log has exactly one `store file deleted or
     replaced` line and one `capture_failed` with `stage: store` per exchange after it.
  2. Restart the gateway on a fresh directory; mid-session `chmod 000 /tmp/llmgw-cap3`,
     then send two more prompts. Expected: Claude Code answers normally; one `store file
     unreadable` warning and no `store file deleted or replaced` line; exchanges that
     fit inline may still be stored, and those that need a blob (every real Claude Code
     request) log `capture_failed` with `stage: store`. `chmod 700` it afterwards.

  Evidence to paste into the PR: the relevant log lines from both runs and a line saying
  Claude Code kept working. (AC62) (shaped: Q10, Q11) (needs-human)
- [ ] T31 — Read `docs/capture.md` against AC63 (needs-human): location, contents,
  redaction scope, inspection with `gateway dump` first and `sqlite3` second. Evidence
  in the PR: one line per item with the heading it is under. (AC63) (needs-human)

## Coverage check

Every AC in `spec.md` (AC1–AC63) maps to at least one task:

| AC | Tasks | AC | Tasks | AC | Tasks |
|---|---|---|---|---|---|
| AC1 | T2 | AC22 | T8, T16 | AC43 | T22 |
| AC2 | T2 | AC23 | T13 | AC44 | T3, T22 |
| AC3 | T2 | AC24 | T6, T8 | AC45 | T4, T20 |
| AC4 | T2 | AC25 | T10, T14 | AC46 | T20 |
| AC5 | T9, T16 | AC26 | T13, T16 | AC47 | T20 |
| AC6 | T13 | AC27 | T7, T18 | AC48 | T21 |
| AC7 | T8, T16 | AC28 | T18 | AC49 | T4, T24 |
| AC8 | T6, T8 | AC29 | T7, T8 | AC50 | T4, T24 |
| AC9 | T17 | AC30 | T22 | AC51 | T4, T24 |
| AC10 | T17 | AC31 | T22 | AC52 | T4, T24 |
| AC11 | T6, T17, T21 | AC32 | T19, T23 | AC53 | T19, T21, T23 |
| AC12 | T6, T8, T17 | AC33 | T23 | AC54 | T3, T25 |
| AC13 | T11 | AC34 | T19, T23 | AC55 | T25 |
| AC14 | T11 | AC35 | T21, T22 | AC56 | T12, T25 |
| AC15 | T10 | AC36 | T21, T22 | AC57 | T12, T25 |
| AC16 | T10, T16 | AC37 | T22 | AC58 | T15 |
| AC17 | T10 | AC38 | T22 | AC59 | T5, T14 |
| AC18 | T11 | AC39 | T22 | AC60 | T18 |
| AC19 | T11 | AC40 | T22 | AC61 | T29 |
| AC20 | T14 | AC41 | T22 | AC62 | T30 |
| AC21 | T11 | AC42 | T22 | AC63 | T26, T31 |

No AC is missing: 63 of 63 appear. T1, T27 and T28 name no AC: they carry the spec's
ADRs, Docs and Config (compose) bullets.
