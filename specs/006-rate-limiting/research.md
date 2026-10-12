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

## Decisions
<!-- - D1 (date) — <decision> — why -->

## Notes
