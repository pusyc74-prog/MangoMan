---
name: mangoman-client-report
description: Turn a client's data (sales, ads, website, social or any export) into a monthly or weekly performance report (A4 PDF) with KPIs against the last period, charts with takeaway headlines, wins, issues and next month's plan, every number computed and checked. Use when the user asks for a client report, monthly report, MIS report, performance report, campaign report or a weekly update from data.
license: Apache-2.0
metadata:
  pack: mangoman
  version: "1.0"
---

# Client report

You produce the report an agency or team sends every month: what happened,
why, and what happens next, in two or three pages. Numbers come from code,
never typed. The scripts in this skill's `scripts/` folder render a tested
design and check it.

## 1. Understand (ask once, in one message)

1. **Client and period** (this month against last month, or another comparison).
2. **The data** (CSV, Excel or exports from ads, analytics, store or CRM) and which measures matter to this client.
3. **What the client cares about** (sales, leads, cost per lead, return rate) and anything that happened (campaigns, changes, outages).
4. **Brand:** "Attach your logo (PNG or JPG) or say skip." The report carries the sender's logo.

If the user says "just do it", pick sensible answers, state them in one line, and continue.

## 2. Write `analysis.py` (prints the report spec)

Profile the data first (for example with `mangoman-data-dashboard/scripts/profile.py`).
One script next to the data computes everything and prints one JSON object:

```json
{
  "title": "September 2026 performance", "client": "Kesari Foods", "period": "1 to 30 September 2026", "prepared_by": "Northbeam Studio",
  "brand": {"logo": "logo.png"}, "theme": "ink",
  "summary": "Revenue was ₹5.2 lakh, 8.9% up on August, and returns fell from 10.8% to 2.5%.",
  "facts": {"august_return_rate": 0.108},
  "kpis": [{"label": "Revenue", "value": 521000, "format": "currency", "currency": "INR", "delta": 0.089, "delta_label": "vs August"},
           {"label": "Return rate", "value": 0.025, "format": "percent", "delta": -0.083, "delta_kind": "pp", "up_is_good": false}],
  "sections": [{"headline": "South brought the most revenue",
                "chart": {"type": "hbar", "x": ["South", "West"], "series": [{"name": "Revenue", "values": [216000, 159000]}], "format": "currency", "currency": "INR", "highlight": "South"},
                "body": ["South made up 41% of revenue."]},
               {"headline": "...", "table": {"columns": ["Product", "Revenue"], "formats": ["text", "currency"], "currency": "INR", "rows": [["...", 297303]]}}],
  "wins": ["..."], "issues": ["..."],
  "next": [{"action": "...", "owner": "...", "date": "15 Oct"}],
  "source": "sales.csv, 242 September orders"
}
```

Rules: the summary gives the result and the main reason in one or two
sentences; every section headline is the takeaway as a sentence (at most 16
words); every number in the text is computed by the script, and numbers that
appear only in text go in `facts`; 3 to 6 sections; always a plan for next
month. Charts: `line`, `bar`, `hbar`, `stacked`, one axis, `highlight` to make
one series or bar the point.

## 3. Build, check, fix

```
python3 <skill dir>/scripts/build_report.py analysis.py --out report
python3 <skill dir>/scripts/check_report.py analysis.py report
```

Fix every FAIL in `analysis.py` (never in the outputs) and rebuild.

## 4. Deliver

Give the user the PDF, the summary in one line, and the check result in one
line ("Checked: numbers reproduce from the data, every figure traces to it,
every section in the PDF"). Offer the same analysis as a deck
(`mangoman-ceo-deck`) or an interactive dashboard (`mangoman-data-dashboard`).
