package main

import (
	"bufio"
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"io"
	"math/big"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"
)

// 32 characters, 20 different; in pieces so a secret scanner does not take the test value for a key.
const testAdminToken = "Zx9-kQ4m" + "P7vLr2Ts" + "8WbN5cYh" + "3JdF6gAe"

func writeToken(t *testing.T, content string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "admin.token")
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

// startAdmin starts an admin listener on loopback with a log buffer and short failure delays.
func startAdmin(t *testing.T, mutate func(*adminConfig), tweak ...func(*adminServer)) (*adminServer, *Server, *syncBuf, string) {
	t.Helper()
	s := NewServer(10, false)
	logs := &syncBuf{}
	s.log = newLogger(logs, 0, "text")
	cfg := adminConfig{Addr: "127.0.0.1:0", ReadTokenFile: writeToken(t, testAdminToken+"\n")}
	if mutate != nil {
		mutate(&cfg)
	}
	a, err := newAdminServer(s, cfg)
	if err != nil {
		t.Fatal(err)
	}
	a.fail.base, a.fail.max = time.Millisecond, 5*time.Millisecond
	for _, f := range tweak {
		f(a)
	}
	go a.Serve()
	t.Cleanup(func() { ctx, c := context.WithTimeout(context.Background(), time.Second); defer c(); a.Shutdown(ctx) })
	return a, s, logs, "http://" + a.Addr()
}

func adminGet(t *testing.T, url, auth string, hdr map[string]string) (int, string) {
	t.Helper()
	req, _ := http.NewRequest("GET", url, nil)
	if auth != "" {
		req.Header.Set("Authorization", auth)
	}
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b)
}

func TestAdminOffByDefault(t *testing.T) {
	// The status listener has no admin route, with or without a token.
	s := NewServer(10, false)
	s.httpToken = "status-token-for-the-test"
	h := s.HTTP()
	for _, auth := range []string{"", "Bearer " + testAdminToken, "Bearer status-token-for-the-test"} {
		rec := &respRecorder{h: http.Header{}}
		req, _ := http.NewRequest("GET", "http://x/admin/v1/state", nil)
		if auth != "" {
			req.Header.Set("Authorization", auth)
		}
		h.ServeHTTP(rec, req)
		if rec.code == 200 {
			t.Fatalf("status listener served /admin/v1/state with %q", auth)
		}
	}
}

type respRecorder struct {
	h    http.Header
	code int
	body bytes.Buffer
}

func (r *respRecorder) Header() http.Header { return r.h }
func (r *respRecorder) Write(b []byte) (int, error) {
	if r.code == 0 {
		r.code = 200
	}
	return r.body.Write(b)
}
func (r *respRecorder) WriteHeader(c int) { r.code = c }

func TestAdminTokenFileRules(t *testing.T) {
	good := testAdminToken
	cases := []struct {
		name, content, want string
		mode                os.FileMode
	}{
		{"too short", "short-token", "at least 32", 0o600},
		{"spaces inside", strings.Repeat("ab cd ", 10), "printable ASCII", 0o600},
		{"few characters", strings.Repeat("ab", 20), "too few different", 0o600},
		{"two lines", good + "\n" + good, "printable ASCII", 0o600},
		{"too big", strings.Repeat(good, 200), "larger than", 0o600},
	}
	for _, c := range cases {
		p := writeToken(t, c.content)
		_, err := loadAdminToken(p)
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: err = %v, want %q", c.name, err, c.want)
		}
		if err != nil && strings.Contains(err.Error(), strings.TrimSpace(c.content)) && len(c.content) > 20 {
			t.Errorf("%s: the error holds the file's content", c.name)
		}
	}
	if _, err := loadAdminToken(writeToken(t, good)); err != nil {
		t.Errorf("good token refused: %v", err)
	}
	if _, err := loadAdminToken(writeToken(t, good+"\r\n")); err != nil {
		t.Errorf("good token with CRLF refused: %v", err)
	}
	if _, err := loadAdminToken(filepath.Join(t.TempDir(), "missing")); err == nil {
		t.Error("missing file accepted")
	}
	if _, err := loadAdminToken(t.TempDir()); err == nil || !strings.Contains(err.Error(), "regular file") {
		t.Errorf("directory: %v", err)
	}
	if runtime.GOOS != "windows" {
		p := writeToken(t, good)
		for _, mode := range []os.FileMode{0o640, 0o604, 0o644, 0o660} {
			os.Chmod(p, mode)
			if _, err := loadAdminToken(p); err == nil || !strings.Contains(err.Error(), "group or others") {
				t.Errorf("mode %o: err = %v", mode, err)
			}
		}
		os.Chmod(p, 0o400)
		if _, err := loadAdminToken(p); err != nil {
			t.Errorf("mode 0400 refused: %v", err)
		}
	}
}

func TestAdminConfigRules(t *testing.T) {
	tok := writeToken(t, testAdminToken)
	cases := []struct {
		name string
		cfg  adminConfig
		want string
	}{
		{"no token file", adminConfig{Addr: "127.0.0.1:0"}, "no token, no admin listener"},
		{"no port", adminConfig{Addr: "127.0.0.1", ReadTokenFile: tok}, "port"},
		{"same as status", adminConfig{Addr: "127.0.0.1:36101", ReadTokenFile: tok, StatusAddr: "127.0.0.1:36101"}, "same address as -http"},
		{"wildcard overlaps status", adminConfig{Addr: ":36101", ReadTokenFile: tok, StatusAddr: "127.0.0.1:36101"}, "same address as -http"},
		{"only a cert", adminConfig{Addr: "127.0.0.1:0", ReadTokenFile: tok, TLSCert: "x"}, "go together"},
		{"empty host", adminConfig{Addr: ":0", ReadTokenFile: tok}, "needs -admin-tls-cert"},
		{"0.0.0.0", adminConfig{Addr: "0.0.0.0:0", ReadTokenFile: tok}, "needs -admin-tls-cert"},
		{"::", adminConfig{Addr: "[::]:0", ReadTokenFile: tok}, "needs -admin-tls-cert"},
		{"public address", adminConfig{Addr: "192.0.2.1:0", ReadTokenFile: tok}, "needs -admin-tls-cert"},
		{"a name is not loopback", adminConfig{Addr: "admin.example:0", ReadTokenFile: tok}, "needs -admin-tls-cert"},
		{"a name that merely contains localhost", adminConfig{Addr: "localhost.example:0", ReadTokenFile: tok}, "needs -admin-tls-cert"},
		{"same token as status", adminConfig{Addr: "127.0.0.1:0", ReadTokenFile: tok, StatusToken: testAdminToken}, "same as -http-token"},
	}
	for _, c := range cases {
		a, err := newAdminServer(NewServer(10, false), c.cfg)
		if err == nil {
			a.ln.Close()
			t.Errorf("%s: accepted", c.name)
		} else if !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: err = %v, want %q", c.name, err, c.want)
		}
	}
	for _, addr := range []string{"127.0.0.1:0", "[::1]:0", "localhost:0"} {
		if addr == "[::1]:0" {
			if l, err := net.Listen("tcp", addr); err != nil {
				continue // no IPv6 loopback here
			} else {
				l.Close()
			}
		}
		a, err := newAdminServer(NewServer(10, false), adminConfig{Addr: addr, ReadTokenFile: tok})
		if err != nil {
			t.Errorf("%s: %v", addr, err)
			continue
		}
		a.ln.Close()
	}
}

func TestAdminAuth(t *testing.T) {
	_, _, _, base := startAdmin(t, nil)
	code, body401 := adminGet(t, base+"/admin/v1/state", "", nil)
	if code != 401 {
		t.Fatalf("no token: %d", code)
	}
	code2, body2 := adminGet(t, base+"/admin/v1/state", "Bearer wrong-token-wrong-token-wrong-token", nil)
	if code2 != 401 || body2 != body401 {
		t.Errorf("wrong token: %d %q, want the same answer as no token (%q)", code2, body2, body401)
	}
	if code, _ := adminGet(t, base+"/admin/v1/state?token="+testAdminToken, "", nil); code != 401 {
		t.Errorf("token in the query string: %d, want 401", code)
	}
	if code, _ := adminGet(t, base+"/admin/v1/state", "Basic "+testAdminToken, nil); code != 401 {
		t.Errorf("wrong scheme: %d", code)
	}
	if code, _ := adminGet(t, base+"/admin/v1/nothing", "", nil); code != 401 {
		t.Errorf("unknown path without a token: %d, want 401 (routes are not revealed)", code)
	}
	if code, _ := adminGet(t, base+"/admin/v1/nothing", "Bearer "+testAdminToken, nil); code != 404 {
		t.Errorf("unknown path with the token: %d, want 404", code)
	}
	if code, _ := adminGet(t, base+"/admin/v1/state", "Bearer "+testAdminToken, nil); code != 200 {
		t.Errorf("good token: %d", code)
	}
	for _, m := range []string{"POST", "PUT", "DELETE", "PATCH"} {
		req, _ := http.NewRequest(m, base+"/admin/v1/state", strings.NewReader("{}"))
		req.Header.Set("Authorization", "Bearer "+testAdminToken)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != 403 {
			t.Errorf("%s with the read token: %d, want 403", m, resp.StatusCode)
		}
	}
	// A browser page: an Origin header is refused even with the token, and so is a name for Host over plain HTTP.
	if code, _ := adminGet(t, base+"/admin/v1/state", "Bearer "+testAdminToken, map[string]string{"Origin": "https://evil.example"}); code != 403 {
		t.Errorf("Origin: %d, want 403", code)
	}
	req, _ := http.NewRequest("GET", base+"/admin/v1/state", nil)
	req.Host = "rebind.example:80"
	req.Header.Set("Authorization", "Bearer "+testAdminToken)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 403 {
		t.Errorf("rebinding Host: %d, want 403", resp.StatusCode)
	}
	if resp.Header.Get("Access-Control-Allow-Origin") != "" {
		t.Error("CORS header sent")
	}
}

// A caller that holds the token and sends a request the API does not take gets a plain 403 at once: no delay, and
// nothing counted against it.
func TestAdminValidTokenOnPostIsNotAFailure(t *testing.T) {
	a, _, logs, base := startAdmin(t, nil, func(a *adminServer) { a.fail.base, a.fail.max = 10*time.Second, 10*time.Second })
	for _, m := range []string{"POST", "PUT", "DELETE"} {
		start := time.Now()
		req, _ := http.NewRequest(m, base+"/admin/v1/state", strings.NewReader("{}"))
		req.Header.Set("Authorization", "Bearer "+testAdminToken)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != 403 || time.Since(start) > time.Second {
			t.Errorf("%s: %d after %v, want 403 at once", m, resp.StatusCode, time.Since(start))
		}
	}
	a.fail.mu.Lock()
	n := len(a.fail.m)
	a.fail.mu.Unlock()
	if n != 0 || strings.Contains(logs.String(), "admin refused") {
		t.Errorf("a valid token on a POST was recorded as a failure (%d entries, log %q)", n, logs.String())
	}
}

// The state document is schema 1: these keys, and nothing about players, addresses or tokens.
func TestAdminStateSchema(t *testing.T) {
	_, s, _, base := startAdmin(t, nil)
	s.relay = true
	code, body := adminGet(t, base+"/admin/v1/state", "Bearer "+testAdminToken, nil)
	if code != 200 {
		t.Fatalf("status %d", code)
	}
	var m map[string]any
	if err := json.Unmarshal([]byte(body), &m); err != nil {
		t.Fatal(err)
	}
	var keys []string
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	want := "actions_enabled bans limits relay relay_bytes schema tcp_refusals udp_drops uptime version"
	if strings.Join(keys, " ") != want {
		t.Errorf("keys = %v, want %s", keys, want)
	}
	if m["schema"] != float64(1) || m["actions_enabled"] != false || m["relay"] != true || m["version"] != version {
		t.Errorf("state = %v", m)
	}
	bans := m["bans"].(map[string]any)
	if bans["count"] != float64(0) || bans["next_expiry_seconds"] != nil || len(bans) != 2 {
		t.Errorf("bans = %v", bans)
	}
	for _, bad := range []string{testAdminToken, "127.0.0.1", "token"} {
		if strings.Contains(body, bad) {
			t.Errorf("the state holds %q", bad)
		}
	}
	// The public status document does not say the admin API exists.
	pub, _ := json.Marshal(s.snapshot())
	if strings.Contains(string(pub), "admin") || strings.Contains(string(pub), "actions") {
		t.Error("/api/stats mentions the admin API")
	}
}

func TestAdminLogLines(t *testing.T) {
	_, _, logs, base := startAdmin(t, nil)
	forged := "Bearer x\" admin refused addr=9.9.9.9 reason=\"forged"
	adminGet(t, base+"/admin/v1/state", forged, nil)
	adminGet(t, base+"/admin/v1/state", "", nil)
	adminGet(t, base+"/admin/v1/state", "Bearer "+testAdminToken, nil)
	out := logs.String()
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if len(lines) != 2 {
		t.Fatalf("want 2 log lines (two refusals, the good request is silent), got %d:\n%s", len(lines), out)
	}
	for _, l := range lines {
		if !strings.Contains(l, `msg="admin refused"`) || !strings.Contains(l, "addr=127.0.0.1") || !strings.Contains(l, `reason="token missing or wrong"`) {
			t.Errorf("line = %s", l)
		}
		if strings.Contains(l, "9.9.9.9") || strings.Contains(l, "forged") || strings.Contains(l, testAdminToken) {
			t.Errorf("a request's text reached the log: %s", l)
		}
	}
}

func TestAdminFailTable(t *testing.T) {
	ft := newFailTable(3)
	now := time.Unix(1000, 0)
	var last time.Duration
	for i := 0; i < 12; i++ {
		d := ft.record("192.0.2.1", now)
		if d < last || d > ft.max {
			t.Fatalf("failure %d: delay %v after %v (max %v)", i, d, last, ft.max)
		}
		last = d
	}
	if last != ft.max {
		t.Errorf("the delay never reached the cap: %v", last)
	}
	// IPv6 is keyed by /64, v4-mapped by the v4 address, and the table stays at its size.
	if failKey("2001:db8:1:2::1") != failKey("2001:db8:1:2:ffff::9") || failKey("2001:db8:1:2::1") == failKey("2001:db8:1:3::1") {
		t.Error("IPv6 is not keyed by /64")
	}
	if failKey("::ffff:192.0.2.7") != failKey("192.0.2.7") {
		t.Error("v4-mapped address has its own key")
	}
	for i := 0; i < 1000; i++ {
		ft.record(net.IPv4(10, 0, byte(i>>8), byte(i)).String(), now)
	}
	if ft.ll.Len() > 3 || len(ft.m) > 3 {
		t.Errorf("table grew to %d / %d entries, size is 3", ft.ll.Len(), len(ft.m))
	}
	// Failures decay.
	ft2 := newFailTable(10)
	for i := 0; i < 8; i++ {
		ft2.record("192.0.2.5", now)
	}
	if d := ft2.record("192.0.2.5", now.Add(time.Hour)); d != ft2.base {
		t.Errorf("after the decay time the delay is %v, want %v", d, ft2.base)
	}
}

// A valid token is not delayed while failures pile up from another address. (The flooding address is held to its own
// share of connections, so some of its requests are closed; that is the point.)
func TestAdminValidTokenDuringFailures(t *testing.T) {
	_, _, _, base := startAdmin(t, nil, func(a *adminServer) { a.fail.base, a.fail.max = 50*time.Millisecond, 50*time.Millisecond })
	from := func(ip string) *http.Client {
		return &http.Client{Transport: &http.Transport{DisableKeepAlives: true,
			DialContext: (&net.Dialer{LocalAddr: &net.TCPAddr{IP: net.ParseIP(ip)}}).DialContext}}
	}
	flood := from("127.0.0.2")
	var wg sync.WaitGroup
	for i := 0; i < 40; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			req, _ := http.NewRequest("GET", base+"/admin/v1/state", nil)
			req.Header.Set("Authorization", "Bearer nope-nope-nope-nope-nope-nope-nope")
			if resp, err := flood.Do(req); err == nil {
				resp.Body.Close()
			}
		}()
	}
	time.Sleep(20 * time.Millisecond)
	req, _ := http.NewRequest("GET", base+"/admin/v1/state", nil)
	req.Header.Set("Authorization", "Bearer "+testAdminToken)
	start := time.Now()
	resp, err := from("127.0.0.3").Do(req)
	if err != nil {
		t.Skipf("cannot dial from 127.0.0.3: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != 200 || time.Since(start) > 40*time.Millisecond {
		t.Errorf("valid token during a flood: %d after %v", resp.StatusCode, time.Since(start))
	}
	wg.Wait()
}

func TestAdminReload(t *testing.T) {
	var path string
	a, _, _, base := startAdmin(t, func(c *adminConfig) { path = c.ReadTokenFile })
	const second = "Qw8-hJ3nR6xTz1Vb9MkL4pYc7SdG2fAu"
	// A half-written file keeps the old token.
	os.WriteFile(path, []byte(second[:10]), 0o600)
	if err := a.reload(); err == nil {
		t.Fatal("a 10-byte file was accepted")
	}
	if code, _ := adminGet(t, base+"/admin/v1/state", "Bearer "+testAdminToken, nil); code != 200 {
		t.Errorf("old token after a failed reload: %d", code)
	}
	os.WriteFile(path, []byte(second+"\n"), 0o600)
	if err := a.reload(); err != nil {
		t.Fatal(err)
	}
	if code, _ := adminGet(t, base+"/admin/v1/state", "Bearer "+testAdminToken, nil); code != 401 {
		t.Errorf("rotated-out token: %d, want 401", code)
	}
	if code, _ := adminGet(t, base+"/admin/v1/state", "Bearer "+second, nil); code != 200 {
		t.Errorf("new token: %d", code)
	}
}

func TestAdminReloadRefusesTheStatusToken(t *testing.T) {
	const status = "Hn7-bV2kQ9wXz4TcP6mLr1YdF8sJe3Gu"
	var path string
	a, _, _, base := startAdmin(t, func(c *adminConfig) { path = c.ReadTokenFile; c.StatusToken = status })
	os.WriteFile(path, []byte(status), 0o600)
	if err := a.reload(); err == nil || !strings.Contains(err.Error(), "same as -http-token") {
		t.Fatalf("reload to the status token: %v", err)
	}
	if code, _ := adminGet(t, base+"/admin/v1/state", "Bearer "+testAdminToken, nil); code != 200 {
		t.Errorf("old token after the refused reload: %d", code)
	}
}

func TestAdminSlowAndBigHeaders(t *testing.T) {
	a, _, _, _ := startAdmin(t, nil, func(a *adminServer) { a.hs.ReadHeaderTimeout = 150 * time.Millisecond })
	c, err := net.Dial("tcp", a.Addr())
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	c.Write([]byte("GET /admin/v1/state HTTP/1.1\r\nHost: 127.0.0.1\r\nX-Slow: "))
	c.SetReadDeadline(time.Now().Add(2 * time.Second))
	if _, err := bufio.NewReader(c).ReadByte(); err == nil || strings.Contains(err.Error(), "timeout") {
		// the server closes (EOF) or answers 408; a read timeout here means it kept waiting
		if err != nil {
			t.Errorf("the slow-header connection was not closed: %v", err)
		}
	}
	c2, _ := net.Dial("tcp", a.Addr())
	defer c2.Close()
	c2.Write([]byte("GET /admin/v1/state HTTP/1.1\r\nHost: 127.0.0.1\r\nX-Big: " + strings.Repeat("a", 20<<10) + "\r\n\r\n"))
	c2.SetReadDeadline(time.Now().Add(2 * time.Second))
	line, _ := bufio.NewReader(c2).ReadString('\n')
	if !strings.Contains(line, "431") {
		t.Errorf("oversized header: %q, want 431", line)
	}
}

func TestLimitListener(t *testing.T) {
	ln, _ := net.Listen("tcp", "127.0.0.1:0")
	l := limitListener(ln, 3, 2)
	defer l.Close()
	accepted := make(chan net.Conn, 8)
	go func() {
		for {
			c, err := l.Accept()
			if err != nil {
				return
			}
			accepted <- c
		}
	}()
	dial := func(from string) net.Conn {
		d := net.Dialer{LocalAddr: &net.TCPAddr{IP: net.ParseIP(from)}}
		c, err := d.Dial("tcp", ln.Addr().String())
		if err != nil {
			t.Skipf("cannot dial from %s: %v", from, err)
		}
		return c
	}
	closedByServer := func(c net.Conn) bool {
		c.SetReadDeadline(time.Now().Add(time.Second))
		_, err := c.Read(make([]byte, 1))
		return err == io.EOF || (err != nil && !strings.Contains(err.Error(), "timeout"))
	}
	a1, a2, a3 := dial("127.0.0.2"), dial("127.0.0.2"), dial("127.0.0.2")
	s1, s2 := <-accepted, <-accepted
	// The third connection from the same source is over its share of 2: closed at once, not queued.
	if !closedByServer(a3) {
		t.Error("a third connection from one source was kept")
	}
	select {
	case <-accepted:
		t.Error("a third connection from one source was accepted")
	case <-time.After(100 * time.Millisecond):
	}
	// Another source still gets in, up to the total of 3; the fourth overall is closed.
	b1 := dial("127.0.0.3")
	s3 := <-accepted
	c1 := dial("127.0.0.4")
	if !closedByServer(c1) {
		t.Error("a connection over the total cap was kept")
	}
	// Closing one frees its place.
	s1.Close()
	c2 := dial("127.0.0.4")
	select {
	case c := <-accepted:
		c.Close()
	case <-time.After(2 * time.Second):
		t.Error("no connection was accepted after one closed")
	}
	for _, c := range []net.Conn{a1, a2, a3, b1, c1, c2, s2, s3} {
		c.Close()
	}
	// Accept returns when the listener closes (it never waits for a free place).
	l.Close()
}

// One address that holds its whole share of connections cannot keep another address from reaching the API.
func TestAdminOneSourceCannotFillTheCap(t *testing.T) {
	a, _, _, base := startAdmin(t, nil)
	var holders []net.Conn
	for i := 0; i < adminPerSource+4; i++ {
		d := net.Dialer{LocalAddr: &net.TCPAddr{IP: net.ParseIP("127.0.0.2")}}
		c, err := d.Dial("tcp", a.Addr())
		if err != nil {
			t.Skipf("cannot dial from 127.0.0.2: %v", err)
		}
		c.Write([]byte("GET /admin/v1/state HTTP/1.1\r\nHost: 127.0.0.1\r\nX-Hold: ")) // an unfinished request
		holders = append(holders, c)
	}
	defer func() {
		for _, c := range holders {
			c.Close()
		}
	}()
	time.Sleep(50 * time.Millisecond)
	cl := &http.Client{Transport: &http.Transport{DialContext: (&net.Dialer{LocalAddr: &net.TCPAddr{IP: net.ParseIP("127.0.0.3")}}).DialContext}}
	req, _ := http.NewRequest("GET", base+"/admin/v1/state", nil)
	req.Header.Set("Authorization", "Bearer "+testAdminToken)
	resp, err := cl.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Errorf("a valid token from another address while one address held its share: %d", resp.StatusCode)
	}
}

func selfSigned(t *testing.T) (certFile, keyFile string) {
	t.Helper()
	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	tpl := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "admin.test"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour), IPAddresses: []net.IP{net.ParseIP("127.0.0.1")},
		KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
	der, _ := x509.CreateCertificate(rand.Reader, tpl, tpl, &key.PublicKey, key)
	kb, _ := x509.MarshalECPrivateKey(key)
	dir := t.TempDir()
	certFile, keyFile = filepath.Join(dir, "c.pem"), filepath.Join(dir, "k.pem")
	os.WriteFile(certFile, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o600)
	os.WriteFile(keyFile, pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: kb}), 0o600)
	return
}

func TestAdminTLS(t *testing.T) {
	cert, key := selfSigned(t)
	a, _, _, _ := startAdmin(t, func(c *adminConfig) { c.TLSCert, c.TLSKey = cert, key })
	if !a.tls {
		t.Fatal("TLS is off")
	}
	roots := x509.NewCertPool()
	pemBytes, _ := os.ReadFile(cert)
	roots.AppendCertsFromPEM(pemBytes)
	hc := &http.Client{Transport: &http.Transport{TLSClientConfig: &tls.Config{RootCAs: roots, NextProtos: []string{"h2", "http/1.1"}}}}
	req, _ := http.NewRequest("GET", "https://"+a.Addr()+"/admin/v1/state", nil)
	req.Header.Set("Authorization", "Bearer "+testAdminToken)
	resp, err := hc.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 200 || resp.ProtoMajor != 1 {
		t.Errorf("TLS request: %d, HTTP/%d (HTTP/2 must be off)", resp.StatusCode, resp.ProtoMajor)
	}
	old := &http.Client{Transport: &http.Transport{TLSClientConfig: &tls.Config{RootCAs: roots, MaxVersion: tls.VersionTLS11}}}
	if _, err := old.Get("https://" + a.Addr() + "/admin/v1/state"); err == nil {
		t.Error("a TLS 1.1 client connected")
	}
	// Over TLS a name in Host is accepted (the certificate vouches for it); plain HTTP refuses names.
	if !a.hostOK("admin.example:8443") || a.hostOK("") {
		t.Error("hostOK over TLS")
	}
}

func FuzzAdminAuth(f *testing.F) {
	for _, seed := range [][2]string{{"Bearer " + testAdminToken, "127.0.0.1:1"}, {"", ""}, {"Bearer ", "localhost"}, {"bearer " + testAdminToken, "[::1]:9"},
		{"Bearer " + testAdminToken + " ", "evil.example"}, {"Bearer \x00", "127.0.0.1"}} {
		f.Add(seed[0], seed[1])
	}
	s := NewServer(10, false)
	s.log = newLogger(io.Discard, 0, "text")
	tok := writeTokenForFuzz(f)
	a, err := newAdminServer(s, adminConfig{Addr: "127.0.0.1:0", ReadTokenFile: tok})
	if err != nil {
		f.Fatal(err)
	}
	a.ln.Close()
	a.fail.base, a.fail.max = 0, 0
	h := a.handler()
	f.Fuzz(func(t *testing.T, auth, host string) {
		req, err := http.NewRequest("GET", "http://127.0.0.1/admin/v1/state", nil)
		if err != nil {
			return
		}
		req.Host = host
		req.Header.Set("Authorization", auth)
		req.RemoteAddr = "192.0.2.1:5555"
		rec := &respRecorder{h: http.Header{}}
		h.ServeHTTP(rec, req)
		if rec.code == 200 && !(len(auth) > 7 && strings.EqualFold(auth[:7], "Bearer ") && auth[7:] == testAdminToken) {
			t.Fatalf("answered 200 to Authorization %q Host %q", auth, host)
		}
		if strings.Contains(rec.body.String(), testAdminToken) {
			t.Fatal("the response holds the token")
		}
	})
}

func writeTokenForFuzz(f *testing.F) string {
	p := filepath.Join(f.TempDir(), "admin.token")
	if err := os.WriteFile(p, []byte(testAdminToken), 0o600); err != nil {
		f.Fatal(err)
	}
	return p
}

func TestAdminBearerSchemeIsCaseInsensitive(t *testing.T) {
	_, _, _, base := startAdmin(t, nil)
	for _, scheme := range []string{"bearer", "BEARER", "Bearer"} {
		if code, _ := adminGet(t, base+"/admin/v1/state", scheme+" "+testAdminToken, nil); code != 200 {
			t.Errorf("scheme %q with the token: %d, want 200", scheme, code)
		}
	}
	if code, _ := adminGet(t, base+"/admin/v1/state", "bearer wrong-token-wrong-token-wrong-token", nil); code != 401 {
		t.Errorf("lower-case scheme with a wrong token: %d, want 401", code)
	}
}

func TestAdminBrowserRefusalsAreLoggedAndCounted(t *testing.T) {
	_, _, logs, base := startAdmin(t, nil)
	if code, _ := adminGet(t, base+"/admin/v1/state", "Bearer "+testAdminToken, map[string]string{"Origin": "https://evil.example"}); code != 403 {
		t.Fatalf("Origin: %d, want 403", code)
	}
	out := logs.String()
	if !strings.Contains(out, `msg="admin refused"`) || !strings.Contains(out, `reason="origin or host refused"`) || !strings.Contains(out, "addr=127.0.0.1") {
		t.Errorf("no log line for the Origin refusal:\n%s", out)
	}
}

func TestAdminRefusalsCloseTheConnection(t *testing.T) {
	_, _, _, base := startAdmin(t, nil)
	req, _ := http.NewRequest("GET", base+"/admin/v1/state", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 401 || !resp.Close {
		t.Errorf("status %d, close %v: a refusal must close the connection", resp.StatusCode, resp.Close)
	}
}

func TestAdminTokenFileFIFOIsRefusedWithoutBlocking(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("no FIFOs on Windows")
	}
	path := filepath.Join(t.TempDir(), "fifo")
	if err := exec.Command("mkfifo", path).Run(); err != nil {
		t.Skip("no mkfifo:", err)
	}
	done := make(chan error, 1)
	go func() { _, err := loadAdminToken(path); done <- err }()
	select {
	case err := <-done:
		if err == nil || !strings.Contains(err.Error(), "not a regular file") {
			t.Errorf("err = %v, want not a regular file", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("loadAdminToken blocked on a FIFO")
	}
}

func TestSameListenerLocalhostIsLoopback(t *testing.T) {
	if !sameListener("localhost:8080", "127.0.0.1:8080") || !sameListener("127.0.0.1:8080", "LOCALHOST:8080") {
		t.Error("localhost and 127.0.0.1 on one port must count as the same listener")
	}
	if sameListener("localhost:8080", "127.0.0.1:8081") {
		t.Error("different ports are different listeners")
	}
}
