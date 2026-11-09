package main

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"os"
	"os/exec"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestUDPLimiter(t *testing.T) {
	a, b := netip.MustParseAddr("192.0.2.1"), netip.MustParseAddr("192.0.2.2")
	now := time.Unix(100, 0)
	l := udpLimiter{rate: 2}
	check := func(ip netip.Addr, at time.Time, rate, capacity bool) {
		t.Helper()
		r, c := l.allow(ip, at)
		if r != rate || c != capacity {
			t.Fatalf("allow(%v, %v) = %v/%v; want %v/%v", ip, at, r, c, rate, capacity)
		}
	}
	check(a, now, false, false)
	check(a, now, false, false)
	check(netip.MustParseAddr("::ffff:192.0.2.1"), now, true, false)
	check(b, now, false, false)
	check(a, now.Add(250*time.Millisecond), true, false)
	check(a, now.Add(500*time.Millisecond), false, false)
	check(a, now.Add(500*time.Millisecond), true, false)
	// Refill is capped at one burst, even after a long idle period.
	check(a, now.Add(10*time.Second), false, false)
	check(a, now.Add(10*time.Second), false, false)
	check(a, now.Add(10*time.Second), true, false)
	if len(l.sources) != 2 {
		t.Fatal("mapped IPv4 created another bucket")
	}
	// Off means no tracking allocation, including for an arbitrary source.
	off := udpLimiter{}
	for range 10 {
		if r, c := off.allow(a, now); r || c || off.sources != nil {
			t.Fatal("disabled limiter tracks or rejects traffic")
		}
	}
}

// TestUDPLimiterIPv6Prefix: every address in one /64 shares a bucket and a table slot; another /64 is a separate source.
func TestUDPLimiterIPv6Prefix(t *testing.T) {
	now := time.Unix(100, 0)
	l := udpLimiter{rate: 2}
	a, b := netip.MustParseAddr("2001:db8:1:2::1"), netip.MustParseAddr("2001:db8:1:2:ffff::9")
	other := netip.MustParseAddr("2001:db8:1:3::1")
	for _, ip := range []netip.Addr{a, b} {
		if r, c := l.allow(ip, now); r || c {
			t.Fatalf("allow(%v) = %v/%v; want an allowed first packet", ip, r, c)
		}
	}
	if r, c := l.allow(netip.MustParseAddr("2001:db8:1:2:1:2:3:4"), now); !r || c {
		t.Fatalf("a third address in the same /64 = %v/%v; want rate limited", r, c)
	}
	if r, c := l.allow(other, now); r || c {
		t.Fatalf("a different /64 = %v/%v; want its own allowance", r, c)
	}
	if len(l.sources) != 2 {
		t.Fatalf("%d table entries; want 2", len(l.sources))
	}
	// One /64 cannot fill the table.
	full := udpLimiter{rate: 1}
	for i := 0; i < udpSourceLimit+10; i++ {
		var raw [16]byte
		copy(raw[:8], []byte{0x20, 0x01, 0x0d, 0xb8, 0, 0, 0, 1})
		raw[14], raw[15] = byte(i>>8), byte(i)
		full.allow(netip.AddrFrom16(raw), now)
	}
	if r, c := full.allow(netip.MustParseAddr("192.0.2.1"), now); r || c || len(full.sources) != 2 {
		t.Fatalf("after a flood from one /64: %v/%v with %d entries; want a new source admitted", r, c, len(full.sources))
	}
}

func TestUDPLimiterCapacityAndExpiry(t *testing.T) {
	now := time.Unix(100, 0)
	l := udpLimiter{rate: 1}
	for i := 0; i < udpSourceLimit; i++ {
		ip := netip.AddrFrom4([4]byte{10, byte(i >> 8), byte(i), 1})
		if r, c := l.allow(ip, now); r || c {
			t.Fatalf("source %d rejected before capacity", i)
		}
	}
	newIP := netip.MustParseAddr("198.51.100.1")
	if r, c := l.allow(newIP, now); r || !c || len(l.sources) != udpSourceLimit {
		t.Fatal("full table did not fail closed")
	}
	first := netip.MustParseAddr("10.0.0.1")
	if r, c := l.allow(first, now); !r || c {
		t.Fatal("capacity pressure evicted or reset an existing sender")
	}
	if r, c := l.allow(newIP, now.Add(udpSourceTTL-time.Nanosecond)); r || !c {
		t.Fatal("entry expired early")
	}
	// Sweeps occur at most once per second; exact TTL can wait for the next sweep.
	if r, c := l.allow(newIP, now.Add(udpSourceTTL)); r || !c {
		t.Fatal("second sweep occurred inside one second")
	}
	if r, c := l.allow(newIP, now.Add(udpSourceTTL+time.Second)); r || c || len(l.sources) != 1 {
		t.Fatal("idle sources were not reclaimed")
	}
}

// These tests exercise actual packets and handlers with a frozen refill clock.
// Results therefore do not depend on processing a burst within a wall second.
func startGuardServer(t *testing.T, rate int, relay bool) (*Server, string) {
	t.Helper()
	s := NewServer(10, false)
	s.maxUDPRate, s.relay = rate, relay
	ln, pc, err := listenTestTCPUDP(randomTestPort, 100)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ln.Close()
		pc.Close()
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		if err := s.Shutdown(ctx); err != nil {
			t.Error(err)
		}
	})
	go s.Serve(ln)
	frozen := time.Now()
	go s.serveUDP(pc, func() time.Time { return frozen })
	return s, ln.Addr().String()
}

func waitUDPCount(t *testing.T, counter *atomic.Uint64, want uint64) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for counter.Load() < want && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if got := counter.Load(); got != want {
		t.Fatalf("UDP counter = %d, want %d", got, want)
	}
}

func sendGuardPacket(t *testing.T, u *net.UDPConn, addr string, packet []byte) {
	t.Helper()
	a, err := net.ResolveUDPAddr("udp", addr)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := u.WriteToUDP(packet, a); err != nil {
		t.Fatal(err)
	}
}

func TestUDPAdmissionRealPackets(t *testing.T) {
	s, addr := startGuardServer(t, 2, false)
	p := join(t, addr, "alice")
	u := udpSock(t)
	probeFrom(t, u, addr, p) // valid admitted control
	sendGuardPacket(t, u, addr, []byte("bad"))
	waitUDPCount(t, &s.udpDrops.malformed, 1)
	// Another source port shares the same source-IP budget, even for valid tokens.
	v := udpSock(t)
	packet := append([]byte(udpMagic), udpProbe)
	packet = binary.BigEndian.AppendUint32(packet, p.id)
	packet = binary.BigEndian.AppendUint32(packet, p.token)
	packet = append(packet, 127, 0, 0, 1)
	sendGuardPacket(t, v, addr, packet)
	waitUDPCount(t, &s.udpDrops.rateLimit, 1)
	if got := recvWithin(v, 50*time.Millisecond); got != nil {
		t.Fatalf("limited probe answered: %x", got)
	}
	if got := s.udpDrops.snapshot(); got != (udpDropStats{RateLimit: 1, Malformed: 1}) {
		t.Fatalf("unexpected first-reason counters: %+v", got)
	}
}

func TestUDPRelaySharesAdmission(t *testing.T) {
	s, addr := startGuardServer(t, 2, true)
	a, b := join(t, addr, "alice"), join(t, addr, "bob")
	meet(t, a, b)
	ua, ub := udpSock(t), udpSock(t)
	probeFrom(t, ub, addr, b)
	sendGuardPacket(t, ua, addr, relayPkt(a, a.token, b.id, "allowed"))
	if got := recvWithin(ub, time.Second); len(got) < 9 || string(got[9:]) != "allowed" {
		t.Fatalf("valid relay control failed: %x", got)
	}
	sendGuardPacket(t, ua, addr, relayPkt(a, a.token, b.id, "limited"))
	waitUDPCount(t, &s.udpDrops.rateLimit, 1)
	if got := recvWithin(ub, 50*time.Millisecond); got != nil {
		t.Fatalf("limited relay forwarded: %x", got)
	}
	if atomic.LoadUint64(&s.relayed) != uint64(len("allowed")) {
		t.Fatal("limited relay counted as forwarded bytes")
	}
}

func TestUDPDropReasonsAndOff(t *testing.T) {
	s, addr := startGuardServer(t, 0, false)
	p := join(t, addr, "alice")
	u := udpSock(t)
	// Short relay is malformed before relay-disabled; a full packet gets disabled.
	sendGuardPacket(t, u, addr, append([]byte(udpMagic), udpRelay))
	waitUDPCount(t, &s.udpDrops.malformed, 1)
	sendGuardPacket(t, u, addr, relayPkt(p, p.token, p.id, "disabled"))
	waitUDPCount(t, &s.udpDrops.relayDisabled, 1)
	packet := append([]byte(udpMagic), udpProbe)
	packet = binary.BigEndian.AppendUint32(packet, p.id)
	packet = binary.BigEndian.AppendUint32(packet, p.token+1)
	packet = append(packet, 127, 0, 0, 1)
	sendGuardPacket(t, u, addr, packet)
	waitUDPCount(t, &s.udpDrops.unauthorized, 1)
	// Zero disables admission only: successful authenticated probes still work.
	for range 3 {
		probeFrom(t, u, addr, p)
	}
	if got := s.udpDrops.snapshot(); got != (udpDropStats{Malformed: 1, RelayDisabled: 1, Unauthorized: 1}) {
		t.Fatalf("off bypassed protocol checks or counted successes: %+v", got)
	}
}

type udpWriteResult struct {
	n   int
	err error
}

func (p udpWriteResult) ReadFrom([]byte) (int, net.Addr, error) { return 0, nil, io.EOF }
func (p udpWriteResult) WriteTo([]byte, net.Addr) (int, error)  { return p.n, p.err }
func (p udpWriteResult) Close() error                           { return nil }
func (p udpWriteResult) LocalAddr() net.Addr                    { return &net.UDPAddr{} }
func (p udpWriteResult) SetDeadline(time.Time) error            { return nil }
func (p udpWriteResult) SetReadDeadline(time.Time) error        { return nil }
func (p udpWriteResult) SetWriteDeadline(time.Time) error       { return nil }

func TestUDPRelayDropAccounting(t *testing.T) {
	s := NewServer(10, false)
	s.relay = true
	a := &client{id: 1, token: 42, ip: [4]byte{127, 0, 0, 1}, rooms: map[uint64]*room{}}
	b := &client{id: 2, udpIP: [4]byte{127, 0, 0, 1}, udpPort: 9000, rooms: map[uint64]*room{}}
	r := &room{}
	a.rooms[1], b.rooms[1] = r, r
	s.clients[1], s.clients[2] = a, b
	pkt := relayPkt(player{id: 1}, 42, 2, "data")
	sender := &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)}
	s.relayUDP(udpWriteResult{n: 13}, pkt, sender)
	if atomic.LoadUint64(&s.relayed) != 4 {
		t.Fatal("successful send not counted")
	}
	for _, pc := range []udpWriteResult{{n: 12}, {err: errors.New("send failed")}} {
		s.relayUDP(pc, pkt, sender)
	}
	if s.udpDrops.writeError.Load() != 2 || atomic.LoadUint64(&s.relayed) != 4 {
		t.Fatal("failed or short send counted bytes")
	}
	s.relayUDP(udpWriteResult{}, relayPkt(player{id: 1}, 41, 2, "bad"), sender)
	delete(b.rooms, 1)
	s.relayUDP(udpWriteResult{}, pkt, sender)
	b.rooms[1] = r
	s.relayUDP(udpWriteResult{}, pkt, &net.UDPAddr{IP: net.IPv4(127, 0, 0, 2)})
	if got := s.udpDrops.snapshot(); got != (udpDropStats{Unauthorized: 2, SourceMismatch: 1, WriteError: 2}) {
		t.Fatalf("relay drop accounting %+v", got)
	}
}

type udpGuardPacketConn struct {
	udpWriteResult
	packets int
	packet  []byte
	address func(int) *net.UDPAddr
}

// RELAY has always allowed an IPv6 source when the host opts out of source
// matching. Its target endpoint still comes from an IPv4 probe. Exercise the
// full UDP dispatch so admission/parse changes cannot silently remove this.
func TestUDPRelayIPv6SourceOptOut(t *testing.T) {
	for _, allow := range []bool{false, true} {
		t.Run(map[bool]string{false: "source check", true: "opt out"}[allow], func(t *testing.T) {
			s := NewServer(10, false)
			s.relay, s.udpAnySource = true, allow
			a := &client{id: 1, token: 42, rooms: map[uint64]*room{}}
			b := &client{id: 2, udpIP: [4]byte{127, 0, 0, 1}, udpPort: 9000, rooms: map[uint64]*room{}}
			r := &room{}
			a.rooms[1], b.rooms[1] = r, r
			s.clients[1], s.clients[2] = a, b
			pc := &udpGuardPacketConn{udpWriteResult: udpWriteResult{n: 13}, packets: 1,
				packet:  relayPkt(player{id: 1}, 42, 2, "data"),
				address: func(int) *net.UDPAddr { return &net.UDPAddr{IP: net.ParseIP("::1")} }}
			if err := s.ServeUDP(pc); !errors.Is(err, io.EOF) {
				t.Fatal(err)
			}
			want := udpDropStats{SourceMismatch: 1}
			var bytes uint64
			if allow {
				want, bytes = udpDropStats{}, 4
			}
			if got := s.udpDrops.snapshot(); got != want || atomic.LoadUint64(&s.relayed) != bytes {
				t.Fatalf("IPv6 relay opt-out %v: drops %+v, bytes %d", allow, got, atomic.LoadUint64(&s.relayed))
			}
		})
	}
}

func (p *udpGuardPacketConn) ReadFrom(buf []byte) (int, net.Addr, error) {
	if p.packets == 0 {
		return 0, nil, io.EOF
	}
	p.packets--
	return copy(buf, p.packet), p.address(p.packets), nil
}

func TestUDPProbeWriteErrorsAndSourceMismatch(t *testing.T) {
	for _, tc := range []struct {
		name string
		ip   [4]byte
		send udpWriteResult
		want udpDropStats
	}{
		{"write failure", [4]byte{127, 0, 0, 1}, udpWriteResult{err: io.ErrClosedPipe}, udpDropStats{WriteError: 1}},
		{"short write", [4]byte{127, 0, 0, 1}, udpWriteResult{n: 10}, udpDropStats{WriteError: 1}},
		{"source mismatch", [4]byte{127, 0, 0, 2}, udpWriteResult{n: 11}, udpDropStats{SourceMismatch: 1}},
		{"success", [4]byte{127, 0, 0, 1}, udpWriteResult{n: 11}, udpDropStats{}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := NewServer(10, false)
			s.clients[1] = &client{id: 1, token: 42, ip: tc.ip, commID: "NPWR00001_00"}
			pkt := append([]byte(udpMagic), udpProbe)
			pkt = binary.BigEndian.AppendUint32(pkt, 1)
			pkt = binary.BigEndian.AppendUint32(pkt, 42)
			pkt = append(pkt, 127, 0, 0, 1)
			pc := &udpGuardPacketConn{udpWriteResult: tc.send, packets: 1, packet: pkt,
				address: func(int) *net.UDPAddr { return &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 1000} }}
			if err := s.ServeUDP(pc); !errors.Is(err, io.EOF) {
				t.Fatal(err)
			}
			if got := s.udpDrops.snapshot(); got != tc.want {
				t.Fatalf("drops %+v, want %+v", got, tc.want)
			}
		})
	}
}

func TestUDPSourceCapacityAccounting(t *testing.T) {
	s := NewServer(10, false)
	pc := &udpGuardPacketConn{packets: udpSourceLimit + 1, packet: []byte("bad"),
		address: func(i int) *net.UDPAddr { return &net.UDPAddr{IP: net.IPv4(10, byte(i>>8), byte(i), 1)} }}
	frozen := time.Now()
	if err := s.serveUDP(pc, func() time.Time { return frozen }); !errors.Is(err, io.EOF) {
		t.Fatal(err)
	}
	if got := s.udpDrops.snapshot(); got != (udpDropStats{Malformed: udpSourceLimit, SourceCapacity: 1}) {
		t.Fatalf("capacity path did not reject before parsing: %+v", got)
	}
}

func TestUDPCountersStatsAuthenticationAndConcurrency(t *testing.T) {
	s := NewServer(10, false)
	s.httpToken = "synthetic-status-token"
	var wg sync.WaitGroup
	for range 4 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range 1000 {
				s.udpDrops.rateLimit.Add(1)
				_ = s.snapshot()
			}
		}()
	}
	wg.Wait()
	for _, auth := range []bool{false, true} {
		req := httptest.NewRequest("GET", "/api/stats", nil)
		if auth {
			req.Header.Set("Authorization", "Bearer synthetic-status-token")
		}
		w := httptest.NewRecorder()
		s.HTTP().ServeHTTP(w, req)
		if !auth {
			if w.Code != http.StatusUnauthorized || strings.Contains(w.Body.String(), "udp_drops") {
				t.Fatal("unauthorized response leaked counters")
			}
			continue
		}
		var data map[string]json.RawMessage
		if w.Code != http.StatusOK || json.Unmarshal(w.Body.Bytes(), &data) != nil {
			t.Fatal("invalid stats response")
		}
		var drops map[string]uint64
		if json.Unmarshal(data["udp_drops"], &drops) != nil || len(drops) != 7 || drops["rate_limit"] != 4000 {
			t.Fatalf("aggregate counters missing or unexpected data: %s", data["udp_drops"])
		}
	}
}

func TestUDPRateSettingRejectedWithoutValues(t *testing.T) {
	for _, tc := range []struct {
		name, value, args, want string
	}{
		{"flag", "", "-max-udp-rate\x1f-1234567", "-max-udp-rate"},
		{"flag", "", "-max-udp-rate\x1fsynthetic-invalid-value", "-max-udp-rate"},
		{"RE_PSN_MAX_UDP_RATE", "-1234567", "", "RE_PSN_MAX_UDP_RATE"},
		{"PSNR_MAX_UDP_RATE", "-1234567", "", "PSNR_MAX_UDP_RATE"},
		{"RE_PSN_MAX_UDP_RATE", "synthetic-invalid-value", "", "RE_PSN_MAX_UDP_RATE"},
	} {
		t.Run(tc.name+tc.value, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestMainProcess$")
			cmd.Env = append(cleanEnv(), "RE_PSN_TEST_MAIN=1", "RE_PSN_TEST_ARGS="+tc.args)
			if tc.value != "" {
				cmd.Env = append(cmd.Env, tc.name+"="+tc.value)
			}
			out, err := cmd.CombinedOutput()
			if err == nil || ctx.Err() != nil || !strings.Contains(string(out), tc.want) || strings.Contains(string(out), "1234567") || strings.Contains(string(out), "synthetic-invalid-value") {
				t.Fatalf("invalid setting did not fail safely: %v, %s", err, out)
			}
		})
	}
}

func TestUDPRateSettingPrecedence(t *testing.T) {
	for _, tc := range []struct {
		name string
		env  []string
		args []string
	}{
		{"default", nil, nil},
		{"new zero", []string{"RE_PSN_MAX_UDP_RATE=0"}, nil},
		{"old zero", []string{"PSNR_MAX_UDP_RATE=0"}, nil},
		{"new wins", []string{"RE_PSN_MAX_UDP_RATE=0", "PSNR_MAX_UDP_RATE=synthetic-invalid-value"}, nil},
		{"flag wins", []string{"RE_PSN_MAX_UDP_RATE=synthetic-invalid-value"}, []string{"-max-udp-rate", "0"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, log := startMain(t, tc.env, tc.args...)
			if strings.Contains(log, "synthetic-invalid-value") {
				t.Fatal("startup logged a setting value")
			}
		})
	}
	for _, tc := range []struct {
		s    setting
		want int
	}{
		{setting{}, defaultUDPRate},
		{setting{"0", "-max-udp-rate", true}, 0},
		{setting{"25", "RE_PSN_MAX_UDP_RATE", true}, 25},
	} {
		if got, err := parseUDPRate(tc.s); err != nil || got != tc.want {
			t.Fatalf("parseUDPRate gave %d/%v, want%d", got, err, tc.want)
		}
	}
}
