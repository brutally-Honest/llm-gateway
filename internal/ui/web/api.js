// fetch and EventSource wrappers; every URL is relative to the UI origin.
export async function getJSON(path, params = {}) {
  const q = new URLSearchParams();
  for (const [k, v] of Object.entries(params)) if (v !== undefined && v !== null) q.set(k, v);
  const qs = q.toString();
  const res = await fetch(`/api/${path}${qs ? `?${qs}` : ""}`);
  if (!res.ok) {
    let msg = res.statusText;
    try {
      msg = (await res.json()).error.message || msg;
    } catch {
      // keep the status text
    }
    throw new Error(msg);
  }
  return res.json();
}

// getBody fetches one stored body of an exchange as text. It resolves to { text, truncated }
// and rejects with an Error whose code is the API's error code, such as body_not_stored.
export async function getBody(id, part) {
  const res = await fetch(`/api/exchanges/${encodeURIComponent(id)}/body?part=${encodeURIComponent(part)}`);
  if (!res.ok) {
    let code = "";
    let msg = res.statusText;
    try {
      const body = (await res.json()).error;
      code = body.code || "";
      msg = body.message || msg;
    } catch {
      // keep the status text
    }
    const err = new Error(msg);
    err.code = code;
    throw err;
  }
  return { text: await res.text(), truncated: res.headers.get("X-Body-Truncated") === "true" };
}

// Reopen delays for a stream the browser has given up on (AC50, E12).
const firstDelay = 1000;
const maxDelay = 30000;
// snapshotRetry is the retry the server sets with snapshot_error; the page never reopens sooner.
const snapshotRetry = 30000;

// openStream opens /api/<path> and calls onEvent(name, data) and onState(state). The
// browser retries a dropped connection itself; when it gives up for good (readyState
// CLOSED, as after a 503 too_many_streams or shutting_down), a new stream is opened with
// backoff, 1 s doubling to 30 s, reset by a successful snapshot. It returns a close function.
export function openStream(onEvent, onState, path = "stream") {
  let es = null;
  let timer = null;
  let closed = false;
  let delay = firstDelay;
  let holdUntil = 0; // no reopen before this time, set by snapshot_error

  const reopenLater = () => {
    const wait = Math.max(delay, holdUntil - Date.now());
    delay = Math.min(delay * 2, maxDelay);
    timer = setTimeout(connect, wait);
  };

  function connect() {
    timer = null;
    if (closed) return;
    const cur = new EventSource(`/api/${path}`);
    es = cur;
    cur.onopen = () => onState("connected");
    cur.onerror = () => {
      if (closed || es !== cur) return;
      onState("disconnected");
      if (cur.readyState === EventSource.CLOSED) {
        cur.close();
        reopenLater();
      }
    };
    for (const name of ["session", "exchange", "entries", "snapshot_error"]) {
      cur.addEventListener(name, (e) => {
        if (closed || es !== cur) return;
        if (name === "snapshot_error") holdUntil = Date.now() + snapshotRetry;
        else delay = firstDelay; // the snapshot arrived
        try {
          onEvent(name, JSON.parse(e.data));
        } catch {
          // ignore a malformed event
        }
      });
    }
  }

  connect();
  return () => {
    closed = true;
    if (timer) clearTimeout(timer);
    if (es) es.close();
  };
}
