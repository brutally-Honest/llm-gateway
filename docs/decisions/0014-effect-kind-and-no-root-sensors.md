---
status: approved
date: 2026-11-08
---

# 0014 — effect as a canonical kind, seen by checkpoints and a forward proxy

## Context
specs/014 shows what a session actually did beside what the model asked for: files that
changed on disk, files the harness's own tools read, and network connections. None of these
is part of an exchange, and none is a harness lifecycle event: a file change has no hook of
its own, and a connection is made by whatever program the harness started. The gateway must
see them with one pure-Go binary on Linux and macOS (`CGO_ENABLED=0`), with no root, no new
dependency and no cost while the harness is idle. Everything downstream reads canonical
events only (AGENTS.md, Agnosticism).

## Decision
Effects are a canonical kind, `effect`, held in `internal/core/effect.go` as `Effect`, with
three harness-neutral kinds: `file_change`, `file_read` and `net_conn`. Like `harness_event`
(ADR 0012) it is not a `StoredEvent` of an exchange and has its own table, `effects`, plus
`effect_snapshots` for each session's last checkpoint. Effects carry their own version per
session, so storing one never moves a session's timeline version.

Two sensors feed it, both in the gateway process and neither needing root:
- **Checkpoints.** Each stored hook event that carries a working folder (`HarnessEvent.WorkDir`,
  a new harness-neutral field) asks "what changed since the last checkpoint?" of the
  session's watched folders, with `git` run as a program (no optional locks, never a writing
  command) or a folder walk when there is no work tree. The snapshot is saved in SQLite in
  the same transaction as the effects, so a restart loses nothing.
- **A forward proxy.** An HTTP proxy (CONNECT and plain `http://`) and an optional SOCKS5
  proxy on loopback. The harness is pointed at them by a settings document the gateway
  prints; HTTPS is never decrypted.

A profile that implements the optional `core.EffectRules` interface names its read, search,
list and write tools, pulls a read's content out of its stored hook payload, and lists the
extra folders its harness's settings add. Read effects come from those stored hooks, not
from the disk.

## Alternatives
- **OS file watchers (inotify on Linux, FSEvents on macOS).** Rejected: two code paths;
  FSEvents needs cgo, which breaks the pure-Go build; inotify needs one watch per folder and
  races on new folders; bursts need coalescing. They would see every write with its exact
  time, which v1 does not need.
- **Sensors in the Docker container (bind mounts + inotify inside).** Rejected: every folder
  must be mounted ahead of time, Docker Desktop on macOS has a history of dropping file
  events on bind mounts, and a container cannot see host programs.
- **Store effects as `StoredEvent` kinds or as hook rows.** Rejected for the reasons of ADR
  0012: an effect belongs to no exchange, and writing it into the hook table would move the
  timeline version on every checkpoint.
- **OS read events or a "which program" lookup (/proc, lsof, Endpoint Security).** Rejected:
  Linux-only or admin-only, and very noisy.

## Consequences
- Downstream code (store, API, web UI) reads only `Effect` fields. A new harness gets file
  and read effects by implementing `EffectRules` and setting `WorkDir` in its mapper.
- Only the result between two checkpoints is seen, and only traffic sent through the proxy.
- The gateway must run on the host, not in Docker, for these sensors.
- The effect kinds are part of the canonical model; adding one needs a new ADR or a change
  to this one. Reversing the decision means dropping two tables.
