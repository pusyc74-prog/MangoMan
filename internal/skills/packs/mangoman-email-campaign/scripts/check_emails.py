"""Check a built email campaign before sending.

Usage: python3 check_emails.py campaign.json emails
Checks: the spec is valid (sender address, sending order); each email has an
unsubscribe link and the sender's postal address; stays under Gmail's 102 KB
clipping size; subject and preview text lengths; spam-trigger wording, shouting
and exclamation marks; every link is a real https link; every image has alt
text and images are uploaded; the phone layout has no sideways scrolling and
easy buttons; every number traces to the user's facts; no placeholder text;
plain-text version present. Prints PASS, WARN or FAIL; exits 1 on any FAIL.
"""
import os
import re
import sys

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
import build_emails as B  # noqa: E402
import render  # noqa: E402
import checks as C  # noqa: E402

SPAMMY = re.compile(r"\b(free money|100% free|act now|buy now!|click here|cash bonus|double your|earn \$|extra cash|get paid|guaranteed?|winner|you(?:'ve| have) won|risk[- ]free|no obligation|urgent|limited time only|once in a lifetime|congratulations|dear friend|miracle|lowest price|cheap|\$\$\$|₹₹₹)\b", re.I)
PLACEHOLDER = re.compile(r"lorem ipsum|\bTBD\b|\bTODO\b|\[(?:name|company|link|insert|date)[^\]]*\]|xxx+|example\.com", re.I)
RISKY = re.compile(r"\b(guaranteed?|cures?|100% (?:safe|natural|pure)|best in (?:india|the world)|clinically proven|no side effects)\b", re.I)
EMOJI = re.compile("[\U0001F300-\U0001FAFF☀-➿]")
MOBILE_JS = r"""
() => ({
  hscroll: Math.max(0, document.documentElement.scrollWidth - window.innerWidth),
  small_btn: [...document.querySelectorAll('td[bgcolor] a')].filter(a => a.getBoundingClientRect().height < 44).map(a => a.textContent.trim()),
  min_font: Math.min(...[...document.querySelectorAll('p,li,td,a,h1,h2,h3')].filter(x => x.textContent.trim() && x.offsetParent !== null).map(x => parseFloat(getComputedStyle(x).fontSize)).filter(f => f > 0)),
  broken: [...document.querySelectorAll('img')].filter(i => i.complete && i.naturalWidth === 0).map(i => i.getAttribute('src'))
})
"""


def email_texts(m):
    out = [m["subject"], m["preview"]]
    for b in m["blocks"]:
        for k in ("text", "label", "author", "note"):
            v = b.get(k)
            if isinstance(v, list):
                out += v
            elif isinstance(v, str):
                out.append(v)
        out += [x for x in b.get("items", []) if isinstance(x, str)]
        for p in b.get("items", []):
            if isinstance(p, dict):
                out += [str(p.get(k, "")) for k in ("name", "price", "cta")]
    return out


def links(m):
    hs = []
    for b in m["blocks"]:
        if b.get("href"):
            hs.append(b["href"])
        for p in b.get("items", []):
            if isinstance(p, dict) and p.get("href"):
                hs.append(p["href"])
        for x in ([b["text"]] if isinstance(b.get("text"), str) else b.get("text", []) if isinstance(b.get("text"), list) else []) + \
                [i for i in b.get("items", []) if isinstance(i, str)]:
            hs += [mm.group(2) for mm in B.LINK.finditer(x)]
    return hs




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
    esp = spec.get("esp", "generic")
    unsub_tag = B.ESP[esp][1]
    pool = C.fact_pool(spec.get("facts", {}))
    hard = {k: [] for k in ("missing", "unsub", "address", "size", "links", "numbers", "placeholder", "text", "subject_long")}
    soft = {k: [] for k in ("subject", "preview", "spam", "shout", "excl", "emoji", "risky", "ctas", "imgheavy", "size_near")}
    for m in spec["emails"]:
        mid = m["id"]
        hp = os.path.join(out, mid + ".html")
        if not os.path.exists(hp):
            hard["missing"].append(mid)
            continue
        doc = open(hp, encoding="utf-8").read()
        if unsub_tag not in doc:
            hard["unsub"].append(mid)
        if spec["sender"]["address"] not in doc and B.e(spec["sender"]["address"]) not in doc:
            hard["address"].append(mid)
        kb = len(doc.encode("utf-8")) / 1024
        if kb > 102:
            hard["size"].append("%s (%.0f KB)" % (mid, kb))
        elif kb > 85:
            soft["size_near"].append("%s (%.0f KB)" % (mid, kb))
        tp = os.path.join(out, mid + ".txt")
        if not os.path.exists(tp) or unsub_tag not in open(tp, encoding="utf-8").read():
            hard["text"].append(mid)
        sj, pv = m["subject"], m["preview"]
        if len(sj) > 90:
            hard["subject_long"].append("%s (%d)" % (mid, len(sj)))
        elif len(sj) > 60 or len(sj) < 15:
            soft["subject"].append("%s (%d characters)" % (mid, len(sj)))
        if not (35 <= len(pv) <= 140) or pv.strip().lower() == sj.strip().lower():
            soft["preview"].append("%s (%d characters%s)" % (mid, len(pv), ", same as the subject" if pv.strip().lower() == sj.strip().lower() else ""))
        texts = email_texts(m)
        for t in texts:
            mm = SPAMMY.search(t)
            if mm:
                soft["spam"].append('%s: "%s"' % (mid, mm.group(0)))
            mm = RISKY.search(t)
            if mm:
                soft["risky"].append('%s: "%s"' % (mid, mm.group(0)))
            mm = PLACEHOLDER.search(t)
            if mm:
                hard["placeholder"].append('%s: "%s"' % (mid, mm.group(0)))
            for u in C.untraced(t, pool):
                hard["numbers"].append("%s: %s" % (mid, u))
        caps = [w for w in re.findall(r"\b[A-Z]{5,}\b", sj)]
        if caps:
            soft["shout"].append("%s: %s" % (mid, ", ".join(caps)))
        if sj.count("!") > 1 or sum(t.count("!") for t in texts) > 4:
            soft["excl"].append(mid)
        if len(EMOJI.findall(sj)) > 1:
            soft["emoji"].append(mid)
        for h in links(m):
            if not re.match(r"^(https://\S+|mailto:\S+@\S+|tel:\+?[\d\s-]+)$", h) or "example.com" in h:
                hard["links"].append("%s: %s" % (mid, h))
        targets = {b["href"] for b in m["blocks"] if b["type"] == "button"}
        if len(targets) > 2:
            soft["ctas"].append(mid)
        words = sum(len(t.split()) for t in texts[2:])
        nimg = sum(1 for b in m["blocks"] if b["type"] == "image") + sum(1 for b in m["blocks"] if b["type"] == "products" for p in b["items"] if p.get("image"))
        if nimg >= 2 and words < 30:
            soft["imgheavy"].append(mid)

    def say(lst, ok, bad, level="FAIL"):
        r("PASS" if not lst else level, ok if not lst else bad + ": " + "; ".join(sorted(set(lst))[:6]))

    say(hard["missing"], "every email built", "emails not built")
    say(hard["unsub"], "every email has an unsubscribe link (%s)" % esp, "no unsubscribe link (required by law and by Gmail and Yahoo)")
    say(hard["address"], "every email shows the sender's postal address", "sender address missing")
    say(hard["size"], "every email under Gmail's 102 KB clipping size", "emails Gmail will clip (hiding the unsubscribe link)")
    say(hard["text"], "plain-text versions present", "plain-text version missing or without unsubscribe")
    say(hard["subject_long"], "subject lines are not overlong", "subject lines over 90 characters")
    say(soft["subject"], "subject lines 15 to 60 characters (fit a phone)", "subject lines outside 15 to 60 characters", "WARN")
    say(soft["preview"], "preview text 35 to 140 characters and adds to the subject", "preview text to fix", "WARN")
    say(hard["links"], "every link is a real https, mailto or tel link", "links that will not work")
    say(hard["numbers"], "every number comes from the facts the user gave", "numbers not in facts (ask the user, or remove them)")
    say(hard["placeholder"], "no placeholder text", "placeholder text left in")
    say(soft["spam"], "no spam-trigger phrases", "phrases spam filters dislike", "WARN")
    say(soft["shout"], "no shouting in subject lines", "capital-letter words in subjects", "WARN")
    say(soft["excl"], "exclamation marks used sparingly", "too many exclamation marks", "WARN")
    say(soft["emoji"], "emoji used sparingly in subjects", "more than one emoji in a subject", "WARN")
    say(soft["risky"], "no risky claims", "claims that need proof", "WARN")
    say(soft["ctas"], "one clear action per email", "more than two different button destinations", "WARN")
    say(soft["imgheavy"], "text-to-image balance is healthy", "mostly images, little text (spam filters and image-off readers)", "WARN")
    if soft["size_near"]:
        r("WARN", "close to Gmail's clipping size: " + "; ".join(soft["size_near"]))
    has_local = any(b["type"] == "image" and not b["src"].startswith("https://") for m in spec["emails"] for b in m["blocks"]) or \
        any(p.get("image") and not p["image"].startswith("https://") for m in spec["emails"] for b in m["blocks"] if b["type"] == "products" for p in b["items"])
    if (has_local or (spec.get("brand") or {}).get("logo")) and not spec.get("assets_base_url"):
        r("WARN", "images point to the local assets/ folder: upload them (your email tool's file manager or your site) and set assets_base_url, or replace the paths, before sending")
    else:
        r("PASS", "images are hosted (assets_base_url set)" if has_local else "no local images")

    hscroll, small, tiny, broken = [], [], [], []
    measured = False
    for m in spec["emails"]:
        ph = os.path.join(out, "preview", m["id"] + ".html")
        if not os.path.exists(ph):
            continue
        res = render.inspect(ph, MOBILE_JS, widths=(375,), height=800)
        if res is None:
            break
        measured = True
        x = res[375]
        if x["hscroll"] > 1:
            hscroll.append("%s (%d px)" % (m["id"], x["hscroll"]))
        small += ["%s: %s" % (m["id"], s) for s in x["small_btn"]]
        if x["min_font"] < 13:
            tiny.append("%s (%.0f px)" % (m["id"], x["min_font"]))
        broken += ["%s: %s" % (m["id"], s) for s in x["broken"]]
    if not measured:
        r("WARN", "phone layout not measured (no Playwright): send yourself a test email and open it on a phone")
    else:
        say(hscroll, "no sideways scrolling on a phone", "emails scroll sideways on a phone")
        say(broken, "every image loads in the preview", "images that do not load")
        say(small, "buttons are easy to tap", "small buttons on a phone", "WARN")
        say(tiny, "text is readable on a phone", "text smaller than 13 px on a phone", "WARN")
    return finish(rs)


def finish(rs):
    rep = C.Report()
    rep.rows = rs
    rep.finish()


if __name__ == "__main__":
    main()
