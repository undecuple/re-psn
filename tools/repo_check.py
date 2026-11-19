#!/usr/bin/env python3
"""Offline checks for tracked repository layout, Markdown links and config paths.

Run: python3 tools/repo_check.py [--root DIR]

The layout policy is the target repository's tracked tools/repo_layout.json.
Inventory comes from git ls-files; every input read must resolve to a tracked file
inside the checkout, and untracked files cannot satisfy links or config references.
Markdown supports inline/image links (balanced destination parentheses, optional
single-line titles), full/collapsed/defined shortcut references with single-line
definitions, ATX/setext
headings, HTML id/name anchors and GitHub duplicate heading suffixes. Fenced and
inline code and HTML comments are excluded. Bare code spans are examples, not
links. HTML href, raw HTML headings, MDX and Markdown extensions are out of scope.
Fragments are checked for Markdown targets only; other file types check existence.

Config checking is deliberately lexical, not a YAML or shell interpreter. It
checks literal paths beginning with an allowed repository directory in workflow
scalars/run blocks, explicit ./ or ../ file paths with suffixes, working-directory, local uses,
Dockerfile file/context, and Dependabot directory/directories (plain or quoted
scalars, flow lists or block lists). Globs must match tracked paths. Expressions,
variables, URLs and generated artifact paths such as dist/ are excluded. Simple
quoted literal arguments retain spaces. A standalone `python -m venv LITERAL_PATH`
line declares an output subtree for run references in that job only; declarations
must not overlap tracked files. Undeclared paths under tools/ still require tracked
targets. Shell
cwd changes and computed paths cannot be resolved (literal workflow/job run
defaults and step working-directory overrides are supported); YAML aliases/merges
are not supported; unsupported
syntax in a recognized path field fails with a diagnostic. No commands in a
workflow are executed and no external links are fetched.
"""

import argparse
from collections import Counter
import fnmatch
from functools import lru_cache
import html
import json
from pathlib import Path
import posixpath
import re
import subprocess
import sys
import unicodedata
from urllib.parse import unquote, urlsplit

POLICY = "tools/repo_layout.json"
DOCS_INDEX = "docs/README.md"
LABEL = re.compile(r"(?<!\\)\[((?:\\.|[^\[\]\\]|\[[^\[\]]*\])*)\]")
DEFINITION = re.compile(r"^ {0,3}\[([^\]]+)\]:[ \t]*(.*)$", re.MULTILINE)
LINK_TAIL = re.compile(r'''\s*(?:(?:"[^"\n]*"|'[^'\n]*'|\([^\n]*\))\s*)?\)''')
EXTERNAL = re.compile(r"^(?:[a-zA-Z][a-zA-Z0-9+.-]*:|//)")


def blank(text):
    return re.sub(r"[^\n]", " ", text)


def prose(text, inline=False):
    """Mask code/comments without changing line numbers or match offsets."""
    text = re.sub(r"<!--[\s\S]*?-->", lambda m: blank(m[0]), text)
    lines = text.splitlines(keepends=True)
    fence = None
    for i, line in enumerate(lines):
        match = re.match(r"^ {0,3}(?:> ?)* {0,3}(`{3,}|~{3,})(.*)$", line)
        if fence:
            lines[i] = blank(line)
            if match and match[1][0] == fence[0] and len(match[1]) >= len(fence) and not match[2].strip():
                fence = None
        elif match:
            fence = match[1]
            lines[i] = blank(line)
    text = "".join(lines)
    if inline:
        text = re.sub(r"(?<!`)(`+)(?!`)([\s\S]*?)(?<!`)\1(?!`)", lambda m: blank(m[0]), text)
    return text


def destination(text, start=0):
    """Read a CommonMark-style destination, returning (URL, end offset)."""
    i = start
    while i < len(text) and text[i].isspace():
        i += 1
    if i < len(text) and text[i] == "<":
        end = text.find(">", i + 1)
        if end < 0 or "\n" in text[i:end]:
            raise ValueError("unsupported Markdown destination")
        return text[i + 1:end], end + 1
    begin, depth = i, 0
    while i < len(text):
        char = text[i]
        if char == "\\" and i + 1 < len(text):
            i += 2
            continue
        if char.isspace() or (char == ")" and depth == 0):
            break
        if char == "(":
            depth += 1
        elif char == ")":
            depth -= 1
        i += 1
    if depth:
        raise ValueError("unbalanced Markdown destination")
    return text[begin:i], i


def reference_id(label):
    return " ".join(label.split()).casefold()


def links(text, inherited=None):
    """Yield (line, destination, error); undefined explicit references fail."""
    text = prose(text, inline=True)
    definitions = dict(inherited or {})
    for match in DEFINITION.finditer(text):
        line = text.count("\n", 0, match.start()) + 1
        try:
            value, _ = destination(match[2])
            if not value:
                raise ValueError("unsupported multiline/empty Markdown reference definition")
            definitions.setdefault(reference_id(match[1]), value)
            yield line, value, None
        except ValueError as exc:
            yield line, None, str(exc)
    text = DEFINITION.sub(lambda m: blank(m[0]), text)
    consumed = 0
    for match in LABEL.finditer(text):
        if match.start() < consumed:
            continue
        line = text.count("\n", 0, match.start()) + 1
        if "[" in match[1]:
            for nested_line, value, error in links(match[1], definitions):
                yield line + nested_line - 1, value, error
        end = match.end()
        if end < len(text) and text[end] == "(":
            try:
                value, stop = destination(text, end + 1)
                # A title is optional; anything else is unsupported, not ignored.
                tail = LINK_TAIL.match(text[stop:])
                if not tail:
                    raise ValueError("unsupported Markdown link/title syntax")
                consumed = stop + tail.end()
                yield line, value, None
            except ValueError as exc:
                yield line, None, str(exc)
        elif end < len(text) and text[end] == "[":
            ref = re.match(r"\[([^\]\n]*)\]", text[end:])
            if ref:
                consumed = end + ref.end()
                key = reference_id(ref[1] or match[1])
                if key not in definitions:
                    yield line, None, f"undefined Markdown reference {key!r}"
                else:
                    yield line, definitions[key], None
        elif reference_id(match[1]) in definitions:
            yield line, definitions[reference_id(match[1])], None


def heading_link_text(text, definitions):
    """Render the text of supported links, sharing their destination parser."""
    masked = prose(text, inline=True)
    result, consumed = [], 0
    for match in LABEL.finditer(masked):
        if match.start() < consumed:
            continue
        end = match.end()
        label = text[match.start(1):match.end(1)]
        if end < len(text) and text[end] == "(":
            try:
                _, stop = destination(text, end + 1)
                tail = LINK_TAIL.match(text[stop:])
                if not tail:
                    continue  # links() reports malformed syntax separately.
                end = stop + tail.end()
            except ValueError:
                continue
        elif end < len(text) and text[end] == "[":
            ref = re.match(r"\[([^\]\n]*)\]", text[end:])
            if not ref or reference_id(ref[1] or label) not in definitions:
                continue
            end += ref.end()
        elif reference_id(label) not in definitions:
            continue
        result.append(text[consumed:match.start()])
        result.append(heading_link_text(label, definitions))
        consumed = end
    result.append(text[consumed:])
    return "".join(result)


def heading_slug(text, definitions):
    text = heading_link_text(text, definitions)
    text = re.sub(r"<[^>]*>", "", text)
    # Emphasis delimiters disappear in rendered headings; code contents do not.
    parts = re.split(r"(`+[^`]*`+)", text)
    for i in range(0, len(parts), 2):
        parts[i] = re.sub(r"(?<![\w\\])(_{1,2})(\S(?:.*?\S)?)\1(?!\w)", r"\2", parts[i])
    text = html.unescape("".join(parts)).lower().replace("`", "")
    text = re.sub(r"\\([!\"#$%&'()*+,\-./:;<=>?@\[\]^_`{|}~])", r"\1", text)
    return "".join(c for c in text if c in " -_" or unicodedata.category(c)[0] in "LNM").replace(" ", "-")


def anchors(text):
    text = prose(text)
    result = set()
    definitions = {reference_id(match[1]) for match in DEFINITION.finditer(prose(text, inline=True))}
    for tag in re.finditer(r"<[a-zA-Z][^>]*>", prose(text, inline=True)):
        for match in re.finditer(r'''\b(?:id|name)\s*=\s*(?:"([^"]*)"|'([^']*)'|([^\s>]+))''', tag[0], re.I):
            result.add(html.unescape(next(v for v in match.groups() if v is not None)))
    seen = Counter()
    lines = text.splitlines()
    for i, line in enumerate(lines):
        match = re.match(r"^ {0,3}#{1,6}(?:\s+(.*?)\s*|\s*)$", line)
        heading = None
        if match:
            heading = re.sub(r"\s+#+\s*$", "", match[1] or "")
        elif i + 1 < len(lines) and line.strip() and re.fullmatch(r" {0,3}(?:=+|-+)\s*", lines[i + 1]):
            heading = line.strip()
        if heading is not None:
            base = heading_slug(heading, definitions)
            slug = base
            while slug in seen:
                seen[base] += 1
                slug = f"{base}-{seen[base]}"
            seen[slug] += 0
            result.add(slug)
    return result


def scalar(value):
    value = value.strip()
    if value.startswith(('"', "'")):
        quote = value[0]
        end = value.find(quote, 1)
        if end < 0 or value[end + 1:].strip():
            raise ValueError("unsupported quoted path scalar")
        return value[1:end]
    if not value or value[0] in "&*!{|>[":
        raise ValueError("unsupported path syntax (use literal scalars/lists)")
    return value


def config_literals(line):
    """Keep simple quoted path arguments whole, without shell execution."""
    line = re.sub(r"(\"[^\"\n]*\"|'[^'\n]*')|(?:^|\s+)#.*$", lambda m: m[1] or "", line)
    line = re.sub(r"\$\{\{.*?\}\}", "DYNAMIC", line)
    token = re.compile(r"\"(?:\\.|[^\"\\])*\"|'[^']*'|[^\s\"'`<>,;|(){}\[\]\\]+")
    for match in token.finditer(line):
        value = match[0]
        if value.startswith(("\"", "'")):
            value = value[1:-1]
        yield value.rstrip(":")


def config_paths(text, directories, dependabot=False, files=()):
    """Yield (line, path, base, error) for a small static YAML/shell subset.

    Mapping indentation identifies workflow/job defaults and step overrides;
    block scalar bodies are opaque strings, never parsed as YAML mappings.
    """
    prefixes = "|".join(re.escape(name) for name in sorted(directories))
    lines = text.splitlines()
    stack, fields = [], []
    step = 0
    i = 0
    while i < len(lines):
        lineno, raw = i + 1, lines[i]
        i += 1
        # A comment starts at # outside quoted scalars; shell quoting is lexical.
        clean = re.sub(r"(\"[^\"\n]*\"|'[^'\n]*')|(?:^|\s+)#.*$", lambda m: m[1] or "", raw)
        if re.match(r"\s*(?:-\s+)?<<:", clean):
            yield lineno, None, "", "unsupported YAML merge; use literal path configuration"
            continue
        field = re.match(r"(\s*)(?:-\s+)?([\w-]+):\s*(.*?)\s*$", clean)
        if not field:
            continue
        column = field.start(2)
        while stack and stack[-1][0] >= column:
            stack.pop()
        if raw.lstrip().startswith("- ") and any(key == "steps" for _, key in stack):
            step += 1
        path = tuple(key for _, key in stack) + (field[2],)
        stack.append((column, field[2]))
        value = field[3]
        if value.startswith(("&", "*")):
            yield lineno, None, "", "unsupported path syntax (YAML anchors/aliases)"
            continue
        if re.fullmatch(r"[|>][+-]?", value):
            body = []
            while i < len(lines) and (not lines[i].strip() or len(lines[i]) - len(lines[i].lstrip()) > column):
                body.append(lines[i])
                i += 1
            value = "\n".join(body)
            lineno += 1
        elif dependabot and field[2] == "directories" and not value:
            body = []
            while i < len(lines) and (not lines[i].strip() or len(lines[i]) - len(lines[i].lstrip()) > column):
                if lines[i].strip():
                    body.append((i + 1, lines[i].strip()))
                i += 1
            for line, item in body:
                try:
                    if not item.startswith("- "):
                        raise ValueError("unsupported Dependabot directory list")
                    yield line, scalar(item[2:]), "", None
                except ValueError as exc:
                    yield line, None, "", str(exc)
            continue
        if field[2] == "run" and "\n" not in value and value.startswith(("\"", "'")):
            try:
                value = scalar(value)
            except ValueError as exc:
                yield lineno, None, "", str(exc)
                continue
        fields.append((lineno, path, step, value))

    defaults, overrides = {}, {}
    for lineno, path, step, value in fields:
        if path[-1] == "working-directory":
            try:
                value = scalar(value)
                job = path[1] if len(path) > 1 and path[0] == "jobs" else None
                if "steps" in path:
                    overrides[step] = value
                elif path[-3:] == ("defaults", "run", "working-directory"):
                    defaults[job] = value
                else:
                    raise ValueError("unsupported working-directory location")
                if not any(c in value for c in "$`"):
                    yield lineno, value, "", None
            except ValueError as exc:
                yield lineno, None, "", str(exc)

    def run_base(path, step):
        job = path[1] if len(path) > 1 and path[0] == "jobs" else None
        return overrides.get(step, defaults.get(job, defaults.get(None, "")))

    generated = {}
    for lineno, path, step, value in fields:
        if path[-1] != "run" or "defaults" in path:
            continue
        job = path[1] if len(path) > 1 and path[0] == "jobs" else None
        base = run_base(path, step)
        for offset, line in enumerate(value.splitlines()):
            words = list(config_literals(line))
            if len(words) < 3 or not re.fullmatch(r"python(?:3(?:\.\d+)?)?", words[0]) or words[1:3] != ["-m", "venv"]:
                continue
            if len(words) != 4 or "*" in words[-1]:
                yield lineno + offset, None, "", "unsupported venv declaration; use python -m venv LITERAL_PATH on its own line"
                continue
            if any(c in base + words[-1] for c in "$`"):
                continue  # Dynamic outputs were already outside static path scope.
            output = posixpath.normpath(posixpath.join(base, words[3]))
            if output.startswith(("../", "/")) or output in (".", ".."):
                yield lineno + offset, None, "", "generated venv must stay inside the repository"
            elif any(name == output or name.startswith(output + "/") for name in files):
                yield lineno + offset, None, "", "generated venv overlaps tracked files"
            else:
                generated.setdefault(job, set()).add(output)

    for lineno, path, step, value in fields:
        key = path[-1]
        if dependabot:
            if key not in ("directory", "directories"):
                continue
            items = value[1:-1].split(",") if value.startswith("[") and value.endswith("]") else [value]
            for item in items:
                try:
                    yield lineno, scalar(item), "", None
                except ValueError as exc:
                    yield lineno, None, "", str(exc)
            continue
        if key in ("name", "description", "working-directory"):
            continue
        if key == "uses":
            if value.strip("\"'").startswith("./"):
                yield lineno, value.strip("\"'"), "", None
            continue  # owner/action@ref and docker:// refs are external
        if key in ("file", "context"):
            try:
                literal = scalar(value)
                if not EXTERNAL.match(literal) and not any(c in literal for c in "$`"):
                    yield lineno, literal, "", None
            except ValueError as exc:
                yield lineno, None, "", str(exc)
        job = path[1] if len(path) > 1 and path[0] == "jobs" else None
        base = run_base(path, step) if key == "run" and "defaults" not in path else ""
        if any(c in base for c in "$`"):
            continue  # A computed cwd makes relative run paths indeterminate.
        for offset, line in enumerate(value.splitlines()):
            for literal in config_literals(line):
                if any(c in literal for c in "$`=") or "DYNAMIC" in literal or EXTERNAL.match(literal):
                    continue
                # ./... is a Go package pattern. A bare ./program may have
                # been downloaded/built; require a suffix or a repo prefix.
                relative_file = re.match(r"^\.\.?/(?!\.\.\.$).+\.[a-zA-Z0-9]+$", literal)
                if key == "run":
                    relative = posixpath.normpath(posixpath.join(base, literal))
                    if any(relative == output or relative.startswith(output + "/") for output in generated.get(job, ())):
                        continue
                if re.match(rf"^(?:\.\.?/)*(?:{prefixes})/", literal) or relative_file:
                    yield lineno + offset, literal, base, None


def glob_matches(path, pattern):
    """Match path segments; only ** can cross directory boundaries."""
    parts, patterns = path.split("/"), pattern.split("/")

    @lru_cache(maxsize=None)
    def match(i, j):
        if j == len(patterns):
            return i == len(parts)
        if patterns[j] == "**":
            return match(i, j + 1) or (i < len(parts) and match(i + 1, j))
        return i < len(parts) and fnmatch.fnmatchcase(parts[i], patterns[j]) and match(i + 1, j + 1)

    return match(0, 0)


def tracked_file(root, files, path):
    """Resolve an input only after validating both index membership and bounds."""
    relative = path.relative_to(root).as_posix()
    if relative not in files:
        raise ValueError(f"missing tracked input: {relative!r}")
    resolved = path.resolve()
    if not resolved.is_relative_to(root):
        raise ValueError("tracked path escapes repository")
    if resolved.relative_to(root).as_posix() not in files:
        raise ValueError("resolved file is not tracked")
    if not resolved.is_file():
        raise ValueError("tracked file missing from checkout")
    return resolved


def layout_policy(root, files):
    policy = json.loads(tracked_file(root, files, root / POLICY).read_text(encoding="utf-8"))
    if not isinstance(policy, dict):
        raise ValueError("layout policy must be an object")
    for key in ("root_files", "top_directories"):
        values = policy.get(key)
        if not isinstance(values, list) or not values or any(not isinstance(value, str) or not value or "/" in value or value in (".", "..") for value in values):
            raise ValueError(f"layout policy {key!r} must be a nonempty list of top-level names")
    return policy


class Checker:
    def __init__(self, root, files, policy):
        self.root = root.resolve()
        self.files = set(files)
        self.policy = policy
        self.errors = set()
        self.anchor_cache = {}

    def error(self, source, line, message):
        self.errors.add(f"{source}:{line}: {message}")

    def target(self, source, line, value, base="", glob=False):
        value = html.unescape(re.sub(r"\\([\W_])", r"\1", value))
        if EXTERNAL.match(value):
            return None
        try:
            url = urlsplit(value)
            path = unquote(url.path)
            fragment = unquote(url.fragment)
            candidate = posixpath.join(base, path) if not path.startswith("/") else path.lstrip("/")
            relative = posixpath.normpath(candidate)
            resolved = (self.root / candidate).resolve()
            if not resolved.is_relative_to(self.root):
                raise ValueError("path escapes repository")
            present = relative == "." or relative in self.files or any(p.startswith(relative.rstrip("/") + "/") for p in self.files)
            if glob and any(c in relative for c in "*?["):
                present = any(glob_matches(p, relative) for p in self.files)
            elif not resolved.exists():
                present = False
            elif present and resolved.is_file() and resolved.relative_to(self.root).as_posix() not in self.files:
                raise ValueError("resolved file is not tracked")
            if not present:
                raise ValueError("missing tracked target")
            return relative, resolved, fragment
        except (ValueError, OSError, RuntimeError) as exc:
            self.error(source, line, f"{exc}: {value!r}")
            return None

    def run(self):
        for name in sorted(self.files):
            top, sep, _ = name.partition("/")
            allowed = self.policy["top_directories"] if sep else self.policy["root_files"]
            if top not in allowed:
                self.error(name, 1, "unexpected top-level directory" if sep else "unexpected root filename")
            try:
                input_path = tracked_file(self.root, self.files, self.root / name)
            except (ValueError, OSError, RuntimeError) as exc:
                self.error(name, 1, str(exc))
                continue
            if name.endswith(".md"):
                text = input_path.read_text(encoding="utf-8")
                for line, value, error in links(text):
                    if error:
                        self.error(name, line, error)
                        continue
                    target = self.target(name, line, name + (value or "")) if not value or value.startswith(("#", "?")) else self.target(name, line, value, posixpath.dirname(name))
                    if target and target[2] and target[0].endswith(".md"):
                        key, path, fragment = target
                        if key not in self.anchor_cache:
                            self.anchor_cache[key] = anchors(path.read_text(encoding="utf-8"))
                        if fragment not in self.anchor_cache[key]:
                            self.error(name, line, f"missing Markdown anchor: {value!r}")
            if (name.startswith(".github/workflows/") or name in (".github/dependabot.yml", ".github/dependabot.yaml")) and name.endswith((".yml", ".yaml")):
                text = input_path.read_text(encoding="utf-8")
                for line, value, base, error in config_paths(text, self.policy["top_directories"], "dependabot" in name, self.files):
                    if error:
                        self.error(name, line, error)
                    else:
                        self.target(name, line, value, base=base, glob=True)
        self.docs_index()
        return sorted(self.errors)

    def docs_index(self):
        """When docs/README.md exists, every other docs/*.md page must be linked from it."""
        if DOCS_INDEX not in self.files:
            return
        text = (self.root / DOCS_INDEX).read_text(encoding="utf-8")
        linked = set()
        for _, value, error in links(text):
            if error or not value or EXTERNAL.match(value):
                continue
            path = unquote(urlsplit(value).path)
            if path:
                linked.add(posixpath.normpath(posixpath.join("docs", path)))
        for name in sorted(self.files):
            if posixpath.dirname(name) == "docs" and name.endswith(".md") and name != DOCS_INDEX and name not in linked:
                self.error(DOCS_INDEX, 1, f"docs index does not link {name}; add it under the section for its readers")


def main(argv=None):
    parser = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    parser.add_argument("--root", type=Path, default=Path(__file__).resolve().parent.parent)
    args = parser.parse_args(argv)
    try:
        root = args.root.resolve()
        top = subprocess.run(["git", "-C", str(root), "rev-parse", "--show-toplevel"], check=True, capture_output=True, text=True)
        if Path(top.stdout.strip()).resolve() != root:
            raise ValueError("--root must be the Git repository root")
        inventory = subprocess.run(["git", "-C", str(root), "ls-files", "-z"], check=True, capture_output=True)
        files = inventory.stdout.decode("utf-8").split("\0")[:-1]
        if not files:
            raise ValueError("repository has no tracked files")
        policy = layout_policy(root, set(files))
        errors = Checker(root, files, policy).run()
    except (OSError, ValueError, RuntimeError, subprocess.CalledProcessError) as exc:
        print(f"repo-check: {exc}", file=sys.stderr)
        return 1
    if errors:
        for error in errors:
            print(error, file=sys.stderr)
        return 1
    print(f"repo-check: OK ({len(files)} tracked files)")
    return 0


if __name__ == "__main__":
    sys.exit(main())
