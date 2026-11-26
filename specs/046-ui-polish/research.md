---
feature: 046-ui-polish
---
# Research & decisions — 046-ui-polish

## Progress
- 2026-11-26 step 1: branch fix/046-ui-polish from main at c2eab80; store copied; 247 turns in 70 sessions classified (D1); spec and research written, committed edf4b08.
- 2026-11-26 step 2 U1 (sonnet): 7aa2d79, turn and session titles from `PromptTitle`, story keeps the whole prompt; 7 timeline tests, a table test, 1 webtest; make verify green.
- 2026-11-26 step 2 U2 (haiku): token cell and bar show cache write between output and cache read, e365ed5; 4 existing expectations moved from 3 figures to 4; make verify green.
- 2026-11-26 step 2 U3 (haiku): a network failure or unreadable answer reads "The gateway is not answering." (code unreachable), the list keeps Retry, 2e3d87e; 7 api tests, 1 list test; make verify green.
- 2026-11-26 step 2 U4 (haiku, one follow-up): a pending row reserves one line per heavy cell and a place line only with a folder, so rows only grow, e8f98e3; 5 old assertions replaced, 1 test added; make verify green.
- 2026-11-26 step 2 U5 (haiku): installer UI line, four doctor fix texts and the install.md row no longer claim a 30 s warm-up; grace logic kept (D6), d88b490; 1 test added, 2 extended; make verify green.
- 2026-11-26 step 2 U6 (haiku): first column is "Last active" from last_at, start in its tooltip, header tooltip names the sort (D5), a18a6dd; 1 test added, 1 header expectation moved; make verify green.
- 2026-11-26 step 2 U7 (haiku): a 404 view shows "Session not found." with a link to the list; any failed open clears the rail skeleton, 5bc3d16; 2 app tests, fake fetch takes an error body; make verify green.
- 2026-11-26 step 2 U8 (haiku): an empty filtered list names the filter and/or search with a Clear that resets both in one history entry, cf34639; 4 tests, 4 expectations moved to the new text; make verify green.
- 2026-11-26 step 2 U9 (haiku): a visible "reconnecting…" beside the live dot while the stream is down, gone on reconnect; aria-hidden since the dot already speaks, 8da3757; 1 test, 2 app assertions; make verify green.
- 2026-11-26 step 2 U10 (haiku): the tool dialog's Input shows multi-line strings with real line breaks, Copy keeps the JSON (D7, D12), a60bc38; 3 tests; make verify green.
- 2026-11-26 step 3 (partial): empty-cache start, 0 of 46 rows shrank at 1,440 and 2,560 px; at 2,560 px the four-figure Tokens cell was cut (U2 regression), fixed by wrapping Tokens at the dots at every width (D13, haiku), fac8476; make verify green.
- 2026-11-26 step 3: UI check on the store copy passed in all four style/theme combinations at 1,440 and 2,560 px (titles 0 raw wrappers, Last active, error + Retry, reconnecting note, empty filter + Clear, session not found, multi-line input); written as docs/manual-checks/046-ui-polish.md (haiku), a5e6b06.
- 2026-11-26 step 4 round 1 (fresh opus): no P1, 2 P2, 5 P3, 7 P4 (see Review round 1). P2-a (sonnet): list and session titles read the same main-thread prompts (D16), 65e572e; 2 tests failing before the fix; make verify green.
- 2026-11-26 step 4 P2-b (sonnet): typed text after a synthetic part with a known end is the title; open-ended parts left as a recorded gap (D17), 74ba20f; 12 rule cases, 6 failing before; make verify green.
- 2026-11-26 step 4 P3-a (sonnet): only real pasted-content wrappers are stripped from titles (D18), bb63fed; 6 rule cases; make verify green.
- 2026-11-26 step 4 P3-b + P4-4 (sonnet): a local command without its caveat block no longer titles a session as a typed slash command (D19), ecd9aa0; 6 rule cases; make verify green.
- 2026-11-26 step 4 P3-c (sonnet): a failed open says "Turns not loaded." in the rail; "No turns." only for a missing session, 673653c; 1 expectation moved, 1 assertion added; make verify green.
- 2026-11-26 step 4 P3-d (sonnet): compile-time check and an end-to-end test that the real profile titles turns (D20), 6e07596; probed by changing the signature (build fails); make verify green.
- 2026-11-26 step 4 P3-e (sonnet): Clear moves focus to the search box, 8ae2249; 1 assertion, failing before; make verify green. Round 1 closed: P2-a, P2-b (partial, D17), P3-a..e fixed; P4 left as recorded.
- 2026-11-26 step 4 round 2 (fresh opus): no P1, 2 P2, 4 P3, 3 P4 (see Review round 2). P2-1 + P3-4 (sonnet): command blocks parsed as a leading run (D21), 8460aa7; 5 cases, probed failing before; make verify green.
- 2026-11-26 step 4 round 2 P2-2 (sonnet): the digest title is shown on the ungrouped page, so D16 stays (D22); end-to-end test asserts list title == view title, 6784b18; make verify green.
- 2026-11-26 step 4 round 2 P3-1 (sonnet): an image-only prompt no longer titles the session (D23), 812a90d; 3 tests, the main one failing before; make verify green.
- 2026-11-26 step 4 round 2 P3-2 (sonnet): a client block ends at its last closing tag (D24), eacab03; 7 cases, 6 failing before; make verify green.
- 2026-11-26 step 4 round 2 P3-3 (sonnet): a typed line starting with a bare pasted tag keeps its text; empty-title safety net (D25), 466cd2b; 4 cases, 3 failing before; make verify green. Round 2 closed: P2-1, P2-2 (kept, D22), P3-1..4 fixed; P4 left. No third round.
- 2026-11-26 final real-data recheck: 25 subagent-message turns titled by the client footer (D26), fixed by sonnet, 9f3bcea; 2 cases, failing before; make verify green.
- 2026-11-26 finished (this commit): recheck on the final binary: 82 synthetic turns titled "(system) …", 0 raw wrappers, 0 typed prompts titled (system), 0 empty titles, list == session title for all 70 sessions; spec marked done; throwaway gateways and Chrome stopped (nothing on 17195-17199 or 9333); `~/.cache/llm-gateway-046` deleted; `llm-gateway.service` never touched.

## Findings

### Store copy
Taken 2026-11-26 08:15 +05:30 with `sqlite3 … ".backup"` and `cp -a` of the blobs into
`~/.cache/llm-gateway-046` (5.2 GB; 345 GB free). Read through a throwaway gateway on
127.0.0.1:17197/17199 (proxy listeners 17198/17195, effects off), `/api/sessions` and every
`/api/sessions/<id>/view`. Nothing from it is written here beyond counts and descriptions.

### Turn titles today
A turn's title is its prompt (`view_turns.go renderTurn`), the first user text of the call
that starts it, with `<system-reminder>` blocks split off. A main call whose new user message
has any text starts a turn (`view_build.go:346`), so text the client adds by itself, beside a
tool result or as its own message, starts a turn and becomes its title. The rail
(`session-page.js:245`) and the story's Prompt block (`story.js:570`) both show `t.prompt`.
The session title (`view_overview.go title`, `digest.go SessionDigest`) is the latest
generated title, else the first prompt.

### Patterns found (n > 0 turns, 247 in all; 101 real prompts)
| Pattern (fixed opening) | Turns | What it is |
|---|---|---|
| pasted-content tags around the text | 36 (+3 with the tag later in the text) | the user's own pasted text |
| "Another Claude session sent a message:" + agent-message tag | 25 | a subagent's report handed back |
| exactly "Tool loaded." | 25 | the client's note after a deferred tool loads |
| "Base directory for this skill: <path>" + skill body | 16 | a skill's instructions, loaded |
| task-notification tags | 7 (all background) | a finished background task |
| empty text (images only) | 7 | — |
| "CRITICAL: Respond with TEXT ONLY…" | 6 | the client's compaction request |
| local-command-caveat + command name/stdout blocks | 6 | a local command (`/clear`, `/chrome`), sometimes followed by typed text |
| "Web page content:" | 5 | a fetched page handed to the model |
| command-message/command-name/command-args tags | 4 | a slash command, often followed by its skill body |
| "The user sent a new message while you were working:" | 2 | a message typed mid-turn, inside a fixed opening and closing paragraph |
| "This session is being continued from a previous conversation…" | 2 | the summary after compaction |
| "The following is the user's CLAUDE.md configuration." | 1 | context the client adds |
| "Your claude.ai usage limit has reset." | 1 | the client's resume note |

List titles: all 70 come from a generated title or the fallback today (66 generated, 4
"No prompt"/"Hooks only"), so the list shows no wrapper now; the rule still applies there for
sessions without a generated title. The session the human named (8 of 28 "Tool loaded.")
has 7 of 28 in this copy.

### U2-U10 in the code at c2eab80
- U2 `sessions-page.js:162-164` (`rowCells`: input, output, cacheRead), `:311` three token
  spans, `:463-471` three `setToken` calls and the bar of three counts.
- U3 `api.js:6` `fetch` rejects with a TypeError on a network failure; `:18` `res.json()`
  rejects with the browser's parse message on a non-JSON 200; both reach the views as
  `e.message`. `getBody` (`:24`) likewise.
- U4 `style.css:372-374` `--pend-work: 3; --pend-changes: 2; --pend-figures: 2` below
  1,600 px and `1/1/1` above: rows shorter than that common row shrink (043 D22).
- U5 `install.go:657`; `doctor.go:257,261,308`; `docs/install.md:344`. `run.go:365`: the UI
  serves as soon as it binds (030); `ui ready warm_ms 24` on this copy.
- U6 `sessions-page.js:149` `start: s.first_at`, `:480` header "Start"; the store orders by
  `last DESC` (`reader_sessions.go:50`).
- U7 `app.js:654` shows `e.message` in the status line; the rail keeps "Loading turns…"
  (`session-page.js:422`).
- U8 `sessions-page.js:843` "no sessions match".
- U9 `shell.js:213` `setLive` shows only a dot, the word is screen-reader only;
  `app.js:536` "live: disconnected, retrying".
- U10 `dialog.js:272` `prettyJSON` (`entries.js:147`) is `JSON.stringify(…, null, 2)`, which
  escapes line breaks inside strings.

## Decisions
- **D1** 2026-11-26 Title rule per pattern. "Synthetic" means the session title skips it.
  | Pattern | Turn title | Synthetic |
  |---|---|---|
  | pasted-content tags | strip the opening and closing tags, keep the text | no |
  | mid-turn message | the message between the fixed opening line and closing paragraph | no |
  | slash command tags | `/<name> <args>`, the skill body after it dropped | no |
  | local command | the text typed after the command blocks; with none, `/<name>` | only when nothing was typed |
  | task notification | its summary text | yes (already a background turn) |
  | "Tool loaded." (whole text) | `(system) tool loaded` | yes |
  | subagent message | `(system) message from another session` | yes |
  | skill body | `(system) skill <last path element>` | yes |
  | compaction request | `(system) compaction request` | yes |
  | continued after compaction | `(system) continued after compaction` | yes |
  | web page content | `(system) web page content` | yes |
  | CLAUDE.md context | `(system) CLAUDE.md context` | yes |
  | usage limit reset | `(system) usage limit reset` | yes |
  | anything else | the text as today | no |
  Wrappers are unwrapped in turn (a pasted block inside a mid-turn message). Whitespace is
  collapsed and the title cut as session titles are. "(system)" plus a two- or three-word
  kind, not a bare "(system)": the rail of the largest session would otherwise read 23 equal
  rows. Strip, not skip, for the user's own words: skipping a pasted prompt would hide the
  one turn the user really typed.
- **D2** 2026-11-26 Where the rule lives. Thesis: rewrite `Turn.Prompt` in place, one field,
  every view agrees. Antithesis: a separate `Turn.Title`, leaving `Prompt` the full text the
  story shows. Synthesis: a separate `title`; the rule is a title, and a title rule that
  rewrote the prompt would hide text from the one place the whole prompt is shown. The rule
  is client knowledge, so it lives in `claudecode.Profile.PromptTitle` and the timeline asks
  for it through an optional interface; a client without it keeps titles as today. Flips if
  the story's Prompt block is ever dropped in favour of a raw view.
- **D3** 2026-11-26 Turn boundaries stay: a "Tool loaded." message still starts a turn. Merging
  such turns into the one before changes counts, durations and the "N prompts" figure, which
  is outside U1. "Skip to the next real prompt" applies to the session title only.
- **D4** 2026-11-26 A session whose every prompt is synthetic takes the first one's title rather
  than "No prompt · <folder>": it has a prompt, just not a typed one.
- **D5** 2026-11-26 U6: the column says what the list is sorted by and shows it: "Last active"
  with `last_at`, the start in its tooltip. Thesis for keeping Start with a sort label: the
  start is what tells two sessions of one folder apart. Antithesis: a column whose times do
  not run in order reads as a broken sort. Synthesis: show last activity; the start stays one
  hover away and in the session page. Flips if a sort picker is added.
- **D6** 2026-11-26 U5: doctor's fix texts (`doctor.go:257,261,308`) make the same "about 30 s"
  claim, so they change with the installer; doctor's 120 s grace after a start stays (a start
  can still be slow on a busy machine, and the grace only softens a fail to a warn).
- **D7** 2026-11-26 U10: the dialog shows a display form of the JSON in which a string holding a
  line break is written with real lines; Copy still copies `prettyJSON`, the exact JSON, so a
  pasted copy stays valid.
- **D8** 2026-11-26 U3: the words are "The gateway is not answering." with `code: "unreachable"`;
  an HTTP error with a JSON error body keeps its own message as today. Views that already show
  a Retry (the sessions list) keep it; no Retry is added where there is none.
- **D9** 2026-11-26 U4: the 043 comment's "as tall as a common filled row" becomes "as short as
  the shortest filled row": one line per heavy cell at every width, and Tokens' cost line and
  bar only as a filled row has them. A row may still grow (043 D22's +14 px case); it never
  shrinks.
- **D10** 2026-11-26 U1 build details: openings are matched on the prompt as given, so a prompt
  that starts with a pasted tag and then a synthetic opening counts as the user's (pasted)
  text, not synthetic; the text left after a local command is stripped of pasted tags only;
  a slash or local command with no readable name is "(system) slash command" / "(system)
  local command"; the digest skips a user text it has already judged (exchanges repeat the
  history). Without a `PromptTitler` both titles behave exactly as before.
- **D11** 2026-11-26 U4: the U4 build found one more way a row shrank: a pending row with no
  folder reserved a place line (1.3em) that a filled row with no folder and no model hides. A
  pending place line now keeps its line only when it has text; a row that later gains a model
  line grows by it, which the rule allows.
- **D12** 2026-11-26 U10: a multi-line string's continuation lines are indented to its key's
  depth plus two spaces, so a long command stays visibly inside its key; the added spaces are
  display only, and Copy gives the exact JSON without them.
- **D13** 2026-11-26 Step 3 found the four-figure Tokens cell cut at 2,560 px (fixed 231 px
  column from 1,600 px, `nowrap`). Thesis: widen the column so the figures stay on one line,
  as 043 laid out. Antithesis: any width is a guess at the font (Stark's figures are wider than
  Lean's) and takes room from Session. Synthesis: Tokens wraps at the dots at every width, as it
  already did below 1,600 px; no column moves. Flips if a later layout gives Tokens its own
  flexible width.
- **D14** 2026-11-26 With four figures, a filled row's Tokens cell takes three lines at 1,440 px
  and two at 2,560 px, so rows are taller than before U2 (measured growth on fill up to 66 px at
  1,440). Accepted: the rule is that rows never shrink, and a layout redesign is a non-goal.
- **D15** 2026-11-26 Step 3: the tool dialog's "Hook input" block (shown only when the hook saw
  a different input) still shows the hook's raw text with escaped line breaks. Left as is: it
  is shown to compare the raw input the hook received, and U10 names the tool's Input.

## Review round 1
Fresh opus reviewer, `git diff main...HEAD` at a5e6b06. No P1.
- **P2-a** `digest.go:92-115` vs `view_overview.go:157-174`: the list digest scans every user
  text of every non-side exchange (sub-agent prompts included) while the overview reads only
  main-thread turns, so the two titles can differ when synthetic turns come first.
- **P2-b** `rules.go:139-172`: synthetic openings are matched on the joined text of a user
  message, so typed text in a later block of the same message would be hidden. The one
  CLAUDE.md turn, one usage-reset turn and two continuation turns in the store copy were
  checked: none carries text after its fixed part; fixed anyway.
- **P3-a** `rules.go:126`: the pasted-tag regexp strips a lone `<pasted_content` a user typed
  mid-sentence, and with no `>` eats to the end of the line.
- **P3-b** `rules.go:145-148`: a local command without the caveat block is titled as a typed
  slash command. With P4-4: "(system) slash command" is not marked synthetic.
- **P3-c** `session-page.js:410-413`: any failed open writes "No turns." into the rail, which
  the page does not know.
- **P3-d** `view.go:31`: nothing proves `claudecode.Profile` satisfies `PromptTitler`; a
  signature drift would turn U1 off silently.
- **P3-e** `sessions-page.js:757-764`: Clear leaves focus on `<body>`.
- P4 (not fixed unless noted): P4-1 `.tokseg-cw` borrows `--hi`; P4-2 CRLF strings show `\r`;
  P4-3 an empty statusText with no JSON body reads "not answering"; P4-4 with P3-b; P4-5
  doctor Detail still says "warming up"; P4-6 7aa2d79's scope could have been `claude-code`;
  P4-7 the Store copy line names a time zone and free disk space — left, since research
  entries are never deleted, and it holds nothing user-authored.
- **D16** 2026-11-26 P2-a fix: the digest builds its entries before the title and runs the
  existing `CallFactsWith` over them, so it takes, per main call in order, the first new user
  text, exactly the text that starts a view turn. Entries are cut to 1 byte, so the digest
  reads the full input of agent tool calls (small JSON) to tell sub-agent calls apart: one
  extra content read per agent call per list row. Nil rules keep the old path (they cannot
  tell main from sub-agent). A media-only user message starts a view turn but gives the
  digest no text; both then fall to the next prompt.
- **D17** 2026-11-26 P2-b fix, partial. Text blocks of a user message reach the title rule
  joined by a single "\n" (`build.go:468`, `digest.go:270`), which a block's own lines also
  contain, so blocks cannot be split back. Openings with a known end — CLAUDE.md context
  (`</user_claude_md>`), task notification, usage-limit reset (its fixed closing sentence),
  subagent message (closing agent-message tag), "Tool loaded." as a first line — now title
  from typed text that follows them. Open-ended openings (skill body, continuation summary,
  web page content, compaction request) still hide typed text that follows in the same
  message. Thesis for finishing it here: carry blocks through `Entry`, the view, the digest
  and `PromptTitler`. Antithesis: that changes core types for a case the store copy never
  shows (each of those arrived as its own message). Synthesis: leave the gap, recorded;
  the story still shows the whole prompt. Flips if a client sends typed text in the same
  message as one of those bodies.
- **D18** 2026-11-26 P3-a fix: a pasted-content tag is stripped when it starts a line
  (open, close, or unclosed to the end of that line) or when it is a complete tag with a
  quoted attribute anywhere (the client's `id="…"` form, which can sit mid-line); a bare or
  unquoted mention typed mid-sentence stays as typed. Probed: the old pattern turned the two
  review scenarios into "why does show in titles" and "explain".
- **D19** 2026-11-26 P3-b/P4-4 fix: command blocks that carry a local-command stdout or stderr
  block are a local command whether or not the caveat block comes first (title the typed text
  left over, else "/<name>" synthetic); "(system) slash command" (no readable name) is now
  synthetic, so no "(system)" title can win over a typed prompt.
- **D20** 2026-11-26 P3-d fix: the guarantee lives in `cmd/gateway`, the one package that
  already imports both the client profile and the timeline (the purity test forbids naming
  the client in `internal/ui` and `internal/timeline`, tests included): a compile-time
  `var _ timeline.PromptTitler = claudecode.Profile{}` beside `uiRules()`, so a signature
  drift fails the build, plus `TestRun_PromptTitleRuleReachesTheView` through the real
  gateway, store and UI handler.

## Review round 2
Fresh opus reviewer, `git diff main...HEAD` at 8ae2249. No P1. Last round (cap two).
- **P2-1** `rules.go:155-158, 265-279`: a message is taken as a local command when an output
  tag appears anywhere (inside typed args or a skill body), and `removeTag` cuts typed text
  after a local command; a typed `/review … <local-command-stdout> …` became "/review"
  synthetic. With **P3-4** (same function): text after a local command is not titled again.
- **P2-2** `digest.go:122-186` vs `api_sessions.go:402,468,584`: the list row's title is
  always the view overview's (`memo.row.title`), never `Digest.Title`, so round 1's P2-a was
  not visible and D16's extra work per row is spent on a value that is replaced. Correction
  to D16 and to step 1's Findings ("the session title (overview and list digest)"): the list
  shows the overview title.
- **P3-1** `build.go:461-470`, `view_overview.go:159`: a media-only first message titles the
  session "[media]"; D16's line on media-only messages is wrong for the view.
- **P3-2** `rules.go:197`: `withTail` cuts at the first end marker, so a body that quotes its
  own closing tag leaks its rest as a typed title.
- **P3-3** `rules.go:131`: an unclosed line-start pasted tag without the client's quoted id
  eats a typed line ("<pasted_content should not show…" → "").
- P4 (left): P4-1 the e2e test reads only the view (folded into P2-2's fix); P4-2 a slash
  command with no readable name drops its args; P4-3 old hooks-only turn mismatch.
- **D21** 2026-11-26 Round 2 P2-1/P3-4 fix (corrects D19): a command message is parsed as a
  leading run of complete blocks (caveat, command name/message/args, stdout, stderr) from
  the start of the text; it is a local command only if the caveat or an output block is in
  that run; only the run is stripped, the rest stays as typed and is titled again by the
  rule (a synthetic remainder stays synthetic). Tag-like text in typed args or a skill body
  no longer changes the verdict. Probed: the five new cases fail on the previous rules.go.
- **D22** 2026-11-26 Round 2 P2-2 (corrects D16 and step 1's Findings): a session-list row
  shows the view overview's title (`api_sessions.go:468-469, 584-585`); `Digest.Title` is
  shown only on the ungrouped page (`/api/ungrouped`, rows with no session view) and is what
  a search matches on those rows (`api_sessions.go:810`). Thesis for dropping D16's work:
  on session rows it is replaced. Antithesis: on ungrouped rows it is the only title, and its
  extra cost is a facts pass over entries already built plus one small read per agent call.
  Synthesis: keep D16; the end-to-end test now checks the list row's title equals the view's.
  Flips if the ungrouped page gets a view of its own. The sub-agent case was not added to the
  end-to-end test (its upstream fixture is a fixed stream with no tool call); the timeline
  tests cover it.
- **D23** 2026-11-26 Round 2 P3-1 fix (corrects D16's media-only line): a title made only of
  the view's placeholders (`[reasoning]`, `[media]`, `[unknown]`, via `stripPlaceholders`) is
  synthetic, so the session title skips an image-only prompt; the rail still shows "[media]".
  The digest never sees placeholders (it reads text blocks only), so the two agree except in
  one edge left as is: an image-only first prompt followed only by synthetic ones gives
  "[media]" in the view and the first synthetic title in the digest (shown only on the
  ungrouped page, D22).
- **D24** 2026-11-26 Round 2 P3-2 fix: a block that ends in a closing tag (agent message, task
  notification, CLAUDE.md context) ends at the LAST such tag, so a body that quotes its own
  closing tag stays whole; the usage-limit sentence and "Tool loaded." keep the first match
  (a fixed sentence cannot be quoted short, and typed text may repeat it). Trade-off: typed
  text that itself contains the same closing tag after the block, or typed text between two
  blocks of the same kind in one message, is taken into the block (synthetic). Rarer than a
  report quoting its tag; the story still shows the whole prompt.
- **D25** 2026-11-26 Round 2 P3-3 fix (narrows D18): an unclosed pasted-content tag at a line
  start is removed to the end of its line only in the client's quoted form
  (`<pasted_content id="…`); a bare `<pasted_content` followed by typed words stays as typed.
  Safety net: if stripping leaves nothing while the prompt had text, the title is the prompt
  itself, not synthetic, so no typed prompt can become an empty title.
- **D26** 2026-11-26 Final real-data recheck (after round 2) found a regression of D17: the
  client appends a fixed paragraph after a subagent's closing agent-message tag, so all 25
  subagent-message turns in the store copy were titled by that paragraph as typed text. A
  remainder that starts with the client's fixed footer opening now keeps the block's
  "(system) message from another session" title, synthetic; other typed text after the tag
  is still titled as typed. Found by re-running the title dump, not by review; no third
  review round (cap two).
