package webui

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"syscall"
	"testing"
	"time"

	"github.com/undecuple/re-psn/app/internal/client"
	"github.com/undecuple/re-psn/app/internal/tui"
)

// replyFetch answers every check with one fixed snapshot or error and records the queries.
type replyFetch struct {
	snap    tui.Snapshot
	err     error
	queries []tui.Query
}

func (r *replyFetch) Fetch(ctx context.Context, q tui.Query) (tui.Snapshot, error) {
	r.queries = append(r.queries, q)
	return r.snap, r.err
}

func playCheck(t *testing.T, f fixture, title string) Play {
	t.Helper()
	var st State
	if err := json.Unmarshal(callUI(t, f, f.token, "/api/play", map[string]any{"title": title, "board": 0, "boards": false}, 200), &st); err != nil {
		t.Fatal(err)
	}
	return st.Play
}

func TestUIPlayFillsUnlistedOnlyFromAStatusPage(t *testing.T) {
	f := serveUI(t)
	status := &replyFetch{snap: tui.Snapshot{HTTP: &tui.HTTPStats{Unlisted: 3, Rooms: []tui.HTTPRoom{{ID: 7, Title: "SYNTH", Members: []string{"a"}, Max: 4}}}}}
	f.server.fetch = status
	saveRole(t, f, "play", map[string]any{"server": "play.example.org:36100", "status_url": "https://status.example.org/status"})
	p := playCheck(t, f, "")
	if p.Status != "reachable" || p.Source != "status_page" || p.Unlisted != 3 || len(p.Rooms) != 1 || !p.UsedStatusURL {
		t.Fatalf("status page check: %+v", p)
	}
	if p.Address != "play.example.org:36100" || p.Titled || p.StatusFallback {
		t.Fatalf("status page check bookkeeping: %+v", p)
	}

	// A protocol-only reply has no unlisted count: it stays zero and the source says why.
	proto := &replyFetch{snap: tui.Snapshot{RTT: 2 * time.Millisecond}}
	f.server.fetch = proto
	saveRole(t, f, "play", map[string]any{"server": "play.example.org:36100"})
	p = playCheck(t, f, "")
	if p.Status != "reachable" || p.Source != "game_connection" || p.Unlisted != 0 || len(p.Rooms) != 0 || p.Titled || p.UsedStatusURL {
		t.Fatalf("protocol-only check: %+v", p)
	}

	// With a title the protocol reply carries rooms, still without an unlisted count.
	proto.snap = tui.Snapshot{Rooms: client.RoomList{Rooms: []client.RoomInfo{{ID: 1, Owner: "owner", Cur: 1, Max: 4}}}}
	p = playCheck(t, f, "SYNTH")
	if !p.Titled || len(p.Rooms) != 1 || p.Unlisted != 0 || p.Source != "game_connection" {
		t.Fatalf("titled protocol check: %+v", p)
	}

	// A status URL that failed and fell back to the game connection says so.
	proto.snap = tui.Snapshot{HTTPUnavailable: true}
	saveRole(t, f, "play", map[string]any{"server": "play.example.org:36100", "status_url": "https://status.example.org/status"})
	p = playCheck(t, f, "")
	if !p.StatusFallback || !p.UsedStatusURL || p.Source != "game_connection" || p.Unlisted != 0 {
		t.Fatalf("fallback check: %+v", p)
	}
}

func TestUIPlayFailureKinds(t *testing.T) {
	f := serveUI(t)
	rf := &replyFetch{}
	f.server.fetch = rf
	for _, c := range []struct {
		name   string
		server string
		err    error
		want   string
	}{
		{"refused", "play.example.org:36100", fmt.Errorf("dial: %w", &net.OpError{Op: "dial", Err: syscall.ECONNREFUSED}), "refused"},
		{"deadline", "play.example.org:36100", context.DeadlineExceeded, "timeout"},
		{"net timeout", "play.example.org:36100", &net.OpError{Op: "read", Err: os.ErrDeadlineExceeded}, "timeout"},
		{"other", "play.example.org:36100", errors.New("bad hello"), "other"},
		{"status only", "", errors.New("down"), "status"},
	} {
		t.Run(c.name, func(t *testing.T) {
			rf.err = c.err
			extra := map[string]any{"server": c.server}
			if c.server == "" {
				extra["status_url"] = "https://status.example.org/status"
			}
			saveRole(t, f, "play", extra)
			p := playCheck(t, f, "")
			if p.Status != "unavailable" || p.Kind != c.want || len(p.Rooms) != 0 || p.Unlisted != 0 {
				t.Fatalf("got %+v, want kind %q", p, c.want)
			}
		})
	}
	rf.err = nil
	saveRole(t, f, "play", map[string]any{"server": "play.example.org:36100"})
	if p := playCheck(t, f, ""); p.Kind != "" {
		t.Fatalf("a reachable check has no failure kind: %+v", p)
	}
}

func TestUIPlayContactsNothingBeforeCheck(t *testing.T) {
	f := serveUI(t)
	rf := &replyFetch{snap: tui.Snapshot{HTTP: &tui.HTTPStats{Unlisted: 2}}}
	f.server.fetch = rf
	saveRole(t, f, "play", map[string]any{"server": "play.example.org:36100", "status_url": "https://status.example.org/status"})
	var st State
	if err := json.Unmarshal(callUI(t, f, f.token, "/api/state", nil, 200), &st); err != nil {
		t.Fatal(err)
	}
	if len(rf.queries) != 0 {
		t.Fatalf("saving settings and reading state contacted %d servers; want none", len(rf.queries))
	}
	if st.Play.Source != "" || st.Play.Address != "" || st.Play.Unlisted != 0 || st.Play.Kind != "" || st.Play.CheckedAt != 0 {
		t.Fatalf("play state before any check: %+v", st.Play)
	}
	playCheck(t, f, "")
	if len(rf.queries) != 1 {
		t.Fatalf("one check made %d queries; want 1", len(rf.queries))
	}
}
