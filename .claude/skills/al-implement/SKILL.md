---
name: al-implement
description: Build the approved feature (or quick brief) — implementer → checks → reviewer when policy asks → fix loop per task — until everything passes or your input is needed. Flags apply to this run only.
argument-hint: "[--profile fast|balanced|strict] [--review none|branch|risk|every] [--verify targeted|task|every-N|end|off] [--fix-rounds 0-3] [--mutation off|risk|every] [--model T002=haiku,reviewer=opus]"
disable-model-invocation: true
allowed-tools: Bash(.claude/scripts/loop.sh *) Bash(./.claude/scripts/loop.sh *)
---

# /al-implement

## Gate (if this failed, nothing runs)
!`.claude/scripts/loop.sh gate implement`

Your session id: ${CLAUDE_SESSION_ID}
Run flags: $ARGUMENTS

Read `${CLAUDE_SKILL_DIR}/LOOP.md` and follow it exactly.
