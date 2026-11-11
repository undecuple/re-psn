package tui

import (
	"context"
	"errors"
	"net"
	"net/netip"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode"
	"unicode/utf8"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/undecuple/re-psn/app/internal/client"
	"github.com/undecuple/re-psn/app/internal/config"
)

// fake is a Fetcher that answers from memory and records the queries.
type fake struct {
	mu      sync.Mutex
	queries []Query
	snap    Snapshot
	err     error
}

func (f *fake) Fetch(ctx context.Context, q Query) (Snapshot, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, ok := ctx.Deadline(); !ok {
		return Snapshot{}, errors.New("fetch without a deadline")
	}
	f.queries = append(f.queries, q)
	return f.snap, f.err
}

func (f *fake) last() Query {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.queries) == 0 {
		return Query{}
	}
	return f.queries[len(f.queries)-1]
}

var sampleSnap = Snapshot{
	Ack:  client.HelloAck{PublicIP: netip.MustParseAddr("127.0.0.1"), Flags: client.FlagRelay},
	Name: "view-0123456789",
	RTT:  1500 * time.Microsecond,
	Rooms: client.RoomList{Total: 2, Rooms: []client.RoomInfo{
		{ID: 1, Owner: "alice_01", Cur: 1, Max: 4, Flags: 0x10},
		{ID: 2, Owner: "bob", Cur: 2, Max: 2},
	}},
	Ranking: client.Ranking{Total: 1, Rows: []client.RankRow{{Rank: 1, OnlineID: "carol", Score: 900, Recorded: 1762300000}}},
}

// press makes a key press: a named key ("enter", "esc", "tab", "backspace", "ctrl+u") or one printable character.
func press(k string) tea.KeyPressMsg {
	switch k {
	case "enter":
		return tea.KeyPressMsg{Code: tea.KeyEnter}
	case "esc":
		return tea.KeyPressMsg{Code: tea.KeyEscape}
	case "tab":
		return tea.KeyPressMsg{Code: tea.KeyTab}
	case "backspace":
		return tea.KeyPressMsg{Code: tea.KeyBackspace}
	case "up":
		return tea.KeyPressMsg{Code: tea.KeyUp}
	case "down":
		return tea.KeyPressMsg{Code: tea.KeyDown}
	case "ctrl+u":
		return tea.KeyPressMsg{Code: 'u', Mod: tea.ModCtrl}
	case "ctrl+f":
		return tea.KeyPressMsg{Code: 'f', Mod: tea.ModCtrl}
	case "ctrl+c":
		return tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl}
	}
	r := []rune(k)[0]
	return tea.KeyPressMsg{Code: r, Text: k}
}

// drive sends messages and runs every command they return, feeding the results back, as the program would.
func drive(t *testing.T, m Model, msgs ...tea.Msg) Model {
	t.Helper()
	for _, msg := range msgs {
		next, cmd := m.Update(msg)
		m = next.(Model)
		for cmd != nil {
			out := cmd()
			if _, ok := out.(tea.QuitMsg); ok || out == nil {
				break
			}
			next, cmd = m.Update(out)
			m = next.(Model)
		}
	}
	return m
}

func keys(ks ...string) []tea.Msg {
	var out []tea.Msg
	for _, k := range ks {
		out = append(out, press(k))
	}
	return out
}

func typed(s string) []tea.Msg {
	var out []tea.Msg
	for _, r := range s {
		out = append(out, press(string(r)))
	}
	return out
}

func seq(parts ...[]tea.Msg) []tea.Msg {
	var out []tea.Msg
	for _, p := range parts {
		out = append(out, p...)
	}
	return out
}

var escapes = regexp.MustCompile(`\x1b\[[0-9;:?]*[ -/]*[@-~]|\x1b\][^\x07\x1b]*(\x07|\x1b\\)`)

func screen(m Model) string { return escapes.ReplaceAllString(m.View().Content, "") }

var boxRuns = regexp.MustCompile(`[│╭╮╰╯─\s]+`)

// flat is the screen as one line: frames dropped and every run of space joined, so wrapped text still matches.
func flat(m Model) string { return boxRuns.ReplaceAllString(screen(m), " ") }

func mustShow(t *testing.T, m Model, want ...string) {
	t.Helper()
	s := flat(m)
	for _, w := range want {
		if !strings.Contains(s, w) {
			t.Errorf("screen lacks %q:\n%s", w, screen(m))
		}
	}
}

func mustNotShow(t *testing.T, m Model, not ...string) {
	t.Helper()
	s := flat(m)
	for _, w := range not {
		if strings.Contains(s, w) {
			t.Errorf("screen shows %q:\n%s", w, screen(m))
		}
	}
}

func homeModel(t *testing.T, f *fake, set config.Settings) Model {
	t.Helper()
	path := filepath.Join(t.TempDir(), config.FileName)
	return New(Options{Path: path, File: &config.File{Settings: set}, Fetcher: f, Now: ticking()})
}

func TestFirstRunChooser(t *testing.T) {
	path := filepath.Join(t.TempDir(), "re-psn", config.FileName)
	f := &fake{snap: sampleSnap}
	m := New(Options{Path: path, FirstRun: true, Fetcher: f})
	mustShow(t, m, "What is this app for", "> Play", "Host", "Both")

	m = drive(t, m, keys("down", "down", "enter")...)
	if m.stage != stageName || m.set.Mode != config.ModeBoth {
		t.Fatalf("after choosing Both: stage %d, mode %q", m.stage, m.set.Mode)
	}
	m = drive(t, m, seq(typed("ab"), keys("enter"))...)
	mustShow(t, m, "Not accepted", "3-16 characters")
	m = drive(t, m, seq(keys("ctrl+u"), typed("alice q"), keys("enter"))...) // q is text here, not quit
	mustShow(t, m, "only letters, digits")
	if m.quitting {
		t.Fatal("q in a text field quit the app")
	}
	m = drive(t, m, seq(keys("ctrl+u"), typed("alice_01"), keys("enter"))...)
	if m.stage != stageServer {
		t.Fatalf("stage %d after a good name", m.stage)
	}
	m = drive(t, m, seq(typed("example.org"), keys("enter"))...)
	mustShow(t, m, "want host:port")
	m = drive(t, m, seq(keys("ctrl+u"), typed("127.0.0.1:36100"), keys("enter"))...)
	if m.stage != stageHome {
		t.Fatalf("stage %d after the server; problem %q", m.stage, m.problem)
	}
	if q := f.last(); q.Server != "127.0.0.1:36100" {
		t.Fatalf("no check after the first run: %+v", q)
	}
	got, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	want := config.Settings{Mode: config.ModeBoth, Name: "alice_01", Server: "127.0.0.1:36100"}
	if got.Settings != want {
		t.Fatalf("saved %+v, want %+v", got.Settings, want)
	}
	if st, err := os.Stat(path); err != nil || runtime.GOOS != "windows" && st.Mode().Perm() != 0o600 {
		t.Fatalf("settings file: %v %v, want mode 0600", st.Mode(), err)
	}
	mustShow(t, m, "Connected", "Overview")
}

func TestFirstRunHostSkipsNameAndGoesBack(t *testing.T) {
	m := New(Options{Path: "x.ini", FirstRun: true, Fetcher: &fake{}, Save: func(string, *config.File) error { return nil }})
	m = drive(t, m, keys("down", "enter")...)
	if m.stage != stageServer || m.set.Mode != config.ModeHost {
		t.Fatalf("Host: stage %d mode %q, want the server step", m.stage, m.set.Mode)
	}
	m = drive(t, m, keys("backspace")...)
	if m.stage != stageMode {
		t.Fatalf("backspace on an empty field: stage %d, want the mode step", m.stage)
	}
	m = drive(t, m, keys("up", "enter", "backspace")...) // Play, then back
	if m.stage != stageMode || m.choice != 0 {
		t.Fatalf("stage %d choice %d", m.stage, m.choice)
	}
}

func TestFirstRunSaveError(t *testing.T) {
	m := New(Options{Path: "x.ini", FirstRun: true, Fetcher: &fake{}, Save: func(string, *config.File) error { return errors.New("disk full") }})
	m = drive(t, m, seq(keys("down", "enter"), typed("h:1"), keys("enter"))...)
	if m.stage != stageServer {
		t.Fatalf("stage %d after a failed save, want to stay", m.stage)
	}
	mustShow(t, m, "could not save: disk full")
}

func TestQuitKeys(t *testing.T) {
	for _, c := range []struct {
		name  string
		first bool
		keys  []string
	}{
		{"esc on the chooser", true, []string{"esc"}},
		{"q on the chooser", true, []string{"q"}},
		{"esc in the name field", true, []string{"enter", "esc"}},
		{"q at home", false, []string{"q"}},
		{"ctrl+c while editing", false, []string{"4", "enter", "down", "enter", "ctrl+c"}},
	} {
		t.Run(c.name, func(t *testing.T) {
			m := New(Options{Path: "x.ini", FirstRun: c.first, Fetcher: &fake{}})
			var cmd tea.Cmd
			for _, k := range c.keys {
				var next tea.Model
				next, cmd = m.Update(press(k))
				m = next.(Model)
			}
			if !m.quitting || cmd == nil {
				t.Fatal("did not quit")
			}
			if _, ok := cmd().(tea.QuitMsg); !ok {
				t.Fatal("the command is not tea.Quit")
			}
			if screen(m) != "" {
				t.Fatal("the screen is not cleared on quit")
			}
		})
	}
}

func TestStatusTabs(t *testing.T) {
	f := &fake{snap: sampleSnap}
	m := homeModel(t, f, config.Settings{Mode: config.ModePlay, Name: "alice_01", Server: "127.0.0.1:36100"})
	m = drive(t, m, tea.WindowSizeMsg{Width: 120, Height: 30})
	m = drive(t, m, keys("2", "enter")...) // Rooms: no title yet, so it asks for one
	if m.menu != paneRooms || m.editing != editTitle {
		t.Fatalf("menu %d editing %d, want Rooms and the title field", m.menu, m.editing)
	}
	m = drive(t, m, seq(typed("NPWR00860_00"), keys("enter"))...)
	if q := f.last(); q.Title != "NPWR00860_00" || q.Boards {
		t.Fatalf("query %+v", q)
	}
	mustShow(t, m, "[Rooms]", "alice_01", "1/4", "0x00000010", "bob", "2/2", "2 of 2 open rooms", "Age (n/a) is not available over the protocol",
		"Uptime, connections and server version: not available over the protocol")

	m = drive(t, m, keys("tab")...)
	if m.menu != paneBoards {
		t.Fatalf("tab: menu %d, want Leaderboards", m.menu)
	}
	mustShow(t, m, "[Leaderboards]", "The protocol cannot list a title's boards")
	mustNotShow(t, m, "alice_01 1/4")
	m = drive(t, m, seq(keys("b"), typed("7x"), keys("enter"))...)
	mustShow(t, m, "want a number")
	m = drive(t, m, seq(keys("backspace", "enter"))...)
	if q := f.last(); !q.Boards || q.Board != 7 {
		t.Fatalf("query %+v, want board 7", q)
	}
	mustShow(t, m, "carol", "900", "Top 1 of 1 players on board 7")

	m = drive(t, m, keys("tab")...)
	if m.menu != paneRooms {
		t.Fatalf("tab back: menu %d", m.menu)
	}
	n := len(f.queries)
	m = drive(t, m, keys("r")...)
	if len(f.queries) != n+1 {
		t.Fatal("r did not refresh")
	}
	m = drive(t, m, keys("esc")...)
	if m.content {
		t.Fatal("esc did not go back to the menu")
	}
}

func TestRankingErrorKeepsRooms(t *testing.T) {
	s := sampleSnap
	s.RankErr = &client.ServerError{Op: "get ranking of board 3", Code: client.ErrBadRequest}
	f := &fake{snap: s}
	m := homeModel(t, f, config.Settings{Server: "h:1"})
	m.title, m.board = "T", "3"
	m = drive(t, m, keys("3", "enter", "r")...)
	mustShow(t, m, "Board 3:", "bad request")
	m = drive(t, m, keys("tab")...)
	mustShow(t, m, "alice_01")
}

func TestConnectionErrorsAreText(t *testing.T) {
	f := &fake{err: errors.New("dial 127.0.0.1:1: connection refused")}
	m := homeModel(t, f, config.Settings{Server: "127.0.0.1:1"})
	m = drive(t, m, keys("r")...)
	mustShow(t, m, "No answer from 127.0.0.1:1", "connection refused", "Check the server in Settings")
	m = drive(t, m, keys("2", "enter")...)
	m = drive(t, m, seq(typed("T"), keys("enter"))...)
	mustShow(t, m, "connection refused", "No room list yet")

	m = homeModel(t, f, config.Settings{})
	m = drive(t, m, keys("r")...)
	mustShow(t, m, "no server set")
	if len(f.queries) != 2 {
		t.Fatalf("%d queries; a model without a server must not fetch", len(f.queries))
	}
}

func TestStaleAnswerIsDropped(t *testing.T) {
	m := homeModel(t, &fake{}, config.Settings{Server: "h:1"})
	m.refresh()
	old := m.seq
	m.refresh()
	m = drive(t, m, fetchedMsg{seq: old, err: errors.New("old answer")})
	if m.fetchErr != nil || !m.loading {
		t.Fatal("an older answer replaced the newer request")
	}
	mustShow(t, m, "Connecting to h:1")
}

func TestNarrowLayout(t *testing.T) {
	m := homeModel(t, &fake{snap: sampleSnap}, config.Settings{Mode: config.ModePlay, Name: "alice_01", Server: "h:1"})
	m = drive(t, m, tea.WindowSizeMsg{Width: 50, Height: 20})
	mustShow(t, m, "> Overview", "Leaderboards")
	mustNotShow(t, m, "Connection")
	m = drive(t, m, keys("enter")...)
	mustShow(t, m, "Overview", "Connection", "Version")
	mustNotShow(t, m, "Leaderboards")
	m = drive(t, m, keys("esc")...)
	mustShow(t, m, "> Overview")

	m = drive(t, m, tea.WindowSizeMsg{Width: 100, Height: 30})
	mustShow(t, m, "> Overview", "Connection") // both panes
}

func TestLinesFitTheTerminal(t *testing.T) {
	for _, w := range []int{30, 50, 69, 70, 80, 120, 200} {
		for _, p := range []string{"1", "2", "3", "4", "5"} {
			m := homeModel(t, &fake{snap: sampleSnap}, config.Settings{Mode: config.ModeBoth, Name: "alice_01", Server: "a-long-host-name.example.org:36100"})
			m.title, m.board = "NPWR00860_00", "1"
			m = drive(t, m, tea.WindowSizeMsg{Width: w, Height: 24})
			m = drive(t, m, keys(p, "enter", "r")...)
			lines := strings.Split(m.View().Content, "\n")
			if len(lines) > 24 {
				t.Errorf("width %d pane %s: %d lines, want at most 24", w, p, len(lines))
			}
			for i, l := range lines {
				if lw := lipgloss.Width(l); lw > w {
					t.Errorf("width %d pane %s line %d is %d wide: %q", w, p, i, lw, escapes.ReplaceAllString(l, ""))
				}
			}
		}
	}
}

func TestThemeToggle(t *testing.T) {
	m := homeModel(t, &fake{}, config.Settings{Server: "h:1"})
	dark := m.View()
	if m.light || dark.BackgroundColor == nil {
		t.Fatal("want dark with a background colour by default")
	}
	mustShow(t, m, "dark")
	m = drive(t, m, keys("t")...)
	light := m.View()
	if !m.light || m.set.Theme != config.ThemeLight || !m.dirty {
		t.Fatalf("after t: light %v theme %q dirty %v", m.light, m.set.Theme, m.dirty)
	}
	if light.BackgroundColor == dark.BackgroundColor || light.Content == dark.Content {
		t.Fatal("the light theme looks the same as the dark one")
	}
	mustShow(t, m, "light")
	m = drive(t, m, keys("t")...)
	if m.light {
		t.Fatal("t did not toggle back")
	}

	nc := New(Options{Path: "x", NoColor: true, Fetcher: &fake{}})
	if v := nc.View(); v.BackgroundColor != nil || strings.Contains(v.Content, "38;2;") {
		t.Fatal("NO_COLOR: colours are set")
	}
}

func TestSettings(t *testing.T) {
	var saved *config.File
	save := func(_ string, f *config.File) error {
		saved = f
		return config.Save(filepath.Join(t.TempDir(), "s.ini"), f)
	}
	m := New(Options{Path: "s.ini", File: &config.File{Settings: config.Settings{Mode: config.ModePlay, Name: "alice_01", Server: "h:1"}},
		Fetcher: &fake{}, Save: save})
	m = drive(t, m, keys("4", "enter")...)
	mustShow(t, m, "Mode", "play", "Status URL", "optional HTTP status")
	m = drive(t, m, keys("enter")...) // mode: play -> host
	if m.set.Mode != config.ModeHost || !m.dirty {
		t.Fatalf("mode %q dirty %v", m.set.Mode, m.dirty)
	}
	mustShow(t, m, "Unsaved changes")
	m = drive(t, m, seq(keys("down", "enter", "ctrl+u"), typed("a b"), keys("enter"))...)
	mustShow(t, m, "[player] name", "only letters")
	if m.editing != editName {
		t.Fatal("a refused name ended the edit")
	}
	m = drive(t, m, keys("esc")...)
	if m.editing != editNone || m.set.Name != "alice_01" {
		t.Fatalf("esc: editing %d name %q", m.editing, m.set.Name)
	}
	m = drive(t, m, seq(keys("down", "enter", "ctrl+u"), typed("10.0.0.2:36100"), keys("enter"))...)
	m = drive(t, m, seq(keys("down", "enter"), typed("ftp://x"), keys("enter"))...)
	mustShow(t, m, "[server] status_url")
	m = drive(t, m, seq(keys("ctrl+u"), typed("https://example.org/status"), keys("enter"))...)
	m = drive(t, m, keys("down", "enter", "s")...) // theme, then save
	if saved == nil {
		t.Fatal("s did not save")
	}
	want := config.Settings{Mode: config.ModeHost, Theme: config.ThemeLight, Name: "alice_01", Server: "10.0.0.2:36100", StatusURL: "https://example.org/status"}
	if saved.Settings != want {
		t.Fatalf("saved %+v, want %+v", saved.Settings, want)
	}
	if m.dirty {
		t.Fatal("still dirty after a save")
	}
	mustShow(t, m, "Saved.")

	m.opt.Save = func(string, *config.File) error { return errors.New("read-only file system") }
	m = drive(t, m, keys("s")...)
	mustShow(t, m, "Not saved", "read-only file system")
}

func TestProtocolFetcherReportsDialErrors(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	ln.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if _, err := (Protocol{}).Fetch(ctx, Query{Server: addr}); err == nil || !strings.Contains(err.Error(), "dial") {
		t.Fatalf("got %v, want a dial error", err)
	}
}

func TestFieldTakesNoControlCharacters(t *testing.T) {
	f := field{limit: 5}
	f.insert("ab\x1b[31mcdefgh")
	if f.value != "ab[31" {
		t.Fatalf("got %q", f.value)
	}
	f.key(press("backspace"))
	if f.value != "ab[3" {
		t.Fatalf("backspace: %q", f.value)
	}
}

// sgr is the only escape the interface itself writes: colour and weight.
var sgr = regexp.MustCompile(`\x1b\[[0-9;:]*m`)

func TestServerTextCannotInjectEscapes(t *testing.T) {
	hostile := []string{"ali\x1b[2Jce", "\x1b]0;x\x07zed", "bo\x00\x00b", "\x9b31mc\x7fd", "bad\xffutf\xc3"}
	for _, in := range hostile {
		if got := clean(in); strings.ContainsFunc(got, unicode.IsControl) || !utf8.ValidString(got) {
			t.Errorf("clean(%q) = %q", in, got)
		}
	}
	if got := clean("ali\x1b[2Jce"); got != "ali[2Jce" {
		t.Errorf("clean keeps the printable bytes: got %q", got)
	}

	s := sampleSnap
	s.Rooms = client.RoomList{Total: 1, Rooms: []client.RoomInfo{{ID: 1, Owner: "ali\x1b[2Jce", Cur: 1, Max: 4}}}
	s.Ranking = client.Ranking{Total: 1, Rows: []client.RankRow{{Rank: 1, OnlineID: "\x1b]0;x\x07zed", Score: 5}}}
	f := &fake{snap: s}
	m := homeModel(t, f, config.Settings{Server: "h:1"})
	m = drive(t, m, tea.WindowSizeMsg{Width: 120, Height: 30})
	m.title, m.board = "T", "1"
	m = drive(t, m, keys("2", "enter", "r")...)
	check := func(want string) {
		t.Helper()
		raw := sgr.ReplaceAllString(m.View().Content, "")
		if strings.ContainsAny(raw, "\x1b\x07\x00\x9b") {
			t.Errorf("a control byte reached the screen:\n%q", raw)
		}
		mustShow(t, m, want)
	}
	check("ali[2Jce")
	m = drive(t, m, keys("tab")...)
	check("]0;xzed")

	f.err = errors.New("dial h:1: \x1b[2Jrefused")
	m = drive(t, m, keys("r")...)
	check("refused")
}

// Spamming r must not open a connection per key press: a press while a fetch runs, or just after one, does nothing.
func TestRefreshKeyDoesNotStackFetches(t *testing.T) {
	f := &fake{snap: sampleSnap}
	now := time.Unix(100, 0)
	m := New(Options{Path: filepath.Join(t.TempDir(), config.FileName), File: &config.File{Settings: config.Settings{Server: "h:1"}},
		Fetcher: f, Now: func() time.Time { return now }})
	m = drive(t, m, initialRefreshMsg{}) // the first fetch on start
	if n := len(f.queries); n != 1 {
		t.Fatalf("start made %d fetches, want 1", n)
	}
	now = now.Add(5 * time.Second)
	// First press starts a fetch; run it later, so it is "in flight" while the key is hit again.
	next, cmd := m.Update(press("r"))
	m = next.(Model)
	if cmd == nil || !m.loading {
		t.Fatal("r did not start a fetch")
	}
	for i := 0; i < 20; i++ {
		var c tea.Cmd
		next, c = m.Update(press("r"))
		m = next.(Model)
		if c != nil {
			t.Fatalf("press %d while a fetch was running started another", i+2)
		}
	}
	// The fetch finishes; a press within the cooldown still does nothing.
	next, _ = m.Update(cmd())
	m = next.(Model)
	if _, c := m.Update(press("r")); c != nil {
		t.Error("r inside the cooldown started a fetch")
	}
	// After the cooldown it works again.
	now = now.Add(refreshCooldown)
	if _, c := m.Update(press("r")); c == nil {
		t.Error("r after the cooldown did nothing")
	}
	if n := len(f.queries); n != 2 {
		t.Errorf("%d fetches in all, want 2 (start and the first press)", n)
	}
}

// ticking is a clock that moves two seconds on every look, so the r cooldown never holds back a test.
func ticking() func() time.Time {
	t := time.Unix(0, 0)
	return func() time.Time { t = t.Add(2 * time.Second); return t }
}
