# 048 research

## Progress
- [x] 2026-11-26 branch chore/048-full-docs from up-to-date main; spec and research created
- [x] 2026-11-26 group A: docs/what-gets-recorded.md (D4-D14)
- [ ] group B: docs/architecture.md and UI docs
- [ ] group C: README.md and docs/getting-started.md
- [ ] group D: AGENTS.md / PLAN.md rules, stale-docs sweep, links
- [ ] review round 1
- [ ] fixes for round 1 P1-P3
- [ ] review round 2 (only if round 1 found P1-P3)
- [ ] spec marked done, PR opened, CI watched

## Decisions
- **D1** 2026-11-26 Branch is `chore/048-full-docs`, not the bare `048-full-docs` the request
  named: AGENTS.md requires a feat/fix/chore prefix and 033 (also docs) used `chore/`. The
  slug is unchanged. Needs the owner's confirmation.
- **D2** 2026-11-26 Commit scopes, all from PLAN §11: `docs(spec)` for files under `specs/`,
  `docs(docs)` for `docs/` and README, `docs(repo)` for AGENTS.md and PLAN.md (process docs),
  `docs(adr)` for `docs/decisions/`, `docs(install)` for comments in `install.sh`.
- **D3** 2026-11-26 All doc writing is done by one sonnet subagent per group, one at a time;
  each group ends with `make verify && git commit` run under
  `systemd-run --user --scope -p MemoryMax=4G`. Review is a fresh opus subagent.
- **D4** 2026-11-26 Group A wording on bypasses (desktop app, IDE extensions): the code only
  proves the installer adds a shell function to one rc file; nothing configures those
  clients. The doc says so and says it was not tested per client version, rather than
  promising they are never captured.
- **D5** 2026-11-26 Node floor stated as "22.21 or later in the 22 line, or Node 24 or later",
  from `NodeNote`. The old claim "23 ignores it" (docs/install.md) is not in code, so the
  new doc does not repeat it. No code checks the Node version; the only place is the note
  `gateway install` prints, and the doc says so.
- **D6** 2026-11-26 AppArmor section kept to the brief: known issue, investigated after v1,
  no fix claimed. It links to docs/install.md#check-apparmor and the sandbox section of
  claude-code.md for the repo's existing wording, and does not copy the profile or the
  "untested workaround" text. The doc names `allowAllUnixSockets` only as the relevant
  setting.
- **D7** 2026-11-26 "Needs a look" lists the six kinds from `internal/timeline/view.go`
  (not the older docs' wording) and the cap of 8 shown (`maxLook`).
- **D8** 2026-11-26 Network attribution: code says a connection is linked only when exactly
  one session has a hook event inside `effects.network.active_window` (default 10m) and no
  session end; zero such sessions is unassigned too. Older docs say "exactly one routed
  session"; the doc follows the code and does not use "routed".
- **D9** 2026-11-26 Disk growth: "no retention" is qualified. Saved file baselines
  (`effect_snapshots`) are deleted at session end or after 7 days
  (`effects.files.snapshot_retention`); the capture store itself is never pruned. Also
  mentioned the disposable `view-cache` folder under capture.dir. No numbers from real
  data; only "very roughly hundreds of MB a day for heavy use" as the brief allowed.
- **D10** 2026-11-26 Dedup wording: parsed content pieces are hashed and stored once, raw
  bodies are hashed whole (`bodyContent`), so a body grown by one message is a new blob.
  Both facts are in code; the doc states both.
- **D11** 2026-11-26 Privacy: said "no telemetry" based on finding no outbound call in
  non-test code except proxying and the optional OTLP exporter; added that the OTLP
  endpoint accepts https anywhere (`internal/config/tracing.go`), so "points at a local
  Jaeger" is only the default.
- **D12** 2026-11-26 Redaction list: "headers such as" Authorization, Proxy-Authorization,
  Cookie, Set-Cookie, X-Gateway-Key (core) plus x-api-key (Anthropic adapter).
- **D13** 2026-11-26 Several accounts: the doc says the row records the auth kind (api key or
  subscription) but no account id; left out any claim about how Claude Code names accounts.
- **D14** 2026-11-26 Left out: ports, hooks install detail, the full effects scope rules
  (extra_dirs, ignored folders). They belong to claude-code.md and architecture.md (group B).

## Notes

### Group A evidence
Claims in docs/what-gets-recorded.md.
- Calls grouped into sessions by client session id; none still stored — internal/core/registry.go:109-114, internal/core/effect.go:13-16, docs/clients/claude-code-hooks.md:117 (ungrouped list)
- Turn is one prompt and what followed — internal/timeline/view.go:257
- Hooks give ran/failed/denied states — NOT VERIFIABLE IN CODE as Claude Code behaviour: worded as what Claude Code reports, linked to claude-code-hooks.md
- Reads by Read, Grep, Glob only — internal/clients/claudecode/effects.go:31, internal/effects/reads.go:23
- Network via gateway proxy, HTTPS not decrypted — internal/netproxy/record.go (records host/port/bytes), docs/clients/claude-code.md:313 (decryption: tunnel only; not re-read in code, taken from doc)
- Needs a look: six kinds, cap 8 — internal/timeline/view.go:196-203, internal/timeline/view_overview.go:12-13
- Sessions filter "Needs a look" — internal/ui/web/sessions-page.js:276
- Cost from token counts and shipped price table, API-equivalent — internal/cost/cost.go:1-5, internal/protocols/anthropic/cost.go:13 (go:embed prices.csv)
- Unknown model or variant is no_price, not an error — internal/cost/cost.go:72-75, 124-139
- Works without an API key — NOT VERIFIABLE IN CODE as a single path: cost reads captured usage only (internal/cost/cost.go:Price), no key is used; subscription mode is described in docs/clients/claude-code.md:33
- Health counts and degraded — internal/observability/health.go:14-82
- /healthz always 200 with the JSON — internal/server/server.go:129-138; /api/health — internal/ui/ui.go:131; banner — internal/ui/web/health.js:8-12; doctor check — internal/install/doctor.go:340; metrics — docs/observability.md:12-28
- Shell function, health-gated, `command claude --settings` — internal/clients/claudecode/install.go:96-108
- rc files (.zshrc/$ZDOTDIR, .bashrc, .bash_profile on darwin; others manual) — internal/install/preflight.go:355-387
- Interactive-only rc files — NOT VERIFIABLE IN CODE (shell behaviour): worded as "files of interactive shells"
- Desktop app / IDE extensions not captured — NOT VERIFIABLE IN CODE: worded as "installer adds only the shell function; not tested per client" (docs/clients/claude-code.md:293 says the same)
- Fast-mode, WebFetch, connectors limits — NOT VERIFIABLE IN CODE: linked to PLAN.md §3 line 76
- Principal always local — internal/core/principal.go:5-20, cmd/gateway/run.go:994, internal/hooks/sink.go:239; auth kind recorded, no account field — internal/core/meta.go:25
- Network linking only with exactly one active session — internal/effects/runner.go:392-407, internal/netproxy/record.go:34-36; window default 10m — internal/config/effects.go:121; no session end — internal/store/effects.go:186-200
- Unassigned listed on Sessions page — internal/ui/web/app.js:195-199, internal/ui/web/sessions-page.js:197-204
- Node floor 22.21 / 24, NODE_USE_ENV_PROXY=1, older ignores — internal/clients/claudecode/snippet.go:63, 76-80; note printed by install — internal/install/install.go:689-690, cmd/gateway/install.go:203; no version check in code — grep for node version in internal/ and cmd/ found none
- Old Node behaviour (calls unseen / ENOTFOUND) — NOT VERIFIABLE IN CODE beyond the NodeNote text (snippet.go:78-80): worded as what the note says
- AppArmor restricts userns, may turn bwrap sandbox off — internal/install/preflight.go:325-348 (doctor warning text); `allowAllUnixSockets` named only — docs/clients/claude-code.md:192 (not in code); "under investigation after v1" — spec 048 non-goals; no fix claimed
- Sandbox option off by default — internal/config/effects.go (Sandbox default) and config.example.yaml:189
- Raw secrets stored — docs/decisions/0005 (decision); capture stores bodies as sent — internal/store/exchange.go:100-110
- Redacted headers — internal/core/redact.go:15-17, internal/protocols/anthropic/adapter.go:48
- No telemetry — NOT VERIFIABLE IN CODE as a negative; grep of non-test code in internal/ and cmd/ for http.Get/Post/NewRequest/DefaultClient found no outbound call besides proxying; OTLP exporter off by default — internal/config/tracing.go:5-28, config.example.yaml:213-217
- Span content (no content/headers) — config.example.yaml:206-208 (comment); not re-proved in internal/observability/tracing.go
- Loopback-only listeners and refusal — internal/config/listen.go:32-52, internal/config/listen.go:16-30 (listener set)
- Perms 0700/0600 for capture dir and files — internal/store/store.go:85-100, internal/store/content.go:133-140, CreateTemp 0600 (content.go:90); config folder/file — internal/install/install.go:607-623
- No retention; snapshots cleaned at session end or 7 days — grep for DELETE/prune in internal/ found only effect_snapshots deletes (internal/store/effects.go:62,144,156); internal/config/effects.go:114; config.example.yaml:164-167
- Blob: sha256, zstd, over 4 KiB — internal/store/content.go:15-33, 82-107
- Raw bodies hashed whole; parsed pieces deduped by hash — internal/store/exchange.go:100-110, internal/core/canonical.go:296-310, docs/capture.md:62-70
- Claude Code resends the whole conversation on every call — NOT VERIFIABLE IN CODE as client behaviour: worded as fact about calls the gateway sees; observed in capture design notes (docs/capture.md:70 "a message resent on every turn")
- Hundreds of MB/day heavy use — NOT VERIFIABLE IN CODE: worded "very roughly", varies; figure supplied by the brief
- capture.dir default — internal/config/capture.go:40-56
- view-cache under capture.dir, disposable, trims itself — internal/ui/disk_cache.go:30-60

Stale or doubtful statements in OTHER docs (not fixed; group D):
1. docs/install.md:328 — "Older Node (22.20 and before, 23, 20 and before)": code (snippet.go:78) only says 22.21+ or 24+ are seen; the 23 claim is not in code.
2. docs/clients/claude-code.md:182 — "Node older than 22.21 or 24": ambiguous; code says 22.21+ or 24+.
3. docs/clients/claude-code.md:310-311 and docs/install.md:321-322 — "exactly one routed session": code (internal/effects/runner.go:392) counts sessions with a hook event in the active window and no session end; "routed" is not checked.
4. docs/clients/claude-code-hooks.md:10-13 — says hooks go in ~/.claude/settings.json at user scope; the installer puts them in the gateway's own settings file loaded by `--settings` (internal/clients/claudecode/snippet.go:50-70, install.go:100). Check whether the page still describes the intended path.
5. docs/install.md:47 and :195-199 vs docs/clients/claude-code.md:192-194 — install.md offers an AppArmor profile fix; claude-code.md calls `allowAllUnixSockets` an untested workaround; docs/roadmap.md:140 says to test it. Not contradictory, but the three should be kept consistent by group D (the new page claims no fix).
6. docs/capture.md:88 area and docs/clients/claude-code.md:313 — "HTTPS ... never decrypted" not re-checked here beyond internal/netproxy recording fields; reviewer to confirm.
