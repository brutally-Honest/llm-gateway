# Roadmap

What comes next, in order. PLAN.md §7 holds the phases; this is the short list of
features. Update it when the order changes.

## Next
1. **Phase 9 cost, API-equivalent** (PLAN §7): each model call and session priced from
   its captured tokens (the four Phase 4 counters) and the published prices, as what it
   would cost on the API; no API key needed. Unknown models show `unknown`.
2. **Phase 8 self-observability** (PLAN §7): Prometheus, OTel → Jaeger, Grafana; the
   gateway's own p50/p99 overhead visible.

## Done
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
- OpenTelemetry
- Other harnesses
