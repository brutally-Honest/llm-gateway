---
feature: 004-session-key-absent-header-warning
kind: fix
mode: quick
status: done
version: 1
created: 2026-10-11
branch: fix/004-session-key-absent-header-warning
approved: 2026-10-11T07:41:57Z
approved-by: human
sha256: e17fcc32cf6fe7152ccfad309f985bbb63e8821b735f4c6bff8595dc606d0388
fingerprint: 93c97c18909369536e1c0e58ffce12c18596d059816777369e95c66341030791
---
# Session key absent header warning

## Change
Repro: send a request that a session-reporting profile matches (Claude Code) without its session header, e.g. `HEAD /api/hello` or a title side call. Actual: core logs `session_key_rejected` with reason `empty`. `SessionKeyer.SessionKey` returns `""` both for "no header" and for "header present but empty", and core runs both through `ValidSessionKey`. Expected: an absent header is "no key" (003 AC3), so nothing is logged; only a header that is present but invalid is logged (003 AC4).

## Acceptance
- **AC1** — When a request matches a session-reporting profile but carries no session header, the system shall capture it with no session key and log no `session_key_rejected` line.
- **AC2** — When the session header is present but empty, longer than 256 bytes or holds a control character, the system shall capture the exchange with no session key and log `session_key_rejected` with the reason and request id, never the value.
- **AC3** — When the session header is present and valid, the system shall store it as the exchange's session key, as before.
- **AC4** — When `gateway backfill-sessions` meets a stored exchange whose headers lack the session header, the system shall leave it without a key and count it under "left without key", as before.

## Out of scope
- No change to the validation rules (`ValidSessionKey`), the stored data, the viewer or the backfill output format.
- No change to 003's spec text; this restores its AC3/AC4 split.

## Approach
`internal/core/adapter.go`: `SessionKeyer.SessionKey(h) (key string, present bool)`, so a profile says whether it found its header. `internal/core/registry.go` returns `ok=false` when the profile reports not present, so `readSessionKey` in `proxy.go` stays silent. `internal/clients/claudecode/profile.go` reports present from `len(h.Values(sessionHeader)) > 0`. `cmd/gateway/backfill.go` counts not present as "left without key". Test profile in `internal/core/sessionid_test.go` follows the new signature.

## Steps
- **S1** — Split "absent" from "present but empty" in the `SessionKeyer` contract, the Claude Code profile, core and backfill — Tests: TestProxy_AbsentSessionHeaderLogsNothing (regression: matched profile, no header, no `session_key_rejected` in the log, exchange ungrouped), TestProxy_InvalidSessionKeyStoredUngrouped (extended: present-but-empty header still logs reason `empty`), TestProfile_NoHeaderNoKey and TestProfile_EmptyHeaderIsPresent, TestBackfill_AbsentHeaderLeftWithoutKey

## Changelog
- v1 (2026-10-11) — first draft
