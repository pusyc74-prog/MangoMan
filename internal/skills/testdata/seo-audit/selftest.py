"""Offline test of audit_site.py: a small site served on this computer, so
robots.txt, the sitemap, a missing page and each page check run as they do
on a real site. Run: python3 selftest.py <the pack's scripts folder> <out>.
It leaves seo-audit.json and seo-audit.md in <out> for check_audit.py."""
import functools
import http.server
import json
import os
import sys
import tempfile
import threading

sys.path.insert(0, sys.argv[1])
import audit_site as A  # noqa: E402

here = os.path.join(os.path.dirname(os.path.abspath(__file__)), "site")


class Site(http.server.SimpleHTTPRequestHandler):
    def do_GET(self):
        if self.path in ("/robots.txt", "/sitemap.xml"):
            body = open(os.path.join(here, self.path[1:]), "rb").read().replace(b"ROOT", root.rstrip("/").encode())
            self.send_response(200)
            self.end_headers()
            self.wfile.write(body)
            return
        super().do_GET()

    def log_message(self, *a):
        pass


srv = http.server.ThreadingHTTPServer(("127.0.0.1", 0), functools.partial(Site, directory=here))
root = "http://127.0.0.1:%d/" % srv.server_port
threading.Thread(target=srv.serve_forever, daemon=True).start()
A.PAUSE = 0
fails, done = [], []


def expect(cond, what):
    done.append(what)
    if not cond:
        fails.append(what)


out = sys.argv[2]
sys.argv = ["audit_site.py", root, "--out", out]
A.main()
a = json.load(open(os.path.join(out, A.OUT + ".json")))
found = {(f["priority"], f["what"], f["where"].replace(root, "/")) for f in a["findings"]}
expect(a["sitemap"]["found"] and a["sitemap"]["count"] == 7, "the sitemap named in robots.txt is read")
expect([p["url"].replace(root, "/") for p in a["pages"]] == ["/", "/about.html", "/products.html", "/missing.html", "/private/offer.html"],
       "pages come from the sitemap, on this site only: %s" % [p["url"] for p in a["pages"]])
expect(a["robots"]["blocked"] == {"Googlebot": False, "Bingbot": False, "OAI-SearchBot": True, "Claude-SearchBot": False, "PerplexityBot": False},
       "robots.txt rules per crawler")
want = {("medium", "robots.txt blocks OAI-SearchBot from the site", "/robots.txt"),
        ("high", "Page did not open: the page answered 404", "/missing.html"),
        ("high", "The site is not on HTTPS", "/"),
        ("high", "The page asks not to be indexed (noindex)", "/about.html"),
        ("medium", "Canonical points to another page: /", "/about.html"),
        ("medium", "No meta description", "/about.html"),
        ("high", "No mobile viewport tag", "/products.html"),
        ("low", "2 H1 headings", "/products.html"),
        ("medium", "1 structured data block(s) are not valid JSON", "/products.html"),
        ("low", "1 of 2 images have no alt text", "/products.html"),
        ("low", "No language on the page", "/products.html"),
        ("low", "Thin page: 13 words", "/about.html"),
        ("low", "Title is 12 characters", "/about.html"),
        ("medium", "2 pages share one title", "/about.html, /products.html"),
        ("high", "robots.txt blocks Googlebot from this page", "/private/offer.html"),
        ("high", "robots.txt blocks Bingbot from this page", "/private/offer.html")}
expect(want <= found, "expected findings: missing %s" % sorted(want - found))
expect(not {f for f in found if f[2] == "/" and f[1] != "The site is not on HTTPS"}, "the good home page has no other findings: %s"
       % sorted(f for f in found if f[2] == "/"))
expect([f["priority"] for f in a["findings"]] == sorted((f["priority"] for f in a["findings"]), key=A.ORDER.get), "high first")
expect(a["pages"][1]["schema"] == ["AboutPage"] and a["pages"][2]["title"] == "Kesari Foods", "structured data in @graph; an icon's title is not the page's")
home = a["pages"][0]
expect(home["schema"] == ["Organization"] and home["og_image"] and home["words"] > 5 and len(home["internal_links"]) == 2, "home page facts")

with tempfile.TemporaryDirectory() as d:  # a site saved in a folder, and an address that does not answer
    sys.argv = ["audit_site.py", here, "products.html", "--out", d]
    A.main()
    b = json.load(open(os.path.join(d, A.OUT + ".json")))
    expect([p["ok"] for p in b["pages"]] == [True, True] and not b["sitemap"]["found"], "a saved site is read from its folder")
    expect(not [f for f in b["findings"] if f["what"].startswith(("robots.txt could not", "No sitemap", "Canonical", "The site is not"))],
           "a saved site gets no address, sitemap or canonical findings: %s" % [f["what"] for f in b["findings"]])
    sys.argv = ["audit_site.py", "http://127.0.0.1:9/", "--out", d]
    A.main()
    c = json.load(open(os.path.join(d, A.OUT + ".json")))
    expect(not c["pages"][0]["ok"] and "did not open" in c["pages"][0]["why"], "a site that does not answer says why")
srv.shutdown()

for f in fails:
    print("FAIL  selftest: " + f)
if not fails:
    print("PASS  audit_site selftest (%d checks)" % len(done))
sys.exit(1 if fails else 0)
