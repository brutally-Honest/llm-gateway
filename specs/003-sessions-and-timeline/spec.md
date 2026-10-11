---
feature: 003-sessions-and-timeline
kind: feat
status: approved
version: 1
created: 2026-10-11
supersedes:
branch: feat/003-sessions-and-timeline
approved: 2026-10-11T03:18:33Z
approved-by: human
sha256: 9156f236294f44373dd8c159b905fd9cfbaaf2aa8381e1e3c1e544d86c563add
fingerprint: 68cb3d068f27d522de3ee67141de9cf03e1897541d0c21cea129bd00686dd1e9
---
# Sessions and timeline

## Problem
Every captured exchange is stored and parsed (002), but each one stands alone. I can't
see one Claude Code session as a story: what I asked, which tools the model called, what
came back, in order. `gateway dump` shows one exchange at a time, and each request
resends the full history, so reading exchanges one by one repeats everything.

This is Phase 3 of PLAN.md §7. It also resolves OQ-7 (what is the viewer): CLI first.

## Goal
- Captured exchanges are grouped into sessions by an opaque session key that the client
  profile extracts (for Claude Code, the `x-claude-code-session-id` header).
- `gateway sessions` lists sessions and `gateway timeline` replays one as an ordered
  timeline (prompt → tool calls → tool results → response), each message shown once,
  not once per resend.
- The timeline reads only canonical events and stored exchange columns, never a wire
  format, and a purity test enforces it.

## Non-goals
- OQ-2 fallback grouping for clients without a session key (prefix hashing, time windows).
- A web UI, live tailing, search.
- Cost per session (Phase 4).
- Ground truth: what the harness actually did (Phase 6).
- Any second client or protocol.
- A new canonical event kind for sessions: the session key is a column on exchanges.
  002's canonical events are otherwise unchanged.
- Visually separating sub-agent work from the main thread. Exchanges are grouped strictly
  by the key the profile returns.

## Acceptance criteria
Session key
- **AC1** — When a request matches a profile that reports a session key, the system shall store that key with the captured exchange, unparsed.
- **AC2** — When a Claude Code request carries `x-claude-code-session-id`, the system shall store the header's value as the exchange's session key.
- **AC3** — When a request's profile reports no key, or the request matches no profile (client `unknown`), the system shall capture the exchange with no session key; `gateway dump` shows it as before.
- **AC4** — When a profile reports a key that is empty, longer than 256 bytes or contains a control character, the system shall store the exchange with no session key and log the reason on the capture log, without the key's value.
- **AC5** — When a test-only profile, registered from a test, reports a session key, the system shall store and group by that key with no change to `internal/core/`.
- **AC6** — When the gateway starts on a store migrated by 002, the system shall add the session key to exchanges in a new migration step, with an index, leaving existing exchanges ungrouped and the released migration step unchanged.
- **AC7** — A session is identified by principal, client and key together: the same key from a different client or principal shall form a different session.

Listing sessions
- **AC8** — When the user runs `gateway sessions`, the system shall print one row per session, newest last activity first, each with the session key, client, principal, first and last exchange time, exchange count and a short preview of the first user prompt.
- **AC9** — When the user runs `gateway sessions` without `-n`, the system shall list the 20 most recent sessions; `-n <count>` shall change the limit and `-n 0` shall list all.
- **AC10** — When ungrouped exchanges exist, `gateway sessions` shall end with one `ungrouped` row giving their count and first and last time.
- **AC11** — When the user runs `gateway sessions -ungrouped`, the system shall list the ungrouped exchanges, newest first, one per line with request id, time, client, method, path and status.

Timeline
- **AC12** — When the user runs `gateway timeline <session-key>`, the system shall print the session's exchanges in start-time order (ties broken by request id), and within each exchange its new messages, tool calls, tool results, response and errors in event order.
- **AC13** — When the timeline shows an exchange, the system shall compare its message list (request history then response, by message content hash) with each earlier exchange of the same session, take as best match the one sharing the longest leading run of messages (ties: an exchange whose own list equals the shared prefix, then the most recent), and show only the messages after that shared prefix, so a message resent in later requests appears once.
- **AC14** — When the best match's own message list is longer than the shared prefix (a rewrite such as `/compact`), the timeline shall precede the exchange's messages with a `history diverged` marker; otherwise it shall show no marker.
- **AC15** — When an exchange in the session carries no message events (count_tokens, `/v1/models`, `HEAD /api/hello` and the like), the timeline shall show it as one folded line with time, method, path and status, and no messages.
- **AC16** — When an exchange's parse is `failed`, `skipped` or `unsupported_encoding`, the timeline shall show it as one folded line with time, path, status and parse state, naming its request id for `gateway dump`; when it is `partial`, the timeline shall show the events that exist, marked partial.
- **AC17** — When a text, tool input or tool result is longer than the timeline's per-entry limit, the system shall cut it and note how many bytes were left out; `-full` shall print everything.
- **AC18** — When the user runs `gateway timeline -last`, the system shall print the timeline of the session with the most recent exchange (the top row of `gateway sessions`).
- **AC19** — When a session key matches no session, `gateway timeline` shall exit non-zero with `session not found` and print nothing else to stdout.
- **AC20** — When a session key matches more than one session (different client or principal), `gateway timeline` shall print each matching session's timeline one after another, each headed by its client and principal.

Output
- **AC21** — By default `gateway sessions` and `gateway timeline` shall print human-readable text with times in the local zone as RFC 3339 with offset; `-json` shall print JSON lines with times in UTC RFC 3339.
- **AC22** — When `gateway sessions`, `gateway sessions -ungrouped` or `gateway timeline` runs, the system shall not write gateway.db or any blob, like `gateway dump`.
- **AC23** — When the store's schema is older or newer than the binary, `gateway sessions` and `gateway timeline` shall refuse with the same message as `gateway dump`.

Backfill
- **AC24** — When the user runs `gateway backfill-sessions`, the system shall set the session key of each stored exchange that has none, by giving the stored request headers to the profile registered under the exchange's client label, and print how many exchanges were filled, left without a key, and skipped (no such profile).
- **AC25** — When `gateway backfill-sessions` runs a second time, or while the gateway is capturing, the system shall change no exchange that already has a key and leave the store consistent.

Purity
- **AC26** — The timeline and viewer package(s) shall contain no provider or client name and no Anthropic wire identifier, enforced by a purity test on the same word lists as core's; `internal/core/` shall still name no client.

Threads
- **AC27** — When an exchange shares no leading message with any earlier exchange in the session, the timeline shall start it as a new thread showing all its messages, with no `history diverged` marker.

Manual smoke
- **AC28** — When a real Claude Code session, signed in with a subscription, that uses the Task tool is captured on a real machine, `gateway timeline -last` shall replay it in order (prompts, tool calls, tool results and responses each once), and research.md shall record whether the sub-agent sent the same `x-claude-code-session-id` as the parent.

## Edge cases
- **E1** — Header present with an over-long or control-character value → stored ungrouped, reason logged without the value (AC4)
- **E2** — Profile reports a key on a request whose capture is dropped (queue full) or whose store write fails → request still succeeds; capture rules from 002 unchanged (AC1)
- **E3** — Session with a single side-call exchange and no messages → listed with an empty preview; timeline shows one folded line (AC8, AC15)
- **E4** — Two exchanges share a start time → ordered by request id (AC12)
- **E5** — A user genuinely repeats the same message later in the session → shown again, because it falls after the shared prefix (AC13)
- **E6** — A sub-agent thread interleaves with the parent under the same key (parent P1, sub-agent S1, parent P2, sub-agent S2) → S1 shares no prefix with P1 and starts a new thread with no marker; P2's best match is P1 and S2's is S1, so each shows only its new messages and neither is marked diverged (AC13, AC14, AC27)
- **E7** — Request body truncated or stream aborted → events that exist are shown, marked partial (AC16)
- **E8** — Empty store, or no sessions → `gateway sessions` prints no session rows and exits zero; `timeline -last` exits non-zero with `session not found` (AC8, AC19)
- **E9** — Store read while the gateway writes → reads may miss the newest exchange but never fail on it (AC22)
- **E10** — Backfill on an exchange whose stored client is `unknown` or a profile no longer registered → skipped and counted (AC24)
- **E11** — Session key containing characters that are special on a terminal → printed escaped in text output (AC21)
- **E12** — `/compact` rewrites the history mid-session → the next exchange's best match is longer than the shared prefix, so `history diverged` precedes the rewritten messages (AC14)
- **E13** — Two earlier exchanges tie for the longest shared prefix → one whose own list equals the prefix wins, then the most recent (AC13)

## Constraints
- Extracting the key adds no I/O and no blocking to the proxy path; it reads request
  headers already in memory. Capture never blocks and never fails a request (002).
- Released migration steps are never edited; the session key is a new step.
- The session key is opaque to core: core stores, bounds and compares it, never parses it.
- The timeline reads only canonical events and stored exchange columns (PLAN §7 Phase 3).
- No new dependency.
- The decision "viewer = CLI first, web UI later" is recorded as an ADR, and PLAN.md
  OQ-7 and the §8 Viewer row are marked resolved with a link to it.

## Agnosticism check
- **Client:** the Claude Code profile gains session-key extraction from
  `x-claude-code-session-id`. The test-only profile (001 AC35) gains a key with no core
  change (AC5).
- **Protocol / provider:** unchanged. The timeline consumes canonical events only.
- `internal/core/` changes: the Profile contract (or an optional interface beside it)
  gains a way to return an opaque session key, and capture stores it. That's justified:
  it is client-agnostic by construction, and the purity test still holds (AC26).
- Resolves **OQ-7** (viewer: CLI first), with a new ADR in `docs/decisions/`. OQ-2 stays
  open.

## Open questions

## Changelog
- v1 (2026-10-11) — first draft; AC13/AC14 compare against one best match and mark divergence only on a rewrite; AC27 new thread; AC28 manual smoke
