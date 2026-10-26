---
name: mangoman-content-strategy
description: Content plan - goals, pillars, platform roles, a rhythm the team can keep, formats, hooks, a dated calendar, measures. Use for social media strategy, content pillars, calendars, posting schedules, what to post, growth.
license: MIT (adapted from coreyhaines31/marketingskills; see LICENSE.txt)
metadata:
  pack: mangoman
  kind: method
  version: "1.0"
---

# Content strategy

Content works when it tells the brand's real story to the right people in
the format each platform rewards, at a rhythm the team can actually keep.
Start from the research and the brand strategy; every choice below should
point back to something you found or the user told you.

## 1. Goals

Pick one main goal for the period and at most two more: awareness, people
visiting the shop or site, enquiries and leads, sales, repeat customers,
community. Each goal gets a measure (section 7). A goal with no measure is
a wish.

## 2. Content pillars (3 to 6)

Pillars are the topics the brand posts about again and again. Find them
where these overlap: what the audience cares about (research), what the
brand can say with authority (proof, people, process), and what moves the
goal. For each pillar give:

- a name people would understand,
- its **share** of posts (all shares add to 100; promotion is rarely more
  than a quarter unless the user asks for a sales push),
- **why** it works for this audience, citing the finding,
- at least 3 **post ideas** specific to this brand (not "share a tip").

## 3. Platforms

Use the platforms in the brief and the ones the research shows the audience
uses; say why for each. For every platform write its **role** (discovery,
trust, conversion, reminders, community), **how often** (what the user's
time allows: a smaller rhythm kept beats a bigger one dropped), the
**formats** it rewards (short video, carousels, single images, text posts,
stories, threads, status updates, live) and its tone shift.

Check current limits and specs on the platform's own help pages when they
matter (caption length, sizes, hashtag behaviour); they change.

## 4. Formats and hooks

- Lead with the point. The first line or the first second decides whether
  anyone reads or watches on.
- One idea per post. If a cover promises five tips, give five.
- Short video: show the payoff in the first seconds, add on-screen text and
  subtitles (most people watch without sound), end with one clear action.
- Carousels: a cover that names the payoff, one step per slide, a last
  slide with the action.
- Hooks that work when they carry a specific fact or story: a surprising
  detail, a before and after, a mistake and the lesson, a how-to with a
  clear outcome. A hook formula filled with nothing specific is noise.
- Repurpose: one strong piece (a video, an article, a review, an event)
  becomes several posts across platforms, each written to stand alone.

## 5. Writing that does not sound generated

Never: "it's not X, it's Y" reveals, "no X, no Y, no Z" lists, self-answered
questions ("The result? ..."), stock phrases ("unlock", "game-changer",
"elevate", "take it to the next level", "in today's fast-paced world"),
engagement bait ("Agree?", "Read that again"), one-sentence-per-line posts,
emoji as bullets, em dashes. Write the way the brand's best person would talk
to one customer, in the customer's language.

## 6. Calendar

- Dates in the plan's period, in the user's time zone, with the platform,
  format, pillar and the specific post idea for each.
- Rhythm: spread posts through the week; at most one feed post a day per
  platform unless the plan says otherwise; leave room for timely posts.
- Build around the user's own dates (launches, offers, events) and the
  local holidays and seasons found in research for their country.
- Every pillar appears, in roughly its share.
- Plan a full month in detail; give later months as themes if the period is
  longer.

## 7. Engagement and measurement

- A daily routine the owner can keep (for example, 15 to 30 minutes): reply
  to every comment and message, comment usefully on posts of people and
  accounts the audience follows, share customers' posts with credit.
- Measures per goal, from the platform's own insights: reach and new
  followers (awareness), saves, shares and comments (engagement), profile
  and link visits, direction taps, calls, messages, code redemptions
  (action). Set targets as plans, say how and when each is checked, and
  review monthly: keep what works, change one thing at a time.

## Output

For the strategy report: `strategy` (goals and the plan in a few lines),
`pillars` (a `pillars` block), `platforms` (a `table`: platform, role, how
often, formats), `calendar` (a `calendar` block), `measure` (a `kpis` block).
For posts to publish, hand the calendar's first posts to the
`mangoman-social-posts` skill to make the images and captions.
