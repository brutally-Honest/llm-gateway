# Claude Code

Claude Code speaks the Anthropic Messages protocol. The gateway serves it under the
`/anthropic` prefix and forwards every request to `api.anthropic.com` (or the
configured `upstreams.anthropic.base_url`) as sent.

## 1. Setup

Start the gateway (`make run`, or `docker compose up`). It listens on
`127.0.0.1:7197`. Then point Claude Code at it in the shell you start `claude` from:

```sh
export ANTHROPIC_BASE_URL=http://127.0.0.1:7197/anthropic
```

Keep the `/anthropic` path: Claude Code keeps the prefix on every request, and the
gateway strips it before forwarding.

Both auth modes work. The gateway forwards the credential untouched and never stores
it.

- **API-key mode.** Set the key as usual, then start Claude Code:

  ```sh
  export ANTHROPIC_API_KEY=<your key>
  claude
  ```

  Messages requests are logged with `auth: api_key`.

- **Subscription mode (claude.ai login).** Make sure no API key is set, start Claude
  Code and log in once with `/login`:

  ```sh
  unset ANTHROPIC_API_KEY
  claude        # then run /login with your claude.ai account
  ```

  Messages requests are logged with `auth: bearer`.

To go direct again, unset `ANTHROPIC_BASE_URL` (or run
`env -u ANTHROPIC_BASE_URL claude`).

### Gateway key for rate limits

With `ratelimit.enabled: true` in the gateway config, every model call must carry a
gateway key. The key names this client in the config's `ratelimit.keys` block. Claude
Code sends it as a custom header, set with `ANTHROPIC_CUSTOM_HEADERS` in the same shell
as `ANTHROPIC_BASE_URL`:

```sh
export ANTHROPIC_CUSTOM_HEADERS="x-gateway-key: <name>"
```

`<name>` is the key from `ratelimit.keys` (for example `alice`). It is matched exactly as
sent. Claude Code sends this header on every `/v1/messages` call, including sub-agent and
side calls, so each one counts against the same key. The header is an identity label, not a
secret; the gateway strips it before forwarding and never stores it. A request without a
known key gets a 403 when limiting is on, and a call over a limit gets a 429 with
`retry-after`, which Claude Code backs off and retries.

## 2. Known limits

Some Claude Code traffic never reaches the gateway, by design of the client, not as a
gateway bug. The list lives in one place:
[PLAN.md §3, "Known client limits"](../../PLAN.md#3-client-support) (the Claude Code
entry).

## 3. Smoke checklist

Run this to check Claude Code through the gateway, once per auth mode.

Setup, once:

- Terminal A: `make run 2>&1 | tee /tmp/llmgw-smoke.log`. The log is JSON lines after
  make's own output, so steps 5 and 6 keep only lines that start with `{`.
- Terminal B, in a scratch directory that holds a few files:
  `export ANTHROPIC_BASE_URL=http://127.0.0.1:7197/anthropic`.

Then, in terminal B, run the same seven steps for each mode:

1. **Set the mode.**
   - API key: `export ANTHROPIC_API_KEY=<your key>`.
   - Subscription: `unset ANTHROPIC_API_KEY`, start `claude`, run `/login` with the
     claude.ai account, then `/exit`.
2. **Tool-call turn.** Run
   `claude -p "Use the Bash tool to run ls and tell me how many entries there are." --allowedTools Bash`.
   It exits `0`, runs the tool and answers correctly.
3. **Streamed turn.** Start `claude`, then ask `Write 200 words about lighthouses.`
   The text appears as it is generated, not in one lump at the end. Then `/exit`.
4. **Direct baseline.** Repeat steps 2 and 3 once with `env -u ANTHROPIC_BASE_URL claude …`
   and check that they behave the same (same success, same streaming).
5. **Session start.**
   `grep '^{' /tmp/llmgw-smoke.log | jq -c 'select(.msg=="request" and .method=="HEAD")'`.
   An interactive session shows one line for `/anthropic/api/hello`, with upstream's
   status and `client: unknown` (`-p` mode may not send it).
6. **Messages lines.**
   `grep '^{' /tmp/llmgw-smoke.log | jq -c 'select(.msg=="request" and (.path|endswith("/v1/messages"))) | {path,status,protocol,client,auth,stream,ttfb_ms}'`.
   Every line shows `protocol: anthropic` and `client: claude-code`; `auth` is
   `api_key` in API-key mode and `bearer` in subscription mode; `stream: true` for
   the streamed turn.
7. **Secrets.** `grep -c "$ANTHROPIC_API_KEY" /tmp/llmgw-smoke.log` (API-key mode) and
   `grep -ci 'sk-ant\|bearer ' /tmp/llmgw-smoke.log` (both modes) print `0`.

What to record, per mode: the gateway sha (`git rev-parse --short HEAD`); one line for
step 2 and one for step 3, each saying what happened and that it matches the direct
run; the output of steps 5 and 6; the counts from step 7. Never paste a key or token.
If a step fails, do not work around it: record it as an open query in the feature's
`research.md`.

## 4. Shutdown

On a stop signal the gateway stops accepting connections and lets in-flight requests,
including open streams, finish. This can take **up to 10 minutes** (the default
`shutdown_timeout`), so a long Claude Code turn is not cut off mid-answer.

- **`make run`:** the first Ctrl-C starts the graceful stop. A **second Ctrl-C stops
  the gateway at once**, cutting any open streams.
- **`docker compose stop`** (and `docker compose down`) waits the same way, up to
  10 minutes: the compose file's `stop_grace_period` matches the shutdown timeout.
  `docker compose kill` stops the container at once.

## 5. Harness hooks

Optional: Claude Code can also report its tool calls and permission checks to the gateway,
so the timeline shows which asked-for tools ran. Install, coexistence with project hooks,
removal and the match states are in [claude-code-hooks.md](claude-code-hooks.md).

## 6. Files and network (014)

The gateway can also show what a session did beside its model calls: the files that changed
in its folders, the files Claude's own Read, Grep and Glob tools looked at, and the network
connections it made. Files are checked at **checkpoints** run on each stored hook event (git,
or a folder walk without git); connections go through the gateway's **forward proxy** (HTTP
on `127.0.0.1:7198`, SOCKS5 on `127.0.0.1:7195`). Everything is configured under `effects`
in `config.example.yaml`; why it works this way is in
[ADR 0014](../decisions/0014-effect-kind-and-no-root-sensors.md).

This needs the gateway **on the host**, not in Docker: a container cannot see your project
folders or your programs. Docker keeps working for API capture alone.

### Build and configure

```sh
make build
install -m 0755 bin/gateway ~/.local/bin/gateway      # any folder on your PATH
mkdir -p ~/.config/llm-gateway
cp config.example.yaml ~/.config/llm-gateway/config.yaml   # edit as needed
```

To pick up new gateway changes later, see [Updating the service](#updating-the-service).

### Save the printed settings file

`gateway hooks print` prints one Claude Code settings document: the hooks, an `env` block
(`ANTHROPIC_BASE_URL`, `HTTPS_PROXY`, `HTTP_PROXY`, `NO_PROXY`) and, with
`effects.network.sandbox: true`, a `sandbox` block (see [The sandbox](#the-sandbox-optional)).
It writes no file; save it as a file of its own, not in `~/.claude/settings.json`:

```sh
gateway hooks print -config ~/.config/llm-gateway/config.yaml \
  > ~/.config/llm-gateway/claude-settings.json
```

Print it again whenever `listen_addr`, a proxy address or `effects.network.sandbox` changes.
With `effects.network.enabled: false` the output is the hooks alone, as in 010.

### The sandbox (optional)

`effects.network.sandbox` is `false` by default. Set it to `true` and reprint the settings
file to add a `sandbox` block that turns on Claude Code's Bash sandbox and points it at the
proxy ports, so every Bash command's traffic goes through the gateway, including programs
that ignore `HTTPS_PROXY` (Node's built-in `fetch`, ssh, database clients). Without it,
only programs that honour `HTTPS_PROXY` / `HTTP_PROXY` are seen.

- **Linux needs both `bubblewrap` and `socat`.** If either is missing, Claude Code quietly
  runs commands without the sandbox: nothing fails, and traffic from programs that ignore
  `HTTPS_PROXY` is not seen.
- **Ubuntu 24.04 and later:** Claude Code's sandbox currently fails every Bash command with
  `apply-seccomp: write /proc/self/setgroups (nested userns is capability-restricted)`. This
  is a known Claude Code bug
  ([anthropics/claude-code#43454](https://github.com/anthropics/claude-code/issues/43454)).
  An untested workaround is adding `"allowAllUnixSockets": true` under `sandbox.network` in
  the saved settings file (`sandbox.network.allowAllUnixSockets`); otherwise leave the
  sandbox off.

### Run the gateway as a login service

So it is up whenever you are, and comes back by itself if it dies.

**Linux (systemd, user unit)** — `~/.config/systemd/user/llm-gateway.service`:

```ini
[Unit]
Description=LLM gateway

[Service]
ExecStart=%h/.local/bin/gateway -config %h/.config/llm-gateway/config.yaml
Restart=on-failure

[Install]
WantedBy=default.target
```

```sh
systemctl --user daemon-reload
systemctl --user enable --now llm-gateway
journalctl --user -u llm-gateway -f      # its JSON log
```

**macOS (launchd)** — `~/Library/LaunchAgents/dev.llm-gateway.plist`, with your user name in
the paths:

```xml
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>Label</key><string>dev.llm-gateway</string>
  <key>ProgramArguments</key>
  <array>
    <string>/Users/you/.local/bin/gateway</string>
    <string>-config</string>
    <string>/Users/you/.config/llm-gateway/config.yaml</string>
  </array>
  <key>RunAtLoad</key><true/>
  <key>KeepAlive</key><true/>
  <key>StandardOutPath</key><string>/Users/you/Library/Logs/llm-gateway.log</string>
  <key>StandardErrorPath</key><string>/Users/you/Library/Logs/llm-gateway.log</string>
</dict>
</plist>
```

```sh
launchctl load -w ~/Library/LaunchAgents/dev.llm-gateway.plist
```

### Updating the service

The service keeps running the binary it started with. After you pull new gateway changes,
it runs the old one until you rebuild it, copy it into place and restart the service. Copy
with `install`, not `cp`: `cp` fails with `Text file busy` while the old binary is running.

Run these from the root of your clone of the repo.

**Linux:**

```sh
git switch main && git pull
make build
install -m 0755 bin/gateway ~/.local/bin/gateway
systemctl --user restart llm-gateway
```

**macOS:**

```sh
git switch main && git pull
make build
install -m 0755 bin/gateway ~/.local/bin/gateway
launchctl kickstart -k gui/$(id -u)/dev.llm-gateway
```

### The `claude` shell function

Put this in the startup file of the shell you actually use: `~/.zshrc` for zsh, `~/.bashrc`
for bash (`echo $SHELL` tells you which). In the wrong file it is never defined, `claude`
runs plain Claude Code, and nothing is captured. It loads the gateway settings only when the
gateway answers, so a stopped gateway means plain Claude Code, never a broken one:

```sh
claude() {
  if curl -fsS --max-time 1 http://127.0.0.1:7197/healthz >/dev/null 2>&1; then
    command claude --settings "$HOME/.config/llm-gateway/claude-settings.json" "$@"
  else
    echo "llm-gateway is not running: starting plain Claude Code" >&2
    command claude "$@"
  fi
}
```

Change the address if `listen_addr` is not the default. Claude Code started from an IDE
extension or the Claude app does not run this function, so those sessions are not captured.

### Remove the old setup

The settings file now carries everything the shell exports and user-scope hooks used to:

- Remove `export ANTHROPIC_BASE_URL=…` (and any `HTTPS_PROXY` you set for the gateway) from
  your shell profile.
- Remove the gateway's hooks from `~/.claude/settings.json`: every entry whose `url` is
  `http://127.0.0.1:7197/_gateway/hooks/claude-code`. Leave your own hooks as they are.
  Keeping both would send every event twice.

### What is and is not seen

- Files: the result at each checkpoint, not every write; the session's starting folder, the
  `additionalDirectories` of Claude Code's settings, folders learned from Claude's write and
  read paths, and `effects.files.extra_dirs`. Never `/tmp`, the home folder or `/`.
- Reads: Claude's own Read, Grep and Glob only, not `cat` or scripts.
- Network: traffic through the gateway's proxy only. HTTPS shows host, port, bytes, timing
  and outcome; it is never decrypted. With the sandbox on, all Bash commands; MCP servers
  only if they use the proxy setting.
- A connection is linked to the one session active in the last 10 minutes; with two at
  once it is listed under "unassigned connections" on the Sessions page.
- Copies of changed text files, `.env` included, are stored as-is (ADR 0005).

The full list of accepted limits is in `specs/014-file-and-network-effects/research.md`.
