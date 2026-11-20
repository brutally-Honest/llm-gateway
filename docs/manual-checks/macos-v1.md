# macOS check for v1

Run on 2026-11-20 at `4d778f4` (main), on the work Mac, by hand from a plain Claude Code
session that was never routed through the gateway. Every gateway test used `claude -p` in a
fresh login shell with the personal login (`CLAUDE_CONFIG_DIR=$HOME/.claude-personal`). The
scratch project was a new git repo at `~/Desktop/real/gw-mac-test`.

## Environment

| Item | Value |
|---|---|
| macOS | 26.5.2 (25F84) |
| Chip | Apple M4 Pro, arm64 |
| Go | 1.25.4 darwin/arm64 |
| Node | 26.12.0 (Homebrew) |
| Claude Code | 2.1.329 |
| Claude account (`~/.claude-personal`) | personal account, claude.ai login, Pro plan |
| Repo git user.email | personal address, not a company one |
| Homebrew | present (`/opt/homebrew/bin/brew`) |
| Docker | Docker Desktop, engine 29.6.1 |
| Shell | zsh; `~/.zshrc:13` already puts `~/.local/bin` first on PATH |
| System locale | `LANG=en_IN.UTF-8` (Node resolves `en-IN`) |
| Checksum tools | `/sbin/sha256sum` (new in macOS 26) and `/usr/bin/shasum` |

## Managed settings

`/Library/Application Support/ClaudeCode/` does not exist, so there is no
`managed-settings.json` and no managed keys. Nothing in the run behaved as if a managed
setting applied: the `--settings` file's hooks, `env` (`ANTHROPIC_BASE_URL`, proxy variables)
and `sandbox` block all took effect. Only that path was checked.

## Results

Deviations from the planned commands, all recorded as findings below:
- The real install ran with `--yes`: under this harness there is no terminal, and `gateway
  install` refused with "No terminal to ask on: run again with --yes". The plan it printed
  was the approved dry-run plan.
- Routed tests used `zsh -l -i -c`, not `zsh -l -c`: a non-interactive login zsh does not
  read `~/.zshrc`, so it has no `claude` function and runs plain Claude Code (D1).

| Step | Expected | Seen | Match |
|---|---|---|---|
| 1 Environment | Facts recorded; `make verify` green | See Environment. `make verify`: lint 0 issues; `make test` 19 failing tests (21 FAIL lines with subtests), all macOS only; `make verify` stops at test, so `web-test` (1 failure, locale) and `kit-test` (14 scripts failing, bash 3.2) were run on their own. Details in B6–B9 | no |
| 2 `make dist`, serve | 4 archives + SHA256SUMS on 127.0.0.1:8000 | `llm-gateway_4d778f4_{linux,darwin}_{amd64,arm64}.tar.gz` + `SHA256SUMS`; `python3 -m http.server` answered 200 | yes |
| 3 `--check` from GitHub | Preflight table, nothing written | GitHub copy byte-identical to local. All rows ✓: darwin/arm64, curl, tar, checksum `/sbin/sha256sum`; Claude Code 2.1.329, launchd `gui/501`, ports 7195–7199 free, zsh, git, Seatbelt built in, docker. Message says "Nothing was written or run" although the archive was downloaded to a temp folder (B10) | yes |
| 3 `--dry-run` | Full plan, nothing written | Plan: binary to `~/.local/bin`, `config.yaml` (`sandbox: true`), `claude-settings.json` (env, 16 async http hooks, `sandbox` with ports 7198/7195), LaunchAgent plist (RunAtLoad, KeepAlive SuccessfulExit=false, ExitTimeOut 30), `~/.zshrc` marker block, `install-state.json`; `launchctl bootout/bootstrap/enable/kickstart -k` | yes |
| 4 Install | Installed and running | Without a tty: refused, nothing written. With `--yes`: "Done: llm-gateway 4d778f4 is installed", healthz waited ok | yes (with `--yes`) |
| 4 `shasum -a 256` path | install.sh uses shasum | macOS 26 has `/sbin/sha256sum`, so the normal run uses it. Extra check with `sha256sum` hidden from PATH: row `✓ checksum tool …/shasum -a 256`, "Checksum ok" | yes (extra check) |
| 4 Gatekeeper | darwin binary runs, no prompt | Runs with no prompt. No `com.apple.quarantine` (curl does not set it); `spctl` says rejected (ad-hoc linker signature, `Identifier=a.out`), which matters only for quarantined files | yes |
| 4 launchd | Bootstrapped into `gui/$UID` | `launchctl print gui/501/dev.llm-gateway`: `state = running`, program `~/.local/bin/gateway` | yes |
| 4 PATH marker | `~/.local/bin` added to PATH in the marker block | No PATH line: `~/.local/bin` was already on PATH, and the code (`internal/install/shell.go:21`) adds it only when missing. The add path was not exercised | n/a |
| 4 Logs | `~/Library/Logs/llm-gateway/` | `gateway.log` there, JSON lines, "gateway started" | yes |
| 4 `/healthz` | Answers | `{"status":"ok"}` 200 | yes |
| 5 doctor | Required checks pass; launchd read right; UI warm-up shown as warn | 20 pass, 0 warn, 0 fail; "launchd: installed, running, starts at login". The warm-up warn could not be seen: on this small store `ui ready` logs `warm_ms` 0–1, and doctor run within 0.3 s of a restart already passed | partly (warm-up not observable) |
| 6 Session | One session, turns | Session `704c8b8b`: 3 prompts, 9 model calls (claude-opus-5-5), 12 tool calls, folder `~/Desktop/real/gw-mac-test` | yes |
| 6 Tool calls | Read, Edit, Bash, WebFetch shown | Read ×2, Edit, Bash ×7 (one failed, my quoting), Agent web-fetch with its 2 sub-agent calls | yes |
| 6 File changes | Edit and created files with diffs | `~ modified calc.py +4 −0` (diff API returns the right hunk), `+ created make3.sh one.txt two.txt three.txt`, `→ read notes.txt, calc.py` | yes |
| 6 Network effects | Bash traffic seen, sandbox block honoured | Sandbox on: `example.com:443` (curl), `github.com:443` (git) pinned to their Bash calls; Bash env shows the sandbox's proxy variables at `localhost:7198`/`7195`, so the Seatbelt block is honoured. Also 35 rows of the gateway's own hook traffic to `127.0.0.1:7197` (B4), and Node `fetch` fails `ENOTFOUND` (B5) | partly |
| 6 Cost | Cost lines | `$0.434129`, source Anthropic, 9/9 calls priced, tokens 18 in / 3,555 out / 42,390 cache write / 180,835 cache read | yes |
| 6 Unassigned | — | 68 unassigned connections, all Claude Code's own traffic labelled `harness` (api.anthropic.com, registry.npmjs.org, mcp-proxy.anthropic.com, Datadog intake) | info |
| 6 Hooks-only row | — | A `claude -p` that failed at argument parsing still sent one `SessionEnd` hook, which made a "Hooks only" session; the list showed it `active: true` for a few minutes while its timeline said ended (B12) | info |
| 6+ Symlink (extra) | Show whether the checkpoint.go:135 bug hits real users | `/tmp/gw-link → ~/Desktop/real/gw-mac-test`, `claude -p` started in `/tmp/gw-link`, Edit appended a line to notes.txt. Hook `cwd` was `/Users/racit/Desktop/real/gw-mac-test` (Claude Code resolves its folder), `~ modified notes.txt +1 −0` shown, one folder, no duplicate root. Not reproduced through Claude Code (B1) | yes |
| 7 014 step 11 (steps 9, 10 on Mac) | See 014-effects.md | Sandbox off: edit and three files with diffs; `example.com:443` for curl, `github.com:443` for git; Node not seen (KI8, allowed); two sessions active + an outside CONNECT to `example.net:443` → unassigned. Step 10: `launchctl kill SIGTERM` → exit 0, not restarted; `claude` printed "llm-gateway is not running: starting plain Claude Code" and answered; `kickstart` brought it back | yes |
| 7 Sandbox on: curl and Node | `example.com:443` for curl and Node | curl yes; Node `ERR ENOTFOUND`. With `NODE_USE_ENV_PROXY=1` Node returned 200 and `example.com:443` was captured | no (Node) |
| 7 V1 sandbox part | `sandbox` block applied | Pass (see 014 research.md, 2026-11-20) | yes |
| 7 V3 | Bash traffic at 7198/7195 with sandbox on; ports idle with it off | Pass for 7198; 7195 not exercised | yes (HTTP) |
| 7 V4 | Does Claude Code ask about new domains? | No prompts, no PermissionRequest/PermissionDenied hooks (in `-p` mode) | yes |
| 8 `kickstart -k` | Fast stop, shutdown record | `kickstart` returned in 0.042 s, healthy again 0.077 s after; `last-shutdown.json` updated (`duration_seconds` 0.0024, 0 calls cut, 0 tunnels cut); log "shutting down" → "capture stopped" → "gateway stopped" | yes |
| 8 `kill -9` | KeepAlive restarts it | `last terminating signal = Killed: 9`, new pid, healthy 3.26 s later; shutdown record correctly unchanged | yes |
| 8 Install again | No change, no duplication | Over a running gateway: **failed 2 of 2** with `launchctl bootstrap … exit status 5 Bootstrap failed: 5: Input/output error`, leaving the agent unloaded and the gateway down (B2). With nothing loaded: succeeded. Files: no checksum changed across the reinstalls, one marker block, one launchd job | no |
| 8 doctor after failed reinstall | — | "Login service: installed, not running" although the agent is not loaded at all; its fix says run `gateway install`, which is what failed (B3) | no |
| 8 Uninstall from plain session | No "cutting itself off" warning | No such warning (this session has no `ANTHROPIC_BASE_URL` or proxy variables). Removed plist, marker block, settings, state; kept binary, config, 5.6 MiB data. The keep list names "bubblewrap, socat" on macOS (B11) | yes |
| 8 doctor after uninstall | Gone, data kept | Login service, settings, claude function: fail "not found … run gateway install"; Store pass, 5.6 MiB kept | yes |
| 8 Install again | Installs | Succeeded; doctor all required pass | yes |
| 9 `make observability-up` | Stack up | prometheus v3.15.1, grafana 12.4.15, jaeger 2.22.0 started | yes |
| 9 Prometheus target | UP via host.docker.internal | `http://host.docker.internal:7197/metrics up`, `up=1`, although the gateway listens on 127.0.0.1 only | yes |
| 9 Dashboard data | Panels have data | Grafana 200, dashboard `llm-gateway-self` 200; its request-rate query returns 12 series, proxy-overhead p50 1 series | yes |
| 9 Jaeger trace | One trace with tracing on | Config backed up, `tracing.enabled: true`, "tracing on" logged; one prompt gave trace `f95ab276…`: `gateway.receive` 1334 ms > `upstream_request` 1334 ms, `response_stream`, `capture` 43.9 ms, 2× `store_write`. Jaeger flags a clock-skew warning on `capture` (it outlives its parent; info only). Jaeger 2.x serves `/api/v3/…`, not `/api/services` | yes |
| 9 Restore, down | Config restored, stack down | `cmp` identical to backup, restarted without tracing, `make observability-down` removed containers and network | yes |
| 9b Compose gateway | — | Skipped by choice | skipped |
| Extra: lsof names holder | Port holder named | A Python server on 7196: `✗ Ports … 7196 held by Python (pid 38173)`; the running gateway's own ports: `held by the running gateway`. With `lsof` missing from PATH the gateway's own ports read "held by another process" (B13) | yes |

## Bugs

### B1 — Effects drop git changes when the watched folder path has a symlink (product)

`TestCheckpoint_GitRepoChanges`, `TestCheckpoint_GitIgnoredEnvSeen` and
`TestRoots_LearnedAndRejected` fail on macOS, where `$TMPDIR` is under `/var`, a symlink to
`/private/var`:

```
--- FAIL: TestCheckpoint_GitRepoChanges (0.24s)
    checkpoint_test.go:165: effects = map[], want 3
--- FAIL: TestCheckpoint_GitIgnoredEnvSeen (0.21s)
    checkpoint_test.go:240: effects = map[], want only .env created
--- FAIL: TestRoots_LearnedAndRejected (0.18s)
    checkpoint_test.go:308: roots = [/private/var/folders/…/002 /var/folders/…/001 /var/folders/…/003/out],
      want [/var/folders/…/001 /var/folders/…/002 /var/folders/…/003/out]
```

Suspected location: `internal/effects/checkpoint.go:135`. `abs` is built from git's
`--show-toplevel` (`internal/effects/git.go:51`, symlinks resolved) and checked with
`within(abs, root)` against the root as given (unresolved), so every git candidate is skipped
and the change is lost silently. `learnRoot` (`internal/effects/runner.go:381`) adds the
resolved `top`, so the same folder can be watched twice under two spellings.

Real-world impact (extra check): not reproduced through Claude Code. Started from
`/tmp/gw-link`, Claude Code reported `cwd` as the resolved path, and the edit was captured
under one root. It still hits any client or payload that sends an unresolved path, and the
tests. Suspected fix: resolve roots with `filepath.EvalSymlinks` when they are learned or
given, before any comparison, and compare resolved paths throughout. Classed as fix before
v1.

### B2 — Reinstall over a running gateway fails and leaves it stopped (product)

```
run   launchctl bootout gui/501/dev.llm-gateway
run   launchctl bootstrap gui/501 /Users/racit/Library/LaunchAgents/dev.llm-gateway.plist
Install failed: launchctl bootstrap gui/501 /Users/racit/Library/LaunchAgents/dev.llm-gateway.plist: exit status 5 Bootstrap failed: 5: Input/output error
```

2 of 2 reinstalls over a running gateway failed; the agent was then not loaded, healthz
refused, and new terminals ran plain Claude Code. Reproduced directly: `bootout` then an
immediate `bootstrap` gave exit 5 and an unloaded agent in 2 of 5 trials. `bootout` returns
before launchd has finished removing the job. Suspected location:
`internal/install/service.go:216-217` (`StartCommands`), and `install.go:543`, which stops at
the failed command without bringing the service back. Suspected fix: after `bootout`, wait
until `launchctl print gui/$UID/dev.llm-gateway` reports the job gone (bounded by
ExitTimeOut), retry `bootstrap` on exit 5, or, when the plist is unchanged, skip
`bootout`/`bootstrap` and only `kickstart -k`. Should be fixed before v1: "run gateway install
again" is the documented fix for most doctor failures.

### B3 — doctor says "installed, not running" when the agent is not loaded (product, minor)

After B2, `launchctl print` says `Could not find service "dev.llm-gateway"`, but doctor shows
`fail Login service installed, not running … then gateway install restarts it`.
`launchd.Status` (`internal/install/service.go:245`) treats a failed `launchctl print` the
same as a loaded, stopped job; the message comes from `internal/install/doctor.go:169`.
Suspected fix: report "installed, not loaded" with `launchctl bootstrap gui/$UID <plist>` as
the fix.

### B4 — With the sandbox on, the gateway records its own hook traffic as network effects (product)

In session `704c8b8b` (sandbox on), 35 `⇄ connected 127.0.0.1:7197` rows sit under tool calls
and "not caused by a tool call", and count towards "7 sites". With the sandbox off, the same
kind of session has none. Claude Code's http hooks go through the HTTP proxy at 7198 when the
sandbox is on. Suspected fix: have the proxy (`internal/netproxy`) or effects attribution
(`internal/effects/attribute.go`) drop or label connections whose target is one of the
gateway's own listeners.

### B5 — Node's fetch fails with the sandbox on (product/config)

```
$ node fetch.mjs        # sandbox on
ERR ENOTFOUND
$ NODE_USE_ENV_PROXY=1 node fetch.mjs
status 200              # and example.com:443 captured
```

014-effects.md step 11 expects `example.com:443` for Node with the sandbox on. Suspected fix:
add `"NODE_USE_ENV_PROXY": "1"` to the settings `env` block
(`internal/clients/claudecode/snippet.go`). Recorded as 014 Q2.

### B6 — macOS test portability (tests)

- **127.0.0.2** — macOS `lo0` has only 127.0.0.1, so binding 127.0.0.2 fails. 14 tests in
  `cmd/gateway` (16 FAIL lines with the SIGTERM/SIGINT subtests): `TestBinary_SignalShutdown`, `TestDashboard_QueriesOnlyExposedMetrics`,
  `TestRun_ShutdownCutsTunnelsAndUIStreamsAtOnce`, `TestRun_MetricsAfterRequest`,
  `TestRun_MetricsNotOnProxyListener`, `TestRun_LastShutdownMetrics`,
  `TestRun_UIServesOnOwnListener`, `TestRun_ShutdownClosesUIStreams`,
  `TestRun_CommitsDuringShutdownAfterHubClose`, `TestRun_UIDisabledOpensNothingLogsNothing`,
  `TestRun_UIWithCaptureOffWarnsOnce`, `TestRun_UIBindFailureExitsRuntime`,
  `TestRun_ProxyUnaffectedByStuckUISubscriber`, `TestRun_UIServeFailureKeepsProxy`.
  ```
  {"level":"error","caller":"gateway/run.go:277","msg":"cannot bind","key":"ui.listen_addr","reason":"bind failed"}
  run_ui_test.go:185: cannot bind lines = [map[… key:listen_addr …]], want one naming ui.listen_addr
  ```
  Locations: `cmd/gateway/binary_test.go:37`, `run_ui_test.go:21`, `run_metrics_test.go:165`,
  `run_fast_shutdown_test.go:88`. Suspected fix: use `127.0.0.1:0` for both listeners (two
  free ports), or a second address that exists on every OS.
- **EADDRNOTAVAIL** — `internal/core/proxy_test.go:708`:
  ```
  dial 127.0.0.1:0: got dial tcp 127.0.0.1:0: connect: can't assign requested address, want ECONNREFUSED
  ```
  macOS refuses a dial to port 0 with EADDRNOTAVAIL. Suspected fix: have `refusedURL`
  (`proxy_test.go:692`) hand out a port from a listener it has just closed, or accept both
  errors.
- **install.sh hint text** — `internal/install/installsh_test.go:178` expects the Linux
  wording `install the tar package`; on darwin install.sh correctly prints `install it (with
  Homebrew: brew install tar)`. Suspected fix: fake `uname` as Linux in that test, or expect
  the darwin wording on darwin.
- **Locale** — `internal/ui/webtest/sessions-page.test.js:259` expects `Nov 10, …`; with the
  Mac's `en_IN` locale `startText` (`internal/ui/web/sessions-page.js:179`,
  `toLocaleDateString(undefined, …)`) gives `10 Nov, 6:09 am`. Passes 114/114 with
  `LANG=en_US.UTF-8`. Suspected fix: pin the locale in the web tests.

### B7 — Data race in cmd/gateway/binary_test.go:76 (tests)

```
WARNING: DATA RACE
Read at … by goroutine 5304:
  bytes.(*Buffer).String()
  …cmd/gateway.TestBinary_SignalShutdown.func1()  binary_test.go:76
Previous write at … by goroutine 5305:
  bytes.(*Buffer).grow() … io.Copy()   (exec's stderr copy)
```

On the "no startup line" timeout path the test reads `stderr` (a `bytes.Buffer` that `exec`
is still writing to). Only seen when startup fails (here, B6). Suspected fix: a
mutex-guarded writer for stderr, or read it only after `cmd.Wait`.

### B8 — The agent-loop kit fails under macOS's bash 3.2 (repo tooling)

`make kit-test`: 14 scripts fail, all with:

```
.claude/scripts/loop.sh: line 91: syntax error near unexpected token `;;'
.claude/scripts/loop.sh: line 91: `	last=$(for t in $(task_ids "$(art tasks)"); do case $(state_get "$t") in PASS | NEEDS-HUMAN) echo "$t" ;; esac; done | tail -1)'
```

`#!/usr/bin/env bash` resolves to `/bin/bash` 3.2.57, which cannot parse a `case` pattern's
`)` inside `$( )`. Suspected fix: move that loop into a function, or write the pattern as
`(PASS | NEEDS-HUMAN)`, which bash 3.2 accepts; or check for bash ≥ 4 at startup.

### B9 — `make verify` stops at the first failing target

Not a bug in itself, but it hid `web-test` and `kit-test` on macOS until they were run on
their own. Listed so the next Mac run checks each target.

### B10 — `--check` says "Nothing was written or run" (product, wording)

`internal/install/install.go:137`. Under install.sh, `--check` and `--dry-run` download and
unpack the archive into a temp folder (removed on exit) before `gateway install --check`
runs, and install.sh's header says `--check` "writes nothing". Suspected fix: "Nothing was
installed or changed" and, in install.sh, say the archive is fetched to a temp folder for
the check.

### B11 — Uninstall lists Linux dependencies on macOS (product, minor)

`internal/install/uninstall.go:99`: "every dependency (Claude Code, git, bubblewrap, socat,
…)". Suspected fix: build the list from the OS's preflight rows.

### B12 — A failed `claude` invocation leaves a hooks-only session (product, minor)

`claude -p --allowedTools … "<prompt>"` (prompt read as a tool name) exited at argument
parsing, but the wrapper had already passed `--settings`, and Claude Code sent one
`SessionEnd` hook under a fresh session id. The sessions list showed "Hooks only ·
~/Desktop/real/gw-mac-test" with `active: true` for some minutes while its timeline said
"■ ended". Suspected fix: a session whose only hook is SessionEnd is not active (sessions
list query), and possibly hidden.

### B13 — Without lsof on PATH, the gateway's own ports read as foreign (product, minor)

`internal/install/real.go:135` looks `lsof` up on PATH; it lives in `/usr/sbin`. With a PATH
that lacks it, the preflight says "7195 held by another process; 7197 …" for the running
gateway's own ports and fails. Suspected fix: fall back to `/usr/sbin/lsof`.

### B14 — Subcommand `--help` prints JSON log lines (product, minor)

`gateway sessions --help`, `timeline --help`, `dump --help`, `hooks print --help` print
`{"level":"info",…,"msg":"usage","forms":[…]}` instead of plain usage text
(`cmd/gateway/viewer.go:100`, `:183`, `dump.go:34`, `hooks.go:30`).

## Doc problems

- **D1** — The `claude` function is in `~/.zshrc`, which only interactive shells read.
  `zsh -l -c 'claude -p …'`, scripts, CI and editor tasks run plain Claude Code. docs/install.md
  and docs/clients/claude-code.md should say so and give the routed form for scripts:
  `claude --settings ~/.config/llm-gateway/claude-settings.json …`.
- **D2** — `curl … | sh` without a terminal stops at "Proceed? [Y/n] (no terminal: no)" and
  needs `--yes`; the install guide could say this for unattended installs.
- **D3** — roadmap line 69 ("`shasum` in install.sh"): macOS 26 ships `/sbin/sha256sum`, so
  `shasum -a 256` is now only the fallback on older macOS.
- **D4** — 014-effects.md step 11 promises `example.com:443` for Node with the sandbox on;
  that needs `NODE_USE_ENV_PROXY=1` (B5).
- **D5** — The doctor warm-up warn cannot be seen on a small store (warm-up 0–1 ms); a check
  that wants to see it needs a large store.
- **D6** — docs/observability.md: Jaeger 2.x's HTTP API is `/api/v3/…`
  (`/api/services` returns 404); worth a line for anyone scripting a trace check.

## Is this Mac ready for v1?

Not yet. The install, launchd service, doctor, capture, cost, effects, sandbox routing,
KeepAlive, uninstall and the observability stack all work on macOS 26.5.2 / M4 Pro. Fix
first:
- B2: reinstalling over a running gateway fails half the time or more and leaves it down.
- B1: before v1, as decided.
- B6–B8: `make verify` is red on a Mac.
- B4 and B5 affect the sandbox default that install turns on (`sandbox: true`), so they need a
  fix or a decision.
