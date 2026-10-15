#!/usr/bin/env python3
"""Makes the messy sample files the engine tests read (all made up).

  python3 internal/sheets/testdata/make.py   (needs openpyxl)
"""
import csv
import os
import random

import openpyxl

HERE = os.path.dirname(os.path.abspath(__file__))
R = random.Random(11)


def inr(v):
    s = str(int(abs(v)))
    head, tail = s[:-3], s[-3:]
    parts = []
    while len(head) > 2:
        parts.insert(0, head[-2:])
        head = head[:-2]
    if head:
        parts.insert(0, head)
    return "₹" + ",".join(parts + [tail])


# Sales: two title rows, rupee amounts, returns in brackets, day-first dates
# written two ways, one city spelt three ways, a totals row; and a Customers
# sheet that the orders point at.
wb = openpyxl.Workbook()
ws = wb.active
ws.title = "Orders"
ws.append(["Sales Report - Kesar Naturals"])
ws.append(["Generated 01-Oct-2026"])
ws.append(["Order ID", "Order Date", "Customer ID", "City", "Channel", "Amount", "Qty"])
total = 0
for i in range(120):
    m, d = 4 + i % 6, 1 + (i * 7) % 28
    amt = R.randint(5, 250) * 100
    if i % 20 == 7:
        amt = -amt
    total += amt
    date = f"{d:02d}/{m:02d}/2026" if i % 5 else f"2026-{m:02d}-{d:02d}"
    ws.append([f"KN-{5000 + i}", date, f"C{100 + i % 30}", ["Mumbai", "mumbai ", "MUMBAI", "Pune", "Delhi"][i % 5],
               ["Website", "Amazon", "Flipkart"][i % 3], f"({inr(amt)[1:]})" if amt < 0 else inr(amt), 1 + i % 4])
ws.append(["Total", None, None, None, None, inr(total), None])
cs = wb.create_sheet("Customers")
cs.append(["Customer ID", "Name", "Segment", "Joined"])
for i in range(30):
    cs.append([f"C{100 + i}", f"Customer {i}", ["Retail", "Wholesale"][i % 2], f"{1 + i % 28:02d}-{['Jan', 'Feb', 'Mar'][i % 3]}-2025"])
wb.save(os.path.join(HERE, "sales.xlsx"))

# Two months with the same columns: stacked into one table.
for name, month in (("jan.csv", 1), ("feb.csv", 2)):
    with open(os.path.join(HERE, name), "w", newline="", encoding="utf-8") as f:
        w = csv.writer(f)
        w.writerow(["Date", "Product", "Units", "Revenue (INR)"])
        for i in range(10):
            w.writerow([f"2026-{month:02d}-{i + 1:02d}", ["Soap", "Oil", "Balm"][i % 3], 1 + i, f"{(1 + i) * 1250:,}"])

# Ads: months across, with "Jul" written once over Spend, Clicks and Conv.
wb = openpyxl.Workbook()
ws = wb.active
ws.append(["Campaign", "Jul", None, None, "Aug", None, None])
ws.append([None, "Spend", "Clicks", "Conv", "Spend", "Clicks", "Conv"])
for c in ["Brand Search", "Display", "Video"]:
    ws.append([c, R.randint(1000, 9000), R.randint(100, 900), R.randint(0, 40), R.randint(1000, 9000), R.randint(100, 900), R.randint(0, 40)])
wb.save(os.path.join(HERE, "ads.xlsx"))

# Years as column names (Excel keeps them numbers) and dates as Excel day
# serials in a column named like a date.
wb = openpyxl.Workbook()
ws = wb.active
ws.append(["Product", 2024, 2025, "Ship date"])
for i, p in enumerate(["Soap", "Oil", "Balm"]):
    ws.append([p, 100 + i, 120 + i, 46000 + i])
wb.save(os.path.join(HERE, "years.xlsx"))
