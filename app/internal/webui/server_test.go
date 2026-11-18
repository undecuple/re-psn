package webui

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/undecuple/re-psn/app/framework"
	"github.com/undecuple/re-psn/app/internal/appinstance"
)

type fixture struct {
	server                    *Server
	origin, token, nonce, dir string
}

func serveUI(t *testing.T) fixture {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "private")
	s, err := New(Options{ConfigPath: filepath.Join(dir, "re-psn.ini"), ProfileDir: filepath.Join(dir, "profile"), ServerBinary: filepath.Join(dir, "missing-server"), Version: "synthetic"})
	if err != nil {
		t.Fatal(err)
	}
	ln, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ready := make(chan string, 1)
	done := make(chan error, 1)
	ctx, cancel := context.WithCancel(context.Background())
	go func() { done <- s.Serve(ctx, ln, func(link string) error { ready <- link; return nil }) }()
	t.Cleanup(func() {
		cancel()
		select {
		case err := <-done:
			if err != nil {
				t.Error(err)
			}
		case <-time.After(4 * time.Second):
			t.Error("UI shutdown did not join")
		}
	})
	var link string
	select {
	case link = <-ready:
	case <-time.After(2 * time.Second):
		t.Fatal("UI not ready")
	}
	u, _ := url.Parse(link)
	return fixture{s, "http://" + ln.Addr().String(), s.token, strings.TrimPrefix(u.Fragment, "token="), dir}
}
func callUI(t *testing.T, f fixture, token, path string, body any, want int) []byte {
	t.Helper()
	var input io.Reader
	method := "GET"
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		input = bytes.NewReader(b)
		method = "POST"
	}
	req, _ := http.NewRequest(method, f.origin+path, input)
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Origin", f.origin)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	c := &http.Client{Timeout: 8 * time.Second}
	res, err := c.Do(req)
	if err != nil {
		t.Fatal("UI request", path, err)
	}
	defer res.Body.Close()
	b, err := io.ReadAll(io.LimitReader(res.Body, maxInput+1))
	if err != nil {
		t.Fatal(err)
	}
	if res.StatusCode != want {
		t.Fatalf("%s status%d expected%d body%s", path, res.StatusCode, want, b)
	}
	return b
}
func saveRole(t *testing.T, f fixture, mode string, extra map[string]any) {
	t.Helper()
	q := map[string]any{"mode": mode, "theme": "dark", "name": "", "server": "", "status_url": "", "social_origin": "", "social_ca_file": "", "host_address": "127.0.0.1:36100", "host_relay": false}
	for k, v := range extra {
		q[k] = v
	}
	callUI(t, f, f.token, "/api/settings", q, 200)
}
func TestUIBootstrapOriginAndReopen(t *testing.T) {
	f := serveUI(t)
	callUI(t, f, "", "/api/state", nil, 401)
	callUI(t, f, f.nonce, "/api/state", nil, 401)
	callUI(t, f, f.nonce, "/api/open", map[string]any{"bad": true}, 400)
	b := callUI(t, f, f.nonce, "/api/open", struct{}{}, 200)
	var reply map[string]string
	if json.Unmarshal(b, &reply) != nil || reply["token"] != f.token || f.nonce == f.token {
		t.Fatal("bootstrap did not exchange separate capability")
	}
	callUI(t, f, f.nonce, "/api/open", struct{}{}, 401)
	for _, tc := range []struct{ host, origin, site string }{{"foreign.invalid", f.origin, "same-origin"}, {strings.TrimPrefix(f.origin, "http://"), "http://foreign.invalid", "same-origin"}, {strings.TrimPrefix(f.origin, "http://"), f.origin, "cross-site"}} {
		req, _ := http.NewRequest("GET", f.origin+"/api/state", nil)
		req.Host = tc.host
		req.Header.Set("Origin", tc.origin)
		req.Header.Set("Sec-Fetch-Site", tc.site)
		req.Header.Set("Authorization", "Bearer "+f.token)
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
		if res.StatusCode != 403 {
			t.Fatal("foreign origin reached UI state", res.StatusCode)
		}
	}
	// A new native launch gets a fresh nonce while the existing process/profile lives.
	owner, yes, err := appinstance.Acquire(filepath.Join(f.dir, "re-psn.ini"))
	if err != nil || !yes {
		t.Fatal("instance lock", err)
	}
	defer owner.Close()
	if err := owner.Publish(f.server.Locator()); err != nil {
		t.Fatal(err)
	}
	peer, yes, err := appinstance.Acquire(filepath.Join(f.dir, "re-psn.ini"))
	if err != nil || yes {
		t.Fatal("second instance became owner", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	link, err := peer.Reopen(ctx, filepath.Join(f.dir, "profile"), filepath.Join(f.dir, "missing-server"))
	if err != nil {
		t.Fatal("reopen", err)
	}
	u, _ := url.Parse(link)
	nonce := strings.TrimPrefix(u.Fragment, "token=")
	if nonce == f.nonce || nonce == f.token {
		t.Fatal("reopen reused capability")
	}
	callUI(t, f, nonce, "/api/open", struct{}{}, 200)
	callUI(t, f, nonce, "/api/open", struct{}{}, 401)
	if _, err = peer.Reopen(ctx, "different-profile", filepath.Join(f.dir, "missing-server")); err == nil {
		t.Fatal("reopen silently ignored explicit profile")
	}
}
func TestUIIntentAndLocalTrophyRoundTrip(t *testing.T) {
	f := serveUI(t)
	if _, err := os.Stat(filepath.Join(f.dir, "profile")); !os.IsNotExist(err) {
		t.Fatal("startup created profile")
	}
	saveRole(t, f, "host", nil)
	callUI(t, f, f.token, "/api/profile/create", map[string]string{"label": "unwanted"}, 403)
	if _, err := os.Stat(filepath.Join(f.dir, "profile")); !os.IsNotExist(err) {
		t.Fatal("Host intent created identity")
	}
	saveRole(t, f, "both", map[string]any{"server": "127.0.0.1:36300", "host_address": "127.0.0.1:36400"})
	callUI(t, f, f.token, "/api/profile/create", map[string]string{"label": "Synthetic UI player"}, 200)
	bad := `{"title_id":"A","title_id":"B","name":"Synthetic","revision":"1","trophies":[{"id":1,"name":"Event","grade":"bronze","description":""}]}`
	callUI(t, f, f.token, "/api/catalog", map[string]string{"catalog": bad}, 400)
	catalog := `{"title_id":"SYNTH-UI","name":"Synthetic UI","revision":"1","trophies":[{"id":1,"name":"Event","grade":"bronze","description":""}]}`
	callUI(t, f, f.token, "/api/catalog", map[string]string{"catalog": catalog}, 200)
	var state State
	raw := callUI(t, f, f.token, "/api/grant", map[string]string{"title": "SYNTH-UI"}, 200)
	if json.Unmarshal(raw, &state) != nil || !state.Grant.Active {
		t.Fatal("UI did not issue title grant")
	}
	if bytes.Contains(raw, []byte(`"seed"`)) || bytes.Contains(raw, []byte(f.token)) || bytes.Contains(raw, []byte(f.nonce)) {
		t.Fatal("secret in state")
	}
	data, err := os.ReadFile(state.Grant.Path)
	if err != nil {
		t.Fatal(err)
	}
	fields := strings.Split(string(data), "\n")
	port := strings.TrimPrefix(fields[1], "port=")
	secret, err := hex.DecodeString(strings.TrimPrefix(fields[2], "token="))
	if err != nil {
		t.Fatal(err)
	}
	conn, err := net.DialTimeout("tcp4", net.JoinHostPort("127.0.0.1", port), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	conn.SetDeadline(time.Now().Add(3 * time.Second))
	request := make([]byte, 48)
	copy(request, "RPT1")
	request[4] = 1
	request[11] = 36
	copy(request[12:44], secret)
	binary.BigEndian.PutUint32(request[44:], 1)
	if _, err = conn.Write(request); err != nil {
		t.Fatal(err)
	}
	conn.(*net.TCPConn).CloseWrite()
	reply := make([]byte, 21)
	_, err = io.ReadFull(conn, reply)
	conn.Close()
	if err != nil || reply[4] != 0 || reply[12] != 1 {
		t.Fatal("UI game capability report failed", err)
	}
	raw = callUI(t, f, f.token, "/api/state", nil, 200)
	json.Unmarshal(raw, &state)
	if state.Settings.Server == state.Settings.HostAddress || len(state.Profile.Titles[0].Unlocks) != 1 {
		t.Fatal("role endpoints or trophy state lost")
	}
	backup := callUI(t, f, f.token, "/api/profile/export", map[string]string{"passphrase": "synthetic UI backup phrase"}, 200)
	restored, err := framework.Restore(filepath.Join(t.TempDir(), "restored"), bytes.NewReader(backup), []byte("synthetic UI backup phrase"))
	if err != nil {
		t.Fatal(err)
	}
	defer restored.Close()
	if restored.View().AccountID != state.Profile.AccountID || len(restored.View().Titles[0].Unlocks) != 1 {
		t.Fatal("UI backup lost identity/trophies")
	}
	callUI(t, f, f.token, "/api/revoke", struct{}{}, 200)
	if _, err = os.Stat(state.Grant.Path); !os.IsNotExist(err) {
		t.Fatal("revoked descriptor remains")
	}
}
func TestUIPlayHasBackendDeadline(t *testing.T) {
	f := serveUI(t)
	ln, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	accepted := make(chan net.Conn, 1)
	go func() {
		c, err := ln.Accept()
		if err == nil {
			accepted <- c
		}
	}()
	saveRole(t, f, "play", map[string]any{"server": ln.Addr().String()})
	start := time.Now()
	raw := callUI(t, f, f.token, "/api/play", map[string]any{"title": "", "board": 0, "boards": false}, 200)
	var state State
	json.Unmarshal(raw, &state)
	if state.Play.Status != "unavailable" || time.Since(start) > 7*time.Second {
		t.Fatal("backend did not enforce check deadline")
	}
	select {
	case c := <-accepted:
		c.Close()
	default:
		t.Fatal("slow peer never accepted request")
	}
	// The global action gate must be released without a browser abort.
	callUI(t, f, f.token, "/api/settings", map[string]any{"mode": "play", "theme": "dark", "host_address": "127.0.0.1:36100"}, 200)
}

func TestPrivateFileLaunchOnlyAllowsInertDocument(t *testing.T) {
	f := serveUI(t)
	for _, tc := range []struct {
		path, dest string
		want       int
	}{{"/", "document", 200}, {"/api/state", "document", 403}, {"/app.js", "script", 403}, {"/", "iframe", 403}} {
		req, _ := http.NewRequest("GET", f.origin+tc.path, nil)
		req.Header.Set("Sec-Fetch-Site", "cross-site")
		req.Header.Set("Sec-Fetch-Dest", tc.dest)
		req.Header.Set("Authorization", "Bearer "+f.token)
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
		if res.StatusCode != tc.want {
			t.Fatal(tc.path, res.StatusCode, tc.want)
		}
	}
}

// The page explains its own terms in a glossary that is always in the document (no script needed to show it).
func TestPageHasPlainWordsGlossary(t *testing.T) {
	b, err := assets.ReadFile("assets/index.html")
	if err != nil {
		t.Fatal(err)
	}
	page := string(b)
	if !strings.Contains(page, `<details id="glossary" class="words">`) || strings.Contains(page, `id="glossary" class="words" hidden`) {
		t.Fatal("the glossary is missing or hidden")
	}
	for _, term := range []string{"Play target", "Game server", "Status URL", "Reachable", "Saved servers", "Owned host", "Both", "Relay (on a host)", "Friend relay"} {
		if !strings.Contains(page, "<dt>"+term) {
			t.Errorf("no definition for %q", term)
		}
	}
}
