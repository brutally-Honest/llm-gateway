---
name: al-status
description: Where the current feature stands and the next step — artifacts, tasks, open questions. Add --config to see every setting and where it came from.
argument-hint: "[--config]"
disable-model-invocation: true
allowed-tools: Bash(.claude/scripts/loop.sh *) Bash(./.claude/scripts/loop.sh *)
---

# /al-status

A hook already ran `loop.sh status` (and `loop.sh config` for `--config`) and put the output in your context as "agent-loop status". Show it to the user exactly as it is, in a code block. Add nothing unless the user asks a question about it.

If that output is missing (the hook didn't run), run `.claude/scripts/loop.sh status` — plus `.claude/scripts/loop.sh config` when the arguments include `--config` — and show the output as it is. Arguments: $ARGUMENTS
