"""Build brand guidelines (PDF) and brand tokens from brand.json.

Usage: python3 build_brand.py brand.json --out brand [--no-pdf]
Writes <out>-guide.pdf (cover, logo, colours with contrast results, type,
voice), <out>-tokens.json (tokens plus a ready "brand" block for every MangoMan
pack), <out>.css (CSS variables) and <out>-guide.html.
"""
import html
import json
import os
import sys

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
import brandkit as BK  # noqa: E402
import render  # noqa: E402

e = lambda s: html.escape(str(s if s is not None else ""), quote=True)
ROLES = [("dark", "Base", "Dark backgrounds, headings"), ("accent_fill", "Accent", "Buttons, highlights, charts"),
         ("text", "Text", "Body text on light backgrounds"), ("body", "Secondary text", "Supporting copy"),
         ("muted", "Muted", "Captions, labels"), ("tint", "Tint", "Panels and light sections"),
         ("grid", "Lines", "Dividers, table rules"), ("good", "Positive", "Gains, success"), ("bad", "Negative", "Losses, errors")]


def load(path):
    with open(path, encoding="utf-8") as f:
        return json.load(f)


def validate(spec, bdir="."):
    p = []
    if not spec.get("name"):
        p.append("name is required")
    if not (spec.get("logo") or spec.get("primary") or spec.get("theme")):
        p.append("give a logo, brand colours (primary, accent) or a theme")
    for k in ("primary", "accent"):
        if spec.get(k):
            try:
                BK.hexc(spec[k])
            except ValueError:
                p.append("%s must be a hex colour like #1F5FA8" % k)
    if len(spec.get("extra_colors", [])) > 4:
        p.append("at most 4 extra colours (a palette people can remember)")
    for c in spec.get("extra_colors", []):
        if not c.get("name"):
            p.append("every extra colour needs a name")
        try:
            BK.hexc(c.get("hex", ""))
        except ValueError:
            p.append("extra colour %s needs a hex value" % c.get("name"))
    if spec.get("logo"):
        pr = BK.logo_check(os.path.join(bdir, spec["logo"]))
        if pr:
            p.append(pr)
    if spec.get("theme") and spec["theme"] not in BK.CURATED:
        p.append("theme must be one of %s" % ", ".join(BK.CURATED))
    if spec.get("type") and spec["type"] not in BK.TYPES:
        p.append("type must be one of %s" % ", ".join(BK.TYPES))
    return p


def tokens(spec, bdir):
    brand = {k: spec[k] for k in ("primary", "accent", "logo") if spec.get(k)}
    T, note = BK.resolve_spec({"brand": brand, "theme": spec.get("theme", "ink"), "type": spec.get("type", "modern")}, bdir)
    a = T["accent_fill"]  # a vivid accent can be too light for button text: deepen it until white reads on it
    T["button"] = a if max(BK.contrast("FFFFFF", a), BK.contrast(T["text"], a)) >= 4.5 else BK._toward(a, "000000", "FFFFFF", 4.5)
    return T, note


def pairs(T):
    """The text and background pairs the packs use, with the contrast each needs."""
    return [("Text on white", T["text"], "FFFFFF", 4.5), ("Secondary text on white", T["body"], "FFFFFF", 4.5),
            ("Muted text on white (captions)", T["muted"], "FFFFFF", 4.5), ("White on base", "FFFFFF", T["dark"], 4.5),
            ("Accent text on white", T["accent_dark"], "FFFFFF", 3.0), ("Accent on base", T["accent"], T["dark"], 3.0),
            ("Button text on button colour", max(("FFFFFF", T["text"]), key=lambda c: BK.contrast(c, T["button"])), T["button"], 4.5)]


def swatch(hexv, name, use):
    r, g, b = (int(hexv[i:i + 2], 16) for i in (0, 2, 4))
    ink = "FFFFFF" if BK.luminance(hexv) < 0.4 else "1D1D1B"
    return ('<div class="sw"><div class="chip" style="background:#%s;color:#%s">%s</div><b>%s</b><span>#%s · RGB %d %d %d</span><span class="u">%s</span></div>' % (
        hexv, ink, e(name), e(name), hexv, r, g, b, e(use)))


def build_html(spec, T):
    name = spec["name"]
    logo = '<img src="%s" alt="%s logo">' % (BK.data_uri(T["logo"]), e(name)) if T.get("logo") else '<b class="word">%s</b>' % e(name)
    chip = "#FFFFFF" if T.get("logo") and BK.logo_hidden_share(T["logo"], T["dark"]) > 0.15 else "transparent"
    cover = '<section class="cover"><div class="lg" style="background:%s">%s</div><h1>%s</h1><p>%s</p><span>Brand guidelines</span></section>' % (
        chip, logo, e(name), e(spec.get("tagline", "")))
    logo_page = ('<section id="logo"><h2>Logo</h2><div class="three"><div class="on" style="background:#fff">%s</div><div class="on" style="background:#%s">%s</div>'
                 '<div class="on" style="background:#%s">%s</div></div><ul><li>Keep clear space around the logo of at least the height of its mark.</li>'
                 '<li>Use it at least %s wide on screen and 25 mm in print.</li><li>On dark or busy backgrounds, place it on a white rounded panel, as above.</li>'
                 '<li>Do not stretch, recolour, outline or add effects.</li></ul></section>') % (
        logo, T["tint"], logo, T["dark"], '<div class="panel">%s</div>' % logo if chip != "transparent" else logo, spec.get("logo_min", "120 px"))
    roles = ROLES if T["button"] == T["accent_fill"] else ROLES[:1] + [("accent_fill", "Accent", "Highlights, charts"), ("button", "Button", "Buttons (a deeper accent, so button text reads)")] + ROLES[2:]
    sw = "".join(swatch(T[k], n, u) for k, n, u in roles) + "".join(swatch(BK.hexc(c["hex"]), c["name"], c.get("use", "")) for c in spec.get("extra_colors", []))
    rows = "".join('<tr><td>%s</td><td><span class="pv" style="color:#%s;background:#%s">Aa 123</span></td><td>%.1f : 1</td><td class="%s">%s (needs %.1f)</td></tr>' % (
        e(lab), fg, bg, BK.contrast(fg, bg), "ok" if BK.contrast(fg, bg) >= need else "no", "Pass" if BK.contrast(fg, bg) >= need else "Fails", need)
        for lab, fg, bg, need in pairs(T))
    colours = '<section id="colour"><h2>Colour</h2><div class="sws">%s</div><h3>Readable combinations</h3><table><tr><th>Use</th><th>Sample</th><th>Contrast</th><th>WCAG AA</th></tr>%s</table></section>' % (sw, rows)
    head, body = T["html_head"], T["html_body"]
    typo = ('<section id="type"><h2>Type</h2><p class="tf" style="font-family:%s;font-size:30pt;font-weight:700">%s for headlines</p>'
            '<p class="tf" style="font-family:%s;font-size:12pt">%s for text. Body copy is set at 10 to 12 points on paper and 16 to 18 pixels on screen, with headlines two to three times larger.</p>'
            '<table><tr><th>Use</th><th>Font</th><th>Size</th></tr><tr><td>Slide and page titles</td><td>%s bold</td><td>36 to 46 pt</td></tr>'
            '<tr><td>Section headings</td><td>%s bold</td><td>20 to 28 pt</td></tr><tr><td>Body</td><td>%s</td><td>10.5 to 12 pt, 16 to 18 px</td></tr>'
            '<tr><td>Captions</td><td>%s</td><td>8 to 10 pt</td></tr></table></section>') % (
        e(head), T["font_head"], e(body), T["font_body"], T["font_head"], T["font_head"], T["font_body"], T["font_body"])
    v = spec.get("voice", {})
    lst = lambda xs: "<ul>%s</ul>" % "".join("<li>%s</li>" % e(x) for x in xs)
    voice = '<section id="voice"><h2>Voice</h2>'
    if v.get("personality"):
        voice += '<p class="pers">%s</p>' % " · ".join(e(x) for x in v["personality"])
    voice += '<div class="two"><div><h3>We are</h3>%s</div><div><h3>We are not</h3>%s</div><div><h3>Do</h3>%s</div><div><h3>Don\'t</h3>%s</div></div>' % (
        lst(v.get("we_are", [])), lst(v.get("we_are_not", [])), lst(v.get("do", [])), lst(v.get("dont", [])))
    if v.get("sample"):
        voice += '<div class="sample"><b style="font-family:%s">%s</b><p>%s</p></div>' % (e(head), e(v["sample"].get("headline", "")), e(v["sample"].get("text", "")))
    voice += "</section>"
    css = """
@page { size: A4 landscape; margin: 0; }
* { box-sizing: border-box; } html { -webkit-print-color-adjust: exact; print-color-adjust: exact; }
body { margin: 0; font: 11pt/1.5 %(body)s; color: #%(text)s; }
section { width: 297mm; height: 210mm; padding: 16mm 20mm; break-after: page; overflow: hidden; position: relative; }
h2 { font: 700 26pt %(head)s; margin: 0 0 8mm; } h3 { font: 700 13pt %(head)s; margin: 6mm 0 2mm; }
.cover { background: #%(dark)s; color: #fff; display: flex; flex-direction: column; justify-content: center; }
.cover .lg { align-self: flex-start; border-radius: 4mm; padding: 4mm 6mm; } .cover img { height: 22mm; display: block; }
.cover h1 { font: 700 40pt %(head)s; margin: 10mm 0 2mm; } .cover p { font-size: 15pt; color: #%(on_dark2)s; margin: 0; }
.cover span { position: absolute; bottom: 16mm; left: 20mm; color: #%(accent)s; font-weight: 700; }
.word { font: 700 24pt %(head)s; }
.three { display: grid; grid-template-columns: repeat(3, 1fr); gap: 6mm; }
.on { height: 60mm; border-radius: 4mm; display: flex; align-items: center; justify-content: center; border: 1px solid #%(grid)s; }
.on img { max-width: 70%%; max-height: 22mm; } .panel { background: #fff; border-radius: 3mm; padding: 3mm 5mm; display: flex; } .panel img { height: 14mm; }
.sws { display: grid; grid-template-columns: repeat(7, 1fr); gap: 4mm 4mm; }
.sw .chip { height: 17mm; border-radius: 3mm; display: flex; align-items: flex-end; padding: 2mm; font-size: 8pt; border: 1px solid #%(grid)s; }
.sw b { display: block; margin-top: 1.5mm; font-size: 10pt; } .sw span { display: block; font-size: 8.5pt; color: #%(muted)s; } .sw .u { color: #%(body)s; }
table { border-collapse: collapse; width: 100%%; font-size: 9.5pt; } th { text-align: left; color: #%(muted)s; border-bottom: 1.5px solid #%(text)s; padding: 1.5mm 2mm; }
td { padding: 1.5mm 2mm; border-bottom: 1px solid #%(grid)s; } .pv { padding: 1mm 3mm; border-radius: 2mm; font-weight: 700; border: 1px solid #%(grid)s; }
.ok { color: #%(good)s; font-weight: 700; } .no { color: #%(bad)s; font-weight: 700; }
.tf { margin: 0 0 4mm; } .pers { font: 700 16pt %(head)s; color: #%(accent_dark)s; }
.two { display: grid; grid-template-columns: repeat(4, 1fr); gap: 6mm; } ul { padding-left: 5mm; margin: 0; } li { margin-bottom: 1.5mm; }
.sample { margin-top: 6mm; background: #%(tint)s; border-radius: 4mm; padding: 6mm 8mm; } .sample b { font-size: 17pt; } .sample p { margin: 2mm 0 0; }
""" % dict(T, head=head, body=body)
    return '<!doctype html><html lang="en"><head><meta charset="utf-8"><title>%s brand guidelines</title><style>%s</style></head><body>%s%s%s%s%s</body></html>' % (
        e(name), css, cover, logo_page, colours, typo, voice)


def main():
    a = sys.argv[1:]
    if not a:
        sys.exit(__doc__)
    spec = load(a[0])
    out = a[a.index("--out") + 1] if "--out" in a else "brand"
    bdir = os.path.dirname(os.path.abspath(a[0]))
    probs = validate(spec, bdir)
    if probs:
        sys.exit("spec problems:\n- " + "\n- ".join(probs))
    T, note = tokens(spec, bdir)
    keep = ("dark", "dark2", "accent", "accent_fill", "accent_dark", "button", "text", "body", "muted", "tint", "grid", "dim", "good", "bad")
    block = {"brand": {"name": spec["name"], "primary": "#" + T["dark"], "accent": "#" + T["accent_fill"], **({"logo": spec["logo"]} if spec.get("logo") else {})},
             "type": spec.get("type", "modern")}
    with open(out + "-tokens.json", "w", encoding="utf-8") as f:
        json.dump({"name": spec["name"], "colors": {k: "#" + T[k] for k in keep}, "fonts": {"head": T["font_head"], "body": T["font_body"]},
                   "use_in_packs": block}, f, indent=2, ensure_ascii=False)
    with open(out + ".css", "w", encoding="utf-8") as f:
        f.write(":root {\n%s\n  --font-head: %s;\n  --font-body: %s;\n}\n" % ("\n".join("  --%s: #%s;" % (k.replace("_", "-"), T[k]) for k in keep), T["html_head"], T["html_body"]))
    with open(out + "-guide.html", "w", encoding="utf-8") as f:
        f.write(build_html(spec, T))
    made = [out + "-tokens.json", out + ".css", out + "-guide.html"]
    if "--no-pdf" not in a:
        try:
            render.html_to_pdf(out + "-guide.html", out + "-guide.pdf")
            made.append(out + "-guide.pdf")
        except Exception as ex:  # noqa: BLE001
            print("PDF skipped: %s" % ex)
    if note:
        print(note)
    print("built " + ", ".join(made))


if __name__ == "__main__":
    main()
