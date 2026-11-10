package main

import (
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestStatusAssetLineEndings(t *testing.T) {
	for _, source := range []string{"a\r\nb\r\n", "a\rb\r", "a\nb\n"} {
		if statusLF(source) != "a\nb\n" || statusHash(source) != statusHash("a\nb\n") {
			t.Fatalf("line endings affect rendered asset or CSP hash: %q", source)
		}
	}
	for _, source := range []string{statusHTML, statusCSS, statusJS} {
		if strings.Contains(source, "\r") {
			t.Fatal("embedded status asset was not normalized")
		}
	}
}

func TestStatusTerminalPage(t *testing.T) {
	st := statsJSON{Uptime: "1m", Rooms: []roomJSON{{ID: 9, Title: "<script>bad</script>", Members: []string{"<img src=x>"}, Max: 4}},
		Boards: []boardJSON{{Title: "<svg>", Entries: 1, Leader: "<script>leader</script>", Best: 100}}}
	var out strings.Builder
	if err := page.Execute(&out, st); err != nil {
		t.Fatal(err)
	}
	html := out.String()
	for _, want := range []string{"re:psn", "lang=\"en\"", "fallback-refresh", "Rooms", "Leaderboards", "&lt;script&gt;bad&lt;/script&gt;", "&lt;img src=x&gt;", "&lt;svg&gt;", "&lt;script&gt;leader&lt;/script&gt;"} {
		if !strings.Contains(html, want) {
			t.Errorf("missing %q", want)
		}
	}
	for _, asset := range []struct{ tag, source string }{{"style", statusCSS}, {"script", statusJS}} {
		start := strings.Index(html, "<"+asset.tag+">") + len(asset.tag) + 2
		end := strings.Index(html[start:], "</"+asset.tag+">") + start
		if html[start:end] != asset.source || !strings.Contains(statusPolicy, statusHash(html[start:end])) {
			t.Errorf("%s CSP hash does not match rendered asset", asset.tag)
		}
	}
}

func TestStatusThemeAuthenticationAndHeaders(t *testing.T) {
	s := NewServer(10, false)
	s.httpToken = "synthetic-status-token"
	for _, path := range []string{"/", "/api/stats"} {
		for _, auth := range []string{"", "Bearer wrong", "Bearer synthetic-status-token"} {
			req := httptest.NewRequest("GET", path, nil)
			req.Header.Set("Authorization", auth)
			out := httptest.NewRecorder()
			s.HTTP().ServeHTTP(out, req)
			want := http.StatusUnauthorized
			if auth == "Bearer synthetic-status-token" {
				want = http.StatusOK
			}
			if out.Code != want || out.Header().Get("Cache-Control") != "no-store" || out.Header().Get("Referrer-Policy") != "no-referrer" {
				t.Errorf("%s: status/headers %d %v", path, out.Code, out.Header())
			}
			if want == http.StatusOK && path == "/" && out.Header().Get("Content-Security-Policy") != statusPolicy {
				t.Fatal("missing asset-hash policy")
			}
			if want == http.StatusOK && path == "/api/stats" {
				var data map[string]any
				if json.Unmarshal(out.Body.Bytes(), &data) != nil || len(data) != 8 {
					t.Fatal("status JSON schema changed")
				}
			}
		}
	}
}

// TestStatusBrowserFixture is an explicit, bounded preview helper for the browser
// check. Its room and leaderboard come through real loopback protocol requests.
// Delete the ready file to stop; otherwise it exits after two minutes.
func TestStatusBrowserFixture(t *testing.T) {
	ready := os.Getenv("RE_PSN_STATUS_BROWSER_READY")
	if ready == "" {
		t.Skip("opt-in browser fixture")
	}
	s := NewServer(10, false)
	s.httpToken = "synthetic-status-token"
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { listener.Close() })
	go s.Serve(listener)
	alice := dial(t, listener.Addr().String(), "NPWR00001_00", "alice", 3658)
	roomOf(t, alice)
	w := &writer{}
	w.u32(1)
	w.u8(0)
	w.u64(100)
	w.blob(nil)
	if typ, _ := alice.call(msgRecordScore, w.b); typ != msgScoreRecorded {
		t.Fatalf("record score: %x", typ)
	}
	server := httptest.NewServer(s.HTTP())
	t.Cleanup(server.Close)
	if err := os.WriteFile(filepath.Clean(ready), []byte(server.URL), 0600); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(2 * time.Minute)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(ready); os.IsNotExist(err) {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
}
