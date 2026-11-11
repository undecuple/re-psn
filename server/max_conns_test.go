package main

import (
	"net"
	"testing"
	"time"
)

// -max-conns bounds the open TCP connections in total. A refused connection is closed without an answer, counted,
// and does not hold a slot; closing a connection frees its slot; 0 means no limit.

func connsServer(t *testing.T, max, perAddr int) (*Server, string) {
	s := NewServer(10, false)
	s.maxConns = max
	s.maxPerAddr = perAddr // every test connection comes from 127.0.0.1, so 0 here leaves only the total under test
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	go s.Serve(ln)
	return s, ln.Addr().String()
}

// connsRefused dials and reports whether the server closed the connection without answering.
func connsRefused(t *testing.T, addr string) bool {
	t.Helper()
	conn, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	_, _, err = readPacket(conn)
	return err != nil
}

func connsOpen(s *Server) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.openConns
}

func TestMaxConnsRefusesBeyondTheTotal(t *testing.T) {
	s, addr := connsServer(t, 3, 0)
	a := dial(t, addr, "NPWR00001_00", "alice", 3658)
	dial(t, addr, "NPWR00001_00", "bob", 3659)
	dial(t, addr, "NPWR00001_00", "carol", 3660)

	for i := 0; i < 5; i++ {
		if !connsRefused(t, addr) {
			t.Fatalf("connection %d beyond the limit was served", i)
		}
	}
	if n := connsOpen(s); n != 3 {
		t.Fatalf("%d connections counted open after refusals, want 3 (a refusal must not hold a slot)", n)
	}
	if got := s.snapshot().TCPRefusals.Conns; got != 5 {
		t.Fatalf("refusals counted %d, want 5", got)
	}
	if got := s.snapshot().Limits.MaxConns; got != 3 {
		t.Fatalf("stats report max_conns %d, want 3", got)
	}

	// closing one frees its slot
	a.conn.Close()
	deadline := time.Now().Add(2 * time.Second)
	for connsOpen(s) != 2 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	dial(t, addr, "NPWR00001_00", "dave", 3661)
	if n := connsOpen(s); n != 3 {
		t.Fatalf("%d open after the freed slot was used, want 3", n)
	}
}

func TestMaxConnsZeroIsNoLimit(t *testing.T) {
	s, addr := connsServer(t, 0, 0)
	for i := 0; i < 40; i++ {
		dial(t, addr, "NPWR00001_00", "p"+string(rune('a'+i%26))+string(rune('a'+i/26)), uint16(4000+i))
	}
	if n := connsOpen(s); n != 40 {
		t.Fatalf("%d open with no limit, want 40", n)
	}
	if got := s.snapshot().TCPRefusals.Conns; got != 0 {
		t.Fatalf("refusals counted %d with no limit", got)
	}
}

// The per-address limit still applies first and keeps its own counter.
func TestMaxConnsAndPerAddrCountSeparately(t *testing.T) {
	s, addr := connsServer(t, 10, 1)
	dial(t, addr, "NPWR00001_00", "alice", 3658)
	if !connsRefused(t, addr) {
		t.Fatal("second connection from one address was served")
	}
	st := s.snapshot().TCPRefusals
	if st.ConnsPerAddr != 1 || st.Conns != 0 {
		t.Fatalf("refusals %+v, want per-address 1 and total 0", st)
	}
}

// The default is on and finite.
func TestMaxConnsDefault(t *testing.T) {
	if s := NewServer(10, false); s.maxConns != defaultMaxConns || defaultMaxConns <= 0 {
		t.Fatalf("default maxConns %d", s.maxConns)
	}
}
