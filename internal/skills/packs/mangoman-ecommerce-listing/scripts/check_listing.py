"""Check marketplace listings before uploading.

Usage: python3 check_listing.py listing.json listing
Checks Amazon's title rules (item name up to 75 characters, highlights up to
125, title up to 200 or 125 for apparel, no ! $ ? _ { } ^ ¬ ¦, no word more
than twice), bullets (up to 5, 500 characters each), description (2,000),
backend search terms (249 bytes, no brands, ASINs, repeats or punctuation);
Shopify's search title (70) and description (320); promotional and risky
health claims; every number in the copy traces to the user's facts; primary
keywords are used; Indian listing disclosures (net quantity, MRP, maker,
country of origin, customer care; FSSAI for food); the image plan and any
image files (main image on white, at least 1,000 px). Prints PASS, WARN or
FAIL; exits 1 on any FAIL.
"""
import os
import re
import sys

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
import build_listing as B  # noqa: E402
import checks as C  # noqa: E402

STOP = set("a an and as at by for from in into of on or the to with without per vs x".split())
PROMO = re.compile(r"\b(best[- ]?seller|best selling|top rated|free shipping|free delivery|sale|discount|offer|deal|cheapest|lowest price|hot item|limited (?:time|offer|stock)|buy now|#1|no\.? ?1|number one|100% (?:guaranteed|satisfaction)|guaranteed?)\b", re.I)
HEALTH = re.compile(r"\b(cures?|treats?|prevents? (?:cancer|diabetes|covid|disease)|boosts? immunity|immunity booster|detox(?:ify|ifies)?|anti[- ]?cancer|weight loss|burns? fat|clinically proven|doctor recommended|fda approved|no side effects|chemical[- ]free|100% (?:natural|pure|safe|organic))\b", re.I)
PLACEHOLDER = re.compile(r"lorem ipsum|\bTBD\b|\bTODO\b|\[(?:brand|product|insert|size)[^\]]*\]|xxx+", re.I)
ASIN = re.compile(r"\bB0[A-Z0-9]{8}\b")
INDIA_FIELDS = {"net_quantity": "net quantity", "mrp": "MRP", "manufacturer": "maker or packer name and address",
                "country_of_origin": "country of origin", "customer_care": "customer care contact"}


def words(t):
    return re.findall(r"[a-z0-9]+(?:'[a-z]+)?", t.lower())


def stem(w):
    for suf in ("es", "s"):
        if len(w) > 4 and w.endswith(suf):
            return w[: -len(suf)]
    return w




def copy_texts(spec):
    """(marketplace field, text) for all customer-facing copy."""
    out = []
    a = spec.get("amazon") or {}
    if any(m.startswith("amazon") for m in spec["marketplaces"]):
        out += [("amazon title", B.amazon_title(a))] + [("amazon bullet %d" % (i + 1), b) for i, b in enumerate(a.get("bullets", []))]
        out += [("amazon description", x) for x in B.paragraphs(a.get("description", ""))]
        out += [("amazon A+", m.get("headline", "") + " " + m.get("text", "")) for m in a.get("aplus", [])]
    f = spec.get("flipkart") or {}
    if "flipkart" in spec["marketplaces"]:
        out += [("flipkart title", f.get("title", ""))] + [("flipkart feature", x) for x in f.get("key_features", [])]
        out += [("flipkart description", x) for x in B.paragraphs(f.get("description", ""))]
    m = spec.get("meesho") or {}
    if "meesho" in spec["marketplaces"]:
        out += [("meesho title", m.get("title", ""))] + [("meesho description", x) for x in B.paragraphs(m.get("description", ""))]
    s = spec.get("shopify") or {}
    if "shopify" in spec["marketplaces"]:
        out += [("shopify title", s.get("title", "")), ("shopify search title", s.get("seo_title", "")), ("shopify search description", s.get("seo_description", ""))]
        out += [("shopify description", x) for x in B.paragraphs(s.get("description", "")) + s.get("features", [])]
    return [(k, t) for k, t in out if t]


def main():
    if len(sys.argv) < 3:
        sys.exit(__doc__)
    src, out = sys.argv[1], sys.argv[2]
    spec = B.load(src)
    bdir = os.path.dirname(os.path.abspath(src))
    rs = []

    def r(level, msg):
        rs.append((level, msg))

    probs = B.validate(spec, bdir)
    r("PASS" if not probs else "FAIL", "spec is valid" if not probs else "; ".join(probs))
    if probs:
        return finish(rs)
    ms = spec["marketplaces"]
    brand = spec["brand"]
    comps = [c.lower() for c in spec.get("competitors", [])]
    L = B.LIMITS["amazon"]

    if any(m.startswith("amazon") for m in ms):
        a = spec["amazon"]
        title = B.amazon_title(a)
        apparel = spec["product"].get("apparel", False)
        tl = L["title_apparel"] if apparel else L["title"]
        fails = []
        if len(a["item_name"]) > L["item_name"]:
            fails.append("item name is %d characters (limit 75)" % len(a["item_name"]))
        if len(a.get("highlights", "")) > L["highlights"]:
            fails.append("highlights are %d characters (limit 125)" % len(a["highlights"]))
        if len(title) > tl:
            fails.append("title is %d characters (limit %d)" % (len(title), tl))
        bad = sorted({c for c in title if c in B.AMAZON_BANNED and c not in brand})
        if bad:
            fails.append("title uses characters Amazon does not allow: %s" % " ".join(bad))
        counts = {}
        for w in words(title):
            if w in STOP or w.isdigit():
                continue
            counts[stem(w)] = counts.get(stem(w), 0) + 1
        rep = sorted(w for w, n in counts.items() if n > 2)
        if rep:
            fails.append("words used more than twice in the title: %s" % ", ".join(rep))
        pm = PROMO.search(title)
        if pm:
            fails.append('promotional wording in the title: "%s"' % pm.group(0))
        if any(c in title.lower() for c in comps):
            fails.append("a competitor's brand is in the title")
        r("PASS" if not fails else "FAIL", "Amazon title follows the title rules (%d characters)" % len(title) if not fails else "Amazon title: " + "; ".join(fails))
        if not title.lower().startswith(brand.lower()):
            r("WARN", "Amazon titles should start with the brand name (%s)" % brand)
        caps = [w for w in re.findall(r"\b[A-Z]{5,}\b", title) if w != brand.upper()]
        if caps:
            r("WARN", "all-capital words in the Amazon title: %s (use title case)" % ", ".join(caps))
        bl = a["bullets"]
        bf = ["bullet %d is %d characters (limit 500)" % (i, len(b)) for i, b in enumerate(bl, 1) if len(b) > L["bullet"]]
        if len(bl) > L["bullets"]:
            bf.append("%d bullets (Amazon shows 5)" % len(bl))
        r("PASS" if not bf else "FAIL", "Amazon bullets within limits" if not bf else "Amazon bullets: " + "; ".join(bf))
        bw = []
        if len(bl) < 5:
            bw.append("only %d bullets; use all 5" % len(bl))
        long_ = [str(i) for i, b in enumerate(bl, 1) if len(b) > 300]
        if long_:
            bw.append("bullets %s are long; 150 to 250 characters read best on phones" % ", ".join(long_))
        if any(PROMO.search(b) for b in bl):
            bw.append("promotional wording in bullets (prices, offers and shipping claims do not belong there)")
        if bw:
            r("WARN", "Amazon bullets: " + "; ".join(bw))
        desc = "\n\n".join(B.paragraphs(a["description"]))
        r("PASS" if len(desc) <= L["description"] else "FAIL", "Amazon description %d characters (limit 2,000)" % len(desc))
        st = a.get("search_terms", "")
        if st:
            nb = len(st.encode("utf-8"))
            sf, sw = [], []
            if nb > L["search_terms_bytes"]:
                sf.append("%d bytes (Amazon indexes 249)" % nb)
            if ASIN.search(st):
                sf.append("contains an ASIN")
            ws = words(st)
            if brand.lower() in st.lower() or any(c in st.lower() for c in comps):
                sw.append("brand names (yours or others) do not belong in search terms")
            dup = sorted({w for w in ws if ws.count(w) > 1})
            if dup:
                sw.append("repeated words: %s" % ", ".join(dup))
            if re.search(r"[,;|]", st):
                sw.append("use spaces, not commas or other punctuation")
            in_title = sorted({w for w in ws if w in set(words(title)) and w not in STOP})
            if len(in_title) > 3:
                sw.append("words already in the title (%s); use the space for other terms" % ", ".join(in_title[:6]))
            if PROMO.search(st):
                sw.append("promotional words")
            r("PASS" if not sf else "FAIL", "search terms %d of 249 bytes" % nb if not sf else "search terms: " + "; ".join(sf))
            if sw:
                r("WARN", "search terms: " + "; ".join(sw))
        else:
            r("WARN", "no backend search terms: add synonyms, other languages (for example Hindi words in Latin letters) and spellings shoppers use")

    if "flipkart" in ms:
        f = spec["flipkart"]
        fw = []
        if len(f["title"]) > 120:
            fw.append("title is %d characters; keep brand, product and the key attribute, short" % len(f["title"]))
        if not (4 <= len(f["key_features"]) <= 8):
            fw.append("%d key features; 4 to 8 work best" % len(f["key_features"]))
        if PROMO.search(f["title"]):
            fw.append("promotional wording in the title")
        r("PASS" if not fw else "WARN", "Flipkart copy follows good practice" if not fw else "Flipkart: " + "; ".join(fw))
    if "shopify" in ms:
        s = spec["shopify"]
        S = B.LIMITS["shopify"]
        sf, sw = [], []
        if len(s.get("seo_title", "")) > S["seo_title"]:
            sf.append("search title is %d characters; Shopify allows 70" % len(s["seo_title"]))
        if len(s.get("seo_description", "")) > S["seo_description"]:
            sf.append("search description is %d characters; Shopify allows 320" % len(s["seo_description"]))
        if not s.get("seo_title") or not s.get("seo_description"):
            sw.append("set seo_title and seo_description, or Google picks text from the page")
        elif not (110 <= len(s["seo_description"]) <= 165):
            sw.append("search description is %d characters; Google shows about 155" % len(s["seo_description"]))
        if s.get("compare_at_price") is not None and s["compare_at_price"] <= s["price"]:
            sf.append("compare_at_price must be higher than price (it shows as the struck-out price)")
        r("PASS" if not sf else "FAIL", "Shopify fields within limits" if not sf else "Shopify: " + "; ".join(sf))
        if sw:
            r("WARN", "Shopify: " + "; ".join(sw))

    texts = copy_texts(spec)
    pool = C.fact_pool(spec.get("facts", {}), spec.get("product", {}), {k: v for k, v in (spec.get("shopify") or {}).items() if k in ("price", "compare_at_price")})
    untr = sorted({"%s: %s" % (k, u) for k, t in texts for u in C.untraced(t, pool)})
    r("PASS" if not untr else "FAIL", "every number in the copy comes from the product facts" if not untr else
      "numbers not in facts (ask the user, or remove them): " + "; ".join(untr[:8]))
    health = sorted({'%s: "%s"' % (k, mm.group(0)) for k, t in texts for mm in [HEALTH.search(t)] if mm})
    r("PASS" if not health else "FAIL", "no health or purity claims that need approval" if not health else
      "claims marketplaces and advertising rules reject without proof (remove, or back with a certificate in facts and rephrase): " + "; ".join(health[:6]))
    promo = sorted({k for k, t in texts if PROMO.search(t) and not k.endswith("title") and not k.startswith("amazon bullet")})
    if promo:
        r("WARN", "promotional wording in: %s (marketplaces show offers separately)" % ", ".join(promo))
    ph = sorted({mm.group(0) for _, t in texts for mm in [PLACEHOLDER.search(t)] if mm})
    r("PASS" if not ph else "FAIL", "no placeholder text" if not ph else "placeholder text left in: " + ", ".join(ph))
    comp = sorted({c for _, t in texts for c in comps if c in t.lower()})
    r("PASS" if not comp else "FAIL", "no competitor brand names" if not comp else "competitor brands in the copy: " + ", ".join(comp))

    kw = (spec.get("keywords") or {}).get("primary", [])
    if kw:
        main_text = " ".join(t for k, t in texts if "title" in k or "bullet" in k or "feature" in k).lower()
        missing = [k for k in kw if not all(stem(w) in {stem(x) for x in words(main_text)} for w in words(k))]
        r("PASS" if not missing else "WARN", "every primary keyword is in a title or bullet" if not missing else
          "primary keywords not in any title or bullet: " + ", ".join(missing))
    else:
        r("WARN", "no keywords given: list what shoppers type (from the marketplace search box suggestions or the seller's search reports)")

    if any(m in ("amazon_in", "flipkart", "meesho") for m in ms):
        facts = {**spec.get("product", {}), **spec.get("facts", {})}
        miss = [v for k, v in INDIA_FIELDS.items() if not facts.get(k)]
        if spec["product"].get("food") and not facts.get("fssai_license"):
            miss.append("FSSAI licence number")
        if spec["product"].get("food") and not (facts.get("shelf_life") or facts.get("best_before")):
            miss.append("shelf life or best before")
        r("PASS" if not miss else "WARN", "Indian listing disclosures present" if not miss else
          "Indian e-commerce listings must show: %s (add to product facts)" % ", ".join(miss))

    ims = spec.get("images", [])
    iw = []
    if len(ims) < 6:
        iw.append("%d images planned; 6 to 9 sell better (features, scale, in the box, lifestyle)" % len(ims))
    main = next((im for im in ims if im["slot"].lower() in ("main", "hero", "primary")), ims[0] if ims else None)
    if main and any(m.startswith("amazon") or m == "flipkart" for m in ms) and "white" not in main["brief"].lower():
        iw.append("the main image brief should say pure white background")
    if any(not im.get("alt") for im in ims):
        iw.append("give every image alt text")
    r("PASS" if not iw else "WARN", "image plan covers what shoppers need" if not iw else "images: " + "; ".join(iw))
    try:
        from PIL import Image
        files = [im for im in ims if im.get("file")]
        bad = []
        for im in files:
            with Image.open(os.path.join(bdir, im["file"])) as pic:
                pic = pic.convert("RGB")
                if max(pic.size) < 1000:
                    bad.append("%s is %dx%d; at least 1,000 px on the longest side lets shoppers zoom" % (im["file"], pic.width, pic.height))
                if im is main and any(m.startswith("amazon") for m in ms):
                    w, h = pic.size
                    corners = [pic.getpixel((x, y)) for x in (2, w - 3) for y in (2, h - 3)]
                    if any(min(c) < 245 for c in corners):
                        bad.append("%s: the main image background is not white at the corners (Amazon needs pure white)" % im["file"])
        if files:
            r("PASS" if not bad else "FAIL", "image files meet size and background rules" if not bad else "; ".join(bad))
    except ImportError:
        pass
    for f in ("listing.md",) + (("amazon.csv",) if any(m.startswith("amazon") for m in ms) else ()) + (("shopify_products.csv",) if "shopify" in ms else ()):
        if not os.path.exists(os.path.join(out, f)):
            r("FAIL", "%s not built" % f)
    return finish(rs)


def finish(rs):
    rep = C.Report()
    rep.rows = rs
    rep.finish()


if __name__ == "__main__":
    main()
