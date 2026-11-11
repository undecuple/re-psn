package main

import (
	"io"
	"net"
	"sync/atomic"
	"testing"
	"time"
)

// A relay stream with no bytes in either direction for -relay-idle is closed at both legs. Bytes one way keep it
// alive; 0 means no idle limit. These tests run splice directly on two loopback TCP pairs: ca and cb are the players'
// ends, the server holds the other ends.

func tcpPair(t *testing.T) (client, server net.Conn) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	ch := make(chan net.Conn, 1)
	go func() {
		c, _ := ln.Accept()
		ch <- c
	}()
	client, err = net.Dial("tcp", ln.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	server = <-ch
	t.Cleanup(func() { client.Close(); server.Close() })
	return client, server
}

// spliced starts a splice with the given idle time and returns the players' ends and a channel closed when it returns.
func spliced(t *testing.T, idle time.Duration) (*Server, net.Conn, net.Conn, chan struct{}) {
	ca, sa := tcpPair(t)
	cb, sb := tcpPair(t)
	s := NewServer(10, false)
	s.relayIdle = idle
	done := make(chan struct{})
	go func() { s.splice(sa, sb); close(done) }()
	return s, ca, cb, done
}

// closedWithin reports whether reading c ends (EOF or reset) within d.
func closedWithin(c net.Conn, d time.Duration) bool {
	c.SetReadDeadline(time.Now().Add(d))
	_, err := c.Read(make([]byte, 1))
	if err == nil {
		return false
	}
	if ne, ok := err.(net.Error); ok && ne.Timeout() {
		return false
	}
	return true
}

func TestSpliceIdleClosesBothLegs(t *testing.T) {
	_, ca, cb, done := spliced(t, 300*time.Millisecond)
	start := time.Now()
	if !closedWithin(ca, 3*time.Second) || !closedWithin(cb, 3*time.Second) {
		t.Fatal("an idle stream was not closed")
	}
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("splice did not return after the idle close")
	}
	if d := time.Since(start); d < 250*time.Millisecond {
		t.Fatalf("closed after %v, before the idle time", d)
	}
}

func TestSpliceOneWayTrafficKeepsItOpen(t *testing.T) {
	s, ca, cb, _ := spliced(t, 300*time.Millisecond)
	var got atomic.Int64
	go func() { // b only listens
		buf := make([]byte, 64)
		for {
			n, err := cb.Read(buf)
			got.Add(int64(n))
			if err != nil {
				return
			}
		}
	}()
	for i := 0; i < 12; i++ { // 1.2 s: four times the idle time, with a byte every 100 ms one way only
		if _, err := ca.Write([]byte{byte(i)}); err != nil {
			t.Fatalf("write %d on a stream that is carrying bytes: %v", i, err)
		}
		time.Sleep(100 * time.Millisecond)
	}
	if got.Load() != 12 {
		t.Fatalf("b received %d bytes, want 12", got.Load())
	}
	if n := atomic.LoadUint64(&s.relayed); n != 12 {
		t.Fatalf("relayed counts %d bytes, want 12", n)
	}
	// and once it goes quiet, it closes
	if !closedWithin(ca, 3*time.Second) {
		t.Fatal("the stream stayed open after the traffic stopped")
	}
}

func TestSpliceNoIdleLimit(t *testing.T) {
	_, ca, cb, done := spliced(t, 0)
	time.Sleep(700 * time.Millisecond)
	select {
	case <-done:
		t.Fatal("a stream with no idle limit was closed")
	default:
	}
	ca.Write([]byte("late"))
	buf := make([]byte, 4)
	cb.SetReadDeadline(time.Now().Add(2 * time.Second))
	if _, err := io.ReadFull(cb, buf); err != nil || string(buf) != "late" {
		t.Fatalf("read %q, %v", buf, err)
	}
	ca.Close() // an ordinary close still ends both legs
	if !closedWithin(cb, 3*time.Second) {
		t.Fatal("closing one leg did not close the other")
	}
}

func TestRelayIdleDefault(t *testing.T) {
	if s := NewServer(10, false); s.relayIdle != defaultRelayIdle || defaultRelayIdle <= 0 {
		t.Fatalf("default relayIdle %v", s.relayIdle)
	}
}
