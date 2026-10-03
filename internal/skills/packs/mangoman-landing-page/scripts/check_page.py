"""Check a built landing page before delivering it.

Usage: python3 check_page.py page.json site
Checks: the spec is valid; the page opens at phone (390 px), tablet (768 px)
and desktop (1440 px) widths with no sideways scrolling and no text cut off;
the main button is visible without scrolling on a phone; one h1 and headings
in order; every image has alt text; form fields have labels; tap targets are
big enough; text is not too small; links point somewhere real; title and
description fit Google's display; page weight; every number traces to the
user's facts; no placeholder text; risky claims flagged.
Prints PASS, WARN or FAIL; exits 1 on any FAIL.
"""
import os
import re
import sys

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
import build_page as B  # noqa: E402
import render  # noqa: E402
import checks as C  # noqa: E402

PLACEHOLDER = re.compile(r"lorem ipsum|\bTBD\b|\bTODO\b|\[(?:your|company|name|insert)[^\]]*\]|xxx+|example\.com|placeholder", re.I)
RISKY = re.compile(r"\b(guaranteed?|100% (?:safe|natural|pure|effective|results)|cures?|risk[- ]free|no side effects|#1|number one|best in (?:india|the world|town|class)|cheapest|lowest price ever|miracle|clinically proven)\b", re.I)

PAGE_JS = r"""
() => {
  const vw = window.innerWidth, vh = window.innerHeight, out = {};
  out.hscroll = Math.max(0, document.documentElement.scrollWidth - vw);
  out.cut = [];
  document.querySelectorAll('[data-check]').forEach(el => {
    if (el.scrollWidth - el.clientWidth > 1) out.cut.push((el.textContent || '').trim().slice(0, 40));
  });
  const cta = document.querySelector('[data-cta=primary]');
  out.cta_top = cta ? Math.round(cta.getBoundingClientRect().bottom) : null;
  out.vh = vh;
  out.h1 = document.querySelectorAll('h1').length;
  let last = 1; out.skips = [];
  document.querySelectorAll('main h1, main h2, main h3, main h4').forEach(h => {
    const l = +h.tagName[1]; if (l > last + 1) out.skips.push(h.textContent.trim().slice(0, 40)); last = l; });
  out.noalt = [...document.querySelectorAll('img')].filter(i => !i.hasAttribute('alt')).length;
  out.emptyalt = [...document.querySelectorAll('main img')].filter(i => i.getAttribute('alt') === '').length;
  out.nolabel = [...document.querySelectorAll('input,select,textarea')].filter(i => !i.closest('label') && !i.getAttribute('aria-label')).length;
  out.small_tap = [...document.querySelectorAll('a.btn,button,summary,input,select')].filter(a => {
    const r = a.getBoundingClientRect(); return r.width > 0 && r.height < 40; }).map(a => (a.textContent || a.name || '').trim().slice(0, 30));
  let minfs = 99, where = '';
  const walk = document.createTreeWalker(document.body, NodeFilter.SHOW_TEXT);
  while (walk.nextNode()) {
    const n = walk.currentNode; if (!n.textContent.trim()) continue;
    const el = n.parentElement; const cs = getComputedStyle(el);
    if (cs.display === 'none' || cs.visibility === 'hidden' || el.closest('details:not([open]) > p')) continue;
    const fs = parseFloat(cs.fontSize); if (fs < minfs) { minfs = fs; where = n.textContent.trim().slice(0, 30); }
  }
  out.min_font = minfs; out.min_font_at = where;
  const ids = new Set([...document.querySelectorAll('[id]')].map(x => x.id));
  out.dead = [...document.querySelectorAll('a[href^="#"]')].map(a => a.getAttribute('href').slice(1)).filter(h => h && !ids.has(h));
  out.broken_img = [...document.querySelectorAll('img')].filter(i => i.complete && i.naturalWidth === 0).map(i => i.getAttribute('src'));
  return out;
}
"""


def visible_text(spec):
    out = []

    def walk(o, key=""):
        if isinstance(o, str):
            if key not in ("href", "type", "id", "image", "src", "logo", "to", "url", "tone", "nav"):
                out.append(o)
        elif isinstance(o, dict):
            for k, v in o.items():
                if k not in ("facts", "action"):
                    walk(v, k)
        elif isinstance(o, list):
            for v in o:
                walk(v, key)
    walk({k: v for k, v in spec.items() if k not in ("facts", "brand", "contact", "social")})
    return out




def folder_size(d):
    return sum(os.path.getsize(os.path.join(r, f)) for r, _, fs in os.walk(d) for f in fs)


def main():
    if len(sys.argv) < 3:
        sys.exit(__doc__)
    src, site = sys.argv[1], sys.argv[2]
    spec = B.load(src)
    bdir = os.path.dirname(os.path.abspath(src))
    rs = []

    def r(level, msg):
        rs.append((level, msg))

    probs = B.validate(spec, bdir)
    r("PASS" if not probs else "FAIL", "spec is valid" if not probs else "; ".join(probs))
    page = os.path.join(site, "index.html")
    if not os.path.exists(page):
        r("FAIL", "no index.html: build the page first")
        return finish(rs)

    m = spec.get("meta", {})
    t, d = m.get("title", ""), m.get("description", "")
    r("PASS" if 15 <= len(t) <= 60 else "WARN", "title fits Google results (%d characters)" % len(t) if 15 <= len(t) <= 60 else
      "title is %d characters; Google shows about 60, aim for 30 to 60 with the brand and the offer" % len(t))
    r("PASS" if 70 <= len(d) <= 160 else "WARN", "description fits Google results (%d characters)" % len(d) if 70 <= len(d) <= 160 else
      "description is %d characters; aim for 70 to 160, saying what it is and why to click" % len(d))
    r("PASS" if m.get("url") else "WARN", "site address set (canonical and sharing image)" if m.get("url") else
      "no meta.url: add the final address so link previews and search use it")

    res = render.inspect(page, PAGE_JS, widths=(390, 768, 1440), height=844)
    if res is None:
        r("WARN", "page not measured (no Playwright): open it on a phone and a laptop and check by eye")
    else:
        hs = ["%d px wide: %d px" % (w, x["hscroll"]) for w, x in res.items() if x["hscroll"] > 1]
        r("PASS" if not hs else "FAIL", "no sideways scrolling on phone, tablet or desktop" if not hs else "page scrolls sideways at " + "; ".join(hs))
        cut = sorted({c for x in res.values() for c in x["cut"]})
        r("PASS" if not cut else "FAIL", "no text is cut off" if not cut else "text cut off (a word too long for its box): " + "; ".join(cut[:5]))
        ph = res[390]
        if ph["cta_top"] is None:
            r("WARN", "no main button in the hero: give the hero a cta")
        else:
            ok = ph["cta_top"] <= ph["vh"]
            r("PASS" if ok else "FAIL", "main button visible without scrolling on a phone" if ok else
              "on a phone the main button starts below the first screen (%d px, screen %d px): shorten the hero headline or sub" % (ph["cta_top"], ph["vh"]))
        r("PASS" if ph["h1"] == 1 else "FAIL", "one main heading (h1)" if ph["h1"] == 1 else "%d h1 headings; there must be exactly one" % ph["h1"])
        r("PASS" if not ph["skips"] else "WARN", "headings in order" if not ph["skips"] else "heading levels skip at: " + "; ".join(ph["skips"][:3]))
        r("PASS" if not ph["noalt"] and not ph["emptyalt"] else "FAIL", "every image has alt text" if not ph["noalt"] and not ph["emptyalt"] else
          "%d images without alt text" % (ph["noalt"] + ph["emptyalt"]))
        r("PASS" if not ph["nolabel"] else "FAIL", "every form field has a label" if not ph["nolabel"] else "%d form fields without a label" % ph["nolabel"])
        r("PASS" if not ph["small_tap"] else "WARN", "buttons and fields are easy to tap" if not ph["small_tap"] else
          "small tap targets on a phone: " + ", ".join(ph["small_tap"][:4]))
        r("PASS" if ph["min_font"] >= 13 else "WARN", "text is readable on a phone (smallest %.0f px)" % ph["min_font"] if ph["min_font"] >= 13 else
          "text as small as %.0f px on a phone (\"%s\")" % (ph["min_font"], ph["min_font_at"]))
        dead = sorted({x for v in res.values() for x in v["dead"]})
        r("PASS" if not dead else "FAIL", "every in-page link has a target" if not dead else "links to missing sections: " + ", ".join(dead))
        broken = sorted({x for v in res.values() for x in v["broken_img"]})
        r("PASS" if not broken else "FAIL", "every image loads" if not broken else "images that do not load: " + ", ".join(broken))

    size = folder_size(site)
    mb = size / 1e6
    r("PASS" if mb <= 2.5 else ("WARN" if mb <= 5 else "FAIL"), "page weight %.1f MB" % mb if mb <= 2.5 else
      "page weight %.1f MB: use fewer or smaller images (phones on mobile data load it slowly)" % mb)

    texts = visible_text(spec)
    pool = C.fact_pool(spec.get("facts", {}))
    bad = sorted({u for tx in texts for u in C.untraced(tx, pool)})
    r("PASS" if not bad else "FAIL", "every number comes from the facts the user gave" if not bad else
      "numbers not in facts (ask the user, or remove them): " + ", ".join(bad[:8]))
    ph_ = sorted({mm.group(0) for tx in texts for mm in [PLACEHOLDER.search(tx)] if mm})
    r("PASS" if not ph_ else "FAIL", "no placeholder text" if not ph_ else "placeholder text left in: " + ", ".join(ph_))
    risky = sorted({mm.group(0) for tx in texts for mm in [RISKY.search(tx)] if mm})
    r("PASS" if not risky else "WARN", "no risky claims" if not risky else "claims that need proof or may break consumer rules: " + ", ".join(risky))
    has_form = any(s.get("form") for s in spec.get("sections", []))
    c = spec.get("contact", {})
    r("PASS" if has_form or c.get("phone") or c.get("email") or c.get("whatsapp") else "WARN",
      "visitors have a way to reach the business" if has_form or c else "no form and no contact details: visitors cannot act")
    return finish(rs)


def finish(rs):
    rep = C.Report()
    rep.rows = rs
    rep.finish()


if __name__ == "__main__":
    main()
