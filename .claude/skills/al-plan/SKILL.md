---
name: al-plan
description: Draft the technical plan AND the task list for the current feature (requires an approved spec), then ask you the open questions. /al-approve plan approves both in one step.
disable-model-invocation: true
allowed-tools: Bash(.claude/scripts/loop.sh *) Bash(./.claude/scripts/loop.sh *)
---

# /al-plan

## Gate (if this failed, the planner does not run)
!`.claude/scripts/loop.sh gate plan`

## Steps
1. Dispatch the **planner** agent (Agent tool `model`: the gate's MODEL) with: `Feature: <FEATURE>. Mode: <MODE>.` — values from the gate output. If CHANGE-REQUESTS lists any, add `Change requests: <ids>`. Mode `tasks` means the plan is approved and only tasks.md is open: add what the user asked to change, or the problems from `.claude/scripts/loop.sh check tasks`.
2. It returns `PLAN-DRAFTED <questions> <tasks>` plus numbered questions. If there are no questions, go to step 4.
3. Ask the user those questions with AskUserQuestion (≤4 per call); put each question's recommended option first, labelled "(Recommended)". Then get the answers into the plan:
   - send them to the same planner with SendMessage: "Mode: revise. Answers: <each question → answer>. Mark each `→ decided: <answer>`, update the affected sections and tasks, run loop.sh check plan --draft and check tasks --draft";
   - if it can't be reached, dispatch a fresh planner with `Feature: <FEATURE>. Mode: revise. Answers: …`.
4. Run `.claude/scripts/loop.sh check plan` and `.claude/scripts/loop.sh check tasks`. If they report problems the planner should fix, send them back once (same agent, SendMessage).
5. Show the user: the chosen approach (1-2 lines); each alternative with one line on why not; the top risks; the tasks, one line each (id — title — size/risk); whether every AC is covered. End with:
   "Review `specs/<feature>/plan.md` and `tasks.md`. Type `/al-approve plan` — it approves the tasks too — or tell me what to change." (Mode `tasks`: "Type `/al-approve tasks`.")

You never approve and never write code. A hook blocks the planner's writes unless the spec is approved.
