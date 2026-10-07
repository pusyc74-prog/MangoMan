---
name: google-ads-pro
description: Write or improve Google search ads using the advertiser's own search term report (or Keyword Planner ideas), with the converting searches ranked, wasted spend found and blocked by safe negative keywords, ad groups by theme with match types, headlines that cover the demand and a before-and-after score. Use when the user wants Google search ads, keywords or negative keywords and has a search term report or keyword ideas.
license: Proprietary. Free to use inside MangoMan only; no copying, changing or reselling (see the MangoMan LICENSE).
metadata:
  pack: mangoman-agent
  version: "1.0"
---

# Google Ads pro

You write Google search ads with the advertiser's own data. Scripts do the
mining and the counting; you write the copy. Run every script through the
sandbox, from the folder with the user's files:

    mangoman agents exec google-ads-pro SCRIPT.py ARGS

This agent builds on the free `mangoman-ad-copy` pack: its `build_ads.py`
and `check_ads.py` run the same way, and all its writing rules apply (read
that pack's SKILL.md if you have not).

## 1. Collect

You need: the facts (prices, offers, delivery, proof), the landing page
address and text, and the search term report (Google Ads, Insights and
reports, Search terms, download as CSV) or, for a new account, keyword ideas
from Keyword Planner (download as CSV). Ask once for anything missing; if the
user has neither, use the free pack instead.

## 2. Mine

    mangoman agents exec google-ads-pro mine.py --terms search_terms.csv --facts facts.md --landing landing.md --out mining

Use `--ideas keyword_ideas.csv` instead of (or with) `--terms`. Read
`mining.md`: the searches to win by demand, the words that matter, the wasted
searches, the negatives that block them (checked against every search worth
keeping), the searches that need a claim the facts do not make, and the ad
groups by theme.

## 3. Write ads.json

Follow the free pack's format and rules (every number from the facts, no
claims without proof, no exclamation marks, no all-capital words except
`allowed_caps`). Then:
- **Ad groups:** one per theme in mining.md when each can carry 15 good headlines; otherwise one ad group. Every ad group goes to the landing page.
- **Headlines:** 12 or fewer of your own, so the next step can add searches you miss. Put the top searches word for word in headlines (each search's words in one headline). Make every headline say something different; two headlines that share most words count as one.
- Never write a claim word (free, organic, pure, best...) the facts do not make, even when people search for it.
- Add your own keywords and negatives if you know better ones; the next step adds the mined ones.

## 4. Fit, build, check

    mangoman agents exec google-ads-pro fit.py ads.json mining.json --facts facts.md
    mangoman agents exec google-ads-pro build_ads.py ads.json --out ads
    mangoman agents exec google-ads-pro check_ads.py ads.json ads

`fit.py` adds the mined keywords and negatives to each ad group, removes any
negative that would block a search worth keeping, fills free headline slots
with top searches no headline covers, and writes `report.md` with the demand
covered before and after and the waste blocked. If a top search is still
missed and the facts support it, rewrite a headline to cover it and run fit
again. Fix every FAIL.

## 5. Deliver

Give `ads.md`, `google_ads.csv` and `google_keywords.csv` (import in Google
Ads Editor), `report.md` (before and after) and the check result in one line.
Say which searches you left out because the facts do not support them, and
suggest adding the negatives at campaign level too.
