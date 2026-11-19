package main

import (
	"context"
	"crypto/ed25519"
	crand "crypto/rand"
	"encoding/binary"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// Tests for -require-pass connect: a connection is a player only after a valid AUTH.

// startConnectServer is startPassServer with connect mode on. The sign-in limits are off unless a test sets them, since
// every test connection comes from 127.0.0.1.
func startConnectServer(t *testing.T, p *passTool, relayRequired bool, more ...func(*Server)) (*Server, string) {
	return startTCPUDPWith(t, func(s *Server) {
		s.relay = true
		s.pass.Store(p.cfg)
		s.requireRelayPass = relayRequired
		s.maxPerSub = 2
		s.requireConnectPass = true
		s.maxUnauth, s.maxUnauthPerAddr = 0, 0
		for _, f := range more {
			f(s)
		}
	})
}

// registered counts the players in s.clients: a replaced connection stays listed, with no name, until its goroutine ends.
func registered(s *Server) (n int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, c := range s.clients {
		if c.commID != "" {
			n++
		}
	}
	return n
}

// connectClaims is a valid payload carrying scope connect for sub.
func connectClaims(now time.Time, sub string) map[string]any {
	c := claims(now)
	c["scope"] = []string{"connect"}
	c["sub"] = sub
	return c
}

func dialRaw(t *testing.T, addr string) *tc {
	conn, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	return &tc{t: t, conn: conn}
}

// helloAck says HELLO as name and returns what the HELLO_ACK carried.
func helloAck(t *testing.T, c *tc, name string) (id, token uint32, flags uint8) {
	t.Helper()
	rt, r := hello(c, "NPWR00001_00", name)
	if rt != msgHelloAck {
		t.Fatalf("hello %s: 0x%02X", name, rt)
	}
	id = r.u32()
	r.take(4)
	token = r.u32()
	return id, token, r.u8()
}

// signIn dials, says HELLO and AUTH with a connect pass for sub, and returns the player with the token from the AUTH reply.
func signIn(t *testing.T, addr string, p *passTool, name, sub string) player {
	t.Helper()
	c := dialRaw(t, addr)
	id, _, _ := helloAck(t, c, name)
	rt, r := c.call(msgAuth, authBody(passVersion, p.pass(connectClaims(time.Now(), sub))))
	if rt != msgOK {
		t.Fatalf("AUTH for %s: 0x%02X", name, rt)
	}
	return player{c, id, r.u32()}
}

// dialProxy dials and sends a PROXY header naming ip as the real address.
func dialProxy(t *testing.T, addr string, ip [4]byte) *tc {
	c := dialRaw(t, addr)
	if _, err := c.conn.Write(v2tcp4(ip)); err != nil {
		t.Fatal(err)
	}
	return c
}

func proxied(s *Server) {
	trusted, _ := parseTrusted("127.0.0.0/8")
	s.trustedProxies, s.proxyProto = trusted, true
}

// waitCounters waits until nothing is counted: no connection open, none waiting to sign in.
func waitCounters(t *testing.T, s *Server) {
	t.Helper()
	var last string
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); time.Sleep(5 * time.Millisecond) {
		s.mu.Lock()
		last = fmt.Sprintf("unauthConns=%d unauthByAddr=%v openConns=%d addrConns=%v clients=%d subConns=%v",
			s.unauthConns, s.unauthByAddr, s.openConns, s.addrConns, len(s.clients), s.subConns)
		done := s.unauthConns == 0 && len(s.unauthByAddr) == 0 && s.openConns == 0 && len(s.addrConns) == 0 && len(s.clients) == 0 && len(s.subConns) == 0
		s.mu.Unlock()
		if done {
			return
		}
	}
	t.Fatalf("counters not back to zero: %s", last)
}

func counts(s *Server) (unauth, byAddr int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.unauthConns, len(s.unauthByAddr)
}

func waitUnauth(t *testing.T, s *Server, want int) {
	t.Helper()
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); time.Sleep(2 * time.Millisecond) {
		if n, _ := counts(s); n == want {
			return
		}
	}
	n, _ := counts(s)
	t.Fatalf("unauthConns = %d, want %d", n, want)
}

// unauthClosedWithin reports how long the server took to close c, or fails when it did not within max. Bytes that arrive first are skipped.
func unauthClosedWithin(t *testing.T, c *tc, max time.Duration) time.Duration {
	t.Helper()
	start := time.Now()
	c.conn.SetReadDeadline(start.Add(max))
	for {
		if _, _, err := readPacket(c.conn); err != nil {
			if ne, ok := err.(net.Error); ok && ne.Timeout() {
				t.Fatalf("connection still open after %v", max)
			}
			return time.Since(start)
		}
	}
}

func sendRaw(t *testing.T, c *tc, typ byte, body []byte) {
	t.Helper()
	c.req++
	w := &writer{}
	w.u32(c.req)
	w.bytes(body)
	if err := writePacket(c.conn, typ, w.b); err != nil {
		t.Fatal(err)
	}
}

func createRoomBody() []byte {
	w := &writer{}
	w.u8(4)
	w.u32(0)
	w.blob(nil)
	w.blob(nil)
	w.blob(nil)
	return w.b
}

func recordScoreBody() []byte {
	w := &writer{}
	w.u32(1)
	w.u8(0)
	w.u64(100)
	w.blob([]byte("gg"))
	return w.b
}

// Before HELLO nothing changes in connect mode: ERROR 7 for every type but HELLO, AUTH included.
func TestConnectBeforeHelloIsUnchanged(t *testing.T) {
	p := newPassTool(t)
	s, addr := startConnectServer(t, p, false)
	c := dialRaw(t, addr)
	var types []byte
	for ty := 0x02; ty <= 0x0F; ty++ {
		types = append(types, byte(ty))
	}
	for ty := 0x10; ty <= 0x22; ty++ {
		types = append(types, byte(ty))
	}
	for _, ty := range types {
		body := []byte(nil)
		if ty == msgAuth {
			body = authBody(passVersion, p.pass(connectClaims(time.Now(), "github:1")))
		}
		if rt, r := c.call(ty, body); rt != msgError || r.u32() != errNoHello {
			t.Fatalf("type 0x%02X before HELLO: 0x%02X, want ERROR 7", ty, rt)
		}
	}
	if n, _ := counts(s); n != 1 {
		t.Fatalf("unauthConns = %d, want 1 while the connection is open", n)
	}
}

// After HELLO and before AUTH every type but AUTH, HEARTBEAT, GET_CAPS and a second HELLO gets ERROR 10, and nothing is made.
func TestConnectGateRefusesEveryRequestBeforeAuth(t *testing.T) {
	p := newPassTool(t)
	s, addr := startConnectServer(t, p, true)
	c := dialRaw(t, addr)
	helloAck(t, c, "homer")
	var types []byte
	types = append(types, 0x03)
	for ty := 0x06; ty <= 0x0F; ty++ {
		types = append(types, byte(ty))
	}
	for ty := 0x10; ty <= 0x22; ty++ {
		types = append(types, byte(ty))
	}
	types = append(types, msgStreamConnect, msgStreamAccept, 0x23, 0x40, 0x7F, 0xFF)
	bodies := [][]byte{nil, createRoomBody(), recordScoreBody()}
	for _, ty := range types {
		for i, body := range bodies {
			if rt, r := c.call(ty, body); rt != msgError || r.u32() != errPass {
				t.Fatalf("type 0x%02X body %d before AUTH: 0x%02X, want ERROR 10", ty, i, rt)
			}
		}
	}
	// HEARTBEAT has no reply: the next reply read belongs to the request after it.
	sendRaw(t, c, msgHeartbeat, nil)
	rt, r := c.call(msgGetCaps, nil)
	if rt != msgCaps {
		t.Fatalf("GET_CAPS before AUTH: 0x%02X", rt)
	}
	if bits := r.u32(); bits != capPassRelay {
		t.Fatalf("GET_CAPS before AUTH: bits 0x%X, want only the pass bit 0x%X", bits, capPassRelay)
	}
	if v := r.blob(); r.err != nil || len(v) != 0 {
		t.Fatalf("GET_CAPS before AUTH: version text %q, want none", v)
	}
	if rt, r := c.call(msgHello, nil); rt != msgError || r.u32() != errBadRequest {
		t.Fatalf("a second HELLO: 0x%02X, want ERROR 1", rt)
	}
	s.mu.Lock()
	rooms, clients := len(s.rooms), len(s.clients)
	s.mu.Unlock()
	if rooms != 0 || clients != 0 {
		t.Fatalf("rooms %d clients %d after refused requests", rooms, clients)
	}
	if bs := s.snapshot().Boards; len(bs) != 0 {
		t.Fatalf("a score board exists: %v", bs)
	}
}

// Without a relay or with a title that uses no peer traffic there is no pass bit to show.
func TestConnectPreAuthCapsFollowTheRelaySettings(t *testing.T) {
	p := newPassTool(t)
	for name, c := range map[string]struct {
		relay, required bool
		want            uint32
	}{"no relay": {false, true, 0}, "relay without a pass": {true, false, 0}, "relay with a pass": {true, true, capPassRelay}} {
		t.Run(name, func(t *testing.T) {
			_, addr := startConnectServer(t, p, c.required, func(s *Server) { s.relay = c.relay })
			a := dialRaw(t, addr)
			helloAck(t, a, "a")
			if _, r := a.call(msgGetCaps, nil); r.u32() != c.want {
				t.Fatal("wrong bits before AUTH")
			}
		})
	}
}

// The same types after a valid AUTH behave as usual: the gate opens.
func TestConnectGateOpensAfterAuth(t *testing.T) {
	p := newPassTool(t)
	s, addr := startConnectServer(t, p, false)
	a := signIn(t, addr, p, "homer", "github:1")
	if rt, _ := a.c.call(msgCreateRoom, createRoomBody()); rt != msgRoomJoined {
		t.Fatalf("create room after AUTH: 0x%02X", rt)
	}
	if rt, _ := a.c.call(msgRecordScore, recordScoreBody()); rt != msgScoreRecorded {
		t.Fatalf("record score after AUTH: 0x%02X", rt)
	}
	if rt, _ := a.c.call(msgPeerAuth, nil); rt != msgOK {
		t.Fatalf("PEER_AUTH after AUTH: 0x%02X", rt)
	}
	rt, r := a.c.call(msgGetCaps, nil)
	if bits := r.u32(); rt != msgCaps || bits&(capRooms|capScores|capRoomCodes|capPeerKeys) != capRooms|capScores|capRoomCodes|capPeerKeys {
		t.Fatalf("GET_CAPS after AUTH: 0x%02X bits 0x%X", rt, bits)
	}
	if rt, r := a.c.call(msgHello, nil); rt != msgError || r.u32() != errBadRequest {
		t.Fatalf("HELLO after AUTH: 0x%02X, want ERROR 1", rt)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.rooms) != 1 || len(s.clients) != 1 {
		t.Fatalf("rooms %d clients %d", len(s.rooms), len(s.clients))
	}
}

// HELLO in connect mode is inert: token 0, bit 3, and the connection is in no list.
func TestConnectHelloIsInert(t *testing.T) {
	p := newPassTool(t)
	s, addr := startConnectServer(t, p, false, func(s *Server) { s.discover = true })
	c := dialRaw(t, addr)
	id, token, flags := helloAck(t, c, "homer")
	if id == 0 || token != 0 || flags&helloPassConnect == 0 || flags&2 == 0 || flags&helloPeerKeys == 0 {
		t.Fatalf("HELLO_ACK id %d token %d flags %d", id, token, flags)
	}
	s.mu.Lock()
	listed := s.clients[id] != nil
	s.mu.Unlock()
	if listed || s.snapshot().Clients != 0 {
		t.Fatal("a connection that has not signed in is listed")
	}
	if n := s.snapshot().VerifiedPlayers; n != 0 {
		t.Fatalf("%d verified players before AUTH", n)
	}
}

// An unauthenticated HELLO reserves no name.
func TestConnectUnauthenticatedHelloHoldsNoName(t *testing.T) {
	p := newPassTool(t)
	s, addr := startConnectServer(t, p, false)
	a, b := dialRaw(t, addr), dialRaw(t, addr)
	helloAck(t, a, "bob")
	helloAck(t, b, "BOB") // two unauthenticated HELLOs of one name: neither is refused
	s.mu.Lock()
	if len(s.clients) != 0 {
		t.Fatal("unauthenticated HELLOs are listed")
	}
	s.mu.Unlock()
	c := signIn(t, addr, p, "bob", "github:1") // and a player who signs in gets the name, no ERROR 8
	if rt, _ := c.c.call(msgCreateRoom, createRoomBody()); rt != msgRoomJoined {
		t.Fatalf("0x%02X", rt)
	}
}

// At AUTH the name is checked: the same address replaces the old connection, another address is refused.
func TestConnectNameClashAtAuth(t *testing.T) {
	p := newPassTool(t)
	s, addr := startConnectServer(t, p, false, proxied)
	pass := func(sub string) []byte { return authBody(passVersion, p.pass(connectClaims(time.Now(), sub))) }

	a := dialProxy(t, addr, [4]byte{198, 51, 100, 1})
	helloAck(t, a, "bob")
	if rt, _ := a.call(msgAuth, pass("github:1")); rt != msgOK {
		t.Fatal("A refused")
	}
	// same address, same name in another case: a takeover
	a2 := dialProxy(t, addr, [4]byte{198, 51, 100, 1})
	helloAck(t, a2, "Bob")
	if rt, _ := a2.call(msgAuth, pass("github:1")); rt != msgOK {
		t.Fatalf("a takeover from the same address was refused: 0x%02X", rt)
	}
	unauthClosedWithin(t, a, 3*time.Second) // the replaced connection is closed
	// another address: refused, nothing registered
	b := dialProxy(t, addr, [4]byte{198, 51, 100, 2})
	helloAck(t, b, "BOB")
	rt, r := b.call(msgAuth, pass("github:2"))
	if rt != msgError || r.u32() != errNameTaken {
		t.Fatalf("name held from another address: 0x%02X, want ERROR 8", rt)
	}
	s.mu.Lock()
	n2 := s.subConns["github:2"]
	s.mu.Unlock()
	if clients := registered(s); n2 != 0 || clients != 1 {
		t.Fatalf("a refused AUTH registered something: subConns %d players %d", n2, clients)
	}
	if rt, r := b.call(msgCreateRoom, createRoomBody()); rt != msgError || r.u32() != errPass {
		t.Fatalf("after ERROR 8 the connection is still not signed in: 0x%02X", rt)
	}
	if n, _ := counts(s); n != 1 {
		t.Fatalf("unauthConns = %d, want 1 (the refused B)", n)
	}
	// the refusal counts toward the three-strikes close
	b.call(msgAuth, pass("github:2"))
	sendRaw(t, b, msgAuth, pass("github:2"))
	unauthClosedWithin(t, b, 3*time.Second)
}

// The account limit counts the connection a reconnect replaces as already gone.
func TestConnectSubLimitWithTakeover(t *testing.T) {
	p := newPassTool(t)
	s, addr := startConnectServer(t, p, false, func(s *Server) { s.maxPerSub = 1 })
	a := signIn(t, addr, p, "homer", "github:1")
	b := signIn(t, addr, p, "Homer", "github:1") // same address, same name: replaces a, within the limit of 1
	unauthClosedWithin(t, a.c, 3*time.Second)    // replaced
	// another name with the same account is over the limit, and kicks nobody
	c := dialRaw(t, addr)
	helloAck(t, c, "marge")
	if rt, r := c.call(msgAuth, authBody(passVersion, p.pass(connectClaims(time.Now(), "github:1")))); rt != msgError || r.u32() != errPass {
		t.Fatalf("over the account limit: 0x%02X, want ERROR 10", rt)
	}
	if rt, _ := b.c.call(msgCreateRoom, createRoomBody()); rt != msgRoomJoined {
		t.Fatalf("the connection in place was kicked: 0x%02X", rt)
	}
	s.mu.Lock()
	n := s.subConns["github:1"]
	s.mu.Unlock()
	if n != 1 {
		t.Fatalf("subConns = %d, want 1", n)
	}
}

// AUTH returns the UDP token; HELLO_ACK's 0 and a guess are dropped before AUTH.
func TestConnectUDPProbeNeedsAuth(t *testing.T) {
	p := newPassTool(t)
	s, addr := startConnectServer(t, p, false)
	c := dialRaw(t, addr)
	id, helloToken, _ := helloAck(t, c, "homer")
	for _, tok := range []uint32{helloToken, 0x12345679} {
		before := s.udpDrops.unauthorized.Load()
		if _, reply := probe(t, addr, id, tok, [4]byte{127, 0, 0, 1}); reply != nil {
			t.Fatalf("a probe from a connection that has not signed in was answered (token %d)", tok)
		}
		waitUDPCount(t, &s.udpDrops.unauthorized, before+1)
	}
	rt, r := c.call(msgAuth, authBody(passVersion, p.pass(connectClaims(time.Now(), "github:1"))))
	if rt != msgOK {
		t.Fatalf("AUTH: 0x%02X", rt)
	}
	token := r.u32()
	if token == 0 || token&1 == 0 {
		t.Fatalf("token %d in the AUTH reply", token)
	}
	if _, reply := probe(t, addr, id, token, [4]byte{127, 0, 0, 1}); reply == nil {
		t.Fatal("the probe with the token from AUTH was not answered")
	}
	rt, r = c.call(msgAuth, authBody(passVersion, p.pass(connectClaims(time.Now(), "github:1")))) // refresh
	if rt != msgOK || r.u32() != token {
		t.Fatalf("a refresh did not return the same token")
	}
}

// RELAY from or to a connection that has not signed in is dropped as unauthorized.
func TestConnectRelayDatagramNeedsAuth(t *testing.T) {
	p := newPassTool(t)
	s, addr := startConnectServer(t, p, true)
	srv, _ := net.ResolveUDPAddr("udp", addr)
	b := signIn(t, addr, p, "b", "github:2")
	ub := udpSock(t)
	probeFrom(t, ub, addr, b)
	c := dialRaw(t, addr)
	id, helloTok, _ := helloAck(t, c, "c")
	u := udpSock(t)
	n := uint64(0)
	for _, tok := range []uint32{helloTok, 0xDEADBEEF} {
		u.WriteTo(relayPkt(player{id: id}, tok, b.id, "hi"), srv)
		n++
		waitUDPCount(t, &s.udpDrops.unauthorized, n)
		if got := recvWithin(ub, 200*time.Millisecond); got != nil {
			t.Fatalf("relayed for a connection that has not signed in: %q", got)
		}
	}
	// to an unauthenticated id
	ub.WriteTo(relayPkt(b, b.token, id, "hi"), srv)
	n++
	waitUDPCount(t, &s.udpDrops.unauthorized, n)
	if s.udpDrops.relayNoPass.Load() != 0 {
		t.Fatal("counted as relay_no_pass, not unauthorized")
	}
}

func streamFirst(t *testing.T, addr string, typ byte, body []byte) *tc {
	conn, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	w := &writer{}
	w.u32(1)
	w.bytes(body)
	writePacket(conn, typ, w.b)
	return &tc{t: t, conn: conn}
}

// A relay leg opened with the id of a connection that has not signed in finds no player; a leg of a signed-in player works
// and stops counting against the sign-in limits.
func TestConnectRelayLegNeedsAuth(t *testing.T) {
	p := newPassTool(t)
	s, addr := startConnectServer(t, p, false)
	c := dialRaw(t, addr)
	id, helloTok, _ := helloAck(t, c, "c")
	b := signIn(t, addr, p, "b", "github:2")
	a := signIn(t, addr, p, "a", "github:3")
	meet(t, a, b)
	for _, tok := range []uint32{helloTok, 0xDEADBEEF} {
		w := &writer{}
		w.u32(id)
		w.u32(tok)
		w.u32(b.id)
		w.u16(3658)
		leg := streamFirst(t, addr, msgStreamConnect, w.b)
		rt, body, err := readPacket(leg.conn)
		if err != nil || rt != msgError || binary.BigEndian.Uint32(body[4:]) != errNotFound {
			t.Fatalf("stream connect from a connection not signed in: 0x%02X %v", rt, err)
		}
		unauthClosedWithin(t, leg, 3*time.Second)
		aw := &writer{}
		aw.u32(id)
		aw.u32(tok)
		aw.u32(7)
		leg = streamFirst(t, addr, msgStreamAccept, aw.b)
		rt, body, err = readPacket(leg.conn)
		if err != nil || rt != msgError || binary.BigEndian.Uint32(body[4:]) != errNotFound {
			t.Fatalf("stream accept from a connection not signed in: 0x%02X %v", rt, err)
		}
	}
	// a real leg: it leaves the sign-in count when its first frame arrives
	before, _ := counts(s)
	legA, legB := openStream(t, addr, a, b)
	waitUnauth(t, s, before) // c is still waiting; the legs are not counted
	legA.conn.Close()
	legB.conn.Close()
}

// openStream opens a relay stream from a to b through the server and returns both legs once the server answered READY.
func openStream(t *testing.T, addr string, a, b player) (*tc, *tc) {
	t.Helper()
	w := &writer{}
	w.u32(a.id)
	w.u32(a.token)
	w.u32(b.id)
	w.u16(3658)
	legA := streamFirst(t, addr, msgStreamConnect, w.b)
	var offer []byte
	for {
		rt, body := b.c.read()
		if rt == msgStreamOffer {
			offer = body
			break
		}
	}
	aw := &writer{}
	aw.u32(b.id)
	aw.u32(b.token)
	aw.u32((&reader{b: offer}).u32())
	legB := streamFirst(t, addr, msgStreamAccept, aw.b)
	for _, leg := range []*tc{legA, legB} {
		leg.conn.SetReadDeadline(time.Now().Add(3 * time.Second))
		if rt, _, err := readPacket(leg.conn); err != nil || rt != msgStreamReady {
			t.Fatalf("stream leg: 0x%02X %v, want READY", rt, err)
		}
	}
	return legA, legB
}

// The per-address and total limits for connections that have not signed in.
func TestConnectUnauthLimits(t *testing.T) {
	p := newPassTool(t)
	t.Run("per address", func(t *testing.T) {
		var log syncBuf
		s, addr := startConnectServer(t, p, false, func(s *Server) {
			s.maxUnauthPerAddr = 2
			s.log = newLogger(&log, levelFor(false), "text")
		})
		a, b := dialRaw(t, addr), dialRaw(t, addr)
		helloAck(t, a, "a")
		helloAck(t, b, "b")
		third := dialRaw(t, addr)
		unauthClosedWithin(t, third, 3*time.Second) // closed at accept with no reply
		for i := 0; i < 5; i++ {                    // a burst of refusals is one log line
			unauthClosedWithin(t, dialRaw(t, addr), 3*time.Second)
		}
		if got := strings.Count(log.String(), "too many connections not signed in"); got != 1 {
			t.Fatalf("%d refusal lines in a burst, want 1 (rate limited):\n%s", got, log.String())
		}
		if rt, _ := a.call(msgGetCaps, nil); rt != msgCaps {
			t.Fatal("a connection already there was closed")
		}
		// a connection that signs in frees its slot
		if rt, _ := a.call(msgAuth, authBody(passVersion, p.pass(connectClaims(time.Now(), "github:1")))); rt != msgOK {
			t.Fatal("AUTH refused")
		}
		waitUnauth(t, s, 1)
		d := dialRaw(t, addr)
		helloAck(t, d, "d")
		waitUnauth(t, s, 2)
	})
	t.Run("total", func(t *testing.T) {
		_, addr := startConnectServer(t, p, false, func(s *Server) {
			proxied(s)
			s.maxUnauth, s.maxUnauthPerAddr = 3, 1
		})
		var held []*tc
		for i := 0; i < 3; i++ { // distinct sources: the per-address limit of 1 is not what stops the fourth
			c := dialProxy(t, addr, [4]byte{198, 51, 100, byte(i + 1)})
			helloAck(t, c, fmt.Sprintf("p%d", i))
			held = append(held, c)
		}
		unauthClosedWithin(t, dialProxy(t, addr, [4]byte{198, 51, 100, 9}), 3*time.Second)
		unauthClosedWithin(t, dialProxy(t, addr, [4]byte{198, 51, 100, 1}), 3*time.Second) // and the same address again
		if rt, _ := held[0].call(msgGetCaps, nil); rt != msgCaps {
			t.Fatal("a connection already there was closed")
		}
	})
	t.Run("zero is no limit", func(t *testing.T) {
		s, addr := startConnectServer(t, p, false) // both off
		for i := 0; i < 12; i++ {
			helloAck(t, dialRaw(t, addr), fmt.Sprintf("p%d", i))
		}
		waitUnauth(t, s, 12)
	})
	t.Run("not applied with connect off", func(t *testing.T) {
		_, addr := startPassServer(t, p, false, func(s *Server) { s.maxUnauth, s.maxUnauthPerAddr = 1, 1 })
		for i := 0; i < 10; i++ {
			join(t, addr, fmt.Sprintf("p%d", i))
		}
	})
}

// Every way a connection can end leaves the counters at zero.
func TestConnectCountersReturnToZero(t *testing.T) {
	p := newPassTool(t)
	ends := map[string]func(t *testing.T, s *Server, addr string){
		"success then close": func(t *testing.T, s *Server, addr string) {
			signIn(t, addr, p, "a", "github:1").c.conn.Close()
		},
		"close before HELLO": func(t *testing.T, s *Server, addr string) {
			dialRaw(t, addr).conn.Close()
		},
		"close after HELLO": func(t *testing.T, s *Server, addr string) {
			c := dialRaw(t, addr)
			helloAck(t, c, "a")
			c.conn.Close()
		},
		"three refused AUTHs": func(t *testing.T, s *Server, addr string) {
			c := dialRaw(t, addr)
			helloAck(t, c, "a")
			c.call(msgAuth, authBody(passVersion, "x"))
			c.call(msgAuth, authBody(passVersion, "x"))
			sendRaw(t, c, msgAuth, authBody(passVersion, "x"))
			unauthClosedWithin(t, c, 3*time.Second)
		},
		"deadline": func(t *testing.T, s *Server, addr string) {
			unauthClosedWithin(t, dialRaw(t, addr), 3*time.Second)
		},
		"oversize frame": func(t *testing.T, s *Server, addr string) {
			c := dialRaw(t, addr)
			c.conn.Write([]byte{msgAuth, byte((maxUnauthFrame + 1) >> 8), byte((maxUnauthFrame + 1) & 0xff)})
			unauthClosedWithin(t, c, 3*time.Second)
		},
		"relay leg": func(t *testing.T, s *Server, addr string) {
			a := signIn(t, addr, p, "a", "github:1")
			b := signIn(t, addr, p, "b", "github:2")
			meet(t, a, b)
			legA, legB := openStream(t, addr, a, b)
			legA.conn.Close()
			legB.conn.Close()
			a.c.conn.Close()
			b.c.conn.Close()
		},
		"takeover victim": func(t *testing.T, s *Server, addr string) {
			a := signIn(t, addr, p, "a", "github:1")
			b := signIn(t, addr, p, "A", "github:1")
			unauthClosedWithin(t, a.c, 3*time.Second)
			b.c.conn.Close()
		},
		"reload kick": func(t *testing.T, s *Server, addr string) {
			a := signIn(t, addr, p, "a", "github:1")
			dir := t.TempDir()
			kf := filepath.Join(dir, "keys")
			os.WriteFile(kf, []byte("k1 "+b64.EncodeToString(p.pub)+"\n"), 0o600)
			rf := filepath.Join(dir, "rev")
			os.WriteFile(rf, []byte("github:1\n"), 0o600)
			if err := s.reloadPass(kf, rf); err != nil {
				t.Fatal(err)
			}
			unauthClosedWithin(t, a.c, 3*time.Second)
		},
	}
	for name, end := range ends {
		t.Run(name, func(t *testing.T) {
			s, addr := startConnectServer(t, p, false, func(s *Server) { s.authTimeout = 150 * time.Millisecond })
			end(t, s, addr)
			waitCounters(t, s)
		})
	}
	t.Run("shutdown", func(t *testing.T) {
		s, addr := startConnectServer(t, p, false)
		c := dialRaw(t, addr)
		helloAck(t, c, "a")
		signIn(t, addr, p, "b", "github:1")
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		s.Shutdown(ctx)
		unauthClosedWithin(t, c, 3*time.Second)
		waitCounters(t, s)
	})
}

// A connection that sends nothing, or only HELLO, is closed at the sign-in deadline, with the reason logged.
func TestConnectDeadline(t *testing.T) {
	p := newPassTool(t)
	var log syncBuf
	_, addr := startConnectServer(t, p, false, func(s *Server) {
		s.authTimeout = 300 * time.Millisecond
		s.log = newLogger(&log, levelFor(false), "text")
	})
	took := unauthClosedWithin(t, dialRaw(t, addr), 5*time.Second)
	if took < 250*time.Millisecond {
		t.Fatalf("closed after %v, before the deadline", took)
	}
	c := dialRaw(t, addr)
	helloAck(t, c, "a")
	unauthClosedWithin(t, c, 5*time.Second)
	if got := strings.Count(log.String(), "no sign-in in time"); got != 2 {
		t.Fatalf("the reason was logged %d times, want 2:\n%s", got, log.String())
	}
}

// HEARTBEAT keeps a signed-in client alive but does not move the sign-in deadline.
func TestConnectDeadlineNotExtendedByHeartbeat(t *testing.T) {
	p := newPassTool(t)
	const deadline = 400 * time.Millisecond
	_, addr := startConnectServer(t, p, false, func(s *Server) { s.authTimeout = deadline })
	c := dialRaw(t, addr)
	helloAck(t, c, "a")
	done := make(chan time.Duration, 1)
	start := time.Now()
	go func() {
		for {
			if _, _, err := readPacket(c.conn); err != nil {
				done <- time.Since(start)
				return
			}
		}
	}()
	var werr error
	for time.Since(start) < 3*deadline && werr == nil {
		select {
		case took := <-done:
			if took < deadline-50*time.Millisecond || took > 2*deadline+time.Second {
				t.Fatalf("closed after %v, want about %v", took, deadline)
			}
			return
		case <-time.After(50 * time.Millisecond):
		}
		c.req++
		w := &writer{}
		w.u32(c.req)
		werr = writePacket(c.conn, msgHeartbeat, w.b)
	}
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("HEARTBEATs kept a connection that never signed in open past the deadline")
	}
}

// The deadline counts from accept: a late HELLO and AUTH get what is left, and a slow PROXY header does not restart it.
func TestConnectDeadlineCountsFromAccept(t *testing.T) {
	p := newPassTool(t)
	const deadline = 600 * time.Millisecond
	_, addr := startConnectServer(t, p, false, func(s *Server) { s.authTimeout = deadline; proxied(s) })

	c := dialRaw(t, addr)
	time.Sleep(deadline * 7 / 10)
	c.conn.Write(v2tcp4([4]byte{198, 51, 100, 1})) // the header arrives late
	helloAck(t, c, "a")
	took := unauthClosedWithin(t, c, 5*time.Second)
	if took > deadline*4/10+500*time.Millisecond {
		t.Fatalf("closed %v after the header, want about 30%% of the deadline", took)
	}

	// AUTH just before the deadline works, and the connection then lives past it
	d := dialProxy(t, addr, [4]byte{198, 51, 100, 2})
	helloAck(t, d, "d")
	time.Sleep(deadline / 2)
	if rt, _ := d.call(msgAuth, authBody(passVersion, p.pass(connectClaims(time.Now(), "github:1")))); rt != msgOK {
		t.Fatal("AUTH before the deadline refused")
	}
	time.Sleep(deadline)
	if rt, _ := d.call(msgGetCaps, nil); rt != msgCaps {
		t.Fatal("a signed-in connection did not outlive the sign-in deadline")
	}
	// AUTH after the deadline: the connection is already closed
	e := dialProxy(t, addr, [4]byte{198, 51, 100, 3})
	helloAck(t, e, "e")
	time.Sleep(deadline + 100*time.Millisecond) // the server has closed it: a late AUTH goes nowhere
	w := &writer{}
	w.u32(9)
	w.bytes(authBody(passVersion, p.pass(connectClaims(time.Now(), "github:2"))))
	writePacket(e.conn, msgAuth, w.b)
	unauthClosedWithin(t, e, 3*time.Second)
}

// A frame header that declares more than a pass can fill is refused before its body is read; after AUTH the usual limit applies.
func TestConnectFrameLimitBeforeAuth(t *testing.T) {
	p := newPassTool(t)
	var log syncBuf
	_, addr := startConnectServer(t, p, false, func(s *Server) { s.log = newLogger(&log, levelFor(false), "text") })
	c := dialRaw(t, addr)
	helloAck(t, c, "a")
	c.conn.Write([]byte{msgAuth, byte((maxUnauthFrame + 1) >> 8), byte((maxUnauthFrame + 1) & 0xff)}) // header only
	if took := unauthClosedWithin(t, c, 3*time.Second); took > 2*time.Second {
		t.Fatalf("took %v: the server waited for the body", took)
	}
	if !strings.Contains(log.String(), "frame too long before sign-in") {
		t.Fatalf("no reason logged:\n%s", log.String())
	}

	// a full frame with a 2048-byte pass is read and judged as a pass
	d := dialRaw(t, addr)
	helloAck(t, d, "d")
	if len(authBody(passVersion, strings.Repeat("A", maxPassLen)))+4 != maxUnauthFrame {
		t.Fatal("maxUnauthFrame is not the size of an AUTH frame with the longest pass")
	}
	if rt, r := d.call(msgAuth, authBody(passVersion, strings.Repeat("A", maxPassLen))); rt != msgError || r.u32() != errPass {
		t.Fatalf("a 2048-byte junk pass: 0x%02X", rt)
	}
	// a pass of 2049 bytes cannot fit the limit, but a blob that declares it and sends 2048 does: refused as a bad AUTH
	w := &writer{}
	w.u8(passVersion)
	w.u16(maxPassLen + 1)
	w.bytes([]byte(strings.Repeat("A", maxPassLen)))
	if rt, r := d.call(msgAuth, w.b); rt != msgError || r.u32() != errPass {
		t.Fatalf("a blob longer than its frame: 0x%02X", rt)
	}
}

// After AUTH a frame above the pre-AUTH limit is fine.
func TestConnectFrameLimitLiftedAfterAuth(t *testing.T) {
	p := newPassTool(t)
	_, addr := startConnectServer(t, p, false)
	a := signIn(t, addr, p, "a", "github:1")
	w := &writer{}
	w.u8(4)
	w.u32(0)
	w.blob(nil)
	w.blob(make([]byte, 4096))
	w.blob(nil)
	if rt, _ := a.c.call(msgCreateRoom, w.b); rt != msgRoomJoined {
		t.Fatalf("a 4096-byte room blob after AUTH: 0x%02X", rt)
	}
}

// Three refused AUTHs close the connection; a success after two works.
func TestConnectRefusedPasses(t *testing.T) {
	p := newPassTool(t)
	s, addr := startConnectServer(t, p, false)
	c := dialRaw(t, addr)
	helloAck(t, c, "a")
	for i := 0; i < 2; i++ {
		if rt, code := c.auth("garbage"); rt != msgError || code != errPass {
			t.Fatalf("refusal %d: 0x%02X %d", i, rt, code)
		}
	}
	if rt, _ := c.call(msgAuth, authBody(passVersion, p.pass(connectClaims(time.Now(), "github:1")))); rt != msgOK {
		t.Fatal("a good pass after two refusals was refused")
	}
	d := dialRaw(t, addr)
	helloAck(t, d, "d")
	d.call(msgAuth, authBody(passVersion, "x"))
	d.call(msgAuth, authBody(passVersion, "x"))
	sendRaw(t, d, msgAuth, authBody(passVersion, "x"))
	unauthClosedWithin(t, d, 3*time.Second)
	c.conn.Close()
	waitCounters(t, s)
}

// A refresh in connect mode must be the same account and carry scope connect.
func TestConnectRefresh(t *testing.T) {
	p := newPassTool(t)
	s, addr := startConnectServer(t, p, false)
	a := signIn(t, addr, p, "a", "github:1")
	if rt, code := a.c.auth(p.pass(connectClaims(time.Now(), "github:2"))); rt != msgError || code != errPass {
		t.Fatalf("a refresh for another account: 0x%02X %d", rt, code)
	}
	relayOnly := claims(time.Now())
	relayOnly["sub"] = "github:1"
	if rt, code := a.c.auth(p.pass(relayOnly)); rt != msgError || code != errPass {
		t.Fatalf("a refresh without scope connect: 0x%02X %d", rt, code)
	}
	s.mu.Lock()
	scopes := s.clients[a.id].passScopes
	s.mu.Unlock()
	if scopes != scopeConnect {
		t.Fatalf("the first pass is not in force: scopes %d", scopes)
	}
	both := connectClaims(time.Now(), "github:1")
	both["scope"] = []string{"connect", "relay"}
	if rt, _ := a.c.auth(p.pass(both)); rt != msgOK {
		t.Fatal("a refresh with both scopes refused")
	}
	// the first AUTH with a relay-only pass is refused
	c := dialRaw(t, addr)
	helloAck(t, c, "c")
	if rt, code := c.auth(p.pass(relayOnly)); rt != msgError || code != errPass {
		t.Fatalf("first AUTH with a relay-only pass: 0x%02X %d", rt, code)
	}
}

// A pass runs out passSkew seconds after exp: the connection is closed then unless a refresh moved it.
func TestConnectExpiry(t *testing.T) {
	p := newPassTool(t)
	now := time.Now()
	var log syncBuf
	_, addr := startConnectServer(t, p, false, func(s *Server) { s.log = newLogger(&log, levelFor(false), "text") })
	// iat 100 s ago, exp 58 s ago: inside the skew, so accepted, and 2 s from the end of it
	short := connectClaims(now, "github:1")
	short["iat"], short["exp"] = now.Unix()-100, now.Unix()-58
	c := dialRaw(t, addr)
	helloAck(t, c, "a")
	if rt, _ := c.call(msgAuth, authBody(passVersion, p.pass(short))); rt != msgOK {
		t.Fatal("a pass inside the skew was refused")
	}
	took := unauthClosedWithin(t, c, 6*time.Second)
	if took > 3*time.Second {
		t.Fatalf("closed after %v", took)
	}
	if !strings.Contains(log.String(), "pass expired") {
		t.Fatalf("no reason logged:\n%s", log.String())
	}
	// a pass past the skew is refused at AUTH
	old := connectClaims(now, "github:1")
	old["iat"], old["exp"] = now.Unix()-200, now.Unix()-61
	d := dialRaw(t, addr)
	helloAck(t, d, "d")
	if rt, code := d.auth(p.pass(old)); rt != msgError || code != errPass {
		t.Fatalf("an expired pass: 0x%02X %d", rt, code)
	}
	// a refresh moves the end
	e := dialRaw(t, addr)
	helloAck(t, e, "e")
	short["sub"] = "github:5"
	e.call(msgAuth, authBody(passVersion, p.pass(short)))
	if rt, _ := e.call(msgAuth, authBody(passVersion, p.pass(connectClaims(time.Now(), "github:5")))); rt != msgOK {
		t.Fatal("refresh refused")
	}
	time.Sleep(2500 * time.Millisecond)
	if rt, _ := e.call(msgGetCaps, nil); rt != msgCaps {
		t.Fatal("a refreshed connection was closed at the old end")
	}
}

// With connect mode off the same near-expired pass never closes the connection.
func TestExpiryDoesNotCloseWithoutConnectMode(t *testing.T) {
	p := newPassTool(t)
	_, addr := startPassServer(t, p, false)
	now := time.Now()
	short := claims(now)
	short["iat"], short["exp"] = now.Unix()-100, now.Unix()-58
	a := join(t, addr, "a")
	if rt, _ := a.c.auth(p.pass(short)); rt != msgOK {
		t.Fatal("refused")
	}
	time.Sleep(2500 * time.Millisecond)
	if rt, _ := a.c.call(msgGetCaps, nil); rt != msgCaps {
		t.Fatal("closed with connect mode off")
	}
}

// An old client that never says AUTH is refused on every request and closed at the deadline; with connect off it works.
func TestConnectOldClientIsRefused(t *testing.T) {
	p := newPassTool(t)
	_, addr := startConnectServer(t, p, false, func(s *Server) { s.authTimeout = 500 * time.Millisecond })
	c := dialRaw(t, addr)
	helloAck(t, c, "old")
	for _, r := range []struct {
		ty   byte
		body []byte
	}{{msgCreateRoom, createRoomBody()}, {msgSearchRooms, nil}, {msgRecordScore, recordScoreBody()}} {
		if rt, rd := c.call(r.ty, r.body); rt != msgError || rd.u32() != errPass {
			t.Fatalf("0x%02X: 0x%02X, want ERROR 10", r.ty, rt)
		}
	}
	unauthClosedWithin(t, c, 5*time.Second)

	_, addr = startPassServer(t, p, false) // pass settings, connect off
	d := dialRaw(t, addr)
	_, tok, flags := helloAck(t, d, "old")
	if tok == 0 || flags&helloPassConnect != 0 {
		t.Fatalf("HELLO_ACK token %d flags %d", tok, flags)
	}
	if rt, _ := d.call(msgCreateRoom, createRoomBody()); rt != msgRoomJoined {
		t.Fatalf("rooms with connect off: 0x%02X", rt)
	}
	if rt, _ := d.call(msgRecordScore, recordScoreBody()); rt != msgScoreRecorded {
		t.Fatalf("scores with connect off: 0x%02X", rt)
	}
	rt, r := d.call(msgAuth, authBody(passVersion, p.good(time.Now())))
	if rt != msgOK || len(r.b) != 0 {
		t.Fatalf("AUTH with connect off: 0x%02X with %d more bytes, want the bare OK", rt, len(r.b))
	}
}

// With connect mode off HELLO registers at once, as before.
func TestConnectOffRegistersAtHello(t *testing.T) {
	p := newPassTool(t)
	s, addr := startPassServer(t, p, false)
	c := dialRaw(t, addr)
	id, tok, flags := helloAck(t, c, "homer")
	if tok == 0 || flags != 1|2|helloPeerKeys {
		t.Fatalf("token %d flags %d", tok, flags)
	}
	s.mu.Lock()
	listed := s.clients[id] != nil
	unauth := s.unauthConns
	s.mu.Unlock()
	if !listed || unauth != 0 {
		t.Fatalf("listed %v, unauth %d", listed, unauth)
	}
	if rt, _ := c.call(msgCreateRoom, createRoomBody()); rt != msgRoomJoined {
		t.Fatal("rooms")
	}
}

// With both scopes required the relay gates still apply after AUTH.
func TestConnectAndRelayBothRequired(t *testing.T) {
	p := newPassTool(t)
	_, addr := startConnectServer(t, p, true)
	a := signIn(t, addr, p, "a", "github:1") // connect scope only
	if _, r := a.c.call(msgGetCaps, nil); true {
		bits := r.u32()
		if bits&capPassRelay == 0 || bits&capRelay != 0 || bits&capRooms == 0 {
			t.Fatalf("bits 0x%X after a connect-only pass", bits)
		}
	}
	c := connectClaims(time.Now(), "github:1")
	c["scope"] = []string{"connect", "relay"}
	if rt, _ := a.c.auth(p.pass(c)); rt != msgOK {
		t.Fatal("refresh refused")
	}
	if _, r := a.c.call(msgGetCaps, nil); r.u32()&capRelay == 0 {
		t.Fatal("no relay with both scopes")
	}
}

// Reload: a key taken out of the file closes the connections it signed in; connections that have not signed in are left alone.
func TestConnectReload(t *testing.T) {
	p := newPassTool(t)
	dir := t.TempDir()
	keyFile := filepath.Join(dir, "keys")
	pub2, priv2, _ := ed25519.GenerateKey(crand.Reader)
	os.WriteFile(keyFile, []byte("k1 "+b64.EncodeToString(p.pub)+"\nk2 "+b64.EncodeToString(pub2)+"\n"), 0o600)
	var log syncBuf
	s, addr := startConnectServer(t, p, false, func(s *Server) { s.log = newLogger(&log, levelFor(false), "text") })
	if err := s.reloadPass(keyFile, ""); err != nil {
		t.Fatal(err)
	}
	a := signIn(t, addr, p, "a", "github:1")
	q := &passTool{t: t, priv: priv2, pub: pub2, cfg: p.cfg}
	cl := connectClaims(time.Now(), "github:2")
	cl["kid"] = "k2"
	b := dialRaw(t, addr)
	helloAck(t, b, "b")
	waiting := dialRaw(t, addr)
	helloAck(t, waiting, "w")
	if rt, _ := b.call(msgAuth, authBody(passVersion, q.pass(cl))); rt != msgOK {
		t.Fatal("AUTH with k2 refused")
	}
	os.WriteFile(keyFile, []byte("k2 "+b64.EncodeToString(pub2)+"\n"), 0o600) // k1 retired
	if err := s.reloadPass(keyFile, ""); err != nil {
		t.Fatal(err)
	}
	unauthClosedWithin(t, a.c, 3*time.Second)
	if rt, _ := b.call(msgGetCaps, nil); rt != msgCaps {
		t.Fatal("a connection signed in by a listed key was closed")
	}
	if rt, _ := waiting.call(msgGetCaps, nil); rt != msgCaps {
		t.Fatal("a connection that has not signed in was touched by the reload")
	}
	if !strings.Contains(log.String(), "pass key removed") {
		t.Fatalf("no reason logged:\n%s", log.String())
	}
	// a bad file keeps the old keys
	os.WriteFile(keyFile, []byte("k2 nonsense\n"), 0o600)
	if err := s.reloadPass(keyFile, ""); err == nil || s.pass.Load().keys["k2"] == nil {
		t.Fatal("a bad key file changed the settings")
	}
}

// A takeover victim is left with no name and no sign-in, so a request that slips in before its close lands gets ERROR 7 and
// never reaches a handler with an empty commID.
func TestConnectTakeoverVictimIsGuarded(t *testing.T) {
	p := newPassTool(t)
	s, addr := startConnectServer(t, p, false)
	old := signIn(t, addr, p, "homer", "github:1")
	s.mu.Lock()
	victim := s.clients[old.id]
	s.mu.Unlock()
	signIn(t, addr, p, "HOMER", "github:1")
	unauthClosedWithin(t, old.c, 3*time.Second)
	s.mu.Lock()
	defer s.mu.Unlock()
	if victim.commID != "" || victim.helloSeen || victim.authed || victim.sub != "" {
		t.Fatalf("the victim keeps state: commID %q helloSeen %v authed %v sub %q", victim.commID, victim.helloSeen, victim.authed, victim.sub)
	}
}

// Pass bytes and the parked name stay out of the log, and the info line for HELLO appears only at registration.
func TestConnectLogHygiene(t *testing.T) {
	p := newPassTool(t)
	var log syncBuf
	_, addr := startConnectServer(t, p, false, func(s *Server) { s.log = newLogger(&log, levelFor(false), "text") })
	c := dialRaw(t, addr)
	helloAck(t, c, "parkedname")
	if out := log.String(); strings.Contains(out, "parkedname") || strings.Contains(out, "msg=hello ") {
		t.Fatalf("the log shows the parked HELLO:\n%s", out)
	}
	good := p.pass(connectClaims(time.Now(), "github:123"))
	c.auth("v1." + strings.Repeat("A", 40) + ".evil\nINJECTED")
	if rt, _ := c.call(msgAuth, authBody(passVersion, good)); rt != msgOK {
		t.Fatal("AUTH refused")
	}
	out := log.String()
	for _, secret := range []string{good, strings.Split(good, ".")[1], "github:123", "INJECTED"} {
		if strings.Contains(out, secret) {
			t.Fatalf("the log holds %q:\n%s", secret, out)
		}
	}
	if strings.Count(out, "msg=hello ") != 1 || !strings.Contains(out, "pass accepted") || !strings.Contains(out, "pass refused") {
		t.Fatalf("log lines:\n%s", out)
	}
}

// Many connections signing in, closing and replacing each other leave the counters at zero.
func TestConnectStress(t *testing.T) {
	p := newPassTool(t)
	s, addr := startConnectServer(t, p, false, func(s *Server) { s.maxPerSub = 0; s.maxPerAddr = 0; s.maxConns = 0 })
	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < 25; i++ {
				conn, err := net.Dial("tcp", addr)
				if err != nil {
					t.Error(err)
					return
				}
				c := &tc{t: t, conn: conn}
				name := fmt.Sprintf("p%d", (g*25+i)%12) // names repeat, so takeovers and name clashes happen
				w := &writer{}
				w.str("NPWR00001_00", 12)
				w.str(name, 16)
				w.u16(1)
				writePacket(conn, msgHello, append([]byte{0, 0, 0, 1}, w.b...))
				if i%3 != 0 {
					writePacket(conn, msgAuth, append([]byte{0, 0, 0, 2}, authBody(passVersion, p.pass(connectClaims(time.Now(), fmt.Sprintf("github:%d", g+1))))...))
				}
				if i%5 == 0 {
					conn.SetReadDeadline(time.Now().Add(50 * time.Millisecond))
					readPacket(conn)
				}
				_ = c
				conn.Close()
			}
		}(g)
	}
	wg.Wait()
	waitCounters(t, s)
}

// The flags: defaults, both spellings of the environment names, refusals.
func TestConnectFlags(t *testing.T) {
	dir := t.TempDir()
	keyFile := filepath.Join(dir, "keys")
	pub, _, _ := ed25519.GenerateKey(crand.Reader)
	os.WriteFile(keyFile, []byte("k1 "+b64.EncodeToString(pub)+"\n"), 0o600)
	base := []string{"-pass-key-file", keyFile, "-pass-issuer", testIssuer, "-pass-audience", testAud, "-require-pass", "connect"}
	run := func(t *testing.T, env []string, args ...string) string {
		_, log := startMain(t, env, append(append([]string{}, base...), args...)...)
		return log
	}
	t.Run("defaults", func(t *testing.T) {
		log := run(t, nil)
		for _, want := range []string{"require_connect=true", "max_unauth_conns=64", "max_unauth_per_addr=4", "clients without AUTH are refused with ERROR 10"} {
			if !strings.Contains(log, want) {
				t.Fatalf("no %q in:\n%s", want, log)
			}
		}
	})
	for name, env := range map[string][]string{
		"RE_PSN names": {"RE_PSN_PASS_MAX_UNAUTH_CONNS=7", "RE_PSN_PASS_MAX_UNAUTH_PER_ADDR=3"},
		"PSNR names":   {"PSNR_PASS_MAX_UNAUTH_CONNS=7", "PSNR_PASS_MAX_UNAUTH_PER_ADDR=3"},
	} {
		t.Run(name, func(t *testing.T) {
			if log := run(t, env); !strings.Contains(log, "max_unauth_conns=7") || !strings.Contains(log, "max_unauth_per_addr=3") {
				t.Fatalf("environment not read:\n%s", log)
			}
		})
	}
	t.Run("flag", func(t *testing.T) {
		if log := run(t, nil, "-pass-max-unauth-conns", "0", "-pass-max-unauth-per-addr", "0"); !strings.Contains(log, "max_unauth_conns=0") {
			t.Fatalf("flag not read:\n%s", log)
		}
	})
	if testing.Short() {
		return
	}
	refuse := func(t *testing.T, env []string, args []string, want string) {
		cmd := exec.Command(os.Args[0], "-test.run=^TestMainProcess$")
		cmd.Env = append(cleanEnv(), "RE_PSN_TEST_MAIN=1", "RE_PSN_ADDR=127.0.0.1:0", "RE_PSN_TEST_ARGS="+strings.Join(args, "\x1f"))
		cmd.Env = append(cmd.Env, env...)
		out, err := cmd.CombinedOutput()
		if err == nil || !strings.Contains(string(out), want) {
			t.Fatalf("want a refusal naming %q, got %v:\n%s", want, err, out)
		}
	}
	t.Run("connect needs a key file", func(t *testing.T) {
		refuse(t, nil, []string{"-require-pass", "connect"}, "need -pass-key-file")
	})
	t.Run("negative", func(t *testing.T) {
		refuse(t, nil, append(append([]string{}, base...), "-pass-max-unauth-conns", "-1"), "must not be negative")
		refuse(t, nil, append(append([]string{}, base...), "-pass-max-unauth-per-addr", "-1"), "must not be negative")
	})
	t.Run("not a number", func(t *testing.T) {
		refuse(t, []string{"RE_PSN_PASS_MAX_UNAUTH_CONNS=many"}, base, "PASS_MAX_UNAUTH_CONNS")
	})
}
