package main

import (
	"context"
	"net"
	"regexp"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// A client that has not signed in must not be able to fill the host's disk with log lines: what it can repeat at will is one
// line a second, with a count of the lines left out, while the counters still count every event. A signed-in player's lines
// are never dropped.

// floodLogBound is far above what a few limited lines and their summaries weigh (about 150 bytes each) and far below one line per event.
const floodLogBound = 4000

// quiet is a server setup with no connection limits (every test client is 127.0.0.1) and an info-level text log into log.
func quiet(log *syncBuf) func(*Server) {
	return func(s *Server) {
		s.maxPerAddr, s.maxConns = 0, 0
		s.log = newLogger(log, levelFor(false), "text")
	}
}

// abruptClose resets the connection instead of closing it, which the server reads as a failed read.
func abruptClose(c *tc) {
	if tcp, ok := c.conn.(*net.TCPConn); ok {
		tcp.SetLinger(0)
	}
	c.conn.Close()
}

// badAuthFlood opens n connections, each saying HELLO and then AUTH with a bad pass until the server closes it.
func badAuthFlood(t *testing.T, addr string, n int) {
	t.Helper()
	for i := 0; i < n; i++ {
		c := dialRaw(t, addr)
		helloAck(t, c, "x"+strconv.Itoa(i))
		for j := 0; j < maxAuthFails; j++ { // the third refusal closes the connection, perhaps before its reply is read
			if _, _, err := c.callErr(msgAuth, authBody(passVersion, "v1."+strings.Repeat("A", 40)+".bad")); err != nil {
				break
			}
		}
		c.conn.Close()
	}
}

// waitConns waits until the server has admitted n connections in all. A client that resets a connection before the server has
// admitted it can leave it uncounted for good (Windows drops a reset connection from the accept queue), so a test that resets
// connections waits here for each one first.
func waitConns(t *testing.T, s *Server, n int) {
	t.Helper()
	var got int
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); time.Sleep(5 * time.Millisecond) {
		s.mu.Lock()
		got = int(s.conns)
		s.mu.Unlock()
		if got == n {
			return
		}
	}
	t.Fatalf("connections counted = %d, want %d", got, n)
}

var logFieldRE = regexp.MustCompile(`(\w+)=("(?:[^"\\]|\\.)*"|\S*)`)

// logRecords splits a text log into its lines' key=value fields.
func logRecords(out string) []map[string]string {
	var recs []map[string]string
	for _, line := range strings.Split(out, "\n") {
		rec := map[string]string{}
		for _, m := range logFieldRE.FindAllStringSubmatch(line, -1) {
			v := m[2]
			if u, err := strconv.Unquote(v); err == nil {
				v = u
			}
			rec[m[1]] = v
		}
		if len(rec) > 0 {
			recs = append(recs, rec)
		}
	}
	return recs
}

// limitedAccount counts what the log says about one kind of limited line (an empty reason matches any): the lines written for
// it, and the lines it says were left out, whether on a later line of that kind or in a "log lines left out" line.
func limitedAccount(out, msg, reason string) (lines, skipped int) {
	for _, rec := range logRecords(out) {
		kind, summary := rec["msg"], rec["msg"] == "log lines left out"
		if summary {
			kind = rec["line"]
		}
		if kind != msg || (reason != "" && rec["reason"] != reason) {
			continue
		}
		if !summary {
			lines++
		}
		n, _ := strconv.Atoi(rec["skipped_since_last_line"])
		skipped += n
	}
	return
}

// waitLimited waits until the lines written and the lines said to be left out of one kind add up to the events that happened
// (the last count can be one second behind), and returns the lines written.
func waitLimited(t *testing.T, log *syncBuf, msg, reason string, events int) int {
	t.Helper()
	var lines, skipped int
	for deadline := time.Now().Add(6 * time.Second); time.Now().Before(deadline); time.Sleep(10 * time.Millisecond) {
		if lines, skipped = limitedAccount(log.String(), msg, reason); lines+skipped == events {
			return lines
		}
	}
	t.Fatalf("%q %q: %d lines and %d left out, want %d events in all:\n%s", msg, reason, lines, skipped, events, log.String())
	return 0
}

func TestLogFloodFromUnsignedClients(t *testing.T) {
	const n = 150
	p := newPassTool(t)
	t.Run("bad AUTH", func(t *testing.T) {
		var log syncBuf
		s, addr := startConnectServer(t, p, false, quiet(&log))
		start := time.Now()
		badAuthFlood(t, addr, n)
		waitConns(t, s, n)
		waitCounters(t, s)
		if got := len(log.String()); got > floodLogBound || time.Since(start) > 3*time.Second {
			t.Fatalf("%d connections with bad passes wrote %d bytes in %v (bound %d):\n%s", n, got, time.Since(start), floodLogBound, log.String())
		}
		if !strings.Contains(log.String(), "pass refused") {
			t.Fatalf("not even one refusal line:\n%s", log.String())
		}
	})
	t.Run("abrupt close before sign-in", func(t *testing.T) {
		var log syncBuf
		s, addr := startConnectServer(t, p, false, quiet(&log))
		for i := 0; i < n; i++ {
			c := dialRaw(t, addr)
			waitConns(t, s, i+1)       // a connection reset while it still waits in the accept queue may never be admitted, on Windows
			c.conn.Write([]byte{0x05}) // part of a frame, then a reset
			abruptClose(c)
		}
		waitConns(t, s, n)
		waitCounters(t, s)
		if got := len(log.String()); got != 0 {
			t.Fatalf("%d reset connections wrote %d bytes at info level:\n%s", n, got, log.String())
		}
	})
	t.Run("name taken", func(t *testing.T) {
		var log syncBuf
		s, addr := startTCPUDPWith(t, func(s *Server) { quiet(&log)(s); proxied(s) })
		owner := dialProxy(t, addr, [4]byte{192, 0, 2, 1})
		helloAck(t, owner, "OWNER")
		for i := 0; i < n; i++ { // another address cannot take the name
			c := dialProxy(t, addr, [4]byte{192, 0, 2, 2})
			if rt, r := hello(c, "NPWR00001_00", "OWNER"); rt != msgError || r.u32() != errNameTaken {
				t.Fatal("HELLO with a taken name was not refused")
			}
			abruptClose(c)
		}
		waitConns(t, s, n+1)
		waitUnauthOrConns(t, s)
		if got := len(log.String()); got > floodLogBound {
			t.Fatalf("%d refused HELLOs wrote %d bytes (bound %d):\n%s", n, got, floodLogBound, log.String())
		}
		if !strings.Contains(log.String(), "hello refused") {
			t.Fatalf("not even one refusal line:\n%s", log.String())
		}
	})
	t.Run("per address", func(t *testing.T) {
		var log syncBuf
		s, addr := startConnectServer(t, p, false, func(s *Server) {
			s.maxPerAddr = 1
			s.log = newLogger(&log, levelFor(false), "text")
		})
		held := dialRaw(t, addr)
		helloAck(t, held, "held")
		for i := 0; i < n; i++ {
			unauthClosedWithin(t, dialRaw(t, addr), 3*time.Second)
		}
		if got := len(log.String()); got > floodLogBound {
			t.Fatalf("%d refused connections wrote %d bytes (bound %d):\n%s", n, got, floodLogBound, log.String())
		}
		if got := atomic.LoadUint64(&s.refusedConns); got != n {
			t.Fatalf("per-address refusals counted = %d, want %d", got, n)
		}
	})
}

// waitUnauthOrConns waits for the server to drop every connection that was reset.
func waitUnauthOrConns(t *testing.T, s *Server) {
	t.Helper()
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); time.Sleep(5 * time.Millisecond) {
		s.mu.Lock()
		open := s.openConns
		s.mu.Unlock()
		if open <= 1 {
			return
		}
	}
	t.Fatal("connections still open")
}

// Each kind of limited line is limited on its own: 20 events of one kind are one line (two if the second changes) and a count of
// the rest, and what the lines and counts add up to is the number of events. A kind that is not limited writes 20 lines, and a
// count that is not reset adds up to more.
func TestLogLimitPerKind(t *testing.T) {
	const events = 20
	p := newPassTool(t)
	badPass := "v1." + strings.Repeat("A", 40) + ".bad"
	closedAll := func(t *testing.T, cs []*tc) {
		for _, c := range cs {
			unauthClosedWithin(t, c, 5*time.Second)
		}
	}
	cases := []struct {
		name, msg, reason string
		setup             func(log *syncBuf) func(*Server)
		plain             bool // a server without connect mode
		run               func(t *testing.T, s *Server, addr string)
	}{
		{name: "pass refused", msg: "pass refused", run: func(t *testing.T, s *Server, addr string) {
			for i := 0; i < events; i++ {
				c := dialRaw(t, addr)
				helloAck(t, c, "x"+strconv.Itoa(i))
				c.callErr(msgAuth, authBody(passVersion, badPass))
				c.conn.Close()
			}
		}},
		{name: "too many refused passes", msg: "connection closed", reason: "too many refused passes", run: func(t *testing.T, s *Server, addr string) {
			badAuthFlood(t, addr, events)
		}},
		{name: "name taken at AUTH", msg: "hello refused", reason: "name taken", setup: func(log *syncBuf) func(*Server) {
			return func(s *Server) { quiet(log)(s); proxied(s) }
		}, run: func(t *testing.T, s *Server, addr string) {
			owner := dialProxy(t, addr, [4]byte{192, 0, 2, 1})
			helloAck(t, owner, "OWNER")
			if rt, _ := owner.call(msgAuth, authBody(passVersion, p.pass(connectClaims(time.Now(), "github:1")))); rt != msgOK {
				t.Fatal("the owner's AUTH was refused")
			}
			for i := 0; i < events; i++ {
				c := dialProxy(t, addr, [4]byte{192, 0, 2, 2})
				helloAck(t, c, "OWNER")
				if rt, r := c.call(msgAuth, authBody(passVersion, p.pass(connectClaims(time.Now(), "github:2")))); rt != msgError || r.u32() != errNameTaken {
					t.Fatal("AUTH with a taken name was not refused")
				}
				c.conn.Close()
			}
		}},
		{name: "name taken at HELLO", msg: "hello refused", reason: "name taken", plain: true, setup: func(log *syncBuf) func(*Server) {
			return func(s *Server) { quiet(log)(s); proxied(s) }
		}, run: func(t *testing.T, s *Server, addr string) {
			helloAck(t, dialProxy(t, addr, [4]byte{192, 0, 2, 1}), "OWNER")
			for i := 0; i < events; i++ {
				c := dialProxy(t, addr, [4]byte{192, 0, 2, 2})
				if rt, r := hello(c, "NPWR00001_00", "OWNER"); rt != msgError || r.u32() != errNameTaken {
					t.Fatal("HELLO with a taken name was not refused")
				}
				c.conn.Close()
			}
		}},
		{name: "no sign-in in time", msg: "connection closed", reason: "no sign-in in time", setup: func(log *syncBuf) func(*Server) {
			return func(s *Server) { quiet(log)(s); s.authTimeout = 200 * time.Millisecond }
		}, run: func(t *testing.T, s *Server, addr string) {
			var cs []*tc
			for i := 0; i < events; i++ {
				cs = append(cs, dialRaw(t, addr))
			}
			closedAll(t, cs)
		}},
		{name: "frame too long", msg: "connection closed", reason: "frame too long before sign-in", run: func(t *testing.T, s *Server, addr string) {
			var cs []*tc
			for i := 0; i < events; i++ {
				c := dialRaw(t, addr)
				c.conn.Write([]byte{msgAuth, byte((maxUnauthFrame + 1) >> 8), byte((maxUnauthFrame + 1) & 0xff)})
				cs = append(cs, c)
			}
			closedAll(t, cs)
		}},
		{name: "request rate before sign-in", msg: "connection closed", reason: "request rate", setup: func(log *syncBuf) func(*Server) {
			return func(s *Server) { quiet(log)(s); s.maxReqRate = 3 }
		}, run: func(t *testing.T, s *Server, addr string) {
			var cs []*tc
			for i := 0; i < events; i++ {
				c := dialRaw(t, addr)
				for j := 0; j < 8; j++ {
					writePacket(c.conn, msgGetCaps, []byte{0, 0, 0, byte(j + 1)})
				}
				cs = append(cs, c)
			}
			closedAll(t, cs)
		}},
		{name: "per address", msg: "connection refused", reason: "too many connections", setup: func(log *syncBuf) func(*Server) {
			return func(s *Server) { quiet(log)(s); s.maxPerAddr = 1 }
		}, run: func(t *testing.T, s *Server, addr string) {
			helloAck(t, dialRaw(t, addr), "held")
			for i := 0; i < events; i++ {
				unauthClosedWithin(t, dialRaw(t, addr), 5*time.Second)
			}
		}},
		{name: "in total", msg: "connection refused", reason: "too many connections in total", setup: func(log *syncBuf) func(*Server) {
			return func(s *Server) { quiet(log)(s); s.maxConns = 1 }
		}, run: func(t *testing.T, s *Server, addr string) {
			helloAck(t, dialRaw(t, addr), "held")
			for i := 0; i < events; i++ {
				unauthClosedWithin(t, dialRaw(t, addr), 5*time.Second)
			}
		}},
		{name: "not signed in", msg: "connection refused", reason: "too many connections not signed in", setup: func(log *syncBuf) func(*Server) {
			return func(s *Server) { quiet(log)(s); s.maxUnauthPerAddr = 1 }
		}, run: func(t *testing.T, s *Server, addr string) {
			helloAck(t, dialRaw(t, addr), "held")
			for i := 0; i < events; i++ {
				unauthClosedWithin(t, dialRaw(t, addr), 5*time.Second)
			}
		}},
		{name: "no PROXY header", msg: "connection refused", reason: "no valid PROXY header from a trusted proxy", plain: true, setup: func(log *syncBuf) func(*Server) {
			return func(s *Server) { quiet(log)(s); proxied(s) }
		}, run: func(t *testing.T, s *Server, addr string) {
			var cs []*tc
			for i := 0; i < events; i++ {
				c := dialRaw(t, addr)
				c.conn.Write([]byte("GET / HTTP/1.0\r\n\r\n\r\n\r\n\r\n\r\n\r\n\r\n\r\n\r\n\r\n\r\n\r\n\r\n\r\n\r\n\r\n\r\n"))
				cs = append(cs, c)
			}
			closedAll(t, cs)
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var log syncBuf
			setup := quiet(&log)
			if tc.setup != nil {
				setup = tc.setup(&log)
			}
			var s *Server
			var addr string
			if tc.plain {
				s, addr = startTCPUDPWith(t, setup)
			} else {
				s, addr = startConnectServer(t, p, false, setup)
			}
			tc.run(t, s, addr)
			if lines := waitLimited(t, &log, tc.msg, tc.reason, events); lines < 1 || lines > 2 {
				t.Fatalf("%d events wrote %d lines, want 1 or 2:\n%s", events, lines, log.String())
			}
		})
	}
}

// What is left out is reported even when nothing of that kind follows: a second after the last line, and at shutdown.
func TestLogLimitTrailingSummary(t *testing.T) {
	const events = 20
	p := newPassTool(t)
	var log syncBuf
	s, addr := startConnectServer(t, p, false, func(s *Server) { quiet(&log)(s); s.maxPerAddr = 1 })
	helloAck(t, dialRaw(t, addr), "held")
	for i := 0; i < events; i++ {
		unauthClosedWithin(t, dialRaw(t, addr), 5*time.Second)
	}
	// the burst wrote one line; the rest is reported by the timer
	waitLimited(t, &log, "connection refused", "too many connections", events)
	if !strings.Contains(log.String(), "log lines left out") {
		t.Fatalf("no summary line:\n%s", log.String())
	}
	// again, and shut down before the timer fires: Shutdown writes the count
	time.Sleep(1100 * time.Millisecond)
	for i := 0; i < events; i++ {
		unauthClosedWithin(t, dialRaw(t, addr), 5*time.Second)
	}
	before := len(log.String())
	s.Shutdown(context.Background())
	if lines, skipped := limitedAccount(log.String(), "connection refused", "too many connections"); lines+skipped != 2*events {
		t.Fatalf("after Shutdown %d lines and %d left out, want %d events (log grew by %d bytes):\n%s", lines, skipped, 2*events, len(log.String())-before, log.String())
	}
}

// Players keep every line, even while the limiter is dropping an unsigned flood.
func TestLogSignedInPlayersStillLogged(t *testing.T) {
	p := newPassTool(t)
	var log syncBuf
	_, addr := startConnectServer(t, p, false, quiet(&log))
	badAuthFlood(t, addr, 30) // the limiter is now busy
	alice := signIn(t, addr, p, "alice", "github:1")
	bob := signIn(t, addr, p, "bob", "github:2")
	alice.c.conn.Close() // a clean close: disconnected
	abruptClose(bob.c)   // a reset: read failed, then disconnected
	want := []string{"msg=hello ", "online_id=alice", "online_id=bob", "msg=\"read failed\"", "msg=disconnected client=" + strconv.Itoa(int(alice.id)) + " online_id=alice", "msg=disconnected client=" + strconv.Itoa(int(bob.id)) + " online_id=bob"}
	var out string
	for deadline := time.Now().Add(3 * time.Second); time.Now().Before(deadline); time.Sleep(10 * time.Millisecond) {
		out = log.String()
		ok := true
		for _, w := range want {
			ok = ok && strings.Contains(out, w)
		}
		if ok {
			return
		}
	}
	t.Fatalf("a player's line is missing (want each of %q):\n%s", want, out)
}

// Without connect mode too: a HELLO'd player's disconnect and failed read are logged at info.
func TestLogPlainPlayerDisconnectLogged(t *testing.T) {
	var log syncBuf
	_, addr := startTCPUDPWith(t, quiet(&log))
	c := dial(t, addr, "NPWR00001_00", "CAROL", 3658)
	abruptClose(c)
	for deadline := time.Now().Add(3 * time.Second); time.Now().Before(deadline); time.Sleep(10 * time.Millisecond) {
		out := log.String()
		if strings.Contains(out, "msg=\"read failed\"") && strings.Contains(out, "msg=disconnected") && strings.Contains(out, "online_id=CAROL") {
			return
		}
	}
	t.Fatalf("the player's lines are missing:\n%s", log.String())
}

// callErr sends a request and reads one packet, returning the error instead of failing the test.
func (c *tc) callErr(t byte, body []byte) (byte, []byte, error) {
	c.req++
	w := &writer{}
	w.u32(c.req)
	w.bytes(body)
	if err := writePacket(c.conn, t, w.b); err != nil {
		return 0, nil, err
	}
	c.conn.SetReadDeadline(time.Now().Add(3 * time.Second))
	return readPacket(c.conn)
}

// A signed-in player's closes are never limited: every one is a line with the player's name, however many there are.
func TestLogSignedInPlayerClosesNeverLimited(t *testing.T) {
	const players = 5
	p := newPassTool(t)
	var log syncBuf
	_, addr := startConnectServer(t, p, false, func(s *Server) { quiet(&log)(s); s.maxReqRate = 20 })
	flood := func(c *tc) {
		for j := 0; j < 40; j++ {
			writePacket(c.conn, msgGetCaps, []byte{0, 0, 0, byte(j + 1)})
		}
	}
	for i := 0; i < players; i++ {
		pl := signIn(t, addr, p, "rate"+strconv.Itoa(i), "github:"+strconv.Itoa(i+1))
		flood(pl.c)
	}
	var got int
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); time.Sleep(10 * time.Millisecond) {
		got = 0
		for _, rec := range logRecords(log.String()) {
			if rec["msg"] == "connection closed" && rec["reason"] == "request rate" && strings.HasPrefix(rec["online_id"], "rate") {
				got++
			}
		}
		if got == players {
			break
		}
	}
	if got != players {
		t.Fatalf("%d of %d players' request-rate closes were logged:\n%s", got, players, log.String())
	}
	for _, rec := range logRecords(log.String()) {
		if _, limited := rec["skipped_since_last_line"]; limited {
			t.Fatalf("a signed-in close went through the limiter:\n%s", log.String())
		}
	}
}

// A signed-in player whose pass runs out is logged every time too.
func TestLogSignedInPlayerExpiryNeverLimited(t *testing.T) {
	const players = 3
	p := newPassTool(t)
	var log syncBuf
	_, addr := startConnectServer(t, p, false, quiet(&log))
	now := time.Now()
	var cs []*tc
	for i := 0; i < players; i++ {
		short := connectClaims(now, "github:"+strconv.Itoa(i+1))
		short["iat"], short["exp"] = now.Unix()-100, now.Unix()-58 // 2 s left of the skew
		c := dialRaw(t, addr)
		helloAck(t, c, "exp"+strconv.Itoa(i))
		if rt, _ := c.call(msgAuth, authBody(passVersion, p.pass(short))); rt != msgOK {
			t.Fatal("a pass inside the skew was refused")
		}
		cs = append(cs, c)
	}
	for _, c := range cs {
		unauthClosedWithin(t, c, 8*time.Second)
	}
	got := 0
	for _, rec := range logRecords(log.String()) {
		if rec["msg"] == "connection closed" && rec["reason"] == "pass expired" && strings.HasPrefix(rec["online_id"], "exp") {
			got++
		}
	}
	if got != players {
		t.Fatalf("%d of %d expiries were logged with the player's name:\n%s", got, players, log.String())
	}
}

// A player who said HELLO with an empty online ID is still a player: info lines. A connection that said HELLO in connect mode and
// never signed in is not: debug.
func TestLogRegisteredMeansPlayer(t *testing.T) {
	var log syncBuf
	_, addr := startTCPUDPWith(t, quiet(&log))
	c := dialRaw(t, addr)
	if rt, _ := hello(c, "NPWR00001_00", ""); rt != msgHelloAck {
		t.Fatal("HELLO with an empty online ID was refused")
	}
	abruptClose(c)
	for deadline := time.Now().Add(3 * time.Second); time.Now().Before(deadline); time.Sleep(10 * time.Millisecond) {
		out := log.String()
		if strings.Contains(out, "msg=\"read failed\"") && strings.Contains(out, "msg=disconnected") {
			goto connect
		}
	}
	t.Fatalf("the player's lines are missing:\n%s", log.String())
connect:
	p := newPassTool(t)
	var clog syncBuf
	s, caddr := startConnectServer(t, p, false, quiet(&clog))
	d := dialRaw(t, caddr)
	helloAck(t, d, "never")
	abruptClose(d)
	waitConns(t, s, 1)
	waitCounters(t, s)
	time.Sleep(100 * time.Millisecond)
	if clog.String() != "" {
		t.Fatalf("a connection that never signed in wrote info lines:\n%s", clog.String())
	}
}
