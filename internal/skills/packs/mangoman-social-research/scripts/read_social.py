"""Read public social accounts and ad pages for competitor research, logged out.

Usage:
  python3 read_social.py instagram <username> [<username> ...] [--posts 12] [--out social]
  python3 read_social.py videos <YouTube channel or TikTok profile link> [...] [--posts 12] [--out social]
  python3 read_social.py page <link or shortcut> [...] [--out social]

Page shortcuts (country or region code after @, default IN):
  meta-ads:<name>[@IN]      ads running now on Facebook and Instagram (Meta Ad Library)
  google-ads:<domain>[@IN]  ads on Google and YouTube (Google Ads Transparency Center)
  linkedin-ads:<name>       ads on LinkedIn (LinkedIn Ad Library)
  tiktok-trends[@IN]        trending hashtags on TikTok this week (TikTok Creative Center)

Only public data, never logged in, a few requests per account with pauses,
each account at most once a day (a fresh file is kept). Writes one JSON file
per account or page into --out; a page also gets a screenshot. When a site
refuses, the file says why, so the research can say what it could not read.
"""
import datetime as dt
import itertools
import json
import os
import re
import sys
import time
import urllib.parse

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
    return re.sub(r"[^a-z0-9]+", "-", s.lower()).strip("-")[:60] or "page"


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
    if name in ("LoginRequiredException", "QueryReturnedForbiddenException") or "login" in str(e).lower():
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
    """A YouTube channel link reads its Shorts unless a tab is named."""
    u = url.strip().rstrip("/")
    if re.match(r"https?://(www\.)?youtube\.com/(@[^/]+|channel/[^/]+|c/[^/]+)$", u):
        return u + "/shorts"
    return u


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


def video_why(e):
    s = str(e)
    if "not a bot" in s or "Sign in" in s or "login" in s.lower():
        return "the site asked to sign in or to prove it is not a bot; add links to its videos by hand"
    if "429" in s or "Too Many" in s:
        return "the site asked to slow down; try again in an hour"
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
            for e in list(info.get("entries") or [])[:n]:
                full = e
                try:  # the list gives little; each video's page gives the date and the counts
                    full = y.extract_info(e.get("url") or e.get("webpage_url"), download=False) or e
                except yt_dlp.utils.DownloadError:
                    pass
                rec["posts"].append(vid_post(full, platform))
    except yt_dlp.utils.DownloadError as e:
        return failed(rec, video_why(e))
    return rec


# Ad libraries and trend pages: the page's text and a screenshot, read by the
# model, so a change in the page's layout does not break the reading.

def page_url(target):
    if target.startswith(("http://", "https://", "file://")):
        return target
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
        return failed(rec, "the page did not open (%s)" % str(e).splitlines()[0][:160])
    text = re.sub(r"\n\s*\n+", "\n", text).strip()
    rec.update(text=text[:30000], screenshot=shot)
    if len(text) < 200:
        return failed(rec, "the page showed little or nothing, likely a login wall or a block; see the screenshot")
    return rec


def fresh(path):
    """A file read successfully within FRESH_HOURS, so the site is not asked again."""
    try:
        with open(path, encoding="utf-8") as f:
            r = json.load(f)
        return r.get("ok") and now() - dt.datetime.fromisoformat(r["read_at"]) < dt.timedelta(hours=FRESH_HOURS)
    except (OSError, ValueError, KeyError, TypeError):
        return False


def main():
    a = sys.argv[1:]
    if len(a) < 2 or a[0] not in ("instagram", "videos", "page"):
        sys.exit(__doc__)
    out = a[a.index("--out") + 1] if "--out" in a else "social"
    n = min(int(a[a.index("--posts") + 1]), MAX_POSTS) if "--posts" in a else 12
    skip = {i + 1 for i, x in enumerate(a) if x in ("--out", "--posts")}
    targets = [x for i, x in enumerate(a[1:], 1) if not x.startswith("--") and i not in skip]
    kind = a[0]
    if kind == "page":
        for t in targets:
            page_url(t)  # an unknown shortcut stops the run before any site is asked
    os.makedirs(out, exist_ok=True)
    for i, t in enumerate(targets):
        name = "%s-%s.json" % ({"instagram": "instagram", "videos": "video", "page": "page"}[kind], slug(t))
        path = os.path.join(out, name)
        if fresh(path):
            print("kept %s (read in the last %d hours)" % (name, FRESH_HOURS))
            continue
        if i:
            time.sleep(PAUSE)
        rec = instagram(t, n) if kind == "instagram" else videos(t, n) if kind == "videos" else page(t, out)
        with open(path, "w", encoding="utf-8") as f:
            json.dump(rec, f, ensure_ascii=False, indent=1)
        print("%s %s: %s" % ("read" if rec["ok"] else "could not read", t,
                             "%d posts" % len(rec["posts"]) if rec["ok"] and kind != "page" else rec["why"] or "page saved"))


if __name__ == "__main__":
    main()
