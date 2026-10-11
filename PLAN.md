# LLM Gateway — Project Plan (gist)

> The one-page source of intent. If a spec, plan, or PR contradicts this file, either this file is updated (with an ADR) or the other thing is wrong.

---

## 1. Goal

An **observability-first LLM gateway**, written in Go. It sits between any LLM client and any LLM provider, and lets me see exactly what flows through: prompts, responses, tool calls, tool results, tokens, cost. Rate limiting, cost controls, and routing come after observability.

**What it is not:**
- **Not a harness.** It doesn't run agents, execute tools, or edit files. The harness (Claude Code, Codex, …) stays in charge. The gateway only watches the traffic.
- **Not tied to one vendor.** Claude Code is the first client because it's what I use today. The design must let Codex, Gemini CLI, Antigravity, OpenCode, Cline, plain apps, and future clients plug in without changing the core.

Why it exists:
1. Observe my own agent and LLM usage (primary).
2. Learn LLM backend internals: streaming proxies, provider protocols, cost accounting.
3. A deployable, well-reasoned system for my resume, not a toy.

## 2. Agnosticism: the central design principle

The gateway is agnostic along **three independent axes**. Each can be extended without touching the other two or the core.

| Axis | What varies | Extension point |
|---|---|---|
| **Client** (harness or app) | How it's pointed at the gateway, how sessions and agents are identified, quirks | **Client profile**: detection rules, session/agent-ID extraction, known limits |
| **Wire protocol** | Anthropic Messages, OpenAI Chat Completions, OpenAI Responses, Gemini, Bedrock, … | **Protocol adapter**: routes, passthrough rules, stream framing, parser into canonical events |
| **Provider / model** | Anthropic, OpenAI, OpenRouter, Google, local models, … | **Upstream config + pricing table**: base URL, credential, per-model prices |

```
 any client ──► [ protocol adapter ] ──passthrough──► any upstream provider
                        │
                        │ (async copy)
                        ▼
                 protocol parser ──► canonical event model ──► store ──► timeline / cost / metrics
                        ▲
                 client profile (session id, agent id, harness name)
```

**Canonical event model.** Every protocol is parsed into the same internal events: `session`, `request`, `message`, `tool_call`, `tool_result`, `usage`, `error`. Everything downstream (timeline, cost, metrics) reads only canonical events and never touches a provider's format directly. This one rule keeps the system agnostic.

**Rules that enforce it:**
- No provider- or harness-specific `if` statements outside adapters and profiles.
- Unknown client → still proxied and captured, labelled `unknown`.
- Unknown model → still captured; cost is `unknown`, not an error.
- Adding a client, protocol, or provider = a new adapter, profile, or config entry, plus its contract tests. The core stays unchanged.

### Scale: local single-user now, extendable later

v1 runs **on my machine, for me only**. It must be able to grow into a team or hosted gateway without a rewrite.

**Rule: build the seam, not the feature.** Like a house with plumbing stubs capped behind the wall for a future bathroom: v1 pays the small cost of the pipe, not the cost of the bathroom.

| Seam | v1 (single user) | Later (team / hosted) | What v1 must do now |
|---|---|---|---|
| **Identity** | Every request belongs to the principal `local` | Per-user / per-team principals | Every captured event carries a `principal_id` from day one. Auth middleware resolves it (currently always `local`). |
| **Auth** | None on localhost; one gateway token on tunnelled routes | Virtual keys, per-key budgets and limits, SSO | Auth is a pluggable middleware behind one interface that returns a principal. |
| **Storage** | Local store (see OQ-1) | Postgres / a managed DB | All access goes through a `Store` interface; nothing outside it knows the engine. |
| **Capture transport** | In-process channel | Kafka / a queue between gateway and a separate ingest service | Producers depend on a `CaptureSink` interface, not on the channel. |
| **Rate limits / budgets** | Keyed by `local` | Keyed by principal and key | Limit keys are built from the principal, never hard-coded. |
| **Instances** | One process | N stateless instances behind a load balancer | The stateless-process rule (§5): no in-memory state that must survive a request. |
| **Viewer** | Local CLI / page, no login | Hosted UI with per-user access | Queries always filter by principal, even when there's only one. |

**Not built in v1:** user management, virtual keys, budgets per user, multi-instance coordination, hosted UI. Each becomes a feature spec when needed and plugs into the seam above.

## 3. Client support

| Client | Protocol | How it connects | Status |
|---|---|---|---|
| Claude Code | Anthropic Messages | `ANTHROPIC_BASE_URL` → `http://localhost:…`; works with an API key **or** a claude.ai subscription | **v1** |
| Codex CLI | OpenAI Responses | custom `model_providers` base URL | designed for, later |
| Gemini CLI / Antigravity / others | Gemini, OpenAI-compatible, … | per-client base-URL setting | designed for, later |
| Plain apps / scripts | any supported protocol | base URL in the SDK | free once the protocol exists |

**Known client limits (inherent, not bugs):**
- **Claude Code:** fast-mode checks and the WebFetch domain check call `api.anthropic.com` directly, so they never reach the gateway. Through a custom base URL, Claude Code does not load claude.ai connectors (Gmail, Calendar, Drive), so the gateway sees no request for them. Its `HEAD /api/hello` sends `User-Agent: Bun/<version>` and no `x-app`, so it is labelled `unknown` on purpose: matching on Bun would mislabel every other Bun app.
- **All clients:** the gateway sees what the model *asked for* and what the harness *reported back*, not the real file-system or network side effects. Phase 6 addresses this.

## 4. Priorities (in order)

1. **Observability.** Faithful capture; a session and tool timeline for every supported client.
2. **Cost.** Accurate per request and per session, including cache tokens.
3. **Rate limiting**
4. **Routing.** Model aliases, weighted selection, fallback.
5. Everything else.

A lower priority never degrades a higher one.

## 5. Core design rules

- **Passthrough first, transform later.** Forward bytes faithfully and parse copies on the side. No mutation unless a feature explicitly and opt-in requires it.
- **Forward as open lists.** Pass unknown headers and body fields through untouched: `anthropic-*` headers, `x-*` client headers, new body fields. Never allowlist today's set, or the next client release breaks.
- **Streams are sacred.** No buffering. Forward keep-alive pings. Keep the upstream `content-type`.
- **Errors pass through verbatim.** Status, body, `retry-after`, `x-should-retry`, and rate-limit headers are forwarded, because clients' retry logic parses them.
- **Don't reshape prompts.** The `system` array, `cache_control`, and message structure stay exactly as sent. Reshaping breaks prompt caching and thinking signatures.
- **Capture never blocks.** SSE is teed and storage writes are async. If capture fails, the request still succeeds, and the failure is logged and counted.
- **Credentials the gateway handles never land in storage:** auth headers, `Proxy-Authorization` included, and keys are always redacted. Secrets inside prompts and tool results are stored as-is on a trusted machine, protected by 0700/0600 permissions, with masking a later opt-in (OQ-5, ADR 0005).
- **Never exposed without auth.** Any route reachable from outside localhost requires a gateway token.
- **Stateless process.** State lives in external stores.
- **Provider-reported usage is primary; our own estimate is the auditor.** How we estimate per tokenizer is OQ-4.

## 6. Out of scope (v1)

- Being a harness or agent runtime of any kind
- Cross-protocol translation (e.g. an Anthropic-format client → an OpenAI-only model). Designed for, not built.
- Multi-user features: virtual keys, per-user budgets, user management, hosted UI. The seams exist (§2, "Scale"); the features don't.
- Semantic caching, MCP, A2A, admin UI, SDK

## 7. Phases

Each phase maps to one or more features, and each feature gets its own spec folder and branch.

| # | Phase | Goal | Done when |
|---|---|---|---|
| 0 | Foundation | Repo, AGENTS.md, spec templates, docker-compose, local gate (`make verify`: lint, test, `-race`; pre-push hook), logging, `/healthz` | On a fresh clone, after `make setup`, `make verify` passes, and a push containing a malformed commit subject or a failing check is rejected |
| 1 | Core + first protocol | Adapter/profile interfaces; Anthropic Messages passthrough (`/v1/messages`, `count_tokens`, `/v1/models`, `HEAD /api/hello`); Claude Code profile | A real Claude Code session (API key **and** subscription) runs through the gateway with no behaviour difference<br>API-key half pending: AC44 deferred, tracked in `specs/001-core-and-anthropic-passthrough/research.md` Q4. |
| 2 | Capture + canonical events | Async raw capture with redaction; Anthropic parser → canonical events | Every exchange is stored and parsed; killing the store doesn't break the client |
| 3 | Sessions & timeline | Group Claude Code requests into sessions via `x-claude-code-session-id`; viewer | Claude Code sessions replay as ordered timelines: prompt → tool calls → results. The timeline reads only canonical events, never Anthropic wire types, enforced by a purity test on the timeline package |
| 4 | Usage (tokens) | Usage from the provider's response: the four counters (input, output, cache write, cache read) per model call, shown per exchange and summed per session; no prices | Every model call shows the counters from its provider usage event; session totals sum them and mark unknown, partial, or errored calls, never as 0 |
| 5 | Rate limiting | Redis token bucket via Lua; well-formed 429s with `retry-after` | Clients back off and retry cleanly |
| 6 | Ground truth (optional) | Ingest harness hooks / OTel events; correlate with captured tool calls | The timeline shows "model asked" vs "harness did" |
| 7 | Routing | Same-protocol model aliases, weighted selection, fallback + circuit breaker | A model swap via config needs no restart and doesn't break the client |
| 8 | Self-observability | Prometheus, OTel → Jaeger, Grafana | The gateway's own p50/p99 overhead is visible |
| 9 | Cost: pricing table keyed by provider + model, needs API billing to verify | Price each model call from the Phase 4 counters using a pricing table keyed by provider + model; estimate audit (OQ-4) | Per-session cost matches the provider console within tolerance; unknown models are shown as `unknown` |

The former Phase 3 (a second wire protocol and its client profile, gateway token auth, a public tunnel) was removed and the later phases moved up by one ([ADR 0006](docs/decisions/0006-drop-cursor-from-v1.md)).

Later, each as a new adapter or profile: OpenAI Responses (Codex), OpenAI Chat Completions (Cursor and others), Gemini (Gemini CLI, Antigravity), Bedrock/Vertex formats, cross-protocol translation, eBPF-level fs/network tracing. Each new protocol's decoder must map its provider usage to the four Phase 4 counters with the meaning defined in `specs/005-session-token-usage/spec.md` (Terms): input is uncached input only, so input + cache write + cache read is the full prompt.

**Why no second protocol before the timeline:** the test-only adapter and profile (001 AC35) prove ingestion is agnostic, and the timeline purity test in Phase 3 proves consumption is. Codex (OpenAI Responses) becomes the first real second protocol.

---

## 8. Tech stack and why

Status: **Decided** = settled; **Proposed** = my default, confirmed in the phase's spec; **Open** = see §9.

| Area | Choice | Why | Rejected, and why | Status | Needed from |
|---|---|---|---|---|---|
| Language | **Go** | Goroutines are cheap (~2 KB each vs ~1 MB per OS thread), so one goroutine per stream scales. `net/http` + `http.Flusher` give first-class streaming. Ships as a single static binary, which suits a local tool. It's also a learning goal. | **Node.js:** I'm stronger in it and it's great at I/O, but parsing and hashing large bodies compete with proxying on one event loop, and it teaches me less. **Rust:** the best raw performance, but a much slower iteration speed for a solo project. | Decided | Phase 0 |
| HTTP router | **chi** | Built on plain `net/http` handlers, so the stdlib proxy, `http.Flusher`, and all middleware work unchanged. Explicit middleware chain; minimal magic. | **Gin:** its own context type; less idiomatic middleware. **Fiber:** built on `fasthttp`, *not* `net/http`, which breaks `httputil.ReverseProxy` and standard streaming — a dealbreaker for a proxy. | Decided | Phase 0 |
| Proxy engine | **`httputil.ReverseProxy`** with a `Rewrite` hook; the response body is wrapped to tee bytes into capture | Already handles hop-by-hop headers, `X-Forwarded-*`, and immediate flushing of `text/event-stream`. Those edge cases would otherwise be mine to rediscover. | **Hand-rolled proxy:** more to learn, but re-implements solved edge cases and is where fidelity bugs hide. Kept as a fallback if a protocol needs control the stdlib can't give. | Decided ([ADR 0003](docs/decisions/0003-reverseproxy-with-rewrite-as-proxy-engine.md)) | Phase 1 |
| Capture pipeline | **In-process bounded channel + worker goroutines**; when full, drop and count rather than block | Keeps "capture never blocks" true with zero extra infrastructure. The counter makes loss visible instead of silent. | **Kafka:** I know it from production, but it's a whole broker for one user on one machine. Revisit if multiple gateway instances ever exist. **Synchronous writes:** add storage latency to every token of every stream. | Decided ([ADR 0004](docs/decisions/0004-capture-store.md)) | Phase 2 |
| Capture store | **SQLite** (pure-Go `modernc.org/sqlite`, WAL) for the index and events + **zstd-compressed content-addressed blob files** for bodies | Zero-ops single file, still SQL, and one user has no concurrent-writer problem. Content addressing dedups identical bodies and parsed content for free. The pure-Go driver keeps the binary static (no cgo). | **PostgreSQL:** strong for cost queries, but heavy to run for one user; revisit if it goes multi-user. **MongoDB:** weaker for cost aggregation joins. **Everything in SQLite:** large bodies bloat the database and its WAL for no gain. **`mattn/go-sqlite3`:** needs cgo. | Decided ([ADR 0004](docs/decisions/0004-capture-store.md)) | Phase 2 |
| Rate-limit state | **Redis** + Lua token bucket | A Lua script runs atomically on the server, so check-and-decrement has no race between instances. Keeps the process stateless. I already run it in production. | **In-memory limiter:** simplest, but state dies on restart and breaks with more than one instance, which violates the stateless rule. Redis is added only in Phase 5, so it isn't a dependency before it's needed. | Decided | Phase 5 |
| Config | **One YAML file** (providers, pricing, client profiles, routes), parsed with **`go.yaml.in/yaml/v3`**; **secrets only from env vars** | Adding a provider or model price is a config edit, not code, which is agnosticism made concrete. Secrets never sit in a file that could be committed. | **DB-backed config:** needed for an admin UI, which is out of scope. **Env-only:** unreadable for nested pricing and routing tables. | Decided ([ADR 0002](docs/decisions/0002-config-format-and-loader.md)) | Phase 0 |
| Logging | **zap** (structured JSON) | Very low allocation on the hot path; mature; structured fields make logs queryable by session or request ID. | **`log/slog`** (stdlib): a credible option with no dependency. zap was already chosen and can sit behind a slog handler, so switching later is cheap. | Decided | Phase 0 |
| Metrics | **Prometheus + Grafana** | The de-facto standard. Pull-based, so no agent is needed. Histograms give p50/p99 latency overhead, one of my learning goals. | **StatsD:** no native histograms/percentiles. **Datadog etc.:** SaaS, costs money, and data leaves my machine. | Decided | Phase 8 |
| Tracing | **OpenTelemetry → Jaeger** | OTel is vendor-neutral, which matches the agnostic principle: the backend can swap without code changes. Jaeger gives a local UI in one container. | **Vendor SDKs:** lock-in. **Grafana Tempo:** fine, but needs more setup to browse locally than Jaeger. | Decided | Phase 8 |
| Token estimation | **tiktoken-go** for OpenAI-family models | Independent count to audit provider-reported usage. | Its accuracy for non-OpenAI tokenizers (Claude, Gemini) is the problem in OQ-4. | Decided for OpenAI; **Open** for others | Phase 9 |
| Testing | **Table-driven tests, `-race`, testcontainers-go, recorded stream fixtures** per protocol | Race detector catches tee/capture concurrency bugs. testcontainers gives real Redis/DB, not mocks that lie. Recorded real SSE streams (golden files) let each adapter be tested without live API calls or cost. | **Mocked stores only:** they pass while the real thing fails. **Live-API tests in CI:** cost money and flake. | Decided | Phase 0 |
| Packaging | **docker-compose** for the stack; gateway also runnable as a plain binary | One command brings up store, Redis, Prometheus, Grafana, Jaeger. Running the binary directly keeps the dev loop fast. | **Kubernetes:** irrelevant for local single-user. | Decided | Phase 0 |
| Viewer | **Local CLI first** (`gateway sessions`, `gateway timeline`); a web UI later, once the canonical event model is stable | Greppable and fast to build, and it reads canonical events directly. Long timelines and nested sub-agents are a later need. | **Small local web UI now:** better for long timelines, but it freezes presentation before the model has met a second protocol. **Grafana panels only:** poor for reading conversations. | Decided ([ADR 0007](docs/decisions/0007-viewer-cli-first.md)) | Phase 3 |

---

## 9. Open questions

Each is resolved in the named phase's spec and recorded as an ADR. The leaning is a starting point, not a decision.

**OQ-1. Where are captured exchanges stored?** *(decide by Phase 2)* **Resolved:** SQLite for the index and events + zstd content-addressed blob files for bodies ([ADR 0004](docs/decisions/0004-capture-store.md)).
- **Why it matters:** harnesses resend the *whole conversation* on every turn. A long Claude Code session produces many near-identical 100 KB+ requests, so storage grows roughly quadratically unless request bodies are deduplicated.
- **Options:**
  - **PostgreSQL:** the original choice; strong for relational cost queries; heavier to run; new to me (I don't use SQL today).
  - **SQLite:** zero-ops single file, ideal for one user; still SQL; weak for concurrent writers (not a problem locally).
  - **MongoDB:** bodies are JSON documents anyway; I have production experience; weaker for cost aggregation joins.
  - **Content-addressed blob files + a small index:** dedup comes naturally (same bytes → same hash); needs one of the above for the index.
- **Leaning:** SQLite for the index and events + content-addressed blobs for bodies. Revisit Postgres if it ever goes multi-user.

**OQ-2. How are requests grouped into sessions for clients without a session header?** *(decide with the first client that has no session header)*
- **Why it matters:** Claude Code is covered: it sends `x-claude-code-session-id`, which Phase 3 uses. A client without a session header would get a timeline that is a flat list of requests, so the fallback rule is deferred to the first such client.
- **Options:** hash the conversation prefix (the first N messages are stable within one chat); time-window + same model; client-specific metadata if the client sends any.
- **Leaning:** prefix hashing, with the time window as a tiebreaker.

**OQ-3. How is the gateway exposed to Cursor's servers?** *(decide by the former Phase 3)*
**Withdrawn (Oct 2026): Cursor dropped from v1, see [ADR 0006](docs/decisions/0006-drop-cursor-from-v1.md).**
- **Why it matters:** Cursor can't reach `localhost`. A tunnel makes my gateway reachable from the internet.
- **Options:**
  - **Cloudflare Tunnel:** free, stable named URL.
  - **ngrok:** easiest to start; the free URL changes, so the Cursor setting must be updated each time.
  - **Tailscale Funnel:** good if I already use Tailscale.
- **Constraint regardless of choice:** gateway token auth on every externally reachable route.

**OQ-4. How is usage audited for models whose tokenizer isn't public?** *(decide by Phase 9)*
- **Why it matters:** tiktoken approximates Claude and Gemini tokenization poorly, so a 5% audit threshold would raise false alarms.
- **Options:** call the provider's own count-tokens endpoint (exact, but extra requests); use a wider tolerance per model family; audit only OpenAI-family models.
- **Leaning:** per-family tolerance in config, with provider count-tokens as an opt-in exact check.

**OQ-5. Should secrets inside prompt and tool-result content be redacted?** *(decide by Phase 2)* **Resolved:** stored as-is on a trusted machine, protected by `0700`/`0600` permissions; masking is a later opt-in ([ADR 0005](docs/decisions/0005-secrets-in-captured-content.md)).
- **Why it matters:** agents routinely read `.env` files, run `env`, or print tokens. Those land in tool results, and then in my capture store.
- **Options:** store as-is (the machine is trusted, fidelity is highest); scan with secret patterns and mask before storage; encrypt the store at rest.
- **Trade-off:** masking changes the captured copy, never the forwarded traffic, but the timeline then shows less than what the model actually saw.

**OQ-6. How much of Cursor actually flows through a custom base URL?** *(verify in the former Phase 3)*
**Withdrawn (Oct 2026): Cursor dropped from v1, see [ADR 0006](docs/decisions/0006-drop-cursor-from-v1.md).**
- **Why it matters:** sources disagree on whether agent mode and sub-agents use the custom key. If most agent traffic bypasses the gateway, Cursor observability is shallow.
- **How to resolve:** test empirically on my Cursor version and record the result in `docs/clients/cursor.md`.

**OQ-7. What is the viewer?** *(decide by Phase 3)* **Resolved:** a local CLI first, a web UI later ([ADR 0007](docs/decisions/0007-viewer-cli-first.md)).
- **Options:** a CLI (fast to build, greppable); a small local web UI (better for long timelines and nested sub-agents); Grafana panels only (no custom code, but poor for reading conversations).
- **Leaning:** CLI first, web UI once the canonical event model is stable.

---

## 10. How the work is organised

### Repo layout
```
AGENTS.md                  # rules for any agent working in this repo
PLAN.md                    # this file
.claude/
  templates/               # agent-loop kit templates: spec, plan, tasks, research, brief, cr
  templates.local/         # repo overrides, used first by loop.sh (spec.md adds the Agnosticism check)
docs/decisions/            # ADRs: NNNN-title.md (context, decision, alternatives, consequences)
docs/clients/              # one page per client: how to connect, known limits
specs/
  003-sessions-and-timeline/
    spec.md                # WHAT and WHY: drafted with me, approved by me
    plan.md                # HOW: agent-drafted, human-approved
    tasks.md               # T001 blocks, one commit each; progress lives in .agent-loop/, not here
    research.md            # open questions, decisions and notes: feature-local
  ...
internal/
  core/                    # proxy engine, canonical events: knows no provider
  protocols/<name>/        # one adapter per wire protocol
  clients/<name>/          # one profile per client/harness
  providers/               # upstream config, pricing
cmd/  deploy/
```

### Flow per feature
Features from 003 on run through the agent-loop kit (`.claude/scripts/loop.sh` and the `/al-*` commands). Specs 000–002 predate it and keep their old formats as history.

1. **Spec.** `/al-spec` creates the branch (`feat/NNN-slug`, `fix/NNN-slug` or `chore/NNN-slug`) and `specs/NNN-slug/`, interviews me, and drafts `spec.md` with `status: draft`. I approve it with `/al-approve`. No code before the spec is approved.
2. **Plan and tasks.** `/al-plan` drafts `plan.md` and `tasks.md` against the approved spec and asks me its open questions; `/al-approve` approves both.
3. **tasks.md.** One `### T001 — …` block per task, in build order, each one commit that leaves `make verify` green (format: `.claude/templates/tasks.md`). Nobody ticks it: progress lives in `.agent-loop/` and git (`loop.sh status`). A blocked task is named by its question line in `research.md` (`- **Q1** (open) T003 — …`), never marked on the task.
4. **Build.** `/al-implement` runs implementer → checks → reviewer → fix rounds per task, until everything passes or my input is needed. When a task is blocked, I answer with `/al-answer Qn <answer>` and continue with `/al-resume`.
5. **Changes.** A change to an approved spec, plan or tasks goes through `/al-change`, never a direct edit. After merge, a new feature with `/al-spec --supersedes NNN`.
6. **Decisions.** Anything decided mid-way that outlives the feature becomes an ADR. Resolving an OQ always produces an ADR, and this file is updated in the same PR.
7. **PR.** Links the spec, lists each acceptance criterion with its evidence (test name, log, screenshot), and notes any deviation from the plan.
8. **Close.** The kit has no close step. When a feature's PR merges, I set `status: done` on its `spec.md`, `plan.md` and `tasks.md` in a follow-up commit.

Small fixes don't need a spec folder. The PR description states the bug, the cause, the fix, and the test that proves it.

### AGENTS.md should contain
- Project goal, the "not a harness" line, and the priority order (a link to this file)
- The agnosticism rules from §2, which are **checked in every review**: no provider or client logic in `core/`
- Workflow: the agent-loop kit (`/al-spec` → `/al-plan` → `/al-implement`); never code outside the current spec's scope
- Hard rules: §5
- Stack: §8, with an instruction not to introduce a dependency without an ADR
- Commands: build, test (`go test -race ./...`), lint, compose up
- Conventions: package layout, error wrapping, logging fields, test style
- When unsure: stop, add the question to the feature's `research.md` as `- **Qn** (open) Tnnn — …` under `## Open questions`, and don't guess

### spec.md skeleton
New specs are rendered from `.claude/templates.local/spec.md`: the kit's template plus one repo section, the Agnosticism check.
```
# NNN — Feature name
## Problem              who has it, what happens today, why it matters
## Goal                 what is true when this is done
## Non-goals            what this explicitly does not do
## Acceptance criteria  - **AC1** — When <trigger>, the system shall <result>.
## Edge cases           - **E1** — <situation> → <expected behaviour> (AC2)
## Constraints          performance, security, compatibility, retention
## Agnosticism check    which axis this touches; confirm core/ is unchanged or justify it; OQ-n → ADR link
## Open questions       [NEEDS CLARIFICATION: …] until answered
## Changelog
```
Goal, Non-goals, Acceptance criteria, Edge cases and Constraints are the approved contract; changing them needs `/al-change`. The Agnosticism check is a review aid outside it. From the old skeleton: Scope / Out of scope → Goal / Non-goals; Do / Don't → Constraints / Non-goals.

### research.md: the feature's working memory

**What goes here:** the drilled-down stuff that's too detailed for the spec or plan but worth remembering:
- **Flow:** "does Claude Code send `count_tokens` before or after the first message?"
- **Low-level technical:** "`ReverseProxy` buffers when `Content-Length` is set — how to force flush?"
- **Limits hit:** "Claude Code's `HEAD /api/hello` sends no `x-app`, so the profile can't label it."
- **Roadblocks and workarounds:** what blocked, what unblocked it.

**Where it's referenced:** a question names the task it blocks on its own line, and commit git notes cite questions (`Research: … (Q2, Q5)`). The spec and plan don't link to it; they stay at the level of intent and approach.

**Escalation rules**, so nothing important stays buried:
| If the answer… | Then |
|---|---|
| changes scope, acceptance criteria, or a constraint | change `spec.md` itself (not a link) through `/al-change` once it is approved, and note `→ spec` |
| changes the approach | change `plan.md` itself through `/al-change` once it is approved, and note `→ plan` |
| matters beyond this feature, or is hard to reverse | write an ADR, and note `→ ADR-NNNN` |
| is local to this feature | it stays here; that's the default |

**Entry format** (`.claude/templates/research.md`). The loop and its hooks parse only this question line, and only under `## Open questions`; anything else there is invisible to them:
```
## Open questions
- **Q1** (open) T003 — Does ReverseProxy delay SSE flushes behind the capture wrapper? — options: A) set FlushInterval -1 B) flush in the tee
```
`/al-answer Q1 <answer>` changes `(open)` to `(answered)` and writes the answer underneath:
```
- **Q1** (answered) T003 — Does ReverseProxy delay SSE flushes behind the capture wrapper? — options: A) set FlushInterval -1 B) flush in the tee
  - **Answer:** A; text/event-stream already flushes at once, test TestProxy_StreamsSSEWithoutBuffering (2026-10-11)
```
Decisions taken along the way go under `## Decisions`, and everything else under `## Notes`:
```
## Decisions
- D1 (2026-10-11) — Session ID comes from the client profile, not core — core must not name a client header
```
Entries are never deleted. A wrong answer later gets a new dated line, not an edit, so the history of the confusion is kept.

---

## 11. Commit conventions

Every commit message is written with intent. No `wip`, `fix stuff`, `update`, or auto-generated summaries.

**The format itself is not defined here.** It is a personal, cross-repository standard, kept in one place so it can't drift per project: the user-level skill `~/.claude/skills/commit-conventions/SKILL.md`. Any agent working in this repo loads that skill before writing a commit message or a PR description. In short, it requires `<type>(<scope>): <imperative summary>` — lowercase, no trailing period, subject ≤ 72 characters — then a blank line and one to three present-tense sentences saying what the commit does and why; one logical change per commit; extended context in a git note rather than the body; and **no `Co-authored-by` or any other AI attribution trailer**, because I am the author of record.

What is repo-specific, and so belongs here:

### Scopes

A scope is a noun naming the area touched, matching this codebase: `proxy`, `capture`, `anthropic`, `openai-chat`, `claude-code`, `cost`, `ratelimit`, `routing`, `store`, `observability`, `config`, `server` (`internal/server`, `cmd/gateway`), `logging`, `deploy` (Dockerfile, compose, image), `docs`, `repo` (repo-level tooling, hooks, process docs).

**Branch and commit agree.** Commits on `feat/003-sessions-and-timeline` use scopes from that feature.

### Git notes in this repo

Add a note (`git notes add -m "<note>" <commit>`) when the commit involves a decision, a trade-off, or a spec criterion:

```
Spec: specs/003-sessions-and-timeline/spec.md (AC2, AC4)
ADR: docs/decisions/0004-capture-store.md
Research: specs/003-sessions-and-timeline/research.md (Q2, Q5)
Why this approach: <one or two lines>
Alternatives rejected: <one line each>
Trade-off / known limit: <…>
Verified by: <test names, manual check>
```

Repo setup, so notes survive and travel:
```
git config core.hooksPath .githooks
git config notes.rewriteRef refs/notes/commits
git config --add remote.origin.fetch '+refs/notes/*:refs/notes/*'
git push origin refs/notes/commits
```
GitHub doesn't display notes, so the PR description still carries the essentials (spec link, acceptance-criteria evidence). Notes are the permanent per-commit record; the PR is the review surface.

### Enforcement

The skill gets the message right when it is written. The repo assumes it wasn't:

- A **`commit-msg` hook** at `.githooks/commit-msg`, enabled with `git config core.hooksPath .githooks`:
  - rejects a subject that doesn't match `^(feat|fix|refactor|perf|test|docs|build|ci|chore|revert)(\([a-z0-9-]+\))!?: .+$`, ends in a period, or runs over 72 characters;
  - rejects any `Co-authored-by:` or AI-attribution trailer.
- A **`pre-push` hook** at `.githooks/pre-push` re-runs `.githooks/commit-msg` on every commit in the pushed range, so a commit made with `git commit --no-verify` can't sneak through. There is no CI; this local gate replaces it ([ADR 0001](docs/decisions/0001-local-gate-instead-of-ci.md)).
- Never bypass either with `--no-verify`.
