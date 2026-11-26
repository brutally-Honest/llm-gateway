# 048 research

## Progress
- [x] 2026-11-26 branch chore/048-full-docs from up-to-date main; spec and research created
- [ ] group A: docs/what-gets-recorded.md
- [ ] group B: docs/architecture.md and UI docs
- [ ] group C: README.md and docs/getting-started.md
- [ ] group D: AGENTS.md / PLAN.md rules, stale-docs sweep, links
- [ ] review round 1
- [ ] fixes for round 1 P1-P3
- [ ] review round 2 (only if round 1 found P1-P3)
- [ ] spec marked done, PR opened, CI watched

## Decisions
- **D1** 2026-11-26 Branch is `chore/048-full-docs`, not the bare `048-full-docs` the request
  named: AGENTS.md requires a feat/fix/chore prefix and 033 (also docs) used `chore/`. The
  slug is unchanged. Needs the owner's confirmation.
- **D2** 2026-11-26 Commit scopes, all from PLAN §11: `docs(spec)` for files under `specs/`,
  `docs(docs)` for `docs/` and README, `docs(repo)` for AGENTS.md and PLAN.md (process docs),
  `docs(adr)` for `docs/decisions/`, `docs(install)` for comments in `install.sh`.
- **D3** 2026-11-26 All doc writing is done by one sonnet subagent per group, one at a time;
  each group ends with `make verify && git commit` run under
  `systemd-run --user --scope -p MemoryMax=4G`. Review is a fresh opus subagent.

## Notes
