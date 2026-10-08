"""Blank out provider keys in every file under a folder, before a report is
published to the eval-reports branch. Keys come from the environment."""
import os
import pathlib
import sys

NAMES = ("GROQ_API_KEY", "CEREBRAS_API_KEY", "OPENROUTER_API_KEY", "NVIDIA_API_KEY", "OPENCODE_ZEN_API_KEY")
keys = [k for k in (os.environ.get(n, "") for n in NAMES) if len(k) >= 8]
for f in pathlib.Path(sys.argv[1]).rglob("*"):
    if not f.is_file() or f.suffix in (".png", ".jpg"):
        continue
    text = f.read_bytes().decode("utf-8", "replace")
    clean = text
    for k in keys:
        clean = clean.replace(k, "[key hidden]")
    if clean != text:
        f.write_text(clean)
