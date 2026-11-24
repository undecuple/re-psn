#!/usr/bin/env python3
"""Generates docs/project-facts.svg, the "Project Facts" label on the README: what re-psn does, its limits and how to use it,
set out like the label on a medicine box. It is drawn in the dark colours of the website (docs/art/palette.json), on its own
panel, so it reads the same on a light or a dark page.

    python3 tools/project_facts.py            # write docs/project-facts.svg
    python3 tools/project_facts.py --check    # write nothing; exit 1 when the file is missing or differs

The wording is in LABEL below; the version comes from VERSION, so a version bump needs this run again (docs/releasing.md).
Lines are wrapped here rather than by the viewer, at a width that fits the common sans-serif fallbacks, so the file is the same
bytes everywhere and needs no font file, script or outside address.
"""
import argparse
import pathlib
import sys
import textwrap
from xml.sax.saxutils import escape

ROOT = pathlib.Path(__file__).resolve().parents[1]
OUT = "docs/project-facts.svg"
W, X0 = 520, 11                 # drawing width and the left edge of the boxes; the right edge is W - 11
X1 = W - 11
SCALE = 1.2                      # the file is shown at W * SCALE pixels: the same drawing, 20 percent larger, still sharp (it is vector)
PAD = 5                          # text starts this far inside a box
SIZE, LEAD = 11.5, 14.0          # body text size and line height
WRAP = 76                        # characters per body line (about 390 of the 476 pixels inside a box in the usual fonts, 480 in the widest fallback)
PURPOSE_X, PURPOSE_WRAP = 235, 36  # the Purpose column: its width in drawing units and its characters per line
# The dark tokens of docs/art/palette.json (tools/test_project_facts.py checks that every colour here is one of them):
# bg1 is the panel, fg1 the text, orange the thick rule and the bullets, grey1 the frames and thin rules.
BG, TEXT, ACCENT, FRAME = "#161719", "#e1ffe5", "#997e95", "#787487"
FONT = "Helvetica Neue, Helvetica, Arial, Liberation Sans, sans-serif"

# Each box is (heading, items). An item is ("bullet", text), ("note", label, text) or ("plain", text).
LABEL = {
    "title": "Project Facts",
    "serving": "Serving size: one group of friends",
    "ingredient": ("Active ingredient (in each server)", "Purpose", "re-psn server", "Answers a recompiled PS3 game's matchmaking and leaderboard calls"),
    "boxes": [
        ("Uses", [
            ("bullet", "keeps rooms, invite codes and leaderboards for your own group"),
            ("bullet", "tells each player where the others are; game traffic goes between players"),
            ("bullet", "hole punching for players behind routers, with an optional relay for when it fails"),
            ("bullet", "an optional desktop app to host, play and keep a profile"),
        ]),
        ("Warnings", [
            ("note", "Do not use:", "as a public service. There are no accounts and no public instance; you run it for your own group."),
            ("note", "Not for:", "games you do not own. No game files, keys or firmware are included or needed."),
            ("note", "When using this product:", "the status page lists every online ID, so keep it on your own machine (the default) or behind a token. The relay uses the host's bandwidth."),
            ("note", "Ask a maintainer or open an issue if:", "players cannot reach each other (a router that picks a new port for every destination cannot be punched) or anything looks wrong. Some features are unverified."),
        ]),
        ("Directions", [
            ("bullet", "get a release and check it with sha256sum -c SHA256SUMS"),
            ("bullet", "run ./re-psn (or the container); it listens on port 36100"),
            ("bullet", "players point their game's client at host:36100"),
            ("bullet", "on different networks, forward the port or use a VPN"),
        ]),
        ("Other information", [
            ("bullet", "version {version}; Linux amd64 and arm64, Windows amd64"),
            ("bullet", "licence MIT; unofficial, not affiliated with Sony"),
            ("bullet", "the Windows build is cross-built and has not been run by us; CI runs the server's tests on Windows"),
        ]),
        ("Inactive ingredients", [
            ("plain", "Go standard library, a C client (client/psnr.c), Docker or Podman (optional), a Tauri window (optional)"),
        ]),
        ("Questions or comments?", [
            ("plain", "github.com/undecuple/re-psn"),
        ]),
    ],
}


def text(x, y, s, size=SIZE, bold=False, anchor="start"):
    return (f'<text x="{x:g}" y="{y:g}" font-size="{size:g}" font-weight="{700 if bold else 400}" text-anchor="{anchor}" fill="{TEXT}">'
            f'{escape(s)}</text>')


def lines(s, width=WRAP):
    return textwrap.wrap(s, width=width, break_long_words=False, break_on_hyphens=False)


def render(version):
    o, y = [], 0.0
    o.append(f'<rect width="{W}" height="@H@" fill="{BG}"/>')
    # title block: a thick rule under the title, the serving size, then the ingredient row inside a box
    o.append(text(X0 + PAD, 39, LABEL["title"], 30, True))
    o.append(f'<rect x="{X0}" y="46" width="{X1 - X0}" height="4" fill="{ACCENT}"/>')
    o.append(text(X0 + PAD, 64, LABEL["serving"]))
    o.append(f'<rect x="{X0}" y="70" width="{X1 - X0}" height="1" fill="{FRAME}"/>')
    head, purpose, name, what = LABEL["ingredient"]
    o.append(text(X0 + PAD, 86, head, 12, True))
    o.append(text(X1 - PAD, 86, purpose, 12, True, "end"))
    o.append(text(X0 + PAD, 102, name, 12))
    what_lines = lines(what, PURPOSE_WRAP)
    for i, ln in enumerate(what_lines):
        o.append(text(X1 - PAD - PURPOSE_X, 102 + i * 14, ln, 12))
    y = 102 + (len(what_lines) - 1) * 14 + 12
    top = 46
    o.append(f'<rect x="{X0 + 1}" y="{top + 4 + 1}" width="{X1 - X0 - 2}" height="{y - top - 5:g}" fill="none" stroke="{FRAME}" stroke-width="2"/>')
    y += 8
    for heading, items in LABEL["boxes"]:
        box_top = y
        y += 19
        o.append(text(X0 + PAD + 1, y, heading, 13, True))
        y += 5
        first = True
        for item in items:
            kind = item[0]
            if kind == "note":
                # a rule above each warning after the first, label in bold run into the text
                y += 5
                if not first:
                    o.append(f'<rect x="{X0 + 1}" y="{y - 4:g}" width="{X1 - X0 - 2}" height="1" fill="{FRAME}"/>')
                    y += 4
                body = lines(item[1] + " " + item[2])
                for i, ln in enumerate(body):
                    y += LEAD
                    if i == 0:
                        o.append(f'<text x="{X0 + PAD + 1}" y="{y:g}" font-size="{SIZE:g}" fill="{TEXT}"><tspan font-weight="700">{escape(item[1])}</tspan>'
                                 f'<tspan font-weight="400">{escape(ln[len(item[1]):])}</tspan></text>')
                    else:
                        o.append(text(X0 + PAD + 1, y, ln))
                y += 3
            elif kind == "bullet":
                body = lines(item[1].format(version=version), WRAP - 3)
                for i, ln in enumerate(body):
                    y += LEAD
                    if i == 0:
                        o.append(f'<rect x="{X0 + PAD + 1}" y="{y - 7:g}" width="4" height="4" fill="{ACCENT}"/>')
                    o.append(text(X0 + PAD + 12, y, ln))
            else:
                for ln in lines(item[1]):
                    y += LEAD
                    o.append(text(X0 + PAD + 1, y, ln))
            first = False
        y += 7
        o.append(f'<rect x="{X0 + 1}" y="{box_top + 1:g}" width="{X1 - X0 - 2}" height="{y - box_top - 1:g}" fill="none" stroke="{FRAME}" stroke-width="2"/>')
        y += 6
    h = int(y - 4)
    svg = (f'<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 {W} {h}" width="{W * SCALE:g}" height="{round(h * SCALE)}" role="img" '
           f'aria-label="Project Facts: a label in the style of a drug facts panel, listing what re-psn does, its limits and how to use it" '
           f'font-family="{FONT}" shape-rendering="crispEdges" text-rendering="geometricPrecision">' + "".join(o).replace("@H@", str(h)) + "</svg>\n")
    return svg


def read_version(root):
    return (root / "VERSION").read_text(encoding="utf-8").strip()


def main(argv=None):
    ap = argparse.ArgumentParser(description="Generate docs/project-facts.svg, or check it.")
    ap.add_argument("--check", action="store_true", help="write nothing; exit 1 if the file is missing or stale")
    ap.add_argument("--root", default=str(ROOT), help="repository root")
    a = ap.parse_args(argv)
    root = pathlib.Path(a.root)
    svg = render(read_version(root))
    path = root / OUT
    if a.check:
        if not path.exists() or path.read_bytes() != svg.encode("utf-8"):
            print(f"project-facts: {OUT} is missing or is not what this script generates (run python3 tools/project_facts.py)", file=sys.stderr)
            return 1
        print("project-facts: OK")
        return 0
    path.parent.mkdir(parents=True, exist_ok=True)
    with open(path, "w", encoding="utf-8", newline="\n") as f:
        f.write(svg)
    print(f"project-facts: wrote {OUT}")
    return 0


if __name__ == "__main__":
    sys.exit(main())
