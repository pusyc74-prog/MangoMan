---
name: mangoman-proposal
description: Write a branded business proposal or quotation (PDF plus editable Word file) with scope, timeline, pricing with GST computed exactly, payment schedule, terms and a sign-off, checked before sending. Use when the user asks for a proposal, quotation, quote, pitch document, statement of work, scope of work, estimate or a client offer.
license: Apache-2.0
metadata:
  pack: mangoman
  version: "1.0"
---

# Proposal

You produce a proposal a client can say yes to: the answer first, a clear
scope, a believable timeline, exact prices and an easy sign-off. The scripts
in this skill's `scripts/` folder render a tested design (PDF and Word) and do
every calculation themselves, so your job is the argument and the words.

## 1. Understand (ask before writing)

Ask only what you do not already know, in one short message (use your
question tool if you have one):

1. **Client and need:** who it is for, what they want to achieve, and anything they said in the brief or meeting.
2. **What you will deliver**, and what is not included.
3. **Prices:** each item with quantity and rate, one-time or monthly; any discount; GST (default 18% for INR) or another tax.
4. **Payment terms** (for example 40% on signing, 30% at design, 30% on delivery) and how long the offer is valid.
5. **Start date and phases**, or rough weeks.
6. **Proof:** past results or clients you can name, and the team. Only real ones.
7. **Brand:** "Attach your logo (PNG or JPG) and share brand colours, or say skip and I'll choose a look."

If the user says "just do it", draft with what you have, mark nothing as
invented, state your assumptions in one line, and ask for the missing prices.
Never make up a price, a past client or a result.

## 2. Structure

Order: `summary` (the answer: what you propose and why, with the key figures
shown automatically), `understanding` (their goals and obstacles, so they feel
heard), `scope`, `timeline`, `pricing`, `proof`, `team`, `terms`,
`acceptance`. Aim for 4 to 8 pages. Leave out what does not help this client.

| Section | Fields |
|---|---|
| `summary` | `headline`, `lede` (two or three sentences), `points` (up to 4) |
| `understanding` | `goals`, `challenges`, optional `body` |
| `scope` | `deliverables` [{name, description}], `included`, `excluded` |
| `timeline` | `phases` [{name, start, end (YYYY-MM-DD), outcome}] or [{name, weeks: [first, last], outcome}] |
| `pricing` | `items` [{item, description, qty, unit, rate, billing: one-time / monthly / yearly, optional: true}], `discount` {label, percent or amount}, `tax` {label, rate}, `payments` [{milestone, percent, due}], `notes` |
| `proof` | `items` [{result, client}] |
| `team` | `people` [{name, role, bio}] |
| `terms` | `items` (numbered) |
| `acceptance` | `body`; signature blocks are added for both sides |
| `text` | `body`, `points` (anything else) |

Any section takes `"new_page": true`. Percentages are fractions (0.4 for 40%).

## 3. Write

- **You never compute a total.** Give quantities and rates; the build computes amounts, discount, GST, totals, the payment schedule and the duration, rounded to whole rupees so every printed figure adds up. Do not write totals in the text; the summary shows them.
- **Every number in the summary, background and results** comes from the user's `facts` (or the pricing); the checker fails anything else. Numbers in scope and terms (revision rounds, notice periods) are flagged for the user to confirm.
- **Write for the client, not about yourself.** Their goal first, your method second. Short sentences, plain words, no jargon.
- **Scope protects both sides:** say what is not included. Name the number of revision rounds.
- **No promises you cannot keep** ("guaranteed results", "double your sales", "unlimited revisions"); the checker warns.
- Always set `valid_until`, and payment milestones that add to 100%.

Write `proposal.json`:

```json
{
  "title": "A direct-to-customer store for Kesari Foods",
  "subtitle": "Website, WhatsApp ordering and launch marketing",
  "client": "Kesari Foods", "date": "2026-10-05", "valid_until": "2026-10-31", "number": "NB-2026-041",
  "currency": "INR",
  "from": {"name": "Northbeam Studio", "signatory": "Rohan Iyer", "role": "Founder"},
  "brand": {"logo": "logo.png"}, "theme": "ink", "motif": "orb", "type": "modern",
  "facts": {"orders": "18,275 orders in the 2026 season"},
  "sections": [
    {"type": "summary", "headline": "Summary", "lede": "...", "points": ["..."]},
    {"type": "pricing", "headline": "Investment",
     "items": [{"item": "Online store", "description": "Design, build, payments", "qty": 1, "rate": 240000},
               {"item": "Hosting and care", "qty": 1, "rate": 9500, "billing": "monthly"}],
     "discount": {"label": "Early sign-up discount", "percent": 0.05},
     "tax": {"label": "GST", "rate": 0.18},
     "payments": [{"milestone": "On signing", "percent": 0.5, "due": "With acceptance"},
                  {"milestone": "On delivery", "percent": 0.5, "due": "Store live"}]},
    {"type": "acceptance", "headline": "Next steps", "body": "Sign below and we will book the kick-off."}
  ]
}
```

Looks: `theme` (ink, forest, coral, ocean, plum, emerald, royal, nordic, wine,
clay, graphite, cobalt), `motif` (orb, rings), `type` (modern, editorial,
classic); with only a logo, colours are read from it (the build prints them;
tell the user).

## 4. Build

```
python3 <skill dir>/scripts/build_proposal.py proposal.json --out proposal
```

Writes `proposal.pdf` (A4, cover and numbered pages), `proposal.docx`
(editable in Word or Google Docs), `proposal.html` and `proposal.numbers.json`.
It prints the total: read it back against what the user expects. The PDF needs
Playwright or Chrome; Word needs `pip install python-docx`.

## 5. Check, fix, re-check

```
python3 <skill dir>/scripts/check_proposal.py proposal.json proposal
```

Fix every FAIL in `proposal.json` and rebuild. Then read the PDF once as the
client would: is the answer on page 2, is the price clear, is it obvious how to
say yes?

## 6. Deliver

Give the user the PDF to send and the Word file to edit, the total in one line
(with and without GST), anything they must confirm (flagged numbers, terms),
and the check result in one line ("Checked: totals and GST add up, payment
schedule is 100%, dates in order, every claim from your facts"). Offer a short
cover email to send with it.
