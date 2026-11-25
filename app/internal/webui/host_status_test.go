package webui

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/undecuple/re-psn/app/internal/host"
	"github.com/undecuple/re-psn/app/internal/tui"
)

func u64(v uint64) *uint64 { return &v }

func TestBuildHostStatusFullAndMissingFields(t *testing.T) {
	now := time.UnixMilli(1_000_000)
	full := &tui.HTTPStats{Clients: 3, Unlisted: 1, Rooms: []tui.HTTPRoom{{ID: 1}, {ID: 2}}, Server: tui.ServerStats{
		Limits: tui.ServerLimits{MaxRooms: u64(100), MaxReqRate: u64(0)},
		TCP:    tui.TCPRefusals{ConnsPerAddr: u64(4)},
		UDP:    tui.UDPDrops{RateLimit: u64(2), Malformed: u64(5)},
	}}
	got := buildHostStatus(full, nil, 1_000_000-90_000, now)
	if *got.Clients != 3 || *got.Rooms != 2 || *got.Unlisted != 1 || got.Error != "" || got.ReadAt != 1_000_000 {
		t.Fatalf("%+v", got)
	}
	if got.Limits == nil || *got.Limits.MaxRooms != 100 || *got.Limits.MaxReqRate != 0 || got.Limits.MaxUDPRate != nil {
		t.Fatalf("limits %+v", got.Limits)
	}
	if got.Refusals == nil || *got.Refusals.TCPConnsPerAddr != 4 || got.Refusals.TCPReqRate != nil || *got.Refusals.UDPDropped != 7 {
		t.Fatalf("refusals %+v", got.Refusals)
	}
	if got.UptimeSeconds == nil || *got.UptimeSeconds != 90 {
		t.Fatalf("uptime %v", got.UptimeSeconds)
	}
	// An older host reports no limits and no counters: those say "not reported", the core counts stay.
	old := buildHostStatus(&tui.HTTPStats{Clients: 0}, nil, 0, now)
	if old.Limits != nil || old.Refusals != nil || old.UptimeSeconds != nil || old.Clients == nil || *old.Clients != 0 {
		t.Fatalf("%+v", old)
	}
	b, _ := json.Marshal(old)
	if strings.Contains(string(b), "uptime_seconds") || !strings.Contains(string(b), `"limits":null`) {
		t.Fatalf("%s", b)
	}
}

func TestHostUptime(t *testing.T) {
	now := time.UnixMilli(10_000)
	if hostUptime(0, now) != nil || hostUptime(20_000, now) != nil {
		t.Fatal("an unknown or future start must give no uptime")
	}
	if v := hostUptime(7_500, now); v == nil || *v != 2 {
		t.Fatalf("%v", v)
	}
}

func TestHostStatusErrorIsPlain(t *testing.T) {
	secret := "/home/someone/.cache/tok-SECRET123"
	got := buildHostStatus(nil, errors.New("open "+secret+": denied"), 0, time.Now())
	b, _ := json.Marshal(got)
	if got.Error == "" || got.Clients != nil || strings.Contains(string(b), "SECRET123") || strings.Contains(string(b), "/home") {
		t.Fatalf("%s", b)
	}
}

func TestHostStatusRoute(t *testing.T) {
	f := serveUI(t)
	calls := 0
	var gotOrigin, gotFile string
	f.server.stats = func(ctx context.Context, origin, file string) (*tui.HTTPStats, error) {
		calls++
		gotOrigin, gotFile = origin, file
		if dl, ok := ctx.Deadline(); !ok || time.Until(dl) > 5*time.Second {
			t.Error("the read needs a deadline of at most 5 seconds")
		}
		return &tui.HTTPStats{Clients: 2, Unlisted: 0}, nil
	}
	callUI(t, f, "", "/api/host/status", struct{}{}, 401)
	callUI(t, f, f.nonce, "/api/host/status", struct{}{}, 401)
	callUI(t, f, f.token, "/api/host/status", struct{}{}, 403) // no workspace chosen yet
	saveRole(t, f, "play", nil)
	callUI(t, f, f.token, "/api/host/status", struct{}{}, 403)
	saveRole(t, f, "host", nil)
	callUI(t, f, f.token, "/api/host/status", struct{}{}, 400) // allowed role, owned host not running
	callUI(t, f, f.token, "/api/host/status", map[string]any{"extra": 1}, 400)
	if calls != 0 {
		t.Fatalf("the reader ran %d times without a running host", calls)
	}
	f.server.hostSnapshot = func() (host.View, string, string) {
		return host.View{Running: true, Address: "127.0.0.1:36100", StartedAt: time.Now().Add(-65 * time.Second).UnixMilli()}, "http://127.0.0.1:9", "/private/token-file"
	}
	b := callUI(t, f, f.token, "/api/host/status", struct{}{}, 200)
	var s HostStatus
	if err := json.Unmarshal(b, &s); err != nil || s.Clients == nil || *s.Clients != 2 || s.UptimeSeconds == nil || *s.UptimeSeconds < 64 || s.Error != "" {
		t.Fatalf("%s %v", b, err)
	}
	if calls != 1 || gotOrigin != "http://127.0.0.1:9" || gotFile != "/private/token-file" {
		t.Fatalf("calls %d %q %q", calls, gotOrigin, gotFile)
	}
	if strings.Contains(string(b), "token-file") {
		t.Fatalf("the answer names the token file: %s", b)
	}
	// A failed read is still a 200 with a plain message that has nothing from the failure.
	f.server.stats = func(context.Context, string, string) (*tui.HTTPStats, error) {
		return nil, errors.New("Bearer abc123 refused at /private/token-file")
	}
	b = callUI(t, f, f.token, "/api/host/status", struct{}{}, 200)
	if !strings.Contains(string(b), `"error"`) || strings.Contains(string(b), "abc123") || strings.Contains(string(b), "private") {
		t.Fatalf("%s", b)
	}
	// A host that stopped between the two reads has no origin: the same 400 as not running.
	f.server.hostSnapshot = func() (host.View, string, string) { return host.View{Running: true}, "", "" }
	callUI(t, f, f.token, "/api/host/status", struct{}{}, 400)
}

func TestHostStatusDefaultsAreWired(t *testing.T) {
	f := serveUI(t)
	if f.server.stats == nil || f.server.hostSnapshot == nil {
		t.Fatal("a new server must read the real status page and the real host")
	}
	v, origin, file := f.server.hostSnapshot()
	if v.Running || origin != "" || file != "" {
		t.Fatalf("no host is running: %+v %q %q", v, origin, file)
	}
}

func TestBuildHostStatusKeepsUptimeOnFailure(t *testing.T) {
	now := time.UnixMilli(100_000)
	got := buildHostStatus(&tui.HTTPStats{}, errors.New("x"), 40_000, now)
	if got.Error == "" || got.Clients != nil || got.Rooms != nil || got.Limits != nil || got.UptimeSeconds == nil || *got.UptimeSeconds != 60 {
		t.Fatalf("%+v", got)
	}
	if nilStats := buildHostStatus(nil, nil, 0, now); nilStats.Error == "" {
		t.Fatal("a nil answer without an error is still a failed read")
	}
	// A UDP counter that is reported as zero is a reported zero, not a missing one.
	zero := buildHostStatus(&tui.HTTPStats{Server: tui.ServerStats{UDP: tui.UDPDrops{Malformed: u64(0)}}}, nil, 0, now)
	if zero.Refusals == nil || zero.Refusals.UDPDropped == nil || *zero.Refusals.UDPDropped != 0 {
		t.Fatalf("%+v", zero.Refusals)
	}
}
