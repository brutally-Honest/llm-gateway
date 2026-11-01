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

// openStream opens /api/stream and calls onEvent(name, data). It returns a close function.
export function openStream(onEvent, onState, path = "stream") {
  const es = new EventSource(`/api/${path}`);
  es.onopen = () => onState("connected");
  es.onerror = () => onState("disconnected");
  for (const name of ["session", "exchange", "entries", "snapshot_error"]) {
    es.addEventListener(name, (e) => {
      try {
        onEvent(name, JSON.parse(e.data));
      } catch {
        // ignore a malformed event
      }
    });
  }
  return () => es.close();
}
