# LLM Gateway

[![verify](https://github.com/brutally-Honest/llm-gateway/actions/workflows/verify.yml/badge.svg?branch=main)](https://github.com/brutally-Honest/llm-gateway/actions/workflows/verify.yml)

An opinionated, observability-first LLM gateway in Go. It sits between any LLM client (Claude Code,
Codex, …) and any LLM provider (Anthropic, OpenAI, …), forwards traffic faithfully,
and captures what flows through: prompts, responses, tool calls, tool results, tokens
and cost.

It is **not** a harness — it never runs agents, executes tools or edits files — and it
is not tied to one vendor.

Status: v1 is complete (every phase in PLAN §7's v1 order is done) apart from its Later items
and AC44; see `docs/roadmap.md`.
Claude Code proxies through the gateway to Anthropic with capture, session timelines, token
usage, per-key rate limiting and a live browser view (see `specs/`). **File and network
effects** (spec 014: changed and read files, connections through a forward proxy) are done.

- `PLAN.md` — the one-page source of intent: goals, phases, stack rationale, open
  questions, commit conventions.
- `AGENTS.md` — the rules any agent working in this repo must follow.
- `specs/` — one folder per feature: `spec.md` (what and why) and `research.md`
  (decisions as D-entries). Some older specs (000–016) also keep `plan.md` and
  `tasks.md`, and 017 `tasks.md`, as history.
- `docs/decisions/` — ADRs.
- `docs/clients/` — one page per client: how to connect, known limits.

## Install

Needs Linux or macOS (amd64 or arm64), Claude Code, curl and a login service manager
(systemd user session or launchd); git, bubblewrap and socat are optional. The installer
checks them and offers to install what is missing: [requirements](docs/install.md#requirements).

```sh
curl -fsSL https://raw.githubusercontent.com/brutally-Honest/llm-gateway/main/install.sh | sh
```

Then open a new terminal and run `claude`. To see first what it checks or would change,
end the line with `sh -s -- --check` or `sh -s -- --dry-run` instead of `sh`.

```sh
gateway doctor       # check the install and the running gateway
gateway uninstall    # remove it; keeps your config and captured data
```

Upgrades, flags, every doctor check, paths and troubleshooting: [docs/install.md](docs/install.md).

### Known limits

- **Loopback only in v1.** Every listener binds 127.0.0.0/8, `::1` or `localhost`; any
  other address, `0.0.0.0` included, stops the gateway at start, with no override. v1 has
  no auth, and captures hold prompts and tool results as sent, secrets included
  ([docs/install.md](docs/install.md#known-limits)).

## Setup

For working on the gateway itself (building from source).

Prerequisites: `shellcheck` and node 22.12 or later on your `PATH`. `make verify` (which the
pre-push hook runs) needs shellcheck to lint `install.sh` and node for the web tests.

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
  session is active while its last exchange started under 5 minutes ago (a hooks-only
  session: its last hook event), except a hooks-only session whose only hook events are
  `SessionEnd`; the session page also counts hook events. The list filters on the server
  (All, Needs a look, Active, Hooks only), searches, and pages with Previous/Next and a
  "Page N of M" label; filter, search and page are kept
  in the URL hash.
- **Session timeline:** built from canonical events:
  prompts, tool calls paired with their results, and a chip per model call with its model, status
  and token counters. The session page has tabs (Story, Files, Commands, Sites, Context); each
  turn has chips for its model calls, tool calls, files, searches, sites and failures, and a
  model call opens a details panel with Preview and Raw.
- **Settings:** the header's gear sets the theme (Dark or Light) and the style (Lean or Stark).
- **Live updates:** an active session's page shows a new model call, hook event or effect
  within about a third of a second of it being stored, even on a session of several hundred
  calls: the gateway keeps each watched session's build in memory and re-reads only what
  changed. Some changes still rebuild the session in full (the first open, a session pushed out
  of the in-memory cache, or a burst that touches most of a session's calls), which takes a
  few seconds on the largest sessions. The page reconnects on its own after a drop.
- **Exchange detail:** one exchange at `#/exchanges/<id>`, with its raw request and response
  bodies, loaded on demand.

The page only reads. It cannot change or delete captured data, and nothing in it can slow down
or fail proxying or capture.

## Self-observability

The gateway serves Prometheus metrics about its own health at `/metrics` and can trace
proxied calls to Jaeger; `make observability-up` starts Prometheus, Grafana (with a
dashboard) and Jaeger on loopback. How to start and stop it, the tracing keys and where
everything is: `docs/observability.md`.
