"""Tests for tools/project_facts.py: the committed label is current, deterministic, self-contained and fits its boxes.

    python3 -m unittest            # from the repository root
"""
import importlib.util
import io
import contextlib
import pathlib
import re
import tempfile
import unittest
import xml.etree.ElementTree as ET

HERE = pathlib.Path(__file__).resolve().parent
ROOT = HERE.parent
_spec = importlib.util.spec_from_file_location("re_psn_project_facts", HERE / "project_facts.py")
pf = importlib.util.module_from_spec(_spec)
_spec.loader.exec_module(pf)


class ProjectFactsTest(unittest.TestCase):
    def test_committed_label_is_current(self):
        svg = pf.render(pf.read_version(ROOT))
        self.assertEqual((ROOT / pf.OUT).read_text(encoding="utf-8"), svg, "run python3 tools/project_facts.py")

    def test_output_is_deterministic_and_carries_the_version(self):
        a, b = pf.render("1.2.3"), pf.render("1.2.3")
        self.assertEqual(a, b)
        self.assertIn("version 1.2.3;", a)
        self.assertNotIn("{version}", a)

    def test_svg_is_self_contained(self):
        root = ET.fromstring(pf.render("1.2.3"))
        tags = {n.tag.split("}")[-1] for n in root.iter()}
        self.assertEqual(tags - {"svg", "rect", "text", "tspan"}, set())
        for n in root.iter():
            for k, v in n.attrib.items():
                self.assertNotIn("http", v.replace("http://www.w3.org/2000/svg", ""), k)
                self.assertFalse(k.split("}")[-1].startswith("on") or k.split("}")[-1] == "href", k)
        self.assertEqual(set(re.findall(r'(?:fill|stroke)="(#[0-9a-f]+)"', pf.render("1.2.3"))), {"#fff", "#000"})

    def test_every_line_fits_inside_its_box(self):
        for heading, items in pf.LABEL["boxes"]:
            for item in items:
                for text in item[1:]:
                    for line in pf.lines(text.format(version="10.20.30"), pf.WRAP):
                        self.assertLessEqual(len(line), pf.WRAP, line)

    def test_words_the_site_also_refuses(self):
        # the same words tools/site.py keeps out of generated files
        self.assertIsNone(re.search(r"\b(AI|LLM|Claude|Anthropic|OpenAI|GPT)\b", pf.render("1.2.3")))

    def test_check_flags_a_stale_or_missing_file(self):
        with tempfile.TemporaryDirectory() as d:
            root = pathlib.Path(d)
            (root / "VERSION").write_text("1.2.3\n")
            out, err = io.StringIO(), io.StringIO()
            with contextlib.redirect_stdout(out), contextlib.redirect_stderr(err):
                self.assertEqual(pf.main(["--root", d, "--check"]), 1)      # missing
                self.assertEqual(pf.main(["--root", d]), 0)
                self.assertEqual(pf.main(["--root", d, "--check"]), 0)
                (root / "VERSION").write_text("1.2.4\n")
                self.assertEqual(pf.main(["--root", d, "--check"]), 1)      # the version moved
            self.assertIn("run python3 tools/project_facts.py", err.getvalue())


if __name__ == "__main__":
    unittest.main()
