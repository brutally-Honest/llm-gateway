---
name: al-resume
description: Continue a paused, interrupted or stopped build of the current feature — from any session. Same as /al-implement; takes the same run flags.
argument-hint: "[--profile …] [--review …] [--verify …] [--fix-rounds N] [--mutation …] [--model …]"
disable-model-invocation: true
allowed-tools: Bash(.claude/scripts/loop.sh *) Bash(./.claude/scripts/loop.sh *)
---

# /al-resume

## Gate (if this failed, nothing runs)
!`.claude/scripts/loop.sh gate implement`

Your session id: ${CLAUDE_SESSION_ID}
Run flags: $ARGUMENTS

Read `.claude/skills/al-implement/LOOP.md` and follow it exactly. `loop.sh start` takes the build over for this session and works out where it stopped.

Never get around a guard, hook or check by splitting, quoting, encoding or renaming strings, or by using another tool to perform the action that was blocked. Reading or editing a file with Read/Edit/Write is fine when only a Bash command naming it was blocked. approve, /al-change and pass-manual are human-only by any route. Run loop.sh answer only after the human has answered, and record their answer as given: if they picked an option, record that option's text, never your own rewording. Never write loop state or task status by hand. If unsure, stop and ask.
