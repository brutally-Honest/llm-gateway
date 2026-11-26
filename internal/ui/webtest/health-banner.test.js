import test from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { StubElement, all, installDom } from "./dom-stub.js";
import { bannerVisible, DISMISS_KEY, dismissalStore, healthBanner, startHealthPoll } from "../web/health.js";

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
  const dismiss = all(b).find((e) => e.tagName === "button");
  assert.ok(dismiss, "a dismiss button");
  assert.equal(dismiss.attrs["aria-label"], "dismiss capture health banner");
  assert.equal(dismiss.type, "button");
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

// spec 047 U11 (D1): the dismissal decision and its storage
const since = "2026-11-26T09:00:00Z";
const answer = (lost, s = since) => ({ ok: true, status: "degraded", since: s, lost, reason: "hook events dropped" });

test("bannerVisibleDismissalDecision", () => {
  assert.equal(bannerVisible(answer(3), { since, lost: 3 }), false, "same since, same lost: hidden");
  assert.equal(bannerVisible(answer(2), { since, lost: 3 }), false, "same since, lower lost: hidden");
  assert.equal(bannerVisible(answer(4), { since, lost: 3 }), true, "lost rises: shown");
  assert.equal(bannerVisible(answer(1, "2026-11-26T10:00:00Z"), { since, lost: 3 }), true, "since differs (restart): shown");
  assert.equal(bannerVisible(answer(3), null), true, "no dismissal: shown");
  assert.equal(bannerVisible(answer(3), { since, lost: "x" }), true, "a malformed dismissal shows");
  assert.equal(bannerVisible({ ok: true, status: "ok", since, lost: 0 }, { since, lost: 0 }), false, "ok: hidden");
  assert.equal(bannerVisible({ ok: true, status: "ok", since, lost: 0 }, null), false, "ok with no dismissal: hidden");
});

class FakeStorage {
  constructor() {
    this.m = new Map();
  }
  getItem(k) {
    return this.m.has(k) ? this.m.get(k) : null;
  }
  setItem(k, v) {
    this.m.set(k, String(v));
  }
  removeItem(k) {
    this.m.delete(k);
  }
}

const throwing = {
  getItem() {
    throw new Error("blocked");
  },
  setItem() {
    throw new Error("blocked");
  },
  removeItem() {
    throw new Error("blocked");
  },
};

// withSessionStorage runs fn with globalThis.sessionStorage set to s (or a getter that throws).
async function withSessionStorage(s, fn) {
  const desc = s instanceof Error ? { get: () => { throw s; }, configurable: true } : { value: s, configurable: true, writable: true };
  Object.defineProperty(globalThis, "sessionStorage", desc);
  try {
    return await fn();
  } finally {
    delete globalThis.sessionStorage;
  }
}

test("dismissalStoreRoundTripsAndFallsBackToMemory", () => {
  const s = new FakeStorage();
  const st = dismissalStore(() => s);
  assert.equal(st.get(), null);
  st.set({ since, lost: 3 });
  assert.deepEqual(JSON.parse(s.getItem(DISMISS_KEY)), { since, lost: 3 });
  assert.deepEqual(st.get(), { since, lost: 3 });
  st.clear();
  assert.equal(s.getItem(DISMISS_KEY), null);
  assert.equal(st.get(), null);
  s.setItem(DISMISS_KEY, "not json");
  assert.equal(st.get(), null, "garbage in storage reads as no dismissal");

  const t = dismissalStore(() => throwing);
  t.set({ since, lost: 2 });
  assert.deepEqual(t.get(), { since, lost: 2 }, "throwing storage: the in-memory copy holds");
  t.clear();
  assert.equal(t.get(), null);
  const u = dismissalStore(() => {
    throw new Error("no access");
  });
  u.set({ since, lost: 1 });
  assert.deepEqual(u.get(), { since, lost: 1 });
});

test("dismissButtonHidesBannerAndStoresTheShownAnswer", async () => {
  const s = new FakeStorage();
  await withSessionStorage(s, async () => {
    const { head, shell } = await mount();
    const [b] = banners(head);
    shell.setHealth(answer(3));
    assert.equal(b.hidden, false);
    all(b).find((e) => e.tagName === "button").fire("click");
    assert.equal(b.hidden, true, "click hides it");
    assert.deepEqual(JSON.parse(s.getItem(DISMISS_KEY)), { since, lost: 3 });
    shell.setHealth(answer(3));
    assert.equal(b.hidden, true, "the same answer on a later poll stays hidden");
    shell.setHealth(answer(4));
    assert.equal(b.hidden, false, "a higher lost shows it again");
    assert.equal(s.getItem(DISMISS_KEY), null, "the stale dismissal is cleared");
    shell.setHealth(answer(4));
    assert.equal(b.hidden, false, "and stays shown on the next poll");
  });
});

test("dismissalShowsAgainOnRestartAndWorksWithThrowingStorage", async () => {
  // a restart: a new since with a lower count shows the banner again
  await withSessionStorage(new FakeStorage(), async () => {
    const { head, shell } = await mount();
    const [b] = banners(head);
    shell.setHealth(answer(5));
    all(b).find((e) => e.tagName === "button").fire("click");
    shell.setHealth(answer(1, "2026-11-26T10:00:00Z"));
    assert.equal(b.hidden, false, "new since: shown");
  });
  // sessionStorage throws: dismissal still holds for the page's lifetime
  await withSessionStorage(new Error("blocked"), async () => {
    const { head, shell } = await mount();
    const [b] = banners(head);
    shell.setHealth(answer(3));
    all(b).find((e) => e.tagName === "button").fire("click");
    assert.equal(b.hidden, true, "dismiss works when storage throws");
    shell.setHealth(answer(3));
    assert.equal(b.hidden, true, "same answer stays hidden");
    shell.setHealth(answer(4));
    assert.equal(b.hidden, false, "higher lost shows");
  });
});

// spec 047 U12 (AC4): the banner's hidden state, text and reason are written only when they change.
// writeCounter wraps a property on one node with a setter that counts the writes (the stub keeps
// the value as a plain property, so the wrap is test-only).
function writeCounter(node, prop) {
  const box = { value: node[prop], writes: 0 };
  Object.defineProperty(node, prop, {
    get: () => box.value,
    set: (v) => {
      box.writes++;
      box.value = v;
    },
    configurable: true,
    enumerable: true,
  });
  return box;
}

test("bannerWritesOnlyWhenItsTextChanges", async () => {
  const { head, shell } = await mount();
  const [b] = banners(head);
  const textNode = all(b).find((e) => e.className === "health-text");
  const reasonNode = all(b).find((e) => e.className === "health-reason");
  const hidden = writeCounter(b, "hidden");
  const text = writeCounter(textNode, "textContent");
  const reason = writeCounter(reasonNode, "textContent");
  const counts = () => ({ hidden: hidden.writes, text: text.writes, reason: reason.writes });

  let current = degraded(3);
  let tick = null;
  const stop = startHealthPoll({
    read: async () => current,
    onHealth: (h) => shell.setHealth(h),
    every: 15000,
    setTimer: (fn, ms) => {
      assert.equal(ms, 15000);
      tick = fn;
      return 1;
    },
    clearTimer: () => {},
  });
  await new Promise((r) => setImmediate(r));
  assert.equal(b.hidden, false, "the first answer shows the banner");
  assert.deepEqual(counts(), { hidden: 1, text: 1, reason: 1 }, "the first render writes each property once");

  // 60 s of polls (the start read and four 15 s ticks) with the same degraded answer: no writes
  hidden.writes = text.writes = reason.writes = 0;
  for (let i = 0; i < 4; i++) await tick();
  assert.deepEqual(counts(), { hidden: 0, text: 0, reason: 0 }, "unchanged polls do not touch the banner");

  // the count rises: the text is written once, the reason and hidden state are not
  current = degraded(5);
  await tick();
  assert.deepEqual(counts(), { hidden: 0, text: 1, reason: 0 }, "a changed count rewrites the text once");
  assert.match(textNode.textContent, /: 5 lost since start$/);

  // and the same answer again is quiet
  hidden.writes = text.writes = reason.writes = 0;
  await tick();
  assert.deepEqual(counts(), { hidden: 0, text: 0, reason: 0 });
  stop();
});
