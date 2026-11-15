---
feature: 018-side-call-turns
kind: fix
status: draft
version: 1
created: 2026-11-15
supersedes:
branch: fix/018-side-call-turns
---
# Side calls shown as turns

## Goal
Claude Code's away summaries ("The user stepped away and is coming back…") and prompt
suggestions ("[SUGGESTION MODE: …") are requests it makes on its own, not prompts the
user typed. Today each one starts a turn of its own on the session page and counts as a
prompt and a main call in its sessions-list row (017 research D12, D16). They become side
calls, read by the Claude Code profile like the quota probe and title generation already
are.

The same class (research D6, D7): when a background task finishes, Claude Code submits a
`<task-notification>` prompt of its own and hands it to the model as a system message. Its
prompt hook is read as a typed prompt that no call answered, so the session shows a false
"no model calls captured" turn and counts a prompt the user never typed.

## Acceptance criteria
- **AC1** — A Claude Code request whose last user text starts with "The user stepped away
  and is coming back." is a side call named "away summary".
- **AC2** — A Claude Code request whose last user text starts with "[SUGGESTION MODE:" is
  a side call named "prompt suggestion".
- **AC3** — Neither kind starts or joins a turn, counts as a prompt or a main call on the
  sessions list, or adds to needs-a-look; both are listed among the session's side calls
  and open in the call details dialog like the other side calls.
- **AC4** — A real turn whose prompt only quotes either marker, or which follows one of
  these side calls in the same conversation, is still a turn.
- **AC5** — Sessions captured before the fix show it once the gateway runs the new build:
  their views are not served from anything built by the old rule.
- **AC6** — Go tests (run with -race) built from redacted copies of real captured request
  shapes cover AC1–AC4, including the session's turn, prompt and side-call counts.
- **AC7** — A prompt hook whose prompt the client's profile reads as a background task's
  notice (Claude Code: it starts "<task-notification>") is never a user prompt: it never
  counts as a prompt, never becomes a turn's typed prompt, and never raises needs-a-look by
  itself.
- **AC8** — The notice and the main call that answers it (the one starting within a second
  of its hook) stay together. While the turn is still open (its last main call asked for a
  tool call), both join that turn; once the turn has ended (its last main call answered
  with no tool call), the call starts a turn of its own marked "background task", whose
  text is the notice. A notice no captured call answers is a background turn too, with no
  needs-a-look item.
- **AC9** — The session page marks a background turn in the turn list and its header, and
  labels its text "Background task", not "Prompt"; the turn list's prompt count leaves it
  out.
- **AC10** — A typed prompt no captured call answered is still a hooks-only turn that
  counts as a prompt and needs a look.
- **AC11** — Go tests (run with -race) built from redacted copies of real captured hooks
  and requests cover a notice during an open turn, one after the turn ended and a real
  hooks-only turn (AC7, AC8, AC10); a web test covers AC9.

## Out of scope
- Any other Claude Code request kind not yet seen in the store.
- Changing how side calls are shown (the 017 side-call list and dialog stay as they are).
- Matching on headers or body fields other than the last user text (research D2).
- Storing the classification at capture time; it stays a view-time rule.
