package client

import (
	"encoding/json"
	"io"
	"net/http"
	"testing"
	"time"
)

func TestRTTTrackerKeepsTheSmallestOfTheLastEight(t *testing.T) {
	var r rttTracker
	if _, ok := r.min(); ok {
		t.Fatal("a sample before any call")
	}
	r.note(40 * time.Millisecond)
	r.note(15 * time.Millisecond)
	r.note(90 * time.Millisecond)
	if ms, ok := r.min(); !ok || ms != 15 {
		t.Fatalf("min = %d %v, want 15", ms, ok)
	}
	// Too large and negative values are not samples.
	r.note(maxRTT + time.Millisecond)
	r.note(-time.Millisecond)
	if ms, _ := r.min(); ms != 15 {
		t.Fatalf("a rejected sample changed the minimum: %d", ms)
	}
	// The 15 ms sample leaves the window after eight newer ones.
	for i := 0; i < rttSamples; i++ {
		r.note(70 * time.Millisecond)
	}
	if ms, _ := r.min(); ms != 70 {
		t.Fatalf("old sample still counted: %d", ms)
	}
	r.note(maxRTT)
	if ms, _ := r.min(); ms != 70 {
		t.Fatalf("min = %d", ms)
	}
}

// statsRTT returns the rtt_ms list of the one room in the server's /api/stats.
func statsRTT(t *testing.T, httpAddr string) []*int {
	t.Helper()
	resp, err := http.Get("http://" + httpAddr + "/api/stats")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	var st struct {
		Rooms []struct {
			Members []string `json:"members"`
			RTT     []*int   `json:"rtt_ms"`
		} `json:"rooms"`
	}
	if err := json.Unmarshal(b, &st); err != nil || len(st.Rooms) != 1 {
		t.Fatalf("stats: %v\n%s", err, b)
	}
	return st.Rooms[0].RTT
}

// A connection that opted in reports its round trip in HEARTBEAT; one that did not sends an empty HEARTBEAT, as
// before. Both against the real server, whose status page shows what it was told.
func TestConformanceHeartbeatReportsRTT(t *testing.T) {
	httpAddr := freePort(t)
	addr := startServer(t, "-http", httpAddr)
	ctx := testCtx(t)

	alice, _ := hello(t, addr, "alice", 3658)
	bob, _ := hello(t, addr, "bob", 3659)
	room, err := alice.CreateRoom(ctx, CreateRoom{Max: 4})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := bob.JoinRoom(ctx, room.ID, nil); err != nil {
		t.Fatal(err)
	}
	if _, ok := alice.RTT(); !ok {
		t.Fatal("no round trip timed after HELLO and CreateRoom")
	}

	alice.ReportRTT(true) // bob leaves the default
	for _, c := range []*Conn{alice, bob} {
		if err := c.Heartbeat(ctx); err != nil {
			t.Fatal(err)
		}
	}
	// A request after the heartbeats: the server has handled them by the time it answers.
	for _, c := range []*Conn{alice, bob} {
		if _, err := c.SearchRooms(ctx, 0, 10); err != nil {
			t.Fatal(err)
		}
	}
	got := statsRTT(t, httpAddr)
	if len(got) != 2 || got[0] == nil || *got[0] < 0 || *got[0] > 5000 || got[1] != nil {
		t.Fatalf("reports = %v, want a small number for alice and none for bob", got)
	}
}
