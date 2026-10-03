"""Check an invoice or quotation before sending it.

Usage: python3 check_invoice.py invoice.json invoice
Checks: the spec is valid (GSTINs with their check digit, invoice number up
to 16 characters, HSN or SAC codes, dates); tax type follows the place of
supply (CGST and SGST in the same state, IGST across states); the arithmetic
recomputes and the amount in words matches; rule 46 details for unregistered
buyers over ₹50,000; GST rates in use since 22 September 2025; the total and
number appear in the PDF. Prints PASS, WARN or FAIL; exits 1 on any FAIL.
"""
import datetime
import json
import os
import sys

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
import build_invoice as B  # noqa: E402
import checks as C  # noqa: E402
import render  # noqa: E402

RATES_NOW = {0, 0.0025, 0.03, 0.05, 0.18, 0.40}  # after the September 2025 GST rate changes
RATES_OLD = {0.12, 0.28}


def main():
    if len(sys.argv) < 3:
        sys.exit(__doc__)
    src, out = sys.argv[1], sys.argv[2]
    spec = B.load(src)
    rep = C.Report()
    probs = B.validate(spec, os.path.dirname(os.path.abspath(src)))
    rep.check(probs, "spec is valid (GSTINs, number, HSN codes, dates)", "spec problems")
    if probs:
        rep.finish()
    n = B.compute(spec)
    path = out + ".numbers.json"
    if os.path.exists(path):
        same = json.load(open(path, encoding="utf-8")) == json.loads(json.dumps(n))
        rep.check([] if same else ["rebuild"], "figures recompute and match the built document", "invoice.json changed since the build")
    lines = n["lines"]
    bad = []
    for l in lines:
        if abs(l["taxable"] - (l["gross"] - l["discount"])) > 0.01 or abs(l["total"] - (l["taxable"] + l["cgst"] + l["sgst"] + l["igst"])) > 0.01:
            bad.append(l["description"])
        if n["inter_state"] and (l["cgst"] or l["sgst"]) or not n["inter_state"] and l["igst"]:
            bad.append("%s: wrong tax type" % l["description"])
    if abs(n["total"] - n["round_off"] - (n["taxable"] + n["tax"])) > 0.01 or abs(n["round_off"]) >= 1:
        bad.append("totals")
    rep.check(bad, "lines, %s, round-off and total add up (%s)" % ("IGST" if n["inter_state"] else "CGST and SGST", B.inr(n["total"])), "arithmetic does not add up")
    rep.check([] if B.in_words(n["total"]) == n["in_words"] else ["words"], "amount in words matches the total", "amount in words is wrong")
    sup_state = B.state_code(spec["supplier"])
    rep.add("PASS", "tax type: %s (supplier in %s, place of supply %s)" % (
        "IGST, different states" if n["inter_state"] else "CGST and SGST, same state", B.STATES.get(sup_state, sup_state), B.STATES.get(n["place_of_supply"], n["place_of_supply"])))
    to = spec["bill_to"]
    if spec.get("kind", "invoice") == "invoice" and not to.get("gstin") and n["total"] > 50000:
        miss = [k for k, ok in (("address", to.get("address")), ("state", to.get("state_code") or spec.get("place_of_supply"))) if not ok]
        rep.check(miss, "unregistered buyer over ₹50,000: name, address and state given", "unregistered buyer over ₹50,000 needs (rule 46)")
    d = datetime.date.fromisoformat(spec["date"])
    if d >= datetime.date(2025, 9, 22):
        old = sorted({"%g%%" % (l["gst_rate"] * 100) for l in lines if l["gst_rate"] in RATES_OLD})
        odd = sorted({"%g%%" % (l["gst_rate"] * 100) for l in lines if l["gst_rate"] not in RATES_NOW | RATES_OLD})
        rep.check(old, "GST rates match the slabs in force (5%, 18%, 40% and special rates)",
                  "GST rates changed on 22 September 2025 (most 12% items moved to 5%, most 28% items to 18% and some to 40%); confirm the rate for", "WARN")
        if odd:
            rep.add("WARN", "unusual GST rates: %s; confirm with the HSN rate schedule" % ", ".join(odd))
    if spec.get("kind", "invoice") == "invoice":
        if spec.get("turnover_above_5cr"):
            rep.add("WARN", "turnover above ₹5 crore: B2B invoices must be e-invoices (IRN from the government portal); this PDF is not one")
        if not spec.get("payment"):
            rep.add("WARN", "no payment details: add bank account, IFSC or UPI so the buyer can pay")
        if not spec.get("due_date"):
            rep.add("WARN", "no due date")
    texts = [l.get("details", "") for l in lines] + spec.get("terms", []) + [spec.get("note", "")]
    rep.check(C.first_match(C.PLACEHOLDER, texts + [spec["bill_to"]["name"], spec["supplier"]["name"]]), "no placeholder text", "placeholder text left in")
    pdf = out + ".pdf"
    if os.path.exists(pdf):
        txt = render.pdf_text(pdf).replace(" ", "")
        miss = [x for x in (B.inr(n["total"]), str(spec["number"])) if x.replace(" ", "") not in txt]
        rep.check(miss, "number and total appear in the PDF", "missing from the PDF")
        pages = render.pdf_pages(pdf)
        if pages > 2:
            rep.add("WARN", "%d pages; invoices read best on one page" % pages)
    else:
        rep.add("WARN", "no PDF (no browser engine): open the HTML and print to PDF")
    rep.finish()


if __name__ == "__main__":
    main()
