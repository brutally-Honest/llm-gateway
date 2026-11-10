# Roadmap

What comes next, in order. PLAN.md §7 holds the phases; this is the short list of
features. Update it when the order changes.

## Next
1. **015 effect attribution**: name the tool call that most likely caused each file
   change, file read and network connection (PLAN §7, C v2).
2. **016 web UI v2**: mockups → pick one → spec → build, built once.

## Later
- The 014 Mac check: step 11 of `docs/manual-checks/014-effects.md`, the sandbox part of
  V1, V3 and V4 (`specs/014-file-and-network-effects/research.md`).
- On Ubuntu, test `sandbox.network.allowAllUnixSockets: true` as the workaround for the
  sandbox bug (014 D17).
- The speed follow-ups, re-measured after 016.
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
