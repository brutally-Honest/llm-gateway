---
status: done
spec: ./spec.md
plan: ./plan.md
tasks: ./tasks.md
---

# NNN — Research

The feature's working memory: doubts, roadblocks and limits, with their answers.

What belongs here, when to escalate an answer to `spec.md`, `plan.md` or an ADR, and
the never-delete rule: `AGENTS.md` (Workflow; it replaced PLAN.md §10 in 048). Don't restate them here.

---

## Q1 — Do the hook tests need make's variables stripped, not only `GIT_*`?
- Status: answered     Level: technical
- Blocks / shapes: T1 (`cleanEnv`, already strips them), T2 (shaped)
- Context: plan.md's Testing strategy only removes `GIT_*` from the children's
  environment. But `make verify` runs `go test` under make, so `MAKEFLAGS`, `MFLAGS`
  and `MAKELEVEL` reach the `make verify` that the temp repo's hook runs against the
  stub Makefile. GNU Make 4.4.1, git 2.53.0, go 1.25.4.
- Question: does anything the outer make exports change what the stub's `verify`
  does, so that a hook test passes or fails for the wrong reason?
- Answer: yes. Experiment, 2026-09-26: with the three names taken out of `cleanEnv`'s
  strip list and `GOFLAGS=-count=1` (the test cache does not key on `MAKEFLAGS`, so a
  cached run proves nothing), `make -i test` fails
  `TestPrePush_Rejects/verify_fails` with "push succeeded, want it rejected": `-i`
  reaches the stub through `MAKEFLAGS` and its `exit 1` is ignored. `make -k`,
  `-j4` and `-s` pass. With the names stripped, `make -i test` and `make -j4 test`
  pass.
- Outcome: `cleanEnv` keeps stripping `MAKEFLAGS`, `MFLAGS` and `MAKELEVEL`, and
  plan.md's Testing strategy now lists them. The same leak reaches the real hook: see
  Q2.
- 2026-09-26: `GNUMAKEFLAGS` is stripped too (GNU make reads it like `MAKEFLAGS`), and
  plan.md's list includes it.

## Q2 — Should the pre-push hook clear an inherited `MAKEFLAGS` before `make verify`?
- Status: answered     Level: technical
- Blocks / shapes: nothing yet (T2's ACs pass as planned)
- Context: found during Q1. The hook runs `make verify` in whatever environment
  `git push` was started from. With a lint failure in the tree, 2026-09-26:
  `make verify` exits 2, but `MAKEFLAGS=i make verify` exits 0. So a push started from
  a shell or make recipe that exports `MAKEFLAGS` with `-i` gets through the gate
  with a failing check.
- Question: should the hook run `MAKEFLAGS= MFLAGS= make verify`, so the gate
  can't be relaxed from outside? That changes plan.md's hook description and needs a
  test (`TestPrePush_Rejects/verify_fails_makeflags`).
- Answer: yes, and `GNUMAKEFLAGS` and `GOFLAGS` with it: `GOFLAGS=-run=^$` makes
  `go test` run nothing and exit 0. `TestPrePush_Rejects/verify_fails_despite_env_flags`
  pushes with `MAKEFLAGS=i`, `GNUMAKEFLAGS=-i` and `GOFLAGS=-run=^$` over a failing
  stub `verify`. Against a hook running plain `make verify` the push succeeds and the
  case fails; with the four cleared it is rejected (2026-09-26).
- Outcome: hook clears MAKEFLAGS, MFLAGS, GNUMAKEFLAGS, GOFLAGS; escalated to plan.

## Q3 — What counts as an empty document, and can step 4 fail after step 3?
- Status: answered     Level: technical
- Blocks / shapes: T3 (shaped)
- Context: plan.md Config steps 2–4 treat only the zero node (an empty or
  comment-only file) as an empty document, and say step 4 "cannot fail on today's
  keys" after step 3. Probed `go.yaml.in/yaml/v3 v3.0.5` with go 1.25.4, 2026-09-26.
- Question: which files hit step 3's `not a mapping` and step 4's `invalid config`
  that shouldn't, or can't?
- Answer: a bare `---` or `~` gives a document whose root is a `!!null` scalar, not a
  mapping; it sets no keys, so it is an empty document, not `not a mapping`. A key with
  a null value (`log_level:` or `log_level: ~`) decodes to a nil `*string`, so the key
  keeps its default, the same as an empty env var counts as unset (ADR 0002). Step 4
  **can** fail after step 3: `log_level: !!int abc` is a scalar, so the walk passes,
  and the decoder fails with ``cannot decode !!str `abc` as a !!int``, which quotes the
  value. That is the `invalid config` path, and it is reachable, so
  `TestLoad_ErrorsNeverContainValue` covers it. A malformed second document fails the
  second `Decode` and gives `multiple documents`.
- Outcome: `documentRoot` treats a `!!null` root as empty; `TestLoad_EmptyFile` covers
  `---` and null values. plan.md steps 3 and 4 corrected.

## Q4 — Does yaml.v3's `line N` always point at the bad line?
- Status: won't fix     Level: limit
- Blocks / shapes: T3 (shaped)
- Context: `TestLoad_MalformedYAMLReportsLine` first used
  `log_level: info\n\nlisten_addr: [127.0.0.1\n` (bad line 3) and got line 2.
  go.yaml.in/yaml/v3 v3.0.5, 2026-09-26.
- Question: is the line pulled from the syntax error the offending line?
- Answer: not always. Unclosed flow collections (`[`, `{`) and `did not find expected
  key` report the line before; `mapping values are not allowed`, bad indentation and
  an unterminated quote at end of stream report the bad line (or the one after, for end
  of stream). The number is yaml.v3's own, taken from its message.
- Outcome: the loader passes yaml.v3's number through unchanged, rather than guessing a
  correction per message. The test pins an error whose line is exact. plan.md's Risks
  bullet on the syntax-error line notes it.

## Q5 — What does Load return when the file exists but can't be read?
- Status: answered     Level: technical
- Blocks / shapes: T3 (shaped)
- Context: plan.md step 1 names only `file not found`, for a missing explicit path.
  `os.ReadFile` can also fail on a directory, a permission error or an I/O error, and
  the default path can hit these too.
- Question: which reason, and does a default path that exists but can't be read fall
  back to defaults?
- Answer: a new fixed reason, `cannot read file`, with the path as the source, for any
  read error other than "does not exist", on either path. A missing default path still
  means defaults (ADR 0002), but an unreadable one is an error: silently ignoring a
  `config.yaml` that is present would run the gateway on settings nobody chose.
- Outcome: `readFile` returns `cannot read file`; `TestLoad_ErrorsNeverContainValue`
  covers it with a directory as the path. plan.md step 1 lists the reason.

## Q6 — What does `logging.New` do with a level it doesn't know?
- Status: answered     Level: technical
- Blocks / shapes: T4 (shaped)
- Context: plan.md gives `New(w io.Writer, level string) *zap.Logger`, with no error
  return. `zapcore.ParseLevel` also accepts `dpanic`, `panic` and `fatal`, which
  config rejects. zap v1.28.0, 2026-09-26.
- Question: an unknown level can only come from a caller bug, since config validates
  it first. Panic, or fall back?
- Answer: fall back to `info`. A panic here would print a plain-text trace to stderr,
  which breaks "nothing writes plain text" at the point where the logger that would
  report it doesn't exist yet. `TestNew_Level` pins the fallback. zap's error output
  is bound to `os.Stderr` when `New` runs, so a test that swaps `os.Stderr` (AC17)
  must do so before building the logger.
- Outcome: none beyond the code comment on `New`; the signature stays as planned.

## Q7 — How much error handling does T6's `run` carry, given T7–T9 own the error paths?
- Status: answered     Level: flow
- Blocks / shapes: T6 (shaped), T7, T8, T9
- Context: T6 is the happy path, and T7 (config and bind errors), T8 (mounts) and T9
  (shutdown timeout) each have their own `feat` commit. But `run` must handle every
  error it gets in T6: errcheck fails lint on an ignored one, and carrying on after a
  failed bind or a failed `Shutdown` would be wrong. 2026-09-26.
- Question: what goes in T6, and what is left for T7–T9?
- Answer: T6 handles each error with the smallest correct branch: one JSON line and the
  exit code from plan.md's table (`invalid flags` and `invalid config` with the
  `*config.Error` fields → `2`; `cannot bind` with the key, `serve failed` and
  `shutdown failed` → `1`). Left for later: T7 splits `cannot bind` into
  `address in use` / `bind failed` and adds the config-path tests; T8 adds
  `deps.mount`; T9 turns `shutdown failed` into `shutdown timed out` with
  `in_flight`, then `srv.Close()`. `gateway -h` currently exits `2` with
  `invalid flags` (`flag.ErrHelp` is a flag error); T7's flag handling is where that
  is decided.
- Outcome: no plan change; each later task's Done list still proves its branch.
- 2026-09-26: decided for T7: `-h` / `-help` (`flag.ErrHelp`) exits `0` with one
  `info` line, `msg` `usage`, field `flags: ["-config <path>"]`. Nothing is loaded or
  bound. Every other flag error stays `invalid flags`, exit `2`, with no flag-package
  text (it can quote the value). `TestRun_Help` pins it; plan.md step 2 updated.

## Q8 — Does a warm `make verify` meet the spec's 30s goal?
- Status: answered     Level: limit
- Blocks / shapes: T19 (shaped)
- Context: T19 times `make verify` with a warm cache against the 30s goal. Two cases
  matter: a push with no code change, where every test result is cached, and a push
  with a code change, where the tests run again. 2026-09-26.
- Question: is either case over 30s, so that T20 (`TestMain` shares the binary build)
  is needed?
- Answer: no. Warm, all test results cached: 0.39s. After `go clean -testcache`, so
  every test runs: 2.75s. Both are well under 30s.
- Outcome: goal met; no T20 is added.
