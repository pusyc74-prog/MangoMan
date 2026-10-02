---
name: mangoman-ceo-deck
description: Turn raw data or an analysis into a CEO or board-ready slide deck (PowerPoint, PDF and web) with an answer-first story and verified numbers. Use when the user asks for a presentation, deck, slides, board pack, monthly or quarterly review, or a CEO summary of data.
license: Apache-2.0
metadata:
  pack: mangoman
  version: "1.0"
---

# CEO deck

You build a short deck an executive can act on in five minutes. Two rules make
it work: **the answer comes first**, and **every number comes from code**
(an analysis script you write and run), never typed or estimated. The scripts
in this skill's `scripts/` folder build the slides in one tested design and
check the result.

## 1. Understand (ask before building)

If the request does not already say, ask in one short message (use your
question tool if you have one), at most:

1. **Who is the audience and what decision or meeting is it for?** (board review, monthly business review, a funding ask)
2. **What is the main question the deck must answer?** (for example "where did growth come from, and what should we fix?")
3. **Period, currency and number style** (₹ with lakh and crore, or $).

If the user says "just do it", choose sensible answers, state them in one line, and continue.

## 2. Get the numbers

With raw data: profile it first with the dashboard skill's profiler
(`mangoman-data-dashboard/scripts/profile.py <file>`), or read its columns
yourself. Fix data problems inside the analysis script and tell the user what
you fixed.

## 3. Find the story (before any slide)

Write down, for yourself:
- **The answer** in one sentence with its key number ("Revenue grew 3.4%, but East and returns need action").
- **Three supporting facts**, each with a number.
- **What should happen next**, with owners if the user can name them.

Structure (the pyramid principle): title, then the answer (slide 2), then one
slide per supporting fact (evidence), then next steps. Detail goes in an
appendix after a `section` slide. Aim for 6 to 12 slides.

## 4. Write `analysis.py` (prints the deck spec)

One Python script next to the data computes everything and **prints one JSON
spec** to stdout:

```json
{
  "title": "H1 FY27 sales review",
  "subtitle": "April to September 2026",
  "date": "October 2026",
  "source": "sales.csv, 18,275 orders",
  "facts": {"east_growth": -0.027, "aug_returns": 0.114},
  "slides": [
    {"type": "title"},
    {"type": "answer", "headline": "Revenue grew 3.4%, but East and returns need action",
     "points": ["Revenue reached ₹1.75 Cr, up 3.4%", "East shrank 2.7% while other regions grew",
                "Returns hit 11.4% in August"]},
    {"type": "kpis", "headline": "Revenue and order value up, order count flat",
     "kpis": [{"label": "Revenue", "value": 17500000, "format": "currency", "currency": "INR",
               "delta": 0.034, "delta_label": "vs previous half"}]},
    {"type": "chart", "headline": "East is the only region that shrank",
     "chart": {"type": "bar", "x": ["South", "North", "West", "East"],
               "series": [{"name": "Growth", "values": [0.059, 0.055, 0.011, -0.027]}], "format": "percent"},
     "takeaway": "Growth compares April to September with the previous six months."},
    {"type": "table", "headline": "Alphonso boxes bring 56% of revenue",
     "table": {"columns": ["Product", "Revenue", "Share"], "formats": ["text", "currency", "percent"],
               "currency": "INR", "rows": [["Alphonso box 12", 9788113, 0.559]]}},
    {"type": "bullets", "headline": "Why returns spiked in August", "bullets": ["..."]},
    {"type": "next_steps", "headline": "Three actions for the next quarter",
     "items": [{"action": "Review East distributor terms", "owner": "Sales head", "date": "Oct 31"}]}
  ]
}
```

Slide types: `title`, `answer` (up to 3 points), `kpis` (1 to 4), `chart`
(`line`, `bar`, `hbar`, `stacked`; same chart rules as the dashboard skill:
one axis, at most 4 series, `highlight` to make one the point), `table`,
`bullets` (up to 5), `next_steps`, `section`.

Writing rules:
- **Every headline is the takeaway as a sentence**, at most 16 words ("East is the only region that shrank", not "Regional growth").
- One message per slide. Bullets at most 16 words; no paragraphs.
- Every number in a headline, point, bullet or takeaway must be computed by the script. Numbers that appear only in text go in `facts`.
- Every claim must be true of the data ("the only region that shrank" needs exactly one negative region). Back claims with facts and reread them against the charts.

## 5. Build

```
python3 <skill dir>/scripts/build_deck.py analysis.py --out deck
```

Writes `deck.pptx` (editable, native charts and tables), `deck.pdf` (one slide
per page), `deck.html` and `deck.spec.json`. PowerPoint needs `python-pptx`
(`pip install python-pptx`); the PDF needs Chrome, Chromium or Playwright.

## 6. Check, fix, re-check

```
python3 <skill dir>/scripts/check_deck.py analysis.py deck
```

Fix every FAIL in `analysis.py` (never by editing the outputs) and rebuild
until there are none. Then look at the slides (open the PDF or screenshot the
HTML): headlines on one or two lines, charts readable, nothing crowded. Reread
each headline against its slide.

## 7. Deliver

Give the user the PowerPoint and PDF, the answer in one line, and the check
result in one line ("Checked: numbers reproduce from the data, every figure in
the text traces to it, no slide overflows"). Mention that the same analysis can
become an interactive dashboard (`mangoman-data-dashboard`).
