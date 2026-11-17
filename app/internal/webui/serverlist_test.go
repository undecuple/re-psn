package webui

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/undecuple/re-psn/app/internal/client"
	"github.com/undecuple/re-psn/app/internal/servers"
	"github.com/undecuple/re-psn/app/internal/tui"
)

// fakeFetch answers every query as reachable (or as an error for addresses in fail) and records what it was asked.
type fakeFetch struct {
	mu            sync.Mutex
	queries       []tui.Query
	inflight, max int32
	hold          time.Duration
	fail          map[string]bool
}

func (f *fakeFetch) Fetch(ctx context.Context, q tui.Query) (tui.Snapshot, error) {
	n := atomic.AddInt32(&f.inflight, 1)
	defer atomic.AddInt32(&f.inflight, -1)
	f.mu.Lock()
	f.queries = append(f.queries, q)
	if n > f.max {
		f.max = n
	}
	f.mu.Unlock()
	time.Sleep(f.hold)
	if f.fail[q.Server] {
		return tui.Snapshot{}, errors.New("down")
	}
	return tui.Snapshot{Rooms: client.RoomList{Rooms: make([]client.RoomInfo, 2)}, RTT: 3 * time.Millisecond}, nil
}

func serverState(t *testing.T, f fixture) ServerList {
	t.Helper()
	var st State
	if err := json.Unmarshal(callUI(t, f, f.token, "/api/state", nil, 200), &st); err != nil {
		t.Fatal(err)
	}
	return st.Servers
}

func addServer(t *testing.T, f fixture, name, server, statusURL string, want int) {
	t.Helper()
	callUI(t, f, f.token, "/api/servers/add", map[string]string{"name": name, "server": server, "status_url": statusURL}, want)
}

func TestServerListAddRemoveAndPersist(t *testing.T) {
	f := serveUI(t)
	saveRole(t, f, "play", nil)
	if l := serverState(t, f); len(l.Entries) != 0 || l.Error != "" || l.Max != servers.MaxEntries {
		t.Fatalf("a new app must start with an empty list: %+v", l)
	}
	addServer(t, f, "Alice", "Play.Example.org:36100", "", 200)
	addServer(t, f, "Bob <img src=x onerror=alert(1)>", "10.0.0.2:36100", "https://10.0.0.2/status", 200)
	addServer(t, f, "again", "play.example.org:36100", "", 400)
	addServer(t, f, "", "h:1", "", 400)
	addServer(t, f, "x", "no-port", "", 400)
	addServer(t, f, "x", "h:1", "https://u:p@h/s", 400)
	l := serverState(t, f)
	if len(l.Entries) != 2 || l.Entries[0].Server != "play.example.org:36100" || l.Entries[0].Local || !l.Entries[1].Local {
		t.Fatalf("wrong list: %+v", l.Entries)
	}
	if l.Entries[1].Name != "Bob <img src=x onerror=alert(1)>" || l.Entries[0].Check.Status != "unchecked" {
		t.Errorf("name or check state altered: %+v", l.Entries)
	}
	path := servers.PathFor(f.server.opts.ConfigPath)
	if runtime.GOOS != "windows" {
		if st, err := os.Stat(path); err != nil || st.Mode().Perm() != 0o600 {
			t.Errorf("servers.json mode %v, err %v; want 0600", st.Mode().Perm(), err)
		}
	}
	callUI(t, f, f.token, "/api/servers/remove", map[string]string{"server": "PLAY.example.org:36100"}, 200)
	callUI(t, f, f.token, "/api/servers/remove", map[string]string{"server": "play.example.org:36100"}, 400)
	// A new app instance reads the same list back.
	if again := newServerStore(f.server.opts.ConfigPath).view(); len(again.Entries) != 1 || again.Entries[0].Server != "10.0.0.2:36100" {
		t.Errorf("not persisted: %+v", again.Entries)
	}
}

func TestServerListNeedsTokenAndPlayRole(t *testing.T) {
	f := serveUI(t)
	saveRole(t, f, "host", nil)
	addServer(t, f, "Alice", "h:1", "", 403)
	callUI(t, f, f.token, "/api/servers/check-all", map[string]string{}, 403)
	saveRole(t, f, "play", nil)
	callUI(t, f, "not-the-token", "/api/servers/add", map[string]string{"name": "x", "server": "h:1"}, 401)
	if l := serverState(t, f); len(l.Entries) != 0 {
		t.Errorf("a refused request changed the list: %+v", l.Entries)
	}
}

func TestServerCheckNeverSendsThePlayToken(t *testing.T) {
	f := serveUI(t)
	saveRole(t, f, "play", nil)
	ff := &fakeFetch{fail: map[string]bool{"down.example.org:1": true}}
	f.server.fetch = ff
	f.server.mu.Lock()
	f.server.file.StatusTokenFile = "/private/play.token"
	f.server.mu.Unlock()
	addServer(t, f, "Up", "up.example.org:1", "https://up.example.org/status", 200)
	addServer(t, f, "Down", "down.example.org:1", "", 200)
	callUI(t, f, f.token, "/api/servers/check", map[string]string{"server": "up.example.org:1", "status_url": "https://up.example.org/status"}, 200)
	callUI(t, f, f.token, "/api/servers/check", map[string]string{"server": "down.example.org:1"}, 200)
	callUI(t, f, f.token, "/api/servers/check", map[string]string{"server": "other.example.org:1"}, 400)
	l := serverState(t, f)
	if l.Entries[0].Check.Status != "reachable" || l.Entries[0].Check.Rooms != 2 || l.Entries[1].Check.Status != "unavailable" {
		t.Errorf("wrong results: %+v", l.Entries)
	}
	if len(ff.queries) != 2 {
		t.Fatalf("want 2 queries, got %d", len(ff.queries))
	}
	for _, q := range ff.queries {
		if q.StatusTokenFile != "" {
			t.Errorf("the Play status token file was sent to a saved server: %+v", q)
		}
	}
	// Results are in memory only and go with the entry.
	callUI(t, f, f.token, "/api/servers/remove", map[string]string{"server": "up.example.org:1", "status_url": "https://up.example.org/status"}, 200)
	addServer(t, f, "Up", "up.example.org:1", "https://up.example.org/status", 200)
	if c := serverState(t, f).Entries[1].Check.Status; c != "unchecked" {
		t.Errorf("a removed entry's result came back: %q", c)
	}
}

func TestServerCheckAllIsBounded(t *testing.T) {
	f := serveUI(t)
	saveRole(t, f, "play", nil)
	ff := &fakeFetch{hold: 40 * time.Millisecond, fail: map[string]bool{"h3:1": true}}
	f.server.fetch = ff
	for i := 0; i < 12; i++ {
		addServer(t, f, "s", "h"+strconv.Itoa(i)+":1", "", 200)
	}
	callUI(t, f, f.token, "/api/servers/check-all", map[string]string{}, 200)
	if ff.max > checkParallel || ff.max < 2 {
		t.Errorf("%d checks ran at once; want 2 to %d", ff.max, checkParallel)
	}
	if len(ff.queries) != 12 {
		t.Errorf("%d checks ran, want 12", len(ff.queries))
	}
	reach, down := 0, 0
	for _, e := range serverState(t, f).Entries {
		switch e.Check.Status {
		case "reachable":
			reach++
		case "unavailable":
			down++
		}
	}
	if reach != 11 || down != 1 {
		t.Errorf("%d reachable, %d unavailable; want 11 and 1", reach, down)
	}
}

func TestServerCheckAllCannotOverlap(t *testing.T) {
	f := serveUI(t)
	saveRole(t, f, "play", nil)
	ff := &fakeFetch{hold: 600 * time.Millisecond}
	f.server.fetch = ff
	addServer(t, f, "s", "h:1", "", 200)
	done := make(chan struct{})
	go func() { callUI(t, f, f.token, "/api/servers/check-all", map[string]string{}, 200); close(done) }()
	time.Sleep(150 * time.Millisecond)
	callUI(t, f, f.token, "/api/servers/check-all", map[string]string{}, 409)
	<-done
	if len(ff.queries) != 1 {
		t.Errorf("%d checks ran, want 1", len(ff.queries))
	}
}

func TestServerUseSetsPlayTargetAndForgetsToken(t *testing.T) {
	f := serveUI(t)
	saveRole(t, f, "play", map[string]any{"server": "old.example.org:1", "status_url": "https://old.example.org/status"})
	f.server.mu.Lock()
	f.server.file.StatusTokenFile = "/private/play.token"
	f.server.mu.Unlock()
	addServer(t, f, "Same URL", "new.example.org:1", "https://old.example.org/status", 200)
	addServer(t, f, "New URL", "new.example.org:2", "https://new.example.org/status", 200)
	addServer(t, f, "No URL", "new.example.org:3", "", 200)
	use := func(server, statusURL string) {
		callUI(t, f, f.token, "/api/servers/use", map[string]string{"server": server, "status_url": statusURL}, 200)
	}
	use("new.example.org:1", "https://old.example.org/status")
	if v := f.server.file.Settings; v.Server != "new.example.org:1" || v.StatusURL != "https://old.example.org/status" || v.StatusTokenFile == "" {
		t.Errorf("an unchanged status URL must keep its token file: %+v", v)
	}
	use("new.example.org:2", "https://new.example.org/status")
	if v := f.server.file.Settings; v.Server != "new.example.org:2" || v.StatusTokenFile != "" {
		t.Errorf("a new status URL must forget the token file: %+v", v)
	}
	use("new.example.org:3", "")
	if v := f.server.file.Settings; v.Server != "new.example.org:3" || v.StatusURL != "" {
		t.Errorf("the status URL was not cleared: %+v", v)
	}
	// The choice reached the settings file, and the old Play result is gone.
	if b, err := os.ReadFile(f.server.opts.ConfigPath); err != nil || !strings.Contains(string(b), "new.example.org:3") {
		t.Errorf("settings file: %v %q", err, b)
	}
	if p := f.server.state().Play; p.Status != "unchecked" {
		t.Errorf("play status %q after Use, want unchecked", p.Status)
	}
	callUI(t, f, f.token, "/api/servers/use", map[string]string{"server": "nowhere.example.org:1"}, 400)
}

func TestServerExportIsTheFileFormat(t *testing.T) {
	f := serveUI(t)
	saveRole(t, f, "play", nil)
	addServer(t, f, "Alice", "a.example.org:1", "", 200)
	b := callUI(t, f, f.token, "/api/servers/export", map[string]string{}, 200)
	l, err := servers.Parse(strings.NewReader(string(b)))
	if err != nil || len(l.Entries) != 1 || l.Entries[0].Name != "Alice" {
		t.Fatalf("export is not a valid list: %v %q", err, b)
	}
	if strings.Contains(string(b), "token") {
		t.Errorf("an export must carry no token: %q", b)
	}
}

func TestCorruptServerFileIsKept(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "private")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	const junk = `{"version": 1, "servers": [ nope`
	path := filepath.Join(dir, servers.FileName)
	if err := os.WriteFile(path, []byte(junk), 0o600); err != nil {
		t.Fatal(err)
	}
	st := newServerStore(filepath.Join(dir, "re-psn.ini"))
	if v := st.view(); v.Error == "" || len(v.Entries) != 0 {
		t.Fatalf("a corrupt file must show an error and no entries: %+v", v)
	}
	if err := st.edit(func(l *servers.List) error { return l.Add(servers.Entry{Name: "x", Server: "h:1"}) }); err == nil {
		t.Error("a corrupt file was edited")
	}
	if b, _ := os.ReadFile(path); string(b) != junk {
		t.Errorf("the corrupt file was changed: %q", b)
	}
}

// A saved server that answers with a redirect is unavailable: the status reader does not follow it, so nothing is sent to
// the address it names.
func TestServerCheckDoesNotFollowRedirects(t *testing.T) {
	var hit atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { hit.Add(1) }))
	defer target.Close()
	redirect := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL+"/api/stats", http.StatusFound)
	}))
	defer redirect.Close()
	ln, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	dead := ln.Addr().String()
	ln.Close()
	f := serveUI(t)
	saveRole(t, f, "play", nil)
	addServer(t, f, "Redirecting", dead, redirect.URL+"/status", 200)
	callUI(t, f, f.token, "/api/servers/check", map[string]string{"server": dead, "status_url": redirect.URL + "/status"}, 200)
	if c := serverState(t, f).Entries[0].Check; c.Status != "unavailable" {
		t.Errorf("status %q, want unavailable", c.Status)
	}
	if hit.Load() != 0 {
		t.Errorf("the redirect target was contacted %d times", hit.Load())
	}
}
