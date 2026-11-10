#!/usr/bin/env python3
"""README art: the banner and terminal images in docs/img/, drawn in one palette.

    python3 tools/art/make_art.py --fetch-fonts   # download the pinned font release into work/art/fonts/ (network;
                                                  # refuses any file whose SHA-256 differs from the pin below)
    python3 tools/art/make_art.py                 # render docs/img/*.png and write tools/art/art.json
    python3 tools/art/make_art.py --check         # render again in memory; exit 1 when any byte differs

Inputs: the palette in docs/art/palette.json (gruvbox-material, dark, medium background; the light mode is kept there
for the apps and the site, the images use dark), the transcripts in docs/art/transcripts/ (recorded from a real run by
tools/art/capture.sh; this script only draws them and never adds output), and JetBrains Mono (SIL OFL 1.1), fetched
from the tagged release of the official repository and pinned by SHA-256. The fonts are not committed, only the images.

Outputs: docs/img/banner.png (1280x320: the name, a one-line description and the palette; an opaque panel with
rounded, transparent corners, so it reads on light and dark pages), docs/img/social.png and one docs/img/term-*.png per
transcript. social.png (1280x640, larger type, fewer words, everything inside SOCIAL_SAFE because sites crop the edges)
is for the repository's social preview. It is uploaded by hand in the repository settings (Settings, General, Social
preview) and is not used by any page.
Every image is palette-quantised and keeps its text and marks at least MARGIN px from the edge.
tools/art/art.json lists the fonts (source and SHA-256), the inputs, the outputs, and the Pillow, FreeType and zlib
versions. Deterministic: the same script, inputs, fonts and Pillow give the same bytes, which --check relies on.
Needs Pillow (pinned in .github/workflows/ci.yml).

Adapted from Project Helghast (MIT, Copyright (c) 2026 undecuple): tools/release/installer_art.py (fitting text,
the font fetch with pins, the manifest and the byte-compare check) and tools/features.py (the command line).
"""
import argparse
import hashlib
import io
import json
import pathlib
import sys
import urllib.request
import zipfile

ROOT = pathlib.Path(__file__).resolve().parents[2]
PALETTE = "docs/art/palette.json"
TRANSCRIPTS = "docs/art/transcripts"
OUT = "docs/img"
MANIFEST = "tools/art/art.json"
FONTS = ROOT / "work" / "art" / "fonts"

FONT_RELEASE = {
    "name": "JetBrains Mono 2.304",
    "license": "SIL Open Font License 1.1",
    "url": "https://github.com/JetBrains/JetBrainsMono/releases/download/v2.304/JetBrainsMono-2.304.zip",
    "sha256": "6f6376c6ed2960ea8a963cd7387ec9d76e3f629125bc33d1fdcd7eb7012f7bbf",
}
# role -> (member of the release zip, SHA-256 of that member)
FONT_FILES = {
    "regular": ("fonts/ttf/JetBrainsMono-Regular.ttf", "a0bf60ef0f83c5ed4d7a75d45838548b1f6873372dfac88f71804491898d138f"),
    "bold": ("fonts/ttf/JetBrainsMono-Bold.ttf", "5590990c82e097397517f275f430af4546e1c45cff408bde4255dad142479dcb"),
    "license": ("OFL.txt", "30f0c136e3c88e422d0791acd97238870f9054a9729bc34cf2ff0d4ed8cac4ad"),
}

MODE = "dark"
MARGIN = 40          # no text or mark closer than this to an image edge
BANNER = (1280, 320)
SOCIAL = (1280, 640)
SOCIAL_SAFE = (96, 64)   # social.png keeps its content this far from the left/right and top/bottom edges
SS = 2               # the banner is drawn at twice the size and scaled down once
NAME = ("re-", "psn")
TAGLINE = "Matchmaking rooms and leaderboards for recompiled PS3 games"
TRAITS = ("self-hosted", "NAT traversal", "optional relay", "Go server", "C client", "desktop app")
REPO = "github.com/undecuple/re-psn"
SOCIAL_TAGLINE = ("Matchmaking rooms and leaderboards", "for recompiled PS3 games")   # TAGLINE on two lines
SOCIAL_TRAITS = ("self-hosted", "NAT traversal", "optional relay")

# transcript -> (image, title bar). The order is the order of the session.
TERMS = {
    "server.txt": ("term-server.png", "terminal 1: the server, debug log"),
    "alice.txt": ("term-alice.png", "terminal 2: alice hosts a room"),
    "bob.txt": ("term-bob.png", "terminal 3: the status page, then bob joins"),
    "client-test.txt": ("term-client-test.png", "terminal 3: the C client's test, then the leaderboard"),
}
COLS = 100           # terminal width; longer lines wrap as a terminal wraps them
TERM_PX = 16         # font size of the terminal images
TERM_COLOURS = 64
BANNER_COLOURS = 128


def sha256(data):
    return hashlib.sha256(data).hexdigest()


def load_palette(root):
    data = json.loads((root / PALETTE).read_text(encoding="utf-8"))
    return {k: tuple(int(v[i:i + 2], 16) for i in (1, 3, 5)) for k, v in data["modes"][MODE].items()}


# ------------------------------------------------------------------------------------------------------- fonts
class Fonts:
    """The regular and bold faces from `fonts_dir`, or Pillow's built-in font when stand_in (tests only)."""

    def __init__(self, fonts_dir=FONTS, stand_in=False):
        from PIL import ImageFont
        self.IF, self.stand_in, self.paths = ImageFont, stand_in, {}
        if stand_in:
            return
        for role in ("regular", "bold"):
            member, want = FONT_FILES[role]
            p = pathlib.Path(fonts_dir) / pathlib.PurePosixPath(member).name
            if not p.is_file():
                raise FileNotFoundError(f"font {p.name} missing in {fonts_dir} (run --fetch-fonts)")
            if sha256(p.read_bytes()) != want:
                raise ValueError(f"font {p.name} differs from the pinned SHA-256 (run --fetch-fonts)")
            self.paths[role] = p

    def get(self, role, px):
        if self.stand_in:
            return self.IF.load_default(max(1, int(px)))
        return self.IF.truetype(str(self.paths[role]), max(1, int(px)), layout_engine=self.IF.Layout.BASIC)


def fetch_fonts(fonts_dir=FONTS, opener=urllib.request.urlopen):
    """Download the release zip, check it and the members against the pins, and write the members to fonts_dir."""
    with opener(FONT_RELEASE["url"], timeout=60) as r:
        data = r.read()
    if sha256(data) != FONT_RELEASE["sha256"]:
        raise ValueError(f"{FONT_RELEASE['url']}: SHA-256 {sha256(data)} is not the pinned {FONT_RELEASE['sha256']}")
    d = pathlib.Path(fonts_dir)
    d.mkdir(parents=True, exist_ok=True)
    with zipfile.ZipFile(io.BytesIO(data)) as z:
        for member, want in FONT_FILES.values():
            body = z.read(member)
            if sha256(body) != want:
                raise ValueError(f"{member}: SHA-256 {sha256(body)} is not the pinned {want}")
            (d / pathlib.PurePosixPath(member).name).write_bytes(body)
    return [pathlib.PurePosixPath(m).name for m, _ in FONT_FILES.values()]


# ----------------------------------------------------------------------------------------------------- drawing
def fit(fonts, role, text, max_w, max_h):
    """The largest font size at which `text` fits max_w wide with capitals at most max_h tall."""
    lo, hi = 1, max(2, int(max_h * 2))
    while lo < hi:
        mid = (lo + hi + 1) // 2
        f = fonts.get(role, mid)
        b = f.getbbox("H")
        if f.getlength(text) <= max_w and b[3] - b[1] <= max_h:
            lo = mid
        else:
            hi = mid - 1
    return fonts.get(role, lo)


def cap_top(font):
    """Offset from the drawing origin to the top of the capitals, so text can be placed by its cap line."""
    return font.getbbox("H")[1]


class Ink:
    """Draws text and shapes and keeps the bounding box of everything drawn, for the margin check."""

    def __init__(self, draw, scale=1):
        self.d, self.scale, self.box = draw, scale, None

    def _grow(self, b):
        b = tuple(v / self.scale for v in b)
        self.box = b if self.box is None else (min(self.box[0], b[0]), min(self.box[1], b[1]),
                                               max(self.box[2], b[2]), max(self.box[3], b[3]))

    def text(self, xy, s, font, fill):
        if not s.strip():
            return
        self.d.text(xy, s, font=font, fill=fill)
        self._grow(self.d.textbbox(xy, s, font=font))

    def rect(self, b, fill, radius=0):
        self.d.rounded_rectangle(b, radius=radius, fill=fill) if radius else self.d.rectangle(b, fill=fill)
        self._grow(b)

    def line(self, pts, fill, width):
        self.d.line(pts, fill=fill, width=width)
        xs, ys = [p[0] for p in pts], [p[1] for p in pts]
        self._grow((min(xs) - width / 2, min(ys) - width / 2, max(xs) + width / 2, max(ys) + width / 2))

    def ellipse(self, b, fill=None, outline=None, width=1):
        self.d.ellipse(b, fill=fill, outline=outline, width=width)
        self._grow(b)


def panel(size, pal, radius):
    """An opaque bg0 panel with a bg3 border and transparent rounded corners (RGBA)."""
    from PIL import Image, ImageDraw
    img = Image.new("RGBA", size, (0, 0, 0, 0))
    d = ImageDraw.Draw(img)
    d.rounded_rectangle((0, 0, size[0] - 1, size[1] - 1), radius=radius, fill=pal["bg0"] + (255,),
                        outline=pal["bg3"] + (255,), width=max(1, radius // 8))
    return img


def quantise(img, colours):
    """A palette PNG; FASTOCTREE keeps the corners' alpha in the palette. No dithering, so the result is stable."""
    from PIL import Image
    return img.quantize(colors=colours, method=Image.Quantize.FASTOCTREE, dither=Image.Dither.NONE)


def png_bytes(img):
    buf = io.BytesIO()
    img.save(buf, format="PNG", optimize=True)
    return buf.getvalue()


def banner(fonts, pal):
    """The README banner: the name with a cursor, the description, the traits, the repository, and a palette strip."""
    from PIL import Image, ImageDraw
    w, h = BANNER
    W, H = w * SS, h * SS
    img = panel((W, H), pal, 18 * SS)
    ink = Ink(ImageDraw.Draw(img), SS)
    m = 64 * SS
    # the name: "re-" muted, "psn" bright, then a block cursor in the accent green
    name = "".join(NAME)
    f = fit(fonts, "bold", name + "_", W - 2 * m, 74 * SS)
    x, y = m, 52 * SS
    top = cap_top(f)
    ink.text((x, y - top), NAME[0], f, pal["grey2"])
    x += f.getlength(NAME[0])
    ink.text((x, y - top), NAME[1], f, pal["fg1"])
    x += f.getlength(NAME[1]) + 10 * SS
    capb = f.getbbox("H")
    ink.rect((x, y, x + f.getlength("_") * 0.8, y + capb[3] - capb[1]), pal["green"])
    y += capb[3] - capb[1] + 38 * SS
    # the description
    ft = fit(fonts, "regular", TAGLINE, W - 2 * m, 22 * SS)
    ink.text((m, y - cap_top(ft)), TAGLINE, ft, pal["fg0"])
    y += 22 * SS + 30 * SS
    # the traits, joined by accent dots
    fs = fonts.get("regular", 18 * SS)
    x = m
    for i, t in enumerate(TRAITS):
        if i:
            ink.text((x, y - cap_top(fs)), "  ·  ", fs, pal["aqua"])
            x += fs.getlength("  ·  ")
        ink.text((x, y - cap_top(fs)), t, fs, pal["grey2"])
        x += fs.getlength(t)
    # the repository, bottom right, and the palette strip, bottom left
    fr = fonts.get("regular", 16 * SS)
    by = H - 60 * SS
    capr = fr.getbbox("H")
    ink.text((W - m - fr.getlength(REPO), by + 4 * SS - (capr[3] - capr[1]) // 2 - capr[1]), REPO, fr, pal["grey1"])
    accents = ("red", "orange", "yellow", "green", "aqua", "blue", "purple")
    sw, gap = 40 * SS, 8 * SS
    for i, k in enumerate(accents):
        x0 = m + i * (sw + gap)
        ink.rect((x0, by, x0 + sw, by + 8 * SS), pal[k], radius=4 * SS)
    out = img.resize((w, h), Image.Resampling.LANCZOS)
    return quantise(out, BANNER_COLOURS), ink.box


def social(fonts, pal):
    """The social preview: a large name with a cursor, the description on two lines, three traits, the palette and repo."""
    from PIL import Image, ImageDraw
    w, h = SOCIAL
    W, H = w * SS, h * SS
    img = panel((W, H), pal, 18 * SS)
    ink = Ink(ImageDraw.Draw(img), SS)
    m = SOCIAL_SAFE[0] * SS
    name = "".join(NAME)
    f = fit(fonts, "bold", name + "_", W - 2 * m, 132 * SS)
    x, y = m, 100 * SS
    top = cap_top(f)
    ink.text((x, y - top), NAME[0], f, pal["grey2"])
    x += f.getlength(NAME[0])
    ink.text((x, y - top), NAME[1], f, pal["fg1"])
    x += f.getlength(NAME[1]) + 16 * SS
    capb = f.getbbox("H")
    ink.rect((x, y, x + f.getlength("_") * 0.8, y + capb[3] - capb[1]), pal["green"])
    y += capb[3] - capb[1] + 62 * SS
    ft = fonts.get("regular", 50 * SS)
    for line in SOCIAL_TAGLINE:
        ink.text((m, y - cap_top(ft)), line, ft, pal["fg0"])
        y += 70 * SS
    y += 4 * SS
    fs = fonts.get("regular", 28 * SS)
    x = m
    for i, t in enumerate(SOCIAL_TRAITS):
        if i:
            ink.text((x, y - cap_top(fs)), "  ·  ", fs, pal["aqua"])
            x += fs.getlength("  ·  ")
        ink.text((x, y - cap_top(fs)), t, fs, pal["grey2"])
        x += fs.getlength(t)
    fr = fonts.get("regular", 22 * SS)
    by = H - SOCIAL_SAFE[1] * SS - 24 * SS
    capr = fr.getbbox("H")
    ink.text((W - m - fr.getlength(REPO), by + 4 * SS - (capr[3] - capr[1]) // 2 - capr[1]), REPO, fr, pal["grey1"])
    accents = ("red", "orange", "yellow", "green", "aqua", "blue", "purple")
    sw, gap = 36 * SS, 8 * SS
    for i, k in enumerate(accents):
        x0 = m + i * (sw + gap)
        ink.rect((x0, by, x0 + sw, by + 8 * SS), pal[k], radius=4 * SS)
    out = img.resize((w, h), Image.Resampling.LANCZOS)
    return quantise(out, BANNER_COLOURS), ink.box


def spans(line, pal):
    """(text, colour, bold) runs for one transcript line. Colour only; the text is never changed."""
    if line.startswith("$ "):
        return [("$ ", pal["green"], True), (line[2:], pal["fg1"], True)]
    for word, colour in (("PASS ", pal["green"]), ("FAIL ", pal["red"])):
        if line.startswith(word):
            return [(word, colour, True), (line[len(word):], pal["fg0"], False)]
    if line.startswith("time="):
        out = []
        for tok in line.split(" "):
            colour = pal["fg0"]
            if tok.startswith("time="):
                colour = pal["grey0"]
            elif tok.startswith("level="):
                colour = {"level=DEBUG": pal["grey1"], "level=INFO": pal["blue"], "level=WARN": pal["yellow"],
                          "level=ERROR": pal["red"]}.get(tok, pal["fg0"])
            out.append((tok + " ", colour, False))
        out[-1] = (out[-1][0][:-1],) + out[-1][1:]
        return out
    return [(line, pal["fg0"], False)]


def terminal(fonts, pal, text, title):
    """A transcript drawn on a terminal-like panel: a title bar, then the lines, wrapped at COLS, one cell per char."""
    from PIL import ImageDraw
    reg, bold = fonts.get("regular", TERM_PX), fonts.get("bold", TERM_PX)
    cw = reg.getlength("M")
    lh = round(TERM_PX * 1.5)
    rows = []
    for line in text.rstrip("\n").split("\n"):
        runs = spans(line, pal)
        cells = [(c, colour, b) for s, colour, b in runs for c in s]
        for i in range(0, max(1, len(cells)), COLS):
            rows.append(cells[i:i + COLS])
    pad = MARGIN + 8
    ft = fonts.get("regular", 14)
    sep = pad + 24                   # the line under the title
    w = round(2 * pad + COLS * cw)
    h = round(sep + 22 + len(rows) * lh + pad - lh * 0.25)
    img = panel((w, h), pal, 12)
    d = ImageDraw.Draw(img)
    ink = Ink(d)
    ink.line(((pad, sep), (w - pad, sep)), pal["bg3"], 1)
    ink.text((pad, pad - cap_top(ft)), title, ft, pal["grey1"])
    y = sep + 22
    for row in rows:
        for i, (c, colour, b) in enumerate(row):
            ink.text((round(pad + i * cw), y), c, bold if b else reg, colour)
        y += lh
    return quantise(img, TERM_COLOURS), ink.box


def margin_problems(name, size, box):
    if box is None:
        return [f"{name}: nothing drawn"]
    x0, y0, x1, y1 = box
    w, h = size
    if x0 < MARGIN or y0 < MARGIN or x1 > w - MARGIN or y1 > h - MARGIN:
        return [f"{name}: a mark at {tuple(round(v) for v in box)} is inside the {MARGIN} px margin of {w}x{h}"]
    return []


def safe_problems(name, size, box, safe):
    """The stricter check for images that sites crop: content must stay inside `safe` (x, y) from the edges."""
    x0, y0, x1, y1 = box
    if x0 < safe[0] or y0 < safe[1] or x1 > size[0] - safe[0] or y1 > size[1] - safe[1]:
        return [f"{name}: a mark at {tuple(round(v) for v in box)} is outside the central area ({safe[0]}, {safe[1]} px in)"]
    return []


def render(fonts, root=ROOT):
    """-> ({image name: PNG bytes}, problems). Nothing is written."""
    pal = load_palette(root)
    files, probs = {}, []
    img, box = banner(fonts, pal)
    files["banner.png"] = png_bytes(img)
    probs += margin_problems("banner.png", img.size, box)
    img, box = social(fonts, pal)
    files["social.png"] = png_bytes(img)
    probs += margin_problems("social.png", img.size, box)
    probs += safe_problems("social.png", img.size, box, SOCIAL_SAFE) if box else []
    for src, (name, title) in TERMS.items():
        text = (root / TRANSCRIPTS / src).read_text(encoding="utf-8")
        img, box = terminal(fonts, pal, text, title)
        files[name] = png_bytes(img)
        probs += margin_problems(name, img.size, box)
    return files, probs


def versions():
    import PIL
    from PIL import features
    return {"pillow": PIL.__version__, "freetype": features.version("freetype2") or "-",
            "zlib": features.version("zlib") or "-"}


def manifest(files, root=ROOT):
    from PIL import Image
    inputs = [PALETTE] + [f"{TRANSCRIPTS}/{n}" for n in TERMS]
    outs = {}
    for name, data in sorted(files.items()):
        with Image.open(io.BytesIO(data)) as im:
            size = list(im.size)
        outs[f"{OUT}/{name}"] = {"size": size, "bytes": len(data), "sha256": sha256(data)}
    m = {
        "format": 1,
        "generator": "tools/art/make_art.py",
        "generator_sha256": sha256(pathlib.Path(__file__).read_bytes()),
        **versions(),
        "fonts": {
            "release": FONT_RELEASE,
            "files": {pathlib.PurePosixPath(mem).name: {"member": mem, "sha256": h} for mem, h in FONT_FILES.values()},
        },
        "inputs": {p: sha256((root / p).read_bytes()) for p in inputs},
        "outputs": outs,
    }
    return (json.dumps(m, indent=1) + "\n").encode("utf-8")


def check(fonts, root=ROOT):
    """-> problems: a fresh render (and its manifest) must equal the files on disk, byte for byte."""
    pinned = root / MANIFEST
    if pinned.is_file():
        old = json.loads(pinned.read_text(encoding="utf-8"))
        want = {k: old.get(k) for k in versions()}
        if want != versions():
            return [f"Pillow/FreeType/zlib {versions()} differ from {MANIFEST}'s {want}; install the pinned Pillow"]
    files, probs = render(fonts, root)
    for name, data in files.items():
        p = root / OUT / name
        if not p.is_file():
            probs.append(f"{OUT}/{name} missing (run python3 tools/art/make_art.py)")
        elif p.read_bytes() != data:
            probs.append(f"{OUT}/{name} differs from a fresh render (run python3 tools/art/make_art.py)")
    extra = sorted(p.name for p in (root / OUT).glob("*.png") if p.name not in files and p.name.startswith("term-"))
    probs += [f"{OUT}/{n} is not made by the generator" for n in extra]
    if not pinned.is_file():
        probs.append(f"{MANIFEST} missing (run python3 tools/art/make_art.py)")
    elif pinned.read_bytes() != manifest(files, root):
        probs.append(f"{MANIFEST} is not what the generator writes (run python3 tools/art/make_art.py)")
    return probs


def main(argv=None):
    ap = argparse.ArgumentParser(description="Render the README art into docs/img/, or check it.")
    ap.add_argument("--check", action="store_true", help="write nothing; exit 1 when a fresh render differs")
    ap.add_argument("--fetch-fonts", action="store_true", help="download the pinned fonts into --fonts")
    ap.add_argument("--fonts", default=str(FONTS), help="font directory (default work/art/fonts)")
    a = ap.parse_args(argv)
    if a.fetch_fonts:
        try:
            got = fetch_fonts(a.fonts)
        except (OSError, ValueError, KeyError, zipfile.BadZipFile) as e:
            print(f"make_art: fetch failed: {e}", file=sys.stderr)
            return 1
        print(f"make_art: {', '.join(got)} in {a.fonts}, each matching its pinned SHA-256")
        return 0
    try:
        fonts = Fonts(a.fonts)
    except (OSError, ValueError, ImportError) as e:
        print(f"make_art: {e}", file=sys.stderr)
        return 1
    if a.check:
        probs = check(fonts)
        for p in probs:
            print("make_art: " + p, file=sys.stderr)
        if not probs:
            print(f"make_art: OK: {len(TERMS) + 2} images and {MANIFEST} match a fresh render")
        return 1 if probs else 0
    files, probs = render(fonts)
    if probs:
        print("make_art: " + "; ".join(probs), file=sys.stderr)
        return 1
    out = ROOT / OUT
    out.mkdir(parents=True, exist_ok=True)
    for name, data in files.items():
        (out / name).write_bytes(data)
    (ROOT / MANIFEST).write_bytes(manifest(files))
    print(f"make_art: wrote {', '.join(f'{n} ({len(b)} B)' for n, b in files.items())} and {MANIFEST}")
    return 0


if __name__ == "__main__":
    sys.exit(main())
