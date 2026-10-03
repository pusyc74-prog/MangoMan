"""Build an email campaign (a sequence of emails) from campaign.json.

Usage: python3 build_emails.py campaign.json --out emails [--no-shots]
Writes, per email: <id>.html (ready to paste into your email tool: tables and
inline styles, 600 px, works in Gmail, Outlook and Apple Mail, dark-mode safe),
<id>.txt (plain-text version), preview/<id>-desktop.png and -mobile.png; plus
sequence.md (schedule, subjects, preview text, alternatives to test) and
assets/ (images to upload).

Merge tags (first name, unsubscribe link) are written in the syntax of the
email tool you name: mailchimp, klaviyo, brevo, mailerlite or generic.
"""
import html
import json
import os
import re
import shutil
import sys

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
import brandkit as BK  # noqa: E402
import render  # noqa: E402

BLOCKS = ("heading", "text", "button", "image", "list", "products", "quote", "coupon", "divider", "spacer")
ESP = {  # first name (with fallback), unsubscribe URL, view-in-browser URL (or None)
    "mailchimp": ("*|FNAME|*", "*|UNSUB|*", "*|ARCHIVE|*"),
    "klaviyo": ("{{ first_name|default:'%s' }}", "{% unsubscribe_url %}", None),
    "brevo": ('{{ contact.FIRSTNAME | default : "%s" }}', "{{ unsubscribe }}", "{{ mirror }}"),
    "mailerlite": ("{$name}", "{$unsubscribe}", "{$url}"),
    "generic": ("{{first_name}}", "{{unsubscribe_url}}", None),
}
SAMPLE = {"first_name": "Priya"}
FONT = "Arial, Helvetica, sans-serif"
e = lambda s: html.escape(str(s if s is not None else ""), quote=True)


def load(path):
    with open(path, encoding="utf-8") as f:
        return json.load(f)


def validate(spec, bdir="."):
    p = []
    s = spec.get("sender") or {}
    if not s.get("name"):
        p.append("sender.name is required")
    if not s.get("address"):
        p.append("sender.address (a postal address) is required in every marketing email by anti-spam law")
    if spec.get("esp", "generic") not in ESP:
        p.append("esp must be one of %s" % ", ".join(ESP))
    for key, allowed in (("theme", tuple(BK.CURATED)),):
        if spec.get(key) is not None and spec[key] not in allowed:
            p.append("%s must be one of %s" % (key, ", ".join(allowed)))
    b = spec.get("brand") or {}
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
    base = spec.get("assets_base_url", "")
    if base and not base.startswith("https://"):
        p.append("assets_base_url must start with https:// (where you uploaded the images)")
    emails = spec.get("emails", [])
    if not emails:
        p.append("no emails")
    ids, last = set(), -1
    for i, m in enumerate(emails, 1):
        tag = "email %d (%s)" % (i, m.get("id", "?"))
        if not re.match(r"^[a-z0-9][a-z0-9-]*$", str(m.get("id", ""))):
            p.append("%s: id must be lowercase letters, digits and dashes" % tag)
        if m.get("id") in ids:
            p.append("%s: duplicate id" % tag)
        ids.add(m.get("id"))
        for k in ("subject", "preview"):
            if not m.get(k):
                p.append("%s: needs %s" % (tag, k))
        d = m.get("send_day")
        if d is not None:
            if not isinstance(d, (int, float)) or d < 0:
                p.append("%s: send_day is a number of days after the start (0 = day one)" % tag)
            elif d < last:
                p.append("%s: send_day %s is before the previous email's day %s; list emails in sending order" % (tag, d, last))
            else:
                last = d
        if not m.get("blocks"):
            p.append("%s: needs blocks" % tag)
        for j, bl in enumerate(m.get("blocks", []), 1):
            t = bl.get("type")
            if t not in BLOCKS:
                p.append("%s block %d: type must be one of %s" % (tag, j, ", ".join(BLOCKS)))
                continue
            if t == "button" and not (bl.get("label") and bl.get("href")):
                p.append("%s block %d: a button needs label and href" % (tag, j))
            if t == "image":
                if not bl.get("src") or not bl.get("alt"):
                    p.append("%s block %d: an image needs src and alt" % (tag, j))
                elif not bl["src"].startswith("https://") and not os.path.exists(path_in(bdir, bl["src"])):
                    p.append("%s block %d: image not found: %s" % (tag, j, bl["src"]))
            if t == "products":
                for pr in bl.get("items", []):
                    if not pr.get("name") or not pr.get("href"):
                        p.append("%s block %d: every product needs name and href" % (tag, j))
                    if pr.get("image") and not pr.get("alt", pr.get("name")):
                        p.append("%s block %d: product images need alt text" % (tag, j))
            if t == "coupon" and not bl.get("code"):
                p.append("%s block %d: a coupon needs a code" % (tag, j))
    return p


def path_in(bdir, p):
    return p if os.path.isabs(p) or p.startswith("https://") else os.path.join(bdir, p)


# ---------- rendering ----------

LINK = re.compile(r"\[([^\]]+)\]\((https?://[^)\s]+|mailto:[^)\s]+|tel:[^)\s]+)\)")
BOLD = re.compile(r"\*\*(.+?)\*\*")


def inline(text, T, sample, esp, color=None):
    """Plain text with **bold**, [links](https://...) and {first_name}."""
    s = e(text)
    s = BOLD.sub(r"<strong>\1</strong>", s)
    s = LINK.sub(lambda m: '<a href="%s" style="color:#%s;text-decoration:underline">%s</a>' % (m.group(2), color or T["accent_dark"], m.group(1)), s)
    return merge(s, sample, esp)


def merge(s, sample, esp, fallback="there"):
    tag = ESP[esp][0]
    if sample:
        return s.replace("{first_name}", SAMPLE["first_name"])
    rep = tag % fallback if "%s" in tag else tag
    return s.replace("{first_name}", rep)


def plain(text):
    t = BOLD.sub(r"\1", text)
    return LINK.sub(lambda m: "%s (%s)" % (m.group(1), m.group(2)), t)


def button(bl, T, sample, esp):
    bg = T["btn_bg"]
    fg = T["btn_fg"]
    label = merge(e(bl["label"]), sample, esp)
    # "bulletproof" button: a padded table cell, so Outlook draws it too
    return ('<table role="presentation" border="0" cellspacing="0" cellpadding="0" style="margin:8px 0 8px"><tr>'
            '<td align="center" bgcolor="#%s" style="border-radius:999px">'
            '<a href="%s" target="_blank" style="display:inline-block;padding:15px 30px;font-family:%s;font-size:16px;font-weight:bold;'
            'line-height:20px;color:#%s;text-decoration:none;border-radius:999px;mso-padding-alt:0">%s</a></td></tr></table>') % (
        bg, e(bl["href"]), FONT, fg, label)


def block_html(bl, T, imgs, sample, esp):
    t = bl["type"]
    pad = "padding:0 32px"
    if t == "heading":
        size = {1: 30, 2: 23, 3: 19}.get(bl.get("level", 1), 30)
        return '<tr><td class="px" style="padding:8px 32px 0"><h%d style="margin:0 0 14px;font-family:%s;font-size:%dpx;line-height:1.2;color:#%s;font-weight:bold">%s</h%d></td></tr>' % (
            bl.get("level", 1), T["head"], size, T["text"], inline(bl["text"], T, sample, esp), bl.get("level", 1))
    if t == "text":
        ps = bl["text"] if isinstance(bl["text"], list) else [bl["text"]]
        return "".join('<tr><td class="px" style="%s"><p style="margin:0 0 16px;font-family:%s;font-size:16px;line-height:1.6;color:#%s">%s</p></td></tr>' % (
            pad, FONT, T["body"], inline(x, T, sample, esp)) for x in ps)
    if t == "button":
        return '<tr><td class="px" style="%s;padding-bottom:12px">%s</td></tr>' % (pad, button(bl, T, sample, esp))
    if t == "image":
        src, w = imgs[bl["src"]]
        full = bl.get("full", False)
        width = 600 if full else 536
        img = '<img src="%s" width="%d" alt="%s" style="display:block;width:100%%;max-width:%dpx;height:auto;border:0;%s">' % (
            e(src), width, e(bl["alt"]), width, "" if full else "border-radius:12px")
        if bl.get("href"):
            img = '<a href="%s" target="_blank">%s</a>' % (e(bl["href"]), img)
        return '<tr><td class="%s" style="%s;padding-bottom:20px">%s</td></tr>' % ("" if full else "px", "padding:0" if full else pad, img)
    if t == "list":
        items = "".join('<tr><td valign="top" style="padding:0 12px 10px 0;font-family:%s;font-size:16px;line-height:1.5;color:#%s">&#9679;</td>'
                        '<td style="padding:0 0 10px;font-family:%s;font-size:16px;line-height:1.5;color:#%s">%s</td></tr>' % (
                            FONT, T["accent_fill"], FONT, T["body"], inline(x, T, sample, esp)) for x in bl["items"])
        return '<tr><td class="px" style="%s;padding-bottom:8px"><table role="presentation" border="0" cellspacing="0" cellpadding="0">%s</table></td></tr>' % (pad, items)
    if t == "products":
        cells = []
        for pr in bl["items"]:
            img = ""
            if pr.get("image"):
                src, _ = imgs[pr["image"]]
                img = '<a href="%s" target="_blank"><img src="%s" width="248" alt="%s" style="display:block;width:100%%;max-width:248px;height:auto;border:0;border-radius:10px"></a>' % (
                    e(pr["href"]), e(src), e(pr.get("alt", pr["name"])))
            cells.append(('<div class="col" style="display:inline-block;width:100%%;max-width:248px;vertical-align:top;margin:0 6px 20px">%s'
                          '<p style="margin:10px 0 2px;font-family:%s;font-size:16px;font-weight:bold;color:#%s">%s</p>'
                          '<p style="margin:0 0 8px;font-family:%s;font-size:15px;color:#%s">%s</p>'
                          '<a href="%s" target="_blank" style="font-family:%s;font-size:15px;font-weight:bold;color:#%s;text-decoration:underline">%s</a></div>') % (
                img, FONT, T["text"], e(pr["name"]), FONT, T["body"], e(pr.get("price", "")), e(pr["href"]), FONT, T["accent_dark"], e(pr.get("cta", "Shop now"))))
        return '<tr><td class="px" align="center" style="padding:0 20px;font-size:0;text-align:center">%s</td></tr>' % "".join(cells)
    if t == "quote":
        return ('<tr><td class="px" style="%s;padding-bottom:20px"><table role="presentation" width="100%%" border="0" cellspacing="0" cellpadding="0"><tr>'
                '<td style="border-left:4px solid #%s;padding:4px 0 4px 18px"><p style="margin:0 0 8px;font-family:%s;font-size:19px;line-height:1.45;color:#%s;font-weight:bold">&ldquo;%s&rdquo;</p>'
                '<p style="margin:0;font-family:%s;font-size:14px;color:#%s">%s</p></td></tr></table></td></tr>') % (
            pad, T["accent_fill"], T["head"], T["text"], e(bl["text"]), FONT, T["muted"], e(bl.get("author", "")))
    if t == "coupon":
        return ('<tr><td class="px" style="%s;padding-bottom:20px"><table role="presentation" width="100%%" border="0" cellspacing="0" cellpadding="0"><tr>'
                '<td align="center" style="border:2px dashed #%s;border-radius:12px;padding:18px;background:#%s">'
                '<p style="margin:0 0 4px;font-family:%s;font-size:14px;color:#%s">%s</p>'
                '<p style="margin:0;font-family:Courier New, monospace;font-size:26px;font-weight:bold;letter-spacing:2px;color:#%s">%s</p>%s</td></tr></table></td></tr>') % (
            pad, T["accent_fill"], T["tint"], FONT, T["body"], e(bl.get("label", "Use code")), T["text"], e(bl["code"]),
            '<p style="margin:6px 0 0;font-family:%s;font-size:13px;color:#%s">%s</p>' % (FONT, T["muted"], e(bl["note"])) if bl.get("note") else "")
    if t == "divider":
        return '<tr><td class="px" style="%s;padding-bottom:20px;padding-top:4px"><div style="height:1px;background:#%s;line-height:1px;font-size:1px">&nbsp;</div></td></tr>' % (pad, T["grid"])
    if t == "spacer":
        return '<tr><td style="height:%dpx;line-height:%dpx;font-size:1px">&nbsp;</td></tr>' % (bl.get("height", 16), bl.get("height", 16))
    return ""


def email_html(spec, m, T, imgs, logo, sample):
    esp = spec.get("esp", "generic")
    snd = spec["sender"]
    unsub = "#" if sample else ESP[esp][1]
    view = ESP[esp][2]
    pre = merge(e(m["preview"]), sample, esp)
    # pad the preheader so the email client does not pull body text after it
    filler = "&#847;&zwnj;&nbsp;" * 60
    head_logo = ('<img src="%s" height="36" alt="%s" style="display:block;height:36px;width:auto;border:0">' % (e(logo[0]), e(snd["name"]))) if logo else \
        '<span style="font-family:%s;font-size:20px;font-weight:bold;color:#%s">%s</span>' % (T["head"], T["text"], e((spec.get("brand") or {}).get("name", snd["name"])))
    rows = "".join(block_html(b, T, imgs, sample, esp) for b in m["blocks"])
    footer = ('<p style="margin:0 0 8px;font-family:%s;font-size:13px;line-height:1.5;color:#%s">%s<br>%s</p>'
              '<p style="margin:0 0 8px;font-family:%s;font-size:13px;line-height:1.5;color:#%s">%s</p>'
              '<p style="margin:0;font-family:%s;font-size:13px;line-height:1.5;color:#%s"><a href="%s" style="color:#%s;text-decoration:underline">Unsubscribe</a>%s</p>') % (
        FONT, T["muted"], e(snd.get("legal_name", snd["name"])), e(snd["address"]),
        FONT, T["muted"], e(spec.get("why", "You are receiving this because you signed up or bought from %s." % snd["name"])),
        FONT, T["muted"], unsub, T["muted"], (' &nbsp;&middot;&nbsp; <a href="%s" style="color:#%s;text-decoration:underline">View in browser</a>' % ("#" if sample else view, T["muted"])) if view else "")
    css = ("body{margin:0;padding:0;width:100%!important;background:#" + T["tint"] + "}"
           "img{-ms-interpolation-mode:bicubic}table{border-collapse:collapse}"
           "@media only screen and (max-width:620px){.wrap{width:100%!important}.px{padding-left:20px!important;padding-right:20px!important}"
           ".col{max-width:100%!important;margin:0 0 20px!important}h1{font-size:26px!important}}"
           ":root{color-scheme:light dark;supported-color-schemes:light dark}")
    return ('<!doctype html><html lang="%s" xmlns="http://www.w3.org/1999/xhtml"><head><meta charset="utf-8">'
            '<meta name="viewport" content="width=device-width,initial-scale=1"><meta name="x-apple-disable-message-reformatting">'
            '<meta name="color-scheme" content="light dark"><meta name="supported-color-schemes" content="light dark">'
            '<title>%s</title><style>%s</style></head>'
            '<body style="margin:0;padding:0;background:#%s">'
            '<div style="display:none;max-height:0;overflow:hidden;mso-hide:all;font-size:1px;line-height:1px;color:#%s">%s%s</div>'
            '<table role="presentation" width="100%%" border="0" cellspacing="0" cellpadding="0" style="background:#%s"><tr><td align="center" style="padding:24px 12px">'
            '<table role="presentation" class="wrap" width="600" border="0" cellspacing="0" cellpadding="0" style="width:600px;max-width:600px;background:#ffffff;border-radius:16px">'
            '<tr><td class="px" style="padding:28px 32px 20px">%s</td></tr>'
            '%s'
            '<tr><td class="px" style="padding:12px 32px 28px"><div style="height:1px;background:#%s;line-height:1px;font-size:1px;margin-bottom:18px">&nbsp;</div>%s</td></tr>'
            '</table></td></tr></table></body></html>') % (
        e(spec.get("language", "en")), e(m["subject"]), css, T["tint"], T["tint"], pre, filler, T["tint"], head_logo, rows, T["grid"], footer)


def email_text(spec, m):
    esp = spec.get("esp", "generic")
    out = []
    for b in m["blocks"]:
        t = b["type"]
        if t == "heading":
            out.append(plain(b["text"]))  # never upper-case: it would break merge tags
        elif t == "text":
            out += [plain(x) for x in (b["text"] if isinstance(b["text"], list) else [b["text"]])]
        elif t == "button":
            out.append("%s: %s" % (b["label"], b["href"]))
        elif t == "image" and b.get("href"):
            out.append("%s: %s" % (b["alt"], b["href"]))
        elif t == "list":
            out.append("\n".join("- " + plain(x) for x in b["items"]))
        elif t == "products":
            out.append("\n".join("- %s %s: %s" % (p["name"], p.get("price", ""), p["href"]) for p in b["items"]))
        elif t == "quote":
            out.append('"%s"\n%s' % (b["text"], b.get("author", "")))
        elif t == "coupon":
            out.append("%s: %s%s" % (b.get("label", "Use code"), b["code"], (" (%s)" % b["note"]) if b.get("note") else ""))
        elif t == "divider":
            out.append("----")
    snd = spec["sender"]
    out.append("--\n%s\n%s\nUnsubscribe: %s" % (snd.get("legal_name", snd["name"]), snd["address"], ESP[esp][1]))
    return merge("\n\n".join(out), False, esp) + "\n"


def tokens(spec, bdir):
    T, note = BK.resolve_spec(spec, bdir)
    head = {"editorial": "Georgia, 'Times New Roman', serif"}.get(spec.get("type"), FONT)
    W = "FFFFFF"
    btn_bg = T["accent_fill"] if BK.contrast(T["accent_fill"], W) >= 3 else T["dark"]
    return dict(T, head=head, btn_bg=btn_bg, btn_fg=max((W, T["text"]), key=lambda c: BK.contrast(c, btn_bg))), note


def prepare_images(spec, bdir, out):
    from PIL import Image
    Image.init()
    adir = os.path.join(out, "assets")
    if os.path.isdir(adir):
        shutil.rmtree(adir)
    os.makedirs(adir)
    base = spec.get("assets_base_url", "").rstrip("/")
    srcs = []
    for m in spec["emails"]:
        for b in m["blocks"]:
            if b["type"] == "image":
                srcs.append(b["src"])
            if b["type"] == "products":
                srcs += [p["image"] for p in b["items"] if p.get("image")]
    imgs = {}
    for k, src in enumerate(dict.fromkeys(srcs), 1):
        if src.startswith("https://"):
            imgs[src] = (src, None)
            continue
        with Image.open(path_in(bdir, src)) as im:
            im.load()
            if im.width > 1200:  # twice the email width is enough for sharp phones
                im = im.resize((1200, round(im.height * 1200 / im.width)))
            name = "img-%02d.jpg" % k
            im.convert("RGB").save(os.path.join(adir, name), quality=80, optimize=True, progressive=True)
        imgs[src] = ((base + "/" if base else "assets/") + name, im.width)
    logo = None
    if spec.get("_logo"):
        with Image.open(spec["_logo"]) as im:
            im = im.convert("RGBA")
            im.thumbnail((480, 144))
            bg = Image.new("RGB", im.size, (255, 255, 255))  # email clients do not all show transparency well on dark mode
            bg.paste(im, mask=im)
            bg.save(os.path.join(adir, "logo.png"))
        logo = ((base + "/" if base else "assets/") + "logo.png", None)
    return imgs, logo


def sequence_md(spec):
    lines = ["# %s" % spec.get("name", "Email campaign"), ""]
    if spec.get("goal"):
        lines += ["Goal: %s" % spec["goal"]]
    if spec.get("audience"):
        lines += ["Audience: %s" % spec["audience"]]
    lines += ["Email tool: %s (merge tags written in its format)" % spec.get("esp", "generic"), "",
              "| # | Send | Email | Subject | Preview text |", "| --- | --- | --- | --- | --- |"]
    for i, m in enumerate(spec["emails"], 1):
        d = m.get("send_day")
        when = m.get("send_note") or ("Day %d" % (d + 1) if isinstance(d, (int, float)) else "")
        lines.append("| %d | %s | %s.html | %s | %s |" % (i, when, m["id"], m["subject"].replace("|", "/"), m["preview"].replace("|", "/")))
    lines.append("")
    for m in spec["emails"]:
        if m.get("subject_alternatives"):
            lines += ["**%s**: subject lines to A/B test: %s" % (m["id"], "; ".join(m["subject_alternatives"])), ""]
    if spec.get("assets_base_url"):
        lines += ["Images: upload the assets folder to %s before sending." % spec["assets_base_url"]]
    else:
        lines += ["Images: upload the files in assets/ to your email tool (or your site) and replace the assets/ paths, or set assets_base_url and rebuild."]
    return "\n".join(lines) + "\n"


def main():
    a = sys.argv[1:]
    if not a:
        sys.exit(__doc__)
    src = a[0]
    out = a[a.index("--out") + 1] if "--out" in a else "emails"
    spec = load(src)
    bdir = os.path.dirname(os.path.abspath(src))
    probs = validate(spec, bdir)
    if probs:
        sys.exit("spec problems:\n- " + "\n- ".join(probs))
    os.makedirs(out, exist_ok=True)
    T, note = tokens(spec, bdir)
    spec_r = dict(spec, _logo=T.get("logo"))
    imgs, logo = prepare_images(spec_r, bdir, out)
    pdir = os.path.join(out, "preview")
    os.makedirs(pdir, exist_ok=True)
    for m in spec["emails"]:
        with open(os.path.join(out, m["id"] + ".html"), "w", encoding="utf-8") as f:
            f.write(email_html(spec, m, T, imgs, logo, sample=False))
        with open(os.path.join(out, m["id"] + ".txt"), "w", encoding="utf-8") as f:
            f.write(email_text(spec, m))
        # a preview copy with sample merge values and local images
        local = {k: (v[0] if k.startswith("https://") else "../assets/" + v[0].rsplit("/", 1)[-1], v[1]) for k, v in imgs.items()}
        plogo = ("../assets/logo.png", None) if logo else None
        with open(os.path.join(pdir, m["id"] + ".html"), "w", encoding="utf-8") as f:
            f.write(email_html(spec, m, T, local, plogo, sample=True))
    with open(os.path.join(out, "sequence.md"), "w", encoding="utf-8") as f:
        f.write(sequence_md(spec))
    with open(os.path.join(out, "campaign.spec.json"), "w", encoding="utf-8") as f:
        json.dump(spec, f, indent=2, ensure_ascii=False)
    made = ["%d emails (.html and .txt)" % len(spec["emails"]), "sequence.md", "assets/"]
    if "--no-shots" not in a:
        n = 0
        for m in spec["emails"]:
            ph = os.path.join(pdir, m["id"] + ".html")
            shots = {640: os.path.abspath(os.path.join(pdir, m["id"] + "-desktop.png")), 375: os.path.abspath(os.path.join(pdir, m["id"] + "-mobile.png"))}
            if render.inspect(ph, "() => true", widths=(640, 375), height=900, shots=shots) is not None:
                n += 1
        if n:
            made.append("previews")
    if note:
        print(note + " (tell the user; they can set brand.primary and brand.accent to change them)")
    print("built %s: %s" % (out, ", ".join(made)))


if __name__ == "__main__":
    main()
