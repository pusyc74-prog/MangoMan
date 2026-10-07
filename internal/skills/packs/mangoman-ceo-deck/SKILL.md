---
name: mangoman-ceo-deck
description: Turn raw data or an analysis into a CEO or board-ready slide deck (PowerPoint, PDF and web) with an answer-first story and verified numbers. Use when the user asks for a presentation, deck, slides, board pack, monthly or quarterly review, or a CEO summary of data.
license: Proprietary. Free to use inside MangoMan only; no copying, changing or reselling (see the MangoMan LICENSE).
metadata:
  pack: mangoman
  version: "1.2"
---

# CEO deck

You build a short deck an executive can act on in five minutes. Two rules make
it work: **the answer comes first**, and **every number comes from code**
(an analysis script you write and run), never typed or estimated. The scripts
in this skill's `scripts/` folder build the slides in one tested design and
check the result.

The design: dark title, stat and closing slides frame light content slides;
one motif (a large soft circle with a small accent dot) on the dark slides;
big numbers carry the story; charts grey out everything except the category
the headline is about. PowerPoint and PDF are drawn from the same layout, so
they match.

## 1. Understand (ask before building)

If the request does not already say, ask in one short message (use your
question tool if you have one), at most:

1. **Who is the audience and what decision or meeting is it for?** (board review, monthly business review, a funding ask)
2. **What is the main question the deck must answer?** (for example "where did growth come from, and what should we fix?")
3. **Period, currency and number style** (₹ with lakh and crore, or $).
4. **Brand:** "Attach your logo (PNG or JPG) and share brand colours if you have them, or say skip and I'll choose a look."

Ask the brand question in the same message as the others, never as a separate
round. If the user attaches only a logo, the build reads the brand colours from
it and prints them; tell the user which colours you used so they can correct
them. If they skip, or say "just do it", choose sensible answers, state them in
one line, and continue. Never hold the deck back waiting for a logo.

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
  "theme": "ink", "motif": "orb", "mode": "contrast", "type": "modern",
  "brand": {"logo": "logo.png"},
  "facts": {"east_growth": -0.027, "aug_returns": 0.114},
  "slides": [
    {"type": "title"},
    {"type": "answer", "headline": "Revenue grew 3.4%, but East and returns need action",
     "points": ["Revenue reached ₹1.75 Cr, up 3.4%", "East shrank 2.7% while other regions grew",
                "Returns hit 11.4% in August"]},
    {"type": "kpis", "headline": "Revenue and order value up, order count flat",
     "kpis": [{"label": "Revenue", "value": 17500000, "format": "currency", "currency": "INR",
               "delta": 0.034, "delta_label": "vs previous half"}]},
    {"type": "stat", "value": 0.114, "format": "percent",
     "headline": "of August orders came back, against 4.7% in other months",
     "note": "optional one-line context"},
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

Slide types: `title`, `answer` (up to 3 points; each point's first number is
shown large beside it, or give `{"text": "...", "stat": "₹1.75 Cr"}`), `kpis`
(1 to 4), `stat` (one huge number with the headline as its sentence; use it
once or twice for the number the audience must remember), `chart` (`line`,
`bar`, `hbar`, `stacked`; same chart rules as the dashboard skill: one axis, at
most 4 series), `table` (up to about 8 rows; `highlight_row` by index or first
cell), `bullets` (up to 5), `next_steps` (up to 4, shown as numbered cards),
`section`.

### The look

Four independent choices, so two decks rarely look alike:

| Key | Options |
|---|---|
| `theme` | one of the 12 below, or leave it out when `brand` is given |
| `motif` | `orb` (large soft circle, default), `rings` (concentric outlines), `dots` (dot grid in the corner) |
| `mode` | `contrast` (dark title, stat and closing slides; default) or `light` (every slide light, calmer) |
| `type` | `modern` (Calibri, default), `editorial` (Cambria headlines, Calibri text), `classic` (Arial) |

| Theme | Colours and where it fits |
|---|---|
| `ink` | navy and saffron; finance, consulting, general business |
| `forest` | deep green and gold; agriculture, sustainability, banking |
| `coral` | slate and coral; consumer brands, marketing, startups |
| `ocean` | deep blue and aqua; health, logistics, travel |
| `plum` | plum and marigold; fashion, beauty, hospitality |
| `emerald` | teal and lime; climate, fintech, wellness |
| `royal` | indigo and pink; media, education, creative |
| `nordic` | slate and ice blue; technology, SaaS, engineering |
| `wine` | burgundy and champagne; luxury, real estate, wine and food |
| `clay` | earth and peach; food, crafts, retail, D2C |
| `graphite` | graphite and mint; data, AI, developer tools |
| `cobalt` | cobalt and sunflower; public sector, education, energy |

Pick the theme from the company's industry and mood, not `ink` every time.
Board and finance decks suit `contrast` with `modern` or `editorial`;
consumer, creative and internal decks often read better `light`.

**Brand:** `"brand": {"primary": "#2B1710", "accent": "#D62839", "logo": "logo.png"}`.
Every field is optional: with only a logo, colours are read from it; with only
`primary`, the accent comes from `theme`. The logo path is relative to
`analysis.py`. It appears on the title slide and small in every footer, on a
white chip where it would not show against the background. Brand colours are
adjusted only as far as needed for readable text and charts.

Chart emphasis: when the headline names one category ("East", "August"), that
bar or point is drawn in the accent colour and the rest in grey, and its value
appears large in the side panel. Set `"highlight_x"` to choose another
category, `false` to turn it off, or `"callout": {"label": "...", "value": n}`
to show a different number. Give a `takeaway` (one sentence) for the panel
when the chart needs context.

Writing rules:
- **Every headline is the takeaway as a sentence**, at most 16 words ("East is the only region that shrank", not "Regional growth").
- One message per slide. Bullets at most 16 words; no paragraphs.
- Vary the rhythm: after two or three light slides, a `stat` or `section` slide resets attention.
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
