"use strict";

// Everything shown comes from commands an agent wrote, so the page is built
// with DOM nodes and textContent, never innerHTML.
function h(tag, attrs, ...children) {
  const el = document.createElement(tag);
  for (const [k, v] of Object.entries(attrs || {})) {
    if (v === false || v == null) continue;
    if (k === "class") el.className = v;
    else if (k.startsWith("on")) el.addEventListener(k.slice(2), v);
    else el.setAttribute(k, v === true ? "" : v);
  }
  for (const c of children.flat()) {
    if (c == null || c === false) continue;
    el.append(c instanceof Node ? c : document.createTextNode(String(c)));
  }
  return el;
}
const $ = (id) => document.getElementById(id);

const LABEL = { deny: "Blocked", ask: "Asked you", pass: "Allowed" };
const KIND = { deny: "deny", ask: "ask", pass: "allow" };
const RULE = { deny: "Block", ask: "Ask you", pass: "Allow" };
const PAGE = 50;

const state = { entries: [], running: [], filter: "all", query: "", shown: PAGE, open: new Set() };

function ago(t) {
  const s = Math.max(0, (Date.now() - new Date(t).getTime()) / 1000);
  if (s < 10) return "just now";
  if (s < 60) return Math.floor(s) + "s ago";
  if (s < 3600) return Math.floor(s / 60) + "m ago";
  if (s < 86400) return Math.floor(s / 3600) + "h ago";
  return new Date(t).toLocaleDateString(undefined, { month: "short", day: "numeric" });
}

function envOf(e) {
  const f = (e.findings || []).find((f) => f.environment) || (e.findings || [])[0];
  if (!f) return "";
  return f.environment || f.target || "";
}

function key(e) {
  return e.time + "\u0000" + e.command;
}

function pill(action) {
  return h("span", { class: "pill " + (KIND[action] || "ask") }, LABEL[action] || action);
}

// Activity

function renderStats() {
  const day = Date.now() - 86400e3;
  const n = { deny: 0, ask: 0, pass: 0 };
  for (const e of state.entries) if (new Date(e.time).getTime() >= day && e.action in n) n[e.action]++;
  $("n-deny").textContent = n.deny;
  $("n-ask").textContent = n.ask;
  $("n-allow").textContent = n.pass;
  $("n-running").textContent = state.running.length;
}

function matches(e) {
  if (state.filter !== "all" && KIND[e.action] !== state.filter) return false;
  if (!state.query) return true;
  const q = state.query.toLowerCase();
  return [e.command, e.dir, envOf(e)].some((s) => (s || "").toLowerCase().includes(q));
}

function renderItem(e, fresh) {
  const k = key(e);
  const body = h("div", { class: "item-body" },
    h("dl", { class: "kv" },
      h("dt", {}, "Command"), h("dd", {}, e.command),
      h("dt", {}, "Directory"), h("dd", {}, e.dir || "–"),
      h("dt", {}, "From"), h("dd", {}, e.tool === "check" ? "blast-radius check" : "Claude Code · " + e.tool),
      h("dt", {}, "When"), h("dd", {}, new Date(e.time).toLocaleString())),
    (e.findings || []).map((f) =>
      h("div", { class: "finding" },
        h("div", { class: "finding-top" },
          pill(f.action),
          h("b", {}, f.tool),
          f.target && h("span", { class: "tag" }, f.target),
          f.environment && h("span", { class: "tag" }, f.environment),
          h("span", { class: "tag" }, f.level === "guard" ? "touches blast-radius" : f.level)),
        f.reason && h("p", {}, f.reason),
        f.destroyed && f.destroyed.length > 0 && h("div", {},
          h("p", {}, f.destroyed.length === 1 ? "1 resource would be deleted:" : f.destroyed.length + " resources would be deleted:"),
          h("div", { class: "chips" }, f.destroyed.map((d) => h("span", { class: "chip" }, d)))))));

  const item = h("article", { class: "item" + (fresh ? " fresh" : "") + (state.open.has(k) ? " open" : "") });
  const head = h("button", {
    class: "item-head", "aria-expanded": state.open.has(k) ? "true" : "false",
    onclick: () => {
      const open = item.classList.toggle("open");
      head.setAttribute("aria-expanded", open);
      open ? state.open.add(k) : state.open.delete(k);
    },
  },
    pill(e.action),
    h("span", { class: "cmd", title: e.command }, e.command),
    h("span", { class: "meta" },
      h("span", { class: "env" }, envOf(e)),
      h("time", { datetime: e.time, "data-ago": e.time }, ago(e.time)),
      h("i", { class: "chev", "aria-hidden": "true" })));
  item.append(head, body);
  return item;
}

function renderFeed(freshKey) {
  const list = state.entries.filter(matches);
  const feed = $("feed");
  feed.replaceChildren(...list.slice(0, state.shown).map((e) => renderItem(e, key(e) === freshKey)));
  $("more").hidden = list.length <= state.shown;
  const none = list.length === 0;
  $("empty").hidden = !none;
  if (none && state.entries.length > 0) {
    $("empty-title").textContent = "No matches";
    $("empty-text").textContent = "Nothing in the log matches this filter.";
  } else if (none) {
    $("empty-title").textContent = "Nothing yet";
    $("empty-text").replaceChildren(
      "When your agent runs ", h("code", {}, "terraform apply"), ", ", h("code", {}, "kubectl delete"),
      " or ", h("code", {}, "helm uninstall"), ", it shows up here as it happens.");
  }
}

function renderRunning() {
  $("now").hidden = state.running.length === 0;
  $("running").replaceChildren(...state.running.map((r) =>
    h("article", { class: "item" },
      h("div", { class: "item-head static" },
        h("span", { class: "pill run" }, "Checking"),
        h("span", { class: "cmd", title: r.command }, r.command),
        h("span", { class: "meta" },
          h("span", { class: "env" }, r.dir),
          h("time", { "data-ago": r.started }, ago(r.started)))))));
  renderStats();
}

function addEntry(e) {
  state.entries.unshift(e);
  renderStats();
  renderFeed(key(e));
}

// Setup steps

const STEPS = {
  binary: {
    title: "Install blast-radius",
    text: "One program, no dependencies. Download it from the releases page, or build it with Go.",
    code: "go install github.com/sandeepkomal/blast-radius@latest",
  },
  hook: {
    title: "Connect it to Claude Code",
    text: "Adds a hook that checks every shell command before it runs. Use --project to protect one repository only.",
    code: "blast-radius install",
  },
  log: {
    title: "Turn on the activity log",
    text: "Writes a config with audit_log set, which feeds this page. If you already have a config, add the audit_log line to it.",
    code: 'blast-radius init --user\n# or add to your config:\n"audit_log": "~/.local/state/blast-radius/audit.jsonl"',
  },
  paranoid: {
    title: "Add the paranoid-sre skill",
    text: "Makes Claude check where it is, preview the change and plan a way back before it runs anything, so fewer commands get this far.",
    code: "claude plugin marketplace add SandeepKomal/paranoid-sre\nclaude plugin install paranoid-sre@paranoid-sre",
  },
  restart: {
    title: "Check, then restart Claude Code",
    text: "doctor tests the hook on this machine. Claude Code loads new hooks and plugins when it starts.",
    code: "blast-radius doctor",
  },
};

function codeBlock(text) {
  const pre = h("pre", {}, text);
  const btn = h("button", { class: "copy", type: "button", "aria-label": "Copy" }, "Copy");
  btn.addEventListener("click", async () => {
    try {
      await navigator.clipboard.writeText(text.split("\n").filter((l) => !l.startsWith("#")).join("\n"));
      btn.textContent = "Copied";
    } catch {
      btn.textContent = "Select and copy";
    }
    setTimeout(() => (btn.textContent = "Copy"), 1600);
  });
  return h("div", { class: "code" }, pre, btn);
}

function renderSteps(steps) {
  $("steps").replaceChildren(...steps.map((s, i) => {
    const d = STEPS[s.id];
    const label = s.unknown
      ? h("span", { class: "state unk" }, "Do this last")
      : s.done ? h("span", { class: "state ok" }, "Done") : h("span", { class: "state todo" }, "To do");
    return h("li", { class: "step" + (s.done ? " done" : ""), id: "step-" + s.id },
      h("div", { class: "step-num" }, h("span", {}, i + 1)),
      h("div", {},
        h("div", { class: "step-top" }, h("h3", {}, d.title), label),
        h("p", {}, d.text),
        s.detail && h("div", { class: "detail" }, s.detail),
        !s.done && codeBlock(d.code)));
  }));
}

// Rules

function ruleCard(name, match, rules) {
  return h("div", { class: "rule" },
    h("h3", {}, name),
    h("div", { class: "match" }, match),
    h("div", { class: "rule-rows" },
      h("div", { class: "rule-row" }, h("span", {}, "Creates and changes"), h("span", { class: "pill " + KIND[rules.change] }, RULE[rules.change])),
      h("div", { class: "rule-row" }, h("span", {}, "Deletes and replaces"), h("span", { class: "pill " + KIND[rules.destroy] }, RULE[rules.destroy]))));
}

function renderRules(st) {
  const p = st.policy;
  const cards = [ruleCard("Everywhere else", "Any environment not listed here", p.default)];
  for (const env of p.environments || []) {
    cards.push(ruleCard(env.name, (env.match || []).join("  ·  "), {
      change: env.change || p.default.change,
      destroy: env.destroy || p.default.destroy,
    }));
  }
  $("rule-cards").replaceChildren(...cards);
  $("rules-source").textContent = st.sources.length
    ? "From " + st.sources.join(" and ") + ". A repository's file can only make these stricter."
    : "The built-in policy. Run blast-radius init --user to write your own.";
  $("protect").hidden = !(p.protect && p.protect.length);
  $("protect-list").replaceChildren(...(p.protect || []).map((x) => h("span", { class: "chip neutral" }, x)));
  $("config-error").hidden = !st.config_error;
  $("config-error").textContent = st.config_error ? "Config error: " + st.config_error + ". Until it's fixed, nothing passes without asking." : "";
}

// Data

async function getJSON(url) {
  const r = await fetch(url, { cache: "no-store" });
  if (!r.ok) throw new Error(url + ": " + r.status);
  return r.json();
}

async function loadStatus() {
  const st = await getJSON("api/status");
  $("version").textContent = st.version;
  $("log-banner").hidden = st.log_enabled;
  renderSteps(st.steps);
  renderRules(st);
}

function setLive(s, text) {
  $("live").dataset.state = s;
  $("live").lastElementChild.textContent = text;
}

function connect() {
  const es = new EventSource("api/stream");
  es.addEventListener("open", () => setLive("live", "Live"));
  es.addEventListener("entry", (ev) => addEntry(JSON.parse(ev.data)));
  es.addEventListener("running", (ev) => {
    state.running = JSON.parse(ev.data);
    renderRunning();
  });
  es.addEventListener("error", () => setLive("off", "Reconnecting"));
}

async function start() {
  for (const b of document.querySelectorAll(".segmented button")) {
    b.addEventListener("click", () => {
      for (const o of document.querySelectorAll(".segmented button")) o.setAttribute("aria-selected", o === b);
      state.filter = b.dataset.filter;
      state.shown = PAGE;
      renderFeed();
    });
  }
  $("search").addEventListener("input", (e) => {
    state.query = e.target.value.trim();
    state.shown = PAGE;
    renderFeed();
  });
  $("more").addEventListener("click", () => {
    state.shown += PAGE;
    renderFeed();
  });
  for (const el of document.querySelectorAll("[data-copy]")) {
    const text = el.textContent.trim();
    el.replaceWith(codeBlock(text));
  }

  try {
    const a = await getJSON("api/activity");
    state.entries = a.entries.reverse();
    state.running = a.running;
  } catch {
    setLive("off", "Offline");
  }
  renderFeed();
  renderRunning();
  loadStatus().catch(() => {});
  connect();
  // Keep "2m ago" fresh, and pick up setup steps done in a terminal.
  setInterval(() => {
    for (const t of document.querySelectorAll("[data-ago]")) t.textContent = ago(t.dataset.ago);
  }, 15000);
  setInterval(() => loadStatus().catch(() => {}), 10000);
}

start();
