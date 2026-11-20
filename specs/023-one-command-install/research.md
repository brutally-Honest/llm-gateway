---
feature: 023-one-command-install
---
# Research & decisions — 023-one-command-install

## Open questions
<!-- Answer by changing "(open)" to "(answered)" and writing the answer underneath. -->
- **Q1** (open) AC29 — The Help link to `docs/install.md` on GitHub breaks
  `TestWeb_NoInlineOrExternalReferences` (internal/ui/web_test.go): no embedded web file
  may hold an absolute URL, with one exact exemption (the SVG namespace), and a test pins
  that nothing else is exempt. Making it pass changes a check, which is the human's call,
  so the link commit (02e0421, "feat(ui): link the install guide from the key bar")
  was reverted and AC29 waits. — options: A) a second exact-literal exemption for the
  help URL, tested like the SVG one (a link the browser only follows on a click; never
  fetched, CSP unaffected) — recommended; B) the gateway serves the guide itself (a
  local `/help` route rendering an embedded docs/install.md; works offline, more code);
  C) drop the Help link from 023.

## Dependencies (each with its source)

Checked 2026-11-20 against the code at `main` (d7f14b3) and Claude Code's docs.

| Dependency | Needed by | Kind | Source |
|---|---|---|---|
| Linux or macOS, amd64 or arm64 | the release builds | required | `make dist` targets; the binary is `CGO_ENABLED=0` (Makefile `build`), so no libc or other runtime library |
| curl or wget | install.sh download | required (install.sh) | install.sh |
| tar | install.sh unpack | required (install.sh) | install.sh |
| sha256sum or `shasum -a 256` | install.sh checksum | required (install.sh) | install.sh |
| curl | the `claude` function's `/healthz` probe | required (`gateway install`) | docs/clients/claude-code.md §6 (the function this feature writes) |
| Claude Code (`claude`) | the only client the installer wires | required | PLAN §3; docs/clients/claude-code.md |
| systemd user session (Linux) / launchd (macOS) | the login service | required (unless `--no-service`) | docs/clients/claude-code.md §6, "Run the gateway as a login service" |
| ports 7195–7199 on 127.0.0.1 | 7195 SOCKS, 7197 API, 7198 HTTP proxy, 7199 UI; 7196 kept free as the range's reserved slot | required | config.example.yaml (`listen_addr`, `ui.listen_addr`, `effects.network.*_addr`) |
| zsh or bash | writing the `claude` function | optional | docs/clients/claude-code.md §6 |
| git | file checkpoints and diffs; without it a folder walk with stored copies of small text files | optional | `internal/effects/git.go` (the only `exec.Command` in the gateway); config.example.yaml `effects.files` |
| bubblewrap + socat (Linux) | Claude Code's Bash sandbox, so all Bash traffic reaches the proxy | optional | Claude Code docs, https://code.claude.com/docs/en/sandboxing ("On Linux and WSL2, the sandbox relies on these packages: bubblewrap … socat"; install with `sudo apt-get install bubblewrap socat` or `sudo dnf install bubblewrap socat`) |
| nothing (macOS) | the same sandbox | – | same page: "macOS: sandboxing uses the built-in Seatbelt framework" |
| AppArmor userns restriction lifted for bwrap (Ubuntu) | the sandbox on Ubuntu 24.04+ | optional, never fixed by install | same page: with `kernel.apparmor_restrict_unprivileged_userns` on, commands run unsandboxed until a `/etc/apparmor.d/bwrap` profile is added; docs/clients/claude-code.md §6 (anthropics/claude-code#43454) |
| seccomp filter (`@anthropic-ai/sandbox-runtime`) | optional Unix-socket blocking inside the sandbox | not checked | same page: "The seccomp filter is optional"; npm, not a system package |
| ripgrep | Claude Code search | not needed | https://code.claude.com/docs/en/setup: "usually included with Claude Code"; sandboxing page: "Ripgrep is bundled with the native Claude Code binary" |
| Docker | the observability stack (`make observability-up`) | optional | docs/observability.md |
| Go, Node, make, golangci-lint, shellcheck | building and testing from source | not needed | Makefile |

Claude Code's official installer, from https://code.claude.com/docs/en/setup ("Native
Install (Recommended)", macOS, Linux, WSL): `curl -fsSL https://claude.ai/install.sh | bash`.
It puts the launcher at `~/.local/bin/claude`. Login stays with the user: "log in by
running `claude` and following the browser prompts". The docs also list Homebrew
(`brew install --cask claude-code`) and signed apt/dnf/apk repositories; the installer
offers only the native installer, the one the docs recommend.

Package names, the same on apt (Debian, Ubuntu), dnf (Fedora, RHEL) and pacman (Arch):
`git`, `bubblewrap`, `socat`, `curl`, `tar`. Homebrew: `git`, `curl`; macOS has no
`bubblewrap`/`socat` need, and ships tar and shasum.

## Release assets

install.sh and `make dist` agree on exactly these names; a release must publish them:

- `llm-gateway_<version>_<os>_<arch>.tar.gz`, with `<version>` the tag without its leading
  `v` (tag `v0.3.0` → `llm-gateway_0.3.0_linux_amd64.tar.gz`), `<os>` `linux` or
  `darwin`, `<arch>` `amd64` or `arm64`. Each holds one file at its root: `gateway`.
- `SHA256SUMS`: `sha256sum` output (`<hex>  <file name>`), one line per archive.

install.sh reads SHA256SUMS first and takes the archive name from the line ending in
`_<os>_<arch>.tar.gz`, so the latest release needs no version lookup:
`https://github.com/brutally-Honest/llm-gateway/releases/latest/download/<file>`, or
`…/releases/download/v<version>/<file>` for a version argument.

## Decisions
- D1 (2026-11-20) — Karthik: built by hand, no agent-loop kit (no /al-* commands), so
  spec.md stays a draft with no approval stamps and there is no plan.md or tasks.md.
  Branch feat/023-one-command-install from main.
- D2 (2026-11-20) — Commit scope `install` for the installer package and commands: PLAN
  §11's list has no installer area, and the history already uses scopes beside the list
  (`docs(spec)`, `docs(roadmap)`). `ui`, `docs` and `deploy` (install.sh, `make dist`) keep
  their meaning.
- D3 (2026-11-20) — Agnosticism: the generic machinery (preflight, offers, files, service
  managers, shell blocks, doctor) lives in `internal/install` and knows no client. What is
  Claude Code's (its binary, its installer command, the settings document, the shell
  function) is passed in from `internal/clients/claudecode` by `cmd/gateway`.
- D4 (2026-11-20) — curl is required by `gateway install`, though the prompt listed it
  for install.sh only: the `claude` function probes `/healthz` with curl. It is offered
  through the package manager like the other installable ones.
- D5 (2026-11-20) — "Without them network effects are off": read as the sandbox part of
  network effects. The settings file always carries `HTTPS_PROXY`/`HTTP_PROXY` (programs
  that honour them are still seen, with no prerequisite); the `sandbox` block, which makes
  every Bash command go through the proxy, is written only when bubblewrap and socat are
  found and AppArmor does not block bwrap. The summary says "network effects: sandbox off"
  with the reason.
- D6 (2026-11-20) — The default config install writes is short and loopback only (the
  listener addresses, and `effects.network.sandbox` true only when the sandbox
  prerequisites passed), with a pointer to config.example.yaml for every other key. An
  existing config is used as it is; the settings file is generated from it, with the
  sandbox forced off when the prerequisites failed.
- D7 (2026-11-20) — The settings file always has `env.ANTHROPIC_BASE_URL`, even with
  `effects.network.enabled: false` (where `gateway hooks print` prints hooks only): a
  `claude` started by the function must reach the gateway.
- D8 (2026-11-20) — Paths are the same on both OSes: binary `~/.local/bin/gateway`,
  config `~/.config/llm-gateway/`, data `config.DefaultCaptureDir` (XDG_DATA_HOME, else
  `~/.local/share/llm-gateway`) or the config's `capture.dir`. Only the service file and
  the logs differ (`~/.config/systemd/user/llm-gateway.service` and the journal;
  `~/Library/LaunchAgents/dev.llm-gateway.plist` and `~/Library/Logs/llm-gateway/`).
- D9 (2026-11-20) — What install made is recorded in `~/.config/llm-gateway/install-state.json`
  (version, files written, backups made, the hand-written lines a marker block replaced).
  Uninstall and doctor read it; the unit and plist also carry a "Managed by gateway
  install" comment so a missing state file still tells them apart from hand-made ones.
- D10 (2026-11-20) — Replacing a hand-written function: the marker block goes where the
  function was (its lines, and a comment line just above that names llm-gateway), and the
  removed lines are kept in the state file and the rc file's `.bak`. Uninstall puts those
  lines back in place of the block, so edits made to the rc file since install survive
  (restoring the whole `.bak` would lose them). A function is detected by a line matching
  `claude() {` or `function claude` outside the markers; it ends at the first line that is
  only `}`.
- D11 (2026-11-20) — Only the rc file of `$SHELL` is edited (`~/.zshrc` or
  `$ZDOTDIR/.zshrc`; `~/.bashrc` on Linux, `~/.bash_profile` on macOS). A hand-written
  function in another shell's file is reported by install and by doctor (as an older
  install left over), not edited.
- D12 (2026-11-20) — Uninstall keeps the `gateway` binary: doctor must still run after it
  ("doctor shows it gone and data kept"), and the summary prints `rm ~/.local/bin/gateway`
  as the last step. `--purge` removes the config directory and the data directory.
- D13 (2026-11-20) — A port is "free" when nothing listens on it or when the process
  holding it is the gateway (upgrade path). The holder is named with `lsof` (both OSes)
  or `ss` (Linux) when present, otherwise "another process".
- D14 (2026-11-20) — Prompts read the terminal: install.sh passes `/dev/tty` as stdin when
  it can be opened. A question with no terminal answers its default; "Proceed? [Y/n]"
  never defaults to yes without a terminal (it stops and says to pass `--yes`).
- D15 (2026-11-20) — Package-manager commands, run with `sh -c` exactly as shown:
  `sudo apt-get install -y <pkg>`, `sudo dnf install -y <pkg>`,
  `sudo pacman -S --needed --noconfirm <pkg>`, `brew install <pkg>`. As root, no `sudo`.
  apt is chosen for `ID`/`ID_LIKE` debian or ubuntu, dnf for fedora, rhel or centos,
  pacman for arch; then whichever of the three is on PATH. One command per dependency,
  as the prompt asks.
- D16 (2026-11-20) — The service manager check: Linux passes when
  `systemctl --user show-environment` succeeds; macOS when `launchctl print gui/<uid>`
  does. Lingering is read with `loginctl show-user <user> -p Linger`.
- D17 (2026-11-20) — The preflight table of `gateway install` lists curl (for the
  function) but not tar or a checksum tool: those are install.sh's own, checked by it
  before the binary exists.
- D18 (2026-11-20) — `gateway install` run from another path (a source build) copies its own
  executable to `~/.local/bin/gateway` as a planned step. Run from `~/.local/bin/gateway`
  (as install.sh does) there is nothing to copy.
- D19 (2026-11-20) — The diff shown by `--dry-run` and on upgrade is a line diff written in
  the package (no new dependency; ADR rule).
- D20 (2026-11-20) — The service is restarted on every install run, so an upgraded binary
  is the one running; "what changed" lists each managed file as new, updated or
  unchanged, and the version before and after.
- D21 (2026-11-20) — install.sh runs the verified binary from its temp folder, and
  `gateway install` copies it to `~/.local/bin/gateway` as the first step of its plan
  (D18). Copying first, as the prompt's step order reads, would write a file under
  `--check`, `--dry-run` and a "no" at "Proceed?", which promise nothing written; this
  way the end state is the same and nothing lands before the user agrees.
- D22 (2026-11-20) — `--yes` also answers the "replace your hand-written setup?"
  question (it is not a dependency offer, so `--install-deps` does not cover it), and
  the second `--purge` question. What was found is still printed first.
- D23 (2026-11-20) — The install, uninstall, doctor and help commands write text for a
  person to the command's stdout, like the viewer (ADR 0007); `-h`/`-help` on the
  gateway itself keep their JSON usage line. No lint rule is changed: nothing prints
  with fmt.Print* or touches os.Stdout outside main.go.
- D24 (2026-11-20) — The install.sh test lives in `internal/install` (Go tests drive the
  script; install.sh has no Go package): shellcheck, then the script against an
  httptest server serving a locally built archive whose `gateway` is a recording
  script. `make dist` builds the real assets for the real-machine check.
