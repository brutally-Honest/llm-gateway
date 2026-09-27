# LLM Gateway

An observability-first LLM gateway in Go. It sits between any LLM client (Claude Code,
Cursor, …) and any LLM provider (Anthropic, OpenAI, …), forwards traffic faithfully,
and captures what flows through: prompts, responses, tool calls, tool results, tokens
and cost.

It is **not** a harness — it never runs agents, executes tools or edits files — and it
is not tied to one vendor.

Status: **Phase 0** done; the gateway serves `/healthz` (see `specs/000-foundation/`).
Status: **Phase 1** done; Claude Code proxies through the gateway to Anthropic,
streams included (see `specs/001-core-and-anthropic-passthrough/`). **Phase 2**
(capture) is next.

- `PLAN.md` — the one-page source of intent: goals, phases, stack rationale, open
  questions, commit conventions.
- `AGENTS.md` — the rules any agent working in this repo must follow.
- `specs/` — one folder per feature: `spec.md` (what and why), `plan.md` (how),
  `tasks.md` (checklist), `research.md` (doubts, roadblocks and limits, with answers).
- `docs/decisions/` — ADRs.
- `docs/clients/` — one page per client: how to connect, known limits.

## Setup

```sh
make setup
```

It enables the git hooks and notes config, installs the pinned golangci-lint into
`./bin` and downloads the Go modules. It is safe to run again. The other commands
(`verify`, `run`) are listed in `AGENTS.md`.
