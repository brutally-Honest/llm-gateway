---
feature: 001-e2e
kind: feat
status: draft
version: 1
created: 2026-10-11
supersedes:
branch: feat/001-e2e
approved:
approved-by:
sha256:
fingerprint:
---
# E2E demo

## Problem
The kit's tests need one real, approved feature to drive the loop end to end.

## Goal
A `demo` package with a function that greets a name.

## Non-goals
- Anything beyond the one function.

## Acceptance criteria
- **AC1** — When `Greet` is called with a name, the system shall return "hello, " followed by the name.

## Edge cases
- **E1** — An empty name → "hello, " (AC1)

## Constraints
None.

## Agnosticism check
Test fixture only.

## Open questions
None.

## Changelog
- v1 (2026-10-11) — first draft
