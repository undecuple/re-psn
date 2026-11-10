package main

import (
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// TestLogsHoldNoSecrets drives the server through a session at the most
// talkative log level and checks that none of the secrets it handles appears
// in the log, in either log format: each player's UDP token, a room's invite
// code (right and wrong), a peer key, and the status page token (right and
// wrong, in the query and in the Authorization header). A check that the log
// is not empty keeps the test from passing on a quiet logger.
func TestLogsHoldNoSecrets(t *testing.T) {
	for _, format := range []string{"text", "json"} {
		t.Run(format, func(t *testing.T) {
			const statusToken, wrongToken = "status-secret-token-value", "wrong-guess-token-value"
			buf := &syncBuf{}
			s, addr := startTCPUDPWith(t, func(s *Server) {
				s.relay, s.httpToken = true, statusToken
				s.log = newLogger(buf, slog.LevelDebug, format)
			})
			srv, _ := net.ResolveUDPAddr("udp", addr)
			a, b := join(t, addr, "alice"), join(t, addr, "bob")
			optIn(t, a)
			optIn(t, b)

			room := roomOf(t, a.c)
			_, code := setAccess(a.c, room, accessCode).access(t)
			wrongCode := "ZZZZZZZZ"
			if code == wrongCode {
				wrongCode = "YYYYYYYY"
			}
			joinCode(b.c, wrongCode) // a miss: logged by count, never by value
			if x := joinCode(b.c, code); x.rt != msgRoomJoined {
				t.Fatalf("join by code: 0x%02X", x.rt)
			}
			keys := peerKeys(a.c, 200*time.Millisecond)
			if len(keys) != 1 {
				t.Fatalf("peer keys: %+v", keys)
			}
			peerKeys(b.c, 200*time.Millisecond)

			ua, ub := udpSock(t), udpSock(t)
			probeFrom(t, ub, addr, b)
			ua.WriteTo(relayPkt(a, a.token, b.id, "hi bob"), srv)
			recvWithin(ub, time.Second)
			wrongUDP := a.token + 1
			ua.WriteTo(relayPkt(a, wrongUDP, b.id, "no"), srv) // refused
			probe := append([]byte(udpMagic), udpProbe)
			probe = append(probe, byte(a.id>>24), byte(a.id>>16), byte(a.id>>8), byte(a.id))
			probe = append(probe, byte(wrongUDP>>24), byte(wrongUDP>>16), byte(wrongUDP>>8), byte(wrongUDP), 127, 0, 0, 1)
			ua.WriteTo(probe, srv) // a probe with the wrong token

			for _, req := range []*http.Request{
				httptest.NewRequest("GET", "/api/stats?token="+statusToken, nil),
				httptest.NewRequest("GET", "/api/stats?token="+wrongToken, nil),
				httptest.NewRequest("GET", "/healthz", nil),
			} {
				s.HTTP().ServeHTTP(httptest.NewRecorder(), req)
			}
			bearer := httptest.NewRequest("GET", "/api/stats", nil)
			bearer.Header.Set("Authorization", "Bearer "+statusToken)
			s.HTTP().ServeHTTP(httptest.NewRecorder(), bearer)
			wrongBearer := httptest.NewRequest("GET", "/api/stats", nil)
			wrongBearer.Header.Set("Authorization", "Bearer "+wrongToken)
			s.HTTP().ServeHTTP(httptest.NewRecorder(), wrongBearer)
			time.Sleep(300 * time.Millisecond)

			out := buf.String()
			if !strings.Contains(out, "room joined") || !strings.Contains(out, "status page refused") {
				t.Fatalf("the log is missing the events this test relies on:\n%s", out)
			}
			secrets := map[string]string{
				"status token": statusToken, "wrong status token": wrongToken,
				"room code": code, "wrong room code": wrongCode,
				"peer key":          fmt.Sprintf("%x", keys[0].key),
				"alice token":       fmt.Sprint(a.token),
				"alice token (hex)": fmt.Sprintf("%08x", a.token),
				"bob token":         fmt.Sprint(b.token),
				"bob token (hex)":   fmt.Sprintf("%08x", b.token),
				"wrong UDP token":   fmt.Sprint(wrongUDP),
			}
			for name, v := range secrets {
				if strings.Contains(strings.ToLower(out), strings.ToLower(v)) {
					t.Errorf("the %s (%q) is in the %s log:\n%s", name, v, format, out)
				}
			}
		})
	}
}
