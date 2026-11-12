package main

import (
	"net"
	"testing"
)

// getCaps sends GET_CAPS and returns the bits and the version.
func getCaps(t *testing.T, p player) (uint32, string) {
	t.Helper()
	rt, r := p.c.call(msgGetCaps, nil)
	if rt != msgCaps {
		t.Fatalf("GET_CAPS: reply 0x%02X, want CAPS", rt)
	}
	bits := r.u32()
	v := r.blob()
	if r.err != nil || len(r.b) != 0 {
		t.Fatalf("CAPS body: err %v, %d bytes left", r.err, len(r.b))
	}
	return bits, string(v)
}

const capAlways = capRooms | capScores | capRoomCodes | capPeerKeys

func TestCapsDefault(t *testing.T) {
	_, addr := startRelay(t, false)
	p, _ := joinTitle(t, addr, onTitle, "alice")
	bits, v := getCaps(t, p)
	if want := uint32(capAlways | capProbe); bits != want {
		t.Fatalf("bits = %#x, want %#x (no relay, no discovery)", bits, want)
	}
	if v != version || v == "" {
		t.Fatalf("version %q, want %q", v, version)
	}
}

func TestCapsRelayDiscoverAndNoPeer(t *testing.T) {
	s, addr := startRelay(t, true)
	s.mu.Lock()
	s.discover = true
	s.noPeer = map[string]bool{offTitle: true}
	s.mu.Unlock()
	on, _ := joinTitle(t, addr, onTitle, "alice")
	off, _ := joinTitle(t, addr, offTitle, "bob")
	if bits, _ := getCaps(t, on); bits != capAlways|capProbe|capRelay|capDiscover {
		t.Fatalf("title with peer traffic: bits %#x", bits)
	}
	if bits, _ := getCaps(t, off); bits != capAlways|capDiscover {
		t.Fatalf("-no-peer-titles title: bits %#x, want no probe and no relay", bits)
	}
}

// GET_CAPS needs a HELLO like every other request.
func TestCapsNeedsHello(t *testing.T) {
	_, addr := startRelay(t, true)
	conn, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	c := &tc{t: t, conn: conn}
	rt, r := c.call(msgGetCaps, nil)
	if rt != msgError || r.u32() != errNoHello {
		t.Fatalf("GET_CAPS before HELLO: reply 0x%02X, want ERROR no hello", rt)
	}
}

// CAPS is only ever the answer to GET_CAPS: a client that never asks (every client before it existed) is sent
// nothing new, whatever else it does, so it needs no code to ignore 0x94.
func TestCapsOnlyOnRequest(t *testing.T) {
	s, addr := startRelay(t, true)
	s.mu.Lock()
	s.discover = true
	s.mu.Unlock()
	a, _ := joinTitle(t, addr, onTitle, "alice")
	b, _ := joinTitle(t, addr, onTitle, "bob")
	room := meet(t, a, b)
	w := &writer{}
	w.u64(room)
	if rt, _ := a.c.call(msgGetRoomAccess, w.b); rt != msgRoomAccess {
		t.Fatalf("GET_ROOM_ACCESS: reply 0x%02X", rt)
	}
	if _, flags := joinTitle(t, addr, onTitle, "carol"); flags&1 == 0 {
		t.Fatalf("HELLO_ACK flags changed: %d", flags)
	}
	for _, p := range [][]pkt{a.c.pushes, b.c.pushes} {
		for _, m := range p {
			if m.t == msgCaps {
				t.Fatal("CAPS arrived without a GET_CAPS")
			}
		}
	}
	// the same connection can still ask, and gets the reply
	if bits, _ := getCaps(t, a); bits&capRelay == 0 {
		t.Fatalf("bits %#x", bits)
	}
}
