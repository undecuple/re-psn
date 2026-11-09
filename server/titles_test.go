package main

import (
	"encoding/json"
	"regexp"
	"strings"
	"testing"
)

func TestTitleTableRows(t *testing.T) {
	id := regexp.MustCompile(`^NPWR\d{5}_00$`)
	serial := regexp.MustCompile(`^[A-Z]{4}\d{5}$`)
	if len(knownGames) == 0 {
		t.Fatal("no known games embedded")
	}
	seen := map[string]bool{}
	for _, line := range strings.Split(titlesRaw, "\n") {
		if line = strings.TrimSpace(line); line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		f := strings.Split(line, "\t")
		if len(f) != 3 || strings.TrimSpace(f[1]) == "" {
			t.Errorf("row %q: want id, name and serials separated by tabs", line)
			continue
		}
		if !id.MatchString(f[0]) || seen[f[0]] {
			t.Errorf("row %q: communication ID malformed or repeated", line)
		}
		seen[f[0]] = true
		for _, s := range strings.Split(f[2], ",") {
			if !serial.MatchString(strings.TrimSpace(s)) {
				t.Errorf("row %q: serial %q malformed", line, s)
			}
		}
	}
	if len(seen) != len(knownGames) {
		t.Errorf("%d rows parsed as %d games", len(seen), len(knownGames))
	}
}

func TestParseGamesSkipsBadLines(t *testing.T) {
	got := parseGames("# comment\n\nNPWR00001_00\tOne\tAAAA00001, BBBB00002\r\nNPWR00002_00\n\tNoID\nNPWR00003_00\t\tAAAA00003\nNPWR00004_00\tFour\n")
	if len(got) != 2 {
		t.Fatalf("games = %v", got)
	}
	if g := got["NPWR00001_00"]; g.name != "One" || strings.Join(g.serials, "|") != "AAAA00001|BBBB00002" {
		t.Errorf("row one = %+v", g)
	}
	if g := got["NPWR00004_00"]; g.name != "Four" || len(g.serials) != 0 {
		t.Errorf("row four = %+v", g)
	}
}

func TestTitleLabel(t *testing.T) {
	label, name, serials := titleLabel("NPWR08138_00")
	if label != "[J-STARS Victory VS+ (BLUS31519, BLES02136)]" || name != "J-STARS Victory VS+" || len(serials) != 2 {
		t.Errorf("known: %q %q %v", label, name, serials)
	}
	// An unknown ID is shown as the client sent it, with no name or serials.
	label, name, serials = titleLabel("NPWR99999_00")
	if label != "NPWR99999_00" || name != "" || serials != nil {
		t.Errorf("unknown: %q %q %v", label, name, serials)
	}
}

func TestStatusShowsGameLabelsSorted(t *testing.T) {
	s, addr, _ := logged(t)
	a := dial(t, addr, "NPWR08138_00", "alice", 3658)
	b := dial(t, addr, "NPWR99999_00", "bob", 3659)
	c := dial(t, addr, "NPWR01569_00", "carol", 3660)
	roomOf(t, a)
	roomOf(t, b)
	roomOf(t, c)

	st := s.snapshot()
	var labels []string
	for _, r := range st.Rooms {
		labels = append(labels, r.Label)
	}
	// Known games come first, ordered by label, then bare communication IDs.
	want := []string{"[J-STARS Victory VS+ (BLUS31519, BLES02136)]", "[Killzone 3 (BCUS98234, BCES01007)]", "NPWR99999_00"}
	if strings.Join(labels, "|") != strings.Join(want, "|") {
		t.Errorf("labels = %q, want %q", labels, want)
	}
	for _, r := range st.Rooms {
		if r.Title == "NPWR99999_00" && (r.Game != "" || r.Serials != nil) {
			t.Errorf("unknown game has name or serials: %+v", r)
		}
	}
	j, _ := json.Marshal(st)
	if !strings.Contains(string(j), `"label":"[Killzone 3 (BCUS98234, BCES01007)]"`) || !strings.Contains(string(j), `"title":"NPWR01569_00"`) {
		t.Errorf("/api/stats keeps title and adds label: %s", j)
	}
	var sb strings.Builder
	if err := page.Execute(&sb, st); err != nil || !strings.Contains(sb.String(), "[Killzone 3 (BCUS98234, BCES01007)]") {
		t.Errorf("status page lacks the label: err %v", err)
	}
}
