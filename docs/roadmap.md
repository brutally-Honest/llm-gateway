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

## Not now
- Cost (Phase 9)
- OpenTelemetry
- Other harnesses
