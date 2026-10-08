"""Drive the coding screen (mangoman code --ui) through one small real task
and write a report: what the screen said while waiting, whether the work
landed, and how long it took.

    python3 scripts/workspace-check.py <screen URL with #token=...> <project dir> <out dir> [timeout s]

Approval cards are answered with Allow, as a user would. Exits 0 even when a
check fails: the report says what happened.
"""
import os
import sys
import time

from playwright.sync_api import sync_playwright

TASK = ("Make index.html in this folder: a page with the heading Hello from MangoMan "
        "and a button that counts how many times it was clicked, shown next to it. "
        "One file only. Then run ls to show the files.")

url, project, out = sys.argv[1], sys.argv[2], sys.argv[3]
limit = int(sys.argv[4]) if len(sys.argv) > 4 else 600
os.makedirs(out, exist_ok=True)
log, errors = [], []

with sync_playwright() as p:
    page = p.chromium.launch().new_page(viewport={"width": 1300, "height": 860})
    page.on("console", lambda m: m.type == "error" and errors.append(m.text))
    page.goto(url)
    page.wait_for_selector("#ws:not([hidden])", timeout=30000)
    page.click("#new-chat")
    time.sleep(1)
    page.fill("#prompt", TASK)
    page.click("#send")
    start = time.time()
    last, quiet, shot, began = "", 0, False, False
    while time.time() - start < limit:
        busy = page.is_visible("#busy")
        text = page.inner_text("#busy-text") if busy else "(done)"
        if text != last:
            log.append(f"{time.time() - start:6.1f}s  {text}")
            last = text
        if busy and "Waiting for" in text and not shot:
            page.screenshot(path=f"{out}/waiting.png")
            shot = True
        for b in page.locator("li.ask:not(.done) button.primary").all():
            b.click()
            log.append(f"{time.time() - start:6.1f}s  (allowed a command)")
        began = began or busy
        quiet = quiet + 1 if began and not busy else 0
        if quiet >= 10:  # idle for 5 s after the work
            break
        time.sleep(0.5)
    took = time.time() - start
    page.click("#tab-changes")
    time.sleep(2)
    page.screenshot(path=f"{out}/end.png", full_page=True)
    notes = page.locator("li.note").all_inner_texts()
    changes = page.inner_text("#changes")
    page.click("#tab-terminal")
    term = page.inner_text("#term")

html = ""
path = os.path.join(project, "index.html")
if os.path.exists(path):
    html = open(path, encoding="utf-8", errors="replace").read()
checks = [
    ("finished within the time limit", took < limit),
    ("index.html written", bool(html)),
    ("heading text in the page", "Hello from MangoMan" in html),
    ("a button in the page", "<button" in html.lower()),
    ("Changes tab lists index.html", "index.html" in changes),
    ("Terminal shows the ls output", "index.html" in term),
    ("no errors in the browser console", not errors),
]
with open(f"{out}/report.md", "w") as f:
    f.write(f"# Coding screen check\n\nTask: {TASK}\n\nTook {took:.0f} s.\n\n")
    for name, ok in checks:
        f.write(f"- {'PASS' if ok else 'FAIL'}  {name}\n")
    f.write("\n## What the screen said\n\n```\n" + "\n".join(log) + "\n```\n")
    if notes:
        f.write("\n## Notes in the chat\n\n" + "\n".join(f"- {n}" for n in notes) + "\n")
    if errors:
        f.write("\n## Console errors\n\n" + "\n".join(f"- {e}" for e in errors) + "\n")
print(open(f"{out}/report.md").read())
