package tui

import (
	"context"
	"encoding/binary"
	"errors"
	"io"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/undecuple/re-psn/app/framework"
)

func TestProfileScreensDoNotCreateOrConnectOnStartup(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "absent")
	session := NewProfileSession(dir)
	defer session.Close()
	m := New(Options{ProfileDir: dir, ProfileSession: session})
	if m.Init() != nil {
		t.Fatal("empty app starts work")
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatal("startup created profile", err)
	}
	m = drive(t, m, press("6"), press("enter"))
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatal("read-only profile view created data", err)
	}
	m = drive(t, m, press("c"), press("n"))
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatal("declined creation wrote data", err)
	}
	m = drive(t, m, press("c"), press("y"))
	if m.profileView == nil || m.profileView.AccountID == "" {
		t.Fatal("confirmed creation failed", m.profileErr)
	}
	if strings.Contains(m.render(), "seed") {
		t.Fatal("secret shown")
	}
	m = drive(t, m, press("esc"), press("8"), press("enter"))
	if !strings.Contains(m.render(), "cached view only") {
		t.Fatal("friends silently implies live data")
	}
}

func TestProfileCachedOriginCanonicalization(t *testing.T) {
	m := New(Options{SocialOrigin: "https://relay.example.invalid/"})
	m.profileView = &framework.View{AccountID: "synthetic", Relays: []framework.RelayView{{Origin: "https://relay.example.invalid", FetchedAt: 1, State: framework.SocialState{Friends: []framework.Contact{{AccountID: "synthetic-friend", Label: "Fixture"}}}}}}
	if m.selectedRelay() == nil || len(m.friendRows()) != 1 {
		t.Fatal("allowed trailing slash hides cached relay")
	}
	m.menu = paneFriends
	if !strings.Contains(strings.Join(m.profileLines(), "\n"), "Cached authenticated snapshot") {
		t.Fatal("cache is not labeled stale")
	}
}

func TestDesktopGameGrantLifetimeAndReadViews(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "profile")
	session := NewProfileSession(dir)
	defer session.Close()
	if err := session.Create("Synthetic desktop"); err != nil {
		t.Fatal(err)
	}
	store, err := session.writer()
	if err != nil {
		t.Fatal(err)
	}
	if err := store.InstallCatalog(framework.Catalog{TitleID: "SYNTH-DESKTOP", Name: "Synthetic desktop", Revision: "1", Trophies: []framework.Trophy{{ID: 7, Name: "Synthetic event", Grade: "silver"}}}); err != nil {
		t.Fatal(err)
	}
	path, err := session.Grant("SYNTH-DESKTOP")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := framework.Open(dir); !errors.Is(err, framework.ErrBusy) {
		t.Fatal("second writer accepted", err)
	}
	if _, err := framework.Read(dir); err != nil {
		t.Fatal("read-only screen cannot read during game", err)
	}
	session.mu.Lock()
	token, port := session.grantToken, session.listener.Addr().(*net.TCPAddr).Port
	session.mu.Unlock()
	call := func() byte {
		t.Helper()
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		d := net.Dialer{}
		conn, e := d.DialContext(ctx, "tcp4", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)))
		if e != nil {
			t.Fatal(e)
		}
		defer conn.Close()
		conn.SetDeadline(time.Now().Add(3 * time.Second))
		var request [48]byte
		copy(request[:], "RPT1")
		request[4] = 1
		request[11] = 36
		copy(request[12:44], token[:])
		binary.BigEndian.PutUint32(request[44:], 7)
		if _, e = conn.Write(request[:]); e != nil {
			t.Fatal(e)
		}
		if e = conn.(*net.TCPConn).CloseWrite(); e != nil {
			t.Fatal(e)
		}
		var reply [12]byte
		if _, e = io.ReadFull(conn, reply[:]); e != nil {
			t.Fatal(e)
		}
		return reply[4]
	}
	if code := call(); code != 0 {
		t.Fatal("desktop game grant", code)
	}
	if err := session.Revoke(); err != nil {
		t.Fatal(err)
	}
	if code := call(); code != 1 {
		t.Fatal("revoked descriptor still works", code)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("revoked descriptor remains", err)
	}
	if err := session.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := framework.Open(dir)
	if err != nil {
		t.Fatal("desktop kept writer after close", err)
	}
	defer reopened.Close()
	if state, err := reopened.Status("SYNTH-DESKTOP", 7); err != nil || !state.Unlocked {
		t.Fatal("desktop report was not persisted", err)
	}
}

func TestProfileNavigationPreservesPendingAction(t *testing.T) {
	m := New(Options{})
	m.menu, m.content = paneFriends, true
	_ = m.profileAction("list", "") // Keep its response pending while navigating.
	seq := m.profileSeq
	m = drive(t, m, press("esc"), press("6"), press("enter"))
	if !m.profileLoading || m.profileSeq != seq {
		t.Fatal("pane read superseded a pending social action")
	}
	m = drive(t, m, profileMsg{seq: seq, view: framework.View{AccountID: "synthetic"}})
	if m.profileLoading || m.profileView == nil || m.profileView.AccountID != "synthetic" {
		t.Fatal("original action response lost after navigation")
	}
}

func TestExpiredDesktopGrantReportsNeedToReissue(t *testing.T) {
	session := NewProfileSession(filepath.Join(t.TempDir(), "profile"))
	defer session.Close()
	if err := session.Create("Synthetic"); err != nil {
		t.Fatal(err)
	}
	session.mu.Lock()
	session.grantErr = framework.ErrUnauthorized // Simulate the renewal loop's expiry result.
	session.mu.Unlock()
	m := New(Options{ProfileSession: session})
	cmd := m.profileAction("read", "")
	m = drive(t, m, cmd())
	if !strings.Contains(m.profileNote, "issue a new grant") {
		t.Fatal("expired grant looked active", m.profileNote)
	}
}
