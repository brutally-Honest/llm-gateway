---
feature: 010-harness-hooks
kind: feat
status: approved
version: 1
created: 2026-11-01
supersedes:
branch: feat/010-harness-hooks
approved: 2026-11-01T06:15:38Z
approved-by: human
sha256: 35d774b244aac4e72b3c28e3ff2c2ec2f3c836d022105f95b420dbeddfa03e59
fingerprint: f36c6f56a3514dd080ce28e79e59a4c1293c8dae9194645dfcc116efa2bd72cd
---
# Harness hooks

## Problem
The gateway sees what the model asked for (`tool_use`) and what the harness reported back
(`tool_result`), never what Claude Code actually did. A denied permission, a tool input
rewritten by another hook, a tool that ran but whose result never came back, a sub-agent's
work: none of these are visible today. The user, reading a session in the CLI viewer or the
web UI, cannot tell "model asked" from "harness did" (PLAN §7 Phase 6).

## Goal
- Claude Code hook events reach the gateway over a loopback ingest route on the proxy
  listener, are stored like captures (SQLite index + zstd content-addressed blobs, payloads
  as-is per ADR 0005), and are mapped by the Claude Code client profile into a new canonical
  kind, `harness_event`, whose vocabulary assumes no harness's event names.
- The timeline (CLI and web UI) shows each tool call with its match state, and sub-agent
  work carries its agent id and type.

## Non-goals
- OTel ingest (a later second adapter onto the same canonical kind).
- Any hook decision output: the gateway never blocks, approves, rewrites or injects context;
  it always answers "no opinion". (Not a harness.)
- `gateway hooks install/uninstall` or any command that edits a settings file; 010 ships
  `gateway hooks print` and docs only.
- Reading `transcript_path` files.
- Verifying side effects on disk or network (later phase). An Edit's hook saying "wrote X"
  is shown as the harness's claim, not as proof.
- Codex or any second harness.
- Fuzzy matching of sessions or tool calls: no join beyond the two exact keys (AC14, AC15).
- Rate limiting the ingest route; it is not an LLM call and does not take `x-gateway-key`.
- Nesting sub-agent events under the parent's Task/Agent tool call (labels only, AC22).
- Showing hook drop counters in `/healthz`.

## Acceptance criteria
### Spike
- **AC1** — When T001 runs against the installed Claude Code, the system shall have recorded
  in `research.md`: the Claude Code version; whether `tool_use_id` is present on PreToolUse
  and PostToolUse; whether hook `session_id` equals `x-claude-code-session-id` byte-for-byte;
  and, for HTTP versus command hooks, the measured latency, the behaviour when the gateway is
  down, and async support, with the hook type chosen; whether PermissionDenied carries
  `tool_use_id`; and how often `input differs` (AC18) fires on a session where no other hook
  modifies tool input. If either join key fails, T001 stops BLOCKED with an open question;
  no other join is invented.

### Ingest route
- **AC2** — When a loopback peer with a loopback `Host` sends `POST /_gateway/hooks/{client}`
  on the proxy listener, the system shall answer 200 with an empty body (which Claude Code
  reads as no decision) without waiting on storage, and hand the body to the store
  asynchronously. `/_gateway/` is a reserved prefix: a request under it that matches no
  gateway route is answered 404 by the gateway and never forwarded upstream.
- **AC3** — When 1000 sequential `POST`s are sent to the ingest route after 100 warm-up
  requests, measured client-side on loopback with `-race` off, the system shall answer with
  p99 under 10 ms in each of two runs: storage healthy, and storage stalled with
  `hooks.queue_size` set to 64 so most of the run takes the full-queue path (AC6).
- **AC4** — When the request's `Host` is not loopback, or the request carries an `Origin` or
  a `Sec-Fetch-Site` header, the system shall answer 403 and store nothing. Reachability is
  decided by the bind address (`listen_addr`), as for the proxy; there is no peer-address
  check.
- **AC5** — When the ingest route receives a method other than POST, the system shall answer
  405 and store nothing.
- **AC6** — When the ingest queue is full, the system shall still answer as in AC2, drop the
  event, count it and log one line per drop.
- **AC7** — When the body is larger than `capture.max_body_bytes`, the system shall still
  answer as in AC2, not store it, count it and log it.
- **AC8** — When `capture.enabled` is false, the system shall still answer as in AC2, store
  nothing and count it.
- **AC9** — When the gateway shuts down, the system shall log the hook-ingest counters
  (queue full, oversize, capture off, not JSON, store failed) next to the existing
  `capture stopped` counters.

### Storage and mapping
- **AC10** — When a hook body is stored, the system shall keep it byte-for-byte as received
  in the content-addressed blob store, index it in SQLite with its receive time, client,
  session id and tool-use id (when present), and store no request header of the ingest
  route, so no credential reaches storage.
- **AC11** — When `{client}` is `claude-code`, the system shall map each in-scope event
  (PreToolUse, PostToolUse, PostToolUseFailure, PostToolBatch, UserPromptSubmit,
  SessionStart, SessionEnd, Stop, StopFailure, SubagentStart, SubagentStop,
  PermissionRequest, PermissionDenied, Notification, PreCompact, PostCompact) to a
  `harness_event` with a harness-neutral phase, the tool call id, the agent id and type
  when present, the receive time and a reference to the stored payload; the event-name
  mapping lives only in the Claude Code client profile.
- **AC12** — When a hook event's `hook_event_name` is not in AC11's list, or its body is not
  JSON, the system shall store it as in AC10 and map it to the `unknown` phase, keeping the
  original event name when one can be read; it is never rejected.
- **AC13** — When `{client}` names no known client profile, the system shall store the body
  as in AC10 with client `unknown` and phase `unknown`.
- **AC14** — When a hook event carries a `session_id`, the system shall join it to the
  session whose `x-claude-code-session-id` key is the same string, exactly.
- **AC15** — When a hook event carries a tool-use id, the system shall join it to the
  captured `tool_call` of that session with the same id, exactly; with no match it stays
  unmatched.

### Match states
- **AC16** — When a session has at least one hook event, the system shall give each
  captured tool call exactly one state:
  `asked+ran+reported` (a post-tool or post-tool-failure event and a captured
  `tool_result` both name the id);
  `ran-not-reported` (a post-tool or post-tool-failure event names it, no captured
  `tool_result` does);
  `asked-not-run` with reason `denied` (a permission-denied event carries the id), or with
  reason `not run` (a later exchange carries this call's `tool_result` and no post-tool or
  post-tool-failure event names it); `rejected` and `interrupted` are not told apart in v1;
  `pending` (otherwise).
  The two states where the tool ran also carry an outcome: `succeeded` from a post-tool
  event, `failed` from a post-tool-failure event.
- **AC17** — When a session has no hook event at all, the system shall label each of its
  tool calls `no harness data`, never `asked-not-run`.
- **AC18** — When a pre-tool event's tool input differs, after the existing canonical-JSON
  form (sorted keys, numbers as written), from the captured `tool_use` input for the same
  id, the system shall flag the tool call `input differs` alongside its state and show both
  inputs.
- **AC19** — When a hook event names a tool-use id that matches no captured tool call, the
  system shall show it in the session timeline as `ran-not-asked-or-not-captured`.
- **AC20** — When the same session holds two byte-identical hook payloads, the system shall
  store both, and show one in the timeline and in every state computation when they carry a
  tool-use id or were received within 1 s of each other; otherwise it shall show both.
- **AC21** — When a hook event arrives before, or after, the exchange holding its tool call
  is stored, the system shall show the same states once both are stored.

### Timeline, sessions and UI
- **AC22** — When a hook event carries an agent id or agent type, the system shall show both
  on the event and on its matched tool call, in the CLI and the web UI.
- **AC23** — When `gateway timeline` renders a session, the system shall show each tool
  call's state (and `input differs`) and list hook events that match no tool call as their
  own lines in time order; `--json` shall include the states and the hook events.
- **AC24** — When the web UI renders a session timeline, the system shall show the same
  states, flags, agent labels and hook-event lines as AC23, built from the same timeline
  package.
- **AC25** — When a session id is known only from hook events (no exchange carries it), the
  system shall list the session in `gateway sessions` and the web UI sessions list,
  labelled `hooks only`.
- **AC26** — When a hook event is stored, the system shall push it to open web UI streams
  (the sessions list and that session's timeline, spec 009) the way a committed exchange is
  pushed.

### Install
- **AC27** — When the user runs `gateway hooks print`, the system shall write to stdout a
  settings snippet for `~/.claude/settings.json` that sends every AC11 event to the
  configured `listen_addr`, uses the hook type T001 chose, makes Claude Code carry on
  without blocking when the gateway is down, and requests no decision; it shall write no
  file and exit 0.
- **AC28** — When the docs are read, they shall explain installing the snippet at user
  scope, that user hooks run alongside project hooks (e.g. the agent-loop kit's) without
  either replacing the other, and how to remove it.

## Edge cases
- **E1** — Gateway down or not started → Claude Code proceeds with no block and no prompt;
  those events are lost, and their tool calls show `no harness data` or the state the
  remaining events give (AC27, AC16, AC17).
- **E2** — Body not JSON, empty, or JSON without `hook_event_name` → stored raw, phase
  `unknown` (AC12).
- **E3** — Body over `capture.max_body_bytes` (e.g. a PostToolUse with a huge Read result) →
  200, not stored, counted (AC7).
- **E4** — Hook event with no or empty `session_id` → stored, shown in the ungrouped list
  (AC10, AC14).
- **E5** — Snippet installed at two scopes, or a retry → stored twice; shown once when the
  payload has a tool-use id or the copies arrived within 1 s, else shown twice (AC20).
- **E6** — Hook event arrives before the exchange with its tool call is captured, or the
  capture was dropped → `ran-not-asked-or-not-captured` until/unless the exchange appears,
  then the matched state (AC19, AC21).
- **E7** — Model's tool_use input rewritten by another hook → `input differs`, both shown
  (AC18).
- **E8** — A denied tool call followed by Claude Code's error `tool_result` → `asked-not-run`,
  reason `denied` only when the PermissionDenied event carries the tool-use id; else reason
  `not run` (AC16).
- **E9** — Unknown `{client}` path segment → stored, client and phase `unknown` (AC13).
- **E10** — Non-loopback `Host`, or a browser request carrying `Origin` or `Sec-Fetch-Site`
  → 403 (AC4).
- **E11** — A pre-tool event and nothing after it (no post-tool event, no denial, no later
  `tool_result`) → `pending`, whether or not the session is still active (AC16).
- **E12** — Many sub-agents running at once in one session → each event keeps its own agent
  id and type; tool calls still join by id (AC15, AC22).
- **E13** — Ingest queue full under a burst → 200, dropped and counted. Hook ingest has its
  own `hooks.queue_size`, `hooks.memory_limit` and `hooks.workers` settings, separate from
  `capture.*`, so a
  hook burst never drops an LLM capture. Known limit: both share the one SQLite writer, so a
  hook burst can delay LLM capture writes (AC6).

## Constraints
- Observability first: hook ingest never slows or drops LLM proxying or LLM capture.
- The route never waits on storage; p99 < 10 ms on loopback (AC3).
- Payloads stored as-is (ADR 0005); auth headers and keys never stored.
- Loopback only; v1 has no token, so non-loopback is refused (AGENTS.md, ADR 0010).
- Stateless process: every hook event and every state is derived from the store; nothing
  survives only in memory.
- `harness_event` phases and fields contain no Claude Code event name; a purity test fails
  if `internal/core/` (or any package outside `internal/clients/claudecode/`) names one.
- No new dependency without an ADR. ADR 0012 records the new canonical kind.
- `make verify` stays green under `-race`.
- Each hook event is stored with its receive time (the gateway's clock; hook payloads carry
  no timestamp) and a per-process monotonic sequence number. Within one tool call, events are
  ordered by phase (pre, permission, post/failure), not by timestamp.
- Hook events take their principal from the same resolver as exchanges (`local` in v1).
- Known limit: hook ingest and LLM capture share the one SQLite writer (E13).

## Agnosticism check
- Axes touched: **client** (Claude Code hook mapping, in `internal/clients/claudecode/`).
  No protocol or provider is touched.
- `internal/core/` changes: a new canonical kind `harness_event`. Justified because it is
  the harness-side counterpart of `tool_call`/`tool_result`, needed by any harness (Claude
  Code hooks now, OTel or Codex later as further adapters onto the same kind); its phases
  are harness-neutral and the Claude Code event names live only in the client profile
  (AC11, purity test). Recorded in ADR 0012.
- Unknown client → stored, client `unknown` (AC13). Unknown event → phase `unknown` (AC12).
- Resolves no PLAN open question.

## Open questions
None.

## Changelog
- v1 (2026-11-01) — first draft; revised before approval (AC1–AC4, AC16, AC20, E13,
  ordering and principal constraints)
