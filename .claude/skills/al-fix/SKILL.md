---
name: al-fix
description: Fix a bug. On an unmerged feature it adds a fix task to tasks.md and builds it right away; on a merged feature (or with no feature) it starts a /al-quick fix on its own fix/ branch.
argument-hint: "<what goes wrong, and when>"
disable-model-invocation: true
allowed-tools: Bash(.claude/scripts/loop.sh *) Bash(./.claude/scripts/loop.sh *)
---

# /al-fix

The bug, in the user's words: $ARGUMENTS

Your session id: ${CLAUDE_SESSION_ID}

## Gate
!`.claude/scripts/loop.sh gate fix`

## First: is it a bug, or a spec change?
Read the spec's acceptance criteria (spec.md or brief.md) for the behaviour the user describes. If an AC says the system should do exactly what the user calls a bug, it isn't a bug — it's a change to the spec. Say so in one or two lines (quote the AC) and suggest `/al-change <what they want instead>`. Stop there.

## MODE task — the feature isn't merged
1. Run `.claude/scripts/loop.sh add-fix <the bug, in the user's words>`. It adds `Tnnn — fix: …` as a not-started task (placed before the other open tasks) and commits tasks.md.
2. If it printed `NEXT the build runs Tnnn now`: read `.claude/skills/al-implement/LOOP.md` and follow it exactly, using the session id above and no run flags. The loop builds the fix first; tell the user they can type any message to pause after it.
3. Otherwise relay its NEXT line and stop.

## MODE quick — merged, or no feature here
Follow `.claude/skills/al-quick/SKILL.md` with kind **fix**: a slug from the bug, `loop.sh new fix <slug> --quick` (from the base branch), then draft the brief — Change = repro, expected vs actual; AC1 = the fixed behaviour; S1 carries the regression test. Then hand back for `/al-approve brief` as /al-quick does.
