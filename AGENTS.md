# LLM Gateway

An observability-first LLM gateway in Go, between any LLM client and any provider,
capturing prompts, responses, tool calls, tokens and cost. **Not a harness** — it never
runs agents, executes tools or edits files. **Not tied to one vendor** — Claude Code and
Cursor are only the first clients. Full intent, phases, stack rationale and open
questions: `PLAN.md`, which wins wherever this file disagrees with it.

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

## Hard rules (PLAN §5)
- Passthrough first, transform later. Forward bytes faithfully; parse copies on the side.
- Forward headers and body fields as open lists. Never allowlist today's set.
- Streams are sacred: no buffering, forward keep-alive pings, keep upstream `content-type`.
- Errors pass through verbatim, including `retry-after` and rate-limit headers.
- Don't reshape prompts: `system`, `cache_control` and message structure stay as sent.
- Capture never blocks; if capture fails the request still succeeds.
- Credentials — auth headers (`Proxy-Authorization` included) and keys — never land
  in storage.
- No route reachable from outside localhost without a gateway token.
- Stateless process; state lives in external stores.

## Workflow
Read `specs/NNN-*/spec.md` → follow its `plan.md` → tick `tasks.md` as you go. Ignore
spec folders whose `spec.md` has `status: done`. Never write code outside the current
spec's scope. One branch per feature or fix: `feat/NNN-slug`, `fix/slug`, `chore/slug`.

Every doubt, roadblock or limit you hit goes in the same folder's `research.md` as a
numbered query. The task it holds up ends with `(blocked: Q2)`; when you record the
answer that becomes `(shaped: Q2)`. The link stays on the task line either way — the
status lives in `research.md`. **Entries there are never deleted:** a correction is a
new dated line, not an edit, so the history of the confusion is kept.

If an answer changes scope or a do/don't, edit `spec.md`; if it changes the approach,
edit `plan.md`; if it outlives the feature or is hard to reverse, write an ADR in
`docs/decisions/`.

## Stack
Decided in `PLAN.md` §8. Do not introduce a dependency without an ADR.

## Commands
- install: `make setup`
- verify:  `make verify`
- run:     `make run`

Commit messages follow the user-level `commit-conventions` skill
(`~/.claude/skills/commit-conventions/SKILL.md`, PLAN §11). It lives outside this repo,
so install it on any machine or harness that commits here.

## Do not
- Do not weaken lint, tests or config to make a check pass.
- Do not add suppressions, skip tests, or delete assertions.
- Do not add a dependency without an ADR.
- When unsure, stop. Add the question to the feature's `research.md` as `open`, link it
  from the blocked task, and do not guess.
