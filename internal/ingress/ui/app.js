"use strict";

// MangoMan dashboard. Talks only to the local router on this machine.
// Every value from the server is inserted as text, never as HTML.

const ORDER = ["groq", "cerebras", "nvidia", "openrouter", "zen", "ollama"];
const NUM = new Intl.NumberFormat();
const state = { token: "", overview: null, activity: null, radar: null, filter: "used", open: new Set(), drawer: false };

function $(id) { return document.getElementById(id); }

function el(tag, attrs, ...kids) {
  const n = document.createElement(tag);
  for (const [k, v] of Object.entries(attrs || {})) {
    if (v === undefined || v === null || v === false) continue;
    if (k === "class") n.className = v;
    else if (k === "style") Object.assign(n.style, v); // CSSOM: allowed by the page's strict CSP
    else if (k.startsWith("on")) n.addEventListener(k.slice(2), v);
    else n.setAttribute(k, v === true ? "" : v);
  }
  for (const k of kids.flat()) {
    if (k === null || k === undefined || k === false) continue;
    n.append(k instanceof Node ? k : document.createTextNode(String(k)));
  }
  return n;
}

function svg(tag, attrs) {
  const n = document.createElementNS("http://www.w3.org/2000/svg", tag);
  for (const [k, v] of Object.entries(attrs || {})) n.setAttribute(k, v);
  return n;
}

function color(provider) {
  return ORDER.includes(provider) ? `var(--s-${provider})` : "var(--muted)";
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
    headers: { "Authorization": "Bearer " + state.token, "Content-Type": "application/json", ...(opts.headers || {}) },
  });
  let body = null;
  try { body = await res.json(); } catch (_) {}
  if (!res.ok) {
    const err = new Error((body && body.error && body.error.message) || `HTTP ${res.status}`);
    err.status = res.status;
    throw err;
  }
  return body;
}

// ---------- formatting ----------

function duration(s) {
  if (s < 90) return `${s} s`;
  if (s < 5400) return `${Math.round(s / 60)} min`;
  if (s < 172800) return `${Math.round(s / 3600)} h`;
  return `${Math.round(s / 86400)} days`;
}
function seconds(ms) {
  if (!ms) return "–";
  return ms < 10000 ? `${(ms / 1000).toFixed(1)} s` : `${Math.round(ms / 1000)} s`;
}
function clock(t) { return new Date(t).toLocaleTimeString([], { hour: "2-digit", minute: "2-digit", second: "2-digit" }); }
function hm(t) { return new Date(t).toLocaleTimeString([], { hour: "2-digit", minute: "2-digit" }); }
function plural(n, one, many) { return `${NUM.format(n)} ${n === 1 ? one : many}`; }

function limitText(l) {
  const parts = [];
  if (l.rpd) parts.push(`${NUM.format(l.rpd)}/day`);
  if (l.rpm) parts.push(`${NUM.format(l.rpm)}/min`);
  return parts.length ? parts.join(", ") : "Not published";
}
function limitTitle(l, source) {
  const parts = [];
  if (l.rpd) parts.push(`${NUM.format(l.rpd)} requests a day`);
  if (l.rpm) parts.push(`${NUM.format(l.rpm)} requests a minute`);
  if (l.tpm) parts.push(`${NUM.format(l.tpm)} tokens a minute`);
  if (l.tpd) parts.push(`${NUM.format(l.tpd)} tokens a day`);
  const from = source === "provider" ? "Reported by the provider." : "From the catalogue; updates once the provider reports its own.";
  return (parts.length ? parts.join(", ") + ". " : "") + from;
}

const TRAINS = { no: "Not used for training", yes: "May be used for training", "opt-out": "Training, opt-out available" };
function dataText(trains) { return TRAINS[trains] || "Training policy unknown"; }

const OUTCOMES = {
  ok: "Answered", ok_truncated: "Answered, cut off", rate_limited: "Rate limited",
  network_error: "Couldn't reach provider", timeout: "Timed out", server_error: "Provider error",
  key_rejected: "Key rejected", model_not_found: "Model no longer available", model_forbidden: "Model not available to you", client_error: "Request rejected",
  stream_error: "Stream failed", stream_broken_after_commit: "Stream broke midway", error_in_200: "Provider error",
  not_a_stream: "Provider error", client_gone: "You cancelled", "quality:empty": "Empty answer",
  "quality:truncated": "Cut-off answer", "quality:invalid_json": "Broken JSON", "quality:bad_tool_call": "Broken tool call",
  "quality:unparseable": "Unreadable answer",
};
function outcomeText(o) { return OUTCOMES[o] || o; }
function outcomeKind(o) {
  if (o.startsWith("ok")) return "good";
  if (o === "rate_limited") return "warning";
  if (o === "client_gone") return "off";
  return o.startsWith("quality:") ? "serious" : "critical";
}

// ---------- header and totals ----------

function renderHead(ov, act) {
  const run = $("runline");
  run.replaceChildren(
    el("span", { class: "lamp on", "aria-hidden": "true" }),
    `Running at 127.0.0.1:${ov.port} for ${duration(ov.uptime_s)}. Version ${ov.version}, catalogue ${ov.catalogue}.`
  );
  const tokens = (act.rows || []).reduce((a, r) => a + (r.tokens || 0), 0);
  $("t-requests").textContent = NUM.format(act.requests || 0);
  $("t-served").textContent = NUM.format(act.served || 0);
  $("t-failover").textContent = NUM.format(act.failed_over || 0);
  $("t-tokens").textContent = NUM.format(tokens);
}

// ---------- connections board ----------

function providerTraffic(act) {
  const out = {};
  for (const b of act.hourly || []) {
    out[b.provider] = out[b.provider] || { attempts: 0, ok: 0 };
    out[b.provider].attempts += b.attempts;
    out[b.provider].ok += b.ok;
  }
  return out;
}

function stateFor(p) {
  switch (p.status) {
    case "connected":
      return { lamp: "on", text: p.key_source === "env" ? `Connected through ${p.env_var}` : "Connected" };
    case "running": return { lamp: "on", text: "Running on this computer" };
    case "key_rejected": return { lamp: "bad", text: "Key rejected, needs a new one" };
    case "excluded": return { lamp: "", text: "Turned off" };
    case "not_running": return { lamp: "", text: "Not running" };
    default: return { lamp: "", text: "Not connected" };
  }
}

function renderBoard(ov, act) {
  const traffic = providerTraffic(act);
  const max = Math.max(1, ...Object.values(traffic).map((t) => t.attempts));
  const live = (p) => p.status === "connected" || p.status === "running";
  const provs = [...ov.providers].sort((a, b) => {
    if (live(a) !== live(b)) return live(a) ? -1 : 1;
    const ta = (traffic[a.id] || {}).attempts || 0, tb = (traffic[b.id] || {}).attempts || 0;
    if (ta !== tb) return tb - ta;
    return ORDER.indexOf(a.id) - ORDER.indexOf(b.id);
  });
  const cloud = ov.providers.filter((p) => p.needs_key);
  const connected = cloud.filter((p) => p.status === "connected").length;
  $("conn-sub").textContent = `${connected} of ${cloud.length} free cloud providers connected. One is enough to start.`;

  $("board").replaceChildren(...provs.map((p) => {
    const st = stateFor(p);
    const t = traffic[p.id] || { attempts: 0, ok: 0 };
    const off = !live(p);
    const actions = [];
    const toggle = (name, fn) => el("button", { type: "button", class: "ghost", onclick: fn }, name);

    if (p.status === "connected") {
      actions.push(toggle("Turn off", () => setExcluded(p.id, true)));
      if (p.key_source !== "env") actions.push(toggle("Remove key", () => removeKey(p)));
    } else if (p.status === "excluded") {
      actions.push(toggle("Turn on", () => setExcluded(p.id, false)));
    } else if (p.local) {
      if (p.status === "not_running") actions.push(el("a", { class: "link", href: p.signup_url, target: "_blank", rel: "noopener noreferrer" }, "Get Ollama"));
    } else {
      const label = p.status === "key_rejected" ? "Replace key" : "Connect";
      actions.push(el("button", {
        type: "button", class: "primary", "aria-expanded": String(state.open.has(p.id)),
        onclick: () => { state.open.has(p.id) ? state.open.delete(p.id) : state.open.add(p.id); renderBoard(state.overview, state.activity); },
      }, label));
    }

    const row = el("li", { class: "line" + (off ? " off" : "") },
      el("div", { class: "line-main" },
        el("span", { class: "lamp " + st.lamp, "aria-hidden": "true" }),
        el("div", { class: "pname" }, p.name, el("small", {}, `${plural(p.models, "free model", "free models")}. ${dataText(p.trains_on_data)}.`)),
        el("div", { class: "pstate" }, st.text, p.models_unavailable ? el("small", { class: "dim" }, ` (${p.models_unavailable} cooling down)`) : null),
        el("div", { class: "share", title: `${t.attempts} requests tried, ${t.ok} answered, last 24 hours` },
          el("span", { class: "bar" }, el("i", { style: { width: `${(100 * t.attempts / max).toFixed(1)}%`, background: color(p.id) } })),
          el("span", { class: "count" }, t.attempts ? plural(t.attempts, "request", "requests") : "No requests")),
        el("div", { class: "actions" }, actions)));

    if (state.open.has(p.id) && p.needs_key) row.append(connectPanel(p));
    return row;
  }));
}

function connectPanel(p) {
  const input = el("input", { type: "password", autocomplete: "off", spellcheck: "false", placeholder: `Paste your ${p.name} key`, "aria-label": `${p.name} API key` });
  const msg = el("p", { class: "msg", role: "status" });
  const btn = el("button", { type: "submit", class: "primary" }, "Connect");
  const form = el("form", {
    onsubmit: async (e) => {
      e.preventDefault();
      if (!input.value.trim()) { msg.className = "msg err"; msg.textContent = "Paste a key first."; return; }
      btn.disabled = true; msg.className = "msg"; msg.textContent = `Checking with ${p.name}…`;
      try {
        await api("/mangoman/keys", { method: "POST", body: JSON.stringify({ provider: p.id, key: input.value.trim() }) });
        input.value = "";
        state.open.delete(p.id);
        await load();
      } catch (err) {
        msg.className = "msg err"; msg.textContent = `Not connected: ${err.message}`;
        btn.disabled = false;
      }
    },
  }, input, btn);
  setTimeout(() => input.focus(), 0);
  return el("div", { class: "connect" },
    el("p", {}, "Get a free key from ",
      el("a", { class: "link", href: p.signup_url, target: "_blank", rel: "noopener noreferrer" }, p.name),
      `, then paste it here. It is checked with ${p.name} and saved in this computer's keychain.`),
    form, msg);
}

async function setExcluded(id, excluded) {
  try { await api(`/mangoman/providers/${encodeURIComponent(id)}/exclude`, { method: "POST", body: JSON.stringify({ excluded }) }); }
  catch (err) { alert(err.message); }
  await load();
}

async function removeKey(p) {
  if (!confirm(`Remove the ${p.name} key from this computer? MangoMan stops using ${p.name} until you connect it again.`)) return;
  try { await api(`/mangoman/keys/${encodeURIComponent(p.id)}`, { method: "DELETE" }); }
  catch (err) { alert(err.message); }
  await load();
}

// ---------- My list ----------

function sameEntry(a, b) { return a.toLowerCase() === b.toLowerCase(); }
function entryMatches(entry, m) { return sameEntry(entry, m.model) || sameEntry(entry, `${m.provider}/${m.model}`); }
function inMyList(m) { return (state.overview.favorites || []).some((f) => entryMatches(f, m)); }

// What a My list entry resolves to right now.
function entryStatus(entry, ov) {
  const ms = ov.models.filter((m) => entryMatches(entry, m));
  const names = Object.fromEntries(ov.providers.map((p) => [p.id, p.name]));
  const pinned = entry.includes("/");
  const where = pinned ? (names[ms[0] && ms[0].provider] || entry.split("/")[0])
    : ms.length === 1 ? names[ms[0].provider] : `${ms.length} providers`;
  if (!ms.length) return { lamp: "bad", text: "No longer in the catalogue", where: "" };
  const ready = ms.filter((m) => m.state === "ready");
  if (ready.length) return { lamp: "on", text: ready.length < ms.length ? `Ready on ${ready.length} of ${ms.length}` : "Ready", where };
  const limited = ms.filter((m) => m.state === "rate_limited" && m.blocked_until);
  if (limited.length) {
    const back = limited.map((m) => new Date(m.blocked_until)).sort((a, b) => a - b)[0];
    return { lamp: "warn", text: `Used up, back at ${hm(back)}`, where };
  }
  if (ms.some((m) => m.state === "cooling_down")) return { lamp: "warn", text: "Cooling down after errors", where };
  return { lamp: "", text: "Provider not connected", where };
}

async function saveMyList(models) {
  try {
    const res = await api("/mangoman/favorites", { method: "PUT", body: JSON.stringify({ models }) });
    state.overview.favorites = res.models;
  } catch (err) { alert(err.message); }
  renderMyList(state.overview);
  renderModels(state.overview, state.activity);
}

function renderMyList(ov) {
  const fav = ov.favorites || [];
  const move = (i, d) => { const m = [...fav]; [m[i], m[i + d]] = [m[i + d], m[i]]; saveMyList(m); };
  $("mylist").replaceChildren(...fav.map((f, i) => {
    const st = entryStatus(f, ov);
    const [prov, name] = f.includes("/") ? [f.slice(0, f.indexOf("/")), f.slice(f.indexOf("/") + 1)] : ["", f];
    return el("li", { class: "fav" },
      el("span", { class: "pos" }, String(i + 1)),
      el("div", { class: "fname" }, name,
        el("small", {}, prov ? `${st.where} only` : `Any provider (${st.where})`)),
      el("div", { class: "fstate" }, el("span", { class: "lamp " + st.lamp, "aria-hidden": "true" }), st.text),
      el("div", { class: "actions" },
        el("button", { type: "button", class: "ghost icon", "aria-label": `Move ${name} up`, disabled: i === 0, onclick: () => move(i, -1) }, "↑"),
        el("button", { type: "button", class: "ghost icon", "aria-label": `Move ${name} down`, disabled: i === fav.length - 1, onclick: () => move(i, 1) }, "↓"),
        el("button", { type: "button", class: "ghost", onclick: () => saveMyList(fav.filter((_, j) => j !== i)) }, "Remove")));
  }));
  const empty = $("mylist-empty");
  empty.hidden = fav.length > 0;
  empty.textContent = "Your list is empty, so MangoMan picks the best free model for each request. Add the models you prefer and they are tried first.";

  // Picker: connected models not yet in the list, then the rest.
  const pick = $("mylist-pick");
  const names = Object.fromEntries(ov.providers.map((p) => [p.id, p.name]));
  const avail = ov.models.filter((m) => !inMyList(m))
    .sort((a, b) => Number(b.connected) - Number(a.connected) || a.model.localeCompare(b.model) || a.provider.localeCompare(b.provider));
  const groups = [["Connected", avail.filter((m) => m.connected)], ["Not connected yet", avail.filter((m) => !m.connected)]];
  pick.replaceChildren(el("option", { value: "" }, "Choose a model"),
    ...groups.filter(([, ms]) => ms.length).map(([label, ms]) => el("optgroup", { label },
      ...ms.map((m) => el("option", { value: `${m.provider}/${m.model}` }, `${m.model} (${names[m.provider] || m.provider})`)))));
}

async function toggleStar(m) {
  const fav = state.overview.favorites || [];
  if (inMyList(m)) await saveMyList(fav.filter((f) => !entryMatches(f, m)));
  else await saveMyList([...fav, `${m.provider}/${m.model}`]);
}

// ---------- new models drawer ----------

function ago(t) {
  const d = Math.max(0, (Date.now() - new Date(t).getTime()) / 1000);
  if (d < 3600) return "within the hour";
  if (d < 86400) return `${Math.round(d / 3600)} h ago`;
  return `${Math.round(d / 86400)} days ago`;
}

function renderFab() {
  const rv = state.radar;
  const fab = $("fab");
  fab.hidden = !rv || !rv.enabled;
  if (fab.hidden) return;
  const n = rv.new_count || 0;
  fab.classList.toggle("has-new", n > 0);
  $("fab-text").textContent = n ? plural(n, "new model", "new models") : "New models";
  fab.setAttribute("aria-expanded", String(state.drawer));
}

function renderDrawer() {
  const rv = state.radar || { items: [] };
  const ov = state.overview;
  const names = Object.fromEntries(ov.providers.map((p) => [p.id, p.name]));
  $("drawer-sub").textContent = rv.last_scan
    ? `Free models your connected providers serve that are not in your catalogue yet. Checked ${ago(rv.last_scan)}; checked again every 6 hours.`
    : "Not checked yet. The first check runs shortly after the router starts.";
  const list = $("radar");
  if (!rv.items.length) {
    list.replaceChildren(el("li", { class: "empty" }, rv.last_scan ? "Nothing new right now. You have every free model your providers list." : "Press Check now to look for new models."));
    return;
  }
  list.replaceChildren(...rv.items.map((it) => {
    const btn = el("button", { type: "button", class: "primary" }, "Add to my list");
    btn.addEventListener("click", async () => {
      btn.disabled = true; btn.textContent = "Adding…";
      try {
        await api("/mangoman/radar/add", { method: "POST", body: JSON.stringify({ provider: it.provider, upstream: it.upstream }) });
        $("drawer-msg").className = "msg ok";
        $("drawer-msg").textContent = `${it.name} added to My list.`;
        await load();
      } catch (err) {
        btn.disabled = false; btn.textContent = "Add to my list";
        $("drawer-msg").className = "msg err"; $("drawer-msg").textContent = err.message;
      }
    });
    return el("li", { class: "ritem" },
      el("div", { class: "rmain" },
        el("div", { class: "rname" }, it.new ? el("span", { class: "badge" }, "New") : null, it.name),
        el("div", { class: "rmeta" }, el("i", { class: "dot", style: { background: color(it.provider) } }),
          `${names[it.provider] || it.provider} · first seen ${ago(it.first_seen)}`),
        el("div", { class: "rmeta dim", title: it.data_policy }, `${dataText(it.trains_on_data)} · ${it.upstream}`)),
      btn);
  }));
}

function openDrawer(open) {
  state.drawer = open;
  $("drawer").hidden = !open;
  renderFab();
  if (open) { renderDrawer(); $("drawer-close").focus(); } else { $("fab").focus(); }
}

async function scanNow() {
  const btn = $("drawer-scan"), msg = $("drawer-msg");
  btn.disabled = true; msg.className = "msg"; msg.textContent = "Checking your providers…";
  try {
    state.radar = await api("/mangoman/radar/scan", { method: "POST" });
    msg.textContent = state.radar.new_count ? `${plural(state.radar.new_count, "new model", "new models")} found.` : "Checked. Nothing new.";
  } catch (err) { msg.className = "msg err"; msg.textContent = err.message; }
  btn.disabled = false;
  renderFab(); renderDrawer();
}

// ---------- chart: requests per hour, stacked by provider ----------

function renderChart(act, ov) {
  // Buckets are UTC hours (as the server groups them). In time zones with a
  // half-hour offset, such as India, labels read 10:30, 11:30 and so on.
  const now = new Date(); now.setUTCMinutes(0, 0, 0);
  const hours = [];
  for (let i = 23; i >= 0; i--) hours.push(new Date(now.getTime() - i * 3600e3));
  const byHour = new Map(hours.map((h) => [h.getTime(), {}]));
  const seen = new Set();
  for (const b of act.hourly || []) {
    const key = new Date(b.hour).getTime();
    if (!byHour.has(key)) continue;
    byHour.get(key)[b.provider] = b;
    seen.add(b.provider);
  }
  const provs = [...ORDER.filter((p) => seen.has(p)), ...[...seen].filter((p) => !ORDER.includes(p))];
  const names = Object.fromEntries((ov.providers || []).map((p) => [p.id, p.name]));

  $("legend").replaceChildren(...provs.map((p) => el("span", {}, el("i", { style: { background: color(p) } }), names[p] || p)));

  const wrap = $("chart");
  if (!provs.length) {
    wrap.replaceChildren(el("p", { class: "empty" },
      `No requests yet. Point a tool at http://127.0.0.1:${ov.port}/v1 with model free/auto, or run mangoman test.`));
    return;
  }
  const W = Math.max(280, Math.round(wrap.clientWidth - 24)), H = 220, L = 36, R = 8, T = 10, B = 26;
  const every = W < 520 ? 6 : W < 800 ? 4 : 3;
  const totals = hours.map((h) => provs.reduce((a, p) => a + ((byHour.get(h.getTime())[p] || {}).attempts || 0), 0));
  const top = niceMax(Math.max(1, ...totals));
  const y = (v) => T + (H - T - B) * (1 - v / top);
  const slot = (W - L - R) / hours.length;
  const bw = Math.max(4, slot * 0.62);

  const g = svg("svg", { viewBox: `0 0 ${W} ${H}`, width: W, height: H, role: "img",
    "aria-label": `Requests per hour for the last 24 hours, ${totals.reduce((a, b) => a + b, 0)} in total` });
  for (const v of [0, top / 2, top]) {
    g.append(svg("line", { class: v === 0 ? "base" : "grid-line", x1: L, x2: W - R, y1: y(v), y2: y(v) }));
    const t = svg("text", { x: L - 6, y: y(v) + 4, "text-anchor": "end" }); t.textContent = NUM.format(v); g.append(t);
  }
  hours.forEach((h, i) => {
    const x = L + i * slot + (slot - bw) / 2;
    let acc = 0;
    const segs = provs.map((p) => ({ p, v: (byHour.get(h.getTime())[p] || {}).attempts || 0 })).filter((s) => s.v > 0);
    segs.forEach((s, j) => {
      const y0 = y(acc), y1 = y(acc + s.v);
      acc += s.v;
      const gap = j > 0 ? 2 : 0; // surface gap between stacked fills
      const hgt = Math.max(1, y0 - y1 - gap);
      const isTop = j === segs.length - 1;
      g.append(svg("path", { d: barPath(x, y1, bw, hgt, isTop ? Math.min(4, bw / 2, hgt) : 0), fill: color(s.p) }));
    });
    if (i % every === 0) {
      const t = svg("text", { x: L + i * slot + slot / 2, y: H - 8, "text-anchor": "middle" });
      t.textContent = hm(h); g.append(t);
    }
    const hit = svg("rect", { class: "hit", x: L + i * slot, y: T, width: slot, height: H - T - B });
    hit.addEventListener("mousemove", (e) => showTip(e, h, provs, byHour.get(h.getTime()), names));
    hit.addEventListener("mouseleave", hideTip);
    g.append(hit);
  });
  wrap.replaceChildren(g);
}

function barPath(x, y, w, h, r) {
  if (r <= 0) return `M${x},${y}h${w}v${h}h${-w}z`;
  return `M${x},${y + r}a${r},${r} 0 0 1 ${r},${-r}h${w - 2 * r}a${r},${r} 0 0 1 ${r},${r}v${h - r}h${-w}z`;
}

function niceMax(v) {
  const p = Math.pow(10, Math.floor(Math.log10(v)));
  for (const m of [1, 2, 2.5, 5, 10]) if (m * p >= v) return m * p;
  return 10 * p;
}

function showTip(e, h, provs, row, names) {
  const tip = $("tip");
  const end = new Date(h.getTime() + 3600e3);
  const lines = provs.filter((p) => row[p]).map((p) =>
    el("div", {}, el("span", {}, el("i", { class: "dot", style: { background: color(p) } }), names[p] || p),
      el("span", {}, `${row[p].attempts} tried, ${row[p].ok} answered`)));
  tip.replaceChildren(el("b", {}, `${hm(h)} to ${hm(end)}`), ...(lines.length ? lines : [el("div", {}, "No requests")]));
  tip.hidden = false;
  const x = Math.min(e.clientX + 14, window.innerWidth - tip.offsetWidth - 8);
  tip.style.left = `${x}px`;
  tip.style.top = `${e.clientY + 14}px`;
}
function hideTip() { $("tip").hidden = true; }

// ---------- models table ----------

const MSTATE = {
  ready: ["good", "Ready"], rate_limited: ["warning", "Rate limited"],
  cooling_down: ["serious", "Cooling down after errors"], not_connected: ["off", "Not connected"],
};

function renderModels(ov, act) {
  const names = Object.fromEntries(ov.providers.map((p) => [p.id, p.name]));
  const usage = {};
  for (const r of act.rows || []) usage[`${r.provider}/${r.model}`] = r;
  let rows = ov.models.map((m) => ({ m, u: usage[`${m.provider}/${m.model}`] }));
  if (state.filter === "used") rows = rows.filter((r) => r.u || r.m.samples);
  if (state.filter === "connected") rows = rows.filter((r) => r.m.connected);
  rows.sort((a, b) => ((b.u || {}).attempts || 0) - ((a.u || {}).attempts || 0)
    || Number(b.m.connected) - Number(a.m.connected) || a.m.model.localeCompare(b.m.model));

  const body = $("models").tBodies[0];
  body.replaceChildren(...rows.map(({ m, u }) => {
    const [kind, label] = MSTATE[m.state] || ["off", m.state];
    const stateText = m.state === "rate_limited" && m.blocked_until ? `${label} until ${hm(m.blocked_until)}` : label;
    const attempts = u ? u.attempts : 0;
    const okPct = u && u.attempts ? `${Math.round(100 * u.ok / u.attempts)}%` : "–";
    const starred = inMyList(m);
    return el("tr", {},
      el("td", { class: "star-col" }, el("button", {
        type: "button", class: "star" + (starred ? " on" : ""), "aria-pressed": String(starred),
        "aria-label": starred ? `Remove ${m.model} (${names[m.provider] || m.provider}) from My list` : `Add ${m.model} (${names[m.provider] || m.provider}) to My list`,
        title: starred ? "In My list" : "Add to My list", onclick: () => toggleStar(m),
      }, starred ? "★" : "☆")),
      el("td", { title: m.upstream }, m.model),
      el("td", {}, el("i", { class: "dot", style: { background: color(m.provider) } }), names[m.provider] || m.provider),
      el("td", {}, el("span", { class: "state" }, el("span", { class: `lamp ${kind === "good" ? "on" : kind === "warning" ? "warn" : kind === "off" ? "" : "bad"}`, "aria-hidden": "true" }), stateText)),
      el("td", { class: "r" }, NUM.format(attempts)),
      el("td", { class: "r" }, okPct),
      el("td", { class: "r" }, seconds(m.latency_ms || (u && u.p50_ms))),
      el("td", { class: "r" }, NUM.format((u && u.tokens) || m.tokens_24h || 0)),
      el("td", { class: m.limits_source === "provider" ? "" : "dim", title: limitTitle(m.limits, m.limits_source) },
        limitText(m.limits)),
      el("td", { class: "dim", title: m.data_policy }, dataText(m.trains_on_data)));
  }));
  const empty = $("models-empty");
  empty.hidden = rows.length > 0;
  empty.textContent = state.filter === "used"
    ? "No model has been used yet. Requests you send through MangoMan show up here."
    : "No connected models. Connect a provider above.";
}

// ---------- recent requests ----------

function renderRecent(ov, act) {
  const names = Object.fromEntries(ov.providers.map((p) => [p.id, p.name]));
  const all = act.recent || [];
  const recent = state.showAll ? all : all.slice(0, 12);
  $("recent").tBodies[0].replaceChildren(...recent.map((e) => {
    const kind = outcomeKind(e.outcome);
    return el("tr", {},
      el("td", { class: "dim" }, clock(e.time)),
      el("td", {}, e.class || "–"),
      el("td", {}, el("i", { class: "dot", style: { background: color(e.provider) } }), `${names[e.provider] || e.provider} / ${e.model}`),
      el("td", {}, el("span", { class: "state" }, el("span", { class: `lamp ${kind === "good" ? "on" : kind === "warning" ? "warn" : kind === "off" ? "" : "bad"}`, "aria-hidden": "true" }), outcomeText(e.outcome))),
      el("td", { class: "r" }, e.attempt),
      el("td", { class: "r" }, seconds(e.latency_ms)));
  }));
  const empty = $("recent-empty");
  empty.hidden = recent.length > 0;
  empty.textContent = "No requests yet.";
  const wrap = $("recent").parentElement;
  wrap.querySelector(".more")?.remove();
  if (all.length > 12) {
    wrap.append(el("p", { class: "more" }, el("button", { type: "button", class: "ghost", onclick: () => { state.showAll = !state.showAll; renderRecent(ov, act); } },
      state.showAll ? "Show fewer" : `Show all ${all.length}`)));
  }
}

// ---------- loading ----------

async function load() {
  try {
    const [ov, act, rv] = await Promise.all([api("/mangoman/overview"), api("/mangoman/activity?hours=24"),
      api("/mangoman/radar").catch(() => null)]);
    state.overview = ov; state.activity = act; state.radar = rv;
    $("app").hidden = false; $("gate").hidden = true;
    renderHead(ov, act);
    renderBoard(ov, act);
    renderMyList(ov);
    renderFab();
    if (state.drawer) renderDrawer();
    renderChart(act, ov);
    renderModels(ov, act);
    renderRecent(ov, act);
  } catch (err) {
    if (err.status === 401) { $("app").hidden = true; $("gate").hidden = false; $("runline").textContent = "No access token."; return; }
    $("runline").replaceChildren(el("span", { class: "lamp bad", "aria-hidden": "true" }), `Can't reach the router: ${err.message}. Is mangoman serve running?`);
  }
}

document.addEventListener("DOMContentLoaded", () => {
  state.token = readToken();
  if (!state.token) { $("gate").hidden = false; $("runline").textContent = "No access token."; return; }
  $("refresh").addEventListener("click", load);
  $("mylist-add").addEventListener("submit", (e) => {
    e.preventDefault();
    const v = $("mylist-pick").value;
    if (v) saveMyList([...(state.overview.favorites || []), v]);
  });
  $("mylist-new").addEventListener("click", () => openDrawer(true));
  $("fab").addEventListener("click", () => openDrawer(!state.drawer));
  $("drawer-close").addEventListener("click", () => openDrawer(false));
  $("drawer-scan").addEventListener("click", scanNow);
  document.addEventListener("keydown", (e) => { if (e.key === "Escape" && state.drawer) openDrawer(false); });
  for (const b of document.querySelectorAll(".seg button")) {
    b.addEventListener("click", () => {
      state.filter = b.dataset.filter;
      for (const o of document.querySelectorAll(".seg button")) o.setAttribute("aria-pressed", String(o === b));
      if (state.overview) renderModels(state.overview, state.activity);
    });
  }
  let resizeTimer;
  window.addEventListener("resize", () => {
    clearTimeout(resizeTimer);
    resizeTimer = setTimeout(() => { if (state.overview) renderChart(state.activity, state.overview); }, 150);
  });
  load();
  setInterval(() => { if (!document.hidden && !state.open.size && !state.drawer) load(); }, 15000);
});
