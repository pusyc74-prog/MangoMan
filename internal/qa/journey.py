"""Walk a journey through a web app, the way a visitor would.

Usage: journey.py BASE_URL STEPS_JSON SHOT_PATH. Prints JSON {failed, detail}:
failed is the 1-based step that went wrong (0 when all worked). Steps are
{"go": url or path}, {"click": text}, {"fill": label, "with": text},
{"see": text}. A screenshot of the page is saved when a step fails.
"""
import json
import os
import sys
from urllib.parse import urljoin

from playwright.sync_api import sync_playwright

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
import render  # noqa: E402  (written next to this file: the computer's own browser)

WAIT = 15000  # ms for each step


def field(page, label):
    for loc in (page.get_by_label(label), page.get_by_placeholder(label)):
        if loc.count():
            return loc.first
    return page.get_by_label(label).first  # waits, then fails with a clear message


def clickable(page, text):
    for role in ("button", "link"):
        loc = page.get_by_role(role, name=text)
        if loc.count():
            return loc.first
    return page.get_by_text(text).first


def main():
    base, steps, shot = sys.argv[1], json.loads(sys.argv[2]), sys.argv[3]
    out = {"failed": 0, "detail": ""}
    with sync_playwright() as p:
        browser = render.launch(p)
        page = browser.new_page()
        page.set_default_timeout(WAIT)
        # Only web pages: never a file on this computer or another kind of link.
        page.route("**/*", lambda route: route.continue_() if route.request.url.startswith(("http://", "https://"))
                   else route.abort())
        for i, s in enumerate(steps, 1):
            try:
                if "go" in s:
                    page.goto(urljoin(base, s["go"]), wait_until="load", timeout=30000)
                elif "click" in s:
                    clickable(page, s["click"]).click()
                    page.wait_for_load_state("load")
                elif "fill" in s:
                    field(page, s["fill"]).fill(s["with"])
                else:
                    page.get_by_text(s["see"]).first.wait_for(state="visible")
            except Exception as e:  # noqa: BLE001 - any failure of a step is the finding
                out = {"failed": i, "detail": str(e).splitlines()[0][:300]}
                try:
                    page.screenshot(path=shot)
                except Exception:  # noqa: BLE001 - the screenshot is a bonus
                    pass
                break
        browser.close()
    print(json.dumps(out))


if __name__ == "__main__":
    main()
