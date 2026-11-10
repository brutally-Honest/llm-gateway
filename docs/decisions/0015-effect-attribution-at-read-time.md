---
status: approved
date: 2026-11-10
---

# 0015 — effect attribution: evidence stored, labels computed when served

## Context
specs/015 names, for each effect (ADR 0014), the tool call that most likely caused it. The
evidence is timing (tool calls' hook events, checkpoint windows, connection start) plus what
a tool call's input names. Claude Code sends its hooks asynchronously, so a hook can reach
the gateway after the tool it describes started or ended, and the rules are new and will
change as real sessions are read.

## Decision
- Five canonical labels in `internal/core/`: `named`, `only_one`, `several`, `outside`,
  `harness`. They say how sure the link is; `several` lists candidates and picks none.
- Evidence is stored with the effect when it is written: a file change's checkpoint window
  (`window_from`, `window_to`), a read's hook id, a connection's start and end; each hook row
  gains its working folder (`work_dir`).
- Labels are computed by the server each time a session's effects are served, from the
  session's hook rows and the inputs of candidate tool calls only. They are never stored and
  the browser never derives them.
- Tool names, input shapes, how a shell command names a path or host, and the harness host
  list live in the client profile behind the optional `core.AttributionRules`.

## Alternatives
- **Label at write time** in the checkpoint runner and the proxy recorder. Rejected for now:
  a hook still queued, or sent late by the harness, would fix a wrong label forever; every
  rule change would need a relabel migration; the open tool calls would have to be held in
  memory and rebuilt after a restart. Its gain, labels countable in SQL, is not needed yet.
- **Window as hook ids.** Rejected: with asynchronous hooks a tool can start during a scan
  before its pre-tool hook is stored; times with a configurable skew bound it soundly.

## Consequences
- A rule fix or a new harness host applies to every stored effect with no migration.
- Each effects request reads the session's hook rows (no payloads) and some tool inputs.
- A label shown live can settle on a later fetch when a hook lands after its effect.
- **Revisit** (switch to write time, the evidence columns stay) if the web UI needs counts or
  filters by label across sessions, or if the per-request cost shows in measurements.
