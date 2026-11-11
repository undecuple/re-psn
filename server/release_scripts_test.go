package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"testing"
)

// A small tree with every file scripts/bump-version.sh touches, at version 0.3.1.
var fixtureTree = map[string]string{
	"VERSION":        "0.3.1\n",
	"server/main.go": "package main\n\nvar version = \"0.3.1\"\n",
	"README.md": "# x\n\n**<!-- version -->v0.3.1<!-- /version -->, alpha.** Get " +
		"<!-- version -->[0.3.1](https://example.invalid/r/releases/tag/v0.3.1)<!-- /version -->.\n\nLog: version=0.3.1\n",
	"CHANGELOG.md": `# Changelog

## [Unreleased]

### Added
- New thing.
  More of it.

### Fixed
- A fix.

## [0.3.1] - 2026-10-01

### Fixed
- Old fix.

## [0.3.0] - 2026-09-01

### Added
- First.

[Unreleased]: https://example.invalid/r/compare/v0.3.1...HEAD
[0.3.1]: https://example.invalid/r/compare/v0.3.0...v0.3.1
[0.3.0]: https://example.invalid/r/releases/tag/v0.3.0
`,
}

func needShell(t *testing.T, tools ...string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("the release scripts are POSIX shell; not run on Windows")
	}
	for _, tool := range append([]string{"sh", "awk", "diff"}, tools...) {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skipf("%s not on PATH", tool)
		}
	}
}

// newTree writes files (and copies the release scripts) into a fresh directory and returns it.
func newTree(t *testing.T, files map[string]string) string {
	t.Helper()
	root := t.TempDir()
	write := func(rel string, data []byte, mode os.FileMode) {
		p := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, data, mode); err != nil {
			t.Fatal(err)
		}
	}
	for rel, text := range files {
		write(rel, []byte(text), 0o644)
	}
	for _, s := range []string{"bump-version.sh", "release-notes.sh", "check-release.sh", "build-appimage.sh", "install.sh", "install.ps1"} {
		b, err := os.ReadFile(filepath.Join("..", "scripts", s))
		if err != nil {
			t.Fatal(err)
		}
		write("scripts/"+s, b, 0o755)
	}
	return root
}

// gitEnv keeps the user's git configuration (signing, hooks, identity) out of the test repositories.
var gitEnv = append(os.Environ(), "GIT_CONFIG_GLOBAL="+os.DevNull, "GIT_CONFIG_NOSYSTEM=1",
	"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@example.invalid", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@example.invalid")

func run(t *testing.T, dir, name string, args ...string) (stdout, stderr string, err error) {
	t.Helper()
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	cmd.Env = gitEnv
	var o, e bytes.Buffer
	cmd.Stdout, cmd.Stderr = &o, &e
	err = cmd.Run()
	return o.String(), e.String(), err
}

func readFile(t *testing.T, root, rel string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel)))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestVersionAgreesCatchesEachPlace(t *testing.T) {
	if err := versionsAgree(newTree(t, fixtureTree)); err != nil {
		t.Fatalf("fixture: %v", err)
	}
	for name, change := range map[string][2]string{
		"VERSION":        {"VERSION", "0.3.2\n"},
		"main.go":        {"server/main.go", "package main\n\nvar version = \"0.3.0\"\n"},
		"main.go ldflag": {"server/main.go", "package main\n\nvar version string\n"},
		"CHANGELOG":      {"CHANGELOG.md", "## [Unreleased]\n\n## [0.3.2] - 2026-11-01\n"},
		"README span":    {"README.md", "<!-- version -->v0.3.1<!-- /version --> <!-- version -->0.3.0<!-- /version -->\n"},
		"README none":    {"README.md", "v0.3.1\n"},
	} {
		files := map[string]string{}
		for k, v := range fixtureTree {
			files[k] = v
		}
		files[change[0]] = change[1]
		if err := versionsAgree(newTree(t, files)); err == nil {
			t.Errorf("%s: a disagreement was not caught", name)
		} else {
			t.Logf("%s: %v", name, err)
		}
	}
}

func TestReleaseNotes(t *testing.T) {
	needShell(t)
	files := map[string]string{}
	for k, v := range fixtureTree {
		files[k] = v
	}
	files["CHANGELOG.md"] = strings.ReplaceAll(fixtureTree["CHANGELOG.md"], "\n", "\r\n") // CRLF in, LF out
	files["extra.md"] = "### Files\r\n\r\nre-psn-v0.3.1-linux-amd64.tar.gz\r\n"
	root := newTree(t, files)
	notice := "Pre-release: this is a 0.x version and may change without notice.\n\n"
	closing := "Next release: when ready\n"
	for _, c := range []struct {
		args []string
		want string
	}{
		{[]string{"0.3.1"}, notice + "### Fixed\n- Old fix.\n\n" + closing},
		{[]string{"0.3.0"}, notice + "### Added\n- First.\n\n" + closing},
		{[]string{"--unreleased"}, notice + "### Added\n- New thing.\n  More of it.\n\n### Fixed\n- A fix.\n\n" + closing},
		{[]string{"--extra", "extra.md", "0.3.1"},
			notice + "### Fixed\n- Old fix.\n\n### Files\n\nre-psn-v0.3.1-linux-amd64.tar.gz\n\n" + closing},
	} {
		out, errout, err := run(t, root, "sh", append([]string{"scripts/release-notes.sh"}, c.args...)...)
		if err != nil {
			t.Errorf("%v: %v: %s", c.args, err, errout)
			continue
		}
		if out != c.want {
			t.Errorf("%v:\n got %q\nwant %q", c.args, out, c.want)
		}
	}
	for _, args := range [][]string{{"0.9.9"}, {"0.3"}, {}, {"0.3.1", "0.3.0"}} {
		if out, _, err := run(t, root, "sh", append([]string{"scripts/release-notes.sh"}, args...)...); err == nil {
			t.Errorf("%v: want an error, got %q", args, out)
		}
	}

	// No notice from 1.0.0 on.
	one := newTree(t, map[string]string{"VERSION": "1.0.0\n", "CHANGELOG.md": "## [Unreleased]\n\n## [1.0.0] - 2027-01-01\n\n- Done.\n"})
	if out, errout, err := run(t, one, "sh", "scripts/release-notes.sh", "1.0.0"); err != nil || out != "- Done.\n\n"+closing {
		t.Errorf("1.0.0: %q %v %s", out, err, errout)
	}
	// An empty section is refused.
	if out, _, err := run(t, one, "sh", "scripts/release-notes.sh", "--unreleased"); err == nil {
		t.Errorf("empty Unreleased: want an error, got %q", out)
	}
}

func TestBumpVersion(t *testing.T) {
	needShell(t, "git", "cmp")
	root := newTree(t, fixtureTree)
	for _, args := range [][]string{{"init", "-q"}, {"add", "-A"}, {"commit", "-q", "-m", "fixture"}} {
		if _, errout, err := run(t, root, "git", args...); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, errout)
		}
	}
	bump := func(args ...string) (string, string, error) {
		return run(t, root, "sh", append([]string{"scripts/bump-version.sh", "--date", "2026-11-05"}, args...)...)
	}
	unchanged := func(when string) {
		t.Helper()
		for rel, text := range fixtureTree {
			if readFile(t, root, rel) != text {
				t.Fatalf("%s: %s changed", when, rel)
			}
		}
	}

	// Refused: not X.Y.Z, not newer, a dirty tree.
	for _, v := range []string{"0.3", "v0.4.0", "0.3.1", "0.3.0", "0.2.9", "00.4.0"} {
		if out, _, err := bump(v); err == nil {
			t.Errorf("%s: want a refusal, got %q", v, out)
		}
	}
	if err := os.WriteFile(filepath.Join(root, "stray"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, errout, err := bump("0.4.0"); err == nil || !strings.Contains(errout, "working tree") {
		t.Errorf("dirty tree: want a refusal, got %v %q", err, errout)
	}
	unchanged("refusals")

	// --dry-run prints the diff (a dirty tree is allowed) and writes nothing.
	out, errout, err := bump("--dry-run", "0.4.0")
	if err != nil {
		t.Fatalf("dry run: %v: %s", err, errout)
	}
	for _, want := range []string{"+++ b/VERSION", "+0.4.0", "+++ b/server/main.go", `+var version = "0.4.0"`,
		"+++ b/CHANGELOG.md", "+## [0.4.0] - 2026-11-05", "+[0.4.0]: https://example.invalid/r/compare/v0.3.1...v0.4.0",
		"+++ b/README.md", "<!-- version -->v0.4.0<!-- /version -->"} {
		if !strings.Contains(out, want) {
			t.Errorf("dry run output lacks %q:\n%s", want, out)
		}
	}
	unchanged("dry run")
	if err := os.Remove(filepath.Join(root, "stray")); err != nil {
		t.Fatal(err)
	}

	// The real bump.
	if out, errout, err := bump("0.4.0"); err != nil {
		t.Fatalf("bump: %v: %s %s", err, out, errout)
	}
	if err := versionsAgree(root); err != nil {
		t.Errorf("after the bump: %v", err)
	}
	cl := readFile(t, root, "CHANGELOG.md")
	wantCl := strings.Replace(fixtureTree["CHANGELOG.md"], "## [Unreleased]\n", "## [Unreleased]\n\n## [0.4.0] - 2026-11-05\n", 1)
	wantCl = strings.Replace(wantCl, "[Unreleased]: https://example.invalid/r/compare/v0.3.1...HEAD\n",
		"[Unreleased]: https://example.invalid/r/compare/v0.4.0...HEAD\n[0.4.0]: https://example.invalid/r/compare/v0.3.1...v0.4.0\n", 1)
	if cl != wantCl {
		t.Errorf("CHANGELOG.md:\n%s\nwant:\n%s", cl, wantCl)
	}
	wantReadme := strings.Replace(strings.Replace(fixtureTree["README.md"], "v0.3.1<", "v0.4.0<", 1),
		"[0.3.1](https://example.invalid/r/releases/tag/v0.3.1)", "[0.4.0](https://example.invalid/r/releases/tag/v0.4.0)", 1)
	if got := readFile(t, root, "README.md"); got != wantReadme || !strings.Contains(got, "Log: version=0.3.1") {
		t.Errorf("README.md:\n%s\nwant:\n%s", got, wantReadme)
	}
	st, _, _ := run(t, root, "git", "status", "--porcelain")
	if st != " M CHANGELOG.md\n M README.md\n M VERSION\n M server/main.go\n" {
		t.Errorf("the bump changed other files:\n%s", st)
	}
	notes, errout, err := run(t, root, "sh", "scripts/release-notes.sh", "0.4.0")
	if err != nil || !strings.Contains(notes, "- New thing.\n  More of it.\n\n### Fixed\n- A fix.\n\nNext release: when ready\n") {
		t.Errorf("notes for 0.4.0: %v %s\n%s", err, errout, notes)
	}
	if _, _, err := run(t, root, "sh", "scripts/release-notes.sh", "--unreleased"); err == nil {
		t.Error("Unreleased should be empty after the bump")
	}

	// Committed, a second bump with nothing under Unreleased is refused.
	if _, errout, err := run(t, root, "git", "commit", "-q", "-am", "bump"); err != nil {
		t.Fatalf("commit: %v: %s", err, errout)
	}
	if _, errout, err := bump("0.4.1"); err == nil || !strings.Contains(errout, "nothing under Unreleased") {
		t.Errorf("empty Unreleased: want a refusal, got %v %q", err, errout)
	}
}

// The tag check in scripts/check-release.sh runs before anything else, so a wrong tag fails without running Go. The passing
// case goes on to `go test`, which the fixture tree cannot run; there the test only checks that the tag was accepted.
func TestCheckReleaseTag(t *testing.T) {
	needShell(t)
	root := newTree(t, fixtureTree) // VERSION 0.3.1
	const refused = "is not v + VERSION"
	for _, tag := range []string{"v0.3.2", "0.3.1", "v0.3.1-rc1", "v0.3.10", "V0.3.1"} {
		_, errout, err := run(t, root, "sh", "scripts/check-release.sh", "--tag", tag)
		if err == nil || !strings.Contains(errout, refused) {
			t.Errorf("%s: want a refusal, got %v %q", tag, err, errout)
		}
	}
	for _, args := range [][]string{{"--tag"}, {"--tag", ""}, {"v0.3.1"}, {"--tag", "v0.3.1", "x"}} {
		if _, errout, err := run(t, root, "sh", append([]string{"scripts/check-release.sh"}, args...)...); err == nil ||
			!strings.Contains(errout, "usage:") {
			t.Errorf("%q: want a usage error, got %v %q", args, err, errout)
		}
	}
	if _, errout, _ := run(t, root, "sh", "scripts/check-release.sh", "--tag", "v0.3.1"); strings.Contains(errout, refused) {
		t.Errorf("v0.3.1 against VERSION 0.3.1 was refused: %q", errout)
	}
}

// build-appimage.sh refuses a bad tag, a missing tool and a tool whose SHA-256 is not the pinned one, before it runs
// anything: the stand-in appimagetool writes a marker file when run, and the marker must never appear.
func TestBuildAppImageRefuses(t *testing.T) {
	needShell(t, "bash", "sha256sum", "cut")
	root := newTree(t, fixtureTree) // VERSION 0.3.1
	tools := t.TempDir()
	marker := filepath.Join(t.TempDir(), "ran")
	if err := os.WriteFile(filepath.Join(tools, "appimagetool-x86_64.AppImage"),
		[]byte("#!/bin/sh\ntouch '"+marker+"'\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tools, "runtime-x86_64"), []byte("not the runtime\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	build := func(args ...string) string {
		t.Helper()
		_, errout, err := run(t, root, "bash", append([]string{"scripts/build-appimage.sh"}, args...)...)
		if err == nil {
			t.Errorf("%q: want a refusal, got success", args)
		}
		if _, err := os.Stat(marker); err == nil {
			t.Fatalf("%q: the tool ran", args)
		}
		return errout
	}
	for tag, want := range map[string]string{
		"0.3.1":      "is not vMAJOR.MINOR.PATCH",
		"v0.3":       "is not vMAJOR.MINOR.PATCH",
		"v0.3.1-rc1": "odd characters",
		"v0.3.1;x":   "odd characters",
		"v0.3.2":     "is not v + VERSION",
		"v0.3.10":    "is not v + VERSION",
	} {
		if errout := build("build", tag, tools); !strings.Contains(errout, want) {
			t.Errorf("%s: want %q, got %q", tag, want, errout)
		}
	}
	for _, tag := range []string{"v0.3.1", "v0.0.0"} { // the right tag, and the dry run's
		errout := build("build", tag, tools)
		if !strings.Contains(errout, "SHA-256 mismatch") || !strings.Contains(errout, "refusing to run appimagetool") {
			t.Errorf("%s with a wrong tool: want a hash refusal, got %q", tag, errout)
		}
	}
	if errout := build("build", "v0.3.1", t.TempDir()); !strings.Contains(errout, "is missing") {
		t.Errorf("empty tools directory: want a missing-file refusal, got %q", errout)
	}
	for _, args := range [][]string{{}, {"build"}, {"build", "v0.3.1"}, {"fetch"}, {"other", "x"}} {
		if errout := build(args...); !strings.Contains(errout, "usage:") {
			t.Errorf("%q: want a usage error, got %q", args, errout)
		}
	}
}

// The tool pins in scripts/build-appimage.sh and .github/workflows/release.yml are the same fixed releases, and
// NOTICE.md and docs/releasing.md state the same hashes.
func TestAppImagePinsAgree(t *testing.T) {
	read := func(rel string) string {
		b, err := os.ReadFile(filepath.Join("..", filepath.FromSlash(rel)))
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}
	script, workflow := read("scripts/build-appimage.sh"), read(".github/workflows/release.yml")
	notice, releasing := read("NOTICE.md"), read("docs/releasing.md")
	hex64 := regexp.MustCompile(`^[0-9a-f]{64}$`)
	for _, key := range []string{"APPIMAGETOOL_URL", "APPIMAGETOOL_SHA256", "RUNTIME_URL", "RUNTIME_SHA256"} {
		re := regexp.MustCompile(`(?m)^\s*` + key + `[=:] ?(\S+)\s*$`)
		s, w := re.FindAllStringSubmatch(script, -1), re.FindAllStringSubmatch(workflow, -1)
		if len(s) != 1 || len(w) != 1 {
			t.Fatalf("%s: want one pin in each file, got %d in the script and %d in the workflow", key, len(s), len(w))
		}
		if s[0][1] != w[0][1] {
			t.Errorf("%s: script %q, workflow %q", key, s[0][1], w[0][1])
		}
		v := s[0][1]
		switch {
		case strings.HasSuffix(key, "_URL"):
			if !strings.HasPrefix(v, "https://github.com/AppImage/") || !strings.Contains(v, "/releases/download/") ||
				strings.Contains(v, "continuous") || strings.Contains(v, "latest") {
				t.Errorf("%s %q is not a fixed AppImage GitHub release", key, v)
			}
		case !hex64.MatchString(v):
			t.Errorf("%s %q is not a SHA-256", key, v)
		case !strings.Contains(notice, v) || !strings.Contains(releasing, v):
			t.Errorf("%s %s is not stated in NOTICE.md and docs/releasing.md", key, v)
		}
	}
	for _, key := range []string{"APPIMAGETOOL", "RUNTIME"} {
		ver := regexp.MustCompile(`(?m)^` + key + `_VERSION=(\S+)$`).FindStringSubmatch(script)
		url := regexp.MustCompile(`(?m)^` + key + `_URL=(\S+)$`).FindStringSubmatch(script)
		if ver == nil || url == nil || !strings.Contains(url[1], "/download/"+ver[1]+"/") {
			t.Errorf("%s: the URL does not name the version %v", key, ver)
		}
	}
}

// releaseAssets is the ten release assets for a tag, sorted as the C locale sorts them.
func releaseAssets(tag string) []string {
	return []string{"SHA256SUMS", "install.ps1", "install.sh", "re-psn-app-" + tag + "-linux-amd64.tar.gz",
		"re-psn-app-" + tag + "-linux-arm64.tar.gz", "re-psn-app-" + tag + "-windows-amd64.zip",
		"re-psn-" + tag + "-linux-amd64.tar.gz", "re-psn-" + tag + "-linux-arm64.tar.gz",
		"re-psn-" + tag + "-windows-amd64.zip", "re-psn-" + tag + "-x86_64.AppImage"}
}

// sumsLine is the SHA256SUMS line for data, in the format sha256sum writes.
func sumsLine(name string, data []byte) string {
	h := sha256.Sum256(data)
	return hex.EncodeToString(h[:]) + "  " + name + "\n"
}

// scripts/check-release.sh --assets passes a directory with the ten assets, whose SHA256SUMS lists the other nine with
// their hashes and whose install scripts are the repository's; it fails on a missing or extra file, a script that is not
// the repository's, a missing, repeated, wrong or malformed SHA256SUMS line, and a bad tag.
func TestCheckReleaseAssets(t *testing.T) {
	needShell(t, "sha256sum", "cmp", "sort", "grep", "sed", "find")
	const tag = "v0.3.1"
	root := newTree(t, fixtureTree)
	type asset struct {
		name string
		data []byte
	}
	script := func(name string) []byte {
		b, err := os.ReadFile(filepath.Join("..", "scripts", name))
		if err != nil {
			t.Fatal(err)
		}
		return b
	}
	// assets returns the nine files other than SHA256SUMS, with data replacing a file's contents and drop leaving one out.
	assets := func(drop string, data map[string][]byte) []asset {
		var as []asset
		for _, name := range releaseAssets(tag) {
			if name == "SHA256SUMS" || name == drop {
				continue
			}
			b := []byte("stand-in for " + name + "\n")
			if name == "install.sh" || name == "install.ps1" {
				b = script(name)
			}
			if d, ok := data[name]; ok {
				b = d
			}
			as = append(as, asset{name, b})
		}
		return as
	}
	sums := func(as []asset) string {
		var b strings.Builder
		for _, a := range as {
			b.WriteString(sumsLine(a.name, a.data))
		}
		return b.String()
	}
	check := func(as []asset, sumsText string, extra ...string) (string, error) {
		t.Helper()
		dir := filepath.Join(t.TempDir(), "dist")
		if err := os.Mkdir(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		for _, a := range as {
			if err := os.WriteFile(filepath.Join(dir, a.name), a.data, 0o644); err != nil {
				t.Fatal(err)
			}
		}
		for _, name := range extra {
			if err := os.WriteFile(filepath.Join(dir, name), []byte("x\n"), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		if err := os.WriteFile(filepath.Join(dir, "SHA256SUMS"), []byte(sumsText), 0o644); err != nil {
			t.Fatal(err)
		}
		out, errout, err := run(t, root, "sh", "scripts/check-release.sh", "--assets", tag, dir)
		return out + errout, err
	}

	// The passing case; SHA256SUMS holds the hashes of the repository's two scripts.
	good := assets("", nil)
	text := sums(good)
	for _, name := range []string{"install.sh", "install.ps1"} {
		if !strings.Contains(text, sumsLine(name, script(name))) {
			t.Fatalf("SHA256SUMS lacks the line for scripts/%s", name)
		}
	}
	if out, err := check(good, text); err != nil || !strings.Contains(out, "release assets passed for "+tag) {
		t.Fatalf("ten good assets: want a pass, got %v %q", err, out)
	}

	appimage := "re-psn-" + tag + "-x86_64.AppImage"
	otherSh := map[string][]byte{"install.sh": []byte("#!/bin/sh\necho other\n")}
	otherPs1 := map[string][]byte{"install.ps1": []byte("Write-Output other\n")}
	ps1Line := sumsLine("install.ps1", script("install.ps1"))
	for _, c := range []struct {
		what, want string
		as         []asset
		sums       string
		extra      []string
	}{
		{"install.ps1 missing", "not the ten assets", assets("install.ps1", nil), sums(assets("install.ps1", nil)), nil},
		{"install.sh missing", "not the ten assets", assets("install.sh", nil), sums(assets("install.sh", nil)), nil},
		{"an app archive missing", "not the ten assets", assets("re-psn-app-"+tag+"-windows-amd64.zip", nil),
			sums(assets("re-psn-app-"+tag+"-windows-amd64.zip", nil)), nil},
		{"the AppImage missing", "not the ten assets", assets(appimage, nil), sums(assets(appimage, nil)), nil},
		{"an extra file", "not the ten assets", good, text, []string{"notes.md"}},
		{"an extra hidden file", "not the ten assets", good, text, []string{".x"}},
		{"install.sh not the repository's", "install.sh differs from scripts/install.sh",
			assets("", otherSh), sums(assets("", otherSh)), nil},
		{"install.ps1 not the repository's", "install.ps1 differs from scripts/install.ps1",
			assets("", otherPs1), sums(assets("", otherPs1)), nil},
		{"no line for install.sh", "does not list", good, sums(assets("install.sh", nil)), nil},
		{"a line twice", "does not list", good, text + ps1Line, nil},
		{"a wrong hash", "does not verify", good, strings.Replace(text, ps1Line, "00000000"+ps1Line[8:], 1), nil},
		{"CRLF line ends", "is not 'HASH  NAME'", good, strings.ReplaceAll(text, "\n", "\r\n"), nil},
		{"a BOM", "is not 'HASH  NAME'", good, "\xef\xbb\xbf" + text, nil},
	} {
		if out, err := check(c.as, c.sums, c.extra...); err == nil || !strings.Contains(out, c.want) {
			t.Errorf("%s: want a failure saying %q, got %v %q", c.what, c.want, err, out)
		}
	}
	if out, err := check(good, ""); err == nil || !strings.Contains(out, "does not list") {
		t.Errorf("empty SHA256SUMS: want a failure, got %v %q", err, out)
	}

	for _, args := range [][]string{{"--assets"}, {"--assets", tag}, {"--assets", tag, ""}, {"--assets", tag, ".", "x"}} {
		if _, errout, err := run(t, root, "sh", append([]string{"scripts/check-release.sh"}, args...)...); err == nil ||
			!strings.Contains(errout, "usage:") {
			t.Errorf("%q: want a usage error, got %v %q", args, err, errout)
		}
	}
	for bad, want := range map[string]string{"0.3.1": "is not vMAJOR.MINOR.PATCH", "v0.3.1;x": "odd characters"} {
		if _, errout, err := run(t, root, "sh", "scripts/check-release.sh", "--assets", bad, root); err == nil ||
			!strings.Contains(errout, want) {
			t.Errorf("tag %q: want %q, got %v %q", bad, want, err, errout)
		}
	}
}

// The ten asset names are the same in scripts/check-release.sh, the draft upload in .github/workflows/release.yml and
// the expected list in .github/workflows/release-check.yml.
func TestReleaseAssetListsAgree(t *testing.T) {
	read := func(rel string) string {
		b, err := os.ReadFile(filepath.Join("..", filepath.FromSlash(rel)))
		if err != nil {
			t.Fatal(err)
		}
		return strings.ReplaceAll(string(b), "\r\n", "\n")
	}
	const tag = "vX.Y.Z"
	want := strings.Join(releaseAssets(tag), " ")
	list := func(names []string) string {
		sort.Strings(names)
		return strings.Join(names, " ")
	}
	wantLine := func(file, variable string) string {
		t.Helper()
		m := regexp.MustCompile(`(?m)^\s*want="(re-psn-\$`+variable+`[^"]*)"$`).FindAllStringSubmatch(read(file), -1)
		if len(m) != 1 {
			t.Fatalf("%s: want one want=\"...\" asset list, got %d", file, len(m))
		}
		return list(strings.Fields(strings.ReplaceAll(m[0][1], "$"+variable, tag)))
	}
	if got := wantLine("scripts/check-release.sh", "tag"); got != want {
		t.Errorf("scripts/check-release.sh: %s, want %s", got, want)
	}
	if got := wantLine(".github/workflows/release-check.yml", "TAG"); got != want {
		t.Errorf("release-check.yml: %s, want %s", got, want)
	}
	workflow := read(".github/workflows/release.yml")
	i := strings.Index(workflow, "gh release create")
	if i < 0 {
		t.Fatal("release.yml: no gh release create")
	}
	var uploaded []string
	for _, m := range regexp.MustCompile(`(?m)^\s*dist/(\S+?)(?: \\)?$`).FindAllStringSubmatch(workflow[i:], -1) {
		uploaded = append(uploaded, strings.ReplaceAll(m[1], `"$TAG"`, tag))
	}
	if got := list(uploaded); got != want {
		t.Errorf("release.yml uploads %s, want %s", got, want)
	}
}

// Exercise the actual release-check shell steps with local metadata and source-file responses.
// Existing five-file releases remain checkable; newer tags need both installers and matching tagged bytes.
func TestReleaseCheckWorkflow(t *testing.T) {
	needShell(t, "bash", "jq", "sort", "tr", "sed", "cmp")
	b, err := os.ReadFile(filepath.Join("..", ".github", "workflows", "release-check.yml"))
	if err != nil {
		t.Fatal(err)
	}
	workflow := strings.ReplaceAll(string(b), "\r\n", "\n")
	step := func(name string) (string, string) {
		t.Helper()
		_, block, ok := strings.Cut(workflow, "      - name: "+name+"\n")
		if !ok {
			t.Fatalf("missing workflow step %q", name)
		}
		block, _, _ = strings.Cut(block, "\n      - ")
		header, body, ok := strings.Cut(block, "        run: |\n")
		if !ok {
			t.Fatalf("missing shell block in %q", name)
		}
		var lines []string
		for _, line := range strings.Split(body, "\n") {
			if strings.TrimSpace(line) != "" && !strings.HasPrefix(line, "          ") {
				t.Fatalf("unexpected shell indentation in %q", name)
			}
			lines = append(lines, strings.TrimPrefix(line, "          "))
		}
		return header, strings.Join(lines, "\n")
	}
	_, metadata := step("Release metadata")
	guard, compare := step("The install scripts are the tag's")
	if !strings.Contains(guard, "if: env.INSTALLER_ASSETS == 'true'") {
		t.Fatal("installer comparison must be conditional on the metadata result")
	}
	for _, c := range []struct {
		name, tag, drop, tamper        string
		historical, extra, five, seven bool
		wantMetadata, wantCmp          bool
	}{
		{name: "old v0.2.0", tag: "v0.2.0", historical: true, wantMetadata: true},
		{name: "old v0.2.0 with the v0.3.0 list", tag: "v0.2.0", historical: true, five: true},
		{name: "old v0.3.0", tag: "v0.3.0", historical: true, wantMetadata: true},
		{name: "old unexpected installers", tag: "v0.3.0", wantMetadata: false},
		{name: "old v0.4.0 seven assets", tag: "v0.4.0", seven: true, wantMetadata: true, wantCmp: true},
		{name: "v0.4.0 with the app archives", tag: "v0.4.0"},
		{name: "new ten assets", tag: "v0.3.1", wantMetadata: true, wantCmp: true},
		{name: "new missing app archive", tag: "v0.3.1", drop: "re-psn-app-v0.3.1-windows-amd64.zip"},
		{name: "new missing sh", tag: "v0.3.1", drop: "install.sh"},
		{name: "new missing ps1", tag: "v0.3.1", drop: "install.ps1"},
		{name: "new extra asset", tag: "v0.3.1", extra: true},
		{name: "sh differs from tag", tag: "v0.3.1", tamper: "install.sh", wantMetadata: true},
		{name: "ps1 differs from tag", tag: "v0.3.1", tamper: "install.ps1", wantMetadata: true},
	} {
		t.Run(c.name, func(t *testing.T) {
			root := t.TempDir()
			if err := os.Mkdir(filepath.Join(root, "assets"), 0o755); err != nil {
				t.Fatal(err)
			}
			write := func(name, data string) {
				t.Helper()
				if err := os.WriteFile(filepath.Join(root, name), []byte(data), 0o755); err != nil {
					t.Fatal(err)
				}
			}
			var assets []map[string]string
			names := releaseAssets(c.tag)
			if c.tag == "v0.2.0" && !c.five {
				// v0.2.0 shipped each download with its own .sha256 file.
				names = []string{"SHA256SUMS"}
				for _, base := range []string{"linux-amd64.tar.gz", "linux-arm64.tar.gz", "windows-amd64.zip", "x86_64.AppImage"} {
					name := "re-psn-v0.2.0-" + base
					names = append(names, name, name+".sha256")
				}
			}
			for _, name := range names {
				if name == c.drop || ((c.seven || c.historical) && strings.HasPrefix(name, "re-psn-app-")) || (c.historical && (name == "install.sh" || name == "install.ps1")) {
					continue
				}
				assets = append(assets, map[string]string{"name": name})
			}
			if c.extra {
				assets = append(assets, map[string]string{"name": "extra.txt"})
			}
			data, err := json.Marshal(map[string]any{"draft": false, "name": c.tag,
				"tag_name": c.tag, "prerelease": true, "assets": assets})
			if err != nil {
				t.Fatal(err)
			}
			write("fixture.json", string(data))
			for _, name := range []string{"install.sh", "install.ps1"} {
				write("source-"+name, "tagged "+name+"\n")
				text := "tagged " + name + "\n"
				if name == c.tamper {
					text = "different\n"
				}
				write("assets/"+name, text)
			}
			write("gh", `#!/bin/sh
set -eu
case "$*" in
  "api repos/example/re-psn/releases/tags/$TAG") cat "$RUNNER_TEMP/fixture.json" ;;
  "api -H Accept: application/vnd.github.raw+json repos/example/re-psn/contents/scripts/install.sh?ref=$TAG") cat "$RUNNER_TEMP/source-install.sh" ;;
  "api -H Accept: application/vnd.github.raw+json repos/example/re-psn/contents/scripts/install.ps1?ref=$TAG") cat "$RUNNER_TEMP/source-install.ps1" ;;
  *) echo 'unexpected API call' >&2; exit 1 ;;
esac
`)
			env := append(append([]string{}, gitEnv...), "PATH="+root+string(os.PathListSeparator)+os.Getenv("PATH"),
				"TAG="+c.tag, "GITHUB_REPOSITORY=example/re-psn", "RUNNER_TEMP="+root,
				"GITHUB_ENV="+filepath.Join(root, "env"))
			runStep := func(script string) (string, error) {
				cmd := exec.Command("bash", "-e", "-o", "pipefail", "-c", script)
				cmd.Env = env
				out, err := cmd.CombinedOutput()
				return string(out), err
			}
			if out, err := runStep(metadata); (err == nil) != c.wantMetadata {
				t.Fatalf("metadata: want success=%v, got %v: %s", c.wantMetadata, err, out)
			}
			if !c.wantMetadata {
				return
			}
			wantEnv := "INSTALLER_ASSETS=true\n"
			if c.historical {
				wantEnv = "INSTALLER_ASSETS=false\n"
			}
			if got := readFile(t, root, "env"); got != wantEnv {
				t.Fatalf("environment %q, want %q", got, wantEnv)
			}
			if !c.historical {
				if out, err := runStep(compare); (err == nil) != c.wantCmp {
					t.Fatalf("tagged scripts: want success=%v, got %v: %s", c.wantCmp, err, out)
				}
			}
		})
	}
}
