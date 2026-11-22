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
