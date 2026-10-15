#!/usr/bin/env python3
"""Tests Chargesheets end to end with real free models: a running MangoMan,
the five made-up files of dash-test.py, questions whose answers this script
works out itself from the raw rows, a follow-up question, and a public
Google Sheet. Writes report.md.

  MANGOMAN_HOME=... python3 scripts/sheets-test.py   (MangoMan serving, openpyxl installed)
"""
import importlib.util
import json
import os
import re
import sys
import tempfile
import time
import traceback
import urllib.error
import urllib.request

import openpyxl

HERE = os.path.dirname(os.path.abspath(__file__))
spec = importlib.util.spec_from_file_location("dashtest", os.path.join(HERE, "dash-test.py"))
dt = importlib.util.module_from_spec(spec)
spec.loader.exec_module(dt)
FILES = {f["name"]: f for f in dt.FILES}
CFG = json.load(open(os.path.join(os.environ["MANGOMAN_HOME"], "config.json")))
BASE = f"http://127.0.0.1:{CFG['port']}"
# A public sample sheet (Google's own Sheets API example).
GOOGLE = "https://docs.google.com/spreadsheets/d/1BxiMVs0XRA5nFMdKvBdBZjgmUUqptlbs74OgvE2upms/edit"


def call(method, path, body=None, raw=None, timeout=600):
    data = raw if raw is not None else (json.dumps(body).encode() if body is not None else None)
    req = urllib.request.Request(BASE + path, data=data, method=method,
                                 headers={"Authorization": "Bearer " + CFG["token"], "Content-Type": "application/json"})
    try:
        with urllib.request.urlopen(req, timeout=timeout) as r:
            return json.loads(r.read() or b"{}")
    except urllib.error.HTTPError as e:
        return {"error": json.loads(e.read() or b"{}")}


def money(s):
    """₹5,02,300 / (1,200) / 49.99 USD / 12,500 to a number, or None."""
    s = (s or "").replace("₹", "").replace("USD", "").strip()
    neg = s.startswith("(")
    s = s.strip("()").replace(",", "").strip()
    try:
        v = float(s)
    except ValueError:
        return None
    return -v if neg else v


def rows_of(f):
    h = f["key"]["header_row"]
    names = f["rows"][h - 1]
    body = [r for r in f["rows"][h:] if r and r[0] != "Total"]
    return [dict(zip(names, r)) for r in body]


def truths():
    """(file, question, follow-up of, expected regexes, expected numbers)."""
    qs = []
    s = rows_of(FILES["Sales orders (India)"])

    def month(r):
        d = r["Order Date"]
        return int(d.split("/")[1]) if "/" in d else int(d.split("-")[1])
    by = lambda m, ch=None: sum(money(r["Amount"]) for r in s if month(r) == m and (ch is None or r["Channel"] == ch))  # noqa: E731
    best = max(["Website", "Amazon", "Flipkart"], key=lambda ch: by(9, ch))
    qs += [("Sales orders (India)", "Which channel had the highest net sales in September 2026?", None, [best.lower()], [by(9, best)]),
           ("Sales orders (India)", "What were total net sales in August 2026?", None, [], [by(8)]),
           ("Sales orders (India)", "And in July?", 1, [], [by(7)]),
           ("Sales orders (India)", "Why did sales drop in September compared to August?", None, ["amazon"], []),
           ("Sales orders (India)", "How many orders were returns (negative amounts)?", None, [], [sum(1 for r in s if money(r["Amount"]) < 0)])]
    fd = rows_of(FILES["Product feed (Google Shopping)"])
    eq = [money(r["price"]) for r in fd if r["product type"].endswith("Equipment")]
    qs += [("Product feed (Google Shopping)", "How many products are out of stock?", None, [], [sum(r["availability"] == "out of stock" for r in fd)]),
           ("Product feed (Google Shopping)", "How many products have no GTIN?", None, [], [sum(not r["gtin"] for r in fd)]),
           ("Product feed (Google Shopping)", "What is the average price of the Equipment products?", None, [], [sum(eq) / len(eq)])]
    ld = rows_of(FILES["CRM leads"])
    src = {}
    for r in ld:
        src[r["Source"]] = src.get(r["Source"], 0) + 1
    top = max(src, key=src.get)
    qs += [("CRM leads", "How many leads are in Proposal status?", None, [], [sum(r["Status"] == "Proposal" for r in ld)]),
           ("CRM leads", "Which source brought the most leads?", None, [re.escape(top.lower())], [src[top]]),
           ("CRM leads", "How many leads have no deal value?", None, [], [sum(not r["Deal Value (INR)"] for r in ld)])]
    ad = rows_of(FILES["Ad report (months across)"])
    spend = {r["Campaign"]: sum(money(r[f"{m} Spend"]) for m in ("Jul", "Aug", "Sep")) for r in ad}
    conv = {r["Campaign"]: sum(int(r[f"{m} Conv"]) for m in ("Jul", "Aug", "Sep")) for r in ad}
    zero = [c for c in conv if conv[c] == 0]
    qs += [("Ad report (months across)", "Which campaign spent the most in total from July to September?", None,
            [re.escape(max(spend, key=spend.get).lower())], [max(spend.values())]),
           ("Ad report (months across)", "Which campaigns got zero conversions from July to September?", None, [re.escape(z.split(" - ")[-1].lower()) for z in zero], []),
           ("Ad report (months across)", "What was the total ad spend in August?", None, [], [sum(money(r["Aug Spend"]) for r in ad)])]
    inv = rows_of(FILES["Inventory"])
    cats = {}
    for r in inv:
        cats[r["Category"]] = cats.get(r["Category"], 0) + 1
    sold = [r for r in inv if float(r["Avg Daily Sales"]) > 0]
    low = min(sold, key=lambda r: int(r["Qty on Hand"]) / float(r["Avg Daily Sales"]))
    qs += [("Inventory", "How many items have zero quantity on hand?", None, [], [sum(int(r["Qty on Hand"]) == 0 for r in inv)]),
           ("Inventory", "Which category has the most items?", None, [re.escape(max(cats, key=cats.get).lower())], [max(cats.values())]),
           ("Inventory", "Which item will run out first at its average daily sales (ignore items with no sales)?", None, [re.escape(low["Item"].lower())], [])]
    return qs


def write_file(f, folder):
    """The made-up file as the user would have it: Excel for some, CSV for others."""
    if f["file"].endswith(".csv"):
        path = os.path.join(folder, f["file"])
        import csv
        with open(path, "w", newline="", encoding="utf-8") as out:
            csv.writer(out).writerows(f["rows"])
        return path
    wb = openpyxl.Workbook()
    ws = wb.active
    for r in f["rows"]:
        ws.append([c if c != "" else None for c in r])
    path = os.path.join(folder, f["file"])
    wb.save(path)
    return path


def numbers(text):
    out = []
    for m in re.finditer(r"(\d[\d,]*(?:\.\d+)?)\s*(lakh|lakhs|crore|k)?", text.replace("−", "-"), re.I):
        try:
            v = float(m.group(1).replace(",", ""))
        except ValueError:
            continue
        out.append(v * {"lakh": 1e5, "lakhs": 1e5, "crore": 1e7, "k": 1e3}.get((m.group(2) or "").lower(), 1))
    return out


def grade(text, words, nums):
    low = text.lower()
    miss = [w for w in words if not re.search(w, low)]
    found = numbers(text)
    for n in nums:
        if not any(abs(x - abs(n)) <= max(0.006 * abs(n), 0.51) for x in found):
            miss.append(f"{n:g}")
    return miss


def main():
    out = ["# Chargesheets test with real free models, " + time.strftime("%d %B %Y %H:%M UTC", time.gmtime()), ""]
    folder = tempfile.mkdtemp()
    sheets, notes = {}, []
    for name, f in FILES.items():
        start = time.time()
        sh = call("POST", "/mangoman/sheets", {"title": ""})
        path = write_file(f, folder)
        up = call("POST", f"/mangoman/sheets/{sh['id']}/files?name={os.path.basename(path)}", raw=open(path, "rb").read())
        v = call("POST", f"/mangoman/sheets/{sh['id']}/load")
        if "error" in up or "error" in v:
            notes.append(f"| {name} | load failed: {up.get('error') or v.get('error')} |")
            continue
        sheets[name] = sh["id"]
        s = v["sheet"]
        notes.append(f"| {name} | {s['rows']} rows, {time.time() - start:.0f} s; measures {', '.join(s['roles'].get('measures') or [])}; "
                     f"date {s['roles'].get('date') or '-'}; about: {s.get('about') or '(no model description)'} |")
    out += ["## 1. Loading", "", "| File | Read |", "| --- | --- |"] + notes
    # Google Sheets link
    sh = call("POST", "/mangoman/sheets", {"title": ""})
    g = call("POST", f"/mangoman/sheets/{sh['id']}/google", {"url": GOOGLE})
    if "error" in g:
        out += ["", f"Google Sheet link: **failed**: {g['error']}"]
    else:
        v = call("POST", f"/mangoman/sheets/{sh['id']}/load")
        r = call("POST", f"/mangoman/sheets/{sh['id']}/refresh")
        out += ["", f"Google Sheet link: **works**. Title {g['title']!r}, {v.get('sheet', {}).get('rows')} rows; "
                f"refresh changed: {r.get('changed')} {r.get('error', '')}"]
    out += ["", "## 2. Questions", "", "| File | Question | Right | Missing | Unchecked numbers | Steps | Seconds | Answer |",
            "| --- | --- | --- | --- | --- | --- | --- | --- |"]
    right = total = 0
    history = {}
    def save():
        head = out[:2] + [f"**{right} of {total} answers right.**", ""] + out[2:]
        open("report.md", "w").write("\n".join(head) + "\n")

    for name, q, follow, words, nums in truths():
        if name not in sheets:
            continue
        total += 1
        try:
            right += ask(sheets[name], name, q, follow, words, nums, history, out)
        except Exception:  # noqa: BLE001 - one broken question must not end the run
            out.append(f"| {name} | {q} | NO | error | | | | {traceback.format_exc().splitlines()[-1][:300].replace('|', '/')} |")
        print(out[-1], flush=True)
        save()
    save()
    print("\n".join(out[:6]))
    return 0


def ask(sheet, name, q, follow, words, nums, history, out):
    hist = history.get(name, []) if follow else []
    start = time.time()
    job = call("POST", f"/mangoman/sheets/{sheet}/ask", {"question": q, "history": hist})
    res = {}
    while time.time() - start < 600:
        time.sleep(2)
        res = call("GET", f"/mangoman/sheets/{sheet}/ask/{job.get('job')}", timeout=60)
        if res.get("done") or "error" in res and not res.get("steps"):
            break
    a = res.get("answer") or {}
    text = str(a.get("text") or res.get("error") or "(no answer)")
    miss = grade(text, words, nums) if a else ["no answer"]
    history.setdefault(name, []).append({"q": q, "a": text})
    steps = a.get("steps") or []
    sql = " / ".join(s.get("sql", "") for s in steps)[:300].replace("|", "/")
    cell = text[:400].replace("|", "/").replace("\n", " ")
    out.append(f"| {name} | {q} | {'yes' if not miss else 'NO'} | {', '.join(miss)} | {', '.join(a.get('unchecked') or [])} | "
               f"{len(steps)} | {time.time() - start:.0f} | {cell} <br>`{sql}` |")
    return not miss


if __name__ == "__main__":
    sys.exit(main())
