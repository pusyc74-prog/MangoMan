"use strict";

// Chargesheets: the user's spreadsheets as a dashboard, in the browser.
// The engine (internal/sheets) cleans the data; this file counts it again
// for every filter, draws it with ECharts (vendor/, Apache-2.0) and talks to
// the free models only through the router's endpoints. Values from files
// are always inserted as text, never as HTML.
// Uses app.js's $, el, api, state and say.

const Sheets = (() => {
  const S = { list: [], view: null, data: null, ds: 0, tab: "overview", charts: [], measure: null, grain: null,
    range: "all", from: null, to: null, dims: {}, compare: true, chat: {}, liveTimer: null, sortTable: null, page: 0, q: "" };
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
    if (v === null || v === undefined || Number.isNaN(v)) return "—";
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
    if (v === null || v === undefined || Number.isNaN(v)) return "—";
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
  function roles() {
    const ds = dataset();
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
    const [, end] = dateBounds(c);
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
    const span = hi - lo;
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
  async function showLibrary() {
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
      el("div", { class: "cs-row" }, el("h4", { class: "cs-grow" }, sh.title), sh.sources.some((s) => s.url) ? el("span", { class: "cs-live" }, "Live") : null),
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
        el("p", { class: "cs-hint" }, "First, in Google Sheets: ", el("b", {}, "Share"), " → General access → ", el("b", {}, "Anyone with the link"), " (Viewer).")));
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

  async function addFiles(id, files) {
    const p = progress("Making your Chargesheet", [...files.map((f) => `Copying ${f.name}`), "Reading and cleaning the data", "Linking tables and checking totals", "Getting the dashboard ready"]);
    try {
      p.next();
      if (!id) id = (await newSheet()).id;
      for (const f of files) {
        const res = await fetch(`/mangoman/sheets/${id}/files?name=${encodeURIComponent(f.name)}`, { method: "POST", body: f, headers: { Authorization: "Bearer " + state.token } });
        const body = await res.json().catch(() => ({}));
        if (!res.ok) throw new Error((body.error && body.error.message) || `HTTP ${res.status}`);
        p.next();
      }
      await load(id, p);
    } catch (err) { p.fail(err.message); }
  }
  async function addGoogle(id, link) {
    const p = progress("Linking your Google Sheet", ["Downloading every tab from Google Sheets", "Reading and cleaning the data", "Linking tables and checking totals", "Getting the dashboard ready"]);
    try {
      p.next();
      if (!id) id = (await newSheet()).id;
      await api(`/mangoman/sheets/${id}/google`, { method: "POST", body: JSON.stringify({ url: link }) });
      p.next();
      await load(id, p);
    } catch (err) { p.fail(err.message); }
  }
  async function load(id, p) {
    const t = setTimeout(() => p.next(), 900);
    const v = await api(`/mangoman/sheets/${id}/load`, { method: "POST" });
    clearTimeout(t);
    p.next(); p.next();
    S.view = v;
    showCheck(id);
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
    const sources = el("div", { class: "cs-sources" }, v.sheet.sources.map((s) => el("span", { class: "cs-chip" + (s.url ? " on" : "") }, s.url ? "Google Sheet: " : "", s.label,
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
    try {
      if (!S.view || S.view.sheet.id !== id) S.view = await api(`/mangoman/sheets/${id}`);
      if (!S.view.profile) { showCheck(id); return; }
      if (!S.data || S.data.id !== id) {
        host().replaceChildren(el("p", { class: "cs-muted" }, "Opening…"));
        S.data = await api(`/mangoman/sheets/${id}/data`);
        S.data.id = id;
        S.ds = Math.max(0, S.data.datasets.findIndex((d) => d.name === (S.view.sheet.roles || {}).table));
        S.dims = {}; S.range = "all"; S.measure = null; S.grain = null; S.page = 0; S.q = "";
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
    const live = v.live ? el("span", { class: "cs-live", id: "cs-live" }, "Live · checked " + ago(sh.sources.find((s) => s.url).fetched)) : null;
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
    const idx = rowsNow();
    if (!idx.length) { body.append(el("p", { class: "cs-empty" }, "No rows match these filters.")); return; }
    const draw = { overview, trends, breakdown, drill, spread, quality, table: allRows, readout }[S.tab] || packs().find((p) => p.id === S.tab).draw;
    draw(body, idx, r);
  }

  function toolbar(r) {
    const parts = [];
    if (S.data.datasets.length > 1) {
      const pick = el("select", { "aria-label": "Table" }, S.data.datasets.map((d, i) => el("option", { value: i, selected: i === S.ds }, `${d.name} (${plural(d.rows, "row")})`)));
      pick.addEventListener("change", () => { S.ds = +pick.value; S.dims = {}; S.measure = null; S.range = "all"; render(); });
      parts.push(pick);
    }
    if (r.date) {
      const opts = [["all", "All time"], ["7", "Last 7 days"], ["30", "Last 30 days"], ["90", "Last 90 days"], ["month", "Latest month"], ["lastmonth", "Month before"], ["year", "Latest year"], ["custom", "Pick dates"]];
      const sel = el("select", { "aria-label": "Dates" }, opts.map(([k, l]) => el("option", { value: k, selected: S.range === k }, l)));
      sel.addEventListener("change", () => { S.range = sel.value; if (S.range === "custom" && S.from === null) { const [lo, hi] = dateBounds(r.date); S.from = lo; S.to = hi; } render(); });
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
  function periodCompare(r, m, idx) {
    if (!r.date) return null;
    const grain = autoGrain(r.date, idx);
    const g = grain === "day" ? "week" : grain === "week" ? "month" : grain;
    const pts = series(r.date, m, idx, g);
    if (pts.length < 2) return null;
    const a = pts[pts.length - 1], b = pts[pts.length - 2];
    const [, end] = dateBounds(r.date);
    const partial = g === "month" && end !== null && dateOf(end).getUTCDate() < 25 ? " so far" : "";
    return { now: a, before: b, grain: g, label: `${a.label}${partial} vs ${b.label}`, change: b.v ? ((a.v - b.v) / Math.abs(b.v)) * 100 : null };
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
        () => ({ head: [d0.name, m ? m.name : "Rows", "Share"], rows: g.map((x) => [x.k, fmtVal(m, x.v), pct(x.share)]), right: [1, 2] })));
    }
    const d1 = r.dims.find((d) => d !== d0 && d.words.length <= 6);
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
    const out = g.slice(0, n).map((x) => ({ ...x, share: (Math.abs(x.v) / sum) * 100 }));
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
        const inP = (k) => idx.filter((i) => r.date.values[i] !== null && bucket(r.date.values[i], cmp.grain) === k);
        const a = new Map(groupBy(d, m, inP(cmp.now.k)).map((x) => [x.k, x.v]));
        const b = new Map(groupBy(d, m, inP(cmp.before.k)).map((x) => [x.k, x.v]));
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
        () => ({ head: [d.name, m ? m.name : "Rows", "Rows", "Share"], rows: all.map((x) => [x.k, fmtVal(m, x.v), fmtInt(x.n), pct(x.share)]), right: [1, 2, 3] })));
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
        series: [{ type: "heatmap", data: heatData(data, max), label: { show: ka.length * kb.length <= 48, formatter: (p) => (p.value[2] ? fmtVal(m, p.value[2], true) : ""), fontSize: 11 }, itemStyle: { borderColor: css("--surface"), borderWidth: 2, borderRadius: 3 } }],
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
    const q = (s) => { s = s === null || s === undefined ? "" : String(s); return /[",\n]/.test(s) || /^[=+\-@]/.test(s) ? `"${(/^[=+\-@]/.test(s) ? "'" : "") + s.replace(/"/g, '""')}"` : s; };
    const text = [cols.map((c) => q(c.name)).join(","), ...rows.map((i) => cols.map((c) => q(cell(c, i))).join(","))].join("\n");
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
    const t = el("table", { class: "cs-table" }, el("thead", {}, el("tr", {}, ["Check", "Missing", "In stock", "Filled", ""].map((h, j) => el("th", { class: j && j < 3 ? "r" : null }, h)))),
      el("tbody", {}, sorted.map((c) => {
        const fill = c.n ? ((c.n - c.rows.length) / c.n) * 100 : 100;
        const tr = el("tr", { class: c.rows.length ? "click" : null }, el("td", {}, el("b", {}, c.label), el("div", { class: "cs-small cs-muted" }, c.rows.length ? c.fix : "All filled.")),
          el("td", { class: "r" }, `${fmtInt(c.rows.length)} / ${fmtInt(c.n)}`), el("td", { class: "r" }, el("b", {}, fmtInt(c.live))),
          el("td", {}, el("div", { class: "cs-bar-cell" }, el("i", { style: { width: `${Math.max(2, fill)}%`, background: fill >= 95 ? "var(--c1)" : fill >= 70 ? "var(--c5)" : "var(--down)" } }), pct(fill))),
          el("td", {}, c.rows.length ? el("span", { class: "cs-mini" }, "See products") : ""));
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
        xAxis: axis({ type: "category", data: res.map((c) => c.label.replace(" (clothing)", "")), splitLine: { show: false }, axisLabel: { interval: 0, rotate: 25, width: 100, overflow: "truncate" } }),
        yAxis: axis({ type: "category", data: types, inverse: true, splitLine: { show: false }, axisLabel: { width: 150, overflow: "truncate" } }),
        visualMap: heat(100, "{value}%"),
        tooltip: { trigger: "item", formatter: (p) => `{t|${rt(types[p.value[1]])}}\n${rt(res[p.value[0]].label)}: ${p.value[2] === "-" ? "does not apply" : rt(p.value[2] + "% filled")}` },
        series: [{ type: "heatmap", data: heatData(data, 100), label: { show: true, formatter: (p) => (p.value[2] === "-" ? "" : p.value[2] + "%"), fontSize: 11 }, itemStyle: { borderColor: css("--surface"), borderWidth: 2, borderRadius: 3 } }],
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
      el("div", { class: "cs-tile" }, el("div", { class: "lab" }, "Cost per conversion"), el("div", { class: "val" }, tConv ? money(tSpend / tConv, unit) : "—"), el("div", { class: "foot" }, "all campaigns")),
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
    for (const mm of r.measures.slice(0, 3)) out.push(`${averaged(mm) ? "Average" : "Total"} ${mm.name}: ${fmtVal(mm, total(mm, idx))}; average per row ${fmtVal(mm, total(mm, idx) / (idx.length || 1))}.`);
    if (r.date && m) {
      const g = autoGrain(r.date, idx);
      const pts = series(r.date, m, idx, g === "day" && idx.length > 60 ? "week" : g);
      out.push(`${m.name} by ${g}: ` + pts.slice(-12).map((p) => `${p.label} ${fmtVal(m, p.v)}`).join("; ") + ".");
      const cmp = periodCompare(r, m, idx);
      if (cmp && cmp.change !== null) out.push(`${cmp.label}: ${m.name} ${cmp.change >= 0 ? "up" : "down"} ${pct(cmp.change)} (${fmtVal(m, cmp.now.v)} against ${fmtVal(m, cmp.before.v)}).`);
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
    const id = S.view.sheet.id;
    const chat = (S.chat[id] = S.chat[id] || []);
    const thread = el("div", { class: "cs-thread", "aria-live": "polite" });
    const box = el("textarea", { rows: "1", placeholder: "Ask anything about this data…", "aria-label": "Your question" });
    const send = el("button", { type: "submit", class: "primary" }, "Ask");
    const form = el("form", { class: "cs-askbox" }, box, send);
    panel = el("aside", { class: "cs-ask", id: "cs-ask", "aria-label": "Ask about this data" },
      el("header", {}, el("h3", { class: "cs-grow" }, "Ask about this data"), el("button", { type: "button", class: "ghost", onclick: () => panel.remove() }, "Close")), thread, form);
    host().append(panel);
    const intro = el("div", { class: "cs-a" }, el("p", {}, "Ask in your own words. I look up the exact numbers in your data, then explain them; you can see how I worked each answer out."),
      el("div", { class: "cs-suggest" }, (S.view.sheet.questions && S.view.sheet.questions.length ? S.view.sheet.questions : starter()).map((q) => el("button", { type: "button", onclick: () => ask(q) }, q))));
    thread.append(intro);
    for (const t of chat) { thread.append(el("div", { class: "cs-q" }, t.q)); thread.append(answerNode(t.answer, t.error)); }
    thread.scrollTop = thread.scrollHeight;
    box.addEventListener("keydown", (e) => { if (e.key === "Enter" && !e.shiftKey) { e.preventDefault(); form.requestSubmit(); } });
    form.addEventListener("submit", (e) => { e.preventDefault(); if (box.value.trim()) ask(box.value.trim()); });
    box.focus();
    async function ask(q) {
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
        const fmt = (v) => (typeof v === "number" ? fmtVal(null, v, true).replace(/^—$/, "") || String(v) : String(v));
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

  // ---------- live Google Sheets ----------
  function startLive() {
    stopLive();
    if (!S.view || !S.view.live) return;
    const id = S.view.sheet.id;
    const fetched = Date.parse(S.view.sheet.sources.find((s) => s.url).fetched || 0);
    if (Date.now() - fetched > 60000) refresh(false);
    S.liveTimer = setInterval(() => { if (!document.hidden && S.view && S.view.sheet.id === id && location.hash.startsWith("#chargesheets/" + id)) refresh(false); }, 5 * 60000);
  }
  function stopLive() { if (S.liveTimer) clearInterval(S.liveTimer); S.liveTimer = null; }
  async function refresh(byHand) {
    const id = S.view.sheet.id;
    const tag = $("cs-live");
    if (tag) tag.textContent = "Live · checking…";
    try {
      const r = await api(`/mangoman/sheets/${id}/refresh`, { method: "POST" });
      if (!S.view || S.view.sheet.id !== id) return;
      S.view.sheet = r.sheet;
      if (r.changed) {
        S.view = await api(`/mangoman/sheets/${id}`);
        S.data = await api(`/mangoman/sheets/${id}/data`);
        S.data.id = id;
        S.ds = Math.min(S.ds, S.data.datasets.length - 1);
        if (!$("cs-ask")) render();
        say(r.sheet.changed || "The Google Sheet changed; the dashboard is up to date.", "ok");
      } else {
        const t = $("cs-live");
        if (t) t.textContent = "Live · checked just now";
        if (byHand) say("No changes in the Google Sheet since the last check.", "ok");
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
  // the page's dark mode changed: charts read colours at draw time
  if (window.matchMedia) window.matchMedia("(prefers-color-scheme: dark)").addEventListener("change", () => { if (S.data && host().querySelector("#cs-body")) render(); });
  return { route, leave: () => { stopLive(); disposeCharts(); } };
})();
