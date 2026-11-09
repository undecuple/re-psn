package tui

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// TestPaletteMatchesTheWebsite: the hex values are the ones in docs/art/palette.json, which the website uses too.
func TestPaletteMatchesTheWebsite(t *testing.T) {
	b, err := os.ReadFile(filepath.Join("..", "..", "..", "docs", "art", "palette.json"))
	if err != nil {
		t.Fatal("docs/art/palette.json must be in the checkout:", err)
	}
	var doc struct {
		Modes map[string]map[string]string `json:"modes"`
	}
	if err := json.Unmarshal(b, &doc); err != nil {
		t.Fatal(err)
	}
	for mode, p := range map[string]palette{"dark": darkPalette, "light": lightPalette} {
		want := doc.Modes[mode]
		got := map[string]string{
			"bg0": p.bg0, "bg1": p.bg1, "bg3": p.bg3, "fg0": p.fg0, "fg1": p.fg1, "grey1": p.grey1, "grey2": p.grey2,
			"red": p.red, "orange": p.orange, "yellow": p.yellow, "green": p.green, "aqua": p.aqua, "blue": p.blue,
		}
		for k, v := range got {
			if want[k] != v {
				t.Errorf("%s %s = %s, palette.json has %q", mode, k, v, want[k])
			}
		}
	}
}
