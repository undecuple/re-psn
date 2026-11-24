"""Tests for tools/third_party.py.

    python3 -m unittest            # from the repository root
"""
import contextlib
import io
import json
import pathlib
import sys
import tempfile
import unittest

sys.path.insert(0, str(pathlib.Path(__file__).resolve().parent))
import third_party  # noqa: E402


class Fixture:
    """A synthetic cargo metadata document with crate directories in a temp dir."""

    def __init__(self):
        self.tmp = tempfile.TemporaryDirectory()
        self.dir = pathlib.Path(self.tmp.name)
        self.packages = []
        self.deps = {}  # id -> [(id, kinds)]
        self.add("re-psn-shell", "0.1.0", "MIT", files=None)

    def add(self, name, version, licence, files=None, repo="https://example.invalid/" + "x", make_dir=True, **extra):
        d = self.dir / ("%s-%s" % (name, version))
        if make_dir:
            d.mkdir()
            (d / "Cargo.toml").write_text("")
            for fname, body in (files or {}).items():
                (d / fname).write_bytes(body if isinstance(body, bytes) else body.encode("utf-8"))
        pkg = {"name": name, "version": version, "id": "%s %s" % (name, version), "license": licence,
               "repository": repo, "manifest_path": str(d / "Cargo.toml")}
        pkg.update(extra)
        self.packages.append(pkg)
        self.deps.setdefault(pkg["id"], [])
        return pkg["id"]

    def link(self, parent, child, kind=None):
        self.deps[parent].append((child, kind))

    def meta(self):
        nodes = [{"id": i, "deps": [{"name": c.split()[0], "pkg": c, "dep_kinds": [{"kind": k, "target": None}]}
                                    for c, k in kids]} for i, kids in self.deps.items()]
        return {"packages": self.packages, "resolve": {"nodes": nodes, "root": "re-psn-shell 0.1.0"}}

    def build(self):
        return third_party.build(self.meta())

    def run(self, *args):
        mp = self.dir / "metadata.json"
        mp.write_text(json.dumps(self.meta()))
        out, err = io.StringIO(), io.StringIO()
        with contextlib.redirect_stdout(out), contextlib.redirect_stderr(err):
            code = third_party.main(["--metadata", str(mp), *args])
        return code, out.getvalue(), err.getvalue()


ROOT = "re-psn-shell 0.1.0"


class Expressions(unittest.TestCase):
    def ok(self, expr):
        return third_party.acceptable(third_party.parse(expr))

    def test_simple_and_or(self):
        self.assertTrue(self.ok("MIT"))
        self.assertTrue(self.ok("MIT OR Apache-2.0"))
        self.assertTrue(self.ok("MIT/Apache-2.0"))
        self.assertTrue(self.ok("(MIT OR GPL-3.0-only) AND Zlib"))

    def test_or_needs_one_and_needs_all(self):
        self.assertTrue(self.ok("GPL-3.0-only OR MIT"))
        self.assertFalse(self.ok("GPL-3.0-only OR LGPL-2.1-only"))
        self.assertFalse(self.ok("MIT AND GPL-3.0-only"))

    def test_with_exception(self):
        self.assertTrue(self.ok("Apache-2.0 WITH LLVM-exception"))
        self.assertTrue(self.ok("Apache-2.0 WITH LLVM-exception OR MIT"))
        self.assertFalse(self.ok("Apache-2.0 WITH Unknown-exception"))
        self.assertFalse(self.ok("GPL-2.0-only WITH LLVM-exception"))

    def test_openssl_not_allowed_and_case(self):
        self.assertFalse(self.ok("OpenSSL"))
        self.assertTrue(self.ok("mit or apache-2.0"))

    def test_malformed(self):
        for bad in ("", "MIT OR", "(MIT", "MIT)", "AND MIT", "MIT WITH", "MIT AND OR Zlib"):
            with self.assertRaises(ValueError, msg=bad):
                third_party.parse(bad)

    def test_source_offer(self):
        self.assertTrue(third_party.needs_source_offer(third_party.parse("MPL-2.0")))
        self.assertFalse(third_party.needs_source_offer(third_party.parse("MIT OR MPL-2.0")))
        self.assertFalse(third_party.needs_source_offer(third_party.parse("MIT")))


class Build(unittest.TestCase):
    def test_graph_selection(self):
        f = Fixture()
        a = f.add("a", "1.0.0", "MIT", {"LICENSE": "A text"})
        b = f.add("b", "1.0.0", "MIT", {"LICENSE": "B text"})
        c = f.add("c", "1.0.0", "MIT", {"LICENSE": "C text"})
        dev = f.add("devonly", "1.0.0", "GPL-3.0-only", {"LICENSE": "dev"})
        stray = f.add("stray", "1.0.0", "GPL-3.0-only", {"LICENSE": "stray"})
        f.link(ROOT, a)
        f.link(a, b, "build")
        f.link(b, c)
        f.link(ROOT, dev, "dev")
        text, problems = f.build()
        self.assertEqual(problems, [])
        for name in ("a 1.0.0", "b 1.0.0", "c 1.0.0"):
            self.assertIn("\n" + name + "\n", text)
        self.assertNotIn("devonly", text)
        self.assertNotIn("stray", text)
        self.assertNotIn("\nre-psn-shell 0.1.0\n", text)
        self.assertIn("superset", text)
        self.assertIn("NOTICE.md", text)

    def test_ordering_and_determinism(self):
        f = Fixture()
        for n, v in (("zeta", "1.0.0"), ("alpha", "2.0.0"), ("alpha", "1.0.0"), ("mid", "0.1.0")):
            f.link(ROOT, f.add(n, v, "MIT", {"LICENSE-MIT": "text of " + n + v}))
        t1, p1 = f.build()
        t2, p2 = f.build()
        self.assertEqual((p1, p2), ([], []))
        self.assertEqual(t1.encode(), t2.encode())
        pos = [t1.index("\n%s\n" % n) for n in ("alpha 1.0.0", "alpha 2.0.0", "mid 0.1.0", "zeta 1.0.0")]
        self.assertEqual(pos, sorted(pos))
        # Link order does not matter.
        g = Fixture()
        for n, v in (("mid", "0.1.0"), ("alpha", "1.0.0"), ("zeta", "1.0.0"), ("alpha", "2.0.0")):
            g.link(ROOT, g.add(n, v, "MIT", {"LICENSE-MIT": "text of " + n + v}))
        self.assertEqual(g.build()[0], t1)

    def test_shared_text_printed_once(self):
        f = Fixture()
        same = "MIT License\n\nCopyright (c) Someone\n"
        f.link(ROOT, f.add("one", "1.0.0", "MIT", {"LICENSE-MIT": same}))
        f.link(ROOT, f.add("two", "1.0.0", "MIT", {"LICENSE": same.replace("\n", "\r\n")}))
        f.link(ROOT, f.add("three", "1.0.0", "MIT", {"LICENSE": "different text"}))
        text, problems = f.build()
        self.assertEqual(problems, [])
        self.assertEqual(text.count("Copyright (c) Someone"), 1)
        self.assertIn("Used by: one 1.0.0, two 1.0.0", text)
        self.assertEqual(text.count("different text"), 1)

    def test_file_names_and_cap(self):
        f = Fixture()
        big = "x" * (third_party.MAX_FILE_BYTES + 50)
        f.link(ROOT, f.add("c", "1.0.0", "MIT OR Apache-2.0",
                           {"license-apache": "apache body", "COPYING.txt": "copying body", "Notice": "notice body",
                            "UNLICENSE": "unl body", "LICENCE-X": big, "README.md": "readme body", "Cargo.lock": "no"}))
        text, problems = f.build()
        self.assertEqual(problems, [])
        for body in ("apache body", "copying body", "notice body", "unl body"):
            self.assertIn(body, text)
        self.assertNotIn("readme body", text)
        self.assertIn("[truncated at 200 KiB]", text)
        self.assertLess(len(text), 3 * third_party.MAX_FILE_BYTES)

    def test_invalid_utf8_replaced(self):
        f = Fixture()
        f.link(ROOT, f.add("c", "1.0.0", "MIT", {"LICENSE": b"caf\xe9 licence"}))
        text, problems = f.build()
        self.assertEqual(problems, [])
        self.assertIn("caf� licence", text)

    def test_declared_licence_without_files_still_listed(self):
        f = Fixture()
        f.link(ROOT, f.add("bare", "1.0.0", "MIT OR Apache-2.0", {}, repo="https://example.invalid/bare"))
        text, problems = f.build()
        self.assertEqual(problems, [])
        self.assertIn("\nbare 1.0.0\n", text)
        self.assertIn("Licence: MIT OR Apache-2.0", text)
        self.assertIn("https://example.invalid/bare", text)
        self.assertIn("none in the crate", text)
        self.assertIn("Copyright (c) the authors of bare", text)

    def test_licence_file_only_is_listed_as_not_declared(self):
        f = Fixture()
        f.link(ROOT, f.add("odd", "1.0.0", None, {"LICENSE": "custom terms"}))
        text, problems = f.build()
        self.assertEqual(problems, [])
        self.assertIn("LICENSE NOT DECLARED", text)
        self.assertIn("custom terms", text)

    def test_mpl_paragraph(self):
        f = Fixture()
        f.link(ROOT, f.add("option-ext", "0.2.0", "MPL-2.0", {"LICENSE.txt": "MPL text"}, repo="https://example.invalid/option-ext"))
        f.link(ROOT, f.add("either", "1.0.0", "MIT OR MPL-2.0", {"LICENSE": "either text"}))
        text, problems = f.build()
        self.assertEqual(problems, [])
        self.assertEqual(text.count("Source Code Form"), 1)
        self.assertIn("https://crates.io/crates/option-ext/0.2.0.", text)
        self.assertIn("The repository of the crate is https://example.invalid/option-ext.", text)
        self.assertNotIn("Source: this crate is licensed under MPL-2.0. Its source code, the Source Code Form that the licence\n  refers to, is available at https://crates.io/crates/either", text)


def only_text(text):
    """The part of the output after the LICENCE TEXTS heading."""
    return text.split("LICENCE TEXTS", 1)[1]


class NoFile(unittest.TestCase):
    def one(self, licence, authors=None, extra=None, name="c"):
        f = Fixture()
        kw = {} if authors is None else {"authors": authors}
        f.link(ROOT, f.add(name, "1.2.3", licence, {}, **kw))
        for n, lic, files in (extra or []):
            f.link(ROOT, f.add(n, "0.1.0", lic, files))
        return f.build()

    def test_mit_and_bsd_templates(self):
        text, problems = self.one("MIT", ["A B <a@example.invalid>", "C D"])
        self.assertEqual(problems, [])
        body = only_text(text)
        self.assertIn("Copyright (c) A B, C D\n\nPermission is hereby granted", body)
        self.assertIn("No licence file ships in this crate; this is the standard text of the licence it declares, with the "
                      "copyright holder taken from its Cargo.toml authors.", body)
        text, problems = self.one("BSD-3-Clause", ["X"])
        self.assertEqual(problems, [])
        self.assertIn("Copyright (c) X\n\nRedistribution and use in source and binary forms", only_text(text))
        self.assertIn("Neither the name of the copyright holder", text)

    def test_author_emails_are_not_printed(self):
        text, problems = self.one("MIT", ["Ann Example <ann@example.org>", "<only@example.org>", "Bob"], name="dlopen2")
        self.assertEqual(problems, [])
        self.assertIn("Copyright (c) Ann Example, Bob\n", text)
        self.assertNotIn("@", text)

    def test_authors_fallback(self):
        for authors in (None, [], [" "]):
            text, problems = self.one("MIT", authors, name="dlopen2")
            self.assertEqual(problems, [])
            self.assertIn("Copyright (c) the authors of dlopen2\n", text)

    def test_or_picks_first_alternative_with_a_template(self):
        text, problems = self.one("Apache-2.0 OR MIT")  # libappindicator-sys
        self.assertEqual(problems, [])
        self.assertIn("standard MIT text", text)
        self.assertNotIn("Apache License", text)
        text, problems = self.one("Zlib OR BSD-3-Clause OR MIT")
        self.assertIn("standard BSD-3-Clause text", text)
        self.assertNotIn("standard MIT text", text)

    def test_and_needs_every_part(self):
        text, problems = self.one("MIT AND BSD-3-Clause")
        self.assertEqual(problems, [])
        self.assertIn("standard MIT text", text)
        self.assertIn("standard BSD-3-Clause text", text)
        text, problems = self.one("MIT AND Apache-2.0")
        self.assertIsNone(text)
        self.assertTrue(any("c 1.2.3" in p for p in problems))

    def test_apache_only_fails_loudly(self):
        text, problems = self.one("Apache-2.0", name="onlyapache")
        self.assertIsNone(text)
        self.assertEqual(len(problems), 1)
        self.assertIn("onlyapache 1.2.3", problems[0])
        self.assertIn("no licence file", problems[0])
        f = Fixture()
        f.link(ROOT, f.add("onlyapache", "1.0.0", "Apache-2.0", {}))
        out = f.dir / "x.txt"
        code, _, err = f.run("--out", str(out))
        self.assertEqual(code, 1)
        self.assertIn("onlyapache 1.0.0", err)
        self.assertFalse(out.exists())

    def test_apache_with_exception_has_no_template(self):
        text, problems = self.one("Apache-2.0 WITH LLVM-exception")
        self.assertIsNone(text)

    def test_mpl_points_to_supplied_text(self):
        text, problems = self.one("MPL-2.0", name="selectors", extra=[
            ("option-ext", "MPL-2.0", {"LICENSE.txt": "Mozilla Public License Version 2.0\n..."})])
        self.assertEqual(problems, [])
        self.assertIn("none in the crate; the MPL-2.0 text printed below for option-ext 0.1.0 applies", text)
        self.assertEqual(text.count("Mozilla Public License Version 2.0"), 1)
        self.assertIn("https://crates.io/crates/selectors/1.2.3.", text)

    def test_mpl_without_any_supplied_text_fails(self):
        text, problems = self.one("MPL-2.0", name="selectors")
        self.assertIsNone(text)
        self.assertIn("selectors 1.2.3", problems[0])
        text, problems = self.one("MPL-2.0", name="selectors", extra=[("o", "MIT", {"LICENSE": "Mozilla Public License Version 2.0"})])
        self.assertIsNone(text)  # the supplier does not itself declare MPL-2.0

    def test_the_five_real_shapes(self):
        f = Fixture()
        f.link(ROOT, f.add("option-ext", "0.2.0", "MPL-2.0", {"LICENSE.txt": "Mozilla Public License Version 2.0"}))
        for n, lic in (("alloc-stdlib", "BSD-3-Clause"), ("dlopen2", "MIT"), ("dlopen2_derive", "MIT"),
                       ("libappindicator-sys", "Apache-2.0 OR MIT"), ("selectors", "MPL-2.0")):
            f.link(ROOT, f.add(n, "1.0.0", lic, {}, authors=["Some One"]))
        text, problems = f.build()
        self.assertEqual(problems, [])
        self.assertNotIn("Files: none in the crate; the declared licence applies", text)
        # dlopen2 and dlopen2_derive share one printed text.
        self.assertEqual(text.count("Copyright (c) Some One\n\nPermission is hereby granted"), 1)
        self.assertEqual(text.count("Copyright (c) Some One\n\nRedistribution and use"), 1)
        self.assertIn("Used by: dlopen2 1.0.0, dlopen2_derive 1.0.0, libappindicator-sys 1.0.0", text)
        self.assertIn("https://crates.io/crates/selectors/1.0.0.", text)

    def test_file_present_means_no_template(self):
        text, problems = self.one("MIT", extra=[("d", "MIT", {"LICENSE": "real text"})], name="e")
        # e has no file and gets the template; d keeps its own text.
        self.assertIn("real text", text)
        f = Fixture()
        f.link(ROOT, f.add("d", "1.0.0", "MIT", {"LICENSE-APACHE": "apache only file"}))
        text, problems = f.build()
        self.assertEqual(problems, [])
        self.assertNotIn("Permission is hereby granted", text)


class Discovery(unittest.TestCase):
    def test_copyright_prefix(self):
        f = Fixture()
        f.link(ROOT, f.add("c", "1.0.0", "MIT", {"COPYRIGHT": "copyright body", "COPYRIGHT.md": "md body"}))
        text, problems = f.build()
        self.assertEqual(problems, [])
        self.assertIn("copyright body", text)
        self.assertIn("md body", text)

    def test_licences_subdirectory(self):
        f = Fixture()
        pid = f.add("c", "1.0.0", "MIT OR Apache-2.0", {})
        d = f.dir / "c-1.0.0"
        for sub in ("LICENSES", "license"):
            (d / sub).mkdir()
        (d / "LICENSES" / "MIT.txt").write_text("mit in dir")
        (d / "license" / "Apache-2.0").write_text("apache in dir")
        (d / "LICENSES" / "deep").mkdir()
        (d / "LICENSES" / "deep" / "x.txt").write_text("too deep")
        (d / "src").mkdir()
        (d / "src" / "LICENSE").write_text("not top level")
        f.link(ROOT, pid)
        text, problems = f.build()
        self.assertEqual(problems, [])
        self.assertIn("mit in dir", text)
        self.assertIn("apache in dir", text)
        self.assertIn("LICENSES/MIT.txt", text)
        self.assertNotIn("too deep", text)
        self.assertNotIn("not top level", text)
        self.assertNotIn("Permission is hereby granted", text)

    def test_license_file_field(self):
        f = Fixture()
        pid = f.add("c", "1.0.0", None, {"docs-terms.txt": "terms body"}, license_file="docs-terms.txt")
        d = f.dir / "c-1.0.0"
        (d / "legal").mkdir()
        (d / "legal" / "T.txt").write_text("nested terms")
        f.packages[-1]["license_file"] = "legal/T.txt"
        f.link(ROOT, pid)
        text, problems = f.build()
        self.assertEqual(problems, [])
        self.assertIn("nested terms", text)
        self.assertIn("legal/T.txt", text)

    def test_license_file_field_outside_crate_ignored(self):
        f = Fixture()
        (f.dir / "outside.txt").write_text("secret outside")
        f.link(ROOT, f.add("c", "1.0.0", "MIT", {"LICENSE": "ok"}, license_file="../outside.txt"))
        text, problems = f.build()
        self.assertEqual(problems, [])
        self.assertNotIn("secret outside", text)

    def test_cap_and_dedup_in_subdirectory(self):
        f = Fixture()
        pid = f.add("c", "1.0.0", "MIT", {"LICENSE": "same"})
        d = f.dir / "c-1.0.0"
        (d / "LICENSES").mkdir()
        (d / "LICENSES" / "big").write_bytes(b"y" * (third_party.MAX_FILE_BYTES + 5))
        (d / "LICENSES" / "dup").write_text("same")
        f.link(ROOT, pid)
        text, problems = f.build()
        self.assertEqual(problems, [])
        self.assertIn("[truncated at 200 KiB]", text)
        self.assertEqual(text.count("\nsame\n"), 1)


class Slash(unittest.TestCase):
    def ok(self, expr):
        return third_party.acceptable(third_party.parse(expr))

    def test_operator_position(self):
        self.assertEqual(third_party.parse("MIT/Apache-2.0"), ("or", [("id", "MIT", None), ("id", "Apache-2.0", None)]))
        self.assertEqual(third_party.parse("MIT / Apache-2.0"), third_party.parse("MIT OR Apache-2.0"))
        self.assertEqual(third_party.parse("(MIT)/Zlib"), third_party.parse("MIT OR Zlib"))
        self.assertTrue(self.ok("GPL-3.0-only/MIT"))

    def test_not_after_with(self):
        tree = third_party.parse("Apache-2.0 WITH LLVM-exception/MIT")
        self.assertEqual(tree, ("id", "Apache-2.0", "LLVM-exception/MIT"))
        self.assertFalse(third_party.acceptable(tree))

    def test_dangling_slash(self):
        for bad in ("MIT/", "/MIT", "MIT//Zlib", "MIT / "):
            with self.assertRaises(ValueError, msg=bad):
                third_party.parse(bad)


class Graph(unittest.TestCase):
    def test_root_package_is_not_checked_or_listed(self):
        f = Fixture()
        f.packages[0]["license"] = "GPL-3.0-only"
        f.link(ROOT, f.add("a", "1.0.0", "MIT", {"LICENSE": "A"}))
        text, problems = f.build()
        self.assertEqual(problems, [])
        self.assertNotIn("GPL-3.0-only", text)

    def test_dev_edge_skipped_but_normal_path_kept(self):
        f = Fixture()
        a = f.add("a", "1.0.0", "MIT", {"LICENSE": "A"})
        shared = f.add("shared", "1.0.0", "MIT", {"LICENSE": "S"})
        f.link(ROOT, a)
        f.link(ROOT, shared, "dev")
        f.link(a, shared)
        text, problems = f.build()
        self.assertEqual(problems, [])
        self.assertIn("\nshared 1.0.0\n", text)

    def test_mixed_dep_kinds_count_as_normal(self):
        f = Fixture()
        a = f.add("a", "1.0.0", "MIT", {"LICENSE": "A"})
        f.link(ROOT, a)
        meta = f.meta()
        node = next(n for n in meta["resolve"]["nodes"] if n["id"] == ROOT)
        node["deps"][0]["dep_kinds"] = [{"kind": "dev", "target": None}, {"kind": None, "target": None}]
        text, problems = third_party.build(meta)
        self.assertEqual(problems, [])
        self.assertIn("\na 1.0.0\n", text)

    def test_cycle_terminates(self):
        f = Fixture()
        a = f.add("a", "1.0.0", "MIT", {"LICENSE": "A"})
        b = f.add("b", "1.0.0", "MIT", {"LICENSE": "B"})
        f.link(ROOT, a)
        f.link(a, b)
        f.link(b, a)
        text, problems = f.build()
        self.assertEqual(problems, [])
        self.assertEqual(text.count("\na 1.0.0\n"), 1)

    def test_older_cargo_dependencies_list(self):
        f = Fixture()
        a = f.add("a", "1.0.0", "MIT", {"LICENSE": "A"})
        b = f.add("b", "1.0.0", "MIT", {"LICENSE": "B"})
        meta = f.meta()
        meta["resolve"]["nodes"] = [{"id": ROOT, "dependencies": [a]}, {"id": a, "dependencies": [b]},
                                    {"id": b, "dependencies": []}]
        text, problems = third_party.build(meta)
        self.assertEqual(problems, [])
        self.assertIn("\nb 1.0.0\n", text)

    def test_resolve_names_unlisted_package(self):
        f = Fixture()
        meta = f.meta()
        meta["resolve"]["nodes"][0]["deps"] = [{"name": "x", "pkg": "ghost 1.0.0", "dep_kinds": []}]
        with self.assertRaises(third_party.Failure):
            third_party.build(meta)


class Failures(unittest.TestCase):
    def test_no_licence_and_no_file(self):
        f = Fixture()
        f.link(ROOT, f.add("nothing", "1.0.0", None, {}))
        text, problems = f.build()
        self.assertIsNone(text)
        self.assertTrue(any("nothing 1.0.0" in p and "no licence declared" in p for p in problems))

    def test_missing_directory(self):
        f = Fixture()
        f.link(ROOT, f.add("gone", "1.0.0", "MIT", make_dir=False))
        text, problems = f.build()
        self.assertIsNone(text)
        self.assertTrue(any("gone 1.0.0" in p and "missing" in p for p in problems))

    def test_disallowed_licence(self):
        f = Fixture()
        f.link(ROOT, f.add("ssl", "1.0.0", "OpenSSL", {"LICENSE": "x"}))
        f.link(ROOT, f.add("gpl", "1.0.0", "GPL-3.0-only", {"LICENSE": "x"}))
        text, problems = f.build()
        self.assertIsNone(text)
        self.assertEqual(len(problems), 2)

    def test_unreadable_expression(self):
        f = Fixture()
        f.link(ROOT, f.add("bad", "1.0.0", "MIT OR", {"LICENSE": "x"}))
        _, problems = f.build()
        self.assertTrue(any("bad 1.0.0" in p for p in problems))

    def test_no_resolve_or_root(self):
        with self.assertRaises(third_party.Failure):
            third_party.build({"packages": []})
        f = Fixture()
        m = f.meta()
        with self.assertRaises(third_party.Failure):
            third_party.build(m, "other-root")


class Cli(unittest.TestCase):
    def test_writes_file_and_is_repeatable(self):
        f = Fixture()
        f.link(ROOT, f.add("a", "1.0.0", "MIT", {"LICENSE": "A"}))
        out = f.dir / "sub" / "THIRD-PARTY-WINDOW.txt"
        code, _, err = f.run("--out", str(out))
        self.assertEqual((code, err), (0, ""))
        first = out.read_bytes()
        self.assertEqual(f.run("--out", str(out))[0], 0)
        self.assertEqual(out.read_bytes(), first)

    def test_check_only_writes_nothing(self):
        f = Fixture()
        f.link(ROOT, f.add("a", "1.0.0", "MIT", {"LICENSE": "A"}))
        out = f.dir / "x.txt"
        code, stdout, _ = f.run("--check-only", "--out", str(out))
        self.assertEqual(code, 0)
        self.assertIn("ok", stdout)
        self.assertFalse(out.exists())

    def test_failure_exits_nonzero_and_writes_nothing(self):
        f = Fixture()
        f.link(ROOT, f.add("nothing", "1.0.0", None, {}))
        out = f.dir / "x.txt"
        code, _, err = f.run("--out", str(out))
        self.assertEqual(code, 1)
        self.assertIn("nothing 1.0.0", err)
        self.assertFalse(out.exists())

    def test_bad_json_file(self):
        f = Fixture()
        p = f.dir / "bad.json"
        p.write_text("{")
        err = io.StringIO()
        with contextlib.redirect_stderr(err):
            self.assertEqual(third_party.main(["--metadata", str(p), "--check-only"]), 1)
        self.assertIn("third_party:", err.getvalue())


if __name__ == "__main__":
    unittest.main()
