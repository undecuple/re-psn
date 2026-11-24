// Package webui serves the optional desktop interface on a private loopback
// origin. Browser authority is separate from game grants and remote reader tokens.
package webui

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/subtle"
	"embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"log"
	"mime"
	"net"
	"net/http"
	"os"
	"strings"
	"sync"
	"syscall"
	"time"
	"unicode/utf8"

	"github.com/undecuple/re-psn/app/framework"
	"github.com/undecuple/re-psn/app/internal/appinstance"
	"github.com/undecuple/re-psn/app/internal/config"
	"github.com/undecuple/re-psn/app/internal/host"
	"github.com/undecuple/re-psn/app/internal/tui"
)

//go:embed assets/index.html assets/app.css assets/app.js
var assets embed.FS

const maxInput = 8 << 20

// defaultHostAddress is the host bind shown when none is saved.
const defaultHostAddress = "127.0.0.1:36100"

// uiWriteTimeout bounds writing any answer, except where a route that is slow by design extends its own (Check all).
var uiWriteTimeout = 20 * time.Second

type Options struct{ ConfigPath, ProfileDir, ServerBinary, Version string }
type Settings struct {
	Mode         string `json:"mode"`
	Theme        string `json:"theme"`
	Name         string `json:"name"`
	Server       string `json:"server"`
	StatusURL    string `json:"status_url"`
	SocialOrigin string `json:"social_origin"`
	SocialCAFile string `json:"social_ca_file"`
	HostAddress  string `json:"host_address"`
	HostRelay    bool   `json:"host_relay"`
	SetupDone    bool   `json:"setup_done"`
}
type Room struct {
	ID      string   `json:"id"`
	Title   string   `json:"title"`
	Current int      `json:"current"`
	Max     int      `json:"max"`
	Members []string `json:"members"`
}
type Board struct {
	ID      uint32 `json:"id"`
	Title   string `json:"title"`
	Leader  string `json:"leader"`
	Best    string `json:"best"`
	Entries int    `json:"entries"`
}
type Play struct {
	Status      string  `json:"status"`
	CheckedAt   int64   `json:"checked_at"`
	Message     string  `json:"message"`
	RoundTripMS float64 `json:"round_trip_ms"`
	Rooms       []Room  `json:"rooms"`
	Boards      []Board `json:"boards"`

	// What the last check asked and found, so the page can say what the empty parts mean.
	Source         string `json:"source"`          // "status_page" or "game_connection"; empty before any check
	Address        string `json:"address"`         // the game server that was checked; empty when only a status URL was used
	Titled         bool   `json:"titled"`          // a title ID filter was set, so the game connection asked for rooms
	UsedStatusURL  bool   `json:"used_status_url"` // a status URL was set for this check
	StatusFallback bool   `json:"status_fallback"` // the status URL failed and the game connection answered instead
	Unlisted       int    `json:"unlisted"`        // unlisted rooms counted without names; only a status page reports it (see Source)
	Kind           string `json:"kind"`            // why a failed check failed: "refused", "timeout", "status" or "other"
}
type Grant struct {
	Active  bool   `json:"active"`
	TitleID string `json:"title_id"`
	Path    string `json:"path"`
	Message string `json:"message"`
}
type State struct {
	Version      string          `json:"version"`
	Settings     Settings        `json:"settings"`
	Play         Play            `json:"play"`
	Host         host.View       `json:"host"`
	Profile      *framework.View `json:"profile"`
	ProfileError string          `json:"profile_error"`
	Grant        Grant           `json:"grant"`
	Servers      ServerList      `json:"servers"`
}
type Server struct {
	mu               sync.Mutex
	action           chan struct{}
	opts             Options
	file             *config.File
	profile          *tui.ProfileSession
	host             *host.Manager
	play             Play
	authority, token string
	bootstrap        string
	bootstrapExpires time.Time
	reopenKey        string
	servers          *serverStore
	fetch            tui.Fetcher
	addrs            addrLister
	ctx              context.Context
	cancel           context.CancelFunc
	closed           bool
}

func New(opts Options) (*Server, error) {
	f, err := config.Load(opts.ConfigPath)
	if err != nil && !os.IsNotExist(err) {
		return nil, errors.New("app settings unavailable")
	}
	var token [32]byte
	if _, err := rand.Read(token[:]); err != nil {
		return nil, err
	}
	var bootstrap [32]byte
	if _, err := rand.Read(bootstrap[:]); err != nil {
		return nil, err
	}
	var reopen [32]byte
	if _, err := rand.Read(reopen[:]); err != nil {
		return nil, err
	}
	ctx, cancel := context.WithCancel(context.Background())
	return &Server{servers: newServerStore(opts.ConfigPath), fetch: tui.NativeStatus{}, addrs: interfaceAddresses, opts: opts, file: f, profile: tui.NewProfileSession(opts.ProfileDir), host: host.New(opts.ServerBinary), action: make(chan struct{}, 1), play: Play{Status: "unchecked", Rooms: []Room{}, Boards: []Board{}}, token: hex.EncodeToString(token[:]), bootstrap: hex.EncodeToString(bootstrap[:]), bootstrapExpires: time.Now().Add(time.Minute), reopenKey: hex.EncodeToString(reopen[:]), ctx: ctx, cancel: cancel}, nil
}

// Locator is passed only to the native per-user controller; HTTP never returns its key.
func (s *Server) Locator() appinstance.Locator {
	s.mu.Lock()
	defer s.mu.Unlock()
	return appinstance.Locator{Origin: "http://" + s.authority, Key: s.reopenKey, Profile: s.opts.ProfileDir, Binary: s.opts.ServerBinary}
}
func (s *Server) settings() Settings {
	v := s.file.Settings
	theme := string(v.Theme)
	if theme == "" {
		theme = "dark"
	}
	address := v.HostAddress
	if address == "" {
		address = defaultHostAddress
	}
	return Settings{string(v.Mode), theme, v.Name, v.Server, v.StatusURL, v.SocialOrigin, v.SocialCAFile, address, v.HostRelay == "true", setupDone(v)}
}

// setupDone reports whether the first-run checklist is finished. A stored true or false wins. With no stored value (a
// settings file from before the checklist existed, or none yet) it counts as done: only the first workspace choice in
// the interface stores false, so nobody who already uses the app is sent back through it.
func setupDone(v config.Settings) bool {
	switch v.SetupDone {
	case "true":
		return true
	case "false":
		return false
	}
	return true
}

// failureKind sorts a failed check by what the error shows, never by a guess about the network. "status" means no game
// server was set, so only the status URL was tried; "refused" is a connection actively turned away; "timeout" is no
// answer in time; anything else (including a reply that was not the game protocol) is "other".
func failureKind(err error, server string) string {
	var ne net.Error
	switch {
	case server == "":
		return "status"
	case errors.Is(err, syscall.ECONNREFUSED):
		return "refused"
	case errors.Is(err, context.DeadlineExceeded), errors.As(err, &ne) && ne.Timeout():
		return "timeout"
	}
	return "other"
}
func (s *Server) state() State {
	s.mu.Lock()
	settings, play := s.settings(), s.play
	s.mu.Unlock()
	out := State{Version: s.opts.Version, Settings: settings, Play: play, Host: s.host.View(), Servers: s.servers.view()}
	if v, err := s.profile.Read(); err == nil {
		out.Profile = &v
	} else if !errors.Is(err, framework.ErrNotFound) && !os.IsNotExist(err) {
		out.ProfileError = "Local profile unavailable; check its private directory"
	}
	active, title, path, err := s.profile.GrantInfo()
	out.Grant = Grant{Active: active, TitleID: title, Path: path}
	if err != nil {
		out.Grant.Message = "Game grant expired or unavailable; issue a new grant"
	}
	return out
}

// Serve owns a literal IPv4 loopback listener and invokes ready only after its
// per-start authority is set. A one-use launch nonce is exchanged inside the
// page; the persistent UI capability never enters a launch URL or process args.
func (s *Server) Serve(ctx context.Context, ln net.Listener, ready func(string) error) error {
	defer ln.Close()
	addr, ok := ln.Addr().(*net.TCPAddr)
	if !ok || !addr.IP.Equal(net.IPv4(127, 0, 0, 1)) {
		return errors.New("app UI requires literal IPv4 loopback")
	}
	s.mu.Lock()
	if s.closed || s.authority != "" {
		s.mu.Unlock()
		return errors.New("app UI already served or closed")
	}
	s.authority = ln.Addr().String()
	s.mu.Unlock()
	h := &http.Server{Handler: http.HandlerFunc(s.handle), ReadHeaderTimeout: 3 * time.Second, ReadTimeout: 8 * time.Second, WriteTimeout: uiWriteTimeout, IdleTimeout: 10 * time.Second, MaxHeaderBytes: 8192, ErrorLog: log.New(io.Discard, "", 0), BaseContext: func(net.Listener) context.Context { return s.ctx }}
	done := make(chan error, 1)
	go func() { done <- h.Serve(&limitedListener{Listener: ln, slots: make(chan struct{}, 32)}) }()
	if ready != nil {
		if err := ready("http://" + s.authority + "/#token=" + s.bootstrap); err != nil {
			s.cancel()
			h.Close()
			<-done
			s.Close()
			return err
		}
	}
	var err error
	select {
	case <-ctx.Done():
	case <-s.ctx.Done():
	case err = <-done:
		done = nil
	}
	s.cancel()
	shutdown, cancel := context.WithTimeout(context.Background(), 6*time.Second)
	defer cancel()
	if e := h.Shutdown(shutdown); e != nil {
		h.Close()
	}
	if done != nil {
		<-done
	}
	s.Close()
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}
func (s *Server) Close() error {
	s.cancel()
	s.action <- struct{}{}
	defer func() { <-s.action }()
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil
	}
	s.closed = true
	s.mu.Unlock()
	_ = s.host.Close()
	return s.profile.Close()
}
func (s *Server) handle(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("X-Frame-Options", "DENY")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; script-src 'self'; style-src 'self'; connect-src 'self'; img-src 'self' blob:; base-uri 'none'; frame-ancestors 'none'; form-action 'none'")
	peer, _, err := net.SplitHostPort(r.RemoteAddr)
	origin := "http://" + s.authority
	// A private file launcher may navigate to the inert document across origins.
	// It grants no API access; scripts and every API keep the origin checks below.
	launchDocument := r.Method == "GET" && r.URL.Path == "/" && r.Header.Get("Origin") == "" && r.Header.Get("Sec-Fetch-Dest") == "document"
	if err != nil || peer != "127.0.0.1" || r.Host != s.authority || r.URL.RawQuery != "" || r.URL.RawPath != "" || (r.Header.Get("Origin") != "" && r.Header.Get("Origin") != origin) || (r.Header.Get("Sec-Fetch-Site") != "" && r.Header.Get("Sec-Fetch-Site") != "same-origin" && r.Header.Get("Sec-Fetch-Site") != "none" && !launchDocument) {
		fail(w, 403, "This app accepts only its private local origin")
		return
	}
	if r.URL.Path == "/" || r.URL.Path == "/app.css" || r.URL.Path == "/app.js" {
		if r.Method != "GET" && r.Method != "HEAD" {
			fail(w, 405, "Method unavailable")
			return
		}
		name := strings.TrimPrefix(r.URL.Path, "/")
		kind := "text/html; charset=utf-8"
		if name == "" {
			name = "index.html"
		}
		if name == "app.css" {
			kind = "text/css; charset=utf-8"
		}
		if name == "app.js" {
			kind = "application/javascript; charset=utf-8"
		}
		b, e := assets.ReadFile("assets/" + name)
		if e != nil {
			fail(w, 404, "Page unavailable")
			return
		}
		w.Header().Set("Content-Type", kind)
		if r.Method == "GET" {
			w.Write(b)
		}
		return
	}
	if r.URL.Path == "/api/reopen" {
		if r.Method != "POST" || r.Header.Get("Content-Type") != "application/json" {
			fail(w, 405, "Use the native reopen action")
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, 1024)
		var q struct {
			Challenge string `json:"challenge"`
			Proof     string `json:"proof"`
		}
		if decode(r, &q) != nil || len(q.Challenge) != 64 || !appinstance.Equal(q.Proof, appinstance.Proof(s.reopenKey, "request", q.Challenge)) {
			fail(w, 401, "Native reopen proof unavailable")
			return
		}
		var nonce [32]byte
		if _, err := rand.Read(nonce[:]); err != nil {
			fail(w, 500, "Launch randomness unavailable")
			return
		}
		s.mu.Lock()
		s.bootstrap = hex.EncodeToString(nonce[:])
		s.bootstrapExpires = time.Now().Add(time.Minute)
		link := "http://" + s.authority + "/#token=" + s.bootstrap
		s.mu.Unlock()
		writeJSON(w, map[string]string{"url": link, "proof": appinstance.Proof(s.reopenKey, "response", q.Challenge+"\n"+link)})
		return
	}
	if r.URL.Path == "/api/open" {
		if r.Method != "POST" || r.Header.Get("Content-Type") != "application/json" {
			fail(w, 405, "Use the app launch exchange")
			return
		}
		s.mu.Lock()
		valid := s.bootstrap != "" && time.Now().Before(s.bootstrapExpires) && subtle.ConstantTimeCompare([]byte(r.Header.Get("Authorization")), []byte("Bearer "+s.bootstrap)) == 1
		s.mu.Unlock()
		if !valid {
			fail(w, 401, "Launch link expired; reopen the app")
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, 64)
		if err := decode(r, &struct{}{}); err != nil {
			fail(w, 400, "Invalid launch exchange")
			return
		}
		s.mu.Lock()
		valid = s.bootstrap != "" && time.Now().Before(s.bootstrapExpires) && subtle.ConstantTimeCompare([]byte(r.Header.Get("Authorization")), []byte("Bearer "+s.bootstrap)) == 1
		if valid {
			s.bootstrap = ""
		}
		s.mu.Unlock()
		if !valid {
			fail(w, 401, "Launch link expired; use the existing app tab or restart the app")
			return
		}
		writeJSON(w, map[string]string{"token": s.token})
		return
	}
	if subtle.ConstantTimeCompare([]byte(r.Header.Get("Authorization")), []byte("Bearer "+s.token)) != 1 {
		fail(w, 401, "Open the app using its private launch link")
		return
	}
	if r.URL.Path == "/api/state" && r.Method == "GET" {
		writeJSON(w, s.state())
		return
	}
	if r.Method != "POST" {
		fail(w, 405, "Use an explicit app action")
		return
	}
	kind, _, e := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if e != nil || kind != "application/json" {
		fail(w, 400, "Expected JSON action")
		return
	}
	select {
	case s.action <- struct{}{}:
		defer func() { <-s.action }()
	default:
		fail(w, 409, "Another app action is still running")
		return
	}
	if err := s.ctx.Err(); err != nil {
		fail(w, 409, "App is closing")
		return
	}
	if err := s.actionRequest(w, r); err != nil {
		code := 400
		if errors.Is(err, framework.ErrBusy) || errors.Is(err, framework.ErrExists) {
			code = 409
		}
		if errors.Is(err, framework.ErrUnauthorized) {
			code = 403
		}
		fail(w, code, err.Error())
	}
}
func fail(w http.ResponseWriter, status int, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": message})
}
func writeJSON(w http.ResponseWriter, v any) {
	b, err := json.Marshal(v)
	if err != nil || len(b) > maxInput {
		fail(w, 500, "App snapshot exceeds its limit")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Write(b)
}

// decode rejects duplicate/case-aliased object keys before typed decoding. Raw
// catalog documents remain strings so their own strict decoder sees every key.
func decode(r *http.Request, v any) error {
	b, err := io.ReadAll(io.LimitReader(r.Body, maxInput+1))
	if err != nil || len(b) > maxInput || !utf8.Valid(b) {
		return errors.New("Action exceeds its input limit")
	}
	b = bytes.TrimSpace(b)
	if len(b) < 2 || b[0] != '{' || b[len(b)-1] != '}' {
		return errors.New("Expected an action object")
	}
	d := json.NewDecoder(bytes.NewReader(b))
	var value func(int) error
	value = func(depth int) error {
		if depth > 16 {
			return errors.New("Action nesting exceeds limit")
		}
		t, err := d.Token()
		if err != nil {
			return errors.New("Invalid JSON action")
		}
		delim, ok := t.(json.Delim)
		if !ok {
			return nil
		}
		switch delim {
		case '{':
			seen := map[string]bool{}
			for d.More() {
				key, e := d.Token()
				k, ok := key.(string)
				if e != nil || !ok || seen[k] || k != strings.ToLower(k) {
					return errors.New("Duplicate or invalid JSON field")
				}
				seen[k] = true
				if e = value(depth + 1); e != nil {
					return e
				}
			}
			end, e := d.Token()
			if e != nil || end != json.Delim('}') {
				return errors.New("Invalid JSON action")
			}
		case '[':
			for d.More() {
				if err = value(depth + 1); err != nil {
					return err
				}
			}
			end, e := d.Token()
			if e != nil || end != json.Delim(']') {
				return errors.New("Invalid JSON action")
			}
		default:
			return errors.New("Invalid JSON action")
		}
		return nil
	}
	if err = value(0); err != nil {
		return err
	}
	if _, err = d.Token(); err != io.EOF {
		return errors.New("Expected one JSON action")
	}
	d = json.NewDecoder(bytes.NewReader(b))
	d.DisallowUnknownFields()
	if d.Decode(v) != nil {
		return errors.New("Unknown or invalid action field")
	}
	return nil
}

type limitedListener struct {
	net.Listener
	slots chan struct{}
}
type limitedConn struct {
	net.Conn
	once    sync.Once
	release func()
}

func (l *limitedListener) Accept() (net.Conn, error) {
	for {
		c, e := l.Listener.Accept()
		if e != nil {
			return nil, e
		}
		select {
		case l.slots <- struct{}{}:
			return &limitedConn{Conn: c, release: func() { <-l.slots }}, nil
		default:
			c.Close()
		}
	}
}
func (c *limitedConn) Close() error { err := c.Conn.Close(); c.once.Do(c.release); return err }
