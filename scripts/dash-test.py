#!/usr/bin/env python3
"""Tests how well free models handle the AI parts of a data dashboard.

Five made-up files carry the traps real files have (a product feed, Indian
sales orders, CRM leads, a wide ad report, inventory). No real customer data.
For each file every model does three jobs, the ones the AI would do in a
dashboard agent while code does the counting:

  read     look at the top and bottom rows; say where the header is, whether
           there is a totals row, whether months run across, and what each
           column holds
  plan     pick tabs and charts from a fixed menu for the user's goal
  insight  write the top findings from numbers code already computed

Each answer is scored against an answer key. Writes report.md.

  NVIDIA_API_KEY=... GROQ_API_KEY=... python3 scripts/dash-test.py [model ...]
"""
import concurrent.futures as cf
import json
import os
import random
import re
import statistics
import sys
import time
import urllib.error
import urllib.request

PROVIDERS = {"nvidia": ("https://integrate.api.nvidia.com/v1/chat/completions", "NVIDIA_API_KEY"),
             "groq": ("https://api.groq.com/openai/v1/chat/completions", "GROQ_API_KEY")}
# deepseek-v4.1-flash and glm-5.3-flash are left out: they gave no answer to any
# request on 15 Oct (this test, search-test and nvidia-check).
MODELS = ["groq:openai/gpt-oss-120b", "groq:qwen/qwen3.8-27b", "nvidia:openai/gpt-oss-20b",
          "nvidia:nvidia/nemotron-3-ultra-550b-a55b", "nvidia:nvidia/nemotron-3-super-120b-a12b",
          "nvidia:google/gemma-4-31b-it"]
REPEATS = 2
KINDS = ["id", "money", "number", "percent", "date", "category", "text", "url", "email", "phone", "yes_no", "packed", "empty"]
VIEWS = ["kpi", "bar", "line", "donut", "table", "histogram", "top", "matrix"]
METRICS = ["count", "sum", "avg", "median", "blank_share", "share", "ratio"]
NUMERIC = {"money", "number", "percent"}
R = random.Random(7)


def inr(v):
    """1234567 -> ₹12,34,567 (Indian grouping)."""
    s = str(int(round(abs(v))))
    head, tail = s[:-3], s[-3:]
    parts = []
    while len(head) > 2:
        parts.insert(0, head[-2:])
        head = head[:-2]
    if head:
        parts.insert(0, head)
    return "₹" + ",".join(parts + [tail]) if parts else "₹" + tail


def grp(v):
    return f"{v:,.0f}"


# ---------- the five files ----------

SINGULAR = {"Apparel": "Shirt", "Equipment": "Scent Unit", "Gift Cards": "Gift Card", "Accessories": "Strap"}


def feed():
    cats = [("Apparel", 40), ("Equipment", 58), ("Gift Cards", 10), ("Accessories", 12)]
    header = ["id", "title", "description", "brand", "color", "size", "gender", "age_group", "product type",
              "price", "sale price", "availability", "gtin", "identifier exists", "additional image link",
              "shipping(country:price:service)", "Unnamed: 16", "link"]
    rows, n = [header], 0
    gtin_gaps = {"Apparel": 5, "Equipment": 9, "Gift Cards": 10, "Accessories": 3}
    for cat, count in cats:
        for i in range(count):
            n += 1
            app = cat == "Apparel"
            price = {"Apparel": R.choice([24.99, 29.99, 49.99]), "Equipment": R.choice([149.0, 299.99, 449.95, 699.99]),
                     "Gift Cards": R.choice([25.0, 50.0, 100.0]), "Accessories": R.choice([7.99, 12.99, 19.99])}[cat]
            sale = f"{round(price * R.choice([0.7, 0.8, 0.8, 0.9]), 2)} USD" if R.random() < 0.6 and cat != "Gift Cards" else ""
            rows.append([
                f"pc-{1000 + n}", f"Pinecrest {SINGULAR[cat]} {n}", "Rugged gear for the trail. " * R.randint(3, 8),
                "Pinecrest", (R.choice(["Black", "Olive", "Tan"]) if not (app and i < 6) else "") if app else "",
                (R.choice(["S", "M", "L", "XL"]) if i >= 3 else "") if app else "",
                (R.choice(["male", "unisex"]) if i >= 4 else "") if app else "", ("adult" if i >= 4 else "") if app else "",
                f"Outdoors > {cat}", f"{price} USD", sale, "in stock" if R.random() < 0.8 else "out of stock",
                "" if i < gtin_gaps[cat] else str(R.randint(10**11, 10**12 - 1)), "yes" if i >= gtin_gaps[cat] else "",
                f"https://cdn.example.com/pc-{n}-2.jpg" if R.random() < 0.6 else "",
                "US:0.00 USD:Free Shipping" if R.random() < 0.7 else "US::", "", f"https://pinecrest.example/p/{n}"])
    data = [dict(zip(header, r)) for r in rows[1:]]
    blank = lambda col, rs: sum(1 for r in rs if not r[col])  # noqa: E731
    facts = [f"Products: {len(data)}."]
    for cat, count in cats:
        rs = [r for r in data if r["product type"].endswith(cat)]
        facts.append(f"{cat}: {count} products; in stock {sum(r['availability'] == 'in stock' for r in rs)}; "
                     + "; ".join(f"{c} blank {blank(c, rs)}" for c in ["color", "size", "gender", "gtin", "additional image link"]))
    facts.append("Blank across all products: " + "; ".join(f"{c} {blank(c, data)}" for c in ["color", "size", "gtin", "additional image link", "sale price"]))
    facts.append(f"Out of stock: {sum(r['availability'] != 'in stock' for r in data)}.")
    facts.append(f"Shipping column has no price on {sum(1 for r in data if r['shipping(country:price:service)'] == 'US::')} products.")
    non = [r for r in data if "Apparel" not in r["product type"]]
    loud = sorted({blank(c, rs) for c in ("color", "size", "gender") for rs in (data, non)})  # blanks that are fine off apparel
    return {
        "name": "Product feed (Google Shopping)", "file": "pinecrest_feed.xlsx", "rows": rows,
        "goal": "Find what I should fix in my Google Shopping product feed.",
        "key": {"header_row": 1, "total_row": False, "wide": False, "currency": "USD",
                "cols": {"id": ["id"], "price": ["money"], "sale price": ["money"], "availability": ["category", "yes_no"],
                         "gtin": ["id"], "shipping(country:price:service)": ["packed"], "Unnamed: 16": ["empty"],
                         "additional image link": ["url"], "product type": ["category"]}},
        "facts": facts,
        "must": [("names GTIN / product codes", r"gtin|product code|identifier"),
                 ("names extra images", r"image"),
                 ("names out of stock", r"out of stock|in stock")],
        "traps": [("treats blank colour, size or gender off apparel as a problem (only apparel needs them)",
                   rf"(colou?r|size|gender)[^.\n]{{0,80}}\b({'|'.join(map(str, loud))})\b|\b({'|'.join(map(str, loud))})\b[^.\n]{{0,80}}(colou?r|size|gender)")],
        "plan_must": [("a view of GTIN blanks", lambda v: v.get("column") == "gtin" or v.get("by") == "gtin"),
                      ("a view by product type", lambda v: v.get("by") == "product type"),
                      ("a view of availability", lambda v: "availability" in (v.get("by"), v.get("column")))],
    }


def sales():
    header = ["Order ID", "Order Date", "Customer", "City", "Channel", "Amount", "Payment"]
    rows = [["Sales Report - Kesar Naturals", "", "", "", "", "", ""], ["Generated 01-Oct-2026", "", "", "", "", "", ""], header]
    months = [4, 5, 6, 7, 8, 9]
    data, oid = [], 5000
    cities = ["Mumbai", "mumbai ", "MUMBAI", "Pune", "Delhi", "Bengaluru", "Bangalore", "Chennai"]
    for m in months:
        for ch, base in [("Website", 18), ("Amazon", 14), ("Flipkart", 8)]:
            k = base if not (ch == "Amazon" and m == 9) else 3
            if ch == "Flipkart" and m == 9:
                k = 9
            for _ in range(k):
                oid += 1
                amt = R.randint(6, 250) * 100
                if R.random() < 0.05:
                    amt = -amt
                d = R.randint(1, 28)
                date = f"{d:02d}/{m:02d}/2026" if R.random() < 0.8 else f"2026-{m:02d}-{d:02d}"
                data.append((m, ch, amt))
                rows.append([f"KN-{oid}", date, f"Customer {oid}", R.choice(cities), ch,
                             f"({inr(-amt)[1:]})" if amt < 0 else inr(amt), R.choice(["UPI", "Card", "COD"])])
    rows.append(["Total", "", "", "", "", inr(sum(a for _, _, a in data)), ""])
    name = {4: "April", 5: "May", 6: "June", 7: "July", 8: "August", 9: "September"}
    facts = []
    for m in months:
        parts = []
        for ch in ["Website", "Amazon", "Flipkart"]:
            rs = [a for mm, c, a in data if mm == m and c == ch]
            parts.append(f"{ch} {len(rs)} orders, {inr(sum(rs))} net")
        tot = [a for mm, _, a in data if mm == m]
        facts.append(f"{name[m]} 2026: {len(tot)} orders, {inr(sum(tot))} net. " + "; ".join(parts) + ".")
    aug = {ch: sum(a for mm, c, a in data if mm == 8 and c == ch) for ch in ["Website", "Amazon", "Flipkart"]}
    sep = {ch: sum(a for mm, c, a in data if mm == 9 and c == ch) for ch in ["Website", "Amazon", "Flipkart"]}
    facts.append("Change, September vs August: " + "; ".join(f"{ch} {round((sep[ch] - aug[ch]) / aug[ch] * 100)}%" for ch in aug) + ".")
    facts.append(f"Returns (negative amounts): {sum(1 for d in data if d[2] < 0)} orders.")
    return {
        "name": "Sales orders (India)", "file": "kesar_sales.xlsx", "rows": rows,
        "goal": "Why did my sales drop in September?",
        "key": {"header_row": 3, "total_row": True, "wide": False, "currency": "INR",
                "cols": {"Order ID": ["id"], "Order Date": ["date"], "Amount": ["money"], "City": ["category"],
                         "Channel": ["category"], "Customer": ["text", "category"]}},
        "facts": facts,
        "must": [("names Amazon as the cause", r"amazon")],
        "traps": [("invents a cause not in the numbers (festival, season, weather)", r"diwali|festiv|monsoon|season|weather|holiday")],
        "plan_must": [("sales over time", lambda v: v.get("type") == "line" and v.get("by") == "Order Date"),
                      ("sales by channel", lambda v: v.get("by") == "Channel" or v.get("split") == "Channel")],
    }


def leads():
    header = ["Lead ID", "Name", "Email", "Phone", "Source", "Status", "Deal Value (INR)", "Created", "Last Contact", "Owner"]
    rows = [header]
    statuses = ["New"] * 30 + ["Contacted"] * 40 + ["Qualified"] * 30 + ["Proposal"] * 20 + ["Won"] * 15 + ["Lost"] * 15
    R.shuffle(statuses)
    data = []
    emails = [f"lead{i}@example.com" for i in range(138)]
    emails += R.sample(emails, 12)
    for i, st in enumerate(statuses):
        stale = R.randint(1, 60)
        val = "" if R.random() < 0.4 else str(R.randint(2, 80) * 5000)
        data.append({"status": st, "stale": stale, "val": val, "email": emails[i]})
        rows.append([f"L-{2000 + i}", f"Lead {i}", emails[i], str(R.randint(7000000000, 9999999999)),
                     R.choice(["Website", "Referral", "LinkedIn", "Trade show"]), st, val,
                     f"2026-{R.randint(5, 9):02d}-{R.randint(1, 28):02d}", f"{stale} days ago" if R.random() < 0.1 else
                     time.strftime("%Y-%m-%d", time.gmtime(time.mktime((2026, 10, 15, 12, 0, 0, 0, 0, 0)) - stale * 86400)), R.choice(["Asha", "Ravi", "Meera"])])
    hot = [d for d in data if d["status"] in ("Qualified", "Proposal") and d["stale"] >= 14]
    hot_val = sum(int(d["val"]) for d in hot if d["val"])
    facts = ["Leads: 150. By status: " + ", ".join(f"{s} {statuses.count(s)}" for s in ["New", "Contacted", "Qualified", "Proposal", "Won", "Lost"]) + ".",
             f"Qualified or Proposal leads not contacted for 14 days or more: {len(hot)}, with known deal value {inr(hot_val)}.",
             f"Deal value blank on {sum(1 for d in data if not d['val'])} leads.",
             f"Duplicate emails: {len(data) - len({d['email'] for d in data})} leads share an email with another lead.",
             f"New leads never contacted: {statuses.count('New')}."]
    return {
        "name": "CRM leads", "file": "leads_export.csv", "rows": rows,
        "goal": "Which leads should my sales team call first this week?",
        "key": {"header_row": 1, "total_row": False, "wide": False, "currency": "INR",
                "cols": {"Lead ID": ["id"], "Email": ["email"], "Phone": ["phone", "id"], "Status": ["category"],
                         "Deal Value (INR)": ["money"], "Last Contact": ["date"], "Created": ["date"]}},
        "facts": facts,
        "must": [("puts stale Qualified/Proposal leads first", r"qualified|proposal"),
                 ("gives how many", rf"\b{len(hot)}\b"),
                 ("flags duplicate emails", r"duplicate")],
        "traps": [("tells the team to call Lost leads first", r"\blost\b[^.\n]{0,50}(first|priorit)")],
        "plan_must": [("a view by status", lambda v: v.get("by") == "Status"),
                      ("a view of last contact", lambda v: "Last Contact" in (v.get("by"), v.get("column")))],
    }


def ads():
    months = ["Jul", "Aug", "Sep"]
    header = ["Campaign"] + [f"{m} {x}" for m in months for x in ["Spend", "Clicks", "Conv"]]
    rows = [header]
    camps = {"Brand Search": (90000, 3000, 300), "Generic - Shoes": (60000, 5200, 90), "Generic - Bags": (40000, 2100, 50),
             "Shopping - All": (55000, 2600, 110), "Display - Remarketing": (45000, 1900, 0), "Video - Awareness": (38000, 800, 0),
             "Competitor Terms": (30000, 900, 18), "Search - Sale": (25000, 1500, 40), "Pmax - Main": (70000, 2800, 120),
             "Search - Kids": (12000, 400, 9), "Display - Prospecting": (20000, 1100, 6), "Search - Gifts": (15000, 700, 21)}
    tot = {}
    for c, (s, k, v) in camps.items():
        row, t = [c], [0, 0, 0]
        for _ in months:
            ms, mk, mv = int(s * R.uniform(0.8, 1.2) / 3), int(k * R.uniform(0.8, 1.2) / 3), int(round(v * R.uniform(0.8, 1.2) / 3))
            row += [grp(ms), str(mk), str(mv)]
            t = [t[0] + ms, t[1] + mk, t[2] + mv]
        rows.append(row)
        tot[c] = t
    facts = [f"{c}: spend {inr(s)}, clicks {k}, conversions {v}, cost per conversion "
             + (inr(s / v) if v else "none (no conversions)") + "." for c, (s, k, v) in sorted(tot.items(), key=lambda x: -x[1][0])]
    facts.append(f"All campaigns: spend {inr(sum(t[0] for t in tot.values()))}, conversions {sum(t[2] for t in tot.values())}, "
                 f"cost per conversion {inr(sum(t[0] for t in tot.values()) / sum(t[2] for t in tot.values()))}.")
    return {
        "name": "Ad report (months across)", "file": "ads_q3.xlsx", "rows": rows,
        "goal": "Which of my ad campaigns are wasting money?",
        "key": {"header_row": 1, "total_row": False, "wide": True, "currency": None,
                "cols": {"Campaign": ["category", "text"], "Jul Spend": ["money"], "Jul Clicks": ["number"], "Jul Conv": ["number"]}},
        "facts": facts,
        "must": [("names Display - Remarketing", r"remarketing"), ("names Video - Awareness", r"video")],
        "traps": [("calls Brand Search waste (it has the best cost per conversion)", r"brand search[^.\n]{0,80}(wast|pause|cut|stop)")],
        "plan_must": [("a view by campaign", lambda v: v.get("by") == "Campaign")],
    }


def inventory():
    header = ["SKU", "Item", "Category", "Qty on Hand", "Reorder Level", "Avg Daily Sales", "Unit Cost", "Lead Time (days)", "Last Restock"]
    rows = [["Inventory snapshot 14-Oct-2026", "", "", "", "", "", "", "", ""], header]
    items, risk, dead = [], [], []
    for i in range(60):
        name = f"{R.choice(['Steel', 'Cotton', 'Bamboo', 'Glass', 'Clay'])} {R.choice(['Bottle', 'Towel', 'Mug', 'Jar', 'Tray', 'Bowl'])} {i + 1}"
        daily = round(R.uniform(0.5, 12), 1)
        qty = R.randint(0, 400)
        lead = R.choice([7, 10, 14, 21])
        if i in (11, 37):
            daily, qty = 0.0, 0
            dead.append(name)
        items.append((name, qty, daily, lead))
        rows.append([str(8901234000000 + i * 7919), name, R.choice(["Kitchen", "Bath", "Decor"]), str(qty), str(R.randint(20, 80)),
                     str(daily), f"{R.randint(80, 2400):,}.00", str(lead), f"2026-{R.randint(6, 10):02d}-{R.randint(1, 28):02d}"])
        if daily and qty / daily < lead:
            risk.append((qty / daily, name, qty, daily, lead))
    risk.sort()
    facts = [f"Items: 60. Items that will run out before a new order can arrive (days of stock left below lead time): {len(risk)}.",
             "Most urgent: " + "; ".join(f"{n} ({round(d, 1)} days left, lead time {lt} days)" for d, n, _, _, lt in risk[:5]) + ".",
             f"Items with no stock and no sales: {len(dead)} ({', '.join(dead)})."]
    return {
        "name": "Inventory", "file": "inventory.xlsx", "rows": rows,
        "goal": "What will run out soon, and what should I reorder?",
        "key": {"header_row": 2, "total_row": False, "wide": False, "currency": None,
                "cols": {"SKU": ["id"], "Qty on Hand": ["number"], "Unit Cost": ["money"], "Last Restock": ["date"],
                         "Lead Time (days)": ["number"], "Avg Daily Sales": ["number"]}},
        "facts": facts,
        "must": [("gives how many will run out", rf"\b{len(risk)}\b"), ("names the most urgent item", re.escape(risk[0][1]))],
        "traps": [("tells them to reorder items with no sales", "(" + "|".join(map(re.escape, dead)) + r")[^.\n]{0,80}reorder|reorder[^.\n]{0,80}(" + "|".join(map(re.escape, dead)) + ")")],
        "plan_must": [("a view of stock on hand", lambda v: "Qty on Hand" in (v.get("column"), v.get("by")) or "Qty on Hand" in (v.get("columns") or []))],
    }


FILES = [feed(), sales(), leads(), ads(), inventory()]


# ---------- prompts ----------

def grid(rows):
    lines = [f"row {i + 1}: " + " | ".join(c[:40] for c in r) for i, r in enumerate(rows)]
    return "\n".join(lines[:16] + (["..."] if len(lines) > 19 else []) + lines[-3:] if len(lines) > 19 else lines)


def prompt_read(f):
    return (f"A user gave us the file {f['file']}. Below are its first rows and its last 3 rows, cells split by |.\n\n{grid(f['rows'])}\n\n"
            "Reply with JSON only, no other text:\n"
            '{"header_row": <row number of the column names>, "total_row": <true if the last row is a total or summary>, '
            '"wide": <true if months or periods run across as columns>, "currency": <"USD", "INR", other code, or null>, '
            '"columns": [{"name": <column name>, "kind": <one of ' + ", ".join(KINDS) + '>, "meaning": <a few words>}]}')


def columns_of(f):
    key = f["key"]
    names = f["rows"][key["header_row"] - 1]
    out = []
    for n in names:
        sample = [r[names.index(n)] for r in f["rows"][key["header_row"]:key["header_row"] + 4]]
        out.append(f"{n} (e.g. {', '.join(s[:24] for s in sample if s) or 'blank'})")
    return out


def prompt_plan(f):
    return (f"Plan a dashboard for this goal: \"{f['goal']}\"\nThe file has {len(f['rows'])} rows. Columns:\n- "
            + "\n- ".join(columns_of(f)) +
            "\n\nUse only these views: " + ", ".join(VIEWS) + ". Metrics: " + ", ".join(METRICS) +
            " (blank_share is the share of empty cells; ratio divides column by column2)."
            "\nReply with JSON only, no other text:\n"
            '{"tabs": [{"title": <tab name>, "views": [{"type": <view>, "metric": <metric>, "column": <column or null>, '
            '"column2": <column for ratio or null>, "by": <column to group or plot by, or null>, "split": <column or null>, "title": <chart title>}]}]}\n'
            "Use 2 to 5 tabs. Use the column names exactly as written.")


def prompt_insight(f):
    return (f"The user's goal: \"{f['goal']}\"\nOur code computed these facts from the file {f['file']}:\n- " + "\n- ".join(f["facts"]) +
            "\n\nWrite the 3 to 5 findings that matter most for the goal, most important first, each with a short next step. "
            "Use only numbers from the facts. Plain short sentences.")


# ---------- scoring ----------

def parse_json(text):
    text = re.sub(r"^.*?</think>", "", text, flags=re.S)
    m = re.search(r"\{.*\}", text, re.S)
    if not m:
        return None
    try:
        return json.loads(m.group(0))
    except ValueError:
        return None


def score_read(f, text):
    j, key, notes = parse_json(text), f["key"], []
    if not isinstance(j, dict):
        return 0.0, ["no JSON"]
    got = total = 0
    for k in ["header_row", "total_row", "wide"]:
        total += 1
        if j.get(k) == key[k]:
            got += 1
        else:
            notes.append(f"{k} {j.get(k)!r}, want {key[k]!r}")
    total += 1
    if (j.get("currency") or None) == key["currency"] or (key["currency"] is None and str(j.get("currency")).lower() in ("none", "null", "")):
        got += 1
    else:
        notes.append(f"currency {j.get('currency')!r}, want {key['currency']!r}")
    cols = {str(c.get("name", "")).strip(): str(c.get("kind", "")) for c in j.get("columns") or [] if isinstance(c, dict)}
    for name, ok in key["cols"].items():
        total += 1
        if cols.get(name) in ok:
            got += 1
        else:
            notes.append(f"{name}: {cols.get(name) or 'missing'}, want {'/'.join(ok)}")
    return got / total, notes


def score_plan(f, text):
    j = parse_json(text)
    if not isinstance(j, dict) or not isinstance(j.get("tabs"), list):
        return 0.0, ["no JSON plan"]
    names = set(f["rows"][f["key"]["header_row"] - 1])
    kinds = f["key"]["cols"]
    views = [v for t in j["tabs"] if isinstance(t, dict) for v in t.get("views") or [] if isinstance(v, dict)]
    if not views:
        return 0.0, ["no views"]
    notes, bad = [], 0
    for v in views:
        for k in ("column", "column2", "by", "split"):  # code would match "Deal Value" to "Deal Value (INR)"
            if v.get(k) and v[k] not in names:
                v[k] = next((n for n in names if re.sub(r"\s*\(.*\)$", "", n).lower() == str(v[k]).strip().lower()), v[k])
        used = [v.get(k) for k in ("column", "column2", "by", "split") if v.get(k)]
        wrong = [u for u in used if u not in names]
        if wrong or v.get("type") not in VIEWS or v.get("metric") not in METRICS:
            bad += 1
            notes.append(f"bad view {v.get('type')}/{v.get('metric')} {wrong or ''}".strip())
        elif v.get("metric") in ("sum", "avg", "median") and v.get("column") in kinds and not set(kinds[v["column"]]) & NUMERIC:
            bad += 1
            notes.append(f"{v['metric']} of {v['column']} makes no sense")
    musts = [label for label, test in f["plan_must"] if not any(test(v) for v in views)]
    notes += [f"missing: {m}" for m in musts]
    valid = 1 - bad / len(views)
    return round(0.5 * valid + 0.5 * (1 - len(musts) / len(f["plan_must"])), 3), notes


def numbers(text):
    text = re.sub(r"(?m)^\s*(\d+)[.)]\s", " ", text)  # list numbers
    return {n.replace(",", "") for n in re.findall(r"\d[\d,]*(?:\.\d+)?", text)}


def score_insight(f, text, prompt):
    text = re.sub(r"^.*?</think>", "", text, flags=re.S)
    if not text.strip():
        return 0.0, ["empty"]
    low, notes = text.lower(), []
    allowed = numbers(prompt)
    allowed |= {str(i) for i in range(0, 11)} | {"14", "2026"}
    nums = [float(a) for a in allowed if len(allowed) < 400]
    allowed |= {f"{x + y:g}" for x in nums for y in nums} | {f"{abs(x - y):g}" for x in nums for y in nums}  # sums and gaps are fine
    made_up = sorted(n for n in numbers(text) - allowed if not any(n == a.rstrip("0").rstrip(".") for a in allowed))
    if made_up:
        notes.append("numbers not in the facts: " + ", ".join(made_up[:6]))
    hits = [label for label, rx in f["must"] if re.search(rx, low, re.I)]
    notes += [f"missed: {label}" for label, rx in f["must"] if label not in hits]
    traps = [label for label, rx in f["traps"] if re.search(rx, low, re.I)]
    notes += [f"TRAP: {t}" for t in traps]
    s = len(hits) / len(f["must"]) - 0.5 * len(traps) - (0.25 if made_up else 0)
    return max(0.0, round(s, 3)), notes


# ---------- running ----------

def ask(model, prompt):
    prov, name = model.split(":", 1)
    url, env = PROVIDERS[prov]
    body = {"model": name, "messages": [{"role": "user", "content": prompt}], "max_tokens": 6000, "temperature": 0.2}
    err = ""
    for attempt in range(6):
        try:
            req = urllib.request.Request(url, json.dumps(body).encode(), {"content-type": "application/json",
                                         "authorization": "Bearer " + os.environ[env], "user-agent": "mangoman-dash-test"})
            with urllib.request.urlopen(req, timeout=120) as r:
                msg = json.loads(r.read())["choices"][0]["message"]
                return (msg.get("content") or "").strip(), ""
        except urllib.error.HTTPError as e:
            err = f"HTTP {e.code}"
            time.sleep(20 * (attempt + 1) if e.code == 429 else 8)
        except Exception as e:  # noqa: BLE001 - every failure is a result here
            err = type(e).__name__ + ": " + str(e)[:100]
            time.sleep(8)
    return "", err


def run(job):
    model, f, task, rep = job
    prompt = {"read": prompt_read, "plan": prompt_plan, "insight": prompt_insight}[task](f)
    start = time.time()
    text, err = ask(model, prompt)
    secs = time.time() - start
    if err and not text:
        return model, f["name"], task, rep, 0.0, [err], secs, ""
    s, notes = {"read": lambda: score_read(f, text), "plan": lambda: score_plan(f, text),
                "insight": lambda: score_insight(f, text, prompt)}[task]()
    return model, f["name"], task, rep, s, notes, secs, text


def write(results, total, models):
    out = [f"# Dashboard AI test, {time.strftime('%d %B %Y')}", "",
           f"{len(FILES)} made-up files, 3 jobs each, {REPEATS} tries per model. {len(results)} of {total} answers in.", "",
           "Scores are 0 to 100. read: header, totals row, months across, currency, column kinds. "
           "plan: valid charts on real columns, plus the charts the goal needs. "
           "insight: findings the goal needs, no traps, no made-up numbers.", "",
           "## 1. By model", "", "| Model | Read | Plan | Insight | Overall | Failed | Traps hit | Median seconds |",
           "| --- | --- | --- | --- | --- | --- | --- | --- |"]
    for m in models:
        rs = [r for r in results if r[0] == m]
        cell = lambda t: (f"{statistics.mean(r[4] for r in rs if r[2] == t) * 100:.0f}" if any(r[2] == t for r in rs) else "")  # noqa: E731
        failed = sum(1 for r in rs if not r[7])
        traps = sum(1 for r in rs for n in r[5] if n.startswith("TRAP"))
        secs = sorted(r[6] for r in rs if r[7])
        out.append(f"| {m.split(':', 1)[1].split('/')[-1]} | {cell('read')} | {cell('plan')} | {cell('insight')} | "
                   f"{statistics.mean(r[4] for r in rs) * 100:.0f} | {failed} | {traps} | {secs[len(secs) // 2]:.0f} |" if rs and secs else
                   f"| {m} | | | | | {failed} | | |")
    out += ["", "## 2. By file (all models)", "", "| File | Read | Plan | Insight |", "| --- | --- | --- | --- |"]
    for f in FILES:
        rs = [r for r in results if r[1] == f["name"]]
        cell = lambda t: (f"{statistics.mean(r[4] for r in rs if r[2] == t) * 100:.0f}" if any(r[2] == t for r in rs) else "")  # noqa: E731
        out.append(f"| {f['name']} | {cell('read')} | {cell('plan')} | {cell('insight')} |")
    out += ["", "## 3. What went wrong", "", "| Model | File | Job | Try | Score | Notes |", "| --- | --- | --- | --- | --- | --- |"]
    for r in sorted(results, key=lambda r: (r[0], r[1], r[2], r[3])):
        if r[5]:
            out.append(f"| {r[0].split('/')[-1]} | {r[1]} | {r[2]} | {r[3]} | {r[4] * 100:.0f} | {'; '.join(r[5]).replace('|', '/')[:300]} |")
    out += ["", "## 4. Insight answers, first try", ""]
    for r in sorted(results, key=lambda r: (r[1], r[0])):
        if r[2] == "insight" and r[3] == 1:
            body = re.sub(r"^.*?</think>", "", r[7], flags=re.S).strip()
            out += [f"### {r[1]} · {r[0].split('/')[-1]} · {r[4] * 100:.0f}", "", body[:1800] or "(no answer)", ""]
    open("report.md", "w").write("\n".join(out) + "\n")


def main():
    models = sys.argv[1:] or MODELS
    jobs = [(m, f, t, rep) for rep in range(1, REPEATS + 1) for f in FILES for t in ("read", "plan", "insight") for m in models]
    results = []
    write(results, len(jobs), models)
    # One queue per provider so a slow or rate-limited provider does not hold up the other.
    pools = {p: cf.ThreadPoolExecutor(4 if p == "nvidia" else 2) for p in PROVIDERS}
    futures = [pools[j[0].split(":")[0]].submit(run, j) for j in jobs]
    for fut in cf.as_completed(futures):
        results.append(fut.result())
        write(results, len(jobs), models)
    print(open("report.md").read()[:4000])


if __name__ == "__main__":
    if "--show" in sys.argv:  # print the prompts and facts, no model calls
        for f in FILES:
            print("=" * 30, f["name"], "\n", prompt_read(f), "\n\n", prompt_plan(f), "\n\n", prompt_insight(f), "\n")
        sys.exit(0)
    main()
