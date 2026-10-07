"use strict";

// MangoMan Code: the coding screen. It talks only to the local router, which
// passes OpenCode's calls through (/mangoman/code/oc/...). Every value from
// the server is inserted as text, never as HTML.

const OC = "/mangoman/code/oc";
const DEVICES = { phone: [390, 844, "Phone"], tablet: [820, 1180, "Tablet"], laptop: [1280, 800, "Laptop"] };
const st = {
  token: "", info: null, session: null, agent: "build",
  msgs: new Map(),   // message id -> { role, el, parts: Map(part id -> el) }
  asked: new Set(),  // permission and question ids already shown
  calls: new Map(),  // tool call id -> its element in the chat, so a request to approve shows next to it
  urls: new Set(), device: "phone",
};

function $(id) { return document.getElementById(id); }
function el(tag, attrs, ...kids) {
  const n = document.createElement(tag);
  for (const [k, v] of Object.entries(attrs || {})) {
    if (v === undefined || v === null || v === false) continue;
    if (k === "class") n.className = v;
    else if (k.startsWith("on")) n.addEventListener(k.slice(2), v);
    else n.setAttribute(k, v === true ? "" : v);
  }
  for (const k of kids.flat()) {
    if (k === null || k === undefined || k === false) continue;
    n.append(k instanceof Node ? k : document.createTextNode(String(k)));
  }
  return n;
}

function readToken() {
  const m = location.hash.match(/token=([^&]+)/);
  if (m) {
    try { sessionStorage.setItem("mm-token", decodeURIComponent(m[1])); } catch (_) {}
    history.replaceState(null, "", location.pathname);
    return decodeURIComponent(m[1]);
  }
  try { return sessionStorage.getItem("mm-token") || ""; } catch (_) { return ""; }
}

async function api(path, opts = {}) {
  const res = await fetch(path, {
    ...opts,
    headers: { "Authorization": "Bearer " + st.token, "Content-Type": "application/json", ...(opts.headers || {}) },
  });
  const text = await res.text();
  let body = null;
  try { body = text ? JSON.parse(text) : null; } catch (_) { body = text; }
  if (!res.ok) {
    const msg = (body && body.error && (body.error.message || body.error.data?.message)) || (typeof body === "string" && body) || `HTTP ${res.status}`;
    throw new Error(msg);
  }
  return body;
}

function offline(text) {
  $("offline").textContent = text;
  $("offline").hidden = !text;
}

// ---------- start ----------

async function start() {
  st.token = readToken();
  if (!st.token) { offline("Open this screen with mangoman code --ui."); return; }
  try { st.info = await api("/mangoman/code/info"); }
  catch (err) { offline(`The router did not answer: ${err.message}`); return; }
  if (!st.info.attached) { offline("No coding workspace is open. In your project folder, run: mangoman code --ui"); return; }
  $("where").textContent = st.info.dir;
  $("ship").hidden = !st.info.can_ship;
  $("ws").hidden = false;
  phoneHint();

  const sessions = await api(`${OC}/session`).catch(() => []);
  const mine = (sessions || []).filter((s) => !s.parentID && s.directory === st.info.dir)
    .sort((a, b) => (b.time?.updated || 0) - (a.time?.updated || 0));
  st.session = mine[0] || await api(`${OC}/session`, { method: "POST", body: JSON.stringify({ title: "MangoMan Code" }) });
  const history = await api(`${OC}/session/${st.session.id}/message`).catch(() => []);
  for (const m of history || []) {
    addMessage(m.info);
    for (const p of m.parts || []) showPart(p);
  }
  await refreshChanges();
  await pending();
  listen();
}

// ---------- chat ----------

function addMessage(info) {
  if (!info || info.sessionID !== st.session.id || st.msgs.has(info.id)) return st.msgs.get(info?.id);
  const li = el("li", { class: info.role === "user" ? "you" : "aiwrap" });
  $("log").append(li);
  const m = { role: info.role, el: li, parts: new Map() };
  st.msgs.set(info.id, m);
  scroll();
  return m;
}

function showPart(p) {
  if (!p || p.sessionID !== st.session.id) return;
  let m = st.msgs.get(p.messageID);
  if (!m) m = addMessage({ id: p.messageID, sessionID: p.sessionID, role: "assistant" });
  if (m.role === "user") {
    if (p.type === "text" && !p.synthetic) m.el.textContent = p.text || "";
    return;
  }
  let node = m.parts.get(p.id);
  if (p.type === "text") {
    if (!node) { node = el("div", { class: "ai" }); m.parts.set(p.id, node); m.el.append(node); }
    node.textContent = p.text || "";
  } else if (p.type === "tool") {
    const fresh = el("details", { class: "tool" + (p.state?.status === "error" ? " error" : "") }, ...toolView(p));
    if (node) node.replaceWith(fresh); else m.el.append(fresh);
    m.parts.set(p.id, fresh);
    st.calls.set(p.callID, fresh);
    if (p.tool === "bash") terminal(p);
    if (p.state?.status === "completed" && /^(write|edit|patch)$/.test(p.tool)) refreshChanges();
  }
  scroll();
}

const TOOL_WORDS = { read: "Read", write: "Wrote", edit: "Edited", bash: "Ran", glob: "Searched files", grep: "Searched text", skill: "Opened skill", webfetch: "Opened page", todowrite: "Updated plan", task: "Asked a helper", question: "Asked you" };

function toolView(p) {
  const s = p.state || {};
  const input = s.input || {};
  const what = input.filePath || input.path || input.command || input.pattern || input.name || input.url || s.title || "";
  const status = { pending: "waiting", running: "running…", completed: "done", error: "failed" }[s.status] || s.status || "";
  const out = s.status === "error" ? s.error : s.output;
  return [
    el("summary", {}, el("b", {}, TOOL_WORDS[p.tool] || p.tool), " ", String(what).slice(0, 140), el("span", { class: "st" }, status)),
    out ? el("pre", {}, String(out).slice(-6000)) : null,
  ];
}

function appendDelta(ev) {
  const m = st.msgs.get(ev.messageID);
  const node = m && m.parts.get(ev.partID);
  if (node && ev.field === "text") { node.textContent += ev.delta; scroll(); }
}

function note(text, err) {
  $("log").append(el("li", { class: "note" + (err ? " err" : "") }, text));
  scroll();
}

function scroll() { const l = $("log"); l.scrollTop = l.scrollHeight; }

async function send(text) {
  if (!text.trim()) return;
  $("send").disabled = true;
  try {
    await api(`${OC}/session/${st.session.id}/prompt_async`, {
      method: "POST", body: JSON.stringify({ agent: st.agent, parts: [{ type: "text", text }] }),
    });
    $("prompt").value = "";
    busy(true);
  } catch (err) { note(`Not sent: ${err.message}`, true); }
  $("send").disabled = false;
}

function busy(on, text) {
  $("busy").hidden = !on;
  $("stop").hidden = !on;
  $("busy-text").textContent = text || "Working…";
}

// ---------- approvals and questions ----------

async function pending() {
  const perms = await api(`${OC}/permission`).catch(() => []);
  for (const p of perms || []) askPermission(p);
  const qs = await api(`${OC}/question`).catch(() => []);
  for (const q of qs || []) askQuestion(q);
}

function askPermission(p) {
  if (p.sessionID !== st.session.id || st.asked.has(p.id)) return;
  st.asked.add(p.id);
  const what = p.metadata?.command || (p.patterns || []).join(" && ") || p.permission;
  const card = el("li", { class: "ask" });
  const reply = async (r, label) => {
    card.querySelectorAll("button").forEach((b) => { b.disabled = true; });
    try {
      await api(`${OC}/permission/${p.id}/reply`, { method: "POST", body: JSON.stringify({ reply: r }) });
      card.classList.add("done");
      card.append(el("p", { class: "note" }, label));
    } catch (err) { card.append(el("p", { class: "note err" }, `Not sent: ${err.message}`)); }
  };
  card.append(
    el("p", {}, "The AI wants to ", p.permission === "bash" ? "run " : `use ${p.permission}: `, el("code", {}, what)),
    el("div", { class: "row" },
      el("button", { type: "button", class: "primary", onclick: () => reply("once", "Allowed once.") }, "Allow"),
      el("button", { type: "button", class: "ghost", onclick: () => reply("always", "Allowed for the rest of this session.") }, "Always allow this kind"),
      el("button", { type: "button", class: "ghost", onclick: () => reply("reject", "Denied.") }, "Deny")));
  placeNear(card, p.tool && p.tool.callID);
}

function askQuestion(q) {
  if (q.sessionID !== st.session.id || st.asked.has(q.id)) return;
  st.asked.add(q.id);
  const card = el("li", { class: "ask" });
  const answers = (q.questions || []).map(() => []);
  const sendAll = async () => {
    card.querySelectorAll("button").forEach((b) => { b.disabled = true; });
    try {
      await api(`${OC}/question/${q.id}/reply`, { method: "POST", body: JSON.stringify({ answers }) });
      card.classList.add("done");
    } catch (err) { card.append(el("p", { class: "note err" }, `Not sent: ${err.message}`)); }
  };
  (q.questions || []).forEach((item, i) => {
    card.append(el("p", {}, item.question));
    card.append(el("div", { class: "row" }, (item.options || []).map((o) => el("button", {
      type: "button", class: "ghost", title: o.description || "",
      onclick: (e) => {
        if (item.multiple) {
          const k = answers[i].indexOf(o.label);
          if (k >= 0) answers[i].splice(k, 1); else answers[i].push(o.label);
          e.target.setAttribute("aria-pressed", String(k < 0));
        } else {
          answers[i] = [o.label];
          if (answers.every((a) => a.length)) sendAll();
        }
      },
    }, o.label))));
  });
  if ((q.questions || []).some((x) => x.multiple)) card.append(el("button", { type: "button", class: "primary", onclick: sendAll }, "Send answers"));
  placeNear(card, q.tool && q.tool.callID);
}

// placeNear shows a card right after the step it is about, or at the end.
function placeNear(card, callID) {
  const step = callID && st.calls.get(callID);
  if (step && step.closest("li")) step.closest("li").after(card);
  else $("log").append(card);
  scroll();
}

// ---------- live events ----------

async function listen() {
  for (let wait = 1000; ; wait = Math.min(wait * 2, 15000)) {
    try {
      const res = await fetch(`${OC}/event`, { headers: { "Authorization": "Bearer " + st.token } });
      if (!res.ok) throw new Error(`HTTP ${res.status}`);
      offline("");
      wait = 1000;
      const reader = res.body.getReader();
      const dec = new TextDecoder();
      let buf = "";
      for (;;) {
        const { value, done } = await reader.read();
        if (done) break;
        buf += dec.decode(value, { stream: true });
        let i;
        while ((i = buf.indexOf("\n\n")) >= 0) {
          const chunk = buf.slice(0, i);
          buf = buf.slice(i + 2);
          const data = chunk.split("\n").filter((l) => l.startsWith("data:")).map((l) => l.slice(5).trim()).join("\n");
          if (data) { try { onEvent(JSON.parse(data)); } catch (_) {} }
        }
      }
    } catch (_) { /* reconnect below */ }
    const info = await api("/mangoman/code/info").catch(() => null);
    if (info && !info.attached) { offline("The workspace was closed. Run mangoman code --ui again to continue."); busy(false); return; }
    offline("Reconnecting…");
    await new Promise((r) => setTimeout(r, wait));
  }
}

function onEvent(ev) {
  const p = ev.properties || {};
  switch (ev.type) {
    case "message.updated": addMessage(p.info); break;
    case "message.part.updated": showPart(p.part); break;
    case "message.part.delta": if (p.sessionID === st.session.id) appendDelta(p); break;
    case "permission.asked": askPermission(p); break;
    case "question.asked": askQuestion(p); break;
    case "session.status":
      if (p.sessionID !== st.session.id) break;
      if (p.status?.type === "idle") busy(false);
      else if (p.status?.type === "retry") busy(true, `The free model is busy, trying again${p.status.message ? `: ${p.status.message}` : ""}…`);
      else busy(true);
      break;
    case "session.idle": if (p.sessionID === st.session.id) { busy(false); refreshChanges(); } break;
    case "session.error":
      if (p.sessionID === st.session.id) {
        busy(false);
        const e = p.error || {};
        if (e.name !== "MessageAbortedError") note(`Stopped: ${e.data?.message || e.message || e.name || "an error"}`, true);
      }
      break;
    case "file.edited": case "session.diff": refreshChanges(); break;
  }
}

// ---------- changes ----------

let changesTimer = 0;
function refreshChanges() {
  clearTimeout(changesTimer);
  changesTimer = setTimeout(async () => {
    let files = await api(`${OC}/vcs/diff?mode=git`).catch(() => null);
    if (!Array.isArray(files)) files = await api(`${OC}/session/${st.session.id}/diff`).catch(() => []);
    files = files || [];
    $("changes-count").textContent = files.length ? `(${files.length})` : "";
    $("changes-empty").hidden = files.length > 0;
    $("changes").replaceChildren(...files.map((f) => el("div", { class: "file" },
      el("h3", {}, f.file || "file", el("span", { class: "plus" }, `+${f.additions || 0}`), el("span", { class: "minus" }, `-${f.deletions || 0}`),
        f.status ? el("span", { class: "dim" }, f.status) : null),
      el("pre", { class: "diff" }, (f.patch || "").split("\n").filter((l) => !/^(diff --git|index |--- |\+\+\+ )/.test(l)).map((l) =>
        el("span", { class: l.startsWith("+") ? "a" : l.startsWith("-") ? "d" : l.startsWith("@@") ? "h" : "" }, l || " "))))));
  }, 300);
}

// ---------- terminal ----------

const seenCalls = new Map(); // tool call id -> element
function terminal(p) {
  const s = p.state || {};
  const cmd = s.input?.command;
  if (!cmd) return;
  const out = s.status === "error" ? s.error : s.output || (s.status === "running" ? "running…" : "");
  const block = el("div", {}, el("div", { class: "cmd" }, "$ " + cmd), out ? el("pre", {}, String(out).slice(-8000)) : null);
  const old = seenCalls.get(p.callID);
  if (old) old.replaceWith(block); else $("term").append(block);
  seenCalls.set(p.callID, block);
  findURLs(String(out || ""));
}

function findURLs(text) {
  for (const m of text.matchAll(/https?:\/\/(?:localhost|127\.0\.0\.1|0\.0\.0\.0):\d{2,5}[^\s"'<>)]*/g)) {
    const u = m[0].replace("0.0.0.0", "localhost").replace(/[.,;]$/, "");
    if (st.urls.has(u)) continue;
    st.urls.add(u);
    // Only fill the box: the app loads when you open Preview or press Show,
    // so a page the AI started never runs in your browser unasked.
    if (!$("url").value) { $("url").value = u; phoneHint(); }
  }
}

// ---------- preview ----------

function preview() {
  const u = $("url").value.trim();
  if (!/^https?:\/\/(localhost|127\.0\.0\.1)(:\d+)?(\/|$)/.test(u)) {
    $("frames").replaceChildren(el("p", { class: "dim" }, "Only apps running on this computer can be shown here, for example http://localhost:3000."));
    return;
  }
  const names = st.device === "all" ? ["phone", "tablet", "laptop"] : [st.device];
  const room = $("frames").clientWidth || 600;
  const each = st.device === "all" ? Math.max(200, (room - 32) / 3) : room;
  $("frames").replaceChildren(...names.map((n) => {
    const [w, h, label] = DEVICES[n];
    const scale = Math.min(1, (each - 20) / w);
    const frame = el("iframe", { src: u, title: `${label} preview`, width: w, height: h, loading: "lazy", sandbox: "allow-scripts allow-forms allow-same-origin allow-popups allow-modals allow-downloads" });
    frame.style.transform = `scale(${scale})`;
    const shell = el("div", { class: "shell" }, frame);
    shell.style.width = `${w * scale}px`;
    shell.style.height = `${h * scale}px`;
    return el("div", { class: "device " + n }, shell, el("span", { class: "label" }, `${label}, ${w} wide`));
  }));
  phoneHint();
}

function phoneHint() {
  const u = $("url").value.trim();
  const port = (u.match(/:(\d+)/) || [])[1];
  const lan = st.info && st.info.lan_ip;
  $("phone-hint").textContent = lan && port
    ? `On your phone (same Wi-Fi): http://${lan}:${port}. The app must listen on your network, for example npm run dev -- --host. For a mobile app made with Expo, run npx expo start and scan its QR code with the Expo Go app.`
    : "For a mobile app made with Expo, run npx expo start and scan its QR code with the Expo Go app.";
}

// ---------- controls ----------

function setAgent(a) {
  st.agent = a;
  $("mode-plan").setAttribute("aria-pressed", String(a === "plan"));
  $("mode-build").setAttribute("aria-pressed", String(a === "build"));
}

function setTab(name) {
  for (const t of ["changes", "terminal", "preview"]) {
    $("tab-" + t).setAttribute("aria-selected", String(t === name));
    $("pane-" + t).hidden = t !== name;
  }
  if (name === "preview") preview();
}

document.addEventListener("DOMContentLoaded", () => {
  $("composer").addEventListener("submit", (e) => { e.preventDefault(); send($("prompt").value); });
  $("prompt").addEventListener("keydown", (e) => {
    if (e.key === "Enter" && (e.ctrlKey || e.metaKey)) { e.preventDefault(); send($("prompt").value); }
  });
  $("mode-plan").addEventListener("click", () => setAgent("plan"));
  $("mode-build").addEventListener("click", () => setAgent("build"));
  $("stop").addEventListener("click", () => api(`${OC}/session/${st.session.id}/abort`, { method: "POST" }).catch(() => {}));
  $("new-chat").addEventListener("click", async () => {
    try { st.session = await api(`${OC}/session`, { method: "POST", body: JSON.stringify({ title: "MangoMan Code" }) }); }
    catch (err) { note(`Could not start a new chat: ${err.message}`, true); return; }
    st.msgs.clear(); st.calls.clear(); st.asked.clear();
    $("log").replaceChildren();
    busy(false);
    $("prompt").focus();
  });
  $("ship").addEventListener("click", async () => {
    if (!confirm("Send this work to QA in dev? You approve it before it reaches production.")) return;
    note("Sending to QA in dev…");
    try {
      const r = await api("/mangoman/code/ship", { method: "POST" });
      note(r.ok ? "Queued: QA tests it in dev, then you approve it." : "Not shipped. See below.", !r.ok);
      if (r.output) $("log").append(el("li", { class: "tool" }, el("pre", {}, r.output)));
    } catch (err) { note(`Not shipped: ${err.message}`, true); }
  });
  for (const t of ["changes", "terminal", "preview"]) $("tab-" + t).addEventListener("click", () => setTab(t));
  $("runbox").addEventListener("submit", async (e) => {
    e.preventDefault();
    const cmd = $("cmd").value.trim();
    if (!cmd) return;
    $("cmd").value = "";
    try { await api(`${OC}/session/${st.session.id}/shell`, { method: "POST", body: JSON.stringify({ agent: st.agent, command: cmd }) }); }
    catch (err) { $("term").append(el("pre", {}, `Could not run: ${err.message}`)); }
  });
  $("urlbar").addEventListener("submit", (e) => { e.preventDefault(); preview(); });
  for (const b of document.querySelectorAll(".devices button")) {
    b.addEventListener("click", () => {
      st.device = b.dataset.device;
      document.querySelectorAll(".devices button").forEach((x) => x.setAttribute("aria-pressed", String(x === b)));
      preview();
    });
  }
  start();
});
