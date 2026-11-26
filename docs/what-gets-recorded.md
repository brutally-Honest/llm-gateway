# What gets recorded

This page says what the gateway keeps, what it misses, and the limits you should know
about. Each topic has one owning page; this one only points to it.

## What is recorded

- **Sessions and turns.** Every model call that goes through the gateway is copied and
  stored. Calls are grouped into sessions by the id the client sends, and into turns by
  the prompts you typed. If a client sends no session id, the call is still stored but is
  not part of a session. Seen on the Sessions page and the session page of the web UI.
  Detail: [capture.md](capture.md#2-what-is-in-it).
- **Tool calls.** The tools the model asked for, their inputs and results, as they
  appear in the conversation. With Claude Code's hooks on, the gateway also stores what
  Claude Code reports about each tool (it ran, failed, was denied). Seen on the session
  page. Detail: [claude-code-hooks.md](clients/claude-code-hooks.md).
- **File and network effects.** File changes found at checkpoints, files Claude's own
  Read, Grep and Glob tools read, and network connections made through the gateway's
  proxy. HTTPS shows host, port, bytes, timing and outcome; it is never decrypted.
  Detail: [claude-code.md](clients/claude-code.md#what-is-and-is-not-seen).
- **"Needs a look".** A list on the session page of things worth a second look. An item
  goes there when it is one of these:
  - a tool call that failed;
  - a model call that failed;
  - a file changed while no tool was running;
  - file or network effects that cannot be pinned to one tool call;
  - a turn with no model calls captured (only hook events);
  - a tool that ran with no captured model call asking for it.

  The page lists the first 8 and says "and N more". The Sessions page can filter to
  sessions that have such items.
- **API-equivalent cost.** What a call would cost at the provider's published API
  prices, worked out from the token counts in the captured response and a table of
  prices shipped with the gateway. It is not what you were charged. It needs no API key,
  so it works on a subscription plan too. A model that is not in the table (or a call
  priced differently, such as fast mode) shows its cost as unknown, never as an error.
- **The gateway's own health.** The gateway counts what it failed to keep: hook events
  and captures dropped, store writes and blob writes that failed. If any count is above
  zero since the last start, the gateway is "degraded". It still proxies normally. You
  see this in `GET /healthz`, `GET /api/health`, a banner in the web UI, `gateway doctor`
  and the Prometheus metrics. Detail:
  [observability.md](observability.md#capture-health).

Copies of changed text files, `.env` included, are stored as they are. See
[Privacy](#privacy).

## What bypasses the gateway

The gateway only sees what is sent through it.

- **`command claude`.** `gateway install` adds a shell function named `claude` to your
  shell's startup file. The function starts Claude Code with the gateway's settings, but
  only when the gateway answers its health check. `command claude` skips shell functions,
  so it runs plain Claude Code and nothing is captured. The same happens when the gateway
  is not running (the function then prints a notice and starts plain Claude Code).
- **Non-interactive shells.** The function lives in `~/.zshrc` (or `$ZDOTDIR/.zshrc`), or
  in `~/.bashrc` on Linux and `~/.bash_profile` on macOS. Those are the files of
  interactive shells. A script, a cron job or any shell that does not read that file
  never defines the function. For fish, ksh and other shells the installer does not edit
  a file; it prints the lines for you to add.
- **The Claude desktop app and IDE extensions.** The installer adds only the shell
  function. Nothing in this repository configures those clients, so their sessions are
  not captured. This follows from how the gateway is installed; it was not tested for
  every client version.
- **Inherent client limits.** Some Claude Code traffic goes straight to Anthropic by
  design of the client: fast-mode checks, the WebFetch domain check, and claude.ai
  connectors. The list is kept in one place:
  [PLAN.md section 3](../PLAN.md#3-client-support).

## Several accounts on one machine

Sessions from several Claude accounts are mixed together. The gateway has no account
label: every request is stored with the principal `local`, and the stored row records
only how the client logged in (API key or subscription), not which account. You cannot
filter or split by account yet.

## Several sessions at once

Model calls and file effects stay with their own session. Network connections are the
exception. The gateway links a connection to a session only when exactly one session
has had a hook event in the last 10 minutes (`effects.network.active_window`) and has
not ended. With two or more such sessions, or none, the connection is stored as
unassigned. Unassigned connections are listed on the Sessions page; they are not shown
under any session. There is no per-session attribution for them yet. The rule is in
[claude-code.md](clients/claude-code.md#what-is-and-is-not-seen) and
[ADR 0015](decisions/0015-effect-attribution-at-read-time.md).

## Node version

To see a Node program's network calls, you need Node 22.21 or later in the 22 line, or
Node 24 or later. The settings file the gateway writes sets `NODE_USE_ENV_PROXY=1`, which
makes Node use the proxy variables; older Node ignores it. On older Node, with the Claude
Code sandbox off, the program's calls go direct and are not seen. With the sandbox on,
they fail. Details and the `http`/`https` minimums are in
[install.md](install.md#known-limits). The check is a note printed by `gateway install`.
Nothing in the gateway checks your Node version.

## AppArmor on Ubuntu

On some Ubuntu systems (24.04 and later), AppArmor restricts unprivileged user
namespaces. That can turn off the `bwrap` sandbox Claude Code uses for Bash commands.
The sandbox setting `sandbox.network.allowAllUnixSockets` is the one that matters here.
This is a known issue and is under investigation after v1. This page does not claim a
fix. The gateway's sandbox option is off by default, and `gateway doctor` warns about
the condition: see [install.md](install.md#check-apparmor) and
[claude-code.md](clients/claude-code.md#the-sandbox-optional).

## Privacy

- **Secrets in content are stored.** Prompts, tool results and request and response
  bodies are stored as sent. A secret an agent read (a `.env` file, say) is in the store
  in plain text. [ADR 0005](decisions/0005-secrets-in-captured-content.md) explains why.
- **Auth headers are redacted.** In the stored copy, headers such as `Authorization`,
  `Proxy-Authorization`, `Cookie`, `Set-Cookie`, `X-Gateway-Key` and `x-api-key` are
  replaced with `[REDACTED]`. Details:
  [capture.md](capture.md#3-what-is-redacted-and-what-isnt).
- **Nothing is uploaded.** The gateway has no telemetry. It sends your requests to the
  provider you configured, as a proxy must, and nothing else. Tracing is off by default.
  If you turn it on, spans (sizes, timings, status, model, exchange id; no content or
  headers) go to `tracing.otlp_endpoint`, which defaults to a Jaeger on `127.0.0.1`. That
  setting also accepts an `https` address elsewhere, so check it if you change it. See
  [observability.md](observability.md#traces-off-by-default).
- **Loopback only.** Every listener (API, UI, proxies) must be `127.0.0.0/8`, `::1` or
  `localhost`. Any other address stops the gateway at start. There is no override.
- **File permissions.** The capture folder is `0700` and every file in it is `0600`. The
  config folder and file the installer writes are `0700` and `0600`.

## Disk growth

The store grows without limit. For heavy use, expect very roughly hundreds of MB a day.
It varies a lot with how you work. There is no retention or pruning: nothing deletes old
sessions. (Only the saved file baselines used for change detection are cleaned up, at
session end or after 7 days.)

Almost all of the growth is conversation history. Claude Code resends the whole
conversation on every call, so each call's request body is a little larger than the last.

The blob store is content-addressed: a blob is named by the SHA-256 of its bytes and
compressed with zstd, so identical content is stored once. This saves space in two
places and not in a third:

- Parsed pieces (each message, tool result, the system prompt and tools list) are hashed
  one by one, so a message resent on every turn is stored once.
- Compression shrinks everything.
- Raw bodies are stored whole, by the hash of the entire body. A body that has grown by
  one message has a different hash, so it is a new blob. Raw storage therefore still
  grows with every turn.

The data lives in `capture.dir`. By default that is `$XDG_DATA_HOME/llm-gateway` if
`XDG_DATA_HOME` is an absolute path, otherwise `~/.local/share/llm-gateway`. To see its
size, run `du -sh` on that folder. The folder also holds a disposable `view-cache` the UI
trims itself. Where the store lives and what is in it:
[capture.md](capture.md#1-where-the-store-lives).
