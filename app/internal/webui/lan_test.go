package webui

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/netip"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/undecuple/re-psn/app/internal/client"
)

// fakeDiscover is an injected discoverer. It records what it was asked and answers from found, err and hold.
type fakeDiscover struct {
	mu      sync.Mutex
	ports   []uint16
	waits   []time.Duration
	found   []client.Found
	err     error
	hold    time.Duration
	hadLine bool
}

func (d *fakeDiscover) run(ctx context.Context, wait time.Duration, port uint16) ([]client.Found, error) {
	d.mu.Lock()
	d.ports = append(d.ports, port)
	d.waits = append(d.waits, wait)
	_, d.hadLine = ctx.Deadline()
	found, err, hold := d.found, d.err, d.hold
	d.mu.Unlock()
	time.Sleep(hold)
	return found, err
}

func foundAt(i int, name string) client.Found {
	return client.Found{IP: netip.AddrFrom4([4]byte{192, 0, 2, byte(i)}), TCPPort: 36100, Players: uint16(i), Name: name, Version: "0.9.0"}
}

func lanState(t *testing.T, f fixture) []LANServer {
	t.Helper()
	var st State
	b := callUI(t, f, f.token, "/api/state", nil, 200)
	if err := json.Unmarshal(b, &st); err != nil {
		t.Fatal(err)
	}
	if st.LAN == nil || !strings.Contains(string(b), `"lan":[`) {
		t.Fatalf("lan must always be a list: %s", b)
	}
	return st.LAN
}

func findLANCall(t *testing.T, f fixture, body any, want int) State {
	t.Helper()
	b := callUI(t, f, f.token, "/api/lan/find", body, want)
	var st State
	if want == 200 {
		if err := json.Unmarshal(b, &st); err != nil {
			t.Fatal(err)
		}
	}
	return st
}

func TestLANFindNoneOneAndMany(t *testing.T) {
	f := serveUI(t)
	saveRole(t, f, "play", nil)
	d := &fakeDiscover{}
	f.server.discover = d.run
	if got := lanState(t, f); len(got) != 0 {
		t.Fatalf("a new app has searched nothing: %v", got)
	}
	st := findLANCall(t, f, struct{}{}, 200)
	if len(st.LAN) != 0 || st.Settings.Mode != "play" {
		t.Fatalf("none found must be an empty list in a full state: %+v", st)
	}
	if len(d.ports) != 1 || d.ports[0] != 36100 || d.waits[0] != 1500*time.Millisecond || !d.hadLine {
		t.Fatalf("defaults: ports %v waits %v deadline %v", d.ports, d.waits, d.hadLine)
	}
	d.found = []client.Found{foundAt(7, "Friend box")}
	st = findLANCall(t, f, struct{}{}, 200)
	if len(st.LAN) != 1 || st.LAN[0] != (LANServer{Address: "192.0.2.7:36100", Name: "Friend box", Players: 7, Version: "0.9.0"}) {
		t.Fatalf("%+v", st.LAN)
	}
	if got := lanState(t, f); len(got) != 1 {
		t.Fatalf("the answer is kept for the next state read: %v", got)
	}
	// Nothing was saved: the settings and the saved servers are as they were.
	if st.Settings.Server != "" || len(st.Servers.Entries) != 0 {
		t.Fatalf("a search must save nothing: %+v %+v", st.Settings, st.Servers)
	}
	// A later search replaces the earlier answer.
	d.found = nil
	if st = findLANCall(t, f, struct{}{}, 200); len(st.LAN) != 0 {
		t.Fatalf("%+v", st.LAN)
	}
}

func TestLANFindErrorIsPlainAndClearsTheList(t *testing.T) {
	f := serveUI(t)
	saveRole(t, f, "both", nil)
	d := &fakeDiscover{found: []client.Found{foundAt(1, "a")}}
	f.server.discover = d.run
	findLANCall(t, f, struct{}{}, 200)
	d.err = errors.New("listen udp4 10.9.8.7:5555: /home/someone/secret: permission denied")
	b := callUI(t, f, f.token, "/api/lan/find", struct{}{}, 400)
	if !strings.Contains(string(b), "search on this network failed") || strings.Contains(string(b), "10.9.8.7") || strings.Contains(string(b), "/home") {
		t.Fatalf("the failure text must carry nothing from the error: %s", b)
	}
	if got := lanState(t, f); len(got) != 0 {
		t.Fatalf("a failed search must not leave an old list looking current: %v", got)
	}
}

func TestLANFindBoundsCountAndCleansText(t *testing.T) {
	f := serveUI(t)
	saveRole(t, f, "play", nil)
	d := &fakeDiscover{}
	for i := 1; i <= 40; i++ {
		d.found = append(d.found, foundAt(i, fmt.Sprintf("box-%d", i)))
	}
	d.found[0].Name = "ev\x00il\x1b[31m‮" + strings.Repeat("N", 100)
	d.found[0].Version = "v\n1⁦" + strings.Repeat("9", 40)
	d.found[1].IP = netip.Addr{} // not an address: dropped, not shown
	f.server.discover = d.run
	st := findLANCall(t, f, struct{}{}, 200)
	if len(st.LAN) != maxLANServers {
		t.Fatalf("%d entries, want %d", len(st.LAN), maxLANServers)
	}
	first := st.LAN[0]
	if strings.ContainsAny(first.Name, "\x00\x1b\n‮") || strings.ContainsAny(first.Version, "\n⁦") || len(first.Name) > 24 || len(first.Version) > 12 || !strings.HasPrefix(first.Name, "ev") {
		t.Fatalf("%q %q", first.Name, first.Version)
	}
	if st.LAN[1].Address != "192.0.2.3:36100" {
		t.Fatalf("the entry without an address is dropped and the order kept: %+v", st.LAN[1])
	}
}

func TestLANFindPort(t *testing.T) {
	f := serveUI(t)
	saveRole(t, f, "play", map[string]any{"server": "play.example.org:4242"})
	d := &fakeDiscover{}
	f.server.discover = d.run
	findLANCall(t, f, struct{}{}, 200) // the saved server's port
	findLANCall(t, f, map[string]int{"port": 5000}, 200)
	findLANCall(t, f, map[string]int{"port": 65535}, 200)
	findLANCall(t, f, map[string]int{"port": 1}, 200)
	want := []uint16{4242, 5000, 65535, 1}
	if fmt.Sprint(d.ports) != fmt.Sprint(want) {
		t.Fatalf("ports %v, want %v", d.ports, want)
	}
	// Settings refuse a server without a port, so the fallbacks are checked on the function itself.
	for _, server := range []string{"", "play.example.org", "play.example.org:notaport", "play.example.org:0", "play.example.org:70000"} {
		if p, err := lanPort(nil, server); err != nil || p != client.DefaultPort {
			t.Fatalf("%q: port %d err %v, want the default", server, p, err)
		}
	}
}

func TestLANFindRefusesBadInput(t *testing.T) {
	f := serveUI(t)
	saveRole(t, f, "play", nil)
	d := &fakeDiscover{}
	f.server.discover = d.run
	for _, body := range []any{map[string]int{"port": 0}, map[string]int{"port": -1}, map[string]int{"port": 65536}, map[string]int{"port": 1 << 40}, map[string]string{"port": "36100"}, map[string]any{"port": 1.5}, map[string]any{"extra": 1}, map[string]any{"port": nil, "x": 1}} {
		callUI(t, f, f.token, "/api/lan/find", body, 400)
	}
	callUI(t, f, f.token, "/api/lan/find", []int{1}, 400)
	if len(d.ports) != 0 {
		t.Fatalf("a refused request must not broadcast: %v", d.ports)
	}
	callUI(t, f, "", "/api/lan/find", struct{}{}, 401)
	callUI(t, f, f.nonce, "/api/lan/find", struct{}{}, 401)
}

func TestLANFindRoleRefused(t *testing.T) {
	f := serveUI(t)
	d := &fakeDiscover{found: []client.Found{foundAt(1, "a")}}
	f.server.discover = d.run
	callUI(t, f, f.token, "/api/lan/find", struct{}{}, 403) // no workspace chosen yet
	saveRole(t, f, "host", nil)
	callUI(t, f, f.token, "/api/lan/find", struct{}{}, 403)
	if len(d.ports) != 0 {
		t.Fatalf("a refused role must not broadcast: %v", d.ports)
	}
	saveRole(t, f, "both", nil)
	findLANCall(t, f, struct{}{}, 200)
}

func TestLANFindSecondPressGets409(t *testing.T) {
	f := serveUI(t)
	saveRole(t, f, "play", nil)
	d := &fakeDiscover{hold: 600 * time.Millisecond, found: []client.Found{foundAt(1, "a")}}
	f.server.discover = d.run
	done := make(chan struct{})
	go func() { callUI(t, f, f.token, "/api/lan/find", struct{}{}, 200); close(done) }()
	time.Sleep(150 * time.Millisecond)
	callUI(t, f, f.token, "/api/lan/find", struct{}{}, 409)
	<-done
	d.mu.Lock()
	n := len(d.ports)
	d.mu.Unlock()
	if n != 1 {
		t.Fatalf("%d searches ran, want 1", n)
	}
}

func TestLANResultsClearedBySettingsChange(t *testing.T) {
	f := serveUI(t)
	saveRole(t, f, "play", nil)
	d := &fakeDiscover{found: []client.Found{foundAt(1, "a"), foundAt(2, "b")}}
	f.server.discover = d.run
	findLANCall(t, f, struct{}{}, 200)
	if len(lanState(t, f)) != 2 {
		t.Fatal("expected two")
	}
	saveRole(t, f, "play", map[string]any{"name": "someone"})
	if got := lanState(t, f); len(got) != 0 {
		t.Fatalf("a settings save must clear the list: %v", got)
	}
	// Using a saved server is a settings change too.
	findLANCall(t, f, struct{}{}, 200)
	addServer(t, f, "s", "h.example.org:1", "", 200)
	if len(lanState(t, f)) != 2 {
		t.Fatal("adding a saved server does not change the settings")
	}
	callUI(t, f, f.token, "/api/servers/use", map[string]string{"server": "h.example.org:1", "status_url": ""}, 200)
	if got := lanState(t, f); len(got) != 0 {
		t.Fatalf("using a saved server must clear the list: %v", got)
	}
}

func TestLANDefaultsAreWired(t *testing.T) {
	f := serveUI(t)
	if f.server.discover == nil {
		t.Fatal("a new server must use the real discovery")
	}
}

func TestLANPort(t *testing.T) {
	n := func(v int) *int { return &v }
	for _, c := range []struct {
		req    *int
		server string
		want   uint16
		bad    bool
	}{{nil, "", 36100, false}, {nil, "h:7", 7, false}, {nil, "[::1]:9000", 9000, false}, {nil, "h:0", 36100, false}, {nil, "h:70000", 36100, false}, {nil, "h", 36100, false}, {n(80), "h:7", 80, false}, {n(0), "", 0, true}, {n(65536), "", 0, true}, {n(-5), "h:7", 0, true}} {
		got, err := lanPort(c.req, c.server)
		if (err != nil) != c.bad || got != c.want {
			t.Errorf("%v %q: %d %v", c.req, c.server, got, err)
		}
	}
}

func TestLANFindDoesNotStopTheApp(t *testing.T) {
	f := serveUI(t)
	saveRole(t, f, "both", nil)
	d := &fakeDiscover{found: []client.Found{foundAt(1, "a")}}
	f.server.discover = d.run
	findLANCall(t, f, struct{}{}, 200)
	if f.server.ctx.Err() != nil {
		t.Fatal("a LAN search must not stop the app")
	}
	// The app still answers afterwards, and the search ran once.
	if got := lanState(t, f); len(got) != 1 || len(d.ports) != 1 {
		t.Fatalf("%v %v", got, d.ports)
	}
}

func TestLANBuildDropsPortlessAndKeepsOrder(t *testing.T) {
	a, b, c := foundAt(3, "c"), foundAt(2, "b"), foundAt(1, "a")
	b.TCPPort = 0
	got := buildLAN([]client.Found{a, b, c})
	if len(got) != 2 || got[0].Name != "c" || got[1].Name != "a" {
		t.Fatalf("%+v", got)
	}
	if got := buildLAN(nil); got == nil || len(got) != 0 {
		t.Fatalf("an empty answer must be an empty list, not null: %#v", got)
	}
	if cleanLAN("  padded \t", 24) != "padded" || cleanLAN("日本語日本語", 3) != "日本語" {
		t.Fatal("cleanLAN trims and counts characters, not bytes")
	}
}
