# 014: file and network effects (T012)

Covers what the automated tests cannot see for `specs/014-file-and-network-effects`: AC10
(connections linked to the one active session, else unassigned), AC14 (one folded row per
turn, filled live and batched, no reload, no timeline rebuild), AC15 (counts on the sessions
list, the unassigned page, the coverage line), and the research items V1–V7 that only a real
Claude Code session can answer. Run steps 1–8 with the `ui-check` skill; steps 9–11 are by
hand.

## Setup

1. From the repo root, build and start the gateway **natively** (not Docker) on a fresh data
   folder, with effects on (the default):

   ```sh
   make build
   XDG_DATA_HOME=/tmp/fx014-data bin/gateway       # in the background; leave it running
   bin/gateway hooks print > /tmp/fx014-settings.json
   ```

   Check the log has `proxy listener started` for `127.0.0.1:7198` and `127.0.0.1:7195`.
   For steps 9–11, also save the settings file and install the `claude` function as in
   [claude-code.md §6](../clients/claude-code.md#6-files-and-network-014).

2. Seed one session (needs `git` and node 22.12+):

   ```sh
   node docs/manual-checks/seed-effects.mjs
   ```

   It makes a scratch git repo under `~/llmgw-seed-effects/<time>` (the gateway never watches
   a temp folder, so not `/tmp`), prints the session id and a link, and takes about 10 s.
   Every proxy line it prints must say `200` or `ok`, except `CONNECT to a closed port`,
   which must say `refused (HTTP/1.1 502 Bad Gateway)`. Open the link **before** it reaches
   turn 1 to watch step 4 live (`--pause 5000` gives more time). It ends every session it
   starts, so none stays active. Remove the scratch repo afterwards.

   The counts in the steps below assume a fresh data folder seeded once: each run adds a
   session and one unassigned connection.

## Steps

Status is always a glyph and a word: files `+ created`, `~ modified`, `− deleted`,
`→ read`; connections `✓ ok`, `✕ refused`, `! error`, `… closed early`.

| # | Do | Expected |
|---|----|----------|
| 3 | Open the Sessions page. | The seeded session's **Effects** column reads `504 · 2 · 2`; hovering it names the counts (changed files, files read, sites). Under the table, a quiet `1 unassigned connection` link; it opens `#/effects/unassigned`, which lists one `✓ ok` connection to `127.0.0.1` with `GET http://127.0.0.1:<port>/unassigned` and no `?token=`. |
| 4 | Open the seeded session while the seed runs (or reload after). | One folded row per turn, `▸ N changed · N read · N sites`: turn 1 `2 changed · 2 read · 0 sites`, turn 2 `1 changed · 0 read · 2 sites`, turn 3 `501 changed · 0 read · 0 sites`. Watching live, each row appears or grows without a reload and the turns above do not redraw. Expanding turn 3 lists 500 `+ created` fixtures and `− deleted README.md`; scrolling and collapsing stay responsive. |
| 5 | Turn 1: expand, then open `content` on `→ read …/src/app.js lines 1–3`. | The section shows what the Read tool returned: the three lines of `greet` with `hello`. The `→ read` row for the Grep shows the repo folder and the pattern `greet`. |
| 6 | Turn 2: look at the connections. | Four rows, each `host:port`, `↑`/`↓` bytes, a duration and `via`: `✓ ok` `127.0.0.1:<port>` via `http` with `GET http://127.0.0.1:<port>/docs/page` (no `?token=`); `✓ ok` via `connect`; `✓ ok` `localhost:<port>` via `socks`; `✕ refused` via `connect` to a closed port with 0 bytes. |
| 7 | Turn 1: open `diff` on `~ modified …/src/app.js`. Turn 2: open `content` on `+ created …/.env`. | The diff is against the committed version: `-  return \`hello ${name}\`;` then `+  return \`hi ${name}\`;`. The `.env` row is listed although `.gitignore` names it, and shows `API_TOKEN=seed-not-a-real-secret`. |
| 8 | Look at every effects list (each expanded turn and the unassigned page). | Each starts with the coverage line ("Files: changes seen at the agent's hook checkpoints, …"). No status is shown by colour alone. |
| 9 | Linux, real Claude Code through the `claude` function, in a scratch git repo under your home folder: ask it to edit a file; to run a script that writes three files; to run `curl https://example.com`; `git ls-remote https://github.com/brutally-Honest/llm-gateway`; and a Node script that calls `fetch("https://example.com")`. Use the default settings (sandbox off). Then run two sessions at once and make a connection from one. A sandbox-on run is optional and is done on the Mac (step 11). | The edit and the three files show in their turns with diffs or content. `example.com:443` and `github.com:443` show for `curl` and `git`; Node's built-in `fetch` may not (KI8). With two sessions active, the connection is under "unassigned connections". Record V1–V7 (below). |
| 10 | Stop the gateway (`systemctl --user stop llm-gateway`, or Ctrl-C), then run `claude`. | `claude` prints `llm-gateway is not running: starting plain Claude Code` and works normally. Start the gateway again. |
| 11 | Steps 9 and 10 on the work Mac (launchd). Optionally, set `effects.network.sandbox: true`, reprint the settings file, restart and repeat the `curl` and the Node script. | The same. With the sandbox on, `example.com:443` shows for `curl` and Node too. |

## What to record

The gateway sha (`git rev-parse --short HEAD`), the OS and Claude Code version, one line per
step saying what was seen, and answers to the research items, each as a dated line under
`## Notes` in `specs/014-file-and-network-effects/research.md`:

- V1 — `claude --settings <file>` applies that file's `env` block (and `sandbox` block, when
  the optional sandbox is on), not only its hooks (the session's model calls reach the
  gateway; Bash traffic reaches the proxy).
- V2 — values from the settings `env` block reach Bash child processes (`env | grep -i proxy`).
- V3 — with the optional sandbox on (step 11), Bash traffic arrives at 7198/7195; with it off,
  the sandbox ports do nothing.
- V4 — with the optional sandbox on (step 11), whether Claude Code still asks about new
  domains when a custom proxy is set.
- V5 — the time of `git status` per checkpoint on a real repo, and how far checkpoints lag in a
  busy turn (the log's `effects_checkpoints` against the number of tool calls).
- V6 — the PostToolUse payload of Read carries the file content in `tool_response` (step 5 on
  a real session shows content, not JSON).
- V7 — hook `cwd` stays the starting folder after Claude runs `cd` in Bash.

Never paste a key, token or the content of a real `.env`. If a step fails, do not work
around it: record it as an open question in the feature's `research.md`.
