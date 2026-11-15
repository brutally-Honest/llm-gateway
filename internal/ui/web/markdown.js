import { el } from "./fmt.js";

// A small Markdown renderer for a turn's prompt and final answer (spec 017 AC13, research
// D3): headings, emphasis, inline code, code blocks, lists, links, quotes, tables and rules.
// Every piece of text goes in through textContent, never as HTML, so a prompt holding
// <script> or any other tag shows as text (E7). What it does not know stays plain text (E8).

const FENCE = /^ {0,3}(```+|~~~+)\s*([^`\s]*)/;
const HEADING = /^ {0,3}(#{1,6})\s+(.*?)\s*#*\s*$/;
const RULE = /^ {0,3}([-*_])(\s*\1){2,}\s*$/;
const QUOTE = /^ {0,3}>\s?/;
const ITEM = /^( {0,3})([-*+]|\d{1,9}[.)])\s+(.*)$/;
const TABLE_SEP = /^\s*\|?\s*:?-{2,}:?\s*(\|\s*:?-{2,}:?\s*)*\|?\s*$/;

// renderMarkdown returns a container element holding the rendered text.
export function renderMarkdown(text) {
  const box = el("div", "md");
  box.append(...blocks(String(text ?? "").replace(/\r\n?/g, "\n").split("\n")));
  return box;
}

const blank = (l) => /^\s*$/.test(l);
const indentOf = (l) => l.match(/^ */)[0].length;

function blocks(lines) {
  const out = [];
  let i = 0;
  while (i < lines.length) {
    const line = lines[i];
    if (blank(line)) {
      i++;
      continue;
    }
    const fence = line.match(FENCE);
    if (fence) {
      const body = [];
      const close = new RegExp(`^ {0,3}${fence[1][0] === "`" ? "`" : "~"}{${fence[1].length},}\\s*$`);
      i++;
      while (i < lines.length && !close.test(lines[i])) body.push(lines[i++]);
      i++;
      const pre = el("pre");
      const code = el("code", "", body.join("\n"));
      if (fence[2]) code.dataset.lang = fence[2];
      pre.append(code);
      out.push(pre);
      continue;
    }
    const h = line.match(HEADING);
    if (h) {
      const node = el(`h${h[1].length}`);
      node.append(...inline(h[2]));
      out.push(node);
      i++;
      continue;
    }
    if (RULE.test(line)) {
      out.push(el("hr"));
      i++;
      continue;
    }
    if (QUOTE.test(line)) {
      const body = [];
      while (i < lines.length && !blank(lines[i]) && (QUOTE.test(lines[i]) || body.length)) {
        body.push(lines[i].replace(QUOTE, ""));
        i++;
      }
      const q = el("blockquote");
      q.append(...blocks(body));
      out.push(q);
      continue;
    }
    if (ITEM.test(line)) {
      const [node, next] = list(lines, i);
      out.push(node);
      i = next;
      continue;
    }
    if (line.includes("|") && i + 1 < lines.length && TABLE_SEP.test(lines[i + 1]) && lines[i + 1].includes("-")) {
      const [node, next] = table(lines, i);
      out.push(node);
      i = next;
      continue;
    }
    // a paragraph runs until a blank line or the start of another block
    const para = [line];
    i++;
    while (i < lines.length && !blank(lines[i]) && !startsBlock(lines, i)) para.push(lines[i++]);
    const p = el("p");
    para.forEach((l, k) => {
      if (k) p.append(el("br"));
      p.append(...inline(l.trim()));
    });
    out.push(p);
  }
  return out;
}

function startsBlock(lines, i) {
  const l = lines[i];
  return FENCE.test(l) || HEADING.test(l) || RULE.test(l) || QUOTE.test(l) || ITEM.test(l);
}

// list reads a list starting at lines[i]: each item with the lines under it, which are
// rendered as blocks of their own, so nested lists and code inside items work.
function list(lines, i) {
  const first = lines[i].match(ITEM);
  const ordered = /\d/.test(first[2]);
  const base = first[1].length;
  const node = el(ordered ? "ol" : "ul");
  if (ordered && parseInt(first[2], 10) !== 1) node.setAttribute("start", String(parseInt(first[2], 10)));
  while (i < lines.length) {
    const m = lines[i].match(ITEM);
    if (!m || m[1].length !== base || /\d/.test(m[2]) !== ordered) break;
    const pad = m[1].length + m[2].length + 1;
    const body = [m[3]];
    i++;
    while (i < lines.length) {
      const l = lines[i];
      if (blank(l)) {
        if (i + 1 < lines.length && !blank(lines[i + 1]) && indentOf(lines[i + 1]) > base) {
          body.push("");
          i++;
          continue;
        }
        break;
      }
      const sub = l.match(ITEM);
      if (sub && sub[1].length <= base) break;
      if (indentOf(l) <= base && startsBlock(lines, i)) break;
      body.push(indentOf(l) >= pad ? l.slice(pad) : l.trimStart());
      i++;
    }
    const li = el("li");
    const inner = blocks(body);
    // a tight item is its text alone, not a paragraph
    if (inner.length && inner[0].tagName === "p" && !body.includes("")) li.append(...inner[0].children, ...inner.slice(1));
    else li.append(...inner);
    node.append(li);
    while (i < lines.length && blank(lines[i]) && i + 1 < lines.length && (lines[i + 1].match(ITEM) || [])[1]?.length === base) i++;
  }
  return [node, i];
}

const cells = (l) => {
  let s = l.trim();
  if (s.startsWith("|")) s = s.slice(1);
  if (s.endsWith("|") && !s.endsWith("\\|")) s = s.slice(0, -1);
  return s.split(/(?<!\\)\|/).map((c) => c.trim().replace(/\\\|/g, "|"));
};

function table(lines, i) {
  const head = cells(lines[i]);
  const aligns = cells(lines[i + 1]).map((c) => (c.startsWith(":") && c.endsWith(":") ? "center" : c.endsWith(":") ? "right" : ""));
  const wrap = el("div", "md-table");
  const t = el("table");
  const thead = el("thead");
  const hr = el("tr");
  head.forEach((c, k) => {
    const th = el("th");
    if (aligns[k]) th.style.textAlign = aligns[k];
    th.append(...inline(c));
    hr.append(th);
  });
  thead.append(hr);
  const tbody = el("tbody");
  i += 2;
  while (i < lines.length && !blank(lines[i]) && lines[i].includes("|")) {
    const tr = el("tr");
    cells(lines[i]).forEach((c, k) => {
      if (k >= head.length) return;
      const td = el("td");
      if (aligns[k]) td.style.textAlign = aligns[k];
      td.append(...inline(c));
      tr.append(td);
    });
    tbody.append(tr);
    i++;
  }
  t.append(thead, tbody);
  wrap.append(t);
  return [wrap, i];
}

// safeHref keeps a link only when it is http or https (E7).
function safeHref(url) {
  try {
    const u = new URL(url);
    return u.protocol === "http:" || u.protocol === "https:" ? u.href : null;
  } catch {
    return null;
  }
}

// inline renders one line's spans: code, links, bold, italic, strikethrough. Unmatched
// markers stay as text.
export function inline(text) {
  const out = [];
  let buf = "";
  const flush = () => {
    if (buf) out.push(buf);
    buf = "";
  };
  let i = 0;
  while (i < text.length) {
    const ch = text[i];
    if (ch === "\\" && i + 1 < text.length && /[\\`*_{}[\]()#+\-.!|~<>]/.test(text[i + 1])) {
      buf += text[i + 1];
      i += 2;
      continue;
    }
    if (ch === "`") {
      const ticks = text.slice(i).match(/^`+/)[0];
      const end = text.indexOf(ticks, i + ticks.length);
      if (end > 0) {
        flush();
        out.push(el("code", "", text.slice(i + ticks.length, end).replace(/^ (.+) $/, (_, inner) => inner)));
        i = end + ticks.length;
        continue;
      }
      buf += ticks;
      i += ticks.length;
      continue;
    }
    if (ch === "[") {
      const m = text.slice(i).match(/^\[([^\]]*)\]\(\s*<?([^)\s>]+)>?(?:\s+"[^"]*")?\s*\)/);
      if (m) {
        const href = safeHref(m[2]);
        flush();
        if (href) {
          const a = el("a");
          a.href = href;
          a.setAttribute("target", "_blank");
          a.setAttribute("rel", "noopener noreferrer");
          a.append(...inline(m[1]));
          out.push(a);
        } else {
          out.push(...inline(m[1]));
        }
        i += m[0].length;
        continue;
      }
    }
    const strong = text.slice(i).match(/^(\*\*|__)(?=\S)([\s\S]*?\S)\1(?!\1)/);
    if (strong && (ch === "*" || !/\w/.test(text[i - 1] || ""))) {
      flush();
      const b = el("strong");
      b.append(...inline(strong[2]));
      out.push(b);
      i += strong[0].length;
      continue;
    }
    const strike = text.slice(i).match(/^~~(?=\S)([\s\S]*?\S)~~/);
    if (strike) {
      flush();
      const s = el("del");
      s.append(...inline(strike[1]));
      out.push(s);
      i += strike[0].length;
      continue;
    }
    const em = text.slice(i).match(/^([*_])(?=[^\s*_])([\s\S]*?[^\s\\])\1(?![*_\w])/);
    if (em && (ch === "*" || !/\w/.test(text[i - 1] || ""))) {
      flush();
      const e = el("em");
      e.append(...inline(em[2]));
      out.push(e);
      i += em[0].length;
      continue;
    }
    buf += ch;
    i++;
  }
  flush();
  return out;
}
