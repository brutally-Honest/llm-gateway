# 017: sessions list and call details

Covers what the automated tests cannot see for `specs/017-sessions-list-and-call-details`:
the pages in a real browser. §1 is the sessions list (slice 1: AC1–AC4, E1, E5, E6); §2
(call details, slice 2) and §3 (other pages, slice 3) are added by their slices. The
automated parts are `internal/ui/api_sessions_test.go`, `api_today_test.go`,
`api_effects_test.go`, `internal/timeline/view_test.go` and
`internal/ui/webtest/sessions-page.test.js`. Run each section with the `ui-check` skill
(`/ui-check 017-sessions-and-calls`); record expected against seen in the spec's
research.md.

## Setup

1. From the repo root, build and start a **throwaway** gateway on a fresh data folder and
   its own ports, forwarding model calls to the seed's stub upstream (`<cfg>` is a copy of
   your config file; nothing reaches a provider):

   ```sh
   make build
   GATEWAY_LISTEN_ADDR=127.0.0.1:17297 GATEWAY_UI_LISTEN_ADDR=127.0.0.1:17299 \
   GATEWAY_CAPTURE_DIR=/tmp/fx017-data \
   GATEWAY_EFFECTS_NETWORK_HTTP_PROXY_ADDR=127.0.0.1:17298 \
   GATEWAY_EFFECTS_NETWORK_SOCKS_PROXY_ADDR=127.0.0.1:17295 \
   GATEWAY_UPSTREAMS_ANTHROPIC_BASE_URL=http://127.0.0.1:17296 \
   bin/gateway -config <cfg>                     # in the background; leave it running
   ```

2. Do step 1.6 (empty store) **now**, before seeding anything.

3. Seed (node 22.12+ and `git`):

   ```sh
   # a session that needs a look (failed Edit, a 500, an edit between turns, a hooks-only
   # turn) and one unassigned connection
   node docs/manual-checks/seed-session.mjs --fast --gateway http://127.0.0.1:17297 \
     --ui http://127.0.0.1:17299 --http-proxy 127.0.0.1:17298 --stub-port 17296
   # a plain session with calls only (no hooks)
   node docs/manual-checks/seed-calls.mjs --calls 12 --gateway http://127.0.0.1:17297 \
     --ui http://127.0.0.1:17299 --stub-port 17296
   # a hooks-only session: one prompt hook, no model call
   curl -s -X POST http://127.0.0.1:17297/_gateway/hooks/claude-code \
     -H 'content-type: application/json' \
     -d '{"hook_event_name":"UserPromptSubmit","session_id":"fx017-hooks-only","cwd":"'"$HOME"'/llmgw-hooks-only","transcript_path":"","prompt":"hooks only"}'
   ```

   Remove the scratch repos under `~/llmgw-seed-session/` when done.

4. Open `http://127.0.0.1:17299/` in a window at least 1,400 px wide. Check every step in
   the light theme, then switch the theme (header toggle) and check them again in dark.

## §1 Sessions list (slice 1)

1.1 **Row counts (AC1).** Each row shows: the start (Today 8:47 PM, Yesterday …, or a
    date); the title — the seed session's generated title, the calls-only session's first
    prompt, never a session key; hovering the title shows "session key …"; under it the
    folder and model (`~/llmgw-seed-session/… · claude-…`, the full folder on hover);
    Sources as glyph and word (`⇄ API`, `⚑ hooks`, or `⇄ API + ⚑ hooks`); Status as glyph
    and word (`● active` or `■ ended`); Work as "N prompts · N calls · M side"; Tools as
    "N tool calls · F failed" with the failures in red; Changes as "N files changed · N
    commands · N sites"; Time as the wall clock from first to last activity (2m 05s); Tokens
    as "3.7k in · 1.2k out · 15.1M cache read", each with its exact value on hover, and
    "— in" where no call reported it. Every count has its word next to it.

1.2 **Hooks-only row (AC1, E5).** The `fx017-hooks-only` row shows `◇ hooks only` as its
    status, `⚑ hooks` as its source, Work "1 prompt · no model calls captured" and Tokens
    "— in · — out · — cache read"; its title is "Hooks only · ~/llmgw-hooks-only", never
    `fx017-hooks-only`.

1.3 **Needs a look (AC2).** Under the seed session's title, a line in the warning colour
    says how many items need a look and of what kinds, in words (for example "6 need a
    look: 1 failed tool call, 1 failed model call, 1 change with no tool running, …"). The
    calls-only session's row has no such line.

1.4 **Unassigned connections (AC2).** Under the list, an "Unassigned connections (N)"
    section lists the seed's unassigned connection with its label (unassigned, or harness),
    client (`unknown`, or the client a harness label names), host, bytes each way and time.
    With more than 10, the section ends with "All N unassigned connections →".

1.5 **Today (AC3).** The "Today" line (hover: "since local midnight") shows labelled totals:
    sessions, model calls (hover: side calls included), files changed, sites, input and
    output tokens. They cover what ran since local midnight.

1.6 **Empty store (E1).** Before seeding: the list first shows "Loading sessions…", then
    "No sessions captured yet."; the Today line reads "0 sessions 0 model calls 0 files
    changed 0 sites — input — output"; there is no unassigned connections section.

1.7 **Filters (AC4).** Type part of the seed session's title: only matching rows stay; Esc
    clears it. Click each of All, Needs a look, Active, Hooks only in turn: each is marked
    pressed and shows only its rows — Needs a look: the seed session; Active: sessions
    whose last activity is under 5 minutes old; Hooks only: the hooks-only sessions.

1.8 **Filtering loaded rows and Load more (AC4, E6).** Make more than 50 sessions so the
    list has a second page:

    ```sh
    for i in $(seq 1 55); do curl -s -o /dev/null -X POST http://127.0.0.1:17297/_gateway/hooks/claude-code \
      -H 'content-type: application/json' \
      -d '{"hook_event_name":"UserPromptSubmit","session_id":"fx017-bulk-'$i'","cwd":"'"$HOME"'/llmgw-bulk","transcript_path":"","prompt":"bulk '$i'"}'; done
    ```

    Reload. Load more shows under the list. With a filter on, a note says "filtering 50
    loaded" and Load more stays; a filter matching nothing ("zzz") says "no sessions match"
    and Load more is still there. Load more adds the next rows.
