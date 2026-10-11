---
name: al-spec
description: Start a feature from a rough requirement — creates the branch (or a worktree), interviews you, and drafts specs/NNN-slug/spec.md as a draft. Re-run on a feature branch to keep refining the draft.
argument-hint: "<rough requirement, in your words> [--here | --worktree] [--supersedes NNN]"
disable-model-invocation: true
allowed-tools: Bash(.claude/scripts/loop.sh *) Bash(./.claude/scripts/loop.sh *)
---

# /al-spec

Requirement, in the user's words: $ARGUMENTS

## Where things stand
!`.claude/scripts/loop.sh status`

## Rules
- The spec is the user's. You ask, they decide. Never invent a requirement. Anything you propose that they haven't confirmed carries `[ASSUMED]`; anything unanswered is `[NEEDS CLARIFICATION: …]`. Approval refuses while either remains.
- You never approve — only the user can, by typing `/al-approve spec` (a hook applies it). A hook also stops you from setting approval fields.
- Write only `specs/<feature>/spec.md` and `research.md`. No plan, no code.

## 1. Branch — only when "Where things stand" shows no feature on this branch
- If it shows a feature whose spec.md is **approved**: stop. Tell the user changes to an approved spec go through `/al-change <what>` (edits outside the contract sections — Goal, Non-goals, ACs, Edge cases, Constraints — need nothing).
- If it shows a **draft** spec: skip to step 2 and refine it with the new input.

Otherwise ask ONE AskUserQuestion call with these questions:
1. "Where should this work live?" — options `New local branch (Recommended)`, `This branch (<current branch>)` and `Worktree`. Leave this question out when the user's text already says (`--here` = this branch, `--worktree`). Offer "This branch" only when the current branch is not the base branch.
2. "What kind of change is this?" — feat / fix / refactor / chore, your best guess first.
3. "Short name for the branch and folder?" — 2-3 kebab-case slugs you derive from the requirement.

Then run `.claude/scripts/loop.sh new <kind> <slug>` adding `--here` (this branch) or `--worktree` if chosen, and `--supersedes <NNN>` if the user passed it. It creates the branch `<kind>/<NNN-slug>` from the base branch (or keeps the current one with `--here`) plus `specs/<NNN-slug>/spec.md` (draft) and `research.md`.
If it prints `WORKTREE <path>`, call EnterWorktree with `path: <path>`. If that tool isn't available, tell the user to run `cd <path> && claude` and `/al-spec` there, and stop.

## 2. Interview
2-4 rounds of AskUserQuestion, at most 4 questions per call. Read the code first, so the options you offer are concrete and grounded in what exists; the user can always answer "Other". Cover, in order, only what the requirement doesn't already answer:
- the problem, who has it, and what "done" looks like → Problem, Goal;
- what's in and explicitly out → Non-goals;
- for each capability, the trigger and the observable result → acceptance criteria;
- edge cases and failures that apply: empty or huge input, duplicates and retries, concurrency, auth and tenant boundaries, a dependency down, time zones → Edge cases;
- constraints: performance budgets, compatibility, security, data → Constraints.
By kind: **fix** — repro steps, expected vs actual; AC1 is the fixed behaviour. **refactor** — the structural goal, plus ACs for behaviour that must NOT change. **chore** — what must still work afterwards.

## 3. Draft
Fill spec.md (keep the frontmatter as is; replace each <!-- hint --> with content):
- ACs numbered `- **AC1** — When <trigger>, the system shall <observable result>.` — behaviour, not implementation; each one traceable to something the user said.
- Edge cases `- **E1** — <situation> → <expected> (ACn)`.
Run `.claude/scripts/loop.sh check spec --draft` and fix the format errors it lists.

## 4. Critic pass — CRITIC is !`.claude/scripts/loop.sh cfg CRITIC`
- `off`: skip this step.
- `self`: critique the draft yourself, briefly, against this list, then ask the user only what matters (AskUserQuestion, ≤4 per call) and update the spec:
  1. Untestable or vague ACs ("fast", "secure", "handles errors"), no observable result, implementation instead of behaviour.
  2. Missing behaviour: each dependency failing; invalid, empty or huge input; duplicates and retries; concurrency; permissions and tenant boundaries; time zones; partial failure.
  3. Contradictions between ACs, or with a non-goal or constraint.
  4. Scope holes: something the goal needs that no AC covers; something an AC implies that the non-goals exclude.
  5. Kind rules (fix: regression case; refactor: what must stay identical; chore: what must still work).
  6. Anything you proposed that the user never confirmed (`[ASSUMED]`).
- `agent`: dispatch the **spec-critic** agent: "Spec: specs/<feature>/spec.md". If it returns `GAPS`, ask the user the questions that matter (AskUserQuestion, ≤4 per call) and update the spec.

## 5. Hand back
Run `.claude/scripts/loop.sh check spec` (the approval check). Show the user: the AC list (one line each), the non-goals, and anything the check still reports. End with:
"Review `specs/<feature>/spec.md`. Type `/al-approve spec` to approve it, or tell me what to change."
