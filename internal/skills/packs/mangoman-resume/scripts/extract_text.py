"""Extract text from an existing resume (PDF, DOCX, TXT, MD) to start from.

Usage: python3 extract_text.py old_resume.pdf
"""
import os
import re
import sys
import zipfile

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
import render  # noqa: E402


def docx_text(path):
    with zipfile.ZipFile(path) as z:
        xml = z.read("word/document.xml").decode("utf-8", "ignore")
    xml = re.sub(r"</w:p>", "\n", xml)
    xml = re.sub(r"<w:tab/>", "\t", xml)
    return re.sub(r"<[^>]+>", "", xml)


if __name__ == "__main__":
    if len(sys.argv) < 2:
        sys.exit(__doc__)
    p = sys.argv[1]
    ext = os.path.splitext(p)[1].lower()
    if ext == ".pdf":
        t = render.pdf_text(p)
        if not t.strip():
            sys.exit("No text found: the PDF may be a scan. Ask the user to paste the text.")
        print(t)
    elif ext == ".docx":
        print(docx_text(p))
    else:
        with open(p, encoding="utf-8", errors="ignore") as f:
            print(f.read())
