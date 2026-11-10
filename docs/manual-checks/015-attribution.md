# 015: effect attribution (T010)

Covers what the automated tests cannot see for `specs/015-effect-attribution`: AC12 (each
effects row shows its label as glyph + word and its tool as `#n Name`, with no other layout
change) and AC3–AC9 end to end through the real hook ingest, checkpoint runner and proxy.
Run steps 1–6 with the `ui-check` skill; step 7 is by hand.

## Setup

1. From the repo root, build and start a **throwaway** gateway natively on a fresh data
   folder and its own ports, so the real store is untouched (`<cfg>` is your config file):

   ```sh
   make build
   GATEWAY_LISTEN_ADDR=127.0.0.1:17197 GATEWAY_UI_LISTEN_ADDR=127.0.0.1:17199 \
   GATEWAY_CAPTURE_DIR=/tmp/fx015-data \
   GATEWAY_EFFECTS_NETWORK_HTTP_PROXY_ADDR=127.0.0.1:17198 \
   GATEWAY_EFFECTS_NETWORK_SOCKS_PROXY_ADDR=127.0.0.1:17195 \
   bin/gateway -config <cfg>                     # in the background; leave it running
   ```

2. Seed one session (needs `git`, node 22.12+ and the internet for the harness step):

   ```sh
   node docs/manual-checks/seed-effects.mjs --gateway http://127.0.0.1:17197 \
     --ui http://127.0.0.1:17199 --http-proxy 127.0.0.1:17198 --socks-proxy 127.0.0.1:17195
   ```

   It prints the session link and, for turn 4, the label each effect must get. Remove the
   scratch repo under `~/llmgw-seed-effects/` afterwards.

## Steps

3. Open the printed link, expand turn 4's effects row. Each row ends with
   `· <glyph> <word>: <tool>`, and the labels are exactly the ones the seed printed:
   - `src/app.js` `● named: Edit`; the `127.0.0.1` connection with `GET …/named` `● named: Bash`
   - `out/gen.txt` `◐ only one running: Bash`
   - `artifacts/out.bin` `◌ several running: Bash, Bash`
   - `notes/todo.md` `● named: Bash`; the `src` read `● named: Grep`; the Glob read `● named: Glob`
   - `agent/report.txt` `◌ several running: Bash (agent), Glob`
   - `downloads.claude.ai` `⚙ harness`
4. The `GET …/outside` connection (last in turn 4, made after its Stop) and
   `notes/manual.txt` (in turn 5, `Done.`: a file change is placed at the checkpoint that
   found it, which the prompt's hook ran) both read `○ outside any tool call`.
5. The seed has no captured model calls, so tools show by name with no `#n` (E11). Every label
   has a glyph and a word; none is colour only.
6. Layout is 014's: the folded row, the coverage line, the file and connection lists, the
   diff and content links all work as before; the attribution is one more text cell, nothing
   moved. `#/effects/unassigned` lists the seed's unassigned connection with no label.

## By hand

7. A real Claude Code session through the `claude` function, on the normal gateway: ask for an
   edit, a Bash script that writes two files, `curl https://example.com`, a WebFetch, two
   parallel reads and a sub-agent that runs Bash. The Edit's file reads `named: #n Edit` with
   the timeline's number for the model call that asked for it; the script's files read
   `only one running` or `named` (if the command names them); `example.com` reads
   `named: #n Bash`; the WebFetch host reads `named: #n WebFetch`; Claude Code's own hosts
   read `harness`. Record anything unexpected in `specs/015-effect-attribution/research.md`.
