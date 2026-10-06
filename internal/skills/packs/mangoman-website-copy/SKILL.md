---
name: mangoman-website-copy
description: Research and write the copy for a whole website (home, product or service pages, about, contact) with a sitemap, page titles, descriptions and buttons, delivered as Markdown per page plus a CSV for the CMS, checked before delivery. Use when the user asks for website content, site copy, web pages text, a sitemap, or "write my website".
license: Apache-2.0
metadata:
  pack: mangoman
  version: "1.0"
---

# Website copy

You write the words for every page of a small business website. The scripts in
this skill's `scripts/` folder check the facts, build the files and check the
copy, so your job is to understand the business, plan the pages and write.

## 1. Understand (ask once, in one message)

1. **What the business sells, to whom, and where** (city or area served).
2. **The one thing a visitor should do** on the site (order, call, book, visit).
3. **Pages wanted.** If the user does not say, propose home, one page per main product or service, about and contact.
4. **Facts**: prices, years, numbers of customers, delivery time, ratings, guarantees, the story of the founder. Put them in `facts.md`, one per line.
5. **Competitor sites** the user admires or fears, the **voice** (warm, formal, playful), and words to use or avoid.
6. **Site address** and language (English is checked fully; other languages skip the reading check).

## 2. Gate: are the facts enough? (never skip)

Save the user's facts as `facts.md` and run:

```
python3 <skill dir>/scripts/brief_gate.py facts.md --pages N
```

If it prints `FACTS TOO THIN`, send the questions it prints and write
nothing until the user answers. This holds even when the user says "just do
it": copy written on thin facts is invented copy. Run the gate again on the
new answers. Go on only after `PASS`.

## 3. Research

If you can search the web: read each competitor page and note what they claim
and what they leave out. Write the competitor's claims into `competitors`
(`claims`). They are for contrast only: the checker fails any five words
copied from them. Search the main keyword of each page and note the questions
people ask. Without web access, say the copy has no outside research.

## 4. Plan (a message plan, then a sitemap)

Before writing, decide for each page: its **goal**, one **keyword** (what a
customer would type), and its **button** (the same one or two labels across
the site). Give the home page the broad keyword and every other page its own.
Every page must be linked from at least one other page.

## 5. Write

- **Every number** comes from `facts` (the user's). The checker fails any other number. Never invent customers, awards, ratings, years or testimonials.
- **Promise first.** The hero says what the visitor gets in the first line. One hero per page; it holds the h1 and the keyword.
- Short sentences (about 15 words on average, 20 at most), plain words, the user's voice. No clichés ("world-class", "seamless", "unlock").
- No claim the facts do not prove ("best", "number one", "guaranteed", "100% pure").
- Do not copy or lightly reword a competitor's claim. Say what is true and specific about this business.
- `meta.title` 30 to 60 characters and `meta.description` 70 to 160, each with the keyword and unique per page.
- Every page ends with a `cta` section and has the same button label as the site's main action.
- Link pages to each other with `[text](/slug)` (`/` is the home page).
- Do not write testimonials unless the user gave real quotes.

Write `site.json` with sections from this list: `hero`, `text`, `features`,
`steps`, `stats`, `testimonials`, `faq`, `cta`. Every section has a `headline`.

```json
{
  "brand": "Kesari Foods", "url": "https://kesarifoods.in", "language": "English",
  "promise": "Naturally ripened Devgad Alphonso mangoes, shipped within 48 hours.",
  "voice": {"rules": ["Warm and plain"], "use": ["Devgad"], "avoid": ["cheap"]},
  "facts": {"box": "A box of 12 mangoes costs Rs 1,499 with delivery included."},
  "competitors": [{"name": "Rival", "url": "https://rival.example.com", "claims": ["one claim they make"]}],
  "pages": [{
    "slug": "boxes", "name": "Mango boxes", "goal": "Get the visitor to order a box", "keyword": "alphonso mango box price",
    "meta": {"title": "Alphonso Mango Box Price: Rs 1,499 for 12", "description": "..."},
    "cta": {"label": "Order a box", "href": "https://wa.me/910000000000"},
    "sections": [
      {"type": "hero", "headline": "...", "sub": "...", "proof": "...", "cta": {"label": "Order a box", "href": "..."}},
      {"type": "features", "headline": "...", "items": [{"title": "...", "text": "..."}]},
      {"type": "steps", "headline": "...", "items": [{"title": "...", "text": "..."}]},
      {"type": "stats", "headline": "...", "items": [{"value": "4.7", "label": "..."}]},
      {"type": "testimonials", "headline": "...", "items": [{"quote": "...", "author": "...", "role": "..."}]},
      {"type": "text", "headline": "...", "body": ["paragraph with a [link](/about)"]},
      {"type": "faq", "headline": "...", "items": [{"q": "...", "a": "..."}]},
      {"type": "cta", "headline": "...", "sub": "...", "cta": {"label": "...", "href": "..."}}
    ]
  }]
}
```

Text accepts `**bold**` and `[link](https://... or /slug)`.

## 6. Critique (at most 2 rounds)

Read the copy as a stranger. For each page ask: does the first line say what
I get? Is every claim backed by a fact? Would a competitor's page say the
same? Fix what fails, then stop. Do not polish past 2 rounds.

## 7. Build, check, fix

```
python3 <skill dir>/scripts/build_copy.py site.json --out site
python3 <skill dir>/scripts/check_copy.py site.json site
```

Fix every FAIL; fix warnings unless there is a reason not to.

## 8. Deliver

Give the user `site/copy/<page>.md` (one file per page), `site/pages.csv` (one
row per page for a CMS import), `site/sitemap.md`, and the check result in one
line ("Checked: every figure from your facts, nothing copied from competitors,
each page has its keyword, a button and a link in"). Send `pages.json` only
if they want the landing pages built next: the landing page pack can start
from it. Make a Word file only if the user asks.
