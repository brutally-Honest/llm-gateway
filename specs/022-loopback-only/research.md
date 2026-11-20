---
feature: 022-loopback-only
---
# Research & decisions — 022-loopback-only

## Open questions
<!-- Answer by changing "(open)" to "(answered)" and writing the answer underneath. -->

## Evidence (main at 43da9fe, 2026-11-20)

### Listeners: what binds, and where its address comes from

| Listener | Serves | Config key | Default | Bound at | Loopback check before this fix |
|---|---|---|---|---|---|
| API | proxy routes (`/anthropic/…`), hooks ingest (`/_gateway/hooks/<client>`), `/healthz`, `/metrics` | `listen_addr` (`GATEWAY_LISTEN_ADDR`) | `127.0.0.1:7197` (`internal/config/config.go` `Defaults`) | `cmd/gateway/run.go:235` | **none**: `setListenAddr` only checks the shape. `0.0.0.0:7197` loaded (`TestLoad_InvalidAddress` treated it as valid) |
| UI | the page, `/api/*`, `/api/health`, `/metrics` | `ui.listen_addr` (`GATEWAY_UI_LISTEN_ADDR`) | `127.0.0.1:7199` (`internal/config/ui.go` `defaultUI`) | `run.go:265`, only with `ui.enabled` | `validateUI` (spec 009 E16), only when the UI is on |
| HTTP forward proxy | CONNECT and plain `http://` | `effects.network.http_proxy_addr` | `127.0.0.1:7198` (`internal/config/effects.go` `defaultEffects`) | `run.go:697` (`startProxy`), only with effects and network on | `validateEffects` (spec 014), only when network effects are on |
| SOCKS5 proxy | SOCKS5 CONNECT | `effects.network.socks_proxy_addr` | `127.0.0.1:7195`; `""` turns it off | `run.go:697` | `validateEffects`, as above |

No other listener: `grep 'net.Listen\|ListenAndServe'` over the Go code finds only
`internal/install/real.go:124`, the installer's preflight probe. It binds
`127.0.0.1:<port>` for a moment to see whether a port is free, and is hardcoded to
loopback. `tracing.otlp_endpoint` (default `http://127.0.0.1:4318`) is an outbound
connection, not a listener. 7196 is reserved but nothing binds it.

The same loopback rule existed three times, with different coverage:
- `internal/config` `loopbackHost`: the UI and the proxies, not `listen_addr`.
- `internal/install/doctor.go` `loopback` and `nonLoopback`: `listen_addr` and the proxies,
  not the UI. `localhost` was matched case-sensitively.
- `internal/install/install.go:197`: `listen_addr` only, reusing doctor's `loopback`.

### Config keys and defaults that set an address
- `listen_addr`, `ui.listen_addr`, `effects.network.http_proxy_addr` and
  `effects.network.socks_proxy_addr`, each from the file or from its `GATEWAY_*` env var.
- Defaults: `internal/config` (above). `internal/install/install.go` `defaultConfig`
  writes the same four loopback values.
- `validAddress` refused an empty host (`:7197`) as `invalid address`, with the comment
  "must be written out as 0.0.0.0 or [::]". That is spec 000 plan step 163, which this
  fix replaces.

### Docs, examples and compose files that show a non-loopback address
- `docker-compose.yml:16`: `GATEWAY_LISTEN_ADDR: 0.0.0.0:7197`, with the host publish
  `127.0.0.1:7197:7197`. This dates from spec 000 (Container; plan line 285; AC23).
- `config.example.yaml:5-7`: "write 0.0.0.0 or [::] to listen on every interface".
- `specs/000-foundation/spec.md:106,129` and `plan.md:163,285,396,437`: history
  (status done); left as they are.
- `deploy/observability/compose.yml` publishes only on `127.0.0.1`, and
  `compose.linux.yml` binds 127.0.0.1 on the host network (`TestObservabilityStack_LoopbackOnly`).
  No change needed.
- README, `docs/install.md`, `docs/clients/claude-code.md` and `docs/observability.md`
  show only 127.0.0.1 addresses. `docs/clients/claude-code.md:9` says `docker compose up`
  listens on `127.0.0.1:7197`, which stays true (D3).

## Decisions
- D1 (2026-11-20) — Built by hand, with no agent-loop kit and no /al-* commands, as in 023.
  So spec.md stays a draft with no approval stamps, and there is no plan.md or tasks.md.
  Commit scopes: `config` (the rule and loading), `install` (install and doctor, 023 D2),
  `deploy` (compose), `server` (the startup log), `docs` and `docs(spec)`.
- D2 (2026-11-20) — "For every listener key, refused": read as every listener that
  binds. A disabled UI, or proxies with network effects off, bind nothing, so their
  addresses stay unchecked, as before. `TestLoad_UIListenAddrMustBeLoopbackAndDistinct`
  already pins this for the UI (spec 009). Refusing them too would break configs that do
  no harm. The new tests run every key with its listener on.
- D3 (2026-11-20) — docker-compose: the container runs on loopback with
  `network_mode: host` and the gateway's defaults, so there is no container exception in
  the code.
  Thesis (host network): no code path can bind `0.0.0.0`, so the rule is absolute. The
  API, UI and proxies all reach the host on 127.0.0.1, where before only 7197 was
  published. The observability stack's Linux override already does the same (spec 021 D12).
  Antithesis (an env var set by compose, for example `GATEWAY_IN_CONTAINER=1`, that
  allows the wildcard bind inside the container, with ports published on 127.0.0.1):
  it works on the default bridge network on every Docker. But it is an override in all
  but name: anyone can export it on a host and get a `0.0.0.0` bind, which the spec rules
  out ("no override flag in v1").
  Synthesis: host network. Scope check: host networking is native on Linux, but on
  Docker Desktop (macOS, Windows) it is opt-in (Settings → Resources → Network, "Enable
  host networking", 4.34 and later). Compose is the secondary path: the installer and the
  login service are the main one. If compose on Docker Desktop becomes a first-class
  target, revisit this with a container check the host cannot fake, not with an env var.
- D4 (2026-11-20) — The error names the value. ADR 0002 and the config package say
  errors never carry a value, because a value can be a secret. A listener address is not
  a secret, and the spec asks for the value. So `Error` gains a `Value` field, set only
  by the loopback refusal and only after the address has parsed as host:port with a
  numeric port. Every other error, including `invalid address`, still carries no value,
  and `TestLoad_ErrorsNeverContainValue` keeps passing unchanged. The value is
  printed with %q, so it cannot inject anything into a log line.
- D5 (2026-11-20) — An empty host (`:7197`) is now refused by the loopback rule ("not
  loopback"), not by the shape check ("invalid address"). It binds every interface,
  which is exactly what the rule is for. `0.0.0.0:7197`, valid since spec 000, is now
  refused: `TestLoad_InvalidAddress` changes accordingly.
- D6 (2026-11-20) — The one function is `config.CheckListeners(cfg)`, built on
  `config.CheckListenAddr(key, addr)`. `config.Load` calls it once file and env are
  applied, and adds the source and line. `gateway install` and `gateway doctor` call the
  same function, and `Error.Message()` is the text all three show. The `loopbackHost`
  checks in `validateUI` and `validateEffects`, and doctor's `loopback`/`nonLoopback`, go:
  CheckListeners runs first and covers them.
- D7 (2026-11-20) — `gateway doctor` takes no `-config` flag. For the real check, doctor
  runs against the copy with `HOME` pointed at a temp dir holding it, at
  `.config/llm-gateway/config.yaml`. The other checks then fail for that fake home,
  which is expected; only the config row matters.
- D8 (2026-11-20) — "README and docs/install.md known limits": neither had a known-limits
  section, so each got a short one (`### Known limits` under README's Install,
  `## Known limits` before docs/install.md's Troubleshooting). AGENTS.md is unchanged:
  its only line on this is the rule itself, which now holds.
- D9 (2026-11-20) — Karthik, at the real-check accept: docs/install.md, the Claude Code
  client page and docker-compose.yml now say the native install is the supported path,
  and that the compose gateway is limited. It sees file effects only for folders
  mounted at the same path as on the host (ADR 0014: the gateway must run on the host
  for the sensors), and on macOS it needs Docker Desktop's "Enable host networking" (D3).

## Notes
- Real check (2026-11-20, build 45a4309, accepted by Karthik). `gateway install --yes`
  upgraded a8807a7 → 45a4309, and `gateway doctor` passed every required check. A copy of
  the real config with line 9 changed to `listen_addr: 0.0.0.0:7197` was refused three
  times with the same text, `listen_addr "0.0.0.0:7197": not loopback: v1 has no auth,
  and captures hold prompts and secrets as sent; use 127.0.0.1, ::1 or localhost`:
  - `gateway -config <copy>` in the foreground: exit 2, before any bind, with key, value,
    reason and line 9;
  - `gateway doctor` with HOME on a temp home (D7): `fail Config`, exit 1;
  - `gateway install --dry-run`: "Stopped: … Nothing was written."
  The real config's sha256 was unchanged afterwards.
- Probes: disabling the loopback check in `CheckListenAddr` fails 33 config tests.
  Planting `0.0.0.0:7197` in install's default config fails
  `TestInstallWritesLoopbackOnly`. The old `docker-compose.yml` fails
  `TestCompose_GatewayLoopbackOnly`. A first probe with `0.0.0.0:9` passed, because the
  scan only matches ports of two or more digits; the default ports all have four digits.
- Docker Desktop on macOS was not tried: there is no Mac here. The compose file was
  checked with `docker compose config -q` and the conventions test, not by running it,
  because the installed gateway holds 127.0.0.1:7197.
