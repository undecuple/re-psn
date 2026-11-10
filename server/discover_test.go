package main

import (
	"bytes"
	"encoding/binary"
	"net"
	"net/netip"
	"strings"
	"testing"
	"time"
)

func discoverPkt(nonce uint32) []byte {
	p := make([]byte, discoverRequestLen)
	copy(p, udpMagic)
	p[4] = udpDiscover
	binary.BigEndian.PutUint32(p[5:], nonce)
	return p
}

func startDiscover(t *testing.T, on bool) (*Server, string, *net.UDPConn) {
	return startDiscoverRelay(t, on, false)
}

func startDiscoverRelay(t *testing.T, on, relay bool) (*Server, string, *net.UDPConn) {
	s, addr := startTCPUDPWith(t, func(s *Server) {
		s.discover, s.name, s.tcpPort, s.relay = on, "lan box", 4242, relay
	})
	ua, _ := net.ResolveUDPAddr("udp", addr)
	u, err := net.DialUDP("udp", nil, ua)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { u.Close() })
	return s, addr, u
}

func readReply(u *net.UDPConn, wait time.Duration) []byte {
	u.SetReadDeadline(time.Now().Add(wait))
	buf := make([]byte, 256)
	n, err := u.Read(buf)
	if err != nil {
		return nil
	}
	return buf[:n]
}

func TestDiscoverOffNoReply(t *testing.T) {
	s, _, u := startDiscover(t, false)
	u.Write(discoverPkt(1))
	if r := readReply(u, 200*time.Millisecond); r != nil {
		t.Fatalf("reply with -discover off: %x", r)
	}
	waitUDPCount(t, &s.udpDrops.malformed, 1) // as today: an unknown type is counted as malformed
}

func TestDiscoverReply(t *testing.T) {
	_, addr, u := startDiscoverRelay(t, true, true)
	conn, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	helloToken(t, &tc{t: t, conn: conn}, "homer", 3658)

	req := discoverPkt(0xdeadbeef)
	u.Write(req)
	r := readReply(u, time.Second)
	if len(r) != discoverReplyLen || len(r) > len(req) {
		t.Fatalf("reply is %d bytes, request %d", len(r), len(req))
	}
	if string(r[:4]) != udpMagic || r[4] != udpDiscoverReply || binary.BigEndian.Uint32(r[5:]) != 0xdeadbeef {
		t.Fatalf("header or nonce: %x", r[:9])
	}
	udpPort := uint16(mustPort(t, addr))
	if binary.BigEndian.Uint16(r[9:]) != 4242 || binary.BigEndian.Uint16(r[11:]) != udpPort {
		t.Fatalf("ports %d %d", binary.BigEndian.Uint16(r[9:]), binary.BigEndian.Uint16(r[11:]))
	}
	if r[13] != 1 || binary.BigEndian.Uint16(r[14:]) != 1 {
		t.Fatalf("flags %d players %d", r[13], binary.BigEndian.Uint16(r[14:]))
	}
	want := append([]byte("lan box"), make([]byte, 24-len("lan box"))...)
	if !bytes.Equal(r[16:40], want) {
		t.Fatalf("name %q", r[16:40])
	}
	ver := append([]byte(version[:min(len(version), 12)]), make([]byte, 12-min(len(version), 12))...)
	if !bytes.Equal(r[40:52], ver) {
		t.Fatalf("version %q", r[40:52])
	}
}

func mustPort(t *testing.T, addr string) int {
	ua, err := net.ResolveUDPAddr("udp", addr)
	if err != nil {
		t.Fatal(err)
	}
	return ua.Port
}

func TestDiscoverWrongLengthAndOtherTypes(t *testing.T) {
	s, _, u := startDiscover(t, true)
	u.Write(discoverPkt(1)[:63])
	u.Write(append(discoverPkt(1), 0))
	// A RELAY of exactly 64 bytes is not a DISCOVER: it reaches the relay path (off here).
	relay := make([]byte, 64)
	copy(relay, udpMagic)
	relay[4] = udpRelay
	u.Write(relay)
	if r := readReply(u, 200*time.Millisecond); r != nil {
		t.Fatalf("answered %x", r)
	}
	waitUDPCount(t, &s.udpDrops.malformed, 2)
	waitUDPCount(t, &s.udpDrops.relayDisabled, 1)
}

func TestDiscoverSourceClasses(t *testing.T) {
	for ip, want := range map[string]bool{
		"10.1.2.3": true, "172.16.0.1": true, "172.31.255.255": true, "192.168.1.9": true,
		"100.64.0.1": true, "100.127.255.254": true, "169.254.3.4": true, "127.0.0.1": true,
		"::1": true, "::ffff:192.168.0.2": true, "fe80::1": true, "fd00::1": true,
		"::ffff:8.8.8.8": false, "0.0.0.0": false, "0.1.2.3": false, "224.0.0.1": false, "255.255.255.255": false,
		"ff02::1": false, "64:ff9b::c0a8:101": false,
		"8.8.8.8": false, "172.32.0.1": false, "172.15.0.1": false, "100.128.0.1": false,
		"100.63.255.255": false, "192.169.0.1": false, "11.0.0.1": false, "203.0.113.7": false,
		"2001:db8::1": false,
	} {
		if got := discoverSource(netip.MustParseAddr(ip)); got != want {
			t.Errorf("%s: %v, want %v", ip, got, want)
		}
	}
}

func TestDiscoverPublicSourceRefused(t *testing.T) {
	s := NewServer(1, false)
	s.discover = true
	pc, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer pc.Close()
	to, _ := net.ListenPacket("udp", "127.0.0.1:0")
	defer to.Close()
	s.discoverUDP(pc, discoverPkt(1), to.LocalAddr().(*net.UDPAddr), netip.MustParseAddr("203.0.113.7"))
	to.SetReadDeadline(time.Now().Add(150 * time.Millisecond))
	if n, _, err := to.ReadFrom(make([]byte, 100)); err == nil {
		t.Fatalf("a public source got %d bytes", n)
	}
	if s.udpDrops.discoverRefused.Load() != 1 {
		t.Fatal("refusal not counted")
	}
}

func TestDiscoverRateLimited(t *testing.T) {
	s, addr := startTCPUDPWith(t, func(s *Server) { s.discover, s.maxUDPRate = true, 3 })
	ua, _ := net.ResolveUDPAddr("udp", addr)
	u, err := net.DialUDP("udp", nil, ua)
	if err != nil {
		t.Fatal(err)
	}
	defer u.Close()
	for i := 0; i < 20; i++ {
		u.Write(discoverPkt(uint32(i)))
	}
	got := 0
	for readReply(u, 300*time.Millisecond) != nil {
		got++
	}
	if got == 0 || got > 6 {
		t.Fatalf("%d replies to 20 requests at 3 per second", got)
	}
	if s.udpDrops.rateLimit.Load() == 0 {
		t.Fatal("no rate_limit drops counted")
	}
}

func TestDiscoverNameRules(t *testing.T) {
	for name, ok := range map[string]bool{
		"": true, "Garage LAN": true, strings.Repeat("a", 24): true,
		strings.Repeat("a", 25): false, "tab\there": false, "nl\n": false,
		"café": false, "\x7f": false, "nul\x00": false,
	} {
		if err := validServerName(name); (err == nil) != ok {
			t.Errorf("%q: %v", name, err)
		}
	}
}
