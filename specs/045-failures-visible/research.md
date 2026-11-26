---
feature: 045-failures-visible
---
# Research & decisions — 045-failures-visible

## Progress
- 2026-11-26 step 1: branch fix/045-failures-visible made from main at bd01c63; spec and research written (paths, D1-D12), committed b818522.
- 2026-11-26 step 2: 4c1cc71, missing/unreadable blob is a per-block placeholder; body endpoint 404 body_not_stored with one log line; make verify green.

## Findings

### Where a blob or content row is read
All reads go through `store.Reader.contentCtx` (`internal/store/reader.go:212`), reached as
`Reader.Content` or `CtxReader.Content` (`reader_ctx.go:64`). A hash with no content row is
`ErrNotFound`; a row whose blob file is missing, unreadable, undecodable or the wrong size is
a plain error (`reader.go:238-249`). That plain error is FAIL-1: callers only forgive
`ErrNotFound`.

| Caller | Today on a missing/unreadable blob |
|---|---|
| `timeline/build.go:495` `content()`, used by `rawContent` (409), `toolResultText` (418), the thinking blocks (453, 460) | error for the whole session build |
| `timeline/digest.go:172` (digest text, the list rows of /api/sessions) | error → `summarizeRows` fails → /api/sessions 500 for every row |
| `timeline/harness.go:160` `readerOf` (hook-input comparison) | error for the whole build |
| `ui/view.go:53` `sessionViewChecked` content func (view builds) | nil block, view marked degraded and not cached |
| `ui/api_exchange.go:177` `getBody` (body endpoint) | 500 `store_error`, nothing logged |
| `ui/api_effects.go:90` `attribute` input | nil, ignored |
| `ui/api_effects.go:306,309` effect content | 500 `store_error` |
| `ui/api_effects.go:363,370` effect diff | 500 `store_error` |
| `ui/api_view.go:586` tool hook input | ignored |
| `ui/store_oracle.go:154` (test oracle) | passes the error through |
| `cmd/gateway/dump.go:145` `gateway dump` CLI | error printed, exit non-zero |

The hub (`ui/hub.go:734, 892`) reads sessions through `summarize`, so it inherits the digest
path.

### Where capture can fail or drop
| Path | Counted today | Metric today |
|---|---|---|
| Blob write (`store/content.go:48 writeBlobs` → `writeBlob`) | no; it fails the whole `write`, so the row is dropped (FAIL-3) | no |
| Store write (`store/store.go:152 write`, used by SaveExchange, SaveParse, SaveHook, SaveEffects, SaveCheckpoint, DeleteSnapshot, Sweep) | per caller: capture sink `storeFailed`, hooks sink `storeFailed`, netproxy `failed`; effects runner only logs | `gateway_store_write_seconds{result}` only |
| Capture queue full (`capture/sink.go:113 Submit`) | `droppedQueueFull` | `gateway_capture_dropped_total{reason="queue_full"}` |
| Capture memory budget (`core/capture.go:331`) | `Budget.Dropped` | `gateway_capture_dropped_total{reason="memory"}` |
| Capture undrained at shutdown | shutdown line | last-shutdown gauges |
| Hook queue full / memory limit (`hooks/sink.go:121 Submit`) | `queueFull`, `oversize` | none (CON-2, PERF-9) |
| Hook body over limit (`hooks/handler.go`, `RecordOversize`) | `oversize` | none |
| Hook body unreadable (`hooks/handler.go:80`) | logged, not counted | none |
| Hook store failure (`hooks/sink.go:233`) | `storeFailed` | none |
| Effects network recorder queue full (`netproxy/record.go:85,91`) | `dropped` | none |
| Effects network store failure (`netproxy/record.go:101`) | `failed` | none |
| Effects checkpoint (`effects/runner.go:280`) | logged only | none |

The hook handler answers 200 in every case.

### How /healthz is read today
- The claude shell function (`internal/install` block, testdata `block-linux.sh`):
  `curl -fsS --max-time 1 http://127.0.0.1:7197/healthz`; only the exit status matters, so
  any 2xx runs through the gateway and anything else (or no answer) bypasses it.
- `gateway doctor` (`install/doctor.go:274, 425`): HTTP status 200 = API listener passes;
  `/api/health` 200 = UI listener passes. The body is not read.
- Install health wait (`install/install.go:629 waitHealthy`): polls for status 200.
- `/healthz` (`server/server.go:116`) always writes `{"status":"ok"}`; `/api/health`
  (`ui/ui.go:127`) always writes `{"ok":true}`.

So the body can carry the degraded state with no consumer changing behaviour, as long as
the status stays 200.

## Decisions
- **D1** A blob that is missing, unreadable, undecodable or the wrong size becomes a new
  sentinel `store.ErrContentUnavailable` (wrapped, so `errors.Is` works). Context errors and
  database errors stay as they are: a deadline still fails the request (spec 030 D16).
- **D2** A missing content row for a referenced hash and `ErrContentUnavailable` both render
  as one placeholder, "[content unavailable]". With D5 a referenced hash without a row only
  arises from a failed blob write or a damaged store, so the old "[content not stored]" text
  meant the same thing. The body endpoint keeps the existing `body_not_stored` 404 for both,
  and logs one warn line with the hash when the row exists but the blob does not read.
  Timeline, digest and view builds do not log per block: they rebuild often and would repeat
  the line.
- **D3** Degraded format. One shape for /healthz and /api/health, always present:
  `{"status":"ok"|"degraded","since":"<process start, RFC 3339>","lost":N,
  "counts":{"hook_events_dropped":N,"captures_dropped":N,"store_write_failures":N,
  "blob_write_failures":N},"reason":"<only when degraded>"}`. /api/health keeps `"ok":true`
  beside it. `lost` is the sum of the four. `reason` names the non-zero kinds in that fixed
  order, comma-separated (for example "hook events dropped, blob writes failed"). Counts are
  since the process started, the same as the Prometheus counters, so a scrape and a health
  read always agree.
  /healthz answers 200 whether ok or degraded: the shell function, the install wait and doctor
  key on the status, so a degraded gateway is never bypassed. Only "not serving" is non-200.
- **D4** Clearing rule: degraded is sticky until the gateway restarts (counts since start
  never go down). The banner shows while `status` is degraded and cannot be dismissed.
  Thesis: one rule everywhere, counts that match the metrics, and a loss is never hidden
  because time passed. Antithesis: a one-off blip leaves the banner up for days and teaches
  people to ignore it; a rolling window ("lost in the last 15 min") would clear itself.
  Synthesis: for a local single-user gateway that restarts with the machine, sticky is the
  honest choice; the banner stays one line so it does not get in the way, and the doc says a
  restart clears it after the cause is fixed. Flips if gateways run for weeks unattended or
  feed an alerting system, where a window (or a separate `recent` count) is the better signal.
- **D5** FAIL-3 is fixed generically in `store.write`: a blob whose file write fails is
  counted, dropped from the items, and the transaction goes on without its content row. The
  row that references it (exchange body, event content, hook payload, effect copy) keeps the
  hash, so it reads as content unavailable (D2). A later write of the same hash finds no row,
  writes the blob again and heals. No schema change (non-goal). Context cancellation and
  database errors while writing blobs still fail the whole write.
- **D6** Counters: the store counts blob write failures and failed data writes (SaveExchange,
  SaveParse, SaveHook, SaveEffects, SaveCheckpoint). Maintenance writes (DeleteSnapshot,
  SweepSnapshots) are not counted: a failed delete loses no data. This one count replaces the
  per-caller store-failure counts in the health total, so nothing is counted twice; the
  callers keep their own counts for the shutdown line.
- **D7** `captures_dropped` = capture queue full + memory budget + network effects the
  recorder dropped. `hook_events_dropped` = hook queue full + memory limit + body over limit +
  body unreadable (the last one is newly counted). Capture-off deliveries are not a loss.
  Parse failures are not a loss: the raw exchange is stored.
- **D8** Metrics: keep `gateway_capture_dropped_total{reason}` and add `reason="effects"`;
  add `gateway_hook_events_dropped_total{reason="queue_full"|"memory"|"oversize"|"unreadable"}`,
  `gateway_store_write_failures_total`, `gateway_blob_write_failures_total`. All are
  CounterFuncs read from the atomic counts at scrape time, the existing pattern.
- **D9** The hook endpoint keeps answering 200 when it drops an event: a non-200 makes the
  harness show a hook error to the user and does not make it retry, so it would add noise and
  recover nothing. The drop is counted and logged instead (CON-2 is "no metric", fixed by D8).
- **D10** The banner reads /api/health on load and every 15 s. It does not ride the SSE
  stream, which only exists on session pages and is per session. The details link opens
  /api/health in a new tab: it is the full count, and it needs no new page.
- **D11** Doctor reads the /healthz body through `Env.Fetch` once the API listener passes and
  adds one `capture-health` result: pass when ok, warn when degraded with the counts and the
  fix "see the gateway's log; restart after fixing the cause to clear the counts".
- **D13** 2026-11-26 The old disk-cache test made its "real read error" by deleting a
  blob, which D1 turns into "unavailable". It is kept with its assertions and now corrupts
  the content row's size (a scan error); a probe that left the row intact made it fail. A
  second test covers the deleted blob being served and not degraded. Effect content and
  diff answer 404 `content_unavailable`; `gateway dump` prints "(content unavailable)".
- **D14** 2026-11-26 Correction to D5 and D2: the row cannot keep a hash with no
  content row. `foreign_keys` is on and every body, event, hook and effect hash column
  `REFERENCES content(hash)`, so leaving the content row out failed every insert. Instead the
  content row is still inserted (location 'blob') and only the file is missing, which the
  reader already reports as `ErrContentUnavailable` (D1), so the call shows "[content
  unavailable]" and the body endpoint 404 `body_not_stored`. To heal, `writeBlobs` now stats
  the file of an item whose row exists and rewrites it when it is missing (one stat per
  large item already stored). A blob that keeps failing is counted once per attempt. The
  "row with no content row" case of D2 stays handled for a damaged store.
- **D12** Capture off: no store and no sinks, so the counts are zero and health is ok.
