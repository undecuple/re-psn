package main

// EXPERIMENTAL admin API, read-only part. Off unless -admin is set. It has its own listener and its own mux, so the
// status page never gains new routes, and one token (the read token) that is read from a file. The only route is
// GET /admin/v1/state, which carries aggregate numbers and no address, player or token. There is no action yet; the
// state says so ("actions_enabled": false). See docs/admin-api.md.

import (
	"container/list"
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"os"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

const (
	adminMinToken     = 32   // bytes in the token file after trimming the line end
	adminMinDistinct  = 12   // distinct byte values: rejects "aaaa..." and short repeating patterns
	adminMaxTokenFile = 4096 // bytes
	adminConnCap      = 32   // concurrent connections on the admin listener
	adminPerSource    = 8    // of those, the most one source (IPv4 address or IPv6 /64) may hold
	adminTableSize    = 4096 // addresses remembered by the failure table
	adminBodyCap      = 4096 // bytes of request body
	adminMaxHeader    = 8 << 10
)

// adminConfig is what -admin and its companions say.
type adminConfig struct {
	Addr          string
	ReadTokenFile string
	TLSCert       string
	TLSKey        string
	StatusAddr    string // -http, which the admin address must differ from
	StatusToken   string // -http-token, which the read token must differ from
}

// adminServer is the experimental admin listener.
type adminServer struct {
	s      *Server
	tls    bool
	ln     net.Listener
	hs     *http.Server
	read   atomic.Pointer[[sha256.Size]byte]
	status *[sha256.Size]byte // digest of -http-token, which the read token must never equal
	path   string             // the read token file, read again on reload
	fail   *failTable
}

// loadAdminToken reads a token file and returns the SHA-256 of its token. The file must be a regular file of at most
// 4 KiB that group and others cannot read (on systems with Unix permissions), hold one token of at least 32 printable
// bytes without spaces and with at least 12 different byte values. Errors name the file and the rule, never the token.
func loadAdminToken(path string) ([sha256.Size]byte, error) {
	var zero [sha256.Size]byte
	// Look before opening: opening a FIFO would block until something writes to it.
	if li, err := os.Lstat(path); err != nil {
		return zero, fmt.Errorf("admin token file: %w", err)
	} else if li.Mode()&os.ModeNamedPipe != 0 {
		return zero, fmt.Errorf("admin token file %s is not a regular file", path)
	}
	f, err := os.Open(path)
	if err != nil {
		return zero, fmt.Errorf("admin token file: %w", err)
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		return zero, fmt.Errorf("admin token file %s: %w", path, err)
	}
	if !fi.Mode().IsRegular() {
		return zero, fmt.Errorf("admin token file %s is not a regular file", path)
	}
	if runtime.GOOS != "windows" && fi.Mode().Perm()&0o077 != 0 {
		return zero, fmt.Errorf("admin token file %s is readable by group or others: chmod 600 it (on a container mount, mount it with mode 0400)", path)
	}
	b, err := io.ReadAll(io.LimitReader(f, adminMaxTokenFile+1))
	if err != nil {
		return zero, fmt.Errorf("admin token file %s: %w", path, err)
	}
	if len(b) > adminMaxTokenFile {
		return zero, fmt.Errorf("admin token file %s is larger than %d bytes", path, adminMaxTokenFile)
	}
	tok := strings.TrimRight(string(b), "\r\n")
	if len(tok) < adminMinToken {
		return zero, fmt.Errorf("admin token file %s holds a token of %d bytes: at least %d are needed", path, len(tok), adminMinToken)
	}
	seen := map[byte]bool{}
	for i := 0; i < len(tok); i++ {
		if tok[i] <= ' ' || tok[i] > '~' {
			return zero, fmt.Errorf("admin token file %s: the token must be printable ASCII without spaces on one line", path)
		}
		seen[tok[i]] = true
	}
	if len(seen) < adminMinDistinct {
		return zero, fmt.Errorf("admin token file %s: the token has too few different characters to be random", path)
	}
	return sha256.Sum256([]byte(tok)), nil
}

// adminLoopback reports whether host is loopback: an IP literal that parses as loopback, or the name localhost (which
// the server binds as 127.0.0.1, so no name lookup decides what is loopback). An empty host and the unspecified
// addresses listen everywhere, so they are not.
func adminLoopback(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip, err := netip.ParseAddr(strings.Trim(host, "[]"))
	return err == nil && ip.Unmap().IsLoopback()
}

// sameListener reports whether two listen addresses could be the same socket: the same port with the same host, or
// with either host listening everywhere.
func sameListener(a, b string) bool {
	ha, pa, errA := net.SplitHostPort(a)
	hb, pb, errB := net.SplitHostPort(b)
	if errA != nil || errB != nil || pa != pb || pa == "0" {
		return false
	}
	wild := func(h string) bool {
		ip, err := netip.ParseAddr(h)
		return h == "" || (err == nil && ip.IsUnspecified())
	}
	if strings.EqualFold(ha, "localhost") {
		ha = "127.0.0.1" // newAdminServer binds it so
	}
	if strings.EqualFold(hb, "localhost") {
		hb = "127.0.0.1"
	}
	return wild(ha) || wild(hb) || strings.EqualFold(ha, hb)
}

// newAdminServer checks the settings, reads the token, loads the certificate and opens the listener, so every mistake
// stops the server at start.
func newAdminServer(s *Server, cfg adminConfig) (*adminServer, error) {
	host, port, err := net.SplitHostPort(cfg.Addr)
	if err != nil {
		return nil, fmt.Errorf("-admin %q: %w", cfg.Addr, err)
	}
	if port == "" {
		return nil, errors.New("-admin needs a port")
	}
	if cfg.ReadTokenFile == "" {
		return nil, errors.New("-admin needs -admin-read-token-file: no token, no admin listener")
	}
	if cfg.StatusAddr != "" && sameListener(cfg.Addr, cfg.StatusAddr) {
		return nil, errors.New("-admin must not be the same address as -http")
	}
	if (cfg.TLSCert == "") != (cfg.TLSKey == "") {
		return nil, errors.New("-admin-tls-cert and -admin-tls-key go together")
	}
	useTLS := cfg.TLSCert != ""
	if !useTLS && !adminLoopback(host) {
		return nil, errors.New("-admin on an address that is not loopback needs -admin-tls-cert and -admin-tls-key (loopback means an IP address such as 127.0.0.1 or ::1, or localhost; an empty host, 0.0.0.0, :: and other names are not)")
	}
	digest, err := loadAdminToken(cfg.ReadTokenFile)
	if err != nil {
		return nil, err
	}
	if cfg.StatusToken != "" && sha256.Sum256([]byte(cfg.StatusToken)) == digest {
		return nil, errors.New("the admin read token is the same as -http-token: use a separate token")
	}
	a := &adminServer{s: s, tls: useTLS, path: cfg.ReadTokenFile, fail: newFailTable(adminTableSize)}
	if cfg.StatusToken != "" {
		d := sha256.Sum256([]byte(cfg.StatusToken))
		a.status = &d
	}
	a.read.Store(&digest)
	a.hs = &http.Server{
		Handler:           a.handler(),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      10 * time.Second,
		IdleTimeout:       30 * time.Second,
		MaxHeaderBytes:    adminMaxHeader,
		TLSNextProto:      map[string]func(*http.Server, *tls.Conn, http.Handler){}, // HTTP/2 off
	}
	if useTLS {
		cert, err := tls.LoadX509KeyPair(cfg.TLSCert, cfg.TLSKey)
		if err != nil {
			return nil, fmt.Errorf("-admin-tls-cert and -admin-tls-key: %w", err)
		}
		a.hs.TLSConfig = &tls.Config{MinVersion: tls.VersionTLS12, Certificates: []tls.Certificate{cert}}
	}
	listen := cfg.Addr
	if strings.EqualFold(host, "localhost") {
		listen = net.JoinHostPort("127.0.0.1", port) // bind the address the loopback check assumed
	}
	ln, err := net.Listen("tcp", listen)
	if err != nil {
		return nil, err
	}
	a.ln = limitListener(ln, adminConnCap, adminPerSource)
	return a, nil
}

// Addr is the address the listener is bound to.
func (a *adminServer) Addr() string { return a.ln.Addr().String() }

// Serve runs the listener until Shutdown.
func (a *adminServer) Serve() error {
	if a.tls {
		return a.hs.ServeTLS(a.ln, "", "")
	}
	return a.hs.Serve(a.ln)
}

// Shutdown stops the listener.
func (a *adminServer) Shutdown(ctx context.Context) error {
	err := a.hs.Shutdown(ctx)
	if err != nil {
		a.hs.Close()
	}
	return err
}

// reload reads the token file again (SIGHUP). On any error the old token stays in force and the error is returned;
// on success the old token stops working at once.
func (a *adminServer) reload() error {
	digest, err := loadAdminToken(a.path)
	if err != nil {
		return err
	}
	if a.status != nil && *a.status == digest {
		return errors.New("the new admin read token is the same as -http-token: use a separate token")
	}
	a.read.Store(&digest)
	return nil
}

// remoteIP is the IP of the socket's peer. It never looks at a header.
func remoteIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return "unknown"
	}
	return host
}

// hostOK is the DNS-rebinding check. Over plain HTTP the Host header must be an IP literal or localhost, so a page
// that rebinds a name to this address is refused; over TLS a name must match the certificate, so any non-empty Host is
// accepted.
func (a *adminServer) hostOK(host string) bool {
	if host == "" {
		return false
	}
	if a.tls {
		return true
	}
	h := host
	if hh, _, err := net.SplitHostPort(host); err == nil {
		h = hh
	}
	if strings.EqualFold(h, "localhost") {
		return true
	}
	_, err := netip.ParseAddr(strings.Trim(h, "[]"))
	return err == nil
}

// authenticated reports whether the request carries the read token in an Authorization: Bearer header. The query
// string is never looked at. The SHA-256 digests are compared in constant time.
func (a *adminServer) authenticated(r *http.Request) bool {
	h := r.Header.Get("Authorization")
	if len(h) < 7 || !strings.EqualFold(h[:7], "Bearer ") {
		return false
	}
	tok := h[7:]
	got := sha256.Sum256([]byte(tok))
	want := a.read.Load()
	return subtle.ConstantTimeCompare(got[:], want[:]) == 1
}

// refuse answers 401 (or code) after the failure delay. The one log line carries the socket's remote address and a
// fixed reason, nothing a peer wrote, so it cannot be forged; the failure delay never applies to a valid token.
func (a *adminServer) refuse(w http.ResponseWriter, r *http.Request, code int, reason string) {
	delay := a.fail.record(remoteIP(r), time.Now())
	a.s.log.Warn("admin refused", "addr", remoteIP(r), "reason", reason)
	// A refused connection is not kept for the idle timeout: an unauthenticated client must not park on a slot.
	w.Header().Set("Connection", "close")
	if delay > 0 {
		t := time.NewTimer(delay)
		select {
		case <-t.C:
		case <-r.Context().Done():
			t.Stop()
		}
	}
	if code == http.StatusUnauthorized {
		w.Header().Set("WWW-Authenticate", "Bearer")
		http.Error(w, "unauthorized", code)
		return
	}
	http.Error(w, "forbidden", code)
}

func (a *adminServer) handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/admin/v1/state", a.state)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("Cache-Control", "no-store")
		h.Set("Referrer-Policy", "no-referrer")
		h.Set("X-Content-Type-Options", "nosniff")
		r.Body = http.MaxBytesReader(w, r.Body, adminBodyCap)
		// A browser page cannot reach this API: no CORS headers, no request with an Origin, no unknown Host.
		if _, ok := r.Header["Origin"]; ok || !a.hostOK(r.Host) {
			a.refuse(w, r, http.StatusForbidden, "origin or host refused")
			return
		}
		if !a.authenticated(r) {
			a.refuse(w, r, http.StatusUnauthorized, "token missing or wrong")
			return
		}
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			// The caller holds the token: no delay and no failure on its record, only a plain refusal.
			w.Header().Set("Allow", "GET, HEAD")
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		mux.ServeHTTP(w, r)
	})
}

// adminState is the document GET /admin/v1/state returns. Its fields are fixed for schema 1: a later change adds
// fields and keeps these. It holds aggregates only: no address, no player, no token.
type adminState struct {
	Schema         int          `json:"schema"`
	Version        string       `json:"version"`
	Uptime         string       `json:"uptime"`
	Relay          bool         `json:"relay"`
	RelayBytes     uint64       `json:"relay_bytes"`
	Limits         limitsJSON   `json:"limits"`
	TCPRefusals    refusalsJSON `json:"tcp_refusals"`
	UDPDrops       udpDropStats `json:"udp_drops"`
	Bans           adminBans    `json:"bans"`
	ActionsEnabled bool         `json:"actions_enabled"`
}

// adminBans is what the read token may know about bans: how many, and when the next one ends. Never an address.
type adminBans struct {
	Count             int  `json:"count"`
	NextExpirySeconds *int `json:"next_expiry_seconds"`
}

func (a *adminServer) state(w http.ResponseWriter, r *http.Request) {
	s := a.s
	s.mu.Lock()
	st := adminState{
		Schema:     1,
		Version:    version,
		Uptime:     time.Since(s.started).Round(time.Second).String(),
		Relay:      s.relay,
		RelayBytes: atomic.LoadUint64(&s.relayed),
		Limits: limitsJSON{MaxRooms: s.maxRooms, MaxPlayerRooms: s.maxPlayerRooms, MaxConnsPerAddr: s.maxPerAddr,
			MaxReqRate: s.maxReqRate, MaxUDPRate: s.maxUDPRate},
		TCPRefusals: refusalsJSON{ConnsPerAddr: atomic.LoadUint64(&s.refusedConns), ReqRate: atomic.LoadUint64(&s.refusedReqRate)},
		UDPDrops:    s.udpDrops.snapshot(),
	}
	s.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	if r.Method == http.MethodHead {
		return
	}
	json.NewEncoder(w).Encode(st)
}

// failTable remembers recent failures per address in a table of fixed size (least recently used goes first), and
// turns the count into a delay that doubles from base up to max. It never blocks anyone: the delay is the whole cost.
type failTable struct {
	mu    sync.Mutex
	size  int
	ll    *list.List
	m     map[string]*list.Element
	base  time.Duration
	max   time.Duration
	decay time.Duration
}

type failEntry struct {
	key  string
	n    int
	last time.Time
}

func newFailTable(size int) *failTable {
	return &failTable{size: size, ll: list.New(), m: map[string]*list.Element{},
		base: 100 * time.Millisecond, max: 5 * time.Second, decay: 10 * time.Minute}
}

// failKey is the table key: an IPv4 address, or an IPv6 address cut to its /64 (so rotating inside a /64 does not
// help); an IPv4-mapped IPv6 address is the IPv4 address. Anything that does not parse shares one key.
func failKey(host string) string {
	ip, err := netip.ParseAddr(host)
	if err != nil {
		return "unknown"
	}
	ip = ip.WithZone("").Unmap()
	if ip.Is6() {
		p, _ := ip.Prefix(64)
		return p.String()
	}
	return ip.String()
}

// record counts one failure for host at now and returns how long to wait before answering.
func (t *failTable) record(host string, now time.Time) time.Duration {
	key := failKey(host)
	t.mu.Lock()
	defer t.mu.Unlock()
	el, ok := t.m[key]
	if ok && now.Sub(el.Value.(*failEntry).last) > t.decay {
		el.Value.(*failEntry).n = 0
	}
	if !ok {
		el = t.ll.PushFront(&failEntry{key: key})
		t.m[key] = el
		for t.ll.Len() > t.size {
			old := t.ll.Back()
			t.ll.Remove(old)
			delete(t.m, old.Value.(*failEntry).key)
		}
	} else {
		t.ll.MoveToFront(el)
	}
	e := el.Value.(*failEntry)
	e.n++
	e.last = now
	d := t.base
	for i := 1; i < e.n && d < t.max; i++ {
		d *= 2
	}
	if d > t.max {
		d = t.max
	}
	return d
}

// limitListener allows at most n connections at once and at most perSource from one source (an IPv4 address or an
// IPv6 /64). A connection over a limit is closed at once, so Accept never waits and one address cannot fill the
// whole cap. Many addresses together still can for the few seconds a request may take, and every request over an SSH
// forward comes from one source; docs/admin-api.md says both.
func limitListener(l net.Listener, n, perSource int) net.Listener {
	return &limited{Listener: l, max: n, perSource: perSource, per: map[string]int{}}
}

type limited struct {
	net.Listener
	max, perSource int
	mu             sync.Mutex
	total          int
	per            map[string]int
}

func (l *limited) Accept() (net.Conn, error) {
	for {
		c, err := l.Listener.Accept()
		if err != nil {
			return nil, err
		}
		host, _, _ := net.SplitHostPort(c.RemoteAddr().String())
		key := failKey(host)
		l.mu.Lock()
		if l.total >= l.max || l.per[key] >= l.perSource {
			l.mu.Unlock()
			c.Close()
			continue
		}
		l.total++
		l.per[key]++
		l.mu.Unlock()
		return &limitedConn{Conn: c, release: func() {
			l.mu.Lock()
			l.total--
			if l.per[key]--; l.per[key] <= 0 {
				delete(l.per, key)
			}
			l.mu.Unlock()
		}}, nil
	}
}

type limitedConn struct {
	net.Conn
	once    sync.Once
	release func()
}

func (c *limitedConn) Close() error {
	c.once.Do(c.release)
	return c.Conn.Close()
}
