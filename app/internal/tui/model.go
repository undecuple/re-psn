// Package tui is the app's terminal UI, built on Bubble Tea. Model's Update and View are plain functions of
// messages, so the tests drive them with tea.Msg values and a fake Fetcher, with no terminal.
package tui

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strconv"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/undecuple/re-psn/app/internal/client"
	"github.com/undecuple/re-psn/app/internal/config"
)

// Options are what the program hands the UI.
type Options struct {
	Path     string       // the settings file
	File     *config.File // its contents; an empty File when it does not exist
	FirstRun bool         // no settings file yet: start with the chooser
	Fetcher  Fetcher      // nil means NativeStatus{}
	Save     func(path string, f *config.File) error
	Timeout  time.Duration // for one refresh; 0 means 5 s
	NoColor  bool          // NO_COLOR is set: bold and reverse only
	Now      func() time.Time
	// Discover looks for servers on the local network; nil means client.Discover. port is 0 for the default.
	Discover func(ctx context.Context, wait time.Duration, port uint16) ([]client.Found, error)
}

type stage int

const (
	stageMode stage = iota // first run: Play, Host or Both
	stageName
	stageServer
	stageHome
)

type pane int

const (
	paneOverview pane = iota
	paneRooms
	paneBoards
	paneSettings
	paneHelp
)

var paneNames = []string{"Overview", "Rooms", "Leaderboards", "Settings", "Help"}

type edit int

const (
	editNone edit = iota
	editTitle
	editBoard
	editName
	editServer
	editStatusURL
)

var modes = []config.Mode{config.ModePlay, config.ModeHost, config.ModeBoth}

// narrowWidth: below this many columns the menu and the content are one list, one at a time.
const narrowWidth = 70

// Model is the whole UI state. It is a value; Update returns the next one.
type Model struct {
	opt  Options
	file *config.File
	set  config.Settings // the settings being edited; saved with s or at the end of the first run

	w, h  int
	light bool
	st    styles

	stage   stage
	choice  int
	input   field
	problem string // why the last entry was refused

	menu    pane
	content bool // the content pane has the focus (else the menu)

	editing edit
	edit    field
	title   string // the title whose rooms and boards Status shows
	board   string // the board id typed, as text

	seq       int // the latest refresh; older answers are dropped
	loading   bool
	snap      *Snapshot
	snapQuery Query
	fetchErr  error
	fetchedAt time.Time

	lan lanState

	setRow   int
	dirty    bool
	saveNote string
	saveErr  error
	quitting bool
}

type initialRefreshMsg struct{}

type fetchedMsg struct {
	seq  int
	snap Snapshot
	err  error
	at   time.Time
}

// lanState is the Find on LAN list: open while it is on screen, loading while a search runs.
type lanState struct {
	open    bool
	loading bool
	seq     int // the latest search; older answers are dropped
	found   []client.Found
	err     error
	cur     int
}

type lanMsg struct {
	seq   int
	found []client.Found
	err   error
}

type savedMsg struct {
	file  *config.File
	err   error
	first bool
}

// New makes the starting model.
func New(o Options) Model {
	if o.File == nil {
		o.File = &config.File{}
	}
	if o.Fetcher == nil {
		o.Fetcher = NativeStatus{}
	}
	if o.Save == nil {
		o.Save = config.Save
	}
	if o.Timeout <= 0 {
		o.Timeout = 5 * time.Second
	}
	if o.Now == nil {
		o.Now = time.Now
	}
	if o.Discover == nil {
		o.Discover = client.Discover
	}
	m := Model{opt: o, file: o.File, set: o.File.Settings, w: 80, h: 24, stage: stageHome}
	m.light = m.set.Theme == config.ThemeLight
	m.st = newStyles(m.light, o.NoColor)
	if o.FirstRun {
		m.stage = stageMode
		for i, md := range modes {
			if md == m.set.Mode {
				m.choice = i
			}
		}
	}
	return m
}

// Init checks the connection at start when a server is already set.
func (m Model) Init() tea.Cmd {
	if m.stage == stageHome && (m.set.Server != "" || m.set.StatusURL != "") {
		return func() tea.Msg { return initialRefreshMsg{} }
	}
	return nil
}

// Update handles one message.
func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case initialRefreshMsg:
		return m, m.refresh()
	case tea.WindowSizeMsg:
		m.w, m.h = msg.Width, msg.Height
	case fetchedMsg:
		if msg.seq == m.seq {
			m.loading = false
			m.snap, m.fetchErr, m.fetchedAt = &msg.snap, msg.err, msg.at
		}
	case savedMsg:
		return m.saved(msg)
	case lanMsg:
		if msg.seq == m.lan.seq && m.lan.open {
			m.lan.loading, m.lan.found, m.lan.err, m.lan.cur = false, msg.found, msg.err, 0
		}
	case tea.PasteMsg:
		if f := m.activeField(); f != nil {
			f.insert(msg.Content)
		}
	case tea.KeyPressMsg:
		if msg.String() == "ctrl+c" {
			return m.quit()
		}
		if m.lan.open {
			return m.lanKey(msg.String())
		}
		if m.stage != stageHome {
			return m.chooserKey(msg)
		}
		return m.homeKey(msg)
	}
	return m, nil
}

func (m Model) quit() (tea.Model, tea.Cmd) {
	m.quitting = true
	return m, tea.Quit
}

func (m *Model) activeField() *field {
	switch {
	case m.stage == stageName || m.stage == stageServer:
		return &m.input
	case m.editing != editNone:
		return &m.edit
	}
	return nil
}

func (m *Model) toggleTheme() {
	m.light = !m.light
	m.st = newStyles(m.light, m.opt.NoColor)
	m.set.Theme = config.ThemeDark
	if m.light {
		m.set.Theme = config.ThemeLight
	}
	if m.stage == stageHome {
		m.dirty = m.set != m.file.Settings
	}
}

// The first run: mode, then name (not for Host), then server, then save.

func (m Model) chooserKey(k tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	key := k.String()
	if key == "esc" {
		return m.quit()
	}
	if m.stage == stageMode {
		switch key {
		case "q":
			return m.quit()
		case "t":
			m.toggleTheme()
		case "up", "k":
			m.choice = (m.choice + len(modes) - 1) % len(modes)
		case "down", "j":
			m.choice = (m.choice + 1) % len(modes)
		case "enter":
			m.set.Mode = modes[m.choice]
			m.problem = ""
			if m.set.Mode == config.ModeHost {
				m.stage, m.input = stageServer, field{value: m.set.Server}
			} else {
				m.stage, m.input = stageName, field{value: m.set.Name, limit: 16}
			}
		}
		return m, nil
	}
	if key == "ctrl+f" && m.stage == stageServer {
		return m.startLAN(m.input.trimmed())
	}
	if key == "backspace" && m.input.value == "" {
		m.problem = ""
		if m.stage == stageServer && m.set.Mode != config.ModeHost {
			m.stage, m.input = stageName, field{value: m.set.Name, limit: 16}
		} else {
			m.stage = stageMode
		}
		return m, nil
	}
	if key != "enter" {
		m.input.key(k)
		return m, nil
	}
	v := m.input.trimmed()
	if m.stage == stageName {
		if err := checkSetting(config.Settings{Name: v}, v); err != nil {
			m.problem = err.Error()
			return m, nil
		}
		m.set.Name, m.problem = v, ""
		m.stage, m.input = stageServer, field{value: m.set.Server}
		return m, nil
	}
	if err := checkSetting(config.Settings{Server: v}, v); err != nil {
		m.problem = err.Error()
		return m, nil
	}
	m.set.Server, m.problem = v, ""
	return m, m.save(true)
}

// checkSetting validates one value with the settings file's own rules; an empty value is refused here.
func checkSetting(s config.Settings, v string) error {
	if v == "" {
		return errors.New("this cannot be empty")
	}
	return s.Validate()
}

func (m *Model) save(first bool) tea.Cmd {
	cp := *m.file
	cp.Settings = m.set
	path, save := m.opt.Path, m.opt.Save
	return func() tea.Msg { return savedMsg{file: &cp, err: save(path, &cp), first: first} }
}

func (m Model) saved(msg savedMsg) (tea.Model, tea.Cmd) {
	if msg.err != nil {
		if msg.first {
			m.problem = "could not save: " + msg.err.Error()
		} else {
			m.saveErr, m.saveNote = msg.err, ""
		}
		return m, nil
	}
	m.file = msg.file
	m.dirty = m.set != m.file.Settings
	m.saveErr, m.saveNote = nil, "Saved."
	if msg.first {
		m.stage, m.saveNote = stageHome, ""
		return m, m.refresh()
	}
	return m, nil
}

// The home screen.

func (m Model) homeKey(k tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	if m.editing != editNone {
		return m.editKey(k)
	}
	key := k.String()
	switch key {
	case "q":
		return m.quit()
	case "t":
		m.toggleTheme()
		return m, nil
	case "r":
		if m.menu <= paneBoards {
			return m, m.refresh()
		}
	case "tab", "shift+tab":
		switch m.menu {
		case paneRooms:
			m.menu = paneBoards
		case paneBoards:
			m.menu = paneRooms
		}
		return m, nil
	}
	if !m.content {
		switch key {
		case "up", "k":
			m.menu = (m.menu + pane(len(paneNames)) - 1) % pane(len(paneNames))
		case "down", "j":
			m.menu = (m.menu + 1) % pane(len(paneNames))
		case "1", "2", "3", "4", "5":
			m.menu = pane(key[0] - '1')
		case "enter", "right", "l", "space":
			m.content = true
			m.startStatusEdit()
		}
		return m, nil
	}
	switch key {
	case "esc", "left", "h":
		m.content = false
		return m, nil
	}
	switch m.menu {
	case paneRooms, paneBoards:
		switch key {
		case "e":
			m.startEdit(editTitle, m.title, 12)
		case "b":
			m.menu = paneBoards
			m.startEdit(editBoard, m.board, 10)
		}
	case paneSettings:
		return m.settingsKey(key)
	}
	return m, nil
}

// startStatusEdit asks for what Status needs when it is opened without it.
func (m *Model) startStatusEdit() {
	switch {
	case m.menu != paneRooms && m.menu != paneBoards:
	case m.title == "":
		m.startEdit(editTitle, "", 12)
	case m.menu == paneBoards && m.board == "":
		m.startEdit(editBoard, "", 10)
	}
}

func (m *Model) startEdit(e edit, v string, limit int) {
	m.editing, m.edit, m.problem = e, field{value: v, limit: limit}, ""
}

func (m Model) editKey(k tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch k.String() {
	case "esc":
		m.editing, m.problem = editNone, ""
		return m, nil
	case "ctrl+f":
		if m.editing == editServer {
			return m.startLAN(m.edit.trimmed())
		}
		return m, nil
	case "enter":
	default:
		m.edit.key(k)
		return m, nil
	}
	v := m.edit.trimmed()
	var err error
	refresh := false
	switch m.editing {
	case editTitle:
		if err = checkTitle(v); err == nil {
			m.title, refresh = v, true
		}
	case editBoard:
		if err = checkBoard(v); err == nil {
			m.board, refresh = v, true
		}
	case editName:
		s := m.set
		s.Name = v
		if err = s.Validate(); err == nil {
			m.set = s
		}
	case editServer:
		s := m.set
		s.Server = v
		if err = s.Validate(); err == nil {
			m.set = s
			m.snap, m.fetchErr, m.loading = nil, nil, false
			m.seq++
		}
	case editStatusURL:
		s := m.set
		s.StatusURL = v
		if v != "" {
			if _, err = statusEndpoint(v); err != nil {
				err = errors.New("[server] status_url: use HTTP or HTTPS without credentials, query or fragment")
			}
		}
		if err == nil {
			err = s.Validate()
		}
		if err == nil {
			m.set = s
			m.snap, m.fetchErr, m.loading = nil, nil, false
			m.seq++
		}
	}
	if err != nil {
		m.problem = err.Error()
		return m, nil
	}
	m.editing, m.problem = editNone, ""
	m.dirty = m.set != m.file.Settings
	if refresh {
		return m, m.refresh()
	}
	return m, nil
}

// checkTitle: a communication ID is 1 to 12 letters, digits, _ or -; empty clears it.
func checkTitle(v string) error {
	if len(v) > 12 {
		return fmt.Errorf("title %q: want at most 12 characters, such as NPWR00860_00", v)
	}
	for _, r := range v {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '_') {
			return fmt.Errorf("title %q: only letters, digits, - and _", v)
		}
	}
	return nil
}

// checkBoard: a board id is a number from 0 to 4294967295; empty clears it.
func checkBoard(v string) error {
	if v == "" {
		return nil
	}
	if _, err := strconv.ParseUint(v, 10, 32); err != nil {
		return fmt.Errorf("board %q: want a number from 0 to 4294967295", v)
	}
	return nil
}

const settingRows = 5

func (m Model) settingsKey(key string) (tea.Model, tea.Cmd) {
	switch key {
	case "up", "k":
		m.setRow = (m.setRow + settingRows - 1) % settingRows
	case "down", "j":
		m.setRow = (m.setRow + 1) % settingRows
	case "s":
		m.saveNote, m.saveErr = "", nil
		return m, m.save(false)
	case "f":
		return m.startLAN(m.set.Server)
	case "enter", "space":
		switch m.setRow {
		case 0:
			i := 0
			for j, md := range modes {
				if md == m.set.Mode {
					i = j + 1
				}
			}
			m.set.Mode = modes[i%len(modes)]
		case 1:
			m.startEdit(editName, m.set.Name, 16)
		case 2:
			m.startEdit(editServer, m.set.Server, 0)
		case 3:
			m.startEdit(editStatusURL, m.set.StatusURL, 0)
		case 4:
			m.toggleTheme()
		}
		m.dirty = m.set != m.file.Settings
	}
	return m, nil
}

// refresh starts one fetch from the server, with a timeout. Without a server it only says so.
func (m *Model) refresh() tea.Cmd {
	m.seq++
	m.snap, m.fetchErr = nil, nil
	if m.set.Server == "" && m.set.StatusURL == "" {
		m.loading = false
		m.fetchErr = errors.New("no server set: add one in Settings")
		return nil
	}
	q := Query{Server: m.set.Server, Title: m.title, StatusURL: m.set.StatusURL, StatusTokenFile: m.set.StatusTokenFile}
	if m.board != "" {
		n, _ := strconv.ParseUint(m.board, 10, 32)
		q.Board, q.Boards = uint32(n), true
	}
	m.loading, m.snapQuery = true, q
	seq, f, timeout, now := m.seq, m.opt.Fetcher, m.opt.Timeout, m.opt.Now
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), timeout)
		defer cancel()
		s, err := f.Fetch(ctx, q)
		return fetchedMsg{seq: seq, snap: s, err: err, at: now()}
	}
}

// Find on LAN. The search runs in a command, so the screen stays responsive; its answer is the list below the
// server field. Choosing a server puts its address into the field being edited, or into Server in Settings.

// lanWait is how long one search listens for answers.
const lanWait = client.DefaultDiscoverWait

// startLAN opens the list and starts a search on the port of the server typed so far (the default if there is none).
func (m Model) startLAN(typed string) (tea.Model, tea.Cmd) {
	var port uint16
	if _, p, err := net.SplitHostPort(typed); err == nil {
		if n, err := strconv.ParseUint(p, 10, 16); err == nil {
			port = uint16(n)
		}
	}
	m.lan.seq++
	m.lan.open, m.lan.loading, m.lan.found, m.lan.err, m.lan.cur = true, true, nil, nil, 0
	seq, find := m.lan.seq, m.opt.Discover
	return m, func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), lanWait+time.Second)
		defer cancel()
		found, err := find(ctx, lanWait, port)
		return lanMsg{seq: seq, found: found, err: err}
	}
}

func (m Model) lanKey(key string) (tea.Model, tea.Cmd) {
	n := len(m.lan.found)
	switch key {
	case "esc", "q":
		m.lan.open = false
		m.lan.seq++ // drop the answer of a search still running
	case "up", "k":
		if n > 0 {
			m.lan.cur = (m.lan.cur + n - 1) % n
		}
	case "down", "j":
		if n > 0 {
			m.lan.cur = (m.lan.cur + 1) % n
		}
	case "r":
		if !m.lan.loading {
			typed := m.set.Server
			switch {
			case m.stage == stageServer:
				typed = m.input.trimmed()
			case m.editing == editServer:
				typed = m.edit.trimmed()
			}
			return m.startLAN(typed)
		}
	case "enter":
		if m.lan.loading || n == 0 {
			return m, nil
		}
		return m.useFound(m.lan.found[m.lan.cur]), nil
	}
	return m, nil
}

func (m Model) useFound(f client.Found) Model {
	addr := f.Addr()
	m.lan.open = false
	m.problem = ""
	switch {
	case m.stage == stageServer:
		m.input.value = addr
	case m.editing == editServer:
		m.edit.value = addr
	default:
		m.set.Server = addr
		m.snap, m.fetchErr, m.loading = nil, nil, false
		m.seq++
		m.dirty = m.set != m.file.Settings
	}
	return m
}
