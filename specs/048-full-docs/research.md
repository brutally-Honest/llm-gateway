# 048 research

## Progress
- [x] 2026-11-26 branch chore/048-full-docs from up-to-date main; spec and research created
- [x] 2026-11-26 group A: docs/what-gets-recorded.md (D4-D14)
- [x] 2026-11-26 group B: docs/architecture.md, docs/ui.md (D15-D20)
- [x] 2026-11-26 group C: README.md, docs/getting-started.md (D21-D24)
- [ ] group D: AGENTS.md / PLAN.md rules, stale-docs sweep, links
- [ ] review round 1
- [ ] fixes for round 1 P1-P3
- [ ] review round 2 (only if round 1 found P1-P3)
- [ ] spec marked done, PR opened, CI watched

## Decisions
- **D1** 2026-11-26 Branch is `chore/048-full-docs`, not the bare `048-full-docs` the request
  named: AGENTS.md requires a feat/fix/chore prefix and 033 (also docs) used `chore/`. The
  slug is unchanged. Needs the owner's confirmation.
- **D2** 2026-11-26 Commit scopes, all from PLAN §11: `docs(spec)` for files under `specs/`,
  `docs(docs)` for `docs/` and README, `docs(repo)` for AGENTS.md and PLAN.md (process docs),
  `docs(adr)` for `docs/decisions/`, `docs(install)` for comments in `install.sh`.
- **D3** 2026-11-26 All doc writing is done by one sonnet subagent per group, one at a time;
  each group ends with `make verify && git commit` run under
  `systemd-run --user --scope -p MemoryMax=4G`. Review is a fresh opus subagent.
- **D4** 2026-11-26 Group A wording on bypasses (desktop app, IDE extensions): the code only
  proves the installer adds a shell function to one rc file; nothing configures those
  clients. The doc says so and says it was not tested per client version, rather than
  promising they are never captured.
- **D5** 2026-11-26 Node floor stated as "22.21 or later in the 22 line, or Node 24 or later",
  from `NodeNote`. The old claim "23 ignores it" (docs/install.md) is not in code, so the
  new doc does not repeat it. No code checks the Node version; the only place is the note
  `gateway install` prints, and the doc says so.
- **D6** 2026-11-26 AppArmor section kept to the brief: known issue, investigated after v1,
  no fix claimed. It links to docs/install.md#check-apparmor and the sandbox section of
  claude-code.md for the repo's existing wording, and does not copy the profile or the
  "untested workaround" text. The doc names `allowAllUnixSockets` only as the relevant
  setting.
- **D7** 2026-11-26 "Needs a look" lists the six kinds from `internal/timeline/view.go`
  (not the older docs' wording) and the cap of 8 shown (`maxLook`).
- **D8** 2026-11-26 Network attribution: code says a connection is linked only when exactly
  one session has a hook event inside `effects.network.active_window` (default 10m) and no
  session end; zero such sessions is unassigned too. Older docs say "exactly one routed
  session"; the doc follows the code and does not use "routed".
- **D9** 2026-11-26 Disk growth: "no retention" is qualified. Saved file baselines
  (`effect_snapshots`) are deleted at session end or after 7 days
  (`effects.files.snapshot_retention`); the capture store itself is never pruned. Also
  mentioned the disposable `view-cache` folder under capture.dir. No numbers from real
  data; only "very roughly hundreds of MB a day for heavy use" as the brief allowed.
- **D10** 2026-11-26 Dedup wording: parsed content pieces are hashed and stored once, raw
  bodies are hashed whole (`bodyContent`), so a body grown by one message is a new blob.
  Both facts are in code; the doc states both.
- **D11** 2026-11-26 Privacy: said "no telemetry" based on finding no outbound call in
  non-test code except proxying and the optional OTLP exporter; added that the OTLP
  endpoint accepts https anywhere (`internal/config/tracing.go`), so "points at a local
  Jaeger" is only the default.
- **D12** 2026-11-26 Redaction list: "headers such as" Authorization, Proxy-Authorization,
  Cookie, Set-Cookie, X-Gateway-Key (core) plus x-api-key (Anthropic adapter).
- **D13** 2026-11-26 Several accounts: the doc says the row records the auth kind (api key or
  subscription) but no account id; left out any claim about how Claude Code names accounts.
- **D14** 2026-11-26 Left out: ports, hooks install detail, the full effects scope rules
  (extra_dirs, ignored folders). They belong to claude-code.md and architecture.md (group B).
- **D15** 2026-11-26 Ports: the brief's list is right but incomplete. `/metrics` is also on the UI
  listener (7199), and `/api/health` is UI-only; the hooks route is `/_gateway/hooks/{client}` on
  7197. architecture.md says so. The proxy listeners start only with capture, `effects.enabled`
  and `effects.network.enabled`; the UI only with capture and `ui.enabled`.
- **D16** 2026-11-26 Shutdown order in architecture.md follows `captureRun.close`: hub closed and UI
  work cancelled first, hook sink, effects, capture queue drain (budget of its own, one
  `shutdown_timeout`), then the 5 s UI wait (`uiWorkTimeout`), then reader and store. The 2 s UI
  server stop (`uiStopTimeout`) is also named, since it happens before the drain.
- **D17** 2026-11-26 View-cache key: the build folder name hashes format, version, executable hash,
  attribution skew, home folder and time zone, so "keyed on the binary" is true but incomplete;
  the page says so (a new binary gives a new folder, empty after every install).
- **D18** 2026-11-26 ui.md is the one home for UI description. Replaced one passage in
  docs/install.md (the list filling in, under "The UI's view cache") with a pointer. Left alone on
  purpose: docs/roadmap.md (a history of specs, not a description), the chip table in
  claude-code-hooks.md (owned by the hooks doc, which ui.md links), README.md (group C).
- **D19** 2026-11-26 docs/observability.md already matched the code on health (200 on degraded,
  banner text, per-tab dismissal, restart or higher count brings it back). No sentence changed.
- **D20** 2026-11-26 Session-page recovery: the code has no Retry button on a failed session view; it
  shows the message and "Turns not loaded.", and a 504 gets one automatic retry after 1 s with
  "building session…". ui.md says "reload the page" and offers Retry only where the code has one
  (sessions list, the "Details are not available yet" line). Search matches client, session key,
  id, folder, title and models (`searchVerdict`), so ui.md lists those.

- **D21** 2026-11-26 README rewritten to the spec's section order; the old project log, the "Known limits" block, the Setup and Web UI sections are gone (limits live in install.md and what-gets-recorded.md, UI in ui.md). One pointer to roadmap.md was dropped from the README body and kept in the Docs index line for the roadmap.
- **D22** 2026-11-26 No LICENSE file exists in the repo (ls LICENSE*: no match), so the README has no license line. docs/licenses/ holds third-party notices and is not linked as a license.
- **D23** 2026-11-26 The README says the sandbox is needed for network effects only by implication ("through its proxy"); the exact conditions stay in what-gets-recorded.md. Keeps one home per rule.
- **D24** 2026-11-26 getting-started.md describes the PATH case from code: install.sh and `gateway install` copy the binary to ~/.local/bin/gateway and add it to PATH only on macOS when missing (shell.go:20-22); on Linux the doc gives the install.md fix. Prompts described: `Run it now?` (preflight.go:467) and `Proceed?` (install.go:128), shown as `[y/N]` and `[Y/n]` per install.md.

## Notes

### Group A evidence
Claims in docs/what-gets-recorded.md.
- Calls grouped into sessions by client session id; none still stored — internal/core/registry.go:109-114, internal/core/effect.go:13-16, docs/clients/claude-code-hooks.md:117 (ungrouped list)
- Turn is one prompt and what followed — internal/timeline/view.go:257
- Hooks give ran/failed/denied states — NOT VERIFIABLE IN CODE as Claude Code behaviour: worded as what Claude Code reports, linked to claude-code-hooks.md
- Reads by Read, Grep, Glob only — internal/clients/claudecode/effects.go:31, internal/effects/reads.go:23
- Network via gateway proxy, HTTPS not decrypted — internal/netproxy/record.go (records host/port/bytes), docs/clients/claude-code.md:313 (decryption: tunnel only; not re-read in code, taken from doc)
- Needs a look: six kinds, cap 8 — internal/timeline/view.go:196-203, internal/timeline/view_overview.go:12-13
- Sessions filter "Needs a look" — internal/ui/web/sessions-page.js:276
- Cost from token counts and shipped price table, API-equivalent — internal/cost/cost.go:1-5, internal/protocols/anthropic/cost.go:13 (go:embed prices.csv)
- Unknown model or variant is no_price, not an error — internal/cost/cost.go:72-75, 124-139
- Works without an API key — NOT VERIFIABLE IN CODE as a single path: cost reads captured usage only (internal/cost/cost.go:Price), no key is used; subscription mode is described in docs/clients/claude-code.md:33
- Health counts and degraded — internal/observability/health.go:14-82
- /healthz always 200 with the JSON — internal/server/server.go:129-138; /api/health — internal/ui/ui.go:131; banner — internal/ui/web/health.js:8-12; doctor check — internal/install/doctor.go:340; metrics — docs/observability.md:12-28
- Shell function, health-gated, `command claude --settings` — internal/clients/claudecode/install.go:96-108
- rc files (.zshrc/$ZDOTDIR, .bashrc, .bash_profile on darwin; others manual) — internal/install/preflight.go:355-387
- Interactive-only rc files — NOT VERIFIABLE IN CODE (shell behaviour): worded as "files of interactive shells"
- Desktop app / IDE extensions not captured — NOT VERIFIABLE IN CODE: worded as "installer adds only the shell function; not tested per client" (docs/clients/claude-code.md:293 says the same)
- Fast-mode, WebFetch, connectors limits — NOT VERIFIABLE IN CODE: linked to PLAN.md §3 line 76
- Principal always local — internal/core/principal.go:5-20, cmd/gateway/run.go:994, internal/hooks/sink.go:239; auth kind recorded, no account field — internal/core/meta.go:25
- Network linking only with exactly one active session — internal/effects/runner.go:392-407, internal/netproxy/record.go:34-36; window default 10m — internal/config/effects.go:121; no session end — internal/store/effects.go:186-200
- Unassigned listed on Sessions page — internal/ui/web/app.js:195-199, internal/ui/web/sessions-page.js:197-204
- Node floor 22.21 / 24, NODE_USE_ENV_PROXY=1, older ignores — internal/clients/claudecode/snippet.go:63, 76-80; note printed by install — internal/install/install.go:689-690, cmd/gateway/install.go:203; no version check in code — grep for node version in internal/ and cmd/ found none
- Old Node behaviour (calls unseen / ENOTFOUND) — NOT VERIFIABLE IN CODE beyond the NodeNote text (snippet.go:78-80): worded as what the note says
- AppArmor restricts userns, may turn bwrap sandbox off — internal/install/preflight.go:325-348 (doctor warning text); `allowAllUnixSockets` named only — docs/clients/claude-code.md:192 (not in code); "under investigation after v1" — spec 048 non-goals; no fix claimed
- Sandbox option off by default — internal/config/effects.go (Sandbox default) and config.example.yaml:189
- Raw secrets stored — docs/decisions/0005 (decision); capture stores bodies as sent — internal/store/exchange.go:100-110
- Redacted headers — internal/core/redact.go:15-17, internal/protocols/anthropic/adapter.go:48
- No telemetry — NOT VERIFIABLE IN CODE as a negative; grep of non-test code in internal/ and cmd/ for http.Get/Post/NewRequest/DefaultClient found no outbound call besides proxying; OTLP exporter off by default — internal/config/tracing.go:5-28, config.example.yaml:213-217
- Span content (no content/headers) — config.example.yaml:206-208 (comment); not re-proved in internal/observability/tracing.go
- Loopback-only listeners and refusal — internal/config/listen.go:32-52, internal/config/listen.go:16-30 (listener set)
- Perms 0700/0600 for capture dir and files — internal/store/store.go:85-100, internal/store/content.go:133-140, CreateTemp 0600 (content.go:90); config folder/file — internal/install/install.go:607-623
- No retention; snapshots cleaned at session end or 7 days — grep for DELETE/prune in internal/ found only effect_snapshots deletes (internal/store/effects.go:62,144,156); internal/config/effects.go:114; config.example.yaml:164-167
- Blob: sha256, zstd, over 4 KiB — internal/store/content.go:15-33, 82-107
- Raw bodies hashed whole; parsed pieces deduped by hash — internal/store/exchange.go:100-110, internal/core/canonical.go:296-310, docs/capture.md:62-70
- Claude Code resends the whole conversation on every call — NOT VERIFIABLE IN CODE as client behaviour: worded as fact about calls the gateway sees; observed in capture design notes (docs/capture.md:70 "a message resent on every turn")
- Hundreds of MB/day heavy use — NOT VERIFIABLE IN CODE: worded "very roughly", varies; figure supplied by the brief
- capture.dir default — internal/config/capture.go:40-56
- view-cache under capture.dir, disposable, trims itself — internal/ui/disk_cache.go:30-60

Stale or doubtful statements in OTHER docs (not fixed; group D):
1. docs/install.md:328 — "Older Node (22.20 and before, 23, 20 and before)": code (snippet.go:78) only says 22.21+ or 24+ are seen; the 23 claim is not in code.
2. docs/clients/claude-code.md:182 — "Node older than 22.21 or 24": ambiguous; code says 22.21+ or 24+.
3. docs/clients/claude-code.md:310-311 and docs/install.md:321-322 — "exactly one routed session": code (internal/effects/runner.go:392) counts sessions with a hook event in the active window and no session end; "routed" is not checked.
4. docs/clients/claude-code-hooks.md:10-13 — says hooks go in ~/.claude/settings.json at user scope; the installer puts them in the gateway's own settings file loaded by `--settings` (internal/clients/claudecode/snippet.go:50-70, install.go:100). Check whether the page still describes the intended path.
5. docs/install.md:47 and :195-199 vs docs/clients/claude-code.md:192-194 — install.md offers an AppArmor profile fix; claude-code.md calls `allowAllUnixSockets` an untested workaround; docs/roadmap.md:140 says to test it. Not contradictory, but the three should be kept consistent by group D (the new page claims no fix).
6. docs/capture.md:88 area and docs/clients/claude-code.md:313 — "HTTPS ... never decrypted" not re-checked here beyond internal/netproxy recording fields; reviewer to confirm.

### Group B evidence
Claims in docs/architecture.md and docs/ui.md.
- Ports 7197/7198/7195/7199 and settings — internal/config/config.go:103, internal/config/effects.go:118-119, internal/config/ui.go:25
- /healthz and /metrics on the API listener — internal/server/server.go:58-60; hooks route `/_gateway/hooks/{client}` — internal/hooks/handler.go:16; other `/_gateway/*` is 404 — internal/server/server.go:68
- Adapter prefix `/anthropic/*` — internal/protocols/anthropic/adapter.go:25, internal/core/registry.go:123-126
- UI listener: /metrics, /api/health, GET only, host and origin checks — internal/ui/ui.go:124-131, internal/ui/middleware.go:73-114
- Port 7196 kept free — docs/install.md:36 (not re-checked in code)
- Proxy listeners need effects and network enabled; UI needs capture and ui.enabled — cmd/gateway/run.go:746-749, 242-248, 712-716
- Hooks route always mounted; capture off answers and counts only — cmd/gateway/run.go:~300 comment "The hook route is always mounted"
- Rate limiter mounted only when enabled, per key, fixed window, in memory — cmd/gateway/run.go:266-281, internal/config/ratelimit.go:7-37
- ReverseProxy with Rewrite and tees — internal/core/proxy.go:55-59, internal/core/tee.go:106-166
- Submit never waits, drops and counts when full — internal/capture/sink.go:102-115; N workers over a bounded channel — internal/capture/sink.go:90-95; defaults queue 256, workers 2 — internal/config/capture.go:33-34
- Raw exchange stored first, then parse, then events — internal/capture/pipeline.go:25-45
- Commit calls the hub — cmd/gateway/run.go:661 (`cfg.OnCommit = hub.Publish`)
- SQLite WAL, pure-Go driver, one writer — internal/store/store.go:1-4, 154; blobs over 4 KiB, sha256, zstd — internal/store/content.go:18
- UI reads through a read-only reader — cmd/gateway/run.go:639, internal/store/reader.go:19-22, 74
- Hook sink has its own queue — cmd/gateway/run.go (hooks.NewSink), docs/clients/claude-code-hooks.md:123-125
- Timeline, cost, effects packages as described — package listing of internal/ (go files), internal/timeline/view.go, internal/cost/cost.go:1-5
- View cache location, `m/` and `v/`, modes 0600/0700 — internal/ui/disk_cache.go:29-60
- View cache limits 32 MiB per view, 1 GiB per build — internal/ui/disk_cache.go:54-56
- View cache build key (format, version, exe hash, skew, home, zone) — internal/ui/disk_cache.go:178-181, 236-239; entry key adds session version — internal/ui/disk_cache.go:338-340
- Store behind cache deletes the build folder; cache open failure runs from memory — internal/ui/disk_cache.go:266-295, cmd/gateway/run.go:652-656
- Debounce 150 ms — internal/ui/stream.go:275; heartbeat 15 s, 32 streams — internal/ui/hub.go:25,29; reopen 1 s growing to 30 s — internal/ui/web/api.js:78-79
- Stream routes /api/stream and /api/sessions/{id}/stream?view=1 — internal/ui/stream.go:21-22, 295-311; spec 040 incremental state — specs/040-incremental-live-updates/spec.md (not re-proved in code: only the debounce and "only changed turns" in stream.go:277-290)
- "A few cases still fall back to a full build" — NOT VERIFIABLE IN CODE here: taken from docs/roadmap.md:17-20 (040 entry)
- Shutdown order and figures — cmd/gateway/run.go:1000-1024 (close), 1026-1040 (waitUIWork), 503-510 (2 s, 2 s, 5 s, 2 s consts), 388-440 (stop sequence); shutdown_timeout default 10 s — internal/config/config.go:105; "recorded as cut by the gateway" — cmd/gateway/run.go (srv.Cancel(core.ErrShutdown))
- last-shutdown.json — docs/observability.md:26-28 (not re-checked in code)
- healthz 200 on degraded, with reason — internal/server/server.go:44-47, 129-138; shell function `curl -fsS --max-time 1 <health> && command claude --settings` — internal/clients/claudecode/install.go:98-108
- Banner text, dismissal per tab, returns on higher count or new since — internal/ui/web/health.js:8-12, 21-32, 15-second poll health.js:2; shell.js:151-166, 210-224
- Four counters — internal/observability/health.go:14-82; doctor warns — internal/install/doctor.go:340
- "[content unavailable]" per block — internal/timeline/build.go:22, 501-541
- Page title "Sessions" and "today" — internal/ui/web/sessions-page.js:~575-580 (h1 + `sp-when`, title "since local midnight")
- Tiles — sessions-page.js todayCells/drawToday (stat() calls); sparklines on sessions and model calls — same
- Columns, "Last active", status glyphs — sessions-page.js COLUMNS (line ~486-489), statusOf (line ~93)
- Pinned header — internal/ui/web/style.css:313 (`.sp-list th` sticky); webtest sticky-header.test.js
- PAGE_SIZE 50, Previous/Next, "Page N of M" — sessions-page.js (PAGE_SIZE, pagerBtn, drawPager text)
- Pending rows, "Details loading", bars, "still being checked", "Details are not available yet", Retry — sessions-page.js:7-10, 625-637, drawStatus; style.css:383-388
- Filters All / Needs a look / Active / Hooks only; box "Filter sessions"; label "Show" — sessions-page.js FILTER_LABELS, 581-587
- Search fields — internal/ui/api_sessions.go:searchVerdict; 200-byte cap — internal/ui/web/route.js:6-20; Esc clears — sessions-page.js:~688
- No-match text and Clear — sessions-page.js drawNoMatch; "No sessions captured yet." — sessions-page.js (shown.length === 0 branch)
- Ungrouped link text — sessions-page.js ungroupedText; "Unassigned connections (N)", first 10, "All N unassigned connections →" — sessions-page.js UNASSIGNED_SHOWN and unassignedSection
- Turns rail, "background task", clamp title, duration — session-page.js:43-50, 229-250
- Title rule and list of "(system)" titles — internal/clients/claudecode/rules.go:140-195, 290-347; session title skips synthetic — specs/046-ui-polish/spec.md U1 and internal/timeline/digest.go:151-169
- Overview, Time, Context, "Needs a look", "and N more", cap 8 — session-page.js:257-305; internal/timeline/view_overview.go:12-13
- Cost label "API-equivalent", partial "≥" — internal/ui/web/cost.js:6-10, 45-55; session-page.js:312-318
- "A model with no price is left out" — cost.js:46-47 (hover "no price for"), internal/cost/cost.go:72-75
- Side calls disclosure text — session-page.js:321-332
- Tabs Story/Files/Commands/Sites/Context — session-page.js:120, side-card.js:10-13
- Chips list — docs/clients/claude-code-hooks.md:78-85; tool-card.js chip rendering
- Preview/Raw, stored in the browser — story.js:238-245 (localStorage key `gw.textMode`); session-page.js:126-139
- Files: Changed/Read/Searched, By file/By turn — side-card.js:150-196
- Context tab, token counts incl. cache write — side-card.js:312-324
- Call details sections, URL changes — call-dialog.js:308, 354; internal/ui/web/route.js:60-62
- "Session not found" and link back — app.js:658, session-page.js:420-422
- Live dot, "reconnecting…", snapshot failed text — app.js:533-538; shell.js:230-240
- Stream only for active sessions — app.js:665-670; list stream — app.js:548
- "The gateway is not answering." and Retry on list — api.js:1-10, sessions-page.js:840-847; webtest api.test.js
- "building session…" and one retry after 1 s — app.js:573-584, 654
- "Turns not loaded." — session-page.js:412; "No such call in this session." — app.js:619
- "Call details unavailable:" — call-dialog.js:436
- Banner "details" link to /api/health, × button — shell.js:150-162
- Settings: Theme Light/Dark, Style Lean/Stark; keys `theme` and `style` in localStorage; defaults dark and lean; OS theme ignored; applied before paint — internal/ui/web/theme.js:1-76, boot.js:1-17, shell.js:60-105
- Stark "monospace capital labels", square corners — style.css:92-105
- "per browser and per address" — NOT VERIFIABLE IN CODE: browser localStorage is per origin; worded from that fact
- Shortcuts — internal/ui/web/keys.js:3-12, 39-69; "?" help — shell.js:177-190; arrow keys on tabs — keys.js comment (line 5-8), not re-proved in side-card.js
- "UI accepts only GET" — internal/ui/middleware.go:106-114
- Screenshot placeholder is an HTML comment only; no images added.

Stale or doubtful statements in OTHER docs (not fixed; group D):
1. docs/install.md:195-199 and 47 — AppArmor fix text vs claude-code.md:192 "untested workaround" (see group A item 5); unchanged.
2. docs/clients/claude-code.md:116-119 — shutdown paragraph says "Captures then finish storing, so a stop with nothing in flight takes well under a second"; code (run.go:1000-1040) also waits for the UI's background work up to 5 s, which is usually instant. Not wrong, but incomplete next to architecture.md.
3. docs/roadmap.md:28 — 029 and 027 entries describe UI details (50 rows, "whole-row link", "newest first", day tiles) that ui.md now owns; left as history. Check "a session is one page scroll of turn cards, newest first" (027): the session page now has a Turns rail and tabs (session-page.js:8-10 comment), so the entry no longer describes the page.
4. docs/clients/claude-code.md:315-317 — "linked to the one session active in the last 10 minutes": same wording issue as group A item 3 (code: a hook event inside the window and no session end).
5. docs/clients/claude-code-hooks.md:118-122 — "its ungrouped list has no hook entries" and "known v1 gap": not re-checked against ungrouped-list.js.
6. docs/install.md:268 (fixed in this group) described rows as showing "start time"; the first column is "Last active".

### Group C evidence
- one-command install line and URL https://raw.githubusercontent.com/brutally-Honest/llm-gateway/main/install.sh — install.sh:5, docs/install.md:6
- flags `--check` and `--dry-run` through `sh -s --` — install.sh:6-7, cmd/gateway/install.go:119-120 (flag defs), install.go:35-37 (help)
- installer shows preflight table, asks `Run it now?`, shows plan, asks `Proceed?` — internal/install/preflight.go:467, internal/install/install.go:128, docs/install.md:61-66
- `--yes` skips Proceed; `--install-deps`, `--no-shell`, `--no-service` exist — cmd/gateway/install.go:111-118
- binary installed to ~/.local/bin/gateway — docs/install.md:303-307 (Paths), install.sh:166-167 comment
- PATH changed only on macOS when ~/.local/bin missing — internal/install/shell.go:20-22
- Linux `gateway: command not found` fix (add ~/.local/bin to PATH or run ~/.local/bin/gateway doctor) — docs/install.md Troubleshooting row
- ends "Open a new terminal, then run claude." — internal/install/install.go:704
- `claude` function loads settings only when /healthz answers, else prints "llm-gateway is not running: starting plain Claude Code" — internal/clients/claudecode/install.go:98-108
- shell files: ~/.zshrc ($ZDOTDIR), ~/.bashrc (Linux), ~/.bash_profile (macOS) — internal/install/preflight.go:365-370
- Linux systemd user unit llm-gateway.service, macOS launchd label dev.llm-gateway — internal/install/service.go:59-60, 69
- service starts at login — docs/install.md:11 (opening lines), cmd/gateway/install.go:17 help text ("login service")
- `gateway doctor` exists, checks install and running gateway, pass/warn/fail with fix — cmd/gateway/install.go:70-80, 138-145; exit 1 on fail, ibid.
- UI default http://127.0.0.1:7199 — internal/config/ui.go:27
- `gateway uninstall` removes service, claude function block, settings file; keeps config, data, binary — cmd/gateway/install.go:50-58, internal/install/uninstall.go:85-99
- `gateway uninstall --purge` deletes config dir and data dir, asks once more naming size, "cannot be undone" — internal/install/uninstall.go:156-166, 228-232
- doc line "plain uninstall keeps config, data and binary" — internal/install/uninstall.go:96-99
- `make build`, `make setup`, `make verify` exist; `make build && ./bin/gateway install` — Makefile:16, 54-55, 110; docs/install.md:99-100; BIN is ./bin (Makefile build target uses $(BIN)/gateway; BIN value not re-read, install.md and AGENTS.md use ./bin)
- `make setup` / `make verify` as contributor commands — AGENTS.md:72-73
- CI badge URL kept verbatim from the old README — README.md:3 (not re-checked against .github/workflows/verify.yml name)
- Claude Code only built client, Anthropic Messages only built protocol; others "designed for, later" — PLAN.md:66-72 (section 3)
- README link PLAN.md#3-client-support — PLAN.md:66 heading "## 3. Client support"
- anchors used: docs/install.md#requirements (line 14), #troubleshooting (line 336); docs/ui.md, what-gets-recorded.md, architecture.md, capture.md, observability.md, roadmap.md, release-checklist.md, clients/claude-code.md, clients/claude-code-hooks.md, decisions/ all exist (ls docs)
- UI list items (sessions, turns, tool calls, file and network effects, "needs a look", live updates, capture-degraded banner) — docs/ui.md headings (Sessions page, Session page, Live updates, Capture-health banner) and docs/what-gets-recorded.md:25-45
- "records prompts, responses, tool calls, tokens, API-equivalent cost" — docs/what-gets-recorded.md:6-45
- "auth headers and keys are not stored" — AGENTS.md hard rules (credentials); not re-verified in capture code
- "listens on loopback only, no login" — docs/ui.md:11-14, docs/install.md Known limits
- "direct calls to api.anthropic.com are missed" — PLAN.md:77 (known client limits)
- getting-started: "session shows up in the UI after using claude" — docs/install.md Troubleshooting row 1 (UI shows sessions); behaviour not run
- getting-started: UI file-change claim for CLAUDE settings file removal on uninstall — internal/install/uninstall.go:85-87 (settings removed when install owns it)
- NOT VERIFIABLE IN CODE: "It starts again each time you log in" — worded from service manager semantics (systemd user unit, launchd agent) and install.md; unit/plist contents not re-read
- NOT VERIFIABLE IN CODE: "a few other tools (such as curl)" and "installer offers to install what is missing" — worded from docs/install.md:14-20 only
- NOT VERIFIABLE IN CODE: CI badge workflow name verify.yml — kept from old README

#### Stale statements elsewhere for group D
1. docs/install.md:99-100 points to clients/claude-code.md §6 for the manual setup; check the anchor `#6-files-and-network-014` still resolves (heading text not re-read).
2. docs/roadmap.md:4-7 says "v1 is complete" and keeps spec numbers; fine as history, but the README no longer carries that line, so roadmap.md is now its only home.
3. The old README's "Known limits" (loopback only, secrets in captures) was removed; install.md#known-limits and what-gets-recorded.md#privacy hold it. No link elsewhere pointed to the README section (not grepped repo-wide: group D should grep for `README.md#`).
4. PLAN.md section 10 and AGENTS.md still carry the workflow split (group D, AC4).
