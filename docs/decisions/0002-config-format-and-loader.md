---
status: approved
date: 2026-09-23
---

# 0002 — Config format and loader

## Context
PLAN.md §8 lists "Config: one YAML file, secrets only from env vars" as Proposed. Phase 0
(`specs/000-foundation/spec.md`) is the first feature that reads config, so the format,
the parser, the lookup rules and the env-var contract have to be settled now. AGENTS.md
forbids a new dependency without an ADR, and a YAML parser is one. The env var names are
a public contract: `docker-compose.yml`, the docs and anyone's shell setup will depend on
them.

## Decision
- **Format:** one YAML file. Secrets only ever come from env vars.
- **Parser:** `go.yaml.in/yaml/v3`, maintained by the YAML organisation.
- **Lookup:** the `-config <path>` flag; if unset, `./config.yaml` if present; if absent,
  built-in defaults. A missing file is not an error, and there is no fallback to
  `config.example.yaml`. That keeps `make run` working on a fresh clone.
- **Precedence:** defaults < file < env.
- **Env names:** prefix `GATEWAY_`. Phase 0 defines `GATEWAY_LISTEN_ADDR`,
  `GATEWAY_LOG_LEVEL` and `GATEWAY_SHUTDOWN_TIMEOUT`. Later features add keys under the
  same prefix. An env var that is set but empty (`GATEWAY_X=`) counts as unset, not as an
  invalid value.
- **No values in errors:** a config error names the key and its source (the file path or
  the env var name), never the value. Later keys will hold secrets, so the rule applies
  from the first key.
- **Visibility:** the startup log line's config source is `defaults` or the file path,
  plus the names of the keys that env overrode. Only the names are logged, never the
  values.

## Alternatives
- **`gopkg.in/yaml.v3`:** the long-standing import path, but the repository was archived
  as unmaintained on 2025-04-01. `go.yaml.in/yaml/v3` is its maintained continuation.
- **`go.yaml.in/yaml/v4`:** where new development happens, but only release candidates
  exist so far (latest v4.0.0-rc.6, June 2026). Revisit once v4.0.0 is stable.
- **Env-only config:** unreadable for the nested pricing and routing tables later phases
  need (PLAN.md §8).
- **DB-backed config:** only needed for an admin UI, which is out of scope.
- **Fall back to `config.example.yaml` when `config.yaml` is missing:** this makes the
  example file load-bearing, so editing documentation would change runtime behaviour.
- **Error when no config file exists:** this would break `make run` on a fresh clone for
  no gain, because every Phase 0 key has a safe default.

## Consequences
- Adding a setting means adding a struct field, a default, a YAML key and a `GATEWAY_`
  env name. The env name is then a contract.
- The v3 line of the parser is frozen to security fixes. Moving to v4 later is a
  one-dependency change behind the config loader.
- The startup line always shows where the config came from, which makes "which value
  won?" answerable from the logs.
- PLAN.md §8's Config row moves from Proposed to Decided and links this ADR.
- Cost of reversing: the format and parser sit behind one loader, so they are cheap to
  change. The env names are expensive: renaming one breaks every setup that uses it.

2026-11-22: the gateway's own Dockerfile and docker-compose.yml were removed in 031; the config contract no longer has a compose file depending on it.
