package main

import (
	"bytes"
	"net"
	"testing"
	"time"
)

// peerKeys returns the PEER_KEY pushes c has (waiting up to d for more):
// room, the user the key is about, and the key.
type peerKey struct {
	room uint64
	user uint32
	key  []byte
}

func peerKeys(c *tc, d time.Duration) []peerKey {
	var out []peerKey
	take := func(p pkt) {
		if p.t != msgPeerKey {
			return
		}
		r := &reader{b: p.p}
		out = append(out, peerKey{r.u64(), r.u32(), append([]byte(nil), r.take(peerKeyLen)...)})
	}
	for _, p := range c.pushes {
		take(p)
	}
	c.pushes = nil
	for {
		c.conn.SetReadDeadline(time.Now().Add(d))
		t, p, err := readPacket(c.conn)
		if err != nil {
			return out
		}
		take(pkt{t, p})
	}
}

func optIn(t *testing.T, p player) {
	t.Helper()
	if rt, _ := p.c.call(msgPeerAuth, nil); rt != msgOK {
		t.Fatalf("PEER_AUTH: 0x%02X", rt)
	}
}

func keyAbout(t *testing.T, keys []peerKey, user uint32) peerKey {
	t.Helper()
	for _, k := range keys {
		if k.user == user {
			return k
		}
	}
	t.Fatalf("no key about user %d in %+v", user, keys)
	return peerKey{}
}

// Two members who both asked get the same 16-byte key for each other, in the
// room they share.
func TestPeerKeyPair(t *testing.T) {
	_, addr := startRelay(t, false)
	a, b := join(t, addr, "alice"), join(t, addr, "bob")
	optIn(t, a)
	optIn(t, b)
	room := meet(t, a, b)
	ka, kb := peerKeys(a.c, 200*time.Millisecond), peerKeys(b.c, 200*time.Millisecond)
	if len(ka) != 1 || len(kb) != 1 {
		t.Fatalf("pushes: alice %d, bob %d, want one each", len(ka), len(kb))
	}
	about := keyAbout(t, ka, b.id)
	if about.room != room || len(about.key) != peerKeyLen || bytes.Equal(about.key, make([]byte, peerKeyLen)) {
		t.Fatalf("alice's key: %+v", about)
	}
	if got := keyAbout(t, kb, a.id); got.room != room || !bytes.Equal(got.key, about.key) {
		t.Fatalf("bob's key %x, alice's %x: want the same", got.key, about.key)
	}
}

// With three members each pair has its own key, and a third member's key for
// one pair is not the pair's.
func TestPeerKeysPerPair(t *testing.T) {
	_, addr := startRelay(t, false)
	a, b, c := join(t, addr, "alice"), join(t, addr, "bob"), join(t, addr, "carol")
	for _, p := range []player{a, b, c} {
		optIn(t, p)
	}
	room := meet(t, a, b)
	w := &writer{}
	w.u64(room)
	w.blob(nil)
	if rt, _ := c.c.call(msgJoinRoom, w.b); rt != msgRoomJoined {
		t.Fatalf("join: 0x%02X", rt)
	}
	ka, kb, kc := peerKeys(a.c, 200*time.Millisecond), peerKeys(b.c, 200*time.Millisecond), peerKeys(c.c, 200*time.Millisecond)
	if len(ka) != 2 || len(kb) != 2 || len(kc) != 2 {
		t.Fatalf("pushes: %d %d %d, want two each", len(ka), len(kb), len(kc))
	}
	ab, ac, bc := keyAbout(t, ka, b.id).key, keyAbout(t, ka, c.id).key, keyAbout(t, kb, c.id).key
	if !bytes.Equal(ab, keyAbout(t, kb, a.id).key) || !bytes.Equal(ac, keyAbout(t, kc, a.id).key) || !bytes.Equal(bc, keyAbout(t, kc, b.id).key) {
		t.Fatal("the two ends of a pair do not hold the same key")
	}
	if bytes.Equal(ab, ac) || bytes.Equal(ab, bc) || bytes.Equal(ac, bc) {
		t.Fatal("two pairs share a key")
	}
}

// A member who did not ask gets no key, and nobody gets a key about them.
func TestPeerKeyNeedsBoth(t *testing.T) {
	_, addr := startRelay(t, false)
	a, b := join(t, addr, "alice"), join(t, addr, "bob")
	optIn(t, a)
	meet(t, a, b)
	if k := peerKeys(a.c, 200*time.Millisecond); len(k) != 0 {
		t.Fatalf("alice got %+v about a member who did not ask", k)
	}
	if k := peerKeys(b.c, 200*time.Millisecond); len(k) != 0 {
		t.Fatalf("bob did not ask and got %+v", k)
	}
}

// Asking after the join works too: the second to ask completes the pair.
func TestPeerKeyLateOptIn(t *testing.T) {
	_, addr := startRelay(t, false)
	a, b := join(t, addr, "alice"), join(t, addr, "bob")
	meet(t, a, b)
	optIn(t, a)
	if k := peerKeys(a.c, 200*time.Millisecond); len(k) != 0 {
		t.Fatalf("a key before the pair is complete: %+v", k)
	}
	optIn(t, b)
	ka, kb := peerKeys(a.c, 200*time.Millisecond), peerKeys(b.c, 200*time.Millisecond)
	if len(ka) != 1 || len(kb) != 1 || !bytes.Equal(keyAbout(t, ka, b.id).key, keyAbout(t, kb, a.id).key) {
		t.Fatalf("late opt-in: alice %+v, bob %+v", ka, kb)
	}
}

// Asking twice changes nothing; a rejoin gets a fresh key.
func TestPeerKeyRepeatAndRejoin(t *testing.T) {
	_, addr := startRelay(t, false)
	a, b := join(t, addr, "alice"), join(t, addr, "bob")
	optIn(t, a)
	optIn(t, b)
	room := meet(t, a, b)
	first := keyAbout(t, peerKeys(a.c, 200*time.Millisecond), b.id).key
	peerKeys(b.c, 200*time.Millisecond)
	optIn(t, a)
	optIn(t, b)
	if k := peerKeys(a.c, 200*time.Millisecond); len(k) != 0 {
		t.Fatalf("asking again sent %+v", k)
	}
	peerKeys(b.c, 200*time.Millisecond)

	w := &writer{}
	w.u64(room)
	if rt, _ := b.c.call(msgLeaveRoom, w.b); rt != msgOK {
		t.Fatalf("leave: 0x%02X", rt)
	}
	w = &writer{}
	w.u64(room)
	w.blob(nil)
	if rt, _ := b.c.call(msgJoinRoom, w.b); rt != msgRoomJoined {
		t.Fatalf("rejoin: 0x%02X", rt)
	}
	second := keyAbout(t, peerKeys(a.c, 200*time.Millisecond), b.id).key
	if bytes.Equal(first, second) {
		t.Fatal("a rejoin got the old key")
	}
}

// PEER_AUTH before HELLO is refused like any request before it, and HELLO_ACK
// says the server issues keys.
func TestPeerAuthNeedsHello(t *testing.T) {
	addr := start(t)
	conn, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	c := &tc{t: t, conn: conn}
	if rt, r := c.call(msgPeerAuth, nil); rt != msgError || r.u32() != errNoHello {
		t.Fatalf("PEER_AUTH before HELLO: 0x%02X", rt)
	}
	w := &writer{}
	w.str("NPWR00001_00", 12)
	w.str("alice", 16)
	w.u16(3658)
	rt, r := c.call(msgHello, w.b)
	if rt != msgHelloAck {
		t.Fatalf("hello: 0x%02X", rt)
	}
	r.take(12) // user id, public ip, token
	if flags := r.u8(); flags&helloPeerKeys == 0 || flags&2 == 0 {
		t.Fatalf("HELLO_ACK flags %#x: want the peer-key bit and the room-code bit", flags)
	}
}
