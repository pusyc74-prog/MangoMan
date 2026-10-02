"""Profile a data file: columns, types, gaps, ranges and likely problems.

Usage: python3 profile.py <data.csv|.tsv|.json|.xlsx> [--sheet NAME]
Prints a JSON report. Only column names, types and summary statistics are
printed, never full rows, so the report is safe to show a model.
"""
import csv
import json
import os
import re
import statistics
import sys
from datetime import datetime

DATE_FORMATS = ["%Y-%m-%d", "%d/%m/%Y", "%m/%d/%Y", "%d-%m-%Y", "%Y/%m/%d", "%d %b %Y", "%b %Y", "%Y-%m", "%d.%m.%Y",
                "%Y-%m-%d %H:%M:%S", "%Y-%m-%dT%H:%M:%S"]
NUM_RE = re.compile(r"^[\s₹$€£]*[-+]?[\d,]*\.?\d+\s*%?$")


def load(path, sheet=None):
    ext = os.path.splitext(path)[1].lower()
    if ext in (".xlsx", ".xlsm", ".xls"):
        try:
            import pandas as pd
            df = pd.read_excel(path, sheet_name=sheet or 0, dtype=str)
            return list(df.columns.astype(str)), df.fillna("").values.tolist()
        except ImportError:
            pass
        try:
            import openpyxl
            wb = openpyxl.load_workbook(path, read_only=True, data_only=True)
            ws = wb[sheet] if sheet else wb.worksheets[0]
            rows = [["" if c is None else str(c) for c in r] for r in ws.iter_rows(values_only=True)]
            return rows[0], rows[1:]
        except ImportError:
            sys.exit("Reading Excel needs pandas or openpyxl (pip install openpyxl), or save the sheet as CSV.")
    if ext == ".json":
        with open(path, encoding="utf-8") as f:
            data = json.load(f)
        if isinstance(data, dict):
            data = next((v for v in data.values() if isinstance(v, list)), [data])
        cols = list({k: 1 for r in data for k in r})
        return cols, [[("" if r.get(c) is None else str(r.get(c))) for c in cols] for r in data]
    with open(path, newline="", encoding="utf-8-sig") as f:
        sample = f.read(65536)
        f.seek(0)
        try:
            dialect = csv.Sniffer().sniff(sample, delimiters=",;\t|")
        except csv.Error:
            dialect = csv.excel_tab if ext == ".tsv" else csv.excel
        rows = list(csv.reader(f, dialect))
    return rows[0], rows[1:]


def as_number(s):
    s = s.strip()
    if not s or not NUM_RE.match(s):
        return None
    pct = s.endswith("%")
    v = float(re.sub(r"[^\d.\-+]", "", s))
    return v / 100 if pct else v


def as_date(s):
    s = s.strip()
    for f in DATE_FORMATS:
        try:
            return datetime.strptime(s, f)
        except ValueError:
            continue
    return None


def profile(cols, rows):
    n = len(rows)
    report = {"rows": n, "columns": [], "problems": []}
    seen, dups = set(), 0
    for r in rows:
        key = tuple(r)
        if key in seen:
            dups += 1
        seen.add(key)
    if dups:
        report["problems"].append("%d duplicate rows" % dups)
    for i, c in enumerate(cols):
        vals = [(r[i] if i < len(r) else "").strip() for r in rows]
        filled = [v for v in vals if v != ""]
        col = {"name": c, "missing_pct": round(100 * (n - len(filled)) / n, 1) if n else 0, "unique": len(set(filled))}
        nums = [as_number(v) for v in filled]
        dates = [as_date(v) for v in filled[:500]]
        if filled and sum(x is not None for x in nums) >= 0.95 * len(filled):
            xs = [x for x in nums if x is not None]
            col.update(type="number", min=min(xs), max=max(xs), mean=round(statistics.fmean(xs), 4),
                       median=statistics.median(xs), sum=round(sum(xs), 4))
            if any("%" in v for v in filled[:50]):
                col["note"] = "percent values, stored as fractions"
            bad = len(filled) - len(xs)
            if bad:
                report["problems"].append("%s: %d non-numeric values" % (c, bad))
            if len(xs) > 8:
                q = statistics.quantiles(xs, n=4)
                iqr = q[2] - q[0]
                outl = [x for x in xs if iqr and (x < q[0] - 3 * iqr or x > q[2] + 3 * iqr)]
                if outl:
                    col["extreme_values"] = len(outl)
        elif filled and sum(d is not None for d in dates) >= 0.9 * len(dates):
            ds = [as_date(v) for v in filled]
            ds = [d for d in ds if d]
            col.update(type="date", first=min(ds).date().isoformat(), last=max(ds).date().isoformat())
        elif filled and col["unique"] <= max(30, 0.2 * len(filled)):
            counts = {}
            for v in filled:
                counts[v] = counts.get(v, 0) + 1
            top = sorted(counts.items(), key=lambda kv: -kv[1])[:8]
            col.update(type="category", top=[{"value": k, "count": v} for k, v in top])
            lowers = {}
            for k in counts:
                lowers.setdefault(k.strip().lower(), []).append(k)
            clash = [v for v in lowers.values() if len(v) > 1]
            if clash:
                report["problems"].append("%s: same value written differently: %s" % (c, "; ".join(" / ".join(x) for x in clash[:3])))
        else:
            col.update(type="text", example_length=round(statistics.fmean(len(v) for v in filled), 1) if filled else 0)
        if col["missing_pct"] > 20:
            report["problems"].append("%s: %.0f%% missing" % (c, col["missing_pct"]))
        report["columns"].append(col)
    return report


if __name__ == "__main__":
    if len(sys.argv) < 2:
        sys.exit(__doc__)
    sheet = sys.argv[sys.argv.index("--sheet") + 1] if "--sheet" in sys.argv else None
    cols, rows = load(sys.argv[1], sheet)
    print(json.dumps(profile(cols, rows), indent=2, default=str, ensure_ascii=False))
