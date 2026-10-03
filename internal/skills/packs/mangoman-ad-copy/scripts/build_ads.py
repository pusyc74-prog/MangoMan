"""Build ad copy for Google search ads and Meta (Facebook and Instagram) ads from ads.json.

Usage: python3 build_ads.py ads.json --out ads
Writes ads.md (every ad with character counts), google_ads.csv and
google_keywords.csv (for Google Ads Editor's import), and meta_ads.csv.
"""
import csv
import json
import os
import sys

GOOGLE = {"headline": 30, "description": 90, "path": 15, "headlines": (3, 15), "descriptions": (2, 4)}
META = {"primary_text": 125, "headline": 40, "description": 30}  # where text is cut off on phones
META_CTAS = ("SHOP_NOW", "LEARN_MORE", "SIGN_UP", "ORDER_NOW", "BOOK_NOW", "CONTACT_US", "GET_OFFER", "GET_QUOTE",
             "SUBSCRIBE", "DOWNLOAD", "APPLY_NOW", "SEND_WHATSAPP_MESSAGE", "CALL_NOW", "WATCH_MORE")
MATCH = ("broad", "phrase", "exact")


def load(path):
    with open(path, encoding="utf-8") as f:
        return json.load(f)


def validate(spec):
    p = []
    if not spec.get("brand"):
        p.append("brand is required")
    g, m = spec.get("google"), spec.get("meta")
    if not g and not m:
        p.append("give google, meta or both")
    for i, ag in enumerate((g or {}).get("ad_groups", []), 1):
        tag = "google ad group %d (%s)" % (i, ag.get("name", "?"))
        for k in ("name", "final_url", "headlines", "descriptions"):
            if not ag.get(k):
                p.append("%s: needs %s" % (tag, k))
        for kw in ag.get("keywords", []):
            if not kw.get("text") or kw.get("match", "phrase") not in MATCH:
                p.append("%s: every keyword needs text and match broad, phrase or exact" % tag)
    if g is not None and not g.get("ad_groups"):
        p.append("google needs ad_groups")
    for i, ad in enumerate((m or {}).get("ads", []), 1):
        tag = "meta ad %d (%s)" % (i, ad.get("name", "?"))
        for k in ("name", "primary_text", "headline", "url"):
            if not ad.get(k):
                p.append("%s: needs %s" % (tag, k))
        if ad.get("cta") and ad["cta"] not in META_CTAS:
            p.append("%s: cta must be one of %s" % (tag, ", ".join(META_CTAS)))
    if m is not None and not m.get("ads"):
        p.append("meta needs ads")
    return p


def counted(text, limit):
    return "%s  (%d/%d)" % (text, len(text), limit)


def ads_md(spec):
    out = ["# Ads: %s" % spec["brand"], ""]
    g = spec.get("google")
    if g:
        out += ["## Google search ads (campaign: %s)" % g.get("campaign", spec["brand"]), ""]
        for ag in g["ad_groups"]:
            out += ["### Ad group: %s" % ag["name"], "", "Final URL: %s" % ag["final_url"],
                    "Display path: /%s" % "/".join(x for x in (ag.get("path1"), ag.get("path2")) if x), "", "Headlines:", ""]
            out += ["%d. %s%s" % (i, counted(h, GOOGLE["headline"]), "  [pinned to position %s]" % ag["pins"][str(i)] if str(i) in ag.get("pins", {}) else "")
                    for i, h in enumerate(ag["headlines"], 1)]
            out += ["", "Descriptions:", ""] + ["%d. %s" % (i, counted(d, GOOGLE["description"])) for i, d in enumerate(ag["descriptions"], 1)]
            if ag.get("keywords"):
                fmt = {"broad": "%s", "phrase": '"%s"', "exact": "[%s]"}
                out += ["", "Keywords: " + ", ".join(fmt[k.get("match", "phrase")] % k["text"] for k in ag["keywords"])]
            if ag.get("negatives"):
                out += ["Negative keywords: " + ", ".join(ag["negatives"])]
            out.append("")
    m = spec.get("meta")
    if m:
        out += ["## Meta ads (Facebook and Instagram)", ""]
        for ad in m["ads"]:
            out += ["### %s" % ad["name"], "", "Primary text (%d characters; phones show about %d):" % (len(ad["primary_text"]), META["primary_text"]), "", "```",
                    ad["primary_text"], "```", "", "Headline: " + counted(ad["headline"], META["headline"])]
            if ad.get("description"):
                out.append("Description: " + counted(ad["description"], META["description"]))
            out += ["Button: %s" % ad.get("cta", "LEARN_MORE").replace("_", " ").title(), "Link: %s" % ad["url"]]
            if ad.get("image_brief"):
                out.append("Image: %s" % ad["image_brief"])
            out.append("")
    return "\n".join(out)


def write_csv(path, rows):
    with open(path, "w", newline="", encoding="utf-8-sig") as f:
        w = csv.DictWriter(f, fieldnames=list(rows[0]))
        w.writeheader()
        w.writerows(rows)


def main():
    a = sys.argv[1:]
    if not a:
        sys.exit(__doc__)
    spec = load(a[0])
    out = a[a.index("--out") + 1] if "--out" in a else "ads"
    probs = validate(spec)
    if probs:
        sys.exit("spec problems:\n- " + "\n- ".join(probs))
    os.makedirs(out, exist_ok=True)
    with open(os.path.join(out, "ads.md"), "w", encoding="utf-8") as f:
        f.write(ads_md(spec))
    made = ["ads.md"]
    g = spec.get("google")
    if g:
        camp = g.get("campaign", spec["brand"])
        rows, kws = [], []
        for ag in g["ad_groups"]:
            row = {"Campaign": camp, "Ad group": ag["name"], "Ad type": "Responsive search ad", "Final URL": ag["final_url"],
                   "Path 1": ag.get("path1", ""), "Path 2": ag.get("path2", "")}
            for i in range(15):
                row["Headline %d" % (i + 1)] = ag["headlines"][i] if i < len(ag["headlines"]) else ""
                row["Headline %d position" % (i + 1)] = ag.get("pins", {}).get(str(i + 1), "")
            for i in range(4):
                row["Description %d" % (i + 1)] = ag["descriptions"][i] if i < len(ag["descriptions"]) else ""
            rows.append(row)
            kws += [{"Campaign": camp, "Ad group": ag["name"], "Keyword": k["text"], "Criterion Type": k.get("match", "phrase").title()} for k in ag.get("keywords", [])]
            kws += [{"Campaign": camp, "Ad group": ag["name"], "Keyword": n, "Criterion Type": "Negative Phrase"} for n in ag.get("negatives", [])]
        write_csv(os.path.join(out, "google_ads.csv"), rows)
        made.append("google_ads.csv")
        if kws:
            write_csv(os.path.join(out, "google_keywords.csv"), kws)
            made.append("google_keywords.csv")
    m = spec.get("meta")
    if m:
        write_csv(os.path.join(out, "meta_ads.csv"), [{"Ad name": ad["name"], "Primary text": ad["primary_text"], "Headline": ad["headline"],
                                                       "Description": ad.get("description", ""), "Call to action": ad.get("cta", "LEARN_MORE"),
                                                       "Website URL": ad["url"], "Image brief": ad.get("image_brief", "")} for ad in m["ads"]])
        made.append("meta_ads.csv")
    print("built %s: %s" % (out, ", ".join(made)))


if __name__ == "__main__":
    main()
