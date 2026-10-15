#!/usr/bin/env python3
"""Chargesheets engine: reads the user's files, cleans them, and writes what
the dashboard and Ask use. Code reads and counts; no model sees the rows here.

  python3 engine.py load <dir>   reads the sources in <dir>/sheet.json from <dir>/files/,
                                 honours <dir>/choices.json, writes
                                 data.json (columns for the dashboard),
                                 profile.json (what was read, for the check
                                 screen and the models) and data.sqlite (Ask)
  python3 engine.py sql <dir>    runs one read-only query from stdin
                                 {"sql": "..."} and prints the rows as JSON

Only the standard library, plus openpyxl for Excel files.
"""
import csv
import datetime as dt
import io
import json
import os
import re
import sqlite3
import sys
import time

MAX_ROWS = 300_000  # per table: the dashboard counts in the browser
BLANKS = {"", "-", "--", "n/a", "#n/a", "null", "none", "nan", "#value!", "#ref!", "#div/0!", "#name?"}
TOTAL = re.compile(r"^(grand\s*|sub\s*)?totals?(\s*\(.*\))?\s*:?\s*$|^sum\s*:?$", re.I)
CURRENCIES = [("₹", "INR"), ("rs.", "INR"), ("rs", "INR"), ("inr", "INR"), ("us$", "USD"), ("$", "USD"), ("usd", "USD"),
              ("€", "EUR"), ("eur", "EUR"), ("£", "GBP"), ("gbp", "GBP"), ("aed", "AED"), ("sgd", "SGD"), ("aud", "AUD"),
              ("cad", "CAD"), ("¥", "JPY"), ("jpy", "JPY")]
SCALE = {"k": 1e3, "thousand": 1e3, "m": 1e6, "mn": 1e6, "million": 1e6, "l": 1e5, "lac": 1e5, "lacs": 1e5, "lakh": 1e5,
         "lakhs": 1e5, "cr": 1e7, "crore": 1e7, "crores": 1e7, "b": 1e9, "bn": 1e9, "billion": 1e9}
GROUPED = re.compile(r"^\d{1,3}(,\d{3})+$|^\d{1,2}(,\d{2})*,\d{3}$")  # 1,234,567 or 12,34,567
MONEY_WORDS = re.compile(r"price|amount|revenue|sales|cost|spend|value|budget|mrp|fee|salary|income|profit|"
                         r"paid|payment|balance|gst|tax|cpc|cpa|rupee|(^|[^a-z])(inr|usd|rs)([^a-z]|$)|\$|₹", re.I)
COUNT_WORDS = re.compile(r"qty|quantity|units|count|orders|clicks|impressions|conv|views|visits|leads|no\.? of|number of|items|stock|days", re.I)
ID_WORDS = re.compile(r"(^|[^a-z])(id|sku|gtin|ean|upc|isbn|asin|mpn|phone|mobile|zip|pin|pincode|postcode|postal\s*code)([^a-z]|$)|"
                      r"(order|invoice|bill|receipt|txn|transaction|reference|ref|account|acct|roll|serial|item|product|customer|lead)"
                      r"\s*(no\.?|number|#|id|code)([^a-z]|$)", re.I)
DATE_WORDS = re.compile(r"date|day|created|updated|time|dob|month|joined|since|when|period|week", re.I)
MONTHS = {m: i + 1 for i, m in enumerate(["jan", "feb", "mar", "apr", "may", "jun", "jul", "aug", "sep", "oct", "nov", "dec"])}
MONTHS["sept"] = 9
EPOCH = dt.date(1970, 1, 1)


class Bad(Exception):
    """A problem to show the user in plain words."""


# ---------- reading ----------

def cell(v):
    if isinstance(v, str):
        v = v.replace(" ", " ").strip()
        return None if v.lower() in BLANKS else v
    if isinstance(v, float) and v != v:
        return None
    return v


def decode(raw):
    for enc in ("utf-8-sig", "cp1252", "latin-1"):
        try:
            return raw.decode(enc)
        except UnicodeDecodeError:
            continue
    return raw.decode("utf-8", "replace")


def grids(path, label):
    """Yields (origin, rows) for every table in one file."""
    ext = os.path.splitext(path)[1].lower()
    if ext in (".xlsx", ".xlsm"):
        try:
            import openpyxl
        except ImportError:
            raise Bad("Excel files need MangoMan's Python: click Get ready on the setup page, then try again.")
        try:
            wb = openpyxl.load_workbook(path, read_only=True, data_only=True)
        except Exception as e:  # noqa: BLE001 - a broken file is the user's message
            raise Bad(f"{label} could not be opened as an Excel file ({type(e).__name__}). Save it again from Excel and retry.")
        sheets = [ws for ws in wb.worksheets if getattr(ws, "sheet_state", "visible") == "visible"]
        for ws in sheets:
            rows = []
            for r in ws.iter_rows(values_only=True):
                rows.append([cell(v) for v in r])
                if len(rows) > MAX_ROWS + 100:
                    break
            yield (f"{label} · {ws.title}" if len(sheets) > 1 else label), rows
        wb.close()
    elif ext in (".csv", ".tsv", ".txt"):
        text = decode(open(path, "rb").read())
        head = text[:65536]
        delim = "\t" if ext == ".tsv" else max([",", ";", "\t", "|"], key=lambda d: head.count(d))
        rows = []
        for r in csv.reader(io.StringIO(text), delimiter=delim):
            rows.append([cell(v) for v in r])
            if len(rows) > MAX_ROWS + 100:
                break
        yield label, rows
    elif ext == ".xls":
        raise Bad(f"{label} is an old Excel file (.xls). Open it in Excel or Google Sheets and save it as .xlsx.")
    else:
        raise Bad(f"{label}: Chargesheets reads .xlsx, .csv and .tsv files.")


def filled(r):
    return sum(1 for c in r if c is not None)


def looks_number(v):
    return isinstance(v, (int, float)) and not isinstance(v, bool) or (isinstance(v, str) and num(v) is not None)


def norm(s):
    return re.sub(r"[^0-9a-z]+", "", str(s).casefold())


def find_header(rows, notes):
    """The column names' row: the first wide row of distinct words."""
    top = rows[:40]
    width = max((filled(r) for r in top), default=0)
    for i, r in enumerate(top):
        f = [c for c in r if c is not None]
        if width == 0 or len(f) < max(2, 0.6 * width):
            continue
        words = [c for c in f if isinstance(c, str) and (not looks_number(c) or re.fullmatch(r"(19|20)\d\d", c.strip()))]
        if len(words) >= 0.8 * len(f):
            if i:
                notes.append(f"Skipped {i} title row{'s' if i > 1 else ''} above the column names.")
            return i
    notes.append("No row of column names found: columns are numbered.")
    return None


def table(origin, rows, header=None):
    """One grid to (names, data rows, notes)."""
    notes = []
    rows = [r for r in rows if r is not None]
    while rows and filled(rows[-1]) == 0:
        rows.pop()
    if not rows:
        return None
    if header is None:
        h = find_header(rows, notes)
    else:
        h = max(0, header - 1)
        notes.append(f"Column names taken from row {header}, as you chose.")
    width = max(len(r) for r in rows)
    rows = [list(r) + [None] * (width - len(r)) for r in rows]
    if h is None:
        names, body = [f"Column {i + 1}" for i in range(width)], rows
    else:
        names, body = [str(c) if c is not None else "" for c in rows[h]], rows[h + 1:]
        above = rows[h - 1] if h > 0 else None
        # Two header rows ("Jul" over "Spend, Clicks, Conv"): join them.
        if above and len({norm(n) for n in names if n}) < len([n for n in names if n]) and filled(above) >= 2:
            last, joined = None, []
            for i, n in enumerate(names):
                last = above[i] if above[i] is not None else last
                joined.append(f"{last} {n}".strip() if last is not None and n else (n or str(above[i] or "")))
            names = joined
            notes[:] = [n for n in notes if not n.startswith("Skipped")]
            if h > 1:
                notes.append(f"Skipped {h - 1} title row{'s' if h > 2 else ''} above the column names.")
            notes.append("Joined two rows of column names into one.")
    # Rows that repeat the header, totals and trailing notes.
    keyset = [norm(n) for n in names]
    totals = 0
    data = []
    for r in body:
        if filled(r) == 0 or [norm(c) if c is not None else "" for c in r] == keyset:
            continue
        first = next((c for c in r if c is not None), None)
        if isinstance(first, str) and TOTAL.match(first):
            totals += 1
            continue
        data.append(r)
    while data and width >= 3 and filled(data[-1]) <= 1:
        data.pop()
    if totals:
        notes.append(f"Left out {totals} total row{'s' if totals > 1 else ''} so nothing is counted twice.")
    if len(data) > MAX_ROWS:
        raise Bad(f"{origin} has more than {MAX_ROWS:,} rows. Chargesheets handles up to {MAX_ROWS:,} rows a table for now.")
    # Empty columns go; unnamed ones get a name; names stay unique.
    keep, seen, final = [], {}, []
    for i, n in enumerate(names):
        if all(r[i] is None for r in data):
            if n and not n.lower().startswith("unnamed"):
                notes.append(f"Column {n} is empty and was left out.")
            continue
        n = n or f"Column {i + 1}"
        k = norm(n)
        seen[k] = seen.get(k, 0) + 1
        final.append(n if seen[k] == 1 else f"{n} {seen[k]}")
        keep.append(i)
    return {"origin": origin, "names": final, "rows": [[r[i] for i in keep] for r in data], "notes": notes}


# ---------- values ----------

def num(v):
    """A number from a cell, with its currency and % if written: (value, currency, percent) or None."""
    if isinstance(v, bool):
        return None
    if isinstance(v, (int, float)):
        return float(v), None, False
    if not isinstance(v, str):
        return None
    t = v.strip().lower().replace("−", "-").replace(" ", " ")
    neg = False
    if t.startswith("(") and t.endswith(")"):
        neg, t = True, t[1:-1].strip()
    if t.startswith("-"):
        neg, t = not neg, t[1:].strip()
    elif t.startswith("+"):
        t = t[1:].strip()
    cur = None
    for sym, code in CURRENCIES:
        if t.startswith(sym):
            cur, t = code, t[len(sym):].strip(" .")
            break
        if t.endswith(sym) and (not sym.isalpha() or not t[: -len(sym)].strip().endswith(tuple("abcdefghijklmnopqrstuvwxyz"))):
            cur, t = code, t[: -len(sym)].strip()
            break
    if t.startswith("-"):
        neg, t = not neg, t[1:].strip()
    pct = t.endswith("%")
    if pct:
        t = t[:-1].strip()
    mult = 1.0
    m = re.match(r"^(.*?\d)\s*([a-z]+)$", t)
    if m and m.group(2) in SCALE:
        t, mult = m.group(1).strip(), SCALE[m.group(2)]
    whole, _, frac = t.partition(".")
    if "," in whole:
        if not GROUPED.match(whole):
            return None
        whole = whole.replace(",", "")
    if not whole.isdigit() and not (whole == "" and frac.isdigit()):
        return None
    if frac and not frac.isdigit():
        return None
    x = float((whole or "0") + ("." + frac if frac else "")) * mult
    return (-x if neg else x), cur, pct


def to_date(v, dayfirst):
    if isinstance(v, dt.datetime):
        return v.date()
    if isinstance(v, dt.date):
        return v
    if not isinstance(v, str):
        return None
    s = v.strip().lower().replace(",", " ")
    try:
        m = re.match(r"^(\d{4})[-/.](\d{1,2})[-/.](\d{1,2})([ t].*)?$", s)
        if m:
            return dt.date(int(m.group(1)), int(m.group(2)), int(m.group(3)))
        m = re.match(r"^(\d{1,2})[-/.](\d{1,2})[-/.](\d{2}|\d{4})([ t].*)?$", s)
        if m:
            a, b, y = int(m.group(1)), int(m.group(2)), int(m.group(3))
            y += 2000 if y < 100 else 0
            d, mo = (a, b) if dayfirst else (b, a)
            return dt.date(y, mo, d)
        m = re.match(r"^(\d{1,2})[- ]([a-z]{3,9})\.?[- ](\d{2}|\d{4})$", s)
        if m and m.group(2)[:3] in MONTHS:
            y = int(m.group(3))
            return dt.date(y + (2000 if y < 100 else 0), MONTHS[m.group(2)[:3]], int(m.group(1)))
        m = re.match(r"^([a-z]{3,9})\.?\s+(\d{1,2})\s+(\d{4})$", s)
        if m and m.group(1)[:3] in MONTHS:
            return dt.date(int(m.group(3)), MONTHS[m.group(1)[:3]], int(m.group(2)))
        m = re.match(r"^([a-z]{3,9})\.?[- '](\d{2}|\d{4})$", s)
        if m and m.group(1)[:3] in MONTHS:
            y = int(m.group(2))
            return dt.date(y + (2000 if y < 100 else 0), MONTHS[m.group(1)[:3]], 1)
    except ValueError:
        return None
    return None


def dayfirst_for(vals, currency):
    a = b = 0
    for v in vals:
        if isinstance(v, str):
            m = re.match(r"^\s*(\d{1,2})[-/.](\d{1,2})[-/.]", v)
            if m:
                a += int(m.group(1)) > 12
                b += int(m.group(2)) > 12
    if a != b:
        return a > b
    return currency != "USD"  # India and most of the world write the day first


# ---------- columns ----------

def detect(name, vals, rows, currency_hint):
    """kind, unit, parsed values and what could not be read, for one column."""
    present = [v for v in vals if v is not None]
    n = len(present)
    out = {"name": name, "kind": "empty", "unit": None, "values": [None] * len(vals), "unreadable": 0, "merged": 0}
    if n == 0:
        return out
    strs = [v for v in present if isinstance(v, str)]
    uniq = len(set(map(str, present)))
    idish = bool(ID_WORDS.search(name)) and not MONEY_WORDS.search(name) and not COUNT_WORDS.search(name)
    # Dates
    real_dates = sum(isinstance(v, (dt.date, dt.datetime)) for v in present)
    sample = present[:: max(1, n // 300)]
    if real_dates or (strs and DATE_WORDS.search(name)) or (len(strs) == n and sum(
            to_date(v, True) is not None or to_date(v, False) is not None for v in sample) >= 0.8 * len(sample)):
        first = dayfirst_for(strs[:2000], currency_hint)
        parsed = [to_date(v, first) for v in vals]
        good = sum(p is not None for p in parsed)
        serial = [v for v in present if isinstance(v, (int, float)) and not isinstance(v, bool)]
        if good < 0.8 * n and DATE_WORDS.search(name) and len(serial) >= 0.8 * n and all(20000 <= x <= 80000 for x in serial):
            parsed = [EPOCH + dt.timedelta(days=int(v) - 25569) if isinstance(v, (int, float)) and not isinstance(v, bool) else None for v in vals]
            good = sum(p is not None for p in parsed)
        if good >= 0.8 * n and good >= 1:
            out.update(kind="date", values=parsed, unreadable=n - good)
            return out
    # Numbers
    good = []
    if sum(num(v) is not None for v in sample) >= 0.9 * len(sample):
        parsed = [num(v) if v is not None else None for v in vals]
        good = [p for p in parsed if p is not None]
    if good and len(good) >= 0.9 * n:
        values = [p[0] if p else None for p in parsed]
        curs = [p[1] for p in good if p[1]]
        pcts = sum(p[2] for p in good)
        unreadable = n - len(good)
        ints = all(float(x).is_integer() for x in values if x is not None)
        digits = [len(str(int(abs(x)))) for x in values if x is not None and float(x).is_integer()]
        if idish or (ints and uniq >= 0.95 * n and n >= 20 and digits and min(digits) >= 6):
            out.update(kind="id", values=[fmt_id(v) for v in vals])
            return out
        if pcts >= 0.5 * len(good):
            out.update(kind="percent", unit="%", values=values, unreadable=unreadable)
            return out
        cur = max(set(curs), key=curs.count) if curs else currency_from_name(name)
        if cur or (MONEY_WORDS.search(name) and not COUNT_WORDS.search(name)):
            out.update(kind="money", unit=cur or currency_hint, values=values, unreadable=unreadable)
            return out
        out.update(kind="number", values=values, unreadable=unreadable)
        return out
    # Words
    text = [str(v) for v in present]
    low = [t.lower() for t in text]
    if all(re.match(r"^[^@\s]+@[^@\s]+\.[a-z]{2,}$", t) for t in low[:500]):
        out.update(kind="email", values=[str(v) if v is not None else None for v in vals])
        return out
    if sum(t.startswith(("http://", "https://", "www.")) for t in low) >= 0.9 * n:
        out.update(kind="url", values=[str(v) if v is not None else None for v in vals])
        return out
    if set(low) <= {"yes", "no", "true", "false", "y", "n"}:
        out.update(kind="yes_no", values=[None if v is None else str(v).lower() in ("yes", "true", "y") for v in vals])
        return out
    if idish or (uniq >= 0.95 * n and n >= 20 and all(re.match(r"^[\w\-/#.]{1,40}$", t) and re.search(r"\d", t) for t in text[:500])):
        out.update(kind="id", values=[fmt_id(v) for v in vals])
        return out
    # Categories: the same word written differently counts as one.
    groups = {}
    for t in text:
        k = re.sub(r"\s+", " ", t.strip()).casefold()
        groups.setdefault(k, {}).setdefault(t.strip(), 0)
        groups[k][t.strip()] += 1
    if len(groups) <= max(60, 0.2 * n) and sum(len(t) for t in text) / n <= 60:
        canon = {k: max(v, key=v.get) for k, v in groups.items()}
        merged = sum(len(v) - 1 for v in groups.values())
        out.update(kind="category", merged=merged,
                   values=[canon[re.sub(r"\s+", " ", str(v).strip()).casefold()] if v is not None else None for v in vals])
        return out
    out.update(kind="text", values=[str(v) if v is not None else None for v in vals])
    return out


def fmt_id(v):
    if v is None:
        return None
    if isinstance(v, float) and v.is_integer():
        return str(int(v))
    return str(v).strip()


def currency_from_name(name):
    low = name.lower()
    for sym, code in CURRENCIES:
        if len(sym) > 2 and re.search(r"(^|[^a-z])" + re.escape(sym) + r"([^a-z]|$)", low) or (len(sym) == 1 and sym in low):
            return code
    return None


def coerce(col, kind, vals, currency):
    """Reads a column again as the kind the user chose."""
    if kind == col["kind"]:
        return col
    out = dict(col, kind=kind, unreadable=0, merged=0)
    raw = vals
    if kind in ("number", "money", "percent"):
        p = [num(v) if v is not None else None for v in raw]
        out["values"] = [x[0] if x else None for x in p]
        out["unreadable"] = sum(1 for v, x in zip(raw, p) if v is not None and x is None)
        out["unit"] = "%" if kind == "percent" else ((col["unit"] or currency) if kind == "money" else None)
    elif kind == "date":
        first = dayfirst_for([v for v in raw if isinstance(v, str)][:2000], currency)
        out["values"] = [to_date(v, first) for v in raw]
        out["unreadable"] = sum(1 for v, x in zip(raw, out["values"]) if v is not None and x is None)
        out["unit"] = None
    else:
        out["values"] = [None if v is None else (fmt_id(v) if kind == "id" else str(v)) for v in raw]
        out["unit"] = None
    return out


# ---------- tables to datasets ----------

def build(d):
    sources = json.load(open(os.path.join(d, "sheet.json"), encoding="utf-8"))["sources"]
    choices = {}
    if os.path.exists(os.path.join(d, "choices.json")):
        choices = json.load(open(os.path.join(d, "choices.json"), encoding="utf-8"))
    tables = []
    for src in sources:
        path = os.path.join(d, "files", src["file"])
        for origin, rows in grids(path, src.get("label") or os.path.splitext(src["file"])[0]):
            t = table(origin, rows, (choices.get("header") or {}).get(origin))
            if t and t["rows"]:
                tables.append(t)
    if not tables:
        raise Bad("No table with data was found in what you added.")
    # Read each column once, with a currency hint from the whole table.
    for t in tables:
        hint = next((c for c in map(currency_from_name, t["names"]) if c), None)
        raw = list(zip(*t["rows"])) if t["rows"] else [[] for _ in t["names"]]
        if not hint:
            for col in raw:
                for v in col[:200]:
                    p = num(v) if isinstance(v, str) else None
                    if p and p[1]:
                        hint = p[1]
                        break
                if hint:
                    break
        t["currency"] = hint
        t["raw"] = [list(c) for c in raw]
        t["cols"] = [detect(n, list(c), len(t["rows"]), hint) for n, c in zip(t["names"], raw)]
    # Same columns in several tables (Jan, Feb, Mar): one table with a Source column.
    groups = []
    for t in tables:
        key = tuple(sorted(norm(n) for n in t["names"]))
        g = next((g for g in groups if g["key"] == key), None)
        if g and not choices.get("nostack"):
            g["tables"].append(t)
        else:
            groups.append({"key": key, "tables": [t]})
    datasets = []
    for g in groups:
        ts = g["tables"]
        if len(ts) == 1:
            ds = {"name": ts[0]["origin"], "origins": [ts[0]["origin"]], "notes": ts[0]["notes"], "cols": ts[0]["cols"],
                  "raw": ts[0]["raw"], "rows": len(ts[0]["rows"]), "currency": ts[0]["currency"]}
        else:
            base = ts[0]
            order = [norm(n) for n in base["names"]]
            cols, raws = [], []
            for i, name in enumerate(base["names"]):
                vals, raw = [], []
                kinds = set()
                for t in ts:
                    j = [norm(n) for n in t["names"]].index(order[i])
                    vals += t["cols"][j]["values"]
                    raw += t["raw"][j]
                    kinds.add(t["cols"][j]["kind"])
                c = dict(base["cols"][i], values=vals, unreadable=sum(t["cols"][[norm(n) for n in t["names"]].index(order[i])]["unreadable"] for t in ts))
                if len(kinds) > 1:  # read again together when the parts disagree
                    c = detect(name, raw, len(raw), base["currency"])
                cols.append(c)
                raws.append(raw)
            src = []
            for t in ts:
                src += [t["origin"]] * len(t["rows"])
            cols.append({"name": "Source", "kind": "category", "unit": None, "values": src, "unreadable": 0, "merged": 0})
            raws.append(src)
            notes = [f"Stacked {len(ts)} tables with the same columns ({', '.join(t['origin'] for t in ts)}); the Source column says where each row came from."]
            for t in ts:
                notes += [f"{t['origin']}: {x}" for x in t["notes"]]
            files = {t["origin"].split(" · ")[0] for t in ts}
            ds = {"name": files.pop() if len(files) == 1 else (" + ".join(t["origin"] for t in ts) if len(ts) <= 3 else f"{ts[0]['origin']} + {len(ts) - 1} more"),
                  "origins": [t["origin"] for t in ts], "notes": notes, "cols": cols, "raw": raws, "rows": sum(len(t["rows"]) for t in ts),
                  "currency": base["currency"]}
        datasets.append(ds)
    unique_names(datasets)
    # The user's corrections to column kinds.
    for ds in datasets:
        for i, c in enumerate(ds["cols"]):
            want = (choices.get("kinds") or {}).get(f"{ds['name']}/{c['name']}")
            if want and want != c["kind"]:
                ds["cols"][i] = coerce(c, want, ds["raw"][i], ds["currency"])
                ds["notes"].append(f"{c['name']} read as {want.replace('_', '/')}, as you chose.")
    links = link(datasets, set(choices.get("nolink") or []))
    return datasets, links


def unique_names(datasets):
    seen = {}
    for ds in datasets:
        k = norm(ds["name"])
        seen[k] = seen.get(k, 0) + 1
        if seen[k] > 1:
            ds["name"] = f"{ds['name']} {seen[k]}"


def link(datasets, refused):
    """Adds the columns of a lookup table (customers, products) to the big table
    that points at it, when the key is unique there and most values match."""
    links = []
    by_size = sorted(datasets, key=lambda ds: -ds["rows"])
    for big in by_size:
        for small in by_size:
            if small is big or small["rows"] > big["rows"] or small.get("joined_into"):
                continue
            best = None
            for i, a in enumerate(big["cols"]):
                if a["kind"] not in ("id", "category", "text", "email"):
                    continue
                for j, b in enumerate(small["cols"]):
                    if b["kind"] not in ("id", "category", "text", "email") or not related(a["name"], b["name"], small["name"]):
                        continue
                    keys = [key(v) for v in b["values"] if v is not None]
                    if not keys or len(set(keys)) < 0.99 * len(keys) or len(set(keys)) != len(keys):
                        continue
                    want = [key(v) for v in a["values"] if v is not None]
                    if not want:
                        continue
                    got = set(keys)
                    share = sum(1 for k in want if k in got) / len(want)
                    if share >= 0.8 and (best is None or share > best[2]):
                        best = (i, j, share)
            if not best:
                continue
            i, j, share = best
            name = f"{big['name']}.{big['cols'][i]['name']} > {small['name']}.{small['cols'][j]['name']}"
            info = {"id": name, "from": big["name"], "to": small["name"], "by": big["cols"][i]["name"], "key": small["cols"][j]["name"],
                    "matched": round(share * 100, 1), "rows": big["rows"], "on": name not in refused}
            links.append(info)
            if not info["on"]:
                continue
            index = {key(v): r for r, v in enumerate(small["cols"][j]["values"]) if v is not None}
            rows_of = [index.get(key(v)) if v is not None else None for v in big["cols"][i]["values"]]
            for k, c in enumerate(small["cols"]):
                if k == j:
                    continue
                big["cols"].append(dict(c, name=f"{small['name'].split(' · ')[-1]} · {c['name']}",
                                        values=[c["values"][r] if r is not None else None for r in rows_of]))
                big["raw"].append([small["raw"][k][r] if r is not None else None for r in rows_of])
            unmatched = sum(1 for v, r in zip(big["cols"][i]["values"], rows_of) if v is not None and r is None)
            big["notes"].append(f"Linked {small['name']} by {big['cols'][i]['name']}: {share * 100:.1f}% of rows matched"
                                + (f", {unmatched} did not." if unmatched else ".") + " Row count unchanged.")
            small["joined_into"] = big["name"]
    return links


def related(a, b, table):
    x, y, t = norm(a), norm(b), norm(table)
    if not x or not y:
        return False
    if x == y:
        return True
    singular = t[:-1] if t.endswith("s") else t
    if y in ("id", "code", "no", "number") and x in (singular + y, t + y, singular, t):
        return True
    return len(y) >= 4 and (x.endswith(y) or y.endswith(x)) and min(len(x), len(y)) >= 4


def key(v):
    if isinstance(v, float) and v.is_integer():
        v = int(v)
    return str(v).strip().casefold()


# ---------- writing ----------

def days(d):
    return (d - EPOCH).days if d else None


def stats(c):
    vals = [v for v in c["values"] if v is not None]
    s = {"blanks": len(c["values"]) - len(vals), "distinct": len(set(map(str, vals)))}
    if c["kind"] in ("number", "money", "percent") and vals:
        s.update(min=min(vals), max=max(vals), sum=round(sum(vals), 6), mean=round(sum(vals) / len(vals), 6))
    elif c["kind"] == "date" and vals:
        s.update(min=min(vals).isoformat(), max=max(vals).isoformat())
    elif c["kind"] in ("category", "yes_no"):
        counts = {}
        for v in vals:
            v = ("yes" if v else "no") if c["kind"] == "yes_no" else str(v)
            counts[v] = counts.get(v, 0) + 1
        s["top"] = sorted(counts.items(), key=lambda x: -x[1])[:8]
    if c["kind"] not in ("number", "money", "percent", "date"):
        ex = []
        for v in vals:
            if str(v) not in ex:
                ex.append(str(v)[:60])
            if len(ex) == 3:
                break
        s["examples"] = ex
    return s


def sql_name(name, used):
    base = re.sub(r"[^0-9a-z]+", "_", name.casefold()).strip("_")[:40] or "data"
    if base[0].isdigit():
        base = "t_" + base
    n, i = base, 2
    while n in used:
        n, i = f"{base}_{i}", i + 1
    used.add(n)
    return n


def write(d, datasets, links):
    tmp = os.path.join(d, "data.sqlite.tmp")
    if os.path.exists(tmp):
        os.remove(tmp)
    con = sqlite3.connect(tmp)
    used, out, prof = set(), [], []
    for ds in datasets:
        if ds.get("joined_into"):
            ds["notes"].append(f"Its columns are also in {ds['joined_into']}.")
        tname = sql_name(ds["name"], used)
        cols = ds["cols"]
        types = {"number": "REAL", "money": "REAL", "percent": "REAL"}
        con.execute(f'CREATE TABLE "{tname}" (' + ", ".join(f'"{c["name"]}" {types.get(c["kind"], "TEXT")}' for c in cols) + ")")
        conv = []
        for c in cols:
            if c["kind"] == "date":
                conv.append([v.isoformat() if v else None for v in c["values"]])
            elif c["kind"] == "yes_no":
                conv.append([None if v is None else ("yes" if v else "no") for v in c["values"]])
            else:
                conv.append(c["values"])
        con.executemany(f'INSERT INTO "{tname}" VALUES (' + ",".join("?" * len(cols)) + ")", zip(*conv))
        out.append({"name": ds["name"], "table": tname, "rows": ds["rows"], "columns": [encode(c) for c in cols]})
        prof.append({"name": ds["name"], "table": tname, "rows": ds["rows"], "origins": ds["origins"], "notes": ds["notes"],
                     "currency": ds["currency"], "joined_into": ds.get("joined_into"),
                     "columns": [dict({"name": c["name"], "kind": c["kind"], "unit": c["unit"], "unreadable": c["unreadable"],
                                       "merged": c["merged"]}, **stats(c)) for c in cols]})
    con.commit()
    con.close()
    os.replace(tmp, os.path.join(d, "data.sqlite"))
    stamp = dt.datetime.now(dt.timezone.utc).isoformat(timespec="seconds")
    dump(os.path.join(d, "data.json"), {"built": stamp, "datasets": out})
    dump(os.path.join(d, "profile.json"), {"built": stamp, "datasets": prof, "links": links})
    return prof


def encode(c):
    """Columns as the browser wants them: numbers as numbers, dates as day
    numbers, words as a list of distinct words and an index per row."""
    e = {"name": c["name"], "kind": c["kind"], "unit": c["unit"]}
    if c["kind"] in ("number", "money", "percent"):
        e["values"] = [None if v is None else (int(v) if float(v).is_integer() and abs(v) < 2**53 else round(v, 6)) for v in c["values"]]
    elif c["kind"] == "date":
        e["values"] = [days(v) for v in c["values"]]
    else:
        words, index, codes = [], {}, []
        for v in c["values"]:
            if v is None:
                codes.append(-1)
                continue
            v = ("yes" if v else "no") if c["kind"] == "yes_no" else str(v)
            if v not in index:
                index[v] = len(words)
                words.append(v)
            codes.append(index[v])
        e["words"], e["codes"] = words, codes
    return e


def dump(path, obj):
    tmp = path + ".tmp"
    with open(tmp, "w", encoding="utf-8") as f:
        json.dump(obj, f, ensure_ascii=False, separators=(",", ":"), default=str)
    os.replace(tmp, path)


# ---------- Ask: one read-only query ----------

ALLOWED = {sqlite3.SQLITE_SELECT, sqlite3.SQLITE_READ, sqlite3.SQLITE_FUNCTION, getattr(sqlite3, "SQLITE_RECURSIVE", 33)}


class Median:
    def __init__(self):
        self.v = []

    def step(self, x):
        if x is not None:
            self.v.append(x)

    def finalize(self):
        if not self.v:
            return None
        s, n = sorted(self.v), len(self.v)
        return s[n // 2] if n % 2 else (s[n // 2 - 1] + s[n // 2]) / 2


def query(d, text, limit=500):
    text = text.strip().rstrip(";")
    if not re.match(r"(?is)^\s*(with|select)\b", text):
        return {"error": "Only SELECT queries can run."}
    con = sqlite3.connect(f"file:{os.path.join(d, 'data.sqlite')}?mode=ro", uri=True)
    con.create_aggregate("median", 1, Median)
    con.set_authorizer(lambda action, *_: sqlite3.SQLITE_OK if action in ALLOWED else sqlite3.SQLITE_DENY)
    start = time.time()
    con.set_progress_handler(lambda: 1 if time.time() - start > 10 else 0, 20000)
    try:
        cur = con.execute(text)
        cols = [c[0] for c in cur.description or []]
        rows = cur.fetchmany(limit + 1)
    except sqlite3.Error as e:
        return {"error": str(e)}
    finally:
        con.close()
    more = len(rows) > limit
    return {"columns": cols, "rows": [[round(v, 6) if isinstance(v, float) else v for v in r] for r in rows[:limit]], "more": more}


def main():
    if len(sys.argv) != 3 or sys.argv[1] not in ("load", "sql"):
        print(__doc__, file=sys.stderr)
        sys.exit(2)
    d = sys.argv[2]
    try:
        if sys.argv[1] == "load":
            datasets, links = build(d)
            prof = write(d, datasets, links)
            print(json.dumps({"ok": True, "datasets": [{"name": p["name"], "rows": p["rows"]} for p in prof]}))
        else:
            print(json.dumps(query(d, json.load(sys.stdin).get("sql", "")), ensure_ascii=False, default=str))
    except Bad as e:
        print(json.dumps({"error": str(e)}))
        sys.exit(1)


if __name__ == "__main__":
    main()
