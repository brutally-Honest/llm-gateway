# Roadmap

What comes next, in order. PLAN.md §7 holds the phases; this is the short list of
features. Update it when the order changes.

## Next
1. **017 sessions list and call details**: the sessions list and the call details dialog
   that replaces the 013 drawer, on 016's view and theme (split from 016;
   `specs/017-sessions-list-and-call-details/`: spec drafted, plan and tasks next).

## Later
- The 014 Mac check: step 11 of `docs/manual-checks/014-effects.md`, the sandbox part of
  V1, V3 and V4 (`specs/014-file-and-network-effects/research.md`).
- On Ubuntu, test `sandbox.network.allowAllUnixSockets: true` as the workaround for the
  sandbox bug (014 D17).
- The speed follow-ups, re-measured after 016.
  - Cold view build for large sessions (2.3 s server build at 2,000 calls; 016 research
    2026-11-13).
- Gateway stop takes 90 s and ends in SIGKILL: open proxy tunnels are only closed after
  the shutdown timeout runs out. Close them as soon as shutdown starts, and set
  `TimeoutStopSec` in the systemd unit in `docs/clients/claude-code.md` (and the launchd
  equivalent, `ExitTimeOut`, in the plist there).
- Watch `TestProxy_GoldenAnthropicStreamReplay` (`internal/protocols/anthropic`): it failed
  once on an unrelated commit with "unexpected EOF" and passed on rerun; possible flake
  (`specs/015-effect-attribution/research.md`, 2026-11-10).

## Not now
- Cost (Phase 9)
- OpenTelemetry
- Other harnesses
