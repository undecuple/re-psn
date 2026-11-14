package tui

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/undecuple/re-psn/app/internal/config"
)

const validHTTPStats = `{"udp_drops":{"rate_limit":42},"uptime":"42s","clients":3,"connections_total":18446744073709551615,"unlisted_rooms":1,"rooms":[{"id":18446744073709551615,"title":"NPWR00001_00","members":["alice","bob"],"max":2,"age":"12s"}],"boards":[{"title":"NPWR00001_00","board":4294967295,"entries":2,"leader":"alice","best":-9223372036854775808}]}`

func TestHTTPStatusEndpointAndToken(t *testing.T) {
	for _, suffix := range []string{"", "/", "/status", "/status/", "/status/api/stats"} {
		t.Run(suffix, func(t *testing.T) {
			tokenFile := filepath.Join(t.TempDir(), "token")
			if err := os.WriteFile(tokenFile, []byte("synthetic-test-token\n"), 0600); err != nil {
				t.Fatal(err)
			}
			requests := 0
			s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests++
				want := "/api/stats"
				if strings.HasPrefix(suffix, "/status") {
					want = "/status/api/stats"
				}
				if r.Method != "GET" || r.URL.Path != want || r.URL.RawQuery != "" || r.Header.Get("Authorization") != "Bearer synthetic-test-token" {
					t.Errorf("unexpected request method/path/header: %s %s", r.Method, r.URL.Path)
				}
				fmt.Fprint(w, validHTTPStats)
			}))
			defer s.Close()
			got, err := fetchHTTPStatus(context.Background(), s.URL+suffix, tokenFile)
			if err != nil || requests != 1 {
				t.Fatalf("fetch %v / %d requests", err, requests)
			}
			if got.Connections != math.MaxUint64 || got.Rooms[0].ID != math.MaxUint64 || got.Boards[0].Board != math.MaxUint32 || got.Boards[0].Best != math.MinInt64 {
				t.Fatalf("integer precision lost: %+v", got)
			}
		})
	}
}

func TestHTTPStatusRejectsUnsafeURLsAndFiles(t *testing.T) {
	var calls atomic.Int32
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls.Add(1); fmt.Fprint(w, validHTTPStats) }))
	defer s.Close()
	for _, address := range []string{s.URL + "?token=synthetic-private", s.URL + "?", s.URL + "#fragment", strings.Replace(s.URL, "http://", "http://synthetic:private@", 1), "ftp://example.invalid/status", ":bad", "https:///missing-host"} {
		if _, err := fetchHTTPStatus(context.Background(), address, ""); err != errHTTPStatus {
			t.Fatalf("URL rejection leaked details: %v", err)
		}
	}
	dir := t.TempDir()
	for name, contents := range map[string]string{"empty": " \n", "oversize": strings.Repeat("x", maxStatusToken+1), "controls": "synthetic\r\nprivate"} {
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, []byte(contents), 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := fetchHTTPStatus(context.Background(), s.URL, path); err != errHTTPStatus {
			t.Fatalf("token file error leaked details: %v", err)
		}
	}
	for _, path := range []string{dir, filepath.Join(dir, "synthetic-private-missing")} {
		if _, err := fetchHTTPStatus(context.Background(), s.URL, path); err != errHTTPStatus {
			t.Fatalf("path error leaked details: %v", err)
		}
	}
	if calls.Load() != 0 {
		t.Fatal("unsafe input issued an HTTP request")
	}
}

func TestHTTPStatusDoesNotFollowRedirectsOrEchoErrors(t *testing.T) {
	var forwarded atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { forwarded.Add(1); fmt.Fprint(w, validHTTPStats) }))
	defer target.Close()
	for _, status := range []int{http.StatusFound, http.StatusUnauthorized, http.StatusInternalServerError} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Location", target.URL+"/synthetic-private")
				w.WriteHeader(status)
				fmt.Fprint(w, "synthetic-private-response")
			}))
			defer s.Close()
			if _, err := fetchHTTPStatus(context.Background(), s.URL+"/synthetic-private", ""); err != errHTTPStatus {
				t.Fatalf("server response leaked: %v", err)
			}
		})
	}
	if forwarded.Load() != 0 {
		t.Fatal("redirect was followed")
	}
}

func TestHTTPStatusBoundsAndDeadline(t *testing.T) {
	for name, body := range map[string]string{
		"malformed": "synthetic-private", "missing": `{}`, "nullrooms": strings.Replace(validHTTPStats, `"rooms":[{"id":18446744073709551615,"title":"NPWR00001_00","members":["alice","bob"],"max":2,"age":"12s"}]`, `"rooms":null`, 1),
		"negative": strings.Replace(validHTTPStats, `"clients":3`, `"clients":-1`, 1),
		"overflow": strings.Replace(validHTTPStats, "-9223372036854775808", "9223372036854775808", 1),
		"fraction": strings.Replace(validHTTPStats, `"clients":3`, `"clients":3.5`, 1),
		"trailing": validHTTPStats + `{}`, "oversize": strings.Repeat(" ", maxStatusBody+1),
		"text": strings.Replace(validHTTPStats, "42s", strings.Repeat("x", 65), 1),
	} {
		t.Run(name, func(t *testing.T) {
			s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, body) }))
			defer s.Close()
			if _, err := fetchHTTPStatus(context.Background(), s.URL, ""); err != errHTTPStatus {
				t.Fatalf("unbounded/invalid body accepted: %v", err)
			}
		})
	}
	var w map[string]json.RawMessage
	if err := json.Unmarshal([]byte(validHTTPStats), &w); err != nil {
		t.Fatal(err)
	}
	rooms := make([]HTTPRoom, maxStatusRows+1)
	for i := range rooms {
		rooms[i] = HTTPRoom{Max: 1}
	}
	w["rooms"], _ = json.Marshal(rooms)
	b, _ := json.Marshal(w)
	if _, err := decodeHTTPStatus(b); err != errHTTPStatus {
		t.Fatal("unbounded room count accepted")
	}
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { <-r.Context().Done() }))
	defer s.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	before := time.Now()
	if _, err := fetchHTTPStatus(ctx, s.URL, ""); err != errHTTPStatus || time.Since(before) > time.Second {
		t.Fatalf("deadline ignored: %v", err)
	}
}

func TestHTTPStatusBoundsResponseHeaders(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Synthetic", strings.Repeat("x", 40<<10))
		fmt.Fprint(w, validHTTPStats)
	}))
	defer s.Close()
	if _, err := fetchHTTPStatus(context.Background(), s.URL, ""); err != errHTTPStatus {
		t.Fatal("oversized response headers accepted")
	}
}

func TestNativeHTTPStatusOptInAndFallback(t *testing.T) {
	var calls atomic.Int32
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls.Add(1); fmt.Fprint(w, validHTTPStats) }))
	defer s.Close()
	// The successful HTTP path needs no game server and must not dial one.
	snap, err := (NativeStatus{}).Fetch(context.Background(), Query{StatusURL: s.URL, Server: "invalid game endpoint"})
	if err != nil || snap.HTTP == nil || snap.HTTPUnavailable {
		t.Fatalf("HTTP success fell back: %+v %v", snap, err)
	}
	// With no HTTP URL, retain the original protocol error and make no request.
	_, err = (NativeStatus{}).Fetch(context.Background(), Query{Server: "invalid game endpoint"})
	if err == nil || calls.Load() != 1 {
		t.Fatal("HTTP was used without opt-in")
	}
	// Configured HTTP errors stay generic even when the game endpoint also fails.
	snap, err = (NativeStatus{}).Fetch(context.Background(), Query{StatusURL: s.URL + "?token=synthetic-private", Server: "invalid game endpoint"})
	if err == nil || !snap.HTTPUnavailable || strings.Contains(err.Error(), "synthetic-private") {
		t.Fatal("fallback failed or leaked HTTP configuration")
	}
}

func TestHTTPStatusNativeViewsAndQuery(t *testing.T) {
	h, err := decodeHTTPStatus([]byte(validHTTPStats))
	if err != nil {
		t.Fatal(err)
	}
	h.Rooms[0].Members[0] = "alice\x1b[2J\n"
	h.Rooms[0].Title = "NPWR00001_00\r"
	h.Boards[0].Leader = "alice\x1b[2J\n"
	f := &fake{snap: Snapshot{HTTP: h}}
	set := config.Settings{Mode: config.ModePlay, StatusURL: "https://status.example.invalid/base", StatusTokenFile: "synthetic-token-file"}
	m := homeModel(t, f, set)
	cmd := m.Init()
	if cmd == nil {
		t.Fatal("HTTP-only settings did not refresh")
	}
	m = drive(t, m, cmd())
	if got := f.last(); got.StatusURL != set.StatusURL || got.StatusTokenFile != set.StatusTokenFile {
		t.Fatal("HTTP settings not passed to fetcher")
	}
	for _, light := range []bool{false, true} {
		m.w, m.h = 160, 40
		m.light = light
		m.st = newStyles(light, true)
		m.menu = paneOverview
		mustShow(t, m, "42s", "3", "18446744073709551615", "HTTP status reachable")
		m.menu = paneRooms
		mustShow(t, m, "18446744073709551615", "alice[2J", "12s", "invite-only")
		m.menu = paneBoards
		mustShow(t, m, "4294967295", "-9223372036854775808", "board summaries")
		for _, width := range []int{100, 45} {
			m.w = width
			m.h = 35
			m.content = true
			out := m.render()
			if strings.Contains(out, "alice\x1b[2J") || strings.Contains(out, "\r") {
				t.Fatal("remote controls reached native view")
			}
		}
	}
	m.title = "another"
	m.menu = paneRooms
	mustShow(t, m, "No public rooms match")
	m.title = ""
	m.board = "1"
	m.menu = paneBoards
	mustShow(t, m, "No leaderboards match")
	m.snap = &Snapshot{HTTPUnavailable: true}
	m.fetchErr = fmt.Errorf("game unavailable")
	m.menu = paneOverview
	mustShow(t, m, "HTTP status unavailable", "game unavailable")
}

// A failed configured HTTP listener must leave the native protocol path usable.
func TestHTTPStatusFallbackUsesProtocol(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	done := make(chan error, 1)
	go func() {
		c, err := ln.Accept()
		if err != nil {
			done <- err
			return
		}
		defer c.Close()
		c.SetDeadline(time.Now().Add(2 * time.Second))
		var hdr [3]byte
		if _, err = io.ReadFull(c, hdr[:]); err != nil {
			done <- err
			return
		}
		payload := make([]byte, binary.BigEndian.Uint16(hdr[1:]))
		if _, err = io.ReadFull(c, payload); err != nil {
			done <- err
			return
		}
		if hdr[0] != 1 || len(payload) < 4 {
			done <- fmt.Errorf("not a HELLO")
			return
		}
		ack := append([]byte(nil), payload[:4]...)
		ack = binary.BigEndian.AppendUint32(ack, 7)
		ack = append(ack, 127, 0, 0, 1)
		ack = binary.BigEndian.AppendUint32(ack, 42)
		ack = append(ack, 3)
		frame := []byte{0x81, 0, byte(len(ack))}
		if _, err = c.Write(append(frame, ack...)); err != nil {
			done <- err
			return
		}
		_, err = io.Copy(io.Discard, c)
		done <- err
	}()
	failedHTTP := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(401); fmt.Fprint(w, "synthetic-private") }))
	defer failedHTTP.Close()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	snap, err := (NativeStatus{}).Fetch(ctx, Query{StatusURL: failedHTTP.URL, Server: ln.Addr().String()})
	if err != nil || !snap.HTTPUnavailable || snap.HTTP != nil || snap.Ack.UserID != 7 || !snap.Ack.Relay() {
		t.Fatalf("protocol fallback failed: %+v %v", snap, err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestHTTPStatusSourceChangeAndSafeValidation(t *testing.T) {
	h, _ := decodeHTTPStatus([]byte(validHTTPStats))
	f := &fake{snap: Snapshot{HTTP: h}}
	m := homeModel(t, f, config.Settings{StatusURL: "https://example.invalid/old"})
	pending := m.refresh()
	m.editing, m.edit = editStatusURL, field{value: "https://example.invalid/new"}
	next, _ := m.editKey(press("enter"))
	m = next.(Model)
	next, _ = m.Update(pending())
	m = next.(Model)
	if m.snap != nil || m.loading {
		t.Fatal("old HTTP result survived source edit")
	}
	m.editing, m.edit = editStatusURL, field{value: "https://example.invalid/?token=synthetic-private"}
	next, _ = m.editKey(press("enter"))
	m = next.(Model)
	if m.problem == "" || strings.Contains(m.problem, "synthetic-private") || m.set.StatusURL != "https://example.invalid/new" {
		t.Fatal("URL validation leaked input or accepted credentials")
	}
}
