---
feature: {{FEATURE}}
kind: {{KIND}}
status: draft
version: 1
created: {{DATE}}
supersedes: {{SUPERSEDES}}
branch: {{BRANCH}}
approved:
approved-by:
sha256:
fingerprint:
---
# {{TITLE}}

## Problem
<!-- Who has the problem, what happens today, why it matters. In the user's words.
     fix: steps to reproduce, expected vs actual. refactor: what hurts about the current shape.
     If this supersedes an earlier feature, say which of its ACs change and why. -->

## Goal
<!-- What is true when this is done. 1-3 lines. -->

## Non-goals
<!-- What this explicitly does NOT do. At least one line. -->

## Acceptance criteria
<!-- One line each: numbered, observable, testable, written as behaviour (not implementation).
     Never reuse or delete an id — strike removed ones so history stays readable:
- **AC1** — When <trigger>, the system shall <observable result>.
- ~~**AC9**~~ — removed in v2 (CR-001): <reason>
-->

## Edge cases
<!-- Only the ones that apply: empty/huge input, duplicates, concurrency, auth/tenant boundary,
     dependency down, time zones, retries/idempotency.
- **E1** — <situation> → <expected behaviour> (AC2)
-->

## Constraints
<!-- Optional: performance budgets, security, compatibility, tenancy, data retention. -->

## Open questions
<!-- Anything unanswered, as [NEEDS CLARIFICATION: <question>]. Approval refuses while any remain,
     and while any [ASSUMED] marker (a proposal the user hasn't confirmed) remains. -->

## Changelog
- v1 ({{DATE}}) — first draft
