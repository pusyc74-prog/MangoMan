"""Render HTML to PDF and PNG, and measure overflow, with whatever is installed.

Order: Playwright (Python) driving the computer's own Edge, Chrome or
Chromium (else Playwright's own Chromium, if one was downloaded), then that
browser alone in headless mode. MangoMan's own Python has the Playwright
package but never downloads a browser for it. Only the standard library is
required to import this file.
"""
import importlib.util
import os
import pathlib
import shutil
import subprocess
import sys

CHROME_NAMES = ["chromium", "chromium-browser", "google-chrome", "google-chrome-stable", "chrome", "msedge"]
MAC_PATHS = [
    "/Applications/Google Chrome.app/Contents/MacOS/Google Chrome",
    "/Applications/Chromium.app/Contents/MacOS/Chromium",
    "/Applications/Microsoft Edge.app/Contents/MacOS/Microsoft Edge",
]
WIN_PATHS = [
    r"C:\Program Files\Google\Chrome\Application\chrome.exe",
    r"C:\Program Files (x86)\Google\Chrome\Application\chrome.exe",
    os.path.expandvars(r"%LOCALAPPDATA%\Google\Chrome\Application\chrome.exe"),
    r"C:\Program Files (x86)\Microsoft\Edge\Application\msedge.exe",
    r"C:\Program Files\Microsoft\Edge\Application\msedge.exe",
]

INSTALL_HINT = ("No browser found to make the PDF. Install Microsoft Edge or Google Chrome "
                "(both free), then try again.")


def _playwright():
    return importlib.util.find_spec("playwright") is not None


def _chrome():
    for n in CHROME_NAMES:
        p = shutil.which(n)
        if p:
            return p
    for p in MAC_PATHS + WIN_PATHS:
        if os.path.exists(p):
            return p
    return None


def _launch(p):
    """Start a headless browser for Playwright: the computer's own, else
    Playwright's own Chromium."""
    exe = _chrome()
    if exe:
        return p.chromium.launch(executable_path=exe)
    try:
        return p.chromium.launch()
    except Exception as e:  # no browser at all: say what to do, not Playwright's install steps
        raise RuntimeError(INSTALL_HINT) from e


def engine():
    """Name of the engine that will be used, or None."""
    if _playwright():
        return "playwright"
    if _chrome():
        return "chrome"
    return None


def _url(path):
    # as_uri gives file:///C:/... on Windows, where "file://" + path does not.
    return pathlib.Path(os.path.abspath(path)).as_uri()


# Some fonts (Inter is the common one) put their alternate digits and dashes
# into the PDF with no way back to the real characters when Chromium uses an
# OpenType feature such as tabular digits: the page looks right, but copying,
# searching or reading the text gives private symbol codes. If that happens,
# the PDF is printed again with those font features off.
PLAIN_FONTS = ("<style>*{font-variant-numeric:normal!important;font-variant-ligatures:none!important;"
               "font-feature-settings:'calt' 0,'case' 0,'liga' 0!important}</style>")


def _unreadable(pdf_path):
    return sum("\ue000" <= ch <= "\uf8ff" for ch in pdf_text(pdf_path))


def html_to_pdf(html_path, pdf_path):
    """Print an HTML file to PDF using its own @page CSS. Returns the engine name.
    The PDF's text stays readable (see PLAIN_FONTS)."""
    used = _print(html_path, pdf_path)
    bad = _unreadable(pdf_path)
    if not bad:
        return used
    with open(html_path, encoding="utf-8") as f:
        html = f.read()
    at = html.lower().find("</head>")
    html = html[:at] + PLAIN_FONTS + html[at:] if at >= 0 else PLAIN_FONTS + html
    # Kept next to the original so its images and styles still load.
    plain = os.path.join(os.path.dirname(os.path.abspath(html_path)), ".plain-" + os.path.basename(html_path))
    plain_pdf = pdf_path + ".plain.pdf"
    try:
        with open(plain, "w", encoding="utf-8") as f:
            f.write(html)
        again = _print(plain, plain_pdf)
        # Icon fonts use private codes on purpose; keep the plain print only
        # if it really reads better.
        if _unreadable(plain_pdf) < bad:
            os.replace(plain_pdf, pdf_path)
            used = again
        return used
    finally:
        for f in (plain, plain_pdf):
            if os.path.exists(f):
                os.remove(f)


def _print(html_path, pdf_path):
    if _playwright():
        from playwright.sync_api import sync_playwright
        with sync_playwright() as p:
            b = _launch(p)
            page = b.new_page()
            page.goto(_url(html_path), wait_until="networkidle")
            page.emulate_media(media="print")
            page.pdf(path=pdf_path, prefer_css_page_size=True, print_background=True)
            b.close()
        return "playwright"
    chrome = _chrome()
    if chrome:
        subprocess.run([chrome, "--headless=new", "--disable-gpu", "--no-pdf-header-footer",
                        "--print-to-pdf=" + os.path.abspath(pdf_path), _url(html_path)],
                       check=True, stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL, timeout=120)
        return "chrome"
    raise RuntimeError(INSTALL_HINT)


def screenshot(html_path, png_path, width=1280, height=900, full_page=True, dark=False):
    """Screenshot an HTML file. Returns False when no engine can do it."""
    if _playwright():
        from playwright.sync_api import sync_playwright
        with sync_playwright() as p:
            b = _launch(p)
            page = b.new_page(viewport={"width": width, "height": height},
                              color_scheme="dark" if dark else "light")
            page.goto(_url(html_path), wait_until="networkidle")
            page.screenshot(path=png_path, full_page=full_page)
            b.close()
        return True
    chrome = _chrome()
    if chrome:
        subprocess.run([chrome, "--headless=new", "--disable-gpu", "--hide-scrollbars",
                        f"--window-size={width},{height}", "--screenshot=" + os.path.abspath(png_path),
                        _url(html_path)], check=True, stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL, timeout=120)
        return True
    return False


FRAME_JS = "() => window.__fit || null"


def render_frames(jobs):
    """Screenshot many fixed-size HTML frames with one browser.

    jobs: list of (html_path, png_path, width, height). Returns a list with,
    per job, what the page's fit script reported in window.__fit (font sizes
    and overflow per text box), or None when it could not be measured (no
    Playwright: Chrome is used, one process per frame). Returns None when no
    engine is available at all.
    """
    if _playwright():
        from playwright.sync_api import sync_playwright
        out = []
        with sync_playwright() as p:
            b = _launch(p)
            for html_path, png_path, w, h in jobs:
                page = b.new_page(viewport={"width": w, "height": h})
                page.goto(_url(html_path), wait_until="networkidle")
                out.append(page.evaluate(FRAME_JS))
                page.screenshot(path=png_path, full_page=False)
                page.close()
            b.close()
        return out
    if _chrome():
        for html_path, png_path, w, h in jobs:
            screenshot(html_path, png_path, w, h, full_page=False)
        return [None] * len(jobs)
    return None


def inspect(html_path, js, widths=(390, 768, 1440), height=844, shots=None):
    """Run a measuring script in a page at several viewport widths.

    js is a JavaScript function source evaluated after load; returns
    {width: result}. shots maps width -> PNG path for a full-page screenshot.
    Returns None without Playwright (the caller then asks for a manual look).
    """
    if not _playwright():
        return None
    from playwright.sync_api import sync_playwright
    out = {}
    with sync_playwright() as p:
        b = _launch(p)
        for w in widths:
            page = b.new_page(viewport={"width": w, "height": height})
            page.goto(_url(html_path), wait_until="networkidle")
            out[w] = page.evaluate(js)
            if shots and w in shots:
                # scroll through once so lazy images load before the full-page shot
                page.evaluate("async () => { for (let y = 0; y < document.body.scrollHeight; y += 600) { window.scrollTo({top: y, behavior: 'instant'}); await new Promise(r => setTimeout(r, 60)); } window.scrollTo({top: 0, behavior: 'instant'}); await new Promise(r => setTimeout(r, 100)); }")
                page.wait_for_load_state("networkidle")
                page.screenshot(path=shots[w], full_page=True)
            page.close()
        b.close()
    return out


OVERFLOW_JS = """
(sel) => {
  const out = [];
  for (const el of document.querySelectorAll(sel)) {
    // scroll size misses content pushed above or left of the box (centred or end-aligned), so compare child boxes too
    const b = el.getBoundingClientRect();
    let extra = Math.max(el.scrollHeight - el.clientHeight, el.scrollWidth - el.clientWidth);
    for (const c of el.children) {
      const r = c.getBoundingClientRect();
      extra = Math.max(extra, b.top - r.top, r.bottom - b.bottom, b.left - r.left, r.right - b.right);
    }
    if (extra > 1) out.push({id: el.id || el.dataset.check || el.className, extra_px: Math.round(extra)});
  }
  return out;
}
"""


def overflow(html_path, selector, width=1280, height=720):
    """Elements matching selector whose content overflows them, or None when
    no engine can measure (the caller then falls back to text budgets)."""
    if not _playwright():
        return None
    from playwright.sync_api import sync_playwright
    with sync_playwright() as p:
        b = _launch(p)
        page = b.new_page(viewport={"width": width, "height": height})
        page.goto(_url(html_path), wait_until="networkidle")
        res = page.evaluate(OVERFLOW_JS, selector)
        b.close()
    return res


def pdf_pages(pdf_path):
    """Page count of a PDF: pypdf when installed, else a byte scan."""
    try:
        from pypdf import PdfReader
        return len(PdfReader(pdf_path).pages)
    except Exception:
        pass
    import re
    with open(pdf_path, "rb") as f:
        data = f.read()
    return len(re.findall(rb"/Type\s*/Page(?![s\w])", data))


def pdf_text(pdf_path):
    """Text of a PDF ('' when nothing can extract it)."""
    try:
        from pypdf import PdfReader
        return "\n".join((pg.extract_text() or "") for pg in PdfReader(pdf_path).pages)
    except Exception:
        pass
    exe = shutil.which("pdftotext")
    if exe:
        out = subprocess.run([exe, "-layout", pdf_path, "-"], capture_output=True, text=True)
        return out.stdout
    return ""


if __name__ == "__main__":
    print(engine() or INSTALL_HINT)
    sys.exit(0 if engine() else 1)
