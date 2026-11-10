---
feature: 015-effect-attribution
---
# Research & decisions — 015-effect-attribution

## Open questions
<!-- Agents add a question here when they are blocked; the loop stops until you answer.
     Answer by changing "(open)" to "(answered)" and writing the answer underneath:
- **Q1** (open) T003 — <question> — options: A) … B) …
-->

## Decisions
- D1 (2026-11-10) — Built outside the agent-loop kit, like 014: one branch, feature tasks
  first, the seed early, functional tests together near the end (one or two per part),
  `make verify` green after every commit, leaks patched as found and logged here as dated
  lines; no `/al-*` commands, no `loop.sh`, no approval fingerprints — Karthik's choice.
- D2 (2026-11-10) — Evidence is stored at write time, labels are computed on the server per
  effects request, never in the browser and never stored (plan.md A1 vs A2): late hooks and
  rule churn favour read time; flip to write time if 016 needs counts or filters by label.
- D3 (2026-11-10) — Labels are canonical values in `internal/core/` (`named`, `only_one`,
  `several`, `outside`, `harness`); tool names, input shapes and the harness host list stay
  in `internal/clients/claudecode/` behind the optional `core.AttributionRules`.
- D4 (2026-11-10) — Start harness host list: `downloads.claude.ai` and
  `http-intake.logs.us5.datadoghq.com` (014 step 9), to be confirmed or extended from fresh
  data in T003.
- D5 (2026-11-10) — Replaces D4's list: the start harness host list is `downloads.claude.ai`,
  `http-intake.logs.us5.datadoghq.com`, `api.anthropic.com` and `mcp-proxy.anthropic.com`;
  not `registry.npmjs.org`. It lives in the Claude Code profile — Karthik's answer 1.
- D6 (2026-11-10) — `named` beats `harness`: when a candidate's input names the host, the
  connection is `named`, not `harness` (spec AC5/AC6 swapped from the draft) — answer 1.
- D7 (2026-11-10) — A Bash path is `named` for a file change only as a whole word in the
  command (relative paths resolved against the stored `work_dir`) and only when the change's
  window overlaps that call's span widened by the skew; no shell parsing; otherwise timing
  labels — answer 4 (refines K6).
- D8 (2026-11-10) — T003's fresh session is run by Karthik from a prompt and commands
  given to him; the skew is set from its measurements — answer 2.

## Decisions for Karthik
Choices made while drafting that you were not asked about. Each has a recommendation; say if
you want one changed.
- K1 — The checkpoint window is stored as two **times** (previous checkpoint's scan start →
  this checkpoint's scan end), not as two hook ids. Hooks are async (see N1), so a hook-id
  window can miss a tool that started during the scan. Recommended: times.
- K2 — The window ends at the scan's **end**, not its start. Ending at the start is tighter
  but can confidently name the wrong tool when a tool starts mid-scan; ending at the end can
  only add a candidate (`several`). Recommended: scan end.
- K3 — Glyphs: `● named`, `◐ only one running`, `◌ several running`,
  `○ outside any tool call`, `⚙ harness` (filled to empty = sure to unsure; none clash with
  014's `+ ~ − → ✓ ✕ ! …` or the chips).
- K4 — A tool call that spawns a sub-agent (`Agent`) is never a candidate: its sub-agent's
  own tool calls carry the spans, and for a background agent the Agent call's Post fires
  before they run (010 research). An effect while only a sub-agent is thinking reads
  `outside`. Recommended: drop it.
- K5 — `#n` in `#n Bash` is the number of the model call that asked for the tool call, as on
  the timeline. A tool call with no captured model call shows only its name. Recommended.
- K6 — A Bash command naming the changed **path** (absolute, or relative to its working
  folder, as a whole token) counts as `named` for file changes too, not only hosts for
  connections. Recommended: yes.
- K7 — `hook_events` gains a `work_dir` column so relative paths in tool inputs can be
  resolved without reading hook payloads. The other way is reading each candidate's payload.
  Recommended: the column (the store starts empty, no backfill).
- K8 — `effects.attribution.skew` (default 250 ms until T003 measures it) widens each tool
  span at both ends. Recommended: config, not a constant.
- K9 — An unassigned connection is `harness` or unlabelled, never `outside`: with two active
  sessions "outside" would be a guess. Recommended.
- K10 — The user's rule is kept as written: one candidate that does not name the target is
  `only_one` even when that candidate is an Edit of another file. A refinement ("a precise
  tool that names something else is not a candidate") is possible later. Recommended: keep.
- K11 — ADR 0015 records the label set and the read-time choice, since 016 builds on it.
- K12 — The previous checkpoint's start lives in memory only (no extra write per checkpoint);
  after a restart or eviction the window starts at the session's last hook before the
  trigger (E7). The other way is one small write on every checkpoint. Recommended: memory.

Karthik's answers (2026-11-10):
- Q1 → A, with `api.anthropic.com` and `mcp-proxy.anthropic.com` added (D5) and named beating
  harness (D6).
- Q2 → A: Karthik runs the T003 session (D8).
- Q3 → `#n` is the model call that asked for the tool (K5 as recommended).
- Q4 → yes, with the whole-word and window conditions (D7).
- K1–K12 → accepted; none conflicts with the answers (K6 refined by D7). Spec, plan and tasks
  approved with these folded in; build outside the kit (D1), no push.

## Notes

### Leaks found during the build
- L1 (2026-11-10) — T007: widening a span's end by the skew made the previous sequential
  tool a candidate of the next change (the seed's `out/gen.txt` read `several` instead of
  `only_one`): the next checkpoint's window starts within milliseconds of that tool's
  PostToolUse. An end hook is only sent once the tool has finished, so its receive time is
  already after the real end. Fixed: only the start moves back by the skew; spec AC3 and
  plan.md say so.
- L2 (2026-11-10) — T006: Grep and Glob "named" every file under their folder, so a
  sub-agent's change while a main-thread Glob ran read `named Glob` instead of `several`.
  Read, Grep and Glob cannot change a file, and a read effect is already linked by its hook
  id. Fixed: they name nothing in `NamesTarget`; plan.md says so.
- L3 (2026-11-10) — T002: the seed's "several" step wrote `build/out.bin`, and `build` is on
  the default ignore list, so no effect was seen. Fixed: `artifacts/out.bin`.
- 2026-11-10 — The seed against a throwaway gateway (temp data folder, ports 1719x) after
  L1–L3: every turn-4 effect got the label the seed prints. Turns 1–3 send each tool's Pre
  and Post back to back, so their changes read `several` or `only_one` by timing; expected.

- 2026-11-10 — Verify of b71e18b in a worktree failed once in an untouched package:
  `TestProxy_GoldenAnthropicStreamReplay` "reading the replayed stream: unexpected EOF". It
  passed 3/3 alone and the full verify of that commit passed on a rerun. Not caused by 015;
  noted as a possible flake.
- 2026-11-10 — T009 mutation check: `several` turned into `only_one` (`len(cands) >= 1`),
  `TestAttribute_Labels` failed on "several running", restored.

- 2026-11-10 — T010 `/ui-check` of 015-attribution.md steps 3–6: NOT EXERCISED, the Chrome
  extension was not connected. The same labels were read from the API of the throwaway
  gateway (see the L1–L3 line above); the browser rendering is still unchecked. Step 7 (real
  session) is by hand.

### Brief facts, checked (2026-11-10)
- F1 — Holds. `Runner.OnStored` signals a checkpoint for every stored hook with a session and
  a `WorkDir` (Pre and Post tool hooks included); `signalLocked` coalesces to one running and
  one dirty; file effects carry no hook id and `At` is the checkpoint's start (`now` in
  `checkpoint`); only `file_read` effects have `HookID`. Correction: a checkpoint that finds
  no change saves nothing, so the previous checkpoint's start must be kept in memory (K12),
  and the window is better kept in times than hook ids (K1).
- F2 — Holds: `net_conn` has `At` (start) and `EndedAt`; a tool's PostToolUse can be
  received after the connection ends. Correction: this alone does not block write-time
  labels (a call with no Post yet counts as running); the late PreToolUse (N1) and rule churn
  do. Read time chosen (D2).
- F3 — Holds: tool calls pair by `tool_use_id`; `PostToolBatch` maps to
  `PhaseToolBatchDone` (no tool id, not a span end); sub-agent tool events carry `agent_id`
  and `agent_type`, `SubagentStart`/`SubagentStop` carry `agent_id` and no `tool_use_id`, and
  main-thread events carry none (010 research, 2026-11-01). For a background sub-agent the
  Agent call's PostToolUse fires before the sub-agent's tools run.
- N1 — The printed hooks are http hooks with `"async": true`
  (`internal/clients/claudecode/snippet.go`): Claude Code does not wait for them. A hook's
  receive time can trail the tool's real start or end, and a fast tool's Pre and Post can
  arrive close together or out of order. Hence the skew (K8) and the scan-end window (K2).
- N2 — `hook_events` stores no working folder (`SaveHook` writes no `cwd`); `HarnessEvent`
  has `WorkDir` in memory only. Hence K7.
- N3 — Hosts in the store before the clear (2026-11-10, 18 sessions): on sessions,
  `http-intake.logs.us5.datadoghq.com` ×4 and `api.github.com` ×1; unassigned,
  `api.anthropic.com` ×21, `mcp-proxy.anthropic.com` ×4,
  `http-intake.logs.us5.datadoghq.com` ×3, `downloads.claude.ai` ×2, `registry.npmjs.org` ×1.
  Most of Claude Code's own traffic lands unassigned (start-up, before the first hook).

### Known issues accepted for v1
- KI1 — Background work (a Bash `run_in_background`, a dev server, a watcher) is labelled by
  whatever ran when its effect was seen: `outside` or a wrong tool.
- KI2 — Bash `named` matching is token-based: a `cd`, a variable, a glob or a script that
  builds the path hides the name; the label then falls back to timing.
- KI3 — A label shown live can change on the next fetch when a hook received before the effect
  was stored is saved after it (milliseconds); a reload shows the settled label. Older rows
  are not refetched live.
- KI4 — 014 KI1 still applies: a change is seen only at checkpoints, so the window can span
  several tool calls when checkpoints coalesce.
- KI5 — Claude Code writing its own files (`.claude/settings.local.json` on an "always allow")
  is labelled by timing, not `harness` (Non-goals).
