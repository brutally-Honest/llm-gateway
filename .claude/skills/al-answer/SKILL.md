---
name: al-answer
description: Answer a question the build stopped on (BLOCKED on Qn). Records the answer in research.md, commits it, and restores the blocked task's attempt; /al-resume continues.
argument-hint: "<Qn> <your answer>"
disable-model-invocation: true
allowed-tools: Bash(.claude/scripts/loop.sh *) Bash(./.claude/scripts/loop.sh *)
---

# /al-answer

A hook already ran `loop.sh answer $ARGUMENTS` from the user's keystroke; its output is in your context ("loop.sh answer"). If it failed, the user saw why and you never got this prompt.

Relay it in one or two lines: the question answered, and which task continues. End with: "Type `/al-resume` to continue the build."

If that output is missing (the hook didn't run), run `.claude/scripts/loop.sh answer $ARGUMENTS` yourself and relay it the same way.

Never get around a guard, hook or check by splitting, quoting, encoding or renaming strings, or by using another tool to perform the action that was blocked. Reading or editing a file with Read/Edit/Write is fine when only a Bash command naming it was blocked. approve, /al-change and pass-manual are human-only by any route. Run loop.sh answer only after the human has answered, and record their answer as given: if they picked an option, record that option's text, never your own rewording. Never write loop state or task status by hand. If unsure, stop and ask.
