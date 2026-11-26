# Roadmap

What comes next, in order. PLAN.md §7 holds the phases; this is the short list of
features. Update it when the order changes.

v1 is complete (PLAN §7: every phase in the v1 order is done) apart from the Later items
below and AC44, the API-key smoke test of Phase 1.

## Next
Nothing queued.

## Done
Each entry describes its spec as it merged. [ui.md](ui.md) describes the UI as it is now.

- **047 flake and banner** (`specs/047-flake-and-banner/`, fix): a test flake named at its
  writer and fixed at the cause; the capture-degraded banner can be dismissed per tab and
  comes back only when the lost count goes up or the gateway restarted.
- **046 UI polish** (`specs/046-ui-polish/`, fix): one title rule per turn and session, a
  cache-write figure in the token cell, "The gateway is not answering." with Retry,
  "Last active" as the first column, a "Session not found" page, a reconnecting note
  beside the live dot and a Clear button on an empty filtered list.
- **045 failures visible** (`specs/045-failures-visible/`, fix): a missing body shows as
  "content unavailable"; a call whose body could not be stored is kept with its metadata;
  every drop or store failure is counted, exported as a metric, reported by `/healthz` and
  `/api/health` as degraded, and shown in the UI as a banner.
- **044 netproxy shutdown race** (`specs/044-netproxy-shutdown-race/`, fix): no tunnel
  joins the wait group after shutdown has started.
- **043 fast rows first** (`specs/043-fast-rows-first/`, fix): every `/api/sessions` page
  and filter answers at once; rows show the cheap columns first and the heavy facts fill
  in when built, the rows on the page being viewed first.
- **042 sessions list UI** (`specs/042-sessions-list-ui/`, fix): a pinned table header
  readable in both styles and themes, a failed page that can be retried, smooth scrolling
  while live events arrive.
- **041 flakes and quick speed** (`specs/041-flakes-and-quick-speed/`, fix): three test
  flakes (a stream replay, wall-clock-minute rate-limit tests, an unexplained
  `cmd/gateway` failure) and four small speed problems (first call details after a
  restart, `/api/today` refetching, two unused indexes, a noisy shutdown warning).
- **036 macOS stream test hang** (`specs/036-macos-stream-test-hang/`, fix): the
  `cmd/gateway` run test that hung on macOS CI until the 10-minute timeout.
- **034 effects git hardening** (`specs/034-effects-git-hardening/`, fix): no git call
  the gateway makes can run a program named by the repo it runs in; the config folder is
  `0700` and `config.yaml` `0600`.
- **033 docs correctness** (`specs/033-docs-correctness/`, chore): wrong or stale claims
  in the docs fixed against the code, and spec and ADR statuses set.
- **040 incremental live updates** (`specs/040-incremental-live-updates/`, feat): a live
  update of an open session re-reads only the calls that changed; on the largest session a
  new call shows in about 0.3 s instead of 2.6 s. The first open and a few fallbacks still
  build the session in full.
- **030 persistent view cache** (`specs/030-persistent-view-cache/`, feat): the UI serves
  as soon as it binds; per-session list rows and views live in a disposable on-disk cache
  (`<capture.dir>/view-cache/`), rebuilt from the store, with a background pass after start.
  Resent message text is decoded once per build.
- **032 kit removal ADR** (`specs/032-kit-removal-adr/`, chore): ADR 0021 records why the
  agent-loop kit was removed (slow, and it blocked too often); ADR 0011, `AGENTS.md` and
  this file point to it.
- **031 remove clutter** (`specs/031-remove-clutter/`, chore): removed what was unused:
  the agent-loop kit (so `make verify` is lint, test and web-test, with no `kit-test`),
  the CLI viewer (`gateway sessions`, `gateway timeline`), `gateway backfill-sessions`,
  the 016 mockups, the gateway's own Docker image and root compose file (the
  observability stack stays) and a latency test that never ran under `-race`. CI lints
  on Linux only.
- **029 UI and small fixes** (`specs/029-ui-and-small-fixes/`, fix): the sessions list
  pages on the server, 50 rows at a time with Previous and Next, filter and search done
  there and kept in the URL; skeleton loaders; Preview and Raw padding; long turn text
  capped in height. B12 (`docs/manual-checks/macos-v1.md`): a `claude` run that fails at
  argument parsing still sends one SessionEnd hook, and its "Hooks only" session showed
  active for minutes; a session whose only hook events are session ends, with no model
  calls, is never active, in the list, the view and `kind=active`. The 017 E10 test
  (`internal/ui/webtest/chip-live.test.js`); `fillDrawer` and the fields only it filled
  are gone from the exchange API.
- **028 CI flakes** (`specs/028-ci-flakes/`, fix): `TestRun_ShutdownCutsInFlightCallAfterGrace`
  and `TestProxy_GoldenAnthropicStreamReplay` fixed at the cause: the request watcher
  (`internal/core/meta.go`) answers net/http's read of a closed body after EOF with EOF,
  so the transport no longer drops an HTTP/1.1 upstream connection mid-stream. A hub
  test's race was in the test and is fixed there.
- **027 UI polish** (`specs/027-ui-polish/`, feat): two styles, Lean (default, neutral
  glass with a green accent) and Stark (square, violet), picked with the theme in a header
  Settings dialog; the saved choice applies before first paint; Geist is bundled (ADR
  0020). The sessions list has one Work cell, status glyphs, day tiles with hourly
  sparklines and a whole-row link. A call opens in a docked panel; a session is one page
  scroll of turn cards, newest first, with Files switchable by file or by turn.
- **026 GitHub Actions** (`specs/026-github-actions/`, chore): `.github/workflows/verify.yml`
  runs on every pull request and push to main on Linux and macOS (ADR 0019, which
  supersedes ADR 0001). Linux runs `make verify`; macOS runs `make test` and
  `make web-test` (lint is Linux-only, 031). Go comes from `go.mod`, Node is pinned to 22,
  and the README shows the badge. `docs/release-checklist.md` lists the release steps.
- **025 macOS re-test fixes** (`specs/025-macos-retest-fixes/`, fix): the findings of
  `docs/manual-checks/macos-v1-retest.md`. The launchd plist sets `ThrottleInterval` to
  1 s, so a reinstall soon after a start is no longer held about 10 s (B15); the roots
  test expects resolved paths and passes where `/home` is a symlink (B16); with no lsof or
  ss, preflight finds the running gateway by its health routes and a reinstall over it
  goes on. A start that fails under the service waits 10 s and logs the same error at
  most once per 10 minutes, and doctor names the loop. Checked on Linux; the launchd
  parts wait for the next Mac upgrade (Later).
- **024 macOS fixes** (`specs/024-macos-fixes/`, fix): the v1 blockers of the macOS check
  (`docs/manual-checks/macos-v1.md`). A launchd reinstall waits for bootout and retries
  bootstrap, or only kickstarts an unchanged job, and says plainly if the gateway is left
  down; doctor tells not loaded, loaded and running apart; the Claude Code settings set
  `NODE_USE_ENV_PROXY=1`; the proxy no longer records the gateway's own listeners;
  effects compare symlink-resolved paths; every subcommand prints plain text, with
  `--version`; the `--check`, uninstall keep-list and unknown port owner wording; the
  tests pass on macOS. Checked on Linux; the launchd fix and Node under Seatbelt wait for
  the Mac re-test.
- **022 loopback only** (`specs/022-loopback-only/`): every listener (API, UI, HTTP and
  SOCKS proxies) refuses any address outside 127.0.0.0/8, `::1` and `localhost`, with no
  override. Config loading, `gateway install` and `gateway doctor` share one check and
  one message naming the key, the value and the reason, with no exception in the code
  (`docs/install.md`, Known limits).
- **023 one-command install** (`specs/023-one-command-install/`): `curl … install.sh | sh`
  on Linux and macOS. install.sh checks its own needs, downloads the release for the
  machine and verifies its SHA256; `gateway install` prints a preflight table, offers to
  install what is missing (sudo only inside a shown, approved package command), shows the
  plan or a `--dry-run` diff, and sets up the config (only if none), the Claude Code
  settings, a systemd user unit or launchd agent and the `claude` function between
  markers, replacing a hand-made setup only on yes with `.bak`s. `gateway uninstall` puts
  back what install replaced, in its prior state, and asks before cutting off its own
  session; `gateway doctor` checks the dependencies and the live install
  (`docs/install.md`). Checked on Linux; the macOS run is under Later.
- **021 self-observability** (`specs/021-self-observability/`, Phase 8): Prometheus
  `/metrics` on the API and UI listeners (requests, upstream latency and errors, gateway
  overhead, capture and store, tunnels and UI streams, UI build times, warm-up, the last
  stop, build info) with low-cardinality labels; OTel spans of each proxied call to
  Jaeger, off by default, rate-limited warnings when the collector is down; a compose
  stack (`make observability-up`) with Prometheus, Grafana and its provisioned dashboard,
  and Jaeger, all on loopback (`docs/observability.md`).
- **020 API-equivalent cost** (`specs/020-api-equivalent-cost/`, Phase 9): every model
  call priced on read from its stored usage and a checked-in table of Anthropic's
  published prices (effective dates, the 5m/1h cache-write split, Haiku 5.5's tier), in
  exact integer pico-dollars; shown as API-equivalent in the call dialog with its
  breakdown, the sessions list, the session overview and the Today line; a model with no
  price is "— no price for <model>" and makes totals partial ("≥").
- **019 fast shutdown** (`specs/019-fast-shutdown/`, fix): a stop takes under a second with
  nothing in flight (was 90 s and a SIGKILL): proxy tunnels and UI streams close at once,
  in-flight model calls get 10 s and are then cut and stored as cut by the gateway; the
  UI builds the sessions list before it accepts after a start.
- **018 side-call turns** (`specs/018-side-call-turns/`, fix): Claude Code's away
  summaries and prompt suggestions are side calls (017 D12), and its background-task
  notices stay with the calls that answer them instead of showing as false hooks-only
  turns or prompts.
- **017 sessions list and call details** (`specs/017-sessions-list-and-call-details/`,
  spec v8): the sessions list, the call details dialog that replaced the 013 drawer, and
  the ungrouped, unassigned and full exchange pages on 016's theme.

## Later
- Agent-loop kit: reinstall from brutally-Honest/agent-loop if a stricter spec flow is wanted again (removed in 031; settings in specs/031-remove-clutter/research.md; see ADR 0021).
- The 025 Mac check: the first upgrade to 025 takes the changed-plist path (bootout,
  wait, bootstrap) against real `launchctl`; back-to-back reinstalls restart within about
  a second; `make verify` green; the own-gateway preflight with `/usr/sbin` off PATH; a
  broken config restarting about every 11 s with one log line per 10 minutes and
  doctor's restarting row (`specs/025-macos-retest-fixes/research.md`, Real check).
- The first release: tag it and publish what `make dist` builds (four archives and
  `SHA256SUMS`, names in `specs/023-one-command-install/research.md`), so the curl line
  works; until then install.sh has nothing to download from GitHub.
- The 023 macOS check: launchd bootstrap, KeepAlive and the 30 s exit timeout, doctor
  reading launchd status, logs in `~/Library/Logs/llm-gateway/`, the PATH line and
  `~/.bash_profile`, brew offers and the no-Homebrew manual steps, the Seatbelt sandbox
  block, `shasum` in install.sh, the darwin binaries running after a curl download, and
  `lsof` naming a port's holder.
- Fast-mode and US-only inference prices (020 D6, D16): nothing captured uses them yet;
  a `speed` column in the price table when something does.
- AC44, the API-key smoke test of Phase 1 (Claude Code in API-key mode through the
  gateway; `specs/001-core-and-anthropic-passthrough/research.md` Q4): needs a Console
  account and prepaid credits.
- The 014 Mac check: step 11 of `docs/manual-checks/014-effects.md`, the sandbox part of
  V1, V3 and V4 (`specs/014-file-and-network-effects/research.md`).
  - [x] Step 11 with the sandbox off, step 10 on launchd, the sandbox part of V1, V3 (HTTP)
    and V4 — 2026-11-20, macOS 26.5.2, Apple M4 Pro (`docs/manual-checks/macos-v1.md`).
  - [x] Node's `fetch` with the sandbox on (014 Q2): `example.com:443` shown under its
    Bash call, through 024's `NODE_USE_ENV_PROXY=1`, Node 26.12.0 — 2026-11-21, macOS
    26.5.2, Apple M4 Pro (`docs/manual-checks/macos-v1-retest.md`).
  - [ ] SOCKS (7195) not exercised.
- On Ubuntu, test `sandbox.network.allowAllUnixSockets: true` as the workaround for the
  sandbox bug (014 D17).
- Per-session network attribution when several Claude Code sessions run at once (today
  connections go to unassigned unless exactly one session has a hook event in the active
  window and has not ended; 014 AC10's rule). Idea: tag each claude launch with its own id in the proxy URL.
- Label sessions by account: when several Claude accounts are used on one machine, show
  which account each session came from; today their sessions are mixed.
- The speed follow-ups, re-measured after 016.
  - Cold view build for large sessions (2.3 s server build at 2,000 calls; 016 research
    2026-11-13).
  - `/api/today` takes ~930 ms at 500 sessions (10,000 calls), unchanged from before 017;
    the hook_events scan slice 1 added to it (017 D30) is not measured yet
    (`specs/017-sessions-list-and-call-details/research.md`, Notes).
  - The today query scans the effects table, which has no index on time (017 D25).
  - These two may share one fix (a time index, or running totals); neither is chosen.

## Not now
- Remote access with auth: a gateway token on every route, then listeners off loopback.
  Until then v1 refuses any non-loopback address (spec 022).
- OpenTelemetry ingestion from harnesses (taking a client's own OTel events as ground
  truth, Phase 6). This is not the gateway's own tracing, which Phase 8 built
  (`docs/observability.md`).
- Other harnesses
