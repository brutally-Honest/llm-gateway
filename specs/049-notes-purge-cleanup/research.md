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

## Notes
