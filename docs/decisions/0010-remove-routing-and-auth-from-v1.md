---
status: approved
date: 2026-11-01
---

# 0010 — Routing and real auth are removed from v1; their seams stay

## Context
PLAN.md (§7) put routing at Phase 7 (same-protocol model aliases, weighted selection, fallback,
circuit breaker), and §6 kept auth as a later feature (virtual keys, per-key budgets, SSO, a
gateway token on tunnelled routes). Both were planned for a multi-provider, multi-user future.
The v1 gateway runs on one machine for one user, and it proxies one provider (Anthropic) for one
client (Claude Code). Neither routing nor real auth is needed to observe that traffic, and
spec 009 sets a new v1 order: web UI, then harness hooks (Phase 6), then file-system and network
visibility, then cost (Phase 9).

PLAN §2 ("build the seam, not the feature") already requires the seams that let these features
arrive later without a rewrite: a `principal_id` on every event (always `local` in v1), auth as a
middleware behind one interface that returns a principal, a `Limiter` strategy (ADR 0008), and
the upstream and adapter registry that takes more than one entry.

## Decision
Routing (Phase 7 in the old order) and real auth are **removed from v1**. They are not scheduled
after v1 either. The PLAN §6 out-of-scope list names both. The PLAN §7 phase table marks the
routing row as removed.

The seams stay exactly as PLAN §2 describes them. Nothing that exists is taken out:

- `principal_id` on every captured event. The `PrincipalResolver` interface resolves it, and
  its v1 implementation (`LocalPrincipal`) always returns `local`.
- The `Limiter` strategy interface (ADR 0008).
- The upstream config and adapter registry, which can take more entries.

v1 has no auth. It is meant for localhost. The config reference says so, and the default
listeners are loopback only.

## Alternatives
- **Keep routing in v1.** Rejected: one provider and one model need no alias table or fallback,
  and building it would delay the web UI and the observability phases that the user needs first.
- **Keep the gateway token for a tunnel in v1.** Rejected: no tunnel is planned for v1, and a
  token that nothing uses is dead code. The rule itself stays in AGENTS.md: a route reachable
  from outside localhost needs the gateway token.
- **Remove the seams with the features.** Rejected: the seams are a few lines each, and removing
  them would turn a later feature into a rewrite, which PLAN §2 forbids.

## Consequences
- **Easier:** the v1 roadmap is short and has a clear order. The UI and the observability phases
  do not wait behind routing or login.
- **Harder:** a second provider or a second user later needs routing and auth built then. The
  seams make that additive, but it is still work.
- **Commits us to:** a v1 that is local-only with no login. Any move beyond localhost needs
  auth first, and that is a new feature with its own spec.
- **Reversing costs:** routing comes back as a new strategy behind the existing upstream
  registry, and auth as a new `PrincipalResolver`. Neither needs a change to the store
  or the canonical events.
