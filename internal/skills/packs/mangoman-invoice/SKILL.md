---
name: mangoman-invoice
description: Make a GST-compliant tax invoice, proforma invoice or quotation (A4 PDF) with CGST and SGST or IGST worked out from the place of supply, HSN or SAC codes, amount in words and payment details, checked before sending. Use when the user asks for an invoice, bill, GST invoice, tax invoice, proforma, quotation, quote or estimate.
license: Apache-2.0
metadata:
  pack: mangoman
  version: "1.0"
---

# Invoice and quotation

You produce an invoice the buyer can pay and the accountant can file. The
scripts in this skill's `scripts/` folder do every calculation (line values,
discounts, CGST and SGST or IGST, round-off, amount in words) and check the
GST basics, so your job is to collect the right details.

## 1. Collect (ask once, in one message)

1. **Kind:** tax invoice, proforma invoice or quotation; invoice number (up to 16 letters, digits, `-` or `/`, in sequence for the financial year) and date; due date or validity.
2. **Your details:** business name, address, GSTIN; bank account, IFSC and UPI ID.
3. **Buyer:** name, address, and GSTIN if registered (else their state); a different delivery address if any.
4. **Items:** description, HSN (goods) or SAC (services, 6 digits starting 99), quantity, unit, rate before tax, discount, GST rate. Do not guess HSN codes or rates: ask, or say which you assumed and that the user must confirm them.
5. **Brand:** "Attach your logo (PNG or JPG), or say skip."

Tax type is automatic: same state as the supplier means CGST and SGST (half
each); a different state means IGST. The state comes from the GSTIN's first
two digits, or `state_code` / `place_of_supply` (27 Maharashtra, 29 Karnataka,
07 Delhi, 33 Tamil Nadu, 24 Gujarat and so on).

## 2. Write `invoice.json`

```json
{
  "kind": "invoice", "number": "NB/26-27/041", "date": "2026-11-20", "due_date": "2026-12-05",
  "brand": {"logo": "logo.png"},
  "supplier": {"name": "Northbeam Studio", "address": "22 Baner Road, Pune 411045", "gstin": "27ABCDE1234F1Z0"},
  "bill_to": {"name": "Kesari Retail Pvt Ltd", "address": "88 Residency Road, Bengaluru 560025", "gstin": "29PQRSX5678K1ZU"},
  "items": [{"description": "Online store build", "details": "Milestone 2 of 3", "hsn": "998314", "qty": 1, "unit": "job", "rate": 120000, "gst_rate": 0.18,
             "discount_percent": 0.1}],
  "payment": {"bank": "HDFC Bank", "account_name": "Northbeam Studio", "account": "50200012345678", "ifsc": "HDFC0001234", "upi": "northbeam@hdfcbank"},
  "terms": ["Payment due within 15 days."]
}
```

Rates and discounts are fractions (0.18 for 18%). Optional: `ship_to`,
`place_of_supply`, `reverse_charge`, `round_off` (default true), `note`,
`turnover_above_5cr` (warns that B2B invoices must then be e-invoices),
`valid_until` for quotations.

## 3. Build, check, fix

```
python3 <skill dir>/scripts/build_invoice.py invoice.json --out invoice
python3 <skill dir>/scripts/check_invoice.py invoice.json invoice
```

The build prints the total; read it back to the user. Fix every FAIL and
rebuild.

## 4. Deliver

Give the user the PDF, the total with the tax type ("₹2,18,300 including
IGST ₹33,300"), anything to confirm (HSN codes, rates), and the check result in
one line ("Checked: GSTINs valid, IGST because the buyer is in another state,
totals and amount in words match"). This is a regular PDF invoice, not a
government e-invoice with an IRN.
