# 049 research

## Progress
- [x] 2026-11-26 PR #77 confirmed on origin/main; branch chore/049-notes-purge-cleanup from up-to-date main; spec and research created
- [x] 2026-11-26 task 1: notes local only (150ed75 hook/Makefile/tests/AGENTS.md, b55a178 ADR 0022)
- [x] 2026-11-26 task 2: uninstall --purge wording (help, keep lists, docs; D6-D8)
- [x] 2026-11-26 task 3: app.js liveText comment says red (style.css:215 `--live-off`); no other amber text in UI or docs
- [x] 2026-11-26 task 4: outbound-destination audit; list under Notes; what-gets-recorded.md privacy bullets fixed (D9)
- [x] 2026-11-26 task 5: check of the 048 round-2 fix commit (6059872): no typos, no links added, no contradictions (`?` help matches keys.js/shell.js; --purge paragraph superseded by 6fbd103); nothing to fix
- [x] 2026-11-27 resumed after a killed session; local git notes added to 150ed75, b55a178, 0344ecf, 6fbd103, b74283f, b927021, ed6f1a4 (none had one)
- [x] 2026-11-27 review round 1 (fresh opus): P2-1, P3-1, P3-2; P4s under Follow-ups (D10, D11)
- [x] 2026-11-27 round 1 fix P2-1: unowned or restored claude-settings.json inside the purged config folder is named on the purge/Deleted lines, not kept (`TestUninstallPurgeNamesUnownedSettingsAsDeleted`; no test for the restored-.bak note)
- [x] 2026-11-27 round 1 fixes P3-1/P3-2: app.test.js:129 message and manual check 046:86 say red; no "amber" left in internal/ui, docs or README (corrects the task 3 line above, which missed both)
- [ ] review round 2 (if needed)
- [ ] spec marked done; PR and CI

## Decisions
- **D1** 2026-11-26 The main session writes spec.md and research.md itself (they are the
  session's working memory, not repo docs); every other file is edited by subagents.
- **D2** 2026-11-26 Scopes: `docs(spec)` for specs/, `docs(repo)` for AGENTS.md,
  `build(repo)`/`test(repo)` for the Makefile and test/githooks, `docs(install)` for
  install docs and help text in `cmd/gateway` via `fix(install)`, `docs(ui)` for the app.js
  comment, `docs(docs)` for other files under docs/.
- **D3** 2026-11-26 `.githooks/pre-push` lets `refs/notes` pushes through and
  `test/githooks/prepush_test.go` ("notes" subtest) asserts it. To make "never push notes"
  hold, the hook rejects any push of `refs/notes/*` and the subtest now expects the
  rejection. This strengthens a hook rather than bypassing it. Needs the owner's
  confirmation (they may prefer docs-only).
- **D4** 2026-11-26 `notes.rewriteRef refs/notes/commits` stays in `make setup`: it is local
  only (keeps notes across a rebase) and pushes nothing.
- **D5** 2026-11-26 The hook policy change outlives 049, so it is ADR 0022 (notes stay local),
  which amends ADR 0001's "notes and tags are pushed unchecked". ADR 0001's body and the
  000 spec/plan lines on the notes refspec stay as history. → ADR-0022

- **D6** 2026-11-26 Task 2 goes beyond help text: with `--purge` the "Will keep"/"Kept"
  lists now name a custom capture.dir, the macOS logs (launchd only) and the startup-error
  record when present (`purgeKept`), and the last line names the deleted folders. The brief
  asked for "any other user-facing message"; a keep list that omits them implies they are
  gone. What is deleted is unchanged. New tests: `TestUninstallPurgeListsWhatItKeeps`,
  `TestUninstallPurgeKeepsMacLogs`; `TestHelpTexts` substring updated (it already existed).
- **D7** 2026-11-26 Task 2 work went to sonnet, not haiku: it changes Go output and tests.
- **D8** 2026-11-26 `purgeKept` lists capture.dir only when it is outside both deleted
  folders (not equal to and not under either), so the keep list never names a path purge
  removes.

- **D9** 2026-11-26 Task 4: "no telemetry" holds, but docs/what-gets-recorded.md's "and
  nothing else" overclaims. It is reworded to name the network sensor's loopback proxy,
  `HTTP(S)_PROXY`, and the installer's asked-first downloads. Tracing and listener
  sentences are supported and keep their wording; "Loopback only" gets one line saying it
  is about listening, not connecting.

- **D10** 2026-11-27 Review P4-5: the HEAD commit body named a model ("The haiku check");
  it was still HEAD and unpushed, so it was amended (37863aa) after `make verify`, and its
  git note reworded. No other commit was rewritten.
- **D11** 2026-11-27 Review P3-2: `docs/manual-checks/046-ui-polish.md` is re-run guidance,
  not history, so its "amber dot" is corrected like the test message (P3-1).

## Follow-ups (review P4s)
- 2026-11-27 P4-1 docs/install.md:157 says "Will keep" shows only what exists; capture.dir,
  the binary and the dependencies line are listed without a Stat.
- 2026-11-27 P4-2 `purgeKept` skips a custom capture.dir when LoadConfig errors, though the
  partial cfg has it.
- 2026-11-27 P4-3 `within` compares paths as text: a symlinked capture.dir into the data
  folder, or a capture.dir that is a parent of DataDir/ConfigDir, is listed as kept.
- 2026-11-27 P4-4 what-gets-recorded.md:120 "minus `X-Gateway-Key`" omits hop-by-hop and
  `Proxy-*` headers.
- 2026-11-27 Settled: the OTLP exporter uses `WithEndpointURL` (observability/tracing.go:41),
  which overrides `OTEL_EXPORTER_OTLP_ENDPOINT`; the Task 4 "unverified" note is answered.

## Notes
- 2026-11-26 Task 4 outbound destinations of the `gateway` binary (sonnet audit, read-only):
  1. Provider upstream, default `https://api.anthropic.com` (protocols/anthropic/adapter.go:28),
     set by `upstreams.anthropic.base_url` / `GATEWAY_UPSTREAMS_ANTHROPIC_BASE_URL`; https
     anywhere, http loopback only. Sends the client's request verbatim minus
     `X-Gateway-Key`, hop-by-hop and `Proxy-*` headers (core/proxy.go).
  2. `HTTP_PROXY`/`HTTPS_PROXY` from the environment for that upstream traffic
     (core/proxy.go:270,279, `http.ProxyFromEnvironment`).
  3. OTLP/HTTP collector, only with `tracing.enabled` (default off), default
     `http://127.0.0.1:4318`; spans carry sizes, timings, status, protocol, client label,
     exchange id, error reason, parse status, model (client string, cut to 128 bytes); no
     content, headers or credentials.
  4. Network sensor proxies (HTTP 127.0.0.1:7198, SOCKS5 127.0.0.1:7195, on by default,
     loopback listen only) dial whatever host a local client names (netproxy/http.go:157,291,
     socks5.go:108); CONNECT tunnels are opaque; host, port, bytes and outcome are stored.
  5. Loopback health checks during install/doctor/uninstall (install/real.go).
  6. `gateway install` only: `curl -fsSL https://claude.ai/install.sh | bash` when `claude`
     is missing, after asking (default no) or with `--install-deps`
     (clients/claudecode/install.go:15, install/preflight.go:448-473).
  7. `gateway install` only: apt-get/dnf/pacman/brew for missing deps, same asking rule
     (install/pkg.go:58-75).
  8. `git` subprocess for file effects runs with `protocol.allow=never` (effects/git.go:124-127):
     no network.
  No analytics, update check or phone-home dependency in go.mod. The web UI loads only
  same-origin files (fonts are bundled). install.sh contacts only GitHub releases (or
  `LLM_GATEWAY_BASE_URL`). Unverified: whether the OTLP SDK honours `OTEL_EXPORTER_OTLP_*`
  env vars.
- 2026-11-26 Task 2 evidence: `uninstall.go` RemoveAll on `p.ConfigDir` and `p.DataDir`
  only; `p.DataDir` is `config.DefaultCaptureDir` (XDG_DATA_HOME or ~/.local/share), never
  the configured capture.dir; `logDir` (service.go:189) and `startup.Path` are never removed.
- 2026-11-26 Pre-existing, not changed: if the user answers no to the purge question, the
  final "Kept" list leaves out the config and data folders (the list was built assuming
  purge); the line "Keeping the config and the data." is printed though. Follow-up.
- 2026-11-26 First cut of `purgeKept` listed a capture.dir inside the default data folder
  (or inside the config folder) as kept, though RemoveAll deletes it; fixed so only a dir
  outside both is listed (D8).
- 2026-11-26 Task 1 grep: no stale notes text in README.md, PLAN.md or docs/*.md; the only
  hits outside AGENTS.md, Makefile, the hook and its tests are history (ADR 0001:25,
  specs/000-foundation spec.md and plan.md, specs/048 research D34/D51).
- 2026-11-26 `shellcheck .githooks/pre-push` reports 5 warnings (SC2046, SC1007 x4) that
  are on main too; `make verify` lints only install.sh, so they do not fail the gate. Not in
  049's scope; follow-up candidate.
- 2026-11-26 Slip: between the two task 1 commits the main session ran a bare `make -s`,
  which runs the default target `setup`. It re-ran `git config core.hooksPath .githooks` and
  `git config notes.rewriteRef refs/notes/commits` on this clone (both already set to those
  values; the refspec lines were already removed, so none was added) and `go mod download`.
  The ADR commit b55a178 was therefore made without `make verify` first; `make verify` was
  run on that same tree right after and passed.
- 2026-11-26 This clone's notes refspec is `refs/notes/*:refs/notes/remote/*` (no `+`, a
  different destination), not the `+refs/notes/*:refs/notes/*` that `make setup` used to
  add, so the removal command for the owner must match any `refs/notes` refspec.
