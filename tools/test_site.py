"""Tests for tools/site.py: escaping, deterministic output, --check on a stale or missing file, the stylesheet tokens.

    python3 -m unittest            # from the repository root

The module is loaded by path under another name because "site" is also a standard library module.
"""
import contextlib
import importlib.util
import io
import json
import pathlib
import re
import sys
import tempfile
import unittest

HERE = pathlib.Path(__file__).resolve().parent
ROOT = HERE.parent
sys.path.insert(0, str(HERE))
_spec = importlib.util.spec_from_file_location("re_psn_site", HERE / "site.py")
sitegen = importlib.util.module_from_spec(_spec)
_spec.loader.exec_module(sitegen)
import features  # noqa: E402

HEAD = "\t".join(features.COLUMNS)
ROWS = [
    "rooms-basic\tRooms\tRooms <b>&\tCreate and join rooms, see `x<y>` and [the roadmap](ROADMAP.md).\tavailable\t0.1.0\tserver/rooms.go\tNote 'q'",
    "rooms-private\tRooms\tPrivate rooms\tPasswords.\tplanned\t\t\t",
    "net-relay\tNetwork\tRelay\tThrough the server.\tunverified\tmain\tserver/relay.go\t",
]
README = """# t

| Flag | Variable | Default | What it does |
|---|---|---|---|
| `-addr` | `RE_PSN_ADDR` | `:36100` | TCP and UDP address <script> |
| `-http` | `RE_PSN_HTTP` | `127.0.0.1:36101` | Status page |

after the table
"""
CHANGELOG = """# Changelog

## [Unreleased]

### Added
- A thing. More words.

## [0.1.0] - 2026-01-02

### Added
- First.
"""
CSS = """/* palette:dark */
:root { --bg0: #000000; --fg0: #ffffff; }
/* /palette */
"""
ICON = ('<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 8 8"><title>t</title>'
        '<rect width="8" height="8" fill="#000000"/><rect width="2" height="2" fill="#ffffff"/></svg>')
PALETTE = {"modes": {"dark": {"bg0": "#000000", "fg0": "#ffffff"}}}


class Fixture:
    def __init__(self):
        self.tmp = tempfile.TemporaryDirectory()
        self.root = pathlib.Path(self.tmp.name)
        for d in ("docs/art", "site", "server"):
            (self.root / d).mkdir(parents=True)
        for path, _, _ in sitegen.DOCS:
            p = self.root / path
            p.parent.mkdir(parents=True, exist_ok=True)
            p.write_text("x\n")
        (self.root / "server" / "rooms.go").write_text("package main\n")
        (self.root / "server" / "relay.go").write_text("package main\n")
        (self.root / features.DATA).write_text("\n".join([HEAD] + ROWS) + "\n", encoding="utf-8")
        (self.root / "VERSION").write_text("0.1.0\n")
        (self.root / "README.md").write_text(README)
        (self.root / "CHANGELOG.md").write_text(CHANGELOG)
        (self.root / "go.mod").write_text("module x\n\ngo 1.22\n")
        (self.root / "docs/art/palette.json").write_text(json.dumps(PALETTE))
        (self.root / "site/style.css").write_text(CSS)
        (self.root / "site/terminal.js").write_text("// stub\n")
        (self.root / "site/favicon.svg").write_text(ICON)
        for path, _, _ in sitegen.DOCS:
            p = self.root / path
            p.parent.mkdir(parents=True, exist_ok=True)
            if not p.exists():
                p.write_text("x\n")

    def run(self, *args):
        out, err = io.StringIO(), io.StringIO()
        with contextlib.redirect_stdout(out), contextlib.redirect_stderr(err):
            code = sitegen.main(["--root", str(self.root), *args])
        return code, out.getvalue(), err.getvalue()

    def read(self, rel):
        return (self.root / rel).read_bytes()


class SiteTest(unittest.TestCase):
    def setUp(self):
        self.f = Fixture()
        self.addCleanup(self.f.tmp.cleanup)

    def test_writes_then_check_passes(self):
        self.assertEqual(self.f.run()[0], 0)
        code, out, err = self.f.run("--check")
        self.assertEqual((code, err), (0, ""), out)

    def test_output_is_deterministic(self):
        self.f.run()
        first = (self.f.read("site/index.html"), self.f.read("site/data.json"))
        self.f.run()
        self.assertEqual(first, (self.f.read("site/index.html"), self.f.read("site/data.json")))

    def test_escaping(self):
        self.f.run()
        page = self.f.read("site/index.html").decode()
        self.assertIn("Rooms &lt;b&gt;&amp;", page)
        self.assertNotIn("<b>", page)
        self.assertIn("<code>x&lt;y&gt;</code>", page)
        self.assertNotIn("<script>", page)
        self.assertIn("TCP and UDP address &lt;script&gt;", page)

    def test_repository_links_and_csp(self):
        self.f.run()
        page = self.f.read("site/index.html").decode()
        self.assertIn(f'<a href="{features.REPO_URL}/blob/main/ROADMAP.md">the roadmap</a>', page)
        self.assertIn("script-src &#x27;self&#x27;; style-src &#x27;self&#x27;;", page)
        self.assertIn("connect-src &#x27;self&#x27;", page)
        self.assertNotIn("&#x27;unsafe", page)
        self.assertEqual(page.count("<title>"), 1)
        self.assertEqual(page.count("<h1"), 1)

    def test_only_the_one_local_script_and_no_outside_load(self):
        self.f.run()
        page = self.f.read("site/index.html").decode()
        scripts = re.findall(r"<script\b[^>]*>", page)
        self.assertEqual(scripts, ['<script src="terminal.js" defer>'])
        self.assertNotIn("</script>", page.replace('<script src="terminal.js" defer></script>', ""))
        self.assertIsNone(re.search(r"\sstyle=|\son[a-z]+=|<style|<iframe|<img|<object|<embed|<form", page))
        for tagtext in re.findall(r"<(?:link|script)\b[^>]*>", page):
            self.assertIsNone(re.search(r'(?:src|href)="(?:[a-z]+:)?//|(?:src|href)="https?:', tagtext), tagtext)
        self.assertIn('<link rel="icon" href="favicon.svg" type="image/svg+xml">', page)

    def test_icon_checks(self):
        self.assertEqual(self.f.run("--check")[0], 1)  # generated files missing, but the icon itself is fine
        self.f.run()
        for bad, word in ((ICON.replace("#ffffff", "#123456"), "not in docs/art/palette.json"),
                          (ICON.replace("</svg>", "<script>x</script></svg>"), "<script>"),
                          (ICON.replace("</svg>", '<use href="x.svg"/></svg>'), "not allowed"),
                          ("<svg", "favicon.svg")):
            (self.f.root / "site/favicon.svg").write_text(bad)
            code, _, err = self.f.run("--check")
            self.assertEqual(code, 1)
            self.assertIn(word, err)
        (self.f.root / "site/favicon.svg").unlink()
        self.assertIn("favicon.svg", self.f.run("--check")[2])

    def test_missing_script_file(self):
        self.f.run()
        (self.f.root / "site/terminal.js").unlink()
        self.assertIn("site/terminal.js is missing", self.f.run("--check")[2])

    def test_real_script_is_safe(self):
        js = (ROOT / "site/terminal.js").read_text(encoding="utf-8")
        for bad in ("eval(", "new Function", "innerHTML", "outerHTML", "insertAdjacentHTML", "document.write", "XMLHttpRequest",
                    "WebSocket", "sendBeacon", "document.cookie", "sessionStorage", "indexedDB", "import("):
            self.assertNotIn(bad, js)
        self.assertEqual(js.count("fetch("), 1)
        self.assertIn("fetch('data.json'", js)
        # Every address in the script is github.com; nothing else is named.
        for url in re.findall(r"https?://[^\s'\"`)]+", js):
            self.assertTrue(url.startswith("https://github.com/"), url)

    def test_real_icon_is_valid(self):
        out, err = io.StringIO(), io.StringIO()
        problems = []
        sitegen.check_icon(ROOT, problems)
        self.assertEqual(problems, [])

    def test_data_comes_from_sources(self):
        self.f.run()
        d = json.loads(self.f.read("site/data.json"))
        self.assertEqual(d["version"], "0.1.0")
        self.assertEqual(d["status_counts"]["available"], 1)
        self.assertEqual([s["flag"] for s in d["settings"]], ["-addr", "-http"])
        self.assertEqual(d["roadmap"]["Done"], ["rooms-basic", "net-relay"])
        self.assertEqual(d["changelog"]["unreleased"]["sections"]["Added"], ["A thing."])
        self.assertEqual(d["changelog"]["latest"]["version"], "0.1.0")

    def test_check_fails_on_stale_file(self):
        self.f.run()
        (self.f.root / "VERSION").write_text("0.2.0\n")
        code, _, err = self.f.run("--check")
        self.assertEqual(code, 1)
        self.assertIn("site/index.html is not what the sources generate", err)
        self.assertIn("site/data.json is not what the sources generate", err)

    def test_check_fails_on_hand_edit(self):
        self.f.run()
        p = self.f.root / "site/index.html"
        p.write_bytes(p.read_bytes() + b"<!-- x -->\n")
        code, _, err = self.f.run("--check")
        self.assertEqual(code, 1)
        self.assertIn("site/index.html is not what", err)

    def test_check_fails_on_missing_file(self):
        code, _, err = self.f.run("--check")
        self.assertEqual(code, 1)
        self.assertIn("site/index.html is missing", err)

    def test_check_writes_nothing(self):
        self.f.run("--check")
        self.assertFalse((self.f.root / "site/index.html").exists())

    def test_css_token_mismatch(self):
        (self.f.root / "site/style.css").write_text(CSS.replace("#ffffff; }", "#fffffe; }"))
        code, _, err = self.f.run("--check")
        self.assertEqual(code, 1)
        self.assertIn("--fg0 in the dark block", err)

    def test_css_must_not_load_remote(self):
        (self.f.root / "site/style.css").write_text(CSS + "@import url(https://x.example/a.css);\n")
        code, _, err = self.f.run("--check")
        self.assertEqual(code, 1)
        self.assertIn("another site", err)

    def test_forbidden_word_stops_the_build(self):
        rows = ROWS + ["ha-x\tRooms\tHandled by AI\tText.\tplanned\t\t\t"]
        (self.f.root / features.DATA).write_text("\n".join([HEAD] + rows) + "\n", encoding="utf-8")
        code, _, err = self.f.run()
        self.assertEqual(code, 1)
        self.assertIn("'AI'", err)
        self.assertFalse((self.f.root / "site/index.html").exists())

    def test_missing_settings_table(self):
        (self.f.root / "README.md").write_text("# t\n")
        code, _, err = self.f.run()
        self.assertEqual(code, 1)
        self.assertIn("no settings table", err)

    def test_real_repository_is_current(self):
        # The same check CI runs, so a stale site/ fails here too.
        out, err = io.StringIO(), io.StringIO()
        with contextlib.redirect_stdout(out), contextlib.redirect_stderr(err):
            code = sitegen.main(["--check", "--root", str(ROOT)])
        self.assertEqual(code, 0, err.getvalue())


# ---------------------------------------------------------------- colour contrast (WCAG 2.x) from docs/art/palette.json and site/style.css

def _lum(hexcolour):
    chans = [int(hexcolour[i:i + 2], 16) / 255 for i in (1, 3, 5)]
    lin = [c / 12.92 if c <= 0.03928 else ((c + 0.055) / 1.055) ** 2.4 for c in chans]
    return 0.2126 * lin[0] + 0.7152 * lin[1] + 0.0722 * lin[2]


def contrast(a, b):
    la, lb = sorted((_lum(a), _lum(b)), reverse=True)
    return (la + 0.05) / (lb + 0.05)


# Every pair a text colour is drawn in: (text role, background role). A role is a CSS variable of site/style.css.
TAGS = ("ok", "warn", "unv", "info", "plan", "dim", "bad")
TEXT_PAIRS = (
    [("text", "page"), ("text", "panel"), ("text", "hover"), ("text", "raised")]
    + [("strong", bg) for bg in ("page", "panel", "raised", "select", "hover")]
    + [("muted", bg) for bg in ("page", "panel", "raised")]
    + [("c-cmd", "panel")]
    + [("link", "page"), ("c-h1", "page"), ("c-h1", "raised"), ("c-h2", "page"), ("c-h3", "page"), ("c-cmd", "page"),
       ("c-prompt", "page"), ("c-code", "panel"), ("c-id", "page"), ("c-err", "page"), ("c-key", "raised"), ("page", "strong")]
    + [("tt-" + t, "page") for t in TAGS]
    # the numbered output: syntax colours, gutter numbers, header
    + [("c-num", "page"), ("c-opt", "page"), ("c-env", "page"), ("c-label", "page")]
)

# Non-text UI (WCAG 1.4.11, 3:1): (the role drawn, the background beside it). The file frame and the gutter rule are `line` on the
# page; the focus ring is `strong` on every background a focusable sits on.
NONTEXT_PAIRS = (
    [("line", "page")]
    + [("strong", bg) for bg in ("page", "panel", "raised", "hover")]
)


def _css_roles(css, mode):
    """The role -> palette slot map of one mode; the copies of a mode must agree."""
    blocks = re.findall(r"/\* roles:%s \*/(.*?)/\* /roles \*/" % mode, css, re.S)
    maps = [dict(re.findall(r"--([a-z0-9-]+):\s*var\(--([a-z0-9]+)\)", b)) for b in blocks]
    return blocks, maps


class ContrastTest(unittest.TestCase):
    css = (ROOT / "site/style.css").read_text(encoding="utf-8")
    modes = json.loads((ROOT / "docs/art/palette.json").read_text(encoding="utf-8"))["modes"]

    def resolve(self, mode):
        """Map every role to a hex colour for a mode (palette slots, shared roles, mode roles)."""
        start = self.css.index("/* Roles that do not change")
        shared = dict(re.findall(r"--([a-z0-9-]+):\s*var\(--([a-z0-9]+)\)", self.css[start:self.css.index("/* Text roles.")]))
        blocks, maps = _css_roles(self.css, mode)
        self.assertEqual(len(blocks), 1, mode)
        roles = dict(shared, **maps[0])
        palette = self.modes[mode]
        return {r: palette[slot] for r, slot in roles.items() if slot in palette}

    def test_every_text_pair_is_at_least_4_5(self):
        for mode in ("dark",):
            r = self.resolve(mode)
            for fg, bg in TEXT_PAIRS:
                self.assertIn(fg, r, f"{mode}: no role {fg}")
                self.assertIn(bg, r, f"{mode}: no role {bg}")
                ratio = contrast(r[fg], r[bg])
                self.assertGreaterEqual(ratio, 4.5, f"{mode}: {fg} {r[fg]} on {bg} {r[bg]} is {ratio:.2f}:1")

    def test_every_non_text_pair_is_at_least_3(self):
        for mode in ("dark",):
            r = self.resolve(mode)
            for fg, bg in NONTEXT_PAIRS:
                ratio = contrast(r[fg], r[bg])
                self.assertGreaterEqual(ratio, 3.0, f"{mode}: {fg} {r[fg]} beside {bg} {r[bg]} is {ratio:.2f}:1")

    def test_output_frame_uses_the_checked_roles(self):
        # The frame and the gutter rule are drawn in `line`; the gutter numbers are `muted` text.
        for rule in (".tui-out .file {", ".tui-out .file-head {", ".tui-out .gut {"):
            block = self.css[self.css.index(rule):].split("}", 1)[0]
            self.assertRegex(block, r"border-(?:top|bottom|right): 1px solid var\(--line\)", rule)
        gut = self.css[self.css.index(".tui-out .gut {"):].split("}", 1)[0]
        self.assertIn("color: var(--muted)", gut)
        self.assertIn("user-select: none", gut)
        self.assertIn(".tui-out .gut::before { content: attr(data-n); }", self.css)
        self.assertIn("border: 1px solid var(--line)", self.css[self.css.index(".tui .chip {"):].split("}", 1)[0])

    def test_stylesheet_text_colours_are_all_covered(self):
        # Every `color: var(--x)` in the stylesheet (not border-color and so on) must be a role in the pair table above.
        covered = {fg for fg, _ in TEXT_PAIRS}
        used = set(re.findall(r"(?<![-\w])color:\s*var\(--([a-z0-9-]+)\)", self.css))
        self.assertEqual(used - covered, set(), "text colour roles with no contrast pair")
        self.assertIsNone(re.search(r"(?<![-\w])color:[ ]*(?![ ]|var\(|inherit|transparent)", self.css.split("@media print")[0]), "a literal text colour")

    def test_every_palette_slot_is_used(self):
        outside = re.sub(r"/\* palette:\w+ \*/.*?/\* /palette \*/", "", self.css, flags=re.S)
        for slot in self.modes["dark"]:
            self.assertIn(f"var(--{slot})", outside, f"--{slot} has no job in site/style.css")



class BannerTest(unittest.TestCase):
    def test_banner_is_decorative_and_has_no_theme_switch(self):
        f = Fixture()
        self.addCleanup(f.tmp.cleanup)
        f.run()
        page = f.read("site/index.html").decode()
        self.assertIn('<svg class="banner"', page)
        self.assertIn('aria-hidden="true"', page.split('<svg class="banner"', 1)[1].split(">", 1)[0])
        for cls in ("bn-frame", "bn-name", "banner-side"):
            self.assertIn(cls, page)
        side = page.split('<svg class="banner-side"', 1)[1].split("</svg>", 1)[0]
        self.assertIn('aria-hidden="true"', side)
        # the name is drawn as paths (no text, so no font to cut it off), and there is no caption
        for svg in (page.split('<svg class="banner"', 1)[1].split("</svg>", 1)[0], side):
            self.assertNotIn("<text", svg)
            self.assertNotIn("letter-spacing", svg)
        self.assertNotIn("theme", page.lower().replace("color-scheme", ""))
        self.assertIsNone(re.search(r"\sstyle=|<script>|\son[a-z]+=", page))


class AutocompleteMarkupTest(unittest.TestCase):
    def test_listbox_is_made_by_script_only(self):
        page = (ROOT / "site/index.html").read_text(encoding="utf-8")
        for needle in ("combobox", "listbox", "tui-list", "tui-input", "aria-activedescendant"):
            self.assertNotIn(needle, page)
        js = (ROOT / "site/terminal.js").read_text(encoding="utf-8")
        for needle in ("'combobox'", "'listbox'", "'option'", "aria-activedescendant"):
            self.assertIn(needle, js)
        css = (ROOT / "site/style.css").read_text(encoding="utf-8")
        self.assertIn(".tui-list", css)
        self.assertNotIn("transition", css)


if __name__ == "__main__":
    unittest.main()
