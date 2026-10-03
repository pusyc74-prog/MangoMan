"""Build a GST tax invoice, proforma invoice or quotation from invoice.json.

Usage: python3 build_invoice.py invoice.json --out invoice [--no-pdf]
Writes <out>.pdf (A4), <out>.html and <out>.numbers.json. The script does all
arithmetic: line values, discounts, CGST and SGST (same state) or IGST
(different states, decided from the place of supply), totals, round-off and
the amount in words.
"""
import datetime
import html
import json
import os
import re
import sys
from decimal import Decimal, ROUND_HALF_UP

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
import brandkit as BK  # noqa: E402
import render  # noqa: E402

STATES = {"01": "Jammu and Kashmir", "02": "Himachal Pradesh", "03": "Punjab", "04": "Chandigarh", "05": "Uttarakhand", "06": "Haryana",
          "07": "Delhi", "08": "Rajasthan", "09": "Uttar Pradesh", "10": "Bihar", "11": "Sikkim", "12": "Arunachal Pradesh", "13": "Nagaland",
          "14": "Manipur", "15": "Mizoram", "16": "Tripura", "17": "Meghalaya", "18": "Assam", "19": "West Bengal", "20": "Jharkhand",
          "21": "Odisha", "22": "Chhattisgarh", "23": "Madhya Pradesh", "24": "Gujarat", "26": "Dadra and Nagar Haveli and Daman and Diu",
          "27": "Maharashtra", "29": "Karnataka", "30": "Goa", "31": "Lakshadweep", "32": "Kerala", "33": "Tamil Nadu", "34": "Puducherry",
          "35": "Andaman and Nicobar Islands", "36": "Telangana", "37": "Andhra Pradesh", "38": "Ladakh", "97": "Other Territory"}
KINDS = {"invoice": "Tax Invoice", "proforma": "Proforma Invoice", "quote": "Quotation"}
B36 = "0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZ"
e = lambda s: html.escape(str(s if s is not None else ""), quote=True)


def load(path):
    with open(path, encoding="utf-8") as f:
        return json.load(f)


def money2(x):
    return float(Decimal(str(x)).quantize(Decimal("0.01"), rounding=ROUND_HALF_UP))


def inr(x):
    """₹1,23,456.78 (Indian grouping, always two decimals)."""
    neg, x = x < 0, abs(x)
    whole, frac = ("%.2f" % x).split(".")
    head, tail = whole[:-3], whole[-3:]
    head = ",".join(re.findall(r"\d{1,2}(?=(?:\d{2})*$)", head)) if head else ""
    return ("-" if neg else "") + "₹" + (head + "," if head else "") + tail + "." + frac


ONES = "zero one two three four five six seven eight nine ten eleven twelve thirteen fourteen fifteen sixteen seventeen eighteen nineteen".split()
TENS = "twenty thirty forty fifty sixty seventy eighty ninety".split()


def _words(n):
    if n < 20:
        return ONES[n]
    if n < 100:
        return TENS[n // 10 - 2] + ("-" + ONES[n % 10] if n % 10 else "")
    if n < 1000:
        return ONES[n // 100] + " hundred" + (" " + _words(n % 100) if n % 100 else "")
    for size, name in ((10 ** 7, "crore"), (10 ** 5, "lakh"), (1000, "thousand")):
        if n >= size:
            return _words(n // size) + " " + name + (" " + _words(n % size) if n % size else "")


def in_words(x):
    """Rupees one lakh twenty-three thousand four hundred fifty-six and paise seventy-eight only."""
    rupees = int(x)
    paise = int(round((x - rupees) * 100))
    s = "Rupees " + _words(rupees) + (" and paise " + _words(paise) if paise else "") + " only"
    return s[0].upper() + s[1:]


def gstin_ok(g):
    """Format and check digit of a GSTIN."""
    g = str(g or "").upper()
    if not re.match(r"^\d{2}[A-Z]{5}\d{4}[A-Z][1-9A-Z]Z[0-9A-Z]$", g) or g[:2] not in STATES:
        return False
    total = 0
    for i, c in enumerate(g[:14]):
        p = B36.index(c) * (2 if i % 2 else 1)
        total += p // 36 + p % 36
    return B36[(36 - total % 36) % 36] == g[14]


def state_code(party):
    """A party's state code: from its GSTIN, else its state_code field."""
    g = str(party.get("gstin") or "")
    return g[:2] if len(g) >= 2 and g[:2].isdigit() else str(party.get("state_code", "")).zfill(2)


def buyer_state(spec):
    """Place of supply: given, else the delivery address's state, else the buyer's."""
    if spec.get("place_of_supply"):
        return str(spec["place_of_supply"]).zfill(2)
    ship = spec.get("ship_to") or {}
    return state_code(ship) if ship.get("gstin") or ship.get("state_code") else state_code(spec["bill_to"])


def compute(spec):
    sup = spec["supplier"]
    pos = buyer_state(spec)
    inter = state_code(sup) != pos
    lines, by_rate = [], {}
    for it in spec["items"]:
        gross = money2(float(it.get("qty", 1)) * float(it["rate"]))
        disc = money2(gross * float(it["discount_percent"])) if it.get("discount_percent") else money2(it.get("discount", 0))
        taxable = money2(gross - disc)
        r = float(it.get("gst_rate", spec.get("gst_rate", 0.18)))
        if inter:
            igst, cgst, sgst = money2(taxable * r), 0.0, 0.0
        else:
            igst, cgst = 0.0, money2(taxable * r / 2)
            sgst = cgst
        lines.append(dict(it, qty=float(it.get("qty", 1)), rate=float(it["rate"]), gross=gross, discount=disc, taxable=taxable,
                          gst_rate=r, cgst=cgst, sgst=sgst, igst=igst, total=money2(taxable + cgst + sgst + igst)))
        g = by_rate.setdefault(r, {"rate": r, "taxable": 0.0, "cgst": 0.0, "sgst": 0.0, "igst": 0.0})
        for k in ("taxable", "cgst", "sgst", "igst"):
            g[k] = money2(g[k] + lines[-1][k])
    taxable = money2(sum(l["taxable"] for l in lines))
    tax = money2(sum(l["cgst"] + l["sgst"] + l["igst"] for l in lines))
    before = money2(taxable + tax)
    total = float(Decimal(str(before)).quantize(Decimal("1"), rounding=ROUND_HALF_UP)) if spec.get("round_off", True) else before
    return {"inter_state": inter, "place_of_supply": pos, "lines": lines, "by_rate": sorted(by_rate.values(), key=lambda g: g["rate"]),
            "taxable": taxable, "tax": tax, "round_off": money2(total - before), "total": total, "in_words": in_words(total)}


def validate(spec, bdir="."):
    p = []
    kind = spec.get("kind", "invoice")
    if kind not in KINDS:
        p.append("kind must be one of %s" % ", ".join(KINDS))
    for k in ("number", "date"):
        if not spec.get(k):
            p.append("%s is required" % k)
    if spec.get("number") and not re.match(r"^[A-Za-z0-9/-]{1,16}$", str(spec["number"])):
        p.append("number: up to 16 letters, digits, - or / (GST rule 46)")
    dates = {}
    for k in ("date", "due_date", "valid_until"):
        if spec.get(k):
            try:
                dates[k] = datetime.date.fromisoformat(spec[k])
            except ValueError:
                p.append("%s must be YYYY-MM-DD" % k)
    for k in ("due_date", "valid_until"):
        if k in dates and "date" in dates and dates[k] < dates["date"]:
            p.append("%s is before the date" % k)
    sup = spec.get("supplier") or {}
    for k in ("name", "address"):
        if not sup.get(k):
            p.append("supplier.%s is required" % k)
    if kind == "invoice" and not sup.get("gstin"):
        p.append("supplier.gstin is required on a tax invoice")
    if not (sup.get("gstin") or sup.get("state_code")):
        p.append("supplier needs gstin or state_code (it decides CGST and SGST or IGST)")
    for who in ("supplier", "bill_to", "ship_to"):
        g = (spec.get(who) or {}).get("gstin")
        if g and not gstin_ok(g):
            p.append("%s.gstin %s is not a valid GSTIN (format or check digit)" % (who, g))
    to = spec.get("bill_to") or {}
    if not to.get("name"):
        p.append("bill_to.name is required")
    if spec.get("ship_to") and not spec["ship_to"].get("name"):
        p.append("ship_to.name is required")
    for who in ("supplier", "bill_to", "ship_to"):
        sc = (spec.get(who) or {}).get("state_code")
        if sc and str(sc).zfill(2) not in STATES:
            p.append("%s.state_code %s is not a state code" % (who, sc))
    if not (to.get("gstin") or to.get("state_code")) and not spec.get("place_of_supply"):
        p.append("give bill_to.gstin, bill_to.state_code or place_of_supply (it decides CGST and SGST or IGST)")
    pos = spec.get("place_of_supply")
    if pos and str(pos).zfill(2) not in STATES:
        p.append("place_of_supply must be a state code, e.g. 27 for Maharashtra")
    if not spec.get("items"):
        p.append("items are required")
    for i, it in enumerate(spec.get("items", []), 1):
        if not it.get("description") or not isinstance(it.get("rate"), (int, float)):
            p.append("item %d: needs description and a numeric rate" % i)
        code = str(it.get("hsn", ""))
        if kind == "invoice" and not re.match(r"^\d{4}(\d{2}){0,2}$", code):
            p.append("item %d: hsn must be 4, 6 or 8 digits (SAC for services is 6 digits starting 99)" % i)
        dp, d = it.get("discount_percent", 0), it.get("discount", 0)
        if not (isinstance(dp, (int, float)) and 0 <= dp < 1) or not isinstance(d, (int, float)) or d < 0 or \
                isinstance(it.get("rate"), (int, float)) and d > float(it.get("qty", 1)) * it["rate"]:
            p.append("item %d: discount_percent is a fraction below 1 (0.1 for 10%%) and discount cannot exceed the line value" % i)
        r = it.get("gst_rate", spec.get("gst_rate", 0.18))
        if not (isinstance(r, (int, float)) and 0 <= r < 1):
            p.append("item %d: gst_rate is a fraction, e.g. 0.18" % i)
    for key in ("primary", "accent"):
        if (spec.get("brand") or {}).get(key):
            try:
                BK.hexc(spec["brand"][key])
            except ValueError:
                p.append("brand %s must be a hex colour" % key)
    if (spec.get("brand") or {}).get("logo"):
        pr = BK.logo_check(os.path.join(bdir, spec["brand"]["logo"]))
        if pr:
            p.append(pr)
    return p


CSS = """
@page { size: A4; margin: 14mm 14mm 16mm; }
* { box-sizing: border-box; }
html { -webkit-print-color-adjust: exact; print-color-adjust: exact; }
body { margin: 0; font: 9.5pt/1.45 "Segoe UI", Calibri, Carlito, Arial, sans-serif; color: #%(text)s; }
.top { display: flex; justify-content: space-between; align-items: flex-start; gap: 10mm; padding-bottom: 5mm; border-bottom: 2px solid #%(dark)s; }
.top img { height: 14mm; }
.title { text-align: right; }
.title h1 { margin: 0; font-size: 20pt; color: #%(dark)s; letter-spacing: -0.01em; }
.title .meta { margin-top: 2mm; display: grid; grid-template-columns: auto auto; gap: 0.5mm 4mm; justify-content: end; }
.title .meta span:nth-child(odd) { color: #%(muted)s; }
.parties { display: grid; grid-template-columns: repeat(var(--n), 1fr); gap: 6mm; margin: 5mm 0; }
.parties h2 { margin: 0 0 1mm; font-size: 8pt; color: #%(muted)s; font-weight: 700; }
.parties b { font-size: 10.5pt; }
table { width: 100%%; border-collapse: collapse; font-variant-numeric: tabular-nums; }
th { background: #%(dark)s; color: #fff; font-size: 8pt; font-weight: 700; text-align: left; padding: 2mm 1.6mm; }
td { padding: 2mm 1.6mm; border-bottom: 1px solid #%(grid)s; vertical-align: top; }
.n { text-align: right; white-space: nowrap; }
td .d { color: #%(muted)s; font-size: 8.5pt; }
.sum { display: grid; grid-template-columns: 1.3fr 1fr; gap: 8mm; margin-top: 5mm; break-inside: avoid; }
.totals td { border: 0; padding: 1mm 1.6mm; }
.totals tr.grand td { border-top: 2px solid #%(dark)s; font-size: 12.5pt; font-weight: 700; padding-top: 2mm; color: #%(dark)s; }
.words { margin-top: 2mm; font-weight: 600; }
.box { border: 1px solid #%(grid)s; border-radius: 2mm; padding: 3mm; margin-top: 3mm; }
.box h3 { margin: 0 0 1mm; font-size: 8pt; color: #%(muted)s; }
.breakup { margin-top: 3mm; font-size: 8.5pt; } .breakup th { background: #%(tint)s; color: #%(text)s; }
.sign { text-align: right; margin-top: 12mm; } .sign .line { margin-top: 14mm; border-top: 1px solid #%(text)s; display: inline-block; padding-top: 1mm; min-width: 55mm; text-align: center; }
.small { font-size: 8pt; color: #%(muted)s; }
"""


def party_html(title, p):
    if not p:
        return ""
    lines = [e(p.get("address", ""))]
    if p.get("gstin"):
        lines.append("GSTIN %s" % e(p["gstin"]))
    sc = state_code(p)
    if sc in STATES:
        lines.append("State: %s (%s)" % (STATES[sc], sc))
    for k in ("phone", "email"):
        if p.get(k):
            lines.append(e(p[k]))
    return "<div><h2>%s</h2><b>%s</b><br>%s</div>" % (e(title), e(p["name"]), "<br>".join(x for x in lines if x))


def build_html(spec, T, n):
    kind = spec.get("kind", "invoice")
    sup = spec["supplier"]
    logo = ""
    if T.get("logo"):
        logo = '<img src="%s" alt="%s">' % (BK.data_uri(T["logo"]), e(sup["name"]))
    day = lambda k: datetime.date.fromisoformat(spec[k]).strftime("%d %b %Y").lstrip("0")
    meta = [("Number", spec["number"]), ("Date", day("date"))]
    meta += [(label, day(k)) for k, label in (("due_date", "Due"), ("valid_until", "Valid until")) if spec.get(k)]
    meta.append(("Place of supply", "%s (%s)" % (STATES.get(n["place_of_supply"], ""), n["place_of_supply"])))
    if kind == "invoice":
        meta.append(("Reverse charge", "Yes" if spec.get("reverse_charge") else "No"))
    top = '<div class="top"><div>%s<div style="margin-top:2mm">%s</div></div><div class="title"><h1>%s</h1><div class="meta">%s</div></div></div>' % (
        logo, party_html("From", sup).replace("<h2>From</h2>", ""), KINDS[kind], "".join("<span>%s</span><span>%s</span>" % (e(a), e(b)) for a, b in meta))
    parties = [party_html("Bill to", spec["bill_to"])] + ([party_html("Ship to", spec["ship_to"])] if spec.get("ship_to") else [])
    inter = n["inter_state"]
    head = "<tr><th>#</th><th>Item</th><th>HSN/SAC</th><th class='n'>Qty</th><th class='n'>Rate</th>%s<th class='n'>Taxable</th><th class='n'>GST</th>%s<th class='n'>Total</th></tr>" % (
        "<th class='n'>Discount</th>" if any(l["discount"] for l in n["lines"]) else "",
        "<th class='n'>IGST</th>" if inter else "<th class='n'>CGST</th><th class='n'>SGST</th>")
    rows = "".join("<tr><td>%d</td><td>%s%s</td><td>%s</td><td class='n'>%s %s</td><td class='n'>%s</td>%s<td class='n'>%s</td><td class='n'>%s%%</td>%s<td class='n'>%s</td></tr>" % (
        i, e(l["description"]), "<div class='d'>%s</div>" % e(l["details"]) if l.get("details") else "", e(l.get("hsn", "")),
        ("%g" % l["qty"]), e(l.get("unit", "")), inr(l["rate"]), "<td class='n'>%s</td>" % inr(l["discount"]) if any(x["discount"] for x in n["lines"]) else "",
        inr(l["taxable"]), "%g" % (l["gst_rate"] * 100), "<td class='n'>%s</td>" % inr(l["igst"]) if inter else "<td class='n'>%s</td><td class='n'>%s</td>" % (inr(l["cgst"]), inr(l["sgst"])),
        inr(l["total"])) for i, l in enumerate(n["lines"], 1))
    br = "".join("<tr><td>%g%%</td><td class='n'>%s</td>%s</tr>" % (g["rate"] * 100, inr(g["taxable"]),
                 "<td class='n'>%s</td>" % inr(g["igst"]) if inter else "<td class='n'>%s</td><td class='n'>%s</td>" % (inr(g["cgst"]), inr(g["sgst"]))) for g in n["by_rate"])
    breakup = "<table class='breakup'><tr><th>GST rate</th><th class='n'>Taxable value</th>%s</tr>%s</table>" % (
        "<th class='n'>IGST</th>" if inter else "<th class='n'>CGST</th><th class='n'>SGST</th>", br)
    tot = "<tr><td>Taxable value</td><td class='n'>%s</td></tr>" % inr(n["taxable"])
    if inter:
        tot += "<tr><td>IGST</td><td class='n'>%s</td></tr>" % inr(n["tax"])
    else:
        c = money2(sum(l["cgst"] for l in n["lines"]))
        tot += "<tr><td>CGST</td><td class='n'>%s</td></tr><tr><td>SGST</td><td class='n'>%s</td></tr>" % (inr(c), inr(money2(n["tax"] - c)))
    if n["round_off"]:
        tot += "<tr><td>Round off</td><td class='n'>%s</td></tr>" % inr(n["round_off"])
    tot += "<tr class='grand'><td>Total</td><td class='n'>%s</td></tr>" % inr(n["total"])
    pay = spec.get("payment") or {}
    pay_lines = [("Bank", pay.get("bank")), ("Account name", pay.get("account_name")), ("Account number", pay.get("account")),
                 ("IFSC", pay.get("ifsc")), ("UPI", pay.get("upi"))]
    pay_html = "<div class='box'><h3>Payment details</h3>%s</div>" % "<br>".join("%s: <b>%s</b>" % (e(a), e(b)) for a, b in pay_lines if b) if any(b for _, b in pay_lines) else ""
    terms = "<div class='box'><h3>Terms</h3>%s</div>" % "<br>".join(e(t) for t in spec["terms"]) if spec.get("terms") else ""
    left = breakup + pay_html + terms
    right = "<table class='totals'>%s</table><div class='words'>%s</div>" % (tot, e(n["in_words"]))
    sign = "<div class='sign'>For %s<br><span class='line'>Authorised signatory</span></div>" % e(sup["name"]) if kind != "quote" else ""
    note = "<p class='small'>%s</p>" % e(spec["note"]) if spec.get("note") else ""
    css = CSS % T
    return '<!doctype html><html lang="en"><head><meta charset="utf-8"><title>%s %s</title><style>%s</style></head><body>%s<div class="parties" style="--n:%d">%s</div><table>%s%s</table><div class="sum"><div>%s</div><div>%s%s</div></div>%s</body></html>' % (
        KINDS[kind], e(spec["number"]), css, top, len(parties), "".join(parties), head, rows, left, right, sign, note)


def main():
    a = sys.argv[1:]
    if not a:
        sys.exit(__doc__)
    src = a[0]
    out = a[a.index("--out") + 1] if "--out" in a else "invoice"
    spec = load(src)
    bdir = os.path.dirname(os.path.abspath(src))
    probs = validate(spec, bdir)
    if probs:
        sys.exit("spec problems:\n- " + "\n- ".join(probs))
    T, note = BK.resolve_spec(spec, bdir)
    n = compute(spec)
    with open(out + ".numbers.json", "w", encoding="utf-8") as f:
        json.dump(n, f, indent=1, ensure_ascii=False)
    with open(out + ".html", "w", encoding="utf-8") as f:
        f.write(build_html(spec, T, n))
    made = [out + ".html"]
    if "--no-pdf" not in a:
        try:
            render.html_to_pdf(out + ".html", out + ".pdf")
            made.append(out + ".pdf")
        except Exception as ex:  # noqa: BLE001
            print("PDF skipped: %s" % ex)
    if note:
        print(note)
    print("%s %s: total %s (%s)" % (KINDS[spec.get("kind", "invoice")], spec["number"], inr(n["total"]), "IGST" if n["inter_state"] else "CGST + SGST"))
    print("built " + ", ".join(made))


if __name__ == "__main__":
    main()
