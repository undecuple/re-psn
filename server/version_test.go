package main

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

var (
	semverRe     = regexp.MustCompile(`^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$`)
	versionVarRe = regexp.MustCompile(`(?m)^var version = "([^"]*)"\r?$`)
	changelogRe  = regexp.MustCompile(`(?m)^## \[([0-9]+\.[0-9]+\.[0-9]+)\]`)
	spanRe       = regexp.MustCompile(`<!-- version -->(.*?)<!-- /version -->`)
	anyVersionRe = regexp.MustCompile(`[0-9]+\.[0-9]+\.[0-9]+`)
)

// versionsAgree checks the places that state the version under root: the VERSION file, the default of `var version` in
// server/main.go and app/cmd/re-psn-app/main.go (release builds override it with -ldflags; the source default is what is compared), the newest numbered
// heading in CHANGELOG.md, and every X.Y.Z inside a <!-- version --> span in README.md. scripts/bump-version.sh changes
// exactly these.
func versionsAgree(root string) error {
	read := func(rel string) (string, error) {
		b, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel)))
		return string(b), err
	}
	raw, err := read("VERSION")
	if err != nil {
		return err
	}
	want := strings.TrimRight(raw, "\r\n")
	if !semverRe.MatchString(want) || strings.TrimSpace(raw) != want {
		return fmt.Errorf("VERSION holds %q, want one X.Y.Z line", raw)
	}

	for _, rel := range []string{"server/main.go", "app/cmd/re-psn-app/main.go"} {
		src, err := read(rel)
		if err != nil {
			return err
		}
		m := versionVarRe.FindAllStringSubmatch(src, -1)
		if len(m) != 1 {
			return fmt.Errorf("%s: want one plain `var version = \"X.Y.Z\"` line, found %d", rel, len(m))
		}
		if m[0][1] != want {
			return fmt.Errorf("%s says %s, VERSION says %s", rel, m[0][1], want)
		}
	}

	cl, err := read("CHANGELOG.md")
	if err != nil {
		return err
	}
	h := changelogRe.FindStringSubmatch(cl)
	if h == nil {
		return fmt.Errorf("CHANGELOG.md has no numbered version heading")
	}
	if h[1] != want {
		return fmt.Errorf("the newest CHANGELOG.md heading is %s, VERSION says %s", h[1], want)
	}

	readme, err := read("README.md")
	if err != nil {
		return err
	}
	spans := spanRe.FindAllStringSubmatch(readme, -1)
	if len(spans) == 0 {
		return fmt.Errorf("README.md has no <!-- version --> span")
	}
	for _, s := range spans {
		vs := anyVersionRe.FindAllString(s[1], -1)
		if len(vs) == 0 {
			return fmt.Errorf("README.md: the span %q names no version", s[0])
		}
		for _, v := range vs {
			if v != want {
				return fmt.Errorf("README.md: the span %q says %s, VERSION says %s", s[0], v, want)
			}
		}
	}
	return nil
}

// TestVersionAgrees fails when VERSION, server/main.go, CHANGELOG.md and the README version spans disagree.
func TestVersionAgrees(t *testing.T) {
	if err := versionsAgree(".."); err != nil {
		t.Fatal(err)
	}
}
