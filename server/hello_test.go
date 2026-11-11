package main

import (
	"net/netip"
	"testing"
)

// clientNamed returns a snapshot of the connection holding onlineID: its title, name, token and room count.
func clientNamed(s *Server, onlineID string) (commID, name string, token uint32, rooms int, ok bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, c := range s.clients {
		if c.onlineID == onlineID {
			return c.commID, c.onlineID, c.token, len(c.rooms), true
		}
	}
	return "", "", 0, 0, false
}

// A connection sends HELLO once. A second one is refused with error 1 and
// changes nothing: the title, name, token and rooms stay as they were.
func TestSecondHelloRefused(t *testing.T) {
	s, addr := startServer(t)
	a := dial(t, addr, "NPWR00001_00", "alice", 3658)

	w := &writer{}
	w.u8(2)
	w.u32(0)
	w.blob([]byte("stage1"))
	w.blob([]byte("secret"))
	w.blob([]byte("homer"))
	if rt, _ := a.call(msgCreateRoom, w.b); rt != msgRoomJoined {
		t.Fatalf("create: 0x%02X", rt)
	}
	commID, name, token, rooms, ok := clientNamed(s, "alice")
	if !ok || rooms != 1 {
		t.Fatalf("before: ok %v rooms %d", ok, rooms)
	}

	for _, second := range [][2]string{{"NPWR99999_00", "carol"}, {"NPWR00001_00", "ALICE"}} {
		if rt, r := hello(a, second[0], second[1]); rt != msgError || r.u32() != errBadRequest {
			t.Fatalf("second hello %v was not refused with error 1", second)
		}
	}

	c2, name2, token2, rooms2, ok2 := clientNamed(s, "alice")
	if !ok2 || c2 != commID || name2 != name || token2 != token || rooms2 != rooms {
		t.Fatalf("second hello changed the player: title %q->%q name %q->%q token %d->%d rooms %d->%d",
			commID, c2, name, name2, token, token2, rooms, rooms2)
	}
	// the connection still works, and it still holds its room in its own title
	bob := dial(t, addr, "NPWR00001_00", "bob", 3659)
	w = &writer{}
	w.u16(0)
	w.u16(10)
	if _, r := bob.call(msgSearchRooms, w.b); r.u16() != 1 {
		t.Fatal("the room is gone from its title")
	}
	if _, _, _, _, ok := clientNamed(s, "carol"); ok {
		t.Fatal("a refused HELLO took a name")
	}
}

// v2tcp6 is a PROXY v2 header for a TCP stream from the IPv6 address src.
func v2tcp6(src netip.Addr) []byte {
	body := append(src.AsSlice(), netip.MustParseAddr("2001:db8::ffff").AsSlice()...)
	body = append(body, 0x30, 0x39, 0x8d, 0xa4)
	return v2(1, 0x21, body)
}

// Two IPv6 players are different players unless they share the address. This
// uses a trusted proxy (127.0.0.0/8) to name the IPv6 source of each
// connection, since loopback has only one IPv6 address to connect from.
func TestIPv6NameNotTakenFromAnotherAddress(t *testing.T) {
	addr := startProxied(t, "127.0.0.0/8", 0)
	first := netip.MustParseAddr("2001:db8::1")
	c1, rt, _ := helloAfter(t, addr, v2tcp6(first), "homer")
	if rt != msgHelloAck {
		t.Fatalf("first hello: 0x%02X", rt)
	}

	_, rt, r := helloAfter(t, addr, v2tcp6(netip.MustParseAddr("2001:db8::2")), "HOMER")
	if rt != msgError || r.u32() != errNameTaken {
		t.Fatal("another IPv6 player took the name")
	}
	if rt, _ := c1.call(msgSearchRooms, []byte{0, 0, 0, 10}); rt == msgError {
		t.Fatal("the first player's connection was closed")
	}

	// the same address is the same player reconnecting, as before
	if _, rt, _ := helloAfter(t, addr, v2tcp6(first), "homer"); rt != msgHelloAck {
		t.Fatalf("reconnect from the same IPv6 address: 0x%02X", rt)
	}
}

func TestSameAddr(t *testing.T) {
	a := netip.MustParseAddr
	for _, c := range []struct {
		name string
		x, y netip.Addr
		want bool
	}{
		{"IPv4 same", a("10.0.0.2"), a("10.0.0.2"), true},
		{"IPv4 different", a("10.0.0.2"), a("10.0.0.3"), false},
		{"IPv6 same", a("2001:db8::1"), a("2001:db8::1"), true},
		{"IPv6 different", a("2001:db8::1"), a("2001:db8::2"), false},
		{"mapped IPv4 is IPv4", a("::ffff:10.0.0.2"), a("10.0.0.2"), true},
		{"zone is ignored", a("fe80::1%eth0"), a("fe80::1"), true},
		{"unknown address matches nothing", netip.Addr{}, netip.Addr{}, false},
		{"unknown never matches a known one", netip.Addr{}, a("2001:db8::1"), false},
		{"known never matches unknown", a("2001:db8::1"), netip.Addr{}, false},
	} {
		if got := sameAddr(c.x, c.y); got != c.want {
			t.Errorf("%s: sameAddr(%v, %v) = %v", c.name, c.x, c.y, got)
		}
	}
}

// Players with no IPv4 address (c.ip zero, as for IPv6) are never on one LAN.
func TestSameNetworkNeedsIPv4(t *testing.T) {
	zero := &client{}
	if sameNetwork(zero, &client{}) {
		t.Fatal("two IPv6 players are on the same network")
	}
	lan := &client{ip: [4]byte{203, 0, 113, 9}}
	if !sameNetwork(lan, &client{ip: [4]byte{203, 0, 113, 9}}) {
		t.Fatal("IPv4 players on one address are not on the same network")
	}
	if sameNetwork(lan, zero) || sameNetwork(zero, lan) {
		t.Fatal("an IPv4 and an IPv6 player are on the same network")
	}
}

// Two IPv6 players, both with a LAN address recorded from a probe, are not on
// one LAN: the viewer gets the probed public endpoint, not the LAN address.
func TestEndpointIPv6PlayersNotSameLAN(t *testing.T) {
	m := &client{udpIP: [4]byte{203, 0, 113, 9}, udpPort: 40000, localIP: [4]byte{192, 168, 1, 5}, p2pPort: 3658}
	viewer := &client{}
	if ip, port := endpoint(m, viewer); ip != ([4]byte{203, 0, 113, 9}) || port != 40000 {
		t.Fatalf("endpoint for an IPv6 viewer: %v:%d", ip, port)
	}
}

// With the relay on, a stream to an IPv6 target is relayed unless the viewer
// is known to be on the target's network, which an IPv6 viewer never is.
func TestRelayStreamIPv6Viewer(t *testing.T) {
	s := &Server{relay: true}
	target := &client{udpPort: 40000, udpIP: [4]byte{203, 0, 113, 9}, localIP: [4]byte{192, 168, 1, 5}}
	if !s.relayStream(target, &client{}) {
		t.Fatal("a stream to an IPv6 viewer is sent direct")
	}
}
