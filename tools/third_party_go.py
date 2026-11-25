#!/usr/bin/env python3
"""Write THIRD-PARTY-GO.txt: the licence texts of what the Go programs of a release contain.

Go programs in the release archives (re-psn, re-psn-host and re-psn-app) are built from the Go standard library and,
for the app, from modules of app/go.mod. This script runs no Go. It reads files that the release workflow made with
the toolchain that builds, one per program and target:

    GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go list -mod=readonly -deps -json ./cmd/re-psn-app > app-windows-amd64.json

and prints, standard library first, one entry for every module that any of the lists links (the main module is left
out), with the full text of the licence files in the module's directory (the `Dir` that `go list` reports, in the
verified module cache): files whose name starts with LICENSE, LICENCE, COPYING or NOTICE, any case, at most 200 KiB
each. Modules that are in go.mod or go.sum but linked by none of the lists (for example a module that only another
operating system needs) are not listed.

The standard library text is the LICENSE file of the toolchain that builds (--goroot-licence "$(go env GOROOT)/LICENSE",
with --go-version "$(go version)"); it must be the BSD-3-Clause text of the Go authors.

It exits non-zero, writing nothing, when:
  - a list names a package that is neither standard library nor in a module, or a module that is replaced
    (a `replace` directive) or has no directory on disk,
  - a module's version in a list is not the version in --go-mod, or --go-sum lacks the module's two hash lines for
    that version (so the cache was not checked against go.sum),
  - a module has no LICENSE, LICENCE or COPYING file, or one of them is not MIT, BSD-3-Clause, BSD-2-Clause or
    Apache-2.0 (ALLOWED_LICENCES; recognised by their wording) or is larger than 200 KiB,
  - the Go standard library licence file is missing or is not the Go BSD-3-Clause text.
The standard texts of licences are never filled in for a module that ships none: a person decides.

    python3 tools/third_party_go.py --go-mod app/go.mod --go-sum app/go.sum --goroot-licence GOROOT/LICENSE \\
        --go-version "go version go1.27.4 linux/amd64" --list app-linux-amd64=app.json --out THIRD-PARTY-GO.txt
"""
import argparse
import json
import pathlib
import re
import sys

ALLOWED_LICENCES = ("MIT", "BSD-3-Clause", "BSD-2-Clause", "Apache-2.0")
FILE_PREFIXES = ("license", "licence", "copying", "notice")
LICENCE_PREFIXES = ("license", "licence", "copying")  # NOTICE files are printed but need not be a licence
MAX_FILE_BYTES = 200 * 1024
OUT_NAME = "THIRD-PARTY-GO.txt"
RULE = "=" * 78


class Failure(Exception):
    pass


def classify(text):
    """The SPDX id of a licence text, recognised by its wording, or None."""
    t = " ".join(text.lower().split())
    if "apache license" in t and "version 2.0" in t:
        return "Apache-2.0"
    if "redistribution and use in source and binary forms" in t and "this software is provided" in t:
        return "BSD-3-Clause" if "neither the name" in t else "BSD-2-Clause"
    if "permission is hereby granted, free of charge" in t and "the software is provided \"as is\"" in t:
        return "MIT"
    return None


def read_text(path):
    raw = path.read_bytes()
    if len(raw) > MAX_FILE_BYTES:
        raise Failure("%s is larger than 200 KiB" % path)
    return raw.decode("utf-8", errors="replace").replace("\r\n", "\n").replace("\r", "\n").strip("\n")


def parse_stream(text):
    """The JSON objects that `go list -json` prints one after another."""
    dec, pos, out = json.JSONDecoder(), 0, []
    while True:
        while pos < len(text) and text[pos].isspace():
            pos += 1
        if pos >= len(text):
            return out
        obj, pos = dec.raw_decode(text, pos)
        out.append(obj)


def go_mod_requires(text):
    """{module path: version} from the require lines of a go.mod."""
    req, block = {}, False
    for line in text.splitlines():
        line = line.split("//")[0].strip()
        if not line:
            continue
        if block:
            if line == ")":
                block = False
                continue
            parts = line.split()
        elif line == "require (":
            block = True
            continue
        elif line.startswith("require "):
            parts = line[len("require "):].split()
        else:
            continue
        if len(parts) >= 2:
            req[parts[0]] = parts[1]
    return req


def go_sum_lines(text):
    return {" ".join(l.split()[:2]) for l in text.splitlines() if len(l.split()) == 3 and l.split()[2].startswith("h1:")}


def linked_modules(lists, requires, sums):
    """{(path, version): {"dir": str, "labels": set}} for the modules the lists link, checked against go.mod and go.sum."""
    mods = {}
    for label, objs in lists:
        for p in objs:
            m = p.get("Module")
            if m is None:
                if p.get("Standard"):
                    continue
                raise Failure("%s: package %s is neither standard library nor in a module" % (label, p.get("ImportPath")))
            if m.get("Main"):
                continue
            path, ver = m.get("Path"), m.get("Version")
            if m.get("Replace"):
                raise Failure("%s: module %s is replaced; a person must decide how its licence is provided" % (label, path))
            if not path or not ver or not m.get("Dir"):
                raise Failure("%s: module %s has no version or no directory on disk (not downloaded?)" % (label, path))
            if requires.get(path) != ver:
                raise Failure("%s: %s %s is not the version in go.mod (%s)" % (label, path, ver, requires.get(path)))
            for key in ("%s %s" % (path, ver), "%s %s/go.mod" % (path, ver)):
                if key not in sums:
                    raise Failure("%s: go.sum has no hash line for %s" % (label, key))
            slot = mods.setdefault((path, ver), {"dir": m["Dir"], "labels": set()})
            if slot["dir"] != m["Dir"]:
                raise Failure("%s: %s %s has two directories" % (label, path, ver))
            slot["labels"].add(label)
    return mods


# A source file such as license.go or notice.go is not a licence file.
TEXT_SUFFIXES = ("", ".txt", ".md", ".rst")


def module_files(path, ver, directory):
    d = pathlib.Path(directory)
    if not d.is_dir():
        raise Failure("%s %s: directory %s is missing" % (path, ver, d))
    files = sorted((f for f in d.iterdir() if f.is_file() and f.name.lower().startswith(FILE_PREFIXES)
                    and f.suffix.lower() in TEXT_SUFFIXES),
                   key=lambda f: f.name)
    out, found = [], False
    for f in files:
        text = read_text(f)
        if f.name.lower().startswith(LICENCE_PREFIXES):
            found = True
            kind = classify(text)
            if kind is None or kind not in ALLOWED_LICENCES:
                raise Failure("%s %s: %s is not one of %s (unknown wording); a person must decide"
                              % (path, ver, f.name, ", ".join(ALLOWED_LICENCES)))
            out.append((f.name, kind, text))
        else:
            out.append((f.name, None, text))
    if not found:
        raise Failure("%s %s: no LICENSE, LICENCE or COPYING file in %s" % (path, ver, d))
    return out


def build(lists, requires, sums, goroot_licence, go_version):
    """Return the text of THIRD-PARTY-GO.txt, or raise Failure."""
    std = read_text(pathlib.Path(goroot_licence)) if pathlib.Path(goroot_licence).is_file() else None
    if std is None:
        raise Failure("the Go standard library licence file %s is missing" % goroot_licence)
    if classify(std) != "BSD-3-Clause" or "go authors" not in std.lower():
        raise Failure("%s is not the Go BSD-3-Clause licence text" % goroot_licence)
    mods = linked_modules(lists, requires, sums)
    parts = [
        "THIRD-PARTY LICENCES OF THE GO PROGRAMS",
        RULE,
        "",
        "The Go programs of re-psn (the server, re-psn-host and re-psn-app) are built from the Go standard library",
        "and, for re-psn-app, from the modules listed here. Each entry gives the module, its version and the full text",
        "of the licence and notice files that came with it. The list is the union of what the build of every shipped",
        "operating system and architecture links, so one binary can hold fewer modules; the labels after \"Linked in\"",
        "say which build links a module (app is re-psn-app; server is re-psn and re-psn-host, which use the standard",
        "library only). The native window program re-psn-window is covered by THIRD-PARTY-WINDOW.txt, the other",
        "components by NOTICE.md.",
        "",
        "Modules: %d. Built with: %s." % (len(mods), go_version.strip() or "unknown Go version"),
        "",
        RULE,
        "GO STANDARD LIBRARY",
        RULE,
        "",
        "Licence: BSD-3-Clause (the LICENSE file of the Go toolchain that built these programs)",
        "-" * 78,
        std,
        "",
    ]
    if mods:
        parts += [RULE, "MODULES", RULE]
    for (path, ver), slot in sorted(mods.items()):
        files = module_files(path, ver, slot["dir"])
        kinds = sorted({k for _, k, _ in files if k})
        parts += ["", "%s %s" % (path, ver), "  Licence: %s" % ", ".join(kinds),
                  "  Source: https://pkg.go.dev/%s@%s" % (path, ver),
                  "  Linked in: %s" % ", ".join(sorted(slot["labels"])),
                  "  Files: %s" % ", ".join(n for n, _, _ in files)]
        for name, _, text in files:
            parts += ["", "File: %s" % name, "-" * 78, text]
    return "\n".join(parts).rstrip("\n") + "\n"


def main(argv=None):
    ap = argparse.ArgumentParser(description="Write %s from `go list -deps -json` output files." % OUT_NAME)
    ap.add_argument("--list", action="append", default=[], metavar="LABEL=FILE", required=True,
                    help="go list -deps -json output of one program and target, e.g. app-windows-amd64=FILE (repeat)")
    ap.add_argument("--go-mod", required=True, help="go.mod of the module that holds the dependencies")
    ap.add_argument("--go-sum", required=True, help="its go.sum")
    ap.add_argument("--goroot-licence", required=True, help="the LICENSE file in the GOROOT of the building toolchain")
    ap.add_argument("--go-version", default="", help="output of `go version`")
    ap.add_argument("--out", default=OUT_NAME, help="file to write (default: %(default)s)")
    ap.add_argument("--check-only", action="store_true", help="validate and write nothing")
    args = ap.parse_args(argv)
    try:
        lists = []
        for item in args.list:
            label, sep, name = item.partition("=")
            if not sep or not label or not name:
                raise Failure("--list needs LABEL=FILE, got %r" % item)
            objs = parse_stream(pathlib.Path(name).read_text(encoding="utf-8"))
            if not objs:
                raise Failure("%s holds no packages" % name)
            lists.append((label, objs))
        if len({l for l, _ in lists}) != len(lists):
            raise Failure("two --list entries have the same label")
        requires = go_mod_requires(pathlib.Path(args.go_mod).read_text(encoding="utf-8"))
        sums = go_sum_lines(pathlib.Path(args.go_sum).read_text(encoding="utf-8"))
        text = build(lists, requires, sums, args.goroot_licence, args.go_version)
    except (OSError, ValueError, Failure) as e:
        print("third_party_go: %s" % e, file=sys.stderr)
        return 1
    if args.check_only:
        print("third_party_go: ok")
        return 0
    out = pathlib.Path(args.out)
    out.parent.mkdir(parents=True, exist_ok=True)
    out.write_bytes(text.encode("utf-8"))
    print("third_party_go: wrote %s" % out)
    return 0


if __name__ == "__main__":
    sys.exit(main())
