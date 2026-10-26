---
name: mangoman-social-research
description: Reads competitors' public Instagram, YouTube Shorts, TikTok, running Meta, Google and LinkedIn ads, TikTok trends and 30 days of Reddit and Hacker News talk, logged out, into citable numbers. Use for competitor research.
license: Proprietary. Free to use inside MangoMan only; no copying, changing or reselling (see the MangoMan LICENSE).
metadata:
  pack: mangoman
  version: "1.1"
---

# Social research

You find out what is working in the niche this week, from the accounts and
ads themselves, not from guesses. The scripts in this skill's `scripts/`
folder read public pages the way a visitor does (never logged in, a few
requests per account, with pauses) and compute the numbers, so your job is
to choose what to read and to say what it means.

## 1. Choose what to read

From the brief and the research so far: the business's own accounts, and 3
to 5 competitors' Instagram usernames, YouTube channel links and TikTok
profile links. If you only have names, search the web for their profiles
first. Read at most about 8 accounts in one run.

## 2. Read

```
python3 <skill dir>/scripts/read_social.py instagram <username> <username> --out social
python3 <skill dir>/scripts/read_social.py videos https://www.youtube.com/@brand https://www.tiktok.com/@brand --out social
python3 <skill dir>/scripts/read_social.py page "meta-ads:Brand Name@IN" "google-ads:brand.com@IN" "tiktok-trends@IN" --out social
python3 <skill dir>/scripts/read_social.py reddit "Brand Name" "mango pickle" --out social
python3 <skill dir>/scripts/read_social.py hackernews "Brand Name" --out social
```

- `instagram` reads the latest 12 posts (`--posts` up to 30): type (reel,
  image, carousel), date, caption, likes, comments, views, hashtags, and the
  followers.
- `videos` reads a YouTube channel's Shorts (or the tab you name) and a
  TikTok profile's videos: title, caption, date, views, likes, comments and
  sound.
- `page` saves a page's text and a screenshot. Shortcuts: `meta-ads:<name>`
  (ads running on Facebook and Instagram), `google-ads:<domain>` (Google and
  YouTube ads), `linkedin-ads:<name>`, `tiktok-trends` (this week's trending
  hashtags). Put the country code after `@` (default IN). Read the text and
  look at the screenshot yourself: which ads run, since when, the offer, the
  message, the format.
- `reddit` and `hackernews` search what people posted in the last 30 days
  about each brand or topic: thread titles, dates, upvotes (or points),
  comments and, on Reddit, the community. Search the brand, its competitors
  and the problem the product solves. Hacker News suits tech, software and
  startups; skip it for other niches.
- X (Twitter) cannot be read without a paid key. Use your web search for
  `site:x.com "Brand Name"` and cite the posts you find by link, with no
  counts.

Sites sometimes refuse a visitor (a login wall, "slow down", a bot check).
The file then says why, in plain words. Do not retry in a loop: a file read
in the last 20 hours is kept, and the script asks again only later. Never
log in, never use the owner's password, never read private accounts.

## 3. Summarise and check

```
python3 <skill dir>/scripts/summarise_social.py social --out social
python3 <skill dir>/scripts/check_social.py social
```

`social-research.md` gives, per account: posts a week, the format mix,
typical likes, comments and views, engagement against followers, the top
three posts with their first lines (their hooks), top hashtags and posting
days; then the pages read and what could not be read. Every number comes
from `social-research.json`, which the checker compares with the files: do
not type numbers in by hand. Fix every FAIL.

## 4. Use it

- Quote the numbers with the date read and the link, as sources: "Their
  reels got a typical 18,200 views (read 20 Oct 2026)". Treat them as what
  the site showed that day.
- Look for patterns, not single posts: which formats and topics get more
  than the account's typical engagement, which hooks the top posts open
  with, how often they post, which offers their ads push and how long the
  ads have run (long-running ads usually work).
- Say plainly what could not be read and ask the owner for links or
  screenshots of those accounts' recent posts and ads.
- From Reddit and Hacker News, take the words real people use, their
  complaints, questions and praise, and the communities where they talk.
  These feed hooks, content ideas and the FAQ. Quote a thread by its link.
- Never copy a competitor's post or ad; learn the pattern and make it the
  brand's own.
