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
