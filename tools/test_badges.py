"""Tests for tools/badges.py.

    python3 -m unittest            # from the repository root
"""
import contextlib
import io
import pathlib
import re
import sys
import tempfile
import unittest
import xml.etree.ElementTree as ET

sys.path.insert(0, str(pathlib.Path(__file__).resolve().parent))
import badges  # noqa: E402

HEAD = "\t".join(badges.COLUMNS)


def rows_for(status_by_id):
    return [f"{i}\tA\tF\td\t{s}\t\t\t" for i, s in status_by_id.items()]


def all_ids():
    return [i for _, _, ids in badges.COMPONENTS for i in ids]


class Fixture:
    def __init__(self, overrides=None, drop=()):
        self.tmp = tempfile.TemporaryDirectory()
        self.root = pathlib.Path(self.tmp.name)
        (self.root / "docs").mkdir()
        st = {i: "available" for i in all_ids() if i not in drop}
        st.update(overrides or {})
        (self.root / badges.DATA).write_text("\n".join([HEAD] + rows_for(st)) + "\n", encoding="utf-8")

    def run(self, *args):
        out, err = io.StringIO(), io.StringIO()
        with contextlib.redirect_stdout(out), contextlib.redirect_stderr(err):
            code = badges.main(["--root", str(self.root), *args])
        return code, out.getvalue(), err.getvalue()


class BadgeTests(unittest.TestCase):
    def test_status_mapping(self):
        for status, (word, bg, fg) in badges.STATUS_STYLE.items():
            svg = badges.render("X", status)
            self.assertIn(f'fill="{bg}"', svg)
            self.assertIn(f">{word}</text>", svg)
        self.assertEqual(badges.STATUS_STYLE["available"][1:], ("#a7d8b0", "#161719"))
        self.assertEqual(badges.STATUS_STYLE["partial"][1], "#997e95")
        self.assertEqual(badges.STATUS_STYLE["in-progress"][1], "#997e95")
        self.assertEqual(badges.STATUS_STYLE["unverified"][1], "#8288a0")
        self.assertEqual(badges.STATUS_STYLE["planned"][1], "#554a62")
        self.assertEqual(badges.STATUS_STYLE["later"][1], "#554a62")

    def test_no_word_stronger_than_the_data(self):
        self.assertEqual(set(badges.STATUS_STYLE), set(badges.WEAK_FIRST))
        for _, (word, _, _) in badges.STATUS_STYLE.items():
            self.assertNotIn("tested", word)
            self.assertNotIn("works", word)

    def test_unknown_status_fails(self):
        with self.assertRaises(badges.BadgeError):
            badges.render("X", "stable")

    def test_unknown_id_fails(self):
        f = Fixture(drop=("net-relay",))
        code, _, err = f.run()
        self.assertEqual(code, 1)
        self.assertIn("net-relay", err)
        code, _, err = f.run("--check")
        self.assertEqual(code, 1)
        self.assertIn("net-relay", err)

    def test_weakest_status_wins(self):
        st = {"a-b": "available", "c-d": "partial", "e-f": "unverified"}
        self.assertEqual(badges.weakest(("a-b", "c-d", "e-f"), st), "partial")
        st["g-h"] = "planned"
        self.assertEqual(badges.weakest(("a-b", "c-d", "g-h"), st), "planned")
        self.assertEqual(badges.weakest(("a-b",), st), "available")

    def test_component_uses_weakest_row(self):
        f = Fixture({"net-pass-connect": "in-progress"})
        self.assertEqual(f.run()[0], 0)
        svg = (f.root / badges.OUT_DIR / "sign-in-passes.svg").read_text()
        self.assertIn("in progress", svg)
        self.assertIn("#997e95", svg)

    def test_deterministic(self):
        f = Fixture()
        self.assertEqual(badges.build(f.root), badges.build(f.root))
        self.assertEqual(f.run()[0], 0)
        first = {p.name: p.read_bytes() for p in (f.root / badges.OUT_DIR).iterdir()}
        self.assertEqual(f.run()[0], 0)
        self.assertEqual(first, {p.name: p.read_bytes() for p in (f.root / badges.OUT_DIR).iterdir()})
        self.assertEqual(len(first), len(badges.COMPONENTS))

    def test_xml_valid_and_safe(self):
        f = Fixture()
        for b in badges.build(f.root).values():
            root = ET.fromstring(b)
            tags = {e.tag.split("}")[-1] for e in root.iter()}
            self.assertFalse(tags & {"script", "image", "use", "foreignObject", "a"}, tags)
            text = b.decode()
            self.assertNotIn("href", text)
            self.assertNotIn("<script", text)
            self.assertIsNotNone(root.find("{http://www.w3.org/2000/svg}title"))
            self.assertTrue(root.get("aria-label"))
            self.assertEqual(root.get("role"), "img")

    def test_label_with_markup_is_escaped(self):
        svg = badges.render("A<&>B", "later")
        ET.fromstring(svg)
        self.assertNotIn("A<&>B", svg)

    def test_width_table(self):
        svg = badges.render("Server", "available")
        w = badges.cell_width("Server") + badges.cell_width("available")
        self.assertIn(f'width="{w}"', svg)
        self.assertEqual(badges.cell_width("abc"), 3 * badges.CHAR_W + 2 * badges.PAD)

    def test_check(self):
        f = Fixture()
        self.assertEqual(f.run("--check")[0], 1)  # missing
        self.assertEqual(f.run()[0], 0)
        self.assertEqual(f.run("--check")[0], 0)
        p = f.root / badges.OUT_DIR / "relay.svg"
        p.write_bytes(p.read_bytes() + b" ")
        code, _, err = f.run("--check")
        self.assertEqual(code, 1)
        self.assertIn("relay.svg", err)
        self.assertEqual(f.run()[0], 0)
        (f.root / badges.OUT_DIR / "stray.svg").write_text("<svg/>")
        self.assertEqual(f.run("--check")[0], 1)

    def test_check_fails_when_status_changes(self):
        f = Fixture()
        f.run()
        (f.root / badges.DATA).write_text("\n".join([HEAD] + rows_for({i: "planned" for i in all_ids()})) + "\n")
        self.assertEqual(f.run("--check")[0], 1)

    def test_repo_components_exist(self):
        root = pathlib.Path(__file__).resolve().parents[1]
        self.assertEqual(badges.main(["--check", "--root", str(root)]), 0)

    def test_docstring_says_badges_are_a_chosen_subset(self):
        self.assertIn("chosen subset", badges.__doc__)
        self.assertIn("app-lan-discover", badges.__doc__)

    def test_readme_says_no_login_service(self):
        root = pathlib.Path(__file__).resolve().parents[1]
        text = " ".join((root / "README.md").read_text(encoding="utf-8").split())
        self.assertIn("Sign-in passes have no login service yet", text)
        self.assertIn('alt="Sign-in passes: available"', text)


if __name__ == "__main__":
    unittest.main()
