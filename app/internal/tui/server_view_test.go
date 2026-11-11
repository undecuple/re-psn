package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/undecuple/re-psn/app/internal/config"
)

const baseStats = `"uptime":"42s","clients":3,"connections_total":5,"unlisted_rooms":0,"rooms":[],"boards":[]`

const fullStats = `{` + baseStats + `,"version":"v0.5.0","relay":true,"relay_bytes":1048576,
"limits":{"max_rooms":0,"max_player_rooms":8,"max_conns_per_addr":4,"max_req_rate":0,"max_udp_rate":100},
"tcp_refusals":{"conns_per_addr":2,"req_rate":3},
"udp_drops":{"rate_limit":1,"source_capacity":2,"malformed":3,"unauthorized":4,"source_mismatch":5,"relay_disabled":6,"write_error":7,"discover_refused":8}}`

func u64(v uint64) *uint64 { return &v }

func TestDecodeServerStatsAllFields(t *testing.T) {
	h, err := decodeHTTPStatus([]byte(fullStats))
	if err != nil {
		t.Fatal(err)
	}
	s := h.Server
	if s.Version == nil || *s.Version != "v0.5.0" || s.Relay == nil || !*s.Relay || s.RelayBytes == nil || *s.RelayBytes != 1048576 {
		t.Fatalf("scalars: %+v", s)
	}
	if s.Limits.MaxRooms == nil || *s.Limits.MaxRooms != 0 || *s.Limits.MaxPlayerRooms != 8 || *s.Limits.MaxUDPRate != 100 {
		t.Fatalf("limits: %+v", s.Limits)
	}
	if *s.TCP.ConnsPerAddr != 2 || *s.TCP.ReqRate != 3 || *s.UDP.RateLimit != 1 || *s.UDP.DiscoverRefused != 8 {
		t.Fatalf("counters: %+v %+v", s.TCP, s.UDP)
	}
	if !s.Reported() {
		t.Fatal("not reported")
	}
}

func TestDecodeServerStatsOldServer(t *testing.T) {
	h, err := decodeHTTPStatus([]byte(`{` + baseStats + `}`))
	if err != nil {
		t.Fatal(err)
	}
	s := h.Server
	if s.Reported() || s.Version != nil || s.Relay != nil || s.RelayBytes != nil || s.Limits.MaxRooms != nil || s.TCP.ReqRate != nil || s.UDP.RateLimit != nil {
		t.Fatalf("old server must leave everything nil: %+v", s)
	}
}

func TestDecodeServerStatsPartial(t *testing.T) {
	h, err := decodeHTTPStatus([]byte(`{` + baseStats + `,"relay":false,"limits":{"max_rooms":5},"udp_drops":{"malformed":9},"relay_bytes":-1,"version":7}`))
	if err != nil {
		t.Fatal(err)
	}
	s := h.Server
	if s.Relay == nil || *s.Relay || s.Limits.MaxRooms == nil || *s.Limits.MaxRooms != 5 || s.Limits.MaxUDPRate != nil {
		t.Fatalf("partial: %+v", s)
	}
	if *s.UDP.Malformed != 9 || s.UDP.RateLimit != nil || s.RelayBytes != nil || s.Version != nil {
		t.Fatalf("partial counters or bad types kept: %+v", s)
	}
}

func TestServerTrackRate(t *testing.T) {
	t0 := time.Unix(1000, 0)
	a := ServerStats{RelayBytes: u64(1000)}
	b := ServerStats{RelayBytes: u64(4000)}
	var tr serverTrack
	tr = tr.next(a, t0)
	if tr.hasRate || tr.restarted {
		t.Fatalf("first sample has no rate: %+v", tr)
	}
	tr = tr.next(b, t0.Add(3*time.Second))
	if !tr.hasRate || tr.rate != 1000 || tr.restarted {
		t.Fatalf("rate: %+v", tr)
	}
	// An older server that stops reporting relay_bytes gives no rate.
	tr = tr.next(ServerStats{}, t0.Add(6*time.Second))
	if tr.hasRate || tr.restarted {
		t.Fatalf("no field, no rate: %+v", tr)
	}
}

func TestServerTrackCounterWentDown(t *testing.T) {
	t0 := time.Unix(1000, 0)
	var tr serverTrack
	tr = tr.next(ServerStats{RelayBytes: u64(9000), TCP: TCPRefusals{ReqRate: u64(5)}}, t0)
	// Only a refusal counter goes down; relay bytes still grow.
	tr = tr.next(ServerStats{RelayBytes: u64(9500), TCP: TCPRefusals{ReqRate: u64(1)}}, t0.Add(time.Second))
	if !tr.restarted || tr.hasRate {
		t.Fatalf("restart must drop the rate: %+v", tr)
	}
	// The baseline is the new sample, so the next refresh has a rate again.
	tr = tr.next(ServerStats{RelayBytes: u64(10500), TCP: TCPRefusals{ReqRate: u64(1)}}, t0.Add(3*time.Second))
	if tr.restarted || !tr.hasRate || tr.rate != 500 {
		t.Fatalf("after restart: %+v", tr)
	}
}

func serverModel(t *testing.T, token string, stats ...string) Model {
	t.Helper()
	set := config.Settings{Server: "127.0.0.1:36100", StatusURL: "http://127.0.0.1:36101"}
	if token != "" {
		p := filepath.Join(t.TempDir(), "token")
		if err := os.WriteFile(p, []byte(token), 0o600); err != nil {
			t.Fatal(err)
		}
		set.StatusTokenFile = p
	}
	now := time.Unix(5000, 0)
	m := New(Options{File: &config.File{Settings: set}, Fetcher: &fake{}, Now: func() time.Time { return now }})
	m.content, m.menu, m.h = true, paneServer, 60
	for i, s := range stats {
		h, err := decodeHTTPStatus([]byte(s))
		if err != nil {
			t.Fatal(err)
		}
		m.seq++
		m.snapQuery = Query{StatusURL: set.StatusURL, Server: set.Server}
		m = drive(t, m, fetchedMsg{seq: m.seq, snap: Snapshot{HTTP: h}, at: now.Add(time.Duration(i) * 2 * time.Second)})
	}
	return m
}

func TestServerViewNewServer(t *testing.T) {
	next := strings.Replace(fullStats, `"relay_bytes":1048576`, `"relay_bytes":1050624`, 1)
	m := serverModel(t, "", fullStats, next)
	mustShow(t, m, "[Server]", "Read-only", "v0.5.0", "42s", "Relay on", "Rooms none", "Player rooms 8", "TCP req/s none",
		"per address 2", "request rate 3", "rate limit 1", "1.0 MiB", "Relay rate 1.0 KiB/s",
		"payload carried by the relay", "not network bandwidth")
	mustNotShow(t, m, "Server restarted")
}

func TestServerViewOldServer(t *testing.T) {
	m := serverModel(t, "", `{`+baseStats+`}`)
	mustShow(t, m, "older server", "Version not reported by this server", "Relay not reported by this server",
		"Rooms not reported by this server", "TCP refused not reported by this server", "Relay bytes not reported by this server")
	mustNotShow(t, m, "Relay off", "Relay rate", "Rooms none", "UDP rate none", "Rooms 0")
}

func TestServerViewFirstRefreshHasNoRate(t *testing.T) {
	m := serverModel(t, "", fullStats)
	mustShow(t, m, "needs a second refresh")
}

func TestServerViewRestart(t *testing.T) {
	const lower = `{` + baseStats + `,"relay_bytes":10,"relay":true,"tcp_refusals":{"conns_per_addr":0,"req_rate":0}}`
	m := serverModel(t, "", fullStats, lower)
	mustShow(t, m, "Server restarted")
	mustNotShow(t, m, "B/s")
}

func TestServerViewNoToken(t *testing.T) {
	const tok = "synthetic-secret-token-value"
	m := serverModel(t, tok+"\n", fullStats, fullStats)
	for _, w := range []int{60, 100, 160} {
		m.w, m.h = w, 60
		if strings.Contains(m.View().Content, tok) || strings.Contains(screen(m), tok) {
			t.Fatalf("token on screen at width %d", w)
		}
	}
}

func TestServerTabOnlyWithStatusURL(t *testing.T) {
	m := New(Options{File: &config.File{Settings: config.Settings{Server: "h:1"}}, Fetcher: &fake{}})
	m.content, m.menu, m.title = true, paneBoards, "NPWR00001_00"
	m = drive(t, m, keys("tab")...)
	if m.menu != paneRooms {
		t.Fatalf("without a status URL tab goes back to Rooms, got %v", m.menu)
	}
	mustNotShow(t, m, "Server ")
	m.set.StatusURL = "http://127.0.0.1:1"
	m = drive(t, m, keys("tab", "tab")...)
	if m.menu != paneServer {
		t.Fatalf("with a status URL tab reaches Server, got %v", m.menu)
	}
	mustShow(t, m, "[Server]", "tab rooms")
	m = drive(t, m, keys("tab")...)
	if m.menu != paneRooms {
		t.Fatalf("tab leaves Server: %v", m.menu)
	}
	m = drive(t, m, keys("tab", "tab", "esc")...)
	if m.menu != paneRooms || m.content {
		t.Fatalf("esc from Server returns to the menu on Status: %v %v", m.menu, m.content)
	}
}
