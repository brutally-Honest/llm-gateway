---
feature: 014-file-and-network-effects
status: draft
version: 1
approved:
approved-by:
sha256:
fingerprint:
spec-fingerprint:
---
# Plan — 014-file-and-network-effects

## Summary
Two sensors feed one new kind, `effect`, stored in its own table. Files are checked at
**checkpoints**: each stored hook event asks "what changed since last time?" with git (or a
folder walk when there is no git). The network goes through a **forward proxy** (HTTP CONNECT
plus optional SOCKS5) that Claude Code is pointed at by a settings file the gateway prints,
with Claude Code's sandbox forcing Bash traffic through it. Claude's own Read/Grep/Glob calls
become read effects straight from the stored hooks. Each session's file snapshot is saved in
SQLite in the same transaction as the checkpoint's effects, so a gateway restart loses nothing.
Effects have their own version, API and live event, and show as one folded row per turn, so
they never trigger a timeline rebuild.
Built in this order (Option B): foundation (store, API, UI), then files, then network, then one
end-to-end check. Functional tests only (research.md D1).

## Context
- Hook ingest: `internal/hooks/sink.go` (`Sink`, `OnStored` callback, `HookStored`);
  `internal/core/harness.go` (`HarnessEvent`, phases, `HarnessMapper`);
  `internal/clients/claudecode/harness.go` (mapper), `snippet.go` (`HookSnippet`, printed by
  `cmd/gateway/hooks.go`), `profile.go`, `rules.go` (`ui.ClientRules`).
- Store: `internal/store/migrate.go` (ordered `migrations`, `user_version`),
  `content.go` (content-addressed blobs), `hooks.go` (`SaveHook`), `reader_versions.go`
  (`SessionVersions`, CTE join pattern on the session index).
- Upstream transport: `internal/core/proxy.go` (`Proxy: http.ProxyFromEnvironment`, 001 Q9).
- UI server: `internal/ui/ui.go` (routes), `api_sessions.go` (`buildSummary`, fresh row merged
  with the cached digest), `hub.go` / `stream.go` (live events), `timeline_cache.go`,
  `flight.go` (singleflight).
- Web: `internal/ui/web/model.js` (`buildTurns`), `timeline-page.js`, `sessions-page.js`,
  `drawer.js`, `drawer-sections.js`, `route.js`; Node tests in `internal/ui/webtest/`.
- Config: `internal/config/` (one file per block, `load_*_test.go`), `config.example.yaml`.
- Run wiring: `cmd/gateway/run.go`.
- Patterns to keep: one running + one queued per session (013 CR-003), a bounded queue that
  never blocks the caller and counts drops (010 sink), facts computed on the server and only
  displayed by the browser, status as glyph + word.

## Approach
- **Files:** a per-session runner hangs off `OnStored`. Each stored hook event with a session and
  a working folder marks the session dirty; one goroutine per busy session runs checkpoints, at
  most one running and one queued. A checkpoint compares the watched folders to the session's
  last saved snapshot (SQLite, cached in memory while the session is active), copies small
  text files into the existing blob store, and saves the new effects and the changed snapshot
  rows in one transaction.
- **Reads:** the same callback asks the client profile whether a completed tool call is a read,
  search or list, and stores a read effect pointing at the hook event.
- **Network:** two loopback listeners in the same process. CONNECT and SOCKS5 tunnels are
  piped and counted; plain HTTP is forwarded with a transport that never uses a proxy. Each
  closed connection becomes one effect linked to the single active session, or unassigned.
- **Settings:** `gateway hooks print` prints the full Claude Code settings document (hooks + env
  + sandbox). The user saves it as a separate file loaded by a `claude` shell function
  (docs), so a stopped gateway means plain Claude, not a broken one.
- **UI:** a flat, time-ordered effects API with its own version; the browser places effects in
  the turns it already builds and renders one folded row per turn.

## Alternatives considered
### A1 — Checkpoints on hook events + forward proxy through Claude Code settings (chosen)
- Pros: one pure-Go path on Linux and macOS; costs nothing while Claude is idle; uses git for
  cheap change lists and a ready "before" version.
- Cons: sees results between checkpoints, not each write; network coverage depends on programs
  honouring the proxy settings (sandbox closes this for Bash); depends on Claude Code hooks.
### A2 — OS file watchers (inotify on Linux, FSEvents on macOS) + the same proxy
- Pros: every write with an exact time; would see changes made while Claude is idle.
- Cons: two code paths; FSEvents needs cgo, which breaks the `CGO_ENABLED=0` build; inotify
  needs one watch per folder plus a race on new folders; noisy bursts need coalescing.
### A3 — Sensors in the Docker container (bind mounts + inotify inside)
- Pros: one Linux code path; runs where the gateway already runs in compose.
- Cons: every folder must be mounted ahead of time; Docker Desktop on macOS has a history of
  dropping file events on bind mounts; the container cannot see host programs.

## Design
### Core and ADR 0014
- `core.Effect` (harness-neutral): `Kind` (`file_change` | `file_read` | `net_conn`), `At`,
  session identity (principal, client, session key; empty key = unassigned), and per-kind
  fields: `Root`, `Path`, `Change`, `Size`, `Mode` (`git` | `scan`), `AfterHash`, `BeforeHash`,
  `HookID`, `Detail` (line range or pattern; method + URL for plain HTTP), `Host`, `Port`,
  `Via` (`connect` | `http` | `socks`), `BytesUp`, `BytesDown`, `EndedAt`, `Outcome`.
- `core.HarnessEvent` gains `WorkDir string` (the folder the harness says it is working in).
- `core.EffectRules`, an optional profile interface (like `HarnessMapper`):
  - `ReadTarget(tool string, input []byte) (path, detail string, ok bool)` for completed read,
    search and list tools;
  - `ReadContent(payload []byte) ([]byte, bool)` to pull what the tool returned out of a stored
    post-tool payload;
  - `WritePaths(tool string, input []byte) []string`;
  - `ExtraDirs(workDir string) []string`.
- `docs/decisions/0014-effect-kind-and-no-root-sensors.md`: the new kind and its own table, the
  checkpoint and proxy sensors, and why not OS watchers or Docker (A2, A3).

### Config (`internal/config/effects.go`)
```yaml
effects:
  enabled: true
  files:
    enabled: true
    extra_dirs: []
    # .env is left out on purpose so changes to .env files are tracked; add it here to stop that.
    ignore: [.git, node_modules, dist, build, target, .venv, venv, __pycache__, .next, .cache]
    max_copy_bytes: 262144
    git_timeout: 10s
    snapshot_retention: 168h
  network:
    enabled: true
    http_proxy_addr: 127.0.0.1:7198
    socks_proxy_addr: 127.0.0.1:7195
    sandbox: true
    active_window: 10m
```
Validation: proxy addresses are loopback, distinct from each other, from `listen_addr` and from
`ui.listen_addr`; an empty `socks_proxy_addr` turns SOCKS5 off; `sandbox: true` with SOCKS5 off
prints a sandbox block with the HTTP port only; `max_copy_bytes` > 0; `extra_dirs` are absolute;
`snapshot_retention` > 0. Env overrides follow the existing `GATEWAY_*` naming. `ignore` names
are matched against each part of a path (folders and file names); nothing forbids adding `.env`.

### Store (`internal/store/effects.go`, one new migration)
```sql
CREATE TABLE effects (
  id           INTEGER PRIMARY KEY AUTOINCREMENT,
  principal_id TEXT NOT NULL,
  client       TEXT NOT NULL,
  session_key  TEXT,
  kind         TEXT NOT NULL CHECK (kind IN ('file_change','file_read','net_conn')),
  at           INTEGER NOT NULL,
  root TEXT, path TEXT, change TEXT, size INTEGER, mode TEXT,
  after_hash  TEXT REFERENCES content(hash),
  before_hash TEXT REFERENCES content(hash),
  hook_id     INTEGER,
  detail      TEXT,
  host TEXT, port INTEGER, via TEXT,
  bytes_up INTEGER, bytes_down INTEGER, ended_at INTEGER, outcome TEXT
);
CREATE INDEX effects_session ON effects(principal_id, client, session_key, id);

CREATE TABLE effect_snapshots (
  principal_id TEXT NOT NULL,
  client       TEXT NOT NULL,
  session_key  TEXT NOT NULL,
  root         TEXT NOT NULL,
  path         TEXT NOT NULL,   -- '' marks the root itself, so a root with no files is kept
  size         INTEGER, mtime_ns INTEGER, hash TEXT,
  copy_hash    TEXT REFERENCES content(hash),
  PRIMARY KEY (principal_id, client, session_key, root, path)
) WITHOUT ROWID;
```
- `at` uses the same unit as `hook_events.received_at`. A session's principal and client are
  taken from the hook event that triggered the checkpoint; unassigned connections use the
  principal the hook ingest uses and client `unknown`.
- `SaveCheckpoint(ctx, session, []Effect, upserts, deletes)` writes a checkpoint's effects and
  its changed snapshot rows in one transaction (AC2, E17). `SaveEffects(ctx, []Effect)` is the
  same without snapshot rows, for read and network effects.
- `LoadSnapshot(session)` returns a session's rows; `DeleteSnapshot(session)` drops them;
  `SweepSnapshots(cutoff)` drops the rows of every session whose last hook event is older than
  the cutoff (one query joined to `hook_events`).
- `ActiveSessions(since)` returns sessions with a hook event after `since` and no session-end
  event after it, so the network's active set survives a restart.
- `EffectVersions(ids)` returns `COUNT(*), MAX(id)` per session with the CTE join used by
  `SessionVersions`; `SessionVersions` is untouched (AC13).
- `EffectCounts(ids)` returns changed / read / sites per session for a list page, one grouped
  query.
- `ListEffects(session, afterID, limit)` and `ListUnassigned(afterID, limit)`, in id order.

### File checkpoints (`internal/effects/`)
- `Runner` subscribes to `OnStored`. `hooks.HookStored` gains `ID` and the mapped
  `core.HarnessEvent` (no body). The callback only updates in-memory session state and signals;
  it never blocks.
- Per session in memory: `roots` (set), `snap` (root → path → `{size, mtimeNs, hash, copyHash}`),
  last hook time, ended flag, and the run/dirty pair. `roots` and `snap` are a cache of
  `effect_snapshots`: loaded with `LoadSnapshot` on the session's first checkpoint after a
  start or an eviction, and evicted from memory after 1 h without a hook. A goroutine runs a
  checkpoint while the session is dirty, so a burst of 60 hooks costs at most one running and
  one queued checkpoint.
- **Roots (AC6):** at the first hook with a `WorkDir`: that folder, plus `rules.ExtraDirs`, plus
  `extra_dirs`. On each post-tool event, `WritePaths` and `ReadTarget` paths outside every root
  add the path's git top level (`git rev-parse --show-toplevel`) or, for a write outside any
  work tree, its folder. Rejected: `/`, the home folder or any of its ancestors, anything under
  `os.TempDir()` or `/tmp` (and `/private/tmp`, `/private/var/folders` on macOS). A root inside
  another root is folded into it.
- **Git mode (AC4):** per root, cached `git rev-parse --show-toplevel`. Candidates =
  `git --no-optional-locks -C <top> status --porcelain=v2 -z --untracked-files=all`
  ∪ `git --no-optional-locks -C <top> ls-files -z -o -i --exclude-standard --directory`
  (entries ending in `/` are folders and skipped) ∪ every path already in `snap`. Paths with a
  part named in `ignore` are dropped. Every git call has `git_timeout`; a failure logs one line
  and skips that root for this checkpoint.
- **Scan mode (AC4):** `filepath.WalkDir`, skipping paths with a part named in `ignore`, not
  following symlinks, regular files only.
- **Compare (AC2, AC3):** `lstat` each candidate. Unchanged size and mtime → skip. Otherwise read
  the head to decide text or binary; for text under the cap read all, hash, store the copy; for
  others hash with a streaming read. A changed hash, a new path or a missing path gives one
  effect: `created` (new and untracked, or new in scan mode), `modified`, `deleted`. A session
  with no saved rows only fills `snap` and saves it. Every checkpoint ends with one
  `SaveCheckpoint` holding its effects and the rows that changed (new, updated, deleted, and
  root markers), so a crash mid-checkpoint saves nothing and the next one redoes it.
- **Before version (AC5):** previous copy if `snap` has one; else in git mode
  `git --no-optional-locks -C <top> show HEAD:<path>` (text under the cap only, stored as a
  blob); else none.
- **End of life (E16):** on the session-end phase, one last checkpoint, then `DeleteSnapshot`
  and the memory entry go. `SweepSnapshots(now − snapshot_retention)` runs at startup and every
  hour.
- The runner never writes inside a root and never runs a git command that writes.

### Read effects (AC7)
In the same callback: on a post-tool event whose `ReadTarget` answers ok, store one
`file_read` effect (`Path`, `Detail`, `HookID`). Claude Code: `Read` → `file_path`, detail
`lines a–b` from `offset`/`limit`; `Grep` → `path` (or the working folder), detail the pattern;
`Glob` → `path`, detail the pattern. `ReadContent` reads `tool_response` from the Read payload.

### Network proxy (`internal/netproxy/`)
- **HTTP listener:** `CONNECT host:port` → dial with a 10 s timeout, answer
  `200 Connection Established`, then two `io.Copy` loops through counting writers. Absolute-form
  requests (`GET http://…`) → forwarded with a transport whose `Proxy` is nil, hop-by-hop
  headers and `Proxy-Authorization` removed, response streamed back; `Detail` = method +
  `scheme://host/path` (query string dropped). Dial failure → 502 and outcome `refused` or
  `error`.
- **SOCKS5 listener (AC9):** RFC 1928, method `00` only, `CONNECT` only, IPv4/IPv6/domain
  addresses; anything else gets reply `07` (or `08` for an address type) and an `error` effect.
- **Recording:** each tunnel or request yields one effect when it ends, sent to a bounded queue
  drained by one store worker; a full queue drops and counts, never slowing the tunnel.
  Shutdown closes the listeners, waits up to the existing shutdown timeout, then stores open
  tunnels as `closed early`.
- **Session link (AC10):** the runner keeps "active" = last hook within `active_window` and no
  session end, seeded at startup from `ActiveSessions(now − active_window)`. Exactly one active
  session → linked; else unassigned.
- **Loop guard (AC11):** `core/proxy.go` wraps `ProxyFromEnvironment`; when the chosen proxy's
  host:port equals one of the gateway's proxy listeners, it returns nil (direct).

### Settings document (AC12)
`claudecode.SettingsDoc(hooksAddr string, net *NetSettings)` keeps `HookSnippet`'s hooks and
adds:
```json
{
  "env": {
    "ANTHROPIC_BASE_URL": "http://127.0.0.1:7197/anthropic",
    "HTTPS_PROXY": "http://127.0.0.1:7198",
    "HTTP_PROXY": "http://127.0.0.1:7198",
    "NO_PROXY": "127.0.0.1,localhost"
  },
  "sandbox": {
    "enabled": true,
    "network": { "httpProxyPort": 7198, "socksProxyPort": 7195 }
  }
}
```
Values come from `listen_addr` and the proxy addresses. `net == nil` (network disabled) prints
exactly today's document.

### API and live updates (AC13, AC15)
- `GET /api/sessions/{id}/effects?after=<id>&limit=<n>` → `{effects: [...], version}` in id
  order; each entry has `id, kind, at, path, change, size, has_copy, has_diff, detail, host,
  port, via, bytes_up, bytes_down, duration_ms, outcome`. Default limit 500, maximum 5,000.
- `GET /api/effects/{id}/content` → a file change's stored copy, or a read effect's content via
  `ReadContent` on its hook payload; 404 with a reason when there is none.
- `GET /api/effects/{id}/diff` → unified line diff from a small Myers diff in
  `internal/effects/diff.go`, output capped at 2,000 lines with a "diff truncated" line.
- `GET /api/effects/unassigned?after=<id>` for the unassigned page.
- `buildSummary` merges `EffectCounts` into the fresh, non-memoised part of each row, so the
  digest cache key and `SessionVersions` stay as they are. `GET /api/sessions` gains
  `unassigned_connections`.
- Hub: a new stream event `effects` `{session, version}`, at most one per session every 500 ms.
  The browser then fetches `after=<last id>`. The timeline cache is never touched.

### Web UI (AC14, AC15)
- `effects.js` (pure, Node-tested): `placeEffects(turns, effects)` → per turn
  `{changed, read, sites, items}` by `at` within the turn's time range (before the first prompt
  goes to the existing "before first prompt" card); display helpers `changeGlyph`
  (`+ created`, `~ modified`, `− deleted`, `→ read`) and `outcomeGlyph` (`✓ ok`,
  `✕ refused`, `! error`, `… closed early`).
- `timeline-page.js`: one folded row per turn, `▸ 3 changed · 12 read · 2 sites`; expanding
  lists files then connections; a diff or content link opens a drawer section
  (`drawer-sections.js` "Effect") that fetches `/diff` or `/content` on open.
- Coverage line at the top of every effects list: "Files: changes seen at Claude's hook
  checkpoints, plus Claude's own reads. Network: traffic through the gateway proxy only;
  with the sandbox on, all Bash commands; MCP servers only if they use the proxy."
- `sessions-page.js`: an "Effects" column (`3 · 12 · 2` with a tooltip naming each count) and a
  quiet `N unassigned connections` link to `#/effects/unassigned` (`route.js`).
- Rendering follows 013: `DocumentFragment`, captured text only via `textContent`.

### Wiring (`cmd/gateway/run.go`)
`effects.enabled` false → nothing above is created (E15). Otherwise the runner subscribes to
the hook sink, the proxy listeners start next to the main listener and stop in the same
shutdown sequence, and the shutdown log line gains the effects counters (checkpoints run,
effects stored, proxy records dropped, git failures).

### Docs
- `docs/clients/claude-code.md`: a "Files and network (014)" section: build, config, save the
  printed settings file, the login service (systemd and launchd), the `claude` function with
  its health-check fallback, and removing old hooks from `~/.claude/settings.json`.
- `docs/manual-checks/014-effects.md` and `docs/manual-checks/seed-effects.mjs`: the seed
  posts Claude Code-shaped hook events for a scratch git repo, makes file changes (including a
  `.env` and a 500-file burst) and connections through both proxy listeners between them.

## AC coverage
| AC | Where (package/file) | Verified by |
|---|---|---|
| AC1 | `effects/runner.go`, `hooks/sink.go` | `TestRunner_BurstRunsOneFollowUp`; manual step 4 |
| AC2 | `effects/checkpoint.go`, `store/effects.go` | `TestCheckpoint_GitRepoChanges` (first run stores nothing), `TestCheckpoint_RestartKeepsSnapshot` |
| AC3 | `effects/checkpoint.go` | `TestCheckpoint_GitRepoChanges`, `TestCheckpoint_PlainFolder` |
| AC4 | `effects/git.go`, `effects/scan.go` | `TestCheckpoint_GitIgnoredEnvSeen`, `TestCheckpoint_PlainFolder` |
| AC5 | `effects/checkpoint.go`, `store/effects.go` | `TestCheckpoint_GitRepoChanges` (copy and HEAD before) |
| AC6 | `effects/roots.go`, `clients/claudecode/effects.go` | `TestRoots_LearnedAndRejected` |
| AC7 | `effects/reads.go`, `clients/claudecode/effects.go` | `TestClaudeCode_EffectRules`; manual step 5 |
| AC8 | `netproxy/http.go` | `TestNetProxy_ConnectCountsBytes`, `TestNetProxy_PlainHTTPRecordsURL` |
| AC9 | `netproxy/socks5.go` | `TestNetProxy_Socks5Connect` |
| AC10 | `effects/runner.go` (active set), `netproxy/record.go` | manual steps 3 and 9 |
| AC11 | `core/proxy.go` | `TestUpstream_SkipsOwnProxyListener` |
| AC12 | `clients/claudecode/snippet.go`, `cmd/gateway/hooks.go` | `TestHooksPrint_SettingsDoc` |
| AC13 | `store/effects.go`, `ui/api_effects.go`, `effects/diff.go` | `TestEffects_SessionVersionUnchanged`, `TestEffectsAPI_ListAndDiff` |
| AC14 | `web/effects.js`, `timeline-page.js`, `ui/hub.go` | `effects.test.js: placesEffectsInTurns`; manual steps 4–8 |
| AC15 | `web/sessions-page.js`, `ui/api_sessions.go` | manual steps 3 and 8 |

## Test strategy
Functional tests only, one or two per part (research.md D1), plus `make verify` green at the end
so 010 and 013 keep working. Tests use real temp folders, real `git` (skipped with a log line if
git is missing) and loopback servers; no mocks of the code under test.
- Store/API: `TestEffects_SessionVersionUnchanged`, `TestEffectsAPI_ListAndDiff`.
- Files: `TestCheckpoint_GitRepoChanges` (baseline, create, modify, delete, copy, HEAD as
  before), `TestCheckpoint_PlainFolder`, `TestCheckpoint_GitIgnoredEnvSeen`,
  `TestCheckpoint_RestartKeepsSnapshot` (checkpoint, a new runner on the same store, change a
  file, the change is reported), `TestRoots_LearnedAndRejected`,
  `TestRunner_BurstRunsOneFollowUp` (a slow fake checkpoint and 50 signals → exactly 2 runs;
  the 013 lesson).
- Reads: `TestClaudeCode_EffectRules` on fixture payloads.
- Network: `TestNetProxy_ConnectCountsBytes`, `TestNetProxy_PlainHTTPRecordsURL`,
  `TestNetProxy_Socks5Connect`, `TestUpstream_SkipsOwnProxyListener`.
- Settings: `TestHooksPrint_SettingsDoc` (with and without network, sandbox on and off).
- Web: `effects.test.js: placesEffectsInTurns`.
- Manual (T012, `docs/manual-checks/014-effects.md`), in a real browser:
  1. Fresh data folder; gateway run natively with effects on; printed settings saved to the
     separate file; `claude` function installed.
  2. `node docs/manual-checks/seed-effects.mjs` prints a session id.
  3. Sessions list: the seeded session shows effect counts; the unassigned link lists the
     seed's unlinked connection.
  4. Seeded session: one folded row per turn with counts; rows fill live while the seed runs,
     with no reload; the 500-file burst row expands and the page stays responsive.
  5. A read effect's content link shows what the tool returned.
  6. Connections show host:port, bytes, duration and outcome as glyph + word; plain HTTP shows
     method and URL without the query string.
  7. A modified file's diff is right; the `.env` change is listed with its diff.
  8. Every effects list has the coverage line; no status is colour-only.
  9. Real Claude Code session through the `claude` function on Linux: edit a file, run a script
     writing three files, `curl https://example.com`,
     `git ls-remote https://github.com/brutally-Honest/llm-gateway`, a Node `fetch` script;
     sandbox on, then off (`effects.network.sandbox: false`, reprint); two sessions at once
     leave connections unassigned.
  10. Gateway stopped: `claude` prints the fallback line and plain Claude works; restart.
  11. Steps 9 and 10 on the work Mac.

## Risks
- **`git status` on a huge repo is slow** → checkpoints lag but never block Claude (one running,
  one queued); `git_timeout` bounds each call; `effects.files.enabled: false` turns files off.
- **Clash with Claude's own git** → `--no-optional-locks` on every git call; no writing command.
- **A proxy bug breaks Claude's network** → `effects.network.enabled: false`, reprint the
  settings file; the `claude` function falls back to plain Claude only when the gateway is down.
- **Sandbox breaks some Bash commands** (writes outside the project, tools that need local
  sockets) → `effects.network.sandbox: false`, reprint.
- **Memory** → only active sessions are cached (about 150 bytes per file in scan mode), evicted
  after 1 h without a hook and reloaded from SQLite when needed.
- **Snapshot writes** → a session's first checkpoint in a folder without git writes one row per
  file (about 0.1–0.3 s for 10,000 files, measured in the manual check); other captures queue
  behind it for that long but never block Claude. Later checkpoints write only changed rows.
- **Snapshot disk use** → rows are deleted at session end and by the hourly sweep after
  `snapshot_retention`.
- **Secrets in copies** → accepted, ADR 0005 (research.md known issues).
- **Minimal tests** → live-update regressions may only surface in the manual check (D1).

## Open questions

## Changelog
- v1 (2026-11-08) — first draft, with Karthik's answers to K1–K10 (research.md)
