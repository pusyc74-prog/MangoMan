---
name: mangoman-brand-kit
description: Create brand guidelines (a PDF covering logo use, colour palette with tested contrast, type and voice) plus colour tokens and CSS for websites and other MangoMan packs, from a logo or brand colours. Use when the user asks for brand guidelines, a brand book, a style guide, a brand kit, brand colours and fonts, or a tone-of-voice guide.
license: Proprietary. Free to use inside MangoMan only; no copying, changing or reselling (see the MangoMan LICENSE).
metadata:
  pack: mangoman
  version: "1.0"
---

# Brand kit

You turn a logo or a pair of colours into a usable brand system: a short
guide people follow and tokens tools use. The scripts in this skill's
`scripts/` folder derive a full palette with readable text colours, render the
guide and check every colour pair for contrast.

## 1. Collect (ask once, in one message)

1. **Logo** (PNG or JPG, the largest they have) and brand colours if they have them (without colours, they are read from the logo).
2. **Personality:** three words for how the brand should feel, who it talks to, and brands they admire or want to avoid sounding like.
3. **Type preference:** modern (clean sans), editorial (serif headlines) or classic.
4. Any extra colours they already use (with what for).

## 2. Write `brand.json`

```json
{
  "name": "Kesari Foods", "tagline": "Alphonso from Ratnagiri orchards, delivered to your door.",
  "logo": "logo.png", "primary": "#2B1710", "accent": "#D62839", "type": "modern",
  "extra_colors": [{"name": "Mango yellow", "hex": "#FFB703", "use": "Illustrations"}],
  "voice": {"personality": ["Warm", "Honest", "Rooted"],
            "we_are": ["..."], "we_are_not": ["..."], "do": ["..."], "dont": ["..."],
            "sample": {"headline": "...", "text": "..."}}
}
```

`primary` and `accent` are optional when a logo is given. Voice lines are
short and concrete ("Name the place and the season", not "Be authentic"); the
sample shows the voice in a real headline and two sentences.

## 3. Build, check, fix

```
python3 <skill dir>/scripts/build_brand.py brand.json --out brand
python3 <skill dir>/scripts/check_brand.py brand.json brand
```

Writes `brand-guide.pdf` (cover, logo, colour with contrast results, type,
voice), `brand-tokens.json` (colours, fonts, and a `use_in_packs` block to
paste into any other MangoMan pack's spec) and `brand.css` (CSS variables for
websites). When the accent is too light for button text, the guide adds a
deeper Button colour; tell the user to use it for buttons. A page cut off at
the A4 edge fails: shorten the voice lines. Fix every FAIL and rebuild.

## 4. Deliver

Give the user the guide, the tokens and CSS, the colours read from the logo
(so they can correct them), and the check result in one line ("Checked: every
text and background pair passes WCAG AA, voice complete, 5 pages"). From now
on, use the `use_in_packs` block for their decks, posts, pages and documents.
