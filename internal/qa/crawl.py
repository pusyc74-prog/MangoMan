"""Click through a web app and report what breaks.

Usage: crawl.py URL MAX_PAGES SHOT_DIR. Prints JSON {visited, findings}.
Follows links on the same site, breadth first. On each page it records
console errors, uncaught errors, failed requests, broken images, and
sideways scrolling at phone width.
"""
import json
import os
import sys
from urllib.parse import urldefrag, urljoin, urlparse

from playwright.sync_api import sync_playwright

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
import render  # noqa: E402  (written next to this file: the computer's own browser)

PHONE = {"width": 390, "height": 844}


def main():
    start, limit, shots = sys.argv[1], int(sys.argv[2]), sys.argv[3]
    os.makedirs(shots, exist_ok=True)
    origin = urlparse(start).netloc
    queue, seen, visited, findings = [start], {urldefrag(start)[0]}, [], []
    with sync_playwright() as p:
        browser = render.launch(p)
        while queue and len(visited) < limit:
            url = queue.pop(0)
            visited.append(url)
            page = browser.new_page()
            errors = []
            page.on("console", lambda m: m.type == "error" and errors.append(("console_error", m.text)))
            page.on("pageerror", lambda e: errors.append(("page_error", str(e))))
            page.on("response", lambda r: r.status >= 400 and errors.append(("http_error", "%d %s" % (r.status, r.url))))
            shown = urldefrag(url)[0]
            try:
                page.goto(url, wait_until="networkidle", timeout=30000)
            except Exception as e:  # noqa: BLE001 - any load failure is a finding
                findings.append({"page": shown, "kind": "unreachable", "detail": str(e).splitlines()[0],
                                 "steps": ["Open %s" % shown]})
                page.close()
                continue
            for img in page.eval_on_selector_all("img", "els => els.filter(i => i.complete && !i.naturalWidth).map(i => i.src)"):
                errors.append(("broken_image", img))
            links = page.eval_on_selector_all("a[href]", "els => els.map(a => a.href)")
            page.set_viewport_size(PHONE)
            page.wait_for_timeout(300)
            wide = page.evaluate("document.documentElement.scrollWidth - window.innerWidth")
            if wide > 1:
                errors.append(("phone_overflow", "the page scrolls sideways by %d px at phone width" % wide))
            if errors:
                shot = os.path.join(shots, "page-%d.png" % len(visited))
                page.screenshot(path=shot)
                for kind, detail in dict.fromkeys(errors):
                    steps = ["Open %s" % shown]
                    if kind == "phone_overflow":
                        steps.append("Make the window 390 px wide (phone size)")
                        steps.append("Scroll sideways: the page moves")
                    elif kind in ("console_error", "page_error"):
                        steps.append("Open the browser console: the error appears on load")
                    else:
                        steps.append("Open the browser's network panel: the request fails")
                    findings.append({"page": shown, "kind": kind, "detail": detail[:500], "steps": steps, "screenshot": shot})
            page.close()
            for link in links:
                link = urldefrag(urljoin(url, link))[0]
                if urlparse(link).netloc == origin and urlparse(link).scheme in ("http", "https") and link not in seen:
                    seen.add(link)
                    queue.append(link)
        browser.close()
    print(json.dumps({"visited": [urldefrag(v)[0] for v in visited], "findings": findings}))


if __name__ == "__main__":
    main()
