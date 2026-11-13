package main

import (
	"net"
	"testing"
	"time"
)

// A stream's accepting side takes the stream out of s.streams, answers its player and then hands over the connection. If the
// connecting side's wait ran out in between, that connection must still be used, not left in the channel with nobody to close it.

func TestAwaitAcceptUsesLegClaimedAtTimeout(t *testing.T) {
	s := NewServer(10, false)
	st := &stream{id: 7, accept: make(chan net.Conn, 1)}
	// The stream is no longer in s.streams: the accepting side has claimed it.
	ca, sa := tcpPair(t)
	_ = ca
	go func() {
		time.Sleep(50 * time.Millisecond)
		st.accept <- sa
	}()
	if got := s.awaitAccept(st, time.Millisecond); got != sa {
		t.Fatalf("awaitAccept returned %v after the wait ran out, want the claimed leg", got)
	}
}

func TestAwaitAcceptClaimedThenFailed(t *testing.T) {
	s := NewServer(10, false)
	st := &stream{id: 8, accept: make(chan net.Conn, 1)}
	go func() {
		time.Sleep(20 * time.Millisecond)
		st.accept <- nil
	}()
	if got := s.awaitAccept(st, time.Millisecond); got != nil {
		t.Fatalf("awaitAccept returned %v, want nil when the accepting side failed", got)
	}
}

func TestAwaitAcceptTimesOutWhenUnclaimed(t *testing.T) {
	s := NewServer(10, false)
	st := &stream{id: 9, accept: make(chan net.Conn, 1)}
	s.streams[st.id] = st
	start := time.Now()
	if got := s.awaitAccept(st, 20*time.Millisecond); got != nil {
		t.Fatalf("awaitAccept returned %v with no accepting side", got)
	}
	if time.Since(start) > 2*time.Second {
		t.Fatal("awaitAccept did not return after its wait")
	}
	if s.streams[st.id] != nil {
		t.Fatal("the unclaimed stream was left in s.streams")
	}
}
