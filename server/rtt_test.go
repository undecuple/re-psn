package main

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

// heartbeat sends a HEARTBEAT with the given body (it has no reply), then a request that does have one, so the
// server has handled the heartbeat before the test looks.
func heartbeat(t *testing.T, c *tc, body []byte) {
	t.Helper()
	c.req++
	w := &writer{}
	w.u32(c.req)
	w.bytes(body)
	if err := writePacket(c.conn, msgHeartbeat, w.b); err != nil {
		t.Fatal(err)
	}
	if rt, _ := c.call(msgSearchRooms, []byte{0, 0, 0, 10}); rt == msgError {
		t.Fatalf("connection refused a request after the heartbeat")
	}
}

func rttBody(ms uint16) []byte { return []byte{byte(ms >> 8), byte(ms)} }

func roomRTT(t *testing.T, s *Server) []*int {
	t.Helper()
	st := s.snapshot()
	if len(st.Rooms) != 1 {
		t.Fatalf("rooms = %d", len(st.Rooms))
	}
	return st.Rooms[0].RTT
}

func TestHeartbeatReportsRTT(t *testing.T) {
	s, addr, _ := logged(t)
	a := dial(t, addr, "NPWR00001_00", "alice", 3658)
	b := dial(t, addr, "NPWR00001_00", "bob", 3659)
	room := roomOf(t, a)
	joinID(b, room)

	// An old client sends a HEARTBEAT with no body: nothing is reported and the connection stays up.
	heartbeat(t, a, nil)
	if got := roomRTT(t, s); len(got) != 2 || got[0] != nil || got[1] != nil {
		t.Fatalf("no report yet: %v", got)
	}

	// A client that reports sends the round trip in milliseconds; bytes after the first two are ignored.
	heartbeat(t, a, rttBody(23))
	heartbeat(t, b, append(rttBody(140), 0xAA, 0xBB))
	got := roomRTT(t, s)
	if got[0] == nil || *got[0] != 23 || got[1] == nil || *got[1] != 140 {
		t.Fatalf("reported: %v", got)
	}
	j, _ := json.Marshal(s.snapshot())
	if !strings.Contains(string(j), `"rtt_ms":[23,140]`) {
		t.Errorf("/api/stats lacks the reports: %s", j)
	}
	var sb strings.Builder
	if err := page.Execute(&sb, s.snapshot()); err != nil || !strings.Contains(roomsTable(sb.String()), "alice (23 ms) bob (140 ms)") {
		t.Errorf("page lacks the reports: err %v\n%s", err, roomsTable(sb.String()))
	}

	// A body of one byte is not a report and leaves the last one alone; a later HEARTBEAT without a body does too.
	heartbeat(t, a, []byte{0x01})
	heartbeat(t, a, nil)
	if got := roomRTT(t, s); got[0] == nil || *got[0] != 23 {
		t.Errorf("a short or empty body changed the report: %v", got)
	}

	// 0xFFFF means unknown and clears the report, and so does anything above the limit.
	heartbeat(t, a, rttBody(0xFFFF))
	heartbeat(t, b, rttBody(maxReportedRTT+1))
	if got := roomRTT(t, s); got[0] != nil || got[1] != nil {
		t.Errorf("unknown or too large should clear: %v", got)
	}
	heartbeat(t, a, rttBody(maxReportedRTT))
	if got := roomRTT(t, s); got[0] == nil || *got[0] != maxReportedRTT {
		t.Errorf("the limit itself is accepted: %v", got)
	}
}

func TestReportedRTTGoesStale(t *testing.T) {
	s, addr, _ := logged(t)
	a := dial(t, addr, "NPWR00001_00", "alice", 3658)
	roomOf(t, a)
	heartbeat(t, a, rttBody(30))
	if got := roomRTT(t, s); got[0] == nil {
		t.Fatal("fresh report missing")
	}
	s.mu.Lock()
	for _, c := range s.clients {
		c.rttAt = time.Now().Add(-rttFresh - time.Second)
	}
	s.mu.Unlock()
	if got := roomRTT(t, s); got[0] != nil {
		t.Errorf("a stale report is still shown: %v", *got[0])
	}
}
