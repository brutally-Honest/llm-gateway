---
feature: 006-rate-limiting
---
# Research & decisions — 006-rate-limiting

## Open questions
<!-- Agents add a question here when they are blocked; the loop stops until you answer.
     Answer by changing "(open)" to "(answered)" and writing the answer underneath:
- **Q1** (open) T003 — <question> — options: A) … B) …
-->
- **Q1** (answered) T001 — Does Claude Code send ANTHROPIC_CUSTOM_HEADERS on every /v1/messages call, including sub-agent and side calls? — options: A) yes, use it B) no, fall back to a header set by a wrapper script
  - **Answer:** A — verified 2026-10-12 with gateway dump: Claude Code 2.1.296 sends ANTHROPIC_CUSTOM_HEADERS on all 5 /v1/messages calls of a run with a sub-agent (main, sub-agent and side calls). Does not block T001; AC20's manual check in T008 re-confirms. (2026-10-12)
- **Q2** (answered) T003 — T003's commit f5e5fd8 already exists with the T003 trailers and the code and tests pass, but the loop recorded its base as f5e5fd8 itself, so post-check reports "no new commit since f5e5fd8" and the findings say no test file changed in this run. Making a new commit would be an empty or filler commit. How should T003 be closed? — options: A) accept f5e5fd8 as T003's commit and re-record the base before it (loop state fix, no new commit) B) allow an empty `chore` commit to satisfy post-check C) re-run T003 from scratch on a branch reset to the T002 commit, dropping f5e5fd8
  - **Answer:** A — accept f5e5fd8 as the T003 commit; base.T003 set by hand to c52818d (its parent, the rebased T002 commit). No new code commit (2026-10-13)
- **Q3** (answered) T010 — NewFeeder must take a *zap.Logger and the warn line needs the request ID, but the logger cannot reach NewFeeder: the anthropic adapter builds the feeder inside `UsageReader(status, h, stream)`, which has no logger, and `core.RateLimitAdapter.UsageReader` is the seam. How should the logger get there? — options: A) add a `log *zap.Logger` parameter to `core.RateLimitAdapter.UsageReader` (core passes its request-scoped proxy logger; about 20 call sites change in core, anthropic and tests) B) keep the signatures; the feeder returns a new `Panicked` status from Close and core logs the one warn line with request_id (NewFeeder would then not take a logger, which the task text asks for) C) give the anthropic `Adapter` a logger field set when the registry is built (larger change to registry and cmd wiring)
  - **Answer:** Option 2, and it supersedes the task text's "NewFeeder takes a *zap.Logger": NewFeeder stays logger-free. A recovered panic in the decode goroutine stops decoding, records a Panicked status, and Close returns it. Core logs exactly one warn line with request_id and the reason "usage decoder panicked" (plus the panic value's type), records no tokens for that response, and forwarding is never affected. Tests: a panicking decoder yields Panicked from Close; core logs exactly one line with request_id; forwarded bytes are byte-identical. Record this decision in research.md under ## Decisions. (2026-10-13)

- **Q4** (answered) T010 — post-check fails on commits outside T010: ab1a7c7 (the answer to Q3) and 2a3c061 (the Q3 blocking record) carry no `Task:` trailer, so `loop.sh findings T010` reports them. T010's own commit f4060ed passes its checks. Rewriting those two commits is not allowed to the agent. — options: A) the human rewrites the trailers of ab1a7c7 and 2a3c061 and T010 is then accepted at f4060ed B) the loop's trailer check skips commits that carry `Blocked:` or only touch research.md, then T010 is accepted at f4060ed C) accept f4060ed for T010 by hand as with Q2, and leave the two commits as history
  - **Answer:** The human rewrites the trailers of ab1a7c7 and 2a3c061; T010 is then accepted at f4060ed (2026-10-13)

## Decisions
<!-- - D1 (date) — <decision> — why -->
- D1 (2026-10-13) — T010 (Q3, option 2): contentcoding.NewFeeder stays logger-free. A recovered decoder panic stops decoding and Close reports Panicked with the panic value's type; core logs one warn line "usage decoder panicked" with request_id and panic_type, records no tokens, and forwarding is never affected — because the feeder has no request ID or logger and the protocol reader has none either, so the log belongs to the core usage writer that owns the request.

## Notes
- 2026-10-13 — after the rebase onto 14917ce, record_base reset base.T003 to HEAD (f5e5fd8) because the old base was no longer an ancestor; set it by hand to c52818d. Kit gap, fix as chore 009.
