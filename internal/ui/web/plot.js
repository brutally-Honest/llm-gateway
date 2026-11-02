import { PLOT_HEIGHT, PLOT_WIDTH } from "./model.js";

// The SVG namespace is read from the empty <svg id="svg-ns"> in index.html rather than
// written out here, so no absolute URL appears in a script.
function svg(tag, attrs = {}) {
  const e = document.createElementNS(document.getElementById("svg-ns").namespaceURI, tag);
  for (const [k, v] of Object.entries(attrs)) e.setAttribute(k, v);
  return e;
}

// contextPlotNode draws a contextPlot() result: a baseline, the polyline and a dot per
// call with its value on hover. Colours and stroke come from style.css classes.
export function contextPlotNode(plot) {
  const root = svg("svg", { viewBox: `0 0 ${PLOT_WIDTH} ${PLOT_HEIGHT}`, preserveAspectRatio: "none", role: "img", "aria-label": plot.label });
  root.setAttribute("class", "ctx-svg");
  root.append(svg("line", { x1: 0, y1: PLOT_HEIGHT - 1, x2: PLOT_WIDTH, y2: PLOT_HEIGHT - 1 }));
  const line = svg("polyline", { points: plot.polyline });
  root.append(line);
  for (const p of plot.points) {
    const dot = svg("circle", { cx: p.x, cy: p.y, r: 3 });
    const tip = svg("title");
    tip.textContent = p.title;
    dot.append(tip);
    root.append(dot);
  }
  return root;
}
