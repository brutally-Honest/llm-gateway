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

## Review round 1 (opus, fresh context, 2026-11-22)

No P1. Verified by running: the old-code proof (D14, same markers); every runner git call
under a repo with `.git/info/attributes` clean/smudge/process filters, `core.fsmonitor`,
aliases shadowing builtins, `core.sshCommand`, `credential.helper`, `gpg.program`,
`core.alternateRefsCommand` and an `include.path` pager — nothing ran; a `blob:none`
partial clone with an `ext::` remote and `protocol.ext.allow=always` — nothing ran with
`GIT_NO_LAZY_FETCH=1`, and nothing ran without it (`GIT_ALLOW_PROTOCOL=none` alone holds);
45 s / 3.4M-run fuzz of the index reader, no panic.

| # | Pri | Finding | Action |
|---|---|---|---|
| R1 | P2 | Reinstall rewrote an existing `config.yaml` (temp + rename): a symlinked config became a regular file, and an edit made during the prompt was lost | fixed: existing config is only chmod'ed (D15) |
| R2 | P2 | (pre-existing on main) a tracked path under a symlinked folder is read through the link: an unpacked repo can make the runner copy an outside file into the store; new `changed()` read it too | `changed()` fixed: leading-symlink path is listed and never opened (D16); the runner's own read in `checkpoint.go` is out of scope → follow-up spec (D17) |
| R3 | P3 | `candidates` ≈3.7× slower than `git status` at 100k files; fallback rehashes everything | fixed: size shortcut, no per-entry `Sprintf`, no reflection, fallback stat cache (D18) |
| R4 | P3 | Goal said "same changes as today" though D2 differs (LFS, autocrlf touched files) | spec Goal reworded |
| R5 | P4 | ctime not compared: same-size rewrite with restored mtime missed | fixed: ctime compared unless `core.trustctime=false` |
| R6 | P4 | `core.symlinks=false` reports every symlink entry changed | fixed: link text compared to the plain file |
| R7 | P4 | Lstat→Open race could block on a FIFO | fixed in `changed()`: non-blocking open + fstat; `readFile` in the runner has the same pre-existing gap → D17 |
| R8 | P4 | inherited `GIT_DIR`, `GIT_INDEX_FILE`, `GIT_CONFIG_PARAMETERS`… | fixed: stripped before the pins |
| R9 | P4 | `HEAD` and index read at different moments | accepted (D19) |
| R10 | P4 | exit 1 also for a corrupt/missing `HEAD` commit | accepted (D19) |
| R11 | P4 | dry-run/describe wording for a mode-only fix | accepted: "update" is accurate |
| R12 | P4 | equivalence tests ran the two sides with different global config | fixed: both sides `GIT_CONFIG_GLOBAL=/dev/null` |
| R13 | P4 | no regression tests for `.git/info/attributes`, smudge, lazy fetch, worktree, nested repo, `core.worktree` | added |

- **D15** (2026-11-22) An existing config is never rewritten by install: only its folder
  (to `0700`) and the file (to `0600`, through a symlink to its target) are chmod'ed when
  looser. Corrects D13's "content is never rewritten differently": it is not rewritten.
- **D16** (2026-11-22) A tracked path whose leading folder is a symlink is listed as
  changed (tracked) without being opened, as git refuses such paths.
- **D17** (2026-11-22) The runner (`checkpoint.go`) still reads candidate and saved paths
  through a symlinked folder and opens them without a FIFO guard — on main too. Fixing it
  changes what the runner captures, outside this spec's files; it needs its own spec
  (proposed `fix/035-effects-symlinked-folders`).
- **D18** (2026-11-22) Performance: as git, a size mismatch with a non-zero index size is
  changed without hashing; inode and ctime come from a build-tagged `syscall.Stat_t`
  helper; the fallback keeps a per-runner stat→oid cache so an unchanged file is hashed
  once.
- **D19** (2026-11-22) Accepted: `HEAD` and the index are not one atomic snapshot (one
  checkpoint can flip `tracked` for a path committed in between; the runner's snapshot
  absorbs it), and a repo whose `HEAD` commit is missing reads as unborn (corrupt repo;
  git status fails there too).
- **D20** (2026-11-22) Leading folders are `Lstat`ed once per `candidates` call and cached;
  a symlinked or missing one makes every file below it changed (tracked), as git does.
- **D21** (2026-11-22) The fallback cache keeps a match only when the file's mtime is more
  than 1 s old (it has no index mtime to guard the racy case); without inode and ctime
  (non-unix) its key is size and mtime.
- **D22** (2026-11-22) Inode and ctime come from `stat_linux.go` / `stat_bsd.go` (darwin,
  freebsd, netbsd); other systems use `stat_other.go` with no inode or ctime, which count
  as a match.
- **D23** (2026-11-22) `GIT_CEILING_DIRECTORIES` and `GIT_DISCOVERY_ACROSS_FILESYSTEM` are
  kept: they are the user's own limits on discovery, not a repo location.
- **D24** (2026-11-22) Performance after R3, 100k files, clean tree: `candidates` ≈555 ms
  against ≈85 ms for `git status` (fallback ≈645 ms after its first call). About 55 % is
  the sequential `Lstat` per entry; git stats in threads. Accepted for this fix: the
  checkpoint runs off the request path and safety wins over speed (AGENTS priority
  observability first, but capture never blocks a request). Follow-up: a worker pool for
  the stat loop.

## Review round 2 (opus, fresh context, 2026-11-22)

No remaining way for repo settings to make a runner git call run a program (probed again).
Old-code proof re-run: hostile, lazy-fetch, env-strip and fallback-cache tests fail on
main's `git.go`; all five `modes_test.go` tests fail on main's `install.go`. 40 random
rounds of the comparison matched `git status` exactly.

| # | Pri | Finding | Action |
|---|---|---|---|
| S1 | P1 | `.git/index` symlinked to `/dev/zero` or a huge file: unbounded `os.ReadFile` kills the process (OOM); main's git failed at once | fixed: non-blocking open, regular file, size cap, length check (D25) |
| S2 | P2 | FIFO at `.git/index` blocks the checkpoint forever | fixed with S1 |
| S3 | P2 | skip-worktree file present in the work tree (written outside the sparse cone) missed; git ≥ 2.37 reports it | fixed: present skip-worktree files are compared (D26); spec edge case corrected |
| S4 | P3 | hashing ignores ctx and the git timeout (huge sparse file) | fixed: chunked hash checks ctx; the Go comparison runs under the git timeout |
| S5 | P3 | racy-entry check and fallback-cache 1 s guard untested | tests added |
| S6 | P4 | index mtime read by a separate `Stat` | fixed with S1 (fstat) |
| S7 | P4 | `--show-object-format` / `config` failures silently become defaults | fixed: only "not set" defaults, other failures fail the call |
| S8 | P4 | stat cache keeps every work tree forever | fixed: tops unused for 1 h dropped |
| S9 | P4 | `..`, absolute or `.git` paths from a crafted index are opened | fixed: skipped |
| S10 | P4 | openbsd/dragonfly/solaris/aix lose inode and ctime | accepted (D22) |
| S11 | P4 | `modes_test.go` uses `syscall.Stat_t` | accepted: CI is linux and macOS; main's `internal/store` already does not build on Windows |
| S12 | P4 | `tighten()` failing on a config symlinked to another user's file stops the reinstall | fixed: a warning with the chmod to run (D27) |
| S13 | P4 | one `candidates` per watched folder in the same repo | accepted, D24 follow-up |

- **D25** (2026-11-22) The index is read only when it is a regular file of at most
  512 MiB; anything else goes to the `ls-files` fallback, where git itself decides.
- **D26** (2026-11-22) A skip-worktree entry is compared when its file is present, unless
  `sparse.expectFilesOutsideOfPatterns=true`; absent is unchanged. Corrects D5.
- **D27** (2026-11-22) A chmod that fails while tightening an existing config prints a
  warning with the command to run instead of failing the install.
- **D28** (2026-11-22) Two review rounds were run, the most the build allows; round 2's
  fixes are not reviewed again by a third opus round.
- **D29** (2026-11-22) `run` appends up to 300 characters of git's stderr to its error
  (still an `*exec.ExitError` underneath), so an old git's unknown-option failure for
  `--show-object-format` (exit 128 naming the option) can default to sha1; it is logged
  only as the folder-skipped warning.
- **D30** (2026-11-22) A crafted index or tree path (absolute, empty, `.`, `..` or a `.git`
  component in any case) is skipped entirely: never opened, never listed, and not listed
  as deleted from `HEAD` either.
- **D31** (2026-11-22) A skip-worktree path under a symlinked leading folder counts as
  absent (unchanged). A non-boolean value for the `core.*`/`sparse.*` keys read now fails
  `candidates` instead of being ignored.
- **D32** (2026-11-22) The fallback-cache guard test asserts a fresh file is not cached;
  a same-size rewrite cannot be made to keep its ctime, so the answer-level check alone
  cannot tell the guard apart.
