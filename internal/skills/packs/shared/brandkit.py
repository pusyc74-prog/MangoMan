"""Themes, brand colours and logos for MangoMan skill packs.

A theme is derived from two colours, a dark base and an accent, so every
curated theme and every customer brand gets the same tested set of tokens
(text, body, muted, tints, grid, dim bars, readable accent shades) with
contrast checked, not guessed.

    T = theme("ink")                                   # a curated theme
    T = brand_theme("#E23744", "#FFC107")              # a customer's colours
    primary, accent = logo_colors("logo.png")          # colours read from a logo
"""
import os

# name: (dark base, accent, mood) - mood helps the model pick one that fits.
CURATED = {
    "ink":      ("14213D", "F4A300", "navy and saffron; finance, consulting, general business"),
    "forest":   ("1E3A2B", "D9A21B", "deep green and gold; agriculture, sustainability, banking"),
    "coral":    ("2B2D42", "FF6B4A", "slate and coral; consumer brands, marketing, startups"),
    "ocean":    ("0B3954", "3FC1C9", "deep blue and aqua; health, logistics, travel"),
    "plum":     ("3D1F47", "F2A541", "plum and marigold; fashion, beauty, hospitality"),
    "emerald":  ("0F3D3E", "9BE564", "teal and lime; climate, fintech, wellness"),
    "royal":    ("1B1F5E", "FF5D8F", "indigo and pink; media, education, creative"),
    "nordic":   ("2E3440", "88C0D0", "slate and ice blue; technology, SaaS, engineering"),
    "wine":     ("4A0E1C", "E8C07D", "burgundy and champagne; luxury, real estate, wine and food"),
    "clay":     ("4A1F17", "F28C55", "earth and peach; food, crafts, retail, D2C"),
    "graphite": ("1F2328", "4ADE80", "graphite and mint; data, AI, developer tools"),
    "cobalt":   ("0F4C81", "FFC857", "cobalt and sunflower; public sector, education, energy"),
}
MOTIFS = ("orb", "rings", "dots")
MODES = ("contrast", "light")
TYPES = {  # heading font, body font (PowerPoint names) and HTML stacks
    "modern":    ("Calibri", "Calibri", 'Calibri, Carlito, "Segoe UI", Arial, sans-serif', 'Calibri, Carlito, "Segoe UI", Arial, sans-serif'),
    "editorial": ("Cambria", "Calibri", 'Cambria, Caladea, Georgia, serif', 'Calibri, Carlito, "Segoe UI", Arial, sans-serif'),
    "classic":   ("Arial", "Arial", 'Arial, "Liberation Sans", Helvetica, sans-serif', 'Arial, "Liberation Sans", Helvetica, sans-serif'),
}
GOOD, BAD = "1E8E5A", "C9384A"


def _rgb(h):
    h = h.lstrip("#")
    if len(h) == 3:
        h = "".join(c * 2 for c in h)
    if len(h) != 6 or any(c not in "0123456789abcdefABCDEF" for c in h):
        raise ValueError("not a hex colour: %r" % h)
    return [int(h[i:i + 2], 16) for i in (0, 2, 4)]


def hexc(h):
    """Normalise '#abc' / 'AABBCC' to 'AABBCC'."""
    return "".join("%02X" % v for v in _rgb(h))


def mix(fg, bg, a):
    """fg at opacity a over bg."""
    f, b = _rgb(fg), _rgb(bg)
    return "".join("%02X" % round(x * a + y * (1 - a)) for x, y in zip(f, b))


def luminance(h):
    def ch(v):
        v /= 255
        return v / 12.92 if v <= 0.03928 else ((v + 0.055) / 1.055) ** 2.4
    r, g, b = (ch(v) for v in _rgb(h))
    return 0.2126 * r + 0.7152 * g + 0.0722 * b


def contrast(a, b):
    la, lb = sorted((luminance(a), luminance(b)), reverse=True)
    return (la + 0.05) / (lb + 0.05)


def _toward(c, target, against, ratio):
    """Move c toward target until it reaches the contrast ratio against `against`."""
    for i in range(41):
        x = mix(target, c, i / 40)
        if contrast(x, against) >= ratio:
            return x
    return target


def saturation(h):
    r, g, b = (v / 255 for v in _rgb(h))
    mx, mn = max(r, g, b), min(r, g, b)
    return 0 if mx == 0 else (mx - mn) / mx


def derive(dark, accent, motif="orb", mode="contrast", type_="modern"):
    """Full token set from a dark base and an accent colour."""
    dark, accent = hexc(dark), hexc(accent)
    W = "FFFFFF"
    dark = dark if contrast(W, dark) >= 4.5 else _toward(dark, "000000", W, 4.5)
    text = dark if contrast(dark, W) >= 11 else _toward(dark, "000000", W, 11)
    acc_on_dark = accent if contrast(accent, dark) >= 3 else _toward(accent, W, dark, 3)
    accent_dark = accent if contrast(accent, W) >= 3.2 else _toward(accent, "000000", W, 3.2)
    fh, fb, hh, hb = TYPES.get(type_, TYPES["modern"])
    return dict(
        dark=dark, dark2=mix(W, dark, 0.08), bg=W, tint=mix(dark, W, 0.06), text=text,
        body=mix(text, W, 0.82), muted=_toward(mix(text, W, 0.5), text, W, 4.5),
        accent=acc_on_dark, accent_dark=accent_dark, accent_fill=accent,
        good=GOOD, bad=BAD, grid=mix(dark, W, 0.11), dim=mix(dark, W, 0.28),
        on_dark=W, on_dark2=mix(W, dark, 0.85), on_dark_muted=mix(W, dark, 0.62),
        motif=motif if motif in MOTIFS else "orb", mode=mode if mode in MODES else "contrast",
        font_head=fh, font_body=fb, html_head=hh, html_body=hb,
    )


def theme(name="ink", **kw):
    d, a, _ = CURATED.get(name, CURATED["ink"])
    return derive(d, a, **kw)


def brand_theme(primary, accent=None, **kw):
    """A theme from a customer's brand colours. A light or vivid primary is
    deepened for dark slides and text; a missing accent is the primary."""
    primary = hexc(primary)
    accent = hexc(accent) if accent else primary
    if accent == primary and contrast(primary, "FFFFFF") < 4.5:
        # one light colour: it becomes the accent on a deepened base
        return derive(_toward(primary, "000000", "FFFFFF", 9), primary, **kw)
    return derive(primary, accent, **kw)


def light_mode(T):
    """Tokens for 'dark' slides drawn light (mode light): tinted background,
    white cards, dark text, the strong accent shade."""
    L = dict(T)
    L.update(dark=T["tint"], dark2="FFFFFF", on_dark=T["text"], on_dark2=T["body"], on_dark_muted=T["muted"],
             accent=T["accent_dark"], motif_base=T["dark"])
    return L


# ---------- logos ----------

LOGO_TYPES = (".png", ".jpg", ".jpeg")


def logo_check(path):
    """Problem with a logo file, or ''."""
    if not os.path.exists(path):
        return "logo not found: %s" % path
    if os.path.splitext(path)[1].lower() not in LOGO_TYPES:
        return "logo must be PNG or JPG (convert SVG to a PNG at least 600 px wide first)"
    return ""


def brand_problems(spec, base_dir="."):
    """Problems with a spec's brand {primary, accent, logo}: colours must be
    hex and the logo a PNG or JPG that exists (relative to base_dir)."""
    b = spec.get("brand")
    if b is None:
        return []
    if not isinstance(b, dict):
        return ["brand must be an object: primary, accent, logo"]
    p = []
    for key in ("primary", "accent"):
        if b.get(key):
            try:
                hexc(str(b[key]))
            except ValueError:
                p.append("brand %s must be a hex colour like #1F5FA8, not %r" % (key, b[key]))
    if b.get("logo"):
        pr = logo_check(os.path.join(base_dir, str(b["logo"])))
        if pr:
            p.append(pr)
    return p


def prepare_logo(path):
    """A copy of the logo with empty margins trimmed (transparent or white),
    so it sits exactly where it is placed. Returns the original on failure."""
    try:
        import tempfile
        from PIL import Image, ImageChops
        with Image.open(path) as im:
            im = im.convert("RGBA")
            box = im.getchannel("A").getbbox()
            if box == (0, 0) + im.size or box is None:
                bg = Image.new("RGB", im.size, (255, 255, 255))
                box = ImageChops.difference(im.convert("RGB"), bg).convert("L").point(lambda v: 255 if v > 18 else 0).getbbox() or box
            if not box or box == (0, 0) + im.size:
                return path
            out = os.path.join(tempfile.gettempdir(), "mangoman-logo-%s.png" % abs(hash(os.path.abspath(path))))
            im.crop(box).save(out)
            return out
    except Exception:
        return path


def data_uri(path):
    """A file as a data: URI with the right image type."""
    import base64
    ext = os.path.splitext(path)[1].lower().lstrip(".").replace("jpg", "jpeg") or "png"
    with open(path, "rb") as f:
        return "data:image/%s;base64,%s" % (ext, base64.b64encode(f.read()).decode())


def logo_size(path):
    try:
        from PIL import Image
        with Image.open(path) as im:
            return im.size
    except Exception:
        return (4, 1)


def fit(path, max_w, max_h):
    """Inches (w, h) keeping the logo's aspect ratio inside a box."""
    w, h = logo_size(path)
    s = min(max_w / w, max_h / h)
    return w * s, h * s


def _pixels(path):
    from PIL import Image
    with Image.open(path) as im:
        im = im.convert("RGBA")
        im.thumbnail((96, 96))
        return [p[:3] for p in im.getdata() if p[3] > 200]


def logo_hidden_share(path, bg):
    """Share of the logo's visible pixels that would blend into background bg."""
    try:
        px = _pixels(path)
    except Exception:
        return 0.0
    if not px:
        return 0.0
    low = sum(1 for p in px if contrast("%02X%02X%02X" % p, bg) < 1.8)
    return low / len(px)


def logo_colors(path):
    """(primary, accent) read from a logo: the darkest common colour and the
    most used vivid colour; None for either when the logo lacks one."""
    try:
        px = _pixels(path)
    except Exception:
        return None, None
    buckets = {}
    for r, g, b in px:
        if min(r, g, b) > 235:  # background white
            continue
        k = (r // 24, g // 24, b // 24)
        buckets.setdefault(k, []).append((r, g, b))
    total = sum(len(v) for v in buckets.values())
    if not total:
        return None, None
    common = []
    for v in sorted(buckets.values(), key=len, reverse=True):
        if len(v) / total < 0.04:
            break
        common.append("".join("%02X" % round(sum(p[i] for p in v) / len(v)) for i in range(3)))
    if not common:
        return None, None
    darkest = min(common, key=luminance)
    vivid = [c for c in common if saturation(c) > 0.35]
    accent = vivid[0] if vivid else None  # the most used vivid colour
    primary = darkest if contrast(darkest, "FFFFFF") >= 4.5 else (accent or darkest)
    if accent == primary:
        others = [c for c in vivid if c != primary]
        accent = others[0] if others else None
    return primary, accent


def resolve_spec(spec, base_dir="."):
    """Theme tokens for a spec with optional theme, motif, mode, type and
    brand {primary, accent, logo}. Returns (tokens, note) where note says
    which colours were read from the logo ('' when none were)."""
    kw = dict(motif=spec.get("motif", "orb"), mode=spec.get("mode", "contrast"), type_=spec.get("type", "modern"))
    b = spec.get("brand") or {}
    lp = None
    if b.get("logo"):
        lp = b["logo"] if os.path.isabs(b["logo"]) else os.path.join(base_dir, b["logo"])
    primary, accent = (hexc(b[k]) if b.get(k) else None for k in ("primary", "accent"))
    note = ""
    if lp and not primary and os.path.exists(lp):
        p2, a2 = logo_colors(lp)
        read = [("primary", p2)] + ([("accent", a2)] if a2 and not accent else [])
        primary, accent = p2, accent or a2
        if primary:
            note = "brand colours read from the logo: " + ", ".join("%s #%s" % kv for kv in read)
    if primary:
        accent = accent or CURATED.get(spec.get("theme", "ink"), CURATED["ink"])[1]
        T = brand_theme(primary, accent, **kw)
    else:
        T = theme(spec.get("theme", "ink"), **kw)
    if lp and os.path.exists(lp):
        T["logo"] = prepare_logo(lp)
    return T, note
