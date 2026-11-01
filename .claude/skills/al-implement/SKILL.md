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

Never get around a guard, hook or check by splitting, quoting, encoding or renaming strings, or by using another tool to perform the action that was blocked. Reading or editing a file with Read/Edit/Write is fine when only a Bash command naming it was blocked. approve, /al-change and pass-manual are human-only by any route. Run loop.sh answer only after the human has answered, and record their answer as given: if they picked an option, record that option's text, never your own rewording. Never write loop state or task status by hand. If unsure, stop and ask.
