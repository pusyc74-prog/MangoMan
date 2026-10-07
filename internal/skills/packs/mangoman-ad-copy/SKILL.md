---
name: mangoman-ad-copy
description: Write paid ad copy for Google search ads (responsive search ads with keywords and negatives) and Meta ads (Facebook and Instagram), exported for Google Ads Editor and checked against character limits and ad policies. Use when the user asks for ad copy, Google Ads, search ads, Facebook or Instagram ads, Meta ads, ad headlines, or keywords for a campaign.
license: Proprietary. Free to use inside MangoMan only; no copying, changing or reselling (see the MangoMan LICENSE).
metadata:
  pack: mangoman
  version: "1.0"
---

# Ad copy

You write ads that get approved and get clicks. The scripts in this skill's
`scripts/` folder export the copy for upload and check it against the limits
and the editorial rules that most often get ads disapproved, so your job is
the message and the keywords.

## 1. Understand (ask once, in one message)

1. **What is advertised and the one action** (buy, book, sign up, call, WhatsApp), and the landing page address.
2. **Platforms:** Google search, Meta (Facebook and Instagram) or both.
3. **Audience and area** (cities, language).
4. **Facts to use:** prices, offers, delivery times, results. You will use only these.
5. **Keywords** the user knows, and terms to exclude (for search ads).

If the user says "just do it", pick sensible answers, state them in one line,
and continue.

## 2. Write

Google responsive search ads, per ad group (one theme of keywords each):
- 10 to 15 headlines of at most 30 characters: the keyword, the offer, proof, the action, the brand. Each must make sense on its own and in any order. Put a keyword in at least two. No exclamation marks, no all-capital words, no phone numbers (use a call asset).
- 4 descriptions of at most 90 characters: benefit, proof, offer with a reason to act now, the action.
- `path1` and `path2` up to 15 characters each (the words after the domain in the shown address).
- Keywords with match types (`phrase` by default, `exact` for the most valuable, `broad` sparingly) and negative keywords for searches you do not want (jobs, free, seeds, and so on). Pin only if a headline must always show (`"pins": {"1": "1"}` pins headline 1 to position 1).

Meta ads, 2 or 3 variants that differ in angle (offer, taste or quality, proof):
- Primary text: the point in the first 125 characters; headline about 40; description about 30.
- Never assert something about the reader's personal attributes ("Are you diabetic?", "other single parents"); talk about the product. No before-and-after or health claims.
- Button (`cta`): SHOP_NOW, ORDER_NOW, LEARN_MORE, SIGN_UP, BOOK_NOW, CONTACT_US, GET_OFFER, GET_QUOTE, SUBSCRIBE, DOWNLOAD, APPLY_NOW, SEND_WHATSAPP_MESSAGE, CALL_NOW, WATCH_MORE.
- An `image_brief` per ad.

Everywhere: every number comes from `facts` (the checker fails others); no
"guaranteed", "best in India" or "#1" without proof.

Write `ads.json`:

```json
{
  "brand": "Kesari",
  "facts": {"price": "boxes from ₹749", "delivery": "delivered in 48 hours"},
  "google": {"campaign": "Alphonso pre-orders", "ad_groups": [{
     "name": "Alphonso mango online", "final_url": "https://kesarifoods.in/preorder", "path1": "alphonso", "path2": "preorder",
     "keywords": [{"text": "buy alphonso mangoes", "match": "phrase"}], "negatives": ["plant", "seeds"],
     "headlines": ["Ratnagiri Alphonso Mangoes", "..."], "descriptions": ["...", "..."]}]},
  "meta": {"ads": [{"name": "Offer", "primary_text": "...", "headline": "...", "description": "...", "cta": "ORDER_NOW",
                    "url": "https://kesarifoods.in/preorder", "image_brief": "..."}]}
}
```

Set `allowed_caps` (for example `["GST", "UPI"]`) for acronyms that must stay in capitals.

## 3. Build, check, fix

```
python3 <skill dir>/scripts/build_ads.py ads.json --out ads
python3 <skill dir>/scripts/check_ads.py ads.json ads
```

Fix every FAIL and rebuild.

## 4. Deliver

Give the user `ads.md` to review, `google_ads.csv` and `google_keywords.csv`
to import in Google Ads Editor (Account, Import, From file), `meta_ads.csv` as
the brief for Meta Ads Manager, and the check result in one line ("Checked:
every headline under 30 characters, no exclamation marks or phone numbers,
Meta text fits phones, every number from your facts"). Offer a landing page
(`mangoman-landing-page`) or images (`mangoman-social-posts`) for the ads.
