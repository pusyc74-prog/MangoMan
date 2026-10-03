"""Build a one-page website (landing page) from page.json.

Usage: python3 build_page.py page.json --out site [--no-shots]
Writes site/index.html (self-contained: styles and script inline, no outside
requests) and site/assets/ (images resized for the web, logo, favicon); next to
the site folder, page.spec.json and preview-desktop.png / preview-mobile.png
(kept out of the folder you publish). Upload the site folder as it is to
any static host (Netlify Drop, GitHub Pages, Cloudflare Pages, cPanel).

Design: the brand theme from brandkit, one motif in the hero, large clear
type, sections that vary in rhythm (light, tinted, dark), mobile first. The
form needs no server: it opens WhatsApp or email with the details filled in,
or posts to a form service the user names.
"""
import datetime
import html
import json
import os
import re
import shutil
import sys

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
import brandkit as BK  # noqa: E402
import render  # noqa: E402

SECTION_TYPES = ("hero", "logos", "features", "steps", "stats", "testimonials", "pricing", "faq", "gallery", "text", "cta")
FIELD_TYPES = ("text", "email", "tel", "number", "date", "textarea", "select")
WEB_TYPES = {  # heading, body font stacks that every device has
    "modern": ('"Segoe UI", system-ui, -apple-system, Roboto, "Helvetica Neue", Arial, sans-serif',
               '"Segoe UI", system-ui, -apple-system, Roboto, "Helvetica Neue", Arial, sans-serif'),
    "editorial": ('Georgia, Cambria, "Times New Roman", serif',
                  '"Segoe UI", system-ui, -apple-system, Roboto, "Helvetica Neue", Arial, sans-serif'),
    "classic": ('Arial, "Helvetica Neue", Helvetica, sans-serif', 'Arial, "Helvetica Neue", Helvetica, sans-serif'),
}
MAX_IMG = 1600
e = lambda s: html.escape(str(s if s is not None else ""), quote=True)


def load(path):
    with open(path, encoding="utf-8") as f:
        return json.load(f)


def slug(s):
    return re.sub(r"[^a-z0-9]+", "-", str(s).lower()).strip("-") or "section"


def section_id(s, i):
    return s.get("id") or (slug(s.get("nav") or s.get("headline") or s["type"]) if s["type"] != "hero" else "top")


def href_ok(h):
    return bool(re.match(r"^(#[A-Za-z][\w-]*|https?://[^\s]+|mailto:[^\s@]+@[^\s@]+|tel:\+?[\d\s-]{6,}|https://wa\.me/\d{8,15}(\?.*)?)$", str(h or "")))


def validate(spec, bdir="."):
    p = []
    meta = spec.get("meta", {})
    if not meta.get("title"):
        p.append("meta.title is required (the browser tab and Google result title)")
    if not meta.get("description"):
        p.append("meta.description is required (the Google result snippet)")
    for key, allowed in (("theme", tuple(BK.CURATED)), ("motif", BK.MOTIFS), ("mode", BK.MODES), ("type", tuple(WEB_TYPES))):
        if spec.get(key) is not None and spec[key] not in allowed:
            p.append("%s must be one of %s" % (key, ", ".join(allowed)))
    b = spec.get("brand") or {}
    if not b.get("name"):
        p.append("brand.name is required")
    for key in ("primary", "accent"):
        if b.get(key):
            try:
                BK.hexc(b[key])
            except ValueError:
                p.append("brand %s must be a hex colour like #1F5FA8" % key)
    if b.get("logo"):
        pr = BK.logo_check(path_in(bdir, b["logo"]))
        if pr:
            p.append(pr)
    secs = spec.get("sections", [])
    if not secs or secs[0].get("type") != "hero":
        p.append("the first section must be the hero")
    ids = set()
    for i, s in enumerate(secs, 1):
        t = s.get("type")
        tag = "section %d (%s)" % (i, t)
        if t not in SECTION_TYPES:
            p.append("%s: type must be one of %s" % (tag, ", ".join(SECTION_TYPES)))
            continue
        sid = section_id(s, i)
        if sid in ids:
            p.append("%s: duplicate section id %r; set a different id" % (tag, sid))
        ids.add(sid)
        if t != "logos" and not s.get("headline"):
            p.append("%s: needs a headline" % tag)
        need = {"features": "items", "steps": "items", "stats": "items", "testimonials": "items", "pricing": "plans",
                "faq": "items", "gallery": "images", "logos": "items"}.get(t)
        if need and not s.get(need):
            p.append("%s: needs %s" % (tag, need))
        for img in ([s["image"]] if s.get("image") else []) + [g.get("src") for g in s.get("images", []) if isinstance(g, dict)]:
            if img and not os.path.exists(path_in(bdir, img)):
                p.append("%s: image not found: %s" % (tag, img))
        for g in s.get("images", []):
            if isinstance(g, dict) and not g.get("alt"):
                p.append("%s: every gallery image needs alt text" % tag)
        if s.get("image") and not s.get("image_alt"):
            p.append("%s: image_alt is required (describe the image)" % tag)
        for c in ctas(s):
            if not href_ok(c.get("href")):
                p.append("%s: link %r must be #section, https://..., mailto:, tel: or https://wa.me/<number>" % (tag, c.get("href")))
        if s.get("form"):
            p += ["%s: %s" % (tag, x) for x in form_problems(s["form"])]
    for i, s in enumerate(secs, 1):
        for c in ctas(s):
            h = str(c.get("href", ""))
            if h.startswith("#") and h[1:] not in ids:
                p.append("section %d: link %s points to no section (ids: %s)" % (i, h, ", ".join(sorted(ids))))
    return p


def ctas(s):
    out = [s[k] for k in ("cta", "cta2") if isinstance(s.get(k), dict)]
    for pl in s.get("plans", []):
        if isinstance(pl.get("cta"), dict):
            out.append(pl["cta"])
    return out


def form_problems(f):
    p = []
    a = f.get("action") or {}
    if a.get("type") not in ("whatsapp", "email", "url"):
        p.append("form.action.type must be whatsapp, email or url")
    elif a["type"] == "whatsapp" and not re.match(r"^\+?\d{8,15}$", str(a.get("to", "")).replace(" ", "")):
        p.append("form.action.to must be a WhatsApp number with country code, e.g. +919812345678")
    elif a["type"] == "email" and not re.match(r"^[^\s@]+@[^\s@]+\.[^\s@]+$", str(a.get("to", ""))):
        p.append("form.action.to must be an email address")
    elif a["type"] == "url" and not str(a.get("to", "")).startswith("https://"):
        p.append("form.action.to must be an https:// form endpoint (e.g. Formspree, Basin, Google Apps Script)")
    fields = f.get("fields", [])
    if not fields:
        p.append("form needs fields")
    for fl in fields:
        if not fl.get("name") or not fl.get("label"):
            p.append("every form field needs a name and a label")
        if fl.get("type", "text") not in FIELD_TYPES:
            p.append("field %s: type must be one of %s" % (fl.get("name"), ", ".join(FIELD_TYPES)))
        if fl.get("type") == "select" and not fl.get("options"):
            p.append("field %s: a select needs options" % fl.get("name"))
    return p


def path_in(bdir, p):
    return p if os.path.isabs(p) else os.path.join(bdir, p)


# ---------- assets ----------

def web_image(src, out_dir, name):
    """Resize to at most MAX_IMG wide; JPEG for photos, PNG when transparent."""
    from PIL import Image
    Image.init()
    os.makedirs(out_dir, exist_ok=True)
    with Image.open(src) as im:
        im.load()
        alpha = im.mode in ("RGBA", "LA") or (im.mode == "P" and "transparency" in im.info)
        if im.width > MAX_IMG:
            im = im.resize((MAX_IMG, round(im.height * MAX_IMG / im.width)))
        if alpha:
            dst = os.path.join(out_dir, name + ".png")
            im.convert("RGBA").save(dst, optimize=True)
        else:
            dst = os.path.join(out_dir, name + ".jpg")
            im.convert("RGB").save(dst, quality=82, optimize=True, progressive=True)
        return os.path.relpath(dst, os.path.dirname(out_dir)), im.size


def favicon(logo, out_dir, T):
    from PIL import Image
    Image.init()
    with Image.open(logo) as im:
        im = im.convert("RGBA")
        side = max(im.size)
        sq = Image.new("RGBA", (side, side), (0, 0, 0, 0))
        # the mark is usually the left part of a wide logo
        if im.width > im.height * 1.6:
            im = im.crop((0, 0, im.height, im.height))
            side = im.height
            sq = Image.new("RGBA", (side, side), (0, 0, 0, 0))
        sq.paste(im, ((side - im.width) // 2, (side - im.height) // 2), im)
        sq.resize((64, 64)).save(os.path.join(out_dir, "favicon.png"))
    return "assets/favicon.png"


# ---------- HTML ----------

CSS = """
*,*::before,*::after{box-sizing:border-box}
html{-webkit-text-size-adjust:100%%;scroll-behavior:smooth;scroll-padding-top:76px}
@media (prefers-reduced-motion:reduce){html{scroll-behavior:auto}}
body{margin:0;font-family:%(body)s;color:#%(text)s;background:#fff;font-size:17px;line-height:1.6}
img{max-width:100%%;height:auto;display:block}
a{color:inherit}
:focus-visible{outline:3px solid #%(accent_fill)s;outline-offset:3px;border-radius:4px}
.wrap{width:min(1120px,100%% - 40px);margin-inline:auto}
h1,h2,h3{font-family:%(head)s;line-height:1.1;margin:0;letter-spacing:-0.015em;text-wrap:balance;overflow-wrap:break-word}
h1{font-size:clamp(2.3rem,6.4vw,4.4rem);font-weight:750}
.hero .split h1{font-size:clamp(2.3rem,5vw,3.7rem)}
h2{font-size:clamp(1.8rem,4.2vw,2.9rem);font-weight:700}
h3{font-size:1.2rem;font-weight:700}
p{margin:0}
.lead{font-size:clamp(1.08rem,2vw,1.3rem);line-height:1.55}
section{padding:clamp(64px,10vw,120px) 0;position:relative}
.sec-head{max-width:720px;margin-bottom:clamp(32px,5vw,56px)}
.sec-head p{margin-top:16px;color:#%(body)s}
.t-tint{background:#%(tint)s}
.t-dark{background:#%(dark)s;color:#%(on_dark)s}
.t-dark .sec-head p,.t-dark .muted{color:#%(on_dark2)s}
.muted{color:#%(muted)s}
.btn{display:inline-flex;align-items:center;justify-content:center;min-height:52px;padding:0 26px;border-radius:999px;
  font-weight:700;font-size:1.02rem;text-decoration:none;border:2px solid transparent;cursor:pointer;font-family:inherit;line-height:1.2}
.btn-primary{background:#%(btn_bg)s;color:#%(btn_fg)s}
.btn-primary:hover{background:#%(btn_hover)s}
.btn-ghost{border-color:currentColor;background:transparent}
.t-dark .btn-ghost{color:#%(on_dark)s}
.actions{display:flex;flex-wrap:wrap;gap:12px;margin-top:32px}
/* nav */
.nav{position:sticky;top:0;z-index:20;background:#ffffffee;backdrop-filter:saturate(1.4) blur(10px);-webkit-backdrop-filter:saturate(1.4) blur(10px);border-bottom:1px solid #%(grid)s}
.nav .wrap{display:flex;align-items:center;justify-content:space-between;gap:16px;min-height:68px}
.brand{display:flex;align-items:center;gap:10px;text-decoration:none;font-weight:750;font-size:1.15rem;color:#%(text)s}
.brand img{height:34px;width:auto}
.nav ul{display:none;list-style:none;margin:0;padding:0;gap:28px}
.nav ul a{text-decoration:none;color:#%(body)s;font-weight:600;font-size:.98rem}
.nav ul a:hover{color:#%(text)s}
.nav .btn{min-height:44px;padding:0 18px;font-size:.95rem}
@media (min-width:880px){.nav ul{display:flex}}
/* hero */
.hero{overflow:hidden;padding:clamp(56px,9vw,112px) 0 clamp(64px,10vw,120px)}
.hero .grid{display:grid;gap:40px;align-items:center;position:relative;z-index:1}
.hero .lead{margin-top:22px;max-width:620px}
.hero.t-dark .lead{color:#%(on_dark2)s}
.hero .proof{margin-top:28px;font-size:.98rem;font-weight:600}
.hero.t-dark .proof{color:#%(accent)s}
.hero:not(.t-dark) .proof{color:#%(accent_dark)s}
.hero-img img{border-radius:20px;width:100%%;aspect-ratio:4/3;object-fit:cover}
@media (min-width:900px){.hero .grid.split{grid-template-columns:1.05fr .95fr;gap:64px}}
.motif{position:absolute;pointer-events:none;border-radius:50%%;z-index:0}
.dotgrid{position:absolute;pointer-events:none;z-index:0;right:4%%;top:48px;width:220px;height:180px;
  background-image:radial-gradient(#%(motif_ink)s 2.2px,transparent 2.6px);background-size:30px 30px}
/* logos */
.logos{padding:36px 0;border-bottom:1px solid #%(grid)s}
.logos .wrap{display:flex;flex-wrap:wrap;align-items:center;gap:14px 40px}
.logos .label{font-weight:600;color:#%(muted)s;font-size:.95rem;margin-right:8px}
.logos .name{font-weight:750;font-size:1.15rem;color:#%(body)s;opacity:.8}
.logos img{height:30px;width:auto;filter:grayscale(1);opacity:.75}
/* features: an editorial list, not boxes */
.features{display:grid;gap:0 48px}
@media (min-width:760px){.features.c2,.features.c4{grid-template-columns:1fr 1fr}.features.c3,.features.c5,.features.c6{grid-template-columns:repeat(3,1fr)}}
.feature{padding:28px 0;border-top:2px solid #%(grid_strong)s}
.feature .mark{display:block;width:14px;height:14px;border-radius:50%%;background:#%(accent_fill)s;margin-bottom:18px}
.feature p{margin-top:10px;color:#%(body)s}
.t-dark .feature{border-top-color:#%(dark_line)s}.t-dark .feature p{color:#%(on_dark2)s}
/* steps: a real sequence, so numbered */
.steps{display:grid;gap:28px;counter-reset:s;list-style:none;margin:0;padding:0}
@media (min-width:760px){.steps{grid-template-columns:repeat(var(--n),1fr)}}
.step{position:relative}
.step .n{display:flex;align-items:center;justify-content:center;width:52px;height:52px;border-radius:50%%;
  background:#%(accent_fill)s;color:#%(on_accent)s;font-weight:800;font-size:1.2rem;margin-bottom:20px}
.step p{margin-top:10px;color:#%(body)s}
/* stats */
.stats{display:grid;gap:36px;grid-template-columns:repeat(auto-fit,minmax(200px,1fr))}
.stat .v{font-family:%(head)s;font-weight:800;font-size:clamp(2.8rem,6vw,4.2rem);line-height:1;letter-spacing:-0.03em;color:#%(accent_dark)s}
.t-dark .stat .v{color:#%(accent)s}
.stat .l{margin-top:12px;font-weight:600}
/* testimonials */
.quotes{display:grid;gap:40px}
figure{margin:0}
@media (min-width:860px){.quotes.many{grid-template-columns:repeat(var(--n),1fr)}}
.quote blockquote{margin:0;font-family:%(head)s;font-size:clamp(1.25rem,2.3vw,1.6rem);line-height:1.35;font-weight:600}
.quote blockquote::before{content:"\\201C";display:block;font-size:4.2rem;line-height:.6;height:34px;color:#%(accent_fill)s}
.quote.one blockquote{font-size:clamp(1.5rem,3.4vw,2.4rem);max-width:900px}
.quote figcaption{margin-top:20px;font-weight:700}
.quote figcaption span{font-weight:400;color:#%(muted)s}
.t-dark .quote figcaption span{color:#%(on_dark2)s}
/* pricing */
.plans{display:grid;gap:20px;align-items:stretch}
@media (min-width:820px){.plans{grid-template-columns:repeat(var(--n),1fr)}}
.plan{border:1.5px solid #%(grid_strong)s;border-radius:22px;padding:32px 28px;display:flex;flex-direction:column;background:#fff}
.plan.hl{background:#%(dark)s;color:#%(on_dark)s;border-color:#%(dark)s}
.plan .tag{align-self:flex-start;font-size:.85rem;font-weight:700;padding:4px 12px;border-radius:99px;background:#%(accent_fill)s;color:#%(on_accent)s;margin-bottom:14px}
.plan .price{font-family:%(head)s;font-size:2.6rem;font-weight:800;margin-top:14px;letter-spacing:-0.02em}
.plan .per{font-size:1rem;font-weight:500;color:#%(muted)s}
.plan.hl .per,.plan.hl li{color:#%(on_dark2)s}
.plan ul{list-style:none;padding:0;margin:22px 0 28px;display:grid;gap:10px}
.plan li{padding-left:26px;position:relative;color:#%(body)s}
.plan li::before{content:"";position:absolute;left:2px;top:.62em;width:10px;height:10px;border-radius:50%%;background:#%(accent_fill)s}
.plan .btn{margin-top:auto}
.plan.hl .btn-primary{background:#%(accent)s;color:#%(on_accent_light)s}
.plan:not(.hl) .btn{background:transparent;border-color:#%(text)s;color:#%(text)s}
/* faq */
.faq{max-width:820px}
.faq details{border-top:1px solid #%(grid_strong)s;padding:6px 0}
.faq details:last-child{border-bottom:1px solid #%(grid_strong)s}
.faq summary{cursor:pointer;list-style:none;font-weight:700;font-size:1.12rem;padding:16px 40px 16px 0;position:relative}
.faq summary::-webkit-details-marker{display:none}
.faq summary::after{content:"+";position:absolute;right:4px;top:10px;font-size:1.7rem;font-weight:400;color:#%(accent_dark)s}
.faq details[open] summary::after{content:"\\2212"}
.faq details p{padding:0 0 18px;color:#%(body)s;max-width:700px}
/* gallery */
.gallery{display:grid;gap:14px;grid-template-columns:repeat(2,1fr)}
@media (min-width:860px){.gallery{grid-template-columns:repeat(3,1fr)}}
.gallery img{border-radius:14px;aspect-ratio:1;object-fit:cover;width:100%%}
/* text */
.s-text .grid{display:grid;gap:40px;align-items:center}
@media (min-width:880px){.s-text .grid.split{grid-template-columns:1fr 1fr;gap:64px}}
.s-text .body p+p{margin-top:16px}
.s-text .body{color:#%(body)s}
.s-text img{border-radius:20px}
/* cta + form */
.cta{overflow:hidden}
.cta .grid{display:grid;gap:40px;position:relative;z-index:1}
@media (min-width:900px){.cta .grid.with-form{grid-template-columns:1fr 1fr;gap:64px;align-items:start}}
form.lead{display:grid;gap:16px;background:#fff;color:#%(text)s;border-radius:22px;padding:28px}
.t-dark form.lead{box-shadow:0 0 0 1px #%(dark_line)s}
form.lead label{display:grid;gap:6px;font-weight:600;font-size:.96rem}
form.lead label > span{display:block}
form.lead input,form.lead select,form.lead textarea{font:inherit;font-size:1rem;padding:13px 14px;border:1.5px solid #%(grid_strong)s;border-radius:12px;background:#fff;color:#%(text)s;min-height:50px;width:100%%}
form.lead textarea{min-height:110px;resize:vertical}
form.lead input:focus,form.lead select:focus,form.lead textarea:focus{outline:none;border-color:#%(accent_dark)s;box-shadow:0 0 0 3px #%(focus_ring)s}
form.lead .btn{width:100%%}
form.lead .note{font-size:.88rem;color:#%(muted)s}
.req{color:#%(bad)s}
/* footer */
footer{background:#%(dark)s;color:#%(on_dark2)s;padding:48px 0 36px;font-size:.95rem}
footer .wrap{display:grid;gap:24px}
@media (min-width:760px){footer .wrap{grid-template-columns:1.2fr 1fr 1fr}}
footer .brand{color:#%(on_dark)s}
footer a{color:#%(on_dark2)s;text-decoration:none}
footer a:hover{color:#%(on_dark)s;text-decoration:underline}
footer ul{list-style:none;margin:0;padding:0;display:grid;gap:8px}
footer .small{grid-column:1/-1;border-top:1px solid #%(dark_line)s;padding-top:20px;color:#%(on_dark_muted)s;font-size:.88rem}
"""

FORM_JS = """
(function(){
  document.querySelectorAll('form.lead[data-kind]').forEach(function(f){
    f.addEventListener('submit', function(ev){
      var kind = f.dataset.kind; if (kind === 'url') return;
      ev.preventDefault();
      if (!f.reportValidity()) return;
      var lines = [];
      f.querySelectorAll('[name]').forEach(function(el){ if (el.value) lines.push(el.dataset.label + ': ' + el.value); });
      var text = (f.dataset.intro ? f.dataset.intro + '\\n' : '') + lines.join('\\n');
      if (kind === 'whatsapp') window.location.href = 'https://wa.me/' + f.dataset.to + '?text=' + encodeURIComponent(text);
      else window.location.href = 'mailto:' + f.dataset.to + '?subject=' + encodeURIComponent(f.dataset.subject || 'Enquiry') + '&body=' + encodeURIComponent(text);
    });
  });
})();
"""


def tokens(spec, bdir):
    T, note = BK.resolve_spec(spec, bdir)
    head, body = WEB_TYPES.get(spec.get("type", "modern"), WEB_TYPES["modern"])
    W = "FFFFFF"
    btn_bg = T["accent_fill"] if BK.contrast(T["accent_fill"], W) >= 3 else T["dark"]
    btn_fg = max((W, T["text"]), key=lambda c: BK.contrast(c, btn_bg))
    T2 = dict(T, head=head, body_font=body, btn_bg=btn_bg, btn_fg=btn_fg, btn_hover=BK.mix("000000", btn_bg, 0.12),
              on_accent=max((W, T["text"]), key=lambda c: BK.contrast(c, T["accent_fill"])),
              on_accent_light=max((W, T["text"]), key=lambda c: BK.contrast(c, T["accent"])),
              grid_strong=BK.mix(T["dark"], W, 0.16), dark_line=BK.mix(W, T["dark"], 0.16),
              focus_ring=BK.mix(T["accent_fill"], W, 0.3))
    return T2, note


def button(c, cls="btn-primary", primary=False):
    attrs = ' data-cta="primary"' if primary else ""
    ext = ' target="_blank" rel="noopener"' if str(c["href"]).startswith(("http", "https://wa.me")) and not str(c["href"]).startswith("#") else ""
    return '<a class="btn %s" href="%s"%s%s>%s</a>' % (cls, e(c["href"]), attrs, ext, e(c["label"]))


def sec_head(s, level="h2"):
    sub = '<p class="lead">%s</p>' % e(s["sub"]) if s.get("sub") else ""
    return '<div class="sec-head"><%s data-check>%s</%s>%s</div>' % (level, e(s["headline"]), level, sub)


def tone(s, i, mode):
    if s.get("tone") in ("light", "tint", "dark"):
        return {"light": "", "tint": "t-tint", "dark": "t-dark"}[s["tone"]]
    if s["type"] == "hero":
        return "t-dark" if mode == "contrast" else "t-tint"
    if s["type"] == "cta":
        return "t-dark" if mode == "contrast" else "t-tint"
    if s["type"] in ("stats", "testimonials"):
        return "t-tint"
    return ""


def motif_html(T, bg):
    style = T.get("motif", "orb")
    ink = "FFFFFF" if BK.luminance(bg) < 0.3 else T["dark"]
    if style == "dots":
        return ""
    if style == "rings":
        return "".join('<div class="motif" style="width:%dpx;height:%dpx;right:%dpx;top:%dpx;border:2px solid #%s"></div>' % (
            d, d, -d // 3 + (900 - d) // 2 - 160, -d // 2 + 140 + (900 - d) // 2 - 260, BK.mix(ink, bg, 0.14)) for d in (900, 680, 460, 240))
    return '<div class="motif" style="width:820px;height:820px;right:-260px;top:-300px;background:#%s"></div>' % BK.mix(ink, bg, 0.07)


def render_section(s, i, spec, T, imgs, mode):
    t = s["type"]
    cls = tone(s, i, mode)
    sid = section_id(s, i)
    bg = {"t-dark": T["dark"], "t-tint": T["tint"]}.get(cls, "FFFFFF")
    if t == "hero":
        img = imgs.get(s.get("image"))
        acts = ""
        if s.get("cta"):
            acts += button(s["cta"], primary=True)
        if s.get("cta2"):
            acts += button(s["cta2"], "btn-ghost")
        text = '<div><h1 data-check>%s</h1>%s%s%s</div>' % (
            e(s["headline"]), '<p class="lead">%s</p>' % e(s["sub"]) if s.get("sub") else "",
            '<div class="actions">%s</div>' % acts if acts else "", '<p class="proof">%s</p>' % e(s["proof"]) if s.get("proof") else "")
        pic = '<div class="hero-img"><img src="%s" alt="%s" width="%d" height="%d" fetchpriority="high"></div>' % (
            e(img[0]), e(s.get("image_alt", "")), img[1][0], img[1][1]) if img else ""
        deco = motif_html(T, bg) + ('<div class="dotgrid" style="--c:1"></div>' if T.get("motif") == "dots" else "")
        return '<section class="hero %s" id="%s">%s<div class="wrap"><div class="grid %s">%s%s</div></div></section>' % (
            cls, sid, deco, "split" if img else "", text, pic)
    if t == "logos":
        items = "".join('<img src="%s" alt="%s">' % (e(imgs[x["logo"]][0]), e(x.get("name", ""))) if isinstance(x, dict) and x.get("logo") in imgs
                        else '<span class="name">%s</span>' % e(x["name"] if isinstance(x, dict) else x) for x in s["items"])
        return '<div class="logos" id="%s"><div class="wrap"><span class="label">%s</span>%s</div></div>' % (sid, e(s.get("label", "Trusted by")), items)
    head = sec_head(s)
    if t == "features":
        n = len(s["items"])
        body = '<div class="features c%d">%s</div>' % (n, "".join(
            '<div class="feature"><span class="mark"></span><h3 data-check>%s</h3><p>%s</p></div>' % (e(x["title"]), e(x.get("text", ""))) for x in s["items"]))
    elif t == "steps":
        body = '<ol class="steps" style="--n:%d">%s</ol>' % (len(s["items"]), "".join(
            '<li class="step"><span class="n">%d</span><h3 data-check>%s</h3><p>%s</p></li>' % (k, e(x["title"]), e(x.get("text", ""))) for k, x in enumerate(s["items"], 1)))
    elif t == "stats":
        body = '<div class="stats">%s</div>' % "".join(
            '<div class="stat"><div class="v" data-check>%s</div><p class="l">%s</p></div>' % (e(x["value"]), e(x["label"])) for x in s["items"])
    elif t == "testimonials":
        n = len(s["items"])
        body = '<div class="quotes %s" style="--n:%d">%s</div>' % ("many" if n > 1 else "", n, "".join(
            '<figure class="quote %s"><blockquote>%s</blockquote><figcaption>%s%s</figcaption></figure>' % (
                "one" if n == 1 else "", e(x["quote"]), e(x["author"]), "<span>, %s</span>" % e(x["role"]) if x.get("role") else "") for x in s["items"]))
    elif t == "pricing":
        ps = s["plans"]
        body = '<div class="plans" style="--n:%d">%s</div>' % (len(ps), "".join(
            '<div class="plan %s">%s<h3>%s</h3><div class="price">%s <span class="per">%s</span></div>%s<ul>%s</ul>%s</div>' % (
                "hl" if pl.get("highlight") else "", '<span class="tag">%s</span>' % e(pl["tag"]) if pl.get("tag") else "",
                e(pl["name"]), e(pl["price"]), e(pl.get("period", "")),
                '<p class="muted" style="margin-top:8px">%s</p>' % e(pl["note"]) if pl.get("note") else "",
                "".join("<li>%s</li>" % e(f) for f in pl.get("features", [])),
                button(pl["cta"]) if pl.get("cta") else "") for pl in ps))
    elif t == "faq":
        body = '<div class="faq">%s</div>' % "".join(
            '<details><summary>%s</summary><p>%s</p></details>' % (e(x["q"]), e(x["a"])) for x in s["items"])
    elif t == "gallery":
        body = '<div class="gallery">%s</div>' % "".join(
            '<img src="%s" alt="%s" loading="lazy" width="%d" height="%d">' % (e(imgs[g["src"]][0]), e(g["alt"]), imgs[g["src"]][1][0], imgs[g["src"]][1][1]) for g in s["images"])
    elif t == "text":
        img = imgs.get(s.get("image"))
        paras = "".join("<p>%s</p>" % e(x) for x in (s.get("body") if isinstance(s.get("body"), list) else [s.get("body", "")]) if x)
        pic = '<img src="%s" alt="%s" loading="lazy" width="%d" height="%d">' % (e(img[0]), e(s.get("image_alt", "")), img[1][0], img[1][1]) if img else ""
        acts = '<div class="actions">%s</div>' % button(s["cta"]) if s.get("cta") else ""
        body = ""
        head = '<div class="grid %s"><div><h2 data-check>%s</h2><div class="body" style="margin-top:20px">%s</div>%s</div>%s</div>' % (
            "split" if img else "", e(s["headline"]), paras, acts, pic)
    elif t == "cta":
        form = form_html(s["form"], T) if s.get("form") else ""
        acts = '<div class="actions">%s</div>' % button(s["cta"], primary=False) if s.get("cta") else ""
        c = spec.get("contact", {})
        alt = []
        if c.get("phone"):
            alt.append('call <a href="tel:%s">%s</a>' % (e(re.sub(r"[^\d+]", "", c["phone"])), e(c["phone"])))
        if c.get("email"):
            alt.append('email <a href="mailto:%s">%s</a>' % (e(c["email"]), e(c["email"])))
        if alt and s.get("form"):
            acts += '<p class="muted" style="margin-top:28px">Prefer to talk? You can also %s.</p>' % " or ".join(alt)
        deco = motif_html(T, bg) if cls == "t-dark" else ""
        return '<section class="cta %s" id="%s">%s<div class="wrap"><div class="grid %s"><div>%s%s</div>%s</div></div></section>' % (
            cls, sid, deco, "with-form" if form else "", sec_head(s), acts, form)
    return '<section class="s-%s %s" id="%s"><div class="wrap">%s%s</div></section>' % (t, cls, sid, head, body)


def form_html(f, T):
    a = f["action"]
    to = str(a.get("to", "")).replace(" ", "").lstrip("+") if a["type"] == "whatsapp" else str(a.get("to", ""))
    rows = []
    for fl in f["fields"]:
        req = fl.get("required", True)
        star = ' <span class="req" aria-hidden="true">*</span>' if req else ""
        common = 'name="%s" data-label="%s"%s' % (e(fl["name"]), e(fl["label"]), " required" if req else "")
        typ = fl.get("type", "text")
        auto = {"email": "email", "tel": "tel", "text": "name" if "name" in fl["name"].lower() else "on"}.get(typ, "on")
        if typ == "textarea":
            ctl = "<textarea %s></textarea>" % common
        elif typ == "select":
            ctl = '<select %s><option value="">Choose</option>%s</select>' % (common, "".join("<option>%s</option>" % e(o) for o in fl["options"]))
        else:
            ctl = '<input type="%s" %s autocomplete="%s"%s>' % (typ, common, auto, ' inputmode="tel"' if typ == "tel" else "")
        rows.append("<label><span>%s%s</span>%s</label>" % (e(fl["label"]), star, ctl))
    note = {"whatsapp": "Sending opens WhatsApp with your details filled in.", "email": "Sending opens your email app with your details filled in.",
            "url": ""}[a["type"]]
    note = f.get("note", note)
    attrs = 'data-kind="%s" data-to="%s" data-subject="%s" data-intro="%s"' % (a["type"], e(to), e(f.get("subject", "Enquiry")), e(f.get("intro", "")))
    if a["type"] == "url":
        attrs += ' action="%s" method="post"' % e(a["to"])
    return '<form class="lead" %s>%s<button class="btn btn-primary" type="submit">%s</button>%s</form>' % (
        attrs, "".join(rows), e(f.get("submit", "Send")), '<p class="note">%s</p>' % e(note) if note else "")


def nav_html(spec, T, logo_rel):
    b = spec["brand"]
    links = [(section_id(s, i), s["nav"]) for i, s in enumerate(spec["sections"], 1) if s.get("nav")][:4]
    brand = '<a class="brand" href="#top">%s</a>' % ('<img src="%s" alt="%s">' % (e(logo_rel), e(b["name"])) if logo_rel else e(b["name"]))
    cta = spec.get("nav_cta") or next((s["cta"] for s in spec["sections"] if s["type"] == "hero" and s.get("cta")), None)
    return '<header class="nav"><div class="wrap">%s<nav aria-label="Sections"><ul>%s</ul></nav>%s</div></header>' % (
        brand, "".join('<li><a href="#%s">%s</a></li>' % (e(i), e(l)) for i, l in links), button(cta) if cta else "")


def footer_html(spec, T, logo_rel):
    b = spec["brand"]
    c = spec.get("contact", {})
    contact = []
    if c.get("phone"):
        contact.append('<li><a href="tel:%s">%s</a></li>' % (e(re.sub(r"[^\d+]", "", c["phone"])), e(c["phone"])))
    if c.get("whatsapp"):
        contact.append('<li><a href="https://wa.me/%s" target="_blank" rel="noopener">WhatsApp</a></li>' % e(re.sub(r"\D", "", c["whatsapp"])))
    if c.get("email"):
        contact.append('<li><a href="mailto:%s">%s</a></li>' % (e(c["email"]), e(c["email"])))
    if c.get("address"):
        contact.append("<li>%s</li>" % e(c["address"]))
    social = "".join('<li><a href="%s" target="_blank" rel="noopener">%s</a></li>' % (e(x["url"]), e(x["label"])) for x in spec.get("social", []))
    links = "".join('<li><a href="%s">%s</a></li>' % (e(x["href"]), e(x["label"])) for x in spec.get("footer_links", []))
    year = datetime.date.today().year
    tagline = '<p style="margin-top:12px;max-width:340px">%s</p>' % e(b["tagline"]) if b.get("tagline") else ""
    return ('<footer><div class="wrap"><div><span class="brand">%s</span>%s</div><ul>%s</ul><ul>%s%s</ul>'
            '<p class="small">&copy; %d %s. %s</p></div></footer>') % (
        e(b["name"]), tagline, "".join(contact), social, links, year, e(b.get("legal_name", b["name"])), e(spec.get("footer_note", "")))


def build(spec, bdir, out):
    T, note = tokens(spec, bdir)
    adir = os.path.join(out, "assets")
    if os.path.isdir(adir):
        shutil.rmtree(adir)
    os.makedirs(adir)
    imgs = {}
    k = 0
    for s in spec["sections"]:
        srcs = [s.get("image")] + [g.get("src") for g in s.get("images", []) if isinstance(g, dict)] + \
               [x.get("logo") for x in s.get("items", []) if isinstance(x, dict) and x.get("logo")]
        for src in srcs:
            if src and src not in imgs:
                k += 1
                imgs[src] = web_image(path_in(bdir, src), adir, "img-%02d-%s" % (k, slug(os.path.splitext(os.path.basename(src))[0])[:24]))
    logo_rel = fav = None
    if T.get("logo"):
        logo_rel = web_image(T["logo"], adir, "logo")[0]
        fav = favicon(T["logo"], adir, T)
    meta = spec["meta"]
    mode = spec.get("mode", "contrast")
    dot_ink = BK.mix("FFFFFF", T["dark"], 0.22) if mode == "contrast" else BK.mix(T["dark"], T["tint"], 0.22)
    css = CSS % dict(T, body=T["body_font"], motif_ink=dot_ink)
    hero_img = next((imgs[s["image"]][0] for s in spec["sections"] if s["type"] == "hero" and s.get("image") in imgs), None)
    url = meta.get("url", "").rstrip("/")
    head = ['<meta charset="utf-8">', '<meta name="viewport" content="width=device-width, initial-scale=1">',
            "<title>%s</title>" % e(meta["title"]), '<meta name="description" content="%s">' % e(meta["description"]),
            '<meta name="theme-color" content="#%s">' % T["dark"],
            '<meta property="og:type" content="website">', '<meta property="og:title" content="%s">' % e(meta.get("og_title", meta["title"])),
            '<meta property="og:description" content="%s">' % e(meta["description"]),
            '<meta name="twitter:card" content="%s">' % ("summary_large_image" if hero_img else "summary")]
    if url:
        head += ['<link rel="canonical" href="%s/">' % e(url), '<meta property="og:url" content="%s/">' % e(url)]
        if hero_img:
            head.append('<meta property="og:image" content="%s/%s">' % (e(url), e(hero_img)))
    if fav:
        head.append('<link rel="icon" type="image/png" href="%s">' % fav)
    org = {"@context": "https://schema.org", "@type": spec.get("schema_type", "Organization"), "name": spec["brand"]["name"]}
    if url:
        org["url"] = url + "/"
    c = spec.get("contact", {})
    if c.get("phone"):
        org["telephone"] = c["phone"]
    if c.get("email"):
        org["email"] = c["email"]
    if c.get("address"):
        org["address"] = c["address"]
    head.append('<script type="application/ld+json">%s</script>' % json.dumps(org, ensure_ascii=False).replace("</", "<\\/"))
    head.append("<style>%s</style>" % css)
    body = nav_html(spec, T, logo_rel) + "<main>" + "".join(render_section(s, i, spec, T, imgs, mode) for i, s in enumerate(spec["sections"], 1)) + \
        "</main>" + footer_html(spec, T, logo_rel)
    doc = '<!doctype html>\n<html lang="%s">\n<head>\n%s\n</head>\n<body>\n%s\n<script>%s</script>\n</body>\n</html>\n' % (
        e(spec.get("language", "en")), "\n".join(head), body, FORM_JS)
    with open(os.path.join(out, "index.html"), "w", encoding="utf-8") as f:
        f.write(doc)
    return note


def main():
    a = sys.argv[1:]
    if not a:
        sys.exit(__doc__)
    src = a[0]
    out = a[a.index("--out") + 1] if "--out" in a else "site"
    spec = load(src)
    bdir = os.path.dirname(os.path.abspath(src))
    probs = validate(spec, bdir)
    if probs:
        sys.exit("spec problems:\n- " + "\n- ".join(probs))
    os.makedirs(out, exist_ok=True)
    note = build(spec, bdir, out)
    side = os.path.dirname(os.path.abspath(out.rstrip("/")))  # files that must not be published go next to the site folder
    with open(os.path.join(side, "page.spec.json"), "w", encoding="utf-8") as f:
        json.dump(spec, f, indent=2, ensure_ascii=False)
    made = ["index.html", "assets/"]
    if "--no-shots" not in a:
        shots = {1440: os.path.join(side, "preview-desktop.png"), 390: os.path.join(side, "preview-mobile.png")}
        if render.inspect(os.path.join(out, "index.html"), "() => true", widths=(390, 1440), height=900, shots=shots) is not None:
            made += ["preview-desktop.png", "preview-mobile.png (next to the site folder)"]
    if note:
        print(note + " (tell the user; they can set brand.primary and brand.accent to change them)")
    print("built %s: %s" % (out, ", ".join(made)))


if __name__ == "__main__":
    main()
