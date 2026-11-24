#!/usr/bin/env python3
"""Write THIRD-PARTY-WINDOW.txt: the Rust crates compiled into re-psn-window, with their licence texts.

Input is the JSON of `cargo metadata --locked --format-version 1`, run in app/shell after `cargo fetch --locked` so
that every crate's sources are on disk (from --metadata FILE, or stdin). The crates are the dependencies of the
re-psn-shell package found through the "resolve" graph: normal and build dependencies for every platform, which is a
superset of what one binary contains (development dependencies are left out; the root package is not listed).

Each crate is listed with its name, version, the SPDX licence expression from its metadata, its repository URL and
the full text of its licence files: files in its directory whose name starts with LICENSE, LICENCE, COPYING,
COPYRIGHT, NOTICE or UNLICENSE (any case), files directly inside a subdirectory named LICENSES, LICENSE (any case,
also the LICENCE spelling), and the file named by the crate's `license_file` field when it lies inside the crate
directory; at most 200 KiB each. Identical texts are printed once, with the crates that share them.

A crate that declares a licence but ships no licence file gets the standard text of the licence it declares (MIT and
BSD-3-Clause are built in), headed by a copyright line made from the authors in its Cargo.toml, with a note saying so.
For an expression with OR the first allowed alternative that has a text is used. A crate under MPL-2.0 alone without a
file is pointed to the MPL-2.0 text that another crate supplied. Any other declared licence without a file (for example
Apache-2.0 alone) fails the run, so that a person decides.

It exits non-zero, writing nothing, when:
  - a crate has neither a declared licence nor a licence file,
  - a crate's directory is missing (its sources were not fetched),
  - a licence expression cannot be satisfied with the ALLOWED_LICENCES below,
  - a crate declares a licence, has no licence file and no standard text applies (see above).

    python3 tools/third_party.py --metadata metadata.json --out dist/THIRD-PARTY-WINDOW.txt
    python3 tools/third_party.py --metadata metadata.json --check-only
"""
import argparse
import hashlib
import json
import pathlib
import re
import sys

# SPDX identifiers accepted in a crate's expression. Compared case-insensitively. In an OR expression one allowed
# alternative suffices; in an AND expression every part must be allowed.
ALLOWED_LICENCES = frozenset({
    "MIT", "Apache-2.0", "BSD-2-Clause", "BSD-3-Clause", "ISC", "MPL-2.0", "Unicode-3.0", "Unicode-DFS-2016",
    "Zlib", "CC0-1.0", "0BSD", "BSL-1.0", "Unlicense",
})
# `LICENCE WITH EXCEPTION` is accepted when LICENCE is allowed and EXCEPTION is listed here.
ALLOWED_EXCEPTIONS = frozenset({"LLVM-exception"})
# Licences whose terms ask that the source of the covered files stay available; the entry says where it is.
SOURCE_OFFER = frozenset({"MPL-2.0"})

ROOT_PACKAGE = "re-psn-shell"
FILE_PREFIXES = ("license", "licence", "copying", "copyright", "notice", "unlicense")
LICENCE_DIRS = ("licenses", "licences", "license", "licence")
MAX_FILE_BYTES = 200 * 1024
OUT_NAME = "THIRD-PARTY-WINDOW.txt"
RULE = "=" * 78

MIT_TEXT = """Permission is hereby granted, free of charge, to any person obtaining a copy of this software and associated documentation files (the "Software"), to deal in the Software without restriction, including without limitation the rights to use, copy, modify, merge, publish, distribute, sublicense, and/or sell copies of the Software, and to permit persons to whom the Software is furnished to do so, subject to the following conditions:

The above copyright notice and this permission notice shall be included in all copies or substantial portions of the Software.

THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY, FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM, OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE SOFTWARE."""

BSD3_TEXT = """Redistribution and use in source and binary forms, with or without modification, are permitted provided that the following conditions are met:

1. Redistributions of source code must retain the above copyright notice, this list of conditions and the following disclaimer.

2. Redistributions in binary form must reproduce the above copyright notice, this list of conditions and the following disclaimer in the documentation and/or other materials provided with the distribution.

3. Neither the name of the copyright holder nor the names of its contributors may be used to endorse or promote products derived from this software without specific prior written permission.

THIS SOFTWARE IS PROVIDED BY THE COPYRIGHT HOLDERS AND CONTRIBUTORS "AS IS" AND ANY EXPRESS OR IMPLIED WARRANTIES, INCLUDING, BUT NOT LIMITED TO, THE IMPLIED WARRANTIES OF MERCHANTABILITY AND FITNESS FOR A PARTICULAR PURPOSE ARE DISCLAIMED. IN NO EVENT SHALL THE COPYRIGHT HOLDER OR CONTRIBUTORS BE LIABLE FOR ANY DIRECT, INDIRECT, INCIDENTAL, SPECIAL, EXEMPLARY, OR CONSEQUENTIAL DAMAGES (INCLUDING, BUT NOT LIMITED TO, PROCUREMENT OF SUBSTITUTE GOODS OR SERVICES; LOSS OF USE, DATA, OR PROFITS; OR BUSINESS INTERRUPTION) HOWEVER CAUSED AND ON ANY THEORY OF LIABILITY, WHETHER IN CONTRACT, STRICT LIABILITY, OR TORT (INCLUDING NEGLIGENCE OR OTHERWISE) ARISING IN ANY WAY OUT OF THE USE OF THIS SOFTWARE, EVEN IF ADVISED OF THE POSSIBILITY OF SUCH DAMAGE."""

# Standard texts used for a crate that declares the licence but ships no file (lower-case id -> (id, text)).
TEMPLATES = {"mit": ("MIT", MIT_TEXT), "bsd-3-clause": ("BSD-3-Clause", BSD3_TEXT)}
TEMPLATE_NOTE = ("No licence file ships in this crate; this is the standard text of the licence it declares, with the "
                 "copyright holder taken from its Cargo.toml authors.")


class Failure(Exception):
    pass


# ---- SPDX expressions -------------------------------------------------------------------------------------------

_TOKEN = re.compile(r"\s*(\(|\)|[^\s()]+)")


def tokenize(expr):
    pos, out = 0, []
    expr = expr.strip()
    while pos < len(expr):
        m = _TOKEN.match(expr, pos)
        if not m:
            break
        out.append(m.group(1))
        pos = m.end()
    return out


def parse(expr):
    """Parse an SPDX expression into a tree: ("id", name, exception|None), ("and", [..]), ("or", [..]).

    Cargo also accepts the old form `MIT/Apache-2.0`, where `/` between two identifiers means OR; it is read that way.
    A `/` in the identifier right after WITH is left alone (the exception is then not on the allowed list). A trailing
    `+` on an identifier is dropped. Raises ValueError on a malformed expression."""
    toks = []
    for t in tokenize(expr):
        if "/" in t and not (toks and toks[-1].upper() == "WITH"):
            for i, part in enumerate(t.split("/")):
                if i:
                    toks.append("OR")
                if part:
                    toks.append(part)
        else:
            toks.append(t)
    if not toks:
        raise ValueError("empty licence expression")
    pos = 0

    def peek():
        return toks[pos] if pos < len(toks) else None

    def take():
        nonlocal pos
        t = peek()
        pos += 1
        return t

    def is_kw(t, kw):
        return t is not None and t.upper() == kw

    def atom():
        t = take()
        if t is None:
            raise ValueError("unexpected end of expression")
        if t == "(":
            node = or_expr()
            if take() != ")":
                raise ValueError("missing )")
            return node
        if t == ")" or t.upper() in ("AND", "OR", "WITH"):
            raise ValueError("unexpected %r" % t)
        name = t[:-1] if t.endswith("+") else t
        exc = None
        if is_kw(peek(), "WITH"):
            take()
            exc = take()
            if exc is None or exc in ("(", ")") or exc.upper() in ("AND", "OR", "WITH"):
                raise ValueError("WITH needs an exception identifier")
        return ("id", name, exc)

    def and_expr():
        parts = [atom()]
        while is_kw(peek(), "AND"):
            take()
            parts.append(atom())
        return parts[0] if len(parts) == 1 else ("and", parts)

    def or_expr():
        parts = [and_expr()]
        while is_kw(peek(), "OR"):
            take()
            parts.append(and_expr())
        return parts[0] if len(parts) == 1 else ("or", parts)

    tree = or_expr()
    if peek() is not None:
        raise ValueError("unexpected %r" % peek())
    return tree


def acceptable(tree, allowed=ALLOWED_LICENCES):
    kind = tree[0]
    if kind == "id":
        names = {a.lower() for a in allowed}
        if tree[1].lower() not in names:
            return False
        return tree[2] is None or tree[2].lower() in {e.lower() for e in ALLOWED_EXCEPTIONS}
    if kind == "and":
        return all(acceptable(t, allowed) for t in tree[1])
    return any(acceptable(t, allowed) for t in tree[1])


def identifiers(tree):
    if tree[0] == "id":
        return [tree[1]]
    return [i for t in tree[1] for i in identifiers(t)]


def needs_source_offer(tree):
    """True when the expression cannot be met without a licence of SOURCE_OFFER (not `MIT OR MPL-2.0`)."""
    mentioned = {i.lower() for i in identifiers(tree)} & {s.lower() for s in SOURCE_OFFER}
    if not mentioned:
        return False
    return not acceptable(tree, ALLOWED_LICENCES - SOURCE_OFFER)


# ---- cargo metadata ---------------------------------------------------------------------------------------------

def reachable_packages(meta, root_name=ROOT_PACKAGE):
    """Packages that root_name depends on (normal and build, all targets), without the root; sorted by name, version."""
    packages = {p["id"]: p for p in meta.get("packages", [])}
    resolve = meta.get("resolve")
    if not resolve or not resolve.get("nodes"):
        raise Failure("the metadata has no 'resolve' section (run cargo metadata without --no-deps)")
    nodes = {n["id"]: n for n in resolve["nodes"]}
    roots = [i for i, p in packages.items() if p.get("name") == root_name]
    if not roots:
        raise Failure("package %r is not in the metadata" % root_name)
    root = roots[0]
    seen, stack = {root}, [root]
    while stack:
        node = nodes.get(stack.pop())
        if node is None:
            continue
        if "deps" in node:
            kids = [d["pkg"] for d in node["deps"]
                    if not d.get("dep_kinds") or any(k.get("kind") != "dev" for k in d["dep_kinds"])]
        else:  # older cargo: no dependency kinds
            kids = node.get("dependencies", [])
        for k in kids:
            if k not in seen:
                seen.add(k)
                stack.append(k)
    seen.discard(root)
    missing = [i for i in seen if i not in packages]
    if missing:
        raise Failure("the resolve graph names packages that are not listed: %s" % ", ".join(sorted(missing)))
    return sorted((packages[i] for i in seen), key=lambda p: (p["name"], p["version"], p["id"]))


def licence_files(pkg):
    """[(file name, text)] of the licence-like files of the crate, sorted by name.

    Names are relative to the crate directory with `/` (so `LICENSES/MIT.txt`)."""
    manifest = pkg.get("manifest_path")
    crate = pathlib.Path(manifest).parent if manifest else None
    label = "%s %s" % (pkg["name"], pkg["version"])
    if crate is None or not crate.is_dir():
        raise Failure("%s: crate directory %s is missing (sources not fetched?)" % (label, crate))
    names = set()
    for f in crate.iterdir():
        if f.is_file() and f.name.lower().startswith(FILE_PREFIXES):
            names.add(f.name)
        elif f.is_dir() and f.name.lower() in LICENCE_DIRS:
            names.update("%s/%s" % (f.name, g.name) for g in f.iterdir() if g.is_file())
    declared = pkg.get("license_file")
    if declared:
        try:
            base = crate.resolve()
            target = (crate / declared).resolve()
            rel = target.relative_to(base)
        except (ValueError, OSError):
            rel = None
        if rel is not None and target.is_file():
            names.add(rel.as_posix())
    out = []
    for name in sorted(names):
        with open(crate / name, "rb") as fh:
            raw = fh.read(MAX_FILE_BYTES + 1)
        text = raw[:MAX_FILE_BYTES].decode("utf-8", errors="replace")
        text = text.replace("\r\n", "\n").replace("\r", "\n").strip("\n")
        if len(raw) > MAX_FILE_BYTES:
            text += "\n[truncated at 200 KiB]"
        out.append((name, text))
    return out


def copyright_line(pkg):
    # An author is "Name <email>" in Cargo.toml; the copyright line names the person, not the address.
    authors = [re.sub(r"\s*<[^>]*>", "", a).strip() for a in (pkg.get("authors") or []) if a and a.strip()]
    authors = [a for a in authors if a]
    return "Copyright (c) " + (", ".join(authors) if authors else "the authors of %s" % pkg["name"])


def standard_text(tree, have_mpl):
    """Ids whose standard text stands in for a missing file: a list of ("template", id) / ("mpl", id), or None.

    OR: the first allowed alternative that works. AND: every part must work."""
    kind = tree[0]
    if kind == "id":
        name, exc = tree[1], tree[2]
        if not acceptable(tree):
            return None
        if exc is None and name.lower() in TEMPLATES:
            return [("template", TEMPLATES[name.lower()][0])]
        if exc is None and name.lower() == "mpl-2.0" and have_mpl:
            return [("mpl", "MPL-2.0")]
        return None
    if kind == "and":
        out = []
        for t in tree[1]:
            r = standard_text(t, have_mpl)
            if r is None:
                return None
            out += r
        return out
    for t in tree[1]:
        r = standard_text(t, have_mpl)
        if r is not None:
            return r
    return None


def build(meta, root_name=ROOT_PACKAGE):
    """Return (text, problems). problems is a list of messages; when it is not empty the text must not be shipped."""
    problems = []
    entries = []
    for pkg in reachable_packages(meta, root_name):
        label = "%s %s" % (pkg["name"], pkg["version"])
        try:
            files = licence_files(pkg)
        except Failure as e:
            problems.append(str(e))
            continue
        expr = (pkg.get("license") or "").strip()
        tree = None
        if expr:
            try:
                tree = parse(expr)
            except ValueError as e:
                problems.append("%s: cannot read licence expression %r (%s)" % (label, expr, e))
                continue
            if not acceptable(tree):
                problems.append("%s: licence %r is not allowed (see ALLOWED_LICENCES in tools/third_party.py)" % (label, expr))
        elif not files:
            problems.append("%s: no licence declared and no licence file found" % label)
        entries.append({"pkg": pkg, "label": label, "expr": expr, "tree": tree, "files": files})
    if problems:
        return None, problems

    texts = {}  # text -> {"names": set, "crates": set, "generated": bool}
    mpl_suppliers = []
    for e in entries:
        for name, text in e["files"]:
            slot = texts.setdefault(text, {"names": set(), "crates": set(), "generated": False})
            slot["names"].add(name)
            slot["crates"].add(e["label"])
            if "mozilla public license" in text.lower() and e["tree"] is not None \
                    and "mpl-2.0" in [i.lower() for i in identifiers(e["tree"])]:
                mpl_suppliers.append(e["label"])
    mpl_suppliers = sorted(set(mpl_suppliers))
    for e in entries:
        e["standard"] = None
        if e["files"] or e["tree"] is None:
            continue
        found = standard_text(e["tree"], bool(mpl_suppliers))
        if found is None:
            problems.append("%s: declares licence %r but ships no licence file, and tools/third_party.py has no standard "
                            "text for it; a person must decide how to provide the licence text" % (e["label"], e["expr"]))
            continue
        e["standard"] = found
        for kind, ident in found:
            if kind != "template":
                continue
            text = copyright_line(e["pkg"]) + "\n\n" + TEMPLATES[ident.lower()][1]
            slot = texts.setdefault(text, {"names": set(), "crates": set(), "generated": True})
            slot["names"].add("standard %s text" % ident)
            slot["crates"].add(e["label"])
    if problems:
        return None, problems

    lines = [
        "THIRD-PARTY LICENCES OF re-psn-window",
        RULE,
        "",
        "re-psn-window is the native window program of the re-psn desktop app. It is written in Rust (app/shell in the",
        "source tree) and is built with the crates listed here. This file gives each crate's name, version, declared",
        "licence, repository and the full text of the licence and notice files that came with it.",
        "",
        "This list is a superset: it names every crate that the window program depends on for any platform (normal",
        "and build dependencies, Windows, Linux and the rest), so it can include crates that a single binary does not",
        "contain. Development dependencies are not listed.",
        "",
        "The Go side of the app and the other components are not covered here; they are in NOTICE.md.",
        "",
        "Crates: %d. Distinct licence texts: %d." % (len(entries), len(texts)),
        "",
        RULE,
        "CRATES",
        RULE,
    ]
    for e in entries:
        pkg = e["pkg"]
        lines += ["", "%s %s" % (pkg["name"], pkg["version"])]
        lines.append("  Licence: %s" % (e["expr"] or "LICENSE NOT DECLARED (the texts below are all that the crate states)"))
        lines.append("  Repository: %s" % (pkg.get("repository") or "not stated in the crate metadata"))
        if e["files"]:
            lines.append("  Files: %s" % ", ".join(n for n, _ in e["files"]))
        elif e["standard"]:
            parts = []
            for kind, ident in e["standard"]:
                if kind == "template":
                    parts.append("none in the crate; the standard %s text is printed below with the crate's Cargo.toml authors" % ident)
                else:
                    parts.append("none in the crate; the %s text printed below for %s applies" % (ident, ", ".join(mpl_suppliers)))
            lines.append("  Files: " + "; ".join(parts))
        else:
            lines.append("  Files: none in the crate; the declared licence applies")
        if e["tree"] is not None and needs_source_offer(e["tree"]):
            url = "https://crates.io/crates/%s/%s" % (pkg["name"], pkg["version"])
            lines.append("  Source: this crate is licensed under MPL-2.0. Its source code, the Source Code Form that the licence")
            lines.append("  refers to, is available at %s." % url)
            if pkg.get("repository"):
                lines.append("  The repository of the crate is %s." % pkg["repository"])

    def order(item):
        text, slot = item
        return (sorted(slot["crates"])[0], sorted(slot["names"])[0], hashlib.sha256(text.encode("utf-8")).hexdigest())

    lines += ["", RULE, "LICENCE TEXTS", RULE]
    for text, slot in sorted(texts.items(), key=order):
        head = ["", "File: %s" % ", ".join(sorted(slot["names"])), "Used by: %s" % ", ".join(sorted(slot["crates"]))]
        if slot["generated"]:
            head.append("Note: " + TEMPLATE_NOTE)
        lines += head + ["-" * 78, text, ""]
    return "\n".join(lines).rstrip("\n") + "\n", []


def main(argv=None):
    ap = argparse.ArgumentParser(description="Write %s from cargo metadata JSON." % OUT_NAME)
    ap.add_argument("--metadata", default="-", help="cargo metadata JSON file (default: stdin)")
    ap.add_argument("--out", help="file to write (default: %s in the current directory)" % OUT_NAME)
    ap.add_argument("--check-only", action="store_true", help="validate and write nothing")
    ap.add_argument("--root-package", default=ROOT_PACKAGE, help="package whose dependencies are listed (default: %(default)s)")
    args = ap.parse_args(argv)
    try:
        if args.metadata == "-":
            meta = json.load(sys.stdin)
        else:
            with open(args.metadata, "r", encoding="utf-8") as fh:
                meta = json.load(fh)
        text, problems = build(meta, args.root_package)
    except (OSError, ValueError, KeyError, Failure) as e:
        print("third_party: %s" % e, file=sys.stderr)
        return 1
    if problems:
        print("third_party: %d problem(s):" % len(problems), file=sys.stderr)
        for p in problems:
            print("  " + p, file=sys.stderr)
        return 1
    if args.check_only:
        print("third_party: ok")
        return 0
    out = pathlib.Path(args.out or OUT_NAME)
    out.parent.mkdir(parents=True, exist_ok=True)
    out.write_bytes(text.encode("utf-8"))
    print("third_party: wrote %s" % out)
    return 0


if __name__ == "__main__":
    sys.exit(main())
