package main

import (
	"net"
	"net/netip"
	"testing"
	"time"
)

// The wrong-code limit also counts by address, so a new connection does not start the count again.

func TestCodeMissesCountByAddressAcrossConnections(t *testing.T) {
	_, addr := startServer(t)
	a := dial(t, addr, "NPWR00001_00", "alice", 3658)
	roomOf(t, a)
	answered := 0
	closed := false
	for round := 0; round < 8 && !closed; round++ {
		b := dial(t, addr, "NPWR00001_00", "bob"+string(rune('a'+round)), 3659)
		for i := 0; i < maxCodeMisses-1; i++ { // under the per-connection limit
			w := &writer{}
			w.u32(uint32(100 + i))
			w.str("ZZZZZZZZ", codeLen)
			w.blob(nil)
			if err := writePacket(b.conn, msgJoinByCode, w.b); err != nil {
				closed = true
				break
			}
			b.conn.SetReadDeadline(time.Now().Add(2 * time.Second))
			if typ, _, err := readPacket(b.conn); err != nil || typ != msgError {
				closed = true
				break
			}
			answered++
		}
		b.conn.Close()
	}
	if !closed {
		t.Fatalf("%d wrong codes from one address across connections, none closed", answered)
	}
	if answered != maxAddrCodeMisses-1 {
		t.Fatalf("answered %d wrong codes before the address limit closed a connection, want %d", answered, maxAddrCodeMisses-1)
	}
}

func TestAddressCodeMissesExpireWithTheWindow(t *testing.T) {
	s := NewServer(10, false)
	c := &client{addr: netip.MustParseAddr("203.0.113.5"), id: 1, conn: pipeConn(t)}
	s.addrMisses[addrLimitKey(c.addr)] = &addrMisses{start: time.Now().Add(-2 * codeMissWindow), n: maxAddrCodeMisses - 1}
	s.codeMiss(c)
	if am := s.addrMisses[addrLimitKey(c.addr)]; am.n != 1 {
		t.Fatalf("count after the window ran out: %d, want 1", am.n)
	}
}

func TestAddressCodeMissesAreForgotten(t *testing.T) {
	s := NewServer(10, false)
	old := time.Now().Add(-2 * codeMissWindow)
	for i := 0; i < 1024; i++ {
		s.addrMisses[netip.AddrFrom4([4]byte{10, 0, byte(i >> 8), byte(i)}).String()] = &addrMisses{start: old, n: 1}
	}
	s.codeMiss(&client{addr: netip.MustParseAddr("203.0.113.6"), id: 2, conn: pipeConn(t)})
	if len(s.addrMisses) != 1 {
		t.Fatalf("%d address entries after a miss with 1024 expired ones, want 1", len(s.addrMisses))
	}
}

func pipeConn(t *testing.T) net.Conn {
	a, b := net.Pipe()
	t.Cleanup(func() { a.Close(); b.Close() })
	return a
}
