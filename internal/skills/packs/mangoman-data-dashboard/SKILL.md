---
name: mangoman-data-dashboard
description: Turn raw data (CSV, Excel, JSON exports) into an executive-quality interactive dashboard with verified numbers. Use when the user shares data and asks for a dashboard, an analysis, charts, a report on the numbers, or "what does this data say".
license: Apache-2.0
metadata:
  pack: mangoman
  version: "1.0"
---

# Data to dashboard

You turn a data file into a dashboard a CEO can read in one minute. The rule
that makes it trustworthy: **every number is computed by code you write and
run, never typed, estimated or rounded in your head.** The scripts in this
skill's `scripts/` folder do the profiling, the drawing and the checking, so
the design is consistent and the numbers are traceable.

## 1. Understand (ask before building)

If the request does not already say, ask, in one short message (use your
question tool if you have one), at most these:

1. **Who reads it and what decision does it support?** (for example "the CEO, deciding where to put next quarter's budget")
2. **Which period and which measures matter most?** (offer the 3 you would pick from the profile)
3. **Currency and number style** if money is involved (₹ with lakh/crore, or $).

If the user says "just do it", choose sensible answers, state them in one line, and go on.

## 2. Profile the data

```
python3 <skill dir>/scripts/profile.py <data file>
```

It prints columns, types, gaps, ranges, duplicates and odd values, never full
rows. Read the problems list. Fix what is safe (trim spaces, unify spellings,
parse dates, drop exact duplicates) inside the analysis script, and tell the
user what you fixed. If something cannot be fixed safely, ask.

Privacy: only the profile and computed results go into your context. Do not
print raw rows unless the user asks.

## 3. Write `analysis.py` (all numbers come from here)

Write one Python script, next to the data, that reads the raw file, cleans it,
computes everything, and **prints one JSON spec** to stdout. Use pandas when
installed, otherwise the standard library. No hard-coded results.

Find the story before choosing charts: what changed, by how much, what drove
it, what needs attention. The headline is that finding as one sentence with
its number ("Revenue grew 18% to ₹4.2 Cr, driven by the South and West").

The spec:

```json
{
  "title": "Sales performance",
  "subtitle": "All regions, online and retail",
  "period": "Apr to Sep 2026",
  "headline": "Revenue grew 18% to ₹4.2 Cr, driven by the South and West",
  "kpis": [
    {"label": "Revenue", "value": 42000000, "format": "currency", "currency": "INR",
     "delta": 0.18, "delta_label": "vs Oct to Mar", "up_is_good": true},
    {"label": "Orders", "value": 18240, "delta": 0.07, "delta_label": "vs Oct to Mar"},
    {"label": "Return rate", "value": 0.064, "format": "percent", "delta": 0.011,
     "delta_kind": "pp", "up_is_good": false}
  ],
  "charts": [
    {"id": "trend", "type": "line", "title": "Revenue rose every month after June",
     "x": ["Apr", "May", "Jun", "Jul", "Aug", "Sep"],
     "series": [{"name": "Revenue", "values": [5900000, 6100000, 6000000, 7300000, 8100000, 8600000]}],
     "format": "currency", "currency": "INR", "wide": true},
    {"id": "regions", "type": "hbar", "title": "South leads; East is flat",
     "x": ["South", "West", "North", "East"],
     "series": [{"name": "Revenue", "values": [15800000, 12100000, 8900000, 5200000]}],
     "format": "currency", "currency": "INR", "highlight": "South"}
  ],
  "table": {"title": "Top products", "columns": ["Product", "Revenue", "Share"],
            "formats": ["text", "currency", "percent"], "currency": "INR",
            "rows": [["Mango pulp 1 kg", 6200000, 0.148]]},
  "notes": ["Returns rose in August after the courier change; 188 region names were cleaned."],
  "facts": {"south_share": 0.376, "names_cleaned": 188},
  "source": "sales_2026.csv, 18,240 orders"
}
```

Rules for the spec:
- `format`: `number`, `currency` (with `currency`: INR, USD, EUR, GBP) or `percent` (values as fractions, 0.18 = 18%). `delta` is a fraction; use `"delta_kind": "pp"` for changes in a rate.
- Chart `type`: `line` (change over time), `bar` (compare a few categories or periods), `hbar` (ranked categories with long names, one series), `stacked` (parts of a whole over time). One axis only; never two measures of different scale on one chart (make two charts).
- Chart titles state the finding, not the topic ("South leads; East is flat", not "Revenue by region").
- At most 6 KPIs (the first is shown large), at most 8 charts, at most 4 series per chart (fold the rest into "Other"). Use `highlight` to make one series or bar the point and grey the rest.
- Every number you write in the headline, titles and notes must be one the script computed. Put any number that appears only in text (a share, a count of fixed rows) in `"facts": {"name": value}` so it is traceable.
- Every claim in a title must be true of the data: "rose every month" needs every month to rise. Back such claims with a computed fact (for example `"months_rising": 4`) and word the title to match it exactly.

## 4. Build

```
python3 <skill dir>/scripts/build_dashboard.py analysis.py --out dashboard
```

This runs the analysis, validates the spec, and writes `dashboard.html` (self-contained, light and dark, hover tooltips, a table view under every chart), `dashboard.spec.json` and `dashboard.png`.

## 5. Check, fix, re-check

```
python3 <skill dir>/scripts/check.py analysis.py dashboard
```

Fix every FAIL (in `analysis.py`, never by editing the HTML) and rebuild until all lines PASS. The checker traces numbers, not claims, so then **look at `dashboard.png`** and reread every title against its chart: does the line really rise every month, did every region really grow? Also check labels are readable, nothing overlaps, and the headline is the first thing you see. Fix and rebuild if not.

## 6. Deliver

Give the user `dashboard.html` (and the PNG for a quick look), the headline finding, what you cleaned in the data, and the check result in one line, for example: "Checked: numbers reproduce from the data, every figure in the text traces to it." Offer a CEO deck from the same analysis (the `mangoman-ceo-deck` skill).
