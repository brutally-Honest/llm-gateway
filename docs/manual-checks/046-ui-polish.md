# 046: UI polish

Covers [specs/046-ui-polish/spec.md](../../specs/046-ui-polish/spec.md): turn titles,
the token cell, row heights, the Last active column, the unreachable-gateway and
reconnecting messages, the empty filter, the missing session, and multi-line tool input.
In a headless Chrome (throwaway profile, DevTools on 127.0.0.1:9333; the Chrome extension
was not connected), Lean and Stark, light and dark, at 1,440 and 2,560 px.
Results are also recorded in that spec's `research.md`.

## Setup

1. Never point a check at the running gateway (`llm-gateway.service`, ports 7195–7199) or
   at the real HOME. Never run a gateway, Chrome and a test run at the same time.

2. Copy the store. `sqlite3 <store>/gateway.db ".backup <copy>/gateway.db"`, then
   `cp -a <store>/blobs <copy>/`. For a run without a store, seed fictional sessions with
   `docs/manual-checks/seed-session.mjs` instead.

3. Write a throwaway config: `listen_addr 127.0.0.1:17197`, `ui.listen_addr 127.0.0.1:17199`,
   `effects.network` proxy listeners on 17198 and 17195, effects off, and `capture.dir` set to
   the copy. Run the gateway inside `systemd-run --user --scope -p MemoryMax=4G`.

4. For an empty view cache, build the binary with a different `-X main.version`. The disk
   cache is keyed by the binary's hash, so the new build starts empty (check 3 needs this).

5. When done, stop the gateway and the browser, and delete the copy.

## Checks

Each check gives what to do, the expected result, then the result seen. Results are in
"Result 2026-11-26".

### 1. Titles (U1, AC1)

Open the list, the session page heading and the turn rail of the session with the most
turns, in all four style and theme combinations at both widths.

Expected: no title shows pasted-content tags, command tags, "Tool loaded.", a skill body or
another session's message. Synthetic turns read "(system) <kind>" (research D1). The story's
Prompt block still shows the whole prompt.

Seen 2026-11-26: 247 turns in 70 sessions were classified first (research Findings). On the
largest session (28 turns), 12 rail rows read "(system) …" (kinds: tool loaded, skill,
compaction request, continued after compaction, usage limit reset). 0 raw wrappers in the
rail, list or heading, in all 8 combinations. The story's Prompt blocks still hold the full
text. Not changed: the rail's "N prompts" count still includes synthetic turns (turn
boundaries are out of scope, research D3).

### 2. Token cell (U2, AC2)

Open the list at both widths and read every token cell and its bar.

Expected: input · out · cache write · cache read, a cost line, and a bar of four segments.
Nothing is cut.

Seen 2026-11-26: four figures and four bar segments in all combinations. At 2,560 px the
first build cut the cell (fixed 231 px column). Fixed by wrapping Tokens at the dots at every
width (research D13). After the fix: 0 cut cells and no horizontal scroll at either width.
Rows are taller with four figures (research D14).

### 3. Pending rows fill without shrinking (U4, AC4)

Start a gateway with an empty view cache (setup 4). Load the list at once and sample each
row's height every 50 ms for 20 s.

Expected: no row shrinks when its details arrive.

Seen 2026-11-26: 46 rows went pending, then filled, at each width. 0 shrank. Growth was up
to 66 px at 1,440 px and up to 47 px at 2,560 px.

### 4. Last active column (U6, AC6)

Read the list's first column header and hover a time in each row.

Expected: the header reads "Last active", with a tooltip naming the sort. Each time's
tooltip reads "Started <time>".

Seen 2026-11-26: as expected in all 8 combinations. The header stays one line.

### 5. Stop the gateway mid-request (U3, AC3) and dropped live connection (U9, AC9)

On the list, click Next and stop the gateway at the same moment. Then restart it and click
Retry.

Expected: the list says "The gateway is not answering." with Retry, never the browser's
text. The header shows the amber dot and "reconnecting…". After a restart the dot is green,
the note is gone, and Retry loads the page.

Seen 2026-11-26: as expected in Lean light at 1,440 px and Stark dark at 2,560 px. No
"Failed to fetch" or "Failed to execute" text appeared. Retry loaded page 2 of 2.

### 6. Empty filter (U8, AC8)

Open `#/?kind=active&q=zzz-no-match`. Then click Clear.

Expected: the list says `No sessions match the filter "Active" and the search "zzz-no-match".`
with a Clear button. Clear empties the search box and returns to `#/`.

Seen 2026-11-26: as expected in 4 combinations (Lean light 1,440; Stark dark 1,440; Lean dark
2,560; Stark light 2,560).

### 7. Missing session (U7, AC7)

Open `#/sessions/does-not-exist`.

Expected: "Session not found." with a link "Back to the sessions list" that goes to `#/`.
The rail says "No turns." No loader is left spinning.

Seen 2026-11-26: as expected in the same 4 combinations.

### 8. Multi-line tool input (U10, AC10)

Open a Bash call whose command holds a heredoc, for example a fictional
`python3 - <<'EOF'` … `EOF` script. Read the Input block, then click Copy.

Expected: the Input block shows the command on real lines. Text the command itself escapes
stays escaped. Copy copies the exact JSON.

Seen 2026-11-26: 23 lines, with `EOF` on its own line, in the same 4 combinations. The "Hook
input" block below it still shows the hook's raw text with escaped line breaks. It is a
separate section and is left as received (research D15).

### 9. Installer message (U5, AC5)

Covered by `TestSummaryUILineHasNoWarmupClaim` and the doctor tests. Not run against a real
HOME.

## Result 2026-11-26

All checks pass after the Tokens fix (fac8476). One regression was found during the check, in
the four-figure token cell at 2,560 px, and fixed in the same step (research D13).
