---
feature: 026-github-actions
---
# Research & decisions — 026-github-actions

## Open questions
<!-- Answer by changing "(open)" to "(answered)" and writing the answer underneath. -->

## Evidence (main at f4012e3, 2026-11-21)

### Tests on a CI-like machine (AC4)
I ran the whole of `make verify`'s test targets on the Linux dev machine with what a
runner lacks taken away:
- `env -i` with a fresh `HOME`, so no `~/.claude`, no `~/.config/llm-gateway`, no global
  git identity, and no `XDG_RUNTIME_DIR` or `DBUS_SESSION_BUS_ADDRESS` (no systemd user
  session reachable).
- A PATH holding all of `/usr/bin` except `docker*`, `systemctl`, `systemd*`, `busctl`,
  `loginctl`, `dbus*`, `claude`, `lsof`, `ss`, `bwrap` and `socat`, plus go and node.
  `go test` also ran on a stricter PATH of about 60 named tools.
- `GOPROXY=off` with the module cache filled, so `go test` could not download anything.

Result: `go test -race -count=1 ./...` all ok, `make web-test` 114/114, `make kit-test`
"all kit tests passed". **No test needed a skip.** That is why:
- systemd and launchd: every install, uninstall, doctor and launchd test goes through
  `fakeRunner` and an `Env` with a fake `LookPath` (`internal/install/fake_test.go`). No
  test runs a real `systemctl` or `launchctl`.
- Docker: `test/conventions/compose_test.go` only parses `docker-compose.yml`.
- Claude Code: tests use recorded fixtures and httptest upstreams. No test runs `claude`.
- Network: every URL a test dials is an `httptest` server or `127.0.0.1`.
  `https://api.anthropic.com` and similar strings appear only as config values.
- git identity: test repos set their own (`test/githooks/world_test.go`, kit
  `helpers.sh`, `GIT_AUTHOR_*` and `GIT_CONFIG_GLOBAL=/dev/null` in
  `internal/effects/checkpoint_test.go`).

The skips that already exist stay as they are. Each has a reason, and each is about the
machine, not a CI gap: `internal/effects/symlink_test.go` (cannot make a symlink),
`internal/effects/checkpoint_test.go` (git not installed), `internal/store/store_test.go`
(root ignores file permissions). `TestInstallShShellcheck` fails, by design, when
shellcheck is missing (ADR 0011), so CI installs it.

`unshare -rn` (an empty network namespace) is not permitted on this machine, so I
checked for network use by grep, not by cutting the network. Neither can be done for
macOS from here. The macOS job is the first check there (AC7).

## Decisions
- D1 (2026-11-21) — Built by hand, no agent-loop kit and no /al-* commands, as in
  022–025: spec.md stays a draft, no plan.md or tasks.md. Commit scopes: `repo` for the
  workflow (repo-level tooling, PLAN §11), `spec` and `docs` as in 024/025.
- D2 (2026-11-21) — This reverses ADR 0001 (no CI), so ADR 0019 supersedes it, and the
  PLAN.md §11 line saying "There is no CI" changes with it. The pre-push gate stays.
- D3 (2026-11-21) — Tool installs. Go: `actions/setup-go` with `go-version-file: go.mod`,
  so the version has one pin, which answers 0001's drift concern. setup-go also caches
  `GOMODCACHE` and `GOCACHE`, keyed on go.sum. golangci-lint: `make setup`, so the
  Makefile's `GOLANGCI_LINT_VERSION` stays the only pin. `bin/golangci-lint` is cached
  with a key on the Makefile's hash, and `setup-lint` skips the download when the cached
  binary has the pinned version. Node: `setup-node` with `"22"`, the newest 22.x
  (≥ 22.12, ADR 0013). jq and shellcheck: apt on Linux, brew on macOS.
- D4 (2026-11-21) — B8: on macOS, `brew install bash` and `$(brew --prefix)/bin` added
  to `GITHUB_PATH`, so `#!/usr/bin/env bash` and run.sh's `bash "$f"` find bash 5. The
  kit itself is not changed (roadmap Later). The job prints which bash it found.
- D5 (2026-11-21) — Additions not in the request: `fail-fast: false`, so one OS failing
  does not cancel the other's result; and a `concurrency` group per ref that cancels a
  superseded run. Both only change when a run stops, never what it checks.
- D6 (2026-11-21) — Not added: a CI re-check of commit messages over the PR range. That
  was the backstop ADR 0001 named for reversing it. The pre-push hook already does it on
  every push, and the request names only `make verify`. ADR 0019 records it as the step
  to add if commits ever arrive by some route other than this machine's push.
- D7 (2026-11-21) — Release checklist smoke test. A draft release's assets are not on
  the public `releases/latest/download` URL that install.sh uses, so the smoke test
  downloads them with `gh release download` and points `LLM_GATEWAY_BASE_URL` at them
  (`docs/install.md` "Releases"). It still runs the install.sh that the curl line
  serves from main. "Upgrade over it once" is read as running the same install again
  over the running gateway (024 B2's path), then doctor.
  Update (2026-11-21, Karthik's review): the draft and `gh release download` smoke test
  is replaced by a release candidate. Tag `vX.Y.Z-rc.N` and publish it as a pre-release.
  Smoke-test it with the real curl line, pinned to the rc, with no gh and no
  `LLM_GATEWAY_BASE_URL`. On a failure, fix and make rc.N+1. When it is clean, tag
  `vX.Y.Z` on the same commit and make a draft that Karthik publishes. D8 and D9 hold
  the checks behind this.
- D8 (2026-11-21) — install.sh and release candidates:
  - **"latest" ignores pre-releases.** With no version argument, install.sh downloads
    from `https://github.com/$REPO/releases/latest/download/`. GitHub resolves that to
    the newest release that is neither a draft nor a pre-release. Checked on a public
    repo whose pre-release is newer than its stable release: neovim's `nightly`
    (prerelease, created 2026-11-19) is newer than `v0.12.6` (2026-10-09), and
    `releases/latest/download/SHA256SUMS` redirects to `…/download/v0.12.6/SHA256SUMS`.
    So an rc published as a pre-release never becomes what the plain curl line installs.
    No change to install.sh was needed. This repo has no releases yet, so the first rc
    is the first check here. Its step-2 box in the release checklist covers it.
  - **The version argument accepts an rc tag.** install.sh takes any first argument not
    starting with `-` as the version, strips one leading `v`, and builds
    `releases/download/v<version>`, with no format check. `sh install.sh v0.4.0-rc.1 --check`
    asked for `…/releases/download/v0.4.0-rc.1/SHA256SUMS` (a 404, since no such release
    exists). `make dist VERSION=v0.4.0-rc.1` named the archives
    `llm-gateway_0.4.0-rc.1_<os>_<arch>.tar.gz`, and the binary printed
    `gateway v0.4.0-rc.1`. install.sh's name parse on that SHA256SUMS line gives
    `0.4.0-rc.1`, which equals the asked version. The rc has no `_`, and the parse
    strips only the `_<os>_<arch>.tar.gz` suffix.
- D9 (2026-11-21) — The final release is rebuilt with `make dist VERSION=vX.Y.Z` on the
  rc's commit, not given the rc's files. The archive names and the binary's version
  string carry the version, so the rc's files would fail install.sh's check
  ("asked for X.Y.Z, the release holds X.Y.Z-rc.N"), and a plain install would get a
  binary that reports the rc. Builds are `-trimpath` and `CGO_ENABLED=0` from the same
  commit, so only the version string differs. `VERSION` is passed explicitly for both,
  because the two tags share a commit and `git describe` would have to choose one.
- D10 (2026-11-21) — The first macOS run (PR #58, run 44862083462) needed three kit-test
  fixes. This corrects the Evidence section's "No test needed a skip": that was checked
  on Linux only. With Homebrew's bash 5.3 first on PATH, B8's parse error is gone and
  lint, `make test` and `make web-test` all passed. `make kit-test` failed 3 of its
  scripts, all on test fixtures, none in the kit's scripts:
  - `test_answer_colon.sh` and `test_commit_attribution.sh` seeded research.md with
    GNU-only `sed -i 's/…\n…/'`. BSD sed takes the argument after `-i` as a backup
    suffix and does not turn `\n` into a newline, so no question was written, and
    `answer` rightly said "Q1 is not an open question". The fix is
    `add_open_questions` in `helpers.sh` (awk, portable). Its output is byte-identical
    to the old GNU sed's on Linux.
  - `test_harness.sh` compared the `mktemp` path with `git rev-parse --show-toplevel`.
    macOS's `$TMPDIR` is under `/var`, a symlink to `/private/var`, and git reports the
    physical path (the same shape as B16). The fix: `mk_repo` and `mk_e2e` take
    `pwd -P` of the temp dir. Reproduced on Linux first with `TMPDIR` behind a symlink:
    it failed the same way before the fix and passed after.
  Assertions are unchanged, and nothing is skipped. B8 itself (bash 3.2) stays on the
  roadmap: CI runs the kit under bash 5, not 3.2.
- D11 (2026-11-21) — The second macOS run (run 44863192822) passed the kit and failed
  `internal/ui` `TestHub_HookRefreshAndCommitInSameWindowSendEachChangeOnce`:
  "extra event: {event:exchange … d …}". It passed on the first macOS run and on every
  Linux run. This is a timing assumption in the test, not a product bug:
  - A probe showed that the commit of `c` alone sends both `entries:c` and `exchange:a`.
    So when `take(2)` returns, the 150 ms hook refresh is still armed. If it fires
    between `save("d")` and the hub handling `publish("d")` (a slow runner), it reads
    `d` and sends it as `exchange`. The commit then sends nothing new. `d` still goes
    out exactly once, which is what the product promises.
  - Reproduced on Linux 3/3 by pausing 200 ms between the save and the publish.
  - Fix (test only): the window is 10 s, so it never ends on its own, and the test ends
    it at the point it means: it stops the armed timer and runs that refresh
    synchronously. The test now also asserts that a refresh was armed, and that the
    refresh itself adds nothing before `entries:d`. Checked: 50/50 passes; it passes
    with the 200 ms pause; and it fails ("extra event: hooks") when `writeLive`'s
    `sent[...] == Hash` dedup is removed. The timer firing on its own stays covered by
    the other hook-window tests in `hub_hook_test.go`.
- D12 (2026-11-21) — The macOS job tested on Node 24, not 22. Found on the green run
  44864079366, through Karthik's question about why the Linux log was longer (web-test:
  697 lines against 123). Node 22 prints TAP when not on a terminal, about 6 lines a
  test, while Node 24 printed the spec format, one line a test.
  - Cause: setup-node put 22.23.4 on PATH ("Found in cache"). The macOS step then put
    all of `$(brew --prefix)/bin` first (D4), and that folder holds the runner image's
    own Node 24. The job's version step printed `v24.22.2`. The run was still valid
    (24 meets ADR 0013's 22.12 floor), but the two OSes tested different Node versions,
    against D3.
  - Fix (Karthik chose C of A: reorder the steps, B: a bash-only temp folder,
    C: Homebrew's bash folder): put only `$(brew --prefix bash)/bin` first, which holds
    bash and bashbug. jq and shellcheck are still found through `/opt/homebrew/bin`,
    which the runner already has on PATH.
  - Guard: the major version is set once as the job's `NODE_MAJOR` (22), read by
    setup-node. "Show tool versions" fails with an `::error::` naming the node found
    when its major differs. Checked locally: it exits 1 against this machine's v25.2.1
    with `NODE_MAJOR=22`, and 0 with `NODE_MAJOR=25`.
  - Why 22 and not 24: it is the oldest supported version, so CI fails on a newer-only
    API before users on 22 hit it. The newest end is covered by the developer's own
    `make verify` (Node v25). Raising the floor is a change to ADR 0013, not to CI.
