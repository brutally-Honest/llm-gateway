# LLM Gateway

An observability-first LLM gateway in Go. It sits between any LLM client (Claude Code,
Codex, …) and any LLM provider (Anthropic, OpenAI, …), forwards traffic faithfully,
and captures what flows through: prompts, responses, tool calls, tool results, tokens
and cost.

It is **not** a harness — it never runs agents, executes tools or edits files — and it
is not tied to one vendor.

Status: **Phases 0–5** done, and the **web UI** (spec 009) is built. Claude Code proxies through
the gateway to Anthropic with capture, session timelines, token usage, per-key rate limiting and
a live browser view (see `specs/`).

- `PLAN.md` — the one-page source of intent: goals, phases, stack rationale, open
  questions, commit conventions.
- `AGENTS.md` — the rules any agent working in this repo must follow.
- `specs/` — one folder per feature: `spec.md` (what and why), `plan.md` (how),
  `tasks.md` (checklist), `research.md` (doubts, roadblocks and limits, with answers).
- `docs/decisions/` — ADRs.
- `docs/clients/` — one page per client: how to connect, known limits.

## Setup

Prerequisite: `jq` on your `PATH`. `make verify` (which the pre-push hook runs) needs it
for the agent-loop kit tests, and the kit's commit guard hook refuses every tool call
without it.

```sh
make setup
```

It enables the git hooks and notes config, installs the pinned golangci-lint into
`./bin` and downloads the Go modules. It is safe to run again. The other commands
(`verify`, `run`) are listed in `AGENTS.md`.

## Web UI

With `make run`, the gateway also serves a read-only page on its own loopback listener. Open
**http://127.0.0.1:7199/** in a browser while the gateway runs. The address is `ui.listen_addr`
(env `GATEWAY_UI_LISTEN_ADDR`); `ui.enabled` (env `GATEWAY_UI_ENABLED`) turns it off. It has no
login and must stay on loopback, so the gateway refuses any other address. The UI needs capture
on (`capture.enabled`): with capture off, no UI listener starts.

What it shows:

- **Sessions:** every captured session, plus the ungrouped exchanges that belong to none. A
  session with an exchange in the last 5 minutes is active.
- **Session timeline:** the same timeline `gateway timeline` prints, built from canonical events:
  prompts, tool calls paired with their results, and a chip per model call with its model, status
  and token counters. Long timelines can be filtered by text or tool name.
- **Live updates:** an active session's page updates within moments of each exchange being
  stored, and it reconnects on its own after a drop.
- **Exchange detail:** one exchange at `#/exchanges/<id>`, with its raw request and response
  bodies, loaded on demand.

The page only reads. It cannot change or delete captured data, and nothing in it can slow down
or fail proxying or capture.
