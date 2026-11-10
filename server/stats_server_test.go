package main

import (
	"encoding/json"
	"net"
	"strings"
	"testing"
	"time"
)

// /api/stats reports the version, whether the relay is on, the limits in force and the refusals so far. All of it
// is aggregate: no address and no player appears in the new fields.
func TestStatsReportServerState(t *testing.T) {
	s := NewServer(7, false)
	s.relay = true
	s.maxPerAddr = 1
	s.maxReqRate = 9
	s.maxPlayerRooms = 3
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	go s.Serve(ln)
	addr := ln.Addr().String()
	dial(t, addr, "NPWR00001_00", "alice", 3658)

	conn, err := net.Dial("tcp", addr) // a second connection from the same address is refused
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	if _, _, err := readPacket(conn); err == nil {
		t.Fatal("second connection was served")
	}

	st := s.snapshot()
	if st.Version != version || !st.Relay || st.RelayBytes != 0 {
		t.Errorf("version %q relay %v bytes %d", st.Version, st.Relay, st.RelayBytes)
	}
	if st.Limits.MaxRooms != 7 || st.Limits.MaxPlayerRooms != 3 || st.Limits.MaxConnsPerAddr != 1 || st.Limits.MaxReqRate != 9 {
		t.Errorf("limits %+v", st.Limits)
	}
	if st.TCPRefusals.ConnsPerAddr != 1 || st.TCPRefusals.ReqRate != 0 {
		t.Errorf("refusals %+v", st.TCPRefusals)
	}

	j, _ := json.Marshal(st)
	var m map[string]json.RawMessage
	if err := json.Unmarshal(j, &m); err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"version", "relay", "relay_bytes", "limits", "tcp_refusals"} {
		if _, ok := m[k]; !ok {
			t.Errorf("/api/stats lacks %q: %s", k, j)
		}
	}
	for _, k := range []string{"version", "relay", "relay_bytes", "limits", "tcp_refusals"} {
		if strings.Contains(string(m[k]), "127.0.0.1") || strings.Contains(string(m[k]), "alice") {
			t.Errorf("%q carries an address or a player: %s", k, m[k])
		}
	}
}
