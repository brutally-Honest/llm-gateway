# Getting started

LLM Gateway is a small program that runs on your computer. Claude Code talks to it, it
passes everything on to the model unchanged, and it keeps a copy so you can see what Claude
Code did and what it would cost.

## What you need

- A Linux or macOS computer.
- Claude Code installed, and a terminal.
- A few other tools (such as curl). The installer checks them for you and offers to install
  what is missing. The full list is in [install.md](install.md#requirements).

## Install

1. Open a terminal.
2. Paste this line and press Enter:

   ```sh
   curl -fsSL https://raw.githubusercontent.com/brutally-Honest/llm-gateway/main/install.sh | sh
   ```

3. The installer prints a table of what it checked. If something is missing and it can
   install it, it shows the command and asks `Run it now? [y/N]`.
4. It then shows its plan: the files it will write and the commands it will run. It asks
   `Proceed? [Y/n]`. Nothing changes until you answer yes.
5. When it finishes, it tells you what it installed and what is off, and says to open a new
   terminal.

To look first without changing anything, end the line with `sh -s -- --check` (only checks)
or `sh -s -- --dry-run` (shows every file it would write).

## What happens after install

- A background service called the gateway starts. On Linux it is a systemd user unit
  (`llm-gateway.service`). On macOS it is a launchd agent (`dev.llm-gateway`). It starts
  again each time you log in.
- The installer adds a small `claude` function to your shell's startup file (`~/.zshrc` or
  `~/.bashrc` on Linux, `~/.zshrc` or `~/.bash_profile` on macOS). It sends Claude Code
  through the gateway, but only when the gateway is running. If it is stopped, you get plain
  Claude Code and a one-line notice.
- Open a **new** terminal. The old one does not have the function yet.
- Run `claude` as you always do. Nothing else about using Claude Code changes.

The program is installed as `~/.local/bin/gateway`. On Linux the installer does not change
your PATH. If `gateway: command not found` appears, add `export PATH="$HOME/.local/bin:$PATH"`
to your shell's startup file, or run `~/.local/bin/gateway doctor`. On macOS the installer
adds `~/.local/bin` to PATH if it is missing.

## Open the UI

Open <http://127.0.0.1:7199> in a browser on the same computer. It lists your sessions, and
each session shows its turns, tool calls, tokens and cost. Details: [ui.md](ui.md).

## Check that it works

1. Run `gateway doctor`. It checks the install and the running gateway, and marks each
   check pass, warn or fail with a fix for anything that is not a pass.
2. Use `claude` for a short chat.
3. Reload the UI. A new session should be there.

## What is not recorded

The gateway only sees what goes through it. A few things Claude Code sends directly never
arrive, and the copies it keeps are stored as sent, secrets included. Read
[what-gets-recorded.md](what-gets-recorded.md) once.

## Uninstall

- `gateway uninstall` stops and removes the background service, removes the `claude`
  function from your shell file and removes the gateway's Claude Code settings file. It
  shows the list first and asks. It keeps your config, your recorded data and the
  `gateway` program itself.
- `gateway uninstall --purge` does the same and also deletes your config folder and the
  default data folder. It keeps a data folder set with `capture.dir`, and on macOS the
  logs in `~/Library/Logs/llm-gateway`. It asks once more and names the size. This cannot
  be undone.

Open a new terminal afterwards. `claude` then runs as plain Claude Code.

## Get help

See [Troubleshooting in install.md](install.md#troubleshooting). Run `gateway doctor` first;
it often names the fix.
