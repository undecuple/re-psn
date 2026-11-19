package main

// Sign-in passes. A host can require that a player holds a pass signed by a login service the host trusts before the
// relay carries anything for them. The server never talks to a provider and holds no provider secret: it checks an
// Ed25519 signature offline against public keys from a file. A pass is a bearer token sent over the game's plain TCP, so
// anyone who can read the connection can use it until it expires; it is short-lived for that reason and proves only that
// a provider account signed in, never who the player is or what name they use.
//
//	AUTH      req u32 | version u8 (1) | pass blob (at most 2048 bytes)       (after HELLO; type 0x05)
//	OK        req u32 [| token u32]                                           the pass is accepted; the token only with -require-pass connect
//	ERROR 10  req u32 | code u32                                              the pass is refused; nothing changed
//
// With -require-pass connect the connection is not a player until AUTH is accepted. HELLO only parks its fields on the
// connection: it reserves no name, makes no token (HELLO_ACK carries 0) and leaves the connection out of every list, room and
// UDP lookup. Until then only AUTH, HEARTBEAT and GET_CAPS (pass bits only) are answered; anything else gets ERROR 10. AUTH
// success registers the player, as HELLO does without the setting, and replies OK with the real UDP token. The connection
// must sign in within a fixed time of accept, no HEARTBEAT extends it, and it is closed passSkew seconds after the pass
// expires unless a refresh AUTH arrived. A relay stream that is already open ends the same way (relay.go): when either end's
// pass has run out plus the skew, when its key leaves the key file, or when its account is revoked.
//
// A pass is "v1." + base64url(payload) + "." + base64url(signature) with no padding. The signature is Ed25519 over the
// ASCII bytes "v1." + base64url(payload) as sent, so nothing is canonicalised. The payload is compact JSON with exactly the
// fields below, and a field that is missing or unknown refuses the pass. Field names are matched as Go's JSON decoder matches them; a pass is only ever made by the host's own issuer.
//
//	v     "v1"
//	kid   the key in the key file that signed it
//	iss   the issuer the host configured
//	aud   the audience the host configured, exactly
//	sub   "github:<number>" or "discord:<number>"
//	scope an array of "connect" and "relay"
//	iat, exp   whole seconds; exp > iat and exp - iat at most 3600

import (
	"bufio"
	"crypto/ed25519"
	"crypto/hmac"
	crand "crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"runtime"
	"strings"
	"time"
)

const (
	msgAuth = 0x05

	errPass = 10 // AUTH: the pass is missing, invalid, expired, for another server or not allowed here

	passVersion = 1 // AUTH body version: 1 is a bearer pass; 2 is reserved for a pass bound to a client key

	maxPassLen = 2048
	// maxUnauthFrame is the largest payload read before a connection has signed in under -require-pass connect: the request
	// id, the version, the blob length and a pass of maxPassLen. Checked on the frame header, before the body is read.
	maxUnauthFrame = 4 + 1 + 2 + maxPassLen
	// defaultAuthTimeout is how long after accept a connection has to sign in under -require-pass connect.
	defaultAuthTimeout = 30 * time.Second
	maxPassLife        = 3600 // seconds
	passSkew           = 60   // seconds of clock difference tolerated on iat and exp
	maxPassKeys        = 16
	maxKeyFile         = 8 << 10
	maxRevokeLen       = 1 << 20

	scopeConnect = 1 << 0
	scopeRelay   = 1 << 1
)

// passConfig is what the host set. A reload replaces keys and revoked; the rest is fixed for the process.
type passConfig struct {
	keys    map[string]ed25519.PublicKey
	revoked map[string]bool
	issuer  string
	aud     string
}

// A passClaims value is a pass that passed every check.
type passClaims struct {
	kid    string
	sub    string
	scopes uint8
	exp    int64
}

type passPayload struct {
	V     string          `json:"v"`
	Kid   string          `json:"kid"`
	Iss   string          `json:"iss"`
	Aud   string          `json:"aud"`
	Sub   string          `json:"sub"`
	Scope []string        `json:"scope"`
	Iat   int64           `json:"iat"`
	Exp   int64           `json:"exp"`
	Cnf   json.RawMessage `json:"cnf"` // a key-bound pass: a bearer-only server refuses it rather than ignore the binding
}

var errBadPass = errors.New("pass refused")

// checkPass verifies pass at now. The error says only that it was refused; the reason is for the log, via why.
func (p *passConfig) checkPass(pass []byte, now time.Time) (passClaims, string, error) {
	fail := func(why string) (passClaims, string, error) { return passClaims{}, why, errBadPass }
	if len(pass) > maxPassLen {
		return fail("too long")
	}
	if strings.ContainsAny(string(pass), "\r\n") { // the base64 decoder would skip them: one spelling only
		return fail("format")
	}
	parts := strings.Split(string(pass), ".")
	if len(parts) != 3 || parts[0] != "v1" {
		return fail("format")
	}
	payload, err := base64.RawURLEncoding.Strict().DecodeString(parts[1])
	if err != nil {
		return fail("format")
	}
	sig, err := base64.RawURLEncoding.Strict().DecodeString(parts[2])
	if err != nil || len(sig) != ed25519.SignatureSize {
		return fail("format")
	}
	dec := json.NewDecoder(strings.NewReader(string(payload)))
	dec.DisallowUnknownFields()
	var pl passPayload
	if err := dec.Decode(&pl); err != nil {
		return fail("payload")
	}
	if _, err := dec.Token(); err != io.EOF {
		return fail("payload")
	}
	key, ok := p.keys[pl.Kid]
	if !ok || pl.V != "v1" {
		return fail("key or version")
	}
	if !ed25519.Verify(key, []byte(parts[0]+"."+parts[1]), sig) {
		return fail("signature")
	}
	if len(pl.Cnf) != 0 {
		return fail("bound pass on a bearer-only server")
	}
	if p.issuer == "" || pl.Iss != p.issuer {
		return fail("issuer")
	}
	if p.aud == "" || pl.Aud != p.aud {
		return fail("audience")
	}
	if !validSub(pl.Sub) {
		return fail("subject")
	}
	if pl.Iat < 0 || pl.Iat > 1<<40 || pl.Exp <= pl.Iat || pl.Exp > pl.Iat+maxPassLife { // iat is not negative, so the sum cannot overflow below 2^62
		return fail("lifetime")
	}
	t := now.Unix()
	if t+passSkew < pl.Iat {
		return fail("not yet valid")
	}
	if t-passSkew >= pl.Exp {
		return fail("expired")
	}
	var scopes uint8
	for _, s := range pl.Scope {
		bit := uint8(0)
		switch s {
		case "connect":
			bit = scopeConnect
		case "relay":
			bit = scopeRelay
		}
		if bit == 0 || scopes&bit != 0 {
			return fail("scope")
		}
		scopes |= bit
	}
	if scopes == 0 {
		return fail("scope")
	}
	if p.revoked[pl.Sub] {
		return fail("revoked")
	}
	return passClaims{pl.Kid, pl.Sub, scopes, pl.Exp}, "", nil
}

// validSub accepts "github:" or "discord:" followed by 1 to 19 digits with no leading zero and nothing else, so one account has
// one spelling and a revoke entry or a connection count cannot be dodged by another.
func validSub(s string) bool {
	_, num, ok := strings.Cut(s, ":")
	if !ok || (s[:len(s)-len(num)-1] != "github" && s[:len(s)-len(num)-1] != "discord") || len(num) < 1 || len(num) > 19 || num[0] == '0' {
		return false
	}
	for i := 0; i < len(num); i++ {
		if num[i] < '0' || num[i] > '9' {
			return false
		}
	}
	return true
}

// subTagKey keys the log tag of an account. It is made at start and never stored or logged, so a tag cannot be reversed by
// hashing guessed account numbers, and means nothing in another process's log or after a restart.
var subTagKey = func() []byte {
	k := make([]byte, 32)
	if _, err := crand.Read(k); err != nil {
		panic(err)
	}
	return k
}()

// subTag is a short keyed hash of an account for the log, so a line can be matched to a player within one run without
// naming their account.
func subTag(sub string) string { return subTagWith(subTagKey, sub) }

func subTagWith(key []byte, sub string) string {
	m := hmac.New(sha256.New, key)
	m.Write([]byte(sub))
	return hex.EncodeToString(m.Sum(nil)[:4])
}

// openPassFile opens path for reading after the checks every secret-adjacent file gets: a regular file, at most max
// bytes, not readable by group or others on systems with Unix permissions. Errors name the file, never its content.
func openPassFile(what, path string, max int64) ([]byte, error) {
	li, err := os.Stat(path) // follows a symlink, so a link to a FIFO is seen before open would block on it
	if err != nil {
		return nil, fmt.Errorf("%s: %w", what, err)
	}
	if !li.Mode().IsRegular() { // a FIFO or a device (also behind a symlink) would block in open
		return nil, fmt.Errorf("%s %s is not a regular file", what, path)
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", what, err)
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		return nil, fmt.Errorf("%s %s: %w", what, path, err)
	}
	if !fi.Mode().IsRegular() {
		return nil, fmt.Errorf("%s %s is not a regular file", what, path)
	}
	if runtime.GOOS != "windows" && fi.Mode().Perm()&0o077 != 0 {
		return nil, fmt.Errorf("%s %s is readable by group or others: chmod 600 it (on a container mount, mount it with mode 0400)", what, path)
	}
	b, err := io.ReadAll(io.LimitReader(f, max+1))
	if err != nil {
		return nil, fmt.Errorf("%s %s: %w", what, path, err)
	}
	if int64(len(b)) > max {
		return nil, fmt.Errorf("%s %s is larger than %d bytes", what, path, max)
	}
	return b, nil
}

// loadPassKeys reads the key file: one key per line, "<kid> <base64url of the 32-byte Ed25519 public key>", blank lines
// and lines starting with # ignored. A kid is 1 to 32 characters from letters, digits, dot, dash and underscore, and is
// never listed twice. A kid is never reused for a different key; the host keeps retired kids out of the file.
func loadPassKeys(path string) (map[string]ed25519.PublicKey, error) {
	b, err := openPassFile("pass key file", path, maxKeyFile)
	if err != nil {
		return nil, err
	}
	keys := map[string]ed25519.PublicKey{}
	sc := bufio.NewScanner(strings.NewReader(string(b)))
	for n := 1; sc.Scan(); n++ {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		kid, enc, ok := strings.Cut(line, " ")
		enc = strings.TrimSpace(enc)
		if !ok || !validKid(kid) {
			return nil, fmt.Errorf("pass key file %s line %d: want <kid> <base64url key>", path, n)
		}
		raw, err := base64.RawURLEncoding.Strict().DecodeString(enc)
		if err != nil || len(raw) != ed25519.PublicKeySize {
			return nil, fmt.Errorf("pass key file %s line %d: the key must be %d bytes in base64url without padding", path, n, ed25519.PublicKeySize)
		}
		if _, dup := keys[kid]; dup {
			return nil, fmt.Errorf("pass key file %s line %d: kid listed twice", path, n)
		}
		if len(keys) == maxPassKeys {
			return nil, fmt.Errorf("pass key file %s: more than %d keys", path, maxPassKeys)
		}
		keys[kid] = ed25519.PublicKey(raw)
	}
	if len(keys) == 0 {
		return nil, fmt.Errorf("pass key file %s holds no key", path)
	}
	return keys, nil
}

func validKid(k string) bool {
	if len(k) < 1 || len(k) > 32 {
		return false
	}
	for i := 0; i < len(k); i++ {
		c := k[i]
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '.' || c == '-' || c == '_') {
			return false
		}
	}
	return true
}

// loadPassRevoked reads the revoke file: one subject per line, blank lines and # lines ignored.
func loadPassRevoked(path string) (map[string]bool, error) {
	b, err := openPassFile("pass revoke file", path, maxRevokeLen)
	if err != nil {
		return nil, err
	}
	out := map[string]bool{}
	for n, line := range strings.Split(string(b), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if !validSub(line) {
			return nil, fmt.Errorf("pass revoke file %s line %d: want github:<number> or discord:<number>", path, n+1)
		}
		out[line] = true
	}
	return out, nil
}

// parseRequirePass reads -require-pass: a comma list of scopes, "connect" and "relay".
func parseRequirePass(list string) (connect, relay bool, err error) {
	for _, f := range strings.Split(list, ",") {
		switch strings.TrimSpace(f) {
		case "":
		case "relay":
			relay = true
		case "connect":
			connect = true
		default:
			return false, false, fmt.Errorf("-require-pass: unknown scope %q (relay, connect)", strings.TrimSpace(f))
		}
	}
	return connect, relay, nil
}

// passAllows reports whether c holds a pass with scope that has not run out at now. The caller holds s.mu.
func (c *client) passAllows(scope uint8, now time.Time) bool {
	return c.passScopes&scope != 0 && now.Unix()-passSkew < c.passExp
}

// maxAuthFails is how many refused AUTH requests a connection gets before it is closed.
const maxAuthFails = 3

// handleAuth handles AUTH. The pass is parsed and verified before s.mu is taken, so a stream of bad passes costs the game's
// other requests nothing; s.mu is held only to record the result. A second AUTH on a connection is a refresh: it must name
// the same account and replaces the scopes and expiry of the first. Under -require-pass connect the first AUTH is also the
// registration of the player (name check, takeover, token), and every pass must carry scope connect.
func (s *Server) handleAuth(c *client, req uint32, r *reader) {
	p := s.pass.Load()
	if p == nil {
		c.send(msgError, errReply(req, errBadRequest)) // no pass settings: as a server from before AUTH answers
		return
	}
	var cl passClaims
	why := "request"
	ver, pass := r.u8(), r.blob()
	ok := r.err == nil && len(r.b) == 0 && ver == passVersion
	if ok {
		var err error
		if cl, why, err = p.checkPass(pass, time.Now()); err != nil {
			ok = false
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if c.outputClosed() {
		return
	}
	if s.requireConnectPass && c.sub == "" && !c.helloSeen { // taken over since handle looked: no longer a player to sign in
		c.send(msgError, errReply(req, errNoHello))
		return
	}
	refuse := func(code uint32) {
		c.send(msgError, errReply(req, code))
		if c.authFails++; c.authFails >= maxAuthFails {
			s.log.Warn("connection closed", "client", c.id, "reason", "too many refused passes")
			go c.conn.Close()
		}
	}
	deny := func(why string) {
		s.log.Warn("pass refused", "client", c.id, "reason", why)
		refuse(errPass)
	}
	if !ok {
		deny(why)
		return
	}
	if s.requireConnectPass && cl.scopes&scopeConnect == 0 { // also on a refresh, so it cannot drop the connect scope
		deny("scope")
		return
	}
	if np := s.pass.Load(); np != p && (np.revoked[cl.sub] || np.keys[cl.kid] == nil) { // a reload since the check
		deny("revoked or key removed")
		return
	}
	if c.sub != "" && c.sub != cl.sub {
		deny("other subject")
		return
	}
	if c.sub == "" {
		var victims []*client
		if s.requireConnectPass {
			var taken bool
			if victims, taken = s.nameConflict(c, c.pendOnline); taken {
				s.log.Warn("hello refused", "client", c.id, "reason", "name taken", "online_id", c.pendOnline, "addr", c.addr.String())
				refuse(errNameTaken)
				return
			}
		}
		n := s.subConns[cl.sub]
		for _, v := range victims {
			if v.sub == cl.sub { // the connection this one replaces gives its slot up
				n--
			}
		}
		if s.maxPerSub > 0 && n >= s.maxPerSub {
			deny("too many connections for one account")
			return
		}
		if s.requireConnectPass { // every check has passed: register
			s.takeOver(victims, c)
			c.commID, c.onlineID, c.p2pPort = c.pendComm, c.pendOnline, c.pendPort
			c.pendComm, c.pendOnline, c.pendPort, c.helloSeen = "", "", 0, false
			s.newToken(c)
			s.clients[c.id] = c
			c.authed = true
			s.releaseUnauth(c)
			s.log.Info("hello", "client", c.id, "comm_id", c.commID, "online_id", c.onlineID, "addr", c.addr.String(), "p2p_port", c.p2pPort)
		}
		s.subConns[cl.sub]++
		c.sub = cl.sub
	}
	c.passScopes, c.passExp, c.passKid = cl.scopes, cl.exp, cl.kid
	s.rearmStreams(c)
	if s.requireConnectPass {
		c.gateUntil = time.Unix(cl.exp+passSkew, 0) // the run loop reads this on this same goroutine
	}
	c.authFails = 0
	s.log.Info("pass accepted", "client", c.id, "sub", subTag(cl.sub), "scopes", cl.scopes)
	w := &writer{}
	w.u32(req)
	if s.requireConnectPass {
		w.u32(c.token)
	}
	c.send(msgOK, w.b)
}

// dropSub forgets c's subject when it disconnects. The caller holds s.mu.
func (s *Server) dropSub(c *client) {
	if c.sub == "" {
		return
	}
	if s.subConns[c.sub]--; s.subConns[c.sub] <= 0 {
		delete(s.subConns, c.sub)
	}
	c.sub, c.passScopes, c.passExp, c.passKid = "", 0, 0, ""
}

// reloadPass reads the key and revoke files again (SIGHUP). On any error the old settings stay and the error is
// returned. Live connections of a subject now revoked are closed.
func (s *Server) reloadPass(keyFile, revokedFile string) error {
	old := s.pass.Load()
	if old == nil {
		return nil
	}
	keys, err := loadPassKeys(keyFile)
	if err != nil {
		return err
	}
	var revoked map[string]bool
	if revokedFile != "" {
		if revoked, err = loadPassRevoked(revokedFile); err != nil {
			return err
		}
	}
	np := &passConfig{keys: keys, revoked: revoked, issuer: old.issuer, aud: old.aud}
	s.pass.Store(np)
	var closing []*client
	removed := map[*client]bool{} // closing because the key went, not because the account was revoked
	s.mu.Lock()
	for _, c := range s.clients {
		if c.sub != "" && revoked[c.sub] {
			c.passScopes = 0 // no relay in the moment before its run loop notices the close
			closing = append(closing, c)
		} else if c.sub != "" && keys[c.passKid] == nil {
			c.passScopes = 0          // its key was taken out of the file: the pass no longer counts, a new one is needed
			if s.requireConnectPass { // and with connect required it was the way in: the connection goes too
				closing = append(closing, c)
				removed[c] = true
			}
		}
	}
	type cutLeg struct {
		ls     *liveStream
		conns  []net.Conn
		reason string
	}
	var cutLegs []cutLeg
	now := time.Now()
	for ls := range s.live { // the same rule as for the connections above, and for a pass that has run out
		if reason, _ := s.streamCutReason(ls, np, now); reason != "" {
			cutLegs = append(cutLegs, cutLeg{ls, s.cutStream(ls), reason})
		}
	}
	s.mu.Unlock()
	for _, cl := range cutLegs {
		s.closeStream(cl.ls, cl.conns, cl.reason)
	}
	for _, c := range closing {
		reason := "pass revoked"
		if removed[c] {
			reason = "pass key removed"
		}
		s.log.Warn("connection closed", "client", c.id, "reason", reason)
		c.conn.Close()
	}
	return nil
}
