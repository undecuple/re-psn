package tui

import (
	"image/color"

	"charm.land/lipgloss/v2"
)

// palette holds the tokens the website uses (dark: the undecuple page; light: gruvbox-material) (docs/art/palette.json, site/style.css).
type palette struct {
	bg0, bg1, bg3, fg0, fg1, grey1, grey2, red, orange, yellow, green, aqua, blue string
}

var (
	darkPalette = palette{
		bg0: "#101113", bg1: "#161719", bg3: "#2b2733", fg0: "#9f9e99", fg1: "#e1ffe5", grey1: "#787487", grey2: "#7a837c",
		red: "#ac7f7b", orange: "#997e95", yellow: "#997e95", green: "#a7d8b0", aqua: "#a7d8b0", blue: "#8288a0",
	}
	lightPalette = palette{
		bg0: "#fbf1c7", bg1: "#f4e8be", bg3: "#eee0b7", fg0: "#654735", fg1: "#4f3829", grey1: "#928374", grey2: "#7c6f64",
		red: "#c14a4a", orange: "#c35e0a", yellow: "#b47109", green: "#6c782e", aqua: "#4c7a5d", blue: "#45707a",
	}
)

// styles are the named slots the screens use. As on the website, the light mode keeps every text colour readable on
// its pale background (4.5:1 or more): accents that are too faint as text there fall back to the strong text colour,
// in bold. No state is shown by colour alone; the words say it.
type styles struct {
	bg, fg              color.Color
	text, strong, muted lipgloss.Style
	heading, key, id    lipgloss.Style
	good, warn, bad     lipgloss.Style
	selected, tabOn     lipgloss.Style
	tabOff              lipgloss.Style
	frame, frameFocus   color.Color
}

func newStyles(light, noColor bool) styles {
	if noColor {
		plain := lipgloss.NewStyle()
		bold := plain.Bold(true)
		return styles{
			text: plain, strong: bold, muted: plain, heading: bold, key: bold, id: plain,
			good: plain, warn: bold, bad: bold, selected: plain.Reverse(true), tabOn: bold.Reverse(true), tabOff: plain,
		}
	}
	p := darkPalette
	if light {
		p = lightPalette
	}
	fg := func(hex string) lipgloss.Style { return lipgloss.NewStyle().Foreground(lipgloss.Color(hex)) }
	s := styles{
		bg: lipgloss.Color(p.bg0), fg: lipgloss.Color(p.fg0),
		text: fg(p.fg0), strong: fg(p.fg1).Bold(true), muted: fg(p.grey2),
		heading: fg(p.yellow).Bold(true), key: fg(p.orange).Bold(true), id: fg(p.aqua),
		good: fg(p.green), warn: fg(p.yellow), bad: fg(p.red).Bold(true),
		selected: fg(p.fg1).Background(lipgloss.Color(p.bg3)).Bold(true),
		tabOn:    fg(p.bg0).Background(lipgloss.Color(p.aqua)).Bold(true),
		tabOff:   fg(p.grey2),
		frame:    lipgloss.Color(p.grey1), frameFocus: lipgloss.Color(p.aqua),
	}
	if light {
		s.muted = fg(p.fg0)
		s.heading, s.key, s.id = fg(p.fg1).Bold(true), fg(p.fg1).Bold(true), fg(p.blue)
		s.good, s.warn, s.bad = fg(p.fg1).Bold(true), fg(p.fg1).Bold(true), fg(p.fg1).Bold(true)
		s.tabOn = fg(p.bg0).Background(lipgloss.Color(p.blue)).Bold(true)
		s.tabOff = fg(p.fg0)
		s.frameFocus = lipgloss.Color(p.blue)
	}
	return s
}
