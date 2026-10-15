// Tests the Mango Arch Worker against a real SQLite database standing in
// for D1 (Node 22 or later):  node server/arch/test.mjs
// With --fixture it also writes internal/arch/testdata/signed.json, which
// the Go tests verify, so the two sides are checked against each other.
import assert from "node:assert/strict";
import { readFileSync, writeFileSync } from "node:fs";
import { DatabaseSync } from "node:sqlite";
import worker, { checkReport, decide, hourly, hourOf, prefix } from "./worker.js";

const here = new URL(".", import.meta.url);

// A small D1: prepare(sql).bind(...).run() / .all() / .first(), and batch().
function fakeD1() {
  const db = new DatabaseSync(":memory:");
  db.exec(readFileSync(new URL("schema.sql", here), "utf8"));
  const stmt = (sql, args = []) => ({
    bind: (...a) => stmt(sql, a),
    run: async () => db.prepare(sql).run(...args),
    all: async () => ({ results: db.prepare(sql).all(...args) }),
    first: async () => db.prepare(sql).get(...args) ?? null,
  });
  return { prepare: (sql) => stmt(sql), batch: async (list) => { for (const s of list) await s.run(); } };
}

const key = await crypto.subtle.generateKey({ name: "Ed25519" }, true, ["sign", "verify"]);
const b64 = (b) => Buffer.from(b).toString("base64");
const env = { DB: fakeD1(), QUORUM: "3", SALT: "s", OWNER_TOKEN: "owner",
  ARCH_PRIVATE_KEY: b64(await crypto.subtle.exportKey("pkcs8", key.privateKey)) };
const pub = b64(await crypto.subtle.exportKey("raw", key.publicKey));

const id = (n) => n.toString(16).padStart(32, "0");
async function report(install, ip, models) {
  const req = new Request("https://arch.example/v1/report", { method: "POST",
    headers: { "cf-connecting-ip": ip }, body: JSON.stringify({ install, models }) });
  return worker.fetch(req, env);
}
async function verify(signed) {
  const k = await crypto.subtle.importKey("raw", Buffer.from(pub, "base64"), { name: "Ed25519" }, false, ["verify"]);
  const data = Buffer.from(signed.data, "base64");
  assert.ok(await crypto.subtle.verify({ name: "Ed25519" }, k, Buffer.from(signed.sig, "base64"), data), "signature");
  return JSON.parse(data.toString());
}

// Bad reports are refused.
for (const bad of [{}, { install: "x", models: { "a/b": { ok: 1 } } }, { install: id(1), models: {} },
  { install: id(1), models: { "a/b": { ok: -1 } } },
  { install: id(1), models: { "a/b": { prompt: 1 } } }, { install: id(1), models: { "a/b": { ok: 1.5 } } }]) {
  assert.ok(checkReport(bad).error, JSON.stringify(bad));
}
assert.equal((await worker.fetch(new Request("https://arch.example/v1/report", { method: "POST", body: "{" }), env)).status, 400);
// A name it cannot read is left out; the rest of the report is kept.
assert.deepEqual(checkReport({ install: id(1), models: { "no slash": { ok: 1 }, "a/b": { ok: 2 } } }).models, { "a/b": { ok: 2 } });
// Networks: IPv4 /24 and IPv6 /48, however the address is written.
assert.equal(prefix("1.2.3.4"), "1.2.3");
assert.equal(prefix("2001:db8::5"), prefix("2001:db8:0:0:0:0:0:6"));
assert.equal(prefix("2001:db8:0:1::5"), "2001:db8:0");
assert.notEqual(prefix("2001:db8:1::5"), prefix("2001:db8:2::5"));
assert.equal(prefix("::1"), "0:0:0");

// Before any decision there is still a signed (empty) bundle.
let res = await worker.fetch(new Request("https://arch.example/v1/lessons"), env);
assert.deepEqual((await verify(await res.json())).lessons, []);

// Three networks see kimi slow; one sees it fine. Three MangoMans on ONE
// network say glm is broken: one place cannot make a lesson. Many fake
// "fine" reports from one network cannot block the kimi lesson either.
const slow = { "nvidia/kimi-k3": { ok: 1, slow: 4 }, "groq/gpt-oss-120b": { ok: 9 } };
assert.equal((await report(id(1), "1.2.3.4", slow)).status, 200);
assert.equal((await report(id(2), "5.6.7.8", slow)).status, 200);
assert.equal((await report(id(3), "2001:db8:1:2::9", slow)).status, 200);
for (const n of [4, 8, 9, 10, 11]) assert.equal((await report(id(n), "9.9.9." + n, { "nvidia/kimi-k3": { ok: 5 } })).status, 200);
for (const n of [5, 6, 7]) await report(id(n), "10.0.0." + n, { "nvidia/glm-5.3-flash": { failed: 5 } });
// A network sends at most 20 reports an hour.
let last;
for (let n = 100; n < 125; n++) last = await report(id(n), "7.7.7." + (n % 250), { "groq/x": { ok: 1 } });
assert.equal(last.status, 429);
// Without its secret salt the server refuses reports.
assert.equal((await worker.fetch(new Request("https://arch.example/v1/report", { method: "POST",
  body: JSON.stringify({ install: id(1), models: { "a/b": { ok: 1 } } }) }), { ...env, SALT: "" })).status, 503);

const rows = env.DB.prepare("SELECT net, models FROM reports");
const hourMs = Date.now() + 3600000; // the hour after these reports
const lessons = decide((await rows.all()).results, 3, hourMs);
assert.deepEqual(lessons.map((l) => [l.target, l.reason, l.installs]), [["nvidia/kimi-k3", "slow", 3]]);
assert.equal(decide((await rows.all()).results, 4, hourMs).length, 0, "quorum");

// The hourly run signs it; GET /v1/lessons serves it.
const bundle = await hourly(env, hourMs);
assert.equal(bundle.lessons.length, 1);
res = await worker.fetch(new Request("https://arch.example/v1/lessons"), env);
const signed = await res.json();
assert.equal((await verify(signed)).lessons[0].target, "nvidia/kimi-k3");

// The next hour, with no new reports, keeps the lesson until it ends.
const next = await hourly(env, hourMs + 3600000);
assert.equal(next.lessons.length, 1);
assert.equal((await hourly(env, hourMs + 3 * 3600000)).lessons.length, 0, "ends after two hours");

// The owner's daily report needs the token.
assert.equal((await worker.fetch(new Request("https://arch.example/v1/daily"), env)).status, 401);
res = await worker.fetch(new Request("https://arch.example/v1/daily", { headers: { authorization: "Bearer owner" } }), env);
assert.equal(res.status, 200);
assert.equal(hourOf(Date.UTC(2026, 9, 15, 9, 59)), "2026-10-15T09");

if (process.argv.includes("--fixture")) {
  writeFileSync(new URL("../../internal/arch/testdata/signed.json", here),
    JSON.stringify({ public_key: pub, signed }, null, 2) + "\n");
}
console.log("mango arch server: all checks passed");
