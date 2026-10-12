---
feature: 007-settle-disconnect-race
kind: fix
mode: quick
status: approved
version: 1
created: 2026-10-12
branch: fix/007-settle-disconnect-race
approved: 2026-10-12T16:28:58Z
approved-by: human
sha256: 5a98731242f7a587a5da8cc0b20e92e135341a981952a898324ee92252a4e3f8
fingerprint: 95b16dfab7a54327c97d06eb8e3b33cad0fae040a339f4437b90a124adfefa9f
---
# Settle disconnect race

## Change
Repro: `TestAccessLog_UpstreamAbortedField/completed` fails about 5 in 300 runs under CPU load on main with
`client_disconnected = true, want absent`. The proxy flushes at once (`FlushInterval: -1`), so the client can
hold the whole response and close the connection before the access log's deferred `Meta.Settle` runs;
net/http then cancels the inbound context and `settleAbort` reads that as the client leaving. Expected: a
response whose upstream body reached a clean EOF and was written to the client without error is never
`client_disconnected`, whatever the context says.

## Acceptance
- **AC1** — When the upstream response body reached a clean EOF and the proxy wrote all of it to the client without error, and the client then closes the connection before `Settle` runs, the system shall log neither `client_disconnected` nor `upstream_aborted` on the access line.
- **AC2** — When the client leaves mid-stream (the response is not complete), the system shall still log `client_disconnected: true`.
- **AC3** — When upstream dies after headers while the client is still there, the system shall still log `upstream_aborted: true` and no `client_disconnected`.
- **AC4** — When the gateway answers 502 because upstream is unreachable, the system shall still log neither flag.

## Out of scope
- Any other access-line field, the capture outcome, or how `UpstreamAborted` is decided.
- Changing `TestAccessLog_UpstreamAbortedField`, `TestCapture_ForwardingUnchanged` or the existing `TestMeta_Settle` cases.

## Approach
`internal/core/meta.go`: `responseWatcher` records a clean `io.EOF`; `Meta` gains an unexported "response
complete" mark that `settleAbort` checks first (complete → no flags). `internal/core/proxy.go`: `Proxy.ServeHTTP`
sets the mark only when `rp.ServeHTTP` returns normally (ReverseProxy panics with `http.ErrAbortHandler` on any
copy error, read or write) and the watcher saw EOF. Deterministic test in `internal/core`: a test-only handler
between the access log and the proxy that, after the proxy returns, flushes and waits for the inbound context to
be cancelled, so `Settle` always runs after the client has read the full response and closed.

## Steps
- **S1** — Mark a completed response and let `settleAbort` honour it — Tests: `TestAccessLog_CompletedThenClientClosesBeforeSettle` (AC1: client reads the full response and closes; the handler holds until the inbound context is cancelled, waiting at most 5s and failing the test with a clear message if it is not, so a regression fails fast instead of hanging the suite; access line has neither flag; fails before the fix every run), `TestMeta_Settle` new case "complete response, cancelled context" (AC1), existing `TestAccessLog_UpstreamAbortedField` cases unchanged (AC2, AC3, AC4)

## Changelog
- v1 (2026-10-12) — first draft
