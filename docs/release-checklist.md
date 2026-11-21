# Release checklist

Run these steps before every release, in order. Do not go on while a step is red.
`X.Y.Z` is the release, `N` the release candidate's number (starting at 1), and `<sha>`
the release commit on main. Each build passes `VERSION` explicitly, because the rc and
the final tag end up on the same commit and `git describe` would have to pick one.

## 1. CI is green for the release commit

- [ ] Both `verify` jobs (`ubuntu-latest` and `macos-latest`) passed on `<sha>`, the
      push-to-main run, not only the PR's run:
      `gh run list --workflow verify.yml --commit <sha>`.

## 2. Release candidate

- [ ] On a clean checkout of `<sha>`, `make dist VERSION=vX.Y.Z-rc.N` builds exactly five
      files: `llm-gateway_X.Y.Z-rc.N_linux_amd64.tar.gz`, `…_linux_arm64.tar.gz`,
      `…_darwin_amd64.tar.gz`, `…_darwin_arm64.tar.gz` and `SHA256SUMS`, which lists all
      four (`docs/install.md` "Releases").
- [ ] `tar -xzOf dist/llm-gateway_X.Y.Z-rc.N_<os>_<arch>.tar.gz gateway > /tmp/gw && chmod +x /tmp/gw && /tmp/gw --version`
      for this machine prints `gateway vX.Y.Z-rc.N`.
- [ ] `git tag vX.Y.Z-rc.N <sha> && git push origin vX.Y.Z-rc.N`, then
      `gh release create vX.Y.Z-rc.N --prerelease --verify-tag --title vX.Y.Z-rc.N --notes-file <notes> dist/*`.
- [ ] The pre-release holds exactly the five files. A pre-release is never "latest", so
      the plain curl line still installs the last full release (spec 026 D8).

## 3. Smoke test on a real Linux machine and a real Mac

Use the real curl line, pinned to the rc with install.sh's version argument. Do not use
`gh` or `LLM_GATEWAY_BASE_URL`, so this is the same path a user takes.

```sh
curl -fsSL https://raw.githubusercontent.com/brutally-Honest/llm-gateway/main/install.sh | sh -s -- vX.Y.Z-rc.N --check
```

On each machine:
- [ ] The line above (`--check`): the preflight table only, and every required row is `✓`.
- [ ] The same line without `--check` (`… | sh -s -- vX.Y.Z-rc.N`): installs and starts
      the gateway.
- [ ] `gateway doctor`: every check passes, version `vX.Y.Z-rc.N`.
- [ ] In a new terminal, `claude -p "say hi"` answers.
- [ ] The UI (`http://127.0.0.1:7199`) shows that session with its call.
- [ ] Install again over it: the same line, over the running gateway. Then
      `gateway doctor` is green and the UI still shows the session.

## 4. Full manual Mac check (only when needed)

Run the full Mac check (`docs/manual-checks/macos-v1.md`) against the rc only when the
release changes one of these areas. CI does not cover them, because it runs no real
launchd, systemd, sandbox or Claude Code:
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

## 5. If anything fails

- [ ] Fix it on a branch and merge it through a PR with CI green. The fix's merge
      commit is the new `<sha>`.
- [ ] Make `rc.N+1` from step 1 on. Leave the failed rc's tag and pre-release as they
      are, as the record of what was tested.

## 6. The release

Once an rc is clean:
- [ ] `git tag vX.Y.Z <sha>` on the clean rc's commit, then `git push origin vX.Y.Z`.
- [ ] `make dist VERSION=vX.Y.Z` on that commit. This is a rebuild, not the rc's files:
      the archive names and the binary's version string carry the version, so the rc's
      archives would fail install.sh's version check for `vX.Y.Z` and would report
      themselves as the rc. The code is the same commit, so only the version differs
      (spec 026 D9).
- [ ] `gh release create vX.Y.Z --draft --verify-tag --title vX.Y.Z --notes-file <notes> dist/*`.
- [ ] Check the draft: exactly the five files, the four archives named
      `llm-gateway_X.Y.Z_*`, and `SHA256SUMS` matching them.
- [ ] I publish the draft myself, in the GitHub UI or with `gh release edit vX.Y.Z --draft=false`.
- [ ] After publishing, the plain curl line with `--check` and no version
      (`… | sh -s -- --check`) downloads `X.Y.Z`.
