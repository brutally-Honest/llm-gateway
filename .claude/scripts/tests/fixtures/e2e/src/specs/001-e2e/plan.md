---
feature: 001-e2e
status: draft
version: 1
approved:
approved-by:
sha256:
fingerprint:
spec-fingerprint:
---
# Plan — 001-e2e

## Summary
One function, `Greet`, in a new `demo` package, with one test.

## Context
New package `demo/`; nothing existing is touched.

## Approach
Write `demo.Greet(name string) string` returning "hello, " + name.

## Alternatives considered
### A1 — Plain string concatenation (chosen)
- Pros: simplest.
- Cons: none for this size.
### A2 — fmt.Sprintf
- Pros: easy to extend.
- Cons: an import for no gain.

## Design
`demo/demo.go` holds `Greet`; `demo/demo_test.go` holds `TestGreet`.

## AC coverage
| AC | Where (package/file) | Verified by (named test) |
|----|----------------------|--------------------------|
| AC1 | `demo/demo.go` | `TestGreet` |

## Test strategy
`TestGreet` covers a name and an empty name (E1).

## Risks
None.

## Open questions
None.

## Changelog
- v1 (2026-10-11) — first draft
