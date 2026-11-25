"""Tests for tools/third_party_go.py, with synthetic module trees (no Go needed).

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
import third_party_go as tpg  # noqa: E402

MIT = ('MIT License\n\nCopyright (c) 2026 Someone\n\nPermission is hereby granted, free of charge, to any person '
       'obtaining a copy of this software.\n\nTHE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND.\n')
BSD3 = ('Copyright 2009 The Go Authors.\n\nRedistribution and use in source and binary forms, with or without '
        'modification, are permitted provided that the following conditions are met:\n * Neither the name of Google '
        'LLC nor the names of its contributors may be used.\n\nTHIS SOFTWARE IS PROVIDED BY THE COPYRIGHT HOLDERS "AS IS".\n')
BSD2 = ('Copyright (c) 2026 Someone\n\nRedistribution and use in source and binary forms, with or without '
        'modification, are permitted.\n\nTHIS SOFTWARE IS PROVIDED BY THE AUTHORS "AS IS".\n')
APACHE = "                                 Apache License\n                           Version 2.0, January 2004\n"
GPL = "GNU GENERAL PUBLIC LICENSE\nVersion 3, 29 June 2007\n"


class Tree:
    def __init__(self):
        self.tmp = tempfile.TemporaryDirectory()
        self.dir = pathlib.Path(self.tmp.name)
        self.requires, self.sums, self.pkgs = {}, set(), []
        (self.dir / "GOROOT").mkdir()
        (self.dir / "GOROOT" / "LICENSE").write_text(BSD3)

    def module(self, path, ver, files, require=True, sums=True, link=True, **extra):
        d = self.dir / "mod" / (path.replace("/", "_") + "@" + ver)
        d.mkdir(parents=True)
        for name, body in files.items():
            (d / name).write_bytes(body if isinstance(body, bytes) else body.encode())
        if require:
            self.requires[path] = ver
        if sums:
            self.sums |= {"%s %s" % (path, ver), "%s %s/go.mod" % (path, ver)}
        if link:
            m = {"Path": path, "Version": ver, "Dir": str(d)}
            m.update(extra)
            self.pkgs.append({"ImportPath": path + "/p", "Module": m})
        return d

    def lists(self, *labels):
        std = [{"ImportPath": "fmt", "Standard": True},
               {"ImportPath": "main", "Module": {"Path": "example.invalid/app", "Main": True, "Dir": "."}}]
        return [(l, std + self.pkgs) for l in (labels or ("app-linux-amd64",))]

    def build(self, lists=None):
        return tpg.build(lists or self.lists(), self.requires, self.sums, str(self.dir / "GOROOT" / "LICENSE"), "go1.99")

    def run(self, *extra):
        (self.dir / "go.mod").write_text("module example.invalid/app\n\ngo 1.26.0\n\nrequire (\n%s)\n" % "".join(
            "\t%s %s\n" % kv for kv in sorted(self.requires.items())))
        (self.dir / "go.sum").write_text("".join("%s h1:abc=\n" % s for s in sorted(self.sums)))
        stream = "\n".join(json.dumps(p, indent=1) for p in self.lists()[0][1])
        (self.dir / "list.json").write_text(stream)
        out, err = io.StringIO(), io.StringIO()
        with contextlib.redirect_stdout(out), contextlib.redirect_stderr(err):
            code = tpg.main(["--list", "app-linux-amd64=" + str(self.dir / "list.json"), "--go-mod", str(self.dir / "go.mod"),
                             "--go-sum", str(self.dir / "go.sum"), "--goroot-licence", str(self.dir / "GOROOT" / "LICENSE"),
                             "--go-version", "go1.99", "--out", str(self.dir / "default-out.txt"), *extra])
        return code, out.getvalue(), err.getvalue()


class Test(unittest.TestCase):
    def setUp(self):
        self.t = Tree()
        self.addCleanup(self.t.tmp.cleanup)

    def fails(self, fragment):
        with self.assertRaises(tpg.Failure) as cm:
            self.t.build()
        self.assertIn(fragment, str(cm.exception))

    def test_classify(self):
        self.assertEqual(tpg.classify(MIT), "MIT")
        self.assertEqual(tpg.classify(BSD3), "BSD-3-Clause")
        self.assertEqual(tpg.classify(BSD2), "BSD-2-Clause")
        self.assertEqual(tpg.classify(APACHE), "Apache-2.0")
        self.assertIsNone(tpg.classify(GPL))
        self.assertIsNone(tpg.classify("all rights reserved"))

    def test_standard_library_first_then_sorted_modules(self):
        self.t.module("example.invalid/zeta", "v1.0.0", {"LICENSE": MIT})
        self.t.module("example.invalid/alpha", "v0.2.0", {"LICENSE.txt": BSD3, "NOTICE": "notice text"})
        text = self.t.build()
        self.assertLess(text.index("GO STANDARD LIBRARY"), text.index("example.invalid/alpha v0.2.0"))
        self.assertLess(text.index("example.invalid/alpha v0.2.0"), text.index("example.invalid/zeta v1.0.0"))
        self.assertIn("Modules: 2.", text)
        self.assertIn("go1.99", text)
        self.assertIn("File: NOTICE", text)
        self.assertIn("notice text", text)
        self.assertNotIn("example.invalid/app ", text)  # the main module is not listed
        self.assertTrue(text.endswith("\n") and not text.endswith("\n\n"))

    def test_deterministic_and_labels_merge(self):
        self.t.module("example.invalid/m", "v1.0.0", {"LICENSE": MIT})
        a = self.t.build(self.t.lists("b-windows", "a-linux"))
        b = self.t.build(self.t.lists("a-linux", "b-windows"))
        self.assertEqual(a, b)
        self.assertIn("Linked in: a-linux, b-windows", a)

    def test_unlinked_module_not_listed(self):
        self.t.module("example.invalid/only-on-mac", "v1.0.0", {"LICENSE": MIT}, link=False)
        self.assertIn("Modules: 0.", self.t.build())

    def test_no_licence_file_fails(self):
        self.t.module("example.invalid/bare", "v1.0.0", {"README.md": "x"})
        self.fails("no LICENSE, LICENCE or COPYING file")

    def test_notice_alone_is_not_a_licence(self):
        self.t.module("example.invalid/n", "v1.0.0", {"NOTICE": "x"})
        self.fails("no LICENSE")

    def test_unknown_licence_fails(self):
        self.t.module("example.invalid/gpl", "v1.0.0", {"LICENSE": GPL})
        self.fails("is not one of")

    def test_second_unknown_licence_file_fails(self):
        self.t.module("example.invalid/two", "v1.0.0", {"LICENSE": MIT, "COPYING": GPL})
        self.fails("COPYING")

    def test_allowed_set(self):
        for i, body in enumerate((MIT, BSD3, BSD2, APACHE)):
            self.t.module("example.invalid/ok%d" % i, "v1.0.0", {"LICENCE": body})
        self.assertIn("Modules: 4.", self.t.build())

    def test_oversized_file_fails(self):
        self.t.module("example.invalid/big", "v1.0.0", {"LICENSE": MIT + "x" * (tpg.MAX_FILE_BYTES + 1)})
        self.fails("200 KiB")

    def test_version_differs_from_go_mod(self):
        self.t.module("example.invalid/m", "v1.0.0", {"LICENSE": MIT})
        self.t.requires["example.invalid/m"] = "v1.1.0"
        self.fails("not the version in go.mod")

    def test_module_missing_from_go_mod(self):
        self.t.module("example.invalid/m", "v1.0.0", {"LICENSE": MIT}, require=False)
        self.fails("not the version in go.mod")

    def test_go_sum_lacks_hash(self):
        self.t.module("example.invalid/m", "v1.0.0", {"LICENSE": MIT}, sums=False)
        self.fails("go.sum has no hash line")
        self.t.sums.add("example.invalid/m v1.0.0")
        self.fails("example.invalid/m v1.0.0/go.mod")

    def test_replaced_module_fails(self):
        self.t.module("example.invalid/m", "v1.0.0", {"LICENSE": MIT}, Replace={"Path": "../m"})
        self.fails("is replaced")

    def test_module_without_dir_fails(self):
        self.t.module("example.invalid/m", "v1.0.0", {"LICENSE": MIT})
        self.t.pkgs[-1]["Module"]["Dir"] = ""
        self.fails("no version or no directory")

    def test_package_outside_modules_fails(self):
        self.t.pkgs.append({"ImportPath": "weird"})
        self.fails("neither standard library nor in a module")

    def test_go_licence_must_be_go_bsd(self):
        (self.t.dir / "GOROOT" / "LICENSE").write_text(MIT)
        self.fails("not the Go BSD-3-Clause")
        (self.t.dir / "GOROOT" / "LICENSE").unlink()
        self.fails("is missing")

    def test_parse_stream_and_go_mod(self):
        self.assertEqual(len(tpg.parse_stream('{"a": 1}\n{"b": {"c": 2}}\n')), 2)
        mod = "module m\n\ngo 1.26\n\nrequire a/b v1.0.0\n\nrequire (\n\tc/d v2.0.0\n\te/f v0.0.0-2026 // indirect\n)\n"
        self.assertEqual(tpg.go_mod_requires(mod), {"a/b": "v1.0.0", "c/d": "v2.0.0", "e/f": "v0.0.0-2026"})

    def test_main_writes_file_and_check_only(self):
        self.t.module("example.invalid/m", "v1.0.0", {"LICENSE": MIT})
        out = self.t.dir / "sub" / "THIRD-PARTY-GO.txt"
        code, _, err = self.t.run("--out", str(out))
        self.assertEqual((code, err), (0, ""))
        first = out.read_bytes()
        self.t.run("--out", str(out))
        self.assertEqual(out.read_bytes(), first)
        out.unlink()
        self.assertEqual(self.t.run("--check-only", "--out", str(out))[0], 0)
        self.assertFalse(out.exists())

    def test_licence_kind_and_text_in_output(self):
        self.t.module("example.invalid/mit", "v1.0.0", {"LICENSE": MIT})
        self.t.module("example.invalid/bsd", "v1.0.0", {"license.md": BSD3})
        text = self.t.build()
        self.assertIn("example.invalid/mit v1.0.0\n  Licence: MIT\n", text)
        self.assertIn("example.invalid/bsd v1.0.0\n  Licence: BSD-3-Clause\n", text)
        self.assertIn("Copyright (c) 2026 Someone", text)
        self.assertIn("File: license.md", text)

    def test_crlf_text_is_normalised(self):
        self.t.module("example.invalid/crlf", "v1.0.0", {"LICENSE": MIT.replace("\n", "\r\n")})
        text = self.t.build()
        self.assertNotIn("\r", text)
        self.assertIn("Permission is hereby granted", text)

    def test_union_of_targets_with_module_on_one_target_only(self):
        base = self.t.lists("app-linux-amd64")[0][1]
        self.t.module("example.invalid/everywhere", "v1.0.0", {"LICENSE": MIT})
        both = self.t.lists("app-linux-amd64")[0][1]
        self.t.module("example.invalid/windows-only", "v1.0.0", {"LICENSE": BSD2})
        win = self.t.lists("app-windows-amd64")[0][1]
        lists = [("app-linux-amd64", both), ("app-windows-amd64", win)]
        text = self.t.build(lists)
        self.assertIn("Modules: 2.", text)
        self.assertIn("example.invalid/everywhere v1.0.0\n  Licence: MIT\n  Source: https://pkg.go.dev/example.invalid/everywhere@v1.0.0\n"
                      "  Linked in: app-linux-amd64, app-windows-amd64\n", text)
        self.assertIn("example.invalid/windows-only v1.0.0\n  Licence: BSD-2-Clause\n  Source: "
                      "https://pkg.go.dev/example.invalid/windows-only@v1.0.0\n  Linked in: app-windows-amd64\n", text)
        self.assertNotIn("example.invalid/windows-only v1.0.0\n  Licence: BSD-2-Clause\n  Source: "
                         "https://pkg.go.dev/example.invalid/windows-only@v1.0.0\n  Linked in: app-linux-amd64", text)
        self.assertEqual(len(base), 2)  # only std and main before any module

    def test_module_in_go_mod_but_not_linked_by_any_target_is_skipped_even_without_licence(self):
        self.t.module("example.invalid/linked", "v1.0.0", {"LICENSE": MIT})
        self.t.module("example.invalid/unused", "v1.0.0", {"README.md": "x"}, link=False)
        text = self.t.build()
        self.assertIn("Modules: 1.", text)
        self.assertNotIn("example.invalid/unused", text)

    def test_unlinked_on_one_target_unknown_licence_on_other_still_fails(self):
        self.t.module("example.invalid/bad", "v1.0.0", {"LICENSE": GPL})
        lists = [("a", self.t.lists("a")[0][1]), ("b", self.t.lists("b")[0][1][:2])]
        with self.assertRaises(tpg.Failure):
            self.t.build(lists)

    def test_standard_only_target_has_no_modules_section(self):
        text = self.t.build([("server-linux-amd64", self.t.lists()[0][1][:2])])
        self.assertIn("Modules: 0.", text)
        self.assertNotIn("\nMODULES\n", text)
        self.assertIn("GO STANDARD LIBRARY", text)

    def test_same_module_two_directories_fails(self):
        self.t.module("example.invalid/m", "v1.0.0", {"LICENSE": MIT})
        other = self.t.dir / "elsewhere"
        other.mkdir()
        (other / "LICENSE").write_text(MIT)
        second = [{"ImportPath": "example.invalid/m/q",
                   "Module": {"Path": "example.invalid/m", "Version": "v1.0.0", "Dir": str(other)}}]
        lists = [("a", self.t.lists("a")[0][1]), ("b", self.t.lists("b")[0][1][:2] + second)]
        with self.assertRaises(tpg.Failure) as cm:
            self.t.build(lists)
        self.assertIn("two directories", str(cm.exception))

    def test_missing_module_directory_fails(self):
        d = self.t.module("example.invalid/m", "v1.0.0", {"LICENSE": MIT})
        (d / "LICENSE").unlink()
        d.rmdir()
        self.fails("is missing")

    def test_main_rejects_bad_list_arguments(self):
        self.t.module("example.invalid/m", "v1.0.0", {"LICENSE": MIT})
        self.t.run()  # writes the inputs
        base = ["--go-mod", str(self.t.dir / "go.mod"), "--go-sum", str(self.t.dir / "go.sum"),
                "--goroot-licence", str(self.t.dir / "GOROOT" / "LICENSE"), "--check-only"]
        lst = str(self.t.dir / "list.json")
        for args in (["--list", "nolabel"], ["--list", "x=" + lst, "--list", "x=" + lst],
                     ["--list", "x=" + str(self.t.dir / "absent.json")]):
            err = io.StringIO()
            with contextlib.redirect_stderr(err), contextlib.redirect_stdout(io.StringIO()):
                self.assertEqual(tpg.main(args + base), 1)
            self.assertIn("third_party_go:", err.getvalue())

    def test_main_empty_list_file_fails(self):
        self.t.module("example.invalid/m", "v1.0.0", {"LICENSE": MIT})
        self.t.run()
        (self.t.dir / "empty.json").write_text("  \n")
        err = io.StringIO()
        with contextlib.redirect_stderr(err), contextlib.redirect_stdout(io.StringIO()):
            code = tpg.main(["--list", "x=" + str(self.t.dir / "empty.json"), "--go-mod", str(self.t.dir / "go.mod"),
                             "--go-sum", str(self.t.dir / "go.sum"),
                             "--goroot-licence", str(self.t.dir / "GOROOT" / "LICENSE")])
        self.assertEqual(code, 1)
        self.assertIn("holds no packages", err.getvalue())

    def test_main_output_is_byte_identical_across_label_order(self):
        self.t.module("example.invalid/m", "v1.0.0", {"LICENSE": MIT})
        self.t.run()
        lst = str(self.t.dir / "list.json")
        outs = []
        for i, order in enumerate((("a", "b"), ("b", "a"))):
            out = self.t.dir / ("o%d.txt" % i)
            args = []
            for l in order:
                args += ["--list", "%s=%s" % (l, lst)]
            with contextlib.redirect_stdout(io.StringIO()):
                self.assertEqual(tpg.main(args + ["--go-mod", str(self.t.dir / "go.mod"), "--go-sum", str(self.t.dir / "go.sum"),
                                                  "--goroot-licence", str(self.t.dir / "GOROOT" / "LICENSE"),
                                                  "--go-version", "go1.99", "--out", str(out)]), 0)
            outs.append(out.read_bytes())
        self.assertEqual(outs[0], outs[1])

    def test_main_failure_writes_nothing(self):
        self.t.module("example.invalid/m", "v1.0.0", {"LICENSE": GPL})
        out = self.t.dir / "THIRD-PARTY-GO.txt"
        code, _, err = self.t.run("--out", str(out))
        self.assertEqual(code, 1)
        self.assertIn("third_party_go:", err)
        self.assertFalse(out.exists())


if __name__ == "__main__":
    unittest.main()
