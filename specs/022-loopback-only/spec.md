---
feature: 022-loopback-only
kind: fix
status: draft
version: 1
created: 2026-11-20
supersedes:
branch: fix/022-loopback-only
---
# Loopback only

## Goal
AGENTS.md says no route is reachable from outside localhost without a gateway token.
v1 has no token, so every listener must be on loopback. `listen_addr` (the API listener:
the proxy routes, the hooks ingest and `/metrics`) currently accepts any host, including
`0.0.0.0`. Captures hold prompts and tool results as they were sent, secrets included,
so binding it to every interface would expose them to the network. This fix makes the
gateway refuse to start on a non-loopback listener. Config loading, `gateway install`
and `gateway doctor` all use one check, so they always agree.

## Acceptance criteria
- **AC1** — One function decides whether a listener address is allowed. Allowed hosts are
  127.0.0.0/8, `::1` and `localhost`. Refused: `0.0.0.0`, `::`, an empty host, and any
  other IP or hostname. No name is resolved.
- **AC2** — Each listener key goes through that function: `listen_addr`,
  `ui.listen_addr` (UI on), `effects.network.http_proxy_addr` and
  `effects.network.socks_proxy_addr` (network effects on; SOCKS unless set to `""`). A
  refused value stops the gateway before anything is bound, with exit code 2. The error
  names the key, the value and the reason: v1 has no auth, and captures hold prompts and
  secrets as they were sent.
- **AC3** — `gateway doctor` reports a config with a refused listener as `fail`, with the
  same message the gateway gives.
- **AC4** — `gateway install` never writes a non-loopback address. It refuses an existing
  config that has one, with the same message, before it writes anything.
- **AC5** — `docker-compose.yml` runs the gateway on loopback, with no exception in the
  code (research D3).
- **AC6** — The docs say v1 is loopback only: `config.example.yaml`, the known limits in
  README and `docs/install.md`, a troubleshooting row in `docs/install.md` with the error
  and the fix, and a roadmap Not now entry for "remote access with auth".

## Out of scope
- An override flag or env var for a non-loopback bind. v1 has none.
- Auth, gateway tokens, TLS.
- Refusing addresses for listeners that are switched off. A disabled UI or proxy binds
  nothing, so its address stays unchecked, as before (research D2).
