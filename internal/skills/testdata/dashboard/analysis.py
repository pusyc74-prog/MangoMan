import pandas as pd, json
df = pd.read_csv("sales.csv")
before = len(df)
df = df.drop_duplicates()
dups = before - len(df)
fixed_names = int((df["region"] != df["region"].str.strip().str.title()).sum())
df["region"] = df["region"].str.strip().str.title()
df["date"] = pd.to_datetime(df["date"])
df["month"] = df["date"].dt.to_period("M")
h2 = df[df["date"] >= "2026-04-01"]; h1 = df[df["date"] < "2026-04-01"]
rev2, rev1 = h2["amount_inr"].sum(), h1["amount_inr"].sum()
ord2, ord1 = len(h2), len(h1)
ret2, ret1 = (h2["returned"] == "Yes").mean(), (h1["returned"] == "Yes").mean()
monthly = h2.groupby("month")["amount_inr"].sum()
reg = h2.groupby("region")["amount_inr"].sum().sort_values(ascending=False)
reg_prev = h1.groupby("region")["amount_inr"].sum()
reg_growth = (reg / reg_prev - 1).reindex(reg.index)
top = h2.groupby("product")["amount_inr"].sum().sort_values(ascending=False)
growth = rev2 / rev1 - 1
lead = reg.index[0]
spec = {
  "title": "Sales performance",
  "subtitle": "All regions, April to September 2026 vs October to March",
  "period": "Apr to Sep 2026",
  "headline": f"Revenue grew {growth*100:.0f}% to ₹{rev2/1e7:.2f} Cr; {lead} brings {reg.iloc[0]/rev2*100:.0f}% of it",
  "kpis": [
    {"label": "Revenue", "value": round(float(rev2), 2), "format": "currency", "currency": "INR", "delta": round(float(growth), 4), "delta_label": "vs Oct to Mar"},
    {"label": "Orders", "value": int(ord2), "delta": round(ord2/ord1-1, 4), "delta_label": "vs Oct to Mar"},
    {"label": "Average order", "value": round(float(rev2/ord2), 2), "format": "currency", "currency": "INR", "delta": round(float((rev2/ord2)/(rev1/ord1)-1), 4), "delta_label": "vs Oct to Mar"},
    {"label": "Return rate", "value": round(float(ret2), 4), "format": "percent", "delta": round(float(ret2-ret1), 4), "delta_kind": "pp", "delta_label": "vs Oct to Mar", "up_is_good": False},
  ],
  "charts": [
    {"id": "trend", "type": "line", "title": "Revenue rose every month from June", "x": [m.strftime("%b") for m in monthly.index],
     "series": [{"name": "Revenue", "values": [round(float(v), 2) for v in monthly.values]}], "format": "currency", "currency": "INR"},
    {"id": "regions", "type": "hbar", "title": f"{lead} leads revenue by region", "x": list(reg.index),
     "series": [{"name": "Revenue", "values": [round(float(v), 2) for v in reg.values]}], "format": "currency", "currency": "INR", "highlight": lead},
    {"id": "growth", "type": "bar", "title": "Every region grew; growth by region", "x": list(reg_growth.index),
     "series": [{"name": "Growth", "values": [round(float(v), 4) for v in reg_growth.values]}], "format": "percent"},
    {"id": "returns", "type": "line", "title": f"Returns peaked in August at {h2[h2['date'].dt.month==8]['returned'].eq('Yes').mean()*100:.1f}%", "x": [m.strftime("%b") for m in monthly.index],
     "series": [{"name": "Return rate", "values": [round(float(v), 4) for v in h2.groupby('month')['returned'].apply(lambda s: (s=='Yes').mean()).values]}], "format": "percent"},
  ],
  "table": {"title": "Products by revenue", "columns": ["Product", "Revenue", "Share"], "formats": ["text", "currency", "percent"], "currency": "INR",
            "rows": [[p, round(float(v), 2), round(float(v/rev2), 4)] for p, v in top.items()]},
  "notes": [f"Region names were cleaned ({fixed_names} rows had other spellings) and {dups} duplicate order was removed."],
  "facts": {"lead_share": round(float(reg.iloc[0]/rev2), 4), "fixed_names": fixed_names, "duplicates": dups},
  "source": f"sales.csv, {len(df):,} orders after cleaning",
}
print(json.dumps(spec, ensure_ascii=False))
