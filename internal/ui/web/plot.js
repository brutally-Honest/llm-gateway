import { PLOT_HEIGHT, PLOT_WIDTH } from "./model.js";

// The SVG namespace is an identifier, never fetched. It is the one absolute URL the guard
// in web_test.go exempts.
const SVG_NS = "http://www.w3.org/2000/svg";

function svg(tag, attrs = {}) {
  const e = document.createElementNS(SVG_NS, tag);
  for (const [k, v] of Object.entries(attrs)) e.setAttribute(k, v);
  return e;
}

// contextPlotNode draws a contextPlot() result: a baseline, the polyline and a dot per
// call with its value on hover. Colours and stroke come from style.css classes. The call
// named by markId, the drawer's own, gets a larger dot and says so on hover.
export function contextPlotNode(plot, markId = "") {
  const root = svg("svg", { viewBox: `0 0 ${PLOT_WIDTH} ${PLOT_HEIGHT}`, preserveAspectRatio: "none", role: "img", "aria-label": plot.label });
  root.setAttribute("class", "ctx-svg");
  root.append(svg("line", { x1: 0, y1: PLOT_HEIGHT - 1, x2: PLOT_WIDTH, y2: PLOT_HEIGHT - 1 }));
  const line = svg("polyline", { points: plot.polyline });
  root.append(line);
  for (const p of plot.points) {
    const mine = p.exchangeId === markId;
    const dot = svg("circle", { cx: p.x, cy: p.y, r: mine ? 6 : 3 });
    if (mine) dot.setAttribute("class", "is-this");
    const tip = svg("title");
    tip.textContent = mine ? `${p.title} · this call` : p.title;
    dot.append(tip);
    root.append(dot);
  }
  return root;
}
