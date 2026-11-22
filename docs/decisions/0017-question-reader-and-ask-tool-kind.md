---
status: accepted
date: 2026-11-15
---

# 0017 — questions and answers are read by the client's profile

> 2026-11-22: Widened to optional profile interfaces.

## Context
specs/017 (research D14) shows a tool call that asked the user questions — Claude Code's
`AskUserQuestion` — as questions and answers in its 016 tool dialog (AC23): each question
with its header, its options with the picked one marked, a typed answer or "No option
picked", and the user's notes; and its line says how many questions and their headers.
The answers are already captured: they are the tool call's input (the questions and
options) and its result (one sentence naming each answer). Nothing new has to be stored.

Reading them takes client knowledge: the tool's name, its input shape and the wording of
its result. AGENTS.md forbids a client `if` outside `internal/clients/<name>/`, and
everything downstream reads canonical shapes only. The session view (ADR 0016) already
takes a tool's kind and line from an optional profile interface, `core.ToolSummarizer`.

## Decision
- `internal/core` gains a neutral, optional profile interface, `QuestionReader`:
  `Questions(name, input, result) ([]Question, ok)`, with `Question` (header, text,
  multi-select, options, answer, notes) and `QuestionOption` (label, description). It
  names no tool and no client.
- `core.ToolKind` gains `ToolAsk` ("ask": puts questions to the user and waits for the
  answers), set by a profile's `ToolSummarizer`, so the view can tell an asking tool from
  the others without its name.
- The Claude Code profile implements both: `AskUserQuestion` is kind `ask`, its line is
  "N questions: headers", and `Questions` parses its input and result. The UI's tool
  endpoint asks the client's rules for a `QuestionReader` and sends what it returns; the
  browser lays it out and holds no tool knowledge.
- A client whose profile does not implement it loses nothing: its asking tool shows its
  raw input and result, as every tool did before.

## Alternatives
- **Parse `AskUserQuestion` in `internal/ui`.** Simplest and smallest, and it would ship
  the same screen. Rejected: a client-specific branch outside `internal/clients/`, the
  exact thing the agnosticism check forbids, and the next client with an asking tool would
  add a second branch beside it.
- **Parse it in the browser.** Rejected for the same reason, and the CLI (`gateway
  timeline`) could not show the answers.
- **Store the answers as their own canonical event at capture time.** It would make the
  answers queryable and free the reader from re-parsing. Rejected for now: it changes the
  capture path and the event set for one tool, while reading at view time needs no
  migration and already serves the only consumer.
- **No interface; a generic "show input and result" dialog.** The status quo. Rejected:
  the answers are the point of the call and were unreadable inside the result's sentence.

## Consequences
- The core stays client-agnostic: one more optional interface and one more tool kind,
  both neutral, with the tool knowledge and its tests in `internal/clients/claudecode/`
  (`questions.go`, `questions_test.go`).
- The reader depends on Claude Code's result wording ("The user answered: …"). If that
  wording changes, `Questions` finds no answer and the dialog says so (an empty answer),
  while the raw result is still shown; the parse tests are where the change shows first.
- Every new client with an asking tool implements `QuestionReader` in its profile, plus
  its contract tests; the core and the UI stay unchanged.
- Reversing it is cheap: drop the interface and the kind, and the dialog falls back to raw
  input and result. Nothing is stored in a new shape.
- **Revisit** — move to a canonical "answer" event at capture time if answers must be
  searched or counted across sessions, or if a second client's asking tool cannot be read
  from its input and result alone.
