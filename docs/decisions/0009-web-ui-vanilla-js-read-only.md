---
status: approved
date: 2026-11-01
---

# 0009 — A read-only web UI in vanilla JS on its own loopback listener

## Context
ADR 0007 kept the viewer a CLI and deferred a web UI until the canonical event model was
stable. Since then the timeline (Phase 3) and token usage (Phase 4) have been built on
canonical events, and the Anthropic protocol has been the only wire format so far (ADR 0006).
Long timelines, tool calls far from their results, raw bodies behind a second command, and a
session that updates only on reload are all hard to read in a terminal. Spec 009 needs a page
served by the gateway, and its requirements (read-only, loopback only, no login, nothing it
does may block or fail proxying or capture) bind the design.

## Decision
The web UI is a page served by the gateway on its own listener (`ui.listen_addr`, loopback
only, default `127.0.0.1:7199`). It is built from:

- **Front end: vanilla JS.** ES modules, custom elements, `<template>`, `fetch`,
  `EventSource` and hash routing. There is no npm, no `node_modules`, no bundler and no build
  step. The files live in `internal/ui/web/` and are embedded with `go:embed`, so the binary
  stays one static file.
- **Back end: a GET-only JSON API under `/api` and an SSE stream for live updates.** Both read
  the capture store and the timeline package, and both answer only from canonical events and
  stored exchange columns. Nothing writes, deletes or edits captured data.
- **Access rules.** No login. The listener answers only loopback hosts and origins (Host and
  Origin checks, no CORS header). Captured content is rendered as text, and the page sets a
  strict Content-Security-Policy.
- **Fallback.** Preact with htm (no build, still no Node) is the recorded fallback, if vanilla
  JS becomes too costly to keep up. Switching means replacing the files in `web/`; the API and
  the stream do not change.

This supersedes [ADR 0007](0007-viewer-cli-first.md). The CLI viewer stays as it is, because it
reads the same timeline package.

## Alternatives
- **React or Preact with Vite.** Rejected: the build needs Node, which this repo does not have,
  and it adds a toolchain for one user on one machine.
- **htmx.** Rejected: it is a dependency that limits a rich timeline (per-entry state, replace
  by id, and the live merge), and vanilla JS does the same with no dependency.
- **Preact with htm, no build, as the first choice.** Not chosen first: it is a dependency
  that vanilla JS does not need yet. It stays the fallback above.
- **Grafana panels only.** Rejected again (see ADR 0007): they cannot replay a conversation.
- **A hosted UI with login.** Rejected for v1: single user, local only (ADR 0010), and it would
  expose captured content beyond localhost.

## Consequences
- **Easier:** no build step and no Node; the UI ships in the same binary; the page reads the
  same timeline entries as `gateway timeline`, so the two cannot drift apart silently.
- **Harder:** there is no component framework, so rendering and state are written by hand. The
  repo has no JS test framework (spec 009 non-goal); the front end is checked by the Go tests
  of the served files and the API, and by a manual check (T017).
- **Commits us to:** a UI with no auth on loopback only. Any route that becomes reachable from
  outside localhost must sit behind the gateway token (AGENTS.md hard rules). The UI does not
  get that route in v1.
- **Reversing costs:** moving to Preact with htm changes only `internal/ui/web/`. Moving to a
  bundled framework would add a build step, which needs its own ADR.
