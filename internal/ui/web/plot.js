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
  const root = svg("svg", { viewBox: `0 0 ${plot.width} ${PLOT_HEIGHT}`, role: "img", "aria-label": plot.label });
  root.setAttribute("class", "ctx-svg");
  root.append(svg("line", { x1: 0, y1: PLOT_HEIGHT - 1, x2: plot.width, y2: PLOT_HEIGHT - 1 }));
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

// fitContextPlot returns a block that holds the plot at the block's own width, redrawn when
// that width changes, so a viewBox unit is a pixel and the dots stay round. plotAt(width)
// gives the contextPlot() result to draw. A block not yet in the page has no width: it draws
// at PLOT_WIDTH, and the observer's first report, before the next paint, corrects it. The
// observer ends itself once the block has left the page.
export function fitContextPlot(plotAt, markId = "") {
  const host = document.createElement("div");
  host.className = "ctx-plot";
  let drawn = null;
  const draw = () => {
    const width = host.clientWidth || PLOT_WIDTH;
    if (width === drawn) return;
    drawn = width;
    host.replaceChildren(contextPlotNode(plotAt(width), markId));
  };
  draw();
  if (typeof ResizeObserver === "function") {
    const watch = new ResizeObserver(() => {
      if (host.isConnected === false) watch.disconnect();
      else draw();
    });
    watch.observe(host);
  }
  return host;
}
