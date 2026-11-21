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
