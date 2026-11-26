package main

import (
	"errors"
	"io"
	"log/slog"
	"net"
	"strings"
	"sync"
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
		{"100.64.0.1", 10071, "private self-hosted"},
		{"100.127.255.254", 10071, "private self-hosted"},
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

func TestNextAcceptBackoff(t *testing.T) {
	want := []time.Duration{5 * time.Millisecond, 10 * time.Millisecond, 20 * time.Millisecond}
	var d time.Duration
	for i, w := range want {
		if d = nextAcceptBackoff(d); d != w {
			t.Fatalf("step %d = %v, want %v", i, d, w)
		}
	}
	for i := 0; i < 20; i++ {
		d = nextAcceptBackoff(d)
	}
	if d != acceptBackoffMax {
		t.Fatalf("after many failures the pause is %v, want %v", d, acceptBackoffMax)
	}
	if got := nextAcceptBackoff(acceptBackoffMax); got != acceptBackoffMax {
		t.Fatalf("at the maximum the pause is %v, want %v", got, acceptBackoffMax)
	}
}

// scriptedListener fails Accept a set number of times, then blocks until closed.
type scriptedListener struct {
	mu     sync.Mutex
	fails  int
	closed chan struct{}
	calls  int
}

func (l *scriptedListener) Accept() (net.Conn, error) {
	l.mu.Lock()
	l.calls++
	if l.fails > 0 {
		l.fails--
		l.mu.Unlock()
		return nil, errors.New("too many open files from 203.0.113.9")
	}
	l.mu.Unlock()
	<-l.closed
	return nil, net.ErrClosed
}
func (l *scriptedListener) Close() error   { close(l.closed); return nil }
func (l *scriptedListener) Addr() net.Addr { return &net.TCPAddr{} }

// A failed Accept is retried with growing pauses and does not stop Serve; the
// error text, which may carry a peer address, is not logged.
func TestMediusServeRetriesAcceptErrors(t *testing.T) {
	var logs strings.Builder
	log := slog.New(slog.NewTextHandler(&logs, nil))
	l := &scriptedListener{fails: 4, closed: make(chan struct{})}
	m := newMediusListenerOn(l, log)
	var pauses []time.Duration
	m.wait = func(d time.Duration) bool { pauses = append(pauses, d); return true }
	done := make(chan error, 1)
	go func() { done <- m.Serve() }()
	deadline := time.After(5 * time.Second)
	for {
		l.mu.Lock()
		n := l.calls
		l.mu.Unlock()
		if n >= 5 {
			break
		}
		select {
		case <-deadline:
			t.Fatal("Serve did not retry after Accept errors")
		case err := <-done:
			t.Fatalf("Serve returned %v after an Accept error", err)
		case <-time.After(time.Millisecond):
		}
	}
	m.Shutdown()
	if err := <-done; err != nil {
		t.Fatalf("Serve after Shutdown returned %v, want nil", err)
	}
	want := []time.Duration{5 * time.Millisecond, 10 * time.Millisecond, 20 * time.Millisecond, 40 * time.Millisecond}
	if len(pauses) != len(want) {
		t.Fatalf("pauses = %v, want %v", pauses, want)
	}
	for i := range want {
		if pauses[i] != want[i] {
			t.Fatalf("pauses = %v, want %v", pauses, want)
		}
	}
	if strings.Contains(logs.String(), "203.0.113.9") || strings.Contains(logs.String(), "open files") {
		t.Fatalf("the Accept error text reached the log: %s", logs.String())
	}
}

// Shutdown ends a pause in progress, so Serve returns at once.
func TestMediusShutdownEndsBackoff(t *testing.T) {
	l := &scriptedListener{fails: 1 << 20, closed: make(chan struct{})}
	m := newMediusListenerOn(l, slog.New(slog.NewTextHandler(io.Discard, nil)))
	done := make(chan error, 1)
	go func() { done <- m.Serve() }()
	time.Sleep(20 * time.Millisecond)
	m.Shutdown()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Serve returned %v, want nil", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Serve did not return after Shutdown")
	}
}

func debugLog(b *strings.Builder) *slog.Logger {
	return slog.New(slog.NewTextHandler(b, &slog.HandlerOptions{Level: slog.LevelDebug}))
}

// freeLoopbackPort finds a port on 127.0.0.1 that nothing holds right now.
func freeLoopbackPort(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	return ln.Addr().(*net.TCPAddr).Port
}

// With the switch off, the defaults mean no listener and no log line, and a
// port or address other than the defaults is refused.
func TestSetupMediusSwitchOff(t *testing.T) {
	var logs strings.Builder
	m, err := setupMedius(false, defaultMediusAddr, defaultMediusPort, debugLog(&logs))
	if m != nil || err != nil || logs.Len() != 0 {
		t.Fatalf("defaults off = %v, %v, log %q; want nil, nil, empty", m, err, logs.String())
	}
	for _, c := range []struct {
		addr string
		port int
	}{{defaultMediusAddr, 10072}, {"192.168.1.5", defaultMediusPort}, {"0.0.0.0", 10072}} {
		m, err := setupMedius(false, c.addr, c.port, debugLog(&logs))
		if m != nil || err == nil || !strings.Contains(err.Error(), "does nothing without -medius") {
			t.Errorf("setupMedius(off, %q, %d) = %v, %v; want the refusal", c.addr, c.port, m, err)
		}
	}
	if logs.Len() != 0 {
		t.Fatalf("a refusal logged %q", logs.String())
	}
}

// With the switch on, a bad address is refused before anything is opened, and
// a good loopback one starts with the one warning and no every-interface line.
func TestSetupMediusSwitchOn(t *testing.T) {
	var logs strings.Builder
	if m, err := setupMedius(true, "8.8.8.8", 10071, debugLog(&logs)); m != nil || err == nil {
		t.Fatalf("a public address gave %v, %v; want a refusal", m, err)
	}
	if logs.Len() != 0 {
		t.Fatalf("a refusal logged %q", logs.String())
	}
	m, err := setupMedius(true, "127.0.0.1", freeLoopbackPort(t), debugLog(&logs))
	if err != nil {
		t.Fatal(err)
	}
	defer m.Shutdown()
	out := logs.String()
	if n := strings.Count(out, "level=WARN"); n != 1 || !strings.Contains(out, "no Medius protocol is implemented") || strings.Contains(out, "every interface") {
		t.Fatalf("start-up log = %q; want exactly the one no-protocol warning", out)
	}
}

func TestMediusWarnings(t *testing.T) {
	for _, wide := range []bool{false, true} {
		var logs strings.Builder
		mediusWarnings(debugLog(&logs), "[::]:10071", wide)
		out := logs.String()
		if got := strings.Count(out, "level=WARN"); got != map[bool]int{false: 1, true: 2}[wide] {
			t.Errorf("wide=%v: %d warnings in %q", wide, got, out)
		}
		if strings.Contains(out, "every interface") != wide {
			t.Errorf("wide=%v: every-interface line wrong in %q", wide, out)
		}
	}
}

// Whatever a client sends or connects from, none of it reaches the log: not
// its address and port, not its bytes.
func TestMediusLogsNothingAboutClients(t *testing.T) {
	var logs strings.Builder
	m, err := setupMedius(true, "127.0.0.1", freeLoopbackPort(t), debugLog(&logs))
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- m.Serve() }()
	c, err := net.Dial("tcp", m.Addr())
	if err != nil {
		t.Fatal(err)
	}
	local := c.LocalAddr().String()
	c.Write([]byte("TICKET-CANARY-0123456789"))
	io.ReadAll(c)
	c.Close()
	deadline := time.Now().Add(5 * time.Second)
	for m.Closed() < 1 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	m.Shutdown()
	<-done
	out := logs.String()
	_, port, _ := net.SplitHostPort(local)
	for _, bad := range []string{local, "port=" + port, "TICKET-CANARY", "remote", "peer"} {
		if strings.Contains(out, bad) {
			t.Errorf("the log mentions %q: %s", bad, out)
		}
	}
	if !strings.Contains(out, "medius connection closed") {
		t.Errorf("the debug line for a closed connection is missing: %s", out)
	}
}
