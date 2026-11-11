// A stand-in for the page that hosts the 013 drawer (spec 016): one `a.call-link` per
// model call, a turn heading for focus to fall back to, and the drawer kept in sync with
// the route the way app.js does. It lets the drawer's tests run without a session page.
import { createDrawer } from "../web/drawer.js";

export function createDrawerHost({ nav = { push() {}, replace() {}, back() {} }, loadDetail = async () => { throw new Error("no detail in this test"); } } = {}) {
  const doc = globalThis.document;
  const root = doc.createElement("div");
  const links = doc.createElement("nav");
  const heading = doc.createElement("h2");
  heading.className = "turn-head";
  let drawer = null;
  drawer = createDrawer({ nav, loadDetail, turnHeading: () => heading });
  root.append(heading, links, drawer.el);
  let drawn = { missing: false, index: 0, total: 0 };
  let ids = [];
  let sessionId = "";
  return {
    el: root,
    drawer,
    // render syncs the drawer to data.callId and redraws the call links; it returns what
    // the drawer reported ({ missing, index, total }).
    render(data) {
      sessionId = data.summary ? data.summary.id : "";
      ids = data.entries.filter((e) => e.kind === "model_call").map((e) => e.exchange_id);
      links.replaceChildren(...ids.map((id) => {
        const a = doc.createElement("a");
        a.className = "call-link";
        a.href = `#/sessions/${sessionId}/call/${id}`;
        a.addEventListener("click", () => drawer.noteOpener(a));
        return a;
      }));
      drawn = drawer.sync({ sessionId, callId: data.callId || null, entries: data.entries, loaded: Boolean(data.loaded) });
      return drawn;
    },
    stepCall: (delta) => drawer.step(delta),
    closeDrawer: () => drawer.close(),
    openFirstCall: () => ids.length && drawer.openCall(ids[0], heading),
    position: () => ({ index: drawn.index, total: drawn.total }),
  };
}
