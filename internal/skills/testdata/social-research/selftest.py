"""Offline test of read_social.py: stand-ins for Instaloader and yt-dlp, so the
readers' handling of good accounts, hidden counts and refusals is checked
without touching any site. Run: python3 selftest.py <the pack's scripts folder>."""
import datetime as dt
import json
import os
import sys
import tempfile
import types

sys.path.insert(0, sys.argv[1])
import read_social as R  # noqa: E402


# A stand-in for Instaloader: two posts as Instagram's list sends them, one
# with its like count missing (only the post's own page would have it).
class Post:
    def __init__(self, code, ts, n, caption):
        self.shortcode, self._node, self.caption = code, n, caption
        self.date_utc = dt.datetime.fromtimestamp(ts, dt.timezone.utc)
        self.caption_hashtags = [w[1:].lower() for w in caption.split() if w.startswith("#")]

    @property
    def likes(self):
        raise AssertionError("read_social must not use properties that fetch the post's page")


class Profile:
    followers, followees, mediacount, biography, external_url, full_name = 1200, 10, 2, "bio", "", "Test Brand"
    is_private = False
    posts = [Post("AA", 1760000000, {"is_video": True, "__typename": "GraphVideo", "edge_media_preview_like": {"count": 90},
                                     "edge_media_to_comment": {"count": 7}, "video_view_count": 4000}, "Hook line #mangoes"),
             Post("BB", 1759000000, {"is_video": False, "__typename": "GraphSidecar"}, "Carousel")]

    @classmethod
    def from_username(cls, ctx, name):
        if name == "missing":
            raise ProfileNotExistsException("no profile")
        if name == "busy":
            raise TooManyRequestsException("429 Too Many Requests")
        if name == "boom":  # Instagram changed its answer: an error Instaloader does not wrap
            raise KeyError("edge_owner_to_timeline_media")
        if name == "walled":  # what a cloud server got on 20 Oct (social-check)
            raise InstaloaderException("302 Found when accessing https://www.instagram.com/walled/")
        return cls()

    def get_posts(self):
        return iter(self.posts)


class InstaloaderException(Exception):
    pass


class ProfileNotExistsException(InstaloaderException):
    pass


class TooManyRequestsException(InstaloaderException):
    pass


fake_ig = types.ModuleType("instaloader")
fake_ig.Profile = Profile
fake_ig.Instaloader = lambda **kw: types.SimpleNamespace(context=None)
fake_ig.exceptions = types.SimpleNamespace(InstaloaderException=InstaloaderException)
sys.modules["instaloader"] = fake_ig


# A stand-in for yt-dlp: a Shorts list, one video's page refusing.
class DownloadError(Exception):
    pass


class YoutubeDL:
    def __init__(self, opts):
        assert opts["skip_download"] and opts["playlistend"] == 12

    def __enter__(self):
        return self

    def __exit__(self, *a):
        return False

    def extract_info(self, url, download):
        if "blocked" in url:
            raise DownloadError("ERROR: Sign in to confirm you're not a bot")
        if url.endswith("/shorts"):
            return {"channel": "Test Brand", "channel_follower_count": 500, "channel_id": "UC1",
                    "entries": [{"url": "https://www.youtube.com/shorts/v1"}, {"url": "https://www.youtube.com/shorts/v2"},
                                {"url": "https://www.youtube.com/@testbrand/videos", "title": "Test Brand - Videos"}, {}]}
        if url.endswith("v2"):
            raise DownloadError("ERROR: video unavailable")
        return {"webpage_url": url, "title": "Short one", "description": "Watch this #tips", "upload_date": "20261012",
                "view_count": 3000, "like_count": 120, "comment_count": 9, "track": "Song", "artist": "Band"}


fake_yt = types.ModuleType("yt_dlp")
fake_yt.YoutubeDL = YoutubeDL
fake_yt.utils = types.SimpleNamespace(DownloadError=DownloadError)
sys.modules["yt_dlp"] = fake_yt

fails, done = [], []


def expect(cond, what):
    done.append(what)
    if not cond:
        fails.append(what)


r = R.instagram("@testbrand", 12)
expect(r["ok"] and r["account"] == "testbrand" and r["profile"]["followers"] == 1200, "instagram profile")
p1, p2 = r["posts"]
expect(p1["type"] == "reel" and p1["likes"] == 90 and p1["comments"] == 7 and p1["views"] == 4000 and p1["hashtags"] == ["mangoes"], "reel counts")
expect(p2["type"] == "carousel" and p2["likes"] is None and p2["views"] is None, "hidden counts stay unknown")
expect(R.instagram("https://www.instagram.com/missing/", 12)["why"] == "no public Instagram account with this name", "missing account")
expect("slow down" in R.instagram("busy", 12)["why"], "rate limit in plain words")
expect("login wall" in R.instagram("walled", 12)["why"], "a redirect to the login page in plain words")

R.feed_dates = lambda cid: {"v2": "2026-10-01"} if cid == "UC1" else {}
v = R.videos("https://www.youtube.com/@testbrand", 12)
expect(v["ok"] and v["platform"] == "youtube" and v["account"] == "testbrand" and v["url"].endswith("/shorts"), "youtube channel reads its shorts")
expect(v["posts"][0]["date"] == "2026-10-12" and v["posts"][0]["views"] == 3000 and v["posts"][0]["sound"] == "Band - Song", "short details")
expect(v["posts"][1]["url"].endswith("v2") and v["posts"][1]["views"] is None, "a video page that refuses keeps the list entry")
expect(v.get("note", "").startswith("1 of 2 videos") and "public feed" in v["note"], "a partial read says so")
expect(v["posts"][1]["date"] == "2026-10-01", "the feed gives the refused video its date")
expect("sign in" in R.videos("https://www.tiktok.com/@blocked", 12)["why"], "bot check in plain words")

expect(R.page_url("meta-ads:Kesari Foods@US") == R.PAGES["meta-ads"].format(q="Kesari%20Foods", region="US"), "meta ads shortcut")
expect("region=IN" in R.page_url("tiktok-trends") and "region=AE" in R.page_url("tiktok-trends@ae"), "trends shortcut and region")
expect(R.page_url("https://example.com/x") == "https://example.com/x", "links pass through")
expect(R.video_url("https://www.youtube.com/@brand/videos") == "https://www.youtube.com/@brand/videos", "a named tab stays")
expect(R.video_url("https://youtube.com/@brand?si=abc") == "https://www.youtube.com/@brand/shorts", "a share link reads the Shorts")
expect(R.video_url("https://m.youtube.com/user/brand/featured") == "https://www.youtube.com/user/brand/shorts", "mobile and featured links read the Shorts")
expect(R.video_url("https://www.tiktok.com/@brand?lang=en") == "https://www.tiktok.com/@brand", "a TikTok link loses its extras")
expect(len(v["posts"]) == 2, "channel tabs and empty entries are not posts")
expect(R.slug("kesari.foods") != R.slug("kesari_foods") and R.slug(R.PAGES["meta-ads"].format(q="A", region="IN")) != R.slug(R.PAGES["meta-ads"].format(q="B", region="IN")), "names that read alike get their own files")

with tempfile.TemporaryDirectory() as d:
    sys.argv = ["read_social.py", "instagram", "boom", "testbrand", "--out", d]
    R.PAUSE = 0
    R.main()
    files = sorted(os.listdir(d))
    boom = json.load(open(os.path.join(d, files[0])))
    expect(len(files) == 2 and not boom["ok"] and "did not expect" in boom["why"], "one account's surprise does not stop the others")

with tempfile.TemporaryDirectory() as d:
    f = os.path.join(d, "x.json")
    json.dump({"ok": True, "read_at": R.now().isoformat()}, open(f, "w"))
    expect(R.fresh(f), "a file read now is fresh")
    json.dump({"ok": True, "read_at": (R.now() - dt.timedelta(days=2)).isoformat()}, open(f, "w"))
    expect(not R.fresh(f), "a file from two days ago is read again")
    json.dump({"ok": False, "read_at": R.now().isoformat()}, open(f, "w"))
    expect(not R.fresh(f), "a failed read is tried again")

for f in fails:
    print("FAIL  selftest: " + f)
if not fails:
    print("PASS  read_social selftest (%d checks)" % len(done))
sys.exit(1 if fails else 0)
