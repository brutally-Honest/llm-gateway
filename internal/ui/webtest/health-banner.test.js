import test from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { StubElement, all, installDom } from "./dom-stub.js";
import { healthBanner, startHealthPoll } from "../web/health.js";

const css = readFileSync(new URL("../web/style.css", import.meta.url), "utf8");

const degraded = (lost, reason = "hook events dropped, blob writes failed") => ({ ok: true, status: "degraded", lost, reason });

test("healthBannerHidesWhenOkOrUnreadable", () => {
  assert.equal(healthBanner({ ok: true, status: "ok", lost: 0 }).show, false);
  for (const bad of [undefined, null, "degraded", 7, [], {}, { status: "weird" }]) assert.equal(healthBanner(bad).show, false);
});

test("healthBannerShowsCountAndReasonWhenDegraded", () => {
  const b = healthBanner(degraded(3));
  assert.deepEqual(b, { show: true, text: "capture degraded: 3 lost since start", reason: "hook events dropped, blob writes failed" });
  assert.equal(healthBanner({ status: "degraded", lost: 1 }).reason, "");
});

function mount() {
  installDom();
  return import("../web/shell.js").then(({ mountShell }) => {
    const head = new StubElement("header");
    const root = new StubElement("html");
    const shell = mountShell({ head, storage: null, root, theme: "dark", look: "lean" });
    return { head, shell };
  });
}
const banners = (head) => all(head).filter((e) => e.className === "health-banner");

test("mountShellBannerFollowsSetHealth", async () => {
  const { head, shell } = await mount();
  assert.equal(banners(head).length, 1);
  assert.equal(banners(head)[0].hidden, true, "hidden before any answer");
  shell.setHealth(degraded(3));
  const [b] = banners(head);
  assert.equal(banners(head).length, 1);
  assert.equal(b.hidden, false);
  assert.equal(b.attrs.role, "status");
  const text = all(b).filter((e) => e.className === "health-text" || e.className === "health-reason").map((e) => e.textContent).join("");
  assert.equal(text, "capture degraded: 3 lost since start — hook events dropped, blob writes failed");
  const link = all(b).find((e) => e.tagName === "a");
  assert.equal(link.href, "/api/health");
  assert.equal(link.target, "_blank");
  assert.equal(link.rel, "noopener");
  assert.ok(link.attrs["aria-label"]);
  assert.equal(all(b).some((e) => e.tagName === "button"), false, "no close button");
  shell.setHealth(degraded(5));
  assert.equal(banners(head).length, 1, "no duplicate banner");
  assert.match(all(banners(head)[0]).find((e) => e.className === "health-text").textContent, /: 5 lost since start$/);
  shell.setHealth({ ok: true, status: "ok", lost: 0 });
  assert.equal(banners(head)[0].hidden, true);
});

test("healthPollerReadsNowAndEveryTickAndKeepsStateOnFailure", async () => {
  let tick = null;
  const cleared = [];
  let timers = 0;
  const answers = [degraded(2), new Error("down"), degraded(4)];
  let reads = 0;
  const seen = [];
  const stop = startHealthPoll({
    read: async () => {
      const a = answers[reads++];
      if (a instanceof Error) throw a;
      return a;
    },
    onHealth: (h) => seen.push(h.lost),
    every: 15000,
    setTimer: (fn, ms) => {
      timers++;
      assert.equal(ms, 15000);
      tick = fn;
      return 42;
    },
    clearTimer: (id) => cleared.push(id),
  });
  await new Promise((r) => setImmediate(r));
  assert.equal(reads, 1, "one read on start");
  await tick();
  assert.equal(reads, 2);
  assert.deepEqual(seen, [2], "a failed read reports nothing, the banner keeps its state");
  await tick();
  assert.deepEqual(seen, [2, 4]);
  assert.equal(timers, 1, "one timer");
  stop();
  assert.deepEqual(cleared, [42]);
});

test("healthPollerSkipsTickWhileReadInFlight", async () => {
  let tick = null;
  let reads = 0;
  let resolveRead = null;
  const read = () => {
    reads++;
    return new Promise((r) => {
      resolveRead = r;
    });
  };
  const seen = [];
  startHealthPoll({
    read,
    onHealth: (h) => seen.push(h.lost),
    every: 15000,
    setTimer: (fn) => {
      tick = fn;
      return 1;
    },
    clearTimer: () => {},
  });
  assert.equal(reads, 1, "one read on start");
  tick();
  tick();
  assert.equal(reads, 1, "ticks while the read is pending start nothing");
  resolveRead(degraded(2));
  await new Promise((r) => setImmediate(r));
  assert.deepEqual(seen, [2]);
  tick();
  assert.equal(reads, 2, "a settled read lets the next tick through");
});

test("healthBannerCssServesBothStylesAndThemes", () => {
  const m = css.match(/(^|\n)\.health-banner\s*\{([^}]*)\}/);
  assert.ok(m, ".health-banner has a rule");
  assert.match(m[2], /color:\s*var\(--bad\)/);
  assert.match(m[2], /background:\s*var\(--bad-bg\)/);
  assert.match(m[2], /border-radius:\s*var\(--radius/, "round in Lean, square in Stark through the radius token");
  assert.match(m[2], /flex:\s*1 0 100%/, "its own full-width row");
  assert.match(m[2], /overflow-wrap:\s*anywhere/, "wraps at narrow widths");
  assert.match(css, /:root\[data-style="stark"\] \.health-banner/);
  // --bad on --bad-bg is one of the pairs contrast.test.js checks in all four combinations
  const contrast = readFileSync(new URL("./contrast.test.js", import.meta.url), "utf8");
  assert.match(contrast, /TEXTS = \[[^\]]*"bad"/);
  assert.match(contrast, /BACKS = \[[^\]]*"bad-bg"/);
});
