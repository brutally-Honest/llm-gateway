# The build loop — orchestrator procedure

You are the orchestrator. You dispatch agents and report; you never write code, edit specs or change git state. While the run flag is held, a hook enforces that for this session.

If the user sends a message while the build runs, a hook pauses it (you'll see "agent-loop: the build was paused" in your context). Log the result of the agent that was running as usual; the next `loop.sh` call then answers `ACTION pause`.

`.claude/scripts/loop.sh` is the state machine. It decides what happens next and prints it as the **last line**, starting with `ACTION`. You do what that line says — nothing else, nothing more.

## Start
Run `.claude/scripts/loop.sh start --session <your session id> <run flags, if any, exactly as the user typed them>`. Then act on its last line (`ACTION next`, or `ACTION stop …` if the run can't start).

Every `implement`, `review` and `fix` action ends with `model=<haiku|sonnet|opus>`: pass that value as the Agent tool's `model` parameter. It is resolved from the user's settings (`loop.sh config <ID>` shows why).

## Actions
| Last line | What you do |
|---|---|
| `ACTION next` | Run `.claude/scripts/loop.sh next`. |
| `ACTION implement <ID> model=<m>` | Dispatch a NEW agent — **implementer**, or **quick-builder** when ID is `Q` — with the prompt `Feature: <feature>. Task: <ID>.` Remember its agent id for this task. Then run `.claude/scripts/loop.sh log <ID> <agent> '<line 1 of its final message>'`. |
| `ACTION review <ID> model=<m>` | Dispatch a NEW **reviewer** — always fresh — with `Mode: task. Feature: <feature>. Task: <ID>.` (ID `Q` → `Mode: brief`; ID `BRANCH` → `Mode: branch`). Keep its full message. Then run `.claude/scripts/loop.sh log <ID> reviewer '<line 1 of its final message>'`. |
| `ACTION fix <ID> review <r/max> model=<m>` | Continue the SAME implementer of this task with SendMessage: `Fix round <r/max>. Fix only these reviewer findings:` followed by the reviewer's full message, verbatim. If it can't be reached (no id, or SendMessage fails), dispatch a NEW implementer (quick-builder for Q) with `Feature: <feature>. Task: <ID>. Fix round <r/max>. Fix only these reviewer findings:` + the findings. Then `log` as for implement. |
| `ACTION fix <ID> post-task <r/max> model=<m>` | Same, with: `Fix round <r/max>. The checks failed: run .claude/scripts/loop.sh findings <ID> and fix exactly those.` (a new agent also gets `Feature: <feature>. Task: <ID>.` first). Then `log` as for implement. |
| `ACTION ask <Qn> <ID>` | The implementer is BLOCKED on a question. Ask the user with ONE AskUserQuestion call: the `QUESTION` line printed above as the question, each `OPTION` line as an option (the user can always pick "Other"). Then run `.claude/scripts/loop.sh answer <Qn> '<their answer, verbatim>'`; it restores the task and prints `ACTION implement <ID> …` — continue the SAME implementer with SendMessage: `<Qn> is answered: <answer>. Your earlier attempt is back in the working tree; finish the task.` (new implementer with `Feature: <feature>. Task: <ID>.` + that sentence if it can't be reached). If AskUserQuestion isn't available or the user doesn't answer, run `.claude/scripts/loop.sh stop 'blocked <ID> <Qn>'` and report: they answer with `/al-answer <Qn> <text>`, then `/al-resume`. |
| `ACTION ask-escalate <ID>` | The reviewer escalated. Ask with ONE AskUserQuestion call — the question is the reviewer's ESCALATE text (short), options: **Change the spec** · **I'll fix the code** · **Accept as is**. Change the spec → run `.claude/scripts/loop.sh pause`, then `loop.sh next`, and tell the user to type `/al-change <what>`. I'll fix the code → `loop.sh pause`, `loop.sh next`; tell them to fix and `/al-resume` (the task is reviewed again). Accept as is → `.claude/scripts/loop.sh accept <ID> '<their reason, or "accepted as is">'` and act on its ACTION. No answer possible → `loop.sh stop 'escalate <ID>'`. |
| `ACTION ask-dirty <ID>` | The build was interrupted mid-task and left uncommitted work (listed above). Ask with ONE AskUserQuestion call: "What should happen to <ID>'s unfinished work?" — **Continue** (an implementer finishes this diff) · **Discard** (stashed, <ID> starts over) · **Keep as my change** (left in the tree for you). Then run `.claude/scripts/loop.sh dirty <ID> continue|discard|keep` and act on its ACTION. For `ACTION implement` after *continue*, dispatch a NEW implementer with `Feature: <feature>. Task: <ID>. Your earlier attempt was interrupted; its uncommitted changes are in the working tree — finish the task from there.` No answer possible → `loop.sh stop 'interrupted <ID>'`. |
| `ACTION implement <ID> …` right after `start` or `answer` says the task *continues* | Same as implement, but tell the agent its earlier attempt is in the working tree and to finish from there (or SendMessage the same implementer if you still have it). |
| `ACTION stop …` | Run `.claude/scripts/loop.sh stop '<the whole ACTION line>'`, then report. |
| `ACTION finish` | Run `.claude/scripts/loop.sh finish`, then report. |
| `ACTION pause` | The user paused the build. Run `.claude/scripts/loop.sh status`, show it, and say `/al-resume` continues. Then you are no longer the orchestrator: do what the user asks. |

Rules:
- Dispatch every agent in the foreground (`run_in_background: false`) and wait for its final message; never start the next step before it returns.
- New agents for `implement` and `review`; fix rounds continue the task's implementer (it already knows the code). Pass only the prompts above (plus findings in fix rounds) — the agents read everything else from the repo.
- Lines before `ACTION` (e.g. "T002 reviewed: changed package.json", "T001 passes without a review (skipped: low risk …)") are for the report; don't act on them.
- Pass line 1 of the agent's final message to `log` exactly, in single quotes. A hook has already checked its format; `log` decides what it means.
- Don't interpret, retry, skip or reorder on your own. If something looks wrong, the next `loop.sh` call will stop the run.
- Keep your own context small: don't read diffs or files yourself.

## Report (the only thing the user reads)
Show the output of `loop.sh stop` / `loop.sh finish` as it is. Then add:
- the branch review's verdict and its findings, if it ran;
- for a stop: what the user must do, in one or two lines — the report's `Next:` line, plus the reviewer's ESCALATE question or the blocked `Qn` if that's why it stopped.
The loop never pushes, merges or approves. Those are the user's.
