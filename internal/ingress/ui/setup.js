"use strict";

// The setup page MangoMan opens on its first run. Talks only to the local
// router; every value from the server is inserted as text, never as HTML.

const $ = (id) => document.getElementById(id);
let token = "";
let view = null;

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
    headers: { "Authorization": "Bearer " + token, "Content-Type": "application/json" },
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

function mb(n) { return n > 0 ? `${Math.round(n / 1e6)} MB` : "size unknown"; }

function cell(text, cls, note) {
  const td = document.createElement("td");
  if (cls) td.className = cls;
  td.append(text);
  if (note) {
    const s = document.createElement("span");
    s.className = "note";
    s.textContent = note;
    td.append(s);
  }
  return td;
}

function stepState(id, state) {
  const s = $(id);
  for (const k of ["now", "done", "later"]) s.classList.toggle("is-" + k, state === k);
}

function show() {
  const keyed = view.keys > 0;
  const stage = !keyed ? 1 : !view.ready ? 2 : 3;
  stepState("s1", keyed ? "done" : "now");
  stepState("s2", view.ready ? "done" : stage === 2 ? "now" : "later");
  stepState("s3", stage === 3 ? "now" : "later");
  $("t1").hidden = !keyed;
  $("t2").hidden = !view.ready;

  const body = $("sizes").tBodies[0];
  body.replaceChildren();
  for (const p of view.parts) {
    const tr = document.createElement("tr");
    tr.append(cell(p.name, "", p.note), cell(mb(p.bytes), "r"));
    body.append(tr);
  }
  const total = document.createElement("tr");
  total.className = "total";
  total.append(cell("In all"), cell(mb(view.total), "r"));
  body.append(total);

  const go = $("go");
  go.disabled = view.running;
  go.textContent = view.error ? `Try again (${mb(view.total)})` : `Get ready (${mb(view.total)})`;
  $("progress").hidden = !view.running;
  if (view.running) {
    const pct = view.of > 0 ? Math.floor(view.done * 100 / view.of) : -1;
    $("pstep").textContent = pct >= 0 ? `${view.step}: ${pct}% of ${mb(view.of)}` : `${view.step}…`;
    $("pbar-wrap").classList.toggle("busy", pct < 0);
    $("pbar").style.width = pct >= 0 ? `${pct}%` : ""; // CSSOM: allowed by the page's strict CSP
  }
  const e = view.error || "";
  $("readymsg").textContent = e && e[0].toUpperCase() + e.slice(1) + (/[.!?]$/.test(e) ? "" : ".");
  $("app").hidden = false; // only once the right step is known
}

async function refresh() {
  view = await api("/mangoman/ready");
  show();
  if (view.running) setTimeout(() => refresh().catch(fail), 700);
}

function fail(e) {
  if (e.status === 401) {
    $("app").hidden = true;
    $("gate").hidden = false;
    return;
  }
  $("readymsg").textContent = `Lost touch with MangoMan (${e.message}). Is its window still open?`;
}

$("keyform").addEventListener("submit", async (ev) => {
  ev.preventDefault();
  const msg = $("keymsg");
  const key = $("key").value.trim();
  $("keysave").disabled = true;
  msg.className = "msg";
  msg.textContent = "Checking the key with NVIDIA…";
  try {
    await api("/mangoman/keys", { method: "POST", body: JSON.stringify({ provider: "nvidia", key }) });
    $("key").value = "";
    msg.className = "msg ok";
    msg.textContent = "Connected.";
    await refresh();
  } catch (e) {
    msg.className = "msg err";
    msg.textContent = e.status === 401 ? "This page lost its access: double-click MangoMan again." : e.message;
  } finally {
    $("keysave").disabled = false;
  }
});

$("go").addEventListener("click", async () => {
  $("go").disabled = true;
  try {
    view = await api("/mangoman/ready", { method: "POST" });
    show();
    setTimeout(() => refresh().catch(fail), 700);
  } catch (e) {
    $("go").disabled = false;
    fail(e);
  }
});

$("to-code").addEventListener("click", async () => {
  const msg = $("codemsg");
  try {
    const r = await api("/mangoman/code/open", { method: "POST" });
    msg.className = "msg ok";
    msg.textContent = `The coding screen is opening in a new tab. Your projects go in ${r.folder}.`;
  } catch (e) {
    msg.className = "msg err";
    msg.textContent = e.message;
  }
});

// The app is not signed yet (signing costs money), so the system warns
// before the first start. Say what that was and how to get past it.
function warnedNote() {
  const ua = navigator.userAgent;
  if (/Windows/.test(ua)) return "Did Windows show \"Windows protected your PC\" before this? It shows for apps that are not signed yet. Next time, click More info, then Run anyway.";
  if (/Mac OS X|Macintosh/.test(ua)) return "Did your Mac say it cannot check this app? It says that for apps that are not signed yet. Next time it will not ask: you already allowed it in System Settings, Privacy & Security, Open Anyway.";
  return "";
}

token = readToken();
if (!token) {
  $("gate").hidden = false;
} else {
  const note = warnedNote();
  $("warned").textContent = note;
  $("warned").hidden = !note;
  refresh().catch(fail);
}
