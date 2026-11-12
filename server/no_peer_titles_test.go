package main

import (
	"encoding/binary"
	"net"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

const (
	offTitle = "NPWR00001_00" // the title helloToken uses
	onTitle  = "NPWR00002_00"
)

// joinTitle is join for a title of its own; it also returns the flags byte of HELLO_ACK.
func joinTitle(t *testing.T, addr, commID, name string) (player, uint8) {
	conn, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	c := &tc{t: t, conn: conn}
	w := &writer{}
	w.str(commID, 12)
	w.str(name, 16)
	w.u16(3658)
	rt, r := c.call(msgHello, w.b)
	if rt != msgHelloAck {
		t.Fatalf("hello: 0x%02X", rt)
	}
	id := r.u32()
	r.take(4)
	token := r.u32()
	return player{c, id, token}, r.u8()
}

func TestParseTitleList(t *testing.T) {
	if m, err := parseTitleList(" "); m != nil || err != nil {
		t.Fatalf("empty: %v, %v", m, err)
	}
	m, err := parseTitleList("NPWR00001_00, NPWR00002_00,NPWR00001_00")
	if err != nil || len(m) != 2 || !m["NPWR00001_00"] || !m["NPWR00002_00"] {
		t.Fatalf("list: %v, %v", m, err)
	}
	for _, bad := range []string{"A,,B", "NPWR00001_00X", "has space", "caf\xc3\xa9", ","} {
		if _, err := parseTitleList(bad); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
}

// A title in -no-peer-titles gets no probe reply and no recorded endpoint; another title on the same server does.
func TestNoPeerTitleProbe(t *testing.T) {
	s, addr := startRelay(t, true)
	s.noPeer = map[string]bool{offTitle: true}
	off, _ := joinTitle(t, addr, offTitle, "alice")
	on, _ := joinTitle(t, addr, onTitle, "bob")

	srv, _ := net.ResolveUDPAddr("udp", addr)
	uOff := udpSock(t)
	pkt := append([]byte(udpMagic), udpProbe)
	pkt = binary.BigEndian.AppendUint32(pkt, off.id)
	pkt = binary.BigEndian.AppendUint32(pkt, off.token)
	pkt = append(pkt, 127, 0, 0, 1)
	uOff.WriteTo(pkt, srv)
	if got := recvWithin(uOff, 400*time.Millisecond); got != nil {
		t.Fatalf("a probe from a -no-peer-titles title was answered: %q", got)
	}
	s.mu.Lock()
	port := s.clients[off.id].udpPort
	s.mu.Unlock()
	if port != 0 {
		t.Fatalf("recorded UDP port %d for a -no-peer-titles title", port)
	}
	probeFrom(t, udpSock(t), addr, on) // fails the test if there is no reply
}

// HELLO_ACK says the relay is available only to titles that use peer traffic.
func TestNoPeerTitleHelloFlag(t *testing.T) {
	s, addr := startRelay(t, true)
	s.noPeer = map[string]bool{offTitle: true}
	if _, flags := joinTitle(t, addr, offTitle, "alice"); flags&1 != 0 {
		t.Fatalf("relay flag set for a -no-peer-titles title: %d", flags)
	}
	if _, flags := joinTitle(t, addr, onTitle, "bob"); flags&1 == 0 {
		t.Fatalf("relay flag missing for another title: %d", flags)
	}
}

// Even with -relay-force, a join pushes no stream-relay route for a -no-peer-titles title.
func TestNoPeerTitleRoute(t *testing.T) {
	s, addr := startRelay(t, true)
	s.relayForce = true
	s.noPeer = map[string]bool{offTitle: true}
	for _, c := range []struct {
		title string
		want  uint8
	}{{offTitle, 0}, {onTitle, routeStreamRelay}} {
		a, _ := joinTitle(t, addr, c.title, "alice"+c.title[7:9])
		b, _ := joinTitle(t, addr, c.title, "bob"+c.title[7:9])
		meet(t, a, b)
		r := b.c.next(msgRoute)
		r.u64()
		if who, flags := r.u32(), r.u8(); who != a.id || flags != c.want {
			t.Fatalf("%s: route about %d flags %d, want %d", c.title, who, flags, c.want)
		}
	}
}

// A relayed datagram or stream for a -no-peer-titles title is refused. The control run, with the title
// taken off the list, delivers the same packet.
func TestNoPeerTitleRelayRefused(t *testing.T) {
	s, addr := startRelay(t, true)
	srv, _ := net.ResolveUDPAddr("udp", addr)
	a, _ := joinTitle(t, addr, offTitle, "alice")
	b, _ := joinTitle(t, addr, offTitle, "bob")
	meet(t, a, b)
	ua, ub := udpSock(t), udpSock(t)
	probeFrom(t, ub, addr, b) // not off yet: the endpoint is recorded

	s.mu.Lock()
	s.noPeer = map[string]bool{offTitle: true}
	s.mu.Unlock()
	before := s.udpDrops.relayDisabled.Load()
	ua.WriteTo(relayPkt(a, a.token, b.id, "hi"), srv)
	if got := recvWithin(ub, 400*time.Millisecond); got != nil {
		t.Fatalf("relayed a datagram for a -no-peer-titles title: %q", got)
	}
	waitUDPCount(t, &s.udpDrops.relayDisabled, before+1)

	w := &writer{}
	w.u32(a.id)
	w.u32(a.token)
	w.u32(b.id)
	w.u16(3658)
	if _, rt := streamLeg(t, addr, msgStreamConnect, w.b); rt != msgError {
		t.Fatalf("stream for a -no-peer-titles title: reply 0x%02X, want an error", rt)
	}

	s.mu.Lock()
	s.noPeer = nil
	s.mu.Unlock()
	ua.WriteTo(relayPkt(a, a.token, b.id, "hi"), srv)
	if got := recvWithin(ub, time.Second); got == nil {
		t.Fatal("control: the datagram was not relayed with the title off the list")
	}
}

// On a live server a listed title never probes, so its target has no endpoint: the datagram is still counted
// as relay_disabled, not unauthorized, and a forged token still counts as unauthorized.
func TestNoPeerTitleRelayCounter(t *testing.T) {
	s, addr := startRelay(t, true)
	s.mu.Lock()
	s.noPeer = map[string]bool{offTitle: true}
	s.mu.Unlock()
	srv, _ := net.ResolveUDPAddr("udp", addr)
	a, _ := joinTitle(t, addr, offTitle, "alice")
	b, _ := joinTitle(t, addr, offTitle, "bob")
	meet(t, a, b)
	other, _ := joinTitle(t, addr, onTitle, "carol")
	probeFrom(t, udpSock(t), addr, other) // a real probe first, so the test's writes are ordered before the relay path reads them
	ua := udpSock(t)

	ua.WriteTo(relayPkt(a, a.token, b.id, "hi"), srv)
	waitUDPCount(t, &s.udpDrops.relayDisabled, 1)
	if got := s.udpDrops.unauthorized.Load(); got != 0 {
		t.Fatalf("unauthorized = %d for a -no-peer-titles datagram", got)
	}
	ua.WriteTo(relayPkt(a, a.token+1, b.id, "forged"), srv)
	waitUDPCount(t, &s.udpDrops.unauthorized, 1)
	if got := s.udpDrops.relayDisabled.Load(); got != 1 {
		t.Fatalf("relay_disabled = %d after a forged token, want 1", got)
	}
}

// The setting is read from the flag and from RE_PSN_NO_PEER_TITLES, and a bad ID stops start-up.
func TestNoPeerTitlesFlagAndEnv(t *testing.T) {
	if testing.Short() {
		t.Skip("starts the server in a subprocess")
	}
	for name, c := range map[string]struct {
		env  []string
		args []string
	}{
		"flag": {nil, []string{"-no-peer-titles=NPWR00001_00"}},
		"env":  {[]string{"RE_PSN_NO_PEER_TITLES=NPWR00001_00"}, nil},
	} {
		t.Run(name, func(t *testing.T) {
			_, log := startMain(t, c.env, c.args...)
			if !strings.Contains(log, "no-peer-titles") {
				t.Fatalf("no start-up line:\n%s", log)
			}
		})
	}
	cmd := exec.Command(os.Args[0], "-test.run=^TestMainProcess$")
	cmd.Env = append(cleanEnv(), "RE_PSN_TEST_MAIN=1", "RE_PSN_ADDR=127.0.0.1:0", "RE_PSN_TEST_ARGS=-no-peer-titles=bad id")
	out, err := cmd.CombinedOutput()
	if err == nil || !strings.Contains(string(out), "-no-peer-titles") {
		t.Fatalf("want a refusal naming -no-peer-titles, got %v:\n%s", err, out)
	}
}
