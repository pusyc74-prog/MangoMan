import json
import pandas as pd

df = pd.read_csv("sales.csv")
df["region"] = df["region"].str.strip().str.title()
df["date"] = pd.to_datetime(df["date"])
cur, prev = df[df.date.dt.strftime("%Y-%m") == "2026-09"], df[df.date.dt.strftime("%Y-%m") == "2026-08"]
rev, rev0 = float(cur.amount_inr.sum()), float(prev.amount_inr.sum())
ret, ret0 = float((cur.returned == "Yes").mean()), float((prev.returned == "Yes").mean())
reg = cur.groupby("region").amount_inr.sum().sort_values(ascending=False)
daily = cur.groupby(cur.date.dt.day).amount_inr.sum()
weeks = cur.groupby((cur.date.dt.day - 1) // 7 + 1).amount_inr.sum()
top = cur.groupby("product").amount_inr.sum().sort_values(ascending=False)
spec = {
    "title": "September 2026 performance", "client": "Kesari Foods", "period": "1 to 30 September 2026", "prepared_by": "Northbeam Studio",
    "brand": {"logo": "agency-logo.png"},
    "facts": {"august_return_rate": round(ret0, 4), "top_region_share": round(float(reg.iloc[0] / rev), 4)},
    "summary": f"Revenue was ₹{rev/1e5:.1f} lakh, {abs(rev/rev0-1)*100:.1f}% {'up' if rev > rev0 else 'down'} on August, and returns fell from {ret0*100:.1f}% to {ret*100:.1f}% after the courier change.",
    "kpis": [{"label": "Revenue", "value": round(rev, 2), "format": "currency", "currency": "INR", "delta": round(rev / rev0 - 1, 4), "delta_label": "vs August"},
             {"label": "Orders", "value": int(len(cur)), "delta": round(len(cur) / len(prev) - 1, 4), "delta_label": "vs August"},
             {"label": "Return rate", "value": round(ret, 4), "format": "percent", "delta": round(ret - ret0, 4), "delta_kind": "pp", "up_is_good": False, "delta_label": "vs August"}],
    "sections": [
        {"headline": f"{reg.index[0]} brought the most revenue", "chart": {"type": "hbar", "x": list(reg.index), "series": [{"name": "Revenue", "values": [round(float(v), 2) for v in reg]}],
                                                                       "format": "currency", "currency": "INR", "highlight": reg.index[0]},
         "body": [f"{reg.index[0]} made up {reg.iloc[0]/rev*100:.0f}% of September revenue."]},
        {"headline": "Revenue by week of the month", "chart": {"type": "bar", "x": [f"Week {int(w)}" for w in weeks.index], "series": [{"name": "Revenue", "values": [round(float(v), 2) for v in weeks]}],
                                                              "format": "currency", "currency": "INR"},
         "body": ["Weeks 1 to 4 were close; week 5 had only two days."]},
        {"headline": f"{top.index[0]} stayed the top product", "table": {"columns": ["Product", "Revenue"], "formats": ["text", "currency"], "currency": "INR",
                                                                        "rows": [[p, round(float(v), 2)] for p, v in top.items()]}}],
    "wins": [f"Returns fell to {ret*100:.1f}% from {ret0*100:.1f}%", f"{reg.index[0]} led all regions"],
    "issues": ["Plan stock for the season early: pre-orders open in February"],
    "next": [{"action": "Brief the pre-order campaign", "owner": "Northbeam", "date": "15 Oct"}, {"action": "Confirm courier rates for the season", "owner": "Kesari", "date": "31 Oct"}],
    "source": f"sales.csv, {len(cur)} September orders and {len(prev)} August orders",
}
print(json.dumps(spec, ensure_ascii=False))
