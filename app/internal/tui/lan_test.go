package tui

import (
	"context"
	"errors"
	"net/netip"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/undecuple/re-psn/app/internal/client"
	"github.com/undecuple/re-psn/app/internal/config"
)

type lanFake struct {
	mu    sync.Mutex
	ports []uint16
	found []client.Found
	err   error
}

func (l *lanFake) discover(ctx context.Context, wait time.Duration, port uint16) ([]client.Found, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if _, ok := ctx.Deadline(); !ok {
		return nil, errors.New("search without a deadline")
	}
	l.ports = append(l.ports, port)
	return l.found, l.err
}

var lanServers = []client.Found{
	{IP: netip.MustParseAddr("192.168.1.20"), TCPPort: 36100, UDPPort: 36101, Relay: true, Players: 3, Name: "attic", Version: "0.5.0"},
	{IP: netip.MustParseAddr("192.168.1.21"), TCPPort: 36200, UDPPort: 36201, Players: 0, Name: "desk", Version: "0.5.0"},
}

func lanSettingsModel(t *testing.T, l *lanFake, server string) Model {
	t.Helper()
	m := New(Options{Path: filepath.Join(t.TempDir(), config.FileName), Fetcher: &fake{snap: sampleSnap}, Discover: l.discover,
		File: &config.File{Settings: config.Settings{Mode: config.ModePlay, Name: "alice_01", Server: server}}})
	return drive(t, m, keys("4", "enter")...)
}

func TestFindOnLANShowsListAndFillsServer(t *testing.T) {
	l := &lanFake{found: lanServers}
	m := lanSettingsModel(t, l, "127.0.0.1:36100")
	// The search runs in a command: the key alone only opens the list.
	next, cmd := m.Update(press("f"))
	m = next.(Model)
	if cmd == nil || !m.lan.loading {
		t.Fatal("f did not start a search in a command")
	}
	mustShow(t, m, "Searching")
	m = drive(t, m, cmd())
	mustShow(t, m, "Servers on the LAN", "attic", "192.168.1.20:36100", "3 players", "relay on", "0.5.0", "desk", "192.168.1.21:36200", "relay off")
	if len(l.ports) != 1 || l.ports[0] != 36100 {
		t.Fatalf("ports %v", l.ports)
	}
	m = drive(t, m, keys("down", "enter")...)
	if m.lan.open || m.set.Server != "192.168.1.21:36200" || !m.dirty {
		t.Fatalf("open %v server %q dirty %v", m.lan.open, m.set.Server, m.dirty)
	}
}

func TestFindOnLANNoneFound(t *testing.T) {
	l := &lanFake{}
	m := lanSettingsModel(t, l, "")
	m = drive(t, m, keys("f")...)
	mustShow(t, m, "none found (the server must run with discovery on; it does not cross subnets)")
	if l.ports[0] != 0 {
		t.Fatalf("port %d, want 0 for the default", l.ports[0])
	}
	m = drive(t, m, keys("enter")...) // nothing to choose
	if !m.lan.open || m.set.Server != "" {
		t.Fatal("enter with an empty list changed something")
	}
	m = drive(t, m, keys("esc")...)
	if m.lan.open {
		t.Fatal("esc did not close the list")
	}
}

func TestFindOnLANWhileTypingTheServer(t *testing.T) {
	l := &lanFake{found: lanServers}
	m := lanSettingsModel(t, l, "10.0.0.2:36100")
	m = drive(t, m, keys("down", "down", "enter", "ctrl+u")...)
	m = drive(t, m, seq(typed("host:4000"), keys("ctrl+f"))...)
	if l.ports[0] != 4000 || !m.lan.open {
		t.Fatalf("ports %v open %v", l.ports, m.lan.open)
	}
	m = drive(t, m, keys("enter")...)
	if m.editing != editServer || m.edit.value != "192.168.1.20:36100" {
		t.Fatalf("editing %d field %q", m.editing, m.edit.value)
	}
	m = drive(t, m, keys("enter")...)
	if m.set.Server != "192.168.1.20:36100" {
		t.Fatalf("server %q", m.set.Server)
	}
}

func TestFindOnLANFirstRun(t *testing.T) {
	l := &lanFake{found: lanServers}
	m := New(Options{Path: filepath.Join(t.TempDir(), "re-psn", config.FileName), FirstRun: true, Discover: l.discover})
	m = drive(t, m, seq(keys("enter"), typed("alice_01"), keys("enter", "ctrl+f", "enter"))...)
	if m.stage != stageServer || m.input.value != "192.168.1.20:36100" {
		t.Fatalf("stage %d field %q", m.stage, m.input.value)
	}
	mustShow(t, m, "ctrl+f")
}

func TestFindOnLANCloseDropsLateAnswer(t *testing.T) {
	l := &lanFake{found: lanServers}
	m := lanSettingsModel(t, l, "")
	next, cmd := m.Update(press("f"))
	m = next.(Model)
	next, _ = m.Update(press("esc"))
	m = next.(Model)
	m = drive(t, m, cmd())
	if m.lan.open || m.lan.found != nil {
		t.Fatal("an answer after esc reopened or filled the list")
	}
}

func TestFindOnLANError(t *testing.T) {
	m := lanSettingsModel(t, &lanFake{err: errors.New("network is unreachable")}, "")
	m = drive(t, m, keys("f")...)
	mustShow(t, m, "Search failed", "network is unreachable")
}
