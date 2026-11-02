# Claude Code harness hooks

Claude Code can report its own hook events to the gateway: each tool call it asks for,
whether the permission check ran, each tool result, sub-agents, session start and stop.
The gateway stores them next to the captured model calls, so `gateway timeline` and the
web UI can show whether a tool the model asked for actually ran.

Hooks are optional. The proxy works without them (see [claude-code.md](claude-code.md)).

## 1. Install at user scope

The hooks go in your user settings, `~/.claude/settings.json`, so they apply to every
project you run `claude` in.

The snippet sends events to the gateway's `listen_addr`, read from its config (the default
`config.yaml` if present, or `-config <path>`). Print it:

```sh
gateway hooks print            # from a checkout: go run ./cmd/gateway hooks print
```

The command writes the snippet to stdout and nothing else. It writes no file and changes
no setting, so nothing is installed until you copy it in. Run it again if `listen_addr`
changes, because the address is written into the snippet.

The snippet is a top-level `hooks` object with one entry per event. Each entry is an
`http` hook: Claude Code POSTs the event to the gateway, asynchronously, with a 5-second
timeout. Merge it into `~/.claude/settings.json` like this:

- **No `hooks` key yet:** add the snippet's `hooks` object as a top-level key.
- **A `hooks` key already exists:** add each event's entry to that event's array. Do not
  replace the `hooks` key, or your own hooks are dropped.

Save the file, then start a new `claude` session so the settings are read.

Install at **one** scope only. The same snippet at both user and project scope sends each
event twice. The gateway stores both copies; the timeline shows one when the payload has a
tool-use id or the copies arrived within a second, and two otherwise.

## 2. Coexisting with project hooks

Claude Code reads hooks from every settings file in scope: user
(`~/.claude/settings.json`), project (`.claude/settings.json`, shared in the repo) and
local (`.claude/settings.local.json`). For one event, each matching hook from each file
runs. No file replaces another.

The agent-loop kit in this repo keeps its hooks in the project `.claude/settings.json`:
a guard on `PreToolUse`, a sub-agent handler on `SubagentStop`, and a command handler on
`UserPromptExpansion`. With the gateway's hooks installed, those keep running unchanged.
The gateway's hooks never set a decision, so they neither allow nor block a tool. The
gateway does not depend on the order in which hooks from different files run.

## 3. Remove

Remove the gateway's entries from `~/.claude/settings.json`: in each event's array, drop
the entry whose hook `url` starts with `http://<listen_addr>/_gateway/hooks/`. Keep your
own entries. If the `hooks` key then holds nothing, delete it.

Check the removal with a count, which prints `0`:

```sh
grep -c '/_gateway/hooks/' ~/.claude/settings.json
```

Stopping the gateway does not remove the hooks. With the gateway down, Claude Code still
tries each event, fails the request, and carries on, so remove the entries when you no
longer run the gateway.

## 4. Match states

When a session has hook events, the gateway gives each captured tool call one state. The
timeline shows it next to the call.

| State | Meaning |
|---|---|
| `asked+ran+reported` | The tool ran and its result went back to the model. A post-tool event and the captured tool result both name the call. Carries the outcome `succeeded` or `failed`. |
| `ran-not-reported` | A post-tool event says the tool ran, but no captured tool result names the call. |
| `asked-not-run`, reason `denied` | A permission-denied event names the call: the user or a rule refused it. |
| `asked-not-run`, reason `not run` | A later request carries the call's tool result, and no post-tool event names the call. This covers rejected and interrupted calls too; the gateway does not tell them apart. |
| `pending` | The call has a pre-tool event and nothing after it, whether or not the session is still running. |
| `no harness data` | The session has no hook events at all. This does not mean the tool did not run. |

Two more labels appear beside these:

- **`input differs`** (a flag, not a state): the tool input in the pre-tool event differs
  from the input the model asked for. Usually another hook rewrote it. Both inputs are
  shown.
- **`ran-not-asked-or-not-captured`**: a hook event names a call the gateway did not
  capture, for example because the exchange had not been stored yet. The state changes to
  the matched one once the exchange is stored.

Sessions known only from hook events are listed as `hooks only`.

Two viewer texts come from hook events. They appear in the CLI timeline, in `--json` and
in the web UI:

- **`… N bytes omitted`**: the hook input was cut for display. `N` is the number of bytes
  cut. `--json` and the web entry carry the same count.
- **`hook unknown: <name>`**: the event name is not one the gateway maps. The stored name
  is shown as received.

## 5. Limits

- **Gateway down or not started:** hook events are lost. Their calls show `no harness
  data`, or the state the remaining events give. Claude Code is not blocked.
- **Permission denials:** `denied` needs a permission-denied event that names the call.
  That event was not seen in the test runs, so a denied call can show `not run` instead.
- **What is reported:** the gateway sees what Claude Code reports, not the real effect of
  a tool on the file system or network (see [PLAN.md §3](../../PLAN.md#3-client-support)).
- **Sub-agents:** their events carry an agent id and type, shown as labels on the event
  and its tool call. They are not nested under the parent's tool call.
- **Hook events with no session id (known v1 gap):** they are stored, but `gateway sessions`
  shows them only as a count. The web UI ungrouped list has no hook entries, so they are
  not visible there. A session id known from an exchange or a hook event is listed normally.
- **Capture off:** with `capture.enabled: false`, hook events are counted but not stored,
  so the timeline has nothing to show.
- **Shared writer:** hook events and LLM captures share one SQLite writer. A large hook
  burst can delay capture writes. The hook sink has its own queue (`hooks.queue_size`);
  when it is full, hook events are dropped and counted.
- **Reachability:** the ingest route answers only for a loopback `Host`, and it has no
  token in v1. Keep `listen_addr` on loopback, as for the proxy.
- **Credentials:** the gateway stores each hook body as received, but no request header of
  the ingest route, so no credential is stored.
