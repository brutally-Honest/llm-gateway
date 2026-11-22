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
- Sessions list: search box "Filter sessions"; filter chips All, Needs a look, Active, Hooks only (`sessions-page.js:266`); server-side filter and search; numbered pages in a fixed box with Previous/Next ("Page N of M"); state in the URL hash `#/?kind=…&q=…&page=…` (`route.js:28-80`).
- Session page: "Session overview", Turns, "Needs a look", Story / Context / Files / Sites / Commands chips per turn, "What this session touched", "Unassigned connections".
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
