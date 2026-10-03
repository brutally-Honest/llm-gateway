# Capture

The gateway copies every exchange it proxies on the side: the request and the
response, streams included. It redacts auth from the copy, stores it, and parses it
into canonical events (`request`, `message`, `tool_call`, `tool_result`, `usage`,
`error`). The forwarded traffic is never changed, and capture never fails or delays a
request.

## 1. Where the store lives

The store is one directory, `capture.dir` (env `GATEWAY_CAPTURE_DIR`). It must be an
absolute path: a relative one fails at startup with `invalid value`.

When `capture.dir` is not set, the gateway uses, in this order:

1. `$XDG_DATA_HOME/llm-gateway`, if `XDG_DATA_HOME` is set to an absolute path. A
   relative `XDG_DATA_HOME` is ignored, as the XDG Base Directory spec requires.
2. `$HOME/.local/share/llm-gateway`.

If neither can be resolved, the gateway exits with `invalid value` for `capture.dir`.

The directory is created `0700`, and every file in it is `0600`. The other capture
settings (on or off, queue size, workers, body cap, memory limit) are listed with
their defaults and env names in [`config.example.yaml`](../config.example.yaml).
With `capture.enabled: false` nothing is copied or stored, and the gateway only
proxies.

## 2. What is in it

```
<capture.dir>/
  gateway.db            SQLite database (WAL mode), with gateway.db-wal and -shm
  blobs/ab/cdef…        content over 4 KiB, zstd-compressed, named by its sha256
```

`gateway.db` holds three tables:

- **`exchanges`:** one row per proxied request, keyed by `request_id`, the gateway's
  request ID. It is the `request_id` field of the `request` access line and the
  `X-Request-Id` response header. The row holds the principal (`local`), protocol,
  client and auth kind; method, path (prefix stripped) and query; request and response
  headers as JSON; status; `started_at` and `ended_at` (Unix nanoseconds); `ttfb_ns`,
  the time to first byte as a duration in nanoseconds since `started_at` (not a
  timestamp), NULL when no response headers arrived; the flags `stream`, `truncated`,
  `request_incomplete`, `client_disconnected`, `upstream_aborted` and `gateway_error`;
  the two bodies by content hash; and the parse status `parse`: `ok`, `partial`,
  `skipped`, `unsupported_encoding` or `failed`. `parse` is NULL until the exchange's
  events are stored. It is written with them in one transaction, so it stays NULL, with
  no events, if that write failed (a `capture_failed` line with `stage: store`, see §5);
  the row and raw bodies are still there.
- **`events`:** the canonical events parsed from each exchange, in `seq` order, with
  their kind, `source` (`request_history` or `response`), `tool_call_id`, content hash
  and a JSON `payload`. Every message in the request history is an event, so each
  exchange shows its full conversation.
- **`content`:** every stored item, keyed by its sha256 hash. Content of 4 KiB or less
  is inline in the `data` column (`location = 'inline'`); larger content is a blob file
  (`location = 'blob'`).

Two kinds of content share that table:

- **Raw bodies,** exactly as they went over the wire. A gzip body is stored, and
  hashed, still gzip. A body larger than `capture.max_body_bytes` is stored up to that
  cap and the exchange is flagged `truncated`; the client still gets the whole body.
- **Parsed content:** message blocks, tool inputs, tool results, and the system and
  tools arrays. It is hashed in a canonical JSON form, so a message resent on every
  turn, or an unchanged system prompt, is stored once.

Raw bodies are stored whole, so raw request storage grows with each turn of a session.
Nothing is pruned yet.

## 3. What is redacted, and what isn't

Redacted in the stored copy (the value becomes `[REDACTED]`, the header name is kept):

- the headers `Authorization`, `Proxy-Authorization`, `Cookie` and `Set-Cookie`, in
  both directions;
- the headers each protocol adapter declares: Anthropic declares `x-api-key`;
- the query parameters each protocol adapter declares as secret. Anthropic declares
  none.

Not redacted: request and response **bodies** are stored as sent. A secret inside a
prompt, a tool input or a tool result (an agent that read a `.env` file, say) is in the
store in plain text, protected only by the `0700`/`0600` permissions. Anyone who can
read `capture.dir`, a backup of it or a `gateway dump` output can read it. The
decision and its reasons: [ADR 0005](decisions/0005-secrets-in-captured-content.md).

No log line carries a header value, a body or a query string.

## 4. How to inspect it

### `gateway dump` first

`gateway dump` prints one exchange. It finds the store through the same config file
and env vars as the gateway, and it only reads: it never creates the store and never
writes `gateway.db` or a blob. It works while the gateway is running.

```sh
make build
bin/gateway dump -last                 # the most recent exchange
bin/gateway dump <request-id>          # one exchange, by its request ID
bin/gateway dump -raw <request-id>     # bodies as stored, still encoded
bin/gateway dump -config ./config.yaml -last
```

The output is, in order:

1. the exchange row, as one JSON line;
2. `== request body` and `== response body`. A body sent with `Content-Encoding`
   (`gzip`, `deflate`, `br`, `zstd`) is decoded unless `-raw` is given, and the header
   line says what was decoded. An encoding the gateway can't decode is printed raw,
   with a notice on the header line;
3. `== events`, then one JSON line per canonical event, content hashes included.

A missing store or an unknown request ID exits `1` with the reason.

### `sqlite3` second

Open the database read-only. The gateway can keep running. Use your `capture.dir`:

```sh
sqlite3 -readonly /path/to/capture.dir/gateway.db
```

If `capture.dir` is set in neither the config file nor the environment, this resolves
the default from §1 (it assumes `XDG_DATA_HOME` is unset or absolute):

```sh
sqlite3 -readonly "${GATEWAY_CAPTURE_DIR:-${XDG_DATA_HOME:-$HOME/.local/share}/llm-gateway}/gateway.db"
```

The last 20 exchanges, newest first:

```sql
select request_id,
       datetime(started_at / 1000000000, 'unixepoch') as started,
       method, path, status, client, stream, truncated, parse
from exchanges
order by started_at desc
limit 20;
```

Tool calls and their results, in order, with who ran each tool:

```sql
select request_id, seq, kind, source, tool_call_id,
       json_extract(payload, '$.executed_by') as executed_by
from events
where kind in ('tool_call', 'tool_result')
order by request_id, seq;
```

A hash in `content_hash`, `request_body` or `response_body` points at a `content`
row. Inline content is in its `data` column; a blob is at
`blobs/<first two hex digits>/<the rest>`, and `zstd -dc <file>` prints it.

## 5. When the store fails

Capture never fails a request. What happens depends on when the store fails:

- **At startup.** If the store can't be created, opened or migrated, the gateway
  exits `1` before it listens, with a `cannot open store` line that names the path and
  a reason (for example `permission denied` or `not a directory`). It never starts with
  capture silently off; set `capture.enabled: false` to run without it.
- **At runtime.** A failed write (a locked or read-only database, a full disk) is
  logged per exchange as `capture_failed` with the `request_id` and `stage: store`.
  A body that can't be parsed is logged the same way with `stage: parse`, and its raw
  bodies stay stored. The request is unaffected either way.
- **Deleted or replaced database.** If `gateway.db` is deleted or replaced while the
  gateway runs, it logs `store file deleted or replaced; restart the gateway to resume
  capture` once, with the path. Capture stays off for the rest of the run: every later
  exchange gets a `capture_failed` line, and the gateway never recreates or reopens
  the database. **Restart the gateway to resume capture.**
- **Unreadable directory.** If the gateway can no longer check the database file (for
  example after `chmod 000` on `capture.dir`), it logs one `store file unreadable`
  warning and keeps trying each write; writes that need a new blob file fail with
  `capture_failed`.

The `request` access line says what happened to each exchange's copy in its `capture`
field: `queued`, `dropped_queue_full` (the queue was full), `dropped_memory` (the
in-flight copies hit `capture.memory_limit`) or `off`. On shutdown the queue is drained
within what is left of `shutdown_timeout`, and one `capture stopped` line reports what
was left undrained, every drop and failure count, and `memory_peak_bytes`.
