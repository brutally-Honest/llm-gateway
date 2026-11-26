// The capture-health banner (spec 045 AC7, D4, D10): the pure text builder and a small poller.

export const HEALTH_EVERY_MS = 15000;

// healthBanner turns a parsed /api/health object into what the banner shows. Anything that is
// not a degraded answer (ok, missing, garbage) shows nothing.
export function healthBanner(h) {
  if (!h || typeof h !== "object" || h.status !== "degraded") return { show: false, text: "", reason: "" };
  const lost = Number.isFinite(h.lost) ? h.lost : 0;
  return {
    show: true,
    text: `capture degraded: ${lost} lost since start`,
    reason: typeof h.reason === "string" ? h.reason : "",
  };
}

// The sessionStorage key for a dismissal of the banner (spec 047 U11, D1).
export const DISMISS_KEY = "llmgw.healthDismissed";

const lostOf = (h) => (Number.isFinite(h && h.lost) ? h.lost : 0);

// bannerVisible decides whether the banner shows for the answer h, given the dismissal stored
// for this tab ({since, lost} of the answer that was dismissed, or null). Only a degraded answer
// can show. A dismissal hides it while the answer has the same `since` and no more lost than the
// dismissed count; a new `since` (the gateway restarted) or a higher count shows it again.
export function bannerVisible(h, dismissed) {
  if (!healthBanner(h).show) return false;
  if (!dismissed || typeof dismissed !== "object" || !Number.isFinite(dismissed.lost)) return true;
  if (h.since !== dismissed.since) return true;
  return lostOf(h) > dismissed.lost;
}

// parseDismissal reads a stored dismissal, or null when the stored text is not one.
function parseDismissal(raw) {
  if (typeof raw !== "string") return null;
  try {
    const d = JSON.parse(raw);
    return d && typeof d === "object" && Number.isFinite(d.lost) ? { since: d.since, lost: d.lost } : null;
  } catch {
    return null;
  }
}

// dismissalStore keeps the dismissal in sessionStorage, with an in-memory copy for when the
// storage is missing or throws (blocked site data, a private window). Every access is guarded,
// so a failing storage never throws out of here. getStore is injectable for tests.
export function dismissalStore(getStore = () => globalThis.sessionStorage) {
  let mem = null;
  const store = () => {
    try {
      return getStore() || null;
    } catch {
      return null;
    }
  };
  return {
    get() {
      try {
        const s = store();
        if (s) return parseDismissal(s.getItem(DISMISS_KEY)) || mem;
      } catch {
        // fall through to the in-memory copy
      }
      return mem;
    },
    set(d) {
      mem = { since: d.since, lost: d.lost };
      try {
        const s = store();
        if (s) s.setItem(DISMISS_KEY, JSON.stringify(mem));
      } catch {
        // the in-memory copy still holds it for this page's lifetime
      }
    },
    clear() {
      mem = null;
      try {
        const s = store();
        if (s) s.removeItem(DISMISS_KEY);
      } catch {
        // nothing stored to remove
      }
    },
  };
}

// repeat is setInterval; under node (the webtests) the timer is unref'd so a page that is
// imported there can still exit. A browser's timer id is a number and is left alone.
function repeat(fn, ms) {
  const id = setInterval(fn, ms);
  if (id && typeof id.unref === "function") id.unref();
  return id;
}

// startHealthPoll reads health once now and again every `every` ms, and hands each good answer
// to onHealth. A failed read changes nothing, so the banner stays as it was. A tick is skipped
// while the previous read is still running, so a hung /api/health never piles up fetches. One
// timer per call; the returned function stops it. The fetcher and timers are injectable for tests.
export function startHealthPoll({ read, onHealth, every = HEALTH_EVERY_MS, setTimer = repeat, clearTimer = clearInterval }) {
  let busy = false;
  const tick = async () => {
    if (busy) return;
    busy = true;
    try {
      onHealth(await read());
    } catch {
      // keep the last state: no flicker, no error banner
    } finally {
      busy = false;
    }
  };
  tick();
  const id = setTimer(tick, every);
  return () => clearTimer(id);
}
