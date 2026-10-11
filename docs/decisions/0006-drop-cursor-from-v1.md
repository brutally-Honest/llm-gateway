---
status: approved
date: 2026-10-11
---

# 0006 — Cursor is dropped from v1; Claude Code is the sole first client

## Context
PLAN.md named two first clients, Claude Code and Cursor. Cursor carried Phase 3, the
"agnosticism proof": an OpenAI Chat Completions adapter and a Cursor profile had to
produce the same canonical events as Claude Code before the timeline was built, so the
timeline could not bake in Anthropic's shape.

Cursor was costly to support and only partly observable:
- Cursor calls the custom base URL **from its own servers**, so the gateway needed a
  public HTTPS tunnel (OQ-3) and gateway token auth on every tunnelled route.
- Tab autocomplete and inline edit (Cmd+K) always use Cursor's own backend, so the
  gateway never sees them, and whether agent mode and sub-agents go through the custom
  base URL was unconfirmed (OQ-6).

Meanwhile Claude Code is not finished end to end: sessions, timeline, cost and rate
limiting are still ahead. The priority is one harness done well before a second one.

## Decision
Cursor is removed from v1, and Claude Code is the sole first client until it is
finished end to end. The old Phase 3 (OpenAI Chat Completions + Cursor) is deleted and
the later phases move up by one. Its job, keeping the timeline protocol-agnostic, moves
into the new Phase 3 (sessions & timeline) as a purity test: the timeline package reads
only canonical events and never names an Anthropic wire type. Codex (OpenAI Responses)
becomes the first real second protocol, later. OQ-3 and OQ-6 are withdrawn, not
deleted, so their IDs are never reused.

## Alternatives
- **Keep Cursor as Phase 3.** Rejected: it front-loads a tunnel, gateway token auth and
  a partly visible client before the first client is finished.
- **Keep the phase numbers and mark Phase 3 dropped.** Rejected: a permanent gap in
  the phase table that every later reference has to explain.
- **Swap Cursor for another OpenAI Chat client as Phase 3.** Rejected: no such client is
  in daily use, so it would be a protocol built for a test, not for observability.

## Consequences
- **Given up:** a real second protocol before the timeline. The canonical event model
  has met only Anthropic's wire format when the timeline is built on it, so some
  Anthropic shape could still slip into the model itself.
- **Mitigation:** ingestion is shown agnostic by 001 AC35 (a test-only adapter and
  profile proxy and label with no core change) and 002 AC59 (a test-only parser yields
  canonical events with no core change). Consumption is shown agnostic by the new
  Phase 3 timeline purity test.
- **Easier:** no public exposure in v1, so gateway token auth and a tunnel are not on
  the critical path. The "no route reachable from outside localhost without a gateway
  token" rule stays and applies whenever a route is first exposed.
- **Unchanged:** `internal/core/` still bans client names, `cursor` included, whatever
  clients are planned.
- **Revisit** when Codex or another OpenAI-protocol client is added. Cursor itself
  returns as a new profile on an OpenAI Chat Completions adapter, listed under "Later"
  in PLAN.md §7. Reversing costs one phase and reopens OQ-3 and OQ-6.
