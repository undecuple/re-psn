package main

// A small table of known games, so the status page can show "[Game Title (serial)]" instead of a bare
// communication ID. The table is embedded, read once at start and never fetched from anywhere. A communication ID
// that is not in it is shown as sent by the client.

import (
	_ "embed"
	"strings"
)

//go:embed titles.tsv
var titlesRaw string

type gameInfo struct {
	name    string
	serials []string
}

var knownGames = parseGames(titlesRaw)

// parseGames reads comm_id<TAB>name<TAB>serial, serial... lines; blank and # lines are skipped, and so is a line
// without both an ID and a name.
func parseGames(src string) map[string]gameInfo {
	games := map[string]gameInfo{}
	for _, line := range strings.Split(strings.ReplaceAll(src, "\r\n", "\n"), "\n") {
		if line = strings.TrimSpace(line); line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		f := strings.Split(line, "\t")
		if len(f) < 2 || strings.TrimSpace(f[0]) == "" || strings.TrimSpace(f[1]) == "" {
			continue
		}
		g := gameInfo{name: strings.TrimSpace(f[1])}
		if len(f) > 2 {
			for _, s := range strings.Split(f[2], ",") {
				if s = strings.TrimSpace(s); s != "" {
					g.serials = append(g.serials, s)
				}
			}
		}
		games[strings.TrimSpace(f[0])] = g
	}
	return games
}

// titleLabel returns the display label for a communication ID: "[Name (SERIAL, SERIAL)]" for a known game,
// otherwise the ID itself, plus the name and serials for the JSON fields (empty when unknown).
func titleLabel(commID string) (label, name string, serials []string) {
	g, ok := knownGames[commID]
	if !ok {
		return commID, "", nil
	}
	label = "[" + g.name
	if len(g.serials) > 0 {
		label += " (" + strings.Join(g.serials, ", ") + ")"
	}
	return label + "]", g.name, g.serials
}
