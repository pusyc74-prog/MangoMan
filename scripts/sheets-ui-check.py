#!/usr/bin/env python3
"""Clicks through Chargesheets in a real browser (the nightly qa workflow):
upload the sample files, open every tab, keep a suggested dashboard, add a
chart, plan one with the AI, ask one question, at laptop and phone width,
in light and dark. Fails on a page error or a page wider than
the screen. Needs a serving MangoMan whose model is scripts/fake-model.py.

  python3 scripts/sheets-ui-check.py <dashboard url with #token=...> [screenshot dir]
"""
import os
import sys

from playwright.sync_api import sync_playwright

HERE = os.path.dirname(os.path.abspath(__file__))
FILES = [os.path.join(HERE, "..", "internal", "sheets", "testdata", f) for f in ("sales.xlsx", "jan.csv", "feb.csv", "spend.csv")]


def dashboards(page):
    """My dashboards: keep a suggestion, add a chart, plan one with the AI."""
    page.click(".cs-tab:has-text('My dashboards')")
    page.locator(".cs-card").first.click()
    page.click("text=Keep this dashboard")
    page.wait_for_selector("text=Kept in My dashboards", timeout=10000)
    before = page.locator(".cs-board > section").count()
    page.click("text=+ Add a chart")
    page.click(".cs-editor button.primary")
    page.wait_for_timeout(1200)
    if page.locator(".cs-board > section").count() != before + 1:
        raise SystemExit("FAIL the new chart was not added")
    page.click("text=Plan with AI")
    page.locator("#cs-plan .cs-suggest button").first.click()
    page.click("#cs-plan button.primary:has-text('Show')", timeout=60000)
    page.click("#cs-plan >> text=Close")
    page.wait_for_selector("text=Keep this dashboard")
    if page.locator(".cs-unverified").count():
        raise SystemExit("FAIL a chart could not be drawn: " + page.locator(".cs-unverified").first.inner_text())


def main():
    url, out = sys.argv[1], (sys.argv[2] if len(sys.argv) > 2 else "")
    errors = []
    with sync_playwright() as p:
        browser = p.chromium.launch()
        for w, h, scheme in [(1366, 900, "light"), (390, 844, "dark")]:
            page = browser.new_page(viewport={"width": w, "height": h}, color_scheme=scheme)
            page.on("pageerror", lambda e, w=w: errors.append(f"{w}px: {e}"))
            page.goto(url)
            page.click('a[data-view="chargesheets"]')
            if w > 1000:
                page.set_input_files('#view-chargesheets input[type="file"]', FILES)
                page.wait_for_selector("text=Here is what I read", timeout=60000)
                page.click("text=Open dashboard")
            else:
                page.click(".cs-card >> nth=0")
            page.wait_for_selector("#cs-body", timeout=30000)
            tabs = page.locator(".cs-tab").all_inner_texts()
            for i, name in enumerate(tabs):
                page.locator(".cs-tab").nth(i).click()
                page.wait_for_timeout(1200)
                width = page.evaluate("document.documentElement.scrollWidth")
                if width > w + 1:
                    errors.append(f"{w}px {name}: the page is {width}px wide")
                if out:
                    page.screenshot(path=os.path.join(out, f"sheets-{w}-{i:02d}.png"), full_page=True)
            if w > 1000:
                dashboards(page)
            page.click("text=Ask a question")
            page.locator(".cs-suggest button").first.click()
            page.wait_for_selector(".cs-a details", timeout=60000)
            print(f"{w}px: {len(tabs)} tabs ({', '.join(tabs)}); answer: {page.locator('.cs-a').last.inner_text()[:80]!r}")
            page.close()
        browser.close()
    for e in errors:
        print("FAIL", e)
    return 1 if errors else 0


if __name__ == "__main__":
    sys.exit(main())
