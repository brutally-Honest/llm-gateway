# Install

One command on Linux or macOS sets up the gateway, starts it at login and wires Claude
Code to it:

```sh
curl -fsSL https://raw.githubusercontent.com/brutally-Honest/llm-gateway/main/install.sh | sh
```

Then open a new terminal and run `claude`. Nothing changes before you have seen the plan
and said yes, and nothing uses `sudo` except a package-manager command you were shown and
approved.

## Requirements

The installer checks all of these first and prints them as one table: `✓` found, `✗`
missing and required, `–` optional and missing. Where a package manager can install a
missing item it shows the exact command and asks `Run it now? [y/N]`; `--install-deps`
answers yes to all of them. It never installs a package manager: without one (macOS with
no Homebrew, for example) it prints the manual steps.

### Required

Anything here still missing after the offers stops the install before any file is
written.

| Requirement | Linux | macOS | What it is for | How the installer offers it |
|---|---|---|---|---|
| OS and CPU | amd64 or arm64 | amd64 (Intel) or arm64 (Apple silicon) | the release builds | – (other systems: build from source) |
| curl or wget | either | built in (curl) | install.sh downloads the release | install.sh stops with the install hint |
| tar | yes | built in | install.sh unpacks the release | install.sh stops with the install hint |
| sha256sum or shasum | either (coreutils) | built in (shasum) | install.sh verifies the download | install.sh stops with the install hint |
| curl | yes | built in | the `claude` function asks the gateway's `/healthz` before loading its settings | `apt-get`/`dnf`/`pacman`/`brew install curl` |
| Claude Code (`claude`) | yes | yes | the client the gateway is wired to | runs Claude Code's official installer, `curl -fsSL https://claude.ai/install.sh \| bash`; logging in stays with you (run `claude` once) |
| Service manager | a systemd **user** session | launchd | starts the gateway at login, restarts it if it crashes | none: without a systemd user session (containers, WSL, minimal distros) use `--no-service` and start the gateway by hand |
| Ports 7195–7199 free on 127.0.0.1 | yes | yes | 7197 API, 7199 UI, 7198 HTTP proxy, 7195 SOCKS proxy; 7196 kept free | none: the table names the process holding a busy port. The running gateway's own ports count as free (upgrade) |

### Optional

Never stops the install; the summary lists each feature that is off and why.

| Optional | Linux | macOS | What it is for | Without it | How the installer offers it |
|---|---|---|---|---|---|
| zsh or bash | from `$SHELL` | from `$SHELL` | adding the `claude` function to `~/.zshrc`, `~/.bashrc` (Linux) or `~/.bash_profile` (macOS) | the lines are printed, with the file they belong in, to add by hand | – |
| git | yes | yes | file checkpoints and diffs of what a session changed | diffs only from stored copies of small text files | `apt-get`/`dnf`/`pacman`/`brew install git` |
| bubblewrap and socat | both | not needed (Seatbelt is built in) | Claude Code's Bash sandbox, so every command's traffic reaches the gateway's proxy | the sandbox is off: only programs that honour `HTTPS_PROXY` are seen | `apt-get`/`dnf`/`pacman install bubblewrap`, `… socat` |
| AppArmor allows bwrap | Ubuntu 24.04 and later | – | the same sandbox | the sandbox is off until bwrap has a profile | never fixed by the installer (it needs root): see [Check: apparmor](#check-apparmor) |
| Docker | yes | yes | the observability stack only (`make observability-up`, [observability.md](observability.md)) | no Prometheus, Grafana or Jaeger | – (see docs.docker.com) |

### Not needed

Go, Node, make, golangci-lint and shellcheck are only for building and testing from
source. ripgrep comes with Claude Code.

## Install

```sh
curl -fsSL https://raw.githubusercontent.com/brutally-Honest/llm-gateway/main/install.sh | sh
```

install.sh checks curl or wget, tar and a checksum tool, downloads `SHA256SUMS` and the
archive for your OS and CPU from the latest release, verifies the checksum and runs
`gateway install` from the download. That command prints the preflight table, makes its
offers, shows the plan and asks `Proceed? [Y/n]`. The first step of the plan copies the
binary to `~/.local/bin/gateway`.

Flags go after `sh -s --`; a version goes first:

```sh
curl -fsSL https://raw.githubusercontent.com/brutally-Honest/llm-gateway/main/install.sh | sh -s -- --check
curl -fsSL https://raw.githubusercontent.com/brutally-Honest/llm-gateway/main/install.sh | sh -s -- 0.3.0 --yes
```

| Flag | What it does |
|---|---|
| `--check` | the preflight table only: no offers, nothing installed. Through install.sh the archive is downloaded to a temporary folder for the check and removed |
| `--dry-run` | the preflight table, then every file it would write (full contents for a new file, the exact diff for an existing one) and every command it would run; nothing written or run |
| `--install-deps` | answers yes to every offer to install a missing dependency |
| `--yes` | does not ask `Proceed?`, nor before replacing a hand-written setup; does **not** install dependencies |
| `--no-shell` | does not touch your shell's startup file |
| `--no-service` | no login service; the summary prints how to start the gateway by hand. It won't start at login |

What it writes:

- `~/.local/bin/gateway`, the binary.
- `~/.config/llm-gateway/config.yaml`, loopback only, **only if there is none**. An existing
  config is never overwritten. Every key is in [config.example.yaml](../config.example.yaml).
- `~/.config/llm-gateway/claude-settings.json`, the settings the `claude` function loads:
  the gateway's address, the hooks, the proxy, and the sandbox block when its
  prerequisites passed. It is rewritten from your config on every install.
- The login service (see [Paths](#paths)), with a 30 s stop timeout.
- A block between `# >>> llm-gateway >>>` and `# <<< llm-gateway <<<` in your shell's
  startup file: the `claude` function and, on macOS when it is missing, `~/.local/bin` on
  PATH. Nothing outside the markers is edited.
- `~/.config/llm-gateway/install-state.json`, what install made, for uninstall and doctor.

**An older, hand-made setup** (a `claude` function you wrote, or a unit or plist install did
not make) is shown and replaced only if you say yes; the original is kept as `.bak` next to
it. The managed block goes where your function was, so there is never a second definition.
A function in another shell's file (say `~/.bashrc` while you use zsh) is reported and left
alone.

It ends by waiting for `/healthz` and printing the UI address, what was installed, what is
off and why, and "open a new terminal, then run claude".

On Linux, if lingering is off the summary says so: the gateway then runs while you are
logged in. `loginctl enable-linger $USER` keeps it running without a session; the
installer prints that command and never runs it.

To install from a source build instead, run `make build && ./bin/gateway install`. The
manual setup, step by step, is in [clients/claude-code.md §6](clients/claude-code.md#6-files-and-network-014).

## Upgrade

Run the same curl line again. Install keeps the config and the captured data, rewrites the
managed files, restarts the service and lists each file as new, updated or unchanged, with
the version before and after. Running it twice with nothing new changes no file.

An upgrade (or any install run) restarts the gateway. For a sub-second gap (about 50–80 ms
when measured on Linux, about 0.6 s on macOS) the gateway's ports refuse connections. On
macOS, launchd starts the gateway no sooner than 1 s after its last start (the plist's
`ThrottleInterval`), so an install within a second of a start waits out the rest of that
second. Claude Code retries a
refused request on its own, so a running session carries on. Nothing in flight is lost
silently: a model call still streaming when the restart begins gets the shutdown grace
period, then is cut and stored as cut by the gateway.

## Uninstall

```sh
gateway uninstall
```

It shows what it will remove and what it will keep, and asks. It stops and removes the login
service, removes the marker block (putting back a function it replaced, in its place) and
the settings file, and restores any `.bak` install made of a file it replaced. It keeps the
config, the captured data and the `gateway` binary (remove that last with
`rm ~/.local/bin/gateway`). It never uninstalls a dependency.

If install replaced a service file of your own, uninstall puts it back **as it was before
install**: enabled at login and running again if it was (install recorded both in
`install-state.json`). It prints what it did and the command to stop it
(`systemctl --user disable --now llm-gateway.service`, or
`launchctl bootout gui/$(id -u)/dev.llm-gateway`).

Run from a session routed through this gateway (a `claude` started by the function, whose
commands carry `ANTHROPIC_BASE_URL` pointing at it), uninstall warns that stopping the
service cuts that session off, and asks first, even with `--yes`. Answer no and nothing
changes; run it from a plain terminal instead, or pass `--yes --force` to skip the question.

`gateway uninstall --purge` also deletes the config and the captured data; it asks once more,
naming the size. `--yes` answers both questions.

## Doctor

```sh
gateway doctor          # or --json
```

Each check is `pass`, `warn` or `fail`; a warn or fail has a one-line fix and a link to its
section below. Doctor exits 1 if any check fails.

### Check: platform
Linux or macOS on amd64 or arm64. **Fail** elsewhere: build from source.

### Check: client
Claude Code is on PATH (or in `~/.local/bin`), with its version. **Fix:** run
`curl -fsSL https://claude.ai/install.sh | bash`, then `claude` once to log in.

### Check: curl
The `claude` function needs curl. **Fix:** install the `curl` package.

### Check: service-manager
A systemd user session (`systemctl --user show-environment` works) or launchd
(`launchctl print gui/$UID` works). **Fail** in containers, WSL and minimal distros:
reinstall with `--no-service` and start the gateway by hand. **Warn** when installed with
`--no-service`.

### Check: shell
Your `$SHELL` is zsh or bash. **Warn** otherwise: add the lines `gateway install` prints to
your shell's startup file.

### Check: git
**Warn** without git: file diffs only come from stored copies of small text files.
**Fix:** install the `git` package.

### Check: bwrap
Linux only. **Warn** without bubblewrap: no Bash sandbox. **Fix:** install `bubblewrap`,
then `gateway install` to rewrite the settings.

### Check: socat
Linux only. **Warn** without socat: no Bash sandbox. **Fix:** install `socat`, then
`gateway install`.

### Check: apparmor
Linux only. **Warn** when `kernel.apparmor_restrict_unprivileged_userns` is 1 and there is
no `/etc/apparmor.d/bwrap` profile (Ubuntu 24.04 and later): Claude Code then runs Bash
unsandboxed. **Fix**, from [Claude Code's sandboxing docs](https://code.claude.com/docs/en/sandboxing):
add the bwrap profile, then run `gateway install` again.

```sh
sudo tee /etc/apparmor.d/bwrap > /dev/null <<'EOF'
abi <abi/4.0>,
include <tunables/global>

profile bwrap /usr/bin/bwrap flags=(unconfined) {
  userns,
  include if exists <local/bwrap>
}
EOF
sudo systemctl reload apparmor
```

### Check: sandbox
macOS only: Seatbelt is built in, so this always passes.

### Check: docker
**Warn** without Docker: only the observability stack needs it.

### Check: binary
`gateway` on PATH is `~/.local/bin/gateway`. **Fail** when it is not on PATH at all;
**warn** when another copy comes first. **Fix:** put `~/.local/bin` first on PATH (Ubuntu's
`~/.profile` does it; on macOS install adds it in the marker block), or remove the other copy.

### Check: config
`~/.config/llm-gateway/config.yaml` exists, is valid and listens on loopback only. **Fix:**
the detail names the key; see [config.example.yaml](../config.example.yaml). The gateway
has no auth, so a non-loopback address fails, with the message the gateway itself stops
with ([Troubleshooting](#troubleshooting)).

### Check: service
The login service is installed, loaded, running and starts at login. Each state has its
own fix:

| doctor says | Linux (systemd) | macOS (launchd) |
|---|---|---|
| `… not found` | `gateway install` | `gateway install` |
| `installed, not loaded` | `systemctl --user daemon-reload && systemctl --user enable --now llm-gateway.service` | `launchctl bootstrap gui/$(id -u) ~/Library/LaunchAgents/dev.llm-gateway.plist` |
| `loaded, not running` | read `journalctl --user -u llm-gateway.service -f`, then `systemctl --user restart llm-gateway.service` | read `tail -f ~/Library/Logs/llm-gateway/gateway.log`, then `launchctl kickstart -k gui/$(id -u)/dev.llm-gateway` |
| `running, but not started at login` | `gateway install` | `gateway install` |
| `service is restarting after a startup error: …` | fix what the error names (often a config key); the service tries again every 10 s | the same |

A gateway that fails its start under the service waits 10 s before it exits, so the
service restarts it about every 10 s, not every second, and writes the same startup error
to the log at most once every 10 minutes. The last error is kept in
`~/.local/state/llm-gateway/startup-error.json` for doctor and removed once a start
succeeds. Run by hand, outside the service, a failed start exits at once.

### Check: listeners
Each listener answers on loopback: the API (`/healthz`), the UI (`/api/health`), the HTTP
and SOCKS proxies (a TCP connect). **Fix:** start the gateway; if it runs, check the ports
in the config and whether another process holds one (`ss -ltnp` or `lsof -iTCP -sTCP:LISTEN`).
The UI answers as soon as the gateway starts. While the API answers and the gateway started
under 2 minutes ago, a silent UI is a **warn**, "warming up, about N s since the gateway
started" (from the API's `/metrics`); after that it is a **fail**.

### The UI's view cache
The UI keeps a per-session cache of list rows and session views in `<capture.dir>/view-cache/`
(by default `~/.local/share/llm-gateway/view-cache/`). It holds only results built from the
store, so it is disposable: delete the folder at any time, even while the gateway runs. It is
rebuilt in the background after a start. `gateway uninstall --purge` removes it with the rest
of the data folder. Restoring an older `gateway.db` from a backup is detected, and that
build's cache is dropped; deleting `view-cache/` when you restore is the simple rule. A view
over 32 MiB is not cached, and the folder is kept under 1 GiB (the oldest views go first).
Folders are 0700 and files 0600. There is no config key for it.

### Check: settings
`~/.config/llm-gateway/claude-settings.json` exists and points at this gateway (its
`ANTHROPIC_BASE_URL` and every hook URL). **Fix:** `gateway install` rewrites it from your
config, for example after you change `listen_addr`.

### Check: function
The `claude` function is in your shell's startup file, in the managed block, exactly once.
**Fail** when it is missing or defined twice; **warn** when it is hand-written. **Fix:**
`gateway install` (say yes to replacing a hand-written one).

### Check: hooks
The hooks route answers on the API listener. **Fix:** start the gateway; a 404 means an old
gateway, so upgrade.

### Check: store
The capture store is writable, with its size and the free disk. **Warn** under 1 GiB free,
or before the gateway's first start (it creates the store).

### Check: leftover
An older install left over: a unit or plist install did not make, or a hand-written
`claude` function in any zsh or bash startup file. **Fix:** `gateway install` replaces the
one in your shell's file; remove one in another shell's file by hand.

### Check: state
`~/.config/llm-gateway/install-state.json` is readable. **Fix:** run `gateway install` to
rewrite it.

## Paths

| What | Linux | macOS |
|---|---|---|
| Binary | `~/.local/bin/gateway` | `~/.local/bin/gateway` |
| Config | `~/.config/llm-gateway/config.yaml` | `~/.config/llm-gateway/config.yaml` |
| Claude Code settings | `~/.config/llm-gateway/claude-settings.json` | `~/.config/llm-gateway/claude-settings.json` |
| Install state | `~/.config/llm-gateway/install-state.json` | `~/.config/llm-gateway/install-state.json` |
| Captured data | `$XDG_DATA_HOME/llm-gateway`, else `~/.local/share/llm-gateway` (or `capture.dir`) | the same |
| Login service | `~/.config/systemd/user/llm-gateway.service` | `~/Library/LaunchAgents/dev.llm-gateway.plist` |
| Logs | `journalctl --user -u llm-gateway.service` | `~/Library/Logs/llm-gateway/gateway.log` |
| Shell function | `~/.zshrc` (or `$ZDOTDIR/.zshrc`), `~/.bashrc` | `~/.zshrc` (or `$ZDOTDIR/.zshrc`), `~/.bash_profile` |
| UI | http://127.0.0.1:7199 | http://127.0.0.1:7199 |

## Known limits

- **Several sessions at once: network effects may show as unassigned.** A connection
  through the proxy is linked to a session only when exactly one routed session is
  active; with two or more open, it is listed under "unassigned connections" instead.

- **Node needs 22.21 or later, or 24 or later, for network effects.** The settings file
  sets `NODE_USE_ENV_PROXY=1`, so Node sends its calls through the gateway's proxy:
  `fetch` from Node 22.21 (22 line) or 24.0, and the `http`/`https` modules from 22.21 or
  24.5. Older Node (22.20 and before, 23, 20 and before) ignores it: with the sandbox off
  its calls go direct and are not seen; with it on they fail with `ENOTFOUND`. Check with
  `node --version`. Install's summary says the same.

- **Loopback only in v1.** Every listener (the API on 7197, with the hooks and `/metrics`,
  the UI on 7199 and the proxies on 7198 and 7195) binds 127.0.0.0/8, `::1` or
  `localhost`. Any other address stops the gateway at start, and there is no override:
  v1 has no auth, and captures hold prompts and tool results as sent, secrets included.
  Remote access with auth is not planned for now ([roadmap](roadmap.md#not-now)).

## Troubleshooting

| Symptom | Cause | Fix |
|---|---|---|
| `claude` runs but nothing shows in the UI | the terminal predates the install, so the function is not defined | open a new terminal; `type claude` should say it is a shell function |
| Claude Code stops answering (connection errors) while it runs `gateway uninstall` | the session goes through the gateway, and uninstall stopped it | uninstall now warns and asks first; answer no and run it from a plain terminal. To recover a cut-off session: `gateway install` from another terminal, or `systemctl --user start llm-gateway` |
| The UI does not load right after install or a restart | it builds its sessions list before it accepts connections (up to about 30 s on a large store) | wait, then reload |
| "llm-gateway is not running: starting plain Claude Code" | the gateway is stopped | `gateway doctor`; then `systemctl --user restart llm-gateway` or `launchctl kickstart -k gui/$(id -u)/dev.llm-gateway` |
| `install.sh: checksum mismatch` | a corrupted or tampered download | run it again; if it repeats, report it and do not install |
| `install.sh: this release has no build for …` | an unsupported OS or CPU | build from source |
| The installer asked nothing and stopped at "Proceed?" | no terminal to ask on (CI, a pipe with no tty) | run with `--yes` (and `--install-deps` to install dependencies) |
| Preflight: a port held by another process | something else uses 7195–7199 | stop it, or change the ports in the config, then install again |
| Preflight: a port held by an unknown process | neither `lsof` nor `ss` is on PATH, so the owner cannot be named, and the port does not answer as this gateway (a running gateway is found by its health routes and counts as free) | put `/usr/sbin` (macOS) or `/usr/bin` back on PATH to see the owner, then stop it, or change the ports in the config, and install again |
| Preflight: no systemd user session | a container, WSL or a minimal distro | `--no-service`, then start the gateway by hand |
| The gateway stops when you log out (Linux) | lingering is off | `loginctl enable-linger $USER` |
| Bash commands fail with `apply-seccomp: … nested userns is capability-restricted` | Claude Code's sandbox on Ubuntu 24.04+ | see [Check: apparmor](#check-apparmor); or set `effects.network.sandbox: false` and run `gateway install` |
| `claude` is defined twice | a hand-written function beside the managed block | `gateway install`, and say yes to replacing it |
| The gateway won't start, or doctor and install stop, with `listen_addr "0.0.0.0:7197": not loopback: v1 has no auth, and captures hold prompts and secrets as sent; use 127.0.0.1, ::1 or localhost` (or the same for `ui.listen_addr` or an `effects.network` proxy address) | a listener is set to a host other than loopback: `0.0.0.0`, `::`, an empty host, a LAN IP or a hostname. v1 refuses it, with no override, because anyone who can reach the port could read every capture | set the key named in the message to a `127.0.0.1` address (or `::1`, `localhost`) in `~/.config/llm-gateway/config.yaml`, or unset the `GATEWAY_*` env var that sets it, then `gateway doctor`. To use the gateway from another machine, tunnel to it (`ssh -L 7197:127.0.0.1:7197 <host>`) |
| `gateway: command not found` (Linux) | `~/.local/bin` is not on PATH | add `export PATH="$HOME/.local/bin:$PATH"` to your startup file, or run `~/.local/bin/gateway doctor` |

## Releases

For maintainers: `make dist` builds what install.sh downloads, and a release publishes
exactly these files: `llm-gateway_<version>_<os>_<arch>.tar.gz` for linux and darwin on
amd64 and arm64, each holding `gateway`, and `SHA256SUMS`. `<version>` is the tag without
its `v`. The steps before every release are in [the release checklist](release-checklist.md).
To try install.sh before a release exists, serve `dist/` and point it there:

```sh
make dist
(cd dist && python3 -m http.server 8765) &
LLM_GATEWAY_BASE_URL=http://127.0.0.1:8765 sh install.sh --dry-run
```
