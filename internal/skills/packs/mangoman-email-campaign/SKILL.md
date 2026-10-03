---
name: mangoman-email-campaign
description: Write and build a branded email campaign or sequence (welcome series, launch, offer, newsletter, abandoned cart, win-back) as ready-to-send HTML emails with plain-text versions, merge tags for the user's email tool, and a send schedule, checked before sending. Use when the user asks for marketing emails, an email campaign, newsletter, drip or nurture sequence, or emails for Mailchimp, Klaviyo, Brevo or MailerLite.
license: Apache-2.0
metadata:
  pack: mangoman
  version: "1.0"
---

# Email campaign

You produce a sequence the user can paste into their email tool and send:
each email as HTML that works in Gmail, Outlook and Apple Mail, a plain-text
version, subject and preview text, and a schedule. The scripts in this skill's
`scripts/` folder render one tested design and check deliverability and law
basics, so your job is the message.

## 1. Understand (ask before writing)

Ask only what you do not already know, in one short message (use your
question tool if you have one):

1. **Goal and audience:** what should readers do, and who are they (new sign-ups, past buyers, cart abandoners, a cold list is not allowed).
2. **How many emails and over how long** (default 3 over about 10 days).
3. **Email tool:** Mailchimp, Klaviyo, Brevo, MailerLite or other (sets the merge-tag format).
4. **Facts to use:** offers, prices, codes, dates, deadlines, results, real quotes. You will use only these.
5. **Sender:** business name and postal address (required in every marketing email), reply-to.
6. **Brand:** "Attach your logo (PNG or JPG) and share brand colours, or say skip and I'll choose a look." Plus any product photos.

If the user says "just do it", pick sensible answers, state them in one line,
and continue. Never hold the work back waiting for a logo. Only write for
people who opted in; say so if the user describes a bought or scraped list.

## 2. Plan the sequence

One job per email, one main button. Common shapes:

| Sequence | Emails |
|---|---|
| Launch or pre-order | Announce (why now, the offer), Choose (options, proof), Last call (deadline, code) |
| Welcome | Welcome and promise, Best of (most loved products or content), First-order nudge |
| Win-back | We miss you, What is new, Final offer |
| Newsletter | One story, two or three short items, one action |

Subjects at most about 50 characters (phones cut at 35 to 40); preview text
that adds to the subject, 40 to 100 characters. Give two `subject_alternatives`
to A/B test.

## 3. Write

- **Never invent facts.** Every price, discount, date, number and quote comes from the user. Put them in `facts`; the checker fails numbers that are not there.
- Short paragraphs, plain words, the reader's benefit first. Use `{first_name}` for personalisation; a fallback ("there") is added where the tool supports it.
- No spammy wording ("act now", "FREE!!!", "you have won"), no all-caps words in subjects, at most one exclamation mark and one emoji in a subject.
- Every link is a real https address; `**bold**` and `[link text](https://...)` work inside text.
- Every image needs alt text (many readers have images off); keep a healthy amount of text.

Write `campaign.json`:

```json
{
  "name": "2027 season pre-orders", "goal": "Pre-orders from past customers", "audience": "2026 buyers",
  "esp": "mailchimp",
  "brand": {"name": "Kesari Foods", "logo": "logo.png"}, "theme": "clay",
  "sender": {"name": "Kesari Foods", "legal_name": "Kesari Foods Pvt Ltd", "address": "12 Market Road, Ratnagiri 415612, India"},
  "assets_base_url": "https://kesarifoods.in/email",
  "facts": {"discount": "10% off pre-orders", "deadline": "pre-orders close 28 February 2027"},
  "emails": [
    {"id": "preorders-open", "send_day": 0, "subject": "Your Alphonso box, before anyone else",
     "preview": "Pre-orders for 2027 are open, with 10% off for returning customers.",
     "subject_alternatives": ["Pre-orders are open, {first_name}"],
     "blocks": [
       {"type": "image", "src": "hero.jpg", "alt": "A crate of ripe mangoes", "full": true, "href": "https://kesarifoods.in/preorder"},
       {"type": "heading", "text": "Hi {first_name}, the season is almost here"},
       {"type": "text", "text": ["First paragraph with **10% off**.", "Second paragraph."]},
       {"type": "button", "label": "Pre-order my box", "href": "https://kesarifoods.in/preorder"}
     ]}
  ]
}
```

Blocks: `heading` (text, level 1 to 3), `text` (one or more paragraphs),
`button` (label, href), `image` (src, alt, href, full), `list` (items),
`products` (items: name, price, image, alt, href, cta; two per row on desktop,
stacked on phones), `quote` (text, author), `coupon` (code, label, note),
`divider`, `spacer`. `send_day` counts days from the start (0 is day one);
`send_note` overrides the schedule wording. `esp`: mailchimp, klaviyo, brevo,
mailerlite or generic. Looks: `theme` and `type` (modern, editorial) as in the
other packs; with only a logo, colours are read from it (tell the user).

## 4. Build

```
python3 <skill dir>/scripts/build_emails.py campaign.json --out emails
```

Writes `emails/<id>.html` (paste into the tool's HTML or code editor),
`emails/<id>.txt` (plain-text part), `emails/sequence.md` (schedule, subjects,
preview text, A/B options), `emails/assets/` (images to upload) and
`emails/preview/` (desktop and phone screenshots with sample values).

## 5. Check, fix, re-check

```
python3 <skill dir>/scripts/check_emails.py campaign.json emails
```

Fix every FAIL and rebuild. Upload the images and set `assets_base_url` (or
use the tool's own image library) before sending. Look at the phone previews:
the point and the button visible without much scrolling.

## 6. Deliver

Give the user the HTML and text files, `sequence.md` and the previews, how to
load them into their tool in one or two lines (for Mailchimp: Campaign, Code
your own, Paste in code; Klaviyo: Template, HTML), and the check result in one
line ("Checked: unsubscribe and address in every email, under Gmail's clipping
size, links work, phone layout fits, every number from your facts"). Remind
them to send a test to themselves first.
