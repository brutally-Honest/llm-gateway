---
feature: 031-remove-clutter
---
# Research & decisions — 031-remove-clutter

## Open questions
None. The brief asked for a build without stopping; judgment calls are D-entries below.

## Baseline (2026-11-22, main at 3566577)

`time make verify`, twice:

| Run | Wall | User | System |
|---|---|---|---|
| 1 | 1:32.20 | 78.58s | 50.81s |
| 2 | 1:08.86 | 50.33s | 48.18s |

`git ls-files | xargs wc -l | tail -1`: **119133 total**

### deadcode (before)
`go run golang.org/x/tools/cmd/deadcode@latest ./...` (x/tools v0.53.0, not in go.mod):

```
cmd/gateway/run.go:580:6: unreachable func: startCapture
internal/core/capture.go:166:23: unreachable func: reservation.heldBytes
internal/core/proxy.go:43:6: unreachable func: NewProxy
internal/core/tee.go:87:20: unreachable func: bodyCopy.size
internal/hooks/handler.go:41:19: unreachable func: Handler.SetClock
internal/server/server.go:30:6: unreachable func: New
internal/timeline/digest.go:170:6: unreachable func: UserTexts
internal/ui/call_facts.go:31:6: unreachable func: callFacts
internal/ui/digest_cache.go:107:6: unreachable func: newDigestCache
internal/ui/digest_cache.go:293:23: unreachable func: DigestCache.len
internal/ui/hub.go:166:15: unreachable func: Hub.Timelines
internal/ui/hub.go:169:15: unreachable func: Hub.Digests
internal/ui/timeline_cache.go:277:25: unreachable func: TimelineCache.len
```

deadcode ignores test callers; most of these are test seams or wrappers used only by tests.

## After (2026-11-22, branch at f6e87b2)

`time make verify`, twice, same machine:

| Run | Wall | User | System | Baseline wall |
|---|---|---|---|---|
| 1 | 0:43.46 | 100.12s | 11.60s | 1:32.20 |
| 2 | 0:06.10 | 10.19s | 2.65s | 1:08.86 |

Run 1 follows edits, so Go recompiles and reruns the changed packages; run 2 hits the
Go test cache on an unchanged tree, as baseline run 2 did. The difference in run 2 is
almost all `make kit-test`, which never caches. `go vet ./...` is green.

`git ls-files | xargs wc -l | tail -1`: **106485 total** (baseline 119133; −12648).

### deadcode (after)
Everything from the baseline list is still there and was not touched (not left orphaned
by 031). New since the baseline, and kept:

```
internal/timeline/render_view.go:14:6: unreachable func: RenderViewText
  … and its printer helpers (viewPrinter.*, FormatDuration, dur, count, groupDigits,
  oneLineText), lines 41–316
```

Kept on purpose (D8): two tests in `internal/timeline/view_test.go` call it. The other new
orphans (`timeline.ListSessions`, `ListUngrouped` and their helpers, the store-reading
`ui.BuildView`) were deleted (D12). `cmd/gateway/run.go:571 startCapture` is the
baseline's line 580 entry, moved up by the deleted dispatch lines.

## Leftover grep (step 4)
`git grep` outside `specs/` and `docs/manual-checks/` for loop.sh, /al-, kit-test,
shellcheck, Dockerfile, docker-compose.yml, backfill-sessions, "gateway sessions",
"gateway timeline", mockups. No hit for loop.sh, /al-, kit-test or backfill-sessions.
Hits that stay:
- **shellcheck**: `.github/workflows/verify.yml`, `Makefile` (setup-shellcheck),
  `README.md:58`, `docs/install.md:52`, `internal/install/installsh_test.go`, ADR 0013,
  0019, and ADR 0011 itself. All are the install.sh lint that outlives the kit (D3).
- **ADRs, as history**: 0002:13 (`docker-compose.yml`, with a 031 dated note at :58),
  0007:24, 0009:51, 0016:14 and :22 (`gateway timeline`). Their Context and Decision
  text records what was decided then. 0007 is marked superseded by 031.
- **PLAN.md:122** (Phase A's exit criterion, "as `gateway timeline` prints it") and
  **PLAN.md:158** (the Viewer stack row, now annotated as removed in 031): history of
  decisions taken, kept.
- **internal/timeline/view_test.go:416**: a test comment ("gateway timeline's overview
  line") on the kept `RenderViewText` test (D8). Test files are left as they are.
- **testdata/sessions/claude-code-task/content.jsonl**: "mockups" inside captured
  session text; it is data.
- The AGENTS.md and docs/roadmap.md kit notes name `brutally-Honest/agent-loop`, as
  expected, and contain none of the grep terms.

## Kit settings
From `.claude/loop.conf` (agent-loop kit, `brutally-Honest/agent-loop`, `PROFILE="fast"`),
so the kit can be reinstalled with the same repo layer:

```
VERIFY_CMD="make verify"
TEST_CMD="go test"
TASK_CHECK_CMD="go test ./internal/core -run '^TestCore_NoProviderOrClientIdentifiers$' -count=1"
BASE_BRANCH="main"
SPECS_DIR="specs"
PROTECTED_GLOBS=".githooks/* .github/workflows/*"
WATCHED_GLOBS="Makefile go.mod go.sum package.json package-lock.json pnpm-lock.yaml .golangci.yml .golangci.yaml .eslintrc* eslint.config.* tsconfig*.json"
TEST_GLOBS="*_test.go *.test.* *.spec.* test_*.py *_test.py tests/* test/* */tests/* */test/* */__tests__/*"
READONLY_EXTRA_CMDS="go list|go doc|go vet"
```

Reinstalling also means restoring `make verify`'s `kit-test` step (or `VERIFY_CMD` without
it), `setup-shellcheck`, the CI jq/shellcheck/bash installs, and the hooks in
`.claude/settings.json` (all in git history before 031).

## Decisions
- **D1** Spec and research are committed on their own (`docs(repo)`) before item A, so each
  item commit holds only its item and reverts cleanly.
- **D2** Item E: `gateway backfill-sessions` is not run before deleting it. Exchanges
  stored before spec 003 keep no session key and stay on the UI's Ungrouped page, which is
  acceptable (the human's call in the brief). The migrations that added the columns stay.
- **D3** Item A: shellcheck stays, against the brief's "remove shellcheck" for CI and
  `setup-shellcheck`. `internal/install/installsh_test.go` `TestInstallShShellcheck`
  (spec 023 AC23) lints `install.sh` and fails when shellcheck is missing, so removing it
  would break `make test` on CI (macOS at least) — a behaviour change for something that
  stays. CI installs shellcheck only (no Homebrew bash, no jq); `setup-shellcheck` now
  names install.sh; ADR 0011 says shellcheck outlives the kit; ADR 0019's dated line says
  so. Flip: if the human prefers to drop the shellcheck lint of install.sh, delete that
  test and the shellcheck steps in a separate commit.
- **D4** Item B: `run_hooks_latency_test.go` (`TestRun_HookLatencyP99HealthyAndStalled`)
  covered **specs/010-harness-hooks AC3** (hook ingest p99 < 10 ms, healthy and with
  storage stalled). That AC has no automated check from 031 on; it never had one that ran
  under `make test` (`-race`).
- **D5** Item H: `specs/017-sessions-list-and-call-details/spec.md:26-27`,
  `docs/manual-checks/016-session-page.md:5` and `docs/manual-checks/seed-session.mjs:2`
  also name the mockups. Left unedited: the brief allows changes under `specs/` only in
  016, and manual checks are history. git history has the files.
- **D6** Item A: removing the `.gitignore` kit lines makes the local `.agent-loop/` and
  `.claude/agent-loop-backup-20261004-134434/` show as untracked. They are local data, not
  committed and not deleted; the human can delete them or add them to `.git/info/exclude`.
- **D7** Item A: `PLAN.md` described the kit workflow (layout tree, `/al-*` steps,
  templates, `/al-change`, `/al-answer`). PLAN wins over AGENTS.md, so leaving it would
  contradict the new Workflow section; it is rewritten in the same commit as A. A's
  commit also rewords the README build prerequisite (jq → shellcheck) and the kit
  paragraph of `docs/clients/claude-code-hooks.md` §2 (now about any project hook).
- **D8** Item F: `RenderViewText` and its printer stay in `internal/timeline/render_view.go`
  (only `RenderViewJSON`, unused, is removed): `TestView_TimeLineText` and the models test
  in `internal/timeline/view_test.go` call it, and a test that stays is never deleted. It
  is dead in production code from 031 on (deadcode lists it). `UngroupedExchange` moved
  from `render.go` to `sessions.go`, because `ListUngrouped` returns it.
- **D9** Item F: `TestCore_NoProviderOrClientIdentifiers` named `cmd/gateway/viewer.go` as
  one of the paths it walks. With the file gone the entry is removed; nothing replaces it
  (`dump.go` was never in the list). The sessions/timeline cases of
  `TestSubcommands_PrintPlainText` go with the commands.
- **D10** Items F/E split: F and E both edit `run.go`, `install.go`, `plain_output_test.go`
  and `internal/logging/plain.go`. To keep one revertible commit each, F's commit keeps the
  backfill lines, and `viewerBase`/`viewerRun` (which `backfill_test.go` used) move into
  `backfill_test.go` for that one commit; E deletes them with the file. Each commit was
  verified on its own staged tree in a throwaway worktree.
- **D11** Item E: `Store.SetSessionKeys` and `KeyAssignment` stay (now
  `internal/store/session_keys.go`): `internal/ui/digest_cache_test.go` (the out-of-scope
  digest cache) and `internal/store/reader_versions_test.go` use them. Production code no
  longer calls `SetSessionKeys`. `Unkeyed` and `UnkeyedBatch`, backfill-only, are removed.
- **D12** Step 4: the new deadcode orphans that F left (`timeline.ListSessions`,
  `ListUngrouped`, `SessionSource`, `SessionListing`, `UngroupedSource`,
  `UngroupedExchange`, `sessionUsage`, `preview`, `firstUserText`, and the store-reading
  `ui.BuildView`) have no caller, tests included, and are deleted in their own commit
  (`refactor(ui)`), so F stays revertible. `PreviewRunes` and `cutRunes` stay (digest.go).
- **D13** Commit types: A is `chore(repo)` (tooling), B `test(server)`, F `refactor(server)`,
  E `refactor(store)`, H `docs(docs)`, D `build(deploy)` (Dockerfile and compose are
  `build` in the commit-conventions skill), I `ci(repo)`. Scopes are the brief's.
- **D14** Process: the main session made a few small edits itself rather than through a
  subagent: the shellcheck restore (D3), README/hooks-doc/PLAN doc lines found by the
  grep, the F/E split edits (D10), and the one-line purity-test entry (D9), which
  `make verify` caught because the F subagent ran only targeted tests.
