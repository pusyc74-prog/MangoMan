import pandas as pd, json
df = pd.read_csv("sales.csv").drop_duplicates()
df["region"] = df["region"].str.strip().str.title()
df["date"] = pd.to_datetime(df["date"]); df["month"] = df["date"].dt.to_period("M")
h2 = df[df["date"] >= "2026-04-01"]; h1 = df[df["date"] < "2026-04-01"]
rev2, rev1 = float(h2.amount_inr.sum()), float(h1.amount_inr.sum())
g = rev2/rev1 - 1
reg2 = h2.groupby("region").amount_inr.sum(); reg1 = h1.groupby("region").amount_inr.sum()
rg = (reg2/reg1 - 1).sort_values(ascending=False)
monthly = h2.groupby("month").amount_inr.sum()
ret = h2.groupby("month").returned.apply(lambda s: (s == "Yes").mean())
aug = float(ret.iloc[4]); rest = float(ret.drop(ret.index[4]).mean())
top = h2.groupby("product").amount_inr.sum().sort_values(ascending=False)
share_top = float(top.iloc[0]/rev2)
east = float(rg["East"])
spec = {
 "title": "H1 FY27 sales review", "subtitle": "April to September 2026", "date": "October 2026",
 "theme": "ink",
 "source": "sales.csv, orders April 2025 to September 2026",
 "facts": {"aug_returns": round(aug,4), "other_months_returns": round(rest,4), "top_share": round(share_top,4), "east_growth": round(east,4)},
 "slides": [
  {"type": "title"},
  {"type": "answer", "headline": f"Revenue grew {g*100:.1f}%, but East and returns need action",
   "points": [f"Revenue reached ₹{rev2/1e7:.2f} Cr, up {g*100:.1f}% on the previous half",
              f"East shrank {abs(east)*100:.1f}% while the other three regions grew",
              f"Returns hit {aug*100:.1f}% in August versus {rest*100:.1f}% in other months"]},
  {"type": "kpis", "headline": "Revenue and order value up, order count flat",
   "kpis": [{"label": "Revenue", "value": round(rev2,2), "format": "currency", "currency": "INR", "delta": round(g,4), "delta_label": "vs previous half"},
            {"label": "Orders", "value": int(len(h2)), "delta": round(len(h2)/len(h1)-1,4), "delta_label": "vs previous half"},
            {"label": "Return rate", "value": round(float((h2.returned=="Yes").mean()),4), "format": "percent",
             "delta": round(float((h2.returned=="Yes").mean()-(h1.returned=="Yes").mean()),4), "delta_kind": "pp", "up_is_good": False, "delta_label": "vs previous half"}]},
  {"type": "chart", "headline": "East is the only region that shrank",
   "chart": {"type": "bar", "x": list(rg.index), "series": [{"name": "Growth", "values": [round(float(v),4) for v in rg.values]}], "format": "percent", "highlight": "Growth"},
   "takeaway": "Growth compares April to September 2026 with October 2025 to March 2026."},
  {"type": "stat", "value": round(aug,4), "format": "percent",
   "headline": f"of August orders came back, against {rest*100:.1f}% in the other months",
   "note": "The spike lines up with the courier change in late July."},
  {"type": "chart", "headline": f"August returns spiked to {aug*100:.1f}%",
   "chart": {"type": "line", "x": [m.strftime('%b') for m in ret.index], "series": [{"name": "Return rate", "values": [round(float(v),4) for v in ret.values]}], "format": "percent"}},
  {"type": "table", "headline": f"Alphonso boxes bring {share_top*100:.0f}% of revenue",
   "table": {"columns": ["Product", "Revenue", "Share"], "formats": ["text","currency","percent"], "currency": "INR",
             "rows": [[p, round(float(v),2), round(float(v/rev2),4)] for p,v in top.items()]}},
  {"type": "next_steps", "headline": "Three actions for the next quarter",
   "items": [{"action": "Review East distributor terms and pricing", "owner": "Sales head", "date": "Oct 31"},
             {"action": "Audit the August courier change and claims", "owner": "Operations", "date": "Oct 20"},
             {"action": "Protect Alphonso supply for the next season", "owner": "Procurement", "date": "Nov 15"}]}
 ]}
print(json.dumps(spec, ensure_ascii=False))
