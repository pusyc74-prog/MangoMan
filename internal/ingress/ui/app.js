"use strict";

// MangoMan dashboard. Talks only to the local router on this machine.
// Every value from the server is inserted as text, never as HTML.

const ORDER = ["nvidia", "groq", "openrouter", "ollama", "cerebras"];
const NUM = new Intl.NumberFormat();
const state = { token: "", overview: null, activity: null, radar: null, filter: "used", open: new Set(), openTeam: new Set(), drawer: false, starters: null, team: [] };

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
  ok: "Answered", ok_truncated: "Answered, cut off", no_answer: "Only thinking, no answer", garbled: "Garbled answer", rate_limited: "Rate limited",
  network_error: "Couldn't reach provider", timeout: "Timed out", server_error: "Provider error",
  key_rejected: "Key rejected", model_gone: "Model retired by the provider", model_not_found: "Model no longer available", model_forbidden: "Model not available to you", client_error: "Request rejected",
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
    `Running for ${duration(ov.uptime_s)}`
  );
  run.title = `127.0.0.1:${ov.port}, version ${ov.version}, catalogue ${ov.catalogue}`;
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
      return { lamp: "on", text: p.key_source === "env" ? `Connected through ${p.env_var}` : p.key_source === "team" ? (p.own_rejected ? "Connected through team keys. Your own key was rejected" : "Connected through team keys") : "Connected" };
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
      if (p.key_source === "store") actions.push(toggle("Remove key", () => removeKey(p)));
      if (p.key_source === "team") actions.push(toggle(p.own_rejected ? "Replace my key" : "Add my key", () => { state.open.add(p.id); renderBoard(state.overview, state.activity); }));
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

    if (p.needs_key && !p.no_team_keys && p.status !== "excluded") {
      actions.push(toggle("Add team key", () => { state.openTeam.has(p.id) ? state.openTeam.delete(p.id) : state.openTeam.add(p.id); renderBoard(state.overview, state.activity); }));
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
    if ((p.team_keys || []).length) row.append(teamList(p));
    if (state.openTeam.has(p.id)) row.append(teamPanel(p));
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
      `, then paste it here. It is checked with ${p.name} and saved in ${state.overview.key_store === "os-keychain" ? "this computer's keychain" : "an encrypted file on this computer"}.`),
    form, msg);
}

// ---------- team keys ----------

const TEAM_STATE = {
  working: "working", resting: "resting", rejected: "key rejected, needs a new one", missing: "key missing, add it again",
};

function teamList(p) {
  return el("ul", { class: "team-keys", "aria-label": `${p.name} team keys` }, p.team_keys.map((k) =>
    el("li", {},
      el("span", { class: "lamp " + (k.status === "working" ? "on" : k.status === "resting" ? "" : "bad"), "aria-hidden": "true" }),
      el("b", {}, k.name),
      ` ${TEAM_STATE[k.status] || k.status}${k.status === "resting" && k.rests_until ? ` until ${hm(k.rests_until)}` : ""}, ${plural(k.requests_today, "request", "requests")} today `,
      el("button", { type: "button", class: "ghost", onclick: () => removeTeamKey(p, k.name) }, "Remove"))));
}

function teamPanel(p) {
  const name = el("input", { type: "text", autocomplete: "off", spellcheck: "false", maxlength: "20", placeholder: "Teammate's name, e.g. ravi", "aria-label": "Teammate's name" });
  const key = el("input", { type: "password", autocomplete: "off", spellcheck: "false", placeholder: `Paste their ${p.name} key`, "aria-label": `Teammate's ${p.name} API key` });
  const agree = el("input", { type: "checkbox", id: `agree-${p.id}` });
  const msg = el("p", { class: "msg", role: "status" });
  const btn = el("button", { type: "submit", class: "primary" }, "Add team key");
  const form = el("form", {
    onsubmit: async (e) => {
      e.preventDefault();
      if (!name.value.trim() || !key.value.trim()) { msg.className = "msg err"; msg.textContent = "Give a name and a key."; return; }
      if (!agree.checked) { msg.className = "msg err"; msg.textContent = "Tick the box once the key's owner has agreed."; return; }
      btn.disabled = true; msg.className = "msg"; msg.textContent = `Checking with ${p.name}…`;
      try {
        await api("/mangoman/keys", { method: "POST", body: JSON.stringify({ provider: p.id, key: key.value.trim(), team: name.value.trim() }) });
        key.value = "";
        state.openTeam.delete(p.id);
        await load();
      } catch (err) {
        msg.className = "msg err"; msg.textContent = `Not added: ${err.message}`;
        btn.disabled = false;
      }
    },
  }, name, key, btn);
  setTimeout(() => name.focus(), 0);
  return el("div", { class: "connect" },
    el("p", {}, `Add a teammate's own ${p.name} key. Requests take turns across every ${p.name} key on this computer, so the team gets more free use of the same models.`),
    el("p", { class: "notice" }, state.overview.team_key_notice),
    el("label", { class: "agree", for: `agree-${p.id}` }, agree, " The key's owner has read this and agreed."),
    form, msg);
}

async function removeTeamKey(p, name) {
  if (!confirm(`Remove ${name}'s ${p.name} key from this computer?`)) return;
  try { await api(`/mangoman/keys/${encodeURIComponent(p.id)}/team/${encodeURIComponent(name)}`, { method: "DELETE" }); }
  catch (err) { alert(err.message); }
  await load();
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

// ---------- decision brain ----------

const BRAIN_KIND = { task: "Task type", non_answer: "Is it a non-answer?", test: "Test question" };
const BRAIN_FALLBACK = { timeout: "too slow, rules kept", error: "engine unavailable, rules kept", unsure: "unsure, rules kept", bad_answer: "unclear reply, rules kept" };

function renderBrain(ov) {
  const b = ov.brain;
  $("brain-block").hidden = !b;
  if (!b) return;
  $("brain-lamp").className = "lamp " + (b.enabled ? "on" : "");
  const parts = [`${b.enabled ? "On" : "Off"}, engine ${b.model}.`];
  if (b.calls || b.cache_hits) {
    parts.push(`${plural(b.decided, "decision", "decisions")}, ${NUM.format(b.fallbacks)} kept the rules, ${NUM.format(b.cache_hits)} from memory; average ${seconds(b.avg_ms)}.`);
  } else {
    parts.push("No decisions yet.");
  }
  $("brain-summary").textContent = parts.join(" ");
  const btn = $("brain-toggle");
  btn.textContent = b.enabled ? "Turn off" : "Turn on";
  btn.onclick = async () => {
    btn.disabled = true;
    try { await api("/mangoman/brain", { method: "PUT", body: JSON.stringify({ enabled: !b.enabled }) }); }
    catch (err) { alert(err.message); }
    btn.disabled = false;
    await load();
  };
  const rows = b.recent || [];
  $("brain-recent-wrap").hidden = rows.length === 0;
  $("brain-recent").tBodies[0].replaceChildren(...rows.map((d) => el("tr", {},
    el("td", { class: "dim" }, clock(d.time)),
    el("td", {}, BRAIN_KIND[d.kind] || d.kind),
    el("td", {}, d.answer ? d.answer.replace("non_answer", "non-answer") : el("span", { class: "dim" }, BRAIN_FALLBACK[d.fallback] || "rules kept")),
    el("td", { class: "r" }, d.answer ? `${Math.round(100 * d.confidence)}%` : "–"),
    el("td", { class: "r dim" }, d.cached ? "from memory" : seconds(d.latency_ms)))));
}

// ---------- advanced agents ----------

async function agentAction(btn, path, opts) {
  btn.disabled = true;
  try { await api(path, opts); } catch (err) { alert(err.message); }
  btn.disabled = false;
  await loadAgents();
  if (!$("market").hidden) await loadMarket();
}

async function loadAgents() {
  const { installed = [] } = await api("/mangoman/agents");
  state.agents = installed;
  $("agents-empty").hidden = installed.length > 0;
  $("agents-installed").hidden = installed.length === 0;
  $("agents-installed").tBodies[0].replaceChildren(...installed.map((a) => {
    const rm = el("button", { type: "button", class: "ghost" }, "Remove");
    rm.onclick = () => { if (confirm(`Remove ${a.title}?`)) agentAction(rm, `/mangoman/agents/${a.name}`, { method: "DELETE" }); };
    return el("tr", {},
      el("td", {}, el("b", {}, a.title), el("div", { class: "dim" }, `${a.name} ${a.version}`)),
      el("td", {}, a.author.name),
      el("td", {}, (a.permissions.network || []).join(", ") || "Nothing (no network)"),
      el("td", { class: "dim" }, a.skill.replace("mangoman-", "").replaceAll("-", " ")),
      el("td", { class: "r" }, rm));
  }));
}

async function loadMarket() {
  $("market").hidden = false;
  const m = await api("/mangoman/agents/market");
  $("market-msg").textContent = m.open ? "Every listed agent passed MangoMan's safety review. Installs check the marketplace's and the creator's signatures." : m.message;
  const have = new Set((state.agents || []).map((a) => a.name));
  $("market-list").hidden = !m.open;
  $("market-list").tBodies[0].replaceChildren(...(m.agents || []).map((a) => {
    const btn = el("button", { type: "button", class: have.has(a.name) ? "ghost" : "primary" }, have.has(a.name) ? "Installed" : "Install");
    btn.disabled = have.has(a.name);
    btn.onclick = () => agentAction(btn, "/mangoman/agents/install", { method: "POST", body: JSON.stringify({ name: a.name }) });
    return el("tr", {},
      el("td", {}, el("b", {}, a.title), el("div", { class: "dim" }, a.description)),
      el("td", {}, a.author.name),
      el("td", {}, a.price_inr_month > 0 ? `₹${NUM.format(a.price_inr_month)} a month` : "Free"),
      el("td", { class: "r" }, a.score ? `${Math.round(a.score.pack)} / ${Math.round(a.score.agent)}` : "–"),
      el("td", { class: "r" }, btn));
  }));
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

// ---------- free AI ----------

function renderFree(ov) {
  const rows = ov.free || [];
  $("free-block").hidden = rows.length === 0;
  if (!rows.length) return;
  const names = Object.fromEntries(ov.providers.map((p) => [p.id, p.name]));
  const count = (n, r) => (n < 0 ? "No daily limit found" : n === 0 && r.tpm ? `0: its requests are bigger than its ${NUM.format(r.tpm)} tokens a minute` : NUM.format(n));
  $("free").tBodies[0].replaceChildren(...rows.map((r) => {
    const lim = [];
    if (r.rpd) lim.push(`${NUM.format(r.rpd)} requests a day`);
    if (r.tpd) lim.push(`${NUM.format(r.tpd)} tokens a day`);
    if (!lim.length) lim.push(r.rpm ? `No daily limit found; up to ${NUM.format(r.rpm)} requests a minute` : "Not published");
    return el("tr", {},
      el("td", {}, el("i", { class: "dot", style: { background: color(r.provider) } }), names[r.provider] || r.provider),
      el("td", { class: "r" }, NUM.format(r.keys)),
      el("td", { title: `On ${r.model}, the model with the most room` }, lim.join(", ")),
      el("td", { class: "r" }, count(r.big_tasks, r)),
      el("td", { class: "r" }, count(r.small_tasks, r)));
  }));
  const open = rows.filter((r) => r.big_tasks < 0).map((r) => names[r.provider] || r.provider);
  const big = rows.reduce((s, r) => s + Math.max(0, r.big_tasks), 0);
  $("free-sub").textContent = open.length
    ? `${open.join(" and ")} ${open.length > 1 ? "publish" : "publishes"} no daily limit, so how much you get there depends on how fast it answers.`
    : `About ${NUM.format(big)} big tasks a day across your connections. MangoMan moves to the next one when a limit is reached.`;
  const c = ov.task_cost;
  $("free-how").textContent = `How this is worked out: a big task (ads, a product listing, an SEO article, a website's copy) used about ${NUM.format(c.big_requests)} requests and ${NUM.format(c.big_tokens)} tokens; a small one (an email campaign, a resume, social posts) about ${NUM.format(c.small_requests)} requests and ${NUM.format(c.small_tokens)} tokens, ${c.measured}. Limits are each provider's published free limits, or what it told MangoMan, for all your keys together. Your numbers will vary with the model and the task.`;
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

// ---------- Guardian ----------

const GSTATUS = { queued: "Waiting its turn", working: "Being fixed and tested in development", ready: "Ready for production",
  deploying: "Going live", live: "Live", rejected: "Rejected", needs_you: "Needs you" };

async function loadGuardian() {
  let projects = [];
  try { projects = await api("/mangoman/guardian"); } catch (_) {}
  const rows = [];
  for (const p of projects) {
    for (const i of p.incidents) {
      const act = (action, label, cls) => el("button", { type: "button", class: cls, onclick: async (e) => {
        e.target.disabled = true;
        try { await api(`/mangoman/guardian/${action}`, { method: "POST", body: JSON.stringify({ config: p.config, id: i.id }) }); } catch (err) { alert(err.message); }
        setTimeout(loadGuardian, 1500);
      } }, label);
      rows.push(el("tr", {},
        el("td", {}, p.app),
        el("td", {}, i.kind === "change" ? i.problem : `Fix: ${i.check}. ${i.problem}`),
        el("td", {}, GSTATUS[i.status] || i.status),
        el("td", {}, i.note || ""),
        el("td", {}, i.status === "ready" ? [act("approve", "Approve", "primary"), " ", act("reject", "Reject", "ghost")] : "")));
    }
  }
  $("guardian-block").hidden = rows.length === 0;
  $("guardian-list").tBodies[0].replaceChildren(...rows);
  const waiting = projects.some((p) => p.incidents.some((i) => i.status === "ready" || i.status === "needs_you"));
  const attention = waiting || projects.some((p) => p.latest.some((e) => e.status === "failing"));
  $("nav-guardian-flag").hidden = !attention;
  renderHealApps(projects);
}

// ---------- Guardian: your apps ----------

// What each kind of check is called on the page.
const CHECK_NAME = { dev: "Safe copy matches the live app", site: "Up and quick", pages: "Pages in a real browser (up to 10)",
  dates: "Certificate and domain dates" };
const EVERY = { "24h": "daily", "1h": "every hour" };

function when(t) {
  const d = new Date(t), today = new Date().toDateString() === d.toDateString();
  return today ? d.toLocaleTimeString([], { hour: "numeric", minute: "2-digit" }) : d.toLocaleString([], { day: "numeric", month: "short", hour: "numeric", minute: "2-digit" });
}

function copyButton(text, what) {
  return el("button", { type: "button", class: "ghost", "aria-label": `Copy ${what}`, onclick: async (e) => {
    try { await navigator.clipboard.writeText(text); e.target.textContent = "Copied"; }
    catch (_) { e.target.textContent = "Select and copy the text"; }
    setTimeout(() => { e.target.textContent = "Copy"; }, 2500);
  } }, "Copy");
}

function checkRow(p, c, ev) {
  const status = !ev ? "" : ev.status === "failing" ? "bad" : "on";
  const name = c.kind === "journey" ? c.name.replace(/^journey: /, "Journey: ") : c.name === c.kind && CHECK_NAME[c.kind] || c.name;
  const often = c.every ? ` (${EVERY[c.every] || "every " + c.every})` : c.kind === "site" ? " (every 5 minutes)" : "";
  const needsBrowser = (c.kind === "pages" || c.kind === "journey") && !p.browser;
  const state = needsBrowser ? "Needs Get ready on the setup page" : !ev ? "Not checked yet" : ev.status === "failing" ? `Failing, ${when(ev.time)}` : ev.status === "fixed" ? `Fixed by first aid, ${when(ev.time)}` : `Fine, ${when(ev.time)}`;
  const kids = [
    el("span", { class: "lamp " + status, "aria-hidden": "true" }),
    el("span", { class: "check-name" }, name, el("span", { class: "dim" }, often)),
    el("span", { class: "check-when" }, state),
  ];
  if (c.steps) kids.push(el("ol", { class: "check-steps" }, c.steps.map((t) => el("li", {}, t))));
  if (ev && ev.status === "failing") {
    const box = [el("p", { class: "what" }, ev.detail)];
    if (ev.cause) box.push(el("p", { class: "why" }, el("b", {}, "Likely cause: "), ev.cause));
    if (ev.request) {
      box.push(el("div", { class: "request" },
        el("div", { class: "request-head" }, el("b", {}, p.builder ? `Fix request for ${p.builder}` : "Fix request for your app builder"), copyButton(ev.request, `the fix request for ${name}`)),
        el("pre", {}, ev.request)));
    }
    kids.push(el("div", { class: "check-problem" }, box));
  }
  if (c.kind === "journey") {
    kids.push(el("button", { type: "button", class: "remove", onclick: async (e) => {
      e.target.disabled = true;
      try { await api("/mangoman/healing/remove", { method: "POST", body: JSON.stringify({ config: p.config, name: c.name }) }); }
      catch (err) { alert(err.message); }
      loadGuardian();
    } }, "Remove this journey"));
  }
  return el("li", { class: "check" }, kids);
}

// Each app's journey box keeps its draft and its last message across the
// page's refreshes; nothing is redrawn while a journey is being added.
const journeys = {};

function journeyForm(p) {
  const j = journeys[p.config] || (journeys[p.config] = { draft: "", msg: "", cls: "msg", busy: false });
  const input = el("input", { id: `journey-${p.config}`, maxlength: "600", autocomplete: "off", value: j.draft,
    placeholder: "For example: open the shop, add a product to the cart, see the cart",
    oninput: (e) => { j.draft = e.target.value; } });
  const msg = el("p", { class: j.cls, role: "status" }, j.msg);
  const btn = el("button", { type: "submit", class: "ghost", disabled: j.busy }, "Add journey");
  const say = (cls, text) => { j.cls = cls; j.msg = text; msg.className = cls; msg.textContent = text; };
  return el("form", { class: "journey-add", onsubmit: async (e) => {
    e.preventDefault();
    if (!input.value.trim() || j.busy) return;
    j.busy = btn.disabled = true;
    say("msg", "Writing the steps and trying them once in a browser. This can take a minute\u2026");
    try {
      const r = await api("/mangoman/healing/journey", { method: "POST", body: JSON.stringify({ config: p.config, text: input.value }) });
      say(r.works ? "msg ok" : "msg err", r.works ? `Added. It worked just now in ${r.steps.length} steps; Guardian walks it every hour.`
        : `Added, but it fails today: ${r.problem}. Check the steps below.`);
      j.draft = "";
    } catch (err) { say("msg err", err.message); }
    j.busy = false;
    loadGuardian();
  } }, el("label", { for: input.id }, "Add a journey: a path a visitor takes, in plain words. Journeys stop before paying or ordering."), input, btn, msg);
}

let howSet = false;

function renderHealApps(projects) {
  // A refresh never wipes what someone is typing or waiting for.
  if (Object.values(journeys).some((j) => j.busy)) return;
  if ($("heal-apps").contains(document.activeElement) && document.activeElement.tagName === "INPUT") return;
  $("heal-empty").hidden = projects.length > 0;
  if (!howSet) { $("heal-how").open = projects.length === 0; howSet = true; }
  $("heal-apps").replaceChildren(...projects.map((p) => {
    const latest = Object.fromEntries(p.latest.map((e) => [e.check, e]));
    const failing = p.latest.filter((e) => e.status === "failing").length;
    const ran = p.latest.length > 0;
    const pill = !p.watching ? el("span", { class: "pill idle" }, "Not watched here")
      : failing ? el("span", { class: "pill bad" }, el("span", { class: "lamp bad" }), failing === 1 ? "1 problem" : `${failing} problems`)
      : ran ? el("span", { class: "pill good" }, el("span", { class: "lamp on" }), "All good")
      : el("span", { class: "pill idle" }, "First check running");
    const bad = p.strip.filter((x) => x === "bad").length, seen = p.strip.filter((x) => x).length;
    const stripLabel = !seen ? "No checks in the last 24 hours" : bad ? `Problems in ${bad} of the last ${seen} half hours checked` : `No problems in the last 24 hours`;
    const rows = p.checks.map((c) => checkRow(p, c, latest[c.name]));
    if (latest.dev) rows.push(checkRow(p, { name: "dev", kind: "dev" }, latest.dev));
    const mode = !p.fixes ? "Watch and diagnose: Guardian has the live address, not the code."
      : p.live ? "Watch and fix: fixes are made in a safe copy and go live after your OK."
      : "Watch and fix in a safe copy. To put fixes live, Guardian needs to know how this app is published.";
    const off = el("button", { type: "button", class: "ghost danger", onclick: async (ev) => {
      if (!confirm(`Stop watching ${p.app}? Its settings stay on this computer.`)) return;
      ev.target.disabled = true;
      try { await api("/mangoman/healing/disconnect", { method: "POST", body: JSON.stringify({ config: p.config }) }); }
      catch (err) { alert(err.message); }
      loadGuardian();
    } }, "Disconnect");
    return el("li", { class: "appcard" + (failing ? " is-bad" : "") },
      el("div", { class: "app-head" },
        el("div", { class: "app-name" }, el("h3", {}, p.app),
          p.url ? el("a", { href: p.url, target: "_blank", rel: "noopener noreferrer" }, p.url) : null),
        pill),
      el("div", { class: "pulse" },
        el("div", { class: "strip", role: "img", "aria-label": stripLabel, title: stripLabel }, p.strip.map((x) => el("i", { class: x }))),
        el("p", { class: "strip-legend" }, el("span", {}, "24 hours ago"), el("span", {}, stripLabel), el("span", {}, "Now"))),
      p.stopped ? el("p", { class: "msg err", style: { padding: "8px 20px 0" } }, `Watching stopped: ${p.stopped}. Disconnect and connect again after fixing it.`) : null,
      el("ul", { class: "checks" }, rows),
      p.url ? journeyForm(p) : null,
      el("div", { class: "app-foot" }, el("span", {}, mode), off));
  }));
}

// ---------- Mango Arch ----------

function renderArch(v) {
  $("arch-share-line").hidden = !v.live;
  $("arch-share").checked = v.share;
  $("arch-status").textContent = !v.live ? "Not live yet. Nothing is sent or received."
    : v.synced ? `Last update ${new Date(v.synced).toLocaleString()}. ${v.lessons.length} lesson${v.lessons.length === 1 ? "" : "s"} in use.`
    : "Waiting for the first update.";
  $("arch-lessons").replaceChildren(...v.lessons.map((l) => el("li", {},
    `${l.target}: tried last until ${new Date(l.until).toLocaleTimeString()} (${l.reason} on ${l.installs} networks)`)));
}

async function loadArch() {
  try { renderArch(await api("/mangoman/arch")); } catch (err) { $("arch-status").textContent = err.message; }
}

async function loadHealFolders() {
  let r;
  try { r = await api("/mangoman/files"); } catch (_) { return; }
  $("heal-folders").replaceChildren(...r.items.filter((i) => i.dir).map((i) => el("option", { value: i.name })));
}

function healMode() { return document.querySelector('input[name="heal-mode"]:checked').value; }

// The form shows the fields for the chosen kind of connection.
function showHealMode() {
  const m = healMode();
  for (const n of document.querySelectorAll("#heal-form [data-mode]")) n.hidden = n.dataset.mode !== m;
}

async function connectApp(e) {
  e.preventDefault();
  const btn = $("heal-connect"), msg = $("heal-msg");
  btn.disabled = true;
  msg.className = "msg";
  msg.textContent = "Connecting\u2026";
  try {
    const fix = healMode() === "fix";
    if (fix && !$("heal-folder").value.trim()) throw new Error("Pick the folder with the app's code.");
    const r = await api("/mangoman/healing/connect", { method: "POST", body: JSON.stringify({
      name: $("heal-name").value, url: $("heal-url").value, folder: fix ? $("heal-folder").value : "",
      builder: fix ? "" : $("heal-builder").value }) });
    msg.className = "msg ok";
    msg.textContent = "Connected. The first check runs now; its result appears below."
      + (r.kept ? " That folder already had a guardian.json, so its own settings are used." : "")
      + (r.branch ? ` The live branch is ${r.branch}.` : "");
    $("heal-form").reset();
    showHealMode();
    setTimeout(loadGuardian, 3000);
  } catch (err) {
    msg.className = "msg err";
    msg.textContent = err.message;
  }
  btn.disabled = false;
  loadGuardian();
}

// ---------- live model panel ----------

function modelName(target) { const [p, ...m] = target.split("/"); return `${m.join("/")} (${p})`; }

function renderNow(o) {
  if (o.last) {
    $("now-model").replaceChildren(o.last.model, " ", el("small", {}, `on ${o.last.provider}${o.last.key ? ` (${o.last.key}'s key)` : ""}, last answer at ${hm(o.last.time)}${o.last.weak ? ", a smaller model" : ""}`));
  }
  const next = (o.next || []).filter((t) => !o.last || t !== `${o.last.provider}/${o.last.model}`).slice(0, 2);
  $("now-next").textContent = next.length ? `Next if it runs out: ${next.map(modelName).join(", ")}` : o.next && o.next.length ? "" : "No model is free right now. Connect a provider below.";
  const allowed = o.weaker_always || o.weaker_until;
  $("now-ask").hidden = !o.strong_busy || allowed;
  $("now-ask-text").textContent = `${o.has_list ? "Your preferred models are busy" : "The strong free models are busy"}${o.back_at ? ` until ${hm(o.back_at)}` : ""}. ${o.has_list ? "Other models can carry on, but answers may differ." : "A smaller model can carry on, with simpler answers."} Or connect another free provider such as NVIDIA below.`;
  $("now-weaker").hidden = !allowed;
  $("now-weaker-text").textContent = o.weaker_always ? (o.has_list ? "Other models are allowed when your preferred ones are busy." : "Smaller models are allowed when the strong ones are busy.") : allowed ? `${o.has_list ? "Other" : "Smaller"} models allowed until ${hm(o.weaker_until)}.` : "";
}

async function loadNow() {
  try { renderNow(await api("/mangoman/now")); } catch (_) {}
}

async function setWeaker(mode) {
  try { renderNow(await api("/mangoman/weaker", { method: "POST", body: JSON.stringify({ mode }) })); } catch (err) { alert(err.message); }
}

// ---------- the menu ----------

const VIEWS = ["agents", "my-agents", "healing", "chargesheets", "templates", "files", "history", "connections", "usage", "settings"];

function showView() {
  const want = location.hash.slice(1);
  const top = want.split("/")[0];
  const v = VIEWS.includes(top) ? top : "agents";
  for (const id of VIEWS) $("view-" + id).hidden = id !== v;
  for (const a of document.querySelectorAll(".nav a")) {
    if (a.dataset.view === v) a.setAttribute("aria-current", "page"); else a.removeAttribute("aria-current");
  }
  // The chart takes its width from the page, which a hidden section has none of.
  if (v === "usage" && state.overview) renderChart(state.activity, state.overview);
  if (v === "files" || v === "settings") loadFiles();
  if (v === "healing") { loadGuardian(); loadHealFolders(); }
  if (v === "settings") loadArch();
  // Chargesheets keeps its own place after the slash: #chargesheets/<id>/<tab>.
  if (v === "chargesheets") Sheets.route(want.slice("chargesheets/".length)); else Sheets.leave();
}

function say(text, kind) {
  const msg = $("codemsg");
  msg.className = "msg" + (kind ? " " + kind : "");
  msg.textContent = text;
}

// openCoding opens the coding screen in its own tab, the same tab every time.
// The tab is opened in the click itself (browsers block tabs opened later)
// and waits there until the workspace is ready. prompt is put in the message
// box of a fresh chat, for the user to read and send.
// openCoding opens the coding screen with a first message. A caller that
// waits for the server first opens the tab at the click and passes it in, as
// browsers block a tab opened later.
function openCoding(prompt, opened) {
  const frag = new URLSearchParams({ token: state.token });
  if (prompt) frag.set("prompt", prompt);
  // The query makes it a new page load, also when that tab is already open.
  const url = `code.html?open=${Date.now()}#${frag}`;
  const tab = opened || window.open(url, "mangoman-code");
  if (opened) opened.location.href = url;
  if (!tab) { say("Your browser blocked the coding screen's tab: allow pop-ups for 127.0.0.1, then try again.", "err"); return; }
  say("Opening the coding screen in its own tab\u2026");
  api("/mangoman/code/open", { method: "POST" })
    .then((r) => say(`The coding screen is open in its own tab. Files are saved in ${r.folder}.`, "ok"))
    .catch((err) => say(err.message, "err"));
}

// ---------- Super Agents and Templates ----------

// fileStart is the foot of an agent that works on a file: pick the file,
// it is saved in a task folder in MangoMan Projects, and Start hands the
// agent its exact place, so the agent never asks for a path.
function fileStart(a) {
  let picked = null;
  const msg = el("p", { class: "msg", role: "status" });
  const start = el("button", { type: "button", class: "primary start", onclick: () =>
    openCoding(picked ? a.file_prompt.replace("{file}", picked.path).replace("{folder}", picked.folder) : a.prompt) }, "Start");
  const input = el("input", { type: "file", accept: a.accept, class: "sr", onchange: async (e) => {
    const f = e.target.files[0];
    if (!f) return;
    picked = null; start.disabled = true; msg.className = "msg"; msg.textContent = `Adding ${f.name}\u2026`;
    try {
      const res = await fetch(`/mangoman/files/upload?name=${encodeURIComponent(f.name)}`,
        { method: "POST", headers: { "Authorization": "Bearer " + state.token }, body: f });
      const body = await res.json().catch(() => null);
      if (!res.ok) throw new Error((body && body.error && body.error.message) || `HTTP ${res.status}`);
      picked = body;
      msg.className = "msg ok"; msg.textContent = `${f.name} is ready. Press Start.`;
    } catch (err) { msg.className = "msg err"; msg.textContent = err.message; }
    start.disabled = false;
  } });
  return el("div", { class: "file-start" },
    el("label", { class: "ghost" }, input, `Choose ${a.file.toLowerCase()}`), start, msg);
}

// The business part of every brief, kept in MangoMan Projects/Brand.
const BUSINESS = [
  ["name", "Business name", "text", true],
  ["website", "Website", "text", false, "Leave empty if you have none"],
  ["sells", "What you sell, with prices", "long", true, "For example: handmade soaps, 250 to 600 each; gift boxes from 1,500"],
  ["customers", "Who buys from you", "long", true, "Who they are, where they find you, why they choose you"],
  ["where", "Where you sell", "text", true, "City, country, online, or all three"],
  ["language", "Your customers' language or languages", "text", true, "For example: English and Hindi"],
  ["currency", "Currency", "text", true, "For example: INR, USD, EUR"],
  ["timezone", "Time zone", "text", false],
  ["social", "Your social pages", "long", false, "One link per line"],
  ["competitors", "Competitors", "long", false, "Names, and their Instagram, YouTube or TikTok links if you know them, one per line"],
  ["facts", "Real facts to use", "long", false, "Ratings, follower counts, awards, years in business, offers"],
  ["tone", "How you talk to customers", "text", false, "For example: warm and simple, never pushy"],
  ["colours", "Brand colours", "text", false, "For example: #1E3A2F and #F2A516"],
  ["avoid", "Things to never say or show", "long", false],
];

// field draws one question of the brief. A choice or multi gets option
// buttons plus a box for an answer of your own; value() reads it back.
function field(id, label, kind, required, hint, options, now) {
  const fid = `brief-${id}`;
  const head = el("label", { for: fid }, label, required ? el("span", { class: "req" }, " (needed)") : null);
  if (kind === "text" || kind === "long") {
    const box = el(kind === "long" ? "textarea" : "input", kind === "long"
      ? { id: fid, rows: 3, maxlength: 4000, placeholder: hint } : { id: fid, type: "text", maxlength: 4000, placeholder: hint });
    box.value = now || "";
    return { id, label, required, node: el("div", { class: "bf" }, head, box), value: () => box.value.trim() };
  }
  const multi = kind === "multi";
  const picks = options.map((o) => el("button", { type: "button", class: "opt", "aria-pressed": "false", onclick: (e) => {
    const b = e.currentTarget, on = b.getAttribute("aria-pressed") !== "true";
    if (!multi) for (const x of picks) x.setAttribute("aria-pressed", "false");
    b.setAttribute("aria-pressed", String(on));
  } }, o));
  const other = el("input", { id: fid, type: "text", maxlength: 400, placeholder: multi ? "Others, comma separated" : "Or your own answer" });
  return { id, label, required, node: el("div", { class: "bf", role: "group", "aria-labelledby": fid + "-h" },
    el("p", { class: "bf-h", id: fid + "-h" }, label, required ? el("span", { class: "req" }, " (needed)") : null),
    el("div", { class: "opts" }, picks), el("label", { for: fid, class: "sr" }, label + ": your own answer"), other),
  value: () => {
    const typed = other.value.trim();
    const on = picks.filter((b) => b.getAttribute("aria-pressed") === "true").map((b) => b.textContent);
    if (!multi) return typed || on[0] || "";
    return [...on, ...typed.split(",").map((t) => t.trim()).filter(Boolean)];
  } };
}

const empty = (v) => Array.isArray(v) ? v.length === 0 : !v;

// modal shows a form in the page's one dialog: a title with a close button,
// then the content.
function modal(title, onsubmit, ...content) {
  const dlg = $("modal");
  dlg.setAttribute("aria-label", title);
  dlg.replaceChildren(el("form", { class: "brief-form", onsubmit: (e) => { e.preventDefault(); onsubmit(); } },
    el("div", { class: "drawer-head" }, el("h2", {}, title),
      el("button", { type: "button", class: "ghost icon", "aria-label": "Close", onclick: () => dlg.close() }, "\u00d7")),
    ...content));
  if (!dlg.open) dlg.showModal();
  return dlg;
}

// openBrief asks a super agent's questions once, in a form: the business
// (kept for every agent) and this job. MangoMan writes the answers to the
// task folder, and the agent starts with them, so it never asks in chat.
async function openBrief(a) {
  const msg = el("p", { class: "msg", role: "status" });
  let biz = { fields: {} };
  try { biz = await api("/mangoman/business"); } catch (err) { say(err.message, "err"); return; }
  const f = biz.fields || {};
  if (!f.timezone) f.timezone = Intl.DateTimeFormat().resolvedOptions().timeZone || "";
  // An agent whose job is not about the business (the Council) asks for it only optionally.
  const optional = a.business === "optional";
  const bizFields = BUSINESS.map(([id, label, kind, req, hint]) => field(id, label, kind, req && !optional, hint, null, f[id]));
  // Job ids get their own prefix: a job may ask again for a business field (the
  // SEO agent's website), prefilled from it.
  const jobFields = a.brief.map((q) => ({ ...field("job-" + q.id, q.label, q.kind, q.required, q.hint, q.options, f[q.id]), id: q.id }));
  const logoMsg = el("span", { class: "fine" }, biz.logo ? `Logo saved (${biz.logo}).` : "No logo yet.");
  const logo = el("input", { type: "file", id: "brief-logo", accept: "image/png,image/jpeg", class: "sr", onchange: async (e) => {
    const file = e.target.files[0];
    if (!file) return;
    logoMsg.textContent = "Saving the logo\u2026";
    try {
      const r = await api(`/mangoman/business/logo?name=${encodeURIComponent(file.name)}`,
        { method: "POST", headers: { "Content-Type": file.type || "application/octet-stream" }, body: file });
      logoMsg.textContent = `Logo saved (${r.logo}).`;
    } catch (err) { logoMsg.textContent = err.message; }
  } });
  const needed = bizFields.filter((q) => q.required && !f[q.id]).length;
  const go = el("button", { type: "submit", class: "primary" }, "Start the agent");
  const dlg = modal(a.title, async () => {
    const missing = [...bizFields, ...jobFields].filter((q) => q.required && empty(q.value()));
    if (missing.length) {
      msg.className = "msg err";
      msg.textContent = `${missing.length} needed ${missing.length > 1 ? "answers are" : "answer is"} missing, starting with: ${missing[0].label}`;
      const d = missing[0].node.closest("details");
      if (d) d.open = true;
      missing[0].node.querySelector("input, textarea, button").focus();
      return;
    }
    go.disabled = true; msg.className = "msg"; msg.textContent = "Saving your brief\u2026";
    const tab = window.open("", "mangoman-code");
    try {
      const fields = Object.fromEntries(bizFields.map((q) => [q.id, q.value()]));
      const job = Object.fromEntries(jobFields.map((q) => [q.id, q.value()]).filter(([, v]) => !empty(v)));
      if (!fields.website && typeof job.website === "string") fields.website = job.website;
      await api("/mangoman/business", { method: "PUT", body: JSON.stringify({ fields }) });
      const r = await api("/mangoman/agents/brief", { method: "POST", body: JSON.stringify({ agent: a.id, title: a.title, job }) });
      dlg.close();
      openCoding(a.prompt.replaceAll("{folder}", r.folder), tab);
    } catch (err) { if (tab && tab.location.href === "about:blank") tab.close(); msg.className = "msg err"; msg.textContent = err.message; go.disabled = false; }
  },
    el("p", { class: "sub" }, "Answer once here. The agent reads these answers and does not ask again in the chat."),
    a.stages ? el("ol", { class: "stages" }, a.stages.map((s) => el("li", {}, s))) : null,
    el("details", { class: "bf-biz", open: needed > 0 },
      el("summary", {}, "Your business", el("span", { class: "fine" }, needed ? ` (${needed} answers needed)`
        : optional && !f.name ? " (optional: helps when the question is about it)" : " (saved; kept for every agent)")),
      bizFields.map((q) => q.node),
      el("div", { class: "bf" }, el("p", { class: "bf-h" }, "Logo"),
        el("div", { class: "opts" }, logo, el("label", { for: "brief-logo", class: "ghost" }, "Choose a PNG or JPG"), logoMsg))),
    el("h3", {}, "This job"),
    jobFields.map((q) => q.node),
    el("div", { class: "brief-foot" }, go, msg));
}

// ---------- Skills library ----------

const KINDS = { agent: "Agent", method: "Know-how", pack: "Builds files" };
const lib = { dir: "", skills: [], show: "all" };
const pretty = (name) => name.replace(/^mangoman-/, "").replaceAll("-", " ").replace(/^./, (c) => c.toUpperCase())
  .replace(/\b(seo|ceo|ai)\b/gi, (w) => w.toUpperCase());

// A user's own agent starts from the same brief as ours, with one open job.
const OWN_BRIEF = [
  { id: "job", label: "What should the agent do this time?", kind: "long", required: true },
  { id: "notes", label: "Anything else it should know", kind: "long" },
];

async function loadLibrary() {
  try { Object.assign(lib, await api("/mangoman/skills")); }
  catch (err) { say(`Could not list the skills: ${err.message}`, "err"); return; }
  renderLibrary();
}

function renderLibrary() {
  renderTeam();
  const counts = { all: lib.skills.length, own: lib.skills.filter((k) => k.own).length };
  for (const k of Object.keys(KINDS)) counts[k] = lib.skills.filter((x) => x.kind === k).length;
  const labels = { all: "All", agent: "Agents", method: "Know-how", pack: "Builds files", own: "Yours" };
  $("lib-kinds").replaceChildren(...Object.entries(labels).map(([k, t]) => el("button", { type: "button",
    "aria-pressed": String(lib.show === k), onclick: () => { lib.show = k; renderLibrary(); } }, `${t} ${counts[k]}`)));
  const shown = lib.skills.filter((k) => lib.show === "all" || (lib.show === "own" ? k.own : k.kind === lib.show))
    .sort((a, b) => b.own - a.own); // the user's own first
  $("lib-cards").replaceChildren(...shown.map((k) => el("li", { class: "card lib-card" },
    el("h3", {}, pretty(k.name)),
    el("span", { class: "kind" + (k.own ? " own" : "") }, (k.own ? "Yours \u00b7 " : "") + (KINDS[k.kind] || "Not valid")),
    k.error ? el("p", { class: "err" }, k.error) : el("p", { class: "desc" }, k.description),
    el("div", { class: "acts" },
      el("button", { type: "button", class: "ghost", onclick: () => openSkill(k.name) }, k.own ? "Edit" : "Read"),
      k.own ? el("button", { type: "button", class: "ghost", onclick: () => improveSkill(k.name) }, "Improve with AI") : null))));
}

// openSkill shows a skill's SKILL.md: MangoMan's to read and copy, the
// user's own to change, save or remove.
async function openSkill(name) {
  let k;
  try { k = await api(`/mangoman/skills/${name}`); } catch (err) { say(err.message, "err"); return; }
  const msg = el("p", { class: "msg", role: "status" });
  const text = el("textarea", { class: "skill-text", id: "skill-text", spellcheck: "false", readonly: !k.own });
  text.value = k.text;
  if (!k.own) {
    const as = el("input", { type: "text", id: "copy-as", maxlength: 64 });
    as.value = "my-" + name.replace(/^mangoman-/, "");
    modal(pretty(name), async () => {
      try {
        const r = await api(`/mangoman/skills/${name}/copy`, { method: "POST", body: JSON.stringify({ as: as.value.trim() }) });
        await loadLibrary();
        openSkill(r.name);
      } catch (err) { msg.className = "msg err"; msg.textContent = err.message; }
    },
      el("p", { class: "sub" }, "One of MangoMan's skills. Copy it to make your own version: the copy is yours to change, and updates leave it alone."),
      el("label", { for: "skill-text", class: "sr" }, "The skill"), text,
      el("div", { class: "brief-foot" }, el("label", { for: "copy-as" }, "Name for your copy"), as,
        el("button", { type: "submit", class: "primary" }, "Copy and improve"), msg));
    return;
  }
  modal(pretty(name), () => saveSkill(name, text.value, msg),
    el("p", { class: "sub" }, `Your skill, kept in ${k.path}. Keep the name line as ${name}.`),
    el("label", { for: "skill-text", class: "sr" }, "The skill"), text,
    el("div", { class: "brief-foot" }, el("button", { type: "submit", class: "primary" }, "Save"),
      el("button", { type: "button", class: "ghost", onclick: () => { $("modal").close(); improveSkill(name); } }, "Improve with AI"),
      el("button", { type: "button", class: "ghost danger", onclick: async () => {
        if (!confirm(`Remove your skill ${name}?`)) return;
        try { await api(`/mangoman/skills/${name}`, { method: "DELETE" }); $("modal").close(); loadLibrary(); }
        catch (err) { msg.className = "msg err"; msg.textContent = err.message; }
      } }, "Remove"), msg));
}

async function saveSkill(name, text, msg) {
  try {
    await api(`/mangoman/skills/${name}`, { method: "PUT", body: JSON.stringify({ text }) });
    msg.className = "msg ok"; msg.textContent = "Saved. Agents can use it from the next task.";
    loadLibrary();
    return true;
  } catch (err) { msg.className = "msg err"; msg.textContent = err.message; return false; }
}

function improveSkill(name) {
  openCoding(`Help me improve my skill ${name}. Its files are in ${lib.dir}/${name}: read SKILL.md first, then ask me what should change. ` +
    `Make the change there, keep it a valid Agent Skill (the name line stays ${name}), and tell me in a few lines what you changed.`);
}

// skillFile writes a SKILL.md from a form's answers.
function skillFile(name, kind, title, what, body, basedOn) {
  const desc = `${title}: ${what}`.replace(/\s+/g, " ").slice(0, 900); // the limit is 1,024 characters, quotes escaped
  return `---\nname: ${name}\ndescription: ${JSON.stringify(desc)}\nlicense: Your own ${kind === "agent" ? "agent" : "skill"}, for use inside MangoMan.\n` +
    `metadata:\n${basedOn ? `  based_on: ${basedOn}\n` : ""}  pack: mine\n  kind: ${kind}\n  version: "1.0"\n---\n\n# ${title}\n\n${body.trim()}\n`;
}

// slugOf makes a skill's folder name from its title; a title in another
// script gets a numbered name.
function slugOf(t) {
  return t.toLowerCase().normalize("NFKD").replace(/[^a-z0-9]+/g, "-").replace(/^-|-$/g, "").slice(0, 60) || `my-skill-${Date.now() % 100000}`;
}

// taken says why a new skill's name cannot be used, or "" when it can.
const taken = (name) => lib.skills.some((k) => k.name === name) ? `You already have a skill named ${name}: open it from the library, or pick another name.` : "";

// newSkill writes a know-how skill: the expert steps an agent follows.
function newSkill() {
  const msg = el("p", { class: "msg", role: "status" });
  const q = [field("sk-title", "Skill name", "text", true, "For example: Restaurant menu writing"),
    field("sk-what", "When should an agent use it?", "long", true, "For example: when writing or improving a menu, dish names or descriptions"),
    field("sk-body", "What an expert does, step by step", "long", true, "The steps, the rules, and what good looks like. Plain sentences.")];
  q[2].node.querySelector("textarea").rows = 10;
  modal("New skill", async () => {
    const [title, what, body] = q.map((x) => x.value());
    if (!title || !what || !body) { msg.className = "msg err"; msg.textContent = "Please fill in all three."; return; }
    const name = slugOf(title);
    if (taken(name)) { msg.className = "msg err"; msg.textContent = taken(name); return; }
    if (await saveSkill(name, skillFile(name, "method", title, what, body), msg)) openSkill(name);
  },
    el("p", { class: "sub" }, "Write down how an expert does one job, so every agent can do it that way. Or let the AI write it with you."),
    q.map((x) => x.node),
    el("div", { class: "brief-foot" }, el("button", { type: "submit", class: "primary" }, "Save the skill"),
      el("button", { type: "button", class: "ghost", onclick: () => {
        const title = q[0].value();
        if (!title) { msg.className = "msg err"; msg.textContent = "Give the skill a name first."; return; }
        $("modal").close();
        openCoding(`Write a new skill with me: "${title}". ${q[1].value()} Save it as ${lib.dir}/${slugOf(title)}/SKILL.md in the Agent Skills format, ` +
          `like the mangoman skills there (frontmatter name ${slugOf(title)}, a description that says when to use it, metadata kind method). ` +
          "Ask me how I do this job first, then write clear steps and rules, and show me the result.");
      } }, "Write it with AI"), msg));
}

// newAgent builds a super agent from skills in the library: its stages,
// the skills it uses, and the standards it keeps.
function newAgent() {
  const msg = el("p", { class: "msg", role: "status" });
  const usable = lib.skills.filter((k) => !k.error && k.kind !== "agent").map((k) => k.name);
  const q = [field("ag-title", "Agent name", "text", true, "For example: Restaurant Growth Manager"),
    field("ag-what", "What job does it do?", "long", true, "For example: plans and runs a restaurant's marketing: menu, reviews, local search and social"),
    field("ag-skills", "Skills it uses", "multi", true, null, usable),
    field("ag-stages", "Its stages, one per line", "long", true, "Research: the restaurant, its reviews and 3 nearby rivals\nPlan: ...\nMake: ...\nCheck: ..."),
    field("ag-rules", "Rules it always keeps (optional)", "long", false, "For example: never promise delivery times")];
  for (const b of q[2].node.querySelectorAll(".opt")) b.textContent = b.textContent.replace(/^mangoman-/, "");
  q[3].node.querySelector("textarea").rows = 6;
  modal("Create an agent", async () => {
    const [title, what, used, stages, rules] = q.map((x) => x.value());
    if (!title || !what || !used.length || !stages) { msg.className = "msg err"; msg.textContent = "Please fill in the name, the job, the skills and the stages."; return; }
    const names = used.map((u) => usable.includes(u) ? u : usable.includes("mangoman-" + u) ? "mangoman-" + u : u);
    const steps = stages.split("\n").map((t) => t.trim()).filter(Boolean)
      .map((t, i) => { const [h, ...rest] = t.split(":"); return `**${i + 1}. ${h.trim()}.** ${rest.join(":").trim()}`; });
    const body = `You are the ${title} for this business. Your job: ${what}\n\nWork in stages and show your work. Use these skills at their stage: ` +
      names.map((n) => "`" + n + "`").join(", ") + ".\n\n## Start\n\n" +
      "1. Read `brief.json` in the task folder named in the first message and work in that folder. Use the logo if one is named.\n" +
      "2. Put the stages in your plan (the todo tool) and tick them off.\n" +
      "3. Ask only what you cannot work without, once, in one message.\n" +
      "4. After each stage, tell the owner in a line or two what you found or made, then continue.\n\n" +
      "## Stages\n\n" + steps.join("\n\n") + "\n\n## Standards\n\n" +
      "- Specific to this business; every claim about the market cites a source you opened.\n" +
      "- Every number is the owner's fact or something a source showed; plans and targets are labelled as such.\n" +
      "- When a skill has a checker, run it and fix every FAIL before you deliver.\n" +
      rules.split("\n").map((t) => t.trim()).filter(Boolean).map((t) => `- ${t}\n`).join("");
    const name = slugOf(title);
    if (taken(name)) { msg.className = "msg err"; msg.textContent = taken(name); return; }
    if (await saveSkill(name, skillFile(name, "agent", title, what, body), msg)) openSkill(name);
  },
    el("p", { class: "sub" }, "Pick the skills it works with and say what it does at each stage. It starts from the same brief as the built-in agents."),
    q.map((x) => x.node),
    el("div", { class: "brief-foot" }, el("button", { type: "submit", class: "primary" }, "Save the agent"), msg));
}

// ---------- My Agents ----------

function myMsg(text, kind) {
  const m = $("my-msg");
  m.className = "msg" + (kind ? " " + kind : "");
  m.textContent = text;
}

// noAsk drops the closing "First ask me ..." of a pre-made agent's first
// message, where a brief or a playbook already says what to ask.
const noAsk = (prompt) => prompt.replace(/\s*First ask me[^.]*\.$/, "");

// Team changes go one at a time, each from the team as last saved. Only the
// buttons change on Super Agents, so a file picked on a card stays picked.
let teamQueue = Promise.resolve();
function toggleTeam(id) {
  teamQueue = teamQueue.then(async () => {
    if (!state.teamLoaded) { say("Could not read your team. Reload the page and try again.", "err"); return; }
    const next = state.team.includes(id) ? state.team.filter((x) => x !== id) : [...state.team, id];
    try { state.team = (await api("/mangoman/my-agents", { method: "PUT", body: JSON.stringify({ agents: next }) })).agents; }
    catch (err) { say(err.message, "err"); return; }
    for (const b of document.querySelectorAll("[data-team]")) {
      b.textContent = teamLabel(b.dataset.team);
      b.setAttribute("aria-label", `${b.textContent}: ${state.starters.find((x) => x.id === b.dataset.team).title}`);
    }
    renderTeam();
  });
}

const teamLabel = (id) => state.team.includes(id) ? "In my team" : "Add to my team";

// ownAgent is how one of the user's own agents starts: from the brief of the
// pre-made agent it was made from, else from a short one.
function ownAgent(k) {
  const base = state.starters.find((s) => s.id === k.based_on);
  return { id: k.name, title: pretty(k.name), own: true, brief: (base && base.brief) || OWN_BRIEF, stages: base && base.stages, business: base && base.business,
    ref: `its playbook ${lib.dir}/${k.name}/SKILL.md`,
    prompt: `Be my ${pretty(k.name)}. Read your playbook in ${lib.dir}/${k.name}/SKILL.md and follow it, with the skills it names at their stages. My brief is in {folder}/brief.json: work in that folder and save every file there.` };
}

// members are the agents in the user's team: the pre-made ones added, then their own.
function members() {
  const pre = state.team.map((id) => state.starters.find((s) => s.id === id)).filter(Boolean);
  const own = lib.skills.filter((k) => k.own && k.kind === "agent" && !k.error);
  return [...pre.map((a) => ({ a, own: null })), ...own.map((k) => ({ a: ownAgent(k), own: k }))];
}

const teamStarts = new Map();

function renderTeam() {
  if (!state.starters) return;
  const list = members();
  $("my-empty").hidden = list.length > 0;
  $("my-agent-cards").replaceChildren(...list.map(({ a, own }) => {
    const base = own && state.starters.find((s) => s.id === own.based_on);
    return el("li", { class: "card" }, cardTop(a.title),
      el("p", { class: "fine" }, own ? (base ? `Yours, made from the ${base.title}` : "Yours") : "Pre-made"),
      el("p", {}, own ? (base ? base.summary : own.description) : a.summary),
      own ? el("button", { type: "button", class: "primary start", onclick: () => openBrief(a) }, "Start") : keptStart(a),
      el("div", { class: "acts" }, own ? [
        el("button", { type: "button", class: "ghost", "aria-label": `Edit: ${a.title}`, onclick: () => openSkill(own.name) }, "Edit"),
        el("button", { type: "button", class: "ghost", "aria-label": `Share: ${a.title}`, onclick: () => shareAgent(own.name) }, "Share"),
      ] : [
        el("button", { type: "button", class: "ghost", "aria-label": `Make it mine: ${a.title}`, onclick: () => makeMine(a) }, "Make it mine"),
        el("button", { type: "button", class: "ghost", "aria-label": `Remove from team: ${a.title}`, onclick: () => toggleTeam(a.id) }, "Remove from team"),
      ]));
  }));
}

function keptStart(a) {
  if (!teamStarts.has(a.id)) teamStarts.set(a.id, startFor(a));
  return teamStarts.get(a.id);
}

// makeMine copies a pre-made agent as the user's own, to change as they like:
// a Super Agent's playbook as it is, any other agent written as a playbook
// from its card. The copy starts from the same brief.
function makeMine(a) {
  const msg = el("p", { class: "msg", role: "status" });
  const as = el("input", { type: "text", id: "mine-as", maxlength: 64 });
  as.value = "my-" + a.id;
  // A Super Agent's first skill is its playbook.
  const playbook = a.super ? a.packs[0] : null;
  modal(`Make the ${a.title} yours`, async () => {
    const name = as.value.trim();
    if (taken(name)) { msg.className = "msg err"; msg.textContent = taken(name); return; }
    try {
      if (playbook) {
        await api(`/mangoman/skills/${playbook}/copy`, { method: "POST", body: JSON.stringify({ as: name, based_on: a.id }) });
      } else {
        const body = `You are the ${a.title} for this business. ${a.summary}\n\nUse these skills: ` +
          a.packs.map((p) => "`" + p + "`").join(", ") + ".\n\n## How you work\n\n" + noAsk(a.prompt) +
          "\n\nAsk only what you cannot work without, once, in one message. When a skill has a checker, run it and fix every FAIL before you deliver.";
        await api(`/mangoman/skills/${name}`, { method: "PUT", body: JSON.stringify({ text: skillFile(name, "agent", a.title, a.summary, body, a.id) }) });
      }
      $("modal").close();
      await loadLibrary();
      location.hash = "my-agents";
      openSkill(name);
    } catch (err) { msg.className = "msg err"; msg.textContent = err.message; }
  },
    el("p", { class: "sub" }, "You get your own copy of this agent's playbook: change what it does, how it works and the rules it keeps. It joins My Agents and starts from the same brief. MangoMan's updates never change your copy."),
    el("div", { class: "brief-foot" }, el("label", { for: "mine-as" }, "Name for your agent"), as,
      el("button", { type: "submit", class: "primary" }, "Make it mine"), msg));
}

// workTogether runs one job with a team: a lead agent that follows its
// teammates' playbooks for the parts they do best.
function workTogether() {
  if (!state.starters) { myMsg("The agents did not load. Reload the page.", "err"); return; }
  const list = members().map((m) => m.a);
  if (list.length < 2) { myMsg("Add at least two agents to your team first.", "err"); return; }
  const titles = list.map((a) => a.title);
  const msg = el("p", { class: "msg", role: "status" });
  const q = [field("tj-lead", "Who leads this job?", "choice", true, null, titles),
    field("tj-mates", "Who helps?", "multi", true, null, titles)];
  for (const x of q) for (const n of x.node.querySelectorAll("input, label.sr")) n.remove(); // only agents in the team can be picked
  modal("Work together", () => {
    const [leadTitle, mateTitles] = q.map((x) => x.value());
    const lead = list.find((a) => a.title === leadTitle);
    const mates = list.filter((a) => mateTitles.includes(a.title) && a !== lead);
    if (!lead || !mates.length) { msg.className = "msg err"; msg.textContent = "Pick a lead and at least one other agent to help."; return; }
    const ref = (a) => a.ref || (a.packs ? `skills ${a.packs.join(", ")}` : "");
    const base = lead.brief ? lead.prompt : `${noAsk(lead.prompt)} My brief is in {folder}/brief.json: work in that folder and save every file there. Ask only what you cannot work without, once, in one message.`;
    const title = `${lead.title} with ${mates.map((m) => m.title).join(", ")}`;
    $("modal").close();
    openBrief({ ...lead, title: title.length <= 80 ? title : `${lead.title} with ${mates.length} teammates`, brief: lead.brief || OWN_BRIEF,
      prompt: base + ` This is a team job and you lead it. Your teammates: ${mates.map((m) => `${m.title} (${ref(m)})`).join("; ")}. ` +
        "Keep your own stages. Where a teammate's work fits a part better, do that part the teammate's way: open the teammate's playbook or skills and follow them for that part, in the same task folder. " +
        "In your plan, mark which teammate's way each part follows, and at the end say what each teammate's part produced." });
  },
    el("p", { class: "sub" }, "One job, several agents. The lead runs it and hands each part to the teammate whose work it is, for example the Social Media Manager with the Website Builder for a launch."),
    q.map((x) => x.node),
    el("div", { class: "brief-foot" }, el("button", { type: "submit", class: "primary" }, "Next: the brief"), msg));
}

// shareAgent saves the user's own agent as a file a colleague can import.
async function shareAgent(name) {
  try {
    const k = await api(`/mangoman/skills/${name}`);
    const a = el("a", { href: URL.createObjectURL(new Blob([k.text], { type: "text/markdown" })), download: `${name}.md` });
    document.body.append(a);
    a.click();
    a.remove();
    setTimeout(() => URL.revokeObjectURL(a.href), 1000);
    myMsg(`Saved ${name}.md to your downloads. Send it to a colleague; they choose Import an agent in My Agents. Only the playbook travels: files it made stay with you.`, "ok");
  } catch (err) { myMsg(err.message, "err"); }
}

// importAgent adds an agent (or skill) a colleague shared.
async function importAgent(e) {
  const f = e.target.files[0];
  e.target.value = "";
  if (!f) return;
  const text = await f.text();
  const m = text.match(/^name:\s*"?([a-z0-9-]+)"?\s*$/m);
  if (!m) { myMsg(`${f.name} is not an agent file: it has no name line.`, "err"); return; }
  const name = m[1];
  if (lib.skills.some((k) => k.name === name && !k.own)) { myMsg(`${name} is one of MangoMan's own; it is already here.`, "err"); return; }
  if (lib.skills.some((k) => k.name === name && k.own) && !confirm(`You already have ${name}. Replace it with the one in ${f.name}?`)) return;
  try {
    const { findings } = await api("/mangoman/skills/scan", { method: "POST", body: JSON.stringify({ text }) });
    if (findings.length && !confirm(`${f.name} has parts to read before you use it:\n` +
      findings.slice(0, 6).map((x) => `- line ${x.line}: it ${x.what}`).join("\n") +
      `${findings.length > 6 ? `\n- and ${findings.length - 6} more` : ""}\n\nImport it anyway?`)) { myMsg(`Not imported: ${f.name}.`); return; }
    const k = await api(`/mangoman/skills/${name}`, { method: "PUT", body: JSON.stringify({ text }) });
    await loadLibrary();
    myMsg(k.kind === "agent" ? `Imported ${pretty(name)}. It is in your team below.`
      : `Imported ${name}. It is a skill, not an agent, so it is in the Skills library below.`, "ok");
  } catch (err) { myMsg(`Could not import ${f.name}: ${err.message}`, "err"); }
}

function startButton(prompt) {
  return el("button", { type: "button", class: "primary start", onclick: () => openCoding(prompt) }, "Start");
}

const cardTop = (title) => el("div", { class: "card-top" }, el("span", { class: "tile", "aria-hidden": "true" }, title[0]), el("h3", {}, title));

// startFor is a pre-made agent's way to start: its brief, a file, or a first message.
function startFor(a) {
  return a.super ? el("button", { type: "button", class: "primary start", onclick: () => openBrief(a) }, "Start")
    : a.file ? fileStart(a) : startButton(a.prompt);
}

function renderStarters() {
  $("agent-cards").replaceChildren(...state.starters.map((a) => el("li", { class: "card" },
    cardTop(a.title), el("p", {}, a.summary),
    el("p", { class: "label" }, "You get"), el("ul", { class: "chips" }, a.gets.map((t) => el("li", {}, t))),
    a.stages ? el("p", { class: "label" }, "How it works") : null,
    a.stages ? el("ol", { class: "stages" }, a.stages.map((t) => el("li", {}, t))) : null,
    el("p", { class: "needs" }, "Have ready: " + a.needs.join("; ")),
    startFor(a),
    el("div", { class: "acts" },
      el("button", { type: "button", class: "ghost", "data-team": a.id, "aria-label": `${teamLabel(a.id)}: ${a.title}`,
        onclick: () => toggleTeam(a.id) }, teamLabel(a.id)),
      el("button", { type: "button", class: "ghost", "aria-label": `Make it mine: ${a.title}`, onclick: () => makeMine(a) }, "Make it mine")))));
}

async function loadStarters() {
  let d;
  try { d = await (await fetch("starters.json")).json(); }
  catch (err) { say(`Could not load the agents: ${err.message}`, "err"); return; }
  state.starters = d.agents;
  try { state.team = (await api("/mangoman/my-agents")).agents; state.teamLoaded = true; } catch (err) { say(err.message, "err"); }
  renderStarters();
  renderTeam();
  $("later-cards").replaceChildren(...d.later.map((a) => el("li", { class: "card" },
    cardTop(a.title), el("p", {}, a.summary), el("p", { class: "needs" }, a.why))));
  $("template-cards").replaceChildren(...d.templates.map((t) => el("li", { class: "card" },
    el("h3", {}, t.title), el("p", {}, t.summary), t.prompt ? startButton(t.prompt) : el("p", {}, t.dev))));
}

// ---------- My Files ----------

async function loadFiles() {
  let r;
  try { r = await api("/mangoman/files"); }
  catch (err) { say(`Could not list your files: ${err.message}`, "err"); return; }
  $("set-projects").textContent = r.folder;
  $("files-sub").textContent = `Each task keeps its files in its own folder in ${r.folder}.`;
  $("files-empty").hidden = r.items.length > 0;
  $("files").replaceChildren(...r.items.map((f) => el("li", {},
    el("span", { class: "name" }, f.name), el("span", { class: "when" }, new Date(f.modified).toLocaleString()),
    el("button", { type: "button", class: "ghost", onclick: () => openEntry(f.name) }, "Open"))));
}

async function openEntry(name) {
  try {
    const r = await api("/mangoman/files/open", name ? { method: "POST", body: JSON.stringify({ name }) } : { method: "POST" });
    say(`Opened ${name || r.folder}.`, "ok");
  } catch (err) { say(err.message, "err"); }
}

async function load() {
  try {
    const [ov, act, rv] = await Promise.all([api("/mangoman/overview"), api("/mangoman/activity?hours=24"),
      api("/mangoman/radar").catch(() => null)]);
    state.overview = ov; state.activity = act; state.radar = rv;
    $("app").hidden = false; $("nav").hidden = false; $("gate").hidden = true;
    $("set-version").textContent = ov.version;
    renderHead(ov, act);
    loadNow();
    renderBoard(ov, act);
    renderFree(ov);
    renderMyList(ov);
    renderBrain(ov);
    await loadAgents();
    loadGuardian();
    renderFab();
    if (state.drawer) renderDrawer();
    renderChart(act, ov);
    renderModels(ov, act);
    renderRecent(ov, act);
  } catch (err) {
    if (err.status === 401) { $("app").hidden = true; $("nav").hidden = true; $("gate").hidden = false; $("runline").textContent = "No access token."; return; }
    $("runline").replaceChildren(el("span", { class: "lamp bad", "aria-hidden": "true" }), `Can't reach the router: ${err.message}. Is mangoman serve running?`);
  }
}

document.addEventListener("DOMContentLoaded", () => {
  state.token = readToken();
  if (!state.token) { $("gate").hidden = false; $("runline").textContent = "No access token."; return; }
  $("refresh").addEventListener("click", load);
  $("to-code").addEventListener("click", () => openCoding(""));
  $("to-files").addEventListener("click", () => openEntry(""));
  $("heal-form").addEventListener("submit", connectApp);
  for (const r of document.querySelectorAll('input[name="heal-mode"]')) r.addEventListener("change", showHealMode);
  $("arch-share").addEventListener("change", async (e) => {
    try { renderArch(await api("/mangoman/arch", { method: "POST", body: JSON.stringify({ share: e.target.checked }) })); }
    catch (err) { alert(err.message); loadArch(); }
  });
  window.addEventListener("hashchange", showView);
  showView();
  loadStarters();
  loadLibrary();
  $("quit").addEventListener("click", async () => {
    if (!confirm("Stop MangoMan? Agents and tools using it stop too.")) return;
    try {
      await api("/mangoman/quit", { method: "POST" });
      document.body.replaceChildren(el("main", { class: "stopped" }, el("h1", {}, "MangoMan has stopped."),
        el("p", {}, "Open it again from its app or icon. You can close this tab.")));
    } catch (err) { alert(err.message); }
  });
  $("lib-new").addEventListener("click", newSkill);
  $("lib-agent").addEventListener("click", newAgent);
  $("my-together").addEventListener("click", workTogether);
  $("my-import").addEventListener("change", importAgent);
  $("mylist-add").addEventListener("submit", (e) => {
    e.preventDefault();
    const v = $("mylist-pick").value;
    if (v) saveMyList([...(state.overview.favorites || []), v]);
  });
  $("mylist-new").addEventListener("click", () => openDrawer(true));
  $("agents-browse").addEventListener("click", () => loadMarket().catch((err) => alert(err.message)));
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
  for (const b of document.querySelectorAll("[data-weaker]")) b.addEventListener("click", () => setWeaker(b.dataset.weaker));
  load();
  setInterval(() => { if (!document.hidden && state.overview) loadNow(); }, 4000);
  setInterval(() => { if (!document.hidden && !state.open.size && !state.drawer) load(); }, 15000);
});
