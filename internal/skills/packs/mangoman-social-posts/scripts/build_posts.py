"""Build a branded set of social media posts from posts.json.

Usage: python3 build_posts.py posts.json --out posts [--no-render]
Writes into the out folder: one PNG per post and size (carousels: one per
slide, plus a PDF for LinkedIn), captions.md (ready to paste, per platform),
preview.png (all images on one sheet), frames.json (text-fit measurements for
the checker) and posts.spec.json.

Design: every image is laid out in one tested template per layout, sized in
units of the canvas, so the same post adapts from a 1080 x 1920 story to a
1600 x 900 X image. Text shrinks to fit its box and the checker fails a post
whose text would have to shrink below a readable size.
"""
import html
import json
import os
import re
import sys

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
import brandkit as BK  # noqa: E402
import render  # noqa: E402

FORMATS = {  # name: (width, height) in pixels
    "square": (1080, 1080), "portrait": (1080, 1350), "story": (1080, 1920),
    "landscape": (1600, 900), "link": (1200, 627),
}
PLATFORMS = {  # default image format, caption limit, hashtag advice (warn above)
    "instagram": ("portrait", 2200, 10), "linkedin": ("portrait", 3000, 5), "facebook": ("portrait", 63206, 3),
    "x": ("landscape", 280, 2), "threads": ("portrait", 500, 1), "whatsapp": ("story", 700, 0),
}
STORY_PLATFORMS = ("instagram", "facebook")
LAYOUTS = ("statement", "stat", "list", "quote", "announce", "photo", "carousel")
SLIDE_KINDS = ("cover", "content", "end")
MIN_SCALE = 0.62  # text may shrink to this share of its design size, no further
e = lambda s: html.escape(str(s if s is not None else ""), quote=True)


def load(path):
    with open(path, encoding="utf-8") as f:
        return json.load(f)


def base_dir(path):
    return os.path.dirname(os.path.abspath(path))


def post_formats(spec, p):
    """Image formats this post is rendered in (deduplicated across platforms)."""
    if p.get("formats"):
        return list(dict.fromkeys(p["formats"]))
    out = []
    for pl in spec.get("platforms", ["instagram"]):
        if pl in PLATFORMS:
            out.append(PLATFORMS[pl][0])
            if spec.get("stories") and pl in STORY_PLATFORMS and p.get("layout") != "carousel":
                out.append("story")
    if p.get("layout") == "carousel":
        out = [f for f in out if f in ("portrait", "square")] or ["portrait"]
    return list(dict.fromkeys(out))


def platform_format(spec, p, platform):
    fm = post_formats(spec, p)
    want = PLATFORMS[platform][0]
    return want if want in fm else fm[0]


def validate(spec, bdir="."):
    probs = []
    posts = spec.get("posts", [])
    if not posts:
        probs.append("no posts")
    for pl in spec.get("platforms", ["instagram"]):
        if pl not in PLATFORMS:
            probs.append("platform %r: use one of %s" % (pl, ", ".join(PLATFORMS)))
    for key, allowed in (("theme", tuple(BK.CURATED)), ("motif", BK.MOTIFS), ("type", tuple(BK.TYPES))):
        if spec.get(key) is not None and spec[key] not in allowed:
            probs.append("%s must be one of %s" % (key, ", ".join(allowed)))
    b = spec.get("brand") or {}
    for key in ("primary", "accent"):
        if b.get(key):
            try:
                BK.hexc(b[key])
            except ValueError:
                probs.append("brand %s must be a hex colour like #1F5FA8" % key)
    if b.get("logo"):
        pr = BK.logo_check(os.path.join(bdir, b["logo"]) if not os.path.isabs(b["logo"]) else b["logo"])
        if pr:
            probs.append(pr)
    ids = set()
    for i, p in enumerate(posts, 1):
        tag = "post %d (%s)" % (i, p.get("id", "?"))
        if not re.match(r"^[a-z0-9][a-z0-9-]*$", str(p.get("id", ""))):
            probs.append("%s: id must be lowercase letters, digits and dashes" % tag)
        if p.get("id") in ids:
            probs.append("%s: duplicate id" % tag)
        ids.add(p.get("id"))
        lay = p.get("layout")
        if lay not in LAYOUTS:
            probs.append("%s: layout must be one of %s" % (tag, ", ".join(LAYOUTS)))
            continue
        for f in p.get("formats", []):
            if f not in FORMATS:
                probs.append("%s: format %r: use one of %s" % (tag, f, ", ".join(FORMATS)))
        need = {"statement": ["headline"], "stat": ["value", "headline"], "list": ["headline", "items"],
                "quote": ["quote", "author"], "announce": ["headline"], "photo": ["image", "headline"],
                "carousel": ["slides"]}[lay]
        for k in need:
            if not p.get(k):
                probs.append("%s: %s layout needs %s" % (tag, lay, k))
        if lay == "list" and not (2 <= len(p.get("items", [])) <= 5):
            probs.append("%s: a list has 2 to 5 items" % tag)
        if lay == "photo" and p.get("image") and not os.path.exists(os.path.join(bdir, p["image"])):
            probs.append("%s: image not found: %s" % (tag, p["image"]))
        if lay == "carousel":
            sl = p.get("slides", [])
            if not (3 <= len(sl) <= 10):
                probs.append("%s: a carousel has 3 to 10 slides" % tag)
            for j, s in enumerate(sl, 1):
                if s.get("kind", "content") not in SLIDE_KINDS:
                    probs.append("%s slide %d: kind must be cover, content or end" % (tag, j))
                if not s.get("headline"):
                    probs.append("%s slide %d: needs a headline" % (tag, j))
        if not p.get("alt"):
            probs.append("%s: needs alt text (what the image shows, for screen readers)" % tag)
        if not p.get("caption"):
            probs.append("%s: needs a caption" % tag)
    return probs


# ---------- HTML ----------

CSS = """
* { box-sizing: border-box; margin: 0; padding: 0; }
html, body { width: %(w)dpx; height: %(h)dpx; overflow: hidden; background: #%(bg)s; }
body { --u: %(u).3fpx; --k: 1; font-family: %(body)s; -webkit-font-smoothing: antialiased; }
.post { position: absolute; inset: 0; overflow: hidden; }
.shape { position: absolute; border-radius: 50%%; }
.frame { position: absolute; inset: calc(7 * var(--u)); display: flex; flex-direction: column; }
.story .frame { top: 250px; bottom: 250px; } /* the app's own bars cover the top and bottom 250 px of a story */
.fit { flex: 1; min-height: 0; overflow: hidden; display: flex; flex-direction: column; }
.end { justify-content: flex-end; } .mid { justify-content: center; }
.h { font-family: %(head)s; font-weight: 700; line-height: 1.06; letter-spacing: -0.01em; overflow-wrap: break-word; }
.foot { display: flex; align-items: center; justify-content: space-between; gap: calc(3 * var(--u)); margin-top: calc(4 * var(--u)); min-height: calc(5.5 * var(--u)); }
.foot img { height: calc(5 * var(--u)); width: auto; display: block; }
.chip { padding: calc(1 * var(--u)) calc(1.6 * var(--u)); border-radius: calc(1.4 * var(--u)); }
.brandname { font-weight: 700; font-size: calc(3.1 * var(--u)); }
.handle { font-size: calc(2.8 * var(--u)); }
.pill { display: inline-block; align-self: flex-start; font-weight: 700; border-radius: 999px;
  padding: calc(1.1 * var(--u) * var(--k)) calc(2.6 * var(--u) * var(--k)); font-size: calc(2.7 * var(--u) * var(--k)); }
.sz { font-size: calc(var(--s) * var(--u) * var(--k)); }
ol.items { list-style: none; display: flex; flex-direction: column; }
ol.items li { display: grid; grid-template-columns: calc(7.5 * var(--u) * var(--k)) 1fr; align-items: center;
  padding: calc(2.2 * var(--u) * var(--k)) 0; border-top: 1px solid #%(grid)s; }
ol.items li:first-child { border-top: 0; }
.num { width: calc(5.4 * var(--u) * var(--k)); height: calc(5.4 * var(--u) * var(--k)); border-radius: 50%%;
  display: flex; align-items: center; justify-content: center; font-weight: 700; font-size: calc(2.6 * var(--u) * var(--k)); }
.dot { width: calc(1.8 * var(--u) * var(--k)); height: calc(1.8 * var(--u) * var(--k)); border-radius: 50%%; }
.photo { position: absolute; object-fit: cover; display: block; }
.counter { font-size: calc(2.6 * var(--u)); font-weight: 700; }
"""

FIT_JS = """
(function(){
  var MIN = %(min)s, res = [];
  document.querySelectorAll('.fit').forEach(function(box){
    var k = 1;
    function over(){ return box.scrollHeight - box.clientHeight > 1 || box.scrollWidth - box.clientWidth > 1; }
    box.style.setProperty('--k', k);
    var words = box.querySelectorAll('.h, .sz');
    function wide(){ for (var i = 0; i < words.length; i++) { if (words[i].scrollWidth - words[i].clientWidth > 1) return true; } return false; }
    while ((over() || wide()) && k > MIN) { k = Math.round((k - 0.02) * 100) / 100; box.style.setProperty('--k', k); }
    res.push({id: box.dataset.check, scale: k, overflow: over() || wide()});
  });
  window.__fit = res;
})();
"""


def text_on(bg, T):
    """The theme colour that reads best on a background."""
    return max((T["text"], "FFFFFF"), key=lambda c: BK.contrast(c, bg))


def logo_html(T, bg, size_u=5):
    if not T.get("logo"):
        return ""
    import base64
    with open(T["logo"], "rb") as f:
        data = base64.b64encode(f.read()).decode()
    img = '<img alt="" src="data:image/png;base64,%s" style="height:calc(%s * var(--u))">' % (data, size_u)
    if BK.logo_hidden_share(T["logo"], bg) > 0.15:
        chip = "FFFFFF" if BK.luminance(bg) < 0.4 else T["dark"]
        return '<span class="chip" style="background:#%s">%s</span>' % (chip, img)
    return img


def foot(spec, T, bg, muted):
    b = spec.get("brand") or {}
    left = logo_html(T, bg) or ('<span class="brandname" style="color:#%s">%s</span>' % (text_on(bg, T), e(b.get("name", ""))) if b.get("name") else "")
    right = '<span class="handle" style="color:#%s">%s</span>' % (muted, e(b.get("handle", ""))) if b.get("handle") else ""
    return '<div class="foot">%s%s</div>' % (left or "<span></span>", right)


def motif(T, bg, w, h, u, place="tr", strength=1.0):
    """The deck motif adapted to a social canvas."""
    style = T.get("motif", "orb")
    ink = "FFFFFF" if BK.luminance(bg) < 0.3 else T["dark"]
    out = []
    if style == "dots":
        gap, n = 4.2 * u, 6
        x0 = w - 7 * u - gap * (n - 1) if place == "tr" else 7 * u
        y0 = 7 * u
        for r in range(n - 1):
            for c in range(n):
                col = BK.mix(ink, bg, (0.16 if (r + c) % 3 else 0.28) * strength)
                out.append('<div class="shape" style="left:%.0fpx;top:%.0fpx;width:%.0fpx;height:%.0fpx;background:#%s"></div>' % (
                    x0 + c * gap, y0 + r * gap, 0.9 * u, 0.9 * u, col))
        return "".join(out)
    d = 0.95 * max(w, h) if h > w else 0.9 * max(w, h) * 0.75
    cx, cy = (w - 0.12 * d, 0.1 * d) if place == "tr" else (w - 0.1 * d, h - 0.08 * d)
    if style == "rings":
        for f in (1.0, 0.76, 0.52, 0.28):
            dd = d * f
            out.append('<div class="shape" style="left:%.0fpx;top:%.0fpx;width:%.0fpx;height:%.0fpx;border:%.1fpx solid #%s"></div>' % (
                cx - dd / 2, cy - dd / 2, dd, dd, max(2, 0.28 * u), BK.mix(ink, bg, 0.14 * strength)))
    else:
        out.append('<div class="shape" style="left:%.0fpx;top:%.0fpx;width:%.0fpx;height:%.0fpx;background:#%s"></div>' % (
            cx - d / 2, cy - d / 2, d, d, BK.mix(ink, bg, 0.07 * strength)))
    return "".join(out)


def sz(text, s, color, cls="", extra=""):
    return '<div class="sz %s" style="--s:%s;color:#%s;%s">%s</div>' % (cls, s, color, extra, e(text))


def unit(w, h):
    """One design unit in pixels: 1% of the short side, a little larger on
    wide images so text keeps its weight next to the extra width."""
    return min(w, h) / 100 * (1.22 if w > h * 1.2 else 1.0)


def layout_html(spec, p, T, w, h, slide=None, idx=0, total=0, bdir="."):
    """(background colour, inner HTML) for one image."""
    u = unit(w, h)
    lay = p["layout"]
    land = w > h * 1.2
    tall = h > w * 1.5
    hs = 0.86 if land else (1.2 if tall else 1.0)  # headline scale by shape
    if lay == "carousel":
        kind = slide.get("kind", "content")
        if kind == "cover":
            q = dict(slide, layout="statement")
            bg, inner = layout_html(spec, q, T, w, h, bdir=bdir)
            hint = sz(slide.get("hint", "Swipe to read"), 2.8, T["accent"], extra="margin-top:calc(3.5*var(--u));font-weight:700")
            return bg, inner.replace("<!--after-->", hint)
        if kind == "end":
            q = dict(slide, layout="statement")
            bg, inner = layout_html(spec, q, T, w, h, bdir=bdir)
            cta = ""
            if slide.get("cta"):
                cta = '<div class="pill" style="background:#%s;color:#%s;margin-top:calc(4*var(--u))">%s</div>' % (
                    T["accent"], text_on(T["accent"], T), e(slide["cta"]))
            return bg, inner.replace("<!--after-->", cta)
        bg = "FFFFFF"
        body = '<div class="h" style="font-size:calc(17*var(--u)*var(--k));color:#%s;line-height:1;margin-bottom:calc(2*var(--u)*var(--k))">%02d</div>' % (
            BK.mix(T["accent_fill"], bg, 0.28), idx - 1)
        body += sz(slide["headline"], 7.4 * hs, T["text"], "h")
        if slide.get("body"):
            body += sz(slide["body"], 4.2, T["body"], extra="margin-top:calc(3*var(--u)*var(--k));line-height:1.38")
        # progress along the carousel: one short bar per slide, this one in the accent
        bars = "".join('<span style="display:inline-block;width:calc(%s*var(--u));height:calc(0.9*var(--u));border-radius:99px;margin-right:calc(0.9*var(--u));background:#%s"></span>' % (
            4 if j == idx else 1.6, T["accent_fill"] if j == idx else T["grid"]) for j in range(1, total + 1))
        top = '<div style="display:flex;align-items:center;justify-content:space-between;margin-bottom:calc(4*var(--u))"><div>%s</div><div class="counter" style="color:#%s">%d / %d</div></div>' % (
            bars, T["muted"], idx, total)
        return bg, '<div class="frame">%s<div class="fit mid" data-check="slide-%d">%s</div>%s</div>' % (
            top, idx, body, foot(spec, T, bg, T["muted"]))
    if lay == "statement":
        bg = T["dark"]
        txt = sz(p["headline"], 9.6 * hs, T["on_dark"], "h")
        if p.get("sub"):
            txt += sz(p["sub"], 3.7, T["on_dark2"], extra="margin-top:calc(3.2*var(--u)*var(--k));line-height:1.3")
        dot = '<div class="shape" style="left:%.0fpx;top:%.0fpx;width:%.0fpx;height:%.0fpx;background:#%s"></div>' % (
            7 * u, 7 * u, 5.2 * u, 5.2 * u, T["accent"])
        return bg, '%s%s<div class="frame"><div class="fit end" data-check="text" style="padding-top:calc(9*var(--u))">%s<!--after--></div>%s</div>' % (
            motif(T, bg, w, h, u), dot, txt, foot(spec, T, bg, T["on_dark_muted"]))
    if lay == "stat":
        bg = "FFFFFF"
        val = p["value"] if isinstance(p["value"], str) else str(p["value"])
        txt = sz(val, 27 * (0.85 if land else 1.0), T["accent_dark"], "h", "line-height:.95;letter-spacing:-0.03em")
        txt += sz(p["headline"], 5.8 * hs, T["text"], "h", "margin-top:calc(3*var(--u)*var(--k))")
        if p.get("sub"):
            txt += sz(p["sub"], 3.4, T["body"], extra="margin-top:calc(2.4*var(--u)*var(--k));line-height:1.35")
        if p.get("source"):
            txt += sz("Source: " + p["source"], 2.3, T["muted"], extra="margin-top:calc(3*var(--u)*var(--k))")
        return bg, '%s<div class="frame"><div class="fit end" data-check="text">%s</div>%s</div>' % (
            motif(T, bg, w, h, u, strength=1.4), txt, foot(spec, T, bg, T["muted"]))
    if lay == "list":
        bg = "FFFFFF"
        ordered = p.get("ordered", True)
        items = []
        for i, it in enumerate(p["items"], 1):
            mark = ('<span class="num" style="background:#%s;color:#%s">%d</span>' % (T["accent_fill"], text_on(T["accent_fill"], T), i)
                    if ordered else '<span class="dot" style="background:#%s"></span>' % T["accent_fill"])
            items.append('<li>%s%s</li>' % (mark, sz(it, 3.9 * (0.92 if land else 1.0), T["body"], extra="line-height:1.28")))
        txt = sz(p["headline"], 6.6 * hs, T["text"], "h", "margin-bottom:calc(3.6*var(--u)*var(--k))")
        txt += '<ol class="items">%s</ol>' % "".join(items)
        return bg, '<div class="frame"><div class="fit mid" data-check="text">%s</div>%s</div>' % (txt, foot(spec, T, bg, T["muted"]))
    if lay == "quote":
        bg = T["dark"]
        mark = '<div class="h" style="font-size:calc(26*var(--u)*var(--k));line-height:.8;color:#%s;height:calc(14*var(--u)*var(--k))">&ldquo;</div>' % T["accent"]
        txt = mark + sz(p["quote"], 6.0 * hs, T["on_dark"], "h", "font-weight:600;line-height:1.18")
        who = '<span style="font-weight:700;color:#%s">%s</span>' % (T["on_dark"], e(p["author"]))
        if p.get("role"):
            who += '<span style="color:#%s"> , %s</span>' % (T["on_dark_muted"], e(p["role"]))
        txt += '<div class="sz" style="--s:3.2;margin-top:calc(4*var(--u)*var(--k))">%s</div>' % who.replace(" , ", ", ")
        return bg, '%s<div class="frame"><div class="fit end" data-check="text">%s</div>%s</div>' % (
            motif(T, bg, w, h, u, place="br"), txt, foot(spec, T, bg, T["on_dark_muted"]))
    if lay == "announce":
        bg = T["accent_fill"]
        ink = text_on(bg, T)
        txt = ""
        if p.get("label"):
            txt += '<div class="pill" style="background:#%s;color:#%s;margin-bottom:calc(4*var(--u)*var(--k))">%s</div>' % (ink, bg, e(p["label"]))
        txt += sz(p["headline"], 9.0 * hs, ink, "h")
        for d in p.get("details", []):
            txt += sz(d, 3.8, ink, extra="margin-top:calc(1.6*var(--u)*var(--k));font-weight:600")
        if p.get("cta"):
            txt += '<div class="pill" style="background:#%s;color:#%s;margin-top:calc(4.5*var(--u)*var(--k))">%s</div>' % (ink, bg, e(p["cta"]))
        return bg, '%s<div class="frame"><div class="fit end" data-check="text">%s</div>%s</div>' % (
            motif(T, bg, w, h, u, strength=1.6), txt, foot(spec, T, bg, BK.mix(ink, bg, 0.75)))
    if lay == "photo":
        bg = T["dark"]
        img = os.path.abspath(os.path.join(bdir, p["image"]))
        if land:
            pic = '<img class="photo" alt="" src="file://%s" style="left:0;top:0;width:%dpx;height:%dpx">' % (e(img), int(w * 0.54), h)
            frame = 'left:%dpx' % int(w * 0.54 + 6 * u)
        else:
            ph = int(h * (0.56 if not tall else 0.6))
            pic = '<img class="photo" alt="" src="file://%s" style="left:0;top:0;width:%dpx;height:%dpx">' % (e(img), w, ph)
            frame = 'top:%dpx' % int(ph + 5 * u)
        txt = sz(p["headline"], 7.2 * (0.9 if land else 1.0), T["on_dark"], "h")
        if p.get("sub"):
            txt += sz(p["sub"], 3.4, T["on_dark2"], extra="margin-top:calc(2.4*var(--u)*var(--k));line-height:1.3")
        return bg, '%s<div class="frame" style="%s"><div class="fit mid" data-check="text">%s</div>%s</div>' % (
            pic, frame, txt, foot(spec, T, bg, T["on_dark_muted"]))
    raise ValueError(lay)


def page(spec, p, T, fmt, slide=None, idx=0, total=0, bdir="."):
    w, h = FORMATS[fmt]
    bg, inner = layout_html(spec, p, T, w, h, slide, idx, total, bdir)
    css = CSS % dict(w=w, h=h, u=unit(w, h), bg=bg, body=T["html_body"], head=T["html_head"], grid=T["grid"])
    return ('<!doctype html><html lang="%s"><head><meta charset="utf-8"><style>%s</style></head>'
            '<body class="%s"><div class="post" style="background:#%s">%s</div><script>%s</script></body></html>') % (
        e(spec.get("language", "en")), css, fmt, bg, inner, FIT_JS % {"min": MIN_SCALE})


def frames(spec, T, bdir):
    """Every image to render: (post id, format, slide number or 0, html)."""
    out = []
    for p in spec["posts"]:
        for fm in post_formats(spec, p):
            if p["layout"] == "carousel":
                n = len(p["slides"])
                for j, s in enumerate(p["slides"], 1):
                    out.append((p["id"], fm, j, page(spec, p, T, fm, s, j, n, bdir)))
            else:
                out.append((p["id"], fm, 0, page(spec, p, T, fm, bdir=bdir)))
    return out


def frame_name(pid, fm, j):
    return "%s-%s%s.png" % (pid, fm, "-%02d" % j if j else "")


# ---------- captions ----------

def caption_for(p, platform):
    c = p.get("caption", "")
    if isinstance(c, dict):
        c = c.get(platform) or c.get("default") or next(iter(c.values()), "")
    return c


def hashtags_for(p, platform, spec):
    tags = [t if t.startswith("#") else "#" + t for t in p.get("hashtags", [])]
    limit = PLATFORMS[platform][2]
    if platform == "whatsapp":
        return []
    return tags[:max(limit, 1)] if platform in ("x", "threads") else tags


def full_caption(p, platform, spec):
    c = caption_for(p, platform).strip()
    tags = hashtags_for(p, platform, spec)
    if tags and not all(t.lower() in c.lower() for t in tags):
        c = c + ("\n\n" if platform in ("instagram", "linkedin", "facebook") else " ") + " ".join(t for t in tags if t.lower() not in c.lower())
    return c


def captions_md(spec):
    lines = ["# Captions", "", "Copy each caption with its image. Image files are in this folder.", ""]
    for i, p in enumerate(spec["posts"], 1):
        lines += ["## %d. %s" % (i, p.get("title") or p["id"]), ""]
        for pl in spec.get("platforms", ["instagram"]):
            fm = platform_format(spec, p, pl)
            files = (", ".join(frame_name(p["id"], fm, j) for j in range(1, len(p["slides"]) + 1))
                     if p["layout"] == "carousel" else frame_name(p["id"], fm, 0))
            if p["layout"] == "carousel" and pl == "linkedin":
                files = "%s-carousel.pdf (upload as a document)" % p["id"]
            name = {"x": "X", "linkedin": "LinkedIn", "whatsapp": "WhatsApp status"}.get(pl, pl.title())
            lines += ["**%s** (%s)" % (name, files), "", "```", full_caption(p, pl, spec), "```", ""]
            if pl in STORY_PLATFORMS and "story" in post_formats(spec, p) and PLATFORMS[pl][0] != "story":
                lines += ["%s story: %s (stories carry no caption; add a link sticker if needed)" % (name, frame_name(p["id"], "story", 0)), ""]
        lines += ["Alt text: %s" % p.get("alt", ""), ""]
        if p.get("first_comment"):
            lines += ["First comment: %s" % p["first_comment"], ""]
    return "\n".join(lines)


def contact_sheet(paths, out, cols=4, cell=360):
    from PIL import Image
    ims = []
    for pth in paths:
        im = Image.open(pth).convert("RGB")
        im.thumbnail((cell, cell * 16 // 9))
        ims.append(im)
    rows = (len(ims) + cols - 1) // cols
    rh = [max(im.height for im in ims[r * cols:(r + 1) * cols]) for r in range(rows)]
    sheet = Image.new("RGB", (cols * (cell + 16) + 16, sum(rh) + 16 * (rows + 1)), (226, 228, 233))
    y = 16
    for r in range(rows):
        x = 16
        for im in ims[r * cols:(r + 1) * cols]:
            sheet.paste(im, (x, y))
            x += cell + 16
        y += rh[r] + 16
    sheet.save(out)


def main():
    a = sys.argv[1:]
    if not a:
        sys.exit(__doc__)
    src = a[0]
    out = a[a.index("--out") + 1] if "--out" in a else "posts"
    spec = load(src)
    bdir = base_dir(src)
    probs = validate(spec, bdir)
    if probs:
        sys.exit("spec problems:\n- " + "\n- ".join(probs))
    os.makedirs(out, exist_ok=True)
    T, note = BK.resolve_spec(spec, bdir)
    with open(os.path.join(out, "posts.spec.json"), "w", encoding="utf-8") as f:
        json.dump(spec, f, indent=2, ensure_ascii=False)
    with open(os.path.join(out, "captions.md"), "w", encoding="utf-8") as f:
        f.write(captions_md(spec))
    fr = frames(spec, T, bdir)
    hdir = os.path.join(out, "html")
    os.makedirs(hdir, exist_ok=True)
    jobs = []
    for pid, fm, j, doc in fr:
        hp = os.path.join(hdir, frame_name(pid, fm, j).replace(".png", ".html"))
        with open(hp, "w", encoding="utf-8") as f:
            f.write(doc)
        w, h = FORMATS[fm]
        jobs.append((hp, os.path.join(out, frame_name(pid, fm, j)), w, h))
    made = ["captions.md"]
    if "--no-render" not in a:
        res = render.render_frames(jobs)
        if res is None:
            print("images skipped: install Playwright (pip install playwright) or Chrome")
        else:
            meta = [{"file": os.path.basename(j[1]), "width": j[2], "height": j[3], "fit": r} for j, r in zip(jobs, res)]
            with open(os.path.join(out, "frames.json"), "w", encoding="utf-8") as f:
                json.dump(meta, f, indent=1)
            made.append("%d images" % len(jobs))
            for p in spec["posts"]:  # carousels as a PDF for LinkedIn documents
                if p["layout"] == "carousel":
                    from PIL import Image
                    Image.init()  # registers the JPEG writer the PDF plugin uses
                    fm = post_formats(spec, p)[0]
                    ims = [Image.open(os.path.join(out, frame_name(p["id"], fm, j))).convert("RGB") for j in range(1, len(p["slides"]) + 1)]
                    ims[0].save(os.path.join(out, "%s-carousel.pdf" % p["id"]), save_all=True, append_images=ims[1:], resolution=144)
                    made.append("%s-carousel.pdf" % p["id"])
            try:
                firsts = [j[1] for j in jobs if not re.search(r"-(0[2-9]|10)\.png$", j[1])]
                contact_sheet(firsts, os.path.join(out, "preview.png"))
                made.append("preview.png")
            except Exception as ex:  # noqa: BLE001
                print("preview skipped: %s" % ex)
    if note:
        print(note + " (tell the user; they can set brand.primary and brand.accent to change them)")
    print("built in %s/: %s" % (out, ", ".join(made)))


if __name__ == "__main__":
    main()
