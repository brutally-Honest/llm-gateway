# LLM Gateway

An observability-first LLM gateway in Go, between any LLM client and any provider,
capturing prompts, responses, tool calls, tokens and cost. **Not a harness** — it never
runs agents, executes tools or edits files. **Not tied to one vendor** — Claude Code is
only the first client.

Two files, two jobs. `PLAN.md` is the plan: intent, agnosticism, design rules (§5),
phases, stack rationale and open questions. This file is how work is done here:
workflow, branches, commits, notes, hooks. Neither file overrides the other.

## Priority order
Observability → cost → rate limiting → routing → everything else.
A lower priority never degrades a higher one.

## Agnosticism (checked in every review)
- `internal/core/` knows no provider and no client. No provider- or client-specific
  `if` outside `internal/protocols/<name>/` and `internal/clients/<name>/`.
- Everything downstream reads only canonical events (`session`, `request`, `message`,
  `tool_call`, `tool_result`, `usage`, `error`) — never a provider's raw format.
- Unknown client → still proxied and captured, labelled `unknown`.
- Unknown model → still captured; cost is `unknown`, never an error.
- Adding a client, protocol or provider = a new profile, adapter or config entry plus
  its contract tests. The core stays unchanged.

## Hard rules
A checklist. The rules and their reasons are PLAN §5, which owns them.
- Passthrough first, transform later. Forward bytes faithfully; parse copies on the side.
- Forward headers and body fields as open lists. Never allowlist today's set.
- Streams are sacred: no buffering, forward keep-alive pings, keep upstream `content-type`.
- Errors pass through verbatim, including `retry-after` and rate-limit headers.
- Don't reshape prompts: `system`, `cache_control` and message structure stay as sent.
- Capture never blocks; if capture fails the request still succeeds.
- Credentials — auth headers (`Proxy-Authorization` included) and keys — never land
  in storage.
- No route reachable from outside localhost without a gateway token.
- Stateless process is the target; v1 keeps its state on local disk (SQLite store and
  blob files under `capture.dir`), plus the rate limiter's counts in memory (PLAN §5).

## Workflow
Each change gets `specs/NNN-slug/spec.md` and `research.md`. There is no plan.md, no
tasks.md and no approval fields; a spec may carry one frontmatter line, `status: draft`
or `status: done`. Never code outside the current spec's scope.

1. **Spec.** Branch `feat/NNN-slug`, `fix/NNN-slug` or `chore/NNN-slug`;
   `specs/NNN-slug/spec.md` is drafted with the human (skeleton below). For a change that
   touches a provider, client or protocol axis, the spec confirms `internal/core/` is
   unchanged or justifies it.
2. **Build.** Built without stopping on judgment calls: each is a D-entry under
   `## Decisions` in `research.md`, except the cases under "Always stop and ask first".
   Commits follow "Commits" below.
3. **Decisions.** A decision that outlives the feature or is hard to reverse becomes an
   ADR in `docs/decisions/` (status line as in `docs/decisions/0000-template.md`), noted
   in `research.md` as `→ ADR-NNNN`. Resolving an open question (OQ) in PLAN §9 always
   produces an ADR, and PLAN.md is updated in the same PR.
4. **PR.** Links the spec, lists each acceptance criterion with its evidence (test name,
   log, screenshot) and notes any deviation from the spec. Opening one needs the
   human's go-ahead (see below).

Small fixes don't need a spec folder. The PR description states the bug, the cause, the
fix and the test that proves it.

Manual checks for UI work live in `docs/manual-checks/`. Entries in `research.md` are
never deleted: a correction is a new dated line, so the history of the confusion is kept.

The agent-loop kit (brutally-Honest/agent-loop) was removed in 031 and may be installed
again later. Its repo settings are in `specs/031-remove-clutter/research.md`. Specs
003-017 were built with it and keep its frontmatter as history; specs 000-002 predate
it. See ADR 0021.

### spec.md skeleton
```markdown
---
feature: NNN-slug
kind: feat | fix | chore
created: YYYY-MM-DD
branch: feat/NNN-slug
---

## Problem              who has it, what happens today, why it matters
## Goal                 what is true when this is done
## Non-goals            what this explicitly does not do
## Acceptance criteria  - **AC1** — When <trigger>, the system shall <result>.
## Edge cases           - **E1** — <situation> → <expected behaviour> (AC2)
```

### research.md: the feature's working memory
Whatever is too detailed for the spec but worth remembering:
- **Flow:** "does Claude Code send `count_tokens` before or after the first message?"
- **Low-level technical:** "`ReverseProxy` buffers when `Content-Length` is set — how to force flush?"
- **Limits hit:** "Claude Code's `HEAD /api/hello` sends no `x-app`, so the profile can't label it."
- **Roadblocks and workarounds:** what blocked, what unblocked it.

Every judgment call is a D-entry under `## Decisions`; everything else goes under
`## Notes`:
```
## Decisions
- **D1** (2026-10-11) — Session ID comes from the client profile, not core — core must not name a client header
```
Commit git notes cite decisions (`Research: … (D2, D5)`). The spec doesn't link to
`research.md`; it stays at the level of intent.

| If the decision… | Then |
|---|---|
| matters beyond this feature, or is hard to reverse | write an ADR, and note `→ ADR-NNNN` |
| is local to this feature | it stays in `research.md`; that is the default |

## Commits
Every commit message is written with intent. No `wip`, `fix stuff`, `update`, or
auto-generated summaries.

The format is not defined here. It is a personal, cross-repository standard in the
user-level `commit-conventions` skill (`~/.claude/skills/commit-conventions/SKILL.md`).
It lives outside this repo, so ask the human to install it on any machine or harness that
commits here. Load it before writing a commit message or a PR description. In short:
`<type>(<scope>): <imperative summary>` — lowercase, no trailing period, subject ≤ 72
characters — then a blank line and one to three present-tense sentences saying what and
why; one logical change per commit; extended context in a git note, not the body; and no
`Co-authored-by` or other AI attribution trailer.

### Scopes
A scope is a noun naming the area touched, matching this codebase: `proxy`, `capture`, `anthropic`, `openai-chat`, `claude-code`, `cost`, `ratelimit`, `routing`, `store`, `observability`, `config`, `server` (`internal/server`, `cmd/gateway`), `logging`, `ui` (`internal/ui`, the web page and its API), `deploy` (`deploy/`, the observability compose stack), `docs`, `repo` (repo-level tooling, hooks, process docs), `core` (`internal/core`, the proxy engine), `timeline` (`internal/timeline`), `install` (`gateway install`, `uninstall`, `doctor`, `install.sh`), `gateway` (`cmd/gateway`), `harness-hooks` (the harness hook events and endpoint), `contentcoding` (response decompression), `session-token-usage` (the Phase 4 usage counters), `spec` (files under `specs/`), `adr` (`docs/decisions/`), `roadmap` (`docs/roadmap.md`), `verify` (the `make verify` gate).

Where `gateway` and `server` overlap (`cmd/gateway`), use `server` for new commits.

**Branch and commit agree.** Commits on `feat/003-sessions-and-timeline` use scopes from
that feature.

### Git notes
Add a note (`git notes add -m "<note>" <commit>`) when the commit involves a decision, a
trade-off, or a spec criterion:

```
Spec: specs/003-sessions-and-timeline/spec.md (AC2, AC4)
ADR: docs/decisions/0004-capture-store.md
Research: specs/003-sessions-and-timeline/research.md (Q2, Q5)
Why this approach: <one or two lines>
Alternatives rejected: <one line each>
Trade-off / known limit: <…>
Verified by: <test names, manual check>
```

Repo setup (`make setup` runs it), so notes survive a rebase:
```
git config core.hooksPath .githooks
git config notes.rewriteRef refs/notes/commits
git config --add remote.origin.fetch '+refs/notes/*:refs/notes/*'
```
Notes are local by default. Pushing `refs/notes/*` changes a remote ref, so it needs the
human's explicit go-ahead (see "Always stop and ask first"). The fetch refspec stays so
notes can be fetched if they are ever pushed. GitHub doesn't display notes anyway, so the
PR description still carries the essentials (spec link, acceptance-criteria evidence).

### Enforcement
The skill gets the message right when it is written. The repo assumes it wasn't:
- `.githooks/commit-msg` (enabled by `core.hooksPath`) rejects a subject that doesn't match
  `^(feat|fix|refactor|perf|test|docs|build|ci|chore|revert)(\([a-z0-9-]+\))!?: .+$`, ends
  in a period, or runs over 72 characters, and rejects `Co-authored-by:` and AI-attribution
  lines.
- `.githooks/pre-push` re-runs `commit-msg` on every commit in the pushed range, so a commit
  made with `--no-verify` can't get through, and blocks the push unless the pushed sha is
  HEAD, tracked files are clean and `make verify` passes (ADR 0001).
- CI runs the `verify` workflow on Linux and macOS for every PR and every push to main
  (ADR 0019, which supersedes ADR 0001's "no CI").
- Bypassing either hook is under "Always stop and ask first".

## Stack
Decided in `PLAN.md` §8. Adding a dependency is under "Always stop and ask first".

## Commands
- install: `make setup`
- verify:  `make verify`
- run:     `make run`
- observability stack: `make observability-up` / `make observability-down`

## Do not
- Do not weaken lint, tests or config to make a check pass.
- Do not add suppressions, skip tests, or delete assertions.
- Never get around a guard, hook or check by splitting, quoting, encoding or renaming
  strings, or by using another tool to perform the action that was blocked. Reading or
  editing a file with Read/Edit/Write is fine when only a Bash command naming it was
  blocked.
- When unsure, record the call as a D-entry in research.md and continue (Workflow step 3
  says when it also gets an ADR). If it cannot be decided safely, or it is listed under
  "Always stop and ask first", stop and ask.

## Always stop and ask first
A D-entry is for judgment calls inside the repo. These need the human's explicit go-ahead
every time, even mid-build:
- Anything outside the repo: other directories, system config, external services.
- Any push or merge (local or pull request), or change to a remote branch or ref (notes included).
- Deleting user data: local stores, captures, untracked files, or anything not in git.
- Adding a dependency: it needs an ADR and the human's go-ahead, even with the ADR drafted.
- Bypassing, disabling or skipping a hook, guard or check (`--no-verify` included).
