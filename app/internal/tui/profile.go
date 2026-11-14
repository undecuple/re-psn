package tui

import (
	"context"
	"crypto/x509"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/undecuple/re-psn/app/framework"
)

// ProfileSession owns one unlocked profile for the desktop. Trophy reporting
// and friend operations share its store rather than racing separate writers.
// Construction does no file or network I/O; actions open it explicitly.
type ProfileSession struct {
	mu          sync.Mutex
	dir         string
	store       *framework.Store
	broker      *framework.Broker
	listener    net.Listener
	removeGrant func()
	grantToken  [32]byte
	hasGrant    bool
	renewDone   chan struct{}
	grantErr    error
	socialGate  chan struct{}
	ctx         context.Context
	cancel      context.CancelFunc
	closed      bool
}

func NewProfileSession(dir string) *ProfileSession {
	ctx, cancel := context.WithCancel(context.Background())
	return &ProfileSession{dir: dir, ctx: ctx, cancel: cancel, socialGate: make(chan struct{}, 1)}
}
func (s *ProfileSession) writer() (*framework.Store, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil, framework.ErrClosed
	}
	if s.store == nil {
		var err error
		s.store, err = framework.Open(s.dir)
		if err != nil {
			return nil, err
		}
	}
	return s.store, nil
}
func (s *ProfileSession) Read() (framework.View, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return framework.View{}, framework.ErrClosed
	}
	if s.store != nil {
		return s.store.View(), nil
	}
	return framework.Read(s.dir)
}
func (s *ProfileSession) GrantError() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.grantErr
}
func (s *ProfileSession) Create(label string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return framework.ErrClosed
	}
	if s.store != nil {
		return framework.ErrExists
	}
	var err error
	s.store, err = framework.Create(s.dir, label)
	return err
}
func (s *ProfileSession) Close() error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil
	}
	s.closed = true
	s.cancel()
	b, store, remove, listener, renewDone := s.broker, s.store, s.removeGrant, s.listener, s.renewDone
	s.mu.Unlock()
	// Cancellation makes an active social request finish before its store closes.
	s.socialGate <- struct{}{}
	defer func() { <-s.socialGate }()
	if b != nil {
		_ = b.Close()
	}
	if listener != nil {
		_ = listener.Close()
	}
	if renewDone != nil {
		<-renewDone
	}
	if remove != nil {
		remove()
	}
	if store != nil {
		return store.Close()
	}
	return nil
}
func (s *ProfileSession) Grant(title string) (string, error) {
	store, err := s.writer()
	if err != nil {
		return "", err
	}
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return "", framework.ErrClosed
	}
	if s.broker == nil {
		s.mu.Unlock()
		ln, e := net.Listen("tcp4", "127.0.0.1:0")
		if e != nil {
			return "", e
		}
		s.mu.Lock()
		if s.closed {
			s.mu.Unlock()
			ln.Close()
			return "", framework.ErrClosed
		}
		if s.broker == nil {
			s.broker = framework.NewBroker(store)
			s.listener = ln
			go func(b *framework.Broker) {
				if err := b.Serve(ln); err != nil {
					_ = b.Close()
				}
			}(s.broker)
		} else {
			s.mu.Unlock()
			ln.Close()
			s.mu.Lock()
			if s.closed {
				s.mu.Unlock()
				return "", framework.ErrClosed
			}
		}
	}
	defer s.mu.Unlock()
	// Revoke under the broker's mutation ordering, retaining the listener.
	if s.hasGrant {
		s.broker.Revoke(s.grantToken)
		s.hasGrant = false
	}
	if s.removeGrant != nil {
		s.removeGrant()
		s.removeGrant = nil
	}
	g, err := s.broker.Grant(title)
	if err != nil {
		return "", err
	}
	path := filepath.Join(s.dir, "game-grant.txt")
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		s.broker.Revoke(g.Token)
		return "", err
	}
	info, _ := f.Stat()
	_, err = fmt.Fprintf(f, "re-psn-app-grant-v1\nport=%d\ntoken=%s\n", s.listener.Addr().(*net.TCPAddr).Port, hex.EncodeToString(g.Token[:]))
	if err == nil {
		err = f.Sync()
	}
	if e := f.Close(); err == nil {
		err = e
	}
	remove := func() {
		if now, e := os.Lstat(path); e == nil && info != nil && os.SameFile(info, now) {
			_ = os.Remove(path)
		}
	}
	if err != nil {
		remove()
		s.broker.Revoke(g.Token)
		return "", err
	}
	s.removeGrant = remove
	s.grantToken = g.Token
	s.hasGrant = true
	s.grantErr = nil
	if s.renewDone == nil {
		s.renewDone = make(chan struct{})
		go s.renewGrants()
	}
	return path, nil
}

func (s *ProfileSession) renewGrants() {
	defer close(s.renewDone)
	ticker := time.NewTicker(5 * time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-s.ctx.Done():
			return
		case <-ticker.C:
			s.mu.Lock()
			b, token, active := s.broker, s.grantToken, s.hasGrant
			s.mu.Unlock()
			if !active || b == nil {
				continue
			}
			if err := b.Renew(token); err != nil {
				s.mu.Lock()
				if s.hasGrant && s.grantToken == token {
					s.hasGrant = false
					s.grantErr = fmt.Errorf("game grant must be reissued: %w", err)
					if s.removeGrant != nil {
						s.removeGrant()
						s.removeGrant = nil
					}
				}
				s.mu.Unlock()
			}
		}
	}
}
func (s *ProfileSession) Revoke() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return framework.ErrClosed
	}
	if s.hasGrant {
		s.broker.Revoke(s.grantToken)
		s.hasGrant = false
	}
	if s.removeGrant != nil {
		s.removeGrant()
		s.removeGrant = nil
	}
	s.grantErr = nil
	return nil
}
func (s *ProfileSession) Social(origin, ca, action, id string) error {
	if err := s.ctx.Err(); err != nil {
		return err
	}
	select {
	case s.socialGate <- struct{}{}:
		defer func() { <-s.socialGate }()
	default:
		return framework.ErrBusy
	}
	store, err := s.writer()
	if err != nil {
		return err
	}
	var roots *x509.CertPool
	if ca != "" {
		st, e := os.Lstat(ca)
		if e != nil || !st.Mode().IsRegular() || st.Size() > 1<<20 {
			return errors.New("invalid CA file")
		}
		f, e := os.Open(ca)
		if e != nil {
			return errors.New("CA file unavailable")
		}
		b, e := io.ReadAll(io.LimitReader(f, (1<<20)+1))
		f.Close()
		if e != nil || len(b) > 1<<20 {
			return errors.New("invalid CA file")
		}
		roots = x509.NewCertPool()
		if !roots.AppendCertsFromPEM(b) {
			return errors.New("invalid CA file")
		}
	}
	c, err := framework.NewSocialClient(store, framework.SocialClientConfig{Origin: origin, Roots: roots})
	if err != nil {
		return err
	}
	defer c.Close()
	ctx, cancel := context.WithTimeout(s.ctx, 5*time.Second)
	defer cancel()
	switch action {
	case "register":
		_, err = c.Register(ctx, store.View().Label)
	case "list":
		_, err = c.State(ctx)
	case "request":
		_, err = c.Request(ctx, id)
	case "accept":
		_, err = c.Accept(ctx, id)
	case "decline":
		_, err = c.Decline(ctx, id)
	case "cancel":
		_, err = c.Cancel(ctx, id)
	case "remove":
		_, err = c.Remove(ctx, id)
	case "block":
		_, err = c.Block(ctx, id)
	case "unblock":
		_, err = c.Unblock(ctx, id)
	default:
		return framework.ErrInvalid
	}
	return err
}

type profileMsg struct {
	seq  int
	view framework.View
	err  error
	note string
}
type profileStartMsg struct{}

func (m *Model) profileAction(action, id string) tea.Cmd {
	m.profileSeq++
	m.profileLoading = true
	m.profileErr = nil
	m.profileNote = ""
	seq, session, origin, ca := m.profileSeq, m.opt.ProfileSession, m.opt.SocialOrigin, m.opt.SocialCAFile
	label := m.set.Name
	if label == "" {
		label = "Player"
	}
	return func() tea.Msg {
		if session == nil {
			return profileMsg{seq: seq, err: errors.New("select a private profile directory with --profile-dir")}
		}
		var err error
		note := ""
		switch action {
		case "create":
			err = session.Create(label)
		case "grant":
			var path string
			path, err = session.Grant(id)
			if err == nil {
				note = fmt.Sprintf("Private game grant: %q", path)
			}
		case "revoke":
			err = session.Revoke()
			if err == nil {
				note = "Game grant revoked."
			}
		case "read":
		default:
			err = session.Social(origin, ca, action, id)
		}
		v, readErr := session.Read()
		if err == nil {
			err = readErr
		}
		if note == "" && session.GrantError() != nil {
			note = "Game grant unavailable; issue a new grant in Trophies."
		}
		return profileMsg{seq: seq, view: v, err: err, note: note}
	}
}

func (m Model) profileKey(key string) (tea.Model, tea.Cmd) {
	if m.profileLoading {
		return m, nil
	}
	if m.profileConfirm != "" {
		action := m.profileConfirm
		m.profileConfirm = ""
		if key == "y" {
			cmd := m.profileAction(action, "")
			return m, cmd
		}
		return m, nil
	}
	switch key {
	case "c":
		if m.menu == paneProfile {
			m.profileConfirm = "create"
		}
	case "e":
		if m.menu == paneFriends {
			m.profileConfirm = "register"
		}
	case "f":
		if m.menu == paneFriends {
			m.startEdit(editFriend, "", 68)
		}
	case "up", "k":
		if m.menu == paneTrophies && m.profileView != nil && len(m.profileView.Titles) > 0 {
			m.profileTitle = (m.profileTitle + len(m.profileView.Titles) - 1) % len(m.profileView.Titles)
		} else if m.menu == paneFriends && m.friendRow > 0 {
			m.friendRow--
		}
	case "down", "j":
		if m.menu == paneTrophies && m.profileView != nil && len(m.profileView.Titles) > 0 {
			m.profileTitle = (m.profileTitle + 1) % len(m.profileView.Titles)
		} else if m.menu == paneFriends {
			if rows := m.friendRows(); m.friendRow+1 < len(rows) {
				m.friendRow++
			}
		}
	case "g":
		if m.menu == paneTrophies && m.profileView != nil && m.profileTitle < len(m.profileView.Titles) {
			cmd := m.profileAction("grant", m.profileView.Titles[m.profileTitle].Catalog.TitleID)
			return m, cmd
		}
	case "v":
		if m.menu == paneTrophies {
			cmd := m.profileAction("revoke", "")
			return m, cmd
		}
	case "a", "d", "u", "b", "x":
		if m.menu == paneFriends {
			rows := m.friendRows()
			if m.friendRow < len(rows) {
				r := rows[m.friendRow]
				action := ""
				switch key {
				case "a":
					if r.kind == "incoming" {
						action = "accept"
					}
				case "d":
					if r.kind == "incoming" {
						action = "decline"
					} else if r.kind == "outgoing" {
						action = "cancel"
					}
				case "u":
					if r.kind == "friend" {
						action = "remove"
					}
				case "b":
					action = "block"
				case "x":
					if r.kind == "blocked" {
						action = "unblock"
					}
				}
				if action != "" {
					cmd := m.profileAction(action, r.id)
					return m, cmd
				}
			}
		}
	}
	return m, nil
}

type friendRow struct{ kind, id, label string }

func (m Model) selectedRelay() *framework.RelayView {
	if m.profileView == nil {
		return nil
	}
	for i := len(m.profileView.Relays) - 1; i >= 0; i-- {
		r := &m.profileView.Relays[i]
		if m.opt.SocialOrigin == "" || r.Origin == strings.TrimSuffix(m.opt.SocialOrigin, "/") {
			return r
		}
	}
	return nil
}
func (m Model) friendRows() []friendRow {
	r := m.selectedRelay()
	if r == nil {
		return nil
	}
	var rows []friendRow
	for _, c := range r.State.Incoming {
		rows = append(rows, friendRow{"incoming", c.AccountID, c.Label})
	}
	for _, c := range r.State.Friends {
		rows = append(rows, friendRow{"friend", c.AccountID, c.Label})
	}
	for _, c := range r.State.Outgoing {
		rows = append(rows, friendRow{"outgoing", c.AccountID, c.Label})
	}
	for _, id := range r.State.Blocked {
		rows = append(rows, friendRow{"blocked", id, ""})
	}
	return rows
}
func (m Model) profileLines() []string {
	b := []string{}
	if m.profileLoading {
		b = append(b, "Working…")
	}
	if m.profileErr != nil {
		b = append(b, "Profile action: "+m.profileErr.Error())
	}
	if m.profileNote != "" {
		b = append(b, m.profileNote)
	}
	if m.profileConfirm == "create" {
		return append(b, "Create a local portable profile? [y/N]", "Relay registration remains a separate explicit action.")
	}
	if m.profileConfirm == "register" {
		return append(b, "Register this public identity on the selected relay? [y/N]", "Reusing it makes registrations linkable across relays.")
	}
	if m.menu == paneFriends && m.editing == editFriend {
		return append(b, "Peer public account ID:", m.cursorField(m.edit), "Enter sends a request; the peer must accept.")
	}
	if m.profileView == nil || m.profileView.AccountID == "" {
		return append(b, "No local profile loaded. In Profile, press c to create one.", "Existing profiles can be selected with --profile-dir DIR.")
	}
	v := m.profileView
	switch m.menu {
	case paneProfile:
		b = append(b, "Portable local profile: "+v.Label, "Public account ID:", v.AccountID, "Same identity is linkable across relays you join.", "Not a PSN login or a provider-verification badge.", "Use profile export/restore for encrypted backups.")
	case paneTrophies:
		b = append(b, "Game-reported trophies · local only; not verified")
		for i, t := range v.Titles {
			mark := "  "
			if i == m.profileTitle {
				mark = "> "
			}
			b = append(b, fmt.Sprintf("%s%s · %d/%d", mark, t.Catalog.Name, len(t.Unlocks), len(t.Catalog.Trophies)))
		}
		if m.profileTitle < len(v.Titles) {
			t := v.Titles[m.profileTitle]
			for _, def := range t.Catalog.Trophies {
				state := "locked"
				for _, u := range t.Unlocks {
					if u.TrophyID == def.ID {
						state = "reported " + time.UnixMilli(u.UnlockedAt).UTC().Format("2006-01-02 15:04")
					}
				}
				b = append(b, fmt.Sprintf("  %d [%s] %s · %s", def.ID, def.Grade, def.Name, state))
			}
		}
		if len(v.Titles) == 0 {
			b = append(b, "Approve a developer catalog with trophies install first.")
		}
	case paneFriends:
		if m.opt.SocialOrigin == "" {
			b = append(b, "No social relay selected; cached view only.", "Use --social-relay HTTPS_ORIGIN and optionally --social-ca-file FILE.")
		}
		if r := m.selectedRelay(); r != nil {
			b = append(b, r.Origin, "Cached authenticated snapshot: "+time.UnixMilli(r.FetchedAt).UTC().Format(time.RFC3339), "Refresh to check changes. Online presence is not provided.")
			for i, row := range m.friendRows() {
				mark := "  "
				if i == m.friendRow {
					mark = "> "
				}
				b = append(b, mark+row.kind+" "+row.label, "    "+row.id)
			}
		} else {
			b = append(b, "No authenticated friend snapshot yet. Register or refresh explicitly.")
		}
	}
	return b
}
