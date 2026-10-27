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
  entries: [], running: [], status: null, safe: load("safe", "1") === "1",
  range: load("range", "24h"), filter: "all", tool: "", agent: "", query: "",
  shown: PAGE, fresh: null, selected: null, tableView: false,
};

function load(k, d) {
  try { return localStorage.getItem("br." + k) || d; } catch { return d; }
}
function save(k, v) {
  try { localStorage.setItem("br." + k, v); } catch { /* storage unavailable */ }
}

// What the recording hooks saw: results by tool use, sessions, prompts.
const idx = { results: new Map(), sessions: new Map(), prompts: new Map(), skills: new Map() };

// ingest files one audit log line and says what kind it was.
function ingest(line) {
  switch (line.kind) {
    case "result":
      if (line.tool_use_id) idx.results.set(line.tool_use_id, line);
      return "result";
    case "session":
      if (line.session_id) idx.sessions.set(line.session_id, line);
      return "session";
    case "skill": {
      if (!line.session_id) return "skill";
      if (!idx.skills.has(line.session_id)) idx.skills.set(line.session_id, []);
      idx.skills.get(line.session_id).push(line);
      return "skill";
    }
    case "prompt": {
      if (!line.session_id) return "prompt";
      if (!idx.prompts.has(line.session_id)) idx.prompts.set(line.session_id, []);
      idx.prompts.get(line.session_id).push(line);
      return "prompt";
    }
  }
  state.entries.unshift(line);
  return "check";
}

const risky = (e) => e.findings && e.findings.length > 0;
function riskyEntries() {
  return state.entries.filter(risky);
}

// Who ran a command: the agent, its subagent and the session's model.
function agentOf(e) {
  if (e.tool === "check") return { name: "blast-radius check", sub: "", model: "", session: "" };
  const ses = idx.sessions.get(e.session_id) || {};
  return {
    name: platformName(e.agent_platform),
    sub: e.agent_type || (e.session_id ? "main agent" : ""),
    model: ses.model || e.model || "",
    session: e.session_id || "",
  };
}
// Display names of the agents blast-radius has a hook for. Any other agent
// (one calling `blast-radius check --agent NAME`) shows under its own name.
const PLATFORMS = {
  claude_code: "Claude Code", codex: "Codex", gemini_cli: "Gemini CLI", cursor: "Cursor",
  copilot: "GitHub Copilot", windsurf: "Windsurf", cline: "Cline", opencode: "OpenCode",
  aider: "Aider", amazon_q: "Amazon Q", continue: "Continue", roo_code: "Roo Code",
};
function platformName(p) {
  if (!p) return "Legacy / Unknown";
  return Object.hasOwn(PLATFORMS, p) ? PLATFORMS[p] : p;
}
function agentLabel(e) {
  const a = agentOf(e);
  return a.sub ? a.name + " · " + a.sub : a.name;
}

// The request the user gave the agent before it ran this command.
function taskOf(e) {
  const list = idx.prompts.get(e.session_id);
  if (!list) return null;
  if (e.prompt_id) {
    const byId = list.find((p) => p.prompt_id === e.prompt_id);
    if (byId) return byId;
  }
  let best = null;
  for (const p of list) if (p.time <= e.time && (!best || p.time > best.time)) best = p;
  return best;
}

// What happened after blast-radius decided. Ask decisions are answered in
// Claude Code, so a command asked about that never reports a result was
// declined (or its session ended).
const WAIT_MS = 15 * 60e3;
function nextOf(e) {
  if (!e.tool_use_id) return null;
  const r = idx.results.get(e.tool_use_id);
  if (r) {
    if (r.outcome === "ran") return { id: "ran", label: e.action === "ask" ? "You approved · ran" : "Ran", r };
    if (r.outcome === "failed") return { id: "failed", label: e.action === "ask" ? "You approved · failed" : "Ran · failed", r };
    if (r.outcome === "interrupted") return { id: "failed", label: "Interrupted", r };
    if (r.outcome === "denied") return { id: "notrun", label: "Denied by auto mode", r };
  }
  if (e.action === "deny") return { id: "notrun", label: "Not run" };
  const age = Date.now() - new Date(e.time).getTime();
  if (!idx.results.size) return null; // recording hooks not installed
  if (e.action === "ask") return age < WAIT_MS ? { id: "waiting", label: "Waiting for you" } : { id: "notrun", label: "Not run", hint: "You declined it, or the session ended" };
  return age < WAIT_MS ? { id: "running", label: "Running" } : { id: "unknown", label: "No result" };
}
const NEXT_ICON = { ran: "✓", failed: "!", notrun: "✕", waiting: "…", running: "…", unknown: "?" };
function nextChip(e) {
  const n = nextOf(e);
  if (!n) return h("span", { class: "muted" }, "–");
  return h("span", { class: "then t-" + n.id, title: n.hint || (n.r && n.r.error) || "" }, h("i", { "aria-hidden": "true" }, NEXT_ICON[n.id]), n.label);
}

// paranoid-sre: the skill that makes the agent check where it is, preview
// and plan a way back before changing anything. Its careful steps show up
// as read-only commands, recognised here.
const PARANOID = /(^|:)paranoid$|^paranoid-sre$/;
const CARE = {
  where: { label: "Checked where it is", short: "Where", re: /\b(terraform|tofu) workspace show\b|kubectl config (current-context|get-contexts|view)|\bkubectx\b|aws sts get-caller-identity|aws configure list|gcloud config (list|get)|az account show|pulumi (stack --show-name|whoami)|\bkubectl cluster-info\b/ },
  preview: { label: "Previewed the change", short: "Preview", re: /\b(terraform|tofu|terragrunt) plan\b|\bkubectl diff\b|--dry-run|helm (get manifest|diff|template)\b|pulumi preview|blast-radius check|\bterraform show\b|select count\(/i },
  wayback: { label: "Made a way back", short: "Way back", re: /snapshot|backup|pg_dump|mysqldump|mongodump|state pull|velero|-o yaml\s*>|export-/i },
};
function careOf(e) {
  for (const [k, c] of Object.entries(CARE)) if (c.re.test(e.command || "")) return k;
  return null;
}
const shield = () => s("svg", { viewBox: "0 0 16 16", width: 12, height: 12, class: "shield", "aria-hidden": "true" }, s("path", { d: "M8 1l5.5 2v4.2c0 3.4-2.3 5.9-5.5 7.3-3.2-1.4-5.5-3.9-5.5-7.3V3z" }));

// Tasks: the commands an agent ran for one request, in order.
function tasks(list) {
  const map = new Map();
  for (const e of list) {
    if (!e.session_id) continue;
    const p = taskOf(e);
    const k = e.session_id + "\u0000" + (p ? p.time : "none") + "\u0000" + (e.agent_type || "");
    if (!map.has(k)) map.set(k, { key: k, session: e.session_id, prompt: p, cmds: [], agentType: e.agent_type || "" });
    map.get(k).cmds.push(e);
  }
  const out = [...map.values()];
  for (const t of out) {
    t.cmds.sort((a, b) => (a.time < b.time ? -1 : 1));
    t.start = t.prompt ? t.prompt.time : t.cmds[0].time;
    t.last = t.cmds[t.cmds.length - 1].time;
    const later = (idx.prompts.get(t.session) || []).filter((p) => p.time > t.start).map((p) => p.time).sort()[0] || "\uffff";
    const skills = (idx.skills.get(t.session) || []).filter((x) => x.time >= t.start && x.time < later && PARANOID.test(x.skill));
    t.paranoidAt = skills.length ? skills[0].time : t.prompt && /^\/paranoid/.test(t.prompt.prompt) ? t.start : null;
    t.care = { where: [], preview: [], wayback: [] };
    for (const e of t.cmds) { const c = careOf(e); if (c) t.care[c].push(e); }
    t.risky = t.cmds.filter((e) => risky(e) && !careOf(e));
    t.n = { deny: 0, ask: 0, pass: 0 };
    for (const e of t.risky) if (e.action in t.n) t.n[e.action]++;
  }
  return out.sort((a, b) => (a.last < b.last ? 1 : -1));
}
function taskFor(e) {
  return tasks(state.entries.filter((x) => x.session_id === e.session_id)).find((t) => t.cmds.includes(e));
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
  const cur = fill(buckets(state.range), riskyEntries());
  const prev = fill(buckets(state.range, 1), riskyEntries());
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

// Radar: every risky command in the range is a blip. The angle is when it
// ran (the range starts and ends at the top, going clockwise), the ring is
// what blast-radius decided: blocked closest to the centre.

const RADAR_RING = { deny: 32, ask: 56, pass: 80 };

// spread gives each command a stable offset in [-1, 1), so blips that land
// on the same spot don't hide each other.
function spread(str) {
  let x = 2166136261;
  for (let i = 0; i < str.length; i++) x = Math.imul(x ^ str.charCodeAt(i), 16777619);
  return ((x >>> 0) % 2000) / 1000 - 1;
}

function renderRadar(list) {
  const bs = buckets(state.range), from = bs[0].start, to = bs[bs.length - 1].end;
  const svg = s("svg", { viewBox: "-100 -100 200 200", class: "radar-svg" });
  for (const r of [RADAR_RING.deny, RADAR_RING.ask, RADAR_RING.pass]) svg.append(s("circle", { r, class: "radar-ring" }));
  svg.append(s("circle", { r: 94, class: "radar-edge" }));
  for (let i = 0; i < bs.length; i++) {
    const a = (2 * Math.PI * i) / bs.length, major = i % (bs.length > 12 ? 6 : 1) === 0;
    const r0 = major ? 89 : 91.5;
    svg.append(s("line", { x1: r0 * Math.sin(a), y1: -r0 * Math.cos(a), x2: 94 * Math.sin(a), y2: -94 * Math.cos(a), class: major ? "radar-tick major" : "radar-tick" }));
  }
  svg.append(s("line", { x1: 0, y1: -94, x2: 0, y2: 94, class: "radar-axis" }), s("line", { x1: -94, y1: 0, x2: 94, y2: 0, class: "radar-axis" }));
  svg.append(s("text", { x: 0, y: -97, class: "radar-label" }, "Now"));
  svg.append(s("circle", { r: 7, class: "radar-core" }), s("circle", { r: 2.5, class: "radar-core-dot" }));

  const blips = list.filter((e) => e.action in RADAR_RING);
  const newest = blips.reduce((m, e) => (!m || e.time > m.time ? e : m), null);
  for (const e of blips) {
    const t = new Date(e.time).getTime();
    const a = (2 * Math.PI * (t - from)) / (to - from) + spread(e.command) * 0.04;
    const r = RADAR_RING[e.action] + spread(key(e)) * 8;
    const x = r * Math.sin(a), y = -r * Math.cos(a), size = 0.5 + Math.min(lost(e), 8) * 0.06;
    const label = OUT[e.action].label + ": " + e.command;
    const g = s("g", { class: "blip b-" + e.action, tabindex: 0, role: "button", "aria-label": label + ", " + ago(e.time),
      transform: `translate(${x.toFixed(2)} ${y.toFixed(2)})`,
      onclick: () => openDrawer(e),
      onkeydown: (ev) => { if (ev.key === "Enter" || ev.key === " ") { ev.preventDefault(); openDrawer(e); } },
      onmouseenter: (ev) => tip.note(ev, OUT[e.action].label + " · " + agentLabel(e) + " · " + ago(e.time), e.command),
      onmousemove: (ev) => tip.place(ev.clientX, ev.clientY),
      onmouseleave: () => tip.hide() });
    if (e === newest) g.append(s("circle", { r: 7, class: "blip-new" }));
    g.append(s("path", { d: OUT[e.action].icon, class: "f-" + e.action, transform: `scale(${size.toFixed(2)}) translate(-8 -8)` }));
    svg.append(g);
  }
  const n = (id) => blips.filter((e) => e.action === id).length;
  $("radar").setAttribute("role", "group");
  $("radar").setAttribute("aria-label", `Radar of the ${RANGES[state.range].label}: ${n("deny")} blocked, ${n("ask")} asked, ${n("pass")} allowed. Select a blip for details.`);
  $("radar").replaceChildren(svg);
}

// Status: what needs you, in plain words, next to the radar.

let pickFilter = () => {};

function renderStatus(list) {
  const risky = list.filter((e) => e.findings && e.findings.length);
  const waiting = state.entries.filter((e) => e.action === "ask" && (nextOf(e) || {}).id === "waiting");
  const blocked = risky.filter((e) => e.action === "deny");
  const kept = blocked.reduce((n, e) => n + lost(e), 0);
  const period = "the " + RANGES[state.range].label;
  const plural = (n, one, many) => fmt.format(n) + " " + (n === 1 ? one : many);
  const go = (label, onclick, primary) => h("button", { type: "button", class: primary ? "btn" : "ghost", onclick }, label);
  const showActivity = (f) => () => { location.hash = "activity"; pickFilter(f); };

  let tone, kicker, word, text, actions;
  if (state.status && !state.status.log_enabled) {
    [tone, kicker, word] = ["ask", "Not recording", "Recording off"];
    text = "blast-radius still checks every command, but this dashboard only shows what the activity log records.";
    actions = [go("Turn on the activity log", () => { location.hash = "setup"; }, true)];
  } else if (waiting.length) {
    [tone, kicker, word] = ["ask", "Your agent is waiting", "Action needed"];
    text = plural(waiting.length, "command needs", "commands need") + " your answer in the agent. Check what " + (waiting.length === 1 ? "it" : "each") + " would delete before you approve.";
    actions = [go("Review " + (waiting.length === 1 ? "it" : "the oldest"), () => openDrawer(waiting[waiting.length - 1]), true), go("Everything asked", showActivity("ask"))];
  } else if (blocked.length) {
    [tone, kicker, word] = ["deny", "Protected", "Shields up"];
    text = "blast-radius stopped " + plural(blocked.length, "command", "commands") + " in " + period + (kept ? ", which would have deleted " + plural(kept, "resource", "resources") : "") + ".";
    actions = [go("See what was blocked", showActivity("deny"), true)];
  } else if (risky.length) {
    [tone, kicker, word] = ["pass", "Watching", "All clear"];
    text = plural(risky.length, "risky command", "risky commands") + " in " + period + ", none blocked. Each one was allowed by your rules or approved by you.";
    actions = [go("Open activity", showActivity("all"), true)];
  } else {
    [tone, kicker, word] = ["idle", "Watching", "Standing by"];
    text = "No risky commands in " + period + ". Every shell command your agents run is checked before it runs.";
    actions = state.entries.length ? [] : [go("Finish setup", () => { location.hash = "setup"; }, true)];
  }
  $("status").dataset.tone = tone;
  $("status-kicker").textContent = kicker;
  $("status-word").textContent = word;
  $("status-text").textContent = text;
  $("status-actions").replaceChildren(...actions);

  const agents = [...new Set(list.filter((e) => e.tool !== "check").map((e) => agentOf(e).name))];
  const envs = new Set(risky.map(envOf).filter(Boolean));
  const last = state.entries[0];
  const stat = (label, value, title) => [h("dt", {}, label), h("dd", { title: title || "" }, value)];
  $("status-grid").replaceChildren(
    ...stat("Agents", fmt.format(agents.length), agents.join(", ")),
    ...stat("Environments", fmt.format(envs.size), [...envs].join(", ")),
    ...stat("Checking now", fmt.format(state.running.length)),
    ...stat("Last check", last ? h("span", { "data-ago": last.time }, ago(last.time)) : "–"));
}

// Command palette: Ctrl+K or ⌘K to jump to a page or find any command.

const palette = { items: [], active: 0, opener: null };

function paletteItems(q) {
  const pages = Object.entries(VIEWS).map(([id, [title, sub]]) => ({ kind: "Page", title, sub, run: () => { location.hash = id; } }));
  const actions = [
    { kind: "Show", title: "Blocked commands", run: () => { location.hash = "activity"; pickFilter("deny"); } },
    { kind: "Show", title: "Commands that asked you", run: () => { location.hash = "activity"; pickFilter("ask"); } },
    { kind: "Show", title: "Allowed commands", run: () => { location.hash = "activity"; pickFilter("pass"); } },
  ];
  const needle = q.toLowerCase();
  const match = (...xs) => !needle || xs.some((x) => (x || "").toLowerCase().includes(needle));
  const cmds = state.entries.filter((e) => match(e.command, e.dir, envOf(e), agentLabel(e))).slice(0, needle ? 12 : 5)
    .map((e) => ({ kind: OUT[e.action] ? OUT[e.action].label : "Checked", outcome: e.action, title: e.command, sub: [agentLabel(e), envOf(e), ago(e.time)].filter(Boolean).join(" · "), mono: true, run: () => openDrawer(e) }));
  return [...pages.filter((p) => match(p.title, p.sub)), ...actions.filter((a) => match(a.title)), ...cmds];
}

function renderPalette() {
  const list = $("palette-list");
  palette.active = Math.min(palette.active, Math.max(0, palette.items.length - 1));
  if (!palette.items.length) {
    list.replaceChildren(h("li", { class: "palette-empty" }, "Nothing matches. Try part of a command, a folder or an agent's name."));
    $("palette-q").removeAttribute("aria-activedescendant");
    return;
  }
  list.replaceChildren(...palette.items.map((it, i) => h("li", {
    id: "pal-" + i, role: "option", "aria-selected": i === palette.active, class: "palette-item",
    onmousemove: () => { if (palette.active !== i) { palette.active = i; renderPalette(); } },
    onclick: () => choosePalette(i),
  },
  h("span", { class: "palette-kind" }, it.outcome && OUT[it.outcome] ? icon(it.outcome) : null, it.kind),
  h("span", { class: "palette-text" }, h("b", { class: it.mono ? "mono" : "" }, it.title), it.sub && h("small", {}, it.sub)))));
  $("palette-q").setAttribute("aria-activedescendant", "pal-" + palette.active);
  const el = $("pal-" + palette.active);
  if (el) el.scrollIntoView({ block: "nearest" });
}

function openPalette() {
  palette.opener = document.activeElement;
  $("palette").hidden = false;
  $("palette-q").value = "";
  palette.items = paletteItems("");
  palette.active = 0;
  renderPalette();
  $("palette-q").focus();
}
function closePalette() {
  $("palette").hidden = true;
  if (palette.opener && palette.opener.focus) palette.opener.focus();
}
function choosePalette(i) {
  const it = palette.items[i];
  closePalette();
  if (it) it.run();
}

// Decisions over time: stacked columns.

const tip = {
  show(ev, title, rows, totalLabel) {
    const t = $("tooltip");
    t.replaceChildren(h("h4", {}, title),
      ...rows.map((r) => h("div", { class: "tt-row" }, h("span", { class: "key k-" + r.id }), r.label, h("b", {}, fmt.format(r.value)))),
      totalLabel != null ? h("div", { class: "tt-total" }, h("span", {}, "Total"), h("span", {}, fmt.format(totalLabel))) : null);
    t.hidden = false;
    this.place(ev.clientX, ev.clientY);
  },
  // note shows a heading and one line of text, such as a command.
  note(ev, title, text) {
    const t = $("tooltip");
    t.replaceChildren(h("h4", {}, title), h("div", { class: "mono" }, text));
    t.hidden = false;
    this.place(ev.clientX, ev.clientY);
  },
  place(cx, cy) {
    const t = $("tooltip"), r = t.getBoundingClientRect();
    let x = cx + 14, y = cy + 14;
    if (x + r.width > innerWidth - 8) x = cx - r.width - 14;
    if (y + r.height > innerHeight - 8) y = cy - r.height - 14;
    t.style.left = Math.max(8, x) + "px";
    t.style.top = Math.max(8, y) + "px";
  },
  hide() { $("tooltip").hidden = true; },
};

function renderTimeChart() {
  const bs = fill(buckets(state.range), riskyEntries());
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
    h("div", { class: "mix-extra" }, h("b", {}, fmt.format(saved)), " resources would have been deleted by the commands blast-radius blocked."),
    askSummary(list));
}

// How the commands blast-radius asked about were answered.
function askSummary(list) {
  const asked = list.filter((e) => e.action === "ask" && e.tool_use_id);
  if (!asked.length || !idx.results.size) return null;
  const n = { yes: 0, no: 0, waiting: 0 };
  for (const e of asked) {
    const x = nextOf(e);
    if (!x) continue;
    if (x.id === "ran" || x.id === "failed") n.yes++;
    else if (x.id === "waiting") n.waiting++;
    else n.no++;
  }
  return h("div", { class: "mix-extra" }, "Of ", h("b", {}, fmt.format(asked.length)), " asked: ",
    h("b", {}, fmt.format(n.yes)), " you approved, ", h("b", {}, fmt.format(n.no)), " not run",
    n.waiting ? [", ", h("b", {}, fmt.format(n.waiting)), " waiting for you"] : null, ".");
}

// Careful mode: how often paranoid-sre was engaged before risky commands,
// and which of its steps the agent took.
function renderCare(list) {
  const ts = tasks(list).filter((t) => t.risky.length);
  if (!ts.length) {
    $("care").replaceChildren(h("div", { class: "empty-mini" }, "No tasks with risky commands in the " + RANGES[state.range].label + "."));
    return;
  }
  const on = ts.filter((t) => t.paranoidAt);
  const pct = (n, d) => (d ? Math.round((n / d) * 100) : 0);
  const bar = (label, n, d) => {
    const fillEl = h("span");
    fillEl.style.width = pct(n, d) + "%";
    return h("div", { class: "care-row" },
      h("div", { class: "hbar-top" }, h("span", {}, label), h("b", {}, fmt.format(n) + " of " + fmt.format(d))),
      h("div", { class: "care-track" }, fillEl));
  };
  $("care").replaceChildren(
    h("div", { class: "care-head" },
      h("div", { class: "mix-total" }, fmt.format(on.length), h("small", {}, "of " + fmt.format(ts.length) + " tasks with risky commands used paranoid-sre")),
      h("div", { class: "care-ring", role: "img", "aria-label": pct(on.length, ts.length) + "%" }, ring(pct(on.length, ts.length)))),
    h("div", { class: "care-steps" },
      bar(CARE.where.label, on.filter((t) => t.care.where.length).length, on.length),
      bar(CARE.preview.label, on.filter((t) => t.care.preview.length).length, on.length),
      bar(CARE.wayback.label, on.filter((t) => t.care.wayback.length).length, on.length)),
    ts.length > on.length && h("div", { class: "mix-extra" }, h("b", {}, fmt.format(ts.length - on.length)),
      " ", ts.length - on.length === 1 ? "task" : "tasks", " ran risky commands without paranoid-sre. ", h("a", { class: "link", href: "#tasks" }, "See tasks →")));
}
function ring(p) {
  const r = 22, c = 2 * Math.PI * r;
  return s("svg", { viewBox: "0 0 56 56", width: 56, height: 56 },
    s("circle", { cx: 28, cy: 28, r, class: "ring-track" }),
    s("circle", { cx: 28, cy: 28, r, class: "ring-fill", "stroke-dasharray": `${(p / 100) * c} ${c}`, transform: "rotate(-90 28 28)" }),
    s("text", { x: 28, y: 32, "text-anchor": "middle", class: "ring-text" }, p + "%"));
}

// Tasks view.
function renderTasks() {
  const ts = tasks(state.entries.filter(inRange));
  $("task-count").textContent = fmt.format(ts.length) + " in the " + RANGES[state.range].label;
  if (!ts.length) {
    $("task-list").replaceChildren(h("div", { class: "card empty" }, h("h3", {}, "No tasks yet"),
      h("p", {}, "Tasks appear when Claude Code runs commands with the recording hooks installed (run blast-radius install again if you installed an older version).")));
    return;
  }
  $("task-list").replaceChildren(...ts.slice(0, 40).map((t) => {
    const a = agentOf(t.cmds[0]);
    const step = (ok, label) => h("li", { class: ok ? "ok" : "" }, h("i", { "aria-hidden": "true" }, ok ? "✓" : ""), label);
    const story = [];
    if (t.paranoidAt) story.push({ kind: "paranoid", time: t.paranoidAt });
    for (const e of t.cmds) story.push({ kind: "cmd", time: e.time, e });
    story.sort((x, y) => (x.time < y.time ? -1 : 1));
    return h("article", { class: "card task-card" + (t.paranoidAt ? " careful" : "") },
      h("header", { class: "task-head" },
        h("div", { class: "task-title" },
          h("h3", {}, t.prompt ? "“" + t.prompt.prompt + "”" : "No request recorded"),
          h("p", { class: "muted" }, [agentLabel(t.cmds[0]), a.model].filter(Boolean).join(" · "), " · ", h("span", { "data-ago": t.last }, ago(t.last)))),
        t.paranoidAt ? h("span", { class: "care-badge on" }, shield(), "paranoid-sre") : h("span", { class: "care-badge" }, "No careful mode")),
      h("ul", { class: "care-checks" },
        step(!!t.paranoidAt, "paranoid-sre engaged"),
        step(t.care.where.length > 0, CARE.where.label),
        step(t.care.preview.length > 0, CARE.preview.label),
        step(t.care.wayback.length > 0, CARE.wayback.label)),
      h("ol", { class: "story" }, story.map((x) => {
        if (x.kind === "paranoid") return h("li", { class: "st-paranoid" }, h("span", { class: "st-kind" }, shield(), "paranoid-sre"), h("span", {}, "Loaded the careful-SRE checklist"), h("span", { class: "when" }, new Date(x.time).toLocaleTimeString()));
        const e = x.e, c = careOf(e);
        return h("li", { class: "click", onclick: () => openDrawer(e) },
          h("span", { class: "st-kind" + (c ? " care" : "") }, c ? CARE[c].short : risky(e) ? "Change" : "Read"),
          h("span", { class: "mono", title: e.command }, e.command),
          h("span", { class: "st-end" }, risky(e) && !c ? pill(e.action) : null, nextChip(e)));
      })),
      t.risky.length > 0 && h("footer", { class: "agent-counts" },
        OUTCOMES.filter((o) => t.n[o.id]).map((o) => h("span", { class: "count-pill" }, icon(o.id), fmt.format(t.n[o.id]), " ", o.label.toLowerCase()))));
  }));
}

// Agents: one row per agent session and subagent.
function renderAgents(list) {
  const groups = new Map();
  for (const e of list) {
    const a = agentOf(e);
    const k = (a.session || a.name) + "\u0000" + a.sub;
    if (!groups.has(k)) groups.set(k, { a, deny: 0, ask: 0, pass: 0, last: e, n: 0 });
    const g = groups.get(k);
    g.n++;
    if (e.action in g) g[e.action]++;
    if (e.time > g.last.time) g.last = e;
  }
  const rows = [...groups.values()].sort((x, y) => (x.last.time < y.last.time ? 1 : -1)).slice(0, 4);
  if (!rows.length) {
    $("agents").replaceChildren(h("div", { class: "empty-mini" }, "No agent activity in the " + RANGES[state.range].label + "."));
    return;
  }
  $("agents").replaceChildren(h("ul", { class: "agents" }, rows.map((g) => {
    const task = taskOf(g.last);
    return h("li", { onclick: () => openDrawer(g.last), title: "Open the latest command" },
      h("div", { class: "agent-top" },
        h("span", { class: "avatar", "aria-hidden": "true" }, (g.a.sub && g.a.sub !== "main agent" ? g.a.sub : g.a.name)[0].toUpperCase()),
        h("div", { class: "agent-name" },
          h("b", {}, g.a.name), g.a.sub && h("span", { class: "tag" }, g.a.sub),
          (idx.skills.get(g.a.session) || []).some((x) => PARANOID.test(x.skill)) && h("span", { class: "care-badge on small", title: "Used paranoid-sre" }, shield(), "paranoid-sre"),
          h("div", { class: "agent-meta" }, [g.a.model, g.a.session && "session " + g.a.session.slice(0, 8)].filter(Boolean).join(" · ") || "\u00a0")),
        h("span", { class: "when", "data-ago": g.last.time }, ago(g.last.time))),
      task && h("p", { class: "agent-task", title: task.prompt }, "“" + task.prompt + "”"),
      h("div", { class: "agent-counts" },
        OUTCOMES.filter((o) => g[o.id]).map((o) => h("span", { class: "count-pill" }, icon(o.id), fmt.format(g[o.id]), " ", o.label.toLowerCase()))));
  })));
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
  const waiting = state.entries.filter((e) => e.action === "ask" && (nextOf(e) || {}).id === "waiting");
  const n = r.length + waiting.length;
  $("run-badge").textContent = n;
  $("run-badge").classList.toggle("active", n > 0);
  $("nav-wait").hidden = waiting.length === 0;
  $("nav-wait").textContent = waiting.length;
  if (!n) {
    $("running").replaceChildren(h("div", { class: "empty-mini" },
      h("div", { class: "art", "aria-hidden": "true" }, h("span"), h("span")), "Idle. Previews and commands waiting for your answer show here."));
    return;
  }
  $("running").replaceChildren(h("ul", { class: "mini" },
    waiting.map((e) => h("li", { class: "click", onclick: () => openDrawer(e) },
      h("span", { class: "then t-waiting" }, h("i", { "aria-hidden": "true" }, "…"), "Waiting for you"),
      h("span", { class: "mono", title: e.command }, e.command),
      h("span", { class: "when", "data-ago": e.time }, ago(e.time)))),
    r.map((x) => h("li", {}, h("span", { class: "pill run" }, s("svg", { viewBox: "0 0 16 16", "aria-hidden": "true", width: 12 }, s("circle", { cx: 8, cy: 8, r: 6 })), "Checking"),
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
    && (state.safe || risky(e))
    && (state.filter === "all" || e.action === state.filter)
    && (!state.tool || toolsOf(e).includes(state.tool))
    && (!state.agent || agentOf(e).name === state.agent)
    && (!q || [e.command, e.dir, envOf(e), agentLabel(e), agentOf(e).model, (taskOf(e) || {}).prompt]
      .some((x) => (x || "").toLowerCase().includes(q))));
}

function renderTable() {
  const list = filtered();
  const inPeriod = state.entries.filter((e) => inRange(e) && (state.safe || risky(e))).length;
  $("safe-toggle").hidden = !state.entries.some((e) => !risky(e));
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
      h("td", {}, risky(e) ? pill(e.action) : h("span", { class: "pill safe" }, "Safe")),
      h("td", {}, nextChip(e)),
      h("td", { class: "cmd", title: e.command }, e.command),
      h("td", { class: "agent", title: [agentLabel(e), agentOf(e).model].filter(Boolean).join(" · ") }, careOf(e) ? h("span", { class: "care-tag" }, CARE[careOf(e)].short) : null, agentLabel(e)),
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
  const agents = [...new Set(state.entries.map((e) => agentOf(e).name))].sort();
  const agentSel = $("agent-filter");
  agentSel.hidden = agents.length < 2 && !state.agent;
  if (agentSel.options.length - 1 !== agents.length) {
    agentSel.replaceChildren(h("option", { value: "" }, "All agents"), ...agents.map((a) => h("option", { value: a }, a)));
    agentSel.value = state.agent;
  }
}

// Drawer with one command's details.

function openDrawer(e, refocus = true) {
  state.selected = key(e);
  const check = "blast-radius check" + (e.dir ? " -C " + shellQuote(e.dir) : "") + " " + shellQuote(e.command);
  $("drawer-pill").replaceChildren(pill(e.action), h("span", { class: "when" }, "  " + new Date(e.time).toLocaleString()));
  const a = agentOf(e), task = taskOf(e);
  $("drawer-body").replaceChildren(
    h("section", {}, h("h3", {}, "Command"), codeBlock(e.command)),
    task && h("section", {}, h("h3", {}, "Task"), h("blockquote", { class: "task" }, task.prompt)),
    h("section", {}, h("h3", {}, "What happened"), timeline(e)),
    h("section", {}, h("h3", {}, "Agent"), h("dl", { class: "kv" },
      h("dt", {}, "Agent"), h("dd", {}, agentLabel(e)),
      h("dt", {}, "Model"), h("dd", {}, a.model || (a.session ? "not recorded (session started before the recording hooks)" : "–")),
      h("dt", {}, "Session"), h("dd", { class: "mono" }, a.session || "–"),
      e.permission_mode && [h("dt", {}, "Permissions"), h("dd", {}, e.permission_mode)],
      h("dt", {}, "Tool"), h("dd", {}, e.tool),
      h("dt", {}, "Folder"), h("dd", { class: "mono" }, e.dir || "–"))),
    risky(e) && h("section", {}, h("h3", {}, "What blast-radius found"), ...(e.findings || []).map((f) =>
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
  if (refocus) $("drawer-close").focus();
  renderTable();
}
// timeline lists the steps a command went through.
function timeline(e) {
  const steps = [];
  const at = (t) => new Date(t).toLocaleTimeString();
  const t = e.session_id ? taskFor(e) : null;
  if (t && t.paranoidAt && t.paranoidAt <= e.time) {
    steps.push({ id: "care", title: "paranoid-sre engaged", detail: "The agent loaded the careful-SRE checklist for this task.", time: at(t.paranoidAt) });
    for (const k of Object.keys(CARE)) {
      const done = t.care[k].filter((x) => x.time < e.time);
      if (done.length) steps.push({ id: "care", title: CARE[k].label, detail: done.map((x) => x.command).join("\n"), time: at(done[0].time) });
    }
  }
  steps.push({ id: "info", title: agentLabel(e) + " wanted to run it", time: at(e.time) });
  if (!risky(e)) steps.push({ id: "pass", title: "blast-radius found nothing risky", detail: "Claude Code's own permission rules applied." });
  else if (e.action === "deny") steps.push({ id: "deny", title: "blast-radius blocked it", detail: (e.findings.find((f) => f.reason) || {}).reason });
  else if (e.action === "ask") steps.push({ id: "ask", title: "blast-radius asked you first", detail: (e.findings.find((f) => f.reason) || {}).reason });
  else steps.push({ id: "pass", title: "blast-radius allowed it", detail: (e.findings.find((f) => f.reason) || {}).reason });
  const n = nextOf(e);
  if (n) {
    const r = n.r;
    const detail = r ? [r.duration_ms ? "took " + (r.duration_ms / 1000).toFixed(1) + "s" : "", r.error || ""].filter(Boolean).join(" · ") : n.hint;
    steps.push({ id: "t-" + n.id, title: n.label, detail, time: r ? at(r.time) : "" });
  }
  return h("ol", { class: "timeline" }, steps.map((x) => h("li", { class: x.id },
    h("i", { "aria-hidden": "true" }),
    h("div", {}, h("div", { class: "tl-top" }, h("b", {}, x.title), x.time && h("span", { class: "when" }, x.time)),
      x.detail && h("p", {}, x.detail)))));
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
  agents: { title: "Connect your other AI agents", text: "Optional. Codex, Gemini CLI and GitHub Copilot CLI get the same checks and show up in this dashboard. Add --project to protect one repository only.", code: "blast-radius install --agent codex\nblast-radius install --agent gemini\nblast-radius install --agent copilot" },
  log: { title: "Turn on the activity log", text: "Writes a config with audit_log set, which feeds this dashboard. With an existing config, add the audit_log line instead.", code: 'blast-radius init --user\n# or add to your existing config:\n"audit_log": "~/.local/state/blast-radius/audit.jsonl"' },
  paranoid: { title: "Add the paranoid-sre skill", text: "Makes the agent check where it is, preview and plan a way back before it runs anything, so fewer commands get this far. Codex, Gemini CLI and Copilot CLI all read skills from ~/.agents/skills.", code: "# Claude Code\nclaude plugin marketplace add SandeepKomal/paranoid-sre\nclaude plugin install paranoid-sre@paranoid-sre\n\n# Codex, Gemini CLI, Copilot CLI\ngit clone https://github.com/SandeepKomal/paranoid-sre\nmkdir -p ~/.agents/skills && cp -r paranoid-sre/skills/paranoid ~/.agents/skills/" },
  restart: { title: "Check the setup, then restart Claude Code", text: "doctor tests the hook on this machine. Claude Code loads new hooks and plugins when it starts.", code: "blast-radius doctor" },
};

function renderSetup(st) {
  const steps = st.steps;
  const countable = steps.filter((x) => !x.unknown && !x.optional);
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
      : x.done ? h("span", { class: "state ok" }, "Done")
      : x.optional ? h("span", { class: "state unk" }, "Optional") : h("span", { class: "state todo" }, "To do");
    return h("li", { class: "step" + (x.done ? " done" : ""), id: "step-" + x.id },
      h("div", { class: "step-num" }, h("span", {}, i + 1)),
      h("div", {}, h("div", { class: "step-top" }, h("h3", {}, d.title), label),
        h("p", {}, d.text),
        x.detail && h("div", { class: "detail" }, x.detail),
        !x.done && codeBlock(d.code)));
  }));

  const byId = Object.fromEntries(steps.map((x) => [x.id, x]));
  const health = [["Hook", byId.hook.done || !byId.agents ? byId.hook : byId.agents], ["Activity log", byId.log], ["paranoid-sre", byId.paranoid],
    ["Masking", { done: st.privacy.redact, detail: "Personal data and secrets are masked before they are written" }]];
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
  renderPrivacy(st);
  $("config-error").hidden = !st.config_error;
  $("config-error").textContent = st.config_error ? "Config error: " + st.config_error + ". Until it's fixed, nothing passes without asking." : "";
}

function renderPrivacy(st) {
  const p = st.privacy;
  const size = p.bytes < 1024 ? p.bytes + " B" : p.bytes < 1048576 ? (p.bytes / 1024).toFixed(1) + " KB" : (p.bytes / 1048576).toFixed(1) + " MB";
  const row = (ok, title, text, fix) => h("li", { class: ok ? "on" : "off" },
    h("i", { "aria-hidden": "true" }, ok ? "✓" : "!"),
    h("div", {}, h("b", {}, title), h("p", {}, text), !ok && fix && h("p", { class: "fix" }, "To change: ", h("code", {}, fix))));
  $("privacy-list").replaceChildren(
    row(true, "Stays on this machine", "The log is a file only you can read (mode 600). This dashboard listens on 127.0.0.1 and sends nothing anywhere."),
    row(p.redact, p.redact ? "Personal data and secrets are masked" : "Masking is off",
      p.redact ? "Emails, Indian phone numbers, PAN, Aadhaar, card numbers and IP addresses in requests and errors, and passwords, tokens and keys everywhere, are replaced before anything is written."
        : "Requests, errors and commands are written as typed.", '"audit_redact": true'),
    row(p.retention_days > 0, p.retention_days > 0 ? "Kept for " + p.retention_days + " days" : "Kept until you delete it",
      p.retention_days > 0 ? "Older entries are deleted automatically while this dashboard runs, or with blast-radius audit prune." : "Entries are never deleted automatically.", '"audit_retention_days": 90'),
    row(true, p.prompts ? "Requests to the agent are recorded" : "Requests to the agent are not recorded",
      p.prompts ? "So each command shows its task. Set audit_prompts to false to stop." : "Commands, decisions and results are still recorded; tasks show as not recorded."),
    row(true, p.all ? "Every shell command is recorded" : "Only risky commands are recorded",
      p.all ? "Including reads such as kubectl get, so paranoid-sre's careful steps show up." : "Set audit_all to true to see paranoid-sre's careful steps."),
    row(true, "Log file", st.log + " · " + size + (p.oldest ? " · oldest entry " + new Date(p.oldest).toLocaleDateString() : "")));
  $("privacy-cmds").replaceChildren(
    h("p", { class: "muted" }, "Someone asks what is stored about them:"), codeBlock("blast-radius audit export --match 'Ravi'"),
    h("p", { class: "muted" }, "Someone asks to be erased:"), codeBlock("blast-radius audit forget --match 'Ravi'"),
    h("p", { class: "muted" }, "Erase one Claude session:"), codeBlock("blast-radius audit forget --session <id>"),
    h("p", { class: "muted" }, "Delete old entries now:"), codeBlock("blast-radius audit prune --older-than 90d"));
}

// Views.

const VIEWS = {
  overview: ["Overview", "What your agent tried to run, and what blast-radius did about it."],
  tasks: ["Tasks", "Each request you gave the agent, the careful steps paranoid-sre made it take, and what blast-radius decided."],
  activity: ["Activity", "Every command blast-radius checked, newest first. Select one for details."],
  setup: ["Setup", "Install blast-radius and paranoid-sre, in order."],
  rules: ["Rules & privacy", "What happens in each environment, and what the activity log keeps."],
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
  if (v === "tasks") renderTasks();
  if (v === "overview") renderTimeChart();
}

function renderAll() {
  const list = state.entries.filter(inRange);
  $("nav-count").textContent = list.length ? fmt.format(list.length) : "";
  renderRadar(list.filter(risky));
  renderStatus(list);
  renderKPIs();
  renderTimeChart();
  renderMix(list.filter(risky));
  renderBreakdown($("by-env"), list.filter(risky), (e) => [envOf(e) || "unknown"], "No environments yet.");
  renderBreakdown($("by-agent"), list.filter(risky), (e) => [agentOf(e).name], "No agents yet.");
  renderBreakdown($("by-tool"), list.filter(risky), toolsOf, "No tools yet.");
  renderAgents(list);
  renderCare(list);
  renderTasks();
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
  renderStatus(state.entries.filter(inRange));
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
    if (ingest(e) === "check") state.fresh = key(e);
    renderAll();
    if (state.selected && $("drawer").classList.contains("open")) {
      const open = state.entries.find((x) => key(x) === state.selected);
      if (open) openDrawer(open, false);
    }
  });
  es.addEventListener("running", (ev) => {
    state.running = JSON.parse(ev.data);
    renderRunning();
    renderStatus(state.entries.filter(inRange));
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
  pickFilter = setFilter;
  $("safe").checked = state.safe;
  $("safe").addEventListener("change", (e) => { state.safe = e.target.checked; save("safe", state.safe ? "1" : "0"); state.shown = PAGE; renderTable(); });
  $("tool-filter").addEventListener("change", (e) => { state.tool = e.target.value; renderTable(); });
  $("agent-filter").addEventListener("change", (e) => { state.agent = e.target.value; renderTable(); });
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
  document.addEventListener("keydown", (e) => {
    const typing = /^(INPUT|SELECT|TEXTAREA)$/.test(document.activeElement.tagName);
    if (((e.ctrlKey || e.metaKey) && e.key.toLowerCase() === "k") || (e.key === "/" && !typing && $("palette").hidden)) {
      e.preventDefault();
      if ($("palette").hidden) openPalette(); else closePalette();
    } else if (e.key === "Escape" && !$("palette").hidden) closePalette();
    else if (e.key === "Escape" && $("drawer").classList.contains("open")) closeDrawer();
  });
  $("palette-kbd").textContent = /Mac|iPhone|iPad/.test(navigator.platform) ? "⌘K" : "Ctrl K";
  $("palette-open").addEventListener("click", openPalette);
  $("palette").addEventListener("mousedown", (e) => { if (e.target === $("palette")) closePalette(); });
  $("palette-q").addEventListener("input", (e) => { palette.items = paletteItems(e.target.value.trim()); palette.active = 0; renderPalette(); });
  $("palette-q").addEventListener("keydown", (e) => {
    const n = palette.items.length;
    if (e.key === "ArrowDown" && n) { e.preventDefault(); palette.active = (palette.active + 1) % n; renderPalette(); }
    else if (e.key === "ArrowUp" && n) { e.preventDefault(); palette.active = (palette.active - 1 + n) % n; renderPalette(); }
    else if (e.key === "Enter") { e.preventDefault(); choosePalette(palette.active); }
    else if (e.key === "Tab") e.preventDefault();
  });
  for (const el of document.querySelectorAll("[data-copy]")) el.replaceWith(codeBlock(el.textContent.trim()));
  window.addEventListener("hashchange", route);
  new ResizeObserver(() => { if (!$("view-overview").hidden) renderTimeChart(); }).observe($("ts-chart"));

  renderLegend();
  setRange(state.range);
  try {
    const a = await getJSON("api/activity");
    for (const line of a.entries) ingest(line);
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
    renderRunning();
    renderStatus(state.entries.filter(inRange));
  }, 15000);
  setInterval(() => loadStatus().catch(() => {}), 10000);
}

start();
