#!/usr/bin/env python3
"""README status badges: docs/img/badges/<name>.svg, generated from the status words in docs/features.tsv.

    python3 tools/badges.py            # write the badges
    python3 tools/badges.py --check    # write nothing; exit 1 on a missing or different file, an unknown id, or a stray file
    python3 tools/badges.py --root DIR # use another repository root (the tests use this)

Each badge is a flat two-cell label: a dark label cell (the component) and a coloured value cell (a status word taken from
the feature rows, never a stronger one). Only the status word of docs/features.tsv is shown. "available" means what that
page says it means: merged, with a test or a recorded run. It does not mean a game was tried, and no badge says so.

COMPONENTS lists, per badge, the feature ids that decide it. When a component spans several rows, the WEAKEST status among
them is shown (order in WEAK_FIRST: declined, planned, later, in-progress, partial, unverified, available). So a badge never
says more than its least-finished row:

    Server         rooms-basic, rooms-data, scores-boards, players-names
    Relay          net-relay
    Sign-in passes net-pass-relay, net-pass-connect (relay gate and connect gate)
    C client       client-c
    Desktop app    client-desktop
    LAN discovery  net-lan-discover, client-lan-discover, app-lan-discover (server, C client, desktop app)
    Admin API      admin-api

Each badge is a chosen subset of the rows in its area, not every row of it. The Desktop app badge reads client-desktop only;
app-lan-discover (also Desktop app) is covered by the LAN discovery badge. Add an id to COMPONENTS to widen a badge.

Text width: there are no font metrics here, so every character is counted as CHAR_W units wide (a monospace-style table:
one fixed width for any character, a little wider than most sans-serif text) and each cell gets PAD on both sides. Text is
centred in its cell, so a viewer that draws it narrower still looks even. The output is plain SVG: no script, link, image,
use or foreignObject, and the same bytes on every run. Every badge is parsed as XML before it is written or compared.
"""
import argparse
import pathlib
import sys
import xml.etree.ElementTree as ET
from xml.sax.saxutils import escape

OUT_DIR = "docs/img/badges"
DATA = "docs/features.tsv"
COLUMNS = ("id", "area", "feature", "detail", "status", "since", "evidence", "note")

H = 20                  # badge height
CHAR_W = 7              # width of every character, in drawing units (font-size 11 monospace-style)
PAD = 8                 # space on each side of a cell's text
FONT_SIZE = 11
FONT = "DejaVu Sans Mono, Menlo, Consolas, monospace"
LABEL_BG, LABEL_FG = "#161719", "#e1ffe5"

# status -> (word shown, value cell colour, text colour)
STATUS_STYLE = {
    "available": ("available", "#a7d8b0", "#161719"),
    "partial": ("partial", "#997e95", "#e1ffe5"),
    "in-progress": ("in progress", "#997e95", "#e1ffe5"),
    "unverified": ("unverified", "#8288a0", "#e1ffe5"),
    "planned": ("planned", "#554a62", "#e1ffe5"),
    "later": ("later", "#554a62", "#e1ffe5"),
    "declined": ("declined", "#554a62", "#e1ffe5"),
}
# Weakest first.
WEAK_FIRST = ("declined", "planned", "later", "in-progress", "partial", "unverified", "available")

# name (file stem), label, deciding ids
COMPONENTS = (
    ("server", "Server", ("rooms-basic", "rooms-data", "scores-boards", "players-names")),
    ("relay", "Relay", ("net-relay",)),
    ("sign-in-passes", "Sign-in passes", ("net-pass-relay", "net-pass-connect")),
    ("c-client", "C client", ("client-c",)),
    ("desktop-app", "Desktop app", ("client-desktop",)),
    ("lan-discovery", "LAN discovery", ("net-lan-discover", "client-lan-discover", "app-lan-discover")),
    ("admin-api", "Admin API", ("admin-api",)),
)


class BadgeError(Exception):
    pass


def read_statuses(root):
    """id -> status from docs/features.tsv."""
    try:
        text = (root / DATA).read_text(encoding="utf-8")
    except OSError as e:
        raise BadgeError(f"{DATA}: {e}")
    lines = [l for l in text.splitlines() if l.strip()]
    if not lines or tuple(lines[0].split("\t")) != COLUMNS:
        raise BadgeError(f"{DATA}: unexpected header")
    out = {}
    for l in lines[1:]:
        cells = l.split("\t")
        if len(cells) != len(COLUMNS):
            raise BadgeError(f"{DATA}: a row has {len(cells)} cells, expected {len(COLUMNS)}")
        out[cells[0].strip()] = cells[4].strip()
    return out


def weakest(ids, statuses):
    """The weakest status among ids. An id missing from the data, or a status with no style, is an error."""
    found = []
    for i in ids:
        if i not in statuses:
            raise BadgeError(f"feature id {i} is not in {DATA}")
        s = statuses[i]
        if s not in STATUS_STYLE:
            raise BadgeError(f"feature id {i} has unknown status '{s}'")
        found.append(s)
    return min(found, key=WEAK_FIRST.index)


def cell_width(text):
    return len(text) * CHAR_W + 2 * PAD


def render(label, status):
    """One badge as SVG text."""
    if status not in STATUS_STYLE:
        raise BadgeError(f"unknown status '{status}'")
    word, bg, fg = STATUS_STYLE[status]
    w1, w2 = cell_width(label), cell_width(word)
    w = w1 + w2
    title = f"{label}: {word}"
    ty = H // 2 + 4
    svg = (
        f'<svg xmlns="http://www.w3.org/2000/svg" width="{w}" height="{H}" viewBox="0 0 {w} {H}" role="img" '
        f'aria-label="{escape(title)}">'
        f'<title>{escape(title)}</title>'
        f'<clipPath id="r"><rect width="{w}" height="{H}" rx="3"/></clipPath>'
        f'<g clip-path="url(#r)">'
        f'<rect width="{w1}" height="{H}" fill="{LABEL_BG}"/>'
        f'<rect x="{w1}" width="{w2}" height="{H}" fill="{bg}"/>'
        f'</g>'
        f'<g font-family="{FONT}" font-size="{FONT_SIZE}" text-anchor="middle">'
        f'<text x="{w1 / 2:g}" y="{ty}" fill="{LABEL_FG}">{escape(label)}</text>'
        f'<text x="{w1 + w2 / 2:g}" y="{ty}" fill="{fg}">{escape(word)}</text>'
        f'</g></svg>\n'
    )
    ET.fromstring(svg)  # raises ParseError when it is not well-formed XML
    return svg


def build(root):
    """file name -> bytes for every badge."""
    statuses = read_statuses(root)
    out = {}
    for name, label, ids in COMPONENTS:
        out[f"{name}.svg"] = render(label, weakest(ids, statuses)).encode("utf-8")
    return out


def main(argv=None):
    ap = argparse.ArgumentParser(description="Generate the README status badges, or check them.")
    ap.add_argument("--check", action="store_true", help="write nothing; exit 1 on any difference or unknown id")
    ap.add_argument("--root", default=str(pathlib.Path(__file__).resolve().parents[1]), help="repository root")
    a = ap.parse_args(argv)
    root = pathlib.Path(a.root)
    try:
        files = build(root)
    except (BadgeError, ET.ParseError) as e:
        print(f"badges: {e}", file=sys.stderr)
        return 1
    d = root / OUT_DIR
    if a.check:
        problems = []
        for n, b in files.items():
            p = d / n
            if not p.exists():
                problems.append(f"{OUT_DIR}/{n} is missing")
            elif p.read_bytes() != b:
                problems.append(f"{OUT_DIR}/{n} differs from what tools/badges.py generates")
        if d.is_dir():
            for p in sorted(d.iterdir()):
                if p.name not in files:
                    problems.append(f"{OUT_DIR}/{p.name} is not a generated badge")
        if problems:
            print("badges: problems (run python3 tools/badges.py):", file=sys.stderr)
            for p in problems:
                print("  - " + p, file=sys.stderr)
            return 1
        print(f"badges: OK: {len(files)} badges")
        return 0
    d.mkdir(parents=True, exist_ok=True)
    for n, b in files.items():
        (d / n).write_bytes(b)
    print(f"badges: wrote {len(files)} badges in {OUT_DIR}")
    return 0


if __name__ == "__main__":
    sys.exit(main())
