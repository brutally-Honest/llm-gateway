# LLM Gateway — Project Plan (gist)

> The one-page source of intent and design: goal, agnosticism, design rules, phases, stack, open questions. If a spec or PR contradicts this file's intent or design, either this file is updated (with an ADR) or the other thing is wrong. How work is done (workflow, commits, hooks) is in [AGENTS.md](AGENTS.md).

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
- **Stateless process (target).** State lives in external stores. *Documented v1 exceptions:* v1 keeps its state on local disk, the SQLite store and blob files under `capture.dir` ([ADR 0004](docs/decisions/0004-capture-store.md), `docs/capture.md`), so it is one process on one machine; and the rate limiter keeps per-key counts in process memory, so they reset on restart and are not shared between instances. It sits behind a `Limiter` strategy and is replaced by the Redis strategy when instances exist ([ADR 0008](docs/decisions/0008-in-memory-fixed-window-limiter.md)).
- **Provider-reported usage is primary; our own estimate is the auditor.** How we estimate per tokenizer is OQ-4.

## 6. Out of scope (v1)

- Being a harness or agent runtime of any kind
- Cross-protocol translation (e.g. an Anthropic-format client → an OpenAI-only model). Designed for, not built.
- Multi-user features: virtual keys, per-user budgets, user management, hosted UI. The seams exist (§2, "Scale"); the features don't.
- Routing (same-protocol model aliases, weighted selection, fallback, circuit breaker) and real auth (virtual keys, SSO, a gateway token on tunnelled routes). Removed from v1 by [ADR 0010](docs/decisions/0010-remove-routing-and-auth-from-v1.md); the seams in §2 stay.
- Semantic caching, MCP, A2A, admin UI, SDK

## 7. Phases

Each phase maps to one or more features, and each feature gets its own spec folder and branch.

| # | Phase | Goal | Done when |
|---|---|---|---|
| 0 | Foundation | Repo, AGENTS.md, spec templates, docker-compose (the gateway's own was removed in 031), local gate (`make verify`: lint, test with `-race`, web-test; pre-push hook), logging, `/healthz` | On a fresh clone, after `make setup`, `make verify` passes, and a push containing a malformed commit subject or a failing check is rejected |
| 1 | Core + first protocol | Adapter/profile interfaces; Anthropic Messages passthrough (`/v1/messages`, `count_tokens`, `/v1/models`, `HEAD /api/hello`); Claude Code profile | A real Claude Code session (API key **and** subscription) runs through the gateway with no behaviour difference<br>API-key half pending: AC44 deferred, tracked in `specs/001-core-and-anthropic-passthrough/research.md` Q4. |
| 2 | Capture + canonical events | Async raw capture with redaction; Anthropic parser → canonical events | Every exchange is stored and parsed; killing the store doesn't break the client |
| 3 | Sessions & timeline | Group Claude Code requests into sessions via `x-claude-code-session-id`; viewer | Claude Code sessions replay as ordered timelines: prompt → tool calls → results. The timeline reads only canonical events, never Anthropic wire types, enforced by a purity test on the timeline package |
| 4 | Usage (tokens) | Usage from the provider's response: the four counters (input, output, cache write, cache read) per model call, shown per exchange and summed per session; no prices | Every model call shows the counters from its provider usage event; session totals sum them and mark unknown, partial, or errored calls, never as 0 |
| 5 | Rate limiting | Per gateway key, requests and tokens per minute enforced by the gateway; in-memory fixed window in v1 behind a `Limiter` strategy (Redis deferred, [ADR 0008](docs/decisions/0008-in-memory-fixed-window-limiter.md)); well-formed 429s with `retry-after` | Over a limit, Claude Code gets a 429 and backs off and retries cleanly |
| A | Web UI | A read-only local page on its own loopback listener: sessions, timelines built from canonical events, live updates for active sessions, exchange detail ([ADR 0009](docs/decisions/0009-web-ui-vanilla-js-read-only.md)) | A session replays in the browser as `gateway timeline` prints it (CLI removed in 031), and an active session updates within moments of each exchange being stored; the UI never blocks or fails proxying or capture |
| 6 | Ground truth (optional) | Ingest harness hooks / OTel events; correlate with captured tool calls | The timeline shows "model asked" vs "harness did" |
| C | File system and network visibility | Show file-system and network activity alongside the timeline and the captured tool calls | Defined in its own spec when it starts |
| 7 | Routing: **removed from v1** ([ADR 0010](docs/decisions/0010-remove-routing-and-auth-from-v1.md)) | Same-protocol model aliases, weighted selection, fallback + circuit breaker. Not scheduled | Not in v1 |
| 8 | Self-observability | Prometheus `/metrics` on the API and UI listeners (requests, upstream latency and errors, capture, tunnels and UI streams, UI build times, start and stop; low-cardinality labels); OTel spans of a proxied call → Jaeger, off by default; a compose stack (Prometheus, Grafana with a provisioned dashboard, Jaeger) on loopback beside the gateway ([spec 021](specs/021-self-observability/spec.md), [ADR 0018](docs/decisions/0018-prometheus-client-and-otel-sdk.md), `docs/observability.md`) | The gateway's own p50/p99 overhead is visible |
| 9 | Cost: API-equivalent, from captured tokens and published prices; no API key needed | Price each model call from the Phase 4 counters using a pricing table of published prices keyed by provider + model, as what the call would cost on the API (a subscription session included); estimate audit (OQ-4) | Every model call and session shows its API-equivalent cost from the published prices; unknown models are shown as `unknown` |

The former Phase 3 (a second wire protocol and its client profile, gateway token auth, a public tunnel) was removed and the later phases moved up by one ([ADR 0006](docs/decisions/0006-drop-cursor-from-v1.md)).

The v1 order is now A → 6 → C → C v2 (effect attribution) → web UI v2 → 9 (cost, API-equivalent) → 8 (self-observability). Routing (old Phase 7) and real auth are removed from v1 ([ADR 0010](docs/decisions/0010-remove-routing-and-auth-from-v1.md)).

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
| Rate-limit state | **In-memory fixed-window limiter** behind a `Limiter` strategy for v1; **Redis** + Lua token bucket deferred to multi-instance use | One process needs no shared state, so no new runtime service. The `Limiter` interface lets the Redis strategy (atomic check-and-decrement across instances) replace it without changing the middleware. | **Redis + Lua token bucket now:** atomic across instances and keeps the process stateless, but adds a runtime service for a single local process. Its counts also live outside the process, so it is the planned strategy once instances exist. | Revised for v1: in-memory, a documented temporary exception to §5 ([ADR 0008](docs/decisions/0008-in-memory-fixed-window-limiter.md)); Redis deferred | Phase 5 |
| Config | **One YAML file** (providers, pricing, client profiles, routes), parsed with **`go.yaml.in/yaml/v3`**; **secrets only from env vars** | Adding a provider or model price is a config edit, not code, which is agnosticism made concrete. Secrets never sit in a file that could be committed. | **DB-backed config:** needed for an admin UI, which is out of scope. **Env-only:** unreadable for nested pricing and routing tables. | Decided ([ADR 0002](docs/decisions/0002-config-format-and-loader.md)) | Phase 0 |
| Logging | **zap** (structured JSON) | Very low allocation on the hot path; mature; structured fields make logs queryable by session or request ID. | **`log/slog`** (stdlib): a credible option with no dependency. zap was already chosen and can sit behind a slog handler, so switching later is cheap. | Decided | Phase 0 |
| Metrics | **Prometheus + Grafana** | The de-facto standard. Pull-based, so no agent is needed. Histograms give p50/p99 latency overhead, one of my learning goals. | **StatsD:** no native histograms/percentiles. **Datadog etc.:** SaaS, costs money, and data leaves my machine. | Decided | Phase 8 |
| Tracing | **OpenTelemetry → Jaeger** | OTel is vendor-neutral, which matches the agnostic principle: the backend can swap without code changes. Jaeger gives a local UI in one container. | **Vendor SDKs:** lock-in. **Grafana Tempo:** fine, but needs more setup to browse locally than Jaeger. | Decided | Phase 8 |
| Token estimation | **tiktoken-go** for OpenAI-family models | Independent count to audit provider-reported usage. | Its accuracy for non-OpenAI tokenizers (Claude, Gemini) is the problem in OQ-4. | Decided for OpenAI; **Open** for others | Phase 9 |
| Testing | **Table-driven tests, `-race`, recorded stream fixtures** per protocol (testcontainers-go is not used: v1 has no Redis or DB service to test against) | Race detector catches tee/capture concurrency bugs. testcontainers gives real Redis/DB, not mocks that lie. Recorded real SSE streams (golden files) let each adapter be tested without live API calls or cost. | **Mocked stores only:** they pass while the real thing fails. **Live-API tests in CI:** cost money and flake. | Decided | Phase 0 |
| Packaging | **docker-compose** for the stack; gateway also runnable as a plain binary | One command (`make observability-up`) brings up Prometheus, Grafana, Jaeger. Running the binary directly keeps the dev loop fast. | **Kubernetes:** irrelevant for local single-user. | Decided; the gateway's own image and compose file were removed in 031: the gateway runs as a binary (native install), and compose runs only the observability stack | Phase 0 |
| Viewer | **Local CLI** (`gateway sessions`, `gateway timeline`), greppable and reading canonical events directly | Fast to build, and it reads canonical events directly. | **Grafana panels only:** poor for reading conversations. | Superseded for the web UI by [ADR 0009](docs/decisions/0009-web-ui-vanilla-js-read-only.md); the CLI viewer was removed in 031 ([ADR 0007](docs/decisions/0007-viewer-cli-first.md)) | Phase 3 |
| Web UI | **Vanilla JS** (ES modules, custom elements, `<template>`, `fetch`, `EventSource`, hash routing) embedded with `go:embed`; no npm, no build step | No Node in the build, so the binary stays one static file. The page reads the same timeline entries the CLI did. Read-only, loopback only. | **React and Preact + Vite:** need Node in the build. **htmx:** a dependency that limits a rich timeline. **Preact + htm:** kept as the recorded fallback if vanilla becomes too costly. | Decided ([ADR 0009](docs/decisions/0009-web-ui-vanilla-js-read-only.md)) | Phase A (web UI) |

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

**OQ-4. How is usage audited for models whose tokenizer isn't public?** *(decide by Phase 9)* **Deferred** with AC44 (roadmap, Later): a usage audit needs the Console's usage records. Phase 9 prices the provider-reported usage as captured (spec 020).
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

## 10. Repo layout, and where the workflow rules live

How work is done here (the per-feature flow, spec and research.md formats, ADR escalation, commit conventions, git notes, hooks) is owned by [AGENTS.md](AGENTS.md), not by this file.

```
AGENTS.md                  # how work is done: workflow, commits, hooks
PLAN.md                    # this file: intent, design rules, phases, stack
docs/decisions/            # ADRs: NNNN-title.md (context, decision, alternatives, consequences)
docs/clients/              # one page per client: how to connect, known limits
specs/
  003-sessions-and-timeline/
    spec.md                # WHAT and WHY: drafted with me
    research.md            # decisions and notes: feature-local
  ...
internal/
  core/                    # proxy engine, canonical events: knows no provider
  protocols/<name>/        # one adapter per wire protocol
  clients/<name>/          # one profile per client/harness
cmd/  deploy/
```
