---
name: amazon-listing-pro
description: Rewrite or optimise an Amazon listing using the seller's own search term report (Sponsored Products or Brand Analytics), competitor listings and the current listing, with keyword research by orders and clicks, a title led by the top search, backend search terms filled to the limit and a before-and-after score. Use when the user wants to improve an Amazon listing's ranking or sales and has a search term report or keyword data.
license: Apache-2.0
metadata:
  pack: mangoman-agent
  version: "1.0"
---

# Amazon listing pro

You improve an Amazon listing with the seller's own data. Scripts do the
research and the counting; you write the copy. Run every script through the
sandbox, from the folder with the user's files:

    mangoman agents exec amazon-listing-pro SCRIPT.py ARGS

This agent builds on the free `mangoman-ecommerce-listing` pack: its
`build_listing.py` and `check_listing.py` run the same way, and all its
writing rules apply (read that pack's SKILL.md if you have not).

## 1. Collect

You need: the product facts, the search term report (CSV export from Amazon
Ads "Search term" report or Brand Analytics "Search query performance"),
and if available competitor listings and the current listing. Ask once for
anything missing; if the user has no report, use the free pack instead.

## 2. Research

    mangoman agents exec amazon-listing-pro research.py --terms search_terms.csv --competitors competitors.md --current current_listing.md --brand BRAND --out research

Read `research.md`: the searches that sell, the words by demand, what must
lead the title, what the current listing misses and what competitors use.

## 3. Write listing.json

Follow the free pack's format and rules (no invented facts, no health or
promotional claims, no competitor names, the Indian disclosures). Then:
- **Title:** start with the brand, then the top search from research.md within the first 80 characters, then the deciding detail and size. Item name 40 to 75 characters.
- **Bullets:** 5 bullets of 150 to 250 characters. Work in the high-demand words naturally, one idea per bullet, each backed by a fact.
- Use a high-demand word only when the facts support it (do not write "sugar free" unless it is).
- In `search_terms`, put only synonyms, other spellings and Hindi words in Latin letters that research did not find (for example "aam ras", "hapus"). The next step puts the demand words first and keeps yours after them.

## 4. Optimise, build, check

    mangoman agents exec amazon-listing-pro optimise.py listing.json research.json --facts facts.md
    mangoman agents exec amazon-listing-pro build_listing.py listing.json --out listing
    mangoman agents exec amazon-listing-pro check_listing.py listing.json listing

`optimise.py` fills the backend search terms (249 bytes, no title words,
brands, repeats, or claim words such as "free" or "natural" that the facts
do not use) and writes `report.md` with demand covered before and
after. If a high-demand search is still missed and the facts support it,
work it into a bullet and run optimise again. Fix every FAIL.

## 5. Deliver

Give `listing.md`, the CSVs, `report.md` (before and after) and the check
result in one line. Say which searches you left out because the facts do not
support them.
