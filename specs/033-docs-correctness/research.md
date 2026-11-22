---
feature: 033-docs-correctness
---
# Research & decisions — 033-docs-correctness

## Inventory

`git ls-files '*.md' | grep -vE '^(specs/|docs/manual-checks/)'` plus `config.example.yaml`,
on main at a5eade1. Each file is in exactly one group.

| Group | Files |
|---|---|
| 1, install and use | `docs/install.md`, `docs/clients/claude-code.md` |
| 2, the UI | `README.md`, `docs/clients/claude-code-hooks.md` |
| 3, observability and config | `docs/observability.md`, `config.example.yaml`, `docs/capture.md` |
| 4, project docs | `AGENTS.md`, `PLAN.md`, `CLAUDE.md`, `docs/roadmap.md`, `docs/release-checklist.md`, `docs/decisions/0000-template.md`, `internal/clients/claudecode/testdata/side_calls/README.md`, `internal/clients/claudecode/testdata/task_notifications/README.md`, `internal/protocols/anthropic/testdata/README.md` |
| statuses only (step 3) | `docs/decisions/0001`–`0021` (bodies are history) |

`docs/decisions/README.md` does not exist.

## Fact sheet (main at a5eade1)

### `gateway` subcommands and flags (`cmd/gateway`)
Flags parse with Go's `flag` package, so `-x` and `--x` both work.
- `gateway [-config <path>]` — serve (`run.go:185-187`). `-config` default: `./config.yaml` if present.
- `gateway --help` / `-h`, `gateway --version` (prints `gateway <version>`) (`run.go:161-167`).
- `gateway dump [-config <path>] [-raw] [-last] [<request id>]` — one stored exchange; an id or `-last` is required, not both (`dump.go:27-50`).
- `gateway hooks print [-config <path>]` — writes the Claude Code settings document (`hooks.go:16-36`).
- `gateway install [--yes] [--install-deps] [--no-shell] [--no-service] [--check] [--dry-run]` (`install.go:35,111-117`).
- `gateway uninstall [--yes] [--force] [--purge]` (`install.go:54,126-129`).
- `gateway doctor [--json]` (`install.go:72,138-139`).
- There is no `gateway sessions`, `gateway timeline` or `backfill-sessions` (removed in 031). Anything else runs the gateway (`run.go:178`).

### make targets (`Makefile`)
`setup` (= `setup-git setup-lint setup-shellcheck setup-node` + `go mod download`), `setup-git`,
`setup-lint` (golangci-lint 2.13.2 into `./bin`), `setup-shellcheck`, `setup-node` (node 22.12+),
`build` (`bin/gateway`), `run` (build, then `bin/gateway`), `dist` (four archives
`llm-gateway_<version>_<os>_<arch>.tar.gz` for linux/darwin × amd64/arm64 + `SHA256SUMS` in
`dist/`), `test` (`go test -race ./...`, which also shellchecks install.sh), `web-test`
(node test runner on `internal/ui/webtest/*.test.js`), `lint`, `verify` (lint, test, web-test),
`observability-up`, `observability-down` (`deploy/observability/compose.yml`, plus
`compose.linux.yml` on Linux). No `kit-test` target. No Dockerfile or root `docker-compose.yml`.

### Config keys and defaults (`internal/config`)
Env override: `GATEWAY_` + key upper-cased, `.` and `-` → `_` (`config.go:143-148`), e.g.
`upstreams.anthropic.base_url` → `GATEWAY_UPSTREAMS_ANTHROPIC_BASE_URL`. Order: defaults, file, env (`load.go:99`).
- `listen_addr` `127.0.0.1:7197`; `log_level` `info`; `shutdown_timeout` `10s` (`config.go:101-112`).
- `upstreams.<name>.base_url` (anthropic: `https://api.anthropic.com`, `protocols/anthropic/adapter.go:28`), `.connect_timeout` 10s, `.tls_handshake_timeout` 10s, `.response_header_timeout` 10m (`upstream.go:35-37`).
- `capture.enabled` true, `capture.dir` (default `$XDG_DATA_HOME/llm-gateway` if absolute, else `~/.local/share/llm-gateway`), `capture.queue_size` 256, `capture.workers` 2, `capture.max_body_bytes` 32 MiB, `capture.memory_limit` 256 MiB (`capture.go:30-58`).
- `hooks.queue_size` 256, `hooks.workers` 1, `hooks.memory_limit` 32 MiB (`hooks.go:28`).
- `ratelimit.enabled` (off by default), `ratelimit.strategy` `fixed_window_memory`, `ratelimit.keys.<name>.rpm`, `.tpm` (`ratelimit.go:9,26-38`).
- `ui.enabled` true, `ui.listen_addr` `127.0.0.1:7199` (`ui.go:26`).
- `effects.enabled` true; `effects.files.enabled` true, `.extra_dirs` [], `.ignore` [.git node_modules dist build target .venv venv __pycache__ .next .cache], `.max_copy_bytes` 256 KiB, `.git_timeout` 10s, `.snapshot_retention` 168h (7 days); `effects.network.enabled` true, `.http_proxy_addr` `127.0.0.1:7198`, `.socks_proxy_addr` `127.0.0.1:7195`, `.sandbox` false, `.active_window` 10m; `effects.attribution.skew` 50ms (`effects.go:100-124`).
- `tracing.enabled` false, `tracing.otlp_endpoint` `http://127.0.0.1:4318` (`tracing.go:22-26`).

### Metrics (`internal/observability`)
`gateway_http_requests_total{listener,status_class,proxied}`,
`gateway_http_request_duration_seconds{listener,status_class,proxied}`,
`gateway_proxy_overhead_seconds{protocol}`, `gateway_upstream_ttfb_seconds{protocol,stream}`,
`gateway_upstream_duration_seconds{protocol,stream}`,
`gateway_upstream_errors_total{protocol,kind}` (timeout, unreachable, aborted, rate_limited, status_5xx),
`gateway_store_write_seconds{op,outcome}`, `gateway_ui_view_build_seconds{cache}`,
`gateway_ui_warmup_seconds`, `gateway_build_info{version,go_version}` (`metrics.go:63-122`);
`gateway_last_shutdown_duration_seconds`, `gateway_last_shutdown_calls_cut`,
`gateway_last_shutdown_tunnels_cut`, `gateway_last_shutdown_timestamp_seconds`
(`shutdown.go:72-75`); plus Go runtime and process collectors. Check docs for any other metric
name against `grep -rn 'Name: *"gateway_' internal` before keeping it.

### HTTP endpoints and ports
- API listener `127.0.0.1:7197` (`internal/server/server.go:45-55`): `GET /healthz`, `GET /metrics`, `POST /_gateway/hooks/{client}` (`internal/hooks/handler.go:16,62`; Claude Code's is `/_gateway/hooks/claude-code`); any other `/_gateway/*` is 404; everything else is proxied upstream.
- UI listener `127.0.0.1:7199` (`internal/ui/ui.go:113-143`), GET only, host and origin checked: `/metrics`, `/api/health`, `/api/sessions`, `/api/sessions/{id}`, `/api/sessions/{id}/view`, `/api/sessions/{id}/timeline`, `/api/sessions/{id}/tools/{tid}`, `/api/sessions/{id}/calls/{eid}`, `/api/sessions/{id}/effects`, `/api/sessions/{id}/stream`, `/api/stream`, `/api/ungrouped`, `/api/today`, `/api/exchanges/{id}`, `/api/exchanges/{id}/body`, `/api/effects/unassigned`, `/api/effects/{eid}/content`, `/api/effects/{eid}/diff`; static files at `/*`.
  `/api/sessions` takes `kind` (all, look, active, hooks), `q`, `after`, `offset`, `limit` (`api_sessions.go:565-600`).
- HTTP forward proxy `127.0.0.1:7198` (CONNECT and absolute http:// URLs, `netproxy/http.go:135`), SOCKS5 `127.0.0.1:7195` (no auth, CONNECT only, `netproxy/socks5.go:16`). 7196 is kept free by preflight.
- Loopback only: every listener refuses addresses outside 127.0.0.0/8, `::1`, `localhost` (spec 022).

### Installer (`internal/install`, `install.sh`)
- Binary `~/.local/bin/gateway`; config `~/.config/llm-gateway/config.yaml`; Claude Code settings `~/.config/llm-gateway/claude-settings.json`; state `~/.config/llm-gateway/install-state.json`; data = capture default dir; start-up error record `$XDG_STATE_HOME/llm-gateway/startup-error.json` or `~/.local/state/llm-gateway/startup-error.json` (`env.go:111-131`, `startup/startup.go:24-58`).
- Linux: systemd user unit `llm-gateway.service` in `~/.config/systemd/user/`, `Restart=on-failure`, `TimeoutStopSec=30` (`service.go:59-98`).
- macOS: launchd label `dev.llm-gateway`, plist `~/Library/LaunchAgents/dev.llm-gateway.plist`, log `~/Library/Logs/llm-gateway/gateway.log`, `ThrottleInterval` 1 s, exit timeout 30 s (`service.go:60-65,186-192`).
- Shell: a managed `claude` function between markers in the shell's rc file; adds `~/.local/bin` to PATH (`shell.go:15-21`, `install.go:258-420`).
- `install.sh`: `LLM_GATEWAY_BASE_URL` replaces the release URL (`install.sh:14,123`).

### UI main labels (`internal/ui/web`)
- Header: Sessions, Settings (gear; dialog with Theme: Dark / Light, Style: Lean / Stark) (`shell.js:32-137`).
- Sessions list: search box "Filter sessions"; filter chips All, Needs a look, Active, Hooks only (`sessions-page.js:266`); server-side filter and search; Previous/Next with a "Page N of M" label; state in the URL hash `#/?kind=…&q=…&page=…` (`route.js:28-80`).
- Session page: "Session overview", Turns, "Needs a look", session-level tabs Story / Files / Commands / Sites / Context (`session-page.js:9,120`, `side-card.js:9-14`) and per-turn chips All, model calls, tool calls, files changed, read, searches, sites, failed (`story.js:431-440`), "What this session touched", "Unassigned connections".
- Call details panel: "Model call", "Call #N", Preview / Raw, "Raw request body", "Raw response stream", "Full exchange page", "Close call" (`call-dialog.js`).
- Skeleton loaders while data loads (029); text fallbacks "Loading sessions…", "Loading the session…", "Loading call…".
- Other pages: `#/ungrouped` (Ungrouped, with "Load more"), `#/effects/unassigned`, `#/exchanges/<id>` (`route.js:38-44`).
- Hooks-only sessions (◇ "hooks only"): a session whose only hook events are SessionEnd, with no model calls, is never active (029 B12, `api_view.go` `isActive`).

## Decisions
- **D1** README.md is in group 2, not group 1: it holds the repo's only description of the web UI ("Web UI" section), and each file has one owner. Its install section is short and the group 2 agent gets the install facts too.
- **D2** `docs/clients/claude-code-hooks.md` is in group 2, not group 1: it describes the UI's match chips and hooks-only sessions (B12), which is group 2's brief.
- **D3** `docs/capture.md` is in group 3: it lists config keys (`capture.*`), the store location and `gateway dump`.
- **D4** The testdata READMEs and the ADR template fit no group, so they are group 4 (spec E1).
- **D5** Specs 029, 031 and 032 have no `status:` line, and AGENTS.md says new specs carry no status frontmatter. The brief lists them as `done` explicitly, so a `status: done` line is added to their frontmatter; the brief, being the more specific instruction, wins.
- **D6** Specs 007, 011 and 012 keep their frontmatter in `brief.md` (no `spec.md`), so their status is set there.
- **D7** Spec 027 does have `status: draft`; the first status run misread it as missing. A second, single-line run set it to `done`; no duplicate key was added.
- **D8** The roadmap's Done list ran from 017 to 025 and left out 026 and 027, which are finished. The brief names 028–032, but a Done list that skips two finished specs is stale, so 026 and 027 entries were added too.
- **D9** "Labelling sessions by account" is not in the roadmap and no spec or doc records it (`grep -rniE 'by account|per.account'` finds nothing). It is not added: there is no source to word it from, and adding it would be new content. Listed in the report for the human.
- **D10** README's "older specs (000–017) also keep plan.md and tasks.md" was half true: 017 keeps only `tasks.md`. The first fix (000–016) lost that, so it now names 017's `tasks.md` separately.
- **D11** `docs/clients/claude-code.md:129` "the timeline shows which asked-for tools ran" is read as the removed CLI timeline (it sits beside the 010 hooks text, written when `gateway timeline` existed) and becomes "the web UI", matching the hooks page.
- **D12** The hand-written systemd unit and plist in `docs/clients/claude-code.md` §6 differ from what `gateway install` writes (log path, KeepAlive, ThrottleInterval). They describe a valid manual setup, not the installer, so they are not false and stay.
- **D13** The PLAN.md §8 Testing row keeps its rationale ("testcontainers gives real Redis/DB") as the reason the choice was made; only the Choice cell says it is not used.

## Statuses left that still look wrong (not changed)
- ADR vocabulary is mixed: 0002–0006, 0009, 0010, 0012–0016, 0019 and 0020 say `approved`; 0008, 0017, 0018 and 0021 say `accepted`. Same meaning, two words.
- Specs 000–017 (except 007, 010, 011, 012) still carry the kit's `status:` frontmatter, which AGENTS.md says new specs do not have; kept as history.
- Spec 004 keeps `status: done` in `brief.md`, like 007/011/012; consistent, no change.
- Specs 029, 031 and 032 now carry `status: done` although AGENTS.md says new specs have no status frontmatter (D5).
- **D14** AC5 grep (2026-11-22, after the group commits): every remaining hit outside `specs/` and `docs/manual-checks/` is history or a negation, so none is changed. `AGENTS.md:39` and `PLAN.md:232` say there is no plan.md or tasks.md; `README.md:22-23` says old specs keep them as history (true: `ls specs/*/plan.md`); `PLAN.md:122` and `:158` are the Phase A and Viewer rows, each marked "removed in 031"; `docs/roadmap.md:25-26` is the 031 Done entry that names what was removed; ADRs 0002 (dated line), 0007, 0009, 0011 and 0016 are ADR bodies; `cmd/gateway/run_test.go:57`, `run_config_test.go:183` and `internal/config/load.go:100` are code comments citing old plan.md files (no code changes in this spec). The AC5 exception list is read as covering these.
- **D15** Opus review round 1 P3 #5 (the §11 scope list adds scopes used once, and `gateway` overlaps `server`): kept. The brief asks for every scope main's commits use, and the list says what history holds; trimming it is the human's call.
- **D16** Opus review round 1 P3 #8 (status frontmatter on 029/031/032 against AGENTS.md): kept, per D5; raised in the report.
- **D17** Opus review round 1 P4 #10 (startup-error.json is also on-disk state) and #11 (no `cost/` in PLAN's layout tree): not changed. The first is a record the service writes on a failed start, not gateway state; the second is a missing entry in an illustrative tree, not a false claim. P4 #12 needs no change.
- **D18** README "last exchange or hook event": checked in `internal/ui/view.go:151-160` (`lastActivity` takes the max of exchange starts and hook receive times).
- **D19** 2026-11-22 correction to D18: `lastActivity` is the session page's rule only. The sessions list, `kind=active` and the live stream use the last exchange start (`internal/store/reader_sessions.go:37-48`, `internal/ui/api_sessions.go:166`, `internal/ui/hub.go:716-723`); a hooks-only session uses its last hook event. README now says so (opus round 2 P2-1), and the hooks page keeps ◇ with "● while active" (P3-1). Round 2's P4s (the "Page N" label while filtered, the chip summary) are left as accurate summaries.
- **D20** 2026-11-22: D5 resolved. The human kept `status: done` on 029, 031 and 032 and changed the rule: a spec may carry one frontmatter line, `status: draft` or `status: done`, and no approval fields (AGENTS.md Workflow). PLAN.md §10 said the same old rule, and AGENTS.md defers to PLAN.md where they disagree, so it got the same change.
- **D21** 2026-11-22: ADR status values unified on `accepted`. `status: approved` became `status: accepted` in the frontmatter of 0002, 0003, 0004, 0005, 0006, 0009, 0010, 0012, 0013, 0014, 0015, 0016, 0019 and 0020; no body changed.
- **D22** 2026-11-22: D15 resolved. Every §11 scope stays; PLAN.md §11 now says `server` is the preferred scope for new commits where `gateway` overlaps it.
- **D23** 2026-11-22: D9 resolved. The human gave the wording; docs/roadmap.md Later has "Label sessions by account".
