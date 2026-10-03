---
name: mangoman-social-posts
description: Create a branded set of social media posts (images in the right size for each platform, captions, hashtags and alt text) for Instagram, LinkedIn, Facebook, X, Threads or WhatsApp status, checked before delivery. Use when the user asks for social media posts, a content calendar's creatives, Instagram or LinkedIn posts, a carousel, stories, or a launch or festive campaign.
license: Apache-2.0
metadata:
  pack: mangoman
  version: "1.0"
---

# Social media posts

You produce a ready-to-post set: images at the correct size for each platform,
a caption per platform, hashtags and alt text. The scripts in this skill's
`scripts/` folder draw every image in one tested design per layout and check
the set, so your job is the message: clear, specific, true.

## 1. Understand (ask before writing)

Ask only what you do not already know, in one short message (use your
question tool if you have one):

1. **What is this for?** The product, offer or event, and the one action you want people to take.
2. **Platforms:** Instagram, LinkedIn, Facebook, X, Threads, WhatsApp status (and whether to add Instagram/Facebook stories).
3. **How many posts** (default 5) and over what period.
4. **Facts to use:** prices, dates, numbers, results, real customer quotes. You will use only these.
5. **Brand:** "Attach your logo (PNG or JPG) and share brand colours and your handle, or say skip and I'll choose a look."
6. **Language and tone:** English, Hindi or Hinglish; friendly, premium, playful or formal.

If the user says "just do it", pick sensible answers, state them in one line,
and continue. Never hold the work back waiting for a logo.

## 2. Plan the set

Mix layouts so the grid does not repeat. A good set of 5: one `statement`,
one `stat` or `quote` (only with a real number or a real customer quote), one
`list` or `carousel` (useful content people save), one `announce` (the offer
or launch), one `photo` (when the user gives a photo).

| Layout | Use it for | Needs |
|---|---|---|
| `statement` | A bold message or opening | `headline`, optional `sub` |
| `stat` | One number people should remember | `value` (text, e.g. "41%"), `headline`, optional `sub`, `source` |
| `list` | Tips or steps (2 to 5) | `headline`, `items`, `ordered` (default true) |
| `quote` | A real customer or founder quote | `quote`, `author`, optional `role` |
| `announce` | Launch, offer, event | `headline`, optional `label`, `details` (date, place, price), `cta` |
| `photo` | The user's own photo with a line of text | `image` (path), `headline`, optional `sub` |
| `carousel` | A swipeable guide (Instagram, LinkedIn) | `slides`: a `cover`, 1 to 8 `content` (`headline`, `body`), an `end` (`headline`, `cta`) |

Sizes are chosen per platform: Instagram, Facebook, LinkedIn and Threads get
1080 x 1350 portrait, X gets 1600 x 900, WhatsApp status and stories get
1080 x 1920 with the top and bottom 250 px kept clear. Set `formats` on a post
to override (`square`, `portrait`, `story`, `landscape`, `link`).

## 3. Write

Rules (these are what make posts perform and keep the user safe):
- **Never invent facts.** Every number, price, date, result or quote must come from the user. Put them in `facts`; the checker fails any number that is not there.
- **One idea per image.** Headlines at most about 10 words; the image is read in a second.
- **Hook first.** Instagram shows about 125 characters before "more"; LinkedIn about 210. Lead with the point.
- **Captions per platform:** Instagram and Facebook warm and short with a clear action; LinkedIn plain, professional, a little longer; X under 280 characters; WhatsApp very short.
- **Links:** Instagram captions do not make links clickable; say "link in bio".
- **Hashtags:** 3 to 8 relevant ones (1 on Threads, 1 or 2 on X); one word each; no banned or unrelated trending tags.
- **No risky claims** ("guaranteed", "cures", "best in India", "100% safe") unless the user can prove them; the checker warns.
- If the cover says "five tips", have five.
- **Alt text** for every post: what the image shows and says, in one or two sentences.

Write `posts.json`:

```json
{
  "brand": {"name": "Kesari Foods", "handle": "@kesarifoods", "logo": "logo.png", "primary": "#2B1710", "accent": "#D62839"},
  "theme": "clay", "motif": "orb", "type": "modern",
  "platforms": ["instagram", "linkedin", "x"],
  "stories": true,
  "language": "en",
  "facts": {"repeat_rate": "41% of customers ordered again", "launch": "12 Oct 2026", "price": "₹1,299 for a box of 12"},
  "posts": [
    {"id": "pulp-launch", "layout": "announce", "title": "Pulp launch", "label": "New",
     "headline": "Kesari mango pulp is here", "details": ["Launching 12 Oct 2026", "₹1,299 for a box of 12"],
     "cta": "Pre-order now",
     "caption": {"default": "Our first mango pulp launches on 12 Oct 2026. Pre-orders open today, link in bio.",
                 "x": "Kesari mango pulp launches 12 Oct 2026. Pre-orders open today."},
     "hashtags": ["MangoPulp", "NewLaunch"],
     "alt": "Announcement poster: Kesari mango pulp launching 12 Oct 2026, 1,299 rupees for a box of 12."}
  ]
}
```

`caption` is one string for every platform or an object with `default` and
per-platform entries. `brand` fields are all optional: with only a logo,
colours are read from it (the build prints them; tell the user). Looks:
`theme` (ink, forest, coral, ocean, plum, emerald, royal, nordic, wine, clay,
graphite, cobalt), `motif` (orb, rings, dots), `type` (modern, editorial,
classic). Pick from the brand's mood, not the same one every time.

## 4. Build

```
python3 <skill dir>/scripts/build_posts.py posts.json --out posts
```

Writes the images (`<id>-<size>.png`; carousels `<id>-<size>-01.png` and so on,
plus `<id>-carousel.pdf` to upload to LinkedIn as a document), `captions.md`
(every caption ready to paste, with the file to use), and `preview.png`. Images
need Playwright (`pip install playwright`) or Chrome.

## 5. Check, fix, re-check

```
python3 <skill dir>/scripts/check_posts.py posts.json posts
```

Fix every FAIL in `posts.json` and rebuild. When text had to shrink, shorten
it rather than accept small type. Then look at `preview.png`: one clear message
per image, nothing cramped, the set looks varied.

## 6. Deliver

Give the user the images, `captions.md` and `preview.png`, a one-line summary
of the set, and the check result in one line ("Checked: every size correct,
text fits, captions within each platform's limit, every number from your
facts"). Offer a posting schedule or more posts in the same look.
