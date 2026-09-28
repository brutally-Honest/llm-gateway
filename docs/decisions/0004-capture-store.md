---
status: proposed
date: 2026-09-28
---

# 0004 — Capture store: SQLite plus zstd content-addressed blobs

## Context
Feature 002 (`specs/002-capture-and-canonical-events/spec.md`) makes the gateway
remember every exchange it forwards: raw bodies, redacted headers and the canonical
events parsed from them. That needs a store, and PLAN.md §9 leaves it open as OQ-1.
PLAN.md §8 also lists the capture pipeline as "Proposed", and the spec confirms it.
The forces that bound the choice:
- **Growth.** Harnesses resend the whole conversation on every turn, so a long Claude
  Code session writes many near-identical 100 KB+ request bodies (OQ-1).
- **Fidelity.** Raw bodies are kept byte for byte as they went over the wire, still
  compressed if `Content-Encoding` was set, for re-parsing later (PLAN.md §5).
- **Packaging.** One static binary, so no cgo (PLAN.md §8, Packaging).
- **Scale.** One user on one machine. Concurrent writers are the gateway's own
  workers; readers are `gateway dump` and tests.
- **Decoding.** Anthropic answers Claude Code with compressed bodies, streams
  included, and Claude Code accepts `gzip, deflate, br, zstd` (research Q1). The
  parser must decode all of them in its copy, never in the forwarded bytes.

## Decision
- **Pipeline (confirms PLAN.md §8).** An in-process bounded queue
  (`capture.queue_size`) drained by `capture.workers` goroutines. When the queue is
  full, the exchange is dropped, logged and counted, never waited for.
- **Index and events (resolves OQ-1).** SQLite, in `<capture.dir>/gateway.db`, in WAL
  mode, behind a `Store` interface that nothing outside the store package sees
  through. One writer connection owned by the store serializes all writes; the busy
  timeout is 5 s; readers open separate `mode=ro` connections, never `immutable=1`.
  The schema is migrated at startup and versioned with `PRAGMA user_version`.
- **Content.** Content-addressed by sha256: raw bodies by their wire bytes, parsed
  content by its canonical encoding. Items up to 4 KiB live inline in SQLite; larger
  ones are zstd-compressed blob files at `<capture.dir>/blobs/ab/cdef…`, written in
  full (temp file, `fsync`, rename) before any row references them.
- **Dependencies**, each added to `go.mod` by the task that first imports it, at the
  version pinned here:

  | Module | Pinned at | Licence | Used for |
  |---|---|---|---|
  | `modernc.org/sqlite` | v1.59.0 (SQLite 3.53.4) | BSD-3-Clause; the SQLite it embeds is public domain | The store, as a `database/sql` driver named `"sqlite"` |
  | `github.com/klauspost/compress` | v1.20.1 | BSD-3-Clause for the `zstd` package imported here (the module's `gzhttp` is Apache-2.0 and is not imported) | Blob compression, and the `zstd` content coding |
  | `github.com/andybalholm/brotli` | v1.2.5 | MIT | Decoding the `br` content coding only |

  `modernc.org/sqlite` is pinned at the version the Q9 and Q10 spikes ran against. The
  other two are their latest releases on 2026-09-28. `gzip`, `zlib` and raw `flate`
  come from the standard library.
- **Verified driver behaviour** (research Q9, Q10; spike on v1.59.0, go1.25.4, Linux
  amd64):
  - A `mode=ro` open of a stopped gateway's database creates `-wal` and `-shm` (with
    the database's `0600` mode) and leaves `gateway.db`'s mtime alone. With the
    gateway running it creates nothing and doesn't block the writer. So `gateway dump`
    promises only "never writes `gateway.db` or a blob".
  - A write after `gateway.db` is unlinked succeeds silently; the driver reports no
    `SQLITE_READONLY_DBMOVED`. So the store records the database's device and inode at
    open and checks them with `os.Stat` before each write transaction. A missing file
    or a changed inode fails the store for the rest of the run.

## Alternatives
- **PostgreSQL.** Strong for relational cost queries, but a server to run for one
  user on one machine. Revisit if the gateway goes multi-user; the `Store` interface
  keeps that door open.
- **MongoDB.** Bodies are JSON anyway, but it is weaker for the cost aggregations of
  Phase 5 and is also a server to run.
- **Everything in SQLite, no blob files.** Large bodies bloat the database and its
  WAL, and gain nothing over files named by their hash.
- **`ncruces/go-sqlite3`.** Also cgo-free and MIT, and fast. It replaces SQLite's OS
  layer with its own Go VFS, so the WAL and locking behaviour the store and dump rely
  on would be a second implementation's, and each connection runs in its own Wasm
  sandbox with more memory.
- **`mattn/go-sqlite3`.** Needs cgo, which the spec forbids.
- **`DataDog/zstd`, `valyala/gozstd`.** cgo.
- **`google/brotli` Go bindings.** cgo.
- **Chunked or message-level dedup of raw bodies.** Would bound raw storage, but the
  raw copy would no longer be the wire bytes, and it is more code than v1 needs. Out
  of scope for 002.
- **A synchronous capture write, or an external queue such as Kafka.** Synchronous
  writes put storage latency on every token of every stream; a broker is a whole
  service for one user (PLAN.md §8).

## Consequences
- **Easier.** Zero-ops local storage in one directory, readable with any SQLite tool.
  Identical parsed content (a resent message, the system prompt, the tools array) is
  stored once, whether inline or in a blob.
- **Trade-off: raw storage grows quadratically.** Raw bodies are stored whole, for
  fidelity and re-parsing, and only parsed content is deduplicated. Each turn's
  request carries the whole conversation so far, so raw request storage grows
  quadratically with session length. zstd absorbs most of it; retention and pruning
  are a later spec.
- **Harder.** The file-level guarantees rest on driver behaviour two spikes observed,
  and a driver upgrade could change either. AC25 (`db_deleted`) and AC57 (dump writes
  nothing) guard them. Orphan blobs from a failure between rename and insert are
  possible and are left for retention to clean up.
- **Commits us to** a single writer connection, so write throughput is one
  connection's. That is ample for one local user; several gateway instances would
  need a different store.
- **Reversing.** The engine sits behind the `Store` interface and the content hash is
  engine-independent, so another store is a new implementation plus a migration of
  the data. Dropping a dependency means replacing its one import site
  (`internal/store` or `internal/contentcoding`).
- PLAN.md §8's "Capture pipeline" and "Capture store" rows become Decided, and §9
  marks OQ-1 resolved, both linking this ADR.
