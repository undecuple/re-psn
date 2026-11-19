package main

import (
	"crypto/ed25519"
	crand "crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

const (
	testIssuer = "login.test"
	testAud    = "server.test"
)

var b64 = base64.RawURLEncoding

// passTool makes throwaway keys and passes; nothing here is a real key.
type passTool struct {
	t    *testing.T
	priv ed25519.PrivateKey
	pub  ed25519.PublicKey
	cfg  *passConfig
}

func newPassTool(t *testing.T) *passTool {
	pub, priv, err := ed25519.GenerateKey(crand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return &passTool{t, priv, pub, &passConfig{
		keys:    map[string]ed25519.PublicKey{"k1": pub},
		revoked: map[string]bool{},
		issuer:  testIssuer,
		aud:     testAud,
	}}
}

// claims is a valid payload at now; a test changes the field it is about.
func claims(now time.Time) map[string]any {
	return map[string]any{
		"v": "v1", "kid": "k1", "iss": testIssuer, "aud": testAud, "sub": "github:123",
		"scope": []string{"relay"}, "iat": now.Unix(), "exp": now.Add(time.Hour).Unix(),
	}
}

func (p *passTool) sign(payload []byte) string {
	head := "v1." + b64.EncodeToString(payload)
	return head + "." + b64.EncodeToString(ed25519.Sign(p.priv, []byte(head)))
}

func (p *passTool) pass(c map[string]any) string {
	b, err := json.Marshal(c)
	if err != nil {
		p.t.Fatal(err)
	}
	return p.sign(b)
}

func (p *passTool) good(now time.Time) string { return p.pass(claims(now)) }

func TestCheckPassAcceptsAGoodPass(t *testing.T) {
	p, now := newPassTool(t), time.Now()
	cl, why, err := p.cfg.checkPass([]byte(p.good(now)), now)
	if err != nil {
		t.Fatalf("refused: %s", why)
	}
	if cl.sub != "github:123" || cl.scopes != scopeRelay || cl.exp != now.Add(time.Hour).Unix() {
		t.Fatalf("claims = %+v", cl)
	}
	c := claims(now)
	c["scope"] = []string{"connect", "relay"}
	c["sub"] = "discord:1844674407370955161"
	if cl, why, err = p.cfg.checkPass([]byte(p.pass(c)), now); err != nil || cl.scopes != scopeConnect|scopeRelay {
		t.Fatalf("both scopes: %v %s %+v", err, why, cl)
	}
}

func TestCheckPassRefusals(t *testing.T) {
	now := time.Now()
	_, otherPriv, _ := ed25519.GenerateKey(crand.Reader)
	edit := func(f func(map[string]any)) func(*passTool) string {
		return func(p *passTool) string {
			c := claims(now)
			f(c)
			return p.pass(c)
		}
	}
	drop := func(k string) func(*passTool) string {
		return edit(func(c map[string]any) { delete(c, k) })
	}
	set := func(k string, v any) func(*passTool) string {
		return edit(func(c map[string]any) { c[k] = v })
	}
	raw := func(s string) func(*passTool) string { return func(p *passTool) string { return p.sign([]byte(s)) } }
	// reasons names the refusals whose cause matters: the test fails if one is refused for another reason.
	reasons := map[string]string{"expired": "expired", "not yet valid": "not yet valid", "lifetime over an hour": "lifetime",
		"overflowing lifetime": "lifetime", "iat at the top of int64": "lifetime", "iat beyond 2^40": "lifetime", "negative iat": "lifetime", "wrong audience": "audience", "wrong issuer": "issuer",
		"unknown kid": "key or version", "unknown scope": "scope", "repeated scope": "scope", "bound pass on a bearer-only server": "bound pass on a bearer-only server",
		"sub with a leading zero": "subject", "tampered payload": "signature", "signed by another key": "signature"}
	cases := []struct {
		name string
		make func(*passTool) string
	}{
		{"expired", edit(func(c map[string]any) {
			c["iat"] = now.Add(-time.Hour).Unix()
			c["exp"] = now.Add(-2 * time.Minute).Unix()
		})},
		{"not yet valid", edit(func(c map[string]any) {
			c["iat"] = now.Add(2 * time.Minute).Unix()
			c["exp"] = now.Add(10 * time.Minute).Unix()
		})},
		{"wrong audience", set("aud", "another.server")},
		{"empty audience", set("aud", "")},
		{"wrong issuer", set("iss", "evil.test")},
		{"unknown kid", set("kid", "k9")},
		{"missing kid", drop("kid")},
		{"version in payload", set("v", "v2")},
		{"missing version", drop("v")},
		{"unknown scope", set("scope", []string{"admin"})},
		{"empty scope", set("scope", []string{})},
		{"scope not an array", set("scope", "relay")},
		{"repeated scope", set("scope", []string{"relay", "relay"})},
		{"missing scope", drop("scope")},
		{"missing sub", drop("sub")},
		{"sub without provider", set("sub", "123")},
		{"sub of another provider", set("sub", "reddit:123")},
		{"sub with a name", set("sub", "github:alice")},
		{"sub with empty number", set("sub", "github:")},
		{"sub number too long", set("sub", "github:123456789012345678901")},
		{"sub with a leading zero", set("sub", "github:0123")},
		{"sub of 20 digits", set("sub", "github:12345678901234567890")},
		{"iat at the top of int64", raw(fmt.Sprintf(`{"v":"v1","kid":"k1","iss":%q,"aud":%q,"sub":"github:1","scope":["relay"],"iat":9223372036854775806,"exp":9223372036854775807}`, testIssuer, testAud))},
		{"iat beyond 2^40", edit(func(c map[string]any) { c["iat"] = int64(1<<40) + 1; c["exp"] = int64(1<<40) + 600 })},
		{"newline in the pass", func(p *passTool) string { g := p.good(now); return g[:len(g)-4] + "\n" + g[len(g)-4:] }},
		{"overflowing lifetime", raw(fmt.Sprintf(`{"v":"v1","kid":"k1","iss":%q,"aud":%q,"sub":"github:1","scope":["relay"],"iat":-9223372036854775808,"exp":9223372036854775807}`, testIssuer, testAud))},
		{"negative iat", edit(func(c map[string]any) { c["iat"] = -5; c["exp"] = 100 })},
		{"sub with a space", set("sub", "github: 123")},
		{"lifetime over an hour", set("exp", now.Add(time.Hour+time.Second).Unix())},
		{"exp equals iat", set("exp", now.Unix())},
		{"exp before iat", set("exp", now.Add(-time.Hour).Unix())},
		{"fractional iat", raw(fmt.Sprintf(`{"v":"v1","kid":"k1","iss":%q,"aud":%q,"sub":"github:1","scope":["relay"],"iat":%d.5,"exp":%d}`, testIssuer, testAud, now.Unix(), now.Unix()+600))},
		{"iat as a string", set("iat", fmt.Sprint(now.Unix()))},
		{"unknown field", set("extra", 1)},
		{"bound pass on a bearer-only server", set("cnf", map[string]string{"k": "AAAA"})},
		{"trailing data", raw(fmt.Sprintf(`{"v":"v1","kid":"k1","iss":%q,"aud":%q,"sub":"github:1","scope":["relay"],"iat":%d,"exp":%d}{}`, testIssuer, testAud, now.Unix(), now.Unix()+600))},
		{"not JSON", raw("nope")},
		{"signed by another key", func(p *passTool) string {
			q := *p
			q.priv = otherPriv
			return q.good(now)
		}},
		{"tampered payload", func(p *passTool) string {
			parts := strings.Split(p.good(now), ".")
			c := claims(now)
			c["sub"] = "github:999"
			b, _ := json.Marshal(c)
			return parts[0] + "." + b64.EncodeToString(b) + "." + parts[2]
		}},
		{"tampered signature", func(p *passTool) string {
			parts := strings.Split(p.good(now), ".")
			sig, _ := b64.DecodeString(parts[2])
			sig[0] ^= 1
			return parts[0] + "." + parts[1] + "." + b64.EncodeToString(sig)
		}},
		{"short signature", func(p *passTool) string {
			parts := strings.Split(p.good(now), ".")
			return parts[0] + "." + parts[1] + "." + parts[2][:20]
		}},
		{"padded base64", func(p *passTool) string { return p.good(now) + "=" }},
		{"wrong version prefix", func(p *passTool) string { return "v2" + strings.TrimPrefix(p.good(now), "v1") }},
		{"two parts", func(p *passTool) string { return "v1." + strings.Split(p.good(now), ".")[1] }},
		{"four parts", func(p *passTool) string { return p.good(now) + ".x" }},
		{"empty", func(p *passTool) string { return "" }},
		{"too long", func(p *passTool) string { return p.good(now) + strings.Repeat("A", maxPassLen) }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := newPassTool(t)
			_, why, err := p.cfg.checkPass([]byte(tc.make(p)), now)
			if err == nil {
				t.Fatal("the pass was accepted")
			}
			if want := reasons[tc.name]; want != "" && why != want {
				t.Fatalf("refused for %q, want %q", why, want)
			}
		})
	}
}

func TestCheckPassSkewAndRevocation(t *testing.T) {
	p, now := newPassTool(t), time.Now()
	c := claims(now)
	c["iat"], c["exp"] = now.Add(30*time.Second).Unix(), now.Add(time.Hour).Unix()
	if _, why, err := p.cfg.checkPass([]byte(p.pass(c)), now); err != nil {
		t.Fatalf("iat 30 s ahead is inside the skew: %s", why)
	}
	c = claims(now)
	c["iat"], c["exp"] = now.Add(-time.Hour).Unix(), now.Add(-30*time.Second).Unix()
	if _, why, err := p.cfg.checkPass([]byte(p.pass(c)), now); err != nil {
		t.Fatalf("exp 30 s ago is inside the skew: %s", why)
	}
	p.cfg.revoked["github:123"] = true
	if _, why, err := p.cfg.checkPass([]byte(p.good(now)), now); err == nil || why != "revoked" {
		t.Fatalf("a revoked account was accepted: %q", why)
	}
	// The key file, not the pass, decides which key is trusted: a pass for a kid that is not listed is refused even
	// when the signature is right.
	p.cfg.revoked = nil
	delete(p.cfg.keys, "k1")
	if _, _, err := p.cfg.checkPass([]byte(p.good(now)), now); err == nil {
		t.Fatal("a pass for a kid that is not in the file was accepted")
	}
}

func TestPassKeyFile(t *testing.T) {
	dir := t.TempDir()
	write := func(name, body string, mode os.FileMode) string {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte(body), mode); err != nil {
			t.Fatal(err)
		}
		return p
	}
	pub, _, _ := ed25519.GenerateKey(crand.Reader)
	enc := b64.EncodeToString(pub)
	keys, err := loadPassKeys(write("ok", "# keys\n\nk1 "+enc+"\nk-2.a_b "+enc+"\n", 0o600))
	if err != nil || len(keys) != 2 {
		t.Fatalf("a good file: %v %d", err, len(keys))
	}
	bad := map[string]string{
		"empty":        "",
		"only comment": "# nothing\n",
		"no key":       "k1\n",
		"short key":    "k1 " + b64.EncodeToString(pub[:16]) + "\n",
		"not base64":   "k1 !!!\n",
		"padded":       "k1 " + base64.URLEncoding.EncodeToString(pub) + "\n",
		"bad kid":      "k 1 " + enc + "\n",
		"empty kid":    " " + enc + "\n",
		"long kid":     strings.Repeat("k", 33) + " " + enc + "\n",
		"duplicate":    "k1 " + enc + "\nk1 " + enc + "\n",
		"too many": strings.Repeat("k0 "+enc+"\n", 1) + func() string {
			var b strings.Builder
			for i := 1; i <= maxPassKeys; i++ {
				fmt.Fprintf(&b, "k%d %s\n", i, enc)
			}
			return b.String()
		}(),
		"too big": strings.Repeat("# x\n", maxKeyFile),
	}
	for name, body := range bad {
		if _, err := loadPassKeys(write("bad-"+strings.ReplaceAll(name, " ", "-"), body, 0o600)); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	if _, err := loadPassKeys(filepath.Join(dir, "missing")); err == nil {
		t.Error("a missing file was accepted")
	}
	if _, err := loadPassKeys(dir); err == nil {
		t.Error("a directory was accepted")
	}
	if runtime.GOOS != "windows" {
		p := write("open", "k1 "+enc+"\n", 0o644)
		os.Chmod(p, 0o644)
		if _, err := loadPassKeys(p); err == nil || !strings.Contains(err.Error(), "chmod 600") {
			t.Errorf("a file readable by others: %v", err)
		}
		if err := os.Symlink(filepath.Join(dir, "ok"), filepath.Join(dir, "link")); err == nil {
			if _, err := loadPassKeys(filepath.Join(dir, "link")); err != nil {
				t.Errorf("a symlink to a good file: %v", err)
			}
		}
	}
}

func TestPassRevokedFile(t *testing.T) {
	dir := t.TempDir()
	write := func(body string) string {
		p := filepath.Join(dir, "r")
		os.WriteFile(p, []byte(body), 0o600)
		os.Chmod(p, 0o600)
		return p
	}
	got, err := loadPassRevoked(write("# gone\n\ngithub:1\r\ndiscord:2\n"))
	if err != nil || !got["github:1"] || !got["discord:2"] || len(got) != 2 {
		t.Fatalf("%v %v", err, got)
	}
	for _, body := range []string{"github:\n", "someone\n", "github:1 extra\n"} {
		if _, err := loadPassRevoked(write(body)); err == nil {
			t.Errorf("%q accepted", body)
		}
	}
}

func TestParseRequirePass(t *testing.T) {
	for in, want := range map[string][2]bool{ // connect, relay
		"": {false, false}, "relay": {false, true}, " relay , ": {false, true}, "connect": {true, false},
		" connect ": {true, false}, "relay,connect": {true, true}, "connect,relay,connect": {true, true},
	} {
		if connect, relay, err := parseRequirePass(in); err != nil || connect != want[0] || relay != want[1] {
			t.Errorf("%q: %v %v %v", in, connect, relay, err)
		}
	}
	for _, in := range []string{"all", "connect,x", "Connect", "relay;connect"} {
		if _, _, err := parseRequirePass(in); err == nil {
			t.Errorf("%q accepted", in)
		}
	}
}

// --- the server ---

// authBody is an AUTH request body after the request id.
func authBody(version byte, pass string) []byte {
	w := &writer{}
	w.u8(version)
	w.blob([]byte(pass))
	return w.b
}

func (c *tc) auth(pass string) (byte, uint32) {
	rt, r := c.call(msgAuth, authBody(passVersion, pass))
	if rt == msgError {
		return rt, r.u32()
	}
	return rt, 0
}

func startPassServer(t *testing.T, p *passTool, relayRequired bool, more ...func(*Server)) (*Server, string) {
	return startTCPUDPWith(t, func(s *Server) {
		for _, f := range more {
			f(s)
		}
		s.relay = true
		s.pass.Store(p.cfg)
		s.requireRelayPass = relayRequired
		s.maxPerSub = 2
	})
}

func TestAuthWithoutPassSettingsIsBadRequest(t *testing.T) {
	_, addr := startRelay(t, true)
	a := join(t, addr, "a")
	if rt, code := a.c.auth("v1.x.y"); rt != msgError || code != errBadRequest {
		t.Fatalf("AUTH on a server with no pass settings: 0x%02X %d, want the answer of a server from before AUTH", rt, code)
	}
}

func TestAuthBeforeHello(t *testing.T) {
	p := newPassTool(t)
	_, addr := startPassServer(t, p, true)
	conn, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	c := &tc{t: t, conn: conn}
	if rt, code := c.auth(p.good(time.Now())); rt != msgError || code != errNoHello {
		t.Fatalf("AUTH before HELLO: 0x%02X %d, want ERROR 7", rt, code)
	}
}

func TestAuthAcceptsAndRefuses(t *testing.T) {
	p := newPassTool(t)
	_, addr := startPassServer(t, p, true)
	a := join(t, addr, "a")
	now := time.Now()
	if rt, code := a.c.auth("garbage"); rt != msgError || code != errPass {
		t.Fatalf("garbage: 0x%02X %d", rt, code)
	}
	if rt, r := a.c.call(msgAuth, authBody(2, p.good(now))); rt != msgError || r.u32() != errPass {
		t.Fatalf("the reserved bound-mode version was not refused")
	}
	if rt, _ := a.c.auth(p.good(now)); rt != msgOK { // a success starts the count of refusals again
		t.Fatalf("a good pass: 0x%02X", rt)
	}
	if rt, r := a.c.call(msgAuth, []byte{1}); rt != msgError || r.u32() != errPass {
		t.Fatalf("a truncated AUTH was not refused")
	}
	if rt, r := a.c.call(msgAuth, append(authBody(passVersion, p.good(now)), 0)); rt != msgError || r.u32() != errPass {
		t.Fatalf("an AUTH with bytes after the pass was not refused")
	}
	// A refresh with the same account replaces the first pass; another account is refused and changes nothing.
	if rt, _ := a.c.auth(p.good(now.Add(time.Minute))); rt != msgOK {
		t.Fatalf("refresh: 0x%02X", rt)
	}
	c := claims(now)
	c["sub"] = "github:456"
	if rt, code := a.c.auth(p.pass(c)); rt != msgError || code != errPass {
		t.Fatalf("a refresh for another account: 0x%02X %d", rt, code)
	}
	c = claims(now)
	c["aud"] = "another.server"
	if rt, code := a.c.auth(p.pass(c)); rt != msgError || code != errPass {
		t.Fatalf("wrong audience: 0x%02X %d", rt, code)
	}
}

func TestConnectionClosedAfterRefusedPasses(t *testing.T) {
	p := newPassTool(t)
	_, addr := startPassServer(t, p, true)
	a := join(t, addr, "a")
	for i := 0; i < maxAuthFails-1; i++ {
		if rt, code := a.c.auth("garbage"); rt != msgError || code != errPass {
			t.Fatalf("refusal %d: 0x%02X %d", i, rt, code)
		}
	}
	w := &writer{}
	w.u32(99)
	w.bytes(authBody(passVersion, "garbage"))
	writePacket(a.c.conn, msgAuth, w.b) // the last refusal closes the connection; its reply may not arrive
	a.c.conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	for {
		if _, _, err := readPacket(a.c.conn); err != nil {
			break // closed (or the deadline passed: checked below)
		}
	}
	if _, _, err := readPacket(a.c.conn); err == nil || strings.Contains(err.Error(), "timeout") {
		t.Fatalf("the connection stayed open after refused passes: %v", err)
	}
}

// A refresh replaces the scopes: a relay pass refreshed by a connect-only pass loses the relay at once.
func TestRefreshReplacesScopes(t *testing.T) {
	p := newPassTool(t)
	s, addr := startPassServer(t, p, true)
	srv, _ := net.ResolveUDPAddr("udp", addr)
	a, b := join(t, addr, "a"), join(t, addr, "b")
	meet(t, a, b)
	ua, ub := udpSock(t), udpSock(t)
	probeFrom(t, ua, addr, a)
	probeFrom(t, ub, addr, b)
	a.c.auth(p.good(time.Now()))
	c := claims(time.Now())
	c["sub"] = "github:2"
	b.c.auth(p.pass(c))
	ua.WriteTo(relayPkt(a, a.token, b.id, "hi"), srv)
	if recvWithin(ub, 2*time.Second) == nil {
		t.Fatal("control: not relayed with passes at both ends")
	}
	c = claims(time.Now())
	c["scope"] = []string{"connect"}
	if rt, _ := a.c.auth(p.pass(c)); rt != msgOK {
		t.Fatal("refresh refused")
	}
	ua.WriteTo(relayPkt(a, a.token, b.id, "hi"), srv)
	if got := recvWithin(ub, 300*time.Millisecond); got != nil {
		t.Fatalf("relayed after a refresh without the relay scope: %q", got)
	}
	waitUDPCount(t, &s.udpDrops.relayNoPass, 1)
	if rt, r := a.c.call(msgGetCaps, nil); rt != msgCaps || r.u32()&capRelay != 0 {
		t.Fatal("the relay bit stayed set after a refresh without the relay scope")
	}
}

// A takeover frees the old connection's account slot once, and the old connection's later cleanup does not take another.
func TestTakeoverFreesTheAccountSlotOnce(t *testing.T) {
	p := newPassTool(t)
	s, addr := startPassServer(t, p, true)
	count := func(sub string) int {
		s.mu.Lock()
		defer s.mu.Unlock()
		return s.subConns[sub]
	}
	pass := p.good(time.Now())
	other := join(t, addr, "other")
	if rt, _ := other.c.auth(pass); rt != msgOK {
		t.Fatal("AUTH refused")
	}
	old := join(t, addr, "homer")
	if rt, _ := old.c.auth(pass); rt != msgOK {
		t.Fatal("AUTH refused")
	}
	if count("github:123") != 2 {
		t.Fatalf("count = %d, want 2", count("github:123"))
	}
	fresh := join(t, addr, "Homer") // the same name from the same address takes the old connection over
	if rt, code := fresh.c.auth(pass); rt != msgOK {
		t.Fatalf("the new connection was refused at the limit after a takeover: 0x%02X %d", rt, code)
	}
	old.c.conn.Close()
	time.Sleep(200 * time.Millisecond) // the old connection's cleanup runs
	if n := count("github:123"); n != 2 {
		t.Fatalf("count after the old connection's cleanup = %d, want 2 (other and the new one)", n)
	}
	other.c.conn.Close()
	fresh.c.conn.Close()
	for deadline := time.Now().Add(3 * time.Second); count("github:123") != 0; time.Sleep(5 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatalf("count = %d after every connection closed", count("github:123"))
		}
	}
}

func TestReloadEndsPassesOfRemovedKeys(t *testing.T) {
	p := newPassTool(t)
	dir := t.TempDir()
	keyFile := filepath.Join(dir, "keys")
	pub2, priv2, _ := ed25519.GenerateKey(crand.Reader)
	os.WriteFile(keyFile, []byte("k1 "+b64.EncodeToString(p.pub)+"\nk2 "+b64.EncodeToString(pub2)+"\n"), 0o600)
	s, addr := startPassServer(t, p, true)
	if err := s.reloadPass(keyFile, ""); err != nil {
		t.Fatal(err)
	}
	a, b := join(t, addr, "a"), join(t, addr, "b")
	a.c.auth(p.good(time.Now()))
	q := &passTool{t: t, priv: priv2, pub: pub2, cfg: p.cfg}
	cl := claims(time.Now())
	cl["kid"], cl["sub"] = "k2", "github:2"
	if rt, _ := b.c.auth(q.pass(cl)); rt != msgOK {
		t.Fatal("AUTH with k2 refused")
	}
	caps := func(pl player) uint32 { _, r := pl.c.call(msgGetCaps, nil); return r.u32() }
	if caps(a)&capRelay == 0 || caps(b)&capRelay == 0 {
		t.Fatal("control: both should hold the relay")
	}
	os.WriteFile(keyFile, []byte("k2 "+b64.EncodeToString(pub2)+"\n"), 0o600) // k1 is retired
	if err := s.reloadPass(keyFile, ""); err != nil {
		t.Fatal(err)
	}
	if caps(a)&capRelay != 0 {
		t.Fatal("a pass signed by a removed key still opens the relay")
	}
	if caps(b)&capRelay == 0 {
		t.Fatal("a pass signed by a key that is still listed lost the relay")
	}
}

func TestRelayCapsFollowThePass(t *testing.T) {
	p := newPassTool(t)
	s, addr := startPassServer(t, p, true)
	a := join(t, addr, "a")
	caps := func(pl player) uint32 {
		rt, r := pl.c.call(msgGetCaps, nil)
		if rt != msgCaps {
			t.Fatalf("GET_CAPS: 0x%02X", rt)
		}
		return r.u32()
	}
	if got := caps(a); got&capPassRelay == 0 || got&capRelay != 0 {
		t.Fatalf("before a pass: caps %#x, want the pass bit set and relay clear", got)
	}
	if rt, _ := a.c.auth(p.good(time.Now())); rt != msgOK {
		t.Fatal("AUTH refused")
	}
	if got := caps(a); got&capPassRelay == 0 || got&capRelay == 0 {
		t.Fatalf("with a pass: caps %#x, want both bits", got)
	}
	// A pass without the relay scope does not open it.
	b := join(t, addr, "b")
	c := claims(time.Now())
	c["scope"] = []string{"connect"}
	c["sub"] = "github:2"
	if rt, _ := b.c.auth(p.pass(c)); rt != msgOK {
		t.Fatal("a connect-only pass refused")
	}
	if got := caps(b); got&capRelay != 0 {
		t.Fatalf("a connect-only pass opened the relay: %#x", got)
	}
	// It lapses at expiry without the player doing anything.
	s.mu.Lock()
	s.clients[a.id].passExp = time.Now().Add(-2 * time.Minute).Unix()
	s.mu.Unlock()
	if got := caps(a); got&capRelay != 0 {
		t.Fatalf("an expired pass still opens the relay: %#x", got)
	}
}

func TestCapsWithoutRequirementAreAsBefore(t *testing.T) {
	p := newPassTool(t)
	_, addr := startPassServer(t, p, false)
	a := join(t, addr, "a")
	rt, r := a.c.call(msgGetCaps, nil)
	if got := r.u32(); rt != msgCaps || got&capRelay == 0 || got&capPassRelay != 0 {
		t.Fatalf("passes configured but not required: %#x", got)
	}
}

func TestRelayUDPNeedsPassesAtBothEnds(t *testing.T) {
	p := newPassTool(t)
	s, addr := startPassServer(t, p, true)
	srv, _ := net.ResolveUDPAddr("udp", addr)
	a, b := join(t, addr, "a"), join(t, addr, "b")
	meet(t, a, b)
	ua, ub := udpSock(t), udpSock(t)
	probeFrom(t, ua, addr, a)
	probeFrom(t, ub, addr, b)
	send := func(wait time.Duration) []byte {
		ua.WriteTo(relayPkt(a, a.token, b.id, "hi"), srv)
		return recvWithin(ub, wait)
	}
	if got := send(300 * time.Millisecond); got != nil {
		t.Fatalf("relayed with no passes: %q", got)
	}
	waitUDPCount(t, &s.udpDrops.relayNoPass, 1)
	if rt, _ := a.c.auth(p.good(time.Now())); rt != msgOK {
		t.Fatal("AUTH a refused")
	}
	if got := send(300 * time.Millisecond); got != nil {
		t.Fatalf("relayed with a pass at the sender only: %q", got)
	}
	waitUDPCount(t, &s.udpDrops.relayNoPass, 2)
	c := claims(time.Now())
	c["sub"] = "github:2"
	if rt, _ := b.c.auth(p.pass(c)); rt != msgOK {
		t.Fatal("AUTH b refused")
	}
	if got := send(2 * time.Second); got == nil {
		t.Fatal("not relayed with passes at both ends")
	}
	// A pass that has run out stops the relay mid-session.
	s.mu.Lock()
	s.clients[b.id].passExp = time.Now().Add(-2 * time.Minute).Unix()
	s.mu.Unlock()
	if got := send(300 * time.Millisecond); got != nil {
		t.Fatalf("relayed to a player whose pass ran out: %q", got)
	}
	waitUDPCount(t, &s.udpDrops.relayNoPass, 3)
}

func TestRelayUDPWithoutRequirementIsAsBefore(t *testing.T) {
	p := newPassTool(t)
	s, addr := startPassServer(t, p, false)
	srv, _ := net.ResolveUDPAddr("udp", addr)
	a, b := join(t, addr, "a"), join(t, addr, "b")
	meet(t, a, b)
	ua, ub := udpSock(t), udpSock(t)
	probeFrom(t, ua, addr, a)
	probeFrom(t, ub, addr, b)
	ua.WriteTo(relayPkt(a, a.token, b.id, "hi"), srv)
	if recvWithin(ub, time.Second) == nil {
		t.Fatal("not relayed with passes configured but not required")
	}
	if s.udpDrops.relayNoPass.Load() != 0 {
		t.Fatal("counted a drop")
	}
}

func TestStreamsNeedPassesAtBothEnds(t *testing.T) {
	p := newPassTool(t)
	_, addr := startPassServer(t, p, true)
	a, b := join(t, addr, "a"), join(t, addr, "b")
	meet(t, a, b)
	connect := func() uint32 {
		w := &writer{}
		w.u32(a.id)
		w.u32(a.token)
		w.u32(b.id)
		w.u16(3658)
		rt, code := streamCode(t, addr, msgStreamConnect, w.b)
		if rt != msgError {
			return 0
		}
		return code
	}
	if code := connect(); code != errPass {
		t.Fatalf("stream with no passes: code %d, want ERROR 10", code)
	}
	if rt, _ := a.c.auth(p.good(time.Now())); rt != msgOK {
		t.Fatal("AUTH a refused")
	}
	if code := connect(); code != errPass {
		t.Fatalf("stream with a pass at one end: code %d, want ERROR 10", code)
	}
	c := claims(time.Now())
	c["sub"] = "github:2"
	if rt, _ := b.c.auth(p.pass(c)); rt != msgOK {
		t.Fatal("AUTH b refused")
	}
	conn, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	w := &writer{}
	w.u32(1)
	w.u32(a.id)
	w.u32(a.token)
	w.u32(b.id)
	w.u16(3658)
	writePacket(conn, msgStreamConnect, w.b)
	if rt, _ := b.c.read(); rt != msgStreamOffer {
		t.Fatalf("with passes at both ends the target was not offered the stream: 0x%02X", rt)
	}
}

func TestStreamErrorCodeIsPass(t *testing.T) {
	p := newPassTool(t)
	_, addr := startPassServer(t, p, true)
	a, b := join(t, addr, "a"), join(t, addr, "b")
	meet(t, a, b)
	conn, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	w := &writer{}
	w.u32(7)
	w.u32(a.id)
	w.u32(a.token)
	w.u32(b.id)
	w.u16(3658)
	writePacket(conn, msgStreamConnect, w.b)
	conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	rt, body, err := readPacket(conn)
	r := &reader{b: body}
	if err != nil || rt != msgError || r.u32() != 7 || r.u32() != errPass {
		t.Fatalf("reply 0x%02X %v, want ERROR 10", rt, err)
	}
}

func TestPerAccountConnectionLimit(t *testing.T) {
	p := newPassTool(t)
	s, addr := startPassServer(t, p, true)
	var players []player
	for i := 0; i < 3; i++ {
		players = append(players, join(t, addr, fmt.Sprintf("p%d", i)))
	}
	pass := p.good(time.Now())
	for i := 0; i < 2; i++ {
		if rt, code := players[i].c.auth(pass); rt != msgOK {
			t.Fatalf("connection %d: 0x%02X %d", i, rt, code)
		}
	}
	if rt, code := players[2].c.auth(pass); rt != msgError || code != errPass {
		t.Fatalf("a third connection for one account: 0x%02X %d", rt, code)
	}
	// A refresh on a counted connection is not a new connection.
	if rt, _ := players[0].c.auth(pass); rt != msgOK {
		t.Fatal("a refresh was counted as a new connection")
	}
	players[0].c.conn.Close()
	deadline := time.Now().Add(3 * time.Second)
	for {
		s.mu.Lock()
		n := s.subConns["github:123"]
		s.mu.Unlock()
		if n == 1 || time.Now().After(deadline) {
			if n != 1 {
				t.Fatalf("count after a disconnect = %d, want 1", n)
			}
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if rt, code := players[2].c.auth(pass); rt != msgOK {
		t.Fatalf("after one left: 0x%02X %d", rt, code)
	}
	players[1].c.conn.Close()
	players[2].c.conn.Close()
	for deadline = time.Now().Add(3 * time.Second); time.Now().Before(deadline); time.Sleep(5 * time.Millisecond) {
		s.mu.Lock()
		n := len(s.subConns)
		s.mu.Unlock()
		if n == 0 {
			return
		}
	}
	t.Fatal("the per-account table kept an entry after every connection closed")
}

func TestReloadClosesRevokedAccountsAndKeepsOldSettingsOnError(t *testing.T) {
	p := newPassTool(t)
	dir := t.TempDir()
	keyFile := filepath.Join(dir, "keys")
	revFile := filepath.Join(dir, "revoked")
	enc := b64.EncodeToString(p.pub)
	os.WriteFile(keyFile, []byte("k1 "+enc+"\n"), 0o600)
	os.WriteFile(revFile, nil, 0o600)
	s, addr := startPassServer(t, p, true)
	a, b := join(t, addr, "a"), join(t, addr, "b")
	a.c.auth(p.good(time.Now()))
	c := claims(time.Now())
	c["sub"] = "github:2"
	b.c.auth(p.pass(c))

	if err := s.reloadPass(keyFile, revFile); err != nil {
		t.Fatal(err)
	}
	if rt, _ := a.c.call(msgGetCaps, nil); rt != msgCaps {
		t.Fatal("a reload with nothing revoked closed a connection")
	}
	os.WriteFile(revFile, []byte("github:123\n"), 0o600)
	if err := s.reloadPass(keyFile, revFile); err != nil {
		t.Fatal(err)
	}
	a.c.conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	if _, _, err := readPacket(a.c.conn); err == nil {
		t.Fatal("the revoked account's connection stayed open")
	}
	if rt, _ := b.c.call(msgGetCaps, nil); rt != msgCaps {
		t.Fatal("another account's connection was closed")
	}
	// The revoked account cannot sign in again.
	a2 := join(t, addr, "a2")
	if rt, code := a2.c.auth(p.good(time.Now())); rt != msgError || code != errPass {
		t.Fatalf("a revoked account signed in again: 0x%02X %d", rt, code)
	}
	// A bad file keeps the old settings, and the error names the file, not the content.
	os.WriteFile(keyFile, []byte("k1 nonsense\n"), 0o600)
	if err := s.reloadPass(keyFile, revFile); err == nil {
		t.Fatal("a bad key file was accepted")
	}
	if s.pass.Load().keys["k1"] == nil || !s.pass.Load().revoked["github:123"] {
		t.Fatal("a failed reload changed the settings")
	}
	// A rotation keeps the old key for an overlap and adds a new one.
	pub2, priv2, _ := ed25519.GenerateKey(crand.Reader)
	os.WriteFile(keyFile, []byte("k1 "+enc+"\nk2 "+b64.EncodeToString(pub2)+"\n"), 0o600)
	os.WriteFile(revFile, nil, 0o600)
	if err := s.reloadPass(keyFile, revFile); err != nil {
		t.Fatal(err)
	}
	q := &passTool{t: t, priv: priv2, pub: pub2, cfg: p.cfg}
	cl := claims(time.Now())
	cl["kid"] = "k2"
	a3 := join(t, addr, "a3")
	if rt, code := a3.c.auth(q.pass(cl)); rt != msgOK {
		t.Fatalf("a pass signed by the new key: 0x%02X %d", rt, code)
	}
}

func TestSubTagIsKeyed(t *testing.T) {
	k1, k2 := []byte("0123456789abcdef0123456789abcdef"), []byte("fedcba9876543210fedcba9876543210")
	if subTagWith(k1, "github:123") != subTagWith(k1, "github:123") {
		t.Fatal("the tag changed within one key")
	}
	if subTagWith(k1, "github:123") == subTagWith(k2, "github:123") {
		t.Fatal("two keys give the same tag, so the tag is not keyed")
	}
	if subTagWith(k1, "github:123") == subTagWith(k1, "github:124") {
		t.Fatal("two accounts share a tag")
	}
	if len(subTag("github:1")) != 8 {
		t.Fatalf("tag %q", subTag("github:1"))
	}
}

func TestPassNeverInTheLog(t *testing.T) {
	p := newPassTool(t)
	var log strings.Builder
	_, addr := startPassServer(t, p, true, func(s *Server) { s.log = newLogger(&log, levelFor(true), "text") })
	a := join(t, addr, "a")
	good := p.good(time.Now())
	a.c.auth(good)
	a.c.auth("v1." + strings.Repeat("A", 40) + ".evil\nINJECTED")
	out := log.String()
	for _, secret := range []string{good, strings.Split(good, ".")[1], "github:123", "INJECTED"} {
		if strings.Contains(out, secret) {
			t.Fatalf("the log holds %q:\n%s", secret, out)
		}
	}
	if !strings.Contains(out, "pass accepted") || !strings.Contains(out, "pass refused") {
		t.Fatalf("the log has no AUTH lines:\n%s", out)
	}
}

func TestPassAllowsBoundary(t *testing.T) {
	now := time.Now()
	c := &client{passScopes: scopeRelay, passExp: now.Unix()}
	if !c.passAllows(scopeRelay, now.Add(-time.Hour)) || !c.passAllows(scopeRelay, now.Add(30*time.Second)) {
		t.Fatal("a pass inside its life or its skew was refused")
	}
	if c.passAllows(scopeRelay, now.Add(61*time.Second)) {
		t.Fatal("a pass 61 s past exp was allowed")
	}
	if c.passAllows(scopeConnect, now) || (&client{}).passAllows(scopeRelay, now) {
		t.Fatal("the wrong scope, or no pass, was allowed")
	}
}

// streamCode sends a stream leg and returns the reply type and, for an ERROR, its code.
func streamCode(t *testing.T, addr string, typ byte, body []byte) (byte, uint32) {
	conn, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	w := &writer{}
	w.u32(1)
	w.bytes(body)
	writePacket(conn, typ, w.b)
	conn.SetReadDeadline(time.Now().Add(3 * time.Second))
	rt, p, err := readPacket(conn)
	if err != nil {
		t.Fatal(err)
	}
	r := &reader{b: p}
	r.u32()
	if rt == msgError {
		return rt, r.u32()
	}
	return rt, 0
}

func TestStreamAcceptNeedsThePassToo(t *testing.T) {
	p := newPassTool(t)
	s, addr := startPassServer(t, p, true)
	a, b := join(t, addr, "a"), join(t, addr, "b")
	meet(t, a, b)
	if rt, _ := a.c.auth(p.good(time.Now())); rt != msgOK {
		t.Fatal("AUTH a refused")
	}
	c := claims(time.Now())
	c["sub"] = "github:2"
	if rt, _ := b.c.auth(p.pass(c)); rt != msgOK {
		t.Fatal("AUTH b refused")
	}
	conn, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	w := &writer{}
	w.u32(1)
	w.u32(a.id)
	w.u32(a.token)
	w.u32(b.id)
	w.u16(3658)
	writePacket(conn, msgStreamConnect, w.b)
	rt, p2 := b.c.read()
	if rt != msgStreamOffer {
		t.Fatalf("no offer: 0x%02X", rt)
	}
	streamID := (&reader{b: p2}).u32()
	s.mu.Lock()
	s.clients[b.id].passExp = time.Now().Add(-2 * time.Minute).Unix()
	s.mu.Unlock()
	aw := &writer{}
	aw.u32(b.id)
	aw.u32(b.token)
	aw.u32(streamID)
	if rt, code := streamCode(t, addr, msgStreamAccept, aw.b); rt != msgError || code != errPass {
		t.Fatalf("accept by a player whose pass ran out: 0x%02X %d, want ERROR 10", rt, code)
	}
}
