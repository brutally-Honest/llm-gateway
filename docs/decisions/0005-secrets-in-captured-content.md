---
status: approved
date: 2026-09-28
---

# 0005 — Secrets inside captured content are stored as-is

## Context
Feature 002 (`specs/002-capture-and-canonical-events/spec.md`) stores every request
and response body the gateway forwards. Auth headers and secret query parameters are
always redacted (PLAN.md §5). Secrets *inside* bodies are a different matter: agents
read `.env` files, run `env` and print tokens, and those land in tool results, and
from there in prompts that are resent on every turn. PLAN.md §9 leaves this open as
OQ-5. The forces:
- **Fidelity.** The capture is the record of what the model actually saw. Masking
  changes the stored copy (never the forwarded traffic), so the timeline would show
  less than the model received, and the raw body would no longer be the wire bytes.
- **Trust boundary.** v1 runs on one trusted machine for one user, who already holds
  every secret the agent can read.
- **Reliability.** Pattern-based secret scanning misses some secrets and masks some
  ordinary data, and it costs CPU on every body.

## Decision
Secrets inside prompts, tool inputs and tool results are stored as sent, neither
masked nor encrypted. The store protects them with file permissions: `capture.dir` is
`0700` and every file under it (`gateway.db`, its `-wal` and `-shm`, every blob) is
`0600`. Masking and encryption at rest are a later, opt-in feature. Header and query
redaction are unchanged: auth-bearing header values and adapter-declared secret query
parameters are always `[REDACTED]`, and no log line carries a header value, a body or
a query string.

## Alternatives
- **Scan with secret patterns and mask before storage.** Protects a leaked store, but
  the capture no longer shows what the model saw, the raw body stops being the wire
  bytes, and the patterns give both false negatives and false positives. Kept as the
  later opt-in.
- **Encrypt the store at rest.** Protects a copied disk or backup, but needs key
  management, and on a single trusted machine the key sits next to the data. Also
  kept as a later opt-in.
- **Don't store bodies at all.** Defeats the gateway's first priority, observability
  (PLAN.md §4).

## Consequences
- **Easier.** The stored content is exactly what was sent, so re-parsing, dedup and
  later audits work on true data, and capture adds no scanning cost.
- **Harder.** Anyone who can read `capture.dir` as the gateway's user, or a backup of
  it, can read every secret an agent ever saw. Sharing a database or a `gateway dump`
  output shares those secrets too.
- **Commits us to** keeping the `0700`/`0600` permissions tested (AC15), and to
  revisiting this decision before the store leaves the local machine: multi-user use,
  a remote database or a hosted deployment.
- **Reversing.** Adding masking or encryption later is additive, an opt-in step
  before storage. It can't reach back: content captured before it is enabled stays as
  stored until it is pruned or rewritten.
- PLAN.md §9 marks OQ-5 resolved, linking this ADR.
