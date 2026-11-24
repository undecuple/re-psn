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
        from PIL import Image, ImageChops
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
            # the cursor blinks: two frames of equal length, looping forever, the cursor on in the first
            self.assertEqual((im.n_frames, im.info.get("loop"), im.info.get("duration")), (2, 0, make_art.BLINK_MS))
            on = im.convert("RGB")
            im.seek(1)
            off = im.convert("RGB")
            self.assertEqual(on.size, off.size)
            diff = ImageChops.difference(on, off).getbbox()
            self.assertIsNotNone(diff)
            self.assertLess((diff[2] - diff[0]) * (diff[3] - diff[1]), 60 * 80 * 4)   # only the cursor differs
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


@unittest.skipUnless(HAVE_PIL, "Pillow is not installed")
class StripTest(unittest.TestCase):
    """The banner's status strip says what tools/badges.py says for docs/features.tsv, and no more."""

    def copy_root(self, d):
        d = pathlib.Path(d)
        for rel in [make_art.PALETTE, *make_art.STRIP_INPUTS] + [f"{make_art.TRANSCRIPTS}/{n}" for n in make_art.TERMS]:
            (d / rel).parent.mkdir(parents=True, exist_ok=True)
            (d / rel).write_bytes((make_art.ROOT / rel).read_bytes())
        return d

    def test_words_equal_the_badges(self):
        cells = make_art.strip_cells()
        self.assertEqual([c[0] for c in cells], [label for _n, label, _i in make_art.badges.COMPONENTS])
        st = make_art.badges.read_statuses(make_art.ROOT)
        for (label, word, key), (_n, _l, ids) in zip(cells, make_art.badges.COMPONENTS):
            self.assertEqual(key, make_art.badges.weakest(ids, st))
            self.assertEqual(word, make_art.badges.STATUS_STYLE[key][0])
        self.assertEqual(len(cells), 7)

    def test_strip_text_is_drawn(self):
        # every label and word of the strip is drawn, by the banner's own call
        from PIL import ImageDraw
        drawn = []
        orig = make_art.Ink.text

        def spy(self, xy, s, font, fill):
            drawn.append(s)
            return orig(self, xy, s, font, fill)
        make_art.Ink.text = spy
        try:
            cells = make_art.strip_cells()
            make_art.banner(fonts(), make_art.load_palette(make_art.ROOT), cells)
        finally:
            make_art.Ink.text = orig
        for label, word, _k in cells:
            self.assertIn(label, drawn)
            self.assertIn(word, drawn)

    def test_changed_status_changes_the_render(self):
        import tempfile
        f = fonts()
        base, _ = make_art.render(f)
        with tempfile.TemporaryDirectory() as t:
            d = self.copy_root(t)
            tsv = d / make_art.badges.DATA
            lines = tsv.read_text(encoding="utf-8").split("\n")
            ids = make_art.badges.COMPONENTS[1][2]          # Relay: net-relay
            out = []
            for l in lines:
                c = l.split("\t")
                if len(c) > 4 and c[0] == ids[0]:
                    c[4] = "planned" if c[4] != "planned" else "available"
                    l = "\t".join(c)
                out.append(l)
            tsv.write_text("\n".join(out), encoding="utf-8")
            self.assertNotEqual(make_art.strip_cells(d), make_art.strip_cells())
            changed, _ = make_art.render(f, d)
        self.assertNotEqual(changed["banner.png"], base["banner.png"])
        self.assertEqual(changed["social.png"], base["social.png"])

    def test_every_status_fits_inside_its_cell_and_the_margins(self):
        from PIL import Image, ImageDraw
        f = fonts()
        pal = make_art.load_palette(make_art.ROOT)
        for key in make_art.badges.WEAK_FIRST:
            word = make_art.badges.STATUS_STYLE[key][0]
            cells = [(label, word, key) for _n, label, _i in make_art.badges.COMPONENTS]
            _img, box = make_art.banner(f, pal, cells)
            self.assertEqual(make_art.margin_problems("banner.png", make_art.BANNER, box), [] if not f.stand_in else
                             make_art.margin_problems("banner.png", make_art.BANNER, box), key)
            if f.stand_in:
                continue
            cw = (make_art.BANNER[0] * make_art.SS - 2 * 64 * make_art.SS - 6 * 8 * make_art.SS) // 7
            for font in (f.get("regular", 14 * make_art.SS), f.get("bold", 15 * make_art.SS)):
                for text in [word] + [c[0] for c in cells]:
                    self.assertLessEqual(font.getlength(text) + 8 * make_art.SS, cw, text)


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
