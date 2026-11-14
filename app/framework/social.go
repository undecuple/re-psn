package framework

import (
	"crypto/ed25519"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"
)

type SocialConfig struct {
	StateDir   string
	AllowedIDs []string
}
type socialAccount struct {
	Label    string          `json:"label"`
	Revoked  bool            `json:"revoked"`
	Friends  map[string]bool `json:"friends"`
	Incoming map[string]bool `json:"incoming"`
	Outgoing map[string]bool `json:"outgoing"`
	Blocked  map[string]bool `json:"blocked"`
}
type socialDisk struct {
	Version  int                       `json:"version"`
	Revision uint64                    `json:"revision"`
	Accounts map[string]*socialAccount `json:"accounts"`
}
type rateWindow struct {
	at time.Time
	n  int
}
type SocialService struct {
	mu        sync.Mutex
	dir       string
	lock      *os.File
	data      socialDisk
	allowed   map[string]bool
	rates     map[string]rateWindow
	global    rateWindow
	server    *http.Server
	closed    bool
	closeDone chan struct{}
}

func OpenSocialService(cfg SocialConfig) (*SocialService, error) {
	if len(cfg.AllowedIDs) > MaxAccounts {
		return nil, ErrInvalid
	}
	allowed := map[string]bool{}
	for _, id := range cfg.AllowedIDs {
		if !idOK(id) {
			return nil, ErrInvalid
		}
		allowed[id] = true
	}
	f, err := lockDir(cfg.StateDir, "social")
	if err != nil {
		return nil, err
	}
	s := &SocialService{dir: cfg.StateDir, lock: f, allowed: allowed, rates: map[string]rateWindow{}, closeDone: make(chan struct{}), data: socialDisk{Version: 1, Accounts: map[string]*socialAccount{}}}
	b, err := readFile(filepath.Join(cfg.StateDir, "social.json"))
	if err == nil {
		err = strictJSON(b, &s.data)
		if err == nil {
			err = validateSocialDisk(s.data)
		}
	} else if errors.Is(err, ErrNotFound) {
		err = s.commit(s.data)
	}
	if err != nil {
		unlockFile(f)
		return nil, err
	}
	return s, nil
}
func newAccount(label string) *socialAccount {
	return &socialAccount{Label: label, Friends: map[string]bool{}, Incoming: map[string]bool{}, Outgoing: map[string]bool{}, Blocked: map[string]bool{}}
}
func validateSocialDisk(d socialDisk) error {
	if d.Version != 1 || d.Accounts == nil || len(d.Accounts) > MaxAccounts {
		return ErrInvalid
	}
	for id, a := range d.Accounts {
		if !idOK(id) || a == nil || !textOK(a.Label, 128, false) {
			return ErrInvalid
		}
		for _, m := range []map[string]bool{a.Friends, a.Incoming, a.Outgoing, a.Blocked} {
			if m == nil || len(m) > MaxContacts {
				return ErrInvalid
			}
			for peer, on := range m {
				if !on || peer == id || d.Accounts[peer] == nil {
					return ErrInvalid
				}
			}
		}
		if a.Revoked && (len(a.Friends) != 0 || len(a.Incoming) != 0 || len(a.Outgoing) != 0) {
			return ErrInvalid
		}
		for peer := range a.Friends {
			p := d.Accounts[peer]
			if !p.Friends[id] || p.Revoked || a.Blocked[peer] || p.Blocked[id] || a.Incoming[peer] || a.Outgoing[peer] {
				return ErrInvalid
			}
		}
		for peer := range a.Outgoing {
			p := d.Accounts[peer]
			if !p.Incoming[id] || p.Revoked || a.Blocked[peer] || p.Blocked[id] || a.Incoming[peer] {
				return ErrInvalid
			}
		}
		for peer := range a.Incoming {
			if !d.Accounts[peer].Outgoing[id] {
				return ErrInvalid
			}
		}
	}
	return nil
}
func validSocialState(s SocialState) bool {
	if !idOK(s.AccountID) || !textOK(s.Label, 128, false) {
		return false
	}
	seen := map[string]bool{}
	for _, list := range [][]Contact{s.Friends, s.Incoming, s.Outgoing} {
		if len(list) > MaxContacts {
			return false
		}
		for _, p := range list {
			if !idOK(p.AccountID) || p.AccountID == s.AccountID || !textOK(p.Label, 128, false) || seen[p.AccountID] {
				return false
			}
			seen[p.AccountID] = true
		}
	}
	if len(s.Blocked) > MaxContacts {
		return false
	}
	for _, id := range s.Blocked {
		if !idOK(id) || id == s.AccountID || seen[id] {
			return false
		}
		seen[id] = true
	}
	return true
}
func (s *SocialService) commit(d socialDisk) error {
	if err := validateSocialDisk(d); err != nil {
		return err
	}
	b, err := json.Marshal(d)
	if err != nil || len(b) > MaxStateBytes {
		return ErrInvalid
	}
	replaced, err := atomicWrite(filepath.Join(s.dir, "social.json"), b)
	if replaced {
		s.data = d
	}
	return err
}
func (s *SocialService) RevokeAccount(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return ErrClosed
	}
	if s.data.Accounts[id] == nil {
		return ErrNotFound
	}
	d := clone(s.data)
	d.Accounts[id].Revoked = true
	for peer, a := range d.Accounts {
		delete(a.Friends, id)
		delete(a.Incoming, id)
		delete(a.Outgoing, id)
		if peer == id {
			clear(a.Friends)
			clear(a.Incoming)
			clear(a.Outgoing)
		}
	}
	d.Revision++
	return s.commit(d)
}

func clientIdentity(cs tls.ConnectionState) (string, error) {
	if cs.Version != tls.VersionTLS13 || len(cs.PeerCertificates) != 1 {
		return "", ErrUnauthorized
	}
	c := cs.PeerCertificates[0]
	pub, ok := c.PublicKey.(ed25519.PublicKey)
	now := time.Now()
	if !ok || len(pub) != ed25519.PublicKeySize || c.IsCA || now.Before(c.NotBefore) || !now.Before(c.NotAfter) || len(c.UnhandledCriticalExtensions) != 0 || len(c.UnknownExtKeyUsage) != 0 || c.KeyUsage != x509.KeyUsageDigitalSignature || len(c.ExtKeyUsage) != 1 || c.ExtKeyUsage[0] != x509.ExtKeyUsageClientAuth {
		return "", ErrUnauthorized
	}
	if err := c.CheckSignature(c.SignatureAlgorithm, c.RawTBSCertificate, c.Signature); err != nil {
		return "", ErrUnauthorized
	}
	return accountID(pub), nil
}
func (s *SocialService) ServeTLS(l net.Listener, certificate tls.Certificate) error {
	if l == nil {
		return ErrInvalid
	}
	defer l.Close()
	tc := &tls.Config{MinVersion: tls.VersionTLS13, MaxVersion: tls.VersionTLS13, ClientAuth: tls.RequireAnyClientCert, Certificates: []tls.Certificate{certificate}, NextProtos: []string{"http/1.1"}, VerifyConnection: func(cs tls.ConnectionState) error { _, err := clientIdentity(cs); return err }}
	// net/http's default TLS error logger includes peer IPs. Keep this library's
	// handshake failures out of host logs; ServeTLS still returns listener errors.
	h := &http.Server{Handler: http.HandlerFunc(s.handle), TLSConfig: tc, ReadHeaderTimeout: 3 * time.Second, ReadTimeout: 5 * time.Second, WriteTimeout: 5 * time.Second, IdleTimeout: 5 * time.Second, MaxHeaderBytes: 8192, ErrorLog: log.New(io.Discard, "", 0)}
	s.mu.Lock()
	if s.closed || s.server != nil {
		s.mu.Unlock()
		return ErrBusy
	}
	s.server = h
	s.mu.Unlock()
	err := h.Serve(tls.NewListener(&boundedListener{Listener: l, slots: make(chan struct{}, 32)}, tc))
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}

type boundedListener struct {
	net.Listener
	slots chan struct{}
}
type countedConn struct {
	net.Conn
	slots chan struct{}
	once  sync.Once
}

func (l *boundedListener) Accept() (net.Conn, error) {
	for {
		c, err := l.Listener.Accept()
		if err != nil {
			return nil, err
		}
		select {
		case l.slots <- struct{}{}:
			return &countedConn{Conn: c, slots: l.slots}, nil
		default:
			c.Close()
		}
	}
}
func (c *countedConn) Close() error {
	err := c.Conn.Close()
	c.once.Do(func() { <-c.slots })
	return err
}
func (s *SocialService) Close() error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		<-s.closeDone
		return nil
	}
	s.closed = true
	h := s.server
	s.mu.Unlock()
	if h != nil {
		h.Close()
	}
	s.mu.Lock()
	// Every mutation takes mu and checks closed. No handler can mutate after
	// this point even though net/http Close does not wait for handler returns.
	err := unlockFile(s.lock)
	s.mu.Unlock()
	close(s.closeDone)
	return err
}
func (s *SocialService) eligible(d socialDisk, id string) bool {
	a := d.Accounts[id]
	return s.allowed[id] && a != nil && !a.Revoked
}
func (s *SocialService) state(d socialDisk, id string) SocialState {
	a := d.Accounts[id]
	v := SocialState{AccountID: id, Label: a.Label, Revision: d.Revision}
	for _, pair := range []struct {
		m   map[string]bool
		dst *[]Contact
	}{{a.Friends, &v.Friends}, {a.Incoming, &v.Incoming}, {a.Outgoing, &v.Outgoing}} {
		for peer := range pair.m {
			if s.eligible(d, peer) {
				*pair.dst = append(*pair.dst, Contact{AccountID: peer, Label: d.Accounts[peer].Label})
			}
		}
		sort.Slice(*pair.dst, func(i, j int) bool { return (*pair.dst)[i].AccountID < (*pair.dst)[j].AccountID })
	}
	for peer := range a.Blocked {
		v.Blocked = append(v.Blocked, peer)
	}
	sort.Strings(v.Blocked)
	return v
}

type socialRequest struct {
	AccountID string `json:"account_id"`
	Label     string `json:"label"`
}

func (s *SocialService) handle(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	fail := func(code int) { http.Error(w, "request unavailable", code) }
	if r.TLS == nil || r.URL.RawQuery != "" || r.Header.Get("Origin") != "" {
		fail(http.StatusForbidden)
		return
	}
	id, err := clientIdentity(*r.TLS)
	if err != nil {
		fail(http.StatusForbidden)
		return
	}
	op := ""
	switch r.URL.Path {
	case "/v1/register":
		op = "register"
	case "/v1/state":
		op = "state"
	case "/v1/request":
		op = "request"
	case "/v1/accept":
		op = "accept"
	case "/v1/decline":
		op = "decline"
	case "/v1/cancel":
		op = "cancel"
	case "/v1/remove":
		op = "remove"
	case "/v1/block":
		op = "block"
	case "/v1/unblock":
		op = "unblock"
	default:
		fail(404)
		return
	}
	var q socialRequest
	if op == "state" {
		if r.Method != http.MethodGet || r.ContentLength != 0 || len(r.TransferEncoding) != 0 {
			fail(400)
			return
		}
	} else {
		if r.Method != http.MethodPost || r.Header.Get("Content-Type") != "application/json" {
			fail(400)
			return
		}
		b, err := boundedRead(r.Body, 1024)
		if err != nil || strictJSON(b, &q) != nil {
			fail(400)
			return
		}
	}
	v, code := s.operation(id, op, q)
	if code != 0 {
		fail(code)
		return
	}
	s.writeState(w, v)
}

// operation linearizes authorization and persistence without holding a lock during network writes.
func (s *SocialService) operation(id, op string, q socialRequest) (SocialState, int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed || !s.allowed[id] || (s.data.Accounts[id] != nil && s.data.Accounts[id].Revoked) {
		return SocialState{}, 403
	}
	now := time.Now()
	if now.Sub(s.global.at) >= time.Second {
		s.global = rateWindow{at: now}
	}
	rw := s.rates[id]
	if now.Sub(rw.at) >= time.Second {
		rw = rateWindow{at: now}
	}
	if s.global.n >= 200 || rw.n >= 40 {
		return SocialState{}, 429
	}
	s.global.n++
	rw.n++
	s.rates[id] = rw
	if op == "state" {
		if !s.eligible(s.data, id) {
			return SocialState{}, 403
		}
		return s.state(s.data, id), 0
	}
	d := clone(s.data)
	if op == "register" {
		if q.AccountID != "" || !textOK(q.Label, 128, false) {
			return SocialState{}, 400
		}
		if d.Accounts[id] == nil {
			if len(d.Accounts) >= MaxAccounts {
				return SocialState{}, 409
			}
			d.Accounts[id] = newAccount(q.Label)
		} else {
			d.Accounts[id].Label = q.Label
		}
	} else {
		if !s.eligible(d, id) || !idOK(q.AccountID) || q.AccountID == id || q.Label != "" {
			return SocialState{}, 403
		}
		a, p := d.Accounts[id], d.Accounts[q.AccountID]
		peer := q.AccountID
		// Owners can remove their own existing references even after a host
		// revokes/disallows the peer. Cleanup never creates consent or access.
		cleanup := op == "unblock" && a.Blocked[peer] ||
			op == "remove" && (a.Friends[peer] || a.Incoming[peer] || a.Outgoing[peer]) ||
			op == "decline" && a.Incoming[peer] || op == "cancel" && a.Outgoing[peer]
		if p == nil || (!s.eligible(d, peer) && !cleanup) {
			return SocialState{}, 403
		}
		switch op {
		case "request":
			if a.Blocked[peer] || p.Blocked[id] {
				return SocialState{}, 403
			}
			if a.Friends[peer] || a.Outgoing[peer] {
				break
			}
			if a.Incoming[peer] || len(a.Outgoing) >= MaxContacts || len(p.Incoming) >= MaxContacts {
				return SocialState{}, 409
			}
			a.Outgoing[peer] = true
			p.Incoming[id] = true
		case "accept":
			if !a.Incoming[peer] || a.Blocked[peer] || p.Blocked[id] {
				return SocialState{}, 403
			}
			if len(a.Friends) >= MaxContacts || len(p.Friends) >= MaxContacts {
				return SocialState{}, 409
			}
			delete(a.Incoming, peer)
			delete(p.Outgoing, id)
			a.Friends[peer] = true
			p.Friends[id] = true
		case "decline":
			if !a.Incoming[peer] {
				return SocialState{}, 403
			}
			delete(a.Incoming, peer)
			delete(p.Outgoing, id)
		case "cancel":
			if !a.Outgoing[peer] {
				return SocialState{}, 403
			}
			delete(a.Outgoing, peer)
			delete(p.Incoming, id)
		case "remove":
			removeEdges(a, p, id, peer)
		case "block":
			if !a.Blocked[peer] && len(a.Blocked) >= MaxContacts {
				return SocialState{}, 409
			}
			removeEdges(a, p, id, peer)
			a.Blocked[peer] = true
		case "unblock":
			delete(a.Blocked, peer)
		}
	}
	d.Revision++
	if err := s.commit(d); err != nil {
		return SocialState{}, 503
	}
	return s.state(s.data, id), 0
}
func removeEdges(a, p *socialAccount, id, peer string) {
	delete(a.Friends, peer)
	delete(p.Friends, id)
	delete(a.Incoming, peer)
	delete(p.Outgoing, id)
	delete(a.Outgoing, peer)
	delete(p.Incoming, id)
}
func (s *SocialService) writeState(w http.ResponseWriter, v SocialState) {
	b, err := json.Marshal(v)
	if err != nil || len(b) > 128<<10 {
		http.Error(w, "request unavailable", 503)
		return
	}
	_, _ = w.Write(b)
}
