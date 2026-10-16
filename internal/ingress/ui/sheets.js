"use strict";

// Chargesheets: the user's spreadsheets as a dashboard, in the browser.
// The engine (internal/sheets) cleans the data; this file counts it again
// for every filter, draws it with ECharts (vendor/, Apache-2.0) and talks to
// the free models only through the router's endpoints. Values from files
// are always inserted as text, never as HTML.
// Uses app.js's $, el, api, state and say.

const Sheets = (() => {
  const S = { list: [], view: null, data: null, ds: 0, tab: "overview", charts: [], measure: null, grain: null,
    range: "all", from: null, to: null, dims: {}, compare: true, chat: {}, liveTimer: null, sortTable: null, page: 0, q: "",
    boards: null, open: null, plans: {}, span: [null, null], rowCache: new Map() };
  const KIND = { number: "Number", money: "Money", percent: "Percent", date: "Date", category: "Category", text: "Text", id: "ID", email: "Email", url: "Link", yes_no: "Yes / No" };
  const DAY = 86400000;
  const SLOTS = 6;

  // ---------- small helpers ----------
  const host = () => $("view-chargesheets");
  const css = (name) => getComputedStyle(host()).getPropertyValue(name).trim();
  const slot = (i) => (i < SLOTS ? css(`--c${i + 1}`) : css("--c-other"));
  const icon = (d) => { const s = svg("svg", { viewBox: "0 0 24 24", "aria-hidden": "true" }); s.append(svg("path", { d })); return s; };
  const plural = (n, w) => `${fmtInt(n)} ${w}${n === 1 ? "" : "s"}`;
  function fmtInt(n) { return new Intl.NumberFormat(S.locale).format(Math.round(n)); }
  function setRoute(sub) { const h = "#chargesheets" + (sub ? "/" + sub : ""); if (location.hash !== h) history.replaceState(null, "", h); }

  // Numbers: Indian grouping and lakh/crore for rupees.
  function money(v, unit, compact) {
    if (v === null || v === undefined || Number.isNaN(v)) return "-";
    const sym = { INR: "₹", USD: "$", EUR: "€", GBP: "£", JPY: "¥" }[unit] || (unit ? unit + " " : "");
    const neg = v < 0 ? "-" : "";
    const a = Math.abs(v);
    if (compact) {
      if (unit === "INR" || (!unit && S.locale === "en-IN")) {
        if (a >= 1e7) return `${neg}${sym}${trim(a / 1e7)} Cr`;
        if (a >= 1e5) return `${neg}${sym}${trim(a / 1e5)} L`;
      } else {
        if (a >= 1e9) return `${neg}${sym}${trim(a / 1e9)}B`;
        if (a >= 1e6) return `${neg}${sym}${trim(a / 1e6)}M`;
        if (a >= 1e4) return `${neg}${sym}${trim(a / 1e3)}K`;
      }
    }
    const dec = a < 100 && a % 1 ? 2 : 0;
    return neg + sym + new Intl.NumberFormat(S.locale, { minimumFractionDigits: dec, maximumFractionDigits: dec }).format(a);
  }
  function trim(x) { return (x >= 100 ? x.toFixed(0) : x >= 10 ? x.toFixed(1) : x.toFixed(2)).replace(/\.?0+$/, ""); }
  function fmtVal(m, v, compact) {
    if (v === null || v === undefined || Number.isNaN(v)) return "-";
    if (!m) return fmtInt(v);
    if (m.kind === "money") return money(v, m.unit, compact);
    if (m.kind === "percent") return `${trim(v)}%`;
    if (compact && Math.abs(v) >= 1e4) return money(v, "", true);
    return new Intl.NumberFormat(S.locale, { maximumFractionDigits: Math.abs(v) < 100 ? 2 : 0 }).format(v);
  }
  const pct = (v) => `${Math.abs(v) >= 10 ? Math.round(v) : Math.round(v * 10) / 10}%`;
  const MON = ["Jan", "Feb", "Mar", "Apr", "May", "Jun", "Jul", "Aug", "Sep", "Oct", "Nov", "Dec"];
  const dateOf = (d) => new Date(d * DAY);
  const fmtDate = (d) => { const x = dateOf(d); return `${x.getUTCDate()} ${MON[x.getUTCMonth()]} ${x.getUTCFullYear()}`; };
  const isoDay = (d) => dateOf(d).toISOString().slice(0, 10);

  // ---------- data ----------
  function dataset() { return S.data && S.data.datasets[S.ds]; }
  function col(name) { const ds = dataset(); return ds && ds.columns.find((c) => c.name === name); }
  // Prices and rates are averaged: adding up the prices of a catalogue means nothing.
  const averaged = (m) => !!m && (m.kind === "percent" || /price|mrp|rate|per unit|unit cost|cost per|average|avg/i.test(m.name));
  const numeric = (c) => c && ["number", "money", "percent"].includes(c.kind);
  const isDim = (c) => c && ["category", "yes_no"].includes(c.kind);
  function word(c, i) { const k = c.codes[i]; return k < 0 ? null : c.words[k]; }
  function roles() { return rolesOf(dataset()); }
  function colIn(ds, name) { return ds && ds.columns.find((c) => c.name === name); }
  function rolesOf(ds) {
    const col = (name) => colIn(ds, name);
    const r = S.view.sheet.roles || {};
    const own = ds && r.table === ds.name;
    const measures = ((own && r.measures) || []).map(col).filter(numeric);
    for (const c of ds.columns) if (measures.length < 4 && numeric(c) && !measures.includes(c) && (c.kind === "money" || !measures.some((m) => m.kind === "money"))) measures.push(c);
    let date = own && r.date ? col(r.date) : null;
    if (!date || date.kind !== "date") date = ds.columns.find((c) => c.kind === "date") || null;
    let dims = ((own && r.dims) || []).map(col).filter(isDim);
    if (!dims.length) dims = ds.columns.filter((c) => isDim(c) && c.words.length >= 2 && c.words.length <= 60).slice(0, 5);
    let entity = own && r.entity ? col(r.entity) : null;
    if (!entity) entity = ds.columns.find((c) => c.codes && !isDim(c) && c.kind !== "url" && /customer|client|buyer|product|item|title|campaign|name|sku/i.test(c.name)) || null;
    return { measures, date, dims, entity };
  }

  // rows that pass the filters
  function rowsNow() {
    const ds = dataset();
    const r = roles();
    const [lo, hi] = rangeDays(r.date);
    const out = [];
    const filters = Object.entries(S.dims).map(([n, set]) => [col(n), set]).filter(([c]) => c);
    for (let i = 0; i < ds.rows; i++) {
      if (r.date && lo !== null) { const d = r.date.values[i]; if (d === null || d < lo || d > hi) continue; }
      let ok = true;
      for (const [c, set] of filters) { if (!set.has(c.codes ? word(c, i) : c.values[i])) { ok = false; break; } }
      if (ok) out.push(i);
    }
    return out;
  }
  function dateBounds(c) {
    let lo = Infinity, hi = -Infinity;
    for (const d of c.values) if (d !== null) { if (d < lo) lo = d; if (d > hi) hi = d; }
    return lo === Infinity ? [null, null] : [lo, hi];
  }
  // The range is counted back from the data's last day, not from today: a file is a snapshot.
  function rangeDays(c) {
    if (!c || S.range === "all") return [null, null];
    return spanEnding(dateBounds(c)[1]);
  }
  function spanEnding(end) {
    if (end === null) return [null, null];
    const e = dateOf(end);
    const y = e.getUTCFullYear(), m = e.getUTCMonth();
    const day = (Y, M, D) => Math.floor(Date.UTC(Y, M, D) / DAY);
    switch (S.range) {
      case "7": return [end - 6, end];
      case "30": return [end - 29, end];
      case "90": return [end - 89, end];
      case "month": return [day(y, m, 1), end];
      case "lastmonth": return [day(y, m - 1, 1), day(y, m, 0)];
      case "year": return [day(y, 0, 1), end];
      case "custom": return [S.from ?? -Infinity, S.to ?? Infinity];
      default: return [null, null];
    }
  }
  function total(m, idx) {
    if (!m) return idx.length;
    let s = 0, n = 0;
    for (const i of idx) { const v = m.values[i]; if (v !== null) { s += v; n++; } }
    return averaged(m) ? (n ? s / n : null) : s;
  }
  function groupBy(c, m, idx) {
    const g = new Map();
    for (const i of idx) {
      const k = c.codes ? word(c, i) : c.values[i];
      if (k === null || k === undefined) continue;
      let e = g.get(k);
      if (!e) g.set(k, (e = { s: 0, n: 0 }));
      const v = m ? m.values[i] : 1;
      if (v !== null) { e.s += v; e.n++; }
    }
    const fin = (e) => (averaged(m) ? (e.n ? e.s / e.n : 0) : e.s);
    return [...g].map(([k, e]) => ({ k, v: fin(e), n: e.n })).sort((a, b) => b.v - a.v);
  }
  // Time buckets: day numbers in, labels and keys out.
  function bucket(d, grain) {
    const x = dateOf(d), y = x.getUTCFullYear(), m = x.getUTCMonth();
    switch (grain) {
      case "day": return d;
      case "week": return d - ((d + 3) % 7);
      case "month": return y * 12 + m;
      case "quarter": return y * 4 + Math.floor(m / 3);
      default: return y;
    }
  }
  function label(k, grain) {
    switch (grain) {
      case "day": { const x = dateOf(k); return `${x.getUTCDate()} ${MON[x.getUTCMonth()]}`; }
      case "week": { const x = dateOf(k); return `w/c ${x.getUTCDate()} ${MON[x.getUTCMonth()]}`; }
      case "month": return `${MON[k % 12]} ${Math.floor(k / 12)}`;
      case "quarter": return `Q${(k % 4) + 1} ${Math.floor(k / 4)}`;
      default: return String(k);
    }
  }
  function nextKey(k, grain) { return grain === "day" ? k + 1 : grain === "week" ? k + 7 : k + 1; }
  function autoGrain(c, idx) {
    let lo = Infinity, hi = -Infinity;
    for (const i of idx) { const d = c.values[i]; if (d !== null) { lo = Math.min(lo, d); hi = Math.max(hi, d); } }
    return grainFor(hi - lo);
  }
  function grainFor(span) {
    return span <= 45 ? "day" : span <= 200 ? "week" : span <= 1100 ? "month" : span <= 2500 ? "quarter" : "year";
  }
  function series(c, m, idx, grain, split) {
    const keys = new Map();
    let lo = Infinity, hi = -Infinity;
    for (const i of idx) {
      const d = c.values[i];
      if (d === null) continue;
      const k = bucket(d, grain);
      lo = Math.min(lo, k); hi = Math.max(hi, k);
      const v = m ? m.values[i] : 1;
      if (v === null) continue;
      const sk = split ? (word(split, i) ?? "(blank)") : "";
      let e = keys.get(k);
      if (!e) keys.set(k, (e = new Map()));
      const cur = e.get(sk) || { s: 0, n: 0 };
      cur.s += v; cur.n++;
      e.set(sk, cur);
    }
    const out = [];
    if (lo === Infinity) return out;
    for (let k = lo; k <= hi && out.length < 2000; k = nextKey(k, grain)) {
      const e = keys.get(k) || new Map();
      const vals = {};
      for (const [sk, x] of e) vals[sk] = averaged(m) ? x.s / x.n : x.s;
      out.push({ k, label: label(k, grain), vals, v: Object.values(vals).reduce((a, b) => a + b, 0) });
    }
    return out;
  }

  // ---------- charts ----------
  function disposeCharts() { for (const c of S.charts) { try { c.ro.disconnect(); c.chart.dispose(); } catch (_) {} } S.charts = []; }
  function base() {
    const ink = css("--ink-2"), hair = css("--hair"), muted = css("--muted");
    return {
      textStyle: { fontFamily: css("--body") || "system-ui", color: ink },
      grid: { left: 8, right: 16, top: 16, bottom: 8, containLabel: true },
      tooltip: { renderMode: "richText", confine: true, borderWidth: 0, padding: [8, 11], backgroundColor: css("--ink"),
        textStyle: { color: css("--page"), fontSize: 12.5, lineHeight: 18, rich: {
          t: { fontWeight: 650, color: css("--page"), lineHeight: 20 }, v: { fontWeight: 650, color: css("--page") },
          k0: { color: css("--off") }, k1: { color: slot(0) }, k2: { color: slot(1) }, k3: { color: slot(2) }, k4: { color: slot(3) }, k5: { color: slot(4) }, k6: { color: slot(5) }, k7: { color: slot(6) } } } },
      animationDuration: 500,
      _axis: { axisLine: { lineStyle: { color: css("--axis") } }, axisTick: { show: false }, axisLabel: { color: muted, fontSize: 11.5 }, splitLine: { lineStyle: { color: hair, type: "solid" } } },
    };
  }
  function axis(extra) { const a = base()._axis; return { ...a, ...extra, axisLabel: { ...a.axisLabel, ...(extra && extra.axisLabel) } }; }
  function chart(node, option, onClick) {
    const b = base();
    delete b._axis;
    const c = echarts.init(node, null, { renderer: "canvas" });
    c.setOption({ ...b, ...option, tooltip: { ...b.tooltip, ...(option.tooltip || {}) } });
    if (onClick) c.on("click", onClick);
    const ro = new ResizeObserver(() => c.resize());
    ro.observe(node);
    S.charts.push({ chart: c, ro });
    return c;
  }
  // Tooltips are drawn on the canvas (renderMode richText): the page's CSP
  // blocks the style attributes an HTML tooltip needs. {x|...} is ECharts' rich text.
  const rt = (s) => String(s ?? "").replace(/[{}|]/g, " ");
  function tipRow(cls, name, val) { return `{${cls}|\u25a0} ${rt(name)}: {v|${rt(val)}}`; }

  // card with a chart and its table twin
  function card(title, sub, build, table, opts = {}) {
    const body = el("div", { class: "cs-chart" + (opts.size ? " " + opts.size : "") });
    const tbl = el("div", { class: "cs-tw cs-scroll", hidden: true });
    const btn = table ? el("button", { type: "button", class: "cs-mini", "aria-pressed": "false" }, "Table") : null;
    if (btn) btn.addEventListener("click", () => {
      const on = btn.getAttribute("aria-pressed") !== "true";
      btn.setAttribute("aria-pressed", String(on));
      tbl.hidden = !on; body.hidden = on;
      if (on && !tbl.firstChild) tbl.append(simpleTable(table()));
    });
    const node = el("section", { class: "cs-card2" + (opts.cls ? " " + opts.cls : "") },
      el("header", {}, el("div", {}, el("h3", {}, title), sub ? el("p", { class: "sub" }, sub) : null), el("div", { class: "cs-row" }, opts.tools || null, btn)),
      body, tbl, opts.after || null);
    requestAnimationFrame(() => { if (body.isConnected) build(body); });
    return node;
  }
  function simpleTable({ head, rows, right }) {
    const t = el("table", { class: "cs-table" });
    t.append(el("thead", {}, el("tr", {}, head.map((h, j) => el("th", { class: right && right.includes(j) ? "r" : null }, h)))));
    t.append(el("tbody", {}, rows.map((r) => el("tr", {}, r.map((v, j) => el("td", { class: right && right.includes(j) ? "r" : null }, v))))));
    return t;
  }
  // Heat squares: the one-hue ramp, and a label colour that reads on its square.
  function heat(max, fmt) {
    return { min: 0, max: max || 1, orient: "horizontal", left: "center", bottom: 0, itemWidth: 12, itemHeight: 120, text: ["More", "Less"],
      textStyle: { color: css("--muted") }, inRange: { color: [1, 2, 3, 4, 5, 6, 7].map((i) => css(`--q${i}`)) }, formatter: fmt };
  }
  function heatData(data, max) {
    return data.map((d) => ({ value: d, label: { color: typeof d[2] === "number" && d[2] > 0.55 * (max || 1) ? css("--page") : css("--ink") } }));
  }
  function legend(items) { return el("div", { class: "cs-legend" }, items.map(([c, l]) => el("span", {}, el("i", { style: { background: c } }), l))); }

  function barChart(node, items, m, { onPick, horizontal = true, colorOf } = {}) {
    const c1 = slot(0);
    const data = items.map((it) => ({ value: it.v, name: String(it.k), itemStyle: { color: colorOf ? colorOf(it) : c1 } }));
    const valAxis = axis({ type: "value", axisLabel: { formatter: (v) => fmtVal(m, v, true) } });
    const catAxis = axis({ type: "category", data: items.map((it) => String(it.k)), inverse: horizontal, axisLabel: { width: 130, overflow: "truncate" }, splitLine: { show: false } });
    return chart(node, {
      grid: { left: 8, right: 54, top: 8, bottom: 8, containLabel: true },
      xAxis: horizontal ? valAxis : catAxis, yAxis: horizontal ? catAxis : valAxis,
      tooltip: { trigger: "item", formatter: (p) => `{t|${rt(p.name)}}\n${rt(fmtVal(m, p.value))}${items[p.dataIndex].share !== undefined ? ` · ${rt(pct(items[p.dataIndex].share))} of the total` : ""}` },
      series: [{ type: "bar", data, barMaxWidth: 22, itemStyle: { borderRadius: horizontal ? [0, 4, 4, 0] : [4, 4, 0, 0] },
        label: { show: items.length <= 12, position: horizontal ? "right" : "top", color: css("--ink-2"), fontSize: 11.5, formatter: (p) => fmtVal(m, p.value, true) },
        emphasis: { itemStyle: { opacity: 0.85 } } }],
    }, onPick ? (p) => onPick(items[p.dataIndex]) : null);
  }

  function stackedBars(node, pts, m, keys) {
    return chart(node, {
      grid: { left: 8, right: 16, top: 16, bottom: 8, containLabel: true },
      xAxis: axis({ type: "category", data: pts.map((p) => p.label), splitLine: { show: false } }),
      yAxis: axis({ type: "value", axisLabel: { formatter: (v) => fmtVal(m, v, true) } }),
      tooltip: { trigger: "axis", axisPointer: { type: "shadow" }, formatter: (ps) => `{t|${rt(ps[0].axisValue)}}\n` + ps.filter((p) => p.value).map((p) => tipRow("k" + (Math.min(p.seriesIndex, SLOTS) + 1), p.seriesName, fmtVal(m, p.value))).join("\n") },
      series: keys.map((k, j) => ({ name: k, type: "bar", stack: "all", barMaxWidth: 34, emphasis: { focus: "series" },
        itemStyle: { color: slot(j), borderColor: css("--surface"), borderWidth: 1 }, data: pts.map((p) => p.vals[k] || 0) })),
    });
  }

  function lineChart(node, pts, m, { onPick } = {}) {
    const x = pts.map((p) => p.label);
    const s = [{ name: m ? m.name : "Count", type: "line", smooth: 0.2, symbol: "circle", symbolSize: 8, showSymbol: pts.length <= 24,
      lineStyle: { width: 2, color: slot(0) }, itemStyle: { color: slot(0), borderColor: css("--surface"), borderWidth: 2 },
      areaStyle: { color: new echarts.graphic.LinearGradient(0, 0, 0, 1, [{ offset: 0, color: slot(0) + "33" }, { offset: 1, color: slot(0) + "00" }]) },
      data: pts.map((p) => p.v) }];
    return chart(node, {
      grid: { left: 8, right: 16, top: 16, bottom: 8, containLabel: true },
      xAxis: axis({ type: "category", data: x, boundaryGap: false, splitLine: { show: false } }),
      yAxis: axis({ type: "value", axisLabel: { formatter: (v) => fmtVal(m, v, true) } }),
      tooltip: { trigger: "axis", axisPointer: { type: "line", lineStyle: { color: css("--axis") } },
        formatter: (ps) => `{t|${rt(ps[0].axisValue)}}\n` + ps.map((p) => tipRow("k1", p.seriesName, fmtVal(m, p.value))).join("\n") },
      series: s,
    }, onPick ? (p) => onPick(pts[p.dataIndex]) : null);
  }

  // ---------- library ----------
  function closeAsk() { for (const p of ["cs-ask", "cs-plan"]) { const a = $(p); if (a) a.remove(); } }
  async function showLibrary() {
    closeAsk();
    stopLive();
    disposeCharts();
    setRoute("");
    const h = host();
    h.replaceChildren(
      el("div", { class: "view-head" }, el("h2", {}, "Chargesheets"),
        el("p", { class: "sub" }, "Turn a spreadsheet into a dashboard in one click, then ask it anything. Your data stays on this computer; only column names, small summaries and answers go to the free AI.")),
      addPanel(null),
      el("h3", { class: "cs-section" }, "Your Chargesheets"),
      el("div", { class: "cs-list", id: "cs-list" }, el("p", { class: "cs-muted" }, "Loading…")));
    try { S.list = (await api("/mangoman/sheets")).sheets; } catch (err) { $("cs-list").replaceChildren(el("p", { class: "cs-empty" }, err.message)); return; }
    const list = $("cs-list");
    if (!S.list.length) { list.replaceChildren(el("p", { class: "cs-empty" }, "No Chargesheets yet. Add a file or link a Google Sheet above.")); return; }
    list.replaceChildren(...S.list.map((sh) => el("button", { type: "button", class: "cs-card", onclick: () => open(sh.id) },
      el("div", { class: "cs-row" }, el("h4", { class: "cs-grow" }, sh.title), sh.sources.some((s) => s.url || s.kind) ? el("span", { class: "cs-live" }, "Live") : null),
      el("p", {}, sh.error ? `Could not read: ${sh.error}` : sh.about || sh.sources.map((s) => s.label).join(", ")),
      el("div", { class: "cs-meta" }, sh.rows ? el("span", {}, plural(sh.rows, "row")) : null, el("span", {}, plural(sh.sources.length, "source")), el("span", {}, "Updated " + ago(sh.updated))))));
  }
  function ago(t) {
    const s = (Date.now() - Date.parse(t)) / 1000;
    if (!(s >= 0)) return "just now";
    if (s < 60) return "just now";
    if (s < 3600) return `${Math.round(s / 60)} min ago`;
    if (s < 86400) return `${Math.round(s / 3600)} h ago`;
    return new Date(t).toLocaleDateString();
  }

  // The two ways in: upload files, or link a Google Sheet. id: add to that sheet.
  function addPanel(id) {
    const input = el("input", { type: "file", multiple: true, accept: ".xlsx,.xlsm,.csv,.tsv,.txt", hidden: true });
    const drop = el("div", { class: "cs-drop", tabindex: "0", role: "button" }, el("b", {}, "Choose files"), " or drop them here", el("br"), el("span", { class: "cs-small cs-muted" }, "Excel (.xlsx) or CSV, up to 50 MB each. Several files are combined."));
    const pick = () => input.click();
    drop.addEventListener("click", pick);
    drop.addEventListener("keydown", (e) => { if (e.key === "Enter" || e.key === " ") { e.preventDefault(); pick(); } });
    drop.addEventListener("dragover", (e) => { e.preventDefault(); drop.classList.add("over"); });
    drop.addEventListener("dragleave", () => drop.classList.remove("over"));
    drop.addEventListener("drop", (e) => { e.preventDefault(); drop.classList.remove("over"); if (e.dataTransfer.files.length) addFiles(id, [...e.dataTransfer.files]); });
    input.addEventListener("change", () => { if (input.files.length) addFiles(id, [...input.files]); });
    const url = el("input", { type: "url", placeholder: "https://docs.google.com/spreadsheets/d/…", "aria-label": "Google Sheet link" });
    const go = el("button", { type: "submit", class: "primary" }, "Link");
    const form = el("form", { class: "cs-link" }, url, go);
    form.addEventListener("submit", (e) => { e.preventDefault(); if (url.value.trim()) addGoogle(id, url.value.trim()); });
    return el("div", { class: "cs-add" },
      el("div", { class: "cs-option" }, el("h3", {}, el("span", { class: "cs-badge" }, icon("M12 16V4M7 9l5-5 5 5M4 20h16")), "Upload files"),
        el("p", {}, "Months in separate files are stacked; a customers or products sheet is linked to the orders."), drop, input),
      el("div", { class: "cs-option" }, el("h3", {}, el("span", { class: "cs-badge" }, icon("M10 14a4 4 0 0 0 5.7 0l3-3a4 4 0 0 0-5.7-5.7l-1 1M14 10a4 4 0 0 0-5.7 0l-3 3a4 4 0 0 0 5.7 5.7l1-1")), "Link a Google Sheet"),
        el("p", {}, "Every tab is read, and the dashboard updates itself while it is open: a live dashboard."), form,
        el("p", { class: "cs-hint" }, "First, in Google Sheets: ", el("b", {}, "Share"), " → General access → ", el("b", {}, "Anyone with the link"), " (Viewer).")),
      connectPanel(id));
  }

  // ---------- connections: Shopify, Meta Ads, Google Ads ----------
  // Google's API needs an approved developer account, so Google Ads comes in
  // through a Google Sheet that a Google Ads script fills every day.
  const ADS_SCRIPT = `// MangoMan Chargesheets: writes each campaign's daily results to a Google Sheet.
const SHEET_URL = 'PASTE YOUR GOOGLE SHEET LINK HERE';

function main() {
  const account = AdsApp.currentAccount();
  const zone = account.getTimeZone();
  const day = (d) => Utilities.formatDate(d, zone, 'yyyy-MM-dd');
  const end = new Date();
  const start = new Date(end.getTime() - 364 * 24 * 3600 * 1000);
  const cur = account.getCurrencyCode();
  const rows = [['Date', 'Campaign', 'Cost (' + cur + ')', 'Impressions', 'Clicks', 'Conversions', 'Conversion value (' + cur + ')']];
  const it = AdsApp.search('SELECT segments.date, campaign.name, metrics.cost_micros, metrics.impressions, metrics.clicks, ' +
    'metrics.conversions, metrics.conversions_value FROM campaign ' +
    "WHERE segments.date BETWEEN '" + day(start) + "' AND '" + day(end) + "'");
  while (it.hasNext()) {
    const r = it.next();
    rows.push([r.segments.date, r.campaign.name, r.metrics.costMicros / 1e6, r.metrics.impressions,
      r.metrics.clicks, r.metrics.conversions, r.metrics.conversionsValue]);
  }
  const sheet = SpreadsheetApp.openByUrl(SHEET_URL).getSheets()[0];
  sheet.clearContents();
  sheet.getRange(1, 1, rows.length, rows[0].length).setValues(rows);
}
`;
  function connectPanel(id) {
    const box = el("div", { class: "cs-connect" });
    const field = (label, attrs) => { const i = el("input", { ...attrs, "aria-label": label }); return [el("label", { class: "cs-field" }, el("span", {}, label), i), i]; };
    const steps = (items) => el("ol", { class: "cs-howto" }, items.map((x) => el("li", {}, x)));
    const forms = {
      shopify() {
        const [a, shop] = field("Store address", { placeholder: "my-store.myshopify.com", autocomplete: "off" });
        const [b, cid] = field("Client ID", { autocomplete: "off" });
        const [c, sec] = field("Client secret", { type: "password", autocomplete: "off" });
        const f = el("form", { class: "cs-cform" }, a, b, c, el("button", { type: "submit", class: "primary" }, "Connect Shopify"));
        f.addEventListener("submit", (e) => { e.preventDefault(); connect(id, "shopify", shop.value, cid.value.trim() + "\n" + sec.value.trim(), "Connecting your Shopify store", "Reading your orders from Shopify"); });
        return [f, el("details", {}, el("summary", {}, "How to get these (about 10 minutes)"), steps([
          "Open Shopify's Dev Dashboard, signed in as the store's owner, and create an app.",
          "In the app's Versions tab, choose the access scope read_orders, then click Release.",
          "Install the app on your store.",
          "In the app's Settings, copy the Client ID and the Client secret into the boxes above."]),
          el("p", { class: "cs-hint" }, "No store yet? In the Dev Dashboard, make a free development store and let Shopify fill it with test orders. Shopify shares the last 60 days of orders unless the app is allowed all orders.")),
          el("p", { class: "cs-hint" }, "MangoMan reads orders and the products in them, and keeps the secret in this computer's key store.")];
      },
      meta() {
        const [a, acct] = field("Ad account ID", { placeholder: "act_1234567890", autocomplete: "off" });
        const [b, tok] = field("Access token", { type: "password", autocomplete: "off" });
        const f = el("form", { class: "cs-cform" }, a, b, el("button", { type: "submit", class: "primary" }, "Connect Meta Ads"));
        f.addEventListener("submit", (e) => { e.preventDefault(); connect(id, "meta", acct.value, tok.value, "Connecting your Meta ad account", "Reading a year of campaign results from Meta"); });
        return [f, el("details", {}, el("summary", {}, "How to get these (about 10 minutes)"), steps([
          "In Ads Manager, copy the ad account ID: the number shown with the account's name.",
          "In Meta Business settings, open System users, add one, and give it your ad account.",
          "Generate a token for that system user with the ads_read permission. It asks for an app: make one in Meta for Developers, any name will do.",
          "For the token's expiry, choose Never; with 60 days, paste a new token here when it runs out."])),
          el("p", { class: "cs-hint" }, "MangoMan reads each campaign's daily spend, clicks and purchases for the last year, and keeps the token in this computer's key store.")];
      },
      google() {
        const url = el("input", { type: "url", placeholder: "https://docs.google.com/spreadsheets/d/…", "aria-label": "Google Sheet link" });
        const f = el("form", { class: "cs-link" }, url, el("button", { type: "submit", class: "primary" }, "Link"));
        f.addEventListener("submit", (e) => { e.preventDefault(); if (url.value.trim()) addGoogle(id, url.value.trim()); });
        const copy = el("button", { type: "button", class: "cs-mini" }, "Copy the script");
        copy.addEventListener("click", async () => { try { await navigator.clipboard.writeText(ADS_SCRIPT); copy.textContent = "Copied"; } catch (_) { copy.textContent = "Select the script and copy it"; } });
        return [el("p", { class: "cs-hint" }, "Google's API needs an approved developer account, so Google Ads comes in through a Google Sheet that a small Google Ads script fills every day."),
          steps(["Make a new Google Sheet. Share → General access → Anyone with the link (Viewer).",
            "In Google Ads: Tools → Bulk actions → Scripts → New script. Paste the script, put your sheet's link in its second line, then Authorize and Run.",
            "Set the script's frequency to Daily.",
            "Paste the sheet's link here."]),
          f, el("details", {}, el("summary", {}, "The script"), el("div", { class: "cs-row" }, copy), el("pre", { class: "cs-code" }, ADS_SCRIPT))];
      },
    };
    const pick = el("div", { class: "cs-seg", role: "group", "aria-label": "App" }, [["shopify", "Shopify"], ["meta", "Meta Ads"], ["google", "Google Ads"]].map(([k, l]) => {
      const b = el("button", { type: "button", "aria-pressed": "false" }, l);
      b.addEventListener("click", () => { for (const x of pick.children) x.setAttribute("aria-pressed", String(x === b)); box.replaceChildren(...forms[k]()); const f = box.querySelector("input"); if (f) f.focus(); });
      return b;
    }));
    return el("div", { class: "cs-option cs-span" }, el("h3", {}, el("span", { class: "cs-badge" }, icon("M4 12h4l3-8 4 16 3-8h2")), "Connect an app"),
      el("p", {}, "Shopify orders and Meta Ads come straight from their apps and stay up to date. Put several in one Chargesheet for the whole picture: sales against ad spend."), pick, box);
  }
  async function connect(id, kind, account, secret, title, step) {
    const p = progress(title, [step, "Reading and cleaning the data", "Linking tables and checking totals", "Getting the dashboard ready"]);
    let fresh = false, added = 0;
    try {
      p.next();
      if (!id) { id = (await newSheet()).id; fresh = true; }
      await api(`/mangoman/sheets/${id}/connect`, { method: "POST", body: JSON.stringify({ kind, account: account.trim(), secret }) });
      added = 1;
      p.next();
      await load(id, p);
    } catch (err) { if (fresh) await forget(id, added); p.fail(err.message); }
  }

  function progress(title, steps) {
    disposeCharts();
    const items = steps.map((s) => el("li", {}, s));
    host().replaceChildren(el("div", { class: "cs-progress", role: "status" }, el("h3", {}, title), el("ol", { class: "cs-steps" }, items)));
    let at = -1;
    return {
      next() { if (at >= 0) items[at].className = "done"; at++; if (items[at]) items[at].className = "now"; },
      fail(msg) { if (items[at]) items[at].className = "fail"; host().querySelector(".cs-progress").append(el("p", { class: "msg err" }, msg), el("button", { type: "button", class: "ghost", onclick: showLibrary }, "Back to Chargesheets")); },
    };
  }

  async function newSheet() { return api("/mangoman/sheets", { method: "POST", body: JSON.stringify({ title: "" }) }); }

  // A new sheet that got no data is removed again when adding fails.
  async function forget(id, added) { if (!added) { try { await api(`/mangoman/sheets/${id}`, { method: "DELETE" }); } catch (_) {} } }
  async function addFiles(id, files) {
    const p = progress("Making your Chargesheet", [...files.map((f) => `Copying ${f.name}`), "Reading and cleaning the data", "Linking tables and checking totals", "Getting the dashboard ready"]);
    let fresh = false, added = 0;
    try {
      p.next();
      if (!id) { id = (await newSheet()).id; fresh = true; }
      for (const f of files) {
        const res = await fetch(`/mangoman/sheets/${id}/files?name=${encodeURIComponent(f.name)}`, { method: "POST", body: f, headers: { Authorization: "Bearer " + state.token } });
        const body = await res.json().catch(() => ({}));
        if (!res.ok) throw new Error((body.error && body.error.message) || `HTTP ${res.status}`);
        added++;
        p.next();
      }
      await load(id, p);
    } catch (err) { if (fresh) await forget(id, added); p.fail(err.message); }
  }
  async function addGoogle(id, link) {
    const p = progress("Linking your Google Sheet", ["Downloading every tab from Google Sheets", "Reading and cleaning the data", "Linking tables and checking totals", "Getting the dashboard ready"]);
    let fresh = false, added = 0;
    try {
      p.next();
      if (!id) { id = (await newSheet()).id; fresh = true; }
      await api(`/mangoman/sheets/${id}/google`, { method: "POST", body: JSON.stringify({ url: link }) });
      added = 1;
      p.next();
      await load(id, p);
    } catch (err) { if (fresh) await forget(id, added); p.fail(err.message); }
  }
  async function load(id, p) {
    const t = setTimeout(() => p.next(), 900);
    const v = await api(`/mangoman/sheets/${id}/load`, { method: "POST" });
    clearTimeout(t);
    p.next(); p.next();
    S.view = v; S.data = null;
    if (host().querySelector(".cs-progress")) showCheck(id); // not when the user went elsewhere meanwhile
  }

  // ---------- the check screen: what was read ----------
  async function showCheck(id) {
    stopLive();
    disposeCharts();
    setRoute(id + "/check");
    if (!S.view || S.view.sheet.id !== id) {
      try { S.view = await api(`/mangoman/sheets/${id}`); } catch (err) { say(err.message, "err"); showLibrary(); return; }
    }
    const v = S.view;
    S.locale = v.profile && v.profile.datasets.some((d) => d.currency === "INR") ? "en-IN" : undefined;
    const choices = { kinds: { ...(v.choices.kinds || {}) }, nolink: [...(v.choices.nolink || [])], nostack: !!v.choices.nostack, header: v.choices.header || {} };
    const apply = el("button", { type: "button", class: "primary", hidden: true }, "Apply changes");
    const openBtn = el("button", { type: "button", class: "primary" }, "Open dashboard");
    openBtn.addEventListener("click", () => open(id));
    apply.addEventListener("click", async () => {
      apply.disabled = true; apply.textContent = "Reading again…";
      try { S.view = await api(`/mangoman/sheets/${id}/choices`, { method: "POST", body: JSON.stringify(choices) }); S.data = null; showCheck(id); }
      catch (err) { say(err.message, "err"); apply.disabled = false; apply.textContent = "Apply changes"; }
    });
    const changed = () => { apply.hidden = false; openBtn.hidden = true; };
    const prof = v.profile;
    const parts = [];
    if (v.sheet.error) parts.push(el("p", { class: "msg err" }, v.sheet.error));
    if (prof) {
      for (const ds of prof.datasets) {
        const rows = ds.columns.map((c) => {
          const sel = el("select", { "aria-label": `${c.name} holds` }, Object.entries(KIND).map(([k, l]) => el("option", { value: k, selected: k === c.kind }, l)));
          if (c.kind === "empty") sel.disabled = true;
          sel.addEventListener("change", () => { choices.kinds[`${ds.name}/${c.name}`] = sel.value; sel.classList.add("changed"); changed(); });
          let what = "";
          if (c.kind === "date") what = `${c.min} to ${c.max}`;
          else if (["number", "money", "percent"].includes(c.kind)) what = `${fmtVal({ kind: c.kind, unit: c.unit }, c.min)} to ${fmtVal({ kind: c.kind, unit: c.unit }, c.max)}` + (c.sum !== undefined && c.kind !== "percent" ? ` · total ${fmtVal({ kind: c.kind, unit: c.unit }, c.sum)}` : "");
          else if (c.top) what = `${c.distinct} kinds: ` + c.top.slice(0, 4).map((t) => t[0]).join(", ");
          else if (c.examples) what = `${fmtInt(c.distinct)} different, e.g. ` + c.examples.join(", ");
          const flags = [c.blanks ? `${fmtInt(c.blanks)} blank` : null, c.unreadable ? `${fmtInt(c.unreadable)} unreadable` : null, c.merged ? `${c.merged} spelling${c.merged > 1 ? "s" : ""} merged` : null].filter(Boolean).join(" · ");
          return el("tr", {}, el("td", {}, el("b", {}, c.name)), el("td", {}, sel, c.unit && c.unit !== "%" ? ` ${c.unit}` : ""), el("td", { class: "cs-ex", title: what }, what), el("td", { class: "cs-small cs-muted" }, flags));
        });
        const notes = ds.notes.map((n) => el("li", { class: /unreadable|did not|empty/.test(n) ? "warn" : null }, n));
        for (const c of ds.columns) if (c.unreadable) notes.push(el("li", { class: "warn" }, `${c.name}: ${fmtInt(c.unreadable)} value${c.unreadable > 1 ? "s" : ""} could not be read as ${KIND[c.kind].toLowerCase()} and count as blank.`));
        parts.push(el("section", { class: "cs-ds" },
          el("div", { class: "cs-row" }, el("h3", { class: "cs-grow" }, ds.name), el("span", { class: "cs-muted cs-small" }, plural(ds.rows, "row") + " · " + plural(ds.columns.length, "column"))),
          notes.length ? el("ul", { class: "cs-notes" }, notes) : null,
          el("div", { class: "cs-tw" }, el("table", { class: "cs-cols" }, el("thead", {}, el("tr", {}, el("th", {}, "Column"), el("th", {}, "Reads as"), el("th", {}, "What is in it"), el("th", {}, "Notes"))), el("tbody", {}, rows)))));
      }
      for (const l of prof.links) {
        const box = el("input", { type: "checkbox", checked: l.on });
        box.addEventListener("change", () => { choices.nolink = box.checked ? choices.nolink.filter((x) => x !== l.id) : [...choices.nolink, l.id]; changed(); });
        parts.splice(1, 0, el("label", { class: "cs-ds cs-row" }, box, el("span", {}, `Link ${l.to} to ${l.from} by ${l.by}: ${l.matched}% of rows match.`)));
      }
    }
    const sources = el("div", { class: "cs-sources" }, v.sheet.sources.map((s) => el("span", { class: "cs-chip" + (s.url || s.kind ? " on" : "") }, s.url ? "Google Sheet: " : "", s.label,
      el("button", { type: "button", title: "Remove", "aria-label": `Remove ${s.label}`, onclick: async () => {
        if (!confirm(`Remove ${s.label} from this Chargesheet?`)) return;
        try { await api(`/mangoman/sheets/${id}/files/${encodeURIComponent(s.file)}`, { method: "DELETE" }); S.view = null; S.data = null;
          const left = await api(`/mangoman/sheets/${id}`); if (left.sheet.sources.length) await reload(id); else { S.view = left; showCheck(id); } } catch (err) { say(err.message, "err"); }
      } }, "×"))));
    const more = el("details", {}, el("summary", { class: "cs-mini" }, "Add more data"), addPanel(id));
    host().replaceChildren(
      el("button", { type: "button", class: "cs-back", onclick: showLibrary }, "← All Chargesheets"),
      el("div", { class: "cs-head" }, el("div", { class: "cs-grow" }, el("h2", { class: "cs-title" }, "Here is what I read"),
        el("p", { class: "cs-about" }, "Check the columns: change any that read wrong, and the data is read again. Totals rows, title rows and repeated spellings are already handled.")),
      el("div", { class: "cs-actions" }, apply, openBtn)),
      sources, more, el("div", { class: "cs-check cs-mt" }, parts));
  }
  async function reload(id) {
    const p = progress("Reading your data again", ["Reading and cleaning the data", "Getting the dashboard ready"]);
    try { p.next(); await load(id, p); } catch (err) { p.fail(err.message); }
  }

  // ---------- the dashboard ----------
  async function open(id, tab) {
    disposeCharts();
    for (const a of [$("cs-ask"), $("cs-plan")]) if (a && a.dataset.sheet !== id) a.remove();
    try {
      if (!S.view || S.view.sheet.id !== id) S.view = await api(`/mangoman/sheets/${id}`);
      if (!S.view.profile) { showCheck(id); return; }
      if (!S.data || S.data.id !== id) {
        host().replaceChildren(el("p", { class: "cs-muted" }, "Opening…"));
        S.data = await api(`/mangoman/sheets/${id}/data`);
        S.data.id = id;
        S.ds = Math.max(0, S.data.datasets.findIndex((d) => d.name === (S.view.sheet.roles || {}).table));
        S.dims = {}; S.range = "all"; S.from = null; S.to = null; S.measure = null; S.grain = null; S.page = 0; S.q = ""; S.sortTable = null; S.tab = "overview"; S.open = null;
      }
    } catch (err) { say(err.message, "err"); showLibrary(); return; }
    const cur = S.view.profile.datasets.find((d) => d.name === dataset().name);
    S.locale = cur && cur.currency === "INR" ? "en-IN" : undefined;
    if (tab) S.tab = tab;
    render();
    startLive();
  }

  function tabs() {
    const r = roles();
    const t = [["overview", "Overview"]];
    if (tables().filter((d) => rolesOf(d).date && rolesOf(d).measures.length).length >= 2) t.push(["business", "Business overview"]);
    t.push(["boards", "My dashboards"]);
    if (r.date) t.push(["trends", "Trends"]);
    if (r.dims.length || r.entity) t.push(["breakdown", "Breakdown"]);
    if (r.dims.length >= 1 && (r.dims.length >= 2 || r.entity)) t.push(["drill", "Drill-down"]);
    if (r.measures.length) t.push(["spread", "Spread"]);
    for (const p of packs()) t.push([p.id, p.name]);
    t.push(["quality", "Data quality"], ["table", "All rows"], ["readout", "Readout"]);
    return t;
  }

  function render() {
    disposeCharts();
    const v = S.view, sh = v.sheet, ds = dataset(), r = roles();
    setRoute(sh.id + "/" + S.tab);
    const t = tabs();
    if (!t.some(([k]) => k === S.tab)) S.tab = "overview";
    const title = el("h2", { class: "cs-title", contenteditable: "true", spellcheck: "false", title: "Click to rename" }, sh.title);
    title.addEventListener("keydown", (e) => { if (e.key === "Enter") { e.preventDefault(); title.blur(); } });
    title.addEventListener("blur", async () => {
      const name = title.textContent.trim();
      if (name && name !== sh.title) { try { S.view.sheet = { ...S.view.sheet, ...(await api(`/mangoman/sheets/${sh.id}`, { method: "PATCH", body: JSON.stringify({ title: name }) })) }; } catch (err) { say(err.message, "err"); } }
    });
    const live = v.live ? el("span", { class: "cs-live", id: "cs-live" }, "Live · checked " + ago(liveSource().fetched)) : null;
    const actions = el("div", { class: "cs-actions" },
      live,
      v.live ? el("button", { type: "button", class: "ghost", onclick: () => refresh(true) }, "Update now") : null,
      el("button", { type: "button", class: "ghost", onclick: () => showCheck(sh.id) }, "Check data"),
      el("button", { type: "button", class: "ghost", onclick: () => remove(sh) }, "Delete"),
      el("button", { type: "button", class: "primary", onclick: () => openAsk() }, "Ask a question"));
    const head = el("div", { class: "cs-head" },
      el("div", { class: "cs-grow" }, el("button", { type: "button", class: "cs-back", onclick: showLibrary }, "← All Chargesheets"), title,
        el("p", { class: "cs-about" }, sh.about || `${plural(ds.rows, "row")} from ${sh.sources.map((s) => s.label).join(", ")}.`)), actions);
    const nodes = [head];
    if (sh.changed) nodes.push(el("p", { class: "msg ok" }, sh.changed));
    nodes.push(toolbar(r));
    nodes.push(el("div", { class: "cs-tabs", role: "tablist" }, t.map(([k, l]) => {
      const b = el("button", { type: "button", class: "cs-tab", role: "tab", "aria-selected": String(k === S.tab) }, l);
      b.addEventListener("click", () => { S.tab = k; render(); });
      return b;
    })));
    const body = el("div", { id: "cs-body" });
    nodes.push(body);
    host().replaceChildren(...nodes);
    const sel = host().querySelector('.cs-tab[aria-selected="true"]');
    if (sel) sel.scrollIntoView({ block: "nearest", inline: "center" });
    if (S.tab === "boards" || S.tab === "business") { (S.tab === "boards" ? boardsTab : businessTab)(body); return; }
    const idx = rowsNow();
    if (!idx.length) { body.append(el("p", { class: "cs-empty" }, "No rows match these filters.")); return; }
    const draw = { overview, trends, breakdown, drill, spread, quality, table: allRows, readout }[S.tab] || packs().find((p) => p.id === S.tab).draw;
    draw(body, idx, r);
  }

  function toolbar(r) {
    const parts = [];
    const many = S.tab === "boards" || S.tab === "business"; // dashboards span every table
    if (many) r = { ...r, date: tables().map((d) => rolesOf(d).date).find(Boolean) || null, measures: [] };
    if (S.data.datasets.length > 1 && !many) {
      const pick = el("select", { "aria-label": "Table" }, S.data.datasets.map((d, i) => el("option", { value: i, selected: i === S.ds }, `${d.name} (${plural(d.rows, "row")})`)));
      pick.addEventListener("change", () => { S.ds = +pick.value; S.dims = {}; S.measure = null; S.range = "all"; render(); });
      parts.push(pick);
    }
    if (r.date) {
      const opts = [["all", "All time"], ["7", "Last 7 days"], ["30", "Last 30 days"], ["90", "Last 90 days"], ["month", "Latest month"], ["lastmonth", "Month before"], ["year", "Latest year"], ["custom", "Pick dates"]];
      const sel = el("select", { "aria-label": "Dates" }, opts.map(([k, l]) => el("option", { value: k, selected: S.range === k }, l)));
      sel.addEventListener("change", () => { S.range = sel.value; if (S.range === "custom" && S.from === null) { const [lo, hi] = many ? (S.span = [null, null], allBounds()) : dateBounds(r.date); S.from = lo; S.to = hi; } render(); });
      parts.push(sel);
      if (S.range === "custom") {
        const from = el("input", { type: "date", value: S.from !== null ? isoDay(S.from) : "", "aria-label": "From" });
        const to = el("input", { type: "date", value: S.to !== null ? isoDay(S.to) : "", "aria-label": "To" });
        const set = () => { S.from = from.value ? Math.floor(Date.parse(from.value) / DAY) : null; S.to = to.value ? Math.floor(Date.parse(to.value) / DAY) : null; render(); };
        from.addEventListener("change", set); to.addEventListener("change", set);
        parts.push(from, to);
      }
    }
    if (r.measures.length > 1) {
      const sel = el("select", { "aria-label": "Measure" }, r.measures.map((m) => el("option", { value: m.name, selected: measure(r) === m }, `Showing: ${m.name}`)), el("option", { value: "", selected: S.measure === "" }, "Showing: Count of rows"));
      sel.addEventListener("change", () => { S.measure = sel.value; render(); });
      parts.push(sel);
    }
    const dims = dataset().columns.filter((c) => isDim(c) && c.words.length <= 200);
    if (dims.length) {
      const add = el("select", { "aria-label": "Add a filter" }, el("option", { value: "" }, "+ Filter"), dims.map((c) => el("option", { value: c.name }, c.name)));
      add.addEventListener("change", () => { if (add.value) pickValues(col(add.value)); add.value = ""; });
      parts.push(add);
    }
    for (const [name, set] of Object.entries(S.dims)) {
      parts.push(el("span", { class: "cs-chip on" }, `${name}: ${[...set].slice(0, 2).join(", ")}${set.size > 2 ? ` +${set.size - 2}` : ""}`,
        el("button", { type: "button", "aria-label": `Remove filter ${name}`, onclick: () => { delete S.dims[name]; render(); } }, "×")));
    }
    if (Object.keys(S.dims).length || S.range !== "all") parts.push(el("button", { type: "button", class: "cs-mini", onclick: () => { S.dims = {}; S.range = "all"; render(); } }, "Clear filters"));
    return el("div", { class: "cs-toolbar" }, parts);
  }
  function measure(r) {
    if (S.measure === "") return null;
    return (S.measure && r.measures.find((m) => m.name === S.measure)) || r.measures[0] || null;
  }
  function filterTo(c, value) { S.dims[c.name] = new Set([value]); render(); }
  function pickValues(c) {
    const counts = groupBy(c, null, rowsNow());
    const boxes = counts.slice(0, 60).map((g) => { const b = el("input", { type: "checkbox", value: g.k, checked: S.dims[c.name] ? S.dims[c.name].has(g.k) : false }); return [b, g]; });
    const dlg = el("div", { class: "cs-card2 cs-mt" }, el("header", {}, el("h3", {}, `Show only these ${c.name} values`)),
      el("div", { class: "cs-scroll" }, boxes.map(([b, g]) => el("label", { class: "cs-row cs-small" }, b, `${g.k} (${fmtInt(g.n)})`))),
      el("div", { class: "cs-row cs-mt" }, el("button", { type: "button", class: "primary", onclick: () => {
        const set = new Set(boxes.filter(([b]) => b.checked).map(([, g]) => g.k));
        if (set.size) S.dims[c.name] = set; else delete S.dims[c.name];
        render();
      } }, "Apply"), el("button", { type: "button", class: "ghost", onclick: render }, "Cancel")));
    $("cs-body").prepend(dlg);
    dlg.scrollIntoView({ behavior: "smooth", block: "nearest" });
  }

  // ---------- Overview ----------
  // first day of a time bucket
  function startOf(k, grain) {
    const day = (y, m) => Math.floor(Date.UTC(y, m, 1) / DAY);
    switch (grain) {
      case "day": case "week": return k;
      case "month": return day(Math.floor(k / 12), k % 12);
      case "quarter": return day(Math.floor(k / 4), (k % 4) * 3);
      default: return day(k, 0);
    }
  }
  // The latest period against the one before. When the data stops partway
  // through the latest period, both are counted over the same first days.
  function periodCompare(r, m, idx) {
    if (!r.date) return null;
    const grain = autoGrain(r.date, idx);
    const g = grain === "day" ? "week" : grain === "week" ? "month" : grain;
    const pts = series(r.date, m, idx, g);
    if (pts.length < 2) return null;
    const a = pts[pts.length - 1], b = pts[pts.length - 2];
    const [, end] = rangeOf(r.date, idx);
    const covered = end - startOf(a.k, g);
    const partial = end < startOf(nextKey(a.k, g), g) - 1;
    const inA = idx.filter((i) => { const d = r.date.values[i]; return d !== null && bucket(d, g) === a.k; });
    const inB = idx.filter((i) => { const d = r.date.values[i]; return d !== null && bucket(d, g) === b.k && (!partial || d - startOf(b.k, g) <= covered); });
    const before = { ...b, v: total(m, inB) };
    const label = partial ? `${a.label} so far vs the same ${covered + 1} days of ${b.label}` : `${a.label} vs ${b.label}`;
    return { now: a, before, rowsNow: inA, rowsBefore: inB, label, change: before.v ? ((a.v - before.v) / Math.abs(before.v)) * 100 : null };
  }
  function delta(change) {
    if (change === null || !Number.isFinite(change)) return null;
    const cls = Math.abs(change) < 0.5 ? "flat" : change > 0 ? "up" : "down";
    return el("span", { class: "cs-delta " + cls, "aria-label": `${cls === "up" ? "up" : cls === "down" ? "down" : "about the same,"} ${pct(Math.abs(change))}` }, cls === "up" ? "▲" : cls === "down" ? "▼" : "●", pct(Math.abs(change)));
  }
  function spark(pts) {
    if (pts.length < 2) return null;
    const vals = pts.map((p) => p.v), lo = Math.min(...vals), hi = Math.max(...vals), w = 200, h = 34;
    const xy = vals.map((v, i) => [(i / (vals.length - 1)) * w, h - 3 - ((v - lo) / (hi - lo || 1)) * (h - 6)]);
    const line = "M" + xy.map((p) => p.map((n) => n.toFixed(1)).join(",")).join("L");
    const s = svg("svg", { class: "cs-spark", viewBox: `0 0 ${w} ${h}`, preserveAspectRatio: "none", "aria-hidden": "true" });
    s.append(svg("path", { class: "a", d: `${line}L${w},${h}L0,${h}Z` }), svg("path", { class: "l", d: line }));
    return s;
  }
  function overview(body, idx, r) {
    const m = measure(r);
    const tiles = [];
    const tileFor = (mm) => {
      const v = total(mm, idx);
      const cmp = periodCompare(r, mm, idx);
      const pts = r.date ? series(r.date, mm, idx, autoGrain(r.date, idx)) : [];
      return el("div", { class: "cs-tile", title: mm ? `${mm.name}: ${fmtVal(mm, v)}` : "" },
        el("div", { class: "lab" }, mm ? (averaged(mm) ? `Average ${mm.name}` : `Total ${mm.name}`) : "Rows"),
        el("div", { class: "val" }, fmtVal(mm, v, true)),
        el("div", { class: "foot" }, cmp ? [delta(cmp.change), cmp.label] : mm ? `${plural(idx.length, "row")}` : r.date ? (() => { const [lo, hi] = rangeOf(r.date, idx); return lo !== null ? `${fmtDate(lo)} to ${fmtDate(hi)}` : ""; })() : ""),
        spark(pts));
    };
    for (const mm of r.measures.slice(0, 3)) tiles.push(tileFor(mm));
    tiles.push(tileFor(null));
    const avgTile = r.measures[0] && !averaged(r.measures[0]) && idx.length ? el("div", { class: "cs-tile" }, el("div", { class: "lab" }, `Average ${r.measures[0].name} per row`), el("div", { class: "val" }, fmtVal(r.measures[0], total(r.measures[0], idx) / idx.length, true)), el("div", { class: "foot" }, `median ${fmtVal(r.measures[0], median(r.measures[0], idx), true)}`)) : null;
    if (avgTile && tiles.length < 5) tiles.push(avgTile);
    body.append(el("div", { class: "cs-grid cs-k" }, tiles));

    const row = [];
    if (r.date) {
      const grain = S.grain || autoGrain(r.date, idx);
      const pts = series(r.date, m, idx, grain);
      row.push(card(`${m ? m.name : "Rows"} over time`, `By ${grain}. Click a point to see that ${grain}.`, (n) => lineChart(n, pts, m, { onPick: (p) => zoomTo(p.k, grain) }),
        () => ({ head: [grain[0].toUpperCase() + grain.slice(1), m ? m.name : "Rows"], rows: pts.map((p) => [p.label, fmtVal(m, p.v)]), right: [1] }), { size: "tall" }));
    }
    row.push(el("section", { class: "cs-card2" }, el("header", {}, el("div", {}, el("h3", {}, "What stands out"), el("p", { class: "sub" }, "Worked out by code from your data. Click one to look closer."))), el("div", { class: "cs-insights" }, insights(r, m, idx).slice(0, 5))));
    const d0 = r.dims[0] || (r.entity && r.entity.codes ? r.entity : null);
    const more = [];
    if (!r.date) more.push(row.pop());
    if (d0) {
      const g = top(groupBy(d0, m, idx), 10, m);
      more.push(card(`${m ? m.name : "Rows"} by ${d0.name}`, "Click a bar to filter everything to it.", (n) => barChart(n, g, m, { onPick: (it) => it.k !== "Other" && filterTo(d0, it.k) }),
        () => ({ head: [d0.name, m ? m.name : "Rows", "Share"], rows: g.map((x) => [x.k, fmtVal(m, x.v), x.share === undefined ? "" : pct(x.share)]), right: [1, 2] })));
    }
    const d1 = averaged(m) ? null : r.dims.find((d) => d !== d0 && d.words.length <= 6);
    if (d1) {
      const g = groupBy(d1, m, idx);
      const sum = g.reduce((a, b) => a + Math.max(0, b.v), 0) || 1;
      more.push(card(`Share by ${d1.name}`, null, (n) => chart(n, {
        tooltip: { trigger: "item", formatter: (p) => `{t|${rt(p.name)}}\n${rt(fmtVal(m, p.value))} · ${rt(pct(p.percent))}` },
        series: [{ type: "pie", radius: ["55%", "80%"], center: ["50%", "50%"], padAngle: 1.5, itemStyle: { borderRadius: 4, borderColor: css("--surface"), borderWidth: 2 },
          label: { show: n.clientWidth > 480, formatter: (p) => `${p.name}\n${pct(p.percent)}`, color: css("--ink-2"), fontSize: 11.5 }, labelLine: { length: 8, length2: 6 },
          data: g.map((x, i) => ({ name: x.k, value: Math.max(0, x.v), itemStyle: { color: slot(i) } })) }],
      }, (p) => filterTo(d1, p.name)), () => ({ head: [d1.name, m ? m.name : "Rows", "Share"], rows: g.map((x) => [x.k, fmtVal(m, x.v), pct((Math.max(0, x.v) / sum) * 100)]), right: [1, 2] }),
      { after: legend(g.map((x, i) => [slot(i), `${x.k} ${pct((Math.max(0, x.v) / sum) * 100)}`])) }));
    } else if (r.entity && r.entity !== d0 && r.entity.codes) {
      const g = top(groupBy(r.entity, m, idx), 10, m, true);
      more.push(card(`Top 10 ${r.entity.name}`, null, (n) => barChart(n, g, m), () => ({ head: [r.entity.name, m ? m.name : "Rows"], rows: g.map((x) => [x.k, fmtVal(m, x.v)]), right: [1] })));
    }
    if (row.length) body.append(el("div", { class: "cs-grid cs-g21 cs-mt" }, row));
    if (more.length) body.append(el("div", { class: "cs-grid cs-g2 cs-mt" }, more));
  }
  // a column whose names come back on many rows (customers who buy again, products sold often)
  function repeats(c) { return !!(c && c.codes && c.words.length <= 0.8 * dataset().rows); }
  function rangeOf(c, idx) {
    let lo = Infinity, hi = -Infinity;
    for (const i of idx) { const d = c.values[i]; if (d !== null) { lo = Math.min(lo, d); hi = Math.max(hi, d); } }
    return lo === Infinity ? [null, null] : [lo, hi];
  }
  function median(m, idx) {
    const v = idx.map((i) => m.values[i]).filter((x) => x !== null).sort((a, b) => a - b);
    if (!v.length) return null;
    return v.length % 2 ? v[(v.length - 1) / 2] : (v[v.length / 2 - 1] + v[v.length / 2]) / 2;
  }
  // top n, the rest folded into "Other"; share in % of the whole
  function top(g, n, m, noOther) {
    const sum = g.reduce((a, b) => a + Math.abs(b.v), 0) || 1;
    const out = g.slice(0, n).map((x) => ({ ...x, share: averaged(m) ? undefined : (Math.abs(x.v) / sum) * 100 }));
    if (!noOther && g.length > n && !averaged(m)) {
      const rest = g.slice(n).reduce((a, b) => a + b.v, 0);
      out.push({ k: "Other", v: rest, n: g.length - n, share: (Math.abs(rest) / sum) * 100 });
    }
    return out;
  }
  function zoomTo(k, grain) {
    if (grain === "month") { S.range = "custom"; S.from = Math.floor(Date.UTC(Math.floor(k / 12), k % 12, 1) / DAY); S.to = Math.floor(Date.UTC(Math.floor(k / 12), (k % 12) + 1, 0) / DAY); }
    else if (grain === "week") { S.range = "custom"; S.from = k; S.to = k + 6; }
    else if (grain === "day") { S.range = "custom"; S.from = k; S.to = k; }
    else if (grain === "quarter") { S.range = "custom"; const y = Math.floor(k / 4), q = k % 4; S.from = Math.floor(Date.UTC(y, q * 3, 1) / DAY); S.to = Math.floor(Date.UTC(y, q * 3 + 3, 0) / DAY); }
    else { S.range = "custom"; S.from = Math.floor(Date.UTC(k, 0, 1) / DAY); S.to = Math.floor(Date.UTC(k, 11, 31) / DAY); }
    render();
  }

  // Insights: code finds them; each says what and offers where to look.
  function insights(r, m, idx) {
    const out = [];
    const name = m ? m.name : "Rows";
    const add = (kind, title, text, go, score) => out.push({ score, node: el("button", { type: "button", class: "cs-ins", onclick: go || null }, el("span", { class: "ic " + kind }, kind === "down" ? "▼" : kind === "warn" ? "!" : "▲"), el("div", {}, el("b", {}, title), el("span", {}, text))) });
    const cmp = periodCompare(r, m, idx);
    if (cmp && cmp.change !== null && Number.isFinite(cmp.change)) {
      const up = cmp.change >= 0;
      add(up ? "" : "down", `${name} ${up ? "rose" : "fell"} ${pct(Math.abs(cmp.change))}`, `${cmp.now.label}: ${fmtVal(m, cmp.now.v)}; ${cmp.before.label}: ${fmtVal(m, cmp.before.v)}.`, () => { S.tab = "trends"; render(); }, 90 + Math.min(Math.abs(cmp.change), 50));
      // who drove the change
      for (const d of r.dims.slice(0, 3)) {
        const a = new Map(groupBy(d, m, cmp.rowsNow).map((x) => [x.k, x.v]));
        const b = new Map(groupBy(d, m, cmp.rowsBefore).map((x) => [x.k, x.v]));
        let best = null;
        for (const k of new Set([...a.keys(), ...b.keys()])) {
          const diff = (a.get(k) || 0) - (b.get(k) || 0);
          if (!best || Math.abs(diff) > Math.abs(best.diff)) best = { k, diff };
        }
        const whole = cmp.now.v - cmp.before.v;
        if (best && whole && Math.sign(best.diff) === Math.sign(whole) && Math.abs(best.diff) >= 0.4 * Math.abs(whole)) {
          add(best.diff < 0 ? "down" : "", `${best.k} explains most of it`, `${d.name} ${best.k}: ${best.diff < 0 ? "down" : "up"} ${fmtVal(m, Math.abs(best.diff))} of the ${fmtVal(m, Math.abs(whole))} change.`, () => filterTo(d, best.k), 95);
          break;
        }
      }
    }
    const d0 = r.dims[0];
    if (d0 && !averaged(m)) {
      const g = groupBy(d0, m, idx);
      const sum = g.reduce((a, b) => a + Math.max(0, b.v), 0);
      if (g.length >= 2 && sum > 0) add("", `${g[0].k} leads ${d0.name}`, `${fmtVal(m, g[0].v)}, ${pct((g[0].v / sum) * 100)} of all ${name.toLowerCase()}.`, () => filterTo(d0, g[0].k), 60);
    }
    if (repeats(r.entity) && m && !averaged(m)) {
      const g = groupBy(r.entity, m, idx).filter((x) => x.v > 0);
      const sum = g.reduce((a, b) => a + b.v, 0);
      if (g.length >= 10 && sum > 0) {
        const n = Math.max(1, Math.round(g.length * 0.2));
        const share = (g.slice(0, n).reduce((a, b) => a + b.v, 0) / sum) * 100;
        add(share >= 70 ? "warn" : "", `The top 20% of ${r.entity.name} bring ${pct(share)}`, `${fmtInt(n)} of ${fmtInt(g.length)} ${r.entity.name} values make ${pct(share)} of ${name.toLowerCase()}.`, () => { S.tab = "breakdown"; render(); }, share >= 70 ? 80 : 50);
      }
    }
    if (r.date && m) {
      const pts = series(r.date, m, idx, autoGrain(r.date, idx));
      if (pts.length >= 6) {
        const vals = pts.map((p) => p.v), mean = vals.reduce((a, b) => a + b, 0) / vals.length;
        const sd = Math.sqrt(vals.reduce((a, b) => a + (b - mean) ** 2, 0) / vals.length);
        const best = pts.reduce((a, b) => (b.v > a.v ? b : a));
        if (sd > 0 && (best.v - mean) / sd >= 2) add("", `${best.label} stood out`, `${fmtVal(m, best.v)}, well above the usual ${fmtVal(m, mean)}.`, null, 55);
      }
    }
    for (const c of dataset().columns) {
      const blanks = c.values ? c.values.filter((x) => x === null).length : c.codes.filter((x) => x < 0).length;
      if ((r.measures.includes(c) || c === r.date) && blanks > 0) add("warn", `${c.name} is blank on ${plural(blanks, "row")}`, "Those rows count as nothing in totals. Fill them in the file for exact numbers.", () => { S.tab = "quality"; render(); }, 40);
    }
    if (!out.length) add("", `${plural(idx.length, "row")} in view`, "Add a date or a category column for more to compare.", null, 1);
    return out.sort((a, b) => b.score - a.score).map((x) => x.node);
  }

  // ---------- Trends ----------
  function trends(body, idx, r) {
    const m = measure(r);
    const grain = S.grain || autoGrain(r.date, idx);
    const seg = el("div", { class: "cs-seg", role: "group", "aria-label": "Group by" }, ["day", "week", "month", "quarter", "year"].map((g) => {
      const b = el("button", { type: "button", "aria-pressed": String(g === grain) }, g[0].toUpperCase() + g.slice(1));
      b.addEventListener("click", () => { S.grain = g; render(); });
      return b;
    }));
    const pts = series(r.date, m, idx, grain);
    let run = 0;
    const cum = pts.map((p) => (run += p.v));
    body.append(card(`${m ? m.name : "Rows"} by ${grain}`, "Hover for exact numbers; click a point to zoom in.", (n) => lineChart(n, pts, m, { onPick: (p) => zoomTo(p.k, grain) }),
      () => ({ head: [grain, m ? m.name : "Rows"], rows: pts.map((p) => [p.label, fmtVal(m, p.v)]), right: [1] }), { size: "tall", tools: seg }));
    const grid = [];
    const d0 = r.dims.find((d) => d.words.length >= 2);
    if (d0 && !averaged(m)) {
      const keys = groupBy(d0, m, idx).slice(0, SLOTS).map((g) => g.k);
      const sg = pts.length > 24 && (grain === "day" || grain === "week") ? "month" : grain;
      const split = series(r.date, m, idx, sg, d0).map((p) => {
        const vals = {}; let other = 0;
        for (const [k, v] of Object.entries(p.vals)) { if (keys.includes(k)) vals[k] = v; else other += v; }
        if (other) vals.Other = other;
        return { ...p, vals };
      });
      const all = split.some((p) => p.vals.Other) ? [...keys, "Other"] : keys;
      grid.push(card(`${m ? m.name : "Rows"} by ${sg}, split by ${d0.name}`, "Each colour is one group; the top of a bar is the total.", (n) => stackedBars(n, split, m, all),
        () => ({ head: [sg, ...all], rows: split.map((p) => [p.label, ...all.map((k) => fmtVal(m, p.vals[k] || 0))]), right: all.map((_, i) => i + 1) }),
        { after: legend(all.map((k, i) => [slot(i), k])) }));
    }
    if (!averaged(m)) {
      grid.push(card(`Running total of ${m ? m.name : "rows"}`, null, (n) => lineChart(n, pts.map((p, i) => ({ ...p, v: cum[i] })), m), () => ({ head: [grain, "Running total"], rows: pts.map((p, i) => [p.label, fmtVal(m, cum[i])]), right: [1] })));
    }
    if (grid.length) body.append(el("div", { class: "cs-grid cs-g2 cs-mt" }, grid));
    // weekday by month heat
    const wd = ["Mon", "Tue", "Wed", "Thu", "Fri", "Sat", "Sun"];
    const months = new Map();
    for (const i of idx) {
      const d = r.date.values[i];
      if (d === null) continue;
      const v = m ? m.values[i] : 1;
      if (v === null) continue;
      const mk = bucket(d, "month"), w = (d + 3) % 7;
      if (!months.has(mk)) months.set(mk, new Array(7).fill(0));
      months.get(mk)[w] += v;
    }
    const mk = [...months.keys()].sort((a, b) => a - b).slice(-18);
    if (mk.length >= 2 && !averaged(m)) {
      const data = [];
      let max = 0;
      mk.forEach((k, x) => months.get(k).forEach((v, y) => { data.push([x, y, v]); max = Math.max(max, v); }));
      body.append(el("div", { class: "cs-mt" }, card("Which weekdays are busiest", `${m ? m.name : "Rows"} by weekday, month by month. Darker is more.`, (n) => chart(n, {
        grid: { left: 8, right: 16, top: 8, bottom: 40, containLabel: true },
        xAxis: axis({ type: "category", data: mk.map((k) => label(k, "month")), splitLine: { show: false } }),
        yAxis: axis({ type: "category", data: wd, inverse: true, splitLine: { show: false } }),
        visualMap: heat(max, (v) => fmtVal(m, v, true)),
        tooltip: { trigger: "item", formatter: (p) => `{t|${rt(wd[p.value[1]])}s in ${rt(label(mk[p.value[0]], "month"))}}\n${rt(fmtVal(m, p.value[2]))}` },
        series: [{ type: "heatmap", data, itemStyle: { borderColor: css("--surface"), borderWidth: 2, borderRadius: 3 } }],
      }), () => ({ head: ["Month", ...wd], rows: mk.map((k) => [label(k, "month"), ...months.get(k).map((v) => fmtVal(m, v))]), right: [1, 2, 3, 4, 5, 6, 7] }), { size: "tall" })));
    }
  }

  // ---------- Breakdown ----------
  function breakdown(body, idx, r) {
    const m = measure(r);
    const grid = [];
    for (const d of r.dims) {
      const whole = groupBy(d, m, idx);
      const g = top(whole, 12, m);
      const all = top(whole, whole.length, m, true);
      grid.push(card(`${m ? m.name : "Rows"} by ${d.name}`, `${fmtInt(d.words.length)} kinds. Click a bar to filter.`, (n) => barChart(n, g, m, { onPick: (it) => it.k !== "Other" && filterTo(d, it.k) }),
        () => ({ head: [d.name, m ? m.name : "Rows", "Rows", "Share"], rows: all.map((x) => [x.k, fmtVal(m, x.v), fmtInt(x.n), x.share === undefined ? "" : pct(x.share)]), right: [1, 2, 3] })));
    }
    if (grid.length) body.append(el("div", { class: "cs-grid cs-g2" }, grid));
    if (r.dims.length >= 2) {
      const [a, b] = r.dims;
      const ka = groupBy(a, m, idx).slice(0, 12).map((x) => x.k), kb = groupBy(b, m, idx).slice(0, 12).map((x) => x.k);
      const cell = new Map();
      for (const i of idx) {
        const x = word(a, i), y = word(b, i);
        const v = m ? m.values[i] : 1;
        if (x === null || y === null || v === null) continue;
        const key = x + "\u0000" + y;
        cell.set(key, (cell.get(key) || 0) + v);
      }
      const data = [];
      let max = 0;
      ka.forEach((x, xi) => kb.forEach((y, yi) => { const v = cell.get(x + "\u0000" + y) || 0; data.push([xi, yi, v]); max = Math.max(max, v); }));
      body.append(el("div", { class: "cs-mt" }, card(`${a.name} × ${b.name}`, "Where the two meet. Click a square to filter to both.", (n) => chart(n, {
        grid: { left: 8, right: 16, top: 8, bottom: 40, containLabel: true },
        xAxis: axis({ type: "category", data: ka, splitLine: { show: false }, axisLabel: { interval: 0, rotate: ka.length > 6 ? 30 : 0, width: 90, overflow: "truncate" } }),
        yAxis: axis({ type: "category", data: kb, inverse: true, splitLine: { show: false }, axisLabel: { width: 120, overflow: "truncate" } }),
        visualMap: heat(max, (v) => fmtVal(m, v, true)),
        tooltip: { trigger: "item", formatter: (p) => `{t|${rt(ka[p.value[0]])} · ${rt(kb[p.value[1]])}}\n${rt(fmtVal(m, p.value[2]))}` },
        series: [{ type: "heatmap", data: heatData(data, max), label: { show: ka.length * kb.length <= 48 && n.clientWidth >= 600, formatter: (p) => (p.value[2] ? fmtVal(m, p.value[2], true) : ""), fontSize: 11 }, itemStyle: { borderColor: css("--surface"), borderWidth: 2, borderRadius: 3 } }],
      }, (p) => { S.dims[a.name] = new Set([ka[p.value[0]]]); S.dims[b.name] = new Set([kb[p.value[1]]]); render(); }),
      () => ({ head: [`${a.name} \\ ${b.name}`, ...kb], rows: ka.map((x) => [x, ...kb.map((y) => fmtVal(m, cell.get(x + "\u0000" + y) || 0))]), right: kb.map((_, i) => i + 1) }), { size: "tall" })));
    }
    if (repeats(r.entity) && !averaged(m)) {
      const g = groupBy(r.entity, m, idx).filter((x) => x.v > 0);
      const sum = g.reduce((a, b) => a + b.v, 0);
      if (g.length >= 3 && sum > 0) {
        const head = g.slice(0, 30);
        let run = 0;
        const cum = head.map((x) => (run += x.v) / sum * 100);
        body.append(el("div", { class: "cs-mt" }, card(`Top ${r.entity.name}`, `Bars: each one's share of ${m ? m.name : "rows"}. Line: running share. Same % scale.`, (n) => chart(n, {
          grid: { left: 8, right: 16, top: 16, bottom: 8, containLabel: true },
          xAxis: axis({ type: "category", data: head.map((x) => x.k), splitLine: { show: false }, axisLabel: { interval: 0, rotate: 35, width: 90, overflow: "truncate" } }),
          yAxis: axis({ type: "value", max: 100, axisLabel: { formatter: "{value}%" } }),
          tooltip: { trigger: "axis", formatter: (ps) => `{t|${rt(ps[0].axisValue)}}\n${rt(fmtVal(m, head[ps[0].dataIndex].v))}\n` + ps.map((p) => tipRow(p.seriesIndex ? "k2" : "k1", p.seriesName, pct(p.value))).join("\n") },
          series: [{ name: "Share", type: "bar", barMaxWidth: 18, itemStyle: { color: slot(0), borderRadius: [4, 4, 0, 0] }, data: head.map((x) => (x.v / sum) * 100) },
            { name: "Running share", type: "line", symbol: "circle", symbolSize: 6, lineStyle: { width: 2, color: slot(1) }, itemStyle: { color: slot(1) }, data: cum }],
        }), () => ({ head: [r.entity.name, m ? m.name : "Rows", "Share", "Running share"], rows: head.map((x, i) => [x.k, fmtVal(m, x.v), pct((x.v / sum) * 100), pct(cum[i])]), right: [1, 2, 3] }),
        { size: "tall", after: legend([[slot(0), "Share"], [slot(1), "Running share"]]) })));
      }
    }
  }

  // ---------- Drill-down ----------
  function drill(body, idx, r) {
    const m = measure(r);
    const levels = [...r.dims.slice(0, 2)];
    if (r.entity && r.entity.codes && !levels.includes(r.entity)) levels.push(r.entity);
    const tree = (rows, depth) => {
      if (depth >= levels.length) return [];
      const c = levels[depth];
      const groups = new Map();
      for (const i of rows) { const k = word(c, i) ?? "(blank)"; if (!groups.has(k)) groups.set(k, []); groups.get(k).push(i); }
      return [...groups].map(([k, rs]) => ({ name: k, value: Math.max(0, total(m, rs)), children: tree(rs, depth + 1) })).filter((n) => n.value > 0).sort((a, b) => b.value - a.value).slice(0, 40);
    };
    const data = tree(idx, 0).map((n, i) => ({ ...n, itemStyle: { color: slot(Math.min(i, SLOTS)) } }));
    body.append(card(`${m ? m.name : "Rows"}: ${levels.map((l) => l.name).join(" › ")}`, "Click a block to go one level down; use the path above the chart to go back up.", (n) => chart(n, {
      tooltip: { trigger: "item", formatter: (p) => `{t|${rt((p.treePathInfo || []).slice(1).map((t) => t.name).join(" › "))}}\n${rt(fmtVal(m, p.value))}` },
      series: [{ type: "treemap", name: "All", data, roam: false, nodeClick: "zoomToNode", leafDepth: 1, visibleMin: 300,
        breadcrumb: { show: true, top: 4, left: 4, itemStyle: { color: css("--sunk"), borderColor: css("--hair"), textStyle: { color: css("--ink") } } },
        top: 36, left: 4, right: 4, bottom: 4,
        label: { show: true, formatter: (p) => `${p.name}\n${fmtVal(m, p.value, true)}`, color: "#fff", fontSize: 12, lineHeight: 16 },
        upperLabel: { show: false }, itemStyle: { borderColor: css("--surface"), borderWidth: 2, gapWidth: 2, borderRadius: 4 },
        levels: [{ itemStyle: { gapWidth: 3 } }, { colorSaturation: [0.35, 0.6], itemStyle: { gapWidth: 2 } }, { colorSaturation: [0.35, 0.6] }] }],
    }), () => ({ head: [...levels.map((l) => l.name), m ? m.name : "Rows"], rows: flatten(data, []), right: [levels.length] }), { size: "tall" }));
    function flatten(nodes, path) {
      const out = [];
      for (const n of nodes) {
        if (n.children && n.children.length) out.push(...flatten(n.children, [...path, n.name]));
        else out.push([...path, n.name, ...new Array(levels.length - path.length - 1).fill(""), fmtVal(m, n.value)]);
      }
      return out.slice(0, 500);
    }
  }

  // ---------- Spread ----------
  function spread(body, idx, r) {
    const grid = [];
    for (const m of r.measures.slice(0, 4)) {
      const v = idx.map((i) => m.values[i]).filter((x) => x !== null).sort((a, b) => a - b);
      if (v.length < 2) continue;
      const lo = v[0], hi = v[v.length - 1];
      const n = Math.min(20, Math.max(5, Math.round(Math.sqrt(v.length))));
      const step = (hi - lo) / n || 1;
      const bins = new Array(n).fill(0);
      for (const x of v) bins[Math.min(n - 1, Math.floor((x - lo) / step))]++;
      const labels = bins.map((_, i) => `${fmtVal(m, lo + i * step, true)}–${fmtVal(m, lo + (i + 1) * step, true)}`);
      const q = (p) => v[Math.min(v.length - 1, Math.floor(p * (v.length - 1)))];
      grid.push(card(`How ${m.name} is spread`, `Half the rows are between ${fmtVal(m, q(0.25), true)} and ${fmtVal(m, q(0.75), true)}; the middle one is ${fmtVal(m, q(0.5), true)}.`, (node) => chart(node, {
        grid: { left: 8, right: 16, top: 16, bottom: 8, containLabel: true },
        xAxis: axis({ type: "category", data: labels, splitLine: { show: false }, axisLabel: { rotate: 30, fontSize: 10.5 } }),
        yAxis: axis({ type: "value", minInterval: 1 }),
        tooltip: { trigger: "item", formatter: (p) => `{t|${rt(p.name)}}\n${rt(plural(p.value, "row"))}` },
        series: [{ type: "bar", data: bins, barCategoryGap: "8%", itemStyle: { color: slot(0), borderRadius: [4, 4, 0, 0] } }],
      }), () => ({ head: [m.name, "Rows"], rows: labels.map((l, i) => [l, fmtInt(bins[i])]), right: [1] })));
    }
    if (grid.length) body.append(el("div", { class: "cs-grid cs-g2" }, grid));
    if (r.measures.length >= 2) {
      const [a, b] = r.measures;
      const d = r.dims.find((c) => c.words.length <= 3) || null;
      const keys = d ? d.words.slice(0, 3) : [""];
      const pts = keys.map(() => []);
      for (const i of idx.slice(0, 5000)) {
        const x = a.values[i], y = b.values[i];
        if (x === null || y === null) continue;
        const k = d ? keys.indexOf(word(d, i)) : 0;
        if (k >= 0) pts[k].push([x, y, i]);
      }
      body.append(el("div", { class: "cs-mt" }, card(`${a.name} against ${b.name}`, "Each dot is a row." + (idx.length > 5000 ? " The first 5,000 rows are shown." : ""), (n) => chart(n, {
        grid: { left: 8, right: 20, top: 16, bottom: 8, containLabel: true },
        xAxis: axis({ type: "value", name: a.name, nameLocation: "middle", nameGap: 26, axisLabel: { formatter: (v) => fmtVal(a, v, true) } }),
        yAxis: axis({ type: "value", axisLabel: { formatter: (v) => fmtVal(b, v, true) } }),
        tooltip: { trigger: "item", formatter: (p) => `{t|${rt(p.seriesName || "Row")}}\n${rt(a.name)}: ${rt(fmtVal(a, p.value[0]))}\n${rt(b.name)}: ${rt(fmtVal(b, p.value[1]))}` },
        series: keys.map((k, j) => ({ name: k, type: "scatter", symbolSize: 9, data: pts[j], itemStyle: { color: slot(j), opacity: 0.75, borderColor: css("--surface"), borderWidth: 2 } })),
      }), () => ({ head: [a.name, b.name, d ? d.name : ""], rows: pts.flatMap((p, j) => p.slice(0, 300).map((x) => [fmtVal(a, x[0]), fmtVal(b, x[1]), keys[j]])), right: [0, 1] }),
      { size: "tall", after: d ? legend(keys.map((k, j) => [slot(j), k])) : null })));
    }
  }

  // ---------- Data quality ----------
  function quality(body, idx) {
    const ds = dataset();
    const prof = S.view.profile.datasets.find((d) => d.name === ds.name);
    const rows = ds.columns.map((c) => {
      const p = prof && prof.columns.find((x) => x.name === c.name);
      const blank = c.values ? idx.filter((i) => c.values[i] === null).length : idx.filter((i) => c.codes[i] < 0).length;
      const fill = idx.length ? ((idx.length - blank) / idx.length) * 100 : 0;
      return { c, p, blank, fill };
    });
    const avg = rows.reduce((a, b) => a + b.fill, 0) / (rows.length || 1);
    const seen = new Set();
    let dup = 0;
    for (const i of idx) {
      const key = ds.columns.map((c) => (c.values ? c.values[i] : c.codes[i])).join("\u0001");
      if (seen.has(key)) dup++; else seen.add(key);
    }
    body.append(el("div", { class: "cs-grid cs-k" },
      el("div", { class: "cs-tile" }, el("div", { class: "lab" }, "Cells filled"), el("div", { class: "val" }, pct(avg)), el("div", { class: "foot" }, `across ${plural(rows.length, "column")}`)),
      el("div", { class: "cs-tile" }, el("div", { class: "lab" }, "Rows"), el("div", { class: "val" }, fmtInt(idx.length)), el("div", { class: "foot" }, prof ? `from ${prof.origins.join(", ")}` : "")),
      el("div", { class: "cs-tile" }, el("div", { class: "lab" }, "Exact duplicate rows"), el("div", { class: "val" }, fmtInt(dup)), el("div", { class: "foot" }, dup ? "Same values in every column: check they are real." : "None found.")),
      el("div", { class: "cs-tile" }, el("div", { class: "lab" }, "Unreadable values"), el("div", { class: "val" }, fmtInt(rows.reduce((a, b) => a + ((b.p && b.p.unreadable) || 0), 0))), el("div", { class: "foot" }, "count as blank"))));
    const t = el("table", { class: "cs-table" }, el("thead", {}, el("tr", {}, ["Column", "Reads as", "Filled", "", "Blank", "Notes"].map((h, j) => el("th", { class: j === 2 || j === 4 ? "r" : null }, h)))),
      el("tbody", {}, rows.map(({ c, p, blank, fill }) => el("tr", {}, el("td", {}, el("b", {}, c.name)), el("td", {}, KIND[c.kind] || c.kind), el("td", { class: "r" }, pct(fill)),
        el("td", {}, el("div", { class: "cs-bar-cell" }, el("i", { style: { width: `${Math.max(2, fill)}%`, background: fill >= 95 ? "var(--c1)" : fill >= 70 ? "var(--c5)" : "var(--down)" } }))),
        el("td", { class: "r" }, fmtInt(blank)),
        el("td", { class: "cs-small cs-muted" }, [p && p.unreadable ? `${fmtInt(p.unreadable)} unreadable` : null, p && p.merged ? `${p.merged} spellings merged` : null].filter(Boolean).join(" · "))))));
    body.append(el("section", { class: "cs-card2 cs-mt" }, el("header", {}, el("div", {}, el("h3", {}, "Column by column"), el("p", { class: "sub" }, "For the rows in view. Green is well filled; red needs attention."))), el("div", { class: "cs-tw" }, t)));
    if (prof && prof.notes.length) body.append(el("section", { class: "cs-card2 cs-mt" }, el("header", {}, el("h3", {}, "What was cleaned when reading")), el("ul", { class: "cs-notes" }, prof.notes.map((n) => el("li", {}, n)))));
  }

  // ---------- All rows ----------
  function allRows(body, idx) {
    const ds = dataset();
    const cols = ds.columns;
    const q = el("input", { type: "search", placeholder: "Search every column", value: S.q, "aria-label": "Search rows" });
    let rows = idx;
    if (S.q) {
      const needle = S.q.toLowerCase();
      rows = idx.filter((i) => cols.some((c) => String(cell(c, i) ?? "").toLowerCase().includes(needle)));
    }
    if (S.sortTable) {
      const c = col(S.sortTable.col);
      if (c) {
        const key = (i) => (c.values ? c.values[i] : word(c, i));
        rows = [...rows].sort((a, b) => { const x = key(a), y = key(b); if (x === null) return 1; if (y === null) return -1; return (x < y ? -1 : x > y ? 1 : 0) * S.sortTable.dir; });
      }
    }
    const per = 50, pages = Math.max(1, Math.ceil(rows.length / per));
    S.page = Math.min(S.page, pages - 1);
    const view = rows.slice(S.page * per, S.page * per + per);
    const right = (c) => ["number", "money", "percent"].includes(c.kind);
    const t = el("table", { class: "cs-table" },
      el("thead", {}, el("tr", {}, cols.map((c) => { const th = el("th", { class: "s" + (right(c) ? " r" : "") }, c.name + (S.sortTable && S.sortTable.col === c.name ? (S.sortTable.dir > 0 ? " ↑" : " ↓") : ""));
        th.addEventListener("click", () => { S.sortTable = { col: c.name, dir: S.sortTable && S.sortTable.col === c.name ? -S.sortTable.dir : 1 }; render(); }); return th; }))),
      el("tbody", {}, view.map((i) => el("tr", {}, cols.map((c) => el("td", { class: right(c) ? "r" : null }, show(c, i)))))));
    let timer;
    q.addEventListener("input", () => { clearTimeout(timer); timer = setTimeout(() => { S.q = q.value; S.page = 0; render(); const n = host().querySelector('input[type="search"]'); if (n) { n.focus(); n.setSelectionRange(n.value.length, n.value.length); } }, 250); });
    const nav = el("div", { class: "cs-row cs-mt" }, el("span", { class: "cs-muted cs-small cs-grow" }, `${fmtInt(rows.length)} rows${rows.length ? `, showing ${fmtInt(S.page * per + 1)} to ${fmtInt(S.page * per + view.length)}` : ""}`),
      el("button", { type: "button", class: "cs-mini", disabled: S.page === 0, onclick: () => { S.page--; render(); } }, "Previous"),
      el("button", { type: "button", class: "cs-mini", disabled: S.page >= pages - 1, onclick: () => { S.page++; render(); } }, "Next"),
      el("button", { type: "button", class: "cs-mini", onclick: () => downloadCSV(rows) }, "Download these rows (CSV)"));
    body.append(el("section", { class: "cs-card2" }, el("div", { class: "cs-row" }, q), el("div", { class: "cs-tw cs-mt" }, t), nav));
  }
  function cell(c, i) {
    if (c.codes) return word(c, i);
    const v = c.values[i];
    if (v === null) return null;
    return c.kind === "date" ? isoDay(v) : v;
  }
  function show(c, i) {
    const v = cell(c, i);
    if (v === null) return "";
    if (c.kind === "date") return fmtDate(c.values[i]);
    if (numeric(c)) return fmtVal(c, v);
    return String(v);
  }
  function downloadCSV(rows) {
    const cols = dataset().columns;
    // Words that a spreadsheet would run as a formula get a ' in front; numbers stay numbers.
    const q = (s, words) => {
      s = s === null || s === undefined ? "" : String(s);
      if (words && /^[=+\-@\t\r]/.test(s)) s = "'" + s;
      return /[",\r\n]/.test(s) || /^'/.test(s) ? `"${s.replace(/"/g, '""')}"` : s;
    };
    const text = [cols.map((c) => q(c.name, true)).join(","), ...rows.map((i) => cols.map((c) => q(cell(c, i), !!c.codes)).join(","))].join("\n");
    const a = el("a", { href: URL.createObjectURL(new Blob(["﻿" + text], { type: "text/csv" })), download: `${S.view.sheet.title}.csv` });
    document.body.append(a); a.click(); a.remove();
  }

  // ---------- packs: expert tabs for data MangoMan knows ----------
  function find(re, kinds) { return dataset().columns.find((c) => re.test(c.name) && (!kinds || kinds.includes(c.kind))) || null; }
  function packs() {
    const out = [];
    const f = feedCols();
    if (f) out.push({ id: "feed", name: "Feed health", draw: (b, idx) => feed(b, idx, f) });
    const s = salesCols();
    if (s) out.push({ id: "customers", name: "Customers", draw: (b, idx) => customers(b, idx, s) });
    const a = adsCols();
    if (a) out.push({ id: "campaigns", name: "Campaigns", draw: (b, idx) => campaigns(b, idx, a) });
    return out;
  }

  // Product feed (Google Shopping, Meta catalogue)
  function feedCols() {
    const avail = find(/^availability$/i);
    const gtin = find(/^gtin$|^ean$|^upc$|barcode/i);
    const img = find(/additional.?image/i);
    const type = find(/^product.?type$|google.?product.?category/i);
    if (!avail || !(gtin || img || type)) return null;
    return { avail, gtin, img, type, ide: find(/identifier.?exists/i), ship: find(/^shipping/i), color: find(/^colou?r$/i), size: find(/^size$/i),
      gender: find(/^gender$/i), age: find(/^age.?group$/i), title: find(/^title$/i), price: find(/^price$/i, ["money", "number"]), sale: find(/^sale.?price$/i, ["money", "number"]) };
  }
  function feedChecks(f) {
    const apparel = (i) => f.type && /apparel|clothing|shirt|hoodie|dress|shoe|jacket|wear|hat|cap/i.test(cell(f.type, i) || "");
    const has = (c, i) => c && cell(c, i) !== null && cell(c, i) !== "";
    const out = [];
    if (f.gtin) out.push({ key: "gtin", label: "Product code (GTIN)", fix: "Add the GTIN, or set identifier exists to no for products without one (gift cards, own-brand items).", ok: (i) => has(f.gtin, i) || (f.ide && /^no$/i.test(cell(f.ide, i) || "")) });
    if (f.img) out.push({ key: "img", label: "Extra images", fix: "Add at least one more photo: back, detail or in use.", ok: (i) => has(f.img, i) });
    if (f.ship) out.push({ key: "ship", label: "Shipping in the feed", fix: "Add a shipping price, or confirm shipping is set for the whole account in Merchant Center.", ok: (i) => /\d/.test(cell(f.ship, i) || "") });
    if (f.title) out.push({ key: "title", label: "Title within 150 characters", fix: "Google Shopping shows up to 150 characters: shorten the title.", ok: (i) => (cell(f.title, i) || "").length <= 150 });
    if (f.price && f.sale) out.push({ key: "sale", label: "Sale price below price", fix: "A sale price must be lower than the price, or left blank.", ok: (i) => f.sale.values[i] === null || f.sale.values[i] < f.price.values[i] });
    for (const [key, c, label] of [["color", f.color, "Colour"], ["size", f.size, "Size"], ["gender", f.gender, "Gender"], ["age", f.age, "Age group"]]) {
      if (c && f.type) out.push({ key, label: `${label} (clothing)`, fix: `Clothing needs ${label.toLowerCase()} for Google Shopping.`, applies: apparel, ok: (i) => has(c, i) });
    }
    return out;
  }
  function feed(body, idx, f) {
    const checks = feedChecks(f);
    const inStock = (i) => /^in.?stock$/i.test(cell(f.avail, i) || "");
    let pass = 0, all = 0, clean = 0;
    const res = checks.map((c) => ({ ...c, rows: [], live: 0, n: 0 }));
    for (const i of idx) {
      let ok = true;
      for (const c of res) {
        if (c.applies && !c.applies(i)) continue;
        c.n++; all++;
        if (c.ok(i)) pass++; else { ok = false; c.rows.push(i); if (inStock(i)) c.live++; }
      }
      if (ok) clean++;
    }
    const health = all ? (pass / all) * 100 : 100;
    const live = idx.filter(inStock).length;
    body.append(el("div", { class: "cs-grid cs-k" },
      el("div", { class: "cs-tile" }, el("div", { class: "lab" }, "Feed health"), el("div", { class: "val" }, pct(health)), el("div", { class: "foot" }, `${fmtInt(pass)} of ${fmtInt(all)} checks pass`)),
      el("div", { class: "cs-tile" }, el("div", { class: "lab" }, "Products with nothing to fix"), el("div", { class: "val" }, fmtInt(clean)), el("div", { class: "foot" }, `of ${plural(idx.length, "product")}`)),
      el("div", { class: "cs-tile" }, el("div", { class: "lab" }, "In stock"), el("div", { class: "val" }, fmtInt(live)), el("div", { class: "foot" }, `${fmtInt(idx.length - live)} out of stock cannot show in ads`))));
    const sorted = [...res].sort((a, b) => b.live - a.live || b.rows.length - a.rows.length);
    const t = el("table", { class: "cs-table" }, el("thead", {}, el("tr", {}, ["Check", "Missing", "In stock", "Filled", ""].map((h, j) => el("th", { class: j === 3 || j === 4 ? "cs-wide" : j ? "r" : null }, h)))),
      el("tbody", {}, sorted.map((c) => {
        const fill = c.n ? ((c.n - c.rows.length) / c.n) * 100 : 100;
        const tr = el("tr", { class: c.rows.length ? "click" : null }, el("td", {}, el("b", {}, c.label), el("div", { class: "cs-small cs-muted" }, c.rows.length ? c.fix : "All filled.")),
          el("td", { class: "r" }, `${fmtInt(c.rows.length)} / ${fmtInt(c.n)}`), el("td", { class: "r" }, el("b", {}, fmtInt(c.live))),
          el("td", { class: "cs-wide" }, el("div", { class: "cs-bar-cell" }, el("i", { style: { width: `${Math.max(2, fill)}%`, background: fill >= 95 ? "var(--c1)" : fill >= 70 ? "var(--c5)" : "var(--down)" } }), pct(fill))),
          el("td", { class: "cs-wide" }, c.rows.length ? el("span", { class: "cs-mini" }, "See products") : ""));
        if (c.rows.length) tr.addEventListener("click", () => showRows(`Missing: ${c.label}`, c.rows));
        return tr;
      })));
    body.append(el("section", { class: "cs-card2 cs-mt" }, el("header", {}, el("div", {}, el("h3", {}, "What to fix first"), el("p", { class: "sub" }, "Ranked by in-stock products affected: those are the ones that can sell today. Only clothing is checked for colour, size, gender and age."))), el("div", { class: "cs-tw" }, t)));
    if (f.type) {
      const types = groupBy(f.type, null, idx).slice(0, 12).map((g) => g.k);
      const data = [];
      types.forEach((ty, y) => res.forEach((c, x) => {
        const rows = idx.filter((i) => cell(f.type, i) === ty && (!c.applies || c.applies(i)));
        data.push([x, y, rows.length ? Math.round((rows.filter((i) => c.ok(i)).length / rows.length) * 100) : "-"]);
      }));
      body.append(el("div", { class: "cs-mt" }, card("Product type × check", "Share filled. Darker green is better; click a square to see the products missing it.", (n) => chart(n, {
        grid: { left: 8, right: 16, top: 8, bottom: 40, containLabel: true },
        xAxis: axis({ type: "category", data: res.map((c) => c.label.replace(" (clothing)", "")), splitLine: { show: false }, axisLabel: { interval: 0, rotate: n.clientWidth < 600 ? 60 : 25, width: 100, overflow: "truncate" } }),
        yAxis: axis({ type: "category", data: types, inverse: true, splitLine: { show: false }, axisLabel: { width: n.clientWidth < 600 ? 80 : 150, overflow: "truncate" } }),
        visualMap: heat(100, "{value}%"),
        tooltip: { trigger: "item", formatter: (p) => `{t|${rt(types[p.value[1]])}}\n${rt(res[p.value[0]].label)}: ${p.value[2] === "-" ? "does not apply" : rt(p.value[2] + "% filled")}` },
        series: [{ type: "heatmap", data: heatData(data, 100), label: { show: n.clientWidth >= 600, formatter: (p) => (p.value[2] === "-" ? "" : p.value[2] + "%"), fontSize: 11 }, itemStyle: { borderColor: css("--surface"), borderWidth: 2, borderRadius: 3 } }],
      }, (p) => { const c = res[p.value[0]]; const ty = types[p.value[1]]; const rows = c.rows.filter((i) => cell(f.type, i) === ty); if (rows.length) showRows(`${ty}: missing ${c.label}`, rows); }),
      () => ({ head: ["Type", ...res.map((c) => c.label)], rows: types.map((ty, y) => [ty, ...res.map((_, x) => { const v = data.find((d) => d[0] === x && d[1] === y)[2]; return v === "-" ? "n/a" : v + "%"; })]) }), { size: "tall" })));
    }
  }
  function showRows(title, rows) {
    const cols = dataset().columns.slice(0, 8);
    const box = el("section", { class: "cs-card2 cs-mt" }, el("header", {}, el("div", {}, el("h3", {}, title), el("p", { class: "sub" }, plural(rows.length, "row"))),
      el("div", { class: "cs-row" }, el("button", { type: "button", class: "cs-mini", onclick: () => downloadCSV(rows) }, "Download CSV"), el("button", { type: "button", class: "cs-mini", onclick: () => box.remove() }, "Close"))),
      el("div", { class: "cs-tw cs-scroll" }, simpleTable({ head: cols.map((c) => c.name), rows: rows.slice(0, 300).map((i) => cols.map((c) => show(c, i))) })));
    $("cs-body").prepend(box);
    box.scrollIntoView({ behavior: "smooth", block: "start" });
  }

  // Sales and orders: customers who come back
  function salesCols() {
    const r = roles();
    const money = r.measures.find((m) => m.kind === "money");
    const cust = find(/customer|client|buyer|email|phone|mobile/i, ["id", "text", "category", "email"]);
    // Only when names repeat: a different name on every row has no returning customers to show.
    if (!r.date || !money || !cust || cust.words.length > 0.8 * dataset().rows) return null;
    return { date: r.date, money, cust };
  }
  function customers(body, idx, s) {
    const per = new Map();
    for (const i of idx) {
      const k = cell(s.cust, i), d = s.date.values[i], v = s.money.values[i];
      if (k === null || d === null) continue;
      let e = per.get(k);
      if (!e) per.set(k, (e = { k, n: 0, v: 0, first: d, last: d }));
      e.n++; e.v += v || 0; e.first = Math.min(e.first, d); e.last = Math.max(e.last, d);
    }
    const list = [...per.values()].sort((a, b) => b.v - a.v);
    const repeat = list.filter((c) => c.n > 1).length;
    const sum = list.reduce((a, b) => a + b.v, 0);
    const orders = list.reduce((a, b) => a + b.n, 0);
    body.append(el("div", { class: "cs-grid cs-k" },
      el("div", { class: "cs-tile" }, el("div", { class: "lab" }, "Customers"), el("div", { class: "val" }, fmtInt(list.length)), el("div", { class: "foot" }, `${(orders / (list.length || 1)).toFixed(1)} rows each on average`)),
      el("div", { class: "cs-tile" }, el("div", { class: "lab" }, "Came back"), el("div", { class: "val" }, pct((repeat / (list.length || 1)) * 100)), el("div", { class: "foot" }, `${fmtInt(repeat)} bought more than once`)),
      el("div", { class: "cs-tile" }, el("div", { class: "lab" }, `Average ${s.money.name} per row`), el("div", { class: "val" }, fmtVal(s.money, sum / (orders || 1), true)), el("div", { class: "foot" }, `per customer ${fmtVal(s.money, sum / (list.length || 1), true)}`))));
    const top10 = list.slice(0, 10).map((c) => ({ k: c.k, v: c.v, n: c.n, share: (c.v / (sum || 1)) * 100 }));
    // new and returning customers per month
    const months = new Map();
    const seenFirst = new Map(list.map((c) => [c.k, bucket(c.first, "month")]));
    const active = new Map();
    for (const i of idx) {
      const k = cell(s.cust, i), d = s.date.values[i];
      if (k === null || d === null) continue;
      const mk = bucket(d, "month");
      if (!active.has(mk)) active.set(mk, new Map());
      active.get(mk).set(k, true);
    }
    const mks = [...active.keys()].sort((a, b) => a - b);
    for (const mk of mks) { let fresh = 0, back = 0; for (const k of active.get(mk).keys()) (seenFirst.get(k) === mk ? fresh++ : back++); months.set(mk, [fresh, back]); }
    body.append(el("div", { class: "cs-grid cs-g2 cs-mt" },
      card(`Top 10 customers by ${s.money.name}`, `${s.cust.name}. Hover for their share.`, (n) => barChart(n, top10, s.money), () => ({ head: [s.cust.name, s.money.name, "Rows", "Share"], rows: top10.map((c) => [c.k, fmtVal(s.money, c.v), fmtInt(c.n), pct(c.share)]), right: [1, 2, 3] })),
      card("New and returning customers", "Each month: customers buying for the first time, and those who bought before.", (n) => chart(n, {
        grid: { left: 8, right: 16, top: 16, bottom: 8, containLabel: true },
        xAxis: axis({ type: "category", data: mks.map((k) => label(k, "month")), splitLine: { show: false } }),
        yAxis: axis({ type: "value", minInterval: 1 }),
        tooltip: { trigger: "axis", formatter: (ps) => `{t|${rt(ps[0].axisValue)}}\n` + ps.map((p) => tipRow("k" + (p.seriesIndex + 1), p.seriesName, fmtInt(p.value))).join("\n") },
        series: [["New", 0], ["Returning", 1]].map(([name, j]) => ({ name, type: "bar", stack: "c", barMaxWidth: 28, itemStyle: { color: slot(j), borderColor: css("--surface"), borderWidth: 1, borderRadius: j ? [4, 4, 0, 0] : 0 }, data: mks.map((k) => months.get(k)[j]) })),
      }), () => ({ head: ["Month", "New", "Returning"], rows: mks.map((k) => [label(k, "month"), ...months.get(k).map(fmtInt)]), right: [1, 2] }), { after: legend([[slot(0), "New"], [slot(1), "Returning"]]) })));
  }

  // Ads: where the money goes and what it brings
  function adsCols() {
    const ds = dataset();
    const spend = ds.columns.filter((c) => numeric(c) && /spend|cost|amount spent/i.test(c.name) && !/per|cpc|cpa|cpm/i.test(c.name));
    const conv = ds.columns.filter((c) => numeric(c) && /conv|purchase|results|leads|sales/i.test(c.name) && !/rate|value|cost|per/i.test(c.name));
    const clicks = ds.columns.filter((c) => numeric(c) && /click/i.test(c.name) && !/rate|cost|per|ctr/i.test(c.name));
    const name = find(/campaign|ad.?group|ad name|keyword/i, ["category", "text", "id"]) || roles().entity;
    if (!spend.length || !conv.length || !name || !name.codes) return null;
    return { spend, conv, clicks, name };
  }
  function campaigns(body, idx, a) {
    const sum = (cols, i) => cols.reduce((s, c) => s + (c.values[i] || 0), 0);
    const by = new Map();
    for (const i of idx) {
      const k = word(a.name, i);
      if (k === null) continue;
      const e = by.get(k) || { k, spend: 0, conv: 0, clicks: 0 };
      e.spend += sum(a.spend, i); e.conv += sum(a.conv, i); e.clicks += sum(a.clicks, i);
      by.set(k, e);
    }
    const list = [...by.values()].sort((x, y) => y.spend - x.spend);
    const tSpend = list.reduce((s, x) => s + x.spend, 0), tConv = list.reduce((s, x) => s + x.conv, 0);
    const unit = a.spend[0].unit;
    const m = { kind: "money", unit };
    const zero = list.filter((x) => x.conv === 0 && x.spend > 0);
    body.append(el("div", { class: "cs-grid cs-k" },
      el("div", { class: "cs-tile" }, el("div", { class: "lab" }, "Spend"), el("div", { class: "val" }, money(tSpend, unit, true)), el("div", { class: "foot" }, a.spend.length > 1 ? `${a.spend.length} spend columns added up` : a.spend[0].name)),
      el("div", { class: "cs-tile" }, el("div", { class: "lab" }, "Conversions"), el("div", { class: "val" }, fmtInt(tConv)), el("div", { class: "foot" }, a.conv.length > 1 ? `${a.conv.length} columns added up` : a.conv[0].name)),
      el("div", { class: "cs-tile" }, el("div", { class: "lab" }, "Cost per conversion"), el("div", { class: "val" }, tConv ? money(tSpend / tConv, unit) : "-"), el("div", { class: "foot" }, "all campaigns")),
      el("div", { class: "cs-tile" }, el("div", { class: "lab" }, "Spend with no conversions"), el("div", { class: "val" }, money(zero.reduce((s, x) => s + x.spend, 0), unit, true)), el("div", { class: "foot" }, zero.length ? plural(zero.length, a.name.name.toLowerCase().replace(/s$/, "")) : "none"))));
    const rows = list.map((x) => ({ k: x.k, v: x.conv ? x.spend / x.conv : null, spend: x.spend, conv: x.conv, clicks: x.clicks }));
    const cpa = rows.filter((x) => x.v !== null).sort((p, q) => p.v - q.v);
    const avg = tConv ? tSpend / tConv : null;
    body.append(el("div", { class: "cs-grid cs-g2 cs-mt" },
      card("Cost per conversion", avg ? `Lower is better. Green: below the average of ${money(avg, unit)}.` : null, (n) => barChart(n, cpa, m, { colorOf: (it) => (avg && it.v <= avg ? slot(0) : slot(2)) }),
        () => ({ head: [a.name.name, "Cost per conversion"], rows: cpa.map((x) => [x.k, money(x.v, unit)]), right: [1] }), { after: avg ? legend([[slot(0), "Below average"], [slot(2), "Above average"]]) : null }),
      card("Share of spend and of conversions", "When the spend bar is much longer than the conversions bar, money buys little there.", (n) => chart(n, {
        grid: { left: 8, right: 30, top: 8, bottom: 8, containLabel: true },
        yAxis: axis({ type: "category", data: list.slice(0, 12).map((x) => x.k), inverse: true, splitLine: { show: false }, axisLabel: { width: 130, overflow: "truncate" } }),
        xAxis: axis({ type: "value", axisLabel: { formatter: "{value}%" } }),
        tooltip: { trigger: "axis", formatter: (ps) => `{t|${rt(ps[0].axisValue)}}\n` + ps.map((p) => tipRow("k" + (p.seriesIndex ? 2 : 1), p.seriesName, pct(p.value))).join("\n") },
        series: [["Spend", 0, (x) => (x.spend / (tSpend || 1)) * 100], ["Conversions", 1, (x) => (x.conv / (tConv || 1)) * 100]].map(([name, j, f]) => ({ name, type: "bar", barMaxWidth: 12, barGap: "20%", itemStyle: { color: slot(j), borderRadius: [0, 4, 4, 0] }, data: list.slice(0, 12).map(f) })),
      }), () => ({ head: [a.name.name, "Spend share", "Conversion share"], rows: list.map((x) => [x.k, pct((x.spend / (tSpend || 1)) * 100), pct((x.conv / (tConv || 1)) * 100)]), right: [1, 2] }), { after: legend([[slot(0), "Spend"], [slot(1), "Conversions"]]) })));
    if (zero.length) body.append(el("section", { class: "cs-card2 cs-mt" }, el("header", {}, el("div", {}, el("h3", {}, "Spending with nothing to show"), el("p", { class: "sub" }, "No conversions at all in the rows in view. Pause or fix these first."))),
      simpleTable({ head: [a.name.name, "Spend", "Clicks"], rows: zero.map((x) => [x.k, money(x.spend, unit), fmtInt(x.clicks)]), right: [1, 2] })));
  }

  // ---------- readout ----------
  function facts(r, idx) {
    const out = [];
    const ds = dataset();
    const m = r.measures[0] || null;
    const dates = r.date ? rangeOf(r.date, idx) : [null, null];
    out.push(`The data: ${S.view.sheet.title}${ds.name !== S.view.sheet.title ? `, table ${ds.name}` : ""}, ${fmtInt(idx.length)} rows${dates[0] !== null ? `, dated ${fmtDate(dates[0])} to ${fmtDate(dates[1])}` : ""}.`);
    if (S.range !== "all" || Object.keys(S.dims).length) out.push(`Filters in use: ${[S.range !== "all" ? "a date range" : null, ...Object.entries(S.dims).map(([k, v]) => `${k} = ${[...v].join(" or ")}`)].filter(Boolean).join("; ")}.`);
    for (const mm of r.measures.slice(0, 3)) out.push(averaged(mm) ? `Average ${mm.name}: ${fmtVal(mm, total(mm, idx))}.` : `Total ${mm.name}: ${fmtVal(mm, total(mm, idx))}; average per row ${fmtVal(mm, total(mm, idx) / (idx.length || 1))}.`);
    if (r.date && m) {
      const g = autoGrain(r.date, idx);
      const pts = series(r.date, m, idx, g === "day" && idx.length > 60 ? "week" : g);
      out.push(`${m.name} by ${g}: ` + pts.slice(-12).map((p) => `${p.label} ${fmtVal(m, p.v)}`).join("; ") + ".");
      const cmp = periodCompare(r, m, idx);
      if (cmp && cmp.change !== null) out.push(`${cmp.label}: ${m.name} ${cmp.change >= 0 ? "up" : "down"} ${pct(Math.abs(cmp.change))} (${fmtVal(m, cmp.now.v)} against ${fmtVal(m, cmp.before.v)}).`);
    }
    for (const d of r.dims.slice(0, 3)) {
      const g = groupBy(d, m, idx);
      const sum = g.reduce((a, b) => a + Math.abs(b.v), 0) || 1;
      out.push(`${m ? m.name : "Rows"} by ${d.name}: ` + g.slice(0, 8).map((x) => `${x.k} ${fmtVal(m, x.v)} (${pct((Math.abs(x.v) / sum) * 100)})`).join("; ") + (g.length > 8 ? `; ${g.length - 8} more` : "") + ".");
    }
    if (r.entity && r.entity.codes && m) {
      const g = groupBy(r.entity, m, idx);
      out.push(`Top ${r.entity.name} by ${m.name}: ` + g.slice(0, 5).map((x) => `${x.k} ${fmtVal(m, x.v)}`).join("; ") + `; ${fmtInt(g.length)} in all.`);
    }
    for (const c of ds.columns) {
      const blanks = c.values ? idx.filter((i) => c.values[i] === null).length : idx.filter((i) => c.codes[i] < 0).length;
      if (blanks && (r.measures.includes(c) || r.dims.includes(c) || c === r.date)) out.push(`${c.name} is blank on ${fmtInt(blanks)} of ${fmtInt(idx.length)} rows.`);
    }
    const f = feedCols();
    if (f) {
      const inStock = idx.filter((i) => /^in.?stock$/i.test(cell(f.avail, i) || "")).length;
      out.push(`Product feed: ${fmtInt(inStock)} of ${fmtInt(idx.length)} products in stock.`);
      for (const c of feedChecks(f)) {
        const rows = idx.filter((i) => !c.applies || c.applies(i));
        const miss = rows.filter((i) => !c.ok(i));
        if (miss.length) out.push(`Feed check "${c.label}": missing on ${fmtInt(miss.length)} of ${fmtInt(rows.length)} products, ${fmtInt(miss.filter((i) => /^in.?stock$/i.test(cell(f.avail, i) || "")).length)} of them in stock. Fix: ${c.fix}`);
      }
    }
    const a = adsCols();
    if (a) {
      const by = new Map();
      for (const i of idx) { const k = word(a.name, i); if (k === null) continue; const e = by.get(k) || [0, 0]; e[0] += a.spend.reduce((s, c) => s + (c.values[i] || 0), 0); e[1] += a.conv.reduce((s, c) => s + (c.values[i] || 0), 0); by.set(k, e); }
      const unit = a.spend[0].unit;
      out.push(`${a.name.name} spend and conversions: ` + [...by].sort((x, y) => y[1][0] - x[1][0]).slice(0, 10).map(([k, [s, c]]) => `${k} ${money(s, unit)} for ${fmtInt(c)} conversions${c ? ` (${money(s / c, unit)} each)` : ""}`).join("; ") + ".");
    }
    return out.slice(0, 60);
  }
  async function readout(body, idx, r, fresh) {
    const fs = facts(r, idx);
    const box = el("section", { class: "cs-card2 cs-readout" }, el("p", { class: "cs-muted" }, "Writing the readout from the dashboard's numbers…"));
    const tools = el("div", { class: "cs-row cs-noprint" },
      el("button", { type: "button", class: "ghost", onclick: () => window.print() }, "Save as PDF"),
      el("button", { type: "button", class: "ghost", onclick: () => readoutAgain() }, "Write again"),
      el("button", { type: "button", class: "ghost", onclick: () => navigator.clipboard && navigator.clipboard.writeText(box.innerText) }, "Copy"));
    const factsBox = el("details", { class: "cs-mt cs-noprint" }, el("summary", { class: "cs-small cs-muted" }, "The numbers it was written from"), el("ul", { class: "cs-notes" }, fs.map((f) => el("li", {}, f))));
    body.append(el("div", { class: "cs-row" }, el("p", { class: "cs-muted cs-small cs-grow" }, "Written by a free AI from the numbers below, which code counted. Every number it writes is checked against them."), tools), box, factsBox);
    try {
      const out = await api(`/mangoman/sheets/${S.view.sheet.id}/readout`, { method: "POST", body: JSON.stringify({ facts: fs, fresh: !!fresh }) });
      if (!box.isConnected) return;
      box.replaceChildren(...renderText(out.text), out.unchecked && out.unchecked.length
        ? el("p", { class: "cs-unverified" }, `Check these numbers: ${out.unchecked.join(", ")}. They are not among the counted numbers.`)
        : el("p", { class: "cs-verified" }, "✓ Every number checked against the data"));
    } catch (err) {
      if (box.isConnected) box.replaceChildren(el("p", { class: "msg err" }, err.message), el("ul", { class: "cs-notes" }, fs.map((f) => el("li", {}, f))));
    }
  }
  function readoutAgain() { const body = $("cs-body"); body.replaceChildren(); readout(body, rowsNow(), roles(), true); }
  // The model's "## heading" and "- point" lines, as text.
  function renderText(text) {
    const out = [];
    let list = null;
    for (const raw of String(text).split("\n")) {
      const line = raw.trim().replace(/\*\*/g, "");
      if (!line) { list = null; continue; }
      if (/^#{1,3}\s/.test(line)) { list = null; out.push(el("h2", {}, line.replace(/^#+\s*/, ""))); }
      else if (/^[-*•]\s/.test(line)) { if (!list) out.push((list = el("ul"))); list.append(el("li", {}, line.replace(/^[-*•]\s*/, ""))); }
      else { list = null; out.push(el("p", {}, line)); }
    }
    return out;
  }

  // ---------- Ask ----------
  function openAsk() {
    let panel = $("cs-ask");
    if (panel) { panel.querySelector("textarea").focus(); return; }
    if ($("cs-plan")) $("cs-plan").remove();
    const id = S.view.sheet.id;
    const chat = (S.chat[id] = S.chat[id] || []);
    const thread = el("div", { class: "cs-thread", "aria-live": "polite" });
    const box = el("textarea", { rows: "1", placeholder: "Ask anything about this data…", "aria-label": "Your question" });
    const send = el("button", { type: "submit", class: "primary" }, "Ask");
    const form = el("form", { class: "cs-askbox" }, box, send);
    // On the page itself, not in the dashboard: switching tabs keeps the chat open.
    panel = el("aside", { class: "cs cs-ask", id: "cs-ask", "data-sheet": id, "aria-label": "Ask about this data" },
      el("header", {}, el("h3", { class: "cs-grow" }, "Ask about this data"), el("button", { type: "button", class: "ghost", onclick: () => panel.remove() }, "Close")), thread, form);
    document.body.append(panel);
    let busy = false;
    const intro = el("div", { class: "cs-a" }, el("p", {}, "Ask in your own words. I look up the exact numbers in your data, then explain them; you can see how I worked each answer out."),
      el("div", { class: "cs-suggest" }, (S.view.sheet.questions && S.view.sheet.questions.length ? S.view.sheet.questions : starter()).map((q) => el("button", { type: "button", onclick: () => ask(q) }, q))));
    thread.append(intro);
    for (const t of chat) { thread.append(el("div", { class: "cs-q" }, t.q)); thread.append(answerNode(t.answer, t.error)); }
    thread.scrollTop = thread.scrollHeight;
    box.addEventListener("keydown", (e) => { if (e.key === "Enter" && !e.shiftKey) { e.preventDefault(); form.requestSubmit(); } });
    form.addEventListener("submit", (e) => { e.preventDefault(); if (box.value.trim()) ask(box.value.trim()); });
    box.focus();
    async function ask(q) {
      if (busy) return;
      busy = true;
      box.value = ""; send.disabled = true;
      thread.append(el("div", { class: "cs-q" }, q));
      const thinking = el("div", { class: "cs-a" }, el("div", { class: "cs-thinking" }, el("div", { class: "now" }, "Reading the question…")));
      thread.append(thinking);
      thread.scrollTop = thread.scrollHeight;
      let answer = null, error = null;
      try {
        const { job } = await api(`/mangoman/sheets/${id}/ask`, { method: "POST", body: JSON.stringify({ question: q, history: chat.slice(-6).map((t) => ({ q: t.q, a: t.answer ? t.answer.text : t.error || "" })) }) });
        for (let i = 0; i < 600; i++) {
          await new Promise((r) => setTimeout(r, 1000));
          if (!thread.isConnected) return; // closed: stop asking for news
          const st = await api(`/mangoman/sheets/${id}/ask/${job}`);
          const lines = st.steps.map((s) => el("div", {}, `${s.why || "Looked something up"}${s.result ? ` (${plural(s.result.rows.length, "row")})` : s.error ? " (fixing a mistake)" : ""}`));
          if (!st.done) lines.push(el("div", { class: "now" }, st.steps.length ? "Working out the answer…" : "Deciding what to look up…"));
          thinking.firstChild.replaceChildren(...lines);
          if (st.done) { answer = st.answer || null; error = st.error || null; break; }
        }
      } catch (err) { error = err.message; }
      if (!answer && !error) error = "This took too long; ask again.";
      chat.push({ q, answer, error });
      thinking.replaceWith(answerNode(answer, error));
      send.disabled = false;
      busy = false;
      thread.scrollTop = thread.scrollHeight;
      box.focus();
    }
  }
  function starter() {
    const r = roles(), m = r.measures[0];
    const qs = [];
    if (m && r.date) qs.push(`How did ${m.name} change month by month?`);
    if (m && r.dims[0]) qs.push(`Which ${r.dims[0].name} has the highest ${m.name}?`);
    if (r.entity && m) qs.push(`Who are the top 5 ${r.entity.name} by ${m.name}?`);
    qs.push("What should I look at first in this data?");
    return qs;
  }
  function answerNode(a, error) {
    if (!a) return el("div", { class: "cs-a" }, el("p", { class: "msg err" }, error || "No answer."));
    const node = el("div", { class: "cs-a" }, ...renderText(a.text));
    if (a.unchecked && a.unchecked.length) node.append(el("p", { class: "cs-unverified" }, `Check: ${a.unchecked.join(", ")} did not come straight from the data.`));
    const c = a.chart;
    if (c && a.steps[c.step - 1] && a.steps[c.step - 1].result) {
      const res = a.steps[c.step - 1].result;
      const xi = res.columns.indexOf(c.x);
      const yi = c.y.map((y) => res.columns.indexOf(y));
      if (c.type !== "table" && res.rows.length >= 2 && res.rows.length <= 60) {
        const div = el("div", { class: "cs-chart" });
        node.append(div);
        const fmt = (v) => (typeof v === "number" ? fmtVal(null, v, true) : String(v));
        requestAnimationFrame(() => {
          if (!div.isConnected) return;
          const cats = res.rows.map((r) => String(r[xi] ?? ""));
          if (c.type === "pie") chart(div, { tooltip: { trigger: "item", formatter: (p) => `{t|${rt(p.name)}}\n${rt(fmtNum(p.value))} · ${rt(pct(p.percent))}` },
            series: [{ type: "pie", radius: ["50%", "78%"], itemStyle: { borderColor: css("--surface"), borderWidth: 2, borderRadius: 4 }, label: { color: css("--ink-2"), fontSize: 11 }, data: res.rows.map((r, i) => ({ name: String(r[xi]), value: r[yi[0]], itemStyle: { color: slot(i) } })) }] });
          else chart(div, {
            grid: { left: 8, right: 16, top: 12, bottom: 8, containLabel: true },
            xAxis: axis({ type: "category", data: cats, splitLine: { show: false }, axisLabel: { width: 80, overflow: "truncate" } }),
            yAxis: axis({ type: "value", axisLabel: { formatter: (v) => fmt(v) } }),
            tooltip: { trigger: "axis", formatter: (ps) => `{t|${rt(ps[0].axisValue)}}\n` + ps.map((p) => tipRow("k" + (p.seriesIndex + 1), p.seriesName, fmtNum(p.value))).join("\n") },
            series: yi.map((j, k) => ({ name: c.y[k], type: c.type, data: res.rows.map((r) => r[j]), smooth: 0.2, symbolSize: 7, barMaxWidth: 24,
              itemStyle: { color: slot(k), borderRadius: c.type === "bar" ? [4, 4, 0, 0] : 0 }, lineStyle: { width: 2, color: slot(k) } })),
          });
        });
        if (yi.length > 1) node.append(legend(c.y.map((y, k) => [slot(k), y])));
      } else if (c.type === "table" || res.rows.length > 60) {
        node.append(el("div", { class: "cs-tw cs-scroll" }, simpleTable({ head: res.columns, rows: res.rows.slice(0, 100).map((r) => r.map((v) => (v === null ? "" : typeof v === "number" ? fmtNum(v) : String(v)))) })));
      }
    }
    if (a.steps && a.steps.length) {
      node.append(el("details", {}, el("summary", {}, "How I worked this out"),
        ...a.steps.map((s, i) => el("div", {}, el("p", {}, `${i + 1}. ${s.why || "A look-up"}${s.result ? `: ${plural(s.result.rows.length, "row")}${s.result.more ? " (first 500)" : ""}` : ""}`), s.error ? el("p", { class: "cs-unverified" }, s.error) : null, el("pre", {}, s.sql)))));
    }
    return node;
  }
  function fmtNum(v) { return typeof v === "number" ? new Intl.NumberFormat(S.locale, { maximumFractionDigits: 2 }).format(v) : String(v ?? ""); }

  // ---------- custom dashboards ----------
  // A tile shows values: a column counted one way in one table, the rows of a
  // table, or the user's own metric. Tables meet on time (by date: exact) or
  // on a column with the same name in each (by matching words). The browser
  // counts every tile; the server only keeps them (internal/sheets/boards.go).
  const AGG = { sum: "Total", avg: "Average", min: "Lowest", max: "Highest", count: "Count of", distinct: "Different" };
  const TYPES = { kpi: "Number", line: "Line over time", column: "Columns", bar: "Bars", pie: "Pie", table: "Table" };
  const GRAIN = { "@day": "day", "@week": "week", "@month": "month", "@quarter": "quarter", "@year": "year" };
  const newId = () => Math.random().toString(36).slice(2, 10) || "x";
  const clone = (x) => JSON.parse(JSON.stringify(x));
  const dsNamed = (name) => S.data.datasets.find((d) => d.name === name);
  function tables() {
    return S.data.datasets.filter((d) => { const p = S.view.profile.datasets.find((x) => x.name === d.name); return !(p && p.joined_into); });
  }
  function metricById(id) {
    const open = S.open && S.open.metrics ? S.open.metrics : [];
    return [...open, ...((S.boards && S.boards.metrics) || [])].find((m) => m.id === id) || null;
  }
  // The dates in view on a dashboard: counted back from the latest day in any table.
  function boardSpan() {
    if (S.range === "all") return [null, null];
    if (S.range === "custom") return [S.from ?? -Infinity, S.to ?? Infinity];
    let end = null;
    for (const d of tables()) { const c = rolesOf(d).date; const e = c ? dateBounds(c)[1] : null; if (e !== null && (end === null || e > end)) end = e; }
    return spanEnding(end);
  }
  // the first and last day in view across the tables
  function allBounds() {
    let lo = Infinity, hi = -Infinity;
    for (const d of tables()) { const c = rolesOf(d).date; if (!c) continue; const [a, b] = dateBounds(c); if (a !== null) { lo = Math.min(lo, a); hi = Math.max(hi, b); } }
    if (lo === Infinity) return [null, null];
    const [a, b] = S.span;
    return [a !== null && a > lo ? a : lo, b !== null && b < hi ? b : hi];
  }
  const autoBy = () => { const [lo, hi] = allBounds(); return lo === null ? "@month" : "@" + grainFor(hi - lo); };
  // Rows of a table in view: the dates in view, the page's filters and the
  // tile's (by column name, where the table has the column), and clip.
  function rowsOf(ds, filters, clip) {
    const key = ds.name + "\u0001" + JSON.stringify(filters || []) + "\u0001" + JSON.stringify(clip);
    if (S.rowCache.has(key)) return S.rowCache.get(key);
    const date = rolesOf(ds).date;
    const [lo, hi] = S.span;
    const fs = [...Object.entries(S.dims), ...(filters || []).map((f) => [f.col, new Set(f.values)])].map(([n, set]) => [colIn(ds, n), set]).filter(([c]) => c);
    const out = [];
    for (let i = 0; i < ds.rows; i++) {
      if (date && (lo !== null || clip)) {
        const d = date.values[i];
        if (d === null || (lo !== null && (d < lo || d > hi)) || (clip && (d < clip[0] || d > clip[1]))) continue;
      }
      let ok = true;
      for (const [c, set] of fs) { if (!set.has(c.codes ? word(c, i) : c.values[i])) { ok = false; break; } }
      if (ok) out.push(i);
    }
    S.rowCache.set(key, out);
    return out;
  }
  function keyer(ds, by) {
    if (!by) return () => "";
    if (GRAIN[by]) {
      const c = rolesOf(ds).date;
      if (!c) throw new Error(`${ds.name} has no dates to split by`);
      return (i) => (c.values[i] === null ? null : bucket(c.values[i], GRAIN[by]));
    }
    const c = colIn(ds, by);
    if (!c) throw new Error(`${ds.name} has no ${by} column`);
    return (i) => { const v = c.codes ? word(c, i) : c.values[i]; return v === null ? null : String(v).trim().toLowerCase(); };
  }
  // One value of a tile, per key ("" when not split): Map key → number.
  function values(ref, t, clip, cx) {
    if (ref.metric) return metricValues(metricById(ref.metric), t, cx);
    const ds = dsNamed(ref.ds);
    if (!ds) throw new Error(`the table ${ref.ds} is not in the data now`);
    const c = ref.col ? colIn(ds, ref.col) : null;
    if (ref.col && !c) throw new Error(`the column ${ref.col} is not in ${ref.ds} now`);
    const key = keyer(ds, t.by);
    const byCol = t.by && !GRAIN[t.by] ? colIn(ds, t.by) : null;
    const acc = new Map();
    for (const i of rowsOf(ds, t.filters, clip)) {
      const k = key(i);
      if (k === null) continue;
      const v = !c ? 1 : c.codes ? word(c, i) : c.values[i];
      if (v === null) continue;
      if (byCol && !cx.names.has(k)) cx.names.set(k, String(byCol.codes ? word(byCol, i) : byCol.values[i]).trim());
      let e = acc.get(k);
      if (!e) acc.set(k, (e = { s: 0, n: 0, lo: Infinity, hi: -Infinity, set: ref.agg === "distinct" ? new Set() : null }));
      e.n++;
      if (e.set) e.set.add(v);
      else if (typeof v === "number") { e.s += v; e.lo = Math.min(e.lo, v); e.hi = Math.max(e.hi, v); }
    }
    const out = new Map();
    for (const [k, e] of acc) out.set(k, { sum: e.s, avg: e.s / e.n, min: e.lo, max: e.hi, count: e.n, distinct: e.set ? e.set.size : 0 }[ref.agg]);
    return out;
  }
  // (sum of add - sum of minus) / sum of per × times. Tables with dates are
  // counted over the days they all cover, so a ratio never compares months
  // one side has and the other lacks.
  function metricValues(m, t, cx) {
    if (!m) throw new Error("its metric was deleted");
    const refs = [...m.add, ...(m.minus || []), ...(m.per || [])];
    const clip = overlap(refs);
    if (clip) cx.notes.add(clip[0] <= clip[1] ? `${m.name} counts ${fmtDate(clip[0])} to ${fmtDate(clip[1])}, the days all its sources cover.` : `${m.name}: its sources share no dates.`);
    const [add, minus, per] = [m.add, m.minus || [], m.per || []].map((rs) => rs.map((r) => values(r, t, clip, cx)));
    const sum = (maps, k) => maps.reduce((a, mp) => a + (mp.get(k) || 0), 0);
    const out = new Map();
    for (const k of new Set([...add, ...per].flatMap((mp) => [...mp.keys()]))) {
      const top = sum(add, k) - sum(minus, k);
      if (!per.length) out.set(k, top * (m.times || 1));
      else if (sum(per, k)) out.set(k, (top / sum(per, k)) * (m.times || 1));
    }
    return out;
  }
  function overlap(refs) {
    const names = [...new Set(refs.map((r) => r.ds))];
    if (names.length < 2) return null;
    let lo = -Infinity, hi = Infinity, n = 0;
    for (const name of names) {
      const ds = dsNamed(name), c = ds && rolesOf(ds).date;
      const [a, b] = c ? dateBounds(c) : [null, null];
      if (a !== null) { lo = Math.max(lo, a); hi = Math.min(hi, b); n++; }
    }
    return n >= 2 ? [lo, hi] : null;
  }
  function fmtOf(ref) {
    if (ref.metric) {
      const m = metricById(ref.metric);
      if (!m) return null;
      const first = m.add[0] && m.add[0].col ? colIn(dsNamed(m.add[0].ds), m.add[0].col) : null;
      return { kind: m.kind, unit: m.kind === "money" && first ? first.unit : m.kind === "percent" ? "%" : undefined, name: m.name };
    }
    if (ref.agg === "count" || ref.agg === "distinct") return null;
    const c = colIn(dsNamed(ref.ds), ref.col);
    return c ? { kind: c.kind, unit: c.unit, name: c.name } : null;
  }
  function refName(ref, multi) {
    if (ref.metric) { const m = metricById(ref.metric); return m ? m.name : "Deleted metric"; }
    const base = !ref.col ? `Rows in ${ref.ds}` : ref.agg === "sum" ? ref.col : `${AGG[ref.agg]} ${ref.col}`;
    return multi && ref.col ? `${base} · ${ref.ds}` : base;
  }
  // Everything a tile shows: its keys in order and a series per value.
  function tileData(t) {
    const cx = { names: new Map(), notes: new Set() };
    const refs = t.values.flatMap((r) => { const m = r.metric && metricById(r.metric); return m ? [...m.add, ...(m.minus || []), ...(m.per || [])] : [r]; });
    for (const f of t.filters || []) if (!refs.some((r) => colIn(dsNamed(r.ds), f.col))) throw new Error(`no table here has the column ${f.col} it filters by`);
    const multi = new Set(t.values.filter((r) => !r.metric).map((r) => r.ds)).size > 1;
    const series = t.values.map((ref) => ({ ref, name: refName(ref, multi), fmt: fmtOf(ref), vals: values(ref, t, null, cx) }));
    let keys = [...new Set(series.flatMap((s) => [...s.vals.keys()]))];
    const time = !!GRAIN[t.by];
    if (time && keys.length) {
      const lo = Math.min(...keys), hi = Math.max(...keys), g = GRAIN[t.by];
      keys = [];
      for (let k = lo; k <= hi && keys.length < 2000; k = nextKey(k, g)) keys.push(k);
    } else if (t.by) {
      keys.sort((a, b) => (series[0].vals.get(b) ?? -Infinity) - (series[0].vals.get(a) ?? -Infinity));
    }
    const name = (k) => (time ? label(k, GRAIN[t.by]) : cx.names.get(k) ?? String(k));
    return { keys, name, series, time, notes: [...cx.notes] };
  }
  function tileSub(t, d) {
    const parts = [];
    if (t.by) parts.push(GRAIN[t.by] ? `By ${GRAIN[t.by]}` : `By ${t.by}`);
    for (const f of t.filters || []) parts.push(`only ${f.col}: ${f.values.slice(0, 3).join(", ")}${f.values.length > 3 ? ` +${f.values.length - 3}` : ""}`);
    const head = parts.join("; ");
    return [head ? head[0].toUpperCase() + head.slice(1) + "." : "", ...(d ? d.notes : [])].filter(Boolean).join(" ");
  }
  // A tile as the dashboard shows it. tools: edit buttons, when editing.
  function tileNode(t, tools) {
    const size = t.size || (t.type === "kpi" ? "s" : "m");
    let d = null, error = null;
    try { d = tileData(t); } catch (err) { error = err.message; }
    const head = el("header", {}, el("div", { class: "cs-grow" }, el("h3", {}, t.title), el("p", { class: "sub" }, tileSub(t, d))), tools ? el("div", { class: "cs-row cs-noprint" }, tools) : null);
    const node = el("section", { class: `cs-card2 cs-${size}`, "data-tile": t.id }, head);
    if (error) { node.append(el("p", { class: "cs-unverified" }, `This chart cannot be drawn: ${error}.`)); return node; }
    if (!d.keys.length) { node.append(el("p", { class: "cs-muted cs-small" }, "Nothing in view for this chart.")); return node; }
    const fmt = (s, v) => fmtVal(s.fmt, v);
    if (t.type === "kpi") {
      const [first, ...rest] = d.series;
      node.classList.add("cs-kpi");
      node.append(el("div", { class: "val" }, fmtVal(first.fmt, first.vals.get(""), true)));
      if (rest.length) node.append(el("div", { class: "cs-small cs-muted" }, rest.map((s) => `${s.name}: ${fmt(s, s.vals.get(""))}`).join(" · ")));
      try { // a sparkline when every value has dates
        const [lo, hi] = allBounds();
        if (lo !== null && hi > lo) {
          const sd = tileData({ ...t, values: [first.ref], by: "@" + grainFor(hi - lo) });
          const line = spark(sd.keys.map((k) => ({ v: sd.series[0].vals.get(k) || 0 })));
          if (line) node.append(line);
        }
      } catch (_) { /* a value without dates: no sparkline */ }
      return node;
    }
    if (t.type === "table") {
      const keys = d.time ? d.keys : d.keys.slice(0, t.top || 50);
      node.append(el("div", { class: "cs-tw cs-scroll" }, simpleTable({ head: [GRAIN[t.by] ? GRAIN[t.by][0].toUpperCase() + GRAIN[t.by].slice(1) : t.by, ...d.series.map((s) => s.name)],
        rows: keys.map((k) => [d.name(k), ...d.series.map((s) => (s.vals.has(k) ? fmt(s, s.vals.get(k)) : "-"))]), right: d.series.map((_, i) => i + 1) })));
      return node;
    }
    const body = el("div", { class: "cs-chart" + (size === "l" ? " tall" : "") });
    node.append(body);
    if (d.series.length > 1) node.append(legend(d.series.map((s, i) => [slot(i), s.name])));
    requestAnimationFrame(() => { if (body.isConnected) drawTile(body, t, d); });
    return node;
  }
  function drawTile(node, t, d) {
    const keys = d.time ? d.keys : d.keys.slice(0, t.top || 10);
    if (t.type === "pie") {
      const s = d.series[0];
      const rest = d.keys.slice(keys.length).reduce((a, k) => a + Math.max(0, s.vals.get(k) || 0), 0);
      const data = keys.map((k, i) => ({ name: d.name(k), value: Math.max(0, s.vals.get(k) || 0), itemStyle: { color: slot(i) } }));
      if (rest) data.push({ name: "Other", value: rest, itemStyle: { color: slot(SLOTS) } });
      chart(node, { tooltip: { trigger: "item", formatter: (p) => `{t|${rt(p.name)}}\n${rt(fmtVal(s.fmt, p.value))} · ${rt(pct(p.percent))}` },
        series: [{ type: "pie", radius: ["52%", "80%"], padAngle: 1.5, itemStyle: { borderRadius: 4, borderColor: css("--surface"), borderWidth: 2 },
          label: { show: node.clientWidth > 420, formatter: (p) => `${p.name}\n${pct(p.percent)}`, color: css("--ink-2"), fontSize: 11.5 }, data }] });
      return;
    }
    // Up to two scales: values in other units get their own axis on the right.
    const scales = [];
    const axisOf = d.series.map((s) => { const u = s.fmt ? `${s.fmt.kind}|${s.fmt.unit}` : "count"; let i = scales.findIndex((x) => x.u === u); if (i < 0 && scales.length < 2) { scales.push({ u, fmt: s.fmt }); i = scales.length - 1; } return Math.max(0, i); });
    const horizontal = t.type === "bar";
    const cats = axis({ type: "category", data: keys.map(d.name), inverse: horizontal, boundaryGap: t.type !== "line", splitLine: { show: false }, axisLabel: { width: horizontal ? 130 : 90, overflow: "truncate" } });
    const vals = scales.map((x, i) => axis({ type: "value", position: horizontal ? (i ? "top" : "bottom") : i ? "right" : "left", splitLine: { show: i === 0 }, minInterval: x.fmt ? 0 : 1, axisLabel: { formatter: (v) => fmtVal(x.fmt, v, true) } }));
    chart(node, {
      grid: { left: 8, right: t.type === "line" || scales.length > 1 ? 30 : 16, top: 16, bottom: 8, containLabel: true },
      xAxis: horizontal ? vals : cats, yAxis: horizontal ? cats : vals,
      tooltip: { trigger: "axis", axisPointer: { type: t.type === "line" ? "line" : "shadow" }, formatter: (ps) => `{t|${rt(ps[0].axisValue)}}\n` + ps.map((p) => tipRow("k" + (Math.min(p.seriesIndex, SLOTS) + 1), p.seriesName, p.value === null || p.value === undefined ? "-" : fmtVal(d.series[p.seriesIndex].fmt, p.value))).join("\n") },
      series: d.series.map((s, j) => ({ name: s.name, type: t.type === "line" ? "line" : "bar", [horizontal ? "xAxisIndex" : "yAxisIndex"]: axisOf[j],
        data: keys.map((k) => (s.vals.has(k) ? s.vals.get(k) : t.type === "line" ? null : 0)), connectNulls: false, smooth: 0.2, symbolSize: 7, showSymbol: keys.length <= 24, barMaxWidth: 26,
        lineStyle: { width: 2, color: slot(j) }, itemStyle: { color: slot(j), borderRadius: t.type === "line" ? 0 : horizontal ? [0, 4, 4, 0] : [4, 4, 0, 0] } })),
    });
  }

  // ---------- suggestions: dashboards worked out from the data ----------
  function suggestions() {
    const out = [];
    const g = autoBy();
    const tile = (title, type, vals, by = "", extra = {}) => ({ id: newId(), title: title.slice(0, 80), type, values: vals, by, ...extra });
    const total = (ds, c) => ({ ds: ds.name, col: c.name, agg: averaged(c) ? "avg" : "sum" });
    const rows = (ds) => ({ ds: ds.name, agg: "count" });
    const metric = (name, kind, add, per, times = 1) => ({ id: "m" + newId(), name: name.slice(0, 60), kind, add, per, times });
    const ov = overviewSuggestion(g);
    if (ov) out.push(ov);
    const main = dataset();
    for (const ds of [main, ...tables().filter((x) => x !== main)].slice(0, 3)) {
      const r = rolesOf(ds), m = r.measures[0];
      const v = m ? total(ds, m) : rows(ds);
      const tiles = r.measures.slice(0, 3).map((mm) => tile(`${averaged(mm) ? "Average" : "Total"} ${mm.name}`, "kpi", [total(ds, mm)]));
      tiles.push(tile("Rows", "kpi", [rows(ds)]));
      if (r.date) tiles.push(tile(`${refName(v)} by ${GRAIN[g]}`, "line", [v], g, { size: "l" }));
      if (r.dims[0]) tiles.push(tile(`${refName(v)} by ${r.dims[0].name}`, "bar", [v], r.dims[0].name, { top: 10 }));
      const small = r.dims.find((x) => x !== r.dims[0] && x.words.length <= 6);
      if (small && !(m && averaged(m))) tiles.push(tile(`Share by ${small.name}`, "pie", [v], small.name, { top: 6 }));
      if (r.entity && r.entity.codes) tiles.push(tile(`Top ${r.entity.name}`, "table", m ? [v, rows(ds)] : [v], r.entity.name, { top: 10, size: "m" }));
      const name = `${ds.name} at a glance`.slice(0, 60);
      if (tiles.length > 2) out.push({ name, why: `The main numbers${r.date ? ", the trend" : ""}${r.dims[0] ? ` and the split by ${r.dims[0].name}` : ""}.`, board: { name, tiles }, metrics: [] });
    }
    for (const ds of tables()) {
      const spend = adSpend(ds);
      const conv = ds.columns.find((c) => numeric(c) && /conv|purchase|results|leads/i.test(c.name) && !/rate|value|cost|per/i.test(c.name));
      const camp = ds.columns.find((c) => c.codes && /campaign|ad.?group|ad name/i.test(c.name));
      if (!spend || !conv || !camp) continue;
      const cpa = metric(`Cost per ${conv.name.toLowerCase()} (${ds.name})`, "money", [total(ds, spend)], [total(ds, conv)]);
      const tiles = [tile(`Total ${spend.name}`, "kpi", [total(ds, spend)]), tile(`Total ${conv.name}`, "kpi", [total(ds, conv)]), tile(cpa.name, "kpi", [{ metric: cpa.id }]),
        tile(`${spend.name} by ${camp.name}`, "bar", [total(ds, spend)], camp.name, { top: 10 }), tile(`${cpa.name} by ${camp.name}`, "bar", [{ metric: cpa.id }], camp.name, { top: 10 })];
      if (rolesOf(ds).date) tiles.push(tile(`${spend.name} and ${conv.name} by ${GRAIN[g]}`, "column", [total(ds, spend), total(ds, conv)], g, { size: "l" }));
      const name = `Ads: ${ds.name}`.slice(0, 60);
      out.push({ name, why: "Where the money goes, what it brings and which campaigns cost most per result.", board: { name, tiles }, metrics: [cpa] });
    }
    const sc = salesCols();
    if (sc) {
      const cust = { ds: main.name, col: sc.cust.name, agg: "distinct" };
      const avg = metric(`${sc.money.name} per customer`, "money", [total(main, sc.money)], [cust]);
      out.push({ name: "Customers", why: `${sc.cust.name}: how many buy, how many come back, who matters most.`, metrics: [avg], board: { name: "Customers", tiles: [
        tile("Customers", "kpi", [cust]), tile(avg.name, "kpi", [{ metric: avg.id }]), tile(`Customers by ${GRAIN[g] === "day" ? "week" : GRAIN[g]}`, "column", [cust], g === "@day" ? "@week" : g),
        tile("Top customers", "table", [total(main, sc.money), rows(main)], sc.cust.name, { top: 15 })] } });
    }
    return out.slice(0, 6);
  }
  // A table's ad spend: "spend", or "cost" in a table of campaigns (not shipping or goods costs).
  function adSpend(ds) {
    const camp = ds.columns.some((c) => c.codes && /campaign|ad.?group|ad name/i.test(c.name));
    return ds.columns.find((c) => c.kind === "money" && (/spend/i.test(c.name) || (camp && /^cost\b/i.test(c.name))) && !/per|cpc|cpa|cpm|unit/i.test(c.name)) || null;
  }
  // Every source added up by date, side by side: sales against ad spend.
  function overviewSuggestion(g) {
    const dated = tables().filter((d) => rolesOf(d).date && rolesOf(d).measures.length);
    if (dated.length < 2) return null;
    const spend = [], mains = [];
    let sales = null;
    for (const ds of dated) {
      const s = adSpend(ds);
      const m = s || rolesOf(ds).measures.find((c) => c.kind === "money" && !averaged(c)) || rolesOf(ds).measures[0];
      mains.push({ ds, m });
      if (s) spend.push({ ds: ds.name, col: s.name, agg: "sum" });
      else if (m.kind === "money" && (!sales || (/revenue|sales|total/i.test(m.name) && !/revenue|sales|total/i.test(sales.col)))) sales = { ds: ds.name, col: m.name, agg: "sum" };
    }
    const tile = (title, type, vals, by = "", extra = {}) => ({ id: newId(), title: title.slice(0, 80), type, values: vals, by, ...extra });
    const ref = (x) => ({ ds: x.ds.name, col: x.m.name, agg: averaged(x.m) ? "avg" : "sum" });
    const tiles = mains.slice(0, 4).map((x) => tile(`${x.m.name} · ${x.ds.name}`, "kpi", [ref(x)]));
    const metrics = [];
    // Sales and spend in different currencies are never divided.
    const unitOf = (r) => colIn(dsNamed(r.ds), r.col).unit;
    if (sales) spend.splice(0, spend.length, ...spend.filter((r) => unitOf(r) === unitOf(sales)));
    if (sales && spend.length) {
      const roas = { id: "m" + newId(), name: "Return on ad spend", kind: "number", add: [sales], per: spend, times: 1 };
      const share = { id: "m" + newId(), name: "Ad spend as % of sales", kind: "percent", add: spend, per: [sales], times: 100 };
      metrics.push(roas, share);
      tiles.push(tile(roas.name, "kpi", [{ metric: roas.id }]), tile(share.name, "kpi", [{ metric: share.id }]));
      const order = dsNamed(sales.ds).columns.find((c) => c.codes && /^order\b/i.test(c.name));
      if (order) { // different orders, not rows: a table of order lines has several rows an order
        const cpo = { id: "m" + newId(), name: "Ad spend per order", kind: "money", add: spend, per: [{ ds: sales.ds, col: order.name, agg: "distinct" }], times: 1 };
        metrics.push(cpo);
        tiles.push(tile(cpo.name, "kpi", [{ metric: cpo.id }]));
      }
      tiles.push(tile(`Sales and ad spend by ${GRAIN[g]}`, "column", [sales, ...spend].slice(0, 4), g, { size: "l" }), tile(`Return on ad spend by ${GRAIN[g]}`, "line", [{ metric: roas.id }], g, { size: "l" }));
    } else {
      tiles.push(tile(`Each source by ${GRAIN[g]}`, "column", mains.slice(0, 4).map(ref), g, { size: "l" }));
    }
    return { overview: true, name: "Business overview", why: "Every source added up by date and set side by side" + (metrics.length ? ": sales against ad spend." : "."), board: { name: "Business overview", tiles }, metrics };
  }

  // ---------- the dashboards tabs ----------
  async function loadBoards() {
    const id = S.view.sheet.id;
    if (!S.boards || S.boards.sheet !== id) S.boards = { sheet: id, ...(await api(`/mangoman/sheets/${id}/boards`)) };
  }
  async function saveBoards(boards, metrics) {
    try {
      const out = await api(`/mangoman/sheets/${S.view.sheet.id}/boards`, { method: "PUT", body: JSON.stringify({ boards, metrics }) });
      S.boards = { sheet: S.view.sheet.id, ...out };
      return true;
    } catch (err) { say(err.message, "err"); return false; }
  }
  // Metrics no board uses any more are kept: the user may want them later.
  async function keepDraft() {
    const b = { ...clone(S.open.board), id: newId() };
    const used = new Set(b.tiles.flatMap((t) => t.values.map((r) => r.metric)).filter(Boolean));
    const metrics = [...S.boards.metrics, ...S.open.metrics.filter((m) => used.has(m.id))];
    if (await saveBoards([...S.boards.boards, b], metrics)) { S.open = { id: b.id }; render(); say("Kept in My dashboards.", "ok"); }
  }
  function currentBoard() { return S.open && (S.open.board || S.boards.boards.find((b) => b.id === S.open.id)); }
  async function boardsTab(body) {
    body.append(el("p", { class: "cs-muted" }, "Opening…"));
    try { await loadBoards(); } catch (err) { body.replaceChildren(el("p", { class: "msg err" }, err.message)); return; }
    if (!body.isConnected) return;
    body.replaceChildren();
    S.span = boardSpan(); S.rowCache = new Map();
    if (S.open && !currentBoard()) S.open = null;
    if (S.open) drawBoard(body); else boardList(body);
  }
  function businessTab(body) {
    S.span = boardSpan(); S.rowCache = new Map();
    const sug = overviewSuggestion(autoBy());
    const cover = tables().filter((d) => rolesOf(d).date).map((d) => { const [a, b] = dateBounds(rolesOf(d).date); return a === null ? null : `${d.name}: ${fmtDate(a)} to ${fmtDate(b)}`; }).filter(Boolean);
    const keep = el("button", { type: "button", class: "ghost" }, "Change this dashboard");
    keep.addEventListener("click", async () => { try { await loadBoards(); } catch (err) { say(err.message, "err"); return; } S.open = { board: sug.board, metrics: sug.metrics }; S.tab = "boards"; render(); });
    body.append(el("div", { class: "cs-row" }, el("p", { class: "cs-muted cs-small cs-grow" }, "Every source added up by date, then set side by side. Dates line up exactly; which ad brought which order is not in these numbers (that needs tracking tags on orders)."), keep),
      el("p", { class: "cs-small cs-muted" }, "What each source covers: " + cover.join("; ") + "."));
    const was = S.open; // its metrics are found through S.open while the tiles are counted
    S.open = { board: sug.board, metrics: sug.metrics };
    body.append(el("div", { class: "cs-board cs-mt" }, sug.board.tiles.map((t) => tileNode(t))));
    S.open = was;
  }
  function boardList(body) {
    const sugs = suggestions();
    const card = (title, text, chips, go) => el("button", { type: "button", class: "cs-card", onclick: go }, el("h4", {}, title), el("p", {}, text), chips.length ? el("div", { class: "cs-meta" }, chips.map((c) => el("span", {}, c))) : null);
    body.append(el("div", { class: "cs-row" }, el("div", { class: "cs-grow" }, el("h3", { class: "cs-section" }, "Suggested for your data"), el("p", { class: "cs-muted cs-small" }, "Worked out from what was read in your data. Open one to look; keep it to change anything.")),
      el("button", { type: "button", class: "primary", onclick: () => openPlan() }, "Plan one with AI")));
    body.append(el("div", { class: "cs-list cs-mt" }, sugs.map((s) => card(s.name, s.why, s.board.tiles.slice(0, 4).map((t) => t.title), () => { S.open = { board: s.board, metrics: s.metrics }; render(); }))));
    const own = S.boards.boards;
    const blank = el("button", { type: "button", class: "ghost" }, "+ New dashboard");
    blank.addEventListener("click", async () => { const b = { id: newId(), name: `My dashboard ${own.length + 1}`, tiles: [] }; if (await saveBoards([...own, b], S.boards.metrics)) { S.open = { id: b.id }; render(); } });
    body.append(el("div", { class: "cs-row cs-mt" }, el("h3", { class: "cs-section cs-grow" }, "My dashboards"), blank),
      own.length ? el("div", { class: "cs-list" }, own.map((b) => card(b.name, plural(b.tiles.length, "chart"), b.tiles.slice(0, 4).map((t) => t.title), () => { S.open = { id: b.id }; render(); })))
        : el("p", { class: "cs-empty" }, "None yet. Keep a suggestion, start a new one, or plan one with the AI."));
    const metrics = S.boards.metrics;
    const addM = el("button", { type: "button", class: "ghost" }, "+ Add a metric");
    addM.addEventListener("click", () => metricEditor(null));
    body.append(el("div", { class: "cs-row cs-mt" }, el("div", { class: "cs-grow" }, el("h3", { class: "cs-section" }, "My metrics"), el("p", { class: "cs-muted cs-small" }, "Your own measures, worked out from your columns: return on ad spend, margin, cost per order.")), addM),
      metrics.length ? el("div", { class: "cs-card2" }, simpleTable({ head: ["Metric", "Worked out as", "Now", ""], right: [2], rows: metrics.map((m) => [m.name, formula(m), metricNow(m),
        el("span", { class: "cs-row" }, el("button", { type: "button", class: "cs-mini", onclick: () => metricEditor(m) }, "Edit"), el("button", { type: "button", class: "cs-mini", onclick: () => dropMetric(m) }, "Delete"))]) }))
        : el("p", { class: "cs-empty" }, "No metrics yet."));
  }
  function formula(m) {
    const names = (rs) => rs.map((r) => refName(r, true)).join(" + ");
    let f = m.minus && m.minus.length ? `(${names(m.add)} − ${names(m.minus)})` : names(m.add);
    if (m.per && m.per.length) f += ` ÷ ${m.per.length > 1 ? `(${names(m.per)})` : names(m.per)}`;
    return m.times === 100 ? f + " × 100" : f;
  }
  function metricNow(m) {
    try { const v = metricValues(m, { by: "" }, { names: new Map(), notes: new Set() }).get(""); return v === undefined ? "-" : fmtVal(fmtOf({ metric: m.id }) || { kind: m.kind }, v); } catch (err) { return err.message; }
  }
  async function dropMetric(m) {
    const users = S.boards.boards.filter((b) => b.tiles.some((t) => t.values.some((r) => r.metric === m.id)));
    if (users.length) { say(`${m.name} is used in ${users.map((b) => b.name).join(", ")}: take it out of those charts first.`, "err"); return; }
    if (confirm(`Delete the metric ${m.name}?`) && (await saveBoards(S.boards.boards, S.boards.metrics.filter((x) => x.id !== m.id)))) render();
  }

  function drawBoard(body) {
    const b = currentBoard();
    const saved = !S.open.board;
    const title = el("h3", { class: "cs-section", contenteditable: saved ? "true" : null, spellcheck: "false", title: saved ? "Click to rename" : null }, b.name);
    if (saved) {
      title.addEventListener("keydown", (e) => { if (e.key === "Enter") { e.preventDefault(); title.blur(); } });
      title.addEventListener("blur", () => { const name = title.textContent.trim().slice(0, 60); if (name && name !== b.name) { b.name = name; saveBoards(S.boards.boards, S.boards.metrics); } });
    }
    const save = () => (saved ? saveBoards(S.boards.boards, S.boards.metrics) : Promise.resolve(true));
    const actions = [el("button", { type: "button", class: "ghost", onclick: () => tileEditor(null, b, save) }, "+ Add a chart"),
      el("button", { type: "button", class: "ghost", onclick: () => openPlan() }, "Plan with AI"),
      el("button", { type: "button", class: "ghost", onclick: () => window.print() }, "Save as PDF")];
    if (saved) actions.push(el("button", { type: "button", class: "ghost", onclick: async () => { if (confirm(`Delete the dashboard ${b.name}?`) && (await saveBoards(S.boards.boards.filter((x) => x !== b), S.boards.metrics))) { S.open = null; render(); } } }, "Delete"));
    else actions.unshift(el("button", { type: "button", class: "primary", onclick: keepDraft }, "Keep this dashboard"));
    body.append(el("div", { class: "cs-row cs-noprint" }, el("button", { type: "button", class: "cs-back", onclick: () => { S.open = null; render(); } }, "← All dashboards")),
      el("div", { class: "cs-row" }, el("div", { class: "cs-grow" }, title, saved ? null : el("p", { class: "cs-muted cs-small" }, "Not kept yet: keep it to have it here next time. Changes you make now are kept with it.")), el("div", { class: "cs-actions cs-noprint" }, actions)));
    const grid = el("div", { class: "cs-board cs-mt" });
    if (!b.tiles.length) grid.append(el("p", { class: "cs-empty cs-l" }, "No charts yet. Add one, or plan the dashboard with the AI."));
    let dragged = null;
    for (const t of b.tiles) {
      const sizeBtn = el("button", { type: "button", class: "cs-mini", title: "Size", "aria-label": `Size of ${t.title}` }, { s: "S", m: "M", l: "L" }[t.size || (t.type === "kpi" ? "s" : "m")]);
      sizeBtn.addEventListener("click", async () => { const cur = t.size || (t.type === "kpi" ? "s" : "m"); t.size = { s: "m", m: "l", l: "s" }[cur]; await save(); render(); });
      const node = tileNode(t, [sizeBtn,
        el("button", { type: "button", class: "cs-mini", onclick: () => tileEditor(t, b, save) }, "Edit"),
        el("button", { type: "button", class: "cs-mini", "aria-label": `Remove ${t.title}`, onclick: async () => { b.tiles = b.tiles.filter((x) => x !== t); await save(); render(); } }, "×")]);
      // Drag a chart onto another to put it there.
      node.draggable = true;
      node.addEventListener("dragstart", (e) => { dragged = t; e.dataTransfer.effectAllowed = "move"; node.classList.add("drag"); });
      node.addEventListener("dragend", () => node.classList.remove("drag"));
      node.addEventListener("dragover", (e) => { if (dragged && dragged !== t) { e.preventDefault(); node.classList.add("over"); } });
      node.addEventListener("dragleave", () => node.classList.remove("over"));
      node.addEventListener("drop", async (e) => {
        e.preventDefault(); node.classList.remove("over");
        if (!dragged || dragged === t) return;
        const from = b.tiles.indexOf(dragged);
        b.tiles.splice(from, 1);
        b.tiles.splice(b.tiles.indexOf(t) + (from <= b.tiles.indexOf(t) ? 1 : 0), 0, dragged);
        await save(); render();
      });
      grid.append(node);
    }
    body.append(grid);
  }

  // ---------- editing: a chart, a metric ----------
  // The values a select offers: each table's rows and columns, and the metrics.
  function refSelect(sel, withMetrics) {
    const key = (r) => JSON.stringify(r.metric ? { metric: r.metric } : { ds: r.ds, col: r.col || undefined, agg: r.agg });
    const s = el("select", { "aria-label": "Value" });
    for (const d of tables()) {
      const opts = [el("option", { value: key({ ds: d.name, agg: "count" }) }, `Number of rows (${d.name})`)];
      for (const c of d.columns) {
        if (numeric(c)) for (const a of averaged(c) ? ["avg", "min", "max"] : ["sum", "avg", "min", "max"]) opts.push(el("option", { value: key({ ds: d.name, col: c.name, agg: a }) }, `${AGG[a]} ${c.name}`));
        else if (c.codes) opts.push(el("option", { value: key({ ds: d.name, col: c.name, agg: "distinct" }) }, `Different ${c.name}`));
      }
      s.append(el("optgroup", { label: d.name }, opts));
    }
    const ms = (S.boards.metrics || []).concat(S.open && S.open.metrics ? S.open.metrics : []);
    if (withMetrics && ms.length) s.append(el("optgroup", { label: "My metrics" }, ms.map((m) => el("option", { value: key({ metric: m.id }) }, m.name))));
    if (!sel) { // a new value starts on the main table's first measure
      const m = roles().measures[0];
      if (m) s.value = key({ ds: dataset().name, col: m.name, agg: averaged(m) ? "avg" : "sum" });
    } else {
      s.value = key(sel);
      if (s.value !== key(sel)) s.append(el("option", { value: key(sel), selected: true }, `${refName(sel)} (not in the data now)`));
    }
    return s;
  }
  const readRef = (s) => JSON.parse(s.value);
  // A list of value pickers with + and ×.
  function refList(refs, withMetrics, max, onChange) {
    const box = el("div", { class: "cs-refs" });
    const add = el("button", { type: "button", class: "cs-mini" }, "+ Add a value");
    const row = (r) => {
      const s = refSelect(r, withMetrics);
      s.addEventListener("change", onChange);
      const x = el("button", { type: "button", class: "cs-mini", "aria-label": "Remove this value" }, "×");
      const line = el("div", { class: "cs-row" }, s, x);
      x.addEventListener("click", () => { line.remove(); add.hidden = false; onChange(); });
      box.insertBefore(line, add);
      add.hidden = box.querySelectorAll("select").length >= max;
    };
    box.append(add);
    add.addEventListener("click", () => { row(null); onChange(); });
    for (const r of refs) row(r);
    return { node: box, read: () => [...box.querySelectorAll("select")].map(readRef) };
  }
  function editorBox(title, fields, preview, onSave) {
    const box = el("section", { class: "cs-card2 cs-editor cs-l" }, el("header", {}, el("h3", {}, title)), el("div", { class: "cs-fields" }, fields), preview,
      el("div", { class: "cs-row cs-mt" }, el("button", { type: "button", class: "primary", onclick: onSave }, "Save"), el("button", { type: "button", class: "ghost", onclick: () => box.remove() }, "Cancel")));
    $("cs-body").prepend(box);
    box.scrollIntoView({ behavior: "smooth", block: "start" });
    return box;
  }
  const labelled = (text, node) => el("label", { class: "cs-field" }, el("span", {}, text), node);
  function tileEditor(t, b, save) {
    const draft = clone(t || { id: newId(), title: "", type: "column", values: [], by: "" });
    const name = el("input", { type: "text", value: draft.title, maxlength: "80", placeholder: "Named after what it shows" });
    const type = el("select", {}, Object.entries(TYPES).map(([k, l]) => el("option", { value: k, selected: k === draft.type }, l)));
    const by = el("select", {});
    const top = el("input", { type: "number", min: "1", max: "50", value: String(draft.top || 10) });
    const fcol = el("select", {});
    const fvals = el("div", { class: "cs-scroll cs-fvals" });
    const size = el("select", {}, [["s", "Small"], ["m", "Medium"], ["l", "Wide"]].map(([k, l]) => el("option", { value: k, selected: k === (draft.size || (draft.type === "kpi" ? "s" : "m")) }, l)));
    const preview = el("div", { class: "cs-board cs-mt" });
    const vals = refList(draft.values.length ? draft.values : [null], true, 4, () => update());
    // the tables a value counts in
    const tablesOf = (r) => (r.metric ? (metricById(r.metric) ? [...metricById(r.metric).add, ...(metricById(r.metric).minus || []), ...(metricById(r.metric).per || [])].map((x) => dsNamed(x.ds)) : []) : [dsNamed(r.ds)]).filter(Boolean);
    function splits(refs) {
      const ts = [...new Set(refs.flatMap(tablesOf))];
      const out = [];
      if (ts.length && ts.every((d) => rolesOf(d).date)) out.push(...Object.entries(GRAIN).map(([k, g]) => [k, `Time: by ${g}`]));
      const shared = ts.length ? ts[0].columns.filter((c) => c.codes && ts.every((d) => { const x = colIn(d, c.name); return x && x.codes; })) : [];
      out.push(...shared.map((c) => [c.name, c.name]));
      return out;
    }
    function filterCols(refs) {
      const ts = [...new Set(refs.flatMap(tablesOf))];
      return ts.length ? ts[0].columns.filter((c) => c.codes && c.words.length <= 200) : [];
    }
    function read() {
      const refs = vals.read();
      const f = fcol.value ? [...fvals.querySelectorAll("input:checked")].map((x) => x.value) : [];
      const out = { id: draft.id, title: (name.value.trim() || (refs[0] ? refName(refs[0]) : "Chart")).slice(0, 80), type: type.value, values: refs, by: type.value === "kpi" ? "" : by.value };
      if (fcol.value && f.length) out.filters = [{ col: fcol.value, values: f }];
      if (!["kpi", "line"].includes(out.type) && !GRAIN[out.by]) out.top = Math.max(1, Math.min(50, Math.round(+top.value) || 10));
      out.size = size.value;
      return out;
    }
    function update(rebuild = true) {
      const refs = vals.read();
      if (rebuild) {
        const want = by.value || draft.by || autoBy();
        const opts = splits(refs).filter(([k]) => type.value !== "line" || GRAIN[k]);
        by.replaceChildren(...opts.map(([k, l]) => el("option", { value: k, selected: k === want }, l)));
        by.disabled = type.value === "kpi";
        const fwant = fcol.value || (draft.filters && draft.filters[0] && draft.filters[0].col) || "";
        fcol.replaceChildren(el("option", { value: "" }, "No filter"), ...filterCols(refs).map((c) => el("option", { value: c.name, selected: c.name === fwant }, c.name)));
      }
      const fc = fcol.value && tablesOf(refs[0] || {})[0] ? colIn(tablesOf(refs[0])[0], fcol.value) : null;
      if (rebuild || fvals.dataset.col !== fcol.value) {
        const had = new Set(draft.filters && draft.filters[0] ? draft.filters[0].values : []);
        fvals.dataset.col = fcol.value;
        fvals.replaceChildren(...(fc ? fc.words.slice(0, 200).map((w) => el("label", { class: "cs-row cs-small" }, el("input", { type: "checkbox", value: w, checked: had.has(w) }), w)) : []));
      }
      top.disabled = ["kpi", "line"].includes(type.value) || !!GRAIN[by.value];
      preview.replaceChildren();
      if (!refs.length) return;
      S.rowCache = new Map();
      if (type.value !== "kpi" && !by.value) { preview.append(el("p", { class: "cs-unverified" }, type.value === "line" ? "A line needs values with dates." : "These values share no column to split by: pick values from one table, or split by time.")); return; }
      preview.append(tileNode(read()));
    }
    for (const n of [type, by, fcol]) n.addEventListener("change", () => update(n !== by));
    for (const n of [top, size]) n.addEventListener("change", () => update(false));
    fvals.addEventListener("change", () => update(false));
    const box = editorBox(t ? "Change this chart" : "Add a chart", [labelled("Title", name), labelled("Chart", type), labelled("What it shows", vals.node), labelled("Split by", by), labelled("How many", top),
      labelled("Only rows where", el("div", {}, fcol, fvals)), labelled("Size", size)], preview, async () => {
      const out = read();
      if (!out.values.length || (out.type !== "kpi" && !out.by)) { say("Pick what the chart shows and how to split it.", "err"); return; }
      if (t) Object.keys(t).forEach((k) => delete t[k]);
      if (t) Object.assign(t, out); else b.tiles.push(out);
      if (await save()) { box.remove(); render(); }
    });
    update();
  }
  function metricEditor(m) {
    const draft = clone(m || { id: "m" + newId(), name: "", kind: "money", add: [], minus: [], per: [], times: 1 });
    const name = el("input", { type: "text", value: draft.name, maxlength: "60", placeholder: "Return on ad spend" });
    const kind = el("select", {}, [["money", "Money"], ["number", "Number"], ["percent", "Percent"]].map(([k, l]) => el("option", { value: k, selected: k === draft.kind }, l)));
    const times = el("input", { type: "checkbox", checked: draft.times === 100 });
    const now = el("div", { class: "cs-tile cs-mt" });
    const show = () => {
      const x = read();
      now.replaceChildren(el("div", { class: "lab" }, "Now, for the dates and filters in view"), el("div", { class: "val" }, x.add.length ? metricNowOf(x) : "-"), el("div", { class: "foot" }, x.add.length ? formulaOf(x) : ""));
    };
    const add = refList(draft.add.length ? draft.add : [null], false, 6, show), minus = refList(draft.minus || [], false, 6, show), per = refList(draft.per || [], false, 6, show);
    function read() { return { id: draft.id, name: name.value.trim(), kind: kind.value, add: add.read(), minus: minus.read(), per: per.read(), times: times.checked ? 100 : 1 }; }
    const metricNowOf = (x) => { const keep = S.open; S.open = { metrics: [x] }; try { return metricNow(x); } finally { S.open = keep; } };
    const formulaOf = (x) => formula(x);
    for (const n of [name, kind, times]) n.addEventListener("change", show);
    const box = editorBox(m ? "Change this metric" : "Add a metric", [labelled("Name", name), labelled("Shown as", kind), labelled("Add up", add.node), labelled("Take away", minus.node), labelled("Divide by", per.node),
      labelled("Times 100 (a percentage)", times)], now, async () => {
      const x = read();
      if (!x.name || !x.add.length) { say("Give the metric a name and at least one value to add up.", "err"); return; }
      if (await saveBoards(S.boards.boards, m ? S.boards.metrics.map((y) => (y.id === m.id ? x : y)) : [...S.boards.metrics, x])) { box.remove(); render(); }
    });
    S.span = boardSpan(); S.rowCache = new Map();
    show();
  }

  // ---------- planning a dashboard with the AI ----------
  function openPlan() {
    if ($("cs-plan")) { $("cs-plan").querySelector("textarea").focus(); return; }
    closeAsk();
    const id = S.view.sheet.id;
    const chat = (S.plans[id] = S.plans[id] || []);
    const thread = el("div", { class: "cs-thread", "aria-live": "polite" });
    const box = el("textarea", { rows: "1", placeholder: "What do you want to keep an eye on?", "aria-label": "Your message" });
    const send = el("button", { type: "submit", class: "primary" }, "Send");
    const form = el("form", { class: "cs-askbox" }, box, send);
    const panel = el("aside", { class: "cs cs-ask", id: "cs-plan", "data-sheet": id, "aria-label": "Plan a dashboard" },
      el("header", {}, el("h3", { class: "cs-grow" }, "Plan a dashboard with AI"), el("button", { type: "button", class: "ghost", onclick: () => panel.remove() }, "Close")), thread, form);
    document.body.append(panel);
    thread.append(el("div", { class: "cs-a" }, el("p", {}, "Tell me what you want to watch, or ask what is worth watching. I see your column names, not your rows. When we agree, I lay the dashboard out for you to check; code counts every number in it."),
      el("div", { class: "cs-suggest" }, ["What should a business like mine watch every week?", "Do my ads pay off?", "Show me sales by month and by channel"].map((q) => el("button", { type: "button", onclick: () => talk(q) }, q)))));
    for (const turn of chat) thread.append(turn.role === "user" ? el("div", { class: "cs-q" }, turn.text) : planNode(turn.plan));
    thread.scrollTop = thread.scrollHeight;
    box.addEventListener("keydown", (e) => { if (e.key === "Enter" && !e.shiftKey) { e.preventDefault(); form.requestSubmit(); } });
    form.addEventListener("submit", (e) => { e.preventDefault(); if (box.value.trim()) talk(box.value.trim()); });
    box.focus();
    let busy = false;
    async function talk(text) {
      if (busy) return;
      busy = true; send.disabled = true; box.value = "";
      chat.push({ role: "user", text });
      thread.append(el("div", { class: "cs-q" }, text));
      const wait = el("div", { class: "cs-a" }, el("div", { class: "cs-thinking" }, el("div", { class: "now" }, "Thinking it through…")));
      thread.append(wait);
      thread.scrollTop = thread.scrollHeight;
      let plan;
      try {
        plan = await api(`/mangoman/sheets/${id}/plan`, { method: "POST", body: JSON.stringify({ chat: chat.slice(-10).map((t) => ({ role: t.role, text: t.text })) }) });
      } catch (err) { plan = { error: err.message }; }
      // What it proposed rides along in the chat, so "add a chart of X" can change it.
      chat.push({ role: "ai", text: plan.error ? "(no answer)" : plan.reply + (plan.board ? `\n(Proposed "${plan.board.name}": ${plan.board.tiles.map((t) => `${t.title} [${t.type}${t.by ? " by " + t.by : ""}]`).join("; ")})` : ""), plan });
      if (!panel.isConnected) return;
      wait.replaceWith(planNode(plan));
      busy = false; send.disabled = false;
      thread.scrollTop = thread.scrollHeight;
      box.focus();
    }
  }
  function planNode(p) {
    if (p.error) return el("div", { class: "cs-a" }, el("p", { class: "msg err" }, p.error));
    const node = el("div", { class: "cs-a" }, ...renderText(p.reply));
    if (p.unchecked && p.unchecked.length) node.append(el("p", { class: "cs-unverified" }, `Check: ${p.unchecked.join(", ")} did not come from your data.`));
    if (p.board) {
      const show = el("button", { type: "button", class: "primary" }, `Show "${p.board.name}"`);
      show.addEventListener("click", async () => { try { await loadBoards(); } catch (err) { say(err.message, "err"); return; } S.open = { board: clone(p.board), metrics: clone(p.metrics || []) }; S.tab = "boards"; render(); });
      node.append(el("ul", { class: "cs-notes" }, p.board.tiles.map((t) => el("li", {}, `${t.title} (${TYPES[t.type].toLowerCase()})`))), show);
    }
    if (p.dropped && p.dropped.length) node.append(el("details", {}, el("summary", {}, `${plural(p.dropped.length, "part")} left out`), el("ul", {}, p.dropped.map((x) => el("li", {}, x)))));
    return node;
  }

  // ---------- live Google Sheets ----------
  function startLive() {
    stopLive();
    if (!S.view || !S.view.live) return;
    const id = S.view.sheet.id;
    const fetched = Date.parse(liveSource().fetched || 0);
    if (Date.now() - fetched > 60000) refresh(false);
    S.liveTimer = setInterval(() => { if (!document.hidden && S.view && S.view.sheet.id === id && location.hash.startsWith("#chargesheets/" + id)) refresh(false); }, 5 * 60000);
  }
  // the linked Google Sheet or connection checked longest ago
  function liveSource() { return S.view.sheet.sources.filter((s) => s.url || s.kind).sort((a, b) => Date.parse(a.fetched || 0) - Date.parse(b.fetched || 0))[0]; }
  function stopLive() { if (S.liveTimer) clearInterval(S.liveTimer); S.liveTimer = null; }
  async function refresh(byHand) {
    const id = S.view.sheet.id;
    const tag = $("cs-live");
    if (tag) tag.textContent = "Live · checking…";
    try {
      const r = await api(`/mangoman/sheets/${id}/refresh${byHand ? "?force=1" : ""}`, { method: "POST" });
      if (!S.view || S.view.sheet.id !== id) return;
      S.view.sheet = r.sheet;
      if (r.changed) {
        const view = await api(`/mangoman/sheets/${id}`);
        const data = await api(`/mangoman/sheets/${id}/data`);
        if (!S.view || S.view.sheet.id !== id) return;
        data.id = id;
        S.view = view; S.data = data;
        S.ds = Math.min(S.ds, S.data.datasets.length - 1);
        if (location.hash.startsWith("#chargesheets/" + id) && host().querySelector("#cs-body")) render();
        say(r.sheet.changed || "Your data changed; the dashboard is up to date.", "ok");
      } else {
        const t = $("cs-live");
        if (t) t.textContent = "Live · checked just now";
        if (byHand) say("No changes since the last check.", "ok");
      }
    } catch (err) {
      const t = $("cs-live");
      if (t) t.textContent = "Live · could not check";
      if (byHand) say(err.message, "err");
    }
  }

  async function remove(sh) {
    if (!confirm(`Delete the Chargesheet "${sh.title}"? Its copies of your files go too; your own files are not touched.`)) return;
    try { await api(`/mangoman/sheets/${sh.id}`, { method: "DELETE" }); S.view = null; S.data = null; showLibrary(); } catch (err) { say(err.message, "err"); }
  }

  // ---------- routes ----------
  function route(sub) {
    host().classList.add("cs");
    const [id, tab] = (sub || "").split("/");
    if (!id) { if (!host().querySelector(".cs-progress")) showLibrary(); return; }
    if (tab === "check") { showCheck(id); return; }
    if (S.view && S.view.sheet.id === id && S.data && S.data.id === id && host().querySelector("#cs-body")) { if (tab && tab !== S.tab) { S.tab = tab; render(); } return; }
    open(id, tab);
  }
  document.addEventListener("keydown", (e) => { if (e.key === "Escape") closeAsk(); });
  // the page's dark mode changed: charts read colours at draw time
  if (window.matchMedia) window.matchMedia("(prefers-color-scheme: dark)").addEventListener("change", () => { if (S.data && host().querySelector("#cs-body")) render(); });
  return { route, leave: () => { closeAsk(); stopLive(); disposeCharts(); } };
})();
