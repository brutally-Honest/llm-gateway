---
status: proposed
date: 2026-10-13
---

# 0008 — In-memory fixed-window limiter for v1, behind a `Limiter` strategy; Redis token bucket deferred

## Context
PLAN.md §8 chose Redis with a Lua token bucket for rate-limit state, scheduled for Phase 5.
Phase 5 (specs/006-rate-limiting) needs a per-gateway-key ceiling on requests and tokens per
minute with well-formed 429s. Two constraints from PLAN.md bind it. The stateless-process
rule (§5) says state lives in external stores. The instances row (§2) says the design must
allow N instances later. Against that, v1 is one local process for one user, and a Redis
server would be a new runtime dependency in compose for a feature that one process can serve.

The limit key is the gateway key sent in `x-gateway-key`. It is an identity label, not a
secret (spec 006 Non-goals), so no store is needed to validate it. Config names the keys in
advance, so unknown keys are rejected before any state is created.

## Decision
The v1 limiter is an **in-memory fixed-window counter** per gateway key, with a 60-second
window aligned to the wall clock. It sits behind a `Limiter` strategy interface, and the
only strategy in v1 is `fixed_window_memory`. The Redis token bucket (Lua, atomic
check-and-decrement) is **deferred**, not dropped: it replaces the in-memory strategy when
the gateway runs more than one instance, and the middleware, config shape and 429 behaviour
do not change when it does.

The limiter counts a request when it is admitted and tokens when the response's usage is
seen. Tokens are input + cache-write + output; cache-read is not counted.

This is a documented, temporary exception to the stateless-process rule (PLAN.md §5). The
known limits of the v1 limiter are accepted and documented:

- **E5 — window-edge bursts.** A fixed window lets a client send up to 2× `rpm` within a
  few seconds across a window boundary. Accepted: a smoother algorithm costs more state and
  code than the v1 goal needs.
- **E6 — restart resets counts.** A gateway restart sets every count to 0, so a client can
  exceed its window once per restart. Accepted: the limit is a ceiling for a local user, not
  an accounting record.
- **E7 — several instances each count separately.** With N instances behind a load balancer,
  each one counts on its own and the effective limit is up to N× the configured limit. This
  is the reason the Redis strategy exists; it is not a v1 requirement.

## Alternatives
- **Redis + Lua token bucket now (PLAN.md §8 as decided).** Rejected for v1: it adds a
  runtime service to compose and a network dependency on the request path, for a single
  process that does not need shared state. It stays the planned strategy for multi-instance
  use.
- **Token bucket or sliding window in memory.** Rejected for v1: it removes E5 but adds
  per-key timing state and more code than the goal needs. The `Limiter` interface leaves it
  open as a later strategy.
- **Hand-rolled shared state in a file or SQLite.** Rejected: a second source of truth for
  counts, with lock contention on every request, for no gain over a mutex-guarded map in one
  process.
- **Key limits on the upstream credential.** Rejected: subscription OAuth tokens refresh, so
  each new token would start an empty counter.

## Consequences
- **Easier:** no new runtime service and no new dependency; a limit is a config entry; the
  limiter is a self-contained package that knows nothing of HTTP.
- **Harder:** limits are per process and reset on restart. Anyone reading a limit must know
  E5-E7. The docs (`config.example.yaml`, `docs/clients/claude-code.md`) state them.
- **Commits us to:** the `Limiter` interface and the `x-gateway-key` header as the identity
  label. Both must stay stable so the Redis strategy can be added without touching callers.
- **Reversing costs:** adding the Redis strategy is one new implementation of the interface,
  plus compose and config for the Redis address. Moving to Redis also removes E6 and E7. No
  captured data depends on the counts, so no migration is needed.
