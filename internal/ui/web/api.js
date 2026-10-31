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

// openStream opens /api/stream and calls onEvent(name, data). It returns a close function.
export function openStream(onEvent, onState) {
  const es = new EventSource("/api/stream");
  es.onopen = () => onState("connected");
  es.onerror = () => onState("disconnected");
  for (const name of ["session", "exchange", "entries"]) {
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
