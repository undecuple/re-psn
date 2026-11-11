package main

import (
	"bytes"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// logged returns a server whose log goes to the returned buffer.
func logged(t *testing.T) (*Server, string, *syncBuf) {
	s := NewServer(10, false)
	buf := &syncBuf{}
	s.log = newLogger(buf, levelFor(true), "text")
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	go s.Serve(ln)
	return s, ln.Addr().String(), buf
}

func TestLogHelloAndRefusals(t *testing.T) {
	s, addr, buf := logged(t)
	s.maxPlayerRooms = 1
	a := dial(t, addr, "NPWR00001_00", "alice", 3658)
	a.call(msgCreateRoom, createBody(nil))
	a.call(msgCreateRoom, createBody(nil))                         // over the room limit
	a.call(msgCreateRoom, createBody(make([]byte, maxRoomBlob+1))) // over the blob limit
	time.Sleep(50 * time.Millisecond)

	out := buf.String()
	for _, want := range []string{
		`msg=hello`, `online_id=alice`, `msg="request refused"`, `reason="room limit"`, `reason="blob over limit"`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("log lacks %q:\n%s", want, out)
		}
	}
}

func TestLogLevelAndFormat(t *testing.T) {
	var info, debug bytes.Buffer
	newLogger(&info, levelFor(false), "text").Debug("room created")
	newLogger(&debug, levelFor(true), "json").Debug("room created")
	if info.Len() != 0 {
		t.Errorf("debug line at info level: %q", info.String())
	}
	if !strings.Contains(debug.String(), `"msg":"room created"`) {
		t.Errorf("json debug line: %q", debug.String())
	}
	if _, _, err := parseLogFlags("loud", "text"); err == nil {
		t.Error("bad level accepted")
	}
	if _, _, err := parseLogFlags("warn", "xml"); err == nil {
		t.Error("bad format accepted")
	}
	if l, f, err := parseLogFlags("warn", "json"); err != nil || l.String() != "WARN" || f != "json" {
		t.Errorf("good flags: %v %q %v", l, f, err)
	}
}

// A refused status page request is logged by path; the token in the query is not.
func TestLogStatusRefusalKeepsTokenOut(t *testing.T) {
	s := NewServer(10, false)
	buf := &syncBuf{}
	s.log = newLogger(buf, levelFor(false), "text")
	s.httpToken = "sesame"
	req := httptest.NewRequest("GET", "/api/stats?token=wrong-guess", nil)
	w := httptest.NewRecorder()
	s.HTTP().ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("got %d", w.Code)
	}
	out := buf.String()
	if !strings.Contains(out, "status page refused") || !strings.Contains(out, "/api/stats") {
		t.Errorf("refusal not logged: %q", out)
	}
	if strings.Contains(out, "wrong-guess") || strings.Contains(out, "sesame") {
		t.Errorf("token in the log: %q", out)
	}
}

// syncBuf is a bytes.Buffer safe for the server goroutines and the test.
type syncBuf struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuf) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuf) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

// TestDiscoveryStartLogQuotesNameOnce checks that the LAN discovery start line
// carries the name as a plain attribute, so the log format quotes it exactly
// once and escapes control characters, in both formats.
func TestDiscoveryStartLogQuotesNameOnce(t *testing.T) {
	cases := map[string]string{
		"text": `name="e2e box"`,
		"json": `"name":"e2e box"`,
	}
	for format, want := range cases {
		t.Run(format, func(t *testing.T) {
			buf := &syncBuf{}
			logDiscoveryOn(newLogger(buf, slog.LevelInfo, format), "e2e box")
			out := buf.String()
			if !strings.Contains(out, "lan discovery on") || !strings.Contains(out, want) {
				t.Fatalf("log %q lacks message or %s", out, want)
			}
			if strings.Contains(out, `\"`) || strings.Count(out, "e2e box") != 1 {
				t.Fatalf("name quoted twice or repeated: %q", out)
			}
		})
	}
	buf := &syncBuf{}
	logDiscoveryOn(newLogger(buf, slog.LevelInfo, "text"), "a\nb")
	if strings.Contains(buf.String(), "a\nb") || !strings.Contains(buf.String(), `a\nb`) {
		t.Fatalf("control character not escaped: %q", buf.String())
	}
}
