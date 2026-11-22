import test from "node:test";
import assert from "node:assert/strict";
import { buildRoute, parseRoute } from "../web/route.js";

test("callUrlRoundTrips", () => {
  const r = { name: "session", id: "s 1/x", call: "ex#2" };
  const h = buildRoute(r);
  assert.equal(h, "#/sessions/s%201%2Fx/call/ex%232");
  assert.deepEqual(parseRoute(h), r);
});

// research D43: a call's Headers link lands on the full exchange page's headers.
test("exchangeHeadersUrlRoundTrips", () => {
  const r = { name: "exchange", id: "e 1", focus: "headers" };
  const h = buildRoute(r);
  assert.equal(h, "#/exchanges/e%201/headers");
  assert.deepEqual(parseRoute(h), r);
});

test("oldRoutesStillParse", () => {
  assert.deepEqual(parseRoute(""), { name: "sessions", kind: "all", q: "" });
  assert.deepEqual(parseRoute("#/"), { name: "sessions", kind: "all", q: "" });
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

// spec 029: the list's kind and search live in the hash query; kind is left out when all and
// q when empty, and buildRoute is the exact inverse of parseRoute.
test("sessionsRouteCarriesKindAndSearch", () => {
  assert.equal(buildRoute({ name: "sessions", kind: "active", q: "some text" }), "#/?kind=active&q=some%20text");
  assert.deepEqual(parseRoute("#/?kind=active&q=some%20text"), { name: "sessions", kind: "active", q: "some text" });
  assert.equal(buildRoute({ name: "sessions", kind: "all", q: "" }), "#/");
  assert.equal(buildRoute({ name: "sessions" }), "#/");
  assert.equal(buildRoute({ name: "sessions", kind: "look", q: "" }), "#/?kind=look");
  assert.equal(buildRoute({ name: "sessions", kind: "all", q: "x" }), "#/?q=x");
  assert.deepEqual(parseRoute("#/?"), { name: "sessions", kind: "all", q: "" });
  assert.deepEqual(parseRoute("#/?q=x"), { name: "sessions", kind: "all", q: "x" });
});

test("sessionsRouteRoundTripsAwkwardText", () => {
  for (const q of ["a b", "a&b=c", "#hash", "100%", "%20", "a+b", "naïve 日本語 ✓", "x?y/z", " lead and trail "]) {
    for (const kind of ["all", "look", "active", "hooks"]) {
      const r = { name: "sessions", kind, q };
      const h = buildRoute(r);
      assert.deepEqual(parseRoute(h), r, h);
      assert.equal(buildRoute(parseRoute(h)), h);
    }
  }
  assert.equal(buildRoute({ name: "sessions", kind: "all", q: "a&b #%" }), "#/?q=a%26b%20%23%25");
});

test("sessionsRouteUnknownKindReadsAsAll", () => {
  assert.deepEqual(parseRoute("#/?kind=bogus&q=x"), { name: "sessions", kind: "all", q: "x" });
  assert.deepEqual(parseRoute("#/?kind="), { name: "sessions", kind: "all", q: "" });
  assert.deepEqual(parseRoute("#/?kind=ALL"), { name: "sessions", kind: "all", q: "" });
  assert.equal(buildRoute({ name: "sessions", kind: "bogus", q: "" }), "#/");
  assert.deepEqual(parseRoute("#/?other=1&kind=hooks"), { name: "sessions", kind: "hooks", q: "" });
});

test("aLongQInTheUrlIsCappedAt200BytesOnACodePointBoundary", () => {
  const bytes = (s) => new TextEncoder().encode(s).length;
  const url = (q) => `#/?q=${encodeURIComponent(q)}`;
  assert.equal(parseRoute(url("a".repeat(300))).q, "a".repeat(200));
  // 'a' + two-byte characters: byte 200 falls inside a character, which is dropped whole
  const two = "a" + "é".repeat(149);
  const q2 = parseRoute(url(two)).q;
  assert.equal(q2, "a" + "é".repeat(99));
  assert.ok(bytes(q2) <= 200);
  // four-byte characters (surrogate pairs) and three-byte ones
  for (const ch of ["😀", "€", "日"]) {
    for (const pad of ["", "a", "ab", "abc"]) {
      const q = parseRoute(url(pad + ch.repeat(100))).q;
      assert.ok(bytes(q) <= 200, `${pad}${ch}: ${bytes(q)} bytes`);
      assert.equal(q, pad + ch.repeat(Math.floor((200 - pad.length) / bytes(ch))));
      assert.ok(!q.includes("�"));
      assert.equal(decodeURIComponent(encodeURIComponent(q)), q, "valid text");
    }
  }
  assert.equal(parseRoute(url("short")).q, "short");
});
