---
feature: 014-file-and-network-effects
---
# Research & decisions — 014-file-and-network-effects

## Open questions
<!-- Agents add a question here when they are blocked; the loop stops until you answer.
     Answer by changing "(open)" to "(answered)" and writing the answer underneath:
- **Q1** (open) T003 — <question> — options: A) … B) …
-->

## Decisions
- D1 (2026-11-08) — Built outside the agent-loop kit: one branch, Option B order (foundation,
  then files, then network), everything checked together at the end; functional tests only,
  one or two per part; `make verify` green after every task; leaks patched as they show up — the
  user's choice, for speed and to avoid test overfitting.
- D2 (2026-11-08) — Linux and macOS from one pure-Go binary run on the host; Docker is not used
  for Phase C — a container sees host folders only through mounts set up in advance, cannot see
  host programs, and Docker Desktop on macOS has a history of dropping file events on mounts.
- D3 (2026-11-08) — File changes come from checkpoints on hook events (git, or a folder walk
  without git), not inotify/FSEvents, timed scans or Docker mounts — one code path, no cgo, no
  cost while Claude is idle; v1 only needs "file changed".
- D4 (2026-11-08) — Reads: the sensor reports changes only; Claude's own Read, Grep and Glob
  calls are shown as read effects taken from the stored hooks — OS read events are Linux-only
  and very noisy, and macOS needs Endpoint Security (admin rights and an Apple entitlement).
- D5 (2026-11-08) — Watched folders: the session's starting folder, `additionalDirectories` from
  Claude Code's settings, folders learned from Claude's write and read paths, and `extra_dirs`;
  never `/tmp`, the home folder or `/` — hook events carry `cwd` but not `--add-dir` folders.
- D6 (2026-11-08) — Fixed ignore list (`.git`, `node_modules`, `dist`, `build`, `target`,
  `.venv`, `venv`, `__pycache__`, `.next`, `.cache`); `.env` is never ignored, and in git mode
  git-ignored files are checked while git-ignored folders are skipped — the user wants to see
  what Claude does to `.env` files.
- D7 (2026-11-08) — Git by default; fallback is facts plus copies of text files up to 256 KB;
  the first change of a file shows its full content when no earlier version exists; copies
  stored as-is (ADR 0005).
- D8 (2026-11-08) — v1 says only "file changed", placed in turns by time; no "who did it"
  labels or confidence levels — deferred by the user.
- D9 (2026-11-08) — Two sessions at work in the same folder: not handled in v1; accepted limit.
- D10 (2026-11-08) — Network: Claude Code's settings `env` block (N1) plus Claude Code's sandbox
  pointed at the gateway's proxy (N2); sandbox on by default, `effects.network.sandbox: false`
  turns it off — no shell exports and no extra commands, same on both OSes, and the sandbox
  forces every Bash command's traffic through the proxy.
- D11 (2026-11-08) — HTTPS gives host, port, bytes, timing and outcome; no decryption and no
  "which program" lookup — under the sandbox every connection arrives through Claude Code's
  relay, so the lookup would only ever name Claude Code.
- D12 (2026-11-08) — Gateway settings live in a separate file that a `claude` shell function
  loads with `--settings` after a `/healthz` check, falling back to plain Claude when the
  gateway is down; the gateway runs as a login service (systemd, launchd); both ship as docs
  — a stopped gateway must not break Claude.
- D13 (2026-11-08) — UI: one folded row per turn with counts, details on expand, diffs and
  content loaded on demand; effects have their own version and live event so they never
  rebuild the session timeline — the 013 lesson.
- D14 (2026-11-08) — Replaces K1's recommendation: each session's file snapshot is saved in
  SQLite (`effect_snapshots`) in the same transaction as the checkpoint's effects, and memory
  only caches active sessions — a gateway restart, crash or rebuild during development then
  loses nothing, and the process stays stateless as AGENTS.md asks; chosen by Karthik after
  weighing the cost (a few rows per checkpoint, one row per file on the first checkpoint in a
  folder without git).
- D15 (2026-11-08) — Amends D6 and K3: `.env` is left out of the default ignore list, with a
  comment in the config example saying so; nothing stops the user adding it later — the
  ignore list is ordinary config, not a hard rule.
- D16 (2026-11-08) — Replaces K8's 24-hour drop: the memory cache is evicted after 1 h without
  a hook and reloaded from SQLite when needed; a session's saved snapshot is deleted at session
  end or after `effects.files.snapshot_retention` (default 7 days) without a hook — follows
  from D14.
- D17 (2026-11-09) — Replaces the "sandbox on by default" part of D10: the sandbox is off by
  default (`effects.network.sandbox: false`) and optional. On Linux, Claude Code's sandbox
  needs both bubblewrap and socat and quietly runs commands unsandboxed if either is missing;
  on Ubuntu 24.04 and later it currently fails every Bash command with "apply-seccomp: write
  /proc/self/setgroups (nested userns is capability-restricted)", a known Claude Code bug
  (github.com/anthropics/claude-code/issues/43454). An untested workaround is
  `sandbox.network.allowAllUnixSockets: true`. The rest of D10 (the settings `env` block)
  stands; with the sandbox off, KI8 applies.

## Decisions for Karthik
Choices made while writing the spec that touch a repo rule or were not asked about. Each has
a recommendation; say if you want one changed.
- K1 — Checkpoint snapshots and the active-session set live in memory. AGENTS.md says
  "Stateless process". Recommended: accept for v1; a restart only re-baselines (E10), and
  persisting snapshots would add a second table and a write per checkpoint.
- K2 — Plain-HTTP URLs are stored without their query string, because tokens often ride there
  and AGENTS.md keeps credentials out of storage. Bodies are not captured at all.
- K3 — `.env` in `effects.files.ignore` is a config error, so the list can never hide it.
- K4 — A read effect stores no copy; its content is read out of the stored hook payload on
  demand, so reads cost no extra storage.
- K5 — Unassigned connections get a small list page (`#/effects/unassigned`), not only a count.
- K6 — `gateway hooks print` prints more (CLI change) and one new table is added (storage
  change). Approving this plan approves both, so the build does not stop for them.
- K7 — ADR 0014 records the new `effect` kind and the two sensors, as ADR 0012 did for
  `harness_event`.
- K8 — A session's in-memory state is dropped at session end or after 24 hours without a hook.
- K9 — The SOCKS5 listener is on by default (127.0.0.1:7195) so the sandbox can send non-HTTP
  Bash traffic (ssh, database clients) through the gateway too.
- K10 — The sandbox's escape hatch (Claude rerunning a command outside the sandbox) stays on.
  Claude Code has a setting that turns it off; that would close the gap in KI9 but make more
  commands fail. Not added to the printed settings in v1.

Build decisions (2026-11-08, made during the build outside the kit; say if you want one changed):
- B1 (T004) — `unassigned_connections` on `GET /api/sessions` is left out when it is 0, so the
  existing exact-body test of an empty list (`TestAPI_EmptyStoreEmptyLists`) still holds; the
  browser reads absent as 0.
- B2 (T005) — The coverage line says "the agent's hook checkpoints, plus the agent's own reads"
  instead of naming Claude: the purity test bans client names anywhere under `internal/ui/`.
- B3 (T005) — The "Effect" section (`effectSection` in `drawer-sections.js`) opens inline under
  the effect's row, not in the call drawer: the drawer is routed per model call
  (`#/sessions/{id}/call/{exchange}`), and an effect belongs to no call (spec non-goal). It
  still fetches `/diff` or `/content` only when opened.
- B4 (T005) — `api.test.js` pinned the stream's event list; it now includes `effects`, the
  new event AC14 needs. No assertion was removed.
- B5 (T007) — `hooks.HookStored` now holds the mapped `HarnessEvent`, which has a slice, so
  `TestSink_OnStoredCalledAfterSave` compares the identity fields one by one instead of the
  whole struct, and also checks the event's session key. It also gains `At` (the receive
  time) so a read effect is timed like its hook.
- B6 (T007) — `startCapture` keeps its signature (a test calls it); `startCaptureWith` adds
  the effects config and `run` calls that.
- B7 (T009) — The run tests' harness treats the default proxy addresses (7198, 7195) as it
  already treated the default UI address: never bound for real and left out of
  `listened()`. Without it, every run test with the default config bound real fixed ports
  and parallel tests collided; the tests' assertions are unchanged.
- B8 (T009) — The proxy listeners are started only when capture is on: network effects need
  the store. With capture off, `effects.network` binds nothing.
- B9 (T011) — The 010 `hooks print` tests describe the hooks-only document, which AC12 now
  prints with network effects off. Their helper sets `GATEWAY_EFFECTS_NETWORK_ENABLED=false`
  unless a test sets it; every 010 assertion is unchanged. `ANTHROPIC_BASE_URL` is
  `listen_addr` plus the Anthropic adapter's prefix (`/anthropic`).
- B10 (T012) — Found while writing the seed: a hooks-only session's turns (built from prompt
  hooks) had no start time, so every effect fell into "before the first prompt". `hookTurns`
  now gives each prompt turn its hook's time. Committed on its own as a fix.
- B11 (T012) — `effects_stored` in the shutdown line counts checkpoint and network effects;
  read effects are stored one by one on the hook worker and are not counted there.

Karthik's answers to the build decisions (2026-11-08):
- B8 → approved as is: no proxy listeners while capture is off.
- B9 → approved: `TestHooksPrint_SettingsDoc` already runs `hooks print` with
  `GATEWAY_EFFECTS_NETWORK_ENABLED=true` and checks the printed env block, the sandbox block
  with both ports, the output with the sandbox off, and the output with SOCKS5 off; no new
  test needed.

Karthik's answers (2026-11-08):
- K1 → changed: snapshots saved in SQLite with batched, per-checkpoint transactions (D14).
- K2 → accepted; revisit if it breaks something.
- K3 → changed: no hard rule, `.env` just left out of the default list with a comment (D15).
- K4, K5, K7, K10 → accepted.
- K6 → accepted, including the new tables.
- K8 → changed with K1 (D16).
- K9 → accepted: SOCKS5 stays on; Karthik may turn it off if it causes performance problems.

## Notes

### Known issues accepted for v1 (2026-11-08)
- KI1 — Only the result at each checkpoint is seen, not every write; a file created and deleted
  between two checkpoints never shows.
- KI2 — Reads outside Claude's Read, Grep and Glob tools (`cat`, `grep`, scripts) are not shown.
- KI3 — A `--add-dir` folder (flag or `/add-dir`) that Claude touches only through Bash is not
  watched; `/tmp` and the home folder are never watched.
- KI4 — In git mode a file's first diff is against the last commit, so it can include edits
  made before the session.
- KI5 — Copies of `.env` and other files hold secrets, stored as-is (ADR 0005).
- KI6 — No "who did it": an edit you make in your editor between prompts shows in the next
  turn; changes by background commands (dev servers, watchers, anything Claude starts without
  waiting for it) show at whichever checkpoint sees them.
- KI7 — Two sessions in one folder: a change can show in both; with two or more sessions
  active, connections are unassigned.
- KI8 — With the sandbox off, programs that ignore `HTTPS_PROXY` (Node's built-in fetch by
  default, ssh, database clients) are invisible; MCP servers are seen only if they use the
  proxy setting.
- KI9 — With the sandbox on, a command Claude reruns outside the sandbox escapes the proxy
  (K10); some commands may fail inside the sandbox (off switch: `effects.network.sandbox`).
- KI10 — HTTPS shows sites only: no paths, no bodies, no program names.
- KI11 — Claude Code started from an IDE extension or the Claude app does not run the `claude`
  shell function, so those sessions are not captured (no API capture, hooks, file or network
  effects). The user runs Claude Code from the terminal and sometimes from the Claude app.
- KI12 — If the gateway dies mid-session, that session's model and network calls fail until
  the login service restarts it (seconds).
- KI13 — A gateway restart re-baselines; changes made while it was down are not reported.
  (Replaced by KI17.)
- KI14 — Phase C needs the gateway on the host, not in Docker.
- KI15 — No store cleanup; copies grow with use, and a small file rewritten often (a log)
  gets a copy at every checkpoint.
- KI16 — Minimal tests: regressions in live-update paths may only show in the manual check.
- KI17 (2026-11-08, replaces KI13 after D14) — After a gateway restart, changes made while it
  was down are reported at the next checkpoint, placed in the turn where they are found rather
  than when they happened; that includes the user's own edits during the downtime. A session
  resumed after `snapshot_retention` (7 days) starts a new baseline.
- KI18 (2026-11-08) — A session's first checkpoint in a folder without git writes one snapshot
  row per file (about 0.1–0.3 s for 10,000 files); other captures queue behind it for that long.

### To confirm in the manual check (T012)
- V1 — `claude --settings <file>` applies that file's `env` and `sandbox` blocks, not only its
  hooks (010 confirmed hooks).
- V2 — Values from the settings `env` block reach Bash child processes.
- V3 — With the sandbox on and `sandbox.network.httpProxyPort` / `socksProxyPort` set, Bash
  traffic arrives at the gateway's listeners; with the sandbox off the ports do nothing (a
  third-party test saw a custom proxy unused while the sandbox was off).
- V4 — Whether Claude Code still asks about new domains when a custom proxy is set, or leaves
  filtering to the proxy.
- V5 — `git status` time per checkpoint on a real repo, and how far checkpoints lag in a busy
  turn.
- V6 — The PostToolUse payload of Read carries the file content in `tool_response`.
- V7 — Hook `cwd` stays the starting folder after Claude runs `cd` in Bash.

Answers (2026-11-09, Linux, step 9, sandbox off):
- V1 (2026-11-09) — Pass for `env`: 11 model calls went through `ANTHROPIC_BASE_URL` from the
  `--settings` file (plus the 429 quota probe before the first prompt, below), and the proxy
  was used. The sandbox part is untested on Linux (D17) and moves to the Mac (step 11).
- V2 (2026-11-09) — Pass: with the sandbox off, `curl` in Bash went through the proxy
  ("200 Connection Established" and an `example.com` row).
- V3 (2026-11-09) — Not answerable on Linux: the sandbox could not run (D17). Test on the Mac.
- V4 (2026-11-09) — Not answerable on Linux, same reason. Test on the Mac.
- V5 (2026-11-09) — Checkpoints are fast and do not lag. The checkpoint's two git calls
  (`--no-optional-locks status --porcelain=v2 -z --untracked-files=all`, then `ls-files -z -o
  -i --exclude-standard --directory`), timed by hand: ~/fx014-step9 (1 tracked file) status
  5–7 ms, ls-files 5–8 ms; this repo (467 tracked files) status 8–10 ms, ls-files 6–8 ms, so
  about 11–18 ms of git per checkpoint. Lag, from the stored data of session 779fbcb7: a file
  effect's `at` is the checkpoint's start time, and every one of the 6 file effects started
  0–1 ms after the PostToolUse hook that triggered it (e.g. `a.txt`, `b.txt`, `c.txt` at
  01:09:22.848 against the hook at 01:09:22.847 UTC). The PostToolBatch that follows each
  PostToolUse arrived 14–17 ms later (157 ms after the Write) and found nothing new, so no
  change was split across checkpoints. Not measured: how long the save takes, and a busier turn than step 9.
- V6 (2026-11-09) — Pass: the Read PostToolUse payload carries the file in
  `tool_response.file.content` ("hello world\n" for `notes.txt`, hook 26). The read effect
  (effect 24, `hook_id` 26) stores no copy (`after_hash` null), and
  `GET /api/effects/24/content` on the step-9 data returned
  `{"path":"/home/zoro/fx014-step9/notes.txt","content":"hello world\n","utf8":true}`.
- V7 (2026-11-09) — Pass: for the Bash call `cd /tmp && pwd` (stdout `/tmp`), the hook `cwd`
  was `/home/zoro/fx014-step9` on its PreToolUse (hook 27), its PostToolUse (28), and on the
  PostToolBatch (29) and Stop (30) after it.

### Manual check results
- 2026-11-08 — 014-effects.md steps 3–8 at f48bd50 (Linux, Chrome, fresh data folder, seed
  with `--pause 5000`): steps 3, 5, 6, 7 and 8 pass; step 4 fails. The counts, the live
  fill and the 501-row turn are right, but every live effects update replaced every turn
  section of the timeline: `setEffects` redraws the whole list, so the turns above are
  rebuilt (rebuilds seen at each update, 2–4 sections removed each time), against AC14's
  "no timeline rebuild". A second seed run in the same gateway put its turn 2 connections
  under unassigned: the first run's session still counted as active (AC10 working), so
  the seed only gives the documented counts once per data folder.
- 2026-11-08 — Step 4 re-check at f61cc46 (fresh data folder, one seed run with
  `--pause 5000`, turn sections tagged at page load): effects updates pass — at 17.23 s and
  22.26 s only the changed turn's effects row redrew and no turn was replaced. New hook
  events still fail: at 17.03 s (turn 2 arrives), 22.05 s (turn 3 arrives) and 27.08 s (the
  final Stop and SessionEnd) every turn section was replaced although its effects had not
  changed, because `render()` rebuilds the whole list. An expanded effects row stayed open
  across the rebuild, as a new element.
- 2026-11-09 — Step 4 re-check at d044c9d passed (run 2026-11-08): turns 1 and 2 were never
  replaced, and turn 3 was replaced only when its own hooks changed.
- 2026-11-09 — Steps 1–2 (setup): done.
- 2026-11-09 — Step 9 on Linux with a real Claude session, sandbox off. Files: `notes.txt`
  modified with a correct diff; `make3.sh`, `a.txt`, `b.txt`, `c.txt` and `fetch.mjs` created;
  one read of `notes.txt`. Connections: `example.com` and `github.com`, plus Claude Code's own
  `downloads.claude.ai` and `http-intake.logs.us5.datadoghq.com`. Node's `fetch` was not seen
  with the sandbox off, as expected (KI8).
- 2026-11-09 — Sandbox on Linux: the first try ran unsandboxed because socat was missing.
  After installing socat, every Bash command failed with the apply-seccomp error (D17). An
  AppArmor profile for bwrap was tried and then removed.
- 2026-11-09 — Step 10 passed: with the gateway stopped, the `claude` function printed
  "llm-gateway is not running: starting plain Claude Code" and plain Claude worked.
- 2026-11-09 — Step 11 (Mac): still to do.
- 2026-11-09 — The `claude` function was first added only to `~/.bashrc` while the shell was
  zsh, so the first real run wasn't captured. Fixed by adding it to `~/.zshrc`;
  docs/clients/claude-code.md §6 now says to use the startup file of the shell in use.
- 2026-11-09 — Follow-up idea, not for now: show the effect counts in the turn header and the
  left rail, because the folded row at the bottom of a turn is easy to miss.
- 2026-11-09 — The error on call #1 under "Before first prompt" in the step-9 sessions is a
  **429** from Anthropic itself on Claude Code's **quota probe**: a non-streaming
  `POST /v1/messages` with `max_tokens: 1` and one user message `"quota"`, sent at start-up
  before the first prompt. The body is `{"type":"error","error":{"type":"rate_limit_error",
  "message":"Error"}}` with a real Anthropic `request_id`, Cloudflare and `Anthropic-*`
  response headers and `X-Should-Retry: true`; the gateway passed it through verbatim (no
  `gateway_error`). All four sessions in /tmp/fx014-data start with it (779fbcb7, b1d5372d,
  f0ca71eb, 1d34c01d). **It predates 014 and is not caused by it:** the pre-014 store
  (~/.local/share/llm-gateway, 2026-10-12 to 2026-11-02, claude-cli 2.1.314 and 2.1.316)
  holds 11 quota probes from 2026-10-31 on, and every one is a 429; 014 does not touch the
  model-call path. Nothing to fix in the gateway.

### Sources (2026-11-08)
- Claude Code honours `HTTPS_PROXY`, `HTTP_PROXY` and `NO_PROXY`, does not support SOCKS
  itself, and every env var on that page can be set in settings.json:
  https://code.claude.com/docs/en/corporate-proxy
- Sandbox: Seatbelt on macOS, bubblewrap on Linux; network goes through a proxy outside the
  sandbox and covers all subprocesses of Bash commands; applies to Bash only:
  https://code.claude.com/docs/en/sandboxing.md
- `sandbox.network.httpProxyPort` / `socksProxyPort` (bring your own proxy):
  https://brewpirate.github.io/claude-code-docs/settings/network-proxy/ and
  https://zenn.dev/tomioka/articles/120252fdaedec3?locale=en
- Docker Desktop for Mac file events on bind mounts: https://github.com/docker/for-mac/issues/681
