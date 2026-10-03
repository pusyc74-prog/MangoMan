"""Check a built post set before delivering it.

Usage: python3 check_posts.py posts.json posts
Checks: the spec is valid; every image exists at the right size; text fits
without shrinking below a readable size; captions fit each platform's limit;
hashtags are valid and not overdone; every number in the posts traces to a
fact the user gave; alt text is present; risky claims are flagged; Instagram
captions do not rely on links. Prints PASS, WARN or FAIL; exits 1 on any FAIL.
"""
import json
import os
import re
import sys

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
import build_posts as B  # noqa: E402
import tracenum  # noqa: E402
import vizlib as V  # noqa: E402

URL = re.compile(r"https?://\S+|www\.\S+")
TAG = re.compile(r"^#[^\W_][\w]*$", re.UNICODE)
RISKY = re.compile(r"\b(guaranteed?|100% (?:safe|natural|pure|effective)|cures?|risk[- ]free|no side effects|#1|number one|best in (?:india|the world|town)|cheapest|lowest price ever|miracle|clinically proven)\b", re.I)
SCALE_WARN = 0.78
WORDNUM = {"two": 2, "three": 3, "four": 4, "five": 5, "six": 6, "seven": 7, "eight": 8, "nine": 9, "ten": 10}
COUNTED = re.compile(r"\b(\d+|two|three|four|five|six|seven|eight|nine|ten)\s+(?:\w+\s+)?(checks|tips|steps|ways|reasons|mistakes|ideas|signs|rules|things|questions|lessons|hacks|facts)\b", re.I)


def promised_count(p):
    """How many items the post actually has, for 'five tips' style promises."""
    if p["layout"] == "list":
        return len(p.get("items", []))
    if p["layout"] == "carousel":
        return sum(1 for s in p.get("slides", []) if s.get("kind", "content") == "content")
    return None


def x_length(text):
    """X's weighted length: links count 23, most scripts 1, emoji and CJK 2."""
    n = 0
    t = URL.sub("x" * 23, text)
    for ch in t:
        cp = ord(ch)
        n += 1 if cp <= 0x10FF or 0x2000 <= cp <= 0x200D or 0x2010 <= cp <= 0x201F or 0x2032 <= cp <= 0x2037 else 2
    return n


def texts(p):
    """Every piece of text that appears on the images of a post."""
    keys = ("headline", "sub", "quote", "label", "cta", "source", "value")
    out = [str(p[k]) for k in keys if p.get(k)]
    out += [str(x) for x in p.get("items", []) + p.get("details", [])]
    for s in p.get("slides", []):
        out += [str(s[k]) for k in ("headline", "body", "cta", "sub") if s.get(k)]
    return out


def fact_pool(spec):
    pool = V.numbers_in(spec.get("facts", {}))

    def strings(o):
        if isinstance(o, str):
            yield o
        elif isinstance(o, dict):
            for v in o.values():
                yield from strings(v)
        elif isinstance(o, list):
            for v in o:
                yield from strings(v)
    for s in strings(spec.get("facts", {})):
        for _, cands in tracenum.mentions(s):
            pool += cands
    return pool


def main():
    if len(sys.argv) < 3:
        sys.exit(__doc__)
    src, out = sys.argv[1], sys.argv[2]
    spec = B.load(src)
    bdir = B.base_dir(src)
    rs = []

    def r(level, msg):
        rs.append((level, msg))

    probs = B.validate(spec, bdir)
    r("PASS" if not probs else "FAIL", "spec is valid" if not probs else "; ".join(probs))
    if probs:
        return finish(rs)
    posts = spec["posts"]

    # images and sizes
    missing, wrong = [], []
    try:
        from PIL import Image
    except ImportError:
        Image = None
    for pid, fm, j, _ in B.frames(spec, *_theme(spec, bdir)):
        path = os.path.join(out, B.frame_name(pid, fm, j))
        if not os.path.exists(path):
            missing.append(os.path.basename(path))
        elif Image:
            with Image.open(path) as im:
                if im.size != B.FORMATS[fm]:
                    wrong.append("%s is %dx%d, should be %dx%d" % ((os.path.basename(path),) + im.size + B.FORMATS[fm]))
    r("PASS" if not missing and not wrong else "FAIL", "every image built at the right size" if not missing and not wrong else
      "; ".join((["missing: " + ", ".join(missing[:6])] if missing else []) + wrong[:6]))

    # text fit
    fpath = os.path.join(out, "frames.json")
    if os.path.exists(fpath):
        meta = json.load(open(fpath, encoding="utf-8"))
        over, small = [], []
        measured = 0
        for m in meta:
            for f in m.get("fit") or []:
                measured += 1
                if f["overflow"]:
                    over.append(m["file"])
                elif f["scale"] < SCALE_WARN:
                    small.append("%s (%d%%)" % (m["file"], round(f["scale"] * 100)))
        if not measured:
            r("WARN", "text fit not measured (no Playwright); look at every image")
        else:
            r("PASS" if not over else "FAIL", "all text fits" if not over else
              "text does not fit even at the smallest readable size: %s; shorten it" % ", ".join(sorted(set(over))[:6]))
            r("PASS" if not small else "WARN", "text is at full size" if not small else
              "text shrank to fit, so it reads small on a phone: %s; consider shorter copy" % ", ".join(small[:6]))
    else:
        r("WARN", "no frames.json: images were not rendered")

    # captions per platform
    cap, tags_bad, tags_many, links = [], [], [], []
    for p in posts:
        for pl in spec.get("platforms", ["instagram"]):
            c = B.full_caption(p, pl, spec)
            n = x_length(c) if pl == "x" else len(c)
            if n > B.PLATFORMS[pl][1]:
                cap.append("%s on %s is %d characters, limit %d" % (p["id"], pl, n, B.PLATFORMS[pl][1]))
            if pl == "instagram" and URL.search(caption_text(p, pl)):
                links.append(p["id"])
            if pl == "instagram" and len(caption_text(p, pl).split("\n")[0]) > 125:
                cap.append("WARN:%s: Instagram shows about 125 characters before 'more'; put the hook first" % p["id"])
        tags = [t if t.startswith("#") else "#" + t for t in p.get("hashtags", [])]
        bad = [t for t in tags if not TAG.match(t) or t[1:].isdigit()]
        if bad:
            tags_bad.append("%s: %s" % (p["id"], ", ".join(bad)))
        if len({t.lower() for t in tags}) != len(tags):
            tags_bad.append("%s: repeated hashtags" % p["id"])
        if "instagram" in spec.get("platforms", ["instagram"]) and len(tags) > 30:
            tags_bad.append("%s: Instagram allows 30 hashtags" % p["id"])
        if len(tags) > 10:
            tags_many.append(p["id"])
    hard = [c for c in cap if not c.startswith("WARN:")]
    soft = [c[5:] for c in cap if c.startswith("WARN:")]
    r("PASS" if not hard else "FAIL", "captions fit every platform" if not hard else "; ".join(hard))
    if soft:
        r("WARN", "; ".join(soft))
    r("PASS" if not tags_bad else "FAIL", "hashtags are valid" if not tags_bad else
      "hashtags must be one word of letters, digits or _ : " + "; ".join(tags_bad))
    if tags_many:
        r("WARN", "more than 10 hashtags on %s: 3 to 8 relevant ones work better" % ", ".join(tags_many))
    if links:
        r("WARN", "links in Instagram captions are not clickable (%s): say 'link in bio' instead" % ", ".join(links))

    # numbers and claims
    pool = fact_pool(spec)
    untr, risky = [], []
    for p in posts:
        allt = texts(p) + [caption_text(p, pl) for pl in spec.get("platforms", ["instagram"])]
        for t in allt:
            u = tracenum.untraced(t, pool)
            if u:
                untr.append("%s: %s" % (p["id"], ", ".join(u)))
            m = RISKY.search(t)
            if m:
                risky.append('%s: "%s"' % (p["id"], m.group(0)))
    untr = sorted(set(untr))
    r("PASS" if not untr else "FAIL", "every number comes from the facts the user gave" if not untr else
      "numbers not in facts (ask the user, or remove them): " + "; ".join(untr[:8]))
    r("PASS" if not risky else "WARN", "no risky claims" if not risky else
      "claims that need proof or may break ad and consumer rules: " + "; ".join(sorted(set(risky))[:6]))

    # "five tips" must come with five tips
    counts = []
    for p in posts:
        have = promised_count(p)
        if have is None:
            continue
        for t in texts(p) + [caption_text(p, pl) for pl in spec.get("platforms", ["instagram"])]:
            for m in COUNTED.finditer(t):
                n = m.group(1).lower()
                n = WORDNUM.get(n, int(n) if n.isdigit() else None)
                if n is not None and n != have:
                    counts.append('%s says "%s" but has %d' % (p["id"], m.group(0), have))
    r("PASS" if not counts else "FAIL", "promised counts match the content" if not counts else "; ".join(sorted(set(counts))))

    alt_long = [p["id"] for p in posts if len(p.get("alt", "")) > 1000]
    r("PASS" if not alt_long else "WARN", "alt text on every post" if not alt_long else "alt text over 1,000 characters (X's limit): " + ", ".join(alt_long))
    has_logo = bool((spec.get("brand") or {}).get("logo") or (spec.get("brand") or {}).get("name"))
    r("PASS" if has_logo else "WARN", "brand shown on every image" if has_logo else "no logo or brand name: posts carry no brand")
    return finish(rs)


def caption_text(p, pl):
    return B.caption_for(p, pl) or ""


def _theme(spec, bdir):
    T, _ = B.BK.resolve_spec(spec, bdir)
    return T, bdir


def finish(rs):
    for level, msg in rs:
        print("%s  %s" % (level, msg))
    sys.exit(1 if any(l == "FAIL" for l, _ in rs) else 0)


if __name__ == "__main__":
    main()
