import test from "node:test";
import assert from "node:assert/strict";
import { buildRoute, parseRoute } from "../web/route.js";

test("drawerUrlRoundTrips", () => {
  const r = { name: "session", id: "s 1/x", call: "ex#2" };
  const h = buildRoute(r);
  assert.equal(h, "#/sessions/s%201%2Fx/call/ex%232");
  assert.deepEqual(parseRoute(h), r);
});

test("oldRoutesStillParse", () => {
  assert.deepEqual(parseRoute(""), { name: "sessions" });
  assert.deepEqual(parseRoute("#/"), { name: "sessions" });
  assert.deepEqual(parseRoute("#/ungrouped"), { name: "ungrouped" });
  assert.deepEqual(parseRoute("#/sessions/abc"), { name: "session", id: "abc", call: null });
  assert.deepEqual(parseRoute("#/exchanges/e1"), { name: "exchange", id: "e1" });
  assert.equal(parseRoute("#/nope").name, "unknown");
  assert.equal(parseRoute("#/sessions/%E0%A4%A").name, "unknown");
  for (const h of ["#/", "#/ungrouped", "#/sessions/abc", "#/exchanges/e1"]) assert.equal(buildRoute(parseRoute(h)), h);
});

// 017 AC5: a call's URL names the session and the call, and round-trips; the session's own
// URL names no call.
test("callRouteNamesSessionAndCall", () => {
  assert.equal(buildRoute({ name: "session", id: "s1", call: "e7" }), "#/sessions/s1/call/e7");
  assert.deepEqual(parseRoute("#/sessions/s1/call/e7"), { name: "session", id: "s1", call: "e7" });
  assert.equal(buildRoute({ name: "session", id: "s1", call: null }), "#/sessions/s1");
});
