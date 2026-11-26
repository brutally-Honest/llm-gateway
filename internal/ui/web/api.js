// UNREACHABLE is the message the page shows when the gateway did not answer at all: the
// request failed, or the reply was cut off or was not JSON.
export const UNREACHABLE = "The gateway is not answering.";

// unreachable makes the error for a request that got no usable answer; cause is what the browser threw.
function unreachable(cause) {
  const err = new Error(UNREACHABLE, { cause });
  err.code = "unreachable";
  return err;
}

// fetch and EventSource wrappers; every URL is relative to the UI origin.
export async function getJSON(path, params = {}) {
  const q = new URLSearchParams();
  for (const [k, v] of Object.entries(params)) if (v !== undefined && v !== null) q.set(k, v);
  const qs = q.toString();
  let res;
  try {
    res = await fetch(`/api/${path}${qs ? `?${qs}` : ""}`);
  } catch (cause) {
    throw unreachable(cause);
  }
  if (!res.ok) {
    let msg = res.statusText;
    try {
      msg = (await res.json()).error.message || msg;
    } catch {
      // keep the status text
    }
    const err = msg ? new Error(msg) : unreachable();
    err.status = res.status;
    throw err;
  }
  try {
    return await res.json();
  } catch (cause) {
    throw unreachable(cause);
  }
}

// getBody fetches one stored body of an exchange as text. It resolves to { text, truncated }
// and rejects with an Error whose code is the API's error code, such as body_not_stored.
export async function getBody(id, part) {
  let res;
  try {
    res = await fetch(`/api/exchanges/${encodeURIComponent(id)}/body?part=${encodeURIComponent(part)}`);
  } catch (cause) {
    throw unreachable(cause);
  }
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
  let text;
  try {
    text = await res.text();
  } catch (cause) {
    throw unreachable(cause);
  }
  return { text, truncated: res.headers.get("X-Body-Truncated") === "true" };
}

// STREAM_EVENTS are the named events the server sends on /api/.../stream. openStream listens
// for each of them; app.js routes them.
export const STREAM_EVENTS = ["session", "exchange", "hooks", "entries", "effects", "view", "snapshot_error"];

// Reopen delays for a stream the browser has given up on (AC50, E12).
const firstDelay = 1000;
const maxDelay = 30000;
// snapshotRetry is the retry the server sets with snapshot_error; the page never reopens sooner.
const snapshotRetry = 30000;

// openStream opens /api/<path> and calls onEvent(name, data) and onState(state). The
// browser retries a dropped connection itself; when it gives up for good (readyState
// CLOSED, as after a 503 too_many_streams or shutting_down), a new stream is opened with
// backoff, 1 s doubling to 30 s, reset by a successful snapshot. States are connected,
// disconnected and snapshot_error; after snapshot_error no other state is reported until a
// later snapshot succeeds (E22). It returns a close function.
export function openStream(onEvent, onState, path = "stream") {
  let es = null;
  let timer = null;
  let closed = false;
  let delay = firstDelay;
  let holdUntil = 0; // no reopen before this time, set by snapshot_error
  let snapshotFailed = false; // set by snapshot_error, cleared by the next successful snapshot
  // the connection state is not reported while a snapshot error stands (E22)
  const report = (state) => {
    if (!snapshotFailed) onState(state);
  };

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
    cur.onopen = () => report("connected");
    cur.onerror = () => {
      if (closed || es !== cur) return;
      report("disconnected");
      if (cur.readyState === EventSource.CLOSED) {
        cur.close();
        reopenLater();
      }
    };
    for (const name of STREAM_EVENTS) {
      cur.addEventListener(name, (e) => {
        if (closed || es !== cur) return;
        if (name === "snapshot_error") {
          holdUntil = Date.now() + snapshotRetry;
          snapshotFailed = true;
          onState("snapshot_error");
        } else {
          delay = firstDelay; // the snapshot arrived
          if (snapshotFailed) {
            snapshotFailed = false;
            onState("connected");
          }
        }
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
