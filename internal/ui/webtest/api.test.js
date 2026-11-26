import test from "node:test";
import assert from "node:assert/strict";
import { STREAM_EVENTS, UNREACHABLE, getBody, getJSON } from "../web/api.js";

test("the stream listens for hooks, entries, exchange, session, effects, view and snapshot_error", () => {
  assert.deepEqual([...STREAM_EVENTS].sort(), ["effects", "entries", "exchange", "hooks", "session", "snapshot_error", "view"]);
});

// withFetch puts fake in place of fetch for the call fn makes, and restores the real one after.
async function withFetch(fake, fn) {
  const orig = globalThis.fetch;
  globalThis.fetch = fake;
  try {
    await fn();
  } finally {
    globalThis.fetch = orig;
  }
}

const unreachableErr = (err) => {
  assert.equal(err.message, "The gateway is not answering.");
  assert.equal(err.message, UNREACHABLE);
  assert.equal(err.code, "unreachable");
  return true;
};

test("getJSON turns a network failure into the unreachable error and keeps the cause", async () => {
  const cause = new TypeError("Failed to fetch");
  await withFetch(async () => { throw cause; }, async () => {
    await assert.rejects(getJSON("health"), (err) => unreachableErr(err) && err.cause === cause);
  });
});

test("getJSON turns an OK reply that is not complete JSON into the unreachable error", async () => {
  const cause = new SyntaxError("Failed to execute 'json' on 'Response': Unexpected end of JSON input");
  const res = { ok: true, status: 200, statusText: "OK", json: async () => { throw cause; } };
  await withFetch(async () => res, async () => {
    await assert.rejects(getJSON("sessions"), (err) => unreachableErr(err) && err.cause === cause);
  });
});

test("getJSON keeps the API's error message on a non-OK reply", async () => {
  const res = { ok: false, status: 404, statusText: "Not Found", json: async () => ({ error: { message: "session not found" } }) };
  await withFetch(async () => res, async () => {
    await assert.rejects(getJSON("sessions/x"), { message: "session not found", status: 404 });
  });
});

test("getJSON on a non-OK reply with no JSON and no status text is the unreachable error, with its status", async () => {
  const res = { ok: false, status: 502, statusText: "", json: async () => { throw new SyntaxError("x"); } };
  await withFetch(async () => res, async () => {
    await assert.rejects(getJSON("sessions"), (err) => unreachableErr(err) && err.status === 502);
  });
});

test("getJSON resolves to the parsed JSON on an OK reply", async () => {
  const res = { ok: true, status: 200, statusText: "OK", json: async () => ({ items: [1, 2] }) };
  await withFetch(async () => res, async () => {
    assert.deepEqual(await getJSON("sessions"), { items: [1, 2] });
  });
});

test("getBody turns a network failure into the unreachable error", async () => {
  await withFetch(async () => { throw new TypeError("Failed to fetch"); }, async () => {
    await assert.rejects(getBody("ex1", "request"), unreachableErr);
  });
});

test("getBody turns an OK reply whose text cannot be read into the unreachable error", async () => {
  const res = { ok: true, status: 200, statusText: "OK", headers: { get: () => null }, text: async () => { throw new TypeError("body cut off"); } };
  await withFetch(async () => res, async () => {
    await assert.rejects(getBody("ex1", "request"), unreachableErr);
  });
});
