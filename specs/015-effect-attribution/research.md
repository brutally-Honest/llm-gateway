---
feature: 015-effect-attribution
---
# Research & decisions — 015-effect-attribution

## Open questions
<!-- Agents add a question here when they are blocked; the loop stops until you answer.
     Answer by changing "(open)" to "(answered)" and writing the answer underneath:
- **Q1** (open) T003 — <question> — options: A) … B) …
-->
- **Q5** (answered) T003 — Unassigned connections never read `harness`: the proxy stores them
  under client `unknown`, which has no rules, so AC10 (no rules → never `harness`) wins
  over AC9 (unassigned → `harness` or nothing). Around T003, 19 unassigned connections
  to `api.anthropic.com`, `mcp-proxy.anthropic.com` and the Datadog host carry no label.
  `TestEffectsAPI_Attribution` misses it because it saves its unassigned row under the
  test client. — options: A) unassigned connections ask every profile's `HarnessHost` and
  read `harness` if any says yes (the label names no client); B) keep them unlabelled and
  change AC9 to say so (via /al-change).
  - Answer (Karthik, 2026-11-10): A, but name the client. For an unassigned connection, ask
    every registered client profile through one interface whether the host is on its
    harness list; on a match, label it `harness` and name that client. No client name in
    `core/`. The attribution test covers it with the real Claude Code profile and a row
    under client `unknown`, red without the fix. AC9/AC10 amended to say which rule applies.

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
- D9 (2026-11-10) — Unassigned connections are labelled by `effects.Unassigned` through a
  `HarnessLookup` that `internal/ui` builds from every registered profile's
  `core.AttributionRules.HarnessHost` (the row's own client first, then by name); a match
  is `harness` with `client` set; AC10 covers sessions only — answer Q5.
- D10 (2026-11-10) — `bridge.claudeusercontent.com` joins the Claude Code harness host list:
  it is Claude Code's own long-lived Chrome extension bridge, and one connection carries
  every browser action, so per-tool attribution does not apply. Seen unassigned on the
  login store (one connection, 75 min) — Karthik's decision.

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
- 2026-11-10 — T010 `/ui-check` rerun in Chrome at 7d1cb9f (skew 50ms), throwaway gateway
  on a fresh data folder, seed as written. Steps 3–6 match: turn 4 shows every label the
  seed printed as glyph + word with its tool (`● named: Edit`, `◐ only one running: Bash`,
  `◌ several running: Bash, Bash`, `◌ several running: Bash (agent), Glob`, `⚙ harness`,
  `○ outside any tool call`); turn 5's `notes/manual.txt` reads outside; no `#n` (no model
  calls, E11); the diff link opens inline as in 014; the unassigned page lists the seed's
  `127.0.0.1` connection with no label; no console errors. Step 7 is T003 (above). GIF:
  `~/Downloads/015-attribution-2026-11-10.gif`. Not a pass record; Karthik decides.
- 2026-11-10 — After the Q5 fix (02d0fef), the unassigned page of a scratch copy of the
  login store shows `⚙ harness: claude-code` on every `api.anthropic.com`,
  `mcp-proxy.anthropic.com`, Datadog and `downloads.claude.ai` row; `api.github.com`,
  `registry.npmjs.org` and `bridge.claudeusercontent.com` stay unlabelled.

### T003 — fresh Claude Code session (2026-11-10)
- 2026-11-10 — Session `63999a51…` (working folder `~/fx015-t003`), run by Karthik on the
  login service at d24c8b8 (skew 250ms). All hook rows carry `work_dir`.
- 2026-11-10 — Skew. Gap from the tool's PreToolUse receive time to its connection start:
  Bash `curl -sI https://example.com` +593ms; WebFetch `https://example.org` +782ms (its
  `api.anthropic.com` check first, +498ms); sub-agent Bash `curl -sI https://github.com`
  +476ms. Connection end to PostToolUse: +0.5ms, +1827ms (WebFetch summarises after the
  fetch), −18ms (the Post landed before the proxy logged the close; ends are not used for
  connections). No PreToolUse arrived after its tool started.
- 2026-11-10 — Skew, the other side. Smallest gap between one tool's PostToolUse and the next
  tool's PreToolUse in one batch: Write → Bash 148ms (window end to Bash Pre 143ms); the two
  "parallel" Reads ran one after another, 268ms apart. A replay of the stored session on a
  copy of the store gives the same labels at 0, 50 and 100ms; at 150 and 250ms `gen.sh`
  turns from `named Write` into `several Write, Bash` (Bash's `bash gen.sh` names it).
- 2026-11-10 — Skew set to 50ms (was 250ms): a late Pre has not been seen (smallest lead
  476ms, spawn and DNS come first), while a skew above ~143ms already gave a wrong label in
  the first real session. 50ms keeps ~90ms below that gap and some slack for a slow hook.
  Flip to a larger value if a connection reads `outside` or `only_one` while its tool ran
  (a Pre received late), e.g. under load or a remote gateway.
- 2026-11-10 — Hosts in the session: `downloads.claude.ai` harness; Datadog
  (`http-intake.logs.us5.datadoghq.com`) ×4 harness; `api.anthropic.com` harness (during
  the WebFetch: Claude Code's domain check, no candidate names it); `example.com` named
  Bash; `example.org` named WebFetch; `github.com` named sub-agent Bash;
  `registry.npmjs.org` outside (opened 5s before the first prompt, open 64s, Claude Code's
  start-up). Unassigned from 12:37:56 to 12:39:13: `api.anthropic.com` ×13,
  `mcp-proxy.anthropic.com` ×4, Datadog ×2, `api.github.com` ×1, all unlabelled (Q5).
- 2026-11-10 — Harness list unchanged. `registry.npmjs.org` stays off it (D5): listing it
  would turn a Bash `npm install` that does not name the host into `harness`; the start-up
  check reads `outside`, which is true. `api.github.com` (after the session ended,
  unassigned) is not Claude Code's own as far as this data shows.
- 2026-11-10 — Labels against the expectation. Matches: notes.txt named Edit; example.com
  named Bash; example.org named WebFetch; agent.txt and github.com named sub-agent Bash;
  reads named by their hooks. Differences: (1) gen.sh read `several Write, Bash` at 250ms,
  fixed by the skew above; (2) a.txt and b.txt read `named Bash`, not `only one running`:
  the command Claude wrote was `bash gen.sh && ls a.txt b.txt`, which names them; correct
  under D7; (3) a third read, notes.txt before the Edit, named Read: Claude Code requires a
  Read before an Edit; (4) a `SubagentStop` for another agent id (`ae47008b…`, no Start)
  landed mid-turn; it closes nothing of ours. Expected limit (KI2): a Bash that runs a
  script by name names that script, so a script changed in the same window as it ran reads
  `several`.

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
