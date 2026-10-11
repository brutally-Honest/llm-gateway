---
name: al-approve
description: Approve the current spec, plan, tasks, quick brief or change request. The approval is applied by a hook from your own keystroke — Claude cannot approve anything.
argument-hint: "[spec|plan|tasks|brief|change] [feature]   (no argument = whatever is waiting)"
disable-model-invocation: true
allowed-tools: Bash(.claude/scripts/loop.sh *) Bash(./.claude/scripts/loop.sh *)
---

# /al-approve

Your session id: ${CLAUDE_SESSION_ID}

The approval already happened — or was refused — before you read this. When the user typed `/al-approve $ARGUMENTS`, a UserPromptExpansion hook ran `.claude/scripts/approve.sh` and added its output to your context as "approve.sh output". If it refused, the user saw why and you never got this prompt.

- **No "approve.sh output" in your context:** the hook didn't run (hooks disabled, untrusted folder, or an older Claude Code). Say so, and tell the user to run `.claude/scripts/approve.sh $ARGUMENTS` in their own terminal. Stop there. Never try to approve by editing files or by running approve.sh — both are blocked.
- **Otherwise** relay the APPROVED / REOPENED lines in one or two sentences, then act on what was approved:

| Approved | You do |
|---|---|
| spec | Say the next step from its NEXT line (usually `/al-plan`). |
| plan | It approved the tasks in the same step when they pass the checks (APPROVED tasks line): list the tasks (id — title, one line each) and end with "Type `/al-implement` to build it." If tasks.md stayed a draft, show the problems it printed and say: fix tasks.md by hand or with `/al-plan`, then `/al-approve tasks`. |
| tasks | "Type `/al-implement` to build it." |
| brief | Build it now: read `.claude/skills/al-implement/LOOP.md` and follow it exactly, using the session id above. |
| change | Follow its NEXT line. Scope **spec**: edit spec.md (or brief.md) to apply the CR's Delta exactly — new ACs added, modified AC text replaced, removed ACs struck through as `- ~~**ACn**~~ — removed in vN (CR-nnn): reason`, never deleted — run `.claude/scripts/loop.sh check spec` (or `check brief`), summarise the diff, and ask the user to `/al-approve spec` (or `/al-approve brief`). Scope **plan** or **tasks**: tell the user to run `/al-plan` (the planner revises what the change request reopened). |
