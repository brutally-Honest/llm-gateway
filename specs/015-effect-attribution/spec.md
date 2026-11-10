---
feature: 015-effect-attribution
kind: feat
status: draft
version: 1
created: 2026-11-10
supersedes:
branch: feat/015-effect-attribution
approved:
approved-by:
sha256:
fingerprint:
---
# Effect attribution

## Problem
014 shows what happened on disk and on the network, placed in turns by time only. Reading a
turn with three tool calls and five changed files, the user cannot tell which call changed
which file, which `curl` opened which connection, or whether a change was their own editor
edit between prompts. Claude Code's own traffic (update downloads, telemetry) sits among the
connections Claude's tools made. The user has to guess from timestamps.

This changes earlier features:
- **014 non-goal "Who caused an effect: no link from a file change or connection to a tool
  call, sub-agent or program, and no confidence levels"**: this feature adds the link, as a
  label saying how sure it is. It still never names a program (no /proc or lsof lookup).
- **014 KI6 ("No 'who did it'")**: replaced by the labels below; what they cannot see
  stays a known issue (research.md).
- **014 AC14 rows**: each effect row gains its label and tool as text. Nothing else in the
  layout changes; 016 redoes the UI.

## Goal
- Every file change, file read and network connection of a session carries one label, shown
  as glyph + word, naming the tool call that most likely caused it when there is one:
  **named**, **only one running**, **several running** (candidates listed, none picked),
  **outside any tool call**, or **harness**.
- Labels are computed by the server from stored evidence, so a rule fix applies to every
  stored effect without a migration, and the browser only displays them.

## Non-goals
- Proof: a label is the most likely tool, not a guarantee (background processes, see E3).
- Naming the program, process or child process behind an effect.
- Picking one tool when several were running and none names the target.
- Labels on the model-call or tool-call side (no "this tool caused N effects" view).
- Backfill: effects stored before this feature are not relabelled; the store starts empty.
- Layout changes in the web UI, filters by label, counts per label (016).
- Harness file activity (Claude Code writing its own `.claude/` files) as `harness`.
- Other harnesses' rules; the seam is there, only Claude Code fills it.

## Acceptance criteria
### Evidence stored with effects
- **AC1** — When a checkpoint stores file changes, the system shall store with each its
  window: from the start of the session's previous checkpoint (or, when the runner has none
  in memory, the receive time of the session's last hook event before the one that triggered
  this checkpoint) to the end of this checkpoint's scan.
- **AC2** — When a hook event is stored, the system shall also store its working folder, so
  a tool call's input can be read against the folder it ran in.

### Tool spans and candidates
- **AC3** — When it labels a session's effects, the system shall take each tool call's span
  from its hook events paired by tool-call id: from its pre-tool event to its post-tool or
  failed event; a denied call ends at its denial; a call with no end event ends at the
  session's next prompt, turn end, turn failure or session end, or for a sub-agent call at its
  agent's end; with none of those it is still running. Both ends are widened by
  `effects.attribution.skew` because hooks are sent asynchronously.
- **AC4** — When it picks candidates, the system shall use, for a file change, every tool call
  whose span overlaps the change's window; for a connection, every tool call whose span holds
  the connection's start; for a read, the tool call of the hook event it came from. A tool
  call that spawns a sub-agent (as the client profile names it) is never a candidate.

### Labels
- **AC5** — When exactly one candidate's input names the effect's target, as the client
  profile reads it (a write tool's path, a read tool's target, a fetch tool's URL host, a path
  or host appearing as a whole word in a shell command, a relative path resolved against the
  call's stored working folder), the system shall label it `named` with that tool call; a
  read is always `named` with its own tool call. No shell parsing.
- **AC6** — When no candidate names a connection's host and the host is on the client
  profile's harness host list, the system shall label it `harness` with no tool.
- **AC7** — When neither AC5 nor AC6 applies and there is exactly one candidate, the
  system shall label it `only_one` with that tool call.
- **AC8** — When neither AC5 nor AC6 applies and there are two or more candidates,
  the system shall label it `several` with every candidate listed and no tool picked.
- **AC9** — When there is no candidate, the system shall label it `outside`; an unassigned
  connection is `harness` (AC6) or carries no label.
- **AC10** — When a client's profile has no attribution rules (an unknown client), the system
  shall label its effects from timing only (`only_one`, `several`, `outside`), never `named`
  or `harness`.

### API and web UI
- **AC11** — When `GET /api/sessions/{id}/effects` or `GET /api/effects/unassigned` returns
  effects, the system shall give each one an `attribution` field holding the label, the
  picked tool call (id, name, agent id) and the candidates, computed on that request and
  never stored.
- **AC12** — When the web UI shows an effects row, the system shall show the label as glyph +
  word and its tool as text (`#n Bash`, every candidate for `several`), with no other change
  to the row or the page layout.

## Edge cases
- **E1** — Post-tool hook received before its pre-tool hook (async delivery) → paired by id;
  the span runs from the earlier to the later receive time (AC3).
- **E2** — Tool interrupted with Esc, no post-tool hook → its span ends at the next turn end or
  prompt, so it is not "running" for the rest of the session (AC3).
- **E3** — A background Bash command or dev server changes a file after its tool call ended →
  labelled by whatever was running then (`outside` or a wrong tool); a known limit (Non-goals).
- **E4** — Background sub-agent: the Agent call's post-tool hook fires before its sub-agent's
  tools run → the sub-agent's tool calls carry their own spans; the Agent call is never a
  candidate (AC3, AC4).
- **E5** — Two parallel tool calls both name the changed path → `several`, both listed (AC8).
- **E5a** — A Bash command names a path but its span does not overlap the change's window →
  it is not a candidate, so it is not `named`; timing labels apply (AC4, AC5).
- **E6** — A checkpoint triggered by a tool's own pre-tool hook already sees that tool's change
  (async hooks) → the tool is still a candidate, because the window ends at the scan's end
  (AC1, AC4).
- **E7** — First checkpoint after a gateway restart or a memory eviction → window starts at the
  session's last hook before the trigger; changes made while the gateway was down are
  `outside` unless a tool call spans the gap (AC1, AC9).
- **E8** — Grep or Glob over the working folder (empty `path`) → `named`, its own tool (AC5).
- **E9** — A shell command naming a path relative to its working folder → `named`; after a `cd`
  inside the command the path may not match, and the label falls back to timing (AC5, AC7).
- **E10** — A connection to a harness host while a tool call runs → `harness`, unless that
  call's input names the host (`curl https://api.anthropic.com/…`), then `named` (AC5, AC6).
- **E11** — A tool call in a hooks-only session (no captured model call) → its tool shows by
  name without `#n` (AC12).
- **E12** — A connection that starts after the session ends → `outside` (AC9).
- **E13** — An effect with no window (stored by a build before this feature) → no label (AC11).

## Constraints
- Labels are computed per effects request from hook rows (no payloads) and the inputs of
  candidate tool calls only; the effects request stays O((effects + hooks) · log) and the
  timeline build does not read effects (014 AC13 unchanged).
- Hook ingest p99 (010 AC3) unchanged: nothing new on the ingest path except one more column.
- No new dependency. Two nullable columns, no new table.
- The effect version and `SessionVersions` keep their meaning; the timeline cache is not
  touched.
- Labels never change stored effects.

## Agnosticism check
- Client axis: the tool names, their input shapes, how a shell command names a path or host,
  and the harness host list live only in `internal/clients/claudecode/`, behind a new optional
  profile interface in `internal/core/` (like `EffectRules`).
- `internal/core/` gains the label values and that interface; it names no client. The
  labelling pass reads canonical hook rows (phase, tool-call id, tool name, agent id, receive
  time, working folder) and canonical effects only.
- Protocol and provider axes: untouched.

## Open questions

## Changelog
- v1 (2026-11-10) — first draft
- v1 (2026-11-10) — approved by Karthik with his answers folded in: `named` beats `harness`
  (AC5/AC6 swapped), Bash paths count as `named` only as whole words inside the call's
  window (research.md, answers 2026-11-10)
