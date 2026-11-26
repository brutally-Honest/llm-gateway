# 049 research

## Progress
- [x] 2026-11-26 PR #77 confirmed on origin/main; branch chore/049-notes-purge-cleanup from up-to-date main; spec and research created
- [ ] task 1: notes local only
- [ ] task 2: uninstall --purge wording
- [ ] task 3: app.js liveText comment
- [ ] task 4: outbound-destination audit
- [ ] task 5: check of the 048 round-2 fix commit (6059872)
- [ ] review round 1
- [ ] review round 2 (if needed)
- [ ] spec marked done; PR and CI

## Decisions
- **D1** 2026-11-26 The main session writes spec.md and research.md itself (they are the
  session's working memory, not repo docs); every other file is edited by subagents.
- **D2** 2026-11-26 Scopes: `docs(spec)` for specs/, `docs(repo)` for AGENTS.md,
  `build(repo)`/`test(repo)` for the Makefile and test/githooks, `docs(install)` for
  install docs and help text in `cmd/gateway` via `fix(install)`, `docs(ui)` for the app.js
  comment, `docs(docs)` for other files under docs/.
- **D3** 2026-11-26 `.githooks/pre-push` lets `refs/notes` pushes through and
  `test/githooks/prepush_test.go` ("notes" subtest) asserts it. To make "never push notes"
  hold, the hook rejects any push of `refs/notes/*` and the subtest now expects the
  rejection. This strengthens a hook rather than bypassing it. Needs the owner's
  confirmation (they may prefer docs-only).
- **D4** 2026-11-26 `notes.rewriteRef refs/notes/commits` stays in `make setup`: it is local
  only (keeps notes across a rebase) and pushes nothing.
- **D5** 2026-11-26 The hook policy change outlives 049, so it is ADR 0022 (notes stay local),
  which amends ADR 0001's "notes and tags are pushed unchecked". ADR 0001's body and the
  000 spec/plan lines on the notes refspec stay as history. → ADR-0022

## Notes
- 2026-11-26 Task 1 grep: no stale notes text in README.md, PLAN.md or docs/*.md; the only
  hits outside AGENTS.md, Makefile, the hook and its tests are history (ADR 0001:25,
  specs/000-foundation spec.md and plan.md, specs/048 research D34/D51).
- 2026-11-26 `shellcheck .githooks/pre-push` reports 5 warnings (SC2046, SC1007 x4) that
  are on main too; `make verify` lints only install.sh, so they do not fail the gate. Not in
  049's scope; follow-up candidate.
