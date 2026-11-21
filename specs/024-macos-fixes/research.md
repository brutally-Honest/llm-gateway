---
feature: 024-macos-fixes
---
# Research & decisions — 024-macos-fixes

## Open questions
<!-- Answer by changing "(open)" to "(answered)" and writing the answer underneath. -->

## Evidence (main at 2db8378, 2026-11-21)

The bugs, their output and suspected locations are in `docs/manual-checks/macos-v1.md`.

### Node and `NODE_USE_ENV_PROXY` (B5)

From the Node.js docs (v26.13.1, `cli.html` and `http.html`, read 2026-11-21):
- `NODE_USE_ENV_PROXY=1`: added in v24.0.0 and v22.21.0. `--use-env-proxy`: v24.5.0 and
  v22.21.0. Stability 1.1 (active development), not experimental.
- With it, Node reads `HTTP_PROXY`, `HTTPS_PROXY` and `NO_PROXY` at startup and routes
  requests through the proxy (lowercase wins when both cases are set). The current docs
  say this covers `fetch()` and, through the global agent, `http.request()` /
  `https.request()`; the built-in proxy section of `http` is marked v24.5.0 and v22.21.0.
  So: v24.5.0+ and v22.21.0+ get `fetch` and `http`/`https`; v24.0.0–v24.4.x get `fetch`
  (the docs give no per-version breakdown, so this is read from the "Added in" lines).
- Older Node (v22.20 and before, v23.x, v20 and before) does not know the variable and
  ignores it. With the sandbox off its `fetch` connects directly, so the call is not seen
  (014 KI8). With the sandbox on the direct DNS lookup is blocked and `fetch` fails with
  `ENOTFOUND`, as seen on the Mac before the fix: the sandbox still blocks, nothing leaks.
- Seen on the Mac (Node 26.12.0, check of 2026-11-20): with the variable, the script
  returned 200 and `example.com:443` was captured. This Linux machine has Node v25.2.1.

## Decisions
- D1 (2026-11-21) — Built by hand, no agent-loop kit and no /al-* commands, as in 022 and
  023: spec.md stays a draft, no plan.md or tasks.md. Unclear details take the simplest
  reading that fits and get a D-entry here. Commit scopes from PLAN §11: `install`
  (install, uninstall, doctor, 023 D2), `claude-code`, `proxy` (the forward proxy),
  `server` (`cmd/gateway`), `ui`, `docs`, `docs(spec)`, `docs(roadmap)`.
- D2 (2026-11-21) — B2, launchd start: `enable` runs before `bootstrap` (bootstrapping a
  disabled job also fails with EIO), and the `kickstart` after a fresh `bootstrap` has no
  `-k` (RunAtLoad has just started the job; `-k` would restart it at once). The bootout
  poll runs every 200 ms for up to ExitTimeOut + 5 s (35 s); bootstrap is tried 5 times
  with a 250 ms backoff that doubles (3.75 s in all). "Never leave the gateway down" is
  read as: no path ends with the job unloaded without saying so. When bootstrap still
  fails, install exits 1 with "The gateway is not running. Start it with:" and the exact
  `launchctl bootstrap gui/$UID <plist>` line. A plist that is unchanged but not loaded
  (after an earlier failure) takes the bootstrap path, so install also repairs it.
- D3 (2026-11-21) — B4, "the gateway's own listener addresses": the configured and bound
  addresses of the API, UI and both proxy listeners. A target matches on the same port
  and the same host, with `localhost` matching a loopback IP. Another loopback address on
  the same port (127.0.0.2:7197) is not the gateway's and is still recorded. The check is
  in netproxy's Recorder, so CONNECT, plain `http://` and SOCKS share it.
- D4 (2026-11-21) — B3, systemd "loaded": `systemctl --user show -p LoadState --value` is
  `loaded`. `not-found`, `masked` and `bad-setting` all count as not loaded.
- D5 (2026-11-21) — B5: the install summary note is a `Client.NetworkNote` printed only
  with network effects on, so `internal/install` names no runtime of its own. Doctor does
  not check for the variable: install rewrites the settings file on every run.
- D6 (2026-11-21) — B10: the binary cannot tell it was fetched by install.sh, so it says
  only what it did ("Ready to install. Nothing was installed."), and install.sh, which did
  the download, adds "The archive was downloaded to a temporary folder for the check and
  removed." when `--check` is among its flags. Read together they are the requested
  sentence. The line is printed just before the script's EXIT trap removes the folder.
- D7 (2026-11-21) — B14, "structured logging is for the running gateway only": every
  command a person runs (help, `--version`, install, uninstall, doctor, sessions,
  timeline, dump, hooks print, backfill-sessions) and the top-level `-h`, `--help` and bad
  arguments print plain text. They keep their zap calls through `logging.Plain`, which
  writes `<command>: <msg> (key: value, …)`. Config errors when the gateway itself starts
  stay JSON: that is the running gateway's log, which the login service keeps. A
  positional argument is still never echoed (spec 000 kept only its count). This replaces
  spec 000's JSON usage line for `-h`.
- D8 (2026-11-21) — B13: the fix is the wording only ("an unknown process (lsof not found
  on PATH …)"). A port with an unknown owner still counts as busy, so with no lsof or ss on
  PATH a reinstall over the running gateway still stops at preflight, now saying why. The
  check's other suggestion, falling back to `/usr/sbin/lsof`, was not asked for.
- D9 (2026-11-21) — B6: tests that need a second listener take a free 127.0.0.1 port
  (`freeAddr`): the config refuses two listeners spelled the same, so `127.0.0.1:0` twice
  is refused. No test needed 127.0.0.2 itself, so none skips. The web tests are pinned in
  `make web-test` (`LC_ALL=en_US.UTF-8 TZ=UTC`): Node reads its default locale from the
  environment at startup, so it cannot be set from inside a test file. Running
  `node --test` by hand on a non-US locale can still fail `listFormatsForReading`.
- D10 (2026-11-21) — B1: roots saved before this fix may be unresolved; they are read
  under their resolved spelling, so the saved snapshot still applies and no new baseline
  is taken. The capture scope is used for `internal/effects`, as in 014 and 015.
- D11 (2026-11-21) — B7: once, in the first full `go test ./cmd/gateway` after the B14
  change, `TestBinary_SignalShutdown` timed out with "no startup line" and an empty
  stderr. It passed alone and in 3 more full runs. The likely cause is `freeAddr`'s short
  window, where a freed port is taken by another test before the gateway binds it. The
  failure message now includes stdout, where a "cannot bind" line would show, so a repeat
  names its cause.
- D12 (2026-11-21) — B7 follow-up (Karthik: if the flake is the free-port race, bind port
  0 or keep the listener open). Neither is possible without changing a rule: the config
  refuses two listeners on the same host and port, so `127.0.0.1:0` four times is refused,
  and exempting port 0 would loosen config validation (AGENTS.md); the gateway binds its
  own addresses and takes no inherited listener. Instead `freeAddr` picks free ports from
  20000–32767, below the ephemeral range on Linux (32768–60999) and macOS (49152–65535),
  so no port-0 listener of another test can be given that port in the window. Only a
  process that binds that exact port on purpose could still take it.
  `TestBinary_SignalShutdown` under `-race`, 50 runs: ok.
- D13 (2026-11-21) — Karthik accepted the restart gap: an upgrade restarts the gateway, and
  its ports refuse connections for a sub-second gap (about 80 ms and 53 ms in the real
  check), which Claude Code retries. Zero-gap upgrades (socket hand-over) stay out of
  scope. `docs/install.md`, Upgrade, says so.
- D14 (2026-11-21) — With two routed sessions active (this build's own session and the
  check's prompt), the prompt's connections were stored as unassigned (014 AC10). That is
  the rule working, not a 024 bug. Karthik put per-session attribution on the roadmap's
  Later list; `docs/install.md`, Known limits, names it.

## Real check (Linux, 2026-11-21, build 7cd17e4)

Ubuntu, Node v25.2.1, Claude Code 2.1.330. The sandbox is off on this machine (AppArmor
restricts unprivileged user namespaces, no bwrap profile), so Node was checked through
`HTTPS_PROXY`. `/healthz` was polled every 200 ms throughout: 1,275 polls, 1 refused.

| Step | Expected | Seen | Match |
|---|---|---|---|
| Smoke test of the build on spare ports | starts, answers, stops | `/healthz` 200, clean stop, exit 0 | yes |
| Reinstall 1 over the running gateway (45a4309 → 7cd17e4) | `/healthz` answers throughout | installed; 12 of 13 polls 200, 1 refused: systemd restart, port closed ~80 ms (shutting down 17.799, started 17.880) | accepted (D13) |
| Reinstall 2 | the same | 11 of 11 polls 200; restart gap ~53 ms, no poll in it | yes |
| `gateway doctor` | green | all required pass; warn: AppArmor/bwrap, hand-written function in `~/.bashrc`, UI warming up 9 s after restart | yes |
| Settings | `NODE_USE_ENV_PROXY=1` | in the dry-run diff and the file; summary prints the Node note | yes |
| Claude Code prompt (`af3241b5`) | Node fetch and curl run | `node fetch status 200` (example.org), `curl 200` (example.com), `{"status":"ok"}` from the gateway's `/healthz` through `-x http://127.0.0.1:7198` | yes |
| Both as network effects in the UI | rows for both | `example.org:443`, `example.com:443` in `/api/effects/unassigned` (ids 5673, 5674), unassigned as two sessions were active | yes, unassigned (D14) |
| No rows for the gateway's own ports | none | 0 rows on 127.0.0.1:7195/7197/7198/7199, although step (3) went through the proxy to 7197 | yes |

Only the Mac re-test can confirm: the launchd reinstall against real `launchctl`
(bootout wait, EIO retries, kickstart-only path); Node's fetch under Seatbelt; hook traffic
through the sandbox proxy not recorded; doctor's launchd states; and the macOS-only test
paths (`/var` symlink, a second listener, EADDRNOTAVAIL, en_IN locale).

## Wrap-up (2026-11-21)

All of AC1–AC10 are built, each with a test that failed before its fix, and
`make verify` is green. The Linux real check passed with the restart gap accepted (D13) and
the connections unassigned by 014 AC10 (D14). B8 and B12, and per-session network
attribution, are on the roadmap's Later list. No open questions.
