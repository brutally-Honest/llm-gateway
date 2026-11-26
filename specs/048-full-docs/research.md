# 048 research

## Progress
- [x] 2026-11-26 branch chore/048-full-docs from up-to-date main; spec and research created
- [x] 2026-11-26 group A: docs/what-gets-recorded.md (D4-D14)
- [x] 2026-11-26 group B: docs/architecture.md, docs/ui.md (D15-D20)
- [x] 2026-11-26 group C: README.md, docs/getting-started.md (D21-D24)
- [x] 2026-11-26 group D: AGENTS.md/PLAN.md rules, stale sweep, status lines, links (D25-D43)
- [x] 2026-11-26 review round 1: 62 claims, 1 P2, 7 P3, 9 P4 (P4s under Notes)
- [x] 2026-11-26 fixes for round 1 P2-P3 (D44-D51)
- [x] 2026-11-26 review round 2: 77 claims, 3 P3 fixed (D52-D54), 15 P4 under Notes; the round 2 fixes were not re-reviewed (two-round limit)
- [x] 2026-11-26 spec marked done; PR and CI below

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
- **D15** 2026-11-26 Ports: the brief's list is right but incomplete. `/metrics` is also on the UI
  listener (7199), and `/api/health` is UI-only; the hooks route is `/_gateway/hooks/{client}` on
  7197. architecture.md says so. The proxy listeners start only with capture, `effects.enabled`
  and `effects.network.enabled`; the UI only with capture and `ui.enabled`.
- **D16** 2026-11-26 Shutdown order in architecture.md follows `captureRun.close`: hub closed and UI
  work cancelled first, hook sink, effects, capture queue drain (budget of its own, one
  `shutdown_timeout`), then the 5 s UI wait (`uiWorkTimeout`), then reader and store. The 2 s UI
  server stop (`uiStopTimeout`) is also named, since it happens before the drain.
- **D17** 2026-11-26 View-cache key: the build folder name hashes format, version, executable hash,
  attribution skew, home folder and time zone, so "keyed on the binary" is true but incomplete;
  the page says so (a new binary gives a new folder, empty after every install).
- **D18** 2026-11-26 ui.md is the one home for UI description. Replaced one passage in
  docs/install.md (the list filling in, under "The UI's view cache") with a pointer. Left alone on
  purpose: docs/roadmap.md (a history of specs, not a description), the chip table in
  claude-code-hooks.md (owned by the hooks doc, which ui.md links), README.md (group C).
- **D19** 2026-11-26 docs/observability.md already matched the code on health (200 on degraded,
  banner text, per-tab dismissal, restart or higher count brings it back). No sentence changed.
- **D20** 2026-11-26 Session-page recovery: the code has no Retry button on a failed session view; it
  shows the message and "Turns not loaded.", and a 504 gets one automatic retry after 1 s with
  "building session…". ui.md says "reload the page" and offers Retry only where the code has one
  (sessions list, the "Details are not available yet" line). Search matches client, session key,
  id, folder, title and models (`searchVerdict`), so ui.md lists those.

- **D21** 2026-11-26 README rewritten to the spec's section order; the old project log, the "Known limits" block, the Setup and Web UI sections are gone (limits live in install.md and what-gets-recorded.md, UI in ui.md). One pointer to roadmap.md was dropped from the README body and kept in the Docs index line for the roadmap.
- **D22** 2026-11-26 No LICENSE file exists in the repo (ls LICENSE*: no match), so the README has no license line. docs/licenses/ holds third-party notices and is not linked as a license.
- **D23** 2026-11-26 The README says the sandbox is needed for network effects only by implication ("through its proxy"); the exact conditions stay in what-gets-recorded.md. Keeps one home per rule.
- **D24** 2026-11-26 getting-started.md describes the PATH case from code: install.sh and `gateway install` copy the binary to ~/.local/bin/gateway and add it to PATH only on macOS when missing (shell.go:20-22); on Linux the doc gives the install.md fix. Prompts described: `Run it now?` (preflight.go:467) and `Proceed?` (install.go:128), shown as `[y/N]` and `[Y/n]` per install.md.
- **D25** 2026-11-26 Group D, conflict 1 (precedence). PLAN.md line 3 said it was the source of intent and a contradicting spec or PR meant "update this file with an ADR or the other thing is wrong"; AGENTS.md said PLAN.md "wins wherever this file disagrees with it". Kept PLAN's wording for intent and design only (goal, agnosticism, §5 rules, phases, stack, open questions), and added that how work is done is in AGENTS.md. AGENTS.md now says the two files have separate jobs and neither overrides the other.
- **D26** 2026-11-26 Group D, conflict 2 (hard rules). AGENTS.md copied the PLAN §5 rules under "Hard rules (PLAN §5)". PLAN §5 is product design, so it stays in PLAN.md. Kept the AGENTS.md list as a short checklist, retitled "Hard rules" with a line naming PLAN §5 as the owner. The wording of the list is unchanged. This is deliberate duplication, as the owner allowed; a change to a rule is made in PLAN §5 first.
- **D27** 2026-11-26 Group D, conflict 3 (small fixes). AGENTS.md said "Each change gets specs/NNN-slug"; PLAN §10 said small fixes need no spec folder, and the PR states the bug, cause, fix and proving test. Kept PLAN's rule and put it in AGENTS.md.
- **D28** 2026-11-26 Group D, conflict 4 (when unsure). AGENTS.md said "if it cannot be decided safely, stop and ask"; PLAN §10 said "if it is hard to reverse, stop and ask". Kept PLAN's wording ("hard to reverse"). The AGENTS.md "Always stop and ask first" list is unchanged and still applies.
- **D29** 2026-11-26 Group D, conflict 5 (spec skeleton). AGENTS.md had bare headings; PLAN §10 had one-line hints per heading and the AC and edge-case formats (`**AC1** — When <trigger>, the system shall <result>.`). Kept PLAN's version, plus its line that a spec for a provider, client or protocol change confirms `internal/core/` is unchanged.
- **D30** 2026-11-26 Group D, conflict 6 (D-entry format). AGENTS.md said `- **Dn** …`; PLAN §10 gave `- **D1** (2026-10-11) — text`. Kept PLAN's example (it is what most specs use: 30+ entries in 003-017 and later specs). This spec's own entries use `- **Dn** 2026-11-26 …` without brackets, as the owner's brief for 048 asked. Not changed here.
- **D31** 2026-11-26 Group D, conflict 7 (ADR trigger). AGENTS.md: "a decision that outlives the feature or is hard to reverse". PLAN §10: "matters beyond this feature, or is hard to reverse", plus "resolving an OQ always produces an ADR and PLAN.md is updated in the same PR". Kept PLAN's version of both, as one rule in AGENTS.md; the OQ rule was missing from AGENTS.md.
- **D32** 2026-11-26 Group D, conflict 8 (agent-loop history). AGENTS.md: specs 003-017 keep the kit's frontmatter. PLAN §10 also said specs 000-002 predate the kit. Kept both facts in AGENTS.md.
- **D33** 2026-11-26 Group D, conflict 9 (hooks and CI). PLAN §11 said the pre-push hook "re-runs commit-msg on every commit in the pushed range" and CI "runs make verify". The hook also blocks unless each pushed sha is HEAD, tracked files are clean and `make verify` passes (.githooks/pre-push:32, 43, 52, 61), and the workflow runs lint (Linux only), the Go tests and the web tests per OS (.github/workflows/verify.yml:1). AGENTS.md states what the code does. PLAN was not the better source here.
- **D34** 2026-11-26 **needs owner decision.** PLAN §11 "Repo setup" had four lines; the fourth is `git push origin refs/notes/commits`. The owner's rule for this work is that git notes stay local and `refs/notes/*` is never pushed, and AGENTS.md "Always stop and ask first" already lists any change to a remote ref, notes included. Carried over the first three lines (they are what `make setup` runs, Makefile `setup-git`) and dropped the push line. Reworded "so notes survive and travel" to "so notes survive a rebase", and added "Notes stay local: never push `refs/notes/*`". For the push: notes then exist only on this machine (lost with the clone, not shown on GitHub); the PR description carries the essentials. For keeping it: a pushed notes ref gives a backup and a second machine. The fetch refspec (third line) is kept because `make setup` adds it; it is inert while nothing is pushed. If the owner wants notes pushed, say so and the line returns with an explicit "ask first".
- **D35** 2026-11-26 Group D, conflict 10 (other rules that could push or act outside the repo). Checked PLAN §10 and §11 for any. The PR step (step 4) and the enforcement text do not say to push, but a PR needs a push. AGENTS.md step 4 now says opening one needs the human's go-ahead, which restates the "Always stop and ask first" bullet. `docs/release-checklist.md` has `git push origin <tag>` and `gh release create`; it is the human's checklist, not an agent workflow, and was not changed.
- **D36** 2026-11-26 **needs owner decision.** PLAN §10 "AGENTS.md should contain" listed items. It is a list of AGENTS.md's content, not a rule, so it was not copied. Gaps found against it: "never code outside the current spec's scope" (added to Workflow), "Commands: lint, compose up" (added `make observability-up` and `-down` to Commands) and "Conventions: package layout, error wrapping, logging fields, test style" (NOT added: no such text exists in the repo and writing it now would invent rules). Decide whether to write a Conventions section.
- **D37** 2026-11-26 Group D (layout). PLAN §10 and §11 are gone as workflow sections. Section numbers 1-9 are unchanged. §10 is kept as "Repo layout, and where the workflow rules live": the layout tree plus a pointer to AGENTS.md (the tree is structure, not a workflow rule). §11 is removed; no later section exists to renumber.
- **D38** 2026-11-26 Group D (history bodies). 17 spec, plan, task, research and ADR files cite PLAN §10 or §11. Citations are not links, so none was reworded. A dated one-line note was appended to the end of 16 files (000 spec, 003 plan, 009 plan/spec/tasks, 013 CR-003, 016, 021, 023, 024, 026, 028, 031, 033, 034 research, ADR 0001), and the 031 file got a second line for "PLAN wins over AGENTS.md". The "see PLAN §10" sentence in 000, 001 and 002 research.md line 13 is a pointer to a rule, so it was changed to point at AGENTS.md. 019 research ("PLAN wins over the roadmap") is about the roadmap, not AGENTS.md, and was left.
- **D39** 2026-11-26 **needs owner decision.** `.githooks/commit-msg` line 2 (a comment) now says "AGENTS.md (Commits)". Line 18 is a `printf` that prints "Format (PLAN.md §11):" when a commit is rejected. It is output, not a comment, so the brief's "comments only" rule stopped me. The text now points at a section that does not exist. A one-word change fixes it; it needs the owner's go-ahead for a hook edit.
- **D40** 2026-11-26 Group D (roadmap). docs/roadmap.md "Done" stopped at 040. Added short entries for 033, 034, 036 and 041-047 from each spec's Goal (all merged: git log merge commits #64, #65, #66, #68, #69, #71, #72, #74, #75, #76) and one line saying each entry describes its spec as merged and ui.md is the UI as it is now. The 027 and 029 entries were left as history, which also answers group B's item 3.
- **D41** 2026-11-26 Group D (statuses). Spec 033 had no `status:` line though it merged in #64; added `status: done`. Every other spec folder with a spec.md carries `done` (048 stays `draft`); 004, 007, 011, 012 keep theirs in brief.md (033 D6). Specs 003-017 keep the kit's frontmatter as history. ADR statuses are `accepted` or `superseded` in all 21; the template names only `proposed` as the starting value, so nothing was changed.
- **D42** 2026-11-26 Group D (AppArmor, left as is). docs/install.md gives an AppArmor profile as the fix for the `apply-seccomp` failure; docs/clients/claude-code.md calls the same symptom a known Claude Code bug with an untested `allowAllUnixSockets` workaround; roadmap says to test it. The code only proves the doctor warning (internal/install/preflight.go:325-348). Which cause is right cannot be settled from code or tests here, so no text was changed; what-gets-recorded.md claims no fix. Left for the after-v1 investigation.
- **D43** 2026-11-26 Group D (links). The checker treated one link as broken: specs/001 tasks.md:318, which sits inside an inline code span that runs over two lines. It is quoted text, not a link. Left.

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

### Group B evidence
Claims in docs/architecture.md and docs/ui.md.
- Ports 7197/7198/7195/7199 and settings — internal/config/config.go:103, internal/config/effects.go:118-119, internal/config/ui.go:25
- /healthz and /metrics on the API listener — internal/server/server.go:58-60; hooks route `/_gateway/hooks/{client}` — internal/hooks/handler.go:16; other `/_gateway/*` is 404 — internal/server/server.go:68
- Adapter prefix `/anthropic/*` — internal/protocols/anthropic/adapter.go:25, internal/core/registry.go:123-126
- UI listener: /metrics, /api/health, GET only, host and origin checks — internal/ui/ui.go:124-131, internal/ui/middleware.go:73-114
- Port 7196 kept free — docs/install.md:36 (not re-checked in code)
- Proxy listeners need effects and network enabled; UI needs capture and ui.enabled — cmd/gateway/run.go:746-749, 242-248, 712-716
- Hooks route always mounted; capture off answers and counts only — cmd/gateway/run.go:~300 comment "The hook route is always mounted"
- Rate limiter mounted only when enabled, per key, fixed window, in memory — cmd/gateway/run.go:266-281, internal/config/ratelimit.go:7-37
- ReverseProxy with Rewrite and tees — internal/core/proxy.go:55-59, internal/core/tee.go:106-166
- Submit never waits, drops and counts when full — internal/capture/sink.go:102-115; N workers over a bounded channel — internal/capture/sink.go:90-95; defaults queue 256, workers 2 — internal/config/capture.go:33-34
- Raw exchange stored first, then parse, then events — internal/capture/pipeline.go:25-45
- Commit calls the hub — cmd/gateway/run.go:661 (`cfg.OnCommit = hub.Publish`)
- SQLite WAL, pure-Go driver, one writer — internal/store/store.go:1-4, 154; blobs over 4 KiB, sha256, zstd — internal/store/content.go:18
- UI reads through a read-only reader — cmd/gateway/run.go:639, internal/store/reader.go:19-22, 74
- Hook sink has its own queue — cmd/gateway/run.go (hooks.NewSink), docs/clients/claude-code-hooks.md:123-125
- Timeline, cost, effects packages as described — package listing of internal/ (go files), internal/timeline/view.go, internal/cost/cost.go:1-5
- View cache location, `m/` and `v/`, modes 0600/0700 — internal/ui/disk_cache.go:29-60
- View cache limits 32 MiB per view, 1 GiB per build — internal/ui/disk_cache.go:54-56
- View cache build key (format, version, exe hash, skew, home, zone) — internal/ui/disk_cache.go:178-181, 236-239; entry key adds session version — internal/ui/disk_cache.go:338-340
- Store behind cache deletes the build folder; cache open failure runs from memory — internal/ui/disk_cache.go:266-295, cmd/gateway/run.go:652-656
- Debounce 150 ms — internal/ui/stream.go:275; heartbeat 15 s, 32 streams — internal/ui/hub.go:25,29; reopen 1 s growing to 30 s — internal/ui/web/api.js:78-79
- Stream routes /api/stream and /api/sessions/{id}/stream?view=1 — internal/ui/stream.go:21-22, 295-311; spec 040 incremental state — specs/040-incremental-live-updates/spec.md (not re-proved in code: only the debounce and "only changed turns" in stream.go:277-290)
- "A few cases still fall back to a full build" — NOT VERIFIABLE IN CODE here: taken from docs/roadmap.md:17-20 (040 entry)
- Shutdown order and figures — cmd/gateway/run.go:1000-1024 (close), 1026-1040 (waitUIWork), 503-510 (2 s, 2 s, 5 s, 2 s consts), 388-440 (stop sequence); shutdown_timeout default 10 s — internal/config/config.go:105; "recorded as cut by the gateway" — cmd/gateway/run.go (srv.Cancel(core.ErrShutdown))
- last-shutdown.json — docs/observability.md:26-28 (not re-checked in code)
- healthz 200 on degraded, with reason — internal/server/server.go:44-47, 129-138; shell function `curl -fsS --max-time 1 <health> && command claude --settings` — internal/clients/claudecode/install.go:98-108
- Banner text, dismissal per tab, returns on higher count or new since — internal/ui/web/health.js:8-12, 21-32, 15-second poll health.js:2; shell.js:151-166, 210-224
- Four counters — internal/observability/health.go:14-82; doctor warns — internal/install/doctor.go:340
- "[content unavailable]" per block — internal/timeline/build.go:22, 501-541
- Page title "Sessions" and "today" — internal/ui/web/sessions-page.js:~575-580 (h1 + `sp-when`, title "since local midnight")
- Tiles — sessions-page.js todayCells/drawToday (stat() calls); sparklines on sessions and model calls — same
- Columns, "Last active", status glyphs — sessions-page.js COLUMNS (line ~486-489), statusOf (line ~93)
- Pinned header — internal/ui/web/style.css:313 (`.sp-list th` sticky); webtest sticky-header.test.js
- PAGE_SIZE 50, Previous/Next, "Page N of M" — sessions-page.js (PAGE_SIZE, pagerBtn, drawPager text)
- Pending rows, "Details loading", bars, "still being checked", "Details are not available yet", Retry — sessions-page.js:7-10, 625-637, drawStatus; style.css:383-388
- Filters All / Needs a look / Active / Hooks only; box "Filter sessions"; label "Show" — sessions-page.js FILTER_LABELS, 581-587
- Search fields — internal/ui/api_sessions.go:searchVerdict; 200-byte cap — internal/ui/web/route.js:6-20; Esc clears — sessions-page.js:~688
- No-match text and Clear — sessions-page.js drawNoMatch; "No sessions captured yet." — sessions-page.js (shown.length === 0 branch)
- Ungrouped link text — sessions-page.js ungroupedText; "Unassigned connections (N)", first 10, "All N unassigned connections →" — sessions-page.js UNASSIGNED_SHOWN and unassignedSection
- Turns rail, "background task", clamp title, duration — session-page.js:43-50, 229-250
- Title rule and list of "(system)" titles — internal/clients/claudecode/rules.go:140-195, 290-347; session title skips synthetic — specs/046-ui-polish/spec.md U1 and internal/timeline/digest.go:151-169
- Overview, Time, Context, "Needs a look", "and N more", cap 8 — session-page.js:257-305; internal/timeline/view_overview.go:12-13
- Cost label "API-equivalent", partial "≥" — internal/ui/web/cost.js:6-10, 45-55; session-page.js:312-318
- "A model with no price is left out" — cost.js:46-47 (hover "no price for"), internal/cost/cost.go:72-75
- Side calls disclosure text — session-page.js:321-332
- Tabs Story/Files/Commands/Sites/Context — session-page.js:120, side-card.js:10-13
- Chips list — docs/clients/claude-code-hooks.md:78-85; tool-card.js chip rendering
- Preview/Raw, stored in the browser — story.js:238-245 (localStorage key `gw.textMode`); session-page.js:126-139
- Files: Changed/Read/Searched, By file/By turn — side-card.js:150-196
- Context tab, token counts incl. cache write — side-card.js:312-324
- Call details sections, URL changes — call-dialog.js:308, 354; internal/ui/web/route.js:60-62
- "Session not found" and link back — app.js:658, session-page.js:420-422
- Live dot, "reconnecting…", snapshot failed text — app.js:533-538; shell.js:230-240
- Stream only for active sessions — app.js:665-670; list stream — app.js:548
- "The gateway is not answering." and Retry on list — api.js:1-10, sessions-page.js:840-847; webtest api.test.js
- "building session…" and one retry after 1 s — app.js:573-584, 654
- "Turns not loaded." — session-page.js:412; "No such call in this session." — app.js:619
- "Call details unavailable:" — call-dialog.js:436
- Banner "details" link to /api/health, × button — shell.js:150-162
- Settings: Theme Light/Dark, Style Lean/Stark; keys `theme` and `style` in localStorage; defaults dark and lean; OS theme ignored; applied before paint — internal/ui/web/theme.js:1-76, boot.js:1-17, shell.js:60-105
- Stark "monospace capital labels", square corners — style.css:92-105
- "per browser and per address" — NOT VERIFIABLE IN CODE: browser localStorage is per origin; worded from that fact
- Shortcuts — internal/ui/web/keys.js:3-12, 39-69; "?" help — shell.js:177-190; arrow keys on tabs — keys.js comment (line 5-8), not re-proved in side-card.js
- "UI accepts only GET" — internal/ui/middleware.go:106-114
- Screenshot placeholder is an HTML comment only; no images added.

Stale or doubtful statements in OTHER docs (not fixed; group D):
1. docs/install.md:195-199 and 47 — AppArmor fix text vs claude-code.md:192 "untested workaround" (see group A item 5); unchanged.
2. docs/clients/claude-code.md:116-119 — shutdown paragraph says "Captures then finish storing, so a stop with nothing in flight takes well under a second"; code (run.go:1000-1040) also waits for the UI's background work up to 5 s, which is usually instant. Not wrong, but incomplete next to architecture.md.
3. docs/roadmap.md:28 — 029 and 027 entries describe UI details (50 rows, "whole-row link", "newest first", day tiles) that ui.md now owns; left as history. Check "a session is one page scroll of turn cards, newest first" (027): the session page now has a Turns rail and tabs (session-page.js:8-10 comment), so the entry no longer describes the page.
4. docs/clients/claude-code.md:315-317 — "linked to the one session active in the last 10 minutes": same wording issue as group A item 3 (code: a hook event inside the window and no session end).
5. docs/clients/claude-code-hooks.md:118-122 — "its ungrouped list has no hook entries" and "known v1 gap": not re-checked against ungrouped-list.js.
6. docs/install.md:268 (fixed in this group) described rows as showing "start time"; the first column is "Last active".

### Group C evidence
- one-command install line and URL https://raw.githubusercontent.com/brutally-Honest/llm-gateway/main/install.sh — install.sh:5, docs/install.md:6
- flags `--check` and `--dry-run` through `sh -s --` — install.sh:6-7, cmd/gateway/install.go:119-120 (flag defs), install.go:35-37 (help)
- installer shows preflight table, asks `Run it now?`, shows plan, asks `Proceed?` — internal/install/preflight.go:467, internal/install/install.go:128, docs/install.md:61-66
- `--yes` skips Proceed; `--install-deps`, `--no-shell`, `--no-service` exist — cmd/gateway/install.go:111-118
- binary installed to ~/.local/bin/gateway — docs/install.md:303-307 (Paths), install.sh:166-167 comment
- PATH changed only on macOS when ~/.local/bin missing — internal/install/shell.go:20-22
- Linux `gateway: command not found` fix (add ~/.local/bin to PATH or run ~/.local/bin/gateway doctor) — docs/install.md Troubleshooting row
- ends "Open a new terminal, then run claude." — internal/install/install.go:704
- `claude` function loads settings only when /healthz answers, else prints "llm-gateway is not running: starting plain Claude Code" — internal/clients/claudecode/install.go:98-108
- shell files: ~/.zshrc ($ZDOTDIR), ~/.bashrc (Linux), ~/.bash_profile (macOS) — internal/install/preflight.go:365-370
- Linux systemd user unit llm-gateway.service, macOS launchd label dev.llm-gateway — internal/install/service.go:59-60, 69
- service starts at login — docs/install.md:11 (opening lines), cmd/gateway/install.go:17 help text ("login service")
- `gateway doctor` exists, checks install and running gateway, pass/warn/fail with fix — cmd/gateway/install.go:70-80, 138-145; exit 1 on fail, ibid.
- UI default http://127.0.0.1:7199 — internal/config/ui.go:27
- `gateway uninstall` removes service, claude function block, settings file; keeps config, data, binary — cmd/gateway/install.go:50-58, internal/install/uninstall.go:85-99
- `gateway uninstall --purge` deletes config dir and data dir, asks once more naming size, "cannot be undone" — internal/install/uninstall.go:156-166, 228-232
- doc line "plain uninstall keeps config, data and binary" — internal/install/uninstall.go:96-99
- `make build`, `make setup`, `make verify` exist; `make build && ./bin/gateway install` — Makefile:16, 54-55, 110; docs/install.md:99-100; BIN is ./bin (Makefile build target uses $(BIN)/gateway; BIN value not re-read, install.md and AGENTS.md use ./bin)
- `make setup` / `make verify` as contributor commands — AGENTS.md:72-73
- CI badge URL kept verbatim from the old README — README.md:3 (not re-checked against .github/workflows/verify.yml name)
- Claude Code only built client, Anthropic Messages only built protocol; others "designed for, later" — PLAN.md:66-72 (section 3)
- README link PLAN.md#3-client-support — PLAN.md:66 heading "## 3. Client support"
- anchors used: docs/install.md#requirements (line 14), #troubleshooting (line 336); docs/ui.md, what-gets-recorded.md, architecture.md, capture.md, observability.md, roadmap.md, release-checklist.md, clients/claude-code.md, clients/claude-code-hooks.md, decisions/ all exist (ls docs)
- UI list items (sessions, turns, tool calls, file and network effects, "needs a look", live updates, capture-degraded banner) — docs/ui.md headings (Sessions page, Session page, Live updates, Capture-health banner) and docs/what-gets-recorded.md:25-45
- "records prompts, responses, tool calls, tokens, API-equivalent cost" — docs/what-gets-recorded.md:6-45
- "auth headers and keys are not stored" — AGENTS.md hard rules (credentials); not re-verified in capture code
- "listens on loopback only, no login" — docs/ui.md:11-14, docs/install.md Known limits
- "direct calls to api.anthropic.com are missed" — PLAN.md:77 (known client limits)
- getting-started: "session shows up in the UI after using claude" — docs/install.md Troubleshooting row 1 (UI shows sessions); behaviour not run
- getting-started: UI file-change claim for CLAUDE settings file removal on uninstall — internal/install/uninstall.go:85-87 (settings removed when install owns it)
- NOT VERIFIABLE IN CODE: "It starts again each time you log in" — worded from service manager semantics (systemd user unit, launchd agent) and install.md; unit/plist contents not re-read
- NOT VERIFIABLE IN CODE: "a few other tools (such as curl)" and "installer offers to install what is missing" — worded from docs/install.md:14-20 only
- NOT VERIFIABLE IN CODE: CI badge workflow name verify.yml — kept from old README

#### Stale statements elsewhere for group D
1. docs/install.md:99-100 points to clients/claude-code.md §6 for the manual setup; check the anchor `#6-files-and-network-014` still resolves (heading text not re-read).
2. docs/roadmap.md:4-7 says "v1 is complete" and keeps spec numbers; fine as history, but the README no longer carries that line, so roadmap.md is now its only home.
3. The old README's "Known limits" (loopback only, secrets in captures) was removed; install.md#known-limits and what-gets-recorded.md#privacy hold it. No link elsewhere pointed to the README section (not grepped repo-wide: group D should grep for `README.md#`).
4. PLAN.md section 10 and AGENTS.md still carry the workflow split (group D, AC4).

### Group D evidence
Changes made, one per line (file: what changed; code or reason).
- AGENTS.md: rewritten as the one home for workflow rules (flow, spec skeleton, research.md, ADR escalation, commits, scopes, git notes, setup, enforcement); "PLAN.md wins" removed; commit skill no longer cited as "PLAN §11"; hooks, CI text follows .githooks/pre-push:32,43,52,61 and .github/workflows/verify.yml; Makefile setup-git (Makefile:19-23) is the source of the setup block; `make observability-up`/`-down` exist (Makefile:88-91)
- PLAN.md: line 3 no longer claims precedence over AGENTS.md (intent and design only); §10 and §11 replaced by one short §10 (layout plus a pointer to AGENTS.md); §1-§9 unchanged
- .githooks/commit-msg:2: comment points at AGENTS.md (Commits); line 18 printf left (D39)
- specs/000-foundation/research.md:13, specs/001-core-and-anthropic-passthrough/research.md:13, specs/002-capture-and-canonical-events/research.md:13: pointer to "PLAN.md §10" changed to AGENTS.md (a section that no longer exists)
- 16 history files: dated note appended about the moved sections (D38); specs/031-remove-clutter/research.md: second note on "PLAN wins over AGENTS.md"
- docs/install.md Known limits: "exactly one routed session" replaced with the rule the code has (a hook event in `effects.network.active_window`, no session end) — internal/effects/runner.go:392-407, internal/config/effects.go:121
- docs/install.md Known limits: "Older Node (22.20 and before, 23, 20 and before)" replaced with "Older Node"; the code names only 22.21+ and 24+ (24.5 for http/https) — internal/clients/claudecode/snippet.go:77-80
- docs/clients/claude-code.md sandbox: "Node older than 22.21 or 24" replaced with a pointer to the Node limit in install.md — snippet.go:77-80
- docs/clients/claude-code.md What is and is not seen: "linked to the one session active in the last 10 minutes" replaced with the code's rule — runner.go:392-407
- docs/clients/claude-code.md Shutdown: added the up-to-5 s wait for the UI's background work — cmd/gateway/run.go:507-508, 1026-1040
- docs/roadmap.md Later: "exactly one routed session" replaced with the code's rule — runner.go:392-407
- docs/roadmap.md Done: entries for 033, 034, 036, 041-047 added and a line pointing to ui.md; reason: the list stopped at 040 (git log merge history, spec goals)
- docs/clients/claude-code-hooks.md section 1: says `gateway install` uses the gateway's own settings file, not ~/.claude/settings.json, and to use one route only — internal/clients/claudecode/snippet.go:46-52, install.go:96-108, docs/clients/claude-code.md section 6 ("not in ~/.claude/settings.json")
- specs/033-docs-correctness/spec.md: `status: done` added (merged in #64)

Checked and found correct (no change): "HTTPS never decrypted" (internal/netproxy/record.go:3); the ungrouped list has no hook entries (internal/ui/web/ungrouped-list.js lists exchanges only, internal/ui/api_sessions.go:627 ungroupedSummary takes an ExchangeRow); docs/observability.md metric, span, log and port names (all found in internal/ and deploy/); docs/capture.md log lines, flags and layout; config.example.yaml defaults for hooks, capture, effects and shutdown (internal/config/*.go); docs/install.md flags (cmd/gateway/install.go:111-139), paths (internal/install/service.go:66,186, env.go:130), doctor texts (doctor.go:256-260, 410); release-checklist.md against Makefile `dist` and install.sh; install.sh comments against its code; the install.md anchor `#6-files-and-network-014` resolves.

Not checked against code: AppArmor profile text and the Claude Code sandbox bug (D42); "Hundreds of MB a day" and other group A items already marked NOT VERIFIABLE; the release steps that use `gh` and GitHub (human-run).

Status-line audit: every spec folder with a spec.md has `status: done` except 033 (fixed) and 048 (`draft`, left); 004, 007, 011, 012 hold it in brief.md; 003-017 keep kit frontmatter. All 21 ADRs use `accepted` or `superseded`, with a date, as docs/decisions/0000-template.md. `make verify` has no link check (Makefile lines 109-113: lint, test, web-test).

Link check: script in the scratchpad (not in the repo), every relative link and #anchor (GitHub slugs) in all tracked .md files outside vendored code. Final run: 144 links checked, 1 reported broken, 0 real. The one report is specs/001-core-and-anthropic-passthrough/tasks.md:318, a path inside an inline code span (D43). First run, before the fixes: 141 checked, the same 1.
- **D44** 2026-11-26 Review round 1 P2: **needs owner decision.** AGENTS.md "Do not" (line ~183) said "if it is hard to reverse, stop and ask", which contradicted Workflow step 2 (build without stopping) and the escalation rule (hard to reverse gets an ADR). Reworded: record a D-entry and continue; a hard-to-reverse call also gets an ADR; stop and ask only if it cannot be decided safely or is under "Always stop and ask first". This deviates from "prefer PLAN.md's wording" because PLAN's wording contradicted its own build-without-stopping step.
- **D45** 2026-11-26 Review round 1 P3: README.md "What it records" said file changes and network connections need the sandbox. File effects are on by default (internal/config/effects.go:110-116); the settings file sets HTTPS_PROXY, HTTP_PROXY and NODE_USE_ENV_PROXY whatever the sandbox says (internal/clients/claudecode/snippet.go:56-63); the sandbox defaults off (effects.go:120). "All Bash traffic" only with the sandbox matches docs/clients/claude-code.md line 315. Reworded.
- **D46** 2026-11-26 Review round 1 P3: docs/ui.md said the live dot turns amber on a drop. It turns red (`--live-off`, internal/ui/web/style.css:15,29,215; shell.js:228). Fixed to red.
- **D47** 2026-11-26 Review round 1 P3: docs/ui.md quoted "live: snapshot failed, retrying in 30 s" as header text. That string is only the dot's hover title and a screen-reader span (internal/ui/web/shell.js:131,234-235); the visible note is empty for snapshot_error (app.js:536); the page shows "Live snapshot failed: …" (app.js:674). Reworded with those exact strings.
- **D48** 2026-11-26 Review round 1 P3: docs/what-gets-recorded.md AppArmor section mixed two problems. Split: (a) AppArmor stops bwrap, sandbox off, doctor warns (internal/install/preflight.go:325-348), fix in install.md#check-apparmor; (b) a Claude Code sandbox failure, untested `sandbox.network.allowAllUnixSockets`, in claude-code.md#the-sandbox-optional. Removed "does not claim a fix"; kept "further investigation deferred until after v1". Both anchors resolve (headings "### Check: apparmor" and "### The sandbox (optional)"). Nothing tested.
- **D49** 2026-11-26 Review round 1 P3: README.md and docs/getting-started.md said `--purge` deletes all recorded data. It deletes the config folder and config.DefaultCaptureDir (internal/install/env.go:120,131; internal/install/uninstall.go:213-218). A custom `capture.dir` is not deleted. On macOS the logs in ~/Library/Logs/llm-gateway (internal/install/service.go:189) are created by install but never removed by uninstall (logDir used only at service.go:192,251,423). Both places now say what is deleted and kept.
- **D50** 2026-11-26 Review round 1 P3: .githooks/commit-msg printed "Format (PLAN.md §11):", a section that moved. Changed that string only to "Format (AGENTS.md, Commits):" (heading "## Commits" exists, AGENTS.md:108).
- **D51** 2026-11-26 Review round 1 P3: **needs owner decision.** AGENTS.md said "Notes stay local: never push `refs/notes/*`" while "Always stop and ask first" lists remote-ref changes, notes included. Made them agree without setting policy: notes are local by default; pushing refs/notes needs the human's explicit go-ahead; the fetch refspec in `make setup` stays so notes can be fetched if ever pushed. Consistent with D34, whose "never pushed" wording is now superseded by this line (D34 is kept as history). The notes-push policy is left to the owner.
- **D52** 2026-11-26 Review round 2 P3: docs/ui.md said `?` opens the shortcuts "for the page you are on" and "Any". The session page and call details have empty shortcut lists (internal/ui/web/keys.js:7-8), keyBar returns level "" for them (keys.js:30) and showHelp returns early without a level (internal/ui/web/shell.js:150). `?` works only on the Sessions, ungrouped and exchange pages; both places reworded.
- **D53** 2026-11-26 Review round 2 P3: docs/install.md said `--purge` deletes "the config and the captured data" and removes view-cache "with the rest of the data folder". Purge removes only the config folder and the default data folder (internal/install/uninstall.go:213-218; internal/install/env.go:120,131); a custom `capture.dir` with its view-cache and the macOS logs (internal/install/service.go:189) are kept. Reworded to match README.md and docs/getting-started.md. The help string in cmd/gateway/install.go has the same problem; it is code and stays a follow-up.
- **D54** 2026-11-26 Review round 2 P3: AGENTS.md stated three rules twice. Dependency rule: now only under "Always stop and ask first" (ADR and go-ahead); Stack and "Do not" point to or drop it. `--no-verify` ban: now only under "Always stop and ask first"; Enforcement points to it. Hard to reverse leads to an ADR: Workflow step 3 and its table are the home; the "When unsure" line refers to step 3 and keeps its meaning (D-entry and continue; stop and ask if it cannot be decided safely or is under "Always stop and ask first"). Supersedes the wording of D44 in that line.

### Review round 1 P4 follow-ups (not fixed in 048)
- docs/architecture.md shutdown steps 3-4: `stopBG` runs inside `captureRun.close` (step 4), and the UI stop runs during the grace period, not after it (cmd/gateway/run.go:429-433, 1009-1011).
- docs/what-gets-recorded.md: "claude.ai connectors" listed as going straight to Anthropic; PLAN §3 says they are not loaded at all through a custom base URL.
- docs/what-gets-recorded.md disk growth: "compression shrinks everything" is too broad; content of 4096 bytes or less is stored inline, uncompressed (internal/store/content.go:16-18,58-59).
- docs/ui.md pager: reads "Page N" without "of M" when a filter or search is on, or the total is unknown (internal/ui/web/sessions-page.js:669).
- docs/ui.md "Active": means the last exchange started under 5 minutes ago (internal/ui/api_sessions.go:26-27, api_view.go:99-102), not "running now".
- docs/what-gets-recorded.md: the stored auth kind is api_key / bearer / both / none (internal/protocols/anthropic/adapter.go:30-44), not "API key or subscription".
- specs/033-docs-correctness/research.md:118 "AGENTS.md defers to PLAN.md" has no dated note like 031's.
- No doc says `uninstall --purge` keeps macOS logs at ~/Library/Logs/llm-gateway (fixed in round 1 fixes, D49; kept here as the original finding).
- PLAN's "agnosticism rules are a review aid, not a template section" and its Commands item were not carried into AGENTS.md.

### Review round 2 P4 follow-ups (not fixed in 048)
- `--purge` also keeps ~/.local/state/llm-gateway/startup-error.json (internal/startup/startup.go:49-58; exists only after a failed start, cleared at cmd/gateway/startgate.go:39).
- what-gets-recorded.md:74 stored auth kind is api_key/bearer/both (internal/core/adapter.go:13-15); "subscription" should be "bearer token (subscription login)".
- what-gets-recorded.md:65-67 connectors: PLAN.md §3 says they are not loaded through a custom base URL (also round 1).
- what-gets-recorded.md:146 "compression shrinks everything": 4096 bytes or less are stored inline, uncompressed (internal/store/content.go:16-18) (also round 1).
- architecture.md:156 the UI stop runs while calls drain (cmd/gateway/run.go:429-433); stopBG is in captureRun.close at the start of step 4 (run.go:1008-1010) (also round 1).
- ui.md:54 the pager reads "Page N" without "of M" when filtering or when the total is unknown (internal/ui/web/sessions-page.js:669) (also round 1).
- ui.md:86-87 the unassigned label and the client column are hidden when every row has the same value (sessions-page.js:505-506).
- ui.md:136 side calls can show an error status (internal/timeline/view_overview.go:94-96); say "never a Needs-a-look item".
- ui.md:172 any non-active session (idle 5 min) has no live connection, not only ended ones (internal/ui/api_sessions.go:27, app.js:669).
- AGENTS.md notes example cites (Q2, Q5) while the text says (D2, D5).
- AGENTS.md "never code outside the current spec's scope" sits next to "small fixes don't need a spec folder".
- AGENTS.md PR step says opening a PR needs the go-ahead "(see below)", but the list below names push and merge.
- specs/048 spec Edge cases "PLAN.md's wording wins" should be scoped to this merge.
- cmd/gateway/install.go:64 uninstall --purge help text overstates what is deleted (code).
- internal/ui/web/app.js:531 comment says the dot is amber; it is red (code comment).
