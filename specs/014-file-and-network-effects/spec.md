---
feature: 014-file-and-network-effects
kind: feat
status: done
version: 1
created: 2026-11-08
supersedes:
branch: feat/014-file-and-network-effects
approved:
approved-by:
sha256:
fingerprint:
---
# File and network effects

## Problem
The gateway shows what the model asked for (tool calls) and what Claude Code reported back
(hooks, 010), never what actually happened on disk or on the network. 010 shows an Edit's hook
as the harness's claim, not as proof. A Bash command that rewrites ten files, a script that
downloads a package, an edit to `.env`, a page fetched by a tool: none of it is visible. Reading
a session, the user cannot see which files changed in a turn, what changed in them, which files
Claude read, or which sites the session contacted (PLAN §7 Phase C).

This changes earlier features:
- **010 non-goal "verifying side effects on disk or network (later phase)"**: this is that phase.
  Hook claims stay shown as claims. Effects are shown beside them, not matched to them in v1.
- **010 non-goal "fuzzy matching"**: unchanged for hooks and tool calls. Effects are placed in
  turns by time only; no effect is joined to a tool call.
- **010 non-goal "no command edits a settings file"**: unchanged. `gateway hooks print` prints
  more (AC12); nothing writes Claude Code's settings.
- **docs/clients/claude-code.md setup**: `ANTHROPIC_BASE_URL` and the hooks move from shell
  exports and `~/.claude/settings.json` into one gateway settings file, loaded by a `claude`
  shell function that falls back to plain Claude when the gateway is down.

## Goal
- For each session, per turn: the files that changed (with a diff when a copy exists), the
  files Claude's own tools read, and the network connections made through the gateway's proxy.
- The same pure-Go binary on Linux and macOS. No root, no OS file-event API, no new dependency.
- Effects never slow Claude Code or the session timeline: the hook ingest answer and the
  timeline build do not depend on effect volume.

## Non-goals
- Who caused an effect: no link from a file change or connection to a tool call, sub-agent or
  program, and no confidence levels. Placement is by time only.
- Every single write: only the result between two checkpoints is seen.
- Reads outside Claude's own read tools (`cat`, `grep`, a script opening files).
- Watching `/tmp`, the OS temp folder, the home folder or the whole disk.
- Decrypting HTTPS or installing a certificate. HTTPS gives host, port, bytes, timing and
  outcome only.
- Which program opened a connection (no /proc or lsof lookup).
- Splitting effects between two sessions at work in the same folder, or connections between
  two active sessions.
- Capturing Claude Code started from an IDE extension or the Claude app.
- A command that installs the login service or the shell function (docs only).
- Store cleanup or retention.
- eBPF, inotify, FSEvents, Endpoint Security, Windows.

## Acceptance criteria
### File changes (checkpoints)
- **AC1** — When a hook event that carries a working folder is stored for a session, the
  system shall run a checkpoint of that session's watched folders after the ingest has
  answered, with at most one checkpoint running and one queued per session.
- **AC2** — When a session has no saved snapshot, the system shall record the state of its
  watched folders and store no effect; every later checkpoint saves its effects and the updated
  snapshot in one database transaction, so after a gateway restart the next checkpoint reports
  every change since the last saved checkpoint.
- **AC3** — When a later checkpoint finds a file created, modified or deleted since the saved
  snapshot, the system shall store one file effect with the path, the change (`created`,
  `modified`, `deleted`), the size and the time; a rename is stored as a delete and a create.
- **AC4** — When a watched folder is in a git work tree, the system shall take candidate files
  from git (tracked changes, untracked files, and git-ignored files but not the contents of
  git-ignored folders), running git without optional locks; otherwise it shall walk the folder.
  In both modes, a path with a part named in `effects.files.ignore` is skipped (default `.git`,
  `node_modules`, `dist`, `build`, `target`, `.venv`, `venv`, `__pycache__`, `.next`, `.cache`);
  the default list leaves out `.env`, so `.env` files are checked.
- **AC5** — When a changed file is text (no NUL byte in its first 8 KB) and no larger than
  `effects.files.max_copy_bytes`, the system shall store a copy of its new content; the diff's
  "before" is the previous copy, else the last committed version in git mode, else none, and the
  effect then shows the full content. Copies are stored as-is (ADR 0005).
- **AC6** — When it builds a session's watched folders, the system shall use the session's
  starting folder, the extra folders listed in the client's settings, the folders learned from
  the client's write and read tool paths outside the watched folders (the path's git work tree,
  or for a write outside any work tree its folder), and `effects.files.extra_dirs`; never the
  home folder, `/` or a temp folder.

### Reads by Claude's tools
- **AC7** — When a stored hook event is a completed read, search or list tool call (as the
  client profile names them), the system shall store a read effect with the path, and the line
  range or pattern, whose content is served from the stored hook payload; no file is read from
  disk for it.

### Network
- **AC8** — When a client sends CONNECT or a plain `http://` request to the HTTP proxy listener,
  the system shall forward the bytes unchanged and store one network effect with host, port,
  bytes up and down, start, end and outcome (`ok`, `refused`, `error`, `closed early`), plus
  method and URL without its query string for plain HTTP; it never decrypts and never stores
  `Proxy-Authorization`.
- **AC9** — When `effects.network.socks_proxy_addr` is set, the system shall accept SOCKS5
  CONNECT with no authentication on it and store the same fields as AC8.
- **AC10** — When a network effect is stored, the system shall link it to the one active
  session (a hook event in the last 10 minutes and no session end); with no active session or
  more than one, it is stored unassigned.
- **AC11** — When the gateway's own upstream transport would send a request through a proxy
  URL that points at one of the gateway's proxy listeners, the system shall connect directly.
- **AC12** — When `gateway hooks print` runs with `effects.network.enabled` true, the system
  shall print one Claude Code settings document holding the hooks, an `env` block
  (`ANTHROPIC_BASE_URL`, `HTTPS_PROXY`, `HTTP_PROXY`, `NO_PROXY`) and, only when
  `effects.network.sandbox` is true (off by default), a `sandbox` block pointing at the proxy
  ports; with network effects disabled the output is the hooks document of today.

### Storage, API and web UI
- **AC13** — When effects are stored for a session, the system shall leave that session's
  timeline version (`SessionVersions`) unchanged, keep a separate effect version per session,
  and serve the effects in time order (`GET /api/sessions/{id}/effects`), a file's content
  (`GET /api/effects/{id}/content`) and a server-computed line diff (`GET /api/effects/{id}/diff`).
- **AC14** — When the web UI shows a session, the system shall show one folded row per turn with
  counts (`N changed · N read · N sites`) that expands to the files (path, change as glyph and
  word, size, diff or content link) and connections (host:port, bytes, duration, outcome as
  glyph and word), each effect placed in the turn whose time range holds it, with new effects
  arriving live and batched, without a reload and without a timeline rebuild.
- **AC15** — When the web UI shows the sessions list, the system shall show each session's
  effect counts, the number of unassigned connections with a link to a page listing them, and,
  on every effects view, a coverage line stating what is and is not seen.

## Edge cases
- **E1** — `git` not installed, or the folder is not a work tree → scan mode (AC4).
- **E2** — A file created and deleted between two checkpoints → never reported (AC3).
- **E3** — Binary file, or a file over `max_copy_bytes` → effect with size, no copy, no diff
  (AC5).
- **E4** — A file modified and then put back to its committed content → reported as modified
  again, because paths seen before are re-checked even when git no longer lists them (AC3, AC4).
- **E5** — A command changes 500 files → one checkpoint stores 500 effects; the timeline stays
  responsive behind one folded row (AC3, AC14).
- **E6** — Claude's own `git commit` runs during a checkpoint → no `index.lock` clash (AC4).
- **E7** — `.env` listed in `.gitignore` → still checked (AC4).
- **E8** — Claude writes to `/tmp/x` or `~/notes.txt` → no folder is learned from it (AC6).
- **E9** — Two sessions at work in the same folder → each keeps its own state; a change can
  show in both (AC2, AC3).
- **E10** — Gateway restarted mid-session → the next checkpoint compares against the last saved
  snapshot and reports every change since, including those made while the gateway was down,
  in the turn where it finds them (AC2).
- **E11** — Hook event with no session id or no working folder → no checkpoint (AC1).
- **E12** — Proxy target unreachable → effect with outcome `refused` or `error`, client gets
  502 (CONNECT) or the SOCKS5 failure reply (AC8, AC9).
- **E13** — Session ends with a tunnel still open → the effect is stored when the tunnel
  closes; at gateway shutdown open tunnels are stored as `closed early` (AC8).
- **E14** — A proxy listener's port is taken → the gateway fails to start with one clear line,
  like `listen_addr` (AC8).
- **E15** — `effects.enabled` false → no checkpoints, no read effects, no proxy listeners; the
  web UI shows no effects rows (AC1, AC7, AC8).
- **E16** — A session ends, or has no hook event for `effects.files.snapshot_retention` (default
  7 days) → its saved snapshot is deleted; a later checkpoint for it starts a new baseline (AC2).
- **E17** — The gateway dies during a checkpoint → nothing from that checkpoint is saved; the
  next checkpoint redoes it from the last saved snapshot (AC2).

## Constraints
- One pure-Go code path for Linux and macOS (`CGO_ENABLED=0`). No new Go dependency: git runs
  as a program, SOCKS5 and the line diff are written in the repo.
- Hook ingest p99 (010 AC3) unchanged: checkpoints never run on the ingest path.
- Proxy listeners accept loopback addresses only, as `listen_addr` does.
- Phase C needs the gateway on the host (binary or login service), not in Docker. Docker keeps
  working for API capture.
- The gateway never writes into a watched folder and never changes a git index.

## Agnosticism check
- Client axis: everything Claude Code-specific lives in `internal/clients/claudecode/`: the hook
  `cwd` field, the read, search, list and write tool names and their inputs,
  `additionalDirectories` in Claude Code's settings files, reading content out of a Read
  payload, and the printed settings document with its `env` and `sandbox` blocks.
- The checkpoint runner, proxy, store, API and web UI read only canonical data: a new kind,
  `effect`, stored in its own table like `harness_event` (ADR 0012), and one new
  harness-neutral field on `HarnessEvent`, the working folder. ADR 0014 records both.
- Protocol and provider axes: untouched. `internal/core/` gains the `effect` type and the
  working-folder field, and the upstream transport's proxy choice skips the gateway's own
  listeners (AC11); no client or provider knowledge enters it.

## Open questions

## Changelog
- v1 (2026-11-08) — first draft, with Karthik's answers to K1–K10 (research.md)
