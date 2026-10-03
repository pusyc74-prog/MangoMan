"""Run a single-file web app in a browser and check it works.

Usage: python3 check_app.py app/index.html tests.json
Opens the app on a phone (390 px) and a laptop (1280 px) and fails on script
errors, failed loads, sideways scrolling, unlabelled fields or buttons, and
images without alt text. Then runs each scenario in tests.json, step by step:

  {"scenarios": [{"name": "GST is added", "steps": [
      {"fill": "#amount", "value": "1000"}, {"select": "#rate", "value": "0.18"},
      {"click": "#calc"}, {"expect_text": "#total", "contains": "1,180"},
      {"reload": true}, {"expect_count": "#history li", "count": 1},
      {"expect_visible": ".error"}, {"press": "#amount", "key": "Enter"}]}]}

Each scenario starts from a fresh page with empty storage. Prints PASS, WARN
or FAIL; exits 1 on any FAIL. Needs Playwright.
"""
import json
import os
import sys

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
import checks as C  # noqa: E402

AUDIT_JS = r"""
() => ({
  hscroll: Math.max(0, document.documentElement.scrollWidth - innerWidth),
  unlabelled: [...document.querySelectorAll('input:not([type=hidden]):not([type=submit]):not([type=button]):not([type=reset]):not([type=image]),select,textarea')].filter(el =>
      !(el.labels && el.labels.length) && !el.getAttribute('aria-label') && !el.getAttribute('aria-labelledby')).map(el => el.id || el.name || el.tagName),
  nameless: [...document.querySelectorAll('button,a[href],[role=button],input[type=submit],input[type=button]')].filter(el =>
      !(el.innerText || el.value || '').trim() && !el.getAttribute('aria-label') && !el.title && !el.querySelector('img[alt]:not([alt=""])')).map(el => el.id || el.outerHTML.slice(0, 40)),
  noalt: [...document.querySelectorAll('img')].filter(i => !i.hasAttribute('alt')).length,
  small: [...document.querySelectorAll('button,input:not([type=checkbox]):not([type=radio]),select,a[href]')].filter(el => { const r = el.getBoundingClientRect(); return r.width && r.height < 36; }).length,
})
"""


NEEDS = {"fill": [], "select": ["value"], "click": [], "check": [], "press": [], "reload": [], "expect_text": [],
         "expect_visible": [], "expect_hidden": [], "expect_count": ["count"], "expect_value": ["value"]}


def wait_for(page, ok, timeout=3000):
    """Poll until ok() is true (results often appear a moment after a click)."""
    for _ in range(timeout // 100):
        if ok():
            return True
        page.wait_for_timeout(100)
    return ok()


def run_step(page, s):
    """Do one step; return an error message or ''."""
    kind = next((k for k in NEEDS if k in s), None)
    if not kind:
        return "unknown step %s" % json.dumps(s)
    missing = [k for k in NEEDS[kind] if k not in s]
    if missing:
        return "%s step needs %s" % (kind, ", ".join(missing))
    if kind == "expect_text" and s.get("contains") is None and s.get("equals") is None:
        return "expect_text step needs contains or equals"
    if kind == "expect_count":
        try:
            n = int(s["count"])
        except (TypeError, ValueError):
            return "expect_count step: count must be a whole number"
    t = 4000
    if "fill" in s:
        page.fill(s["fill"], str(s.get("value", "")), timeout=t)
    elif "select" in s:
        page.select_option(s["select"], str(s["value"]), timeout=t)
    elif "click" in s:
        page.click(s["click"], timeout=t)
    elif "check" in s:
        page.check(s["check"], timeout=t)
    elif "press" in s:
        page.press(s["press"], s.get("key", "Enter"), timeout=t)
    elif "reload" in s:
        page.reload()
    elif kind == "expect_text":
        sel = s["expect_text"]
        good = lambda: (s.get("contains") is None or str(s["contains"]) in page.inner_text(sel, timeout=t)) and \
            (s.get("equals") is None or page.inner_text(sel, timeout=t).strip() == str(s["equals"]))
        if not wait_for(page, good):
            return "%s shows %r, expected %s" % (sel, page.inner_text(sel, timeout=t).strip()[:80],
                                                "it to contain %r" % s["contains"] if s.get("contains") is not None else "%r" % s["equals"])
    elif kind == "expect_visible" and not wait_for(page, lambda: page.is_visible(s["expect_visible"])):
        return "%s is not visible" % s["expect_visible"]
    elif kind == "expect_hidden" and not page.locator(s["expect_hidden"]).count():
        return "%s not found (check the selector)" % s["expect_hidden"]
    elif kind == "expect_hidden" and not wait_for(page, lambda: not page.is_visible(s["expect_hidden"])):
        return "%s should be hidden" % s["expect_hidden"]
    elif kind == "expect_count" and not wait_for(page, lambda: page.locator(s["expect_count"]).count() == n):
        return "%s: %d found, expected %d" % (s["expect_count"], page.locator(s["expect_count"]).count(), n)
    elif kind == "expect_value" and not wait_for(page, lambda: page.input_value(s["expect_value"], timeout=t) == str(s["value"])):
        return "%s has value %r, expected %r" % (s["expect_value"], page.input_value(s["expect_value"], timeout=t), s["value"])
    return ""


def main():
    if len(sys.argv) < 3:
        sys.exit(__doc__)
    app, tests = sys.argv[1], json.load(open(sys.argv[2], encoding="utf-8"))
    rep = C.Report()
    try:
        from playwright.sync_api import sync_playwright, Error as PWError
    except ImportError:
        rep.add("FAIL", "Playwright is needed to test the app: pip install playwright && python -m playwright install chromium")
        rep.finish()
    url = "file://" + os.path.abspath(app)
    scen = tests.get("scenarios", [])
    rep.check([] if scen else ["none"], "%d test scenarios" % len(scen), "no scenarios in tests.json: write one per feature")
    with sync_playwright() as p:
        b = p.chromium.launch()
        for w in (390, 1280):
            ctx = b.new_context(viewport={"width": w, "height": 844})
            page = ctx.new_page()
            errors, failed = [], []
            page.on("pageerror", lambda ex: errors.append(str(ex).splitlines()[0]))
            page.on("console", lambda m: m.type == "error" and errors.append(m.text[:120]))
            page.on("requestfailed", lambda r: failed.append(r.url[:100]))
            page.goto(url, wait_until="networkidle")
            x = page.evaluate(AUDIT_JS)
            label = "phone" if w == 390 else "laptop"
            rep.check(sorted(set(errors)), "no script errors on load (%s)" % label, "script errors on load (%s)" % label)
            rep.check(sorted(set(failed)), "everything loads (%s)" % label, "failed to load (%s)" % label)
            rep.check([] if x["hscroll"] <= 1 else ["%d px" % x["hscroll"]], "no sideways scrolling (%s)" % label, "page scrolls sideways (%s)" % label)
            if w == 390:
                rep.check(x["unlabelled"], "every field has a label", "fields without a label")
                rep.check(x["nameless"], "every button and link has a name", "buttons or links with no text or aria-label")
                rep.check([] if not x["noalt"] else ["%d" % x["noalt"]], "images have alt text", "images without alt text")
                rep.check([] if not x["small"] else ["%d" % x["small"]], "controls are easy to tap", "controls shorter than 36 px", "WARN")
            ctx.close()
        for sc in scen:
            ctx = b.new_context(viewport={"width": 390, "height": 844})
            page = ctx.new_page()
            errors = []
            page.on("pageerror", lambda ex: errors.append(str(ex).splitlines()[0]))
            page.goto(url, wait_until="networkidle")
            problem = ""
            for i, s in enumerate(sc.get("steps", []), 1):
                try:
                    problem = run_step(page, s)
                except PWError as ex:
                    problem = str(ex).splitlines()[0][:160]
                if problem or errors:
                    problem = "step %d: %s" % (i, problem or "script error: " + errors[0])
                    break
            rep.check([problem] if problem else [], "scenario passes: %s" % sc.get("name", "?"), "scenario fails: %s" % sc.get("name", "?"))
            ctx.close()
        b.close()
    size = os.path.getsize(app) / 1024
    rep.check([] if size <= 300 else ["%.0f KB" % size], "app file is %.0f KB" % size, "app file is large; keep it under 300 KB", "WARN")
    rep.finish()


if __name__ == "__main__":
    main()
