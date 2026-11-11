import test from "node:test";
import assert from "node:assert/strict";
import { STREAM_EVENTS } from "../web/api.js";

test("the stream listens for hooks, entries, exchange, session, effects, view and snapshot_error", () => {
  assert.deepEqual([...STREAM_EVENTS].sort(), ["effects", "entries", "exchange", "hooks", "session", "snapshot_error", "view"]);
});
