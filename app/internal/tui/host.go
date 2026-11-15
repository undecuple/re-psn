package tui

import (
	tea "charm.land/bubbletea/v2"
	"context"
	"fmt"
	"github.com/undecuple/re-psn/app/internal/config"
	"time"
)

type hostMsg struct{ err error }

func (m Model) paneAllowed(p pane) bool {
	if p == paneHost {
		return m.set.Mode == config.ModeHost || m.set.Mode == config.ModeBoth
	}
	if m.set.Mode == config.ModeHost {
		return p == paneOverview || p == paneSettings || p == paneHelp
	}
	return true
}
func (m *Model) moveMenu(direction int) {
	for i := 0; i < len(paneNames); i++ {
		m.menu = (m.menu + pane(len(paneNames)) + pane(direction)) % pane(len(paneNames))
		if m.paneAllowed(m.menu) {
			return
		}
	}
}
func (m Model) hostLines() []string {
	b := []string{m.st.heading.Render("Your hosted server"), "", "The app manages only the server it starts.", "Standalone servers remain independent of this app."}
	address := m.set.HostAddress
	if address == "" {
		address = "127.0.0.1:36100"
	}
	b = append(b, "", "Game TCP/UDP bind: "+address, fmt.Sprintf("Relay: %t", m.set.HostRelay == "true"))
	if m.opt.Host == nil || !m.opt.Host.View().Available {
		return append(b, "", "Server component missing. Use the complete app package.")
	}
	v := m.opt.Host.View()
	b = append(b, "", v.Message)
	if v.Running {
		b = append(b, "Hosting on "+v.Address, "Started "+time.UnixMilli(v.StartedAt).UTC().Format(time.RFC3339), "Closing this app stops its owned server.")
	} else {
		b = append(b, "Stopped. Starting requires explicit confirmation.")
	}
	if m.hostLoading {
		b = append(b, "Working…")
	}
	if m.hostError != nil {
		b = append(b, "Host action: "+m.hostError.Error())
	}
	if m.hostConfirm {
		b = append(b, "Start the server on this bind address? [y/N]", "A LAN/public bind is reachable by other machines.")
	}
	return b
}
func (m Model) hostKey(key string) (tea.Model, tea.Cmd) {
	if m.hostLoading || m.opt.Host == nil {
		return m, nil
	}
	if m.hostConfirm {
		m.hostConfirm = false
		if key != "y" {
			return m, nil
		}
		m.hostLoading = true
		manager, address, relay := m.opt.Host, m.set.HostAddress, m.set.HostRelay == "true"
		if address == "" {
			address = "127.0.0.1:36100"
		}
		return m, func() tea.Msg {
			ctx, cancel := context.WithTimeout(context.Background(), 7*time.Second)
			defer cancel()
			return hostMsg{manager.Start(ctx, address, relay)}
		}
	}
	switch key {
	case "s":
		m.hostConfirm = true
	case "x":
		m.hostLoading = true
		manager := m.opt.Host
		return m, func() tea.Msg {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			return hostMsg{manager.Stop(ctx)}
		}
	}
	return m, nil
}
