# 010: hooks in the web UI (T012)

Run with the `ui-check` skill. It checks that tool-call states, agent labels and the
`hooks only` session label from `specs/010-harness-hooks` show up in the web UI, live and
after a reload. Every `claude` run below starts from `/tmp/uicheck`, the scratch repo.

## Setup

From the repo root:

```sh
make run                                              # in the background
go run ./cmd/gateway hooks print > /tmp/gw-hooks.json
git init /tmp/uicheck
SESSION=$(uuidgen)
```

Open <http://127.0.0.1:7199>. Open the session once step 1 has created it, and do not
reload until step 7.

## Traffic

All runs in steps 1 to 6 share one Claude Code session. The first prompt:

```sh
ANTHROPIC_BASE_URL=http://127.0.0.1:7197/anthropic claude -p \
  --settings /tmp/gw-hooks.json --permission-mode default \
  --session-id "$SESSION" "<prompt>"
```

Every later prompt replaces `--session-id "$SESSION"` with `--resume "$SESSION"`.

If `claude` refuses to start inside another Claude Code session, prefix the command with
`env -u CLAUDECODE`.

## Steps

A call's badge reads `state: reason: outcome`, leaving out what is empty, so succeeded
shows as `asked+ran+reported: succeeded`.

| # | Do | Expected |
|---|----|----------|
| 1 | Prompt: `Run ls.` | The call shows succeeded. |
| 2 | Prompt: `Run the command false.` | The call shows failed. |
| 3 | Prompt: `Run python3 -c 'import time; time.sleep(20)' in the foreground.` Watch while it runs. | Live, with no reload: pending, then ran-not-reported, then asked+ran+reported. |
| 4 | Prompt: `Create a file d.txt containing hello.` | The write is denied, because no one can approve it in `-p`. The call shows pending. |
| 5 | Prompt: `ok, skip it` | That write turns asked-not-run. |
| 6 | Prompt: `Use a sub-agent to list the files here.` | The sub-agent's calls carry its agent id and type. Its SubagentHandback shows ran-not-reported, which is correct. |
| 7 | Reload the page. | Identical to the live view. |
| 8 | Run `go run ./cmd/gateway timeline -last` from the repo root. | The same states as the UI. |
| 9 | Run a prompt such as `Run ls.` without `ANTHROPIC_BASE_URL`, keeping `--settings /tmp/gw-hooks.json`, in a new session. | The sessions list labels a new session `hooks only`, and its entries show ran-not-asked-or-not-captured. |

## Known noise

A red 429 "quota" exchange at each session start comes from Claude Code itself. It is not
a failure of this check.

## Cleanup

Stop the gateway, then remove `/tmp/uicheck` and `/tmp/gw-hooks.json`.
