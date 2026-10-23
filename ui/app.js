"use strict";

// Everything shown comes from commands an agent wrote, so the page is built
// with DOM nodes and textContent, never innerHTML.
function h(tag, attrs, ...children) {
  const el = document.createElement(tag);
  setAttrs(el, attrs);
  append(el, children);
  return el;
}
const SVGNS = "http://www.w3.org/2000/svg";
function s(tag, attrs, ...children) {
  const el = document.createElementNS(SVGNS, tag);
  setAttrs(el, attrs);
  append(el, children);
  return el;
}
function setAttrs(el, attrs) {
  for (const [k, v] of Object.entries(attrs || {})) {
    if (v === false || v == null) continue;
    if (k.startsWith("on")) el.addEventListener(k.slice(2), v);
    else el.setAttribute(k === "className" ? "class" : k, v === true ? "" : v);
  }
}
function append(el, children) {
  for (const c of children.flat()) {
    if (c == null || c === false) continue;
    el.append(c instanceof Node ? c : document.createTextNode(String(c)));
  }
}
const $ = (id) => document.getElementById(id);
const fmt = new Intl.NumberFormat();

// Outcomes, top of a stack first. Each has a shape as well as a color, so
// color is never the only cue.
const OUTCOMES = [
  { id: "deny", label: "Blocked", icon: "M6 1h4l5 5v4l-5 5H6l-5-5V6z" },
  { id: "ask", label: "Asked you", icon: "M8 1l7 7-7 7-7-7z" },
  { id: "pass", label: "Allowed", icon: "M8 1a7 7 0 1 1 0 14A7 7 0 0 1 8 1z" },
];
const OUT = Object.fromEntries(OUTCOMES.map((o) => [o.id, o]));
const RULE = { deny: "Block", ask: "Ask you", pass: "Allow" };
const RANGES = {
  "24h": { n: 24, step: 3600e3, unit: "hour", label: "last 24 hours", short: "24h" },
  "7d": { n: 7, step: 86400e3, unit: "day", label: "last 7 days", short: "7 days" },
  "30d": { n: 30, step: 86400e3, unit: "day", label: "last 30 days", short: "30 days" },
};
const PAGE = 50;

const state = {
  entries: [], running: [], status: null,
  range: load("range", "24h"), filter: "all", tool: "", query: "",
  shown: PAGE, fresh: null, selected: null, tableView: false,
};

function load(k, d) {
  try { return localStorage.getItem("br." + k) || d; } catch { return d; }
}
function save(k, v) {
  try { localStorage.setItem("br." + k, v); } catch { /* storage unavailable */ }
}

// Helpers

function icon(outcome) {
  return s("svg", { viewBox: "0 0 16 16", width: 12, height: 12, class: "f-" + outcome, "aria-hidden": "true" }, s("path", { d: OUT[outcome].icon }));
}
function pill(outcome, text) {
  return h("span", { class: "pill " + outcome }, icon(outcome), text || OUT[outcome].label);
}
function ago(t) {
  const sec = Math.max(0, (Date.now() - new Date(t).getTime()) / 1000);
  if (sec < 10) return "just now";
  if (sec < 60) return Math.floor(sec) + "s ago";
  if (sec < 3600) return Math.floor(sec / 60) + "m ago";
  if (sec < 86400) return Math.floor(sec / 3600) + "h ago";
  return new Date(t).toLocaleDateString(undefined, { month: "short", day: "numeric" });
}
function envOf(e) {
  const f = (e.findings || []).find((f) => f.environment) || (e.findings || [])[0];
  return f ? f.environment || f.target || "" : "";
}
function toolsOf(e) {
  return [...new Set((e.findings || []).map((f) => f.tool))];
}
function lost(e) {
  return (e.findings || []).reduce((n, f) => n + (f.destroyed ? f.destroyed.length : 0), 0);
}
function key(e) {
  return e.time + "\u0000" + e.command;
}
function shellQuote(x) {
  return "'" + x.replaceAll("'", "'\\''") + "'";
}

// Time buckets for the selected range, oldest first.
function buckets(range, offset = 0) {
  const r = RANGES[range];
  const end = new Date();
  if (r.unit === "hour") end.setMinutes(0, 0, 0);
  else end.setHours(0, 0, 0, 0);
  const last = end.getTime() - offset * r.n * r.step;
  const out = [];
  for (let i = r.n - 1; i >= 0; i--) {
    const start = last - i * r.step;
    out.push({ start, end: start + r.step, deny: 0, ask: 0, pass: 0, lost: 0 });
  }
  return out;
}
function fill(bs, entries) {
  const from = bs[0].start, step = bs[0].end - bs[0].start;
  for (const e of entries) {
    const i = Math.floor((new Date(e.time).getTime() - from) / step);
    if (i < 0 || i >= bs.length || !(e.action in bs[i])) continue;
    bs[i][e.action]++;
    if (e.action === "deny") bs[i].lost += lost(e);
  }
  return bs;
}
function total(b) {
  return b.deny + b.ask + b.pass;
}
function inRange(e) {
  const bs = buckets(state.range);
  const t = new Date(e.time).getTime();
  return t >= bs[0].start && t < bs[bs.length - 1].end;
}
function bucketLabel(b, long) {
  const d = new Date(b.start);
  if (RANGES[state.range].unit === "hour") {
    const hr = d.toLocaleTimeString(undefined, { hour: "numeric" });
    return long ? d.toLocaleDateString(undefined, { weekday: "short" }) + " " + hr : hr;
  }
  return d.toLocaleDateString(undefined, long ? { weekday: "short", month: "short", day: "numeric" } : { month: "short", day: "numeric" });
}
function niceMax(v) {
  if (v <= 4) return 4;
  const p = Math.pow(10, Math.floor(Math.log10(v)));
  for (const m of [1, 2, 2.5, 5, 10]) if (m * p >= v) return m * p;
  return 10 * p;
}

// KPIs

function sparkline(values, cls) {
  const w = 96, hgt = 34, max = Math.max(1, ...values);
  const pts = values.map((v, i) => [i * (w / (values.length - 1 || 1)), hgt - 3 - (v / max) * (hgt - 8)]);
  const d = pts.map((p, i) => (i ? "L" : "M") + p[0].toFixed(1) + " " + p[1].toFixed(1)).join("");
  const last = pts[pts.length - 1];
  return s("svg", { class: "spark", viewBox: `0 0 ${w} ${hgt}`, "aria-hidden": "true" },
    s("path", { class: "area", d: d + `L${w} ${hgt}L0 ${hgt}Z` }),
    s("path", { d }),
    s("circle", { cx: last[0], cy: last[1], r: 3.5, class: cls }));
}

function renderKPIs() {
  const cur = fill(buckets(state.range), state.entries);
  const prev = fill(buckets(state.range, 1), state.entries);
  const sum = (bs, f) => bs.reduce((n, b) => n + f(b), 0);
  const tiles = [
    { label: "Risky commands", f: total, cls: "f-all", key: "k-all" },
    { label: "Blocked", f: (b) => b.deny, cls: "f-deny", outcome: "deny" },
    { label: "Asked you", f: (b) => b.ask, cls: "f-ask", outcome: "ask" },
    { label: "Resources kept safe", f: (b) => b.lost, cls: "f-deny", hint: "Resources the blocked commands would have deleted" },
  ];
  const period = "prior " + RANGES[state.range].short;
  $("kpis").replaceChildren(...tiles.map((t) => {
    const v = sum(cur, t.f), p = sum(prev, t.f), d = v - p;
    const delta = d === 0 ? "Same as " + period : (d > 0 ? "▲ " : "▼ ") + fmt.format(Math.abs(d)) + " vs " + period;
    return h("div", { class: "kpi", title: t.hint || "" },
      h("div", { class: "kpi-label" }, t.outcome ? icon(t.outcome) : null, t.label),
      h("div", { class: "kpi-row" },
        h("div", {},
          h("div", { class: "kpi-value" }, fmt.format(v)),
          h("div", { class: "kpi-delta" }, delta)),
        sparkline(cur.map(t.f), t.cls)));
  }));
}

// Decisions over time: stacked columns.

const tip = {
  show(ev, title, rows, totalLabel) {
    const t = $("tooltip");
    t.replaceChildren(h("h4", {}, title),
      ...rows.map((r) => h("div", { class: "tt-row" }, h("span", { class: "key k-" + r.id }), r.label, h("b", {}, fmt.format(r.value)))),
      totalLabel != null ? h("div", { class: "tt-total" }, h("span", {}, "Total"), h("span", {}, fmt.format(totalLabel))) : null);
    t.hidden = false;
    const r = t.getBoundingClientRect();
    let x = ev.clientX + 14, y = ev.clientY + 14;
    if (x + r.width > innerWidth - 8) x = ev.clientX - r.width - 14;
    if (y + r.height > innerHeight - 8) y = ev.clientY - r.height - 14;
    t.style.left = x + "px";
    t.style.top = y + "px";
  },
  hide() { $("tooltip").hidden = true; },
};

function renderTimeChart() {
  const bs = fill(buckets(state.range), state.entries);
  const box = $("ts-chart");
  const W = Math.max(280, box.clientWidth), H = box.clientHeight || 260;
  const m = { t: 8, r: 4, b: 26, l: 32 };
  const iw = W - m.l - m.r, ih = H - m.t - m.b;
  const max = niceMax(Math.max(...bs.map(total)));
  const y = (v) => m.t + ih - (v / max) * ih;
  const slot = iw / bs.length, bw = Math.max(3, Math.min(28, slot * 0.62));

  const svg = s("svg", { viewBox: `0 0 ${W} ${H}`, role: "img", "aria-label": "Decisions over time, stacked by outcome" });
  const axis = s("g", { class: "axis" });
  for (let i = 0; i <= 4; i++) {
    const v = (max / 4) * i, yy = y(v);
    axis.append(s("line", { class: i ? "gridline" : "baseline", x1: m.l, x2: W - m.r, y1: yy, y2: yy }));
    axis.append(s("text", { x: m.l - 8, y: yy + 4, "text-anchor": "end" }, fmt.format(v)));
  }
  const every = Math.ceil(bs.length / Math.max(2, Math.floor(iw / 64)));
  bs.forEach((b, i) => {
    if ((bs.length - 1 - i) % every) return;
    axis.append(s("text", { x: m.l + slot * i + slot / 2, y: H - 6, "text-anchor": "middle" }, bucketLabel(b)));
  });
  svg.append(axis);

  bs.forEach((b, i) => {
    const x = m.l + slot * i + (slot - bw) / 2;
    const g = s("g", { class: "col" });
    g.append(s("rect", { class: "hover-band", x: m.l + slot * i, y: m.t, width: slot, height: ih }));
    // Bottom to top: allowed, asked, blocked; 2px surface gap between segments.
    let base = 0;
    const segs = ["pass", "ask", "deny"].filter((k) => b[k] > 0);
    segs.forEach((k, j) => {
      const y0 = y(base), y1 = y(base + b[k]);
      base += b[k];
      const top = j === segs.length - 1;
      const hh = Math.max(1, y0 - y1 - (j ? 2 : 0));
      const yTop = y0 - (j ? 2 : 0) - hh;
      if (top) {
        const r = Math.min(4, bw / 2, hh);
        g.append(s("path", { class: "s-" + k, d: `M${x} ${yTop + hh}V${yTop + r}Q${x} ${yTop} ${x + r} ${yTop}H${x + bw - r}Q${x + bw} ${yTop} ${x + bw} ${yTop + r}V${yTop + hh}Z` }));
      } else {
        g.append(s("rect", { class: "s-" + k, x, y: yTop, width: bw, height: hh }));
      }
    });
    const rows = OUTCOMES.map((o) => ({ id: o.id, label: o.label, value: b[o.id] }));
    g.addEventListener("mousemove", (ev) => tip.show(ev, bucketLabel(b, true), rows, total(b)));
    g.addEventListener("mouseleave", tip.hide);
    svg.append(g);
  });

  if (!bs.some(total)) {
    svg.append(s("text", { x: m.l + iw / 2, y: m.t + ih / 2, "text-anchor": "middle", class: "empty-label" },
      "No risky commands in the " + RANGES[state.range].label));
  }
  box.replaceChildren(svg);
  $("ts-caption").textContent = "Per " + RANGES[state.range].unit + ", " + RANGES[state.range].label;

  // The same numbers as a table, for screen readers and exact values.
  $("ts-table").replaceChildren(h("table", { class: "data static" },
    h("thead", {}, h("tr", {}, h("th", {}, RANGES[state.range].unit === "hour" ? "Hour" : "Day"),
      OUTCOMES.map((o) => h("th", { class: "num" }, o.label)), h("th", { class: "num" }, "Total"))),
    h("tbody", {}, [...bs].reverse().filter(total).map((b) => h("tr", {},
      h("td", {}, bucketLabel(b, true)), OUTCOMES.map((o) => h("td", { class: "num" }, b[o.id])), h("td", { class: "num" }, total(b)))))));
  if (!bs.some(total)) $("ts-table").append(h("p", { class: "empty-mini" }, "Nothing in this period."));
}

function renderLegend() {
  $("ts-legend").replaceChildren(...OUTCOMES.map((o) => h("span", {}, h("i", { class: "key k-" + o.id }), o.label)));
}

// Outcome mix and breakdowns.

function renderMix(list) {
  const n = { deny: 0, ask: 0, pass: 0 };
  for (const e of list) if (e.action in n) n[e.action]++;
  const all = n.deny + n.ask + n.pass;
  const bar = h("div", { class: "mix-bar" + (all ? "" : " empty"), role: "img", "aria-label": OUTCOMES.map((o) => o.label + " " + n[o.id]).join(", ") });
  for (const o of OUTCOMES) {
    if (!n[o.id]) continue;
    const seg = h("span", { class: "k-" + o.id });
    seg.style.flex = String(n[o.id]);
    seg.addEventListener("mousemove", (ev) => tip.show(ev, o.label, [{ id: o.id, label: Math.round((n[o.id] / all) * 100) + "% of risky commands", value: n[o.id] }]));
    seg.addEventListener("mouseleave", tip.hide);
    bar.append(seg);
  }
  const saved = list.filter((e) => e.action === "deny").reduce((x, e) => x + lost(e), 0);
  $("mix").replaceChildren(
    h("div", { class: "mix-total" }, fmt.format(all), h("small", {}, "risky commands in the " + RANGES[state.range].label)),
    bar,
    h("ul", { class: "mix-list" }, OUTCOMES.map((o) => h("li", {},
      icon(o.id), h("span", {}, o.label), h("b", {}, fmt.format(n[o.id])),
      h("span", { class: "pct" }, all ? Math.round((n[o.id] / all) * 100) + "%" : "–")))),
    h("div", { class: "mix-extra" }, h("b", {}, fmt.format(saved)), " resources would have been deleted by the commands blast-radius blocked."));
}

function renderBreakdown(el, list, by, emptyText) {
  const groups = new Map();
  for (const e of list) {
    for (const k of by(e)) {
      if (!groups.has(k)) groups.set(k, { name: k, deny: 0, ask: 0, pass: 0 });
      if (e.action in groups.get(k)) groups.get(k)[e.action]++;
    }
  }
  const rows = [...groups.values()].sort((a, b) => total(b) - total(a) || b.deny - a.deny).slice(0, 6);
  if (!rows.length) {
    el.replaceChildren(h("div", { class: "empty-mini" }, emptyText));
    return;
  }
  const max = Math.max(...rows.map(total));
  el.replaceChildren(h("div", { class: "hbars" }, rows.map((r) => {
    const track = h("div", { class: "hbar-track" });
    for (const o of OUTCOMES) {
      if (!r[o.id]) continue;
      const seg = h("span", { class: "b-" + o.id });
      seg.style.width = (r[o.id] / max) * 100 + "%";
      track.append(seg);
    }
    const row = h("div", {}, h("div", { class: "hbar-top" }, h("span", { title: r.name }, r.name), h("b", {}, fmt.format(total(r)))), track);
    const rowsTip = OUTCOMES.map((o) => ({ id: o.id, label: o.label, value: r[o.id] }));
    row.addEventListener("mousemove", (ev) => tip.show(ev, r.name, rowsTip, total(r)));
    row.addEventListener("mouseleave", tip.hide);
    return row;
  })));
}

function renderRunning() {
  const r = state.running;
  $("run-badge").textContent = r.length;
  $("run-badge").classList.toggle("active", r.length > 0);
  if (!r.length) {
    $("running").replaceChildren(h("div", { class: "empty-mini" },
      h("div", { class: "art", "aria-hidden": "true" }, h("span"), h("span")), "Idle. Previews show here while they run."));
    return;
  }
  $("running").replaceChildren(h("ul", { class: "mini" }, r.map((x) =>
    h("li", {}, h("span", { class: "pill run" }, s("svg", { viewBox: "0 0 16 16", "aria-hidden": "true", width: 12 }, s("circle", { cx: 8, cy: 8, r: 6 })), "Checking"),
      h("span", { class: "mono", title: x.command }, x.command),
      h("span", { class: "when", "data-ago": x.started }, ago(x.started))))));
}

function renderLatestBlocked(list) {
  const rows = list.filter((e) => e.action === "deny").slice(0, 5);
  if (!rows.length) {
    $("latest-blocked").replaceChildren(h("div", { class: "empty-mini" }, "Nothing blocked in the " + RANGES[state.range].label + "."));
    return;
  }
  $("latest-blocked").replaceChildren(h("div", { class: "table-wrap" }, h("table", { class: "data click" },
    h("thead", {}, h("tr", {}, h("th", {}, "Command"), h("th", {}, "Environment"), h("th", { class: "num" }, "Deletes"), h("th", { class: "num" }, "When"))),
    h("tbody", {}, rows.map((e) => h("tr", { onclick: () => openDrawer(e) },
      h("td", { class: "cmd", title: e.command }, e.command),
      h("td", { class: "env" }, envOf(e)),
      h("td", { class: "num" }, lost(e) || "–"),
      h("td", { class: "num when", "data-ago": e.time }, ago(e.time))))))));
}

// Activity table.

function filtered() {
  const q = state.query.toLowerCase();
  return state.entries.filter((e) => inRange(e)
    && (state.filter === "all" || e.action === state.filter)
    && (!state.tool || toolsOf(e).includes(state.tool))
    && (!q || [e.command, e.dir, envOf(e)].some((x) => (x || "").toLowerCase().includes(q))));
}

function renderTable() {
  const list = filtered();
  const inPeriod = state.entries.filter(inRange).length;
  $("result-count").textContent = list.length === inPeriod
    ? fmt.format(list.length) + " in the " + RANGES[state.range].label
    : fmt.format(list.length) + " of " + fmt.format(inPeriod) + " in the " + RANGES[state.range].label;
  $("rows").replaceChildren(...list.slice(0, state.shown).map((e) => {
    const k = key(e);
    return h("tr", {
      class: (k === state.fresh ? "fresh " : "") + (state.selected === k ? "selected" : ""),
      tabindex: "0",
      onclick: () => openDrawer(e),
      onkeydown: (ev) => { if (ev.key === "Enter") openDrawer(e); },
    },
      h("td", {}, pill(e.action)),
      h("td", { class: "cmd", title: e.command }, e.command),
      h("td", {}, toolsOf(e).join(", ")),
      h("td", { class: "env", title: envOf(e) }, envOf(e) || "–"),
      h("td", { class: "num" }, lost(e) || "–"),
      h("td", { class: "num when", "data-ago": e.time, title: new Date(e.time).toLocaleString() }, ago(e.time)));
  }));
  $("more").hidden = list.length <= state.shown;
  const empty = list.length === 0;
  $("empty").hidden = !empty;
  $("rows").closest("table").hidden = empty;
  if (empty) {
    $("empty").replaceChildren(
      h("h3", {}, state.entries.length ? "No matching commands" : "Nothing recorded yet"),
      h("p", {}, state.entries.length
        ? "Try a longer time range or clear the filters."
        : "When your agent runs terraform, kubectl, helm or a cloud CLI command that changes something, it appears here as it happens."));
  }
  const tools = [...new Set(state.entries.flatMap(toolsOf))].sort();
  const sel = $("tool-filter");
  if (sel.options.length - 1 !== tools.length) {
    sel.replaceChildren(h("option", { value: "" }, "All tools"), ...tools.map((t) => h("option", { value: t }, t)));
    sel.value = state.tool;
  }
}

// Drawer with one command's details.

function openDrawer(e) {
  state.selected = key(e);
  const check = "blast-radius check" + (e.dir ? " -C " + shellQuote(e.dir) : "") + " " + shellQuote(e.command);
  $("drawer-pill").replaceChildren(pill(e.action), h("span", { class: "when" }, "  " + new Date(e.time).toLocaleString()));
  $("drawer-body").replaceChildren(
    h("section", {}, h("h3", {}, "Command"), codeBlock(e.command)),
    h("section", {}, h("h3", {}, "Details"), h("dl", { class: "kv" },
      h("dt", {}, "Folder"), h("dd", { class: "mono" }, e.dir || "–"),
      h("dt", {}, "From"), h("dd", {}, e.tool === "check" ? "blast-radius check" : "Claude Code · " + e.tool),
      h("dt", {}, "Environment"), h("dd", {}, envOf(e) || "–"),
      h("dt", {}, "When"), h("dd", {}, new Date(e.time).toLocaleString() + " (" + ago(e.time) + ")"))),
    h("section", {}, h("h3", {}, "What blast-radius found"), ...(e.findings || []).map((f) =>
      h("div", { class: "finding" },
        h("div", { class: "finding-top" }, pill(f.action), h("b", {}, f.tool),
          f.environment && h("span", { class: "tag" }, f.environment),
          h("span", { class: "tag" }, f.level === "guard" ? "touches blast-radius itself" : f.level)),
        f.target && h("p", {}, f.target),
        f.reason && h("p", {}, f.reason),
        f.destroyed && f.destroyed.length > 0 && h("div", {},
          h("p", {}, f.destroyed.length === 1 ? "1 resource would be deleted" : f.destroyed.length + " resources would be deleted"),
          h("div", { class: "chips" }, f.destroyed.map((d) => h("span", { class: "chip lost" }, d))))))),
    h("section", {}, h("h3", {}, "Check it again yourself"), codeBlock(check)));
  $("drawer").classList.add("open");
  $("drawer").setAttribute("aria-hidden", "false");
  $("scrim").hidden = false;
  $("drawer-close").focus();
  renderTable();
}
function closeDrawer() {
  state.selected = null;
  $("drawer").classList.remove("open");
  $("drawer").setAttribute("aria-hidden", "true");
  $("scrim").hidden = true;
  renderTable();
}

function codeBlock(text) {
  const btn = h("button", { class: "copy", type: "button" }, "Copy");
  btn.addEventListener("click", async () => {
    try {
      await navigator.clipboard.writeText(text.split("\n").filter((l) => !l.startsWith("#")).join("\n"));
      btn.textContent = "Copied";
    } catch {
      btn.textContent = "Select to copy";
    }
    setTimeout(() => (btn.textContent = "Copy"), 1600);
  });
  return h("div", { class: "code" }, h("pre", {}, text), btn);
}

// Setup and rules.

const STEPS = {
  binary: { title: "Install blast-radius", text: "One program with no dependencies. Download a release, or build it with Go.", code: "go install github.com/sandeepkomal/blast-radius@latest" },
  hook: { title: "Connect it to Claude Code", text: "Adds a hook that checks every shell command before it runs. Add --project to protect one repository only.", code: "blast-radius install" },
  log: { title: "Turn on the activity log", text: "Writes a config with audit_log set, which feeds this dashboard. With an existing config, add the audit_log line instead.", code: 'blast-radius init --user\n# or add to your existing config:\n"audit_log": "~/.local/state/blast-radius/audit.jsonl"' },
  paranoid: { title: "Add the paranoid-sre skill", text: "Makes Claude check where it is, preview and plan a way back before it runs anything, so fewer commands get this far.", code: "claude plugin marketplace add SandeepKomal/paranoid-sre\nclaude plugin install paranoid-sre@paranoid-sre" },
  restart: { title: "Check the setup, then restart Claude Code", text: "doctor tests the hook on this machine. Claude Code loads new hooks and plugins when it starts.", code: "blast-radius doctor" },
};

function renderSetup(st) {
  const steps = st.steps;
  const countable = steps.filter((x) => !x.unknown);
  const done = countable.filter((x) => x.done).length;
  const track = h("div", { class: "progress-track" }, h("span"));
  track.firstChild.style.width = (done / countable.length) * 100 + "%";
  $("progress").replaceChildren(h("span", {}, done + " of " + countable.length + " done"), track);
  const todo = countable.length - done;
  $("setup-count").hidden = todo === 0;
  $("setup-count").textContent = todo;

  $("steps").replaceChildren(...steps.map((x, i) => {
    const d = STEPS[x.id];
    const label = x.unknown ? h("span", { class: "state unk" }, "Do this last")
      : x.done ? h("span", { class: "state ok" }, "Done") : h("span", { class: "state todo" }, "To do");
    return h("li", { class: "step" + (x.done ? " done" : ""), id: "step-" + x.id },
      h("div", { class: "step-num" }, h("span", {}, i + 1)),
      h("div", {}, h("div", { class: "step-top" }, h("h3", {}, d.title), label),
        h("p", {}, d.text),
        x.detail && h("div", { class: "detail" }, x.detail),
        !x.done && codeBlock(d.code)));
  }));

  const byId = Object.fromEntries(steps.map((x) => [x.id, x]));
  const health = [["Hook", byId.hook], ["Activity log", byId.log], ["paranoid-sre", byId.paranoid]];
  $("health").replaceChildren(...health.map(([name, x]) =>
    h("li", { title: x.detail || "" }, h("i", { class: "dot " + (x.done ? "on" : "off") }), name, h("span", {}, x.done ? "On" : "Off"))));
}

function renderRules(st) {
  const p = st.policy;
  const rule = (a) => pill(a, RULE[a]);
  const rows = [h("tr", {}, h("td", {}, h("b", {}, "Everywhere else")), h("td", { class: "muted" }, "Anything not listed below"), h("td", {}, rule(p.default.change)), h("td", {}, rule(p.default.destroy)))];
  for (const env of p.environments || []) {
    rows.push(h("tr", {}, h("td", {}, h("b", {}, env.name)),
      h("td", {}, h("div", { class: "chips" }, (env.match || []).map((m) => h("span", { class: "chip" }, m)))),
      h("td", {}, rule(env.change || p.default.change)), h("td", {}, rule(env.destroy || p.default.destroy))));
  }
  $("rule-rows").replaceChildren(...rows);
  $("rules-source").textContent = st.sources.length
    ? "From " + st.sources.join(" and ") + ". A repository's .blast-radius.json can only make these stricter."
    : "The built-in policy. Run blast-radius init --user to write your own.";
  $("protect").hidden = !(p.protect && p.protect.length);
  $("protect-list").replaceChildren(...(p.protect || []).map((x) => h("span", { class: "chip" }, x)));
  $("config-error").hidden = !st.config_error;
  $("config-error").textContent = st.config_error ? "Config error: " + st.config_error + ". Until it's fixed, nothing passes without asking." : "";
}

// Views.

const VIEWS = {
  overview: ["Overview", "What your agent tried to run, and what blast-radius did about it."],
  activity: ["Activity", "Every risky command, newest first. Select one for details."],
  setup: ["Setup", "Install blast-radius and paranoid-sre, in order."],
  rules: ["Rules", "What happens in each environment."],
};

function route() {
  const v = location.hash.slice(1) in VIEWS ? location.hash.slice(1) : "overview";
  for (const id of Object.keys(VIEWS)) $("view-" + id).hidden = id !== v;
  for (const a of document.querySelectorAll(".nav a")) {
    if (a.dataset.view === v) a.setAttribute("aria-current", "page");
    else a.removeAttribute("aria-current");
  }
  $("title").textContent = VIEWS[v][0];
  $("subtitle").textContent = VIEWS[v][1];
  document.querySelector(".top-actions .segmented").hidden = v === "setup" || v === "rules";
  if (v === "overview") renderTimeChart();
}

function renderAll() {
  const list = state.entries.filter(inRange);
  $("nav-count").textContent = list.length ? fmt.format(list.length) : "";
  renderKPIs();
  renderTimeChart();
  renderMix(list);
  renderBreakdown($("by-env"), list, (e) => [envOf(e) || "unknown"], "No environments yet.");
  renderBreakdown($("by-tool"), list, toolsOf, "No tools yet.");
  renderLatestBlocked(list);
  renderRunning();
  renderTable();
}

// Data.

async function getJSON(url) {
  const r = await fetch(url, { cache: "no-store" });
  if (!r.ok) throw new Error(url + ": " + r.status);
  return r.json();
}

async function loadStatus() {
  const st = await getJSON("api/status");
  state.status = st;
  $("version").textContent = "blast-radius " + st.version;
  $("log-banner").hidden = st.log_enabled;
  renderSetup(st);
  renderRules(st);
}

function setLive(st, text) {
  $("live").dataset.state = st;
  $("live").lastElementChild.textContent = text;
}

function connect() {
  const es = new EventSource("api/stream");
  es.addEventListener("open", () => setLive("live", "Live"));
  es.addEventListener("entry", (ev) => {
    const e = JSON.parse(ev.data);
    state.entries.unshift(e);
    state.fresh = key(e);
    renderAll();
  });
  es.addEventListener("running", (ev) => {
    state.running = JSON.parse(ev.data);
    renderRunning();
  });
  es.addEventListener("error", () => setLive("off", "Reconnecting"));
}

function radio(group, attr, onPick) {
  const buttons = group.querySelectorAll("button");
  const pick = (b) => {
    for (const o of buttons) o.setAttribute("aria-checked", o === b);
    onPick(b.dataset[attr]);
  };
  for (const b of buttons) b.addEventListener("click", () => pick(b));
  return (value) => pick([...buttons].find((b) => b.dataset[attr] === value) || buttons[0]);
}

async function start() {
  const setRange = radio($("range"), "range", (r) => {
    state.range = r;
    save("range", r);
    state.shown = PAGE;
    renderAll();
  });
  const setFilter = radio($("filter"), "filter", (f) => {
    state.filter = f;
    state.shown = PAGE;
    renderTable();
  });
  $("tool-filter").addEventListener("change", (e) => { state.tool = e.target.value; renderTable(); });
  $("search").addEventListener("input", (e) => { state.query = e.target.value.trim(); state.shown = PAGE; renderTable(); });
  $("more").addEventListener("click", () => { state.shown += PAGE; renderTable(); });
  $("ts-toggle").addEventListener("click", () => {
    state.tableView = !state.tableView;
    $("ts-toggle").setAttribute("aria-pressed", state.tableView);
    $("ts-chart").hidden = state.tableView;
    $("ts-table").hidden = !state.tableView;
  });
  for (const a of document.querySelectorAll("[data-goto-filter]")) a.addEventListener("click", () => setFilter(a.dataset.gotoFilter));
  $("drawer-close").addEventListener("click", closeDrawer);
  $("scrim").addEventListener("click", closeDrawer);
  document.addEventListener("keydown", (e) => { if (e.key === "Escape" && $("drawer").classList.contains("open")) closeDrawer(); });
  for (const el of document.querySelectorAll("[data-copy]")) el.replaceWith(codeBlock(el.textContent.trim()));
  window.addEventListener("hashchange", route);
  new ResizeObserver(() => { if (!$("view-overview").hidden) renderTimeChart(); }).observe($("ts-chart"));

  renderLegend();
  setRange(state.range);
  try {
    const a = await getJSON("api/activity");
    state.entries = a.entries.reverse();
    state.running = a.running;
  } catch {
    setLive("off", "Offline");
  }
  route();
  renderAll();
  loadStatus().catch(() => {});
  connect();
  setInterval(() => {
    for (const t of document.querySelectorAll("[data-ago]")) t.textContent = ago(t.dataset.ago);
  }, 15000);
  setInterval(() => loadStatus().catch(() => {}), 10000);
}

start();
