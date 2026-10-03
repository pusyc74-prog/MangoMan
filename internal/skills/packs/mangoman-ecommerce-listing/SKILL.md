---
name: mangoman-ecommerce-listing
description: Write or optimise product listings for Amazon (India or US), Flipkart, Meesho and Shopify (title, bullets, description, backend search terms, A+ content, SEO fields and an image plan), exported as ready-to-paste text and import CSVs and checked against marketplace rules. Use when the user asks for an Amazon listing, product title or bullets, a Flipkart or Meesho listing, a Shopify product page, listing optimisation, or keywords for a product.
license: Apache-2.0
metadata:
  pack: mangoman
  version: "1.0"
---

# E-commerce listing

You produce listings that rank and convert, and that the marketplace will not
suppress. The scripts in this skill's `scripts/` folder export the copy in
each marketplace's shape and check it against the current rules, so your job
is the copy: accurate, specific, written for shoppers.

## 1. Understand (ask before writing)

Ask only what you do not already know, in one short message (use your
question tool if you have one):

1. **The product facts:** what it is, size or quantity, materials or ingredients, variants, what is in the box, certifications, how to use and care for it. Ask for the label or spec sheet if there is one.
2. **Marketplaces:** Amazon India, Amazon US, Flipkart, Meesho, Shopify.
3. **Keywords:** what shoppers type. Best sources: the marketplace search box suggestions, the seller's search query report or Brand Analytics, and competitors' titles. If you can search the web, look at the search suggestions yourself and say where the words came from.
4. **Price** (and MRP for India), plus the main competitors (their names stay out of the copy).
5. **Photos** the user has, or that the image plan should describe.

For Amazon India, Flipkart and Meesho, also collect what Indian e-commerce
rules require on a listing: net quantity, MRP, maker or packer name and
address, country of origin, customer care contact; for food, the FSSAI licence
number and shelf life.

If the user says "just do it", write from what you have, state your
assumptions in one line, and list the facts you still need.

## 2. Write

Rules (the checker enforces the hard ones):
- **Never invent facts.** Every number, size, percentage, count and claim comes from the product facts; the checker fails numbers that are not there.
- **No health, purity or medical claims** ("boosts immunity", "cures", "100% natural", "chemical-free", "clinically proven") unless the user has proof, and even then marketplaces often reject them. No promotional words in titles ("best seller", "sale", "free delivery", "#1").
- **Never name a competitor** anywhere.

Amazon:
- `item_name` up to 75 characters: brand, product type, the key feature and size ("Kesari Alphonso Mango Pulp from Ratnagiri, No Added Sugar, 850 g"). `highlights` up to 125 characters: comma-separated differentiators. The full title is both, at most 200 characters (125 for clothing). No `! $ ? _ { } ^ ¬ ¦`; no word more than twice.
- Phones show only the first 70 to 80 characters in search: put the primary keyword and the deciding detail there.
- 5 bullets, 150 to 250 characters each (500 at most): a short label then the benefit, backed by a fact. No prices, offers or shipping promises.
- Description up to 2,000 characters; A+ modules as headline, text and an image brief.
- Backend `search_terms` up to 249 bytes: synonyms, other spellings and other languages in Latin letters (hapus, aamras), separated by spaces; no brands, no ASINs, no repeats, no words already in the title.

Flipkart and Meesho: short title (brand, product, key attribute, size), 4 to 8
key features, a plain description. Shopify: title, a description in short
paragraphs plus `features`, `seo_title` up to 70 characters and
`seo_description` around 155 characters (320 at most), tags, price and an
optional higher compare-at price.

Image plan (6 to 9 images): the main image on pure white with the product
filling about 85% of the frame, then features, how to use, ingredients or
materials, size and scale, what is in the box, lifestyle. Give each an alt text.

Write `listing.json`:

```json
{
  "brand": "Kesari",
  "product": {"name": "Alphonso mango pulp, 850 g", "food": true, "apparel": false,
              "net_quantity": "850 g", "mrp": "₹349", "manufacturer": "Kesari Foods Pvt Ltd, Ratnagiri",
              "country_of_origin": "India", "customer_care": "care@kesarifoods.in", "fssai_license": "11523999000123", "shelf_life": "18 months"},
  "facts": {"sugar": "no added sugar", "pack": "850 g can, about 3.5 cups"},
  "keywords": {"primary": ["alphonso mango pulp"], "secondary": ["aamras", "hapus mango pulp"], "source": "Amazon search suggestions"},
  "competitors": ["Other Brand"],
  "marketplaces": ["amazon_in", "flipkart", "shopify"],
  "amazon": {"item_name": "...", "highlights": "...", "bullets": ["...", "..."], "description": "...", "search_terms": "...",
             "aplus": [{"module": "image and text", "headline": "...", "text": "...", "image_brief": "..."}]},
  "flipkart": {"title": "...", "key_features": ["..."], "description": "..."},
  "shopify": {"title": "...", "handle": "alphonso-mango-pulp-850g", "price": 299, "compare_at_price": 349, "sku": "KES-PULP-850",
              "seo_title": "...", "seo_description": "...", "description": ["..."], "features": ["..."], "tags": ["..."]},
  "images": [{"slot": "main", "brief": "The can on a pure white background...", "alt": "...", "file": "main.jpg"}]
}
```

`images[].file` is optional: when given, the checker measures it (at least
1,000 px, main image white at the corners). `images[].url` (a hosted image)
goes into the Shopify import file.

## 3. Build

```
python3 <skill dir>/scripts/build_listing.py listing.json --out listing
```

Writes `listing.md` (every field ready to paste, with its character count and
limit), `amazon.csv`, `flipkart.csv`, `shopify_products.csv` (Shopify's
product import format), `image-plan.md` and `preview.png` (how the copy reads
at phone width; a neutral layout, not a marketplace page).

## 4. Check, fix, re-check

```
python3 <skill dir>/scripts/check_listing.py listing.json listing
```

Fix every FAIL and rebuild. Read the first 80 characters of the title as a
shopper scrolling on a phone: is it clear what this is and why to tap?

## 5. Deliver

Give the user `listing.md` (and the CSVs for bulk upload or Shopify import),
the image plan, the preview, the facts you still need, and the check result in
one line ("Checked: Amazon title, bullet and search-term limits, no banned
characters or repeats, no unproven claims, every number from your product
facts"). Offer social posts or a landing page for the same product.
