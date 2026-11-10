---
feature: 015-effect-attribution
status: done
version: 1
approved:
approved-by:
sha256:
fingerprint:
spec-fingerprint:
---
# Plan — 015-effect-attribution

## Summary
Store evidence when effects are written, assign labels when a session's effects are served.
Each file change gains its checkpoint window (two times); each hook row gains its working
folder. The effects API reads the session's hook rows, builds one span per tool call, finds
each effect's candidates by time, and asks the Claude Code profile whether a candidate's input
names the target or whether a host is Claude Code's own. The browser shows the label it is
given. Built outside the agent-loop kit like 014 (research.md D1).

## Context
- Checkpoints: `internal/effects/runner.go` (`OnStored`, one running + one dirty per session,
  `session` state), `checkpoint.go` (`now` taken once per checkpoint and used as every
  effect's `At`; a checkpoint with no change saves nothing).
- Reads: `internal/effects/reads.go` (a `file_read` effect carries `HookID`).
- Network: `internal/netproxy/record.go` (`At` = start, `EndedAt` = end, session from the
  runner's active set).
- Hooks: `internal/clients/claudecode/harness.go` (`MapHarness`, `WorkDir` from `cwd`),
  `snippet.go` (http hooks with `"async": true`: Claude Code does not wait for them),
  `internal/store/hooks.go` (`SaveHook`), `reader_sessions.go` (`HookRow`, `HookEvents`).
- Store: `internal/store/effects.go` (`insertEffects`, `effectSelect`, `EffectRow`),
  `migrate.go`.
- API: `internal/ui/api_effects.go` (`effectJSON`, `listEffects`, `listUnassigned`),
  `d.Rules[client]` (profile, type-asserted to optional interfaces).
- Web: `internal/ui/web/effects.js` (glyph helpers), `timeline-page.js` (effects rows),
  `model.js` (`callNumbers`: `#n` is a model call's position).
- Profile: `internal/clients/claudecode/effects.go` (`ReadTarget`, `WritePaths`),
  `rules.go` (`IsAgentTool`).
- Seed: `docs/manual-checks/seed-effects.mjs` (`tool()` sends Pre then Post back to back).

## Approach
- **Evidence at write time.** File changes: `window_from`, `window_to` (unix ns) on the
  effect. Reads: the existing `hook_id`. Connections: the existing `at` and `ended_at`. Hook
  rows: a new `work_dir` column.
- **Labels at read time.** `internal/effects/attribute.go` is a pure function:
  `Attribute(hooks []store.HookRow, effects []store.EffectRow, rules core.AttributionRules,
  inputs InputFn, skew time.Duration) []Attribution`. The API calls it on every effects
  page; the unassigned list uses `Unassigned(e, HarnessLookup)` (Q5).
- **Rules in the profile.** A new optional `core.AttributionRules` interface; Claude Code
  implements it in `internal/clients/claudecode/attribution.go`.

## Alternatives considered
### A1 — Store evidence, label on the server per request (chosen)
- Pros: every hook ever stored is used, including ones that arrive late (async hooks, a
  queued sink, Post after the connection ended); a rule fix or a new harness host applies
  to all stored effects with no migration and no backfill; nothing to repair after a
  restart; the stored rows stay facts, not opinions.
- Cons: each effects request reads the session's hook rows and some tool inputs; a label
  shown live can change on the next fetch if a hook received before the effect was stored
  only lands afterwards (milliseconds, see KI in research.md); labels cannot be counted in
  SQL.
### A2 — Label at write time, in the runner and the proxy recorder
- Pros: a label is a stored fact; reads cost nothing extra; labels can be counted and
  filtered in SQL, which 016 might want.
- Cons: it needs the runner to hold the open tool calls in memory (lost on restart, rebuilt
  from the store); hooks still in the sink queue are invisible, and a label computed from them
  is wrong forever; every rule change needs a relabel migration; the label of an effect seen
  before its tool's Pre hook arrived (async) is wrong unless the recorder waits.
### Verdict
Fact 2 of the brief holds with a correction: write-time labelling is not blocked by the late
Post (a call with no Post yet can count as running), but by the late *Pre* and by rule churn.
While the rules are new and the harness host list is unverified, A1 wins. **Flip to A2** if
016 needs counts or filters by label over many sessions, or if the per-request cost shows up
in the speed follow-ups; the evidence columns make that a later migration, not a redesign.

## Design
### Core (`internal/core/effect.go`)
```go
type Attribution string
const (
    AttrNamed   Attribution = "named"
    AttrOnlyOne Attribution = "only_one"
    AttrSeveral Attribution = "several"
    AttrOutside Attribution = "outside"
    AttrHarness Attribution = "harness"
)

// AttributionRules is an optional profile interface for effect attribution.
type AttributionRules interface {
    // NamesTarget reports whether a tool call's input names target: an absolute path or a
    // host. workDir is the folder the call ran in.
    NamesTarget(tool string, input []byte, workDir, target string) bool
    // HarnessHost reports whether a connection to host is the harness's own traffic.
    HarnessHost(host string) bool
    // IsAgentTool reports whether a tool spawns a sub-agent.
    IsAgentTool(tool string) bool
}
```
`Effect` gains `WindowFrom, WindowTo time.Time`. ADR 0015 records the label set, the
read-time choice (A1 vs A2) and the flip condition.

### Store
One migration:
```sql
ALTER TABLE effects ADD COLUMN window_from INTEGER;
ALTER TABLE effects ADD COLUMN window_to   INTEGER;
ALTER TABLE hook_events ADD COLUMN work_dir TEXT;
```
`insertEffects`, `effectSelect`, `EffectRow` and `SaveHook`, `HookRow` carry them. Nothing
else in `effects` or `hook_events` changes.

### Runner (AC1)
- `session` gains `prevStart time.Time` and `lastHookAt time.Time` (the receive time of the
  hook before the newest one, kept in `OnStored`).
- `checkpoint` takes `start := now()` before the scan and `end := now()` after it; every file
  effect gets `WindowFrom = prevStart` (or, when zero, the hook time before the trigger) and
  `WindowTo = end`. `prevStart = start` is kept in memory after every checkpoint, including
  ones that save nothing; no extra write.
- On a load from the store (restart, eviction) `prevStart` is zero and the fallback applies
  (E7). Fallback read: `LastHookBefore(session, triggerID)`, one indexed query, only on load.

### Labelling (`internal/effects/attribute.go`, AC3–AC10)
1. **Spans.** Walk the session's hook rows (already ordered by receive time). Per tool-call
   id: `start` = earliest receive time of any of its rows, `end` = receive time of its
   post-tool, failed or denied row; `agent` = its agent id. Calls with no end: closed by the
   next prompt, turn end, turn failure or session end received after `start`, or for a call
   with an agent id by that agent's agent-end row; else open. Widen: `start − skew` only
   (an end hook is sent after the tool finished; L1). Drop calls whose tool `IsAgentTool`.
2. **Candidates.** Sort spans by start; for each effect, a binary search plus a scan of spans
   that started before the effect's upper bound. File change: overlap with
   `[window_from, window_to]`. Connection: span holds `at`. Read: the call of `hook_id`.
3. **Label.** The candidates whose input `NamesTarget` the effect's path (file) or host
   (connection): exactly one → `named` (named beats harness). Else a connection with
   `HarnessHost(host)` → `harness`. Else one candidate → `only_one`; two or more →
   `several`; none → `outside`. A read → `named` with its own call. No rules (AC10): skip
   named and harness. A Bash call can only be `named` while it is a candidate, i.e. its
   widened span overlaps the change's window (answer 4).
4. **Inputs.** `InputFn(hash)` reads a candidate's tool input from content storage, cached per
   request by hash; only candidates' inputs are read.
5. **Unassigned connections:** `harness` or no label (AC9). An effect with no window and no
   hook id (E13): no label.

### Claude Code rules (`internal/clients/claudecode/attribution.go`, AC5, AC6)
- `NamesTarget`:
  - Write, Edit, MultiEdit, NotebookEdit: `WritePaths` made absolute against `workDir`,
    equal to target.
  - Read, Grep, Glob: name nothing (they cannot change a file; a read effect already
    points at its hook; L2).
  - WebFetch: the `url` input's host equals target.
  - Bash: the `command` holds the target as a whole token: the absolute path, the path
    relative to `workDir`, or the host (also inside a URL). Plain substring checks on token
    boundaries; no shell parsing.
- `HarnessHost`: exact names, plus suffix entries written with a leading dot. Start list
  (research D4): `downloads.claude.ai`, `http-intake.logs.us5.datadoghq.com`,
  `api.anthropic.com`, `mcp-proxy.anthropic.com`; not `registry.npmjs.org`. Rechecked
  against the T003 data.
- `IsAgentTool`: the existing method.

### Config (`internal/config/effects.go`)
```yaml
effects:
  attribution:
    # How far a tool call's span start is moved back: hooks are sent asynchronously,
    # so a pre-tool hook can reach the gateway after the tool started (L1: end not widened).
    # Env: GATEWAY_EFFECTS_ATTRIBUTION_SKEW
    skew: 50ms
```
Validation: 0 ≤ skew ≤ 5s.

### API (AC11)
`effectJSON` gains:
```json
"attribution": {
  "label": "several",
  "tool": {"id": "toolu_…", "name": "Bash", "agent_id": ""},
  "candidates": [{"id": "toolu_…", "name": "Bash"}, {"id": "toolu_…", "name": "Read"}]
}
```
`tool` only for `named` and `only_one`; `candidates` only for `several`; `client` only for
an unassigned `harness`; the field is absent when there is no label. `listEffects` reads
`HookEvents` for the session once per request. `listUnassigned` needs no hooks: it calls
`effects.Unassigned` with `Deps.harnessLookup`, which asks every registered profile's
`core.AttributionRules.HarnessHost`, the row's own client first, then the rest by name
(Q5: unassigned rows are stored under client `unknown`, which has no rules).

### Web UI (AC12)
- `effects.js`: `attributionText(a, callNo)` → glyph + word, then the tool text. Glyphs:
  `● named`, `◐ only one running`, `◌ several running`, `○ outside any tool call`,
  `⚙ harness` (K3).
- The tool text is `#n Name`, where `#n` is the number of the model call that asked for that
  tool call (`callNumbers` over the timeline entries the page already holds, matched by
  tool-call id); a tool call with no captured model call shows its name only (E11).
- `timeline-page.js` and the unassigned page append that text to each effects row as one
  more text cell. No new section, no layout change.

### Seed (`docs/manual-checks/seed-effects.mjs`)
A new `--attribution` scenario (default on) that, in its own turn, produces every label:
- named: Edit of `src/app.js` (path in input) and a Bash `curl http://127.0.0.1:<port>/x`
  connection through the proxy;
- only one running: a Bash `node gen.js` whose command does not name the file it writes;
- several running: two parallel calls (Pre A, Pre B, change, Post A, Post B), plus a sub-agent
  (SubagentStart, sub-agent Bash with `agent_id` overlapping a main-thread Grep, SubagentStop);
- outside: a file change after Stop and before the next prompt;
- harness: a CONNECT to `downloads.claude.ai:443`, closed at once (needs internet).
Each step waits for the checkpoint to land (`--pause`) and prints which label each effect
should get.

## AC coverage
| AC | Where | Verified by |
|---|---|---|
| AC1 | `effects/runner.go`, `checkpoint.go`, `store/effects.go` | `TestCheckpoint_StoresWindow` |
| AC2 | `claudecode/harness.go`, `store/hooks.go` | `TestAttribute_Labels` (rows built from stored hooks) |
| AC3 | `effects/attribute.go` | `TestAttribute_Labels` (interrupted call, sub-agent end, skew) |
| AC4 | `effects/attribute.go` | `TestAttribute_Labels` |
| AC5 | `attribute.go`, `claudecode/attribution.go` | `TestClaudeCode_NamesTarget`, `TestAttribute_Labels` |
| AC6 | `attribute.go`, `claudecode/attribution.go` | `TestClaudeCode_NamesTarget`, `TestAttribute_Labels` |
| AC7–AC9 | `attribute.go` | `TestAttribute_Labels` |
| AC10 | `attribute.go` | `TestAttribute_Labels` (nil rules case) |
| AC11 | `ui/api_effects.go` | `TestEffectsAPI_Attribution` |
| AC12 | `web/effects.js`, `timeline-page.js` | `effects.test.js: attributionText`; manual check |

## Test strategy
Functional tests only, one or two per part (D1), written together near the end:
- `TestCheckpoint_StoresWindow`: two checkpoints in a temp git repo; the second's effect
  carries `[start of first, end of second]`; a reloaded runner falls back to the last hook.
- `TestAttribute_Labels`: one table over a hand-built session of hook rows and effects that
  yields each label, an interrupted call, a background sub-agent, Post-before-Pre, skew, and
  nil rules.
- `TestClaudeCode_NamesTarget`: Edit/Write/Read/Grep/Glob/WebFetch/Bash fixture inputs,
  relative and absolute paths, a host inside a URL, a near-miss (`a.txt` vs `a.txt.bak`).
- `TestEffectsAPI_Attribution`: the API returns `attribution` with tool and candidates and
  leaves it out for an unlabelled effect.
- `effects.test.js: attributionText`.
- Manual: `docs/manual-checks/015-attribution.md` via `/ui-check`: the seed in a real browser,
  every label visible with glyph + word and `#n Name`, plus one real Claude Code session
  (edit, Bash script writing files, `curl`, parallel reads, a sub-agent).

## Risks
- **Skew too small** → a fast `curl` starts before its Pre hook is received and reads
  `outside`. Measured in T002 on fresh data; `skew` is config.
- **Skew too large** → sequential calls in one batch overlap and give `several`. Same
  measurement; `named` resolves most of these.
- **Bash token matching** is shallow → misses after `cd`, globbing, variables; it only
  ever falls back to timing labels, never to a wrong `named` for a non-matching path.
- **Harness host list drifts** with Claude Code releases → one list in the profile; the
  fresh-data check is repeated in the manual check.
- **Per-request cost** on a long session → hook rows only (no payloads), inputs only for
  candidates; re-measured in the post-016 speed follow-ups.

## Open questions

## Changelog
- v1 (2026-11-10) — first draft
- v1 (2026-11-10) — Karthik's answers folded in: named beats harness, four start hosts,
  Bash paths named only as whole words inside the call's window
- v1 (2026-11-10) — skew default 50ms, start only, from the T003 measurements (research.md)
- v1 (2026-11-10) — unassigned connections: `Unassigned` over every registered profile's
  harness list, naming the client (research.md Q5)
