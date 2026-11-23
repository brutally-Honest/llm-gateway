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

## Follow-ups (2026-11-22)

- **D33** (2026-11-22) Corrects D17: the symlink copy hole (R2) is fixed in this PR, as
  the same hostile-repo threat. Thesis for refusing every symlink with `Lstat` on each
  component: simplest to reason about, never follows anything. Antithesis: it also refuses
  a folder symlinked inside the root, which scan mode reaches today, and a
  check-then-open walk races with a folder swapped for a link in between. Synthesis: every
  work-tree file the runner reads, hashes or copies is opened through `os.Root` (Go 1.24+,
  stdlib) for the watched root (the work tree `top` in `changed()`). The kernel-backed walk
  refuses any path that leaves the root through a symlink or `..`, with no race, and
  symlinks that stay inside still work. The final component must be a regular file both
  by `Lstat` and by `fstat` after the open, so a final symlink is never copied. A refused
  escape counts as "not a regular file", the same branch as today. Flip: if roots must
  one day follow links out on purpose (e.g. a monorepo that symlinks a sibling checkout),
  that would need an explicit allowlist of extra roots, not a relaxed open.
- **D34** (2026-11-22) One `os.Root` per `checkRoot` call and one per `candidates` call.
  `os.Root` exports no escape error, so after `rt.Lstat` fails the runner retries a plain
  `os.Lstat`: if that succeeds the path escapes and is treated as not a regular file
  (deleted, or a tombstone in git mode); if it fails too, the file is "unreadable now"
  and checked again next time. A flip inside that window only delays a report.
- **D35** (2026-11-22) The runner's `readFile` now opens non-blocking and requires a
  regular file by `fstat`, closing the FIFO gap D17 noted (R7).
- **D36** (2026-11-22) If `os.OpenRoot(top)` fails, `candidates` fails and the folder is
  skipped for this checkpoint; treating every file as changed would flood false effects.
- **D37** (2026-11-22) A-test evidence on the branch before the fix: `SymlinkedFolderToOutside`
  and `SymlinkChain` fail in git and scan mode (a modified effect carrying the secret,
  and the secret in `gateway.db-wal`). `FinalSymlinkToOutsideFile` passes before and
  after: `Lstat` already saw a final link as non-regular. It is kept as a regression
  guard and does not reproduce the hole. `SymlinkInsideRootStillWorks` passes in both,
  showing in-root links keep today's behaviour.

## Fuzz run (2026-11-22, git 2.53.0, go 1.25.4)

`FuzzReadIndex` (`internal/effects/gitindex_fuzz_test.go`) runs `readIndex` at hash lengths
20 and 32. It checks: no panic, no entries returned with an error, entry count at most
`len/(40+hashLen+3)`, every path non-empty and every oid hex, and total path bytes at most
`maxPathExpansion × len`. Seeds: git-made indexes for v2, v3 (skip-worktree and
assume-unchanged), v4, sha256 v2 and v4, split index and cone sparse index, each also
truncated at several points and with count `0xFFFFFFFF`; empty, header-only, random bytes
with a fixed seed, absurd v4 strip varints, and a long-path regression seed. `make test`
(`go test -race ./...`) runs only the seeds, in about 0.06 s.

| Run | Time | Execs | Result |
|---|---|---|---|
| 1 | 176 s | — | **failed**: hashLen 32, 14 370 path bytes from 3 302 input bytes |
| 2 (after the fix) | 10 m 0 s | 15 733 961 (≈40 100/s at the end), 10 new interesting, 149 in the corpus | pass, no crasher |

- **D38** (2026-11-22) Bug found by run 1: a v4 path is rebuilt from the previous one and
  each copy is kept, so one long path followed by many strip-0 entries grows memory
  quadratically. Fix: a path-byte budget of `64 × len(index)`; going over it is
  `errIndexUnsupported`, so the `ls-files` fallback answers. The run-1 crasher
  (`testdata/fuzz/FuzzReadIndex/6e8242082b016071`, 4.35×) is kept as a seed. Under the 64×
  budget it is harmless, and the `long` seed (≈94×) is the one that fails without the fix.
  The property was 4× in run 1 and was loosened to the budget.
- **D39** (2026-11-22) The fuzz test has its own small git helper: the repo helpers take
  `*testing.T` and the fuzz body has `*testing.F`.

## Review round 3 (opus, fresh context, 2026-11-23)

Covers the `.git/index` reader, the Go replacement for `git status`, the round-2 fixes
(512 MiB regular-file rule, FIFO, sparse checkout, hash timeout), 0d58b37 and 30b50c1.
Corrects D28: rounds 3 and 4 were run. Checked sound: `indexVarint` cannot overflow,
`strip` is bounds-checked, v2/v3 padding matches git, the extension loop always advances,
the non-blocking index open rejects a FIFO, `/dev/zero` and size-0 `/proc` files, `os.Root`
refuses links out of the root, and the racy, size, executable-bit and symlink comparisons
match git.

| # | Pri | Finding | Action |
|---|---|---|---|
| T1 | P1 | v4 path budget of 64× the index allowed up to 32 GiB of paths; each path built twice; budget checked after allocating | fixed 8246391 (D40) |
| T2 | P1 | `run` buffered all of git's stdout: a refused crafted index made `ls-files` print ~1 GB, a 44 KB repo made `ls-tree -r` print 1.2 GB (also on main), `show` buffered a whole blob | fixed bfc2392 (D41) |
| T3 | P2 | a present skip-worktree file was compared without sparse checkout; `git status` is silent | fixed 2b667b0 (D42) |
| T4 | P2 | one 10 s deadline for git and hashing, and the hash cache stored only after a full pass: a slow repo was skipped at every checkpoint | fixed 39f88d4 (D44) |
| T5 | P2 | `os.OpenRoot` blocked forever on a FIFO in place of the watched root (from 0d58b37) | fixed 65ef68a (D43) |
| T6 | P3 | a 512 MiB index could cost 2-3 GB parsed; `io.ReadAll` grows to 2× | fixed 1a51779 (D45) |
| T7 | P3 | the runner's `readFile` hashed with `io.Copy` and never checked ctx (on main) | fixed 4dbf067 (D46) |
| T8 | P4 | entry-flag name length and index checksum not checked: a crafted index can parse unlike git | accepted: `safeRelPath` and `os.Root` stop any escape; only a hostile repo's own candidates are wrong |
| T9 | P4 | gitindex.go comment claimed a 4096-byte path limit the code did not enforce | fixed with T1 |

Allocation check: every allocation in `readIndex` sized by a number from the file is bounded
by the bytes left in the file except the v4 paths (T1). After T1, round 4 re-checked the
table: the read buffer is at most 128 MiB (regular file only), `out` is `count × 72 B` with
`count ≤ len/(43+hashLen)` checked before allocating, each oid is a fixed `2×hashLen`, a v2/v3
path is at most 4096 bytes and a substring of the file, a v4 path is at most 4096 bytes and all
v4 paths at most `min(4×len, 64 MiB)`, both checked before the append; the varint and the
extensions allocate nothing.

- **D40** (2026-11-23) T1: a path over 4096 bytes (git's PATH_MAX), in any version, and v4
  paths over `min(4×len(index), 64 MiB)` in total are `errIndexUnsupported`, checked before
  anything is allocated; v4 paths are built in one reused buffer with a single `string` copy.
  A real v4 index expands about 0.52× (GOROOT). Corrects D38's 64× budget. Flip: a legitimate
  repo of very few very deep paths can pass 4× and uses the slower `ls-files` fallback.
- **D41** (2026-11-23) T2: each git call's stdout is capped; past the cap git is cancelled and
  the call fails with `errGitOutputTooLarge`, so the folder is skipped for this checkpoint like
  any other git failure. `show` holds at most `maxCopy+1` bytes, and an over-limit blob means no
  before-version. The cap was 256 MiB here and is 64 MiB after D48. The added regression test
  passes before the context reorder too: os/exec closes the pipe and git dies of SIGPIPE.
- **D42** (2026-11-23) T3, corrects D26: a present skip-worktree file is compared only when
  `core.sparseCheckout` is true and `sparse.expectFilesOutsideOfPatterns` is not, as git's
  `clear_skip_worktree_from_present_files`. A non-boolean `core.sparseCheckout` fails
  `candidates` (D31); git status exits 128 on it too. The spec edge case is corrected.
  Antithesis: reporting edits hidden by `--skip-worktree` is more observability; rejected,
  since the spec promises `git status` answers.
- **D43** (2026-11-23) T5: `openDirRoot` refuses a watched root or work tree that is not a
  directory before `os.OpenRoot` (the not-exist branch: rows kept, checked again later). Go
  1.25 cannot build an `os.Root` from an fd, so a FIFO swapped in between the check and the
  open can still block. Accepted as P4: the window is microseconds and needs a hand-timed swap.
- **D44** (2026-11-23) T4: git calls keep their own timeout each; the Go comparison has a
  separate hashing budget (`hashTimeout`, default the same 10 s, no new config field). When it
  runs out, `keepProgress` stores the matches already proven under D21's rules plus the old
  cache entries the pass did not reach, and the call still fails; a repo too big for one pass
  warms its cache across checkpoints.
- **D45** (2026-11-23) T6, corrects D25's size: the index cap is 128 MiB, not 512 MiB; real
  non-split indexes are tens of MB. The file is read into one fstat-sized buffer with
  `io.ReadFull` and a one-byte growth check. Oids stay hex strings (out of scope).
- **D46** (2026-11-23) T7: `readFile` hashes streams with the shared ctx-aware `hashChunks`. The
  checkpoint ctx is the runner's lifetime, so this only lets shutdown stop a long hash; a
  cancelled checkpoint ends unsaved and every file is judged again next time. A file whose size
  changes while it is hashed is skipped and retried next checkpoint.

## Review round 4 (opus, fresh context, 2026-11-23)

Covers the round-3 fixes (8246391..4dbf067). Checked sound: no cache poisoning by
`keepProgress` (only hash-proven or exact-oid matches enter `seen`; carried entries are keyed
on size, mtime ns, ctime ns, inode and oid, and ctime cannot be set back on Linux or macOS);
no race in `capWriter` and partial output is never returned; skip-worktree answers checked
against git 2.53 with sparse checkout on and off; `readExactly` makes one capped allocation.

| # | Pri | Finding | Action |
|---|---|---|---|
| F1 | P1 | a watched root swapped for a symlink to an outside folder was opened through it (`Stat` and `OpenRoot` follow links): scan mode stored outside files, secret in `gateway.db-wal` (old hole, kept by 65ef68a) | fixed 51d8a98 (D47) |
| F2 | P2 | the stdout cap was multiplied ~9× by parsing (buffer doubling, `string(out)`, `Split`, maps): ~2.3 GiB peak per `candidates` from an 84 KB git bomb | fixed 074a0bd (D48) |
| F3 | P2 | a sparse tracked file of 2^35+5 bytes passes the size check (git stores size mod 2^32) and burns the hash budget: the folder fails at every checkpoint | fixed f6999f0 (D49) |
| F4 | P3 | round 3 not recorded here; code cited a missing "D-P2b"; D25 still said 512 MiB | fixed: this section, D44, D45 |
| F5 | P3 | the hash budget started before the `configBools` git call, and the first file was not always tried, so a call could make no progress | fixed b86e3f6 (D50) |
| F6 | P4 | git's stderr goes into an unbounded buffer | accepted: stderr scales with git's own messages, not with repo data a listing prints |
| F7 | P4 | `show` with `limit+1` bypassed the cap for a huge `max_copy_bytes`, and overflowed at MaxInt64 | fixed with F2 |
| F8 | P4 | a skip-worktree path under a symlinked folder counts as unchanged | accepted (D31) |
| F9 | P4 | while timeouts repeat, `keepProgress` keeps old entries for paths gone from the index | accepted: never larger than the previous cache, and the first full pass drops them |

- **D47** (2026-11-23) F1: roots are `EvalSymlinks`-resolved when added (`roots.go resolve`) and
  git reports `top` resolved, so a link there is always a later swap. `openDirRoot` `Lstat`s
  the path (a link or non-directory is gone) and after `os.OpenRoot` requires `rt.Stat(".")` to
  be `os.SameFile` with it, which also catches a directory swapped in between. The FIFO window
  of D43 remains.
- **D48** (2026-11-23) F2: the git output cap is 64 MiB (about 600k files at ~100 B a record) and
  both listing parsers stop at 2M records; they walk the `[]byte` output and copy only the
  fields kept, so the buffer is freed after parsing. Antithesis: stream records from
  `StdoutPipe` and keep 256 MiB, so larger monorepos are watched; rejected for now as a bigger
  change. Flip: if a watched repo over ~600k files must be supported, stream.
- **D49** (2026-11-23) F3: a stat-dirty tracked file of 4 GiB or more is hashed after all other
  files, on its own hashing budget; if it is not proven equal in time it is listed as a tracked
  candidate and not cached. The runner then re-reads its content, so a false "maybe changed"
  gives no effect, only a slow read. Rejected: always listing such files unhashed, which also
  reports unchanged ones and breaks `TestCandidates_HashingStopsWithContext`.
- **D50** (2026-11-23) F5: the hashing budget starts after `configBools`, the last git call
  before hashing. The first file that needs a hash is hashed whole under the caller's ctx (a
  spent budget would stop `hashChunks` at byte 0), and the call fails after the loop only when
  a hash was actually cut off, so a spent budget still proves one file per call and then
  succeeds. A file of 4 GiB or more never gets this free hash (D49).
- **D51** (2026-11-23) D24 (one parallel `Lstat` loop, and one `candidates` per repo rather than
  per watched folder, S13) stays a follow-up for a later spec; not in 034.
- **D52** (2026-11-23) D22 needs nothing: v1 supports only Linux and macOS, which
  `stat_linux.go` and `stat_bsd.go` cover; other systems only build.
- **D53** (2026-11-23) The round-4 fixes (D47-D50) were not reviewed by a fifth round: the
  build allows a fourth round only on a round-3 P1.
- **D54** (2026-11-23) Corrects D53: the human allowed a narrow fifth round over the round-4
  commits only (51d8a98, 074a0bd, f6999f0, b86e3f6); no sixth round.
- **D55** (2026-11-23) D43 (a FIFO swapped in between `Lstat` and `os.OpenRoot` can block) and
  D48 (a repo whose git listing passes 64 MiB or 2M records, about 600k files, is skipped)
  stay recorded limits of this PR.
- **D56** (2026-11-23) PR #65's macOS job (run 45100294664) failed at the 10-minute test
  timeout in `cmd/gateway`, stuck in `TestRun_ClientDisconnectMidStreamRecordsSeenTokens`
  (`core.(*responseWatcher).Read`; the effects runner was idle in its sweep loop), while
  Ubuntu passed. main's runs 45013800880 (5120e65) and 45001477758 (b4b2d6c) show the same
  pattern: the macOS Test step failed after about 10 minutes and Ubuntu passed. So it is a
  hang already on main, not caused by 034, which touches neither `cmd/`, `internal/core` nor
  `internal/server`. It gets its own spec (036) and is not fixed here.

## Review round 5 (opus, fresh context, 2026-11-23)

Narrow, as D54 allows: only the round-4 commits 51d8a98, 074a0bd, f6999f0 and b86e3f6.
Checked sound: `eachRecord` on empty output, a trailing NUL, doubled NULs and paths with tabs,
spaces or newlines (`-z`, cut at the first tab); kept fields are copied, so nothing pins the
output buffer; deferred huge files are never cached unproven and never lost on a full pass;
the `show` clamp cannot overflow; `os.SameFile` holds across bind mounts.

| # | Pri | Finding | Action |
|---|---|---|---|
| V1 | P1 | 51d8a98 guarded only the last component: `Lstat` and `os.OpenRoot` both follow links in parent folders, so a parent swapped for a link pointed a root at outside files (probe read the outside secret) | fixed 15a39d6 (D57) |
| V2 | P2 | the free first hash ran on the budget clock; a modified same-size first file cached nothing and the next file failed the call, so the folder stalled at every checkpoint | fixed f9f1c58 (D58) |
| V3 | P2 | the 4 GiB deferral used the `Lstat` size but the hash used the `fstat` size: a huge file renamed in between was hashed whole without a budget | fixed 59e12eb (D59) |
| V4 | P3 | D49 said an unproven huge file "gives no effect"; with no saved row the runner records one modified effect | corrected (D61) |
| V5 | P3 | an expired automount root gives a different inode before and after the open triggers it, so it looked gone | fixed with V1 (D57) |
| V6 | P3 | the untracked and ignored listings had neither the record cap nor in-place parsing | fixed 7be9cf0 (D60) |
| V7 | P4 | a root that legitimately becomes a symlink is treated as gone, unlogged, until a reload re-resolves it | accepted (D47); logging is a follow-up |
| V8 | P4 | with `max_copy_bytes` of 64 MiB or more, a blob between 64 MiB and the limit has no before-version | accepted: the git output cap wins (D48) |

- **D57** (2026-11-23) V1, V5: `openDirRoot` identifies a root by a no-follow walk from `/`. On
  Linux each component is opened with `openat(O_DIRECTORY|O_NOFOLLOW|O_NONBLOCK)`, so a
  link or non-directory anywhere in the path is gone. macOS's `syscall` has no `Openat`, so
  there every prefix is `Lstat`ed before and after an `O_NOFOLLOW` open; a parent swapped and
  restored between the two checks can still slip through. Other systems keep `Lstat`. After
  `os.OpenRoot`, `rt.Stat(".")` must be `os.SameFile` with that identity; a mismatch is
  identified once more before the root counts as gone, for an automount the open triggered.
  Production roots and git's `top` are already resolved, and the macOS `/var` link was
  checked by running the darwin logic on Linux under a symlinked `TMPDIR`. Rejected:
  `openat2` with `RESOLVE_NO_SYMLINKS`, which needs golang.org/x/sys (a dependency), and an
  `EvalSymlinks` equality check, which races.
- **D58** (2026-11-23) V2, corrects D50: hashes run under the caller's ctx until one proves a
  file equal to the index, and the budget clock starts then. The budget is checked only right
  before a hash starts, so a file that needs no hash never fails the call. Rejected: starting
  the budget at the second hash, since a second mismatch under a spent budget stalls again.
  Limit: a run of mismatching files hashes without a time bound, in proportion to the
  changed bytes, which the runner reads again anyway.
- **D59** (2026-11-23) V3: when the opened file is not the one `Lstat` saw (inode, size or
  mtime), `changed()` lists it as changed without reading it or caching it. No hash outside
  the budget reads a file of 4 GiB or more. That cap has no test, because it would need a
  multi-TiB sparse file.
- **D60** (2026-11-23) V6: the untracked and ignored `ls-files` listings go through
  `eachRecord` under the 2M-record cap and copy only the kept path, completing D48.
- **D61** (2026-11-23) V4, corrects D49: an unchanged huge file that cannot be proven in time
  and has no saved row gives one modified effect, the same class D2 accepts. It gives no
  effect only when a saved row's hash matches.

## Follow-ups (2026-11-23)

Found in review round 5 but older than round 4, so not in this PR:

- **P3** `internal/effects/checkpoint.go:309-358` (`readFile`, at 7be9cf0): any candidate of any size,
  untracked or in scan mode, is streamed in full under the runner's lifetime ctx. A sparse
  multi-TiB file holds up its session's checkpoints for a long time; f6999f0 adds unproven
  tracked huge files to the same path. Possible fix: cap streamed hashing, or key the row by
  size and mtime above a threshold.

Also later, though from round 4 (accepted, V7): **P4** `internal/effects/checkpoint.go:127`
(`openDirRoot`) should log once when a root is treated as gone because it became a symlink.
