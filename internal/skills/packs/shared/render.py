"""Render HTML to PDF and PNG, and measure overflow, with whatever is installed.

Order: Playwright (Python) with its Chromium, then a Chrome or Chromium binary
in headless mode. Only the standard library is required to import this file.
"""
import os
import shutil
import subprocess
import sys
import tempfile

CHROME_NAMES = ["chromium", "chromium-browser", "google-chrome", "google-chrome-stable", "chrome", "msedge"]
MAC_PATHS = [
    "/Applications/Google Chrome.app/Contents/MacOS/Google Chrome",
    "/Applications/Chromium.app/Contents/MacOS/Chromium",
    "/Applications/Microsoft Edge.app/Contents/MacOS/Microsoft Edge",
]
WIN_PATHS = [
    r"C:\Program Files\Google\Chrome\Application\chrome.exe",
    r"C:\Program Files (x86)\Google\Chrome\Application\chrome.exe",
    r"C:\Program Files (x86)\Microsoft\Edge\Application\msedge.exe",
]

INSTALL_HINT = ("No browser engine found to render PDF. Install one of: "
                "`pip install playwright && python -m playwright install chromium`, "
                "or Google Chrome / Chromium.")


def _playwright():
    try:
        from playwright.sync_api import sync_playwright  # noqa: F401
        return True
    except Exception:
        return False


def _chrome():
    for n in CHROME_NAMES:
        p = shutil.which(n)
        if p:
            return p
    for p in MAC_PATHS + WIN_PATHS:
        if os.path.exists(p):
            return p
    return None


def engine():
    """Name of the engine that will be used, or None."""
    if _playwright():
        return "playwright"
    if _chrome():
        return "chrome"
    return None


def _url(path):
    return "file://" + os.path.abspath(path)


def html_to_pdf(html_path, pdf_path):
    """Print an HTML file to PDF using its own @page CSS. Returns the engine name."""
    if _playwright():
        from playwright.sync_api import sync_playwright
        with sync_playwright() as p:
            b = p.chromium.launch()
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
            b = p.chromium.launch()
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


OVERFLOW_JS = """
(sel) => {
  const out = [];
  for (const el of document.querySelectorAll(sel)) {
    const over = el.scrollHeight - el.clientHeight > 1 || el.scrollWidth - el.clientWidth > 1;
    if (over) out.push({id: el.id || el.dataset.check || el.className, extra_px: Math.max(el.scrollHeight - el.clientHeight, el.scrollWidth - el.clientWidth)});
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
        b = p.chromium.launch()
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
