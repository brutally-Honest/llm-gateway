---
name: al-change
description: Change approved work — spec, plan or tasks — before merge. Nothing built yet → the artifact reopens and Claude edits it. Something built → an impact-analysed change request you approve. --adopt turns your own edit of the spec into that request; --reconcile handles code that drifted from the spec. After merge, use /al-spec --supersedes NNN.
argument-hint: "[spec|plan|tasks] <what changes and why> [--adopt] [--reconcile]"
disable-model-invocation: true
allowed-tools: Bash(.claude/scripts/loop.sh *) Bash(./.claude/scripts/loop.sh *)
---

# /al-change

The change, in the user's words: $ARGUMENTS

A hook already ran `loop.sh gate change` for this command (and paused this session's build, if one was running). Its output is in your context as "/al-change gate", with `MODE`, `TARGET`, `FILE`. If the gate refused, the user saw why and you never got this prompt. If that output is missing (the hook didn't run), run `.claude/scripts/loop.sh gate change $ARGUMENTS` yourself; for MODE reopen tell the user to run `.claude/scripts/approve.sh reopen <TARGET> --reason "…"` in a terminal, and stop.

## MODE edit — the target is still a draft
Edit FILE as the user asked (spec.md: keep the AC format, never reuse an id). Run `.claude/scripts/loop.sh check <TARGET> --draft`. Summarise the change and say: "Type `/al-approve <TARGET>` when it's right."

## MODE reopen — nothing is built yet
The hook already reopened TARGET (REOPENED line in your context). Edit FILE as the user asked — for the spec: new ACs get the next unused id, changed ACs keep their id, removed ACs are struck through as `- ~~**ACn**~~ — removed in vN: reason`, never deleted. Run `.claude/scripts/loop.sh check <TARGET>`, summarise the diff, and say: "Type `/al-approve <TARGET>`" (for the spec: "plan and tasks reopen automatically if the contract changed; then `/al-plan`").

## MODE cr, adopt, reconcile — something is built
1. If the request is vague (cr/reconcile), ask ONE AskUserQuestion call (≤3 questions): what exactly changes; why; what should happen to behaviour that's already built (keep, rework, remove).
2. Run `.claude/scripts/loop.sh cr-new` — for MODE adopt: `.claude/scripts/loop.sh cr-new --adopt` (it fills the Delta from the user's edit). It prints the CR id and file (or an existing draft to revise).
3. Dispatch the **impact-analyst** (Agent tool `model`: the gate's MODEL) with: `Feature: <feature>. CR: <id>. Mode: <amend | adopt | reconcile>. Request: <the user's words plus their answers, verbatim>.`
4. It returns `CR-DRAFTED <id>`. Show the user: class and scope; the Delta lines; the impact on done tasks (one line each); the recommendation. End with:
   "Type `/al-approve change` to accept it — this reopens the <scope>, and the build stays paused until it's approved again — or tell me what to change."

Nothing is reopened in these modes until the user types `/al-approve change`.
