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
