# The web UI

The UI shows what the gateway has captured: your sessions, their turns, tool calls, file
and network effects, tokens and cost. It is read-only. This page describes it as it is on
this branch. For how it is built, see [architecture.md](architecture.md).

<!-- screenshots: added in the release PR from seeded fictional data -->

## Open it

Open <http://127.0.0.1:7199> in a browser on the same machine. That is the default
`ui.listen_addr`. The address is loopback only, and the gateway refuses to start if you set
it to anything else. The UI accepts only GET requests, so it cannot change what was
captured. It needs capture on (`capture.enabled`) and `ui.enabled` (on by default).

It serves as soon as the gateway starts. Right after an install or an upgrade the view
cache is empty, so details fill in over a few seconds
([architecture.md](architecture.md#the-view-cache)).

## The header

- The logo links to the Sessions page.
- A trail of links shows where you are.
- A small dot shows the live connection (see [Live updates](#live-updates)).
- The gear button opens **Settings** (see [Themes](#themes)).
- The `?` key opens **Keyboard shortcuts** for the page you are on.
- A red banner appears when the gateway reports a capture problem (see
  [Capture-health banner](#capture-health-banner)).

## Sessions page

The page title is "Sessions" with the word "today" beside it. Tiles under the title are the
day's totals since local midnight: sessions, model calls, files changed, sites, input,
output and API-equivalent cost. Two tiles carry a small chart of the hours.

### The list

One row per session, newest activity first. The columns are:

| Column | What it shows |
|---|---|
| (status icon) | `●` active, `✗` failed, `◇` hooks only. An ordinary ended session shows none. |
| **Last active** | When the session last did something. Hover for its start. |
| **Session** | The title (the first real prompt), the folder and model, and, when there are any, the kinds of thing that need a look. |
| **Work** | Prompts, calls and tool calls. |
| **Changes** | Files changed, commands and sites. |
| **Time** | How long the session took. |
| **Tokens** | Input, output, cache write and cache read, with a bar, and the API-equivalent cost on a second line. |

The table header stays in view while the list scrolls. Click a row to open the session. The
keys are the up and down arrows to select, Enter to open, and `/` to jump to the filter.

**Paging.** The server pages the list, 50 rows at a time. The pager under the table has
**‹ Previous** and **Next ›**, and reads "Page N of M". The page, the filter and the search
are in the URL, so a link or a reload comes back to the same view.

**Rows appear first, details fill in after.** The list shows each session at once with its
time, folder and status. The title, work, changes, time and tokens show as grey bars, and
the title reads "Details loading", until they are built. Then they fill in without the rows
changing height. If a filter or search is on, a line under the pager says how many sessions
are "still being checked". If the details do not arrive, it says "Details are not available
yet" and offers **Retry**.

### Filter and search

The segmented control **Show** has four choices:

- **All**
- **Needs a look**: sessions with at least one item on their "Needs a look" list.
- **Active**: sessions that are running now.
- **Hooks only**: sessions known only from hook events, with no model calls captured.

The box **Filter sessions** searches the client, session key, id, folder, title and model
names. It is case-insensitive and keeps the first 200 bytes you type. Esc clears it. The
server does the filtering and searching.

If nothing matches, the list says `No sessions match the filter "Needs a look" and the
search "..."` (it names whichever applies) and shows a **Clear** button that resets both.
With no sessions at all it says "No sessions captured yet."

### Below the list

- **Ungrouped.** A link, such as "3 ungrouped exchanges", opens calls that carry no session
  id. See [what-gets-recorded.md](what-gets-recorded.md#what-is-recorded).
- **Unassigned connections (N).** Network connections through the gateway's proxy that could
  not be linked to one session. The section lists the first 10 with label, client, host,
  bytes each way and time. A link, "All N unassigned connections", opens the full page. The
  linking rule is in [what-gets-recorded.md](what-gets-recorded.md#several-sessions-at-once).

## Session page

A session page has three parts: the turns on the left, an overview on top, and tabs below.
For a session that does not exist, the page says **Session not found** with a link back to
the sessions list.

### Turns

The **Turns** rail on the left lists each prompt as a turn, with its number, a title, its
duration, how many files it changed and an end marker. A turn started by a background task
is labelled "background task". Press `j` and `k` to step through the turns.

**Turn titles.** The title is the prompt with the wrappers Claude Code adds taken off, and
whitespace collapsed. The full prompt is still in the story. When the prompt is text that
Claude Code added by itself, the title starts with "(system)" and says what it is. The
rule is `PromptTitle` in `internal/clients/claudecode/rules.go`. The titles are:

- `(system) background task` (or the task's own summary line, when it has one)
- `(system) tool loaded`
- `(system) message from another session`
- `(system) skill NAME`, or `(system) skill`
- `(system) compaction request`
- `(system) continued after compaction`
- `(system) web page content`
- `(system) CLAUDE.md context`
- `(system) usage limit reset`
- `(system) slash command` and `(system) local command`, for a command wrapper with no
  readable name. A typed slash command with a name is a real prompt and its title is
  `/name args`.

When real text follows such a block, the real text becomes the title. The session's own title
skips these and takes the first real prompt. A turn titled this way is still a turn.

### Overview

At the top of the page:

- The status ("active" shows a pill), the time span, the folder, the models and the client.
- **Time**, split into model, tools, waiting and between turns.
- **Context**, the tokens in the last call. Click it to open the Context tab.
- **Needs a look**: items worth a second look, each a button that takes you to the place. Up
  to 8 are shown, then "and N more". The kinds are listed in
  [what-gets-recorded.md](what-gets-recorded.md#what-is-recorded).
- **API-equivalent cost**. This is what the tokens would cost at the provider's published
  prices, not what you were charged. A partial total reads "≥ $N" and its hover says what was
  left out. A model with no price is left out, and the cost is never an error.
- A **side calls** disclosure: the client's own calls, never a turn and never an error.

### Tabs

**Story**, **Files**, **Commands**, **Sites** and **Context**.

- **Story** is the turns as chapters: the prompt, the model's steps with their tool calls,
  and the answer. Each tool call shows a chip: `✓ ok`, `✗ failed`, `… pending`, `⊘ not run`,
  `? unmatched` or `– no hooks`
  ([claude-code-hooks.md](clients/claude-code-hooks.md#4-match-states)). File changes show
  `Diff` or `View`. Prompts and answers can be shown as **Preview** or **Raw**; the choice is
  kept in the browser.
- **Files** has **Changed**, **Read** and **Searched**, and switches between **By file** and
  **By turn**. Changes show added and removed lines and open a diff.
- **Commands** lists the shell commands, with the longest one.
- **Sites** lists hosts that were contacted, with a link to the unassigned connections.
- **Context** shows tokens at the end of each turn, and the token counts: input, output,
  **cache write** and cache read.

Click a call number such as `#3` to open its details: what the model saw, and the response.
The URL changes, so you can share the link.

### Content that is not there

If a stored body is missing or cannot be read, that one block shows `[content unavailable]`.
The call is kept, and the rest of the page works. The cause and the fix are in
[observability.md](observability.md#capture-health).

## Live updates

An active session page updates by itself. New calls, hook events and effects appear without
a reload, and only the turns that changed are redrawn. The sessions list updates the same
way. A dot in the header is green while the connection is up. If it drops, the dot turns
red and the word **reconnecting…** shows beside it. The page then retries by itself, a bit
more slowly each time, up to every 30 seconds. If the live snapshot fails, the dot turns
red and the page shows "Live snapshot failed: …" with the reason. Hovering the dot, or a
screen reader, gives "live: snapshot failed, retrying in 30 s". Ended sessions have no live connection.

## When something fails

- **The gateway is not answering.** If the gateway stops, or a reply is cut off, the page says
  "The gateway is not answering." instead of a browser error. On the sessions list there is a
  **Retry** button. It asks for the same page again.
- **Session page.** While a large session builds, the page says "building session…" and tries
  again once by itself. If the load fails for another reason, the page shows the message and
  "Turns not loaded." in the rail. Reload the page to try again.
- **A call that is not in the session.** The page says "No such call in this session."
- **Details.** A dialog that cannot load shows, for example, "Call details unavailable:"
  and the reason.

## Capture-health banner

When the gateway has lost capture data since it started, a banner shows across the header:
"capture degraded: N lost since start", with a short reason and a **details** link to
`/api/health`. The page checks every 15 seconds. The **×** button hides the banner for that
browser tab only. It comes back when the lost count rises or the gateway restarts. A closed
tab or a new tab shows it again. What the counts mean is in
[observability.md](observability.md#capture-health).

## Themes

Two choices, set in **Settings** (the gear in the header):

- **Theme**: **Light** or **Dark**. Dark is the default. The page ignores the operating
  system's setting.
- **Style**: **Lean** (the default; smooth and round) or **Stark** (square corners and
  monospace capital labels).

That gives four looks: Lean dark, Lean light, Stark dark and Stark light. A choice applies at
once. It is saved in the browser's local storage, under the keys `theme` and `style`, and
applied before the page first paints. It is per browser and per address. If storage is
blocked, the choice still works until you reload.

## Keyboard shortcuts

| Page | Keys |
|---|---|
| Sessions | Up and down arrows select, Enter opens, `/` goes to the filter, Esc leaves the box |
| Session | `j` and `k` step through turns, arrow keys move between tabs, Esc closes a dialog |
| Call details | Up and down arrows move between calls, Esc closes |
| Ungrouped list, exchange page | Backspace goes back to Sessions |
| Any | `?` shows the shortcuts for the page |
