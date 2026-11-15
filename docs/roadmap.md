# Roadmap

What comes next, in order. PLAN.md §7 holds the phases; this is the short list of
features. Update it when the order changes.

## Next
1. **Phase 8 self-observability** (PLAN §7): Prometheus, OTel → Jaeger, Grafana; the
   gateway's own p50/p99 overhead visible.

## Done
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
- Fast-mode and US-only inference prices (020 D6, D16): nothing captured uses them yet;
  a `speed` column in the price table when something does.
- Cold sessions-list build takes ~17 s on the real store (warm_ms 17670 after 019), versus
  ~210 ms in the 500-session benchmark; the UI waits that long after every start. Likely
  fix: a view cache that persists across restarts. The benchmark misses it by shape, not
  size: it seeds 500 sessions × 20 calls × 4 events (~40k events), while the real store
  has 14 sessions, 2,337 calls (up to 661 in one) and ~510 events per call (each call
  re-sends the conversation), 1.18M events; seeding that shape takes minutes a run, so the
  benchmark is left as it is (`internal/ui/timeline_bench_test.go`).
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
- On Ubuntu, test `sandbox.network.allowAllUnixSockets: true` as the workaround for the
  sandbox bug (014 D17).
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
- OpenTelemetry ingestion from harnesses (taking a client's own OTel events as ground
  truth, Phase 6). This is not the gateway's own tracing, which Phase 8 built
  (`docs/observability.md`).
- Other harnesses
