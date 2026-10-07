---
name: mangoman-seo-article
description: Research and write an SEO blog article (with meta title, description, address, FAQ and structured data) that is accurate, cited and easy to read, delivered as a page and as text ready to paste into WordPress, Shopify or Webflow, checked before publishing. Use when the user asks for a blog post, SEO article, content for their website, a guide or how-to article, or meta titles and descriptions.
license: Proprietary. Free to use inside MangoMan only; no copying, changing or reselling (see the MangoMan LICENSE).
metadata:
  pack: mangoman
  version: "1.0"
---

# SEO article

You write an article that answers the searcher's question better than the
pages already ranking, with every fact backed. The scripts in this skill's
`scripts/` folder render it with the search settings and structured data and
check it, so your job is research and writing.

## 1. Understand (ask once, in one message)

1. **Topic and main search phrase** (the keyword), and who is searching (beginner, buyer, expert).
2. **What the business wants the reader to do next** (product, booking, sign-up) and the site address.
3. **Facts and experience** the user can add (their numbers, methods, examples): first-hand detail is what ranks.
4. **Length** (default 800 to 1,200 words) and language.

If the user says "just do it", pick sensible answers, state them in one line,
and continue.

## 2. Research

If you can search the web: search the keyword, read the top results, and note
what they cover and miss, the questions people also ask, and trustworthy
sources (official, research, well-known publishers). Never copy wording.
Without web access, write from the user's facts and say the article has no
outside sources.

## 3. Write

- **Answer first:** the opening says what the reader will get; each section answers one question.
- **Every number** is in `facts` (from the user) or in a sentence that cites a source as `[1]`; the checker fails others. Never invent statistics, studies or quotes.
- Keyword in the title, h1, first 100 words, description, at least one h2 and the address, naturally; use the secondary keywords and related words; no stuffing.
- Short sentences (about 15 words on average) and paragraphs (under about 100 words); subheadings every 200 to 300 words; lists where steps or options are listed.
- Link to 2 or more of the user's own pages (`[text](/page)`) and cite sources.
- `meta.title` 30 to 60 characters, `meta.description` 120 to 160, `meta.slug` short (lowercase words joined by dashes).
- FAQ: 3 to 6 real questions with answers under 60 words.

Write `article.json`:

```json
{
  "keyword": "real alphonso mango", "secondary_keywords": ["hapus mango"],
  "author": "Kesari Foods team", "date": "2027-02-10", "target_words": 900, "language": "en",
  "meta": {"title": "How to Spot a Real Alphonso Mango: 5 Simple Checks", "description": "...", "slug": "real-alphonso-mango-checks",
           "url": "https://kesarifoods.in/blog"},
  "facts": {"ripening": "ripens in 7 to 10 days in hay"},
  "sources": [{"id": 1, "title": "Alphonso (mango)", "url": "https://en.wikipedia.org/wiki/Alphonso_(mango)", "publisher": "Wikipedia"}],
  "article": {
    "h1": "How to spot a real Alphonso mango",
    "intro": ["..."],
    "sections": [{"h2": "...", "paragraphs": ["... a fact from a source [1]."], "bullets": ["..."],
                  "subsections": [{"h3": "...", "paragraphs": ["..."]}]}],
    "faq": [{"q": "...", "a": "..."}],
    "conclusion": ["..."],
    "cta": {"label": "See this season's boxes", "href": "https://kesarifoods.in/preorder"}
  },
  "images": [{"after": "intro", "src": "https://...", "alt": "...", "caption": "..."}]
}
```

Text accepts `**bold**`, `[link](https://...)` and `[1]` citations. Images go
after `intro` or a section's h2 text.

## 4. Build, check, fix

```
python3 <skill dir>/scripts/build_article.py article.json --out article
python3 <skill dir>/scripts/check_article.py article.json article
```

Fix every FAIL; fix warnings unless there is a reason not to.

## 5. Deliver

Give the user `article.md` (to paste into their CMS), `seo.md` (the title,
description and address to set), `article.html` (a full page with structured
data) and the preview, and the check result in one line ("Checked: every
figure sourced or from your facts, keyword in the title, opening and a
subheading, 950 words, readable on a phone"). Offer social posts or an email
announcing the article.
