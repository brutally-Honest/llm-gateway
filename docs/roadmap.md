# Roadmap

What comes next, in order. PLAN.md §7 holds the phases; this is the short list of
features. Update it when the order changes.

v1 is complete (PLAN §7: every phase in the v1 order is done) apart from the Later items
below and AC44, the API-key smoke test of Phase 1.

## Next
1. **Persistent view cache** (first after v1): the UI's start-up warm-up keeps growing
   with the store, 17.7 s after 019 (warm_ms 17670) and 23.4 s on 2026-11-15 (warm_ms
   23429, 021's real-gateway check; `gateway_ui_warmup_seconds` now tracks it). The UI
   waits that long after every start. Likely fix: a sessions-list and view cache that
   persists across restarts. The 500-session benchmark (~210 ms) misses it by shape, not
   size: it seeds 500 sessions × 20 calls × 4 events (~40k events), while the real store
   had 14 sessions, 2,337 calls (up to 661 in one) and ~510 events per call (each call
   re-sends the conversation), 1.18M events, at 019; seeding that shape takes minutes a
   run, so the benchmark was left as it is (`internal/ui/timeline_bench_test.go`).

## Done
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
  one message naming the key, the value and the reason. `docker-compose.yml` runs the
  gateway on the host's network, with no exception in the code; the native install is
  the supported path (`docs/install.md`, Known limits).
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
- 017 E10 has no test for a live update that changes the very turn whose chip panel is
  open (the code keeps the choice, `story.js` chipsNode; only the unchanged-turn case is
  tested).
- The exchange API still fills the retired drawer's fields (messages, carried, response;
  `fillDrawer`), which no page reads since 017; dropping them saves a session build per
  full exchange page.
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
  connections go to unassigned unless exactly one routed session is active; 014 AC10's
  rule). Idea: tag each claude launch with its own id in the proxy URL.
- The agent-loop kit on macOS's bash 3.2: `loop.sh:91` (a `case` inside `$( )`) fails to
  parse, so 14 kit scripts fail (`docs/manual-checks/macos-v1.md` B8).
- A `claude` run that fails at argument parsing still sends one SessionEnd hook, which
  shows as an active "Hooks only" session for minutes (`docs/manual-checks/macos-v1.md` B12).
- The speed follow-ups, re-measured after 016.
  - Cold view build for large sessions (2.3 s server build at 2,000 calls; 016 research
    2026-11-13).
  - `/api/today` takes ~930 ms at 500 sessions (10,000 calls), unchanged from before 017;
    the hook_events scan slice 1 added to it (017 D30) is not measured yet
    (`specs/017-sessions-list-and-call-details/research.md`, Notes).
  - The today query scans the effects table, which has no index on time (017 D25).
  - These two may share one fix (a time index, or running totals); neither is chosen.
- Watch `TestProxy_GoldenAnthropicStreamReplay` (`internal/protocols/anthropic`): it failed
  once on an unrelated commit with "unexpected EOF" and passed on rerun; possible flake
  (`specs/015-effect-attribution/research.md`, 2026-11-10).

## Not now
- Remote access with auth: a gateway token on every route, then listeners off loopback.
  Until then v1 refuses any non-loopback address (spec 022).
- OpenTelemetry ingestion from harnesses (taking a client's own OTel events as ground
  truth, Phase 6). This is not the gateway's own tracing, which Phase 8 built
  (`docs/observability.md`).
- Other harnesses
