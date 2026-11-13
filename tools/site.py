#!/usr/bin/env python3
"""Project website: site/index.html and site/data.json are generated; site/style.css is written by hand.

    python3 tools/site.py            # write site/index.html and site/data.json
    python3 tools/site.py --check    # write nothing; exit 1 if a file is missing or not what the sources generate
    python3 tools/site.py --root DIR # use another repository root (the tests use this)

Sources, one for each kind of content, so no number or status is copied by hand:

    docs/features.tsv   the features list, the status counts and the roadmap summary
    VERSION             the version, and the release and container names in the install steps
    README.md           the settings table (the one that starts with | Flag | Variable | Default |)
    CHANGELOG.md        the Unreleased section and the latest release
    go.mod              the Go version named in the build steps

The page works without script. site/terminal.js (hand-written, no dependencies) turns it into a terminal-style interface when
it runs; it reads site/data.json from the same origin and nothing else. The page loads nothing from another site (see CSP
below). site/style.css has the colour tokens of
docs/art/palette.json on :root; --check fails when a token differs from the palette.
"""
import argparse
import html
import json
import pathlib
import re
import sys

sys.path.insert(0, str(pathlib.Path(__file__).resolve().parent))
import features  # noqa: E402

OUT_HTML = "site/index.html"
OUT_JSON = "site/data.json"
CSS = "site/style.css"
JS = "site/terminal.js"
ICON = "site/favicon.svg"
PALETTE = "docs/art/palette.json"
REPO_URL = features.REPO_URL
TITLE = "re:psn by undecuple"
CSP = "default-src 'self'; script-src 'self'; style-src 'self'; img-src 'self' data:; connect-src 'self'"
UNOFFICIAL = "Unofficial; not affiliated with Sony."
AI_LINE = "Developed with AI agent assistance."

# status -> colour class (see the .tag rules in site/style.css)
TAG_CLASS = {
    "available": "ok", "unverified": "unv", "partial": "warn", "in-progress": "info",
    "planned": "plan", "later": "dim", "declined": "bad",
}
# The banner: the name in pixel letters inside a double frame, as on the undecuple page. Drawn with classes (the stylesheet sets the
# colours from the palette tokens, since the page allows no inline style); the title bar below it carries the real text.
GLYPHS = {
    "R": ("11110", "10001", "10001", "11110", "10100", "10010", "10001"),
    "E": ("11111", "10000", "10000", "11110", "10000", "10000", "11111"),
    ":": ("0", "0", "1", "0", "0", "1", "0"),
    "P": ("11110", "10001", "10001", "11110", "10000", "10000", "10000"),
    "S": ("01111", "10000", "10000", "01110", "00001", "00001", "11110"),
    "N": ("10001", "11001", "10101", "10101", "10011", "10001", "10001"),
}


def banner_svg():
    cell, x0, y0 = 11, 44, 46
    x, cells = x0, []
    for ch in "RE:PSN":
        rows = GLYPHS[ch]
        for r, row in enumerate(rows):
            for c, bit in enumerate(row):
                if bit == "1":
                    cells.append(f"M{x + c * cell} {y0 + r * cell}h{cell}v{cell}h-{cell}z")
        x += (len(rows[0]) + 1) * cell
    return ('<svg class="banner" viewBox="0 0 671 200" aria-hidden="true" focusable="false" xmlns="http://www.w3.org/2000/svg">'
            '<rect class="bn-frame" x="3" y="3" width="665" height="194"/><rect class="bn-frame" x="8" y="8" width="655" height="184"/>'
            '<text class="bn-sub" x="647" y="34" text-anchor="end" font-family="monospace" font-size="14">self-hosted</text>'
            f'<path class="bn-name" d="{"".join(cells)}"/>'
            '<path class="bn-rule" d="M8 146H663"/>'
            '<text class="bn-text" x="335" y="176" text-anchor="middle" font-family="monospace" font-size="16" textLength="560" lengthAdjust="spacing">'
            'ROOMS AND LEADERBOARDS</text></svg>\n')


# roadmap group -> the statuses in it, in the order the page shows them
ROADMAP = (
    ("Done", ("available", "unverified", "partial")),
    ("Now", ("in-progress",)),
    ("Next", ("planned",)),
    ("Later", ("later",)),
    ("Not planned", ("declined",)),
)
# Paragraphs written once and used by the page and by the terminal interface (through data.json). Inline `code` and [text](link).
ABOUT = (
    "re-psn answers the matchmaking and leaderboard calls of a statically recompiled PS3 game, for a group running its own "
    "server: a LAN, a VPN, or friends connecting to one person's machine. The server keeps rooms and leaderboards and tells "
    "each player where the others are. Game traffic goes directly between players, and through the server only when two "
    "players cannot reach each other and the host has turned the relay on.",
    "It is a fork of [psnr](https://github.com/sp00nznet/psnr) by sp00nz: a Go server and a one-file C client, "
    "with no accounts and no public instance.",
)
NOTICE = UNOFFICIAL + " No game files, keys or firmware are in the repository or needed from it. Play only games you own."
CREDITS = (
    "sp00nz ([sp00nznet](https://github.com/sp00nznet)) wrote [psnr](https://github.com/sp00nznet/psnr): the server, the C client, "
    "the protocol, NAT traversal, the relay and the NAT lab.",
    "undecuple ([undecuple](https://github.com/undecuple)) maintains re-psn and wrote everything added since the fork: the release and "
    "container tooling, the AppImage, hardening and limits, rooms and invite codes, the desktop app, this website, the documentation "
    "and the tests.",
    "[ps3recomp](https://github.com/sp00nznet/ps3recomp) is the recompiler whose NP modules use the client.",
    "The site's colours follow the [undecuple page](https://undecuple.github.io/me/), which credits them to [guts.nvim](https://github.com/vossenwout/guts.nvim) (that repository states no licence). The light colours of the desktop app and the status page are from [gruvbox-material](https://github.com/sainnhe/gruvbox-material) (MIT, Copyright (c) 2020 sainnhe).",
)
LICENCE = ("MIT, see [LICENSE](LICENSE); upstream's copyright line is kept and a line for the fork's changes is added. PlayStation, PS3 and PSN are "
           "trademarks of Sony Interactive Entertainment, used only to say what the server stands in for.")
# (path, title, what is in it); each path must exist
DOCS = (
    ("docs/website.md", "Website controls", "Numbered terminal output, keyboard controls and contributor checks"),
    ("docs/architecture.md", "Architecture", "The parts, the threadless client, why game traffic is peer to peer, the relay"),
    ("docs/api.md", "Protocol", "Framing, session, rooms, leaderboards, UDP, relay, HTTP"),
    ("docs/running.md", "Running a server", "Flags, variables, UDP source check, reverse proxy, logging, ports, firewall, storage"),
    ("docs/connecting.md", "Connecting players", "LAN, port forwarding, a public server, Tailscale or ZeroTier"),
    ("docs/features.md", "Features", "Every feature with its status and evidence"),
    ("ROADMAP.md", "Roadmap", "What is done, planned and out of scope"),
    ("CHANGELOG.md", "Changelog", "What changed in each release"),
    ("docs/releasing.md", "Releasing", "The version, the bump and cutting a pre-release"),
    ("deploy/README.md", "Deployment examples", "Settings file, systemd unit, Traefik, nginx and HAProxy"),
    ("docker/README.md", "Container", "Building and running with Docker or Podman"),
    ("lab/README.md", "NAT lab", "Players behind routers, in Docker"),
    ("CONTRIBUTING.md", "Contributing", "Ground rules and the tests to run"),
    ("SECURITY.md", "Security", "How to report a vulnerability privately"),
)

SETTINGS_HEAD = re.compile(r"^\|\s*Flag\s*\|\s*Variable\s*\|\s*Default\s*\|\s*What it does\s*\|\s*$")
RELEASE_HEAD = re.compile(r"^## \[([^\]]+)\](?:\s*-\s*(\S+))?\s*$")
INLINE_CODE = re.compile(r"`([^`]+)`")
INLINE_LINK = re.compile(r"\[([^\]]+)\]\(([^)\s]+)\)")


class SourceError(Exception):
    pass


# ---------------------------------------------------------------- reading the sources

def read(root, rel):
    try:
        return (root / rel).read_text(encoding="utf-8")
    except OSError as e:
        raise SourceError(f"{rel}: {e.strerror or e}") from e


def read_version(root):
    v = read(root, "VERSION").strip()
    if not re.fullmatch(r"[0-9]+\.[0-9]+\.[0-9]+", v):
        raise SourceError(f"VERSION: '{v}' is not X.Y.Z")
    return v


def read_features(root):
    problems = []
    try:
        rows = features.read_rows(root, problems)
    except OSError as e:
        raise SourceError(f"{features.DATA}: {e.strerror or e}") from e
    if problems:
        raise SourceError("\n".join(problems))
    return [{k: v for k, v in r.items() if k != "line"} for r in rows]


def read_settings(root):
    """The README settings table as a list of dicts (backticks removed)."""
    rows = []
    inside = False
    for line in read(root, "README.md").splitlines():
        if not inside:
            inside = bool(SETTINGS_HEAD.match(line))
            continue
        if line.startswith("|---"):
            continue
        if not line.startswith("|"):
            break
        cells = [c.strip() for c in line.strip().strip("|").split("|")]
        if len(cells) != 4:
            raise SourceError(f"README.md: settings row has {len(cells)} cells: {line[:60]}")
        flag, var, default, what = (c.replace("`", "") for c in cells)
        rows.append({"flag": flag, "variable": var, "default": default, "description": what})
    if not rows:
        raise SourceError("README.md: no settings table (| Flag | Variable | Default | What it does |)")
    return rows


def first_sentence(text, limit=220):
    text = " ".join(text.split())
    m = re.search(r"\.(?=\s|$)", text)
    if m and m.end() <= limit:
        return text[:m.end()]
    if len(text) > limit:
        text = text[:limit].rsplit(" ", 1)[0] + " ..."
    return text


def read_changelog(root):
    """{'unreleased': {...}, 'latest': {...}}: each is {'version', 'date', 'sections': {'Added': [text, ...]}}."""
    releases = []
    cur = sec = None
    for line in read(root, "CHANGELOG.md").splitlines():
        m = RELEASE_HEAD.match(line)
        if m:
            cur = {"version": m.group(1), "date": m.group(2) or "", "sections": {}}
            releases.append(cur)
            sec = None
        elif cur is not None and line.startswith("### "):
            sec = cur["sections"].setdefault(line[4:].strip(), [])
        elif cur is not None and sec is not None:
            if line.startswith("- "):
                sec.append(line[2:].strip())
            elif line.startswith("  ") and sec and line.strip():
                sec[-1] += " " + line.strip()
    unreleased = next((r for r in releases if r["version"].lower() == "unreleased"), None)
    latest = next((r for r in releases if r["version"].lower() != "unreleased"), None)
    if latest is None:
        raise SourceError("CHANGELOG.md: no released version")
    out = {}
    for key, r in (("unreleased", unreleased), ("latest", latest)):
        if r is None:
            out[key] = None
            continue
        # The unreleased entries are listed; a release is summarised by counts and a link, the full text being in CHANGELOG.md.
        full = key == "unreleased"
        out[key] = {"version": r["version"], "date": r["date"],
                    "sections": {s: [first_sentence(b) for b in bullets] for s, bullets in r["sections"].items() if bullets} if full else {},
                    "counts": {s: len(bullets) for s, bullets in r["sections"].items() if bullets}}
    return out


def read_go_version(root):
    m = re.search(r"^go\s+(\S+)", read(root, "go.mod"), re.M)
    return m.group(1) if m else ""


# ---------------------------------------------------------------- the data file

def build_data(root):
    version = read_version(root)
    rows = read_features(root)
    settings = read_settings(root)
    for path, _, _ in DOCS:
        if not (root / path).exists():
            raise SourceError(f"site.py lists {path}, which does not exist")
    counts = {s: sum(1 for r in rows if r["status"] == s) for s in features.STATUS}
    roadmap = {name: [r["id"] for r in rows if r["status"] in statuses] for name, statuses in ROADMAP}
    changelog = read_changelog(root)
    go = read_go_version(root)
    stage = "pre-release" if version.startswith("0.") else "release"
    latest = changelog["latest"]["version"]
    by_flag = {r["flag"]: r for r in settings}
    game = by_flag.get("-addr", {}).get("default", "")
    web = by_flag.get("-http", {}).get("default", "")
    points = []
    if game:
        points.append(f"Game connection and address probes: `{game}`, TCP and UDP, open to every player.")
    if web:
        points.append(f"Status page: `{web}`, for the host only; it lists every online ID.")
    points.append("Each player's P2P port (UDP and TCP) must be reachable by the other players; the game opens it.")
    return {
        "version": version,
        "stage": stage,
        "repository": REPO_URL,
        "go": go,
        "status_counts": counts,
        "features": [dict(r, status_label=features.STATUS[r["status"]][0], status_class=TAG_CLASS[r["status"]]) for r in rows],
        "roadmap": roadmap,
        "settings": settings,
        "changelog": changelog,
        "about": {"paragraphs": list(ABOUT), "notice": NOTICE},
        "install": {
            "release_tag": f"v{latest}",
            "release_url": f"{REPO_URL}/releases/tag/v{latest}",
            "archives": "Archives for Linux (amd64, arm64) and Windows (amd64), and a Linux x86_64 AppImage, all unsigned, with a "
                        "`SHA256SUMS` file. Check a download on Linux:",
            "verify_unix": ["sha256sum -c SHA256SUMS --ignore-missing"],
            "verify_windows": "On Windows, compare the output of `Get-FileHash <archive>` with the line in `SHA256SUMS`.",
            "container": [f"docker pull ghcr.io/undecuple/re-psn:{latest}",
                          f"docker run --rm -p 36100:36100 -p 36100:36100/udp ghcr.io/undecuple/re-psn:{latest}"],
            "container_note": "Podman works the same way. See [docker/README.md](docker/README.md).",
            "source_note": f"You need {'Go ' + go + ' or newer' if go else 'Go'}; a C compiler only for the C client and its tests.",
            "source": ["go build -o re-psn ./server", "./re-psn"],
        },
        "connect": {
            "intro": "Players on one LAN or VPN need nothing forwarded. Otherwise the host forwards a port, runs a public server, or "
                     "uses an overlay network such as Tailscale or ZeroTier; the relay is the fallback when two players cannot reach each other.",
            "points": points,
            "client": "A game reaches the server through the C client in `client/psnr.c`, via ps3recomp's NP modules. That side is "
                      "upstream's work; [docs/connecting.md](docs/connecting.md) covers each way to connect.",
        },
        "credits": list(CREDITS),
        "licence": LICENCE,
        "links": [
            {"title": "Repository", "url": REPO_URL},
            {"title": "Issues", "url": f"{REPO_URL}/issues"},
            {"title": "Releases", "url": f"{REPO_URL}/releases"},
            {"title": "Security policy", "url": doc_url("SECURITY.md")},
            {"title": "Upstream psnr", "url": "https://github.com/sp00nznet/psnr"},
            {"title": "ps3recomp", "url": "https://github.com/sp00nznet/ps3recomp"},
        ],
        "docs": [{"path": p, "title": t, "summary": s, "url": doc_url(p)} for p, t, s in DOCS],
    }


# Words that must not appear in the generated files (the one AI_LINE excepted), whatever the sources say.
FORBIDDEN = re.compile(r"\b(AI|LLM|Claude|Anthropic|OpenAI|GPT)\b|\bD[0-9]{1,3}\b")


def guard(outputs):
    for rel, text in outputs.items():
        m = FORBIDDEN.search(text.replace(AI_LINE, ""))
        if m:
            line = text[:m.start()].count("\n") + 1
            raise SourceError(f"{rel}:{line}: generated text contains '{m.group(0)}'; reword the source it came from")


def render_json(data):
    return json.dumps(data, indent=1, ensure_ascii=False) + "\n"


# ---------------------------------------------------------------- the page

def esc(s):
    return html.escape(s, quote=True)


def doc_url(path):
    kind = "tree" if path.endswith("/") else "blob"
    return f"{REPO_URL}/{kind}/main/{path}"


def inline(text):
    """Escape text, then turn `code` and [text](link) into elements. Repository paths become repository links."""
    s = esc(text)
    s = INLINE_CODE.sub(lambda m: f"<code>{m.group(1)}</code>", s)

    def link(m):
        url = html.unescape(m.group(2))
        if not re.match(r"(https?://|mailto:)", url):
            if url.startswith("#"):
                return m.group(0)
            url = doc_url(url)
        return f'<a href="{esc(url)}">{m.group(1)}</a>'
    return INLINE_LINK.sub(link, s)


def tag(status):
    word = features.STATUS[status][0]
    return f'<span class="tag {TAG_CLASS[status]}">{esc(word)}</span>'


def anchor(slug):
    return re.sub(r"[^a-z0-9]+", "-", slug.lower()).strip("-")


def render_features(data, w):
    areas = []
    for r in data["features"]:
        if r["area"] not in areas:
            areas.append(r["area"])
    for a in areas:
        w(f'<h3 id="area-{anchor(a)}">{esc(a)}</h3>\n')
        w('<table class="stack feat">\n<thead><tr><th>Feature</th><th>Status</th><th>Since</th></tr></thead>\n<tbody>\n')
        for r in (x for x in data["features"] if x["area"] == a):
            since = esc(r["since"]) if r["since"] else "not released"
            detail = f'<br>{inline(r["detail"])}' if r["detail"] else ""
            note = f'<br><span class="note">{inline(r["note"])}</span>' if r["note"] else ""
            w(f'<tr id="f-{esc(r["id"])}"><td data-label="Feature"><strong>{esc(r["feature"])}</strong> '
              f'<code>{esc(r["id"])}</code>{detail}{note}</td>'
              f'<td data-label="Status">{tag(r["status"])}</td><td data-label="Since">{since}</td></tr>\n')
        w('</tbody>\n</table>\n')


def render_roadmap(data, w):
    names = {r["id"]: r for r in data["features"]}
    w('<div class="cols">\n')
    for name, _ in ROADMAP:
        ids = data["roadmap"][name]
        if not ids:
            continue
        w(f'<div><h3>{esc(name)} <span class="count">({len(ids)})</span></h3>\n<ul>\n')
        for i in ids:
            r = names[i]
            extra = f' {tag(r["status"])}' if r["status"] in ("unverified", "partial") else ""
            w(f'<li><a href="#f-{esc(i)}">{esc(r["feature"])}</a>{extra}</li>\n')
        w('</ul></div>\n')
    w('</div>\n')


def render_settings(data, w):
    w('<table class="stack set">\n<thead><tr><th>Flag</th><th>Variable</th><th>Default</th><th>What it does</th></tr></thead>\n<tbody>\n')
    for s in data["settings"]:
        w(f'<tr><td data-label="Flag"><code>{esc(s["flag"])}</code></td><td data-label="Variable"><code>{esc(s["variable"])}</code></td>'
          f'<td data-label="Default"><code>{esc(s["default"])}</code></td><td data-label="What it does">{inline(s["description"])}</td></tr>\n')
    w('</tbody>\n</table>\n')


def render_changes(data, w):
    ch = data["changelog"]
    for key in ("unreleased", "latest"):
        r = ch[key]
        if not r:
            continue
        label = "Unreleased" if key == "unreleased" else f'{r["version"]} ({esc(r["date"])})' if r["date"] else r["version"]
        w(f'<h3>{label}</h3>\n')
        if key == "latest":
            parts = ", ".join(f"{n} under {esc(s)}" for s, n in r["counts"].items())
            w(f'<p>{parts}.</p>\n' if parts else '')
        for sec, bullets in r["sections"].items():
            w(f'<h4>{esc(sec)}</h4>\n<ul>\n')
            for b in bullets:
                w(f'<li>{inline(b)}</li>\n')
            w('</ul>\n')


def render_page(data):
    v = data["version"]
    n = data["status_counts"]
    total = len(data["features"])
    ch = data["changelog"]
    o = []
    w = o.append
    w('<!doctype html>\n<!-- Generated by tools/site.py from the repository; do not edit by hand. -->\n')
    w('<html lang="en">\n<head>\n<meta charset="utf-8">\n')
    w('<meta name="viewport" content="width=device-width, initial-scale=1">\n')
    w(f'<meta http-equiv="Content-Security-Policy" content="{esc(CSP)}">\n')
    w('<meta name="color-scheme" content="dark">\n')
    w(f'<meta name="description" content="Matchmaking rooms and leaderboards for statically recompiled PS3 games, with NAT traversal and an optional relay. {esc(UNOFFICIAL)}">\n')
    w(f'<title>{esc(TITLE)}</title>\n')
    w('<link rel="icon" href="favicon.svg" type="image/svg+xml">\n<link rel="stylesheet" href="style.css">\n<script src="terminal.js" defer></script>\n</head>\n<body id="top">\n')
    w('<a class="skip" href="#overview">Skip to the content</a>\n')
    w(banner_svg())
    w('<header class="bar">\n')
    w(f'<h1 class="title"><strong>re:psn</strong> <span>by undecuple &middot; v{esc(v)} &middot; {esc(data["stage"])}</span></h1>\n')
    w('</header>\n')
    w('<nav class="bar sticky" aria-label="Sections"><ul>\n')
    for slug, label in (("overview", "Overview"), ("features", "Features"), ("roadmap", "Roadmap"), ("changes", "Changes"),
                        ("settings", "Settings"), ("install", "Install"), ("connect", "Connect"), ("docs", "Docs"),
                        ("credits", "Credits")):
        w(f'<li><a href="#{slug}">{label}</a></li>\n')
    w('</ul></nav>\n<main>\n')

    w('<section id="overview">\n<h2>Overview</h2>\n')
    for para in data["about"]["paragraphs"]:
        w(f'<p>{inline(para)}</p>\n')
    w(f'<p class="status-line">Version {esc(v)} ({esc(data["stage"])}). {total} features: '
      + ", ".join(f'{n[s]} {esc(features.STATUS[s][0])}' for s in features.STATUS if n[s]) + '.</p>\n')
    w(f'<p class="notice">{esc(data["about"]["notice"])}</p>\n</section>\n')

    w('<section id="features">\n<h2>Features</h2>\n')
    w(f'<p>One row per feature. A status rests on a test or a recorded run in the repository; the '
      f'<a href="{esc(doc_url("docs/features.md"))}">features page</a> has the evidence for each row.</p>\n')
    render_features(data, w)
    w('</section>\n')

    w('<section id="roadmap">\n<h2>Roadmap</h2>\n')
    w(f'<p>The same list grouped by status. No dates are promised; '
      f'<a href="{esc(doc_url("ROADMAP.md"))}">ROADMAP.md</a> has the order and the scope.</p>\n')
    render_roadmap(data, w)
    w('</section>\n')

    w('<section id="changes">\n<h2>Changes</h2>\n')
    render_changes(data, w)
    w(f'<p>The full history is in <a href="{esc(doc_url("CHANGELOG.md"))}">CHANGELOG.md</a>.</p>\n</section>\n')

    w('<section id="settings">\n<h2>Settings</h2>\n')
    w(f'<p>Each of the {len(data["settings"])} flags can also be set as a <code>RE_PSN_&lt;FLAG&gt;</code> variable. A flag on the '
      'command line wins, then the variable; a value that does not parse stops the server at start. It runs with no settings at all. '
      f'<a href="{esc(doc_url("docs/running.md"))}">docs/running.md</a> has the full text.</p>\n')
    render_settings(data, w)
    w('</section>\n')

    ins = data["install"]
    w('<section id="install">\n<h2>Install</h2>\n')
    w(f'<p>The latest release is <a href="{esc(ins["release_url"])}">{esc(ins["release_tag"])}</a>, a {esc(data["stage"])}.</p>\n')
    w('<h3>Release archives</h3>\n')
    w(f'<p>{inline(ins["archives"])}</p>\n<pre><code>{esc(chr(10).join(ins["verify_unix"]))}</code></pre>\n')
    w(f'<p>{inline(ins["verify_windows"])}</p>\n')
    w('<h3>Container</h3>\n')
    w(f'<pre><code>{esc(chr(10).join(ins["container"]))}</code></pre>\n')
    w(f'<p>{inline(ins["container_note"])}</p>\n')
    w('<h3>From source</h3>\n')
    w(f'<p>{esc(ins["source_note"])}</p>\n')
    w(f'<pre><code>{esc(chr(10).join(ins["source"]))}</code></pre>\n</section>\n')

    con = data["connect"]
    w('<section id="connect">\n<h2>Connect</h2>\n')
    w(f'<p>{esc(con["intro"])}</p>\n<ul>\n')
    for pt in con["points"]:
        w(f'<li>{inline(pt)}</li>\n')
    w(f'</ul>\n<p>{inline(con["client"])}</p>\n</section>\n')

    w('<section id="docs">\n<h2>Docs</h2>\n<ul class="docs">\n')
    for d in data["docs"]:
        w(f'<li><a href="{esc(d["url"])}">{esc(d["title"])}</a> <span class="note">{esc(d["summary"])}</span></li>\n')
    w(f'</ul>\n<p>Source and issues: <a href="{REPO_URL}">{esc(REPO_URL)}</a>.</p>\n</section>\n')

    w('<section id="credits">\n<h2>Credits and licence</h2>\n<ul>\n')
    w('<li><strong>sp00nz</strong> (<a href="https://github.com/sp00nznet">sp00nznet</a>) wrote '
      '<a href="https://github.com/sp00nznet/psnr">psnr</a>: the server, the C client, the protocol, NAT traversal, the relay and '
      'the NAT lab.</li>\n')
    w('<li><strong>undecuple</strong> (<a href="https://github.com/undecuple">undecuple</a>) maintains re-psn and wrote everything '
      'added since the fork: the release and container tooling, the AppImage, hardening and limits, rooms and invite codes, '
      'the desktop app, this website, the documentation and the tests.</li>\n')
    w('<li><a href="https://github.com/sp00nznet/ps3recomp">ps3recomp</a> is the recompiler whose NP modules use the client.</li>\n')
    w('<li>The site\'s colours follow the <a href="https://undecuple.github.io/me/">undecuple page</a>, which credits them to <a href="https://github.com/vossenwout/guts.nvim">guts.nvim</a> (that repository states no licence). The light colours of the desktop app and the status page are from <a href="https://github.com/sainnhe/gruvbox-material">gruvbox-material</a> (MIT, Copyright (c) 2020 sainnhe).</li>\n')
    w('</ul>\n')
    w(f'<p>MIT, see <a href="{esc(doc_url("LICENSE"))}">LICENSE</a>; upstream\'s copyright line is kept and a line for the fork\'s changes is added. PlayStation, PS3 and PSN are '
      'trademarks of Sony Interactive Entertainment, used only to say what the server stands in for.</p>\n')
    w('</section>\n')

    w('</main>\n<footer>\n')
    w(f'<p>{esc(UNOFFICIAL)} {esc(AI_LINE)}</p>\n</footer>\n')
    w('<a class="totop" href="#top"><span aria-hidden="true">&uarr;</span> Top</a>\n</body>\n</html>\n')
    return "".join(o)


# ---------------------------------------------------------------- the stylesheet's colour tokens

def check_css(root, problems):
    """Every palette token declared in site/style.css must equal docs/art/palette.json."""
    try:
        css = read(root, CSS)
        modes = json.loads(read(root, PALETTE))["modes"]
    except (SourceError, ValueError, KeyError) as e:
        problems.append(f"{CSS}: {e}")
        return
    for mode in ("dark",):  # the site has one theme; palette.json keeps the light mode for the desktop app and the status page
        blocks = re.findall(r"/\* palette:%s \*/(.*?)/\* /palette \*/" % mode, css, re.S)
        if not blocks:
            problems.append(f"{CSS}: no '/* palette:{mode} */ ... /* /palette */' block")
        for block in blocks:
            found = dict(re.findall(r"--([a-z0-9]+):\s*(#[0-9a-fA-F]{6})\s*;", block))
            for name, value in modes[mode].items():
                if found.get(name, "").lower() != value.lower():
                    problems.append(f"{CSS}: --{name} in the {mode} block is '{found.get(name, 'missing')}', palette.json has {value}")
    if "<script" in css or "@import" in css or "url(http" in css:
        problems.append(f"{CSS}: must not load anything from another site")


def check_icon(root, problems):
    """site/favicon.svg exists, is XML without script, foreignObject or an outside reference, and uses palette colours only."""
    import xml.etree.ElementTree as ET
    try:
        text = read(root, ICON)
        tree = ET.fromstring(text)
        allowed = {v.lower() for mode in json.loads(read(root, PALETTE))["modes"].values() for v in mode.values()}
    except (SourceError, ValueError, KeyError, ET.ParseError) as e:
        problems.append(f"{ICON}: {e}")
        return
    for node in tree.iter():
        name = node.tag.split("}")[-1].lower()
        if name in ("script", "foreignobject", "image", "use", "style"):
            problems.append(f"{ICON}: <{name}> is not allowed")
        for k, v in node.attrib.items():
            k = k.split("}")[-1].lower()
            if k == "href" or k.startswith("on") or "url(" in v or "http" in v.lower() and k != "xmlns":
                problems.append(f"{ICON}: attribute {k} must not reference anything")
            if k in ("fill", "stroke") and v.lower() not in allowed and v.lower() != "none":
                problems.append(f"{ICON}: colour {v} is not in {PALETTE}")


def main(argv=None):
    ap = argparse.ArgumentParser(description="Generate site/index.html and site/data.json, or check them.")
    ap.add_argument("--check", action="store_true", help="write nothing; exit 1 if a file is missing or stale")
    ap.add_argument("--root", default=str(pathlib.Path(__file__).resolve().parents[1]), help="repository root")
    a = ap.parse_args(argv)
    root = pathlib.Path(a.root)
    try:
        data = build_data(root)
    except SourceError as e:
        print(f"site: problems:\n  - {e}", file=sys.stderr)
        return 1
    outputs = {OUT_HTML: render_page(data), OUT_JSON: render_json(data)}
    try:
        guard(outputs)
    except SourceError as e:
        print(f"site: problems:\n  - {e}", file=sys.stderr)
        return 1
    problems = []
    check_css(root, problems)
    check_icon(root, problems)
    if not (root / JS).exists():
        problems.append(f"{JS} is missing (the page loads it)")
    if a.check:
        for rel, text in outputs.items():
            p = root / rel
            if not p.exists():
                problems.append(f"{rel} is missing (run python3 tools/site.py)")
            elif p.read_bytes() != text.encode("utf-8"):
                problems.append(f"{rel} is not what the sources generate (run python3 tools/site.py)")
    if problems:
        print("site: problems:", file=sys.stderr)
        for p in problems:
            print("  - " + p, file=sys.stderr)
        return 1
    if not a.check:
        for rel, text in outputs.items():
            (root / rel).parent.mkdir(parents=True, exist_ok=True)
            with open(root / rel, "w", encoding="utf-8", newline="\n") as f:
                f.write(text)
    print(f"site: {'OK' if a.check else 'wrote ' + OUT_HTML + ' and ' + OUT_JSON}: "
          f"{len(data['features'])} features, {len(data['settings'])} settings, version {data['version']}")
    return 0


if __name__ == "__main__":
    sys.exit(main())
