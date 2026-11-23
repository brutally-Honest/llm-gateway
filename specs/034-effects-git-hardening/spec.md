---
feature: 034-effects-git-hardening
kind: fix
created: 2026-11-22
branch: fix/034-effects-git-hardening
---

## Problem
Audit SEC-1: the effects runner runs git in the work tree of any file a session reads or
writes (`internal/effects/git.go`). git obeys that repo's own `.git/config` and
`.gitattributes`, so a cloned or unpacked repo runs a program of its choosing on the
user's machine: `git status` runs `core.fsmonitor` and the clean or process filter of any
stat-dirty file; `git ls-files -o` runs `core.fsmonitor`.
Audit SEC-6: `gateway install` leaves the config folder `0775`/`0755` and `config.yaml`
`0664`/`0644`.

## Goal
- No git call the gateway makes can run a program named by the repo it runs in.
- Effects report the same changes as today (same paths, same tracked/untracked answer,
  same before versions from `HEAD`), except where git's answer needs a filter to compute
  (research D2) or enters a submodule (D4).
- The config folder is `0700` and `config.yaml` is `0600`, on a new install and on a
  reinstall over looser modes.

## Non-goals
- Defending against a malicious git binary on `PATH`, or the user's own global git config.
- Changing which folders are watched, or the scan mode used without git.
- Modes of any other installed file (binary, service unit, shell rc, client settings).

## Acceptance criteria
- **AC1** Every git call carries `-c core.fsmonitor=false -c core.hooksPath=/dev/null
  --no-optional-locks`, `--no-ext-diff --no-textconv` where the command takes them, and the
  environment `GIT_CONFIG_NOSYSTEM=1 GIT_TERMINAL_PROMPT=0 GIT_PAGER=cat`, plus no lazy fetch
  and no transport (research D3).
- **AC2** No git call can run a clean, smudge or process filter: the work-tree comparison
  that `git status` did is done in Go (research D1).
- **AC3** A test repo with a planted `core.fsmonitor`, `filter.evil.clean`,
  `filter.evil.process` (with matching `.gitattributes`), `diff.external` and a textconv
  driver, each writing a marker file, is put through every git path the runner uses; no
  marker appears. The test fails on main's `git.go`.
- **AC4** The existing effects tests stay green; candidates for ordinary modified, added,
  deleted, renamed, unmerged, untracked and ignored files match what `git status` reported.
- **AC5** Install creates the config folder `0700` and `config.yaml` `0600`; on reinstall a
  looser folder or file is tightened and the summary lists it as `updated`. No other file's
  mode changes.

## Edge cases
- Unborn `HEAD` (fresh `git init`): every index entry is added.
- Sparse checkout (`skip-worktree`): an absent file is not reported; a present one is
  compared, as git ≥ 2.37 does, only when `core.sparseCheckout` is on. A
  `skip-worktree` entry without sparse checkout is not reported. `assume-unchanged`
  entries are not reported.
- A planted `.git/index` that is a FIFO, a device or huge never hangs or exhausts memory.
- Racy entries (file mtime not older than the index): hashed, not trusted by stat.
- Submodules (gitlinks) are not entered: running git in them is the same hole.
- An index the Go reader cannot parse (split index, sparse index, unknown version): a
  slower path from `git ls-files`, still without filters.
- SHA-256 repositories.
- git missing: scan mode as today.
- Config folder that already exists with other files in it: only the folder's own mode
  changes.
