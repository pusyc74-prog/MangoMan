---
name: mangoman-landing-page
description: Build a fast, mobile-first one-page website (landing page) for a business, product, launch or event, with the user's brand, a lead form that sends to WhatsApp, email or a form service, and search and sharing tags, checked on phone, tablet and desktop before delivery. Use when the user asks for a landing page, a one-page website, a product or launch page, a page for an ad campaign, or a simple site for their business.
license: Apache-2.0
metadata:
  pack: mangoman
  version: "1.0"
---

# Landing page

You produce a one-page site the user can publish today: a folder with
`index.html` and its images, no build step, no outside requests. The scripts
in this skill's `scripts/` folder render one tested, responsive design and
check it at phone, tablet and desktop sizes, so your job is the page's
argument: who it is for, what they get, why to trust it, and one clear action.

## 1. Understand (ask before writing)

Ask only what you do not already know, in one short message (use your
question tool if you have one):

1. **What is it and who is it for?** The product, service or event, the customer, and the one action the page should get (order, book a call, sign up, visit).
2. **How should visitors reach them?** WhatsApp number, phone, email, or a form service (Formspree, Basin, Google Apps Script). WhatsApp is the default in India.
3. **Facts to use:** prices, offers, dates, numbers, customer quotes, cities served. You will use only these.
4. **Brand:** "Attach your logo (PNG or JPG) and share brand colours, or say skip and I'll choose a look."
5. **Photos** (product, team, place). Without photos the page uses a type-led design; never use stock pictures of other businesses.
6. **Address** the site will live at (for search and link previews), if they have one.

If the user says "just do it", pick sensible answers, state them in one line,
and continue. Never hold the work back waiting for a logo.

## 2. Plan the page

The first section is always the `hero`. Then pick only what the business
needs, usually 5 to 8 sections, in an order that answers the visitor's
questions: what is it, why believe it, how it works, what it costs, what
others say, objections (FAQ), and the action.

| Section | Use it for | Needs |
|---|---|---|
| `hero` | Promise and main action | `headline`, `sub`, `cta` {label, href}, optional `cta2`, `proof` (one line of evidence), `image` + `image_alt` |
| `logos` | Clients, press, cities served | `items` (names, or {name, logo}), optional `label` |
| `features` | Benefits (2 to 6) | `items` [{title, text}] |
| `steps` | How it works (a real sequence, 3 or 4) | `items` [{title, text}] |
| `stats` | Proof in numbers (2 to 4) | `items` [{value, label}] |
| `testimonials` | Real customer quotes (1 to 3) | `items` [{quote, author, role}] |
| `pricing` | Plans or packs (1 to 3) | `plans` [{name, price, period, features, cta, highlight, tag, note}] |
| `faq` | Objections and practical questions | `items` [{q, a}] |
| `gallery` | Product or place photos (3 or 6) | `images` [{src, alt}] |
| `text` | About, story, details | `headline`, `body` (paragraphs), optional `image`, `cta` |
| `cta` | The closing action, with or without a form | `headline`, `sub`, `form` or `cta` |

Every section takes `nav` (a short menu label; up to 4 appear in the menu),
`id` (the anchor, used in links like `#prices`) and `tone` (`light`, `tint`,
`dark`) to override the automatic rhythm.

Forms: `{"fields": [{"name", "label", "type", "required", "options"}], "submit", "intro",
"action": {"type": "whatsapp" | "email" | "url", "to": "+919812345678" | "hello@brand.in" | "https://formspree.io/f/..."}}`.
Field types: text, email, tel, number, date, textarea, select. WhatsApp and
email open the visitor's app with the details filled in, so no server is
needed; `url` posts to the form service.

## 3. Write

- **Never invent facts.** Every number, price, date, result and quote comes from the user. Put them in `facts`; the checker fails numbers that are not there.
- **Hero headline:** the outcome for the customer, at most about 10 words. The sub says how, in one or two sentences. On a phone the main button must be visible without scrolling; the checker fails it otherwise.
- **One main action** repeated (hero, menu, pricing, closing section). A second, softer action at most.
- **Specific beats clever:** "Delivered in 48 hours" beats "Lightning fast".
- **No risky claims** ("guaranteed", "best in India", "cures") without proof; no placeholder text.
- `meta.title` 30 to 60 characters (brand and offer); `meta.description` 70 to 160 characters.
- Alt text describes each image; buttons say what happens ("Order on WhatsApp", not "Submit").

Write `page.json`:

```json
{
  "brand": {"name": "Kesari Foods", "logo": "logo.png", "tagline": "Alphonso mangoes from Ratnagiri orchards."},
  "theme": "clay", "motif": "orb", "mode": "contrast", "type": "modern", "language": "en",
  "meta": {"title": "Kesari Foods | Ratnagiri Alphonso mangoes delivered",
           "description": "Hand-picked Alphonso mangoes from Ratnagiri, delivered in 48 hours across Pune, Mumbai and Bengaluru.",
           "url": "https://kesarifoods.in"},
  "contact": {"phone": "+91 98765 43210", "whatsapp": "+919876543210", "email": "hello@kesarifoods.in", "address": "Ratnagiri"},
  "social": [{"label": "Instagram", "url": "https://instagram.com/kesarifoods"}],
  "facts": {"delivery": "delivered in 48 hours", "price": "Boxes from ₹1,299", "boxes": "boxes of 6, 12 and 24"},
  "sections": [
    {"type": "hero", "headline": "Real Alphonso, picked this week in Ratnagiri",
     "sub": "Straight from partner farms to your door in 48 hours.",
     "cta": {"label": "Order on WhatsApp", "href": "https://wa.me/919876543210"},
     "cta2": {"label": "See prices", "href": "#prices"}, "image": "mangoes.jpg", "image_alt": "A crate of ripe Alphonso mangoes"},
    {"type": "cta", "id": "order", "headline": "Order your box",
     "form": {"fields": [{"name": "name", "label": "Your name"}, {"name": "phone", "label": "Phone", "type": "tel"}],
              "submit": "Send on WhatsApp", "action": {"type": "whatsapp", "to": "+919876543210"}}}
  ]
}
```

Looks: `theme` (ink, forest, coral, ocean, plum, emerald, royal, nordic, wine,
clay, graphite, cobalt), `motif` (orb, rings, dots), `mode` (`contrast`: dark
hero and closing section; `light`: tinted), `type` (modern, editorial,
classic). With only a logo, brand colours are read from it (the build prints
them; tell the user). Links: `#section-id`, `https://...`, `mailto:`, `tel:`
or `https://wa.me/<number>`.

## 4. Build

```
python3 <skill dir>/scripts/build_page.py page.json --out site
```

Writes `site/index.html` and `site/assets/` (images resized for the web,
logo, favicon), and next to the folder `preview-desktop.png`,
`preview-mobile.png` and `page.spec.json`. Previews need Playwright
(`pip install playwright`).

## 5. Check, fix, re-check

```
python3 <skill dir>/scripts/check_page.py page.json site
```

Fix every FAIL in `page.json` and rebuild. Then look at both previews: the
promise and the button are clear on the first phone screen, sections vary,
nothing looks cramped.

## 6. Deliver

Give the user the `site` folder (zip it), the two previews, a one-line summary
and the check result in one line ("Checked: works on phone, tablet and
desktop, button visible on the first phone screen, every number from your
facts, page weight 0.3 MB"). Explain publishing in one step: drag the folder
onto Netlify Drop (app.netlify.com/drop) for a free address, or upload it to
their existing hosting. Offer to connect a custom domain, add analytics, or
make ad and social creatives in the same look (`mangoman-social-posts`).
