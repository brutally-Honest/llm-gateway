---
feature: 003-sessions-and-timeline
---
# Research & decisions — 003-sessions-and-timeline

## Open questions
<!-- Agents add a question here when they are blocked; the loop stops until you answer.
     Answer by changing "(open)" to "(answered)" and writing the answer underneath:
- **Q1** (open) T003 — <question> — options: A) … B) …
-->

## Decisions
- D1 (2026-10-11) — Session is a column on exchanges, not a canonical event kind — the key is exchange metadata a client profile reads from headers, not something a protocol parser reads from the body; events stay parser output and 002's event model is unchanged.
- D2 (2026-10-11) — Sub-agents: group strictly by the key the profile returns; no separate sub-agent view. If sub-agents share the parent's key their turns appear inline in time order, and the longest-shared-prefix rule (AC13/AC14) keeps each thread from repeating.
- D3 (2026-10-11) — Side calls (count_tokens, /v1/models, HEAD /api/hello) that carry the key are in the session and its count, shown as folded one-line entries (AC15).
- D4 (2026-10-11) — Backfill: an explicit `gateway backfill-sessions` command, run by the user, using the profile named by each exchange's stored client label. 002 already noted the stored headers make this possible. Not run automatically on startup.
- D5 (2026-10-11) — Session identity is principal + client + key. A key that matches several sessions prints each timeline in turn rather than refusing.
- D6 (2026-10-11) — Dedup by longest shared message-hash prefix with any earlier exchange of the session, not a global seen-set (which would hide a genuinely repeated message) and not only the previous exchange (which breaks on interleaving).
- D7 (2026-10-11) — Keys that are empty, over 256 bytes or contain control characters are stored as no key; core bounds the key but never parses it.
- D8 (2026-10-11) — OQ-7 resolved: CLI viewer first (text by default, -json for JSON lines), web UI once the event model is stable. ADR to be written in this feature.
- D9 (2026-10-11) — Refines D6: each exchange is compared with a single best-matching earlier exchange (longest shared message-hash prefix; ties go to an exact continuation, then the most recent). `history diverged` is shown only when that best match's own list is longer than the shared prefix (a rewrite such as /compact); an exchange sharing no prefix starts a new thread with no marker. Keeps an interleaved sub-agent thread from being marked diverged.
- D10 (2026-10-11) — Corrects the 2026-10-11 note below: the manual smoke test is back in the spec as AC28 (subscription login, Task tool), and it is where the sub-agent session-key question gets answered.

## Notes
- 2026-10-11 — No capture store exists on the spec-writing machine, so what Claude Code sends as `x-claude-code-session-id` for sub-agents (Task tool) and for side calls is still unverified. The manual smoke AC was dropped from the spec by choice; whoever captures a real session with a sub-agent should add the observed behaviour here as a new dated line. The design (D2, D3) works whichever way it turns out.
