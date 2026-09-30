---
status: approved
spec: ./spec.md
---

# 002 — Implementation plan

> Agent-drafted against the spec, human-approved. Written in a separate session, after
> the spec is approved. Do not write this file at the same time as the spec.

## Approach

Capture hangs off 001's proxy at the three places it already owns: `Rewrite` (the
request body), the response writer, and `Meta.Settle` (the end of every exchange,
aborts included). Nothing new sits on the byte path except two read-through or
write-through copies. Everything after `Settle` runs off the request goroutine: a
bounded queue, worker goroutines, a store, a parser.

Core gains the protocol-neutral parts the spec's Agnosticism check lists: the tee,
the memory budget, `CaptureSink`, the principal resolver, redaction, the parser seam,
the canonical event types and the canonical encoding. The queue and the pipeline
(`internal/capture`), the SQLite store (`internal/store`), the content decoder
(`internal/contentcoding`) and the Anthropic parser live outside core.

Where this plan cites research queries, the answers are in `research.md`. All of
Q1–Q10 are answered except Q3, which the manual smoke answers (AC61).

### Package layout

| Package | Holds | Imports |
|---|---|---|
| `internal/core` | Tee, `Budget`, `Exchange`, `CaptureSink`, `PrincipalResolver`, redaction, `Parser` seam, canonical events, canonical encoding and hashing | `config`, `logging` (as today) |
| `internal/capture` | `Sink`: the bounded queue and workers; the pipeline (store raw, parse, canonicalise, store events); the `Store` interface it writes through | `core`, `logging` |
| `internal/store` | The SQLite store: schema and migrations, content (inline or blob), writer and read-only reader | `core`, the SQLite driver, `zstd` |
| `internal/contentcoding` | `Decode`: `identity`, `gzip`, `deflate` (zlib and raw), `br`, `zstd` | stdlib, `brotli`, `zstd` |
| `internal/protocols/anthropic` | Redaction lists, `HashExcludedFields`, the parser (JSON and SSE) | `core`, `contentcoding` |
| `internal/config` | The `capture.*` keys; `DefaultCaptureDir` | (unchanged) |
| `internal/server` | The access line's `capture` field; `Meta.RequestID` | `core` (unchanged) |
| `cmd/gateway` | Wiring, startup and shutdown order; the `dump` subcommand | all of the above |

`internal/core` imports none of `capture`, `store` or `contentcoding`. `capture`
imports no adapter. `cmd/gateway` stays the only package that imports an adapter.

### Where an exchange begins and ends

- **Start.** `Proxy.ServeHTTP` fills three new `Meta` fields next to 001's: the
  `PrincipalID` from the resolver, and, when capture is on, a `capture` state holding
  the two tees. `accessLog` already has the request ID; it sets `Meta.RequestID` when
  it creates the `Meta`, so core never imports `server`.
- **Request body.** `Rewrite` wraps `pr.Out.Body` in a request tee, under 001's
  watcher: `watcher(tee(body))`. The tee copies what each `Read` returns, which is
  what the transport sends. It never sets `GetBody` (AC8). A nil body or
  `http.NoBody` is not wrapped, since a wrapped `NoBody` would change how the
  transport frames the request (`teeRequestBody` returns nil). It also snapshots
  `pr.Out.Header` after 001's five steps: the request headers as they went upstream.
  It records whether a `Read` returned `io.EOF` before the seal. If not, the
  exchange gets `request_incomplete` (spec, Exchange record), for example when
  upstream answered before the transport finished sending the body (AC12
  `request_read_after_seal`).
- **Response body.** `ServeHTTP` hands `ReverseProxy` a `teeWriter` around `w`. It
  copies each `Write` after the inner `Write` returns, and records the status and a
  header snapshot at `WriteHeader` (the first status of 200 or more; a 1xx is
  forwarded, not recorded; a `Write` with no `WriteHeader` records 200). It embeds
  `http.ResponseWriter` and adds `Unwrap()`, so `http.NewResponseController` still reaches chi's `Flush` (001 Q7).
  001's AC23 guards that: a writer that hides `Flush` fails it. The error handler
  writes through the same writer, so a gateway `502` body is captured (AC12).
- **End.** `Meta.Settle` already runs from `accessLog`'s `defer` for every exchange,
  including `http.ErrAbortHandler` aborts. After it decides the abort flags, it calls
  `m.finishCapture()`. That seals both tees, builds the `Exchange`, redacts it,
  submits it, and sets `Meta.Capture` to `queued`, `dropped_queue_full`,
  `dropped_memory` or `off`. `accessLog` logs `capture` when `Protocol` is set
  (AC7, AC23, AC24). The value is decided before the line is written, as the spec
  requires.
- **Details fixed in T8.**
  - `NewProxy` keeps 001's signature and builds a proxy with capture off; the
    registry builds its proxies through an unexported constructor that takes the
    `Capture`. Tests reach it through `export_test.go`.
  - The exchange's `Path` is the inbound `r.URL.Path` with the adapter prefix
    stripped, as the access line logs it, not the upstream path with `base_url`'s
    path joined on.
  - `Meta.Status` is set in `modifyResponse` (upstream's status) and in the error
    handler (the status it writes). The exchange takes the response tee's recorded
    status and falls back to `Meta.Status` when nothing was written.
  - The response header snapshot leaves out entries with no value, such as the
    empty `Content-Type` entry `ServeHTTP` adds to stop sniffing: it is never sent.
  - The access line carries `capture` whenever `Meta.Capture` is set, which every
    proxy does (`off` included), so a line off the proxy keeps exactly 000's keys.
- **Details fixed in T17.**
  - A gateway-made error before upstream read anything (a refused dial) leaves the
    request copy sealed empty, so that exchange has `request_incomplete` as well as
    `gateway_error`, and no request body: the copy holds only what the transport read.
  - `ended_at` is taken when the handler returns, which can be after the client has
    read the whole response; tests bound only `started_at` by the client's clock.

The request tee is written by the transport's goroutine and sealed on the handler's,
so it holds a mutex. A `Read` after the seal is passed through and not copied. The
response tee and `Meta` are only touched on the handler's goroutine, as in 001.

### Memory: reserve, transfer, release

One `core.Budget` per process: `limit` from `capture.memory_limit`, an atomic
`inUse`, and an atomic `peak`, raised by a compare-and-swap max on each successful
reservation. `Peak()` is logged as `memory_peak_bytes` in the `capture stopped` line
(Q12). An atomic `dropped` counts the exchanges dropped for memory: `finishCapture`
returns before `Submit` for those, so the sink never sees them, and `Dropped()` is
where the `dropped_memory` count comes from.
1. **Reserve.** Each tee chunk calls `TryReserve(n)` before copying `n` bytes. A
   chunk is a copy of that `Read` or `Write`, appended to a list of chunks, so the
   bytes held equal the bytes reserved: no slice doubling behind the budget's back.
2. **Cap first.** Before reserving, a tee checks `capture.max_body_bytes`. It copies
   up to the cap, reserves only that, flags `truncated`, and stops copying. A
   truncated body is never dropped for memory (spec, Memory bound).
3. **Refuse.** When `TryReserve` fails, the exchange releases everything both tees
   hold at once, marks itself `dropped_memory`, and copies nothing more. Forwarding
   carries on (AC24). Both tees reserve through one per-exchange `reservation`
   (mutex, bytes held, dropped flag) in `capture.go`: the refusal gives back the
   exchange's whole reservation there, and clears both tees' chunk copies at once,
   each under its own copy's mutex.
4. **Transfer.** At `finishCapture` the reservation moves to the `Exchange`. If
   `Submit` refuses it (queue full, sink closed), it is released there and then.
5. **Release.** The worker calls `ex.Release()` once the pipeline is done, whatever
   the outcome. `Release` is idempotent.

Header snapshots, the parser's working copies and decoded bodies are not budgeted:
headers are small, and the rest is bounded by `capture.workers` and the decode cap
(see Risks).

### Data flow

```
client ─► teeReader ─► transport ─► upstream         (request, transport goroutine)
upstream ─► ReverseProxy ─► teeWriter ─► client      (response, handler goroutine)
        │ chunks, reserved from Budget
        ▼
Meta.Settle ─► finishCapture: seal, build Exchange, redact headers and query
        │ CaptureSink.Submit (never blocks; false = dropped_queue_full)
        ▼
capture.Sink queue (capture.queue_size) ─► worker (capture.workers, logging.Go)
        1. Store.SaveExchange   raw row + both bodies (content-addressed)
        2. Parser.Parse         (nil parser → parse: skipped)
        3. core.Canonicalize    hashes, exclusions, stored content
        4. Store.SaveParse      events + content + the exchange's parse status
        5. ex.Release()
```
- A failure in step 1 logs `capture_failed` with `stage: store` and stops: there is no
  row for events to hang from.
- A parser panic is recovered in the worker and becomes `parse: failed`.
- A `failed` status logs `capture_failed` with `stage: parse`. A failure in step 4
  logs `stage: store`, and the raw capture from step 1 stays (spec, Ordering).
- Every drop and failure is also counted (`dropped_queue_full`, `dropped_memory`,
  `store_failed`, `parse_failed`), and the counts are logged in the shutdown line.
  `dropped_memory` is counted by the `Budget` at the drop site in `finishCapture`
  (`Budget.Dropped()`); the others by the sink (`Sink.Counts()`).
  Prometheus is Phase 9.

### Redaction (AC27–AC29)

`finishCapture` redacts before `Submit`, so nothing unredacted reaches the queue.
- **Headers.** A clone of each snapshot, with the value of every header on the list
  set to `[REDACTED]`. The list is core's four (`Authorization`,
  `Proxy-Authorization`, `Cookie`, `Set-Cookie`) plus the adapter's
  `SecretHeaders()`. Names are matched case-insensitively and kept.
- **Query.** The raw query is split on `&` and on the first `=` by hand, not with
  `url.ParseQuery`, which would reorder and re-escape it. A parameter whose unescaped
  name is on the adapter's `SecretQueryParams()` keeps its name and gets `[REDACTED]`
  as its value. Everything else stays byte-identical.
- Edge cases (T7). Every value of a secret header is replaced, so the value count
  stays (two `Cookie` lines stay two). Query names are unescaped with
  `url.QueryUnescape` (`+` is a space) and matched case-sensitively; a name that does
  not unescape is compared as sent. A parameter with no `=` has no value and is kept
  as sent; `key=` becomes `key=[REDACTED]`.
- The forwarded request and response are never touched. The snapshots are clones.

### Canonical encoding and hashing

`core.Canonicalize(events []Event, excluded []string) ([]StoredEvent, []Content,
error)` is the one place content becomes hashes. Every JSON content value is decoded
with `UseNumber`, re-encoded with sorted keys (`map[string]any` through
`encoding/json`), compact, with `SetEscapeHTML(false)`, then hashed with sha256 and
kept as a `Content{Hash, Bytes}`. Only the exclusion scope differs:

| Content | Exclusions applied |
|---|---|
| A message block (`Block.Content`) | to the block object's top-level keys |
| The system array, the tools array | to each entry's top-level keys |
| Tool input, tool result content | none (spec, Exclusion scope; AC52) |
| A tool event's raw block JSON | none; it is the record of what was sent |

The stored parsed content is the canonical form. The raw body keeps the original.

**Message hash.** A message's content is its canonical list of blocks: role, then
each block's type and hash in order. A `tool_call` block contributes the hash of
`{id, name, input_hash}` and a `tool_result` block `{tool_call_id, is_error,
content_hash}`. `stop_reason` is not part of it, since a resent copy has none. This is
what makes turn N's response message and its copy in turn N+1 share a hash (AC50) and
what AC61's manual step compares (Q3).
- A tool block's `{id, name, input_hash}` or `{tool_call_id, is_error, content_hash}`
  comes from the first tool event with that id after the message. A block with no
  such event is a parser contract violation, and `Canonicalize` returns an error, as
  it does for an event whose `Kind` has no matching payload or for invalid JSON.
- The message's content object (`{role, blocks: [{type, hash}]}`) and each tool
  block's reference object are stored as `Content` too, so every hash resolves.

**Payload.** Each `StoredEvent.Payload` is canonical JSON with snake_case keys:
`schema_version`, `kind`, `partial`, plus per kind:
- `request`: `model`, `stream`, `max_tokens`, `system_hash`, `tools_hash`,
  `has_system`, `has_tools`, `cache_hints`, `reasoning_requested`, `tool_names`;
- `message`: `index`, `role`, `source`, `stop_reason`, `content_hash`, `blocks`
  (`type`, `hash`, and `redacted` and `tool_call_id` when set);
- `tool_call`: `id`, `name`, `input_hash`, `executed_by`, `source`, `raw_hash`;
- `tool_result`: `tool_call_id`, `is_error`, `content_hash`, `executed_by`, `source`,
  `raw_hash`;
- `usage`: the four token counters (`null` when not reported) and `detail`, inline;
- `error`: `status`, `type`, `message`.

An empty hash means the value was absent. `Seq` is the event's index in the parser's
list.

### Anthropic parser

`anthropic.Adapter` gains `SecretHeaders() = [x-api-key]`, `SecretQueryParams() =
[]`, and `Parser()`. The parser:
- Returns `skipped` for anything but `POST /v1/messages` (AC47).
- Decodes both bodies with `contentcoding.Decode`, limited to `DecodeLimit`.
  `Unsupported` → `unsupported_encoding`, `CutShort` → `partial` (AC44, AC42).
- **Request.** A `json.Decoder` over the decoded body, `UseNumber`, into a struct
  with `json.RawMessage` fields: unknown fields are kept in the raw content, never an
  error.
  - `cache_hints`: any `cache_control` key at any depth of the request (a walk of the
    decoded tree).
  - `reasoning_requested`: a top-level `thinking` whose `type` isn't `disabled` (Q6).
  - Tool names come from `tools[].name`.
  - Each message becomes a `message` event (`source: request_history`), followed by a
    `tool_call` or `tool_result` event for each tool block in order, and a
    `tool_call` or `tool_result` block referencing it by `tool_call_id` (Q5).
- **Response, JSON.** The same block mapping, `source: response`, plus `stop_reason`
  and `usage`.
- **Response, SSE.** A line reader over the decoded body, event by event. There is no
  buffering problem here: it reads a stored copy. Per block index it holds a builder:
  - `text_delta` and `thinking_delta` are joined;
  - `partial_json` is joined and parsed only at `content_block_stop`;
  - `signature_delta` is set on the block, and `citations_delta` is appended.
  
  `message_start` gives the id, model and usage; `message_delta` gives `stop_reason`
  and usage, last value per counter wins (AC41). `ping` and unknown events are
  skipped (AC37). An `error` event becomes an `error` event (AC36). A block with no
  `content_block_stop`, or joined JSON that won't parse, keeps its raw string and the
  status becomes `partial` (AC42, AC43).
- **Upstream error.** A non-2xx status with a JSON error body becomes one `error`
  event with the provider's type and message (AC36).
- **Truncated bodies.** A body flagged `truncated` parses as far as it goes and gives
  `partial`, never `failed`. For JSON that means a tolerant pass that keeps the
  complete messages and blocks before the cut. `failed` is left for a body that is
  malformed without being truncated (AC11, AC48).
- **Tool origin.** `tool_use` gives `executed_by: client`. `server_tool_use` and
  `mcp_tool_use` give `executed_by: provider`, as do `mcp_tool_result` and any
  `*_tool_result` that carries a `tool_use_id` (AC34).
- **Block types.** `thinking` and `redacted_thinking` map to `reasoning`
  (`redacted: true`, `data` kept; AC39, AC40). `image` and `document` map to `media`.
  Anything else maps to `unknown`, `search_result` and `container_upload` included.
- `HashExcludedFields() = [cache_control]`.
- **Details fixed in T20.**
  - A message whose `content` is a string becomes one `text` block,
    `{"type":"text","text":…}`, so it hashes like the same text sent as a block. The
    raw body keeps the string.
  - The status is the worst of the two bodies' (ok, partial, unsupported_encoding,
    failed), each from `contentcoding.Decode` and the body's `truncated` flag. An
    unsupported request encoding gives no request events.
  - A request body that doesn't decode as a Messages request is `failed`, unless it
    is cut short (truncated or past `DecodeLimit`): then it is `partial` with no
    request events. Salvaging messages before a cut in the request is not done.
  - A block that can't be read as an object with the mapped fields (a non-object, or
    a wrong-typed field) is `unknown` with its JSON kept, not a failure.
  - `reasoning_requested` is true for a `thinking` that isn't an object as well: it
    has no `type`, so none that is `disabled`.
  - The response body is decoded, so its encoding and completeness count towards the
    status; its events come with T21 (JSON) and T22 (SSE).

The wire names live only in this package. Core's purity denylist gains them (AC58).

### Content decoding

`contentcoding.Decode(encoding string, body []byte, limit int64) ([]byte, Status)`.
The caller passes `capture.max_body_bytes` as `limit` (Q7); there is no new config.
- The header value is split on commas and decoded in reverse order, as HTTP stacks
  codings; any token it doesn't know makes the result `Unsupported`, with the body
  returned unchanged. Tokens are trimmed and matched case-insensitively; empty list
  elements are skipped, so an empty header is `identity`.
- `deflate` checks for a zlib header (`CMF` method 8 and `(CMF<<8|FLG) % 31 == 0`)
  and otherwise reads raw DEFLATE.
- A reader error part-way (`io.ErrUnexpectedEOF` and the like) returns what was
  decoded, with `CutShort`.
- Output stops at `limit` bytes. Anything past it is dropped and the result is
  `CutShort`, so the parser reports `partial` (Q7). The decoder reads through an
  `io.LimitReader` of `limit+1` bytes, so it never holds more than the cap plus one
  read.

### SQLite store

**Opening (`store.Open(dir, log)`).** The logger carries the inode check's two lines.
1. `os.MkdirAll(dir, 0700)`, then `Chmod(0700)`, since the umask can narrow
   `MkdirAll` but not widen it.
2. Create `gateway.db` with `O_CREATE|0600` if it's absent, before the driver opens
   it. SQLite gives `-wal` and `-shm` the database file's mode, so all three stay
   `0600`. Blobs are written through `os.CreateTemp` (`0600`) and renamed (AC15).
3. Open one writer handle: `SetMaxOpenConns(1)`, with the pragmas
   `journal_mode=WAL`, `busy_timeout=5000`, `synchronous=NORMAL`,
   `foreign_keys=ON`.
4. Migrate.

Any failure returns an error naming the path. `run` logs it and exits `1` before it
binds (AC16).

**Readers.** `store.OpenReader(dir)` checks that `gateway.db` exists, never creates
it, and opens `mode=ro`. `immutable=1` is never used: it gives torn reads while the
gateway writes. The Q9 spike showed that `mode=ro` on a stopped gateway's database
creates `-wal` and `-shm` (with the database's `0600` mode) and leaves `gateway.db`'s
mtime alone. So the spec's dump rule is "never writes `gateway.db` or a blob", and
AC57 checks exactly that.

**A deleted database (Q10).** The Q10 spike showed that writes after `gateway.db` is
unlinked succeed silently. So the store records the file's device and inode at open
(`syscall.Stat_t` from `os.Stat`). It compares them with a fresh `os.Stat` before each
write transaction (Q11):
- A missing file (`ENOENT`) or a changed device or inode is a store failure (AC25
  `db_deleted`), handled as below.
- Any other stat error, such as permission denied after `chmod 000` on
  `capture.dir`, is not. The store logs it once per run as a warning, `store file
  unreadable: <err>`, with the `path`, and lets the write itself decide. So an
  exchange that fits inline can still be stored through the open handles, and one
  that needs a blob fails (AC62).

The check is one `stat` per transaction, next to a transaction that already
`fsync`s.

Details fixed in T10:
- `gateway.db` is `Chmod`ed to `0600` after the pre-create too, so a narrow umask
  can't leave it unwritable; SQLite then gives `-wal` and `-shm` the same mode.
- The writer DSN is built with `url.URL` (`file:` plus `_pragma` parameters), so a
  `?`, `#` or `%` in the path can't be read as part of the query.
- Migration runs on one `sql.Conn` with a literal `BEGIN IMMEDIATE`; a database
  newer than the binary is refused without touching its `user_version`.
- All writes go through one unexported `write(ctx, fn)`: the inode check, then one
  transaction on the writer. T10's tests reach it through `export_test.go` with an
  inline `content` insert; T11's `SaveExchange` and `SaveParse` use it.
- A replaced file (same path, new inode) takes the same "deleted or replaced" path as
  an unlinked one.

Details fixed in T11:
- `write(ctx, items, fn)` now takes the content: inode check, then every blob on
  disk, then one transaction that inserts the content rows and runs `fn`. A
  test-only hook between the blobs and the transaction proves the order (AC14).
- A blob's directory is `fsync`ed after the rename too, so the rename is durable
  before the row that references it commits.
- An empty body is stored as no body (`request_body` / `response_body` NULL).
  Headers are stored as `{}` when there are none.
- `SaveParse` for an exchange the store doesn't hold returns `ErrNotFound` and
  writes nothing (the `ErrNotFound` T12's reader also uses).

Details fixed in T12:
- `OpenReader` stats `gateway.db` before the driver sees it; a missing file (or
  directory) is an error wrapping `fs.ErrNotExist` that names the path, and nothing is
  created. The reader DSN is `mode=ro` plus `busy_timeout`, built with `url.URL`.
- A reader never migrates: a `user_version` newer than the binary fails with `schema
  newer than gateway`, an older one (including an unmigrated file) with `schema older
  than gateway`.
- `Last()` is the exchange with the greatest `started_at`, ties to the one stored
  last (`rowid`).
- `ExchangeRow` and `EventRow` carry JSON tags named after the columns, so dump
  prints a row as one JSON line; a NULL reads as `""` (`ttfb_ns` as `null`).
- `Content(hash)` returns `ErrNotFound` for a hash that isn't 64 lowercase hex
  characters, so no caller string reaches a blob path. A blob whose decoded size
  differs from the row's `size` is an error.
- `Events(id)` is `ErrNotFound` for an unknown exchange and empty for one with no
  parse stored.

After a missing file or a changed inode:
- On the first one, the store logs one error line, `store file deleted or replaced;
  restart the gateway to resume capture`, with the database's `path`. The path is
  the store's own, not request data.
- The store then marks itself failed, sticky for the rest of the run. Every later
  write returns an error at once, without another `stat`. Each exchange then fails
  with `capture_failed` and `stage: store` and is counted, as with any store failure.
- The store never recreates or reopens the database mid-run. Capture resumes only
  after a restart, which opens or creates the store as at startup.

**Schema versioning.** Migrations are an ordered list of SQL strings in Go.
- At open, inside `BEGIN IMMEDIATE`, `PRAGMA user_version` is read, the missing steps
  are applied, and `user_version` is set.
- A database newer than the binary fails fast with `reason: schema newer than
  gateway`.
- Canonical events carry their own `schema_version`, `core.SchemaVersion = 1`, which
  is separate from the database's.

**Tables (migration 1).**
```sql
CREATE TABLE content (
  hash     TEXT PRIMARY KEY,          -- sha256, lowercase hex
  size     INTEGER NOT NULL,          -- bytes before zstd
  location TEXT NOT NULL CHECK (location IN ('inline', 'blob')),
  data     BLOB,                      -- the bytes when inline, else NULL
  CHECK ((location = 'inline') = (data IS NOT NULL))
) WITHOUT ROWID;

CREATE TABLE exchanges (
  request_id          TEXT PRIMARY KEY,
  principal_id        TEXT NOT NULL,
  protocol            TEXT NOT NULL,
  client              TEXT NOT NULL,
  auth                TEXT NOT NULL,
  method              TEXT NOT NULL,
  path                TEXT NOT NULL,   -- prefix stripped
  query               TEXT NOT NULL,   -- raw, redacted
  request_headers     TEXT NOT NULL,   -- JSON {name: [values]}, redacted
  response_headers    TEXT NOT NULL,
  status              INTEGER NOT NULL,
  started_at          INTEGER NOT NULL, -- unix nanoseconds
  ttfb_ns             INTEGER,          -- NULL: no response headers
  ended_at            INTEGER NOT NULL,
  stream              INTEGER NOT NULL,
  request_truncated   INTEGER NOT NULL,
  response_truncated  INTEGER NOT NULL,
  request_incomplete  INTEGER NOT NULL,  -- sealed before the request body's EOF
  truncated           INTEGER GENERATED ALWAYS AS (request_truncated OR response_truncated) VIRTUAL,
  client_disconnected INTEGER NOT NULL,
  upstream_aborted    INTEGER NOT NULL,
  gateway_error       TEXT,
  request_body        TEXT REFERENCES content(hash),  -- NULL: no body
  response_body       TEXT REFERENCES content(hash),
  parse               TEXT CHECK (parse IN ('ok','partial','skipped','unsupported_encoding','failed'))
                                                      -- NULL until the parse is stored
);
CREATE INDEX exchanges_started_at ON exchanges(started_at);
CREATE INDEX exchanges_principal ON exchanges(principal_id, started_at);

CREATE TABLE events (
  request_id     TEXT NOT NULL REFERENCES exchanges(request_id),
  seq            INTEGER NOT NULL,     -- order within the exchange
  kind           TEXT NOT NULL,        -- request, message, tool_call, tool_result, usage, error
  schema_version INTEGER NOT NULL,
  principal_id   TEXT NOT NULL,
  source         TEXT,                 -- request_history | response; NULL for other kinds
  partial        INTEGER NOT NULL,
  content_hash   TEXT REFERENCES content(hash), -- message content, tool input or result
  tool_call_id   TEXT,
  payload        TEXT NOT NULL,        -- the canonical event as JSON; content by hash
  PRIMARY KEY (request_id, seq)
) WITHOUT ROWID;
CREATE INDEX events_content ON events(content_hash);
CREATE INDEX events_tool_call ON events(tool_call_id);
CREATE INDEX events_principal ON events(principal_id, kind);
```
`truncated` is the spec's one flag. The two columns say which body, which the
parser needs.

**Inline and blob content.** Content of 4096 bytes or less is stored in `data`, with
`location = 'inline'`. Larger content is zstd-compressed to
`<dir>/blobs/<hash[0:2]>/<hash[2:]>`, with `location = 'blob'` and `data` NULL. Raw
bodies and parsed content share the table: the hash is the key either way (AC13,
AC18, AC19, AC49). Each write goes in order:
1. Skip if the row exists.
2. Otherwise write the blob to a temp file in the target directory, `fsync`, rename.
3. Insert the row (`INSERT OR IGNORE`) in the same transaction as the exchange or
   events that reference it.

A crash or failure between steps 2 and 3 leaves an orphan blob and never a row
without a blob (AC14). Two workers writing the same blob rename identical bytes over
each other, which is harmless.

**Writes.** `SaveExchange` and `SaveParse` each run in one transaction on the single
writer connection, after their blobs are on disk. Workers parse concurrently and wait
their turn for the connection, which gives no `SQLITE_BUSY` from the gateway's own
writers (AC20). A reader in WAL mode doesn't block the writer (AC21).

### Config

The `capture` block is parsed like `upstreams`: one level, fields read as scalars,
unknown keys rejected. The new reason `invalid value` is used for a bool that isn't
`true` or `false`, an integer that doesn't parse or isn't `> 0`, and a relative
`capture.dir`.

`Config.Capture.Dir` stays `""` unless the file or env sets it. `""` means the
default location, resolved in `run` by `config.DefaultCaptureDir(lookupEnv)`:
- `XDG_DATA_HOME` joined with `llm-gateway` when it's set and absolute;
- a relative `XDG_DATA_HOME` is ignored, per the XDG spec (AC1
  `relative_xdg_ignored`);
- otherwise `$HOME/.local/share/llm-gateway`, where `HOME` is read through
  `lookupEnv`, not `os.UserHomeDir`, so tests control it. A relative `HOME` counts as
  unset, since it would give a relative directory;
- with neither set, `run` reports `invalid value` for `capture.dir` from source
  `default` and exits `2`.

Keeping the default out of `Config` means `Defaults()` needs no environment, so 000's
`reflect.DeepEqual(cfg, Defaults())` checks and AC4 hold unchanged.
`config.example.yaml` shows `# dir:` commented out, with its env name.

### `gateway dump`

`run` checks `args[0] == "dump"` before flag parsing and hands the rest to
`runDump(d)`. Its own flag set takes `-config`, `-raw` and `-last` (Go's flag package
also accepts `--raw` and `--last`), plus at most one request ID. It loads config
exactly as `run` does, resolves the directory, and calls `store.OpenReader`. A
missing store or an unknown ID exits `1` with a reason (AC56).

Output on stdout:
1. The exchange row as one JSON line.
2. `== request body` and `== response body`, each followed by the decoded bytes.
   - The header line says which encoding was decoded; with `-raw`, nothing is
     decoded.
   - An unsupported encoding prints the raw bytes, with the header line saying so,
     and doesn't fail (AC54).
3. `== events`, then one JSON line per event in `seq` order, content hashes included.

Dump never names a wire format. Decoding is `contentcoding`, with the loaded
`capture.max_body_bytes` as its limit.

### Startup, shutdown and wiring (`run`)

**Startup,** after config load and before bind:
1. If `capture.enabled`, resolve the directory, `store.Open`, `capture.NewSink(...)`
   (which starts the workers through `logging.Go`), and build a `core.Capture{Sink,
   Budget, MaxBodyBytes, Principal}`.
2. Pass it to `core.NewRegistry(log, core.WithCapture(c))`. With no `WithCapture`,
   capture is `off` and the proxy behaves exactly as 001 (AC7).
3. `LocalPrincipal` is used either way, so `principal_id` is `local` (AC22).

**Shutdown,** after `srv.Shutdown` (successful or timed out):
1. `sink.Close(ctx)` stops accepting, then drains within whatever is left of the
   shutdown context, and returns the count left undrained. A `Submit` after `Close`
   returns `false` and releases the exchange; it never panics:
   - `Submit` holds a read lock, checks a `closed` flag and does a non-blocking send;
   - `Close` takes the write lock, sets `closed` and only then closes the channel, so
     no send can reach a closed channel;
   - workers range over the channel. At the context's deadline, `Close` cancels the
     workers' context, which the in-flight store call also gets. Workers then stop
     after their current item, and what is left in the channel is counted and
     released.
2. `store.Close()`.
3. One `capture stopped` line with `undrained`, the drop and failure counts
   (`Sink.Counts()`, plus `dropped_memory` from `Budget.Dropped()`), and
   `memory_peak_bytes` from `Budget.Peak()` (Q12).

This follows the spec's four steps: exchanges that end while the HTTP server shuts
down are still submitted, because the sink is only closed afterwards.

**Details fixed in T13.**
- A `Submit` refused because the sink is closed is counted in
  `Counts.DroppedQueueFull`, like a full queue: the access line says
  `dropped_queue_full` for both, and the count matches the lines.
- A worker that takes an exchange off the queue after the cancel releases it unstored,
  counts it as undrained and stops, so `Close`'s count is those plus what is left in
  the queue. The item in flight at the cancel is not undrained: its store call gets
  the cancelled context and fails (T14 logs and counts that as `stage: store`).
- `Close` waits for every worker to return after the cancel, so a store call that
  ignores its context holds shutdown up. A second `Close` returns 0.
- Workers run as `capture_worker` through `logging.Go`. `Counts` holds only
  `DroppedQueueFull` until T14 adds the store and parse failure counts.

**Details fixed in T14.**
- `capture.Config` gains `DecodeLimit` (`capture.max_body_bytes`): the worker is what
  builds `ParseInput`, and the `Exchange` doesn't carry the cap. `run` sets it (T16).
- `Counts` gains `StoreFailed` (every failed `SaveExchange` or `SaveParse`) and
  `ParseFailed` (every exchange whose parse status is `failed`).
- `capture_failed` lines carry `request_id` and `stage`. A store failure adds the
  store's error, which names the path and request ID and no request data. A parse
  failure adds a fixed `reason` (`parser reported failure`, `parser panicked`,
  `parser returned an unknown status`, `parser events are not canonical`), never the
  parser's error text or a panic's value: a panic is logged through
  `logging.LogPanic` (type and stack) as that one `capture_failed` line.
- A `Canonicalize` error (a parser contract violation) or a status outside the five
  makes the parse `failed`; with a `Canonicalize` error no events are stored. A
  parser that returns `failed` with events has them stored.
- A nil parser still gets `SaveParse` with `skipped` and no events, so every stored
  exchange ends with a parse status.

**Details fixed in T16.**
- The capture setup sits between the logger and `listen`. A directory that can't be
  resolved logs `invalid config` (`capture.dir`, source `default`, `invalid value`)
  and exits `2`; a failed `store.Open` logs `cannot open store` with `key`, `path` and
  a fixed `reason` (`permission denied`, `read-only file system`,
  `no space left on device`, `not a directory`, else `cannot open or migrate`) and
  exits `1`. Either is the run's only line, and nothing is bound.
- With `capture.enabled: false` the directory is never resolved, so a machine with no
  usable `HOME` still starts.
- A bind failure after the store opened closes the sink and store without a
  `capture stopped` line: nothing was queued, and 001's bind test expects one line.
- `capture stopped` is `info`, written after `srv.Shutdown` on both the clean and the
  timed-out path (and after `serve failed`), before `gateway stopped`. It carries
  `undrained`, `dropped_queue_full`, `dropped_memory`, `store_failed`,
  `parse_failed` and `memory_peak_bytes`. Undrained exchanges don't change the exit
  code. A `store.Close` error is a `store close failed` warning.
- `cmd/gateway/main_test.go`'s `TestMain` also checks `$HOME/.local/share/llm-gateway`,
  and hands `go build` in `binary_test.go` the real `HOME`, so the toolchain keeps
  its caches.
- AC16 through `run` uses a `capture.dir` under a regular file (`not a directory`),
  which fails for root too; the other reasons are checked on built errors (Q17).
- `TestCapture_PrincipalLocal` proves the exchange half of AC22 and pins the messages
  exchange at `skipped` with no events; T20 flips the pin to require events (Q16).

**Docker.** Compose mounts a named volume at `/var/lib/llm-gateway` and sets
`GATEWAY_CAPTURE_DIR` to it. The distroless image runs as `nonroot` and has no shell,
and a named volume mounted where the image has no directory is created owned by
root. So the Dockerfile copies an empty directory there with `--chown=nonroot`, and
Docker then initialises the volume with that owner. The spec names only compose; see
Deviations.

### Build order (input to tasks.md)

1. **Spikes, first (done 2026-09-28).** Q9 and Q10 ran in a throwaway module outside
   the repo on `modernc.org/sqlite` v1.59.0 (SQLite 3.53.4). Both decision rules
   fired: the spec's narrower dump rule and the store's inode check, as applied
   above.
2. **ADRs, before any dependency.** ADR 0004 (store, pipeline confirmation,
   dependencies, with pinned versions and licences), written after the spikes so it
   records a verified driver, and ADR 0005 (secrets in content). No `go get` here:
   each dependency is added, at the version 0004 pins, by the task that first imports
   it (`klauspost/compress` and `andybalholm/brotli` with the decoder,
   `modernc.org/sqlite` with the store).
3. **Config:** keys, `DefaultCaptureDir`, the example file (AC1–AC4).
4. **`internal/contentcoding`**, with the decode cap (Q7).
5. **Core:** canonical event types, `Canonicalize`, the `Parser` seam and optional
   adapter interfaces, `PrincipalResolver`, `Budget`, the tees, redaction,
   `finishCapture`, `WithCapture`, and the access-line field; the purity denylist
   (AC5, AC8, AC24, AC29, AC58).
6. **`internal/store`** (AC13–AC19, AC21).
7. **`internal/capture`**: the sink, workers and pipeline (AC6, AC20, AC23, AC25,
   AC59).
8. **`run` wiring and shutdown;** the test data-dir guard (AC7, AC9–AC12, AC16, AC22,
   AC26–AC28, AC60).
9. **Anthropic parser and fixtures** (AC30–AC53).
10. **`gateway dump`** (AC54–AC57).
11. **Docs:** `docs/capture.md`, the PLAN.md §3, §8 and §9 edits, compose and
    Dockerfile; then the manual checks (AC61–AC63).

## Files and packages touched

| Path | Why |
|---|---|
| `internal/core/capture.go` | `Capture`, `Budget`, `Exchange`, `Body`, `CaptureSink`, `WithCapture`, `finishCapture` |
| `internal/core/tee.go` | `teeReader` (request, mutex, seal), `teeWriter` (response, `Unwrap`) |
| `internal/core/redact.go` | Header and raw-query redaction |
| `internal/core/principal.go` | `PrincipalResolver`, `LocalPrincipal` |
| `internal/core/parser.go` | `Parser`, `ParseInput`, `ParseResult`, `ParseStatus`, `SecretDeclarer`, `ParsingAdapter` |
| `internal/core/event.go` | Canonical event types, `SchemaVersion` |
| `internal/core/canonical.go` | `Canonicalize`, `StoredEvent`, `Content` |
| `internal/core/meta.go`, `proxy.go`, `registry.go` | `Meta` fields, the tee hooks, `NewRegistry` options |
| `internal/core/*_test.go` | Tee, budget, redaction, canonical encoding, the AC5 wrapper; `purity_test.go`'s denylist and directory list |
| `internal/capture/sink.go`, `pipeline.go`, `store.go` | Queue, workers, pipeline, `Store` interface |
| `internal/store/*.go` | `Open`, `OpenReader`, migrations, content, blobs, reads for dump |
| `internal/contentcoding/decode.go` | `Decode`, `Status` |
| `internal/protocols/anthropic/adapter.go` | `SecretHeaders`, `SecretQueryParams`, `Parser()` |
| `internal/protocols/anthropic/parser.go`, `request.go`, `response.go`, `stream.go` | The parser |
| `internal/protocols/anthropic/testdata/` | New fixtures and README entries |
| `internal/config/capture.go`, `config.go`, `load.go` | `Capture`, keys, `invalid value`, `DefaultCaptureDir` |
| `internal/server/middleware.go` | `Meta.RequestID`; the `capture` field |
| `cmd/gateway/run.go`, `dump.go` | Wiring, shutdown order, `dump`; `deps.openStore` for tests |
| `cmd/gateway/main_test.go` | `TestMain`: the default data-dir guard |
| `cmd/gateway/run_test.go`, `binary_test.go` | Helpers set `GATEWAY_CAPTURE_DIR` to a temp dir |
| `config.example.yaml`, `docker-compose.yml`, `Dockerfile` | Capture keys; volume; volume ownership |
| `docs/capture.md` | AC63 |
| `docs/decisions/0004-capture-store.md`, `0005-secrets-in-captured-content.md` | ADRs |
| `PLAN.md` | §3 known limit (Q4), §8 rows, §9 OQ-1 and OQ-5 resolved |
| `go.mod`, `go.sum` | Three dependencies, after ADR 0004 |

## Interfaces introduced or changed

```go
// internal/core — capture
type Capture struct {
	Sink         CaptureSink
	Budget       *Budget
	MaxBodyBytes int64
	Principal    PrincipalResolver
}
type RegistryOption func(*Registry)
func WithCapture(c Capture) RegistryOption
func NewRegistry(log *zap.Logger, opts ...RegistryOption) *Registry // 001 callers unchanged

// CaptureSink takes finished, redacted exchanges. Submit never blocks. It owns ex
// either way: on false (queue full or closed) it has already released ex's memory.
type CaptureSink interface {
	Submit(ex *Exchange) bool
}

type Budget struct{ /* limit; inUse, peak and dropped atomic.Int64 */ }
func NewBudget(limit int64) *Budget
func (b *Budget) TryReserve(n int64) bool
func (b *Budget) Release(n int64)
func (b *Budget) InUse() int64
func (b *Budget) Peak() int64 // highest InUse seen; raised by CAS max
func (b *Budget) Dropped() int64 // exchanges dropped for memory (dropped_memory)

type Body struct {
	Chunks    [][]byte // as captured, still content-encoded
	Size      int64
	Truncated bool
}
func (b Body) Bytes() []byte // joins the chunks; called in the worker

type Exchange struct {
	RequestID, PrincipalID, Protocol, Client string
	Auth                                     AuthKind
	Method, Path, Query                      string      // Query redacted
	RequestHeader, ResponseHeader            http.Header // redacted clones
	Status                                   int
	Start, End                               time.Time
	TTFB                                     time.Duration
	HasTTFB                                  bool
	Stream, ClientDisconnected, UpstreamAborted bool
	RequestIncomplete                        bool // request copy sealed before EOF
	GatewayError                             string
	Request, Response                        Body
	Parser                                   Parser   // nil: parse: skipped
	Excluded                                 []string // the parser's HashExcludedFields
	// unexported: the budget and the bytes reserved
}
func (ex *Exchange) Release() // idempotent

// Meta (changed): RequestID and PrincipalID (set by accessLog and ServeHTTP), Status,
// and Capture ("queued", "dropped_queue_full", "dropped_memory", "off"; "" off-proxy).

// internal/core — identity
const PrincipalLocal = "local"
type PrincipalResolver interface {
	Resolve(r *http.Request) string // the principal ID
}
type LocalPrincipal struct{} // always PrincipalLocal

// internal/core — adapter extensions, both optional
type SecretDeclarer interface {
	SecretHeaders() []string     // redacted on top of core's four
	SecretQueryParams() []string // query parameters whose values are secrets
}
type ParsingAdapter interface {
	Parser() Parser
}

// internal/core — parser seam
type ParseStatus string
const (
	ParseOK                  ParseStatus = "ok"
	ParsePartial             ParseStatus = "partial"
	ParseSkipped             ParseStatus = "skipped"
	ParseUnsupportedEncoding ParseStatus = "unsupported_encoding"
	ParseFailed              ParseStatus = "failed"
)
type ParseInput struct {
	Method, Path, Query                   string
	Status                                int
	RequestHeader, ResponseHeader         http.Header
	RequestBody, ResponseBody             []byte // still content-encoded
	RequestTruncated, ResponseTruncated   bool
	Stream                                bool
	DecodeLimit                           int64 // capture.max_body_bytes, for contentcoding.Decode
}
type ParseResult struct {
	Status ParseStatus
	Events []Event
}
type Parser interface {
	Parse(in ParseInput) ParseResult
	// HashExcludedFields are wire-only keys left out of content hashes, removed from
	// the top level of a block, a system entry or a tool definition only.
	HashExcludedFields() []string
}

// internal/core — canonical events
const SchemaVersion = 1
type EventKind string   // request, message, tool_call, tool_result, usage, error
type Source string      // request_history, response
type ExecutedBy string  // client, provider
type BlockType string   // text, reasoning, media, tool_call, tool_result, unknown
type Block struct {
	Type       BlockType
	Content    json.RawMessage // the block object; nil for tool_call / tool_result
	Redacted   bool
	ToolCallID string // tool_call and tool_result blocks
}
type RequestEvent struct {
	Model                        string
	Stream                       bool
	MaxTokens                    *int64
	System, Tools                json.RawMessage // hashed to system_hash, tools_hash
	HasSystem, HasTools          bool
	CacheHints, ReasoningRequested bool
	ToolNames                    []string
}
type MessageEvent struct {
	Index      int // -1 for the response message
	Role       string
	Source     Source
	StopReason string
	Blocks     []Block
}
type ToolCallEvent struct {
	ID, Name   string
	Input      json.RawMessage
	ExecutedBy ExecutedBy
	Source     Source
	Raw        json.RawMessage
}
type ToolResultEvent struct {
	ToolCallID string
	IsError    bool
	Content    json.RawMessage
	ExecutedBy ExecutedBy
	Source     Source
	Raw        json.RawMessage
}
type UsageEvent struct {
	InputTokens, OutputTokens, CacheWriteTokens, CacheReadTokens *int64
	Detail json.RawMessage // any finer breakdown, as sent
}
type ErrorEvent struct {
	Status        int
	Type, Message string
}
type Event struct {
	Kind    EventKind
	Partial bool
	// exactly one of these is set, matching Kind
	Request    *RequestEvent
	Message    *MessageEvent
	ToolCall   *ToolCallEvent
	ToolResult *ToolResultEvent
	Usage      *UsageEvent
	Error      *ErrorEvent
}
type Content struct {
	Hash  string // sha256, lowercase hex
	Bytes []byte // canonical form
}
type StoredEvent struct {
	Seq                   int
	Kind                  EventKind
	Source                Source
	Partial               bool
	ContentHash, ToolCallID string
	Payload               []byte // canonical JSON with content by hash
}
func Canonicalize(events []Event, excluded []string) ([]StoredEvent, []Content, error)

// internal/capture
type Store interface {
	SaveExchange(ctx context.Context, ex *core.Exchange) error
	SaveParse(ctx context.Context, requestID string, principalID string,
		status core.ParseStatus, events []core.StoredEvent, contents []core.Content) error
	Close() error
}
type Config struct {
	QueueSize, Workers int
	DecodeLimit        int64 // capture.max_body_bytes, handed to each parser (T14)
}
type Sink struct{ /* chan *core.Exchange, counters */ }
func NewSink(cfg Config, st Store, log *zap.Logger) *Sink // starts workers via logging.Go
func (s *Sink) Submit(ex *core.Exchange) bool             // satisfies core.CaptureSink
func (s *Sink) Close(ctx context.Context) (undrained int) // later Submits return false
func (s *Sink) Counts() Counts

// internal/store
func Open(dir string, log *zap.Logger) (*Store, error) // satisfies capture.Store
func OpenReader(dir string) (*Reader, error) // read-only; never creates (Q9)
func (r *Reader) Exchange(id string) (ExchangeRow, error)
func (r *Reader) Last() (ExchangeRow, error)
func (r *Reader) Content(hash string) ([]byte, error)
func (r *Reader) Events(id string) ([]EventRow, error)
var ErrNotFound = errors.New("not found")

// internal/contentcoding
type Status int
const (
	Complete Status = iota
	CutShort
	Unsupported
)
func Decode(encoding string, body []byte, limit int64) ([]byte, Status) // limit: capture.max_body_bytes (Q7)

// internal/config
type Capture struct {
	Enabled      bool
	Dir          string // "" = DefaultCaptureDir
	QueueSize    int
	Workers      int
	MaxBodyBytes int64
	MemoryLimit  int64
}
func DefaultCaptureDir(lookupEnv func(string) (string, bool)) (string, bool)
```

## New dependencies

Each one lands only after ADR 0004 is written, which pins its version and records
its licence. It is added to `go.mod` by the task that first imports it, at that
version.

| Need | Choice | Why | Rejected |
|---|---|---|---|
| SQLite, no cgo | **`modernc.org/sqlite`** | SQLite's C source transpiled to Go. It uses SQLite's own unix VFS, so file-level behaviour (WAL, locking) is upstream SQLite's; the Q9 and Q10 spikes ran against v1.59.0 (SQLite 3.53.4). A write after unlink is not detected (Q10), so the store checks the inode itself. It is a plain `database/sql` driver (`"sqlite"`), widely used, BSD-3-Clause. Its docs put it about 1.3× slower than C on indexed work, far below what one local user's capture needs. | **`ncruces/go-sqlite3`:** also cgo-free (a Wasm build of SQLite translated to Go with wasm2go), MIT, and competitive in speed. But it replaces SQLite's OS layer with its own Go VFS, so the file behaviour the store and dump rely on would be a second implementation's, and each connection runs in its own Wasm sandbox with higher memory. **`mattn/go-sqlite3`:** needs cgo, which the spec forbids. |
| zstd (blobs; the `zstd` coding) | **`github.com/klauspost/compress/zstd`** | Pure Go, the de-facto Go zstd. `EncodeAll` and `DecodeAll` suit whole blobs; one decoder serves both blobs and the `zstd` content coding. | **`DataDog/zstd`, `valyala/gozstd`:** cgo. |
| brotli (the `br` coding) | **`github.com/andybalholm/brotli`** | Pure Go, decoder only is used. Named in the spec. | **`google/brotli` Go bindings:** cgo. |

`gzip`, `zlib` and raw `flate` are the standard library's.

## ADRs and PLAN.md

- **ADR 0004** (`docs/decisions/0004-capture-store.md`), build-order step 1:
  - SQLite plus zstd content-addressed blobs (OQ-1);
  - the three dependencies above;
  - the quadratic raw-storage trade-off;
  - the confirmation of PLAN.md §8's "Proposed" capture pipeline, which the spec
    makes.
- **ADR 0005** (`docs/decisions/0005-secrets-in-captured-content.md`), the same step:
  secrets in content are stored as-is, with `0700`/`0600` permissions, and masking is
  a later opt-in (OQ-5).
- Both are written `proposed` and become `approved` when this plan is, as ADR 0003
  did.
- **PLAN.md, in build-order step 11:**
  - §8 "Capture pipeline" → Decided (ADR 0004); §8 "Capture store" → the choice,
    Decided (ADR 0004);
  - §9 OQ-1 and OQ-5 marked resolved with their ADR links;
  - §3's Claude Code known-limits entry gains the `HEAD /api/hello` line (Q4).

## Testing strategy

Everything in `make verify`, under `-race`, with no live API and no network beyond
loopback. Stores live in `t.TempDir()`.

**No test touches the default data dir (spec, Do).**
- `run`'s test helper (`startGateway`) puts `XDG_DATA_HOME=<t.TempDir()>` into the
  env map it hands `run`, unless the test sets `GATEWAY_CAPTURE_DIR`, `XDG_DATA_HOME`
  or `HOME` itself, so the default `capture.dir` resolves into a temp dir.
  `binary_test.go` puts `GATEWAY_CAPTURE_DIR=<t.TempDir()>` in the child's
  environment. (T16: not `GATEWAY_CAPTURE_DIR` in the helper, because every `GATEWAY_*`
  variable lands in the startup line's `env_overrides`, which 001's tests assert.)
- `cmd/gateway/main_test.go` gets a `TestMain` that points `HOME` and `XDG_DATA_HOME`
  at a temp directory before `m.Run()`. Afterwards it fails the package if
  `<that dir>/llm-gateway` exists: a forgotten override is caught rather than writing
  into the real `~/.local/share`.
- `internal/config` tests resolve the default only through an injected `lookupEnv`.
- No other package resolves the default at all.

**How 001's tests run with capture on (AC5).**
- **`cmd/gateway`.** `capture.enabled` defaults to `true`, and the helper now gives
  each run a temp directory. So every 001 end-to-end test runs with capture on, a
  real store and the real parser, with its test body unchanged.
- **`internal/core` and `internal/protocols/anthropic`.** Their helpers build the
  proxy through one function. `TestCapture_ForwardingUnchanged` lists 001's
  forwarding, streaming, compression and error test functions (AC8, AC12, AC14–AC24,
  AC26–AC31, AC47, AC48 in 001's numbering) and runs each as a subtest. The helper
  turns capture on, with a recording sink and a real `Budget`, when `t.Name()` starts
  with `TestCapture_ForwardingUnchanged/`. The name test is crude, but it reaches
  nested subtests, which a registry keyed by `*testing.T` wouldn't, and the 001
  functions themselves stay unchanged.
- **Capture is proven on, not assumed.** After each 001 function returns, its
  subtest asserts that the recording sink received at least one exchange. A rename
  of the wrapper, or a 001 test that bypasses the helper, then fails instead of
  passing with capture silently off. Only 001 tests that send a request through
  the proxy are on the list.

**Shared helpers.**
- A recording sink (`core` tests) keeps submitted exchanges for inspection.
- A blocking store and a failing store (`capture` tests) let a test hold or break the
  pipeline at will.
- 001's stream helper and leak check are reused. Every capture test that ends with a
  cancel, abort or shutdown runs the leak check, now also looking for stacks through
  `internal/capture`.

### AC evidence

| AC | Test | File |
|---|---|---|
| 1 | `TestLoad_CaptureDefaults`, with `DefaultCaptureDir` subtests incl. `relative_xdg_ignored` | `internal/config/load_capture_test.go` |
| 2 | `TestLoad_CaptureEnvOverridesFile` | same |
| 3 | `TestLoad_InvalidCaptureValues` | same |
| 4 | `TestLoad_ExampleFileIsDefaults` (existing, unchanged) | `internal/config/load_test.go` |
| 5 | `TestCapture_ForwardingUnchanged` (wrapper over 001's tests); `cmd/gateway`'s 001 tests with capture on by default | `internal/core/capture_fidelity_test.go`, `internal/protocols/anthropic/capture_fidelity_test.go` |
| 6 | `TestCapture_StreamNotDelayed` (blocking store behind the real sink) | `internal/capture/capture_test.go` |
| 7 | `TestCapture_DisabledIsPassthrough` | `cmd/gateway/run_capture_test.go` |
| 8 | `TestCapture_OutgoingRequestHasNoGetBody` | `internal/core/tee_test.go` |
| 9 | `TestCapture_ExchangeStored` | `cmd/gateway/run_capture_test.go` |
| 10 | `TestCapture_EncodedBodyStoredAsSent` | same |
| 11 | `TestCapture_BodyOverCapTruncated`, incl. `non_streamed_json_parses_partial` | same |
| 12 | `TestCapture_AbortedExchangesRecorded`, incl. `request_read_after_seal` (upstream answers before reading the body; `request_incomplete` set) | same |
| 13 | `TestStore_BlobDedup` | `internal/store/store_test.go` |
| 14 | `TestStore_BlobBeforeRow` (a fault hook between blob and row, via `export_test.go`) | same |
| 15 | `TestStore_Permissions` (under a `0022` and a `0000` umask) | same |
| 16 | `TestStore_OpenFailsFast` (through `run`: exit code and line) | `cmd/gateway/run_capture_test.go` |
| 17 | `TestStore_MigratesFromEmpty` | `internal/store/store_test.go` |
| 18 | `TestStore_SmallContentInline` | same |
| 19 | `TestStore_LargeContentBlob` | same |
| 20 | `TestStore_ConcurrentWorkersNoBusyErrors` (real sink and store) | `internal/capture/sink_test.go` |
| 21 | `TestStore_OpenReaderDoesNotFailWrites` | `internal/store/store_test.go` |
| 22 | `TestCapture_PrincipalLocal` | `cmd/gateway/run_capture_test.go` |
| 23 | `TestCapture_QueueFullDrops` | `internal/capture/capture_test.go` |
| 24 | `TestCapture_MemoryLimitDrops` (`Budget.InUse` before and after; `Budget.Dropped` counts the drop) | `internal/core/capture_test.go` |
| 25 | `TestCapture_StoreDeadClientUnaffected` (`db_deleted` caught by the store's inode check, Q10). `db_deleted` has a subtest, `deleted_logged_once`: across several exchanges after the delete, exactly one `store file deleted or replaced` line, every exchange gets `capture_failed` with `stage: store`, and no new `gateway.db` appears | `internal/capture/capture_test.go` |
| 26 | `TestCapture_ShutdownDrainsQueue` (`deps.openStore` injects a slow store) | `cmd/gateway/run_capture_test.go` |
| 27 | `TestRedact_AuthHeaders` (scans the database and every decompressed blob) | same |
| 28 | `TestRedact_ForwardedTrafficUntouched` | same |
| 29 | `TestRedact_AdapterQueryParams` (test adapter): the submitted exchange (T8), and subtest `in_store` through the real sink and store (T14) | `internal/core/capture_test.go`, `internal/core/capture_pipeline_test.go` |
| 30 | `TestParse_GoldenStream` | `internal/protocols/anthropic/parser_test.go` |
| 31 | `TestParse_GoldenStreamGzip` | same |
| 32 | `TestParse_ToolUseTurn` (fixture: `tool_turn`, recorded) | same |
| 33 | `TestParse_MessageKeepsToolBlockOrder` (fixture: `tool_order`, synthetic from recorded blocks) | same |
| 34 | `TestParse_ServerToolUse` (fixture: `server_tool`, assembled from recorded blocks) | same |
| 35 | `TestParse_NonStreaming` (fixture: `non_streaming`, synthetic) | same |
| 36 | `TestParse_UpstreamError` (fixtures: `error/*`, synthetic) | same |
| 37 | `TestParse_UnknownBlockAndEventKept` | `internal/protocols/anthropic/stream_test.go` |
| 38 | `TestParse_ToolInputSplitAcrossDeltas` | same |
| 39 | `TestParse_ThinkingSignatureKept` | same |
| 40 | `TestParse_RedactedThinkingKept` | `internal/protocols/anthropic/parser_test.go` |
| 41 | `TestParse_UsageLastValueWins` | `internal/protocols/anthropic/stream_test.go` |
| 42 | `TestParse_TruncatedStreamPartial` | same |
| 43 | `TestParse_TruncatedToolInputPartial` | same |
| 44 | `TestParse_ContentEncodings` | `internal/protocols/anthropic/parser_test.go` |
| 45 | `TestParse_SystemChangeChangesHash` | same |
| 46 | `TestParse_ReasoningRequestedFlag` | same |
| 47 | `TestParse_OtherPathsSkipped` | same |
| 48 | `TestParse_FailureKeepsRaw` (through the pipeline, real store) | `internal/protocols/anthropic/pipeline_test.go` |
| 49 | `TestStore_BlockContentDedup` | same |
| 50 | `TestStore_ResponseMessageDedupsWithNextRequest` (fixture: `tool_turn`, recorded) | same |
| 51 | `TestStore_SystemAndToolsStoredOnce` | same |
| 52 | `TestStore_ExclusionNotAppliedInsideToolInput` | same |
| 53 | `TestFixtures_NoIdentifiers` (existing, unchanged) | `test/conventions/fixtures_test.go` |
| 54 | `TestDump_PrintsExchange` | `cmd/gateway/dump_test.go` |
| 55 | `TestDump_Last` | same |
| 56 | `TestDump_UnknownIDFails` | same |
| 57 | `TestDump_NeverWritesStore` (both store states; `-wal`/`-shm` allowed, Q9) | same |
| 58 | `TestCore_NoProviderOrClientIdentifiers`: the full denylist, wire names included, over `internal/core`; the provider and client names only over `internal/capture`, `internal/store` and `internal/contentcoding` | `internal/core/purity_test.go` |
| 59 | `TestCore_TestParserNeedsNoCoreChange` (test adapter and parser through the real sink and store) | `internal/core/capture_pipeline_test.go` |
| 60 | `TestCapture_SecretsNotLogged` (stored, dropped, failed) | `cmd/gateway/run_capture_test.go` |
| 61 | Manual: a Claude Code session with a tool call and ≥3 turns; the hash comparison recorded as Q3's answer. Evidence in the PR | PR |
| 62 | Manual: delete, then `chmod 000`, `capture.dir` mid-session. Expected: after the delete, one `store file deleted or replaced` line, then `capture_failed` per exchange. After `chmod 000`, open handles still work, so an exchange whose content all fits inline may still be stored; one that needs a blob fails with `stage: store`. Real Claude Code requests are over 4 KiB and need blobs, so `capture_failed` lines appear. Claude Code keeps working in both cases | PR |
| 63 | Manual: `docs/capture.md` read against the AC | PR |

**Unmapped ACs:** none. Every fixture source is settled (Q8), and the spikes behind
AC25 and AC57 have run (Q9, Q10).

**Tests no AC names.**
- `TestDecode_*` in `internal/contentcoding`: each coding, stacked codings,
  `CutShort`, `Unsupported`.
- `TestCanonicalize_*` in `internal/core`: key order, `UseNumber`, HTML characters
  kept, exclusions only at top level, the message hash.
- `TestRedact_QueryKeepsBytes`: unredacted parameters are byte-identical.
- `TestTeeWriter_Unwrap`: `http.NewResponseController(tw).Flush()` reaches the inner
  flusher.
- `TestDecode_CapsOutput` (a subtest of the `TestDecode` table): output past
  `limit` is dropped and the status is `CutShort` (Q7).
- `TestSink_SubmitAfterCloseReturnsFalse`, under `-race`: `Submit` racing and
  following `Close` returns `false`, releases the exchange and never panics.

## Fixtures

New files in `internal/protocols/anthropic/testdata/`. Each has a README entry for
date, model, method and what was scrubbed, as in 001, and says `recorded` or
`synthetic` (Q8):

| File | For | Source |
|---|---|---|
| `tool_turn/response.sse`, `tool_turn/next_request.json` | AC32, AC50 | `recorded`: a Claude Code turn that calls a tool, and the next request |
| `tool_order/response.sse` | AC33 | `synthetic`: hand-built from recorded blocks (text, `tool_use`, text), since it tests the parser's ordering, not upstream behaviour |
| `server_tool/response.sse` | AC34 | `synthetic (assembled from recorded blocks)`: the recorded WebSearch request's `server_tool_use` and `web_search_tool_result`, plus a client `tool_use` spliced in from `tool_turn` |
| `non_streaming/request.json`, `response.json` | AC35 | `synthetic`, from Anthropic's documented shapes |
| `error/response_429.json`, `error/stream_error.sse` | AC36 | `synthetic`, from Anthropic's documented shapes |

The OAuth token is never extracted to call the API directly. AC33 and AC34 are
built from recorded blocks by owner decision (Q8), because a real message or turn
rarely has that exact shape. Each block keeps its recorded bytes, only their
arrangement is synthetic, and the README says so. No other fixture is edited beyond
scrubbing.

- **How they're recorded.** 001's throwaway recording proxy, extended to save the
  request body as well, with `Accept-Encoding: identity` forced as in 001 Q10.
  AC31 and AC44 gzip and encode in the test.
- **Scrubbing.** As in 001, plus request bodies: system prompts and tool
  descriptions are trimmed to what the tests need; file paths, user names and any
  secrets in tool results are removed.
- AC53 (`TestFixtures_NoIdentifiers`) checks every new file unchanged.
- Small synthetic bodies (unknown block types, split deltas, truncation) are built in
  the test files, not stored as fixtures.

## Risks and unknowns

- **Buffering on the response path.** `teeWriter` sits between `ReverseProxy` and
  the socket. Missing `Unwrap` would turn streams into one lump with every functional
  test green. Caught by 001's AC23 run under AC5, and by AC6 with a store that never
  returns.
- **Request tee lifetime.** The transport can still be reading the request body when
  the handler returns, for example after an early upstream error. The seal plus
  mutex keeps the copy consistent, and `-race` checks it. A read after the seal is
  forwarded but not captured, so the exchange is flagged `request_incomplete`
  whenever the seal came before the body's EOF (AC12 `request_read_after_seal`).
  `truncated` still means the cap only.
- **Unbudgeted worker memory.** Each worker can hold both joined bodies and both
  decoded copies, each at most `max_body_bytes` (Q7 caps the decoded ones). The worst
  case outside `capture.memory_limit` is `workers × 4 × max_body_bytes`: about
  256 MiB at the defaults (2 workers, 32 MiB). The parser's own structures come on
  top, in proportion to the decoded bodies.
- **SQLite file behaviour.** The spikes settled both: a read-only open writes `-wal`
  and `-shm` (Q9, allowed by the spec's dump rule), and a write after unlink succeeds
  (Q10, caught by the inode check). A later driver upgrade could change either; AC25
  and AC57 guard them.
- **Canonical encoding and dedup.** AC50 holds on fixtures; real Claude Code resends
  are Q3, answered by AC61. `cache_control` nested in a `tool_result`'s content
  blocks is hashed, per the spec's exclusion scope, so such a resent result won't
  dedup (Q3, 2026-09-28 line).
- **Purity outside core.** AC58's test also walks `internal/capture`,
  `internal/store` and `internal/contentcoding`, for provider and client names only;
  wire names stay core-only. "Cursor" is a common database word, so store code and
  comments must avoid it. The test flags it rather than the list growing an
  exception.
- **`chmod 000` on `capture.dir` mid-run is only partly visible.** The open database,
  `-wal` and `-shm` handles keep working, so an exchange whose content all fits
  inline (4 KiB or less per item) can still be stored. Only exchanges that need a new
  blob fail, with `stage: store`. Real Claude Code requests always need blobs, so the
  failure shows up at once in practice (AC62). A small-request client could see
  capture carry on for a while. The spec asks only that requests are unaffected and
  failures logged, which holds either way.
- **Docker volume ownership.** Without the Dockerfile change, compose fails fast at
  startup because `nonroot` can't write the volume. This is caught the first time
  compose runs, not by `make verify`.
- **Shutdown time.** The drain gets only what `shutdown_timeout` has left after the
  HTTP server. A long stream can leave none, and then the queue is counted, not
  stored, as the spec accepts.

## Deviations from the spec

Both deviations below were accepted by the owner on 2026-09-28.

- **`Dockerfile`** gains an empty, `nonroot`-owned `/var/lib/llm-gateway`. The spec
  names only `docker-compose.yml`, but its volume can't be written without this.
- **A default directory that can't be resolved** (no `HOME`, no absolute
  `XDG_DATA_HOME`) fails with `invalid value` for `capture.dir` from source `default`.
  The spec doesn't cover this case.

Nothing else. `request_incomplete` isn't a deviation, and neither is the narrower
dump rule: each spec edit went in with the plan change it matches.
