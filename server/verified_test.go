package main

import (
	"encoding/json"
	"strings"
	"testing"
)

// A player counts as verified once the server has accepted a UDP probe from it (right token, source address
// matching the TCP connection). A wrong token and a player that never probes are not counted.
func TestVerifiedPlayerCount(t *testing.T) {
	s, addr := startTCPUDP(t)
	a, b := join(t, addr, "alice"), join(t, addr, "bob")
	meet(t, a, b)

	st := s.snapshot()
	if st.VerifiedPlayers != 0 || len(st.Rooms) != 1 || st.Rooms[0].Verified != 0 {
		t.Fatalf("before any probe: players %d, rooms %+v", st.VerifiedPlayers, st.Rooms)
	}
	var sb strings.Builder
	if err := page.Execute(&sb, st); err != nil || strings.Contains(roomsTable(sb.String()), " verified)") {
		t.Errorf("page shows a verified count with none: err %v", err)
	}

	if _, reply := probe(t, addr, b.id, b.token+1, [4]byte{}); reply != nil {
		t.Fatal("a wrong token was answered")
	}
	if _, reply := probe(t, addr, a.id, a.token, [4]byte{}); reply == nil {
		t.Fatal("a right token was not answered")
	}

	st = s.snapshot()
	if st.VerifiedPlayers != 1 || st.Rooms[0].Verified != 1 || len(st.Rooms[0].Members) != 2 {
		t.Fatalf("after one probe: players %d, rooms %+v", st.VerifiedPlayers, st.Rooms)
	}
	j, _ := json.Marshal(st)
	if !strings.Contains(string(j), `"verified_players":1`) || !strings.Contains(string(j), `"verified":1`) {
		t.Errorf("/api/stats lacks the counts: %s", j)
	}
	sb.Reset()
	if err := page.Execute(&sb, st); err != nil || !strings.Contains(roomsTable(sb.String()), "2/4 (1 verified)") {
		t.Errorf("page lacks the verified count: err %v", err)
	}
	if strings.Contains(string(j), "alice") && !strings.Contains(string(j), `"members":["alice","bob"]`) {
		t.Errorf("members changed shape: %s", j)
	}
}

// roomsTable is the server-rendered rows of the rooms table; the page's script text, which also contains the
// words, is left out.
func roomsTable(html string) string {
	start := strings.Index(html, `id="rooms-body"`)
	end := strings.Index(html[start:], "</tbody>")
	if start < 0 || end < 0 {
		return ""
	}
	return html[start : start+end]
}
