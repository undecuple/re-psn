"""Focused, offline repo-check tests using real temporary Git indexes.

Run: python3 -m unittest -v tools.test_repo_check
"""
import contextlib
import io
import json
from pathlib import Path
import subprocess
import tempfile
import unittest

from tools import repo_check


class RepoCheckTest(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory()
        self.addCleanup(self.tmp.cleanup)
        self.root = Path(self.tmp.name) / "repo"
        self.root.mkdir()
        self.git("init", "--quiet")
        self.write("README.md", "# Fixture\n")
        self.write("docs/guide.md", "# Guide\n")
        self.write("scripts/check.sh", "#!/bin/sh\n")
        self.write("app/go.mod", "module example.org/app\n")
        self.write("tools/check.py", "pass\n")
        self.write(repo_check.POLICY, Path(repo_check.__file__).with_name("repo_layout.json").read_text(encoding="utf-8"))
        self.track()

    def git(self, *args):
        return subprocess.run(["git", "-C", str(self.root), *args], check=True, capture_output=True)

    def write(self, name, text):
        path = self.root / name
        path.parent.mkdir(parents=True, exist_ok=True)
        path.write_text(text, encoding="utf-8")
        return path

    def track(self):
        self.git("add", "--all")

    def check(self, root=None):
        err, out = io.StringIO(), io.StringIO()
        with contextlib.redirect_stderr(err), contextlib.redirect_stdout(out):
            code = repo_check.main(["--root", str(root or self.root)])
        return code, err.getvalue(), out.getvalue()

    def good(self):
        code, err, _ = self.check()
        self.assertEqual(code, 0, err)
        self.assertEqual(err, "")

    def bad(self, needle):
        code, err, _ = self.check()
        self.assertEqual(code, 1, err)
        self.assertIn(needle, err)
        return err

    def test_valid_inline_and_reference_links(self):
        self.write("README.md", """# Fixture
[guide](docs/guide.md#guide "title") ![image](docs/image.png)
[ref][guide] [guide][] [guide]
[guide]: <docs/guide.md#guide> 'title'
[empty]() [self](#fixture) [query](?plain=1#fixture)
[dir](scripts/) [root](/docs/guide.md)
[paren](docs/a(b).md) [escaped](docs/a\\(b\\).md)
[space](<docs/a b.md>) [encoded](docs/a%20b.md)
""")
        self.write("docs/image.png", "fake fixture image")
        self.write("docs/a(b).md", "# Parenthesis\n")
        self.write("docs/a b.md", "# Space\n")
        self.track()
        self.good()

    def test_docs_index_must_link_every_docs_page(self):
        self.write("docs/README.md", "# Docs\n[other](guide.md#guide)\n")
        self.track()
        self.good()
        self.write("docs/extra.md", "# Extra\n")
        self.track()
        err = self.bad("docs/README.md:1: docs index does not link docs/extra.md")
        self.assertNotIn("docs/guide.md", err)
        self.write("docs/README.md", "# Docs\n[a](guide.md) [b](./extra.md) [site](https://example.org/x.md)\n")
        self.track()
        self.good()

    def test_docs_index_ignores_subfolders_and_is_optional(self):
        self.write("docs/sub/page.md", "# Sub\n")
        self.track()
        self.good()
        self.write("docs/README.md", "# Docs\n[a](guide.md)\n")
        self.track()
        self.good()

    def test_linked_image_target_is_checked(self):
        self.write("README.md", "[![image](docs/missing.png)](https://example.invalid/)\n")
        self.bad("missing tracked target: 'docs/missing.png'")
        self.write("docs/missing.png", "fixture image")
        self.track()
        self.good()
        self.write("README.md", "[![image][img]](docs/guide.md)\n[img]: docs/nope.png\n")
        self.bad("missing tracked target: 'docs/nope.png'")

    def test_multiline_reference_definition_fails_explicitly(self):
        self.write("README.md", "[ref]:\n  docs/missing.md\n[ref]\n")
        self.bad("unsupported multiline/empty Markdown reference definition")

    def test_missing_link_and_untracked_target(self):
        self.write("README.md", "[missing](docs/missing.md)\n")
        self.write("docs/missing.md", "# Present but not tracked\n")
        self.bad("README.md:1: missing tracked target: 'docs/missing.md'")
        self.track()
        self.good()
        (self.root / "docs/missing.md").unlink()
        self.bad("missing tracked target")

    def test_bad_anchor(self):
        self.write("README.md", "[bad](docs/guide.md#no-such-heading)\n")
        self.bad("missing Markdown anchor")

    def test_duplicate_unicode_and_explicit_anchors(self):
        self.write("docs/guide.md", """# Repeat
# Repeat
# Repeat-1
# Repeat
Setext heading
--------------
## Hello, *World*! `x_y` &amp; café
## _Emphasis_ and `_literal_`
<a id="chosen"></a><a name='legacy'></a>
""")
        fragments = ("repeat", "repeat-1", "repeat-1-1", "repeat-2", "setext-heading", "hello-world-x_y--café", "emphasis-and-_literal_", "chosen", "legacy")
        self.write("README.md", "\n".join(f"[link](docs/guide.md#{fragment})" for fragment in fragments))
        self.good()
        self.write("README.md", "[bad](docs/guide.md#repeat-3)\n")
        self.bad("missing Markdown anchor")

    def test_heading_links_use_rendered_text_for_slugs(self):
        self.write("docs/a(b).md", "# Parenthesis\n")
        self.write("README.md", """# [Fixture][guide]
# [Fixture](docs/a(b).md)
# [Fixture][]
# [Fixture]
[guide]: docs/guide.md
[Fixture]: docs/a(b).md
[first](#fixture) [second](#fixture-1) [third](#fixture-2) [fourth](#fixture-3)
""")
        self.track()
        self.good()
        self.write("README.md", "# [Fixture][guide]\n[guide]: docs/guide.md\n[bad](#fixtureguide)\n")
        self.bad("missing Markdown anchor")

    def test_heading_inline_code_is_text_but_not_an_explicit_anchor(self):
        self.write("README.md", '# `<a id="fake"></a>` code\n[ok](#-code)\n[bad](#fake)\n')
        self.bad("README.md:3: missing Markdown anchor")

    def test_fenced_inline_code_and_comments_are_ignored(self):
        self.write("README.md", """# Fixture
`[bad](missing.md)` ``[also bad](absent.md)``
```markdown
[bad](missing.md)
# Phantom
````
~~~
[bad][missing]
~~~
> ```
> [bad](missing.md)
> ```
<!-- [bad](missing.md) -->
[real](docs/guide.md)
""")
        self.good()
        self.write("README.md", self.root.joinpath("README.md").read_text() + "[bad](#phantom)\n")
        self.bad("missing Markdown anchor")

    def test_undefined_reference_and_broken_reference_definition(self):
        self.write("README.md", "[label][unknown]\n")
        self.bad("undefined Markdown reference 'unknown'")
        self.write("README.md", "[label][broken]\n[broken]: docs/nope.md\n")
        self.bad("missing tracked target")

    def test_malformed_real_link_is_diagnosed(self):
        self.write("README.md", "[bad](docs/guide.md \"unterminated)\n")
        self.bad("unsupported Markdown link/title syntax")

    def test_external_links_are_not_fetched(self):
        self.write("README.md", """[http](https://example.invalid/docs/missing.md#nothing)
[scheme](custom:docs/nope) [mail](mailto:?subject=fixture)
[protocol relative](//example.invalid/docs/nope)
""")
        self.good()

    def test_path_traversal_is_rejected(self):
        self.write("README.md", "[bad](../outside.md)\n[encoded](%2e%2e/outside.md)\n")
        self.bad("path escapes repository")

    def test_symlink_target_and_source_containment(self):
        outside = Path(self.tmp.name) / "outside.md"
        outside.write_text("# Outside\n")
        try:
            (self.root / "docs/link.md").symlink_to(outside)
        except OSError as exc:
            self.skipTest(f"symlinks unavailable: {exc}")
        self.write("README.md", "[bad](docs/link.md)\n")
        self.bad("path escapes repository")
        self.track()
        self.bad("tracked path escapes repository")

    def test_tracked_symlink_to_untracked_input_is_rejected(self):
        self.write("docs/private.md", "# Private\n[do not read](secret.md)\n")
        try:
            (self.root / "docs/link.md").symlink_to("private.md")
        except OSError as exc:
            self.skipTest(f"symlinks unavailable: {exc}")
        self.git("add", "docs/link.md")
        self.write("README.md", "[bad](docs/link.md#private)\n")
        err = self.bad("resolved file is not tracked")
        self.assertNotIn("secret.md", err)
        (self.root / "docs/link.md").unlink()
        (self.root / "docs/link.md").symlink_to("guide.md")
        self.write("README.md", "[good](docs/link.md#guide)\n")
        self.good()

    def test_policy_symlink_is_validated_before_read(self):
        outside = Path(self.tmp.name) / "outside.json"
        outside.write_text("not JSON; must never be parsed")
        policy = self.root / repo_check.POLICY
        policy.unlink()
        try:
            policy.symlink_to(outside)
        except OSError as exc:
            self.skipTest(f"symlinks unavailable: {exc}")
        self.git("add", repo_check.POLICY)
        self.bad("tracked path escapes repository")
        policy.unlink()
        self.write("tools/untracked.json", "also not JSON")
        policy.symlink_to("untracked.json")
        self.bad("resolved file is not tracked")

    def test_policy_schema_is_validated_without_traceback(self):
        for value in ([], {}, {"root_files": "README.md", "top_directories": ["tools"]}, {"root_files": [1], "top_directories": ["tools"]}):
            with self.subTest(value=value):
                self.write(repo_check.POLICY, json.dumps(value))
                err = self.bad("layout policy")
                self.assertNotIn("Traceback", err)

    def test_policy_must_be_a_tracked_input(self):
        self.git("rm", "--cached", repo_check.POLICY)
        self.bad("missing tracked input: 'tools/repo_layout.json'")

    def test_symlink_parent_traversal_is_not_normalized_away(self):
        outside = Path(self.tmp.name) / "other" / "child"
        outside.mkdir(parents=True)
        try:
            (self.root / "docs/link").symlink_to(outside, target_is_directory=True)
        except OSError as exc:
            self.skipTest(f"symlinks unavailable: {exc}")
        self.write("README.md", "[bad](docs/link/../guide.md)\n")
        self.bad("path escapes repository")

    def test_unexpected_tracked_layout_only(self):
        self.write("new.md", "# Untracked\n")
        self.write("assets/image.png", "untracked")
        self.good()
        self.track()
        err = self.bad("unexpected root filename")
        self.assertIn("assets/image.png:1: unexpected top-level directory", err)

    def workflow(self, text):
        self.write(".github/workflows/test.yml", text)
        self.track()

    def test_missing_static_workflow_paths(self):
        self.workflow("""jobs:
  test:
    steps:
      - run: sh scripts/missing.sh
      - run: python3 ./missing.py
      - uses: ./missing-action
      - uses: docker/build-push-action@abc
        with:
          file: docker/Missingfile
""")
        err = self.bad("scripts/missing.sh")
        for value in ("./missing.py", "./missing-action", "docker/Missingfile"):
            self.assertIn(f"missing tracked target: '{value}'", err)

    def test_workflow_quoted_paths_retain_spaces(self):
        self.write("scripts/space name.sh", "#!/bin/sh\n")
        self.workflow("""jobs:
  test:
    steps:
      - run: sh "scripts/space name.sh"
      - run: 'sh "scripts/space name.sh"'
      - run: sh 'scripts/space name.sh'
""")
        self.good()
        self.workflow('jobs:\n  test:\n    steps:\n      - run: sh "scripts/missing space.sh"\n')
        err = self.bad("missing tracked target: 'scripts/missing space.sh'")
        self.assertNotIn("target: 'scripts/missing'", err)

    def test_declared_venv_output_is_scoped_to_run_and_job(self):
        self.workflow("""jobs:
  test:
    steps:
      - run: |
          python3 -m venv tools/venv
          tools/venv/bin/python tools/check.py
      - run: tools/venv/bin/python tools/check.py
""")
        self.good()
        with (self.root / ".github/workflows/test.yml").open("a") as f:
            f.write("""      - uses: actions/upload-artifact@abc
        with:
          path: tools/venv/output.txt
  other:
    steps:
      - run: tools/venv/bin/python tools/check.py
""")
        err = self.bad("missing tracked target: 'tools/venv/output.txt'")
        self.assertIn("missing tracked target: 'tools/venv/bin/python'", err)
        self.write("README.md", "[source link](tools/venv/source.md)\n")
        self.bad("missing tracked target: 'tools/venv/source.md'")

    def test_undeclared_generated_looking_source_still_fails(self):
        self.workflow("jobs:\n  test:\n    steps:\n      - run: python tools/venv/check.py\n")
        self.bad("missing tracked target: 'tools/venv/check.py'")

    def test_venv_declaration_cannot_hide_tracked_sources(self):
        self.workflow("""jobs:
  test:
    steps:
      - run: |
          python3 -m venv tools
          python tools/missing.py
""")
        err = self.bad("generated venv overlaps tracked files")
        self.assertIn("missing tracked target: 'tools/missing.py'", err)

    def test_workflow_cwd_defaults_override_and_root_action_input(self):
        self.workflow("""defaults:
  run:
    working-directory: docs
jobs:
  test:
    defaults:
      run:
        working-directory: app
    steps:
      - run: python3 ../tools/check.py
      - uses: actions/setup-go@abc
        with:
          cache-dependency-path: app/go.mod
      - working-directory: .
        run: sh scripts/check.sh
      - run: sh scripts/missing.sh
        working-directory: .
  other:
    steps:
      - run: cat ../README.md
""")
        err = self.bad("scripts/missing.sh")
        self.assertEqual(len(err.splitlines()), 1, err)
        self.write("scripts/missing.sh", "#!/bin/sh\n")
        self.track()
        self.good()

    def test_workflow_dynamic_external_generated_and_go_paths(self):
        self.workflow("""jobs:
  test:
    steps:
      - run: |
          python tools/${{ matrix.file }}.py
          sh scripts/$SCRIPT
          curl https://example.invalid/scripts/missing.sh
          ./gitleaks git .
          go test ./...
          # scripts/obsolete.sh is only a comment.
          cat dist/output.txt
          python "$RUNNER_TEMP/tools/test.py"
      - uses: docker/build-push-action@abc
        with:
          file: ${{ matrix.dockerfile }}
          context: https://example.invalid/missing
      - uses: actions/upload-artifact@abc
        with:
          path: dist/
""")
        self.good()

    def test_workflow_globs(self):
        self.workflow("on:\n  push:\n    paths: ['docs/**']\n")
        self.good()
        self.workflow("on:\n  push:\n    paths: ['docs/**/*.md']\n")
        self.good()
        self.workflow("on:\n  push:\n    paths: ['docs/missing/**']\n")
        self.bad("missing tracked target")

    def test_workflow_path_field_unsupported_alias_is_error(self):
        self.workflow("jobs:\n  test:\n    steps:\n      - working-directory: *cwd\n        run: echo hi\n")
        self.bad("unsupported path syntax")

    def test_globs_keep_single_star_within_path_segments(self):
        self.write("scripts/nested/input.txt", "fixture\n")
        self.workflow("on:\n  push:\n    paths: ['scripts/*.txt']\n")
        self.bad("missing tracked target")
        self.workflow("on:\n  push:\n    paths: ['scripts/**/*.txt']\n")
        self.good()
        self.workflow("on:\n  push:\n    paths: ['scripts/nested/*.txt']\n")
        self.good()
        self.write("scripts/direct.txt", "fixture\n")
        self.workflow("on:\n  push:\n    paths: ['scripts/**/*.txt']\n")
        self.good()

    def test_dependabot_flow_and_block_lists(self):
        self.write(".github/dependabot.yml", """version: 2
updates:
  - package-ecosystem: gomod
    directory: /
  - package-ecosystem: docker
    directories: ['/app', /docs]
  - package-ecosystem: docker
    directories:
      - /scripts
      - '/tools'
""")
        self.track()
        self.good()
        with (self.root / ".github/dependabot.yml").open("a") as f:
            f.write("      - /missing\n")
        self.bad("missing tracked target: '/missing'")

    def test_dependabot_unsupported_path_syntax(self):
        self.write(".github/dependabot.yml", "updates:\n  - directories: *dirs\n")
        self.track()
        self.bad("unsupported path syntax")

    def test_workflow_unsupported_yaml_merge(self):
        self.workflow("jobs:\n  test:\n    <<: *defaults\n    steps:\n      - run: echo hi\n")
        self.bad("unsupported YAML merge")

    def test_config_traversal(self):
        self.workflow("jobs:\n  test:\n    steps:\n      - working-directory: ../outside\n        run: echo hi\n")
        self.bad("path escapes repository")

    def test_diagnostics_are_sorted_and_repeatable(self):
        self.write("README.md", "[z](docs/z.md)\n[a](docs/a.md)\n")
        err = self.bad("missing tracked target")
        self.assertEqual(err, self.check()[1])
        self.assertEqual(err.splitlines(), sorted(err.splitlines()))

    def test_unversioned_subdirectory_cannot_use_ancestor_index(self):
        child = self.root / "export"
        child.mkdir()
        code, err, _ = self.check(child)
        self.assertEqual(code, 1)
        self.assertIn("--root must be the Git repository root", err)

    def test_empty_index_is_error(self):
        self.git("rm", "--cached", "-r", ".")
        self.bad("repository has no tracked files")


if __name__ == "__main__":
    unittest.main()
