---
status: done
branch: chore/foundation
---

# 000 — Foundation

## Intent
Give every later feature a working, local-only build-and-run loop: a long-running
server process, built as one binary, that serves requests until it is told to stop. It
starts with a known config, logs every request, answers a health check while running, and
on SIGINT/SIGTERM finishes in-flight requests before exiting. Plus one command to verify
the code before it is pushed. It serves no priority from PLAN.md §4 directly;
it is the ground the observability work (priority 1) is built on.

Local only. There is no CI (ADR 0001). Everything a CI pipeline would check runs on my
machine, through `make verify` and git hooks.

## Scope
- **Go module**: `go.mod` at the repo root, module path `github.com/brutally-honest/llm-gateway`
  (all lowercase). The GitHub repo was renamed to lowercase to match. The lowercase path
  is canonical; GitHub displays the owner as `brutally-Honest`. The `go` line
  in `go.mod` is the single source of truth for the Go version: `go 1.25.4`, the locally
  installed toolchain.
- **Entry point**: `cmd/gateway/main.go` — load config, build logger, build router,
  start the HTTP server, shut down gracefully on SIGINT/SIGTERM.
- **Config** (ADR 0002): YAML, parsed with `go.yaml.in/yaml/v3`. `config.example.yaml`
  committed, `config.yaml` ignored. Only what this feature needs:

  | Key | Env var | Default |
  |---|---|---|
  | `listen_addr` | `GATEWAY_LISTEN_ADDR` | `127.0.0.1:7197` |
  | `log_level` | `GATEWAY_LOG_LEVEL` | `info` |
  | `shutdown_timeout` | `GATEWAY_SHUTDOWN_TIMEOUT` | `30s` |

  - Lookup: the `-config <path>` flag; if unset, `./config.yaml` if present; if absent,
    built-in defaults. A missing file is not an error, and there is no fallback to
    `config.example.yaml`.
  - Precedence: defaults < file < env.
  - Durations use Go syntax (`30s`).
  - The env contract (names, empty values, no values in errors) is ADR 0002's Decision.
    Secrets will only ever come from env vars.
  - **Invalid config fails fast.** Malformed YAML, an unknown key (the decoder runs with
    `KnownFields(true)`), an invalid log level, an invalid duration (including zero or
    negative) or an invalid listen address (an empty host, or a port that is not an
    integer from 0 to 65535) makes the gateway exit non-zero before it binds the port, with one JSON error
    line built as the Don't below describes.
  - Errors that happen before config loads go through a bootstrap zap logger at `info`,
    so they are JSON too.
- **Logging**: zap, structured JSON to stdout; level from config.
  - Request logging is zap-based; chi's `middleware.Logger` is not used.
  - Panic recovery is zap-based; chi's `middleware.Recoverer` is not used.
  - `http.Server.ErrorLog` is routed through zap.
  - Nothing writes plain text to stdout or stderr.
  - That rule excludes Go runtime crash output (unrecovered panics outside handlers,
    fatal errors, SIGQUIT dumps), which the runtime writes directly; see `plan.md` Risks.
- **Router**: chi, with request-ID and panic-recovery middleware.
- **Request ID**: always generated server-side; an incoming `X-Request-Id` is ignored as
  untrusted. The generated ID is returned in the `X-Request-Id` response header and
  logged on the request log line as `request_id`.
- **Health**: `GET /healthz` → `200` with `{"status":"ok"}`.
- **Version**: set at build time with `-ldflags -X` from
  `git describe --tags --always --dirty`; `dev` when that is unavailable (for example, a
  Docker build with no `.git` in the context). The Makefile passes it into the Docker
  build as a build arg.
- **Startup and shutdown lines**: one startup line with `version`, the listen address and
  the config source. The config source is `defaults` or the file path, plus the names
  (never the values) of the keys that env overrode. One shutdown line.
- **Graceful shutdown**: on SIGINT/SIGTERM, stop accepting new requests and wait up to
  `shutdown_timeout` for in-flight requests. If they finish, log the shutdown line and
  exit `0`. If the timeout passes, log one error line with the in-flight count and exit
  non-zero.
- **Make targets**:
  - `make setup`: idempotent. Sets `core.hooksPath`, `notes.rewriteRef`, and the notes
    fetch refspec (only if it is not already present), and installs the pinned
    golangci-lint binary into `./bin`. It does not push notes.
  - `make build`, `make run`
  - `make image`: builds the Docker image, passing the version as a build arg
  - `make test`: `go test -race ./...`
  - `make lint`: golangci-lint with a committed config
  - `make verify`: lint, then test; non-zero exit on any failure
- **Local gate instead of CI** (ADR 0001): a `pre-push` hook in `.githooks/` that blocks
  the push if any of these checks fails:
  - **Clean tree.** Tracked files have no uncommitted changes, and each pushed local sha
    is `HEAD`. This makes `make verify` check exactly what is pushed, without stashing
    or a temporary worktree.
  - **`make verify`** passes.
  - **Commit messages.** Every commit being pushed passes `.githooks/commit-msg`. This
    restores PLAN.md §11's backstop for `git commit --no-verify`. The check logic stays
    only in `commit-msg`: for each commit, the hook writes `git log -1 --format=%B` to a
    temp file and runs `.githooks/commit-msg` on it.

  Every check applies only to `refs/heads/*`. The hook skips `refs/notes/*` and
  `refs/tags/*` entirely: they are not `HEAD`, and notes commits carry git's own
  messages.

  The range comes from each stdin line (`<local ref> <local sha> <remote ref> <remote sha>`):
  - Deletion (local sha is all zeros): skipped.
  - New branch (remote sha is all zeros): `git rev-list <local sha> --not --remotes=origin`.
  - Otherwise: `<remote sha>..<local sha>`.

  `git push --no-verify` bypasses the hook; this is accepted (ADR 0001).
- **Container**: a `Dockerfile` (multi-stage, static binary) and a `docker-compose.yml`
  with the gateway service only.
  - The builder image's Go version matches `go.mod`, with a comment pointing there.
  - Inside the container the gateway must listen on `0.0.0.0:7197` (set via
    `GATEWAY_LISTEN_ADDR` in compose), because a container's own loopback is unreachable
    from the host.
  - The host-side publish is `127.0.0.1:7197:7197`, so it is still loopback-only on my
    machine.
- **`.gitignore`**: add `config.yaml`.
- **README.md**: the Setup section points to `make setup` instead of listing the git
  config commands.
- **AGENTS.md**: fill the `install`, `verify` and `run` commands with the real, tested
  ones.

## Out of scope
- CI pipelines of any kind (GitHub Actions or others).
- Any proxy route, protocol adapter, client profile or upstream config (001).
- Capture, storage, Redis, Prometheus, Grafana, Jaeger. Each is added to compose by the
  phase that first needs it, not before.
- Empty placeholder packages for `internal/core`, `protocols`, `clients`, `providers`.
  Each package is created by the feature that first puts code in it.
- Auth of any kind.
- A compose healthcheck. Distroless has no curl, and a probe mode isn't worth it yet.
- Pushing git notes from `make setup`.

## Do
- Bind to `127.0.0.1:7197` by default. Listening on `0.0.0.0` needs an explicit config
  change. Reason: the gateway has no auth until the Cursor tunnel work (003), and later it
  will hold captured prompts. Loopback-only means nothing else on the network can reach it.
- Pin tool versions:
  - Go in `go.mod`.
  - golangci-lint in the Makefile, installed as a binary into `./bin` and not through the
    `go.mod` `tool` directive (ADR 0001).
- Keep `make verify` under 30s with a warm cache on an empty codebase, so it can run on
  every push.
- Fail fast on invalid config, before binding the port. Name the bad key and its source,
  never its value (ADR 0002).
- Keep `main.go` thin: wiring only, no logic that needs its own test.
- Config, logging and server code do not go in `internal/core/`. The exact packages are
  for `plan.md` to decide.

## Don't
- Don't add a dependency beyond chi, zap (PLAN.md §8) and `go.yaml.in/yaml/v3`
  (ADR 0002) without a new ADR.
- Don't use a global logger or global config. Pass them in, because later features must
  be testable in isolation.
- Don't log the `err.Error()` of any parser or validator for config failures.
  yaml.v3, `time.ParseDuration`, `strconv.Atoi` and `net.SplitHostPort` all put the
  value in their messages. Build the error line from the key, its source and a fixed
  reason (for example `unknown key`, `invalid duration`). The full set of reasons is
  defined in plan.md's Config steps. For malformed YAML, log only the file path and
  line number.
- Don't log headers or bodies in the request log line (PLAN.md §5: secrets never land in
  storage, and logs count).
- Don't let the pre-push hook edit files (no auto-format, no stash on push). It only checks.
- Don't copy the commit-message check into the pre-push hook. It calls `.githooks/commit-msg`.
- Don't commit `config.yaml`, `.env` or any secret.

## Agnosticism check
Touches none of the three axes: there is no client, protocol or provider code yet.
`internal/core/` does not exist yet, and this feature does not create it.

## Open questions resolved here
- PLAN.md §7 and §11 assumed CI → `docs/decisions/0001-local-gate-instead-of-ci.md`.
- PLAN.md §8 "Config: one YAML file, secrets only from env" (Proposed) →
  `docs/decisions/0002-config-format-and-loader.md`.

## Acceptance criteria
- **AC1** `make build` produces a single static binary.
- **AC2** `make run` starts the gateway; `GET http://127.0.0.1:7197/healthz` returns
  `200 {"status":"ok"}`.
- **AC3** On startup the gateway logs one line with `version`, the listen address and
  the config source.
- **AC4** With no `config.yaml` and no `-config` flag, the gateway starts on the built-in
  defaults, and the startup line's config source is `defaults`.
- **AC5** With no config change, the bound listener address is `127.0.0.1:7197`.
- **AC6** For each of `GATEWAY_LISTEN_ADDR`, `GATEWAY_LOG_LEVEL` and
  `GATEWAY_SHUTDOWN_TIMEOUT`, the env value overrides the value set in the config file,
  and the startup line names the overridden key.
- **AC7** Setting `log_level` in the file changes which lines are emitted, and so does
  setting `GATEWAY_LOG_LEVEL`.
- **AC8** An unknown key in the config file makes the gateway exit non-zero before it
  binds the port, with a JSON error line that names the key and the file path.
- **AC9** An invalid `GATEWAY_LOG_LEVEL` makes the gateway exit non-zero before it binds
  the port, with a JSON error line that names `GATEWAY_LOG_LEVEL`.
- **AC10** An invalid `GATEWAY_SHUTDOWN_TIMEOUT` (for example `abc`) makes the gateway
  exit non-zero before it binds the port, with a JSON error line that names
  `GATEWAY_SHUTDOWN_TIMEOUT`.
- **AC11** In AC8, AC9 and AC10, the invalid value never appears in the output (in
  AC10, `abc` appears nowhere).
- **AC12** With `GATEWAY_LOG_LEVEL=` set but empty, the gateway starts normally, using
  the file's `log_level` if present, else the default.
- **AC13** Every log line is valid JSON with `level`, `ts` and `msg`; request logs carry a
  `request_id`.
- **AC14** A request whose `Authorization` and `x-api-key` headers carry a sentinel
  string produces log output with no occurrence of that sentinel.
- **AC15** SIGTERM stops the server gracefully: in-flight requests finish, then the process
  exits `0` and logs a shutdown line.
- **AC16** If in-flight requests are still running when `shutdown_timeout` passes, the
  gateway logs one error line with the in-flight count and exits non-zero.
- **AC17** A panicking handler returns `500` and logs a JSON error line, with no
  plain-text stack on stdout or stderr. The server keeps serving.
- **AC18** Every response carries an `X-Request-Id` header. A request that sends its own
  `X-Request-Id` gets a different, server-generated one.
- **AC19** `make verify` runs lint and `go test -race ./...`, and exits non-zero if either
  fails.
- **AC20** After `make setup`, the pre-push hook rejects each of these:
  - a push with a malformed commit subject anywhere in the pushed range;
  - a push where `make verify` fails;
  - a push with uncommitted changes to tracked files;
  - a push whose local sha is not `HEAD`.
- **AC21** After `make setup`, the pre-push hook allows each of these:
  - a branch deletion;
  - a push of a clean, valid range;
  - a push of `refs/notes/commits`;
  - a push of a tag.
- **AC22** Running `make setup` a second time leaves git config unchanged. In
  particular, the notes fetch refspec is not added twice.
- **AC23** `docker compose up` serves `/healthz` on `127.0.0.1:7197`.
- **AC24** The AGENTS.md `install`, `verify` and `run` commands are filled, and each one
  runs as written on a fresh clone.

## Open questions
- [OPEN] Shutdown for long SSE streams: a 30s `shutdown_timeout` will cut streams that
  run longer. Feature 001 must revisit this when it adds streaming routes.

> Note (2026-11-26, spec 048): PLAN.md §10 and §11 moved to AGENTS.md (Workflow, Commits, Scopes, Enforcement). A citation of either section above now points there. Nothing else in this file changed.
