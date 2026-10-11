---
status: approved
date: 2026-10-11
---

# 0007 — The viewer is a local CLI first; a web UI comes later

## Context
PLAN.md OQ-7 asked what the viewer is, to be decided by Phase 3 (sessions & timeline).
Three options were on the table: a CLI (fast to build, greppable), a small local web UI
(better for long timelines and nested sub-agents), or Grafana panels only (no custom
code, but poor for reading conversations).

Phase 3 builds the timeline on the canonical event model, which has so far met only one
wire format (Anthropic's, through Claude Code; see ADR 0006). A web UI would freeze
layout and interaction choices before that model is proven stable. Grafana panels cannot
show a conversation as an ordered replay of prompts, tool calls and results.

## Decision
The viewer is a local CLI: `gateway sessions` lists sessions and `gateway timeline <key>`
replays one session as an ordered timeline, as text or JSON. It opens the capture store
read-only and needs no login, because it runs on localhost with the captured data.
A web UI is deferred until the canonical event model is stable.

The timeline package reads only canonical events and stored exchange columns, enforced by
a purity test. One known exception is accepted and recorded here: the text-block helper
reads the `text` string of a stored `text` block. That field name is wire-shaped, because
block content is stored as sent and has no canonical text field. The helper is limited to
one function with a fallback to the raw JSON. It is to be revisited when the second
protocol lands, since that protocol may name its text differently and the helper would
then need a canonical field or a per-protocol read.

## Alternatives
- **Small local web UI now.** Rejected: better for long timelines and nested sub-agents,
  but it freezes presentation before the event model has met a second protocol.
- **Grafana panels only.** Rejected: no custom code, but it cannot replay a conversation
  as an ordered sequence of prompts, tool calls and results.
- **Hosted UI with per-user access.** Rejected: out of scope for v1 (single user, no
  login), and it would expose captured content beyond localhost.

## Consequences
- **Easier:** the viewer ships with the reader and the store, with no front end, no extra
  route and no token handling. Output pipes into grep and jq.
- **Harder:** long timelines and nested sub-agents are read in a terminal. A web UI is a
  later addition, not a rewrite: the renderers read the same timeline entries.
- **Commits us to:** the text-block helper's wire-shaped read, until the second protocol
  is added and the helper is revisited (the ADR owner is the Phase that adds that
  protocol).
- **Reversing costs:** a web UI can be added on top of the timeline package without
  changing the store or the canonical events. Any route it adds must sit behind the
  gateway token rule if it is reachable from outside localhost.
