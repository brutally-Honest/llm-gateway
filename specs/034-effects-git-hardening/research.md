---
feature: 034-effects-git-hardening
---
# Research & decisions — 034-effects-git-hardening

## Git commands the gateway runs (main at 5120e65)

`grep -rn 'exec.Command\|\.run(ctx\|"git"' internal/ cmd/` outside tests. All repo-context
calls go through `gitRunner.run` (`internal/effects/git.go:33`), which ran
`git --no-optional-locks -C <dir> <args>` with `GIT_OPTIONAL_LOCKS=0 LC_ALL=C
GIT_TERMINAL_PROMPT=0` added to the gateway's environment.

| # | Where | Command | Purpose |
|---|---|---|---|
| G1 | `git.go:51` `topLevel` | `rev-parse --show-toplevel` | work tree of a watched folder |
| G2 | `git.go:72` `candidates` | `status --porcelain=v2 -z --untracked-files=all` | changed, added, deleted, renamed, unmerged, untracked paths |
| G3 | `git.go:77` `candidates` | `ls-files -z -o -i --exclude-standard --directory` | ignored files (not ignored folders) |
| G4 | `git.go:126` `show` | `show HEAD:<path>` | before version of a tracked file |
| G5 | `install/preflight.go:179` | `git --version` (via `Env.Run.Output`) | preflight detail; no repo setup, reads no repo config |

## Repo-controlled settings that can make git run a program

Probed on git 2.53.0 in a throwaway repo with every setting below planted as a script
that touches a marker file (scratchpad probe, 2026-11-22):

| Setting | Runs in | Probe result on main's calls |
|---|---|---|
| `core.fsmonitor` (hook path) | index refresh, untracked scan: G2, G3 | **G2 and G3 ran it** |
| `filter.<d>.clean` / `.process` via `.gitattributes` or `.git/info/attributes` | hashing a stat-dirty or racy work-tree file: G2 (index refresh) | **G2 ran clean; G2 ran process** (even with `core.fsmonitor=false`) |
| `filter.<d>.smudge` | checkout, `cat-file --filters`; not G1–G4 | not run |
| `diff.<d>.textconv` | diff output, `show --textconv` | G4 did not run it; `show --textconv` did |
| `diff.external`, `diff.<d>.command` | patch output | not run (no patch output); blocked by `--no-ext-diff` anyway |
| `core.hooksPath`, `.git/hooks/*` | commands that run hooks (commit, checkout, …); none of G1–G4 | not run; pinned anyway |
| `core.pager`, `pager.<cmd>` | only when stdout is a terminal | not run (stdout is a pipe); `GIT_PAGER=cat` |
| `core.sshCommand`, `core.gitProxy`, `remote.<r>.uploadpack`, `protocol.<p>.allow`, `credential.helper`, `core.askPass` | any transport, including the **lazy fetch** a partial clone (`remote.<r>.promisor`) does when G4 needs a missing blob | not probed; reachable from G4 → blocked by D3 |
| `gpg.program`, `gpg.<fmt>.program` with `log.showSignature` | showing a commit; G4 shows a blob or tree | not reachable; pinned `log.showSignature=false` (D3) |
| `core.editor`, `sequence.editor`, `merge.<d>.driver`, `uploadpack.packObjectsHook` | write or server commands; not G1–G4 | not reachable |
| submodule recursion (`status` runs `git status` inside each submodule, with that submodule's config) | G2 | same filter and fsmonitor hole, one level down → removed with G2 (D1, D4) |

How each is blocked after this change: see D1–D5.

## Decisions

- **D1** (2026-11-22) G2 `git status` is replaced by Go code. Thesis for keeping status:
  it is git's exact answer, and empty `-c filter.<name>.clean=` overrides for every name
  in `git config --get-regexp '^filter\.'` do switch filters off (probed). Antithesis: that
  relies on enumerating names before each call, on git treating an empty command as no
  filter, and on submodules (own config, own names) being skipped; any future git feature
  that runs a program during index refresh reopens the hole. Synthesis: compute the answer
  in Go from data git gives without touching the work tree — `HEAD` entries from
  `git ls-tree -r -z --full-tree HEAD` (object store only), index entries and their stat
  from reading the index file in Go (path from `git rev-parse --git-path index`), and a
  work-tree file is changed when its stat differs from the index (or is racy) and the git
  blob hash of its raw bytes differs from the index's object id. Untracked files come from
  `git ls-files -z -o --exclude-standard` (no hashing). Flip: if the Go reader becomes a
  maintenance burden across index format changes, the enumeration approach plus a
  per-call filter override is the fallback.
- **D2** (2026-11-22) The one place the Go answer can differ from git: a file whose
  checked-in form differs from its work-tree form (clean filter, `eol`/`autocrlf`, LFS)
  and whose stat no longer matches the index is reported as a candidate even when git
  would call it clean, because the Go side hashes raw bytes and cannot run the filter.
  Candidates are only paths to look at; the runner compares them with its own snapshot,
  so the cost is at most one extra snapshot row, or one "modified" effect for a file
  that was only touched. Accepted: running the filter is the hole.
- **D3** (2026-11-22) Every git call: `git --no-optional-locks -c core.fsmonitor=false
  -c core.hooksPath=/dev/null -c log.showSignature=false -c protocol.allow=never -C <dir>`,
  env adds `GIT_CONFIG_NOSYSTEM=1 GIT_TERMINAL_PROMPT=0 GIT_PAGER=cat GIT_NO_LAZY_FETCH=1
  GIT_ALLOW_PROTOCOL=none GIT_OPTIONAL_LOCKS=0 LC_ALL=C`. `GIT_NO_LAZY_FETCH` (git ≥ 2.44)
  stops a partial clone from fetching a missing blob during G4; `GIT_ALLOW_PROTOCOL` set to
  a name no transport has refuses every transport on older git too, and overrides the
  repo's `protocol.<name>.allow` (which would beat `protocol.allow=never`). `show` also
  gets `--no-ext-diff --no-textconv`; `rev-parse`, `ls-files` and `ls-tree` take no diff
  options (`rev-parse` would read them as revisions), so they get only the global options.
- **D4** (2026-11-22) Gitlinks (mode 160000, submodules) are skipped by the Go
  comparison: entering a submodule means running git under its config. Before, status
  listed a changed submodule as a tracked path that is a folder, which the runner then
  stored as a deletion tombstone — so skipping it also drops a wrong effect.
- **D5** (2026-11-22) `skip-worktree` and `assume-unchanged` index entries are treated as
  unchanged in the work tree, as git status does. An index the Go reader cannot read
  (split index `link` extension, sparse index directory entries, a version other than
  2–4) falls back to `git ls-files -z -s -t` for entries and flags, and to hashing every
  entry without the stat shortcut.
- **D6** (2026-11-22) Commit scope: PLAN §11 has no `effects` scope; `internal/effects`
  history uses `capture` (`500a8d1`, `3378faa`). The hardening commit uses `capture`;
  install uses `install`.
- **D7** (2026-11-22) D2 also covers `eol`/`autocrlf` normalisation and LFS pointers, and
  a stat-dirty file whose content was only touched: listed as tracked, never wrongly left
  out; the runner's snapshot filters it.
- **D8** (2026-11-22) A path that is a gitlink in `HEAD` and a regular file in the index is
  listed as added (status said typechange). Rare; accepted with D4.
- **D9** (2026-11-22) `HEAD` counts as unborn only when `rev-parse --verify -q HEAD` exits
  with code 1. Any other failure (timeout, killed, other code) fails `candidates`, so the
  folder is skipped for this checkpoint rather than every tracked file reported as added.
- **D10** (2026-11-22) The stat shortcut needs size, mtime (s and ns) and inode to match the
  index and the file to be strictly older than the index file; anything else is hashed.
  A git that stores ns as 0 only costs hashing.
- **D11** (2026-11-22) The inode is read from `FileInfo.Sys()` by reflection on its `Ino`
  field so the file builds on every OS; no inode counts as a match, as in git.
- **D12** (2026-11-22) The fallback uses `git ls-files -z -s -v`, not `-t`: only `-v` tags
  assume-unchanged entries (lowercase), and `S` marks skip-worktree in both. Corrects D5.
- **D13** (2026-11-22) Reinstall always lists the config row in the summary: `unchanged`,
  or `updated` when the file has bits outside `0600` or its folder has bits outside
  `0700`; the content is never rewritten differently. A stricter existing mode (e.g.
  `0400`) is left alone. The folder is made or tightened before the temp file is written,
  so the config never sits in a looser folder; only that folder is chmod'ed.
- **D14** (2026-11-22) Old-code proof: with main's `git.go`, `TestGit_HostileRepoRunsNothing`
  fails in all three subtests — `filter clean` ran `[clean fsmonitor]`, `filter process`
  ran `[fsmonitor process]`, `full checkpoint` ran `[clean fsmonitor]`.
