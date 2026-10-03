"""Build marketplace listings (Amazon, Flipkart, Shopify, Meesho) from listing.json.

Usage: python3 build_listing.py listing.json --out listing [--no-shots]
Writes into the out folder: listing.md (every field ready to paste, with
character counts and limits), amazon.csv, flipkart.csv, shopify_products.csv
(Shopify's product import format), image-plan.md (the shot list, with the
marketplace image rules), preview.html and preview.png (how the copy reads on
a phone-width product page; a neutral layout, not any marketplace's design).
"""
import csv
import html
import json
import os
import re
import sys

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
import render  # noqa: E402

MARKETS = ("amazon_in", "amazon_com", "flipkart", "shopify", "meesho")
LIMITS = {  # characters (bytes for search terms)
    "amazon": {"item_name": 75, "highlights": 125, "title": 200, "title_apparel": 125, "bullet": 500, "bullets": 5,
               "description": 2000, "search_terms_bytes": 249},
    "shopify": {"seo_title": 70, "seo_description": 320},
}
AMAZON_BANNED = "!$?_{}^¬¦"
e = lambda s: html.escape(str(s if s is not None else ""), quote=True)


def load(path):
    with open(path, encoding="utf-8") as f:
        return json.load(f)


def amazon_title(a):
    hl = a.get("highlights", "").strip()
    return a["item_name"].strip() + (", " + hl if hl else "")


def validate(spec, bdir="."):
    p = []
    if not spec.get("brand"):
        p.append("brand is required")
    if not (spec.get("product") or {}).get("name"):
        p.append("product.name is required")
    ms = spec.get("marketplaces", [])
    if not ms:
        p.append("marketplaces: name at least one of %s" % ", ".join(MARKETS))
    for m in ms:
        if m not in MARKETS:
            p.append("marketplace %r: use one of %s" % (m, ", ".join(MARKETS)))
    if any(m.startswith("amazon") for m in ms):
        a = spec.get("amazon") or {}
        for k in ("item_name", "bullets", "description"):
            if not a.get(k):
                p.append("amazon.%s is required" % k)
        if a.get("bullets") and not isinstance(a["bullets"], list):
            p.append("amazon.bullets must be a list")
    if "flipkart" in ms:
        f = spec.get("flipkart") or {}
        for k in ("title", "key_features", "description"):
            if not f.get(k):
                p.append("flipkart.%s is required" % k)
    if "meesho" in ms:
        f = spec.get("meesho") or {}
        for k in ("title", "description"):
            if not f.get(k):
                p.append("meesho.%s is required" % k)
    if "shopify" in ms:
        s = spec.get("shopify") or {}
        for k in ("title", "description", "price"):
            if s.get(k) in (None, "", []):
                p.append("shopify.%s is required" % k)
        if s.get("handle") and not re.match(r"^[a-z0-9]+(?:-[a-z0-9]+)*$", s["handle"]):
            p.append("shopify.handle must be lowercase words joined by dashes")
        for k in ("price", "compare_at_price"):
            if s.get(k) is not None and not isinstance(s[k], (int, float)):
                p.append("shopify.%s must be a number" % k)
    for i, im in enumerate(spec.get("images", []), 1):
        if not im.get("slot") or not im.get("brief"):
            p.append("image %d: needs slot and brief" % i)
        if im.get("file") and not os.path.exists(os.path.join(bdir, im["file"])):
            p.append("image %d: file not found: %s" % (i, im["file"]))
    return p


def handle(spec):
    s = spec.get("shopify") or {}
    return s.get("handle") or re.sub(r"[^a-z0-9]+", "-", s.get("title", spec["product"]["name"]).lower()).strip("-")


def paragraphs(x):
    return x if isinstance(x, list) else [p for p in str(x).split("\n\n") if p.strip()]


def count_line(label, text, limit=None, unit="characters"):
    n = len(text.encode("utf-8")) if unit == "bytes" else len(text)
    return "%s (%d%s %s)" % (label, n, " of %d" % limit if limit else "", unit)


def listing_md(spec):
    L = LIMITS["amazon"]
    out = ["# %s: %s" % (spec["brand"], spec["product"]["name"]), ""]
    ms = spec["marketplaces"]
    if any(m.startswith("amazon") for m in ms):
        a = spec["amazon"]
        apparel = spec["product"].get("apparel", False)
        title = amazon_title(a)
        out += ["## Amazon (%s)" % ", ".join(m.split("_")[1].upper() for m in ms if m.startswith("amazon")), "",
                "**%s**" % count_line("Item name", a["item_name"], L["item_name"]), "", "```", a["item_name"], "```", ""]
        if a.get("highlights"):
            out += ["**%s**" % count_line("Item highlights (title differentiation)", a["highlights"], L["highlights"]), "", "```", a["highlights"], "```", ""]
        out += ["**%s**" % count_line("Full title as shown", title, L["title_apparel"] if apparel else L["title"]), "", "```", title, "```", ""]
        for i, b in enumerate(a["bullets"], 1):
            out += ["**%s**" % count_line("Bullet %d" % i, b, L["bullet"]), "", "```", b, "```", ""]
        desc = "\n\n".join(paragraphs(a["description"]))
        out += ["**%s**" % count_line("Description", desc, L["description"]), "", "```", desc, "```", ""]
        if a.get("search_terms"):
            out += ["**%s**" % count_line("Search terms (backend)", a["search_terms"], L["search_terms_bytes"], "bytes"), "", "```", a["search_terms"], "```", ""]
        if a.get("aplus"):
            out += ["### A+ content", ""]
            for k, mod in enumerate(a["aplus"], 1):
                out += ["%d. **%s** (%s)" % (k, mod.get("headline", ""), mod.get("module", "image and text")), "",
                        "   " + mod.get("text", ""), "", "   Image: " + mod.get("image_brief", ""), ""]
    if "flipkart" in ms:
        f = spec["flipkart"]
        out += ["## Flipkart", "", "**%s**" % count_line("Title", f["title"]), "", "```", f["title"], "```", "", "**Key features**", "", "```"]
        out += [x for x in f["key_features"]] + ["```", ""]
        out += ["**%s**" % count_line("Description", "\n\n".join(paragraphs(f["description"]))), "", "```", "\n\n".join(paragraphs(f["description"])), "```", ""]
    if "meesho" in ms:
        f = spec["meesho"]
        out += ["## Meesho", "", "**%s**" % count_line("Title", f["title"]), "", "```", f["title"], "```", "",
                "**%s**" % count_line("Description", "\n\n".join(paragraphs(f["description"]))), "", "```", "\n\n".join(paragraphs(f["description"])), "```", ""]
    if "shopify" in ms:
        s = spec["shopify"]
        S = LIMITS["shopify"]
        out += ["## Shopify (your own store)", "", "**Title**: %s" % s["title"], "", "**Handle (web address)**: /products/%s" % handle(spec), ""]
        if s.get("seo_title"):
            out += ["**%s**" % count_line("Search title", s["seo_title"], S["seo_title"]), "", "```", s["seo_title"], "```", ""]
        if s.get("seo_description"):
            out += ["**%s**" % count_line("Search description", s["seo_description"], S["seo_description"]), "", "```", s["seo_description"], "```", ""]
        out += ["**Description** is in shopify_products.csv as HTML, ready to import.", "",
                "**Price**: %s%s" % (s["price"], (", compare at %s" % s["compare_at_price"]) if s.get("compare_at_price") else ""), "",
                "**Tags**: %s" % ", ".join(s.get("tags", [])), ""]
    kw = spec.get("keywords") or {}
    if kw:
        out += ["## Keywords used", "", "Primary: %s" % ", ".join(kw.get("primary", [])), "", "Secondary: %s" % ", ".join(kw.get("secondary", [])), ""]
        if kw.get("source"):
            out += ["Source: %s" % kw["source"], ""]
    return "\n".join(out)


IMAGE_RULES = {
    "amazon": ["Main image: pure white background (RGB 255, 255, 255), the product filling about 85% of the frame, no text, logos, borders or props that are not included.",
               "At least 1,000 px on the longest side so shoppers can zoom; 2,000 px is better.",
               "Up to 8 more images: use them for features, size and scale, what is in the box, ingredients or materials, lifestyle, and a comparison or care chart."],
    "flipkart": ["Main image on a white background, product centred, no watermarks or promotional text.",
                 "High resolution so zoom works; show every side and the key details."],
    "shopify": ["Use one consistent background and aspect ratio across the store (square works everywhere).",
                "Compress images; aim for under 300 KB each so the page loads fast on phones."],
}


def image_plan(spec):
    ims = spec.get("images", [])
    out = ["# Image plan: %s" % spec["product"]["name"], "", "| # | Slot | What to shoot or design | Alt text |", "| --- | --- | --- | --- |"]
    for i, im in enumerate(ims, 1):
        out.append("| %d | %s | %s | %s |" % (i, im["slot"], im["brief"].replace("|", "/"), im.get("alt", "").replace("|", "/")))
    out.append("")
    for m, rules in IMAGE_RULES.items():
        if any(x.startswith(m) for x in spec["marketplaces"]):
            out += ["## %s rules" % m.title(), ""] + ["- " + r for r in rules] + [""]
    return "\n".join(out)


def write_csvs(spec, out):
    ms = spec["marketplaces"]
    made = []
    if any(m.startswith("amazon") for m in ms):
        a = spec["amazon"]
        row = {"brand_name": spec["brand"], "item_name": a["item_name"], "title_differentiation": a.get("highlights", ""),
               "full_title": amazon_title(a), "product_description": "\n\n".join(paragraphs(a["description"])),
               "generic_keyword": a.get("search_terms", "")}
        for i in range(5):
            row["bullet_point%d" % (i + 1)] = a["bullets"][i] if i < len(a["bullets"]) else ""
        with open(os.path.join(out, "amazon.csv"), "w", newline="", encoding="utf-8-sig") as f:
            w = csv.DictWriter(f, fieldnames=list(row))
            w.writeheader()
            w.writerow(row)
        made.append("amazon.csv")
    if "flipkart" in ms:
        fk = spec["flipkart"]
        row = {"Brand": spec["brand"], "Title": fk["title"], "Key Features": "; ".join(fk["key_features"]),
               "Description": "\n\n".join(paragraphs(fk["description"]))}
        with open(os.path.join(out, "flipkart.csv"), "w", newline="", encoding="utf-8-sig") as f:
            w = csv.DictWriter(f, fieldnames=list(row))
            w.writeheader()
            w.writerow(row)
        made.append("flipkart.csv")
    if "shopify" in ms:
        s = spec["shopify"]
        body = "".join("<p>%s</p>" % e(x) for x in paragraphs(s["description"]))
        if s.get("features"):
            body += "<ul>%s</ul>" % "".join("<li>%s</li>" % e(x) for x in s["features"])
        img = next((im for im in spec.get("images", []) if im.get("url")), None)
        row = {"Handle": handle(spec), "Title": s["title"], "Body (HTML)": body, "Vendor": s.get("vendor", spec["brand"]),
               "Type": s.get("product_type", ""), "Tags": ", ".join(s.get("tags", [])), "Published": "TRUE",
               "Option1 Name": "Title", "Option1 Value": "Default Title", "Variant SKU": s.get("sku", ""),
               "Variant Price": s["price"], "Variant Compare At Price": s.get("compare_at_price", ""),
               "Variant Requires Shipping": "TRUE", "Variant Taxable": "TRUE",
               "Image Src": img["url"] if img else "", "Image Alt Text": img.get("alt", "") if img else "",
               "SEO Title": s.get("seo_title", ""), "SEO Description": s.get("seo_description", ""), "Status": s.get("status", "draft")}
        with open(os.path.join(out, "shopify_products.csv"), "w", newline="", encoding="utf-8") as f:
            w = csv.DictWriter(f, fieldnames=list(row))
            w.writeheader()
            w.writerow(row)
        made.append("shopify_products.csv")
    return made


PREVIEW_CSS = """
*{box-sizing:border-box}body{margin:0;background:#f3f3f1;font:15px/1.5 "Segoe UI",Roboto,Arial,sans-serif;color:#1d1d1b}
.page{max-width:420px;margin:0 auto;background:#fff;min-height:100vh}
.bar{background:#1d1d1b;color:#fff;font-weight:700;padding:12px 16px;font-size:13px}
.img{aspect-ratio:1;background:repeating-linear-gradient(45deg,#ececea,#ececea 12px,#f6f6f4 12px,#f6f6f4 24px);display:flex;align-items:center;justify-content:center;color:#77766f;font-weight:600;text-align:center;padding:24px}
.img img{width:100%;height:100%;object-fit:contain;background:#fff}
.in{padding:16px}
.brand{color:#2a6fb0;font-size:13px;font-weight:600}
h1{font-size:17px;line-height:1.35;margin:4px 0 6px;font-weight:600}
.cut{color:#a33;font-size:12px;margin-bottom:10px}
.price{font-size:24px;font-weight:700;margin:6px 0 14px}
h2{font-size:15px;margin:18px 0 8px}
ul{padding-left:18px;margin:0}li{margin:0 0 8px}
.desc p{margin:0 0 10px}
.tag{display:inline-block;font-size:11px;font-weight:700;background:#fff4d6;color:#7a5600;padding:2px 8px;border-radius:99px;margin-bottom:8px}
"""


def preview_html(spec, bdir):
    a = spec.get("amazon")
    s = spec.get("shopify") or {}
    title = amazon_title(a) if a else (s.get("title") or (spec.get("flipkart") or {}).get("title", ""))
    bullets = a["bullets"] if a else (spec.get("flipkart") or {}).get("key_features", s.get("features", []))
    desc = paragraphs(a["description"]) if a else paragraphs(s.get("description", ""))
    main = next((im for im in spec.get("images", []) if im.get("file")), None)
    img = '<img src="file://%s" alt="%s">' % (e(os.path.abspath(os.path.join(bdir, main["file"]))), e(main.get("alt", ""))) if main else \
        e((spec.get("images") or [{"brief": "Main image"}])[0]["brief"])
    cut = title[:80] + "..." if len(title) > 80 else ""
    price = s.get("price") or spec.get("product", {}).get("price", "")
    return ('<!doctype html><html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1">'
            '<title>Listing preview</title><style>%s</style></head><body><div class="page"><div class="bar">Listing preview (phone width)</div>'
            '<div class="img">%s</div><div class="in"><span class="tag">Preview, not a marketplace page</span><div class="brand">%s</div><h1>%s</h1>%s'
            '<div class="price">%s</div><h2>About this item</h2><ul>%s</ul><h2>Description</h2><div class="desc">%s</div></div></div></body></html>') % (
        PREVIEW_CSS, img, e(spec["brand"]), e(title),
        '<div class="cut">Search results on phones often show only the first 80 characters: "%s"</div>' % e(cut) if cut else "",
        e(("₹%s" % price) if price and spec.get("currency", "INR") == "INR" else price), "".join("<li>%s</li>" % e(b) for b in bullets),
        "".join("<p>%s</p>" % e(p) for p in desc))


def main():
    a = sys.argv[1:]
    if not a:
        sys.exit(__doc__)
    src = a[0]
    out = a[a.index("--out") + 1] if "--out" in a else "listing"
    spec = load(src)
    bdir = os.path.dirname(os.path.abspath(src))
    probs = validate(spec, bdir)
    if probs:
        sys.exit("spec problems:\n- " + "\n- ".join(probs))
    os.makedirs(out, exist_ok=True)
    with open(os.path.join(out, "listing.md"), "w", encoding="utf-8") as f:
        f.write(listing_md(spec))
    with open(os.path.join(out, "image-plan.md"), "w", encoding="utf-8") as f:
        f.write(image_plan(spec))
    made = ["listing.md", "image-plan.md"] + write_csvs(spec, out)
    with open(os.path.join(out, "preview.html"), "w", encoding="utf-8") as f:
        f.write(preview_html(spec, bdir))
    with open(os.path.join(out, "listing.spec.json"), "w", encoding="utf-8") as f:
        json.dump(spec, f, indent=2, ensure_ascii=False)
    if "--no-shots" not in a:
        if render.inspect(os.path.join(out, "preview.html"), "() => true", widths=(420,), height=900,
                          shots={420: os.path.abspath(os.path.join(out, "preview.png"))}) is not None:
            made.append("preview.png")
    print("built %s: %s" % (out, ", ".join(made)))


if __name__ == "__main__":
    main()
