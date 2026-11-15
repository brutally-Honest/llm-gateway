# 017: sessions list and call details

Covers what the automated tests cannot see for `specs/017-sessions-list-and-call-details`:
the pages in a real browser. §1 is the sessions list (slice 1: AC1–AC4, E1, E5, E6); §2
(call details, slice 2) and §3 (other pages, slice 3) are added by their slices. The
automated parts are `internal/ui/api_sessions_test.go`, `api_today_test.go`,
`api_effects_test.go`, `internal/timeline/view_test.go` and
`internal/ui/webtest/sessions-page.test.js`. Run each section with the `ui-check` skill
(`/ui-check 017-sessions-and-calls`); record expected against seen in the spec's
research.md. §2's automated parts are `internal/timeline/view_test.go` (TestView_Call*,
TestView_SideCallThread, TestView_FirstCallOfThread), `internal/ui/api_view_test.go`
(TestCallDetails_*) and `internal/ui/webtest/call-dialog.test.js`.

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
    status once it has ended (`● active` while its last hook is under 5 minutes old, D33), `⚑ hooks` as its source, Work "1 prompt · no model calls captured" and Tokens
    "— in · — out · — cache read"; its title is its first prompt, "hooks only" (016's title
    rule), never `fx017-hooks-only`. It has no needs-a-look line: a session that is hooks
    only has no hooks-only turn to single out (research D31).

1.3 **Needs a look (AC2).** Under the seed session's title, a line in the warning colour
    says how many items need a look and of what kinds, in words (for example "6 need a
    look: 1 failed tool call, 1 failed model call, 1 change with no tool running, …");
    hovering it shows the whole line when it is cut. The calls-only and hooks-only
    sessions' rows have no such line.

1.4 **Unassigned connections (AC2).** Under the list, an "Unassigned connections (N)"
    section lists the seed's unassigned connection with its label (unassigned, or harness),
    client (`unknown`, or the client a harness label names), host, bytes each way and time.
    With more than 10, the section ends with "All N unassigned connections →".

1.5 **Today (AC3).** The "Today" line (hover: "since local midnight") shows labelled totals:
    sessions, model calls (hover: side calls included), files changed, sites, input and
    output tokens. They cover what ran since local midnight; sessions counts every session
    with a model call or a hook since then, hooks-only sessions included (research D30).

1.6 **Empty store (E1).** Before seeding: the list first shows "Loading sessions…", then
    "No sessions captured yet."; the Today line reads "0 sessions 0 model calls 0 files
    changed 0 sites — input — output"; there is no unassigned connections section.

1.7 **Filters (AC4).** Type part of the seed session's title: only matching rows stay; Esc
    clears it. Click each of All, Needs a look, Active, Hooks only in turn: each is marked
    pressed and shows only its rows — Needs a look: the seed session, not the hooks-only
    ones; Active: sessions
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
    once, in the list (the note still says "filtering 50 loaded", D34), and Load more is
    still there. Load more adds the next rows.

## §2 Call details dialog (slice 2)

Setup: the same throwaway gateway as §1 (a fresh data folder is fine). Seed the §1 session
that needs a look (`seed-session.mjs`, as in Setup step 3), then the call kinds:

```sh
# a plain call, a streamed call, a call with a very long request and reply, an errored call
node docs/manual-checks/seed-call-kinds.mjs --gateway http://127.0.0.1:17297 \
  --ui http://127.0.0.1:17299 --stub-port 17296
```

Check every step in the light theme, then in dark.

2.1 **Open, URL, reload, Back (AC5).** Open the seed session. Click a step's `#n`: the call
    details open as a dialog over the session page (the page stays behind it), and the URL
    becomes `#/sessions/{id}/call/{exchange id}`. Reload: the same call opens again. Back:
    the dialog closes and the URL is the session's again, with focus back where it was.
    Open the same call from a Context tab row, from a tool dialog's "Asked by" link and by
    pasting the call URL into a new tab: each opens this dialog. A Context bar still only
    scrolls the list to its turn's calls (AC20).

2.2 **Facts (AC6).** The dialog starts with labelled facts: model, duration, start, stop
    (the provider's stop reason in words, such as "end turn" or "tool use", or the error
    status), thread ("main", "sub-agent Explore" or the side call's kind) and context size.

2.3 **Side call (E2).** Open the seed's quota probe (the side calls control on the session
    page): its thread is "quota probe", its "429 Too Many Requests" is a word, not red.

2.4 **Context (AC7).** The Context section shows the size, the change since the previous
    call of its thread ("+1,234 since #3", or "first call of its thread"), the reason in
    words (016's Context rows), and input, cache write and cache read. A sub-agent's call
    compares with the sub-agent's own previous call.

2.5 **What it saw, thinking, response, tools (AC8).** "What the model saw" shows the counts
    ("12 carried over · 2 new"; a thread's first call says so) and the messages, each marked
    carried or new. A call that thought shows "Thinking" behind a toggle. The response shows
    as Markdown with a Raw toggle. Each tool call the call asked for opens its 016 tool
    dialog.

2.6 **Health and raw (AC9).** Health says in words what went wrong, or "No disconnect, abort
    or error." Raw links the request body, the response stream, the headers and the full
    exchange page; each link opens.

2.7 **Step through calls (AC10, E4).** With the dialog open, ↓ and ↑ (and Next and
    Previous) move to the next and previous call of the session, side calls included; the
    URL follows without adding history (Back still closes the dialog). On the first call
    Previous is disabled; on the last, Next.

2.8 **Live updates (AC10).** Run `seed-call-kinds.mjs --live` (or `seed-session.mjs --live`)
    with a call open: new calls arrive in the session behind the dialog, and the dialog
    stays on its call, its scroll and focus kept; Next becomes enabled once a later call
    exists.

2.9 **Missing data (AC11).** The errored call: facts say the error status in words (red),
    Context shows "—" for every count with why ("No usage reported: …"), the response says
    why it is missing, Health says what failed, and the raw links still open. The very long
    call: its messages and reply are cut, each saying so, with the raw links for the whole
    body; the reply is capped like a message box, and "Show all" expands it in place ("Show
    less" caps it again; research D41). The streamed call: its reply is the streamed text
    assembled.

2.10 **A URL naming no call (E3).** Edit the call id in the URL to one that does not exist:
    the session opens with a "No such call in this session." note and no dialog.

2.11 **Modal (AC21).** While the dialog is open the page behind is dimmed and nothing behind
    it can be clicked or reached with Tab; a click on the dimmed page closes it; the top
    (title, the facts, the context line and the message counts) stays while the messages
    and later sections scroll.
