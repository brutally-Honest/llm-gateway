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

// repeat is setInterval; under node (the webtests) the timer is unref'd so a page that is
// imported there can still exit. A browser's timer id is a number and is left alone.
function repeat(fn, ms) {
  const id = setInterval(fn, ms);
  if (id && typeof id.unref === "function") id.unref();
  return id;
}

// startHealthPoll reads health once now and again every `every` ms, and hands each good answer
// to onHealth. A failed read changes nothing, so the banner stays as it was. One timer per
// call; the returned function stops it. The fetcher and timers are injectable for tests.
export function startHealthPoll({ read, onHealth, every = HEALTH_EVERY_MS, setTimer = repeat, clearTimer = clearInterval }) {
  const tick = async () => {
    try {
      onHealth(await read());
    } catch {
      // keep the last state: no flicker, no error banner
    }
  };
  tick();
  const id = setTimer(tick, every);
  return () => clearTimer(id);
}
