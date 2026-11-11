package tui

import (
	"fmt"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/undecuple/re-psn/app/internal/client"
	"github.com/undecuple/re-psn/app/internal/config"
)

// notOverProtocol is what the screen says where the server's status page shows something the game protocol has no
// message for.
const notOverProtocol = "not available over the protocol"

// View renders the model. The content is a plain function of the state; the background and text colour are the
// theme's, unless NO_COLOR is set.
func (m Model) View() tea.View {
	v := tea.NewView(m.render())
	v.AltScreen = true
	v.WindowTitle = "re-psn"
	if !m.opt.NoColor && !m.quitting {
		v.BackgroundColor, v.ForegroundColor = m.st.bg, m.st.fg
	}
	return v
}

func (m Model) render() string {
	if m.quitting {
		return ""
	}
	var body string
	if m.stage != stageHome {
		body = m.renderChooser()
	} else {
		body = m.renderHome()
	}
	return lipgloss.NewStyle().MaxWidth(m.w).MaxHeight(m.h).Render(body)
}

// paneTitle is the heading of the content pane when the menu is hidden; the Server tab belongs to Status.
func (m Model) paneTitle() string {
	if m.menu == paneServer {
		return "Status: Server"
	}
	return paneNames[m.menu]
}

func (m Model) themeName() string {
	if m.light {
		return "light"
	}
	return "dark"
}

// hints renders a key-hint bar: pairs of key and what it does.
func (m Model) hints(pairs ...string) string {
	var b []string
	for i := 0; i+1 < len(pairs); i += 2 {
		b = append(b, m.st.key.Render(pairs[i])+" "+m.st.muted.Render(pairs[i+1]))
	}
	return strings.Join(b, "  ")
}

func (m Model) cursorField(f field) string {
	return m.st.strong.Render(f.value) + m.st.selected.Render(" ")
}

// The first run.

var modeHelp = map[config.Mode]string{
	config.ModePlay: "join servers: see their rooms and leaderboards",
	config.ModeHost: "run a server on this machine (the Host screen comes later; Status works now)",
	config.ModeBoth: "play and host",
}

func (m Model) renderChooser() string {
	var b []string
	b = append(b, m.st.heading.Render("re-psn")+m.st.muted.Render(" · first start"), "")
	switch m.stage {
	case stageMode:
		b = append(b, m.st.text.Render("What is this app for on this machine?"), "")
		for i, md := range modes {
			label := fmt.Sprintf("%-5s", strings.ToUpper(string(md[:1]))+string(md[1:]))
			line := "  " + label + "  " + modeHelp[md]
			if i == m.choice {
				line = m.st.selected.Render("> "+label) + "  " + m.st.text.Render(modeHelp[md])
			} else {
				line = m.st.text.Render(line)
			}
			b = append(b, line)
		}
		b = append(b, "", m.st.muted.Render("You can change this later in Settings."), "",
			m.hints("↑/↓", "choose", "enter", "next", "t", "theme", "esc/q", "quit"))
	case stageName:
		b = append(b, m.st.text.Render("Your online ID: 3 to 16 letters, digits, - or _."), "",
			"  "+m.st.muted.Render("Name   ")+m.cursorField(m.input))
		b = append(b, m.problemLines()...)
		b = append(b, "", m.hints("enter", "next", "backspace", "back (when empty)", "esc", "quit"))
	case stageServer:
		b = append(b, m.st.text.Render("The server to use, as host:port (for example 127.0.0.1:36100)."), "",
			"  "+m.st.muted.Render("Server ")+m.cursorField(m.input))
		b = append(b, m.problemLines()...)
		if m.lan.open {
			b = append(b, "")
			b = append(b, m.lanLines()...)
			b = append(b, "", m.hints(m.lanHints()...))
			break
		}
		b = append(b, "", m.st.muted.Render("Enter saves to "+m.opt.Path+"."), "",
			m.hints("enter", "save", "ctrl+f", "find on LAN", "backspace", "back (when empty)", "esc", "quit"))
	}
	return lipgloss.NewStyle().Padding(1, 2).Render(strings.Join(b, "\n"))
}

func (m Model) problemLines() []string {
	if m.problem == "" {
		return nil
	}
	return []string{"", "  " + m.st.bad.Render("Not accepted: ") + m.st.text.Render(m.problem)}
}

// The home screen: a header, the menu and content panes (or one of them when narrow), a hint bar.

func (m Model) renderHome() string {
	header := m.st.heading.Render("re-psn") + "  " + m.st.text.Render(m.summary())
	if gap := m.w - lipgloss.Width(header) - len(m.themeName()); gap > 1 {
		header += strings.Repeat(" ", gap) + m.st.muted.Render(m.themeName())
	}
	bodyH := max(m.h-2, 3)
	var body string
	if m.w < narrowWidth {
		if m.content {
			body = m.box(m.st.heading.Render(m.paneTitle())+"\n\n"+m.renderContent(m.w-4), m.w, bodyH, true)
		} else {
			body = m.box(m.renderMenu(), m.w, bodyH, true)
		}
	} else {
		const menuW = 18
		body = lipgloss.JoinHorizontal(lipgloss.Top,
			m.box(m.renderMenu(), menuW, bodyH, !m.content),
			m.box(m.renderContent(m.w-menuW-4), m.w-menuW, bodyH, m.content))
	}
	return header + "\n" + body + "\n" + m.hintBar()
}

func (m Model) summary() string {
	name := m.set.Name
	if name == "" {
		name = "no name"
	}
	server := m.set.Server
	if server == "" {
		server = "no server"
	}
	mode := string(m.set.Mode)
	if mode == "" {
		mode = "mode not set"
	}
	return mode + " · " + name + " · " + server
}

// box draws a frame of outer size w by h around s.
func (m Model) box(s string, w, h int, focus bool) string {
	c := m.st.frame
	if focus {
		c = m.st.frameFocus
	}
	st := lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).Padding(0, 1).
		Width(w).Height(h).MaxWidth(w).MaxHeight(h)
	if c != nil {
		st = st.BorderForeground(c)
	}
	return st.Render(s)
}

func (m Model) renderMenu() string {
	var b []string
	cur := m.menu
	if cur == paneServer {
		cur = paneRooms // the Server tab belongs to Status
	}
	for i, n := range paneNames {
		if pane(i) == cur {
			b = append(b, m.st.selected.Render("> "+n))
		} else {
			b = append(b, m.st.text.Render("  "+n))
		}
	}
	return strings.Join(b, "\n")
}

func (m Model) hintBar() string {
	switch {
	case m.lan.open:
		return m.hints(m.lanHints()...)
	case m.editing == editServer:
		return m.hints("enter", "done", "esc", "cancel", "ctrl+u", "clear", "ctrl+f", "find on LAN")
	case m.editing != editNone:
		return m.hints("enter", "done", "esc", "cancel", "ctrl+u", "clear")
	case !m.content:
		return m.hints("↑/↓", "move", "enter", "open", "r", "refresh", "t", "theme", "q", "quit")
	case m.menu == paneServer:
		return m.hints("tab", "rooms", "r", "refresh", "esc", "menu", "t", "theme", "q", "quit")
	case m.menu == paneRooms:
		return m.hints("tab", "leaderboards", "e", "title", "r", "refresh", "esc", "menu", "t", "theme", "q", "quit")
	case m.menu == paneBoards:
		next := "rooms"
		if m.set.StatusURL != "" {
			next = "server"
		}
		return m.hints("tab", next, "e", "title", "b", "board", "r", "refresh", "esc", "menu", "q", "quit")
	case m.menu == paneSettings:
		return m.hints("↑/↓", "move", "enter", "change", "f", "find on LAN", "s", "save", "esc", "menu", "t", "theme", "q", "quit")
	case m.menu == paneOverview:
		return m.hints("r", "refresh", "esc", "menu", "t", "theme", "q", "quit")
	}
	return m.hints("esc", "menu", "t", "theme", "q", "quit")
}

func (m Model) renderContent(w int) string {
	var lines []string
	switch m.menu {
	case paneOverview:
		lines = m.renderOverview()
	case paneRooms, paneBoards:
		lines = m.renderStatus()
	case paneServer:
		lines = m.serverLines()
	case paneSettings:
		lines = m.renderSettings()
	case paneHelp:
		lines = m.renderHelp()
	}
	return lipgloss.NewStyle().Width(w).Render(strings.Join(lines, "\n"))
}

func (m Model) row(label, value string) string {
	return m.st.muted.Render(fmt.Sprintf("%-10s", label)) + " " + m.st.text.Render(value)
}

func orUnset(v string) string {
	if v == "" {
		return "(not set)"
	}
	return v
}

func (m Model) renderOverview() []string {
	b := []string{
		m.row("Mode", orUnset(string(m.set.Mode))),
		m.row("Name", orUnset(m.set.Name)),
		m.row("Server", orUnset(m.set.Server)),
		m.row("Settings", m.opt.Path),
		"",
		m.st.heading.Render("Connection"),
	}
	b = append(b, m.connection()...)
	if h := m.httpStats(); h != nil {
		b = append(b, "", m.row("Version", httpVersion(h)), m.row("Uptime", httpText(h.Uptime)), m.row("Clients", fmt.Sprint(h.Clients)), m.row("Connections", fmt.Sprint(h.Connections)))
	} else {
		b = append(b, "",
			m.row("Version", notOverProtocol),
			m.row("Uptime", notOverProtocol),
			m.row("Players", notOverProtocol+" (connection counts)"))
	}
	if m.set.Mode == config.ModeHost || m.set.Mode == config.ModeBoth {
		b = append(b, "", m.st.muted.Render("Hosting from the app comes in a later version. For now, run the re-psn server and set its address as Server."))
	}
	return b
}

// connection says in words what the last refresh found.
func (m Model) connection() []string {
	lines := m.connectionLines()
	if m.snap != nil && m.snap.HTTPUnavailable {
		lines = append(lines, m.st.warn.Render("HTTP status unavailable; using the game protocol when reachable."))
	}
	return lines
}

func (m Model) connectionLines() []string {
	server := m.snapQuery.Server
	switch {
	case m.loading:
		return []string{m.st.warn.Render("Connecting") + m.st.text.Render(" to "+server+"...")}
	case m.fetchErr != nil:
		where := ""
		if server != "" {
			where = " from " + server
		}
		return []string{
			m.st.bad.Render("No answer"+where+": ") + m.st.text.Render(clean(m.fetchErr.Error())),
			m.st.muted.Render("Check the server in Settings, then press r."),
		}
	case m.snap == nil:
		return []string{m.st.muted.Render("Not checked yet: press r.")}
	}
	if h := m.httpStats(); h != nil {
		return []string{m.st.good.Render("HTTP status connected") + m.st.text.Render("; read-only server snapshot."), m.st.muted.Render("Checked at " + m.fetchedAt.Format("15:04:05") + ". No game-protocol session was opened.")}
	}
	if m.snap.HTTP != nil {
		return []string{m.st.muted.Render("Status source changed: press r.")}
	}
	a := m.snap.Ack
	return []string{
		m.st.good.Render("Connected") + m.st.text.Render(fmt.Sprintf(" to %s; HELLO answered in %s.", server, ms(m.snap.RTT))),
		m.st.text.Render(fmt.Sprintf("Relay %s, invite-only rooms %s. The server sees this machine as %s.", onOff(a.Relay()), onOff(a.RoomCodes()), a.PublicIP)),
		m.st.muted.Render("Checked at " + m.fetchedAt.Format("15:04:05") + " as " + clean(m.snap.Name) + "."),
	}
}

func (m Model) tabs() string {
	on, off := m.st.tabOn, m.st.tabOff
	rooms, boards, server := off.Render(" Rooms "), off.Render(" Leaderboards "), off.Render(" Server ")
	switch m.menu {
	case paneRooms:
		rooms = on.Render("[Rooms]")
	case paneServer:
		server = on.Render("[Server]")
	default:
		boards = on.Render("[Leaderboards]")
	}
	if m.set.StatusURL != "" {
		return rooms + " " + boards + " " + server
	}
	return rooms + " " + boards
}

func (m Model) editOr(e edit, v, empty string) string {
	if m.editing == e {
		return m.cursorField(m.edit)
	}
	if v == "" {
		return m.st.muted.Render(empty)
	}
	return m.st.id.Render(v)
}

func (m Model) renderStatus() []string {
	b := []string{m.tabs(), ""}
	b = append(b, m.st.muted.Render(fmt.Sprintf("%-10s", "Title"))+" "+m.editOr(editTitle, m.title, "(not set: press e)"))
	if m.menu == paneBoards {
		b = append(b, m.st.muted.Render(fmt.Sprintf("%-10s", "Board"))+" "+m.editOr(editBoard, m.board, "(not set: press b)"))
	}
	if m.problem != "" {
		b = append(b, m.st.bad.Render("Not accepted: ")+m.st.text.Render(m.problem))
	}
	b = append(b, "")
	b = append(b, m.connection()...)
	if h := m.httpStats(); h != nil {
		b = append(b, m.st.text.Render(fmt.Sprintf("Up %s · %d clients · %d connections total", httpText(h.Uptime), h.Clients, h.Connections)), m.st.muted.Render("Version: "+httpVersion(h)+"."), "")
	} else {
		b = append(b, m.st.muted.Render("Uptime, connections and server version: "+notOverProtocol+"."), "")
	}
	if m.menu == paneRooms {
		return append(b, m.roomLines()...)
	}
	return append(b, m.boardLines()...)
}

// current reports whether the last answer is for the title (and board) on screen.
func (m Model) current(board bool) bool {
	if m.snap == nil || m.fetchErr != nil || m.snapQuery.Title != m.title || m.title == "" {
		return false
	}
	return !board || (m.snapQuery.Boards && fmt.Sprint(m.snapQuery.Board) == m.board)
}

func (m Model) roomLines() []string {
	if h := m.httpStats(); h != nil {
		return m.httpRoomLines(h)
	}
	if m.title == "" {
		return []string{m.st.text.Render("Rooms belong to one title: press e and type its communication ID, such as NPWR00860_00.")}
	}
	if !m.current(false) {
		return []string{m.st.muted.Render("No room list yet: press r.")}
	}
	l := m.snap.Rooms
	if len(l.Rooms) == 0 {
		return []string{m.st.text.Render("No open rooms for " + m.title + "."), m.st.muted.Render("Full and invite-only rooms are not listed by the server.")}
	}
	b := []string{m.st.strong.Render(fmt.Sprintf("%-10s %-16s %-8s %-10s %s", "ID", "Owner", "Members", "Flags", "Age"))}
	for _, r := range l.Rooms {
		b = append(b, m.st.text.Render(fmt.Sprintf("%-10d %-16s %-8s 0x%08X %s", r.ID, clean(r.Owner), fmt.Sprintf("%d/%d", r.Cur, r.Max), r.Flags, "n/a")))
	}
	return append(b, "",
		m.st.muted.Render(fmt.Sprintf("%d of %d open rooms. Full and invite-only rooms are not listed; Age (n/a) is %s.", len(l.Rooms), l.Total, notOverProtocol)))
}

func (m Model) boardLines() []string {
	if h := m.httpStats(); h != nil {
		return m.httpBoardLines(h)
	}
	note := m.st.muted.Render("The protocol cannot list a title's boards: type a board id (b).")
	switch {
	case m.title == "":
		return []string{m.st.text.Render("Boards belong to one title: press e and type its communication ID."), note}
	case m.board == "":
		return []string{note}
	case !m.current(true):
		return []string{m.st.muted.Render("No ranking yet: press r."), note}
	case m.snap.RankErr != nil:
		return []string{m.st.bad.Render("Board "+m.board+": ") + m.st.text.Render(clean(m.snap.RankErr.Error())), note}
	}
	k := m.snap.Ranking
	if len(k.Rows) == 0 {
		return []string{m.st.text.Render("Board " + m.board + " of " + m.title + " has no scores."), note}
	}
	b := []string{m.st.strong.Render(fmt.Sprintf("%-6s %-16s %14s  %s", "Rank", "Player", "Score", "Recorded"))}
	for _, r := range k.Rows {
		b = append(b, m.st.text.Render(fmt.Sprintf("%-6d %-16s %14d  %s", r.Rank, clean(r.OnlineID), r.Score, recorded(r))))
	}
	return append(b, "", m.st.muted.Render(fmt.Sprintf("Top %d of %d players on board %s.", len(k.Rows), k.Total, m.board)), note)
}

func recorded(r client.RankRow) string {
	if r.Recorded <= 0 {
		return "-"
	}
	return time.Unix(r.Recorded, 0).UTC().Format("2006-01-02 15:04 UTC")
}

func (m Model) renderSettings() []string {
	rows := []struct{ label, value, note string }{
		{"Mode", orUnset(string(m.set.Mode)), "enter: play, host, both"},
		{"Name", orUnset(m.set.Name), "enter to edit"},
		{"Server", orUnset(m.set.Server), "host:port; f finds one on the LAN"},
		{"Status URL", orUnset(m.set.StatusURL), "optional HTTP status; r refreshes"},
		{"Theme", m.themeName(), "enter or t"},
	}
	edits := []edit{editNone, editName, editServer, editStatusURL, editNone}
	b := []string{m.st.heading.Render("Settings"), ""}
	for i, r := range rows {
		mark := "  "
		label := m.st.muted.Render(fmt.Sprintf("%-10s", r.label))
		if i == m.setRow && m.content {
			mark = m.st.selected.Render(">") + " "
			label = m.st.selected.Render(fmt.Sprintf("%-10s", r.label))
		}
		value := m.st.text.Render(fmt.Sprintf("%-22s", r.value))
		if m.editing != editNone && edits[i] == m.editing {
			value = m.cursorField(m.edit)
		}
		b = append(b, mark+label+" "+value+" "+m.st.muted.Render(r.note))
	}
	b = append(b, "")
	if m.lan.open {
		return append(b, m.lanLines()...)
	}
	if m.problem != "" {
		b = append(b, m.st.bad.Render("Not accepted: ")+m.st.text.Render(m.problem))
	}
	switch {
	case m.saveErr != nil:
		b = append(b, m.st.bad.Render("Not saved: ")+m.st.text.Render(clean(m.saveErr.Error())))
	case m.dirty:
		b = append(b, m.st.warn.Render("Unsaved changes: ")+m.st.text.Render("press s to save."))
	case m.saveNote != "":
		b = append(b, m.st.good.Render(m.saveNote))
	}
	return append(b, m.st.muted.Render("File: "+m.opt.Path+" (written with mode 0600)."))
}

func (m Model) renderHelp() []string {
	keys := [][2]string{
		{"↑/↓ or k/j", "move in the menu or a list"},
		{"enter", "open the item, or change a setting"},
		{"esc", "back to the menu; cancel an edit"},
		{"tab", "switch between Rooms, Leaderboards and (with a status URL) Server"},
		{"e / b", "type the title / the board id"},
		{"r", "ask the server again"},
		{"f", "find servers on the LAN (Settings)"},
		{"ctrl+f", "the same, while typing the server"},
		{"s", "save the settings"},
		{"t", "dark or light theme"},
		{"q, ctrl+c", "quit"},
	}
	b := []string{m.st.heading.Render("Keys"), ""}
	for _, k := range keys {
		b = append(b, m.st.key.Render(fmt.Sprintf("%-12s", k[0]))+" "+m.st.text.Render(k[1]))
	}
	return append(b, "", m.st.heading.Render("What the screens show"), "",
		m.st.text.Render("A configured Status URL supplies native server totals, public rooms and board summaries without a browser. Otherwise, Rooms and Leaderboards come from the game protocol under a throwaway name. "+
			"The protocol cannot list a title's boards or report the server's version, uptime or connection counts, so those read \""+notOverProtocol+"\"."))
}

func ms(d time.Duration) string { return fmt.Sprintf("%.1f ms", float64(d.Microseconds())/1000) }

func onOff(b bool) string {
	if b {
		return "on"
	}
	return "off"
}

// lanNone is the whole answer when nothing replied.
const lanNone = "none found (the server must run with discovery on; it does not cross subnets)"

func (m Model) lanHints() []string {
	if m.lan.loading {
		return []string{"esc", "cancel"}
	}
	return []string{"↑/↓", "choose", "enter", "use", "r", "search again", "esc", "close"}
}

func (m Model) lanLines() []string {
	b := []string{m.st.heading.Render("Servers on the LAN")}
	switch {
	case m.lan.loading:
		return append(b, m.st.warn.Render("Searching..."))
	case m.lan.err != nil:
		b = append(b, m.st.bad.Render("Search failed: ")+m.st.text.Render(clean(m.lan.err.Error())))
	case len(m.lan.found) == 0:
		return append(b, m.st.text.Render(lanNone))
	}
	for i, f := range m.lan.found {
		line := fmt.Sprintf("%s  %s  %d players  relay %s  %s", f.Name, f.Addr(), f.Players, onOff(f.Relay), f.Version)
		if i == m.lan.cur {
			b = append(b, m.st.selected.Render("> "+line))
		} else {
			b = append(b, m.st.text.Render("  "+line))
		}
	}
	return b
}

// httpVersion is the version a status page reports, or says that this server does not.
func httpVersion(h *HTTPStats) string {
	if h.Server.Version == nil {
		return notReported
	}
	return httpText(*h.Server.Version)
}
