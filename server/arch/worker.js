// Mango Arch server: a Cloudflare Worker with a D1 database (README.md).
//
// POST /v1/report   one MangoMan's counts for this hour (one per hour; a
//                   second replaces the first)
// GET  /v1/lessons  the signed lessons in force
// GET  /v1/daily    the owner's daily report (needs OWNER_TOKEN)
// Every hour (cron) it decides the lessons from the last full hour.
//
// Votes are per network, not per MangoMan: a network is the first three
// parts of an IPv4 address (the first three groups, a /48, of IPv6), stored
// only as a hash salted with a secret and the day. A lesson needs QUORUM
// networks where the model failed at least half the time, and they must be
// at least 60% of the networks that used it, so one place, however many
// MangoMans or fake ids it has, cannot make or block a lesson on its own.
// At most PER_NET reports per network per hour are kept. Lessons only
// reorder models and end within two hours, so the worst forged reports can
// do is cost speed.

const WORDS = ["ok", "busy", "slow", "garbled", "empty", "failed"];
const TARGET = /^[a-z0-9-]{1,40}\/[A-Za-z0-9._:\/-]{1,120}$/;
const INSTALL = /^[0-9a-f]{32}$/;
const MIN_TRIES = 3; // a MangoMan's view of a model counts from this many tries
const LIFE_MS = 2 * 60 * 60 * 1000;
const MAX_LESSONS = 20;
const PER_NET = 20;

const json = (body, status = 200) =>
  new Response(JSON.stringify(body), { status, headers: { "content-type": "application/json" } });

export function hourOf(ms) {
  return new Date(Math.floor(ms / 3600000) * 3600000).toISOString().slice(0, 13);
}

// checkReport returns the cleaned counts, or a reason it was refused.
export function checkReport(r) {
  if (!r || typeof r !== "object" || !INSTALL.test(r.install || "")) return { error: "bad install id" };
  const models = r.models;
  if (!models || typeof models !== "object" || Array.isArray(models)) return { error: "no models" };
  const names = Object.keys(models);
  if (names.length === 0 || names.length > 200) return { error: "1 to 200 models" };
  const out = {};
  for (const t of names) {
    if (!TARGET.test(t)) continue; // a name we do not know how to read is left out, not the whole report
    const m = models[t];
    if (!m || typeof m !== "object") return { error: "bad counts" };
    const c = {};
    for (const [w, n] of Object.entries(m)) {
      if (!WORDS.includes(w) || !Number.isInteger(n) || n < 0 || n > 100000) return { error: "bad counts" };
      if (n > 0) c[w] = n;
    }
    if (Object.keys(c).length) out[t] = c;
  }
  return { install: r.install, models: out };
}

async function sha256(text) {
  const d = await crypto.subtle.digest("SHA-256", new TextEncoder().encode(text));
  return [...new Uint8Array(d)].map((b) => b.toString(16).padStart(2, "0")).join("").slice(0, 16);
}

// prefix is the network part of an address: IPv4 /24, IPv6 /48.
export function prefix(ip) {
  if (!ip.includes(":")) return ip.split(".").slice(0, 3).join(".");
  const [head, tail = ""] = ip.toLowerCase().split("::");
  const h = head ? head.split(":") : [];
  const t = ip.includes("::") && tail ? tail.split(":") : [];
  const full = [...h, ...Array(Math.max(0, 8 - h.length - t.length)).fill("0"), ...t];
  return full.slice(0, 3).map((g) => g.replace(/^0+(?=.)/, "")).join(":");
}

export async function network(ip, salt, day) {
  return sha256(salt + "|" + day + "|" + prefix(ip));
}

// decide turns one hour of reports into demote lessons.
export function decide(rows, quorum, nowMs) {
  const byTarget = {}; // target -> net -> counts added up over that network's MangoMans
  for (const row of rows) {
    for (const [t, c] of Object.entries(JSON.parse(row.models))) {
      const nets = (byTarget[t] ||= {});
      const sum = (nets[row.net] ||= {});
      for (const w of WORDS) sum[w] = (sum[w] || 0) + (c[w] || 0);
    }
  }
  const lessons = [];
  for (const [t, nets] of Object.entries(byTarget)) {
    const views = Object.values(nets).map((c) => ({ c, tries: WORDS.reduce((s, w) => s + c[w], 0) }))
      .filter((v) => v.tries >= MIN_TRIES);
    const bad = views.filter((v) => (v.tries - v.c.ok) * 2 >= v.tries);
    if (bad.length < quorum || bad.length * 10 < views.length * 6) continue;
    const words = {};
    for (const v of bad) for (const w of WORDS.slice(1)) words[w] = (words[w] || 0) + v.c[w];
    const reason = Object.entries(words).sort((a, b) => b[1] - a[1])[0][0];
    lessons.push({ id: `${t}@${hourOf(nowMs - 3600000)}`, kind: "demote", target: t,
      until: new Date(nowMs + LIFE_MS).toISOString(), reason, installs: bad.length });
  }
  return lessons.sort((a, b) => b.installs - a.installs).slice(0, MAX_LESSONS);
}

// same compares in constant time, so the token cannot be guessed by timing.
function same(a, b) {
  let diff = a.length ^ b.length;
  for (let i = 0; i < Math.max(a.length, b.length); i++) diff |= (a.charCodeAt(i) | 0) ^ (b.charCodeAt(i) | 0);
  return diff === 0;
}

const b64 = (bytes) => btoa(String.fromCharCode(...new Uint8Array(bytes)));
const unb64 = (s) => Uint8Array.from(atob(s), (ch) => ch.charCodeAt(0));

export async function sign(bundle, privateKeyB64) {
  const key = await crypto.subtle.importKey("pkcs8", unb64(privateKeyB64), { name: "Ed25519" }, false, ["sign"]);
  const data = new TextEncoder().encode(JSON.stringify(bundle));
  return { data: b64(data), sig: b64(await crypto.subtle.sign({ name: "Ed25519" }, key, data)) };
}

// hourly keeps the lessons still in force, adds the new ones, signs and
// stores the bundle, and drops reports older than a week.
export async function hourly(env, nowMs = Date.now()) {
  const last = hourOf(nowMs - 3600000);
  const { results } = await env.DB.prepare("SELECT net, models FROM reports WHERE hour = ?").bind(last).all();
  const fresh = decide(results, Number(env.QUORUM || 5), nowMs);
  const prev = await env.DB.prepare("SELECT data FROM bundles ORDER BY issued DESC LIMIT 1").first();
  const kept = prev ? JSON.parse(new TextDecoder().decode(unb64(prev.data))).lessons : [];
  const now = new Date(nowMs).toISOString();
  const byTarget = new Map();
  for (const l of [...kept, ...fresh]) if (l.until > now) byTarget.set(l.target, l);
  const bundle = { issued: now, lessons: [...byTarget.values()].slice(0, MAX_LESSONS) };
  const s = await sign(bundle, env.ARCH_PRIVATE_KEY);
  await env.DB.batch([
    env.DB.prepare("INSERT INTO bundles (issued, data, sig) VALUES (?, ?, ?)").bind(now, s.data, s.sig),
    env.DB.prepare("DELETE FROM reports WHERE hour < ?").bind(hourOf(nowMs - 7 * 24 * 3600000)),
    env.DB.prepare("DELETE FROM bundles WHERE issued < ?").bind(new Date(nowMs - 30 * 24 * 3600000).toISOString()),
  ]);
  return bundle;
}

export default {
  async fetch(req, env) {
    const url = new URL(req.url);
    if (req.method === "POST" && url.pathname === "/v1/report") {
      const text = await req.text();
      if (text.length > 64 * 1024) return json({ error: "too large" }, 413);
      let body;
      try { body = JSON.parse(text); } catch { return json({ error: "not JSON" }, 400); }
      if (!env.SALT) return json({ error: "server not set up" }, 503);
      const r = checkReport(body);
      if (r.error) return json({ error: r.error }, 400);
      if (Object.keys(r.models).length === 0) return json({ status: "nothing to keep" });
      const hour = hourOf(Date.now());
      const net = await network(req.headers.get("cf-connecting-ip") || "", env.SALT, hour.slice(0, 10));
      const seen = await env.DB.prepare("SELECT COUNT(*) AS n FROM reports WHERE hour = ? AND net = ? AND install != ?")
        .bind(hour, net, r.install).first();
      if (seen.n >= PER_NET) return json({ error: "too many reports from this network this hour" }, 429);
      await env.DB.prepare("INSERT OR REPLACE INTO reports (hour, install, net, models) VALUES (?, ?, ?, ?)")
        .bind(hour, r.install, net, JSON.stringify(r.models)).run();
      return json({ status: "kept" });
    }
    if (req.method === "GET" && url.pathname === "/v1/lessons") {
      let row = await env.DB.prepare("SELECT data, sig FROM bundles ORDER BY issued DESC LIMIT 1").first();
      if (!row) row = await sign({ issued: new Date().toISOString(), lessons: [] }, env.ARCH_PRIVATE_KEY);
      return json({ data: row.data, sig: row.sig });
    }
    if (req.method === "GET" && url.pathname === "/v1/daily") {
      if (!env.OWNER_TOKEN || !same(req.headers.get("authorization") || "", "Bearer " + env.OWNER_TOKEN)) return json({ error: "owner only" }, 401);
      const since = hourOf(Date.now() - 24 * 3600000);
      const installs = await env.DB.prepare("SELECT COUNT(DISTINCT install) AS n, COUNT(*) AS reports FROM reports WHERE hour >= ?").bind(since).first();
      const { results } = await env.DB.prepare("SELECT data FROM bundles WHERE issued >= ? ORDER BY issued")
        .bind(new Date(Date.now() - 24 * 3600000).toISOString()).all();
      const lessons = new Map(); // every lesson in force at some point in the day, once
      for (const b of results) for (const l of JSON.parse(new TextDecoder().decode(unb64(b.data))).lessons) lessons.set(l.id, l);
      return json({ installs: installs.n, reports: installs.reports, hours: results.length, lessons: [...lessons.values()] });
    }
    return json({ error: "not found" }, 404);
  },
  async scheduled(_event, env) {
    await hourly(env);
  },
};
