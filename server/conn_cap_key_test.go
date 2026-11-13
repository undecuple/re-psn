package main

import (
	"net"
	"net/netip"
	"testing"
	"time"
)

func TestAddrLimitKey(t *testing.T) {
	for _, c := range []struct{ in, want string }{
		{"203.0.113.9", "203.0.113.9"},
		{"::ffff:203.0.113.9", "203.0.113.9"}, // an IPv4-mapped address is the IPv4 address
		{"2001:db8:1:2::1", "2001:db8:1:2::/64"},
		{"2001:db8:1:2:ffff:ffff:ffff:ffff", "2001:db8:1:2::/64"}, // the low 64 bits do not matter
		{"2001:db8:1:3::1", "2001:db8:1:3::/64"},                  // another /64 is another source
		{"fe80::1%eth0", "fe80::/64"},                             // the zone does not matter
	} {
		if got := addrLimitKey(netip.MustParseAddr(c.in)); got != c.want {
			t.Errorf("addrLimitKey(%s) = %s, want %s", c.in, got, c.want)
		}
	}
	if addrLimitKey(netip.MustParseAddr("2001:db8:1:2::1")) == addrLimitKey(netip.MustParseAddr("2001:db8:1:3::1")) {
		t.Error("two /64 networks share a key")
	}
}

// startCapped starts a server behind a trusted proxy with the given per-address limit and returns it with its address.
func startCapped(t *testing.T, perAddr int) (*Server, string) {
	t.Helper()
	s := NewServer(10, false)
	var err error
	if s.trustedProxies, err = parseTrusted("127.0.0.0/8"); err != nil {
		t.Fatal(err)
	}
	s.proxyProto, s.maxPerAddr = true, perAddr
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	go s.Serve(ln)
	return s, ln.Addr().String()
}

func v1from(addr string) []byte {
	return []byte("PROXY TCP6 " + addr + " ::1 51000 36100\r\n")
}

// closedAtOnce reports whether the server closes a connection that starts with prefix (a refused one) instead of serving it.
func closedAtOnce(t *testing.T, addr string, prefix []byte) bool {
	t.Helper()
	conn, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	conn.Write(prefix)
	conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	var one [1]byte
	_, err = conn.Read(one[:])
	if ne, ok := err.(net.Error); ok && ne.Timeout() {
		return false
	}
	return err != nil
}

// Addresses in one IPv6 /64 share the per-address limit; another /64 has its own.
func TestPerAddrLimitFoldsIPv6To64(t *testing.T) {
	s, addr := startCapped(t, 2)
	for i, src := range []string{"2001:db8:1:2::1", "2001:db8:1:2::2"} {
		if _, rt, _ := helloAfter(t, addr, v1from(src), "p"+string(rune('a'+i))); rt != msgHelloAck {
			t.Fatalf("connection %d from %s: 0x%02X", i+1, src, rt)
		}
	}
	if !closedAtOnce(t, addr, v1from("2001:db8:1:2:aaaa:bbbb:cccc:dddd")) {
		t.Fatal("a third address of the same /64 was served over the limit of 2")
	}
	if _, rt, _ := helloAfter(t, addr, v1from("2001:db8:1:3::1"), "pc"); rt != msgHelloAck {
		t.Fatalf("a different /64 was refused: 0x%02X", rt)
	}
	// IPv4 behaves as before: the same address twice is fine up to the limit, one more is refused
	for i := 0; i < 2; i++ {
		if _, rt, _ := helloAfter(t, addr, []byte("PROXY TCP4 203.0.113.9 10.0.0.1 51000 36100\r\n"), "q"+string(rune('a'+i))); rt != msgHelloAck {
			t.Fatalf("IPv4 connection %d: 0x%02X", i+1, rt)
		}
	}
	if !closedAtOnce(t, addr, []byte("PROXY TCP4 203.0.113.9 10.0.0.1 51000 36100\r\n")) {
		t.Fatal("a third IPv4 connection from one address was served over the limit of 2")
	}
	s.mu.Lock()
	n := len(s.addrConns)
	s.mu.Unlock()
	if n != 3 { // the two /64 keys and the IPv4 address
		t.Fatalf("%d keys counted, want 3", n)
	}
}

// The count is released under the same key it was taken under, so closing connections frees the allowance and the table empties.
func TestPerAddrLimitReleasesTheSameKey(t *testing.T) {
	s, addr := startCapped(t, 1)
	conn, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	conn.Write(v1from("2001:db8:1:2::1"))
	c := &tc{t: t, conn: conn}
	if rt, _ := hello(c, "NPWR00001_00", "alice"); rt != msgHelloAck {
		t.Fatalf("hello: 0x%02X", rt)
	}
	conn.Close()
	deadline := time.Now().Add(3 * time.Second)
	for {
		s.mu.Lock()
		n := len(s.addrConns)
		s.mu.Unlock()
		if n == 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("%d keys still counted after the connection closed", n)
		}
		time.Sleep(20 * time.Millisecond)
	}
	if _, rt, _ := helloAfter(t, addr, v1from("2001:db8:1:2::9"), "bob"); rt != msgHelloAck {
		t.Fatalf("the allowance was not freed: 0x%02X", rt)
	}
}
