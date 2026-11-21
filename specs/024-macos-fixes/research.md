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
