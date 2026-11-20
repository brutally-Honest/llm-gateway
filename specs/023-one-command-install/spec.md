---
feature: 023-one-command-install
kind: feat
status: draft
version: 1
created: 2026-11-20
supersedes:
branch: feat/023-one-command-install
---
# One-command install

## Goal
A user on Linux or macOS runs one command and is done:

```sh
curl -fsSL https://raw.githubusercontent.com/brutally-Honest/llm-gateway/main/install.sh | sh
```

then opens a new terminal and uses `claude`, which now runs through the gateway. The
same commands work on both OSes. The installer is upfront: it says what it needs, offers
to install what is missing, and shows what it will change before it changes anything. It
never uses sudo, except inside a package-manager command the user has seen and approved.

Four pieces: `install.sh` (download, verify, hand over), `gateway install`,
`gateway uninstall` and `gateway doctor`, plus the help that explains them
(`docs/install.md`, README, `gateway help`).

## Acceptance criteria

### Preflight (shared by install.sh, `gateway install --check` and `gateway doctor`)
- **AC1** — Preflight runs first and prints one table: each dependency as `✓` found (with
  its version or path), `✗` missing, or `–` optional and missing, with what that means for
  the user. Under the table: the install location (`~/.local/bin`), the config and data
  paths, and the free disk space where the data lives.
- **AC2** — Required: Linux or macOS on amd64 or arm64; Claude Code (`claude` on PATH or
  at `~/.local/bin/claude`, version shown); `curl` (the `claude` function's health probe);
  a service manager (a systemd user session on Linux, launchd on macOS); ports 7195–7199
  free on 127.0.0.1, a busy port naming the process holding it. A port held by the running
  gateway counts as free. install.sh also requires curl or wget, tar, and sha256sum or
  shasum. Anything required that is still missing after the offers stops the install
  before any gateway file is written.
- **AC3** — Optional, never stopping the install, the feature reported off with the
  reason: zsh or bash (adding the `claude` function automatically); git (file diffs);
  Claude Code's sandbox prerequisites (the sandbox part of network effects: bubblewrap and
  socat on Linux, with Ubuntu's AppArmor userns restriction checked; built in on macOS);
  Docker (the observability stack only).

### Offers to install what is missing
- **AC4** — For each missing dependency a package manager can install, preflight shows the
  exact command for this OS (brew on macOS if present; apt, dnf or pacman on Linux, chosen
  from `/etc/os-release`) and asks `Run it now? [y/N]`. Only on yes does it run that
  command, show its output and re-check that dependency. "N" continues for an optional
  dependency and stops for a required one.
- **AC5** — sudo appears only inside a shown package-manager command, never anywhere else.
  `--install-deps` answers yes to every offer; `--yes` alone runs none.
- **AC6** — Claude Code missing: offer to run Claude Code's official installer
  (`curl -fsSL https://claude.ai/install.sh | bash`), after asking, and say that logging in
  stays with the user.
- **AC7** — No package manager (for example macOS without Homebrew): print the manual
  steps for each missing item; never install a package manager.
- **AC8** — No systemd user session on Linux (containers, WSL, minimal distros): explain
  it and offer `--no-service`, which does everything except the service and prints how to
  start the gateway by hand, marked "won't start at login".

### `gateway install [--yes] [--install-deps] [--no-shell] [--no-service] [--check] [--dry-run]`
- **AC9** — `--check`: preflight only, with no install offers; it writes and runs nothing.
- **AC10** — `--dry-run`: preflight with no install offers, then every file it would write
  or change (full contents for a new file, the exact diff for an existing one) and every
  command it would run; then it stops, having written and run nothing.
- **AC11** — Without `--yes`, after preflight it shows the plan (files to write, service to
  start, shell file and lines to add) and asks `Proceed? [Y/n]`.
- **AC12** — Config: writes a default `~/.config/llm-gateway/config.yaml`, loopback only,
  only if none exists. An existing config is never overwritten.
- **AC13** — Claude Code settings: writes `~/.config/llm-gateway/claude-settings.json`, the
  file the `claude` function loads (docs/clients/claude-code.md §6), with the sandbox block
  only if its prerequisites passed preflight.
- **AC14** — Service, unless `--no-service`: a systemd user unit on Linux, enabled and
  started (with lingering off it explains and prints `loginctl enable-linger`, never runs
  it); a launchd agent on macOS (bootstrapped into `gui/$UID`, RunAtLoad, KeepAlive on
  crash, logs in `~/Library/Logs/llm-gateway/`). Stop timeout 30 s on both.
- **AC15** — Shell: zsh or bash, from `$SHELL`. Unless `--no-shell`, adds the `claude`
  function and, on macOS when it is missing, `~/.local/bin` on PATH, between
  `# >>> llm-gateway >>>` and `# <<< llm-gateway <<<`. Nothing outside the markers is
  edited, except replacing a hand-written function the user approved (AC16). Other
  shells: prints the lines and the file they belong in.
- **AC16** — Existing setups: a hand-written `claude` function, or a unit or plist not made
  by install, is shown and replaced only after the user says yes, keeping a `.bak`. Never
  two definitions of `claude` in the file install edits.
- **AC17** — Idempotent: running it again (the upgrade path) keeps the config and data,
  rewrites the managed files, restarts the service and says what changed. A second run
  with nothing new leaves every file byte-identical.
- **AC18** — It ends by waiting for `/healthz`, then prints a summary: the UI URL, what was
  installed, which optional features are off and why, and "open a new terminal, then run
  claude".

### `gateway uninstall [--yes] [--force] [--purge]`
- **AC19** — Shows what will be removed and what will be kept, asks, then stops and removes
  the service, removes the marker block and the settings file, and restores a `.bak` if
  install made one. It removes exactly what install added.
- **AC20** — Keeps the config and captured data unless `--purge`, which asks once more and
  names the data size. Never uninstalls dependencies. Prints the result.

### `gateway doctor [--json]`
- **AC21** — The preflight checks (no offers) plus the live state, each `pass`, `warn` or
  `fail` with a one-line fix and a link to the matching `docs/install.md` section. Exits
  non-zero on any fail. `--json` prints the same as one JSON document.
- **AC22** — Live checks: the binary on PATH and its version; the service installed,
  running and starting at login; each listener answering on loopback; the config valid and
  loopback only; the Claude Code settings file present and pointing at this gateway; the
  `claude` function present and not defined twice; hooks reachable; the store writable,
  with its size and the free disk; an older install left over (a unit, plist or function
  not made by install).

### install.sh
- **AC23** — POSIX sh, shellcheck clean. It runs its part of preflight first (OS, arch,
  curl or wget, tar, a checksum tool) and stops with an install hint if anything is
  missing.
- **AC24** — Downloads `SHA256SUMS` and `llm-gateway_<version>_<os>_<arch>.tar.gz` from the
  latest GitHub release, or from the release named by a version argument; verifies the
  checksum; runs the verified `gateway install` with every flag passed through, so
  `sh -s -- --check`, `--dry-run` and `--install-deps` work through the curl line; that
  install puts `gateway` in `~/.local/bin` as its first planned step (research D21).
  `LLM_GATEWAY_BASE_URL` points it at another server (local testing).
- **AC25** — `make dist` builds exactly those assets for linux and darwin on amd64 and arm64.

### Help
- **AC26** — `docs/install.md`, starting with "Requirements" (required / optional /
  not needed, per OS, what each is for, what happens without it, how the installer offers
  it); then install (with `--check`, `--dry-run`, `--install-deps`), upgrade, uninstall,
  doctor (one section per check, matching the links doctor prints), a per-OS paths table
  and troubleshooting (symptom → cause → fix).
- **AC27** — README.md has a short Install section: requirements in one line with a link,
  the curl line, `--check`, doctor, uninstall, and a link to install.md.
- **AC28** — `gateway help` and `--help` on install, uninstall and doctor.
- **AC29** — ~~The UI's key bar has a "Help" link opening docs/install.md on GitHub.~~
  Dropped (research Q1): the README and docs/install.md carry the help.

### Added after the real-machine check (research D25–D27)
- **AC30** — install-state.json records whether a replaced unit or plist was enabled at
  login and running; uninstall restores that state (enables and starts it if it was) and
  prints what it did with the command to stop it.
- **AC31** — doctor reports a UI still in its warm-up, while the API answers, as a warn
  ("warming up, about N s since the gateway started"), not a fail.
- **AC32** — uninstall run from a session routed through this gateway warns that stopping
  the service cuts that session off and asks first, even with `--yes`, unless `--force`.

## Edge cases
- **E1** — `curl | sh`: stdin is the script, so install.sh hands `/dev/tty` to
  `gateway install`. With no terminal and no `--yes`, a question is answered with its
  default "no" for offers and the install stops at "Proceed?" with a hint to pass `--yes`.
- **E2** — The gateway already running (upgrade): its own ports are not "busy".
- **E3** — A hand-written function also in another shell's file (for example `~/.bashrc`
  when `$SHELL` is zsh): reported by install and doctor, not edited.
- **E4** — `~/.local/bin` not on PATH on Linux: doctor warns with the fix; install does not
  edit PATH on Linux.
- **E5** — Claude Code installed but not yet on PATH (just installed by the offer): found at
  `~/.local/bin/claude`.
- **E6** — Ubuntu with the AppArmor unprivileged-userns restriction and no bwrap profile:
  the sandbox is reported off with the reason and the manual fix; never fixed by install.
- **E7** — Uninstall after the user edited the rc file elsewhere: only the marker block is
  touched; the hand-written function install replaced is put back in its place.
- **E8** — `gateway install` run from a build outside `~/.local/bin`: the binary is copied
  to `~/.local/bin/gateway` as part of the plan.
- **E9** — Release version given with or without a leading `v`.

## Out of scope
- Windows and WSL1; other service managers (OpenRC, runit).
- Installing a package manager, running `loginctl enable-linger`, or fixing AppArmor.
- Uninstalling dependencies or Claude Code; removing the `gateway` binary on uninstall
  (doctor needs it; the last line says how to remove it).
- Logging in to Claude Code.
- A CI release workflow (ADR 0001: local gate); `make dist` builds the assets locally.
- Signing the binaries beyond SHA256SUMS.
- Shells other than zsh and bash getting the function written for them.
- A Help link in the UI (AC29, research Q1).
