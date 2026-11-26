# Architecture

This page shows how the gateway is built, for a developer reading the repo. It maps each
part to its package and follows one call from the client to the store and the UI. Rules
live in their own pages and are linked, not repeated: the design rules are in
[PLAN.md section 5](../PLAN.md#5-core-design-rules) and the working rules in
[AGENTS.md](../AGENTS.md).

## The parts

```
 client (Claude Code)                                   provider (Anthropic)
        |                                                      ^
        v                                                      |
  +---------------- API listener 127.0.0.1:7197 -----------------+
  | rate limiter -> core proxy (ReverseProxy + Rewrite) ---------+
  |                    | tee: copies of request and response     |
  |                    v                                         |
  |   /_gateway/hooks/{client}   /healthz   /metrics             |
  +---------|---------------------|------------------------------+
            |                     |
            v                     v
     capture sink  <---- hook sink      HTTP proxy :7198, SOCKS :7195
   (bounded queue,       (own queue)         |  (effects: network)
    N workers)                |              v
            \                 |        effects runner (files, reads,
             v                v         network connections)
              +--------- store ---------+
              | SQLite (WAL) + blobs/   |
              +--------|----------------+
                       | commit            read-only reader
                       v                        |
                 UI hub (live events) ----> UI listener 127.0.0.1:7199
                                            JSON API, SSE streams, static page
                                                |
                                         view cache (memory + disk)
```

| Part | Package | What it does |
|---|---|---|
| Startup, wiring, shutdown | `cmd/gateway` (`run.go`) | Reads the config, opens the store, builds every part below, binds the listeners, and stops them in order. Other commands (`install`, `doctor`, `dump`, `hooks`) are in the same folder. |
| Config | `internal/config` | Defaults, file and environment values, and the loopback-only check on every listener (`listen.go`). |
| Core proxy | `internal/core` | One `httputil.ReverseProxy` per protocol adapter. `Rewrite` builds the upstream request, and tees copy the request body and the response as they pass. It knows no provider and no client. |
| Protocol adapter | `internal/protocols/anthropic` | Provider-specific parts: the URL prefix (`/anthropic`), the parser from raw bodies to canonical events, usage and cost rules. |
| Client profile | `internal/clients/claudecode` | Client-specific parts: how a session is recognised, turn titles, hook mapping, the installer's settings file and shell function. |
| Capture pipeline | `internal/capture` | A bounded channel and a fixed number of workers. A worker stores the raw exchange, parses it, and stores the canonical events. |
| Store | `internal/store` | SQLite plus blob files. Writes go through one writer connection; the UI uses a separate read-only reader. |
| Timeline | `internal/timeline` | Turns stored events into turns, tool calls, effects, "Needs a look" items and per-call views. Used only on the read side. |
| Cost | `internal/cost` | API-equivalent cost from captured usage and a price table shipped with the adapter. |
| Effects | `internal/effects`, `internal/netproxy` | File changes at checkpoints, files Claude's tools read, and network connections made through the gateway's own HTTP and SOCKS proxies. |
| Hooks endpoint | `internal/hooks` | Takes Claude Code's hook events and hands them to their own queue and sink. |
| Rate limiter | `internal/ratelimit` | Per gateway key, fixed window, counts in memory. It is mounted only when `ratelimit.enabled` is true. |
| UI | `internal/ui` (`web/` is the page) | Read-only JSON API, live streams, the view cache, and the static page. |
| Observability | `internal/observability`, `internal/logging` | Prometheus metrics, capture health, traces, shutdown record, logs. |
| Install and doctor | `internal/install`, `internal/startup` | The installer, service files, `gateway doctor`, the start-up error file. |

## How one call flows

1. The client sends a request to the API listener. The path prefix (`/anthropic`) picks the
   adapter.
2. If the rate limiter is on, it admits or refuses the call before anything goes upstream.
3. The core proxy forwards the request. `Rewrite` changes only what forwarding needs. A tee
   on the request body copies bytes on the side.
4. The response goes back to the client as it arrives, through a second tee. Streams are not
   buffered. Errors pass through unchanged.
5. When the call ends, the copies become one `Exchange` and are handed to the capture sink with
   `Submit`. `Submit` never waits. If the queue is full, the exchange is dropped and counted.
   The call itself is not affected.
6. A worker stores the raw exchange first, so a parse failure never loses it. Then the
   adapter's parser makes canonical events, and the worker stores them with the parse status.
7. After a commit, the sink calls the UI hub, which tells open pages that something changed.

The core only sees canonical events and the adapter and profile interfaces. Anything that
knows a provider or a client sits in `internal/protocols/<name>/` or
`internal/clients/<name>/`. The reasoning is in
[PLAN.md section 2](../PLAN.md#2-agnosticism-the-central-design-principle), and the checks
are in [AGENTS.md](../AGENTS.md).

## Ports and routes

All listeners must be loopback (`127.0.0.0/8`, `::1` or `localhost`); the gateway refuses to
start otherwise.

| Port | Setting | What listens |
|---|---|---|
| 7197 | `listen_addr` | The API listener. It serves the proxied provider API under each adapter's prefix (`/anthropic/*`), the hooks endpoint `/_gateway/hooks/{client}`, `GET /healthz` and `GET /metrics`. Any other path under `/_gateway/` is a 404 and is never forwarded. |
| 7198 | `effects.network.http_proxy_addr` | The HTTP proxy for network effects. |
| 7195 | `effects.network.socks_proxy_addr` | The SOCKS proxy for network effects. |
| 7199 | `ui.listen_addr` | The UI listener: the static page, the `/api/...` JSON and stream routes (including `GET /api/health`), and also `GET /metrics`. Only GET is accepted, and the Host and Origin are checked. |

Notes:

- `/metrics` is on two listeners, 7197 and 7199, and on neither proxy listener
  ([observability.md](observability.md#metrics-always-on)).
- Port 7196 is not used; the installer keeps it free ([install.md](install.md)).
- The two proxy listeners start only when capture, `effects.enabled` and `effects.network.enabled` are on. The UI
  listener starts only when capture and `ui.enabled` are on.
- The hooks route is always mounted. With capture off it answers and counts but stores nothing.

## The store

`capture.dir` holds one SQLite database in WAL mode (the pure-Go `modernc.org/sqlite`
driver, [ADR 0004](decisions/0004-capture-store.md)) and a `blobs/` folder. Content over 4 KiB is
compressed with zstd and written to a file named by the SHA-256 of its bytes, so equal content
is stored once. Where the store lives, its tables and what is redacted are in
[capture.md](capture.md). What is and is not recorded is in
[what-gets-recorded.md](what-gets-recorded.md).

The capture side writes through one writer connection. The UI reads through a read-only
reader opened on the same folder, so a UI request cannot change the store.

## The view cache

The UI builds each session's list row ("digest") and full view from the store. Building a
large session is slow, so results are cached in memory and on disk.

- **Where.** `<capture.dir>/view-cache/<build>/`, with `m/` for per-session memos and `v/`
  for views. One file per entry, `0600`, in `0700` folders.
- **What.** Only things rebuilt from the store: list-row facts and session views. Views over
  32 MiB are not written, and a build's views are kept under 1 GiB.
- **Key.** An entry is used only when its whole key matches. The folder name is a 16-character
  hash of the cache format, the gateway version, a hash of the running executable, the
  attribution skew, the home folder and the local time zone. The entry's own key adds the
  store's version of that session. A new binary means a new folder, so the cache is empty
  after every install or upgrade and is rebuilt in the background.
- **Disposable.** Nothing is lost if you delete it. If the store is found to be older than
  the cache (a restored backup), that build's folder is deleted. If the cache cannot open, the
  UI runs from memory alone.

Operator detail (sizes, uninstall, the list's behaviour while it fills) is in
[install.md](install.md#the-uis-view-cache).

## Live updates

An open session page does not reload. After a commit, the capture sink calls the UI hub. The
hub tells the streams that watch that session. The page holds one `EventSource` on
`/api/sessions/{id}/stream?view=1`; the sessions list holds one on `/api/stream`.

For a session view, the server waits 150 ms to gather changes, then rebuilds the view. Since
spec 040 it does not re-read every call: it keeps per-session state for the calls already
read and reads only what changed, then sends only the turns whose content changed since that
stream last sent them. A few cases still fall back to a full build, and the result is always
what a full rebuild would give
([spec 040](../specs/040-incremental-live-updates/spec.md)). Streams send a keep-alive every
15 seconds, at most 32 are open at once, and the browser reopens a dropped one after a delay
that grows to 30 seconds. While it is down the header shows "reconnecting…"
([ui.md](ui.md#live-updates)).

## Shutdown order

On a stop signal the gateway:

1. stops accepting new connections, and cuts proxy tunnels and the UI's live streams at once;
2. gives in-flight model calls up to `shutdown_timeout` (default 10 s) to finish, and cuts
   what is still running, recording it as cut by the gateway;
3. stops the UI server (up to 2 s) and asks the UI's background work to stop;
4. drains capture: the hook sink, the effects runner and the capture queue get a budget of
   their own, as long as `shutdown_timeout` again;
5. waits for the UI's background builds to end, up to 5 s, and only then
6. closes the reader and the store.

Capture drains before the UI's work is waited for, and the UI wait never uses the drain's
budget, so observability ranks above the UI. The wait before closing exists because a build
that is still reading when the database closes would reopen it
([spec 047](../specs/047-flake-and-banner/research.md), D15 to D19). `run.go` holds the
order (`captureRun.close`) and the 5 s figure (`uiWorkTimeout`). Each stop also writes
`last-shutdown.json`, which the next start exposes as metrics
([observability.md](observability.md#metrics-always-on)).

## Degraded health and what fails quietly

The gateway never lets a capture problem fail a call. It counts what it lost and says so.

- **Counted.** Dropped hook events, dropped captures, failed store writes and failed blob
  writes. Each is a Prometheus counter.
- **Degraded.** If any count is above zero since the start, status is `degraded`.
  `/healthz` and `/api/health` still answer HTTP 200 on purpose. The `claude` shell function
  checks the health URL with `curl -f` and starts plain Claude Code when it fails, so a
  non-200 answer would send traffic around a gateway that is still working. Check the
  `status` field, not the HTTP code.
- **Where it shows.** `gateway doctor` warns with the counts. The UI shows a banner,
  "capture degraded: N lost since start". Closing it hides it for that browser tab only; it
  comes back when the lost count rises or the gateway restarts (spec 047 D1).
- **Missing content.** A body whose blob is missing or unreadable shows as
  `[content unavailable]` in that one block. The call is kept, and the rest of the session is
  unaffected.

Counters, causes and fixes are in
[observability.md](observability.md#capture-health). They are not repeated here.

## Where to read next

- Rules and agnosticism: [PLAN.md](../PLAN.md), [AGENTS.md](../AGENTS.md).
- What is stored and how to inspect it: [capture.md](capture.md).
- The web UI as a user sees it: [ui.md](ui.md).
- Claude Code specifics: [clients/claude-code.md](clients/claude-code.md),
  [clients/claude-code-hooks.md](clients/claude-code-hooks.md).
- Decisions that outlive a feature: [decisions/](decisions/).
