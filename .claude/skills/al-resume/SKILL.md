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
