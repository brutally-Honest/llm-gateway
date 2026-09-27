---
status: approved
date: 2026-09-26
---

# 0003 — `httputil.ReverseProxy` with `Rewrite` as the proxy engine

## Context
Feature 001 puts the gateway on the wire. Every request must reach the provider
unchanged and every response, streams included, must come back unchanged
(PLAN.md §5). PLAN.md §8 lists the proxy engine as "Proposed" and leaves it to the
Phase 1 spec to confirm. Three forces bound the choice:
- **Fidelity.** No buffering, no invented or dropped headers, no re-encoding, and an
  upstream error is the client's error.
- **Reach.** The engine must serve any protocol adapter (003 adds a second one) with no
  provider knowledge in `internal/core/`.
- **Cost.** One developer, one binary. Edge cases the standard library already solved
  are not worth rediscovering.

## Decision
The core proxy is `httputil.ReverseProxy`, configured with the `Rewrite` hook and
never `Director` or `SetXForwarded`. The details that make it faithful:
- `Rewrite` sets the target with `SetURL`, then puts back what `ReverseProxy` strips
  before `Rewrite` runs: the client's `Forwarded` and `X-Forwarded-*` headers and its
  exact `RawQuery`. It drops `Te`, which `ReverseProxy` re-adds.
- `FlushInterval: -1` flushes after every write. 000's response wrapper keeps
  `http.Flusher`.
- The transport is our own `http.Transport` with `DisableCompression: true`, so the
  gateway never decompresses.
- `ModifyResponse` deletes upstream's `X-Request-Id`, so the gateway's, already set by
  000's middleware, is the only one. `ErrorHandler` builds gateway-made errors through
  the adapter.
- A failure after response headers is left to `ReverseProxy`'s own abort
  (`http.ErrAbortHandler`); no event is invented.

## Alternatives
- **Hand-rolled proxy** (`http.Client` plus manual copy). More to learn, but it
  re-implements hop-by-hop stripping, trailers, `Upgrade` and flush handling, and that
  is where fidelity bugs hide. Kept as the fallback if a protocol needs control the
  standard library cannot give (PLAN.md §8).
- **`ReverseProxy` with `Director`.** `Director` runs before hop-by-hop stripping, so
  a header named in `Connection` can be re-added by it, and `X-Forwarded-For` is added
  by default. `Rewrite` (Go 1.20+) sees the request after stripping and adds nothing.
- **`Rewrite` with `SetXForwarded`.** Rejected by the spec: Anthropic should see what
  the client sent, and an added `X-Forwarded-*` is a change to the traffic.
- **A third-party proxy library.** A new dependency needs an ADR, and none offers
  more than the standard library does here.

## Consequences
- **Easier.** Streaming, trailers, hop-by-hop stripping, HTTP/2 upstream and
  connection reuse come from the standard library. A new adapter reuses the same
  proxy with only a prefix, an error body and an auth kind.
- **Harder.** `ReverseProxy` has behaviours a faithful proxy must undo, and they
  change with Go releases: it drops client `X-Forwarded-*` and unparsable query
  parameters (put back in `Rewrite`) and re-adds `Te: trailers`. Tests pin each one
  (AC8, AC14, AC16), so a Go upgrade that changes one fails a test. A response that
  fails after its headers can only be aborted, never replaced (research Q8).
- **Commits us to** the standard library's `Transport` semantics: it replays a
  request on a stale idle connection, invisibly to upstream, so "never retries" is
  measured at upstream (AC29).
- **Reversing.** Replacing the engine touches only `internal/core/proxy.go`, not the
  adapter or profile interfaces; the AC tests are written against behaviour, so they
  carry over.
