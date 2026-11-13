package main

import (
	"bufio"
	"errors"
	"io"
	"net"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"
)

func TestStatusServerHasTimeouts(t *testing.T) {
	hs := newStatusServer("127.0.0.1:0", http.NotFoundHandler())
	if hs.ReadHeaderTimeout != statusReadHeaderTimeout || hs.ReadTimeout != statusReadTimeout ||
		hs.WriteTimeout != statusWriteTimeout || hs.IdleTimeout != statusIdleTimeout {
		t.Fatalf("timeouts: header %v read %v write %v idle %v", hs.ReadHeaderTimeout, hs.ReadTimeout, hs.WriteTimeout, hs.IdleTimeout)
	}
	for name, d := range map[string]time.Duration{"header": hs.ReadHeaderTimeout, "read": hs.ReadTimeout, "write": hs.WriteTimeout, "idle": hs.IdleTimeout} {
		if d <= 0 || d > time.Minute {
			t.Errorf("%s timeout %v: want between 0 and a minute", name, d)
		}
	}
	if hs.Addr != "127.0.0.1:0" {
		t.Errorf("Addr %q", hs.Addr)
	}
}

// startStatus serves the status handler through newStatusServer with short timeouts.
func startStatus(t *testing.T) string {
	t.Helper()
	s := NewServer(10, false)
	hs := newStatusServer("", s.HTTP())
	hs.ReadHeaderTimeout = 150 * time.Millisecond
	hs.IdleTimeout = 150 * time.Millisecond
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go hs.Serve(ln)
	t.Cleanup(func() { hs.Close() })
	return ln.Addr().String()
}

// statusClosedWithin reports whether the server closes c (EOF or a reset) before d passes; a read that only times out means it kept waiting.
func statusClosedWithin(c net.Conn, r *bufio.Reader, d time.Duration) bool {
	c.SetReadDeadline(time.Now().Add(d))
	_, err := r.ReadByte()
	var ne net.Error
	return err != nil && !(errors.As(err, &ne) && ne.Timeout())
}

func TestStatusClosesSlowAndIdleConnections(t *testing.T) {
	addr := startStatus(t)

	slow, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	defer slow.Close()
	slow.Write([]byte("GET /healthz HTTP/1.1\r\nHost: 127.0.0.1\r\nX-Slow: "))
	br := bufio.NewReader(slow)
	slow.SetReadDeadline(time.Now().Add(2 * time.Second))
	line, _ := br.ReadString('\n') // 408 is allowed; what matters is that the connection then ends
	if line != "" && !strings.Contains(line, "408") {
		t.Errorf("slow header: %q, want a 408 or a close", line)
	}
	if !statusClosedWithin(slow, br, 2*time.Second) {
		t.Error("a connection that never finished its header was not closed")
	}

	idle, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	defer idle.Close()
	idle.Write([]byte("GET /api/stats HTTP/1.1\r\nHost: 127.0.0.1\r\n\r\n"))
	ibr := bufio.NewReader(idle)
	idle.SetReadDeadline(time.Now().Add(2 * time.Second))
	resp, err := http.ReadResponse(ibr, nil)
	if err != nil || resp.StatusCode != 200 {
		t.Fatalf("a normal request: %v, %v", resp, err)
	}
	io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	if !statusClosedWithin(idle, ibr, 2*time.Second) {
		t.Error("an idle connection after an answer was not closed")
	}
}

// The -http server must be built by newStatusServer; a bare http.Server in main has no timeouts.
func TestMainBuildsStatusServerWithTimeouts(t *testing.T) {
	src, err := os.ReadFile("main.go")
	if err != nil {
		t.Fatal(err)
	}
	text := string(src)
	if !strings.Contains(text, "newStatusServer(*httpAddr, s.HTTP())") {
		t.Error("main.go does not build the status server with newStatusServer")
	}
	if strings.Contains(text, "&http.Server{") {
		t.Error("main.go builds an http.Server directly")
	}
}
