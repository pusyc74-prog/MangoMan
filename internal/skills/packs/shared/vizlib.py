"""Charts and number formatting for MangoMan skill packs (standard library only).

Charts are SVG strings drawn from a small spec, so a model never hand-draws a
chart: it computes the numbers, and this file applies one tested design.
Design rules: one axis; thin marks (2px lines, bars at most 24px, 4px rounded
data ends); hairline grid; a legend for two or more series plus selective
direct labels; text never in the series colour; a validated categorical
palette in a fixed order, light and dark; a table view for every chart.
"""
import html
import json
import math
import os
import subprocess
import sys

PALETTE_LIGHT = ["#2a78d6", "#eb6834", "#1baf7a", "#eda100", "#e87ba4", "#008300", "#4a3aa7", "#e34948"]
PALETTE_DARK = ["#3987e5", "#d95926", "#199e70", "#c98500", "#d55181", "#008300", "#9085e9", "#e66767"]

CSS_TOKENS = """
:root { color-scheme: light;
  --page:#f9f9f7; --surface:#fcfcfb; --ink:#0b0b0b; --ink-2:#52514e; --muted:#898781;
  --grid:#e1e0d9; --axis:#c3c2b7; --dim:#c3c2b7; --ring:rgba(11,11,11,0.10); --up:#006300; --down:#d03b3b;
  %s }
@media (prefers-color-scheme: dark) { :root:where(:not([data-theme="light"])) { color-scheme: dark;
  --page:#0d0d0d; --surface:#1a1a19; --ink:#ffffff; --ink-2:#c3c2b7; --muted:#898781;
  --grid:#2c2c2a; --axis:#383835; --dim:#55544f; --ring:rgba(255,255,255,0.10); --up:#0ca30c; --down:#e66767;
  %s } }
:root[data-theme="dark"] { color-scheme: dark;
  --page:#0d0d0d; --surface:#1a1a19; --ink:#ffffff; --ink-2:#c3c2b7; --muted:#898781;
  --grid:#2c2c2a; --axis:#383835; --dim:#55544f; --ring:rgba(255,255,255,0.10); --up:#0ca30c; --down:#e66767;
  %s }
""" % (
    " ".join("--s%d:%s;" % (i + 1, c) for i, c in enumerate(PALETTE_LIGHT)),
    " ".join("--s%d:%s;" % (i + 1, c) for i, c in enumerate(PALETTE_DARK)),
    " ".join("--s%d:%s;" % (i + 1, c) for i, c in enumerate(PALETTE_DARK)),
)

CHART_CSS = """
.chart svg { display:block; width:100%; height:auto; overflow:visible; }
.chart text { font: 12px system-ui, -apple-system, "Segoe UI", sans-serif; fill: var(--muted); font-variant-numeric: tabular-nums; }
.chart text.lbl { fill: var(--ink-2); }
.chart .grid { stroke: var(--grid); stroke-width:1; }
.chart .base { stroke: var(--axis); stroke-width:1; }
.chart .hit { fill: transparent; }
.chart .hit:hover { fill: var(--ink); fill-opacity: .04; }
.legend { display:flex; flex-wrap:wrap; gap:4px 16px; margin: 0 0 8px; font-size:13px; color: var(--ink-2); }
.legend span { display:inline-flex; align-items:center; gap:6px; }
.legend i { width:10px; height:10px; border-radius:3px; display:inline-block; }
.tableview { margin-top:6px; font-size:13px; color: var(--ink-2); }
.tableview summary { cursor:pointer; color: var(--muted); }
.tableview table { border-collapse: collapse; margin-top:6px; font-variant-numeric: tabular-nums; }
.tableview th, .tableview td { padding:4px 10px; border-top:1px solid var(--grid); text-align:right; }
.tableview th:first-child, .tableview td:first-child { text-align:left; }
#viz-tip { position:fixed; z-index:9; pointer-events:none; background: var(--surface); color: var(--ink);
  border:1px solid var(--ring); border-radius:8px; padding:6px 9px; font: 12px system-ui, sans-serif;
  box-shadow: 0 6px 20px rgba(0,0,0,.12); display:none; white-space:nowrap; }
#viz-tip b { display:block; margin-bottom:2px; }
"""

TIP_JS = """
(function(){
  var tip = document.createElement('div'); tip.id = 'viz-tip'; document.body.appendChild(tip);
  document.addEventListener('mousemove', function(e){
    var t = e.target.closest && e.target.closest('[data-tip]');
    if (!t) { tip.style.display = 'none'; return; }
    var d = JSON.parse(t.getAttribute('data-tip'));
    tip.textContent = '';
    var b = document.createElement('b'); b.textContent = d.t; tip.appendChild(b);
    (d.r || []).forEach(function(r){ var l = document.createElement('div'); l.textContent = r; tip.appendChild(l); });
    tip.style.display = 'block';
    tip.style.left = Math.min(e.clientX + 14, window.innerWidth - tip.offsetWidth - 8) + 'px';
    tip.style.top = (e.clientY + 14) + 'px';
  });
})();
"""


def esc(s):
    return html.escape(str(s), quote=True)


# ---------- numbers ----------

def _compact(v, currency):
    a = abs(v)
    if currency == "INR":  # lakh and crore; below a lakh the full figure reads better
        for div, suf in ((1e7, " Cr"), (1e5, " L")):
            if a >= div:
                return _trim(v / div) + suf
        return _trim(v)
    for div, suf in ((1e9, "B"), (1e6, "M"), (1e3, "K")):
        if a >= div:
            return _trim(v / div) + suf
    return _trim(v)


def _trim(x):
    if abs(x) >= 100:
        return "{:,.0f}".format(x)
    s = "{:,.1f}".format(x) if abs(x) >= 10 else "{:,.2f}".format(x)
    return s.rstrip("0").rstrip(".") if "." in s else s


def _indian(n):
    s = str(int(round(abs(n))))
    if len(s) <= 3:
        out = s
    else:
        head, tail = s[:-3], s[-3:]
        parts = []
        while len(head) > 2:
            parts.insert(0, head[-2:])
            head = head[:-2]
        if head:
            parts.insert(0, head)
        out = ",".join(parts) + "," + tail
    return ("-" if n < 0 else "") + out


SYMBOL = {"INR": "₹", "USD": "$", "EUR": "€", "GBP": "£"}


def fmt(v, kind="number", currency=None, compact=True):
    """Format a value: kind is number, currency, percent (v is a fraction) or int."""
    if v is None:
        return "–"
    if kind == "percent":
        return "{:.1f}%".format(v * 100).replace(".0%", "%")
    if kind == "currency":
        sym = SYMBOL.get(currency or "USD", (currency or "") + " ")
        if compact and abs(v) >= (1e5 if currency == "INR" else 1000):
            return sym + _compact(v, currency)
        if currency == "INR":
            return sym + _indian(v)
        return sym + "{:,.0f}".format(v) if abs(v) >= 100 else sym + "{:,.2f}".format(v)
    if compact and abs(v) >= 10000:
        return _compact(v, currency)
    if float(v).is_integer():
        return _indian(v) if currency == "INR" else "{:,.0f}".format(v)
    return "{:,.2f}".format(v).rstrip("0").rstrip(".")


def fmt_delta(d, kind="percent"):
    """Signed change: a fraction shown as +12.3%, or points (pp) for rates."""
    if d is None:
        return ""
    sign = "+" if d > 0 else ("−" if d < 0 else "±")
    if kind == "pp":
        return "%s%.1f pp" % (sign, abs(d) * 100)
    return "%s%.1f%%" % (sign, abs(d) * 100)


def nice_ticks(lo, hi, n=4):
    if hi == lo:
        hi = lo + 1
    if lo > 0 and lo < hi * 0.6:
        lo = 0
    span = hi - lo
    step = 10 ** math.floor(math.log10(span / n))
    for m in (1, 2, 2.5, 5, 10):
        if span / (step * m) <= n:
            step *= m
            break
    start = math.floor(lo / step) * step
    ticks = []
    t = start
    while t <= hi + step * 0.001:
        ticks.append(round(t, 10))
        t += step
    if ticks[-1] < hi:
        ticks.append(round(ticks[-1] + step, 10))
    return ticks


# ---------- charts ----------

def _bar_color(i, j, chart):
    hx = chart.get("highlight_x")
    if hx is not None and len(chart["series"]) == 1:
        return "var(--s1)" if str(chart["x"][j]) == str(hx) else "var(--dim)"
    return _color(i, chart)


def _color(i, chart):
    hl = chart.get("highlight")
    if hl:
        name = chart["series"][i]["name"]
        return "var(--s1)" if name == hl else "var(--muted)"
    return "var(--s%d)" % (i % 8 + 1)


def legend(chart):
    series = chart.get("series", [])
    if len(series) < 2:
        return ""
    items = "".join('<span><i style="background:%s"></i>%s</span>' % (_color(i, chart), esc(s["name"]))
                    for i, s in enumerate(series))
    return '<div class="legend">%s</div>' % items


def table_view(chart):
    k, cur = chart.get("format", "number"), chart.get("currency")
    head = "<tr><th>%s</th>%s</tr>" % (esc(chart.get("x_label", "")), "".join("<th>%s</th>" % esc(s["name"]) for s in chart["series"]))
    rows = "".join("<tr><td>%s</td>%s</tr>" % (esc(x), "".join("<td>%s</td>" % esc(fmt(s["values"][j], k, cur, False)) for s in chart["series"]))
                   for j, x in enumerate(chart["x"]))
    return '<details class="tableview"><summary>Table view</summary><table>%s%s</table></details>' % (head, rows)


def _bar_path(x, y, w, h, r, horizontal=False):
    r = max(0, min(r, w / 2 if not horizontal else h / 2, h if not horizontal else w))
    if h <= 0 or w <= 0:
        return ""
    if horizontal:  # rounded at the right end
        return "M%.1f,%.1fh%.1fa%.1f,%.1f 0 0 1 %.1f,%.1fv%.1fa%.1f,%.1f 0 0 1 %.1f,%.1fh%.1fz" % (
            x, y, w - r, r, r, r, r, h - 2 * r, r, r, -r, r, -(w - r))
    return "M%.1f,%.1fv%.1fa%.1f,%.1f 0 0 1 %.1f,%.1fh%.1fa%.1f,%.1f 0 0 1 %.1f,%.1fv%.1fz" % (
        x, y + h, -(h - r), r, r, r, -r, w - 2 * r, r, r, r, r, h - r)


def _tip(title, rows):
    return esc(json.dumps({"t": str(title), "r": rows}, ensure_ascii=False))


def svg_chart(chart, width=640, height=280):
    """Draw a chart spec: type line | bar | hbar | stacked."""
    t = chart.get("type", "line")
    if t == "hbar":
        return _hbar(chart, width)
    return _xy(chart, width, height, t)


def _xy(chart, W, H, kind):
    k, cur = chart.get("format", "number"), chart.get("currency")
    xs, series = chart["x"], chart["series"]
    L, R, T, B = 52, 16, 12, 28
    bare = bool(chart.get("hide_axis")) and kind == "bar" and len(series) == 1
    if bare:
        L, T = 16, 24  # values sit on the bars, so no value axis
    if kind == "line" and len(series) <= 4:
        R = 110  # room for end labels
    n = len(xs)
    if kind == "stacked":
        tops = [sum(max(0, s["values"][j] or 0) for s in series) for j in range(n)]
        vals = tops + [0]
    else:
        vals = [v for s in series for v in s["values"] if v is not None] + ([0] if kind == "bar" else [])
    lo, hi = min(vals), max(vals)
    ticks = nice_ticks(lo, hi)
    lo, hi = ticks[0], ticks[-1]
    pw, ph = W - L - R, H - T - B
    y = lambda v: T + ph * (1 - (v - lo) / (hi - lo))
    slot = pw / max(n, 1)
    xc = lambda j: L + slot * (j + 0.5)
    out = ['<svg viewBox="0 0 %d %d" role="img" aria-label="%s">' % (W, H, esc(chart.get("title", "")))]
    for tk in ticks:
        if bare:
            if tk == 0:
                out.append('<line class="base" x1="%d" x2="%d" y1="%.1f" y2="%.1f"/>' % (L, W - R, y(tk), y(tk)))
            continue
        out.append('<line class="%s" x1="%d" x2="%d" y1="%.1f" y2="%.1f"/>' % ("base" if tk == 0 else "grid", L, W - R, y(tk), y(tk)))
        out.append('<text x="%d" y="%.1f" text-anchor="end">%s</text>' % (L - 8, y(tk) + 4, esc(fmt(tk, k, cur))))
    every = max(1, math.ceil(n / max(1, pw // 70)))
    for j, xv in enumerate(xs):
        if j % every == 0 or j == n - 1 and n <= 12:
            out.append('<text x="%.1f" y="%d" text-anchor="middle">%s</text>' % (xc(j), H - 8, esc(xv)))
    if kind == "line":
        for i, s in enumerate(series):
            pts = [(xc(j), y(v)) for j, v in enumerate(s["values"]) if v is not None]
            if not pts:
                continue
            c = _color(i, chart)
            out.append('<polyline fill="none" stroke="%s" stroke-width="2" stroke-linejoin="round" stroke-linecap="round" points="%s"/>' % (
                c, " ".join("%.1f,%.1f" % p for p in pts)))
            ex, ey = pts[-1]
            out.append('<circle cx="%.1f" cy="%.1f" r="4" fill="%s" stroke="var(--surface)" stroke-width="2"/>' % (ex, ey, c))
            hx = chart.get("highlight_x")
            sx = [str(v) for v in xs]
            if hx is not None and len(series) == 1 and str(hx) in sx:
                j = sx.index(str(hx))
                v = s["values"][j]
                if v is not None:  # mark the point the headline is about
                    out.append('<circle class="hlpt" cx="%.1f" cy="%.1f" r="7" fill="var(--s1)" stroke="var(--surface)" stroke-width="3"/>' % (xc(j), y(v)))
                    out.append('<text class="lbl hl" x="%.1f" y="%.1f" text-anchor="middle">%s</text>' % (xc(j), y(v) - 16, esc(fmt(v, k, cur))))
        if len(series) <= 4:  # direct end labels when they do not collide
            ends = sorted(((y(s["values"][-1]), s) for s in series if s["values"] and s["values"][-1] is not None), key=lambda e: e[0])
            if all(b[0] - a[0] >= 14 for a, b in zip(ends, ends[1:])):
                for ey, s in ends:
                    lab = (s["name"] + " " if len(series) > 1 else "") + fmt(s["values"][-1], k, cur)
                    out.append('<text class="lbl" x="%.1f" y="%.1f">%s</text>' % (W - R + 10, ey + 4, esc(lab)))
    elif kind == "bar":
        m = len(series)
        bw = min(chart.get("bar_max", 24), slot * 0.7 / m)
        gap = 2 if m > 1 else 0
        for j in range(n):
            x0 = xc(j) - (bw * m + gap * (m - 1)) / 2
            for i, s in enumerate(series):
                v = s["values"][j]
                if v is None:
                    continue
                y0, y1 = y(max(v, 0)), y(min(v, 0))
                d = _bar_path(x0 + i * (bw + gap), y0, bw, max(1, y1 - y0), chart.get("bar_radius", 4))
                if v < 0:  # round the data end, which is the bottom
                    cx, cy = x0 + i * (bw + gap) + bw / 2, (y0 + y1) / 2
                    d = '%s" transform="rotate(180 %.1f %.1f)' % (d, cx, cy)
                out.append('<path fill="%s" d="%s"/>' % (_bar_color(i, j, chart), d))
                if m == 1 and n <= 12:  # value at the tip of each bar
                    ty = y0 - 7 if v >= 0 else y1 + 15
                    out.append('<text class="lbl" x="%.1f" y="%.1f" text-anchor="middle">%s</text>' % (
                        x0 + bw / 2, ty, esc(fmt(v, k, cur))))
    elif kind == "stacked":
        bw = min(24, slot * 0.6)
        for j in range(n):
            acc = 0
            segs = [(i, s["values"][j]) for i, s in enumerate(series) if (s["values"][j] or 0) > 0]
            for q, (i, v) in enumerate(segs):
                y0, y1 = y(acc + v), y(acc)
                acc += v
                h = y1 - y0 - (2 if q > 0 else 0)
                r = 4 if q == len(segs) - 1 else 0
                out.append('<path fill="%s" d="%s"/>' % (_color(i, chart), _bar_path(xc(j) - bw / 2, y0, bw, max(1, h), r)))
    for j, xv in enumerate(xs):  # hover targets
        rows = ["%s: %s" % (s["name"], fmt(s["values"][j], k, cur, False)) for s in series]
        out.append('<rect class="hit" x="%.1f" y="%d" width="%.1f" height="%d" data-tip="%s"/>' % (
            L + slot * j, T, slot, ph, _tip(xv, rows)))
    out.append("</svg>")
    return "".join(out)


def _hbar(chart, W):
    k, cur = chart.get("format", "number"), chart.get("currency")
    s = chart["series"][0]
    cats = chart["x"]
    label_w = min(220, 12 + 7 * max(len(str(c)) for c in cats))
    row, bh = 32, 18
    H = row * len(cats) + 8
    vmax = max(v for v in s["values"] if v is not None) or 1
    pw = W - label_w - 90
    out = ['<svg viewBox="0 0 %d %d" role="img" aria-label="%s">' % (W, H, esc(chart.get("title", "")))]
    out.append('<line class="base" x1="%d" x2="%d" y1="0" y2="%d"/>' % (label_w, label_w, H))
    for j, (c, v) in enumerate(zip(cats, s["values"])):
        yc = 4 + j * row + row / 2
        out.append('<text class="lbl" x="%d" y="%.1f" text-anchor="end">%s</text>' % (label_w - 10, yc + 4, esc(c)))
        if v is None:
            continue
        w = max(1, pw * v / vmax)
        hl = chart.get("highlight_x", chart.get("highlight"))
        col = "var(--s1)" if not hl or c == hl else "var(--dim)"
        out.append('<path fill="%s" d="%s"/>' % (col, _bar_path(label_w, yc - bh / 2, w, bh, 4, True)))
        out.append('<text class="lbl" x="%.1f" y="%.1f">%s</text>' % (label_w + w + 8, yc + 4, esc(fmt(v, k, cur))))
        out.append('<rect class="hit" x="0" y="%.1f" width="%d" height="%d" data-tip="%s"/>' % (
            yc - row / 2, W, row, _tip(c, ["%s: %s" % (s["name"], fmt(v, k, cur, False))])))
    out.append("</svg>")
    return "".join(out)


def chart_block(chart, width=640, height=280):
    """Legend + SVG + table view for one chart."""
    return '<div class="chart">%s%s%s</div>' % (legend(chart), svg_chart(chart, width, height), table_view(chart))


def validate_chart(c):
    """Problems with a chart spec (empty list when fine)."""
    p = []
    cid = c.get("id") or c.get("title", "?")
    if c.get("type", "line") not in ("line", "bar", "hbar", "stacked"):
        p.append("%s: type must be line, bar, hbar or stacked (one axis only)" % cid)
    if not c.get("title"):
        p.append("%s: needs a title that states the finding" % cid)
    xs = c.get("x") or []
    ser = c.get("series") or []
    if not xs or not ser:
        p.append("%s: needs x values and at least one series" % cid)
    for s in ser:
        if len(s.get("values", [])) != len(xs):
            p.append("%s: series %r has %d values for %d x values" % (cid, s.get("name"), len(s.get("values", [])), len(xs)))
    if len(ser) > 8:
        p.append("%s: %d series; fold the smallest into 'Other' (8 at most, ideally 4)" % (cid, len(ser)))
    if c.get("type") == "hbar" and len(ser) != 1:
        p.append("%s: hbar takes exactly one series" % cid)
    return p


def numbers_in(obj):
    """Every number inside a spec (for tracing text back to computed values)."""
    out = []
    if isinstance(obj, bool):
        return out
    if isinstance(obj, (int, float)):
        out.append(float(obj))
    elif isinstance(obj, dict):
        for v in obj.values():
            out += numbers_in(v)
    elif isinstance(obj, list):
        for v in obj:
            out += numbers_in(v)
    return out


def run_analysis(script):
    """Run the user's analysis script and return the JSON spec it prints."""
    res = subprocess.run([sys.executable, script], capture_output=True, text=True, timeout=600,
                         cwd=os.path.dirname(os.path.abspath(script)) or ".")
    if res.returncode != 0:
        sys.exit("analysis failed:\n" + res.stderr[-4000:])
    out, dec = res.stdout, json.JSONDecoder()
    for i, ch in enumerate(out):  # the first complete JSON object, ignoring other prints
        if ch == "{":
            try:
                obj = dec.raw_decode(out, i)[0]
            except json.JSONDecodeError:
                continue
            if isinstance(obj, dict):
                return obj
    sys.exit("analysis printed no JSON object")
