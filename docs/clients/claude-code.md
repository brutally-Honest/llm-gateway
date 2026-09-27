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
