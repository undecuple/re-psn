package main

import (
	"io"
	"log/slog"
	"net"
	"strings"
	"testing"
	"time"
)

func TestParseMediusAddr(t *testing.T) {
	ok := []struct {
		addr string
		port int
		want string
		wide bool
	}{
		{"127.0.0.1", 10071, "127.0.0.1:10071", false},
		{"::1", 1, "[::1]:1", false},
		{"192.168.1.5", 65535, "192.168.1.5:65535", false},
		{"10.0.0.2", 10071, "10.0.0.2:10071", false},
		{"169.254.1.1", 10071, "169.254.1.1:10071", false},
		{"0.0.0.0", 10071, "0.0.0.0:10071", true},
		{"::", 10071, "[::]:10071", true},
		{"::ffff:0.0.0.0", 10071, "0.0.0.0:10071", true},
		{"::ffff:192.168.1.5", 10071, "192.168.1.5:10071", false},
	}
	for _, c := range ok {
		got, wide, err := parseMediusAddr(c.addr, c.port)
		if err != nil || got != c.want || wide != c.wide {
			t.Errorf("parseMediusAddr(%q, %d) = %q, %v, %v; want %q, wide %v", c.addr, c.port, got, wide, err, c.want, c.wide)
		}
	}
	bad := []struct {
		addr string
		port int
		msg  string
	}{
		{"8.8.8.8", 10071, "private self-hosted"},
		{"2001:4860:4860::8888", 10071, "private self-hosted"},
		{"::ffff:8.8.8.8", 10071, "private self-hosted"},
		{"localhost", 10071, "not an IP address"},
		{"", 10071, "not an IP address"},
		{"127.0.0.1", 0, "not a port number"},
		{"127.0.0.1", 65536, "not a port number"},
		{"127.0.0.1", -1, "not a port number"},
	}
	for _, c := range bad {
		if got, _, err := parseMediusAddr(c.addr, c.port); err == nil || !strings.Contains(err.Error(), c.msg) {
			t.Errorf("parseMediusAddr(%q, %d) = %q, %v; want an error containing %q", c.addr, c.port, got, err, c.msg)
		}
	}
}

// The listener closes every connection without reading or writing: the client
// sees EOF with no data, even after sending bytes.
func TestMediusListenerClosesEveryConnection(t *testing.T) {
	m, err := newMediusListener("127.0.0.1:0", slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- m.Serve() }()
	for i := 0; i < 3; i++ {
		c, err := net.Dial("tcp", m.Addr())
		if err != nil {
			t.Fatal(err)
		}
		c.Write([]byte("PSNR"))
		c.SetReadDeadline(time.Now().Add(2 * time.Second))
		buf := make([]byte, 16)
		if n, err := c.Read(buf); n != 0 || err == nil {
			t.Fatalf("read %d bytes, err %v; want nothing sent by the listener", n, err)
		}
		c.Close()
	}
	deadline := time.Now().Add(2 * time.Second)
	for m.Closed() < 3 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if got := m.Closed(); got != 3 {
		t.Fatalf("closed %d connections, want 3", got)
	}
	m.Shutdown()
	if err := <-done; err != nil {
		t.Fatalf("Serve after Shutdown returned %v, want nil", err)
	}
	if _, err := net.DialTimeout("tcp", m.Addr(), 200*time.Millisecond); err == nil {
		t.Fatal("the port still accepts after Shutdown")
	}
}

func TestMediusListenerPortInUse(t *testing.T) {
	m, err := newMediusListener("127.0.0.1:0", slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	defer m.Shutdown()
	if _, err := newMediusListener(m.Addr(), slog.New(slog.NewTextHandler(io.Discard, nil))); err == nil {
		t.Fatal("a second listener on the same port succeeded")
	}
}
