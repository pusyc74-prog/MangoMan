---
name: mangoman-strategy-report
description: A designed, checked strategy report (HTML, PDF, Markdown, calendar CSV) for social, marketing or search. Use when a super agent delivers its strategy, or for a strategy document or plan report.
license: Proprietary. Free to use inside MangoMan only; no copying, changing or reselling (see the MangoMan LICENSE).
metadata:
  pack: mangoman
  kind: pack
  version: "1.0"
---

# Strategy report

You write the content as `report.json`; the scripts lay it out and check it.
The report is what the user keeps and acts on, so it must be specific to
their business, sourced, and honest about what is a fact and what is a plan.

## report.json

```json
{
  "kind": "social",
  "title": "Social media strategy",
  "for": "Brand name from the brief",
  "date": "2026-10-16",
  "period": "November 2026 to January 2027",
  "language": "en",
  "brand": {"name": "Brand", "logo": "logo.png", "primary": "#1E3A2F", "accent": "#F2A516"},
  "facts": {"price": "Box of 12 at 1,299", "followers": "Instagram 2,140 followers on 14 Oct 2026"},
  "sources": [
    {"id": "s1", "title": "Competitor X on Instagram", "url": "https://www.instagram.com/x/", "found": "Posts about 4 times a week, mostly Reels of recipes; 18k followers"}
  ],
  "summary": ["3 to 6 points: the decisions the owner should take away"],
  "sections": [
    {"id": "competitors", "title": "What competitors do", "intro": "One line on why this matters.", "blocks": [
      {"type": "cards", "items": [{"title": "Competitor X", "tag": "Strongest", "text": "Reels of recipes, 4 a week [s1].", "points": ["What to borrow", "What to avoid"]}]}
    ]}
  ]
}
```

`kind` is `social`, `marketing` or `seo`. Each needs these section ids (any
order, add more if useful):

| kind | sections |
|---|---|
| social | research, competitors, audience, strategy, voice, pillars, platforms, calendar, measure |
| marketing | market, competitors, audience, positioning, swot, channels, plan, measure |
| seo | audit, competitors, keywords, content, technical, measure |

## Blocks

| type | fields | use for |
|---|---|---|
| `text` | `text` (paragraphs split by a blank line) | reasoning, context |
| `points` | `items` | findings, actions |
| `table` | `columns`, `rows` | platform plans, comparisons, budgets |
| `cards` | `items`: `title`, `tag`, `text`, `points` | competitors, personas, channels |
| `pillars` | `items`: `name`, `share` (percent), `why`, `ideas` | content pillars (section `pillars`); shares add to 100 |
| `voice` | `traits`: `trait`, `means`, `do`, `dont`; `samples` | brand and content tone (section `voice`) |
| `swot` | `strengths`, `weaknesses`, `opportunities`, `threats` | section `swot` |
| `kpis` | `items`: `metric`, `target`, `how`, `when` | section `measure` |
| `timeline` | `items`: `when`, `what`, `detail` | section `plan`, launch steps |
| `issues` | `items`: `priority` (high, medium, low), `issue`, `where`, `why`, `fix` | section `audit`, technical fixes |
| `keywords` | `items`: `keyword`, `intent`, `page`, `priority`, `note` | section `keywords` |
| `budget` | `total` (a number the user gave, also in `facts`), `currency`, `items`: `channel`, `share` (percent), `why` | a budget split; amounts are computed |
| `calendar` | `posts`: `date` (YYYY-MM-DD), `platform`, `format`, `pillar`, `idea` | section `calendar`, up to 3 months |

## Rules the checker enforces

- **Cite the research.** Every claim about the market, competitors, audience,
  search results or the user's own pages carries a citation like `[s1]` to a
  source you actually opened, with `found` saying what is there (copy the
  numbers you saw). If you had no web access, set top-level `"no_web": "why"`;
  the report then says so on its first page.
- **Numbers are facts or findings.** A number in the writing must be in
  `facts` (from the user) or in a source's `found`. Targets (`target`), dates
  and pillar shares are the plan's own and are allowed. Never estimate
  follower counts, search volumes, market sizes or prices.
- **Pillars** 3 to 6, adding to 100%, each with at least 2 post ideas; every
  calendar post names one of them.
- **Targets** say how they are measured (`how`) and by when.
- **Plain, specific writing.** No placeholders, no promises the facts cannot
  back ("guaranteed", "best in the world"), no stock phrases ("unlock the
  power", "game-changer", "it's not X, it's Y"), no em dashes.

## Build and check

```
python3 <skill dir>/scripts/build_report.py report.json --out report
python3 <skill dir>/scripts/check_report.py report.json report
```

Fix every FAIL in `report.json`, build again and re-check. Then open
`report.html` and read it as the owner would: is every part specific to this
business, and could they act on it on Monday?

Deliver `report.html` and `report.pdf` (and `report-calendar.csv` when there
is a calendar), with a three-line summary of the strategy and the check
result in one line.
