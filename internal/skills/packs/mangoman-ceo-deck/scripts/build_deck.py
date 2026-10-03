"""Run the analysis script and build the deck from what it prints.

Usage: python3 build_deck.py analysis.py --out deck [--no-pdf] [--no-pptx]
Writes <out>.html (all slides, 1280x720 each), <out>.pdf (one slide per page),
<out>.pptx (editable, native charts and tables; needs python-pptx) and
<out>.spec.json.

Design: each slide is laid out once as a scene (shapes, text, a chart or a
table, placed in inches on a 13.333 x 7.5 inch canvas). The same scene is drawn
to PowerPoint and to HTML/PDF, so the two match. Dark title, stat, section and
closing slides frame light content slides; one motif (a large soft circle)
(orb, rings or dots) repeats on the dark slides; charts grey out everything except the category the
headline is about.
"""
import html
import json
import os
import re
import subprocess
import sys

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
import vizlib as V  # noqa: E402
import render  # noqa: E402
import brandkit as BK  # noqa: E402

SLIDE_TYPES = ("title", "answer", "kpis", "stat", "chart", "bullets", "table", "next_steps", "section")
W, H, M = 13.333, 7.5, 0.75  # canvas and side margin, inches
CW = W - 2 * M  # content width
BASE_DIR = "."  # folder of the analysis script; logo paths are relative to it

SERIES_EXTRA = ["3B82C4", "1BAF7A", "E87BA4", "4A3AA7", "8A8DA0", "E34948", "008300"]


def run_analysis(script):
    res = subprocess.run([sys.executable, script], capture_output=True, text=True, timeout=600,
                         cwd=os.path.dirname(os.path.abspath(script)) or ".")
    if res.returncode != 0:
        sys.exit("analysis failed:\n" + res.stderr[-4000:])
    out = res.stdout.strip()
    start = out.find("{")
    if start < 0:
        sys.exit("analysis printed no JSON object")
    try:
        return json.loads(out[start:])
    except json.JSONDecodeError as e:
        sys.exit("analysis output is not valid JSON: %s" % e)


def point_text(p):
    return p.get("text", "") if isinstance(p, dict) else str(p)


def validate(spec):
    p = []
    slides = spec.get("slides", [])
    if not spec.get("title"):
        p.append("deck needs a title")
    if not slides:
        p.append("deck has no slides")
    if spec.get("theme", "ink") not in BK.CURATED and spec.get("theme") != "brand":
        p.append("theme must be one of %s, or give brand colours" % ", ".join(BK.CURATED))
    for key, allowed in (("motif", BK.MOTIFS), ("mode", BK.MODES), ("type", tuple(BK.TYPES))):
        if spec.get(key) is not None and spec[key] not in allowed:
            p.append("%s must be one of %s" % (key, ", ".join(allowed)))
    b = spec.get("brand")
    if b is not None:
        if not isinstance(b, dict):
            p.append("brand must be an object: primary, accent, logo")
        else:
            for key in ("primary", "accent"):
                if b.get(key):
                    try:
                        BK.hexc(b[key])
                    except ValueError:
                        p.append("brand %s must be a hex colour like #1F5FA8" % key)
            if b.get("logo"):
                prob = BK.logo_check(logo_path(b["logo"]))
                if prob:
                    p.append(prob)
    for i, s in enumerate(slides, 1):
        t = s.get("type")
        if t not in SLIDE_TYPES:
            p.append("slide %d: type must be one of %s" % (i, ", ".join(SLIDE_TYPES)))
            continue
        if t != "title" and not s.get("headline"):
            p.append("slide %d: needs a headline (the takeaway, as a sentence)" % i)
        if t == "chart":
            p += ["slide %d: %s" % (i, x) for x in V.validate_chart(dict(s.get("chart", {}), title=s.get("headline", "")))]
            co = s.get("callout")
            if co is not None and not isinstance(co.get("value") if isinstance(co, dict) else None, (int, float)):
                p.append("slide %d: callout needs a numeric value" % i)
        if t == "kpis" and not (1 <= len(s.get("kpis", [])) <= 4):
            p.append("slide %d: 1 to 4 KPIs per slide" % i)
        if t == "stat" and not isinstance(s.get("value"), (int, float)):
            p.append("slide %d: a stat slide needs a numeric value (computed in the script)" % i)
        if t == "answer" and not (1 <= len(s.get("points", [])) <= 3):
            p.append("slide %d: the answer has 1 to 3 points" % i)
        if t == "next_steps" and not (1 <= len(s.get("items", [])) <= 4):
            p.append("slide %d: 1 to 4 next steps" % i)
        if t == "table" and not s.get("table", {}).get("rows"):
            p.append("slide %d: table has no rows" % i)
    return p


# ---------- scene helpers ----------

blend = BK.mix  # fg at opacity a over bg, as an opaque hex colour (same in both outputs)


def logo_path(p):
    return p if os.path.isabs(p) else os.path.join(BASE_DIR, p)


def run(t, size, color, bold=False):
    return {"t": str(t), "size": size, "color": color, "bold": bold}


def text(x, y, w, h, paras, align="l", valign="t", check=None, lh=1.12):
    """paras: a list of paragraphs, each a list of runs (or one run)."""
    ps = []
    for p in paras:
        if isinstance(p, dict) and "runs" in p:
            ps.append(p)
        else:
            ps.append({"runs": p if isinstance(p, list) else [p]})
    return {"k": "text", "x": x, "y": y, "w": w, "h": h, "paras": ps, "align": align, "valign": valign, "check": check, "lh": lh}


def rect(x, y, w, h, fill, r=0.0):
    return {"k": "rect", "x": x, "y": y, "w": w, "h": h, "fill": fill, "r": r}


def circle(x, y, d, fill):
    return {"k": "circle", "x": x, "y": y, "d": d, "fill": fill}


def ring(x, y, d, color, width=1.5):
    return {"k": "ring", "x": x, "y": y, "d": d, "color": color, "width": width}


def image(path, x, y, w, h):
    return {"k": "image", "path": path, "x": x, "y": y, "w": w, "h": h}


def hline(x, y, w, color):
    return rect(x, y, w, 0.014, color)


def vline(x, y, h, color):
    return rect(x, y, 0.014, h, color)


NUM_RX = re.compile(r"(?<![A-Za-z\d])[−+-]?[₹$€£]?\s?\d[\d,]*(?:\.\d+)?(?:\s?(?:%|pp|Cr|crore|lakh|L|K|M|B|bn|x)(?![A-Za-z]))?")


def lead_stat(point):
    if isinstance(point, dict) and point.get("stat"):
        return str(point["stat"])
    m = NUM_RX.search(point_text(point))
    return m.group(0).strip() if m else ""


def auto_highlight(headline, xs):
    """The one category the headline names (Aug matches August), else None."""
    h = (headline or "").lower()
    hits = [x for x in xs if len(str(x)) >= 2 and re.search(r"(?<![a-z0-9])" + re.escape(str(x).lower()), h)]
    return hits[0] if len(hits) == 1 else None


def est_width(s, size):
    """Rough Calibri width in inches (used for pills only)."""
    return len(s) * size * 0.52 / 72


# ---------- layouts ----------

def logo(T, bg, x, y, max_w, max_h, right=False):
    """The brand logo fitted in a box; on a white chip when it would not show on bg."""
    path = T.get("logo")
    if not path:
        return [], 0
    w, h = BK.fit(path, max_w, max_h)
    if right:
        x = x - w
    els = []
    if BK.logo_hidden_share(path, bg) > 0.15:  # part of the logo would vanish on this background
        chip = "FFFFFF" if BK.luminance(bg) < 0.4 else T["dark"]
        pad = min(0.12, h * 0.35)
        els.append(rect(x - pad, y - pad, w + 2 * pad, h + 2 * pad, chip, r=min(0.08, (h + 2 * pad) / 2)))
    els.append(image(path, x, y, w, h))
    return els, w


def footer(spec, n, total, T, dark=False, bg=None):
    c = T["on_dark_muted"] if dark else T["muted"]
    lg, lw = logo(T, bg or (T["dark"] if dark else T["bg"]), W - M, 6.99, 1.3, 0.3, right=True)
    nx = W - M - (lw + 0.25 if lw else 0)
    return lg + [text(M, 7.0, 8.5, 0.3, [run(spec.get("source", spec.get("title", "")), 10, c)], valign="m"),
                 text(nx - 1.5, 7.0, 1.5, 0.3, [run("%d / %d" % (n, total), 10, c)], align="r", valign="m")]


def motif(T, base, big=(8.4, -1.7, 7.6), small=None, a=0.06):
    """The deck's one motif, in the style the spec chose (orb, rings, dots)."""
    x, y, d = big
    ink = T.get("motif_base", "FFFFFF")
    style = T.get("motif", "orb")
    els = []
    if style == "rings":
        cx, cy = x + d / 2, y + d / 2
        for k, f in enumerate((1.0, 0.78, 0.56, 0.34)):
            dd = d * f
            els.append(ring(cx - dd / 2, cy - dd / 2, dd, blend(ink, base, a * (2.2 if k else 2.6)), 1.25))
    elif style == "dots":
        cols, rows, gap = 7, 6, 0.34
        x0, y0 = W - 0.55 - gap * (cols - 1), 0.5  # always the top-right corner
        for r_ in range(rows):
            for c_ in range(cols):
                els.append(circle(x0 + c_ * gap, y0 + r_ * gap, 0.07, blend(ink, base, 0.18 if (r_ + c_) % 3 else 0.3)))
    else:
        els.append(circle(x, y, d, blend(ink, base, a)))
    if small:
        sx, sy, sd = small
        els.append(circle(sx, sy, sd, T["accent"]))
    return els


def headline_top(s, n, T, size=28, color=None, w=CW):
    return text(M, 0.55, w, 1.15, [run(s["headline"], size, color or T["text"], True)], valign="b", check="headline-%d" % n)


def headline_left(s, n, T, w=4.4, size=30):
    return text(M, 0.95, w, 5.6, [run(s["headline"], size, T["text"], True)], valign="m", check="headline-%d" % n, lh=1.0)


def scene_title(s, spec, n, total, T):
    els = motif(T, T["dark"], big=(8.3, -1.9, 7.8), small=(7.75, 5.55, 0.7))
    if T.get("motif", "orb") == "orb":
        els.append(circle(10.9, 4.3, 3.6, blend(T.get("motif_base", "FFFFFF"), T["dark"], 0.035)))
    els += logo(T, T["dark"], M, 0.65, 2.4, 0.6)[0]
    els.append(text(M, 1.6, 7.6, 2.75, [run(s.get("title") or spec["title"], 46, T["on_dark"], True)], valign="b", check="title-%d" % n, lh=1.04))
    sub = s.get("subtitle") or spec.get("subtitle")
    if sub:
        els.append(text(M, 4.5, 7.6, 0.9, [run(sub, 20, T["on_dark2"])], check="subtitle-%d" % n))
    if spec.get("date"):
        els.append(text(M, 5.55, 6.5, 0.5, [run(spec["date"], 14, T["accent"], True)], valign="m"))
    return {"bg": T["dark"], "els": els}


def scene_section(s, spec, n, total, T):
    els = motif(T, T["dark2"], big=(7.9, 1.1, 7.2), small=(7.6, 5.9, 0.55))
    els.append(text(M, 2.2, 8.6, 2.8, [run(s["headline"], 40, T["on_dark"], True)], valign="m", check="headline-%d" % n, lh=1.06))
    return {"bg": T["dark2"], "els": els + footer(spec, n, total, T, True)}


def scene_stat(s, spec, n, total, T):
    els = motif(T, T["dark"], big=(9.1, -2.3, 7.0))
    val = V.fmt(s["value"], s.get("format", "number"), s.get("currency") or spec.get("currency"))
    els.append(text(M, 0.9, 11.0, 2.9, [run(val, 120, T["accent"], True)], valign="b", check="stat-%d" % n, lh=1.0))
    els.append(text(M, 4.05, 9.6, 1.7, [run(s["headline"], 28, T["on_dark"], True)], check="headline-%d" % n))
    if s.get("note"):
        els.append(text(M, 5.85, 9.6, 0.8, [run(s["note"], 15, T["on_dark_muted"])], check="note-%d" % n))
    return {"bg": T["dark"], "els": els + footer(spec, n, total, T, True)}


def scene_answer(s, spec, n, total, T):
    els = [headline_left(s, n, T)]
    pts = s.get("points", [])
    x0 = 5.75
    els.append(vline(5.35, 1.25, 5.0, T["grid"]))
    rh = 1.7
    y0 = 1.2 + (3 - len(pts)) * rh / 2
    for i, p in enumerate(pts):
        y = y0 + i * rh
        if i:
            els.append(hline(x0, y, W - M - x0, T["grid"]))
        st = lead_stat(p)
        if st:
            els.append(text(x0, y + 0.15, 2.45, rh - 0.3, [run(st, 34, T["accent_dark"], True)], valign="m", check="stat-%d-%d" % (n, i)))
        tx = x0 + (2.65 if st else 0)
        els.append(text(tx, y + 0.15, W - M - tx, rh - 0.3, [run(point_text(p), 17, T["body"])], valign="m", check="point-%d-%d" % (n, i), lh=1.18))
    return {"bg": T["bg"], "els": els + footer(spec, n, total, T)}


def scene_kpis(s, spec, n, total, T):
    els = [headline_top(s, n, T)]
    ks = s["kpis"]
    cw = CW / len(ks)
    vsize = 56 if len(ks) <= 3 else 46
    y = 2.45
    for i, k in enumerate(ks):
        x = M + i * cw
        pad = 0 if i == 0 else 0.4
        if i:
            els.append(vline(x, y, 3.2, T["grid"]))
        iw = cw - pad - 0.2
        els.append(text(x + pad, y, iw, 0.45, [run(k["label"], 15, T["muted"], True)], valign="m"))
        els.append(text(x + pad, y + 0.5, iw, 1.2, [run(V.fmt(k["value"], k.get("format", "number"), k.get("currency")), vsize, T["text"], True)],
                        valign="m", check="kpi-%d-%d" % (n, i), lh=1.0))
        if isinstance(k.get("delta"), (int, float)):
            dv = k["delta"]
            good = (dv >= 0) == k.get("up_is_good", True)
            col = T["muted"] if dv == 0 else (T["good"] if good else T["bad"])
            dt = V.fmt_delta(dv, k.get("delta_kind", "percent"))
            pw = est_width(dt, 13) + 0.36
            els.append(rect(x + pad, y + 1.95, pw, 0.42, blend(col, T["bg"], 0.13), r=0.21))
            els.append(text(x + pad, y + 1.95, pw, 0.42, [run(dt, 13, col, True)], align="c", valign="m"))
            if k.get("delta_label"):
                els.append(text(x + pad, y + 2.5, iw, 0.4, [run(k["delta_label"], 12, T["muted"])], valign="m"))
    return {"bg": T["bg"], "els": els + footer(spec, n, total, T)}


def chart_view(s, T):
    """The chart spec as drawn on a slide, plus the side-panel callout."""
    c = dict(s["chart"])
    kind = c.get("type", "line")
    single = len(c["series"]) == 1
    if "highlight_x" not in c and single and kind in ("bar", "hbar", "line"):
        hx = auto_highlight(s.get("headline"), c["x"])
        if hx is not None:
            c["highlight_x"] = hx
    if c.get("highlight_x") is False:
        c.pop("highlight_x")
    if kind == "bar" and single and len(c["x"]) <= 12:
        c["hide_axis"] = True
    callout = None
    k, cur = c.get("format", "number"), c.get("currency")
    if isinstance(s.get("callout"), dict):
        co = s["callout"]
        callout = (co.get("label", ""), V.fmt(co["value"], co.get("format", k), co.get("currency", cur)))
    elif single and c.get("highlight_x") is not None and str(c["highlight_x"]) in [str(x) for x in c["x"]]:
        j = [str(x) for x in c["x"]].index(str(c["highlight_x"]))
        callout = (str(c["x"][j]), V.fmt(c["series"][0]["values"][j], k, cur))
    elif single and kind == "line" and c["series"][0]["values"]:
        callout = ("Latest, %s" % c["x"][-1], V.fmt(c["series"][0]["values"][-1], k, cur))
    return c, callout


def scene_chart(s, spec, n, total, T):
    els = [headline_top(s, n, T)]
    c, callout = chart_view(s, T)
    side = callout or s.get("takeaway")
    cy, ch = 1.95, 4.75
    cwid = 8.35 if side else CW
    els.append({"k": "chart", "x": M, "y": cy, "w": cwid, "h": ch, "chart": c})
    if side:
        px = M + cwid + 0.35
        pw = W - M - px
        els.append(rect(px, cy, pw, ch, T["tint"], r=0.14))
        iy = cy + 0.4 if s.get("takeaway") else cy + (ch - 1.45) / 2
        if callout:
            els.append(text(px + 0.35, iy, pw - 0.7, 0.4, [run(callout[0], 14, T["muted"], True)], valign="m", check="callout-label-%d" % n))
            els.append(text(px + 0.35, iy + 0.45, pw - 0.7, 1.0, [run(callout[1], 44, T["accent_dark"], True)], valign="m", check="callout-%d" % n, lh=1.0))
            iy += 1.75
        if s.get("takeaway"):
            els.append(text(px + 0.35, iy, pw - 0.7, cy + ch - iy - 0.35, [run(s["takeaway"], 15, T["body"])], check="takeaway-%d" % n, lh=1.22))
    return {"bg": T["bg"], "els": els + footer(spec, n, total, T)}


def scene_bullets(s, spec, n, total, T):
    els = [headline_left(s, n, T)]
    bs = s.get("bullets", [])
    els.append(vline(5.35, 1.25, 5.0, T["grid"]))
    rh = min(1.15, 5.2 / max(1, len(bs)))
    y0 = 1.15 + (5.2 - rh * len(bs)) / 2
    for i, b in enumerate(bs):
        y = y0 + i * rh
        els.append(circle(5.8, y + rh / 2 - 0.08, 0.16, T["accent_fill"]))
        els.append(text(6.25, y, W - M - 6.25, rh, [run(b, 18, T["body"])], valign="m", check="bullet-%d-%d" % (n, i), lh=1.18))
    return {"bg": T["bg"], "els": els + footer(spec, n, total, T)}


def col_widths(n):
    first = 0.42 if n <= 3 else 0.32
    return [first] + [(1 - first) / (n - 1)] * (n - 1) if n > 1 else [1]


def table_rows(t):
    cols = t["columns"]
    fm = t.get("formats") or ["number"] * len(cols)
    isnum = [all(isinstance(r[i], (int, float)) for r in t["rows"]) for i in range(len(cols))]
    body = [[V.fmt(v, fm[i], t.get("currency"), False) if isinstance(v, (int, float)) else str(v) for i, v in enumerate(r)] for r in t["rows"]]
    return [str(c) for c in cols], body, isnum


def scene_table(s, spec, n, total, T):
    t = s["table"]
    cols = t["columns"]
    split = len(cols) <= 3
    hl = s.get("highlight_row", t.get("highlight_row"))
    if hl is None:
        hits = [i for i, r in enumerate(t["rows"]) if str(r[0]).lower() in (s["headline"] or "").lower()]
        hl = hits[0] if len(hits) == 1 else None
    elif isinstance(hl, str):
        hl = next((i for i, r in enumerate(t["rows"]) if str(r[0]) == hl), None)
    if split:
        els = [headline_left(s, n, T)]
        tx, ty, tw, avail = 5.75, 1.25, W - M - 5.75, 5.0
    else:
        els = [headline_top(s, n, T)]
        tx, ty, tw, avail = M, 2.0, CW, 4.6
    rows = len(t["rows"]) + 1
    rh = min(0.62, avail / rows)
    th = rh * rows
    if split:
        ty = 1.15 + (5.2 - th) / 2
    els.append({"k": "table", "x": tx, "y": ty, "w": tw, "h": th, "rh": rh, "table": t, "hl": hl})
    return {"bg": T["bg"], "els": els + footer(spec, n, total, T)}


def scene_next(s, spec, n, total, T):
    els = [] if T.get("motif") == "dots" else motif(T, T["dark"], big=(8.9, 3.6, 6.4), a=0.04)
    els.append(headline_top(s, n, T, size=30, color=T["on_dark"]))
    items = s.get("items", [])
    gap = 0.3
    cw = (CW - gap * (len(items) - 1)) / len(items)
    y, h = 2.2, 4.1
    for i, it in enumerate(items):
        x = M + i * (cw + gap)
        els.append(rect(x, y, cw, h, T["dark2"], r=0.14))
        els.append(circle(x + 0.35, y + 0.4, 0.62, T["accent"]))
        num = "FFFFFF" if BK.contrast("FFFFFF", T["accent"]) > BK.contrast(T["text"], T["accent"]) else T["text"]
        els.append(text(x + 0.35, y + 0.4, 0.62, 0.62, [run(i + 1, 18, num, True)], align="c", valign="m"))
        els.append(text(x + 0.35, y + 1.3, cw - 0.7, 1.75, [run(it.get("action", ""), 19, T["on_dark"], True)], check="step-%d-%d" % (n, i), lh=1.15))
        if it.get("owner"):
            els.append(text(x + 0.35, y + h - 0.95, cw - 0.7, 0.35, [run(it["owner"], 13, T["on_dark_muted"])], valign="m"))
        if it.get("date"):
            els.append(text(x + 0.35, y + h - 0.6, cw - 0.7, 0.35, [run(it["date"], 13, T["accent"], True)], valign="m"))
    return {"bg": T["dark"], "els": els + footer(spec, n, total, T, True)}


SCENES = {"title": scene_title, "section": scene_section, "stat": scene_stat, "answer": scene_answer, "kpis": scene_kpis,
          "chart": scene_chart, "bullets": scene_bullets, "table": scene_table, "next_steps": scene_next}


DARK_SLIDES = ("title", "section", "stat", "next_steps")


def resolve_theme(spec):
    """Tokens for the deck: brand colours (or colours read from the logo), else a curated theme."""
    T, note = BK.resolve_spec(spec, BASE_DIR)
    T["note"] = note
    return T


def scenes(spec):
    T = resolve_theme(spec)
    D = BK.light_mode(T) if T["mode"] == "light" else T
    sl = spec["slides"]
    return T, [SCENES[s["type"]](s, spec, i + 1, len(sl), D if s["type"] in DARK_SLIDES else T) for i, s in enumerate(sl)]


# ---------- HTML / PDF ----------

PX = 96
e = lambda s: html.escape(str(s), quote=True)


def is_head(r):
    """Headlines, titles and big numbers use the heading font."""
    return r["bold"] and r["size"] >= 24


def box(el):
    return "left:%.1fpx;top:%.1fpx;width:%.1fpx;height:%.1fpx" % (el["x"] * PX, el["y"] * PX, el["w"] * PX, el["h"] * PX)


def html_el(el, T, bg):
    k = el["k"]
    if k == "rect":
        return '<div class="el" style="%s;background:#%s;border-radius:%.1fpx"></div>' % (box(el), el["fill"], el["r"] * PX)
    if k == "circle":
        d = el["d"] * PX
        return '<div class="el" style="left:%.1fpx;top:%.1fpx;width:%.1fpx;height:%.1fpx;background:#%s;border-radius:50%%"></div>' % (
            el["x"] * PX, el["y"] * PX, d, d, el["fill"])
    if k == "ring":
        d = el["d"] * PX
        return '<div class="el" style="left:%.1fpx;top:%.1fpx;width:%.1fpx;height:%.1fpx;border:%.2fpx solid #%s;border-radius:50%%"></div>' % (
            el["x"] * PX, el["y"] * PX, d, d, el["width"] * 4 / 3, el["color"])
    if k == "image":
        import base64
        ext = os.path.splitext(el["path"])[1].lower().lstrip(".").replace("jpg", "jpeg")
        with open(el["path"], "rb") as f:
            data = base64.b64encode(f.read()).decode()
        return '<img class="el" alt="logo" style="%s" src="data:image/%s;base64,%s">' % (box(el), ext, data)
    if k == "text":
        jc = {"t": "flex-start", "m": "center", "b": "flex-end"}[el["valign"]]
        ta = {"l": "left", "r": "right", "c": "center"}[el["align"]]
        ps = []
        for p in el["paras"]:
            runs = "".join('<span style="font-size:%gpt;color:#%s;font-weight:%d;font-family:%s">%s</span>' % (
                r["size"], r["color"], 700 if r["bold"] else 400, e(T["html_head"] if is_head(r) else T["html_body"]), e(r["t"])) for r in p["runs"])
            mx = max(r["size"] for r in p["runs"])
            ps.append('<p style="line-height:%.2fpt;margin-bottom:%gpt">%s</p>' % (mx * el["lh"] * 1.2, p.get("after", 0), runs))
        chk = ' data-check="%s"' % e(el["check"]) if el.get("check") else ""
        return '<div class="el tx"%s style="%s;justify-content:%s;text-align:%s">%s</div>' % (chk, box(el), jc, ta, "".join(ps))
    if k == "chart":
        c = el["chart"]
        cls = "chart deckchart"
        if c.get("type") == "line" and c.get("highlight_x") is not None and len(c["series"]) == 1:
            cls += " hlline"
        leg = V.legend(c)
        h = el["h"] * PX - (30 if leg else 0)
        c2 = dict(c, bar_max=64, bar_radius=6)
        return '<div class="el %s" style="%s">%s%s</div>' % (cls, box(el), leg, V.svg_chart(c2, int(el["w"] * PX), int(h)))
    if k == "table":
        t = el["table"]
        cols, body, isnum = table_rows(t)
        rh = el["rh"] * PX
        head = "".join('<th class="%s">%s</th>' % ("n" if isnum[i] else "", e(c)) for i, c in enumerate(cols))
        rows = "".join('<tr class="%s">%s</tr>' % ("hl" if el["hl"] == ri else "", "".join(
            '<td class="%s">%s</td>' % ("n" if isnum[i] else "", e(v)) for i, v in enumerate(r))) for ri, r in enumerate(body))
        cg = "<colgroup>%s</colgroup>" % "".join('<col style="width:%.2f%%">' % (wf * 100) for wf in col_widths(len(cols)))
        return '<div class="el" style="%s"><table class="dt" style="--rh:%.1fpx">%s%s%s</table></div>' % (box(el), rh, cg, "<tr>%s</tr>" % head, rows)
    return ""


def build_html(spec):
    T, sc = scenes(spec)
    css = """
* { box-sizing: border-box; margin: 0; padding: 0; }
@page { size: 1280px 720px; margin: 0; }
html, body { background: #d9dce3; }
body { font-family: %(font)s; padding: 24px 0; -webkit-print-color-adjust: exact; print-color-adjust: exact; }
.slide { width: 1280px; height: 720px; margin: 0 auto 24px; position: relative; overflow: hidden; box-shadow: 0 2px 14px rgba(20,33,61,.12); }
@media print { html, body { background: none; padding: 0; } .slide { margin: 0; box-shadow: none; break-after: page; } }
.el { position: absolute; }
.tx { display: flex; flex-direction: column; overflow: hidden; }
.tx p { white-space: pre-wrap; overflow-wrap: break-word; }
.deckchart { --s1:#%(accent)s; --dim:#%(dim)s; --muted:#%(muted)s; --ink:#%(text)s; --ink-2:#%(body)s; --grid:#%(grid)s; --axis:#%(dim)s; --surface:#%(bg)s; }
.deckchart text { font: 15px %(font)s !important; fill: #%(muted)s; }
.deckchart text.lbl { fill: #%(body)s; font-weight: 700 !important; }
.deckchart text.hl { fill: #%(accent_dark)s; font-size: 17px !important; }
.deckchart polyline { stroke-width: 3.5px; }
.deckchart.hlline polyline { stroke: #%(dim)s; }
.deckchart.hlline circle:not(.hlpt) { fill: #%(dim)s; }
.deckchart.hlline text.lbl:not(.hl) { display: none; }
.deckchart .legend { font-size: 15px; color: #%(body)s; }
table.dt { border-collapse: collapse; width: 100%%; table-layout: fixed; font-variant-numeric: tabular-nums; }
table.dt th, table.dt td { height: var(--rh); padding: 0 14px; border-bottom: 1px solid #%(grid)s; text-align: left; vertical-align: middle; }
table.dt th { font-size: 12pt; font-weight: 700; color: #%(muted)s; }
table.dt td { font-size: 15pt; color: #%(body)s; }
table.dt th:first-child, table.dt td:first-child { padding-left: 0; }
table.dt th:last-child, table.dt td:last-child { padding-right: 0; }
table.dt .n { text-align: right; }
table.dt tr.hl td { color: #%(text)s; font-weight: 700; }
table.dt tr.hl td:first-child { color: #%(accent_dark)s; }
""" % dict(T, font=T["html_body"], accent=T["accent_fill"])
    slides = "".join('<section class="slide" id="s%d" style="background:#%s">%s</section>' % (
        i + 1, s["bg"], "".join(html_el(x, T, s["bg"]) for x in s["els"])) for i, s in enumerate(sc))
    return '<!doctype html><html lang="en"><head><meta charset="utf-8"><title>%s</title><style>%s%s</style></head><body>%s<script>%s</script></body></html>' % (
        e(spec["title"]), V.CHART_CSS, css, slides, V.TIP_JS)


# ---------- PowerPoint ----------

def build_pptx(spec, path):
    try:
        from pptx import Presentation
        from pptx.chart.data import CategoryChartData
        from pptx.dml.color import RGBColor
        from pptx.enum.chart import XL_CHART_TYPE, XL_LEGEND_POSITION, XL_TICK_LABEL_POSITION, XL_LABEL_POSITION, XL_MARKER_STYLE
        from pptx.enum.shapes import MSO_SHAPE
        from pptx.enum.text import PP_ALIGN, MSO_ANCHOR, MSO_AUTO_SIZE
        from pptx.oxml.ns import qn
        from pptx.util import Inches, Pt
        from lxml import etree
    except ImportError:
        return False
    T, sc = scenes(spec)
    rgb = lambda h: RGBColor.from_string(h)
    prs = Presentation()
    prs.slide_width, prs.slide_height = Inches(W), Inches(H)
    blank = prs.slide_layouts[6]
    ALIGN = {"l": PP_ALIGN.LEFT, "r": PP_ALIGN.RIGHT, "c": PP_ALIGN.CENTER}
    ANCHOR = {"t": MSO_ANCHOR.TOP, "m": MSO_ANCHOR.MIDDLE, "b": MSO_ANCHOR.BOTTOM}

    def solid(shape, color):
        shape.fill.solid()
        shape.fill.fore_color.rgb = rgb(color)
        shape.line.fill.background()
        shape.shadow.inherit = False
        st = shape._element.find(qn("p:style"))
        if st is not None:  # theme effects would add a shadow in some viewers
            shape._element.remove(st)

    def add_text(sl, el):
        tb = sl.shapes.add_textbox(Inches(el["x"]), Inches(el["y"]), Inches(el["w"]), Inches(el["h"]))
        tf = tb.text_frame
        tf.word_wrap = True
        tf.auto_size = MSO_AUTO_SIZE.NONE
        tf.margin_left = tf.margin_right = tf.margin_top = tf.margin_bottom = 0
        tf.vertical_anchor = ANCHOR[el["valign"]]
        for i, p in enumerate(el["paras"]):
            para = tf.paragraphs[0] if i == 0 else tf.add_paragraph()
            para.alignment = ALIGN[el["align"]]
            mx = max(r["size"] for r in p["runs"])
            para.line_spacing = Pt(mx * el["lh"] * 1.2)
            if p.get("after"):
                para.space_after = Pt(p["after"])
            for r in p["runs"]:
                rn = para.add_run()
                rn.text = r["t"]
                f = rn.font
                f.size, f.bold, f.name = Pt(r["size"]), r["bold"], T["font_head"] if is_head(r) else T["font_body"]
                f.color.rgb = rgb(r["color"])
        if el.get("check"):
            tb.name = el["check"]

    def add_chart(sl, el):
        c = el["chart"]
        kind_name = c.get("type", "line")
        kind = {"line": XL_CHART_TYPE.LINE, "bar": XL_CHART_TYPE.COLUMN_CLUSTERED,
                "hbar": XL_CHART_TYPE.BAR_CLUSTERED, "stacked": XL_CHART_TYPE.COLUMN_STACKED}[kind_name]
        xs, series = [str(x) for x in c["x"]], c["series"]
        if kind_name == "hbar":  # PowerPoint draws bar categories bottom-up
            xs = xs[::-1]
            series = [dict(series[0], values=series[0]["values"][::-1])]
        cd = CategoryChartData()
        cd.categories = xs
        for sr in series:
            cd.add_series(sr["name"], list(sr["values"]))
        gf = sl.shapes.add_chart(kind, Inches(el["x"]), Inches(el["y"]), Inches(el["w"]), Inches(el["h"]), cd)
        ch = gf.chart
        ch.font.size, ch.font.name = Pt(12), T["font_body"]
        ch.font.color.rgb = rgb(T["muted"])
        ch.has_title = False
        ch.has_legend = len(series) > 1
        if ch.has_legend:
            ch.legend.position, ch.legend.include_in_layout = XL_LEGEND_POSITION.TOP, False
            ch.legend.font.size = Pt(12)
        numfmt = {"percent": '0.0%', "currency": ('"₹"#,##,##0' if c.get("currency") == "INR" else '"$"#,##0')}.get(c.get("format"), '#,##0')
        ca, va = ch.category_axis, ch.value_axis
        ca.tick_label_position = XL_TICK_LABEL_POSITION.LOW
        ca.format.line.color.rgb = rgb(T["dim"])
        ca.has_major_gridlines = False
        ca.tick_labels.font.size = Pt(12)
        va.format.line.fill.background()
        va.tick_labels.number_format, va.tick_labels.number_format_is_linked = ("0%" if c.get("format") == "percent" else numfmt), False
        va.tick_labels.font.size = Pt(12)
        bare = c.get("hide_axis") and kind_name == "bar" and len(series) == 1
        if bare:
            va.has_major_gridlines = False
            va.tick_label_position = XL_TICK_LABEL_POSITION.NONE
        else:
            va.has_major_gridlines = True
            va.major_gridlines.format.line.color.rgb = rgb(T["grid"])
        hx, hl = c.get("highlight_x"), c.get("highlight")
        palette = [T["accent_fill"]] + SERIES_EXTRA
        plot = ch.plots[0]
        for i, ps in enumerate(plot.series):
            name = series[i]["name"]
            col = palette[i % len(palette)]
            if hl:
                col = T["accent_fill"] if name == hl else T["dim"]
            if kind == XL_CHART_TYPE.LINE:
                lc = T["dim"] if hx is not None and len(series) == 1 else col
                ps.format.line.color.rgb = rgb(lc)
                ps.format.line.width = Pt(3)
                ps.smooth = False
                ps.marker.style = XL_MARKER_STYLE.NONE
                if hx is not None and str(hx) in xs and len(series) == 1:
                    j = xs.index(str(hx))
                    pt = ps.points[j]
                    pt.marker.style, pt.marker.size = XL_MARKER_STYLE.CIRCLE, 11
                    pt.marker.format.fill.solid()
                    pt.marker.format.fill.fore_color.rgb = rgb(T["accent_fill"])
                    pt.marker.format.line.color.rgb = rgb(T["bg"])
                    dl = pt.data_label
                    dl.position = XL_LABEL_POSITION.ABOVE
                    dr = dl.text_frame.paragraphs[0].add_run()
                    dr.text = V.fmt(series[0]["values"][j], c.get("format", "number"), c.get("currency"))
                    dr.font.size, dr.font.bold, dr.font.name = Pt(13), True, T["font_body"]
                    dr.font.color.rgb = rgb(T["accent_dark"])
            else:
                ps.format.fill.solid()
                ps.format.fill.fore_color.rgb = rgb(col)
                ps.format.line.fill.background()
                if hx is not None and len(series) == 1:
                    for j, x in enumerate(xs):
                        p = ps.points[j]
                        p.format.fill.solid()
                        p.format.fill.fore_color.rgb = rgb(T["accent_fill"] if x == str(hx) else T["dim"])
                        p.format.line.fill.background()
                # Negative values must keep their colour (no inverted fill).
                ps.invert_if_negative = False
        if kind != XL_CHART_TYPE.LINE and len(series) == 1 and len(xs) <= 12:
            plot.has_data_labels = True
            dls = plot.data_labels
            dls.number_format, dls.number_format_is_linked = numfmt, False
            dls.position = XL_LABEL_POSITION.OUTSIDE_END
            dls.font.size, dls.font.bold = Pt(13), True
            dls.font.color.rgb = rgb(T["body"])
        if kind != XL_CHART_TYPE.LINE:
            plot.gap_width = 70 if kind_name != "hbar" else 50
            plot.overlap = 100 if kind_name == "stacked" else -10

    def set_cell(cell, s, size, color, bold, align, bottom):
        cell.text = ""
        tf = cell.text_frame
        p = tf.paragraphs[0]
        p.alignment = align
        r = p.add_run()
        r.text = s
        r.font.size, r.font.bold, r.font.name = Pt(size), bold, T["font_body"]
        r.font.color.rgb = rgb(color)
        cell.margin_top = cell.margin_bottom = 0
        cell.vertical_anchor = MSO_ANCHOR.MIDDLE
        tcPr = cell._tc.get_or_add_tcPr()
        for child in list(tcPr):
            tcPr.remove(child)
        for tag in ("a:lnL", "a:lnR", "a:lnT"):
            ln = etree.SubElement(tcPr, qn(tag), w="0")
            etree.SubElement(ln, qn("a:noFill"))
        ln = etree.SubElement(tcPr, qn("a:lnB"), w="12700")
        sf = etree.SubElement(ln, qn("a:solidFill"))
        etree.SubElement(sf, qn("a:srgbClr"), val=bottom)
        etree.SubElement(tcPr, qn("a:noFill"))

    def add_table(sl, el):
        t = el["table"]
        cols, body, isnum = table_rows(t)
        nr = len(body) + 1
        gt = sl.shapes.add_table(nr, len(cols), Inches(el["x"]), Inches(el["y"]), Inches(el["w"]), Inches(el["h"])).table
        tblPr = gt._tbl.tblPr
        tblPr.set("firstRow", "0")
        tblPr.set("bandRow", "0")
        sid = tblPr.find(qn("a:tableStyleId"))
        if sid is None:
            sid = etree.SubElement(tblPr, qn("a:tableStyleId"))
        sid.text = "{2D5ABB26-0587-4C30-8999-92F81FD0307C}"  # No Style, No Grid
        # first column wider: text columns share the rest
        for j, wf in enumerate(col_widths(len(cols))):
            gt.columns[j].width = Inches(el["w"] * wf)
        for i in range(nr):
            gt.rows[i].height = Inches(el["rh"])
        for j, cname in enumerate(cols):
            set_cell(gt.cell(0, j), cname, 12, T["muted"], True, PP_ALIGN.RIGHT if isnum[j] else PP_ALIGN.LEFT, T["grid"])
        for i, r in enumerate(body, 1):
            hl = el["hl"] == i - 1
            for j, v in enumerate(r):
                col = (T["accent_dark"] if j == 0 else T["text"]) if hl else T["body"]
                set_cell(gt.cell(i, j), v, 15, col, hl, PP_ALIGN.RIGHT if isnum[j] else PP_ALIGN.LEFT, T["grid"])
        for i in range(nr):
            for j in range(len(cols)):
                c = gt.cell(i, j)
                c.margin_left = Inches(0 if j == 0 else 0.15)
                c.margin_right = Inches(0 if j == len(cols) - 1 else 0.15)

    for s in sc:
        sl = prs.slides.add_slide(blank)
        f = sl.background.fill
        f.solid()
        f.fore_color.rgb = rgb(s["bg"])
        for el in s["els"]:
            k = el["k"]
            if k == "rect":
                shp = sl.shapes.add_shape(MSO_SHAPE.ROUNDED_RECTANGLE if el["r"] else MSO_SHAPE.RECTANGLE,
                                          Inches(el["x"]), Inches(el["y"]), Inches(el["w"]), Inches(el["h"]))
                if el["r"]:
                    shp.adjustments[0] = min(0.5, el["r"] / min(el["w"], el["h"]))
                solid(shp, el["fill"])
            elif k == "circle":
                shp = sl.shapes.add_shape(MSO_SHAPE.OVAL, Inches(el["x"]), Inches(el["y"]), Inches(el["d"]), Inches(el["d"]))
                solid(shp, el["fill"])
            elif k == "ring":
                shp = sl.shapes.add_shape(MSO_SHAPE.OVAL, Inches(el["x"]), Inches(el["y"]), Inches(el["d"]), Inches(el["d"]))
                solid(shp, "FFFFFF")
                shp.fill.background()
                shp.line.color.rgb = rgb(el["color"])
                shp.line.width = Pt(el["width"])
            elif k == "image":
                sl.shapes.add_picture(el["path"], Inches(el["x"]), Inches(el["y"]), Inches(el["w"]), Inches(el["h"]))
            elif k == "text":
                add_text(sl, el)
            elif k == "chart":
                add_chart(sl, el)
            elif k == "table":
                add_table(sl, el)
    prs.save(path)
    return True


def main():
    args = sys.argv[1:]
    if not args:
        sys.exit(__doc__)
    out = args[args.index("--out") + 1] if "--out" in args else "deck"
    global BASE_DIR
    BASE_DIR = os.path.dirname(os.path.abspath(args[0]))
    spec = run_analysis(args[0])
    probs = validate(spec)
    if probs:
        sys.exit("spec problems:\n- " + "\n- ".join(probs))
    with open(out + ".spec.json", "w", encoding="utf-8") as f:
        json.dump(spec, f, indent=2, ensure_ascii=False)
    with open(out + ".html", "w", encoding="utf-8") as f:
        f.write(build_html(spec))
    made = [out + ".html", out + ".spec.json"]
    if "--no-pdf" not in args:
        try:
            render.html_to_pdf(out + ".html", out + ".pdf")
            made.append(out + ".pdf")
        except Exception as ex:  # noqa: BLE001
            print("PDF skipped: %s" % ex)
    if "--no-pptx" not in args:
        if build_pptx(spec, out + ".pptx"):
            made.append(out + ".pptx")
        else:
            print("PowerPoint skipped: run `pip install python-pptx` and build again")
    note = resolve_theme(spec)["note"]
    if note:
        print(note + " (tell the user; they can set brand.primary and brand.accent to change them)")
    print("built " + ", ".join(made))


if __name__ == "__main__":
    main()
