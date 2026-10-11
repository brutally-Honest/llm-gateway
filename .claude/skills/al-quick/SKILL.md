---
name: al-quick
description: Small change with minimal ceremony — one brief.md (change, ≤5 ACs, ≤5 steps) instead of spec/plan/tasks, then one build-and-review loop. Bigger work belongs in /al-spec.
argument-hint: "<small requirement> [--here | --worktree]"
disable-model-invocation: true
allowed-tools: Bash(.claude/scripts/loop.sh *) Bash(./.claude/scripts/loop.sh *)
---

# /al-quick

Requirement: $ARGUMENTS

Your session id: ${CLAUDE_SESSION_ID}

## Where things stand
!`.claude/scripts/loop.sh status`

## Steps
- **Approved brief on this branch** (status shows `brief.md approved`): resume the build — read `.claude/skills/al-implement/LOOP.md` and follow it.
- **Draft brief**: refine it with the user's new input, then go to Draft.
- **Full feature (spec.md) on this branch**: this isn't a quick change; point the user to `/al-spec`, `/al-fix` or `/al-change`, and stop.
- **No feature**: decide the kind (feat / fix / refactor / chore) and a kebab-case slug yourself. Ask ONE AskUserQuestion call (≤3 questions) only if the requirement is ambiguous. Run `.claude/scripts/loop.sh new <kind> <slug> --quick` — add `--here` (stay on the current branch) or `--worktree` only if the user asked for it. If it prints `WORKTREE <path>`, call EnterWorktree with that path.

## Draft
Read the code the change touches, then fill `specs/<feature>/brief.md` (keep the frontmatter as is):
- **Change** 2-5 lines (fix: repro, expected vs actual).
- **Acceptance** at most 5: `- **AC1** — When <trigger>, the system shall <observable result>.`
- **Out of scope** at least one line.
- **Approach** the files you expect to touch and the pattern to follow.
- **Steps** at most 5: `- **S1** — <what> — Tests: <named behaviour tests>`.
Run `.claude/scripts/loop.sh check brief`. If it says the change is too big for /al-quick, tell the user and suggest `/al-spec`, then stop.
Show the brief in a few lines and end with: "Type `/al-approve brief` to approve and build it, or tell me what to change."

You never approve. Only the user can, with `/al-approve brief`; the build starts right after.
