---
name: al-pause
description: Pause this session's build. Plain /al-pause lets the current task finish; /al-pause now stops the agents at their next tool call. /al-resume continues. (While a build turn is running, just type any message — that pauses it too.)
argument-hint: "[now]"
disable-model-invocation: true
allowed-tools: Bash(.claude/scripts/loop.sh *) Bash(./.claude/scripts/loop.sh *)
---

# /al-pause

A hook already ran `loop.sh pause` for this session ("agent-loop pause" in your context). Relay it in one line — PAUSED (graceful: the current task finishes first; now: agents stop at their next tool call), or "no build is running in this session". End with "Type `/al-resume` to continue."

If that output is missing (the hook didn't run), tell the user to run `.claude/scripts/loop.sh pause` (add `--now` to stop agents at once) in a terminal, or press Esc.
