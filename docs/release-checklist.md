# Release checklist

Run these steps before every release, in order. Do not go on while a step is red.
`<v>` is the tag (for example `v0.4.0`), and `<sha>` is the release commit on main.

## 1. CI is green for the release commit

- [ ] Both `verify` jobs (`ubuntu-latest` and `macos-latest`) passed on `<sha>`, the
      push-to-main run, not only the PR's run:
      `gh run list --workflow verify.yml --commit <sha>`.

## 2. The archives build

- [ ] On a clean checkout of `<sha>`, with the tag set locally (`git tag <v> <sha>`) so
      the version is `<v>` and not `dirty`, `make dist` builds exactly five files:
      `llm-gateway_<version>_linux_amd64.tar.gz`, `…_linux_arm64.tar.gz`,
      `…_darwin_amd64.tar.gz`, `…_darwin_arm64.tar.gz` and `SHA256SUMS`, which lists all
      four (`docs/install.md` "Releases").
- [ ] `tar -xzOf dist/llm-gateway_<version>_<os>_<arch>.tar.gz gateway` for this machine,
      run with `--version`, prints `<v>`.

## 3. Draft release

- [ ] `git push origin <v>`, then
      `gh release create <v> --draft --title <v> --notes-file <notes> dist/*`.
- [ ] The draft holds exactly the five files from step 2.

## 4. Smoke test on a real Linux machine and a real Mac

A draft's files are not on the public `releases/latest` URL, so fetch them and point
install.sh at them (spec 026 D7). install.sh itself comes from main, like the curl line.

```sh
mkdir -p ~/rel && gh release download <v> -D ~/rel
(cd ~/rel && python3 -m http.server 8765) &
export LLM_GATEWAY_BASE_URL=http://127.0.0.1:8765
```

On each machine:
- [ ] `curl -fsSL https://raw.githubusercontent.com/brutally-Honest/llm-gateway/main/install.sh | sh -s -- --check`:
      the preflight table only. Every required row is `✓`.
- [ ] The same line ending in `| sh`: installs and starts the gateway.
- [ ] `gateway doctor`: every check passes, version `<v>`.
- [ ] In a new terminal, `claude -p "say hi"` answers.
- [ ] The UI (`http://127.0.0.1:7199`) shows that session with its call.
- [ ] Upgrade over it once: run the install line again over the running gateway. Then
      `gateway doctor` is green and the UI still shows the session.
- [ ] Stop the server and `unset LLM_GATEWAY_BASE_URL`.

## 5. Full manual Mac check (only when needed)

Run the full Mac check (`docs/manual-checks/macos-v1.md`) only when the release changes
one of these areas. CI does not cover them, because it runs no real launchd, systemd,
sandbox or Claude Code:
- **install, uninstall, doctor** (B2: a reinstall over a running gateway failed and left
  it stopped. B3: doctor said "installed, not running" when the agent was not loaded.
  B13: without lsof, the gateway's own ports read as foreign.)
- **launchd or systemd units** (B15: back-to-back reinstalls left the gateway down 8–9 s
  under launchd's throttle. 025 AC5: the crash-loop backoff.)
- **file effects** (B1: git changes were dropped when the watched folder's path had a
  symlink.)
- **network effects or the sandbox** (B4: the gateway recorded its own hook traffic as
  network effects. B5: Node's fetch failed with the sandbox on.)
- **shutdown and ports** (024's fast shutdown and port checks. B13 above.)

To find out: `git diff --stat <previous tag>..<sha>` touches `internal/install/`,
`internal/effects/`, `internal/netproxy/`, `internal/startup/`, `install.sh`, or the
shutdown and listen code in `cmd/gateway/`. The launchd and systemd units are in
`internal/install/service.go`.

- [ ] Not needed (say why), or done with its result written under
      `docs/manual-checks/`.

## 6. Publish

- [ ] I publish the draft myself, in the GitHub UI or with `gh release edit <v> --draft=false`.
- [ ] After publishing, the plain curl line (with no `LLM_GATEWAY_BASE_URL`) with
      `--check` finds `<v>` as the latest release.
