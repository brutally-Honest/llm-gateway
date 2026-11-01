---
status: proposed
date: 2026-11-01
---

# 0012 — harness_event as a canonical kind of its own

## Context
Claude Code reports lifecycle hooks beside the model exchanges: a tool about to run, a
tool finished or failed, a permission asked or denied, a turn ended, a subagent started
or stopped, a compaction. The gateway captures exchanges as canonical events
(`request`, `message`, `tool_call`, `tool_result`, `usage`, `error`), and everything
downstream reads only those. Hook events are not parts of an exchange, and they name
a client's own events, which the core must not know (AGENTS.md, Agnosticism). specs/010
needs them stored and joined to exchanges by session key.

## Decision
Hook events are a canonical kind, `harness_event`, held in `internal/core/harness.go` as
`HarnessEvent`. It is not a `StoredEvent` of an exchange and has its own table. Its
`HarnessPhase` values are harness-neutral words (`pre_tool`, `turn_end`, `unknown`, and so
on). A client profile that implements the optional `HarnessMapper` interface turns its
hook body into a `HarnessEvent`; the event-name mapping lives only in that profile
(`internal/clients/claudecode/`). A purity test fails any other package that names a
Claude Code hook event.

## Alternatives
- **Store hook events as `StoredEvent` kinds of the exchange.** Rejected: a hook has no
  request or response body and often arrives with no exchange at all. Forcing it into an
  exchange would make the exchange model carry lifecycle state.
- **Keep the Claude Code event names as the stored kind.** Rejected: the core would then
  know one client's vocabulary, and a second harness would need its own kinds.
- **Derive states in the store.** Rejected for this kind: the states are derived from
  stored rows by `internal/timeline`, not stored, so arrival order cannot change them
  (AC21) and the process stays stateless.

## Consequences
- Downstream code reads a harness-neutral phase. A new harness is a new profile
  implementing `HarnessMapper`, with its contract tests; the core stays unchanged.
- The phase list is part of the canonical model. Adding a phase is a schema-visible
  change and needs a new ADR or a change to this one.
- Reversing it means folding hook rows back into exchange events or dropping the table,
  which is a migration; the cost grows with the amount of stored hook data.
