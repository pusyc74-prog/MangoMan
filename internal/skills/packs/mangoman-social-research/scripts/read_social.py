"""Read public social accounts and ad pages for competitor research, logged out.

Usage:
  python3 read_social.py instagram <username> [<username> ...] [--posts 12] [--out social]
  python3 read_social.py videos <YouTube channel or TikTok profile link> [...] [--posts 12] [--out social]
  python3 read_social.py page <link, shortcut or saved page file> [...] [--out social]
  python3 read_social.py reddit "<brand or topic>" [...] [--posts 12] [--out social]
  python3 read_social.py hackernews "<brand or topic>" [...] [--posts 12] [--out social]

Page shortcuts (country or region code after @, default IN):
  meta-ads:<name>[@IN]      ads running now on Facebook and Instagram (Meta Ad Library)
  google-ads:<domain>[@IN]  ads on Google and YouTube (Google Ads Transparency Center)
  linkedin-ads:<name>       ads on LinkedIn (LinkedIn Ad Library)
  tiktok-trends[@IN]        trending hashtags on TikTok this week (TikTok Creative Center)

Only public data, never logged in, a few requests per account with pauses,
each account at most once a day (a fresh file is kept). Writes one JSON file
per account or page into --out; a page also gets a screenshot. When a site
refuses, the file says why, so the research can say what it could not read.
Reddit and Hacker News are searched for the last 30 days, the way
last30days does it (github.com/mvanhorn/last30days-skill, MIT).
"""
import datetime as dt
import hashlib
import html
import itertools
import json
import os
import pathlib
import re
import sys
import time
import urllib.error
import urllib.parse
import urllib.request

MAX_POSTS = 30
FRESH_HOURS = 20
PAUSE = 3  # seconds between accounts, so the reading stays light
HASHTAG = re.compile(r"#(\w+)")

PAGES = {
    "meta-ads": "https://www.facebook.com/ads/library/?active_status=active&ad_type=all&country={region}&q={q}&search_type=keyword_unordered&media_type=all",
    "google-ads": "https://adstransparency.google.com/?region={region}&domain={q}",
    "linkedin-ads": "https://www.linkedin.com/ad-library/search?accountOwner={q}",
    "tiktok-trends": "https://ads.tiktok.com/business/creativecenter/inspiration/popular/hashtag/pc/en?period=7&region={region}",
}


def now():
    return dt.datetime.now(dt.timezone.utc)


def slug(s):
    """A file name for a target: readable, plus a short code of the whole
    target, so two names that read alike never share a file."""
    words = re.sub(r"[^a-z0-9]+", "-", s.lower()).strip("-")[:40].strip("-") or "page"
    return "%s-%s" % (words, hashlib.sha1(s.strip().encode("utf-8")).hexdigest()[:6])


def record(platform, account, url):
    return {"platform": platform, "account": account, "url": url, "read_at": now().isoformat(timespec="seconds"),
            "ok": True, "why": "", "profile": {}, "posts": []}


def failed(rec, why):
    rec.update(ok=False, why=why)
    return rec


def number(v):
    """A count as the site gave it, or None when hidden or missing."""
    return v if isinstance(v, int) and not isinstance(v, bool) and v >= 0 else None


# Instagram, through Instaloader (MIT), logged out.

def node(x, *paths):
    """A field from what Instagram already sent for the post. Instaloader's own
    properties fetch the post's page when a field is missing, one request per
    post; reading only what came with the list keeps the visit light."""
    for path in paths:
        d = x._node
        try:
            for k in path:
                d = d[k]
            return d
        except (KeyError, TypeError, IndexError):
            continue
    return None


def ig_post(x):
    video = bool(node(x, ("is_video",)))
    kind = node(x, ("__typename",))
    return {"url": "https://www.instagram.com/p/%s/" % x.shortcode, "date": x.date_utc.date().isoformat(),
            "type": "carousel" if kind in ("GraphSidecar", "XDTGraphSidecar") else "reel" if video else "image",
            "caption": x.caption or "",
            "likes": number(node(x, ("edge_media_preview_like", "count"), ("edge_liked_by", "count"), ("like_count",))),
            "comments": number(node(x, ("edge_media_to_comment", "count"), ("edge_media_to_parent_comment", "count"), ("comment_count",))),
            "views": number(node(x, ("video_view_count",), ("video_play_count",), ("play_count",))) if video else None,
            "hashtags": list(x.caption_hashtags), "sound": ""}


def ig_why(e):
    """Plain words for why Instagram did not give the account."""
    name = type(e).__name__
    if name == "ProfileNotExistsException":
        return "no public Instagram account with this name"
    if name == "TooManyRequestsException" or "429" in str(e) or "wait a few minutes" in str(e).lower():
        return "Instagram asked to slow down; try again in an hour, or add links to its posts by hand"
    if name in ("LoginRequiredException", "QueryReturnedForbiddenException") or "login" in str(e).lower() or "302" in str(e):
        return "Instagram showed a login wall for this account; add links to its posts or screenshots by hand"
    return "Instagram did not answer as expected (%s)" % str(e)[:160]


def instagram(name, n):
    import instaloader
    name = name.strip().lstrip("@").rstrip("/").split("/")[-1]
    rec = record("instagram", name, "https://www.instagram.com/%s/" % name)
    loader = instaloader.Instaloader(quiet=True, download_pictures=False, download_videos=False,
                                     download_video_thumbnails=False, download_comments=False, download_geotags=False,
                                     save_metadata=False, compress_json=False, max_connection_attempts=1)
    try:
        p = instaloader.Profile.from_username(loader.context, name)
        rec["profile"] = {"name": p.full_name, "followers": number(p.followers), "following": number(p.followees),
                          "posts": number(p.mediacount), "bio": p.biography or "", "link": p.external_url or ""}
        if p.is_private:
            return failed(rec, "the account is private")
        rec["posts"] = [ig_post(x) for x in itertools.islice(p.get_posts(), n)]
    except instaloader.exceptions.InstaloaderException as e:
        return failed(rec, ig_why(e))
    return rec


# YouTube Shorts and TikTok, through yt-dlp (Unlicense), logged out.

def video_url(url):
    """A YouTube channel link reads its Shorts unless the Videos tab is named;
    share-link extras (?si=...) are dropped."""
    u = url.strip()
    parts = urllib.parse.urlsplit(u)
    m = re.match(r"(/(?:@[^/]+|channel/[^/]+|c/[^/]+|user/[^/]+))(?:/(\w+))?/?$", parts.path)
    if parts.netloc.lower().endswith("youtube.com") and m:
        tab = m.group(2) if m.group(2) in ("videos", "shorts", "streams") else "shorts"
        return "https://www.youtube.com%s/%s" % (m.group(1), tab)
    return u.split("?")[0].rstrip("/") if "tiktok.com/@" in u else u


def is_video(url):
    """A link to one video, not to a channel's tab."""
    return bool(re.search(r"/shorts/|watch\?v=|/video/", url or ""))


def vid_post(v, platform):
    day = v.get("upload_date") or ""
    if not day and v.get("timestamp"):
        day = dt.datetime.fromtimestamp(v["timestamp"], dt.timezone.utc).strftime("%Y%m%d")
    text = v.get("description") or v.get("title") or ""
    sound = " - ".join(x for x in (v.get("artist") or v.get("creator") or "", v.get("track") or "") if x)
    page = v.get("webpage_url") or v.get("url") or ""
    return {"url": page, "date": "%s-%s-%s" % (day[:4], day[4:6], day[6:8]) if len(day) == 8 else "",
            "type": "short" if platform == "youtube" else "video", "caption": text,
            "likes": number(v.get("like_count")), "comments": number(v.get("comment_count")),
            "views": number(v.get("view_count")), "hashtags": HASHTAG.findall(text), "sound": sound,
            "title": v.get("title") or ""}


FEED = "https://www.youtube.com/feeds/videos.xml?channel_id="
AGENT = "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/124.0.0.0 Safari/537.36"


def fetch(url):
    """A public page's body, read the way a browser asks for it."""
    import ssl
    try:  # Python on a Mac may not see the system's certificates; requests brings certifi's
        import certifi
        ctx = ssl.create_default_context(cafile=certifi.where())
    except ImportError:
        ctx = None
    req = urllib.request.Request(url, headers={"User-Agent": AGENT, "Accept-Language": "en-US,en;q=0.9"})
    with urllib.request.urlopen(req, timeout=20, context=ctx) as r:
        return r.read().decode("utf-8", "replace")


def feed_dates(channel_id):
    """Upload dates from a YouTube channel's public feed (its latest 15
    uploads), for when each video's own page refuses a visitor."""
    import xml.etree.ElementTree as ET
    ns = {"a": "http://www.w3.org/2005/Atom", "yt": "http://www.youtube.com/xml/schemas/2015"}
    if not channel_id:
        return {}
    try:
        root = ET.fromstring(fetch(FEED + urllib.parse.quote(channel_id)))
    except Exception:
        return {}
    return {e.findtext("yt:videoId", "", ns): e.findtext("a:published", "", ns)[:10] for e in root.findall("a:entry", ns)}


def video_why(e):
    s = str(e)
    if "not a bot" in s or "Sign in" in s or "login" in s.lower():
        return "the site asked to sign in or to prove it is not a bot; add links to its videos by hand"
    if "429" in s or "Too Many" in s:
        return "the site asked to slow down; try again in an hour"
    if "Failed to parse JSON" in s or "Unable to extract" in s:
        return "the site sent a page the reader could not use, often a bot check; add links to its videos by hand"
    return "the site did not answer as expected (%s)" % s.replace("ERROR: ", "")[:160]


def videos(url, n):
    import yt_dlp
    url = video_url(url)
    platform = "tiktok" if "tiktok.com" in url else "youtube"
    rec = record(platform, url.split("/@")[-1].split("/")[0] if "/@" in url else url, url)
    opts = {"quiet": True, "no_warnings": True, "skip_download": True, "extract_flat": "in_playlist", "playlistend": n}
    try:
        with yt_dlp.YoutubeDL(opts) as y:
            info = y.extract_info(url, download=False)
            rec["profile"] = {"name": info.get("channel") or info.get("uploader") or info.get("title") or "",
                              "followers": number(info.get("channel_follower_count")), "posts": None, "bio": "", "link": ""}
            refused = 0
            for e in [e for e in info.get("entries") or [] if is_video(e.get("url") or e.get("webpage_url"))][:n]:
                full = e
                try:  # the list gives little; each video's page gives the date and the counts
                    full = y.extract_info(e.get("url") or e.get("webpage_url"), download=False) or e
                except yt_dlp.utils.DownloadError:
                    refused += 1
                rec["posts"].append(vid_post(full, platform))
            if refused:
                rec["note"] = ("%d of %d videos: only the list could be read, so titles and views (rounded as the list shows "
                               "them) but no date, likes or comments; the site refused their own pages" % (refused, len(rec["posts"])))
                dates = feed_dates(info.get("channel_id") or "") if platform == "youtube" else {}
                for p in rec["posts"]:
                    vid = p["url"].rstrip("/").split("/")[-1].split("=")[-1]
                    if not p["date"] and dates.get(vid):
                        p["date"] = dates[vid]
                if dates:
                    rec["note"] += "; dates added from the channel's public feed, which lists its latest 15 uploads"
    except yt_dlp.utils.DownloadError as e:
        return failed(rec, video_why(e))
    return rec


# Reddit and Hacker News: what people say about a brand or topic in the last
# 30 days, from the public search pages. The Reddit page reading follows
# last30days (MIT, Matt Van Horn): Reddit's own search page, since its JSON
# search refuses visitors without a key.

REDDIT = "https://www.reddit.com/svc/shreddit/search/?"
RD_UNIT = re.compile(r'<search-telemetry-tracker\b[^>]*?\bdata-faceplate-tracking-context="([^"]*)"')
RD_TIME = re.compile(r'<faceplate-timeago\b[^>]*?\bts="([^"]+)"')
RD_COUNT = re.compile(r'<faceplate-number\b[^>]*?\bnumber="(\d+)"[^>]*>[^<]*</faceplate-number>\s*(vote|comment)')
HN = "https://hn.algolia.com/api/v1/search?"


def talk_why(e, site):
    code = getattr(e, "code", None)
    if code == 429:
        return "%s asked to slow down; try again in an hour" % site
    if code in (401, 403):
        return "%s refused the visit, as it often does for cloud servers; try from a home connection, or search it yourself" % site
    return "%s did not answer as expected (%s)" % (site, str(e)[:160])


def nothing(rec, site):
    """A search that found nothing still read fine: no one posted about it."""
    rec["note"] += "; nothing on %s in the last 30 days, try other words" % site
    return rec


def reddit_posts(page):
    """Posts on one Reddit search page, in its order, with their date, upvotes
    and comments. None when the page is not a search page (a bot check)."""
    if 'view-events="search/view/no_results"' in page:
        return []
    units = []
    for m in RD_UNIT.finditer(page):
        try:
            ctx = json.loads(html.unescape(m.group(1)))
        except ValueError:
            continue
        post = ctx.get("post") if isinstance(ctx, dict) else None
        if isinstance(post, dict) and post.get("id") and (ctx.get("action_info") or {}).get("type") == "post":
            units.append((m.start(), ctx))
    if not units:
        return None
    posts = []
    for i, (start, ctx) in enumerate(units):
        chunk = page[start:units[i + 1][0] if i + 1 < len(units) else len(page)]
        sub = ctx.get("subreddit")
        sub = str(sub.get("name") or "") if isinstance(sub, dict) else ""
        pid = str(ctx["post"]["id"]).removeprefix("t3_")
        when = RD_TIME.search(chunk)
        counts = {}
        for n, kind in RD_COUNT.findall(chunk):
            counts.setdefault(kind, int(n))
        title = str(ctx["post"].get("title") or "")
        posts.append({"url": "https://www.reddit.com/%scomments/%s/" % ("r/%s/" % sub if sub else "", pid),
                      "date": when.group(1)[:10] if when else "", "type": "thread", "caption": title, "title": title,
                      "likes": counts.get("vote"), "comments": counts.get("comment"), "views": None,
                      "hashtags": [], "sound": "", "community": "r/" + sub if sub else ""})
    return posts


def reddit(words, n):
    q = urllib.parse.urlencode({"q": words, "type": "posts", "t": "month", "sort": "relevance"})
    rec = record("reddit", words, "https://www.reddit.com/search/?" + q)
    rec["profile"] = {"name": "Reddit search: " + words}
    rec["note"] = "likes are upvotes"
    try:
        posts = reddit_posts(fetch(REDDIT + q))
    except (urllib.error.URLError, OSError) as e:
        return failed(rec, talk_why(e, "Reddit"))
    if posts is None:
        return failed(rec, "Reddit showed a bot check instead of results; try later from a home connection, or search it yourself")
    rec["posts"] = posts[:n]
    return rec if posts else nothing(rec, "Reddit")


def about(hit, words):
    """A story is about the words when its title or link names them all; the
    search also matches story text, which brings in unrelated stories."""
    seen = re.sub(r"[^a-z0-9]+", " ", ("%s %s" % (hit.get("title") or "", hit.get("url") or "")).lower())
    return all(w in seen.split() or w in seen.replace(" ", "") for w in re.findall(r"[a-z0-9]+", words.lower()))


def hackernews(words, n):
    since = int((now() - dt.timedelta(days=30)).timestamp())
    rec = record("hackernews", words, "https://hn.algolia.com/?" + urllib.parse.urlencode({"q": words, "dateRange": "pastMonth"}))
    rec["profile"] = {"name": "Hacker News search: " + words}
    rec["note"] = "likes are points"
    q = urllib.parse.urlencode({"query": words, "tags": "story", "numericFilters": "created_at_i>%d" % since, "hitsPerPage": 100})
    try:
        hits = json.loads(fetch(HN + q))["hits"]
    except (urllib.error.URLError, OSError, ValueError, KeyError, TypeError) as e:
        return failed(rec, talk_why(e, "Hacker News"))
    rec["posts"] = [{"url": "https://news.ycombinator.com/item?id=%s" % h["objectID"], "date": (h.get("created_at") or "")[:10],
                     "type": "story", "caption": h.get("title") or "", "title": h.get("title") or "",
                     "likes": number(h.get("points")), "comments": number(h.get("num_comments")), "views": None,
                     "hashtags": [], "sound": "", "link": h.get("url") or ""} for h in hits if h.get("objectID") and about(h, words)][:n]
    return rec if rec["posts"] else nothing(rec, "Hacker News")


# Ad libraries and trend pages: the page's text and a screenshot, read by the
# model, so a change in the page's layout does not break the reading.

def page_url(target):
    if target.startswith(("http://", "https://", "file://")):
        return target
    if os.path.isfile(target):  # a page saved on this computer
        return pathlib.Path(os.path.abspath(target)).as_uri()
    kind, _, rest = target.partition(":")
    kind, _, region = kind.partition("@")  # tiktok-trends@IN
    q, _, r2 = rest.partition("@")  # meta-ads:Kesari Foods@IN
    if kind not in PAGES:
        sys.exit("unknown page %r: give a link or one of %s" % (target, ", ".join(PAGES)))
    return PAGES[kind].format(q=urllib.parse.quote(q.strip()), region=(region or r2 or "IN").upper())


def page(target, out):
    import render
    from playwright.sync_api import sync_playwright
    url = page_url(target)
    rec = record("page", target, url)
    shot = slug(target) + ".png"
    try:
        with sync_playwright() as p:
            browser = render.launch(p)
            probe = browser.new_page()
            agent = probe.evaluate("navigator.userAgent").replace("HeadlessChrome", "Chrome")
            probe.close()
            ctx = browser.new_context(user_agent=agent, viewport={"width": 1280, "height": 1600}, locale="en-US")
            pg = ctx.new_page()
            pg.goto(url, wait_until="domcontentloaded", timeout=45000)
            try:
                pg.wait_for_load_state("networkidle", timeout=15000)
            except Exception:
                pass  # busy pages never go quiet; what has loaded is enough
            for _ in range(4):  # ad lists load more as you scroll
                pg.mouse.wheel(0, 2500)
                pg.wait_for_timeout(1500)
            text = pg.inner_text("body")
            pg.screenshot(path=os.path.join(out, shot), full_page=False)
            browser.close()
    except Exception as e:
        return failed(rec, "the page did not open (%s)" % (str(e).splitlines() or [type(e).__name__])[0][:160])
    text = re.sub(r"\n\s*\n+", "\n", text).strip()
    rec.update(text=text[:30000], screenshot=shot)
    blocked = re.search(r"you have been blocked|access denied|verify you are human|are you a robot|unusual traffic", text, re.I)
    if blocked:
        return failed(rec, "the site blocked the visit with a security check; see the screenshot, or open the page yourself")
    if len(text) < 200:
        return failed(rec, "the page showed little or nothing, likely a login wall; see the screenshot")
    return rec


def fresh(path):
    """A file read successfully within FRESH_HOURS, so the site is not asked again."""
    try:
        with open(path, encoding="utf-8") as f:
            r = json.load(f)
        return r.get("ok") and now() - dt.datetime.fromisoformat(r["read_at"]) < dt.timedelta(hours=FRESH_HOURS)
    except (OSError, ValueError, KeyError, TypeError, AttributeError):
        return False


READERS = {"instagram": "instagram", "videos": "video", "page": "page", "reddit": "reddit", "hackernews": "hn"}


def main():
    a = sys.argv[1:]
    if len(a) < 2 or a[0] not in READERS:
        sys.exit(__doc__)
    try:
        out = a[a.index("--out") + 1] if "--out" in a else "social"
        n = int(a[a.index("--posts") + 1]) if "--posts" in a else 12
    except (IndexError, ValueError):
        sys.exit("--out needs a folder and --posts a number from 1 to %d" % MAX_POSTS)
    if not 1 <= n <= MAX_POSTS:
        sys.exit("--posts must be a number from 1 to %d" % MAX_POSTS)
    skip = {i + 1 for i, x in enumerate(a) if x in ("--out", "--posts")}
    targets = [x.strip() for i, x in enumerate(a[1:], 1) if not x.startswith("--") and i not in skip and x.strip()]
    kind = a[0]
    if kind == "page":
        for t in targets:
            page_url(t)  # an unknown shortcut stops the run before any site is asked
    os.makedirs(out, exist_ok=True)
    for i, t in enumerate(targets):
        name = "%s-%s.json" % (READERS[kind], slug(t))
        path = os.path.join(out, name)
        if fresh(path):
            print("kept %s (read in the last %d hours)" % (name, FRESH_HOURS))
            continue
        if i:
            time.sleep(PAUSE)
        try:
            rec = page(t, out) if kind == "page" else {"instagram": instagram, "videos": videos, "reddit": reddit,
                                                        "hackernews": hackernews}[kind](t, n)
        except Exception as e:  # a site's new answer must not stop the other accounts
            where = kind if kind != "videos" else "tiktok" if "tiktok.com" in t else "youtube"
            rec = failed(record(where, t, t), "the reader stopped on an answer it did not expect (%s: %s)"
                         % (type(e).__name__, (str(e).splitlines() or [""])[0][:140]))
        with open(path, "w", encoding="utf-8") as f:
            json.dump(rec, f, ensure_ascii=False, indent=1)
        print("%s %s: %s" % ("read" if rec["ok"] else "could not read", t,
                             "%d posts" % len(rec["posts"]) if rec["ok"] and kind != "page" else rec["why"] or "page saved"))


if __name__ == "__main__":
    main()
