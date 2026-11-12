"""Tests for tools/art/make_art.py: the palette values, deterministic output, and the font pins.

    python3 -m unittest            # from the repository root; the rendering tests skip without Pillow

The rendering tests use the fonts in work/art/fonts/ when they are there (after --fetch-fonts), else Pillow's built-in
font, so they need no network.
"""
import io
import json
import pathlib
import sys
import unittest
import zipfile

sys.path.insert(0, str(pathlib.Path(__file__).resolve().parent))
import make_art  # noqa: E402

try:
    import PIL  # noqa: F401
    HAVE_PIL = True
except ImportError:
    HAVE_PIL = False

# The undecuple page palette (https://undecuple.github.io/me/style.css), as in docs/art/palette.json.
DARK = {
    "bg0": "#101113", "bg1": "#161719", "bg2": "#161719", "bg3": "#2b2733", "bg4": "#2b2733", "bg5": "#554a62",
    "fg0": "#9f9e99", "fg1": "#e1ffe5",
    "grey0": "#554a62", "grey1": "#787487", "grey2": "#7a837c",
    "red": "#ac7f7b", "orange": "#997e95", "yellow": "#997e95", "green": "#a7d8b0", "aqua": "#a7d8b0",
    "blue": "#8288a0", "purple": "#997e95",
}


def fonts():
    try:
        return make_art.Fonts()
    except (OSError, ValueError):
        return make_art.Fonts(stand_in=True)


class PaletteTest(unittest.TestCase):
    def setUp(self):
        self.data = json.loads((make_art.ROOT / make_art.PALETTE).read_text(encoding="utf-8"))

    def test_dark_matches(self):
        self.assertEqual(self.data["modes"]["dark"], DARK)

    def test_light_has_the_same_names(self):
        light = self.data["modes"]["light"]
        self.assertEqual(set(light), set(DARK))
        for k, v in light.items():
            self.assertRegex(v, r"^#[0-9a-f]{6}$", k)

    def test_slots_name_colours(self):
        for slot, name in self.data["slots"].items():
            self.assertIn(name, DARK, slot)


@unittest.skipUnless(HAVE_PIL, "Pillow is not installed")
class RenderTest(unittest.TestCase):
    def test_two_runs_identical(self):
        f = fonts()
        a, pa = make_art.render(f)
        b, pb = make_art.render(f)
        self.assertEqual(sorted(a), sorted(b))
        for name in a:
            self.assertEqual(a[name], b[name], name)
        self.assertEqual(make_art.manifest(a), make_art.manifest(b))
        self.assertEqual(pa, pb)

    def test_outputs_are_palette_pngs_within_margin(self):
        from PIL import Image
        f = fonts()
        files, probs = make_art.render(f)
        self.assertEqual(set(files), {"banner.png", "social.png"} | {img for img, _ in make_art.TERMS.values()})
        if not f.stand_in:
            self.assertEqual(probs, [])
        for name, data in files.items():
            with Image.open(io.BytesIO(data)) as im:
                self.assertEqual(im.mode, "P", name)
        with Image.open(io.BytesIO(files["banner.png"])) as im:
            self.assertEqual(im.size, make_art.BANNER)
        self.assertLess(len(files["social.png"]), 1_000_000)
        with Image.open(io.BytesIO(files["social.png"])) as im:
            self.assertEqual(im.size, (1280, 640))
            self.assertEqual(im.mode, "P")

    def test_safe_area_reported(self):
        safe = make_art.SOCIAL_SAFE
        self.assertEqual(make_art.safe_problems("x.png", (1280, 640), (safe[0], safe[1], 1280 - safe[0], 640 - safe[1]), safe), [])
        self.assertTrue(make_art.safe_problems("x.png", (1280, 640), (safe[0] - 1, safe[1], 1000, 500), safe))
        self.assertTrue(make_art.safe_problems("x.png", (1280, 640), (safe[0], safe[1], 1000, 640 - safe[1] + 1), safe))
        self.assertGreaterEqual(safe[0], make_art.MARGIN)
        self.assertGreaterEqual(safe[1], make_art.MARGIN)

    def test_margin_problem_reported(self):
        self.assertEqual(make_art.margin_problems("x.png", (200, 100), (40, 40, 160, 60)), [])
        self.assertTrue(make_art.margin_problems("x.png", (200, 100), (39, 40, 160, 60)))
        self.assertTrue(make_art.margin_problems("x.png", (200, 100), (40, 40, 161, 60)))


@unittest.skipUnless(HAVE_PIL, "needs Pillow")
class VersionCheckTest(unittest.TestCase):
    """--check accepts the listed zlib versions and refuses others before it renders anything."""

    def problems(self, zlib):
        saved = make_art.zlib_version
        make_art.zlib_version = lambda: zlib
        try:
            return make_art.check(fonts())
        finally:
            make_art.zlib_version = saved

    def test_unlisted_zlib_refused(self):
        p = self.problems("1.2.13")
        self.assertEqual(len(p), 1)
        self.assertIn("zlib 1.2.13", p[0])

    def test_listed_zlib_not_a_problem(self):
        for z in ("1.3", "1.3.1"):
            self.assertFalse([x for x in self.problems(z) if "zlib" in x], z)


class FetchTest(unittest.TestCase):
    """The fetch refuses a download or a member whose SHA-256 is not the pinned one."""

    def opener(self, payload):
        class R(io.BytesIO):
            def __enter__(self):
                return self

            def __exit__(self, *a):
                return False
        return lambda url, timeout=0: R(payload)

    def test_wrong_zip_refused(self):
        import tempfile
        with tempfile.TemporaryDirectory() as d:
            with self.assertRaisesRegex(ValueError, "pinned"):
                make_art.fetch_fonts(d, opener=self.opener(b"not the release"))
            self.assertEqual(list(pathlib.Path(d).iterdir()), [])

    def test_wrong_member_refused(self):
        import tempfile
        buf = io.BytesIO()
        with zipfile.ZipFile(buf, "w") as z:
            for member, _ in make_art.FONT_FILES.values():
                z.writestr(member, b"changed")
        data = buf.getvalue()
        saved = dict(make_art.FONT_RELEASE)
        make_art.FONT_RELEASE["sha256"] = make_art.sha256(data)
        try:
            with tempfile.TemporaryDirectory() as d:
                with self.assertRaisesRegex(ValueError, "pinned"):
                    make_art.fetch_fonts(d, opener=self.opener(data))
        finally:
            make_art.FONT_RELEASE.clear()
            make_art.FONT_RELEASE.update(saved)


if __name__ == "__main__":
    unittest.main()
