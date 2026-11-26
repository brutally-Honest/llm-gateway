# LLM Gateway

[![verify](https://github.com/brutally-Honest/llm-gateway/actions/workflows/verify.yml/badge.svg?branch=main)](https://github.com/brutally-Honest/llm-gateway/actions/workflows/verify.yml)

## What it is

An observability-first LLM gateway in Go. It sits between an LLM client and a provider,
passes the traffic through unchanged, and records prompts, responses, tool calls, tokens
and API-equivalent cost. It is not a harness: it never runs agents, executes tools or
edits files. It is built to be client- and provider-agnostic.

Today Claude Code is the only built client and Anthropic Messages is the only built
protocol. Other clients and providers are designed for, not built
([PLAN.md §3](PLAN.md#3-client-support)).

## Who it is for

Someone who runs Claude Code on their own Linux or macOS machine and wants to see what it
does and what it would cost. New to this? Start with [docs/getting-started.md](docs/getting-started.md).

<!-- screenshots: added in the release PR from seeded fictional data -->

## What you see in the UI

- A list of sessions, with turns, tokens and API-equivalent cost.
- Each session's tool calls, file changes and network connections.
- A "needs a look" list of failures and effects that cannot be pinned to a tool call.
- Live updates while a session runs, and a banner when capture is degraded.

Details: [docs/ui.md](docs/ui.md).

## What it records and what it does not

- Records model calls (prompts and responses), tool calls and results, tokens and cost.
- Records file changes and network connections made through its proxy, when the sandbox is on.
- Stores copies as sent, secrets in prompts and files included. Auth headers and keys are not stored.
- Misses calls that do not go through the gateway, such as Claude Code's direct checks to `api.anthropic.com`.
- Listens on loopback only and has no login.

Details: [docs/what-gets-recorded.md](docs/what-gets-recorded.md).

## Install

```sh
curl -fsSL https://raw.githubusercontent.com/brutally-Honest/llm-gateway/main/install.sh | sh
```

Needs Linux or macOS, Claude Code, curl and a login service manager; the full list is in
[docs/install.md](docs/install.md#requirements). The installer shows its plan and asks before
it changes anything. To look first, end the line with `sh -s -- --check` or `sh -s -- --dry-run`.

When it finishes, open a new terminal and run `claude`.

## Check it works

```sh
gateway doctor
```

Then open <http://127.0.0.1:7199> in a browser on the same machine. Use `claude`, and the
session shows up there.

## Uninstall

```sh
gateway uninstall            # removes the service, the claude function and the settings file
gateway uninstall --purge    # also deletes the config and the recorded data
```

Plain `uninstall` keeps your config, your recorded data and the `gateway` binary.

## Build from source

```sh
make build && ./bin/gateway install
```

For contributors: `make setup` once, then `make verify` before you push.

## Docs

- [docs/getting-started.md](docs/getting-started.md): install and first use, in plain words.
- [docs/what-gets-recorded.md](docs/what-gets-recorded.md): what is kept, what is missed, privacy, disk growth.
- [docs/ui.md](docs/ui.md): the web UI, page by page.
- [docs/architecture.md](docs/architecture.md): how the gateway is built.
- [docs/install.md](docs/install.md): requirements, flags, doctor checks, paths, troubleshooting.
- [docs/capture.md](docs/capture.md): what the capture store holds.
- [docs/observability.md](docs/observability.md): the gateway's own metrics, traces and health.
- [docs/clients/claude-code.md](docs/clients/claude-code.md): how Claude Code connects, and its limits.
- [docs/clients/claude-code-hooks.md](docs/clients/claude-code-hooks.md): Claude Code hook events.
- [docs/roadmap.md](docs/roadmap.md): what is done and what comes next.
- [docs/release-checklist.md](docs/release-checklist.md): the steps before a release.
- [docs/decisions/](docs/decisions/): ADRs for decisions that are hard to reverse.
- [PLAN.md](PLAN.md): the plan: goals, phases, stack and open questions.
- [AGENTS.md](AGENTS.md): how work is done in this repo: workflow and rules.
