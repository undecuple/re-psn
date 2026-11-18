package main

import (
	"bytes"
	"encoding/binary"
	"net"
	"sort"
	"testing"
	"time"
)

// recvBig returns the next datagram on u, or nil after d. Unlike recvWithin its buffer holds a full-size datagram.
func recvBig(u *net.UDPConn, d time.Duration) []byte {
	u.SetReadDeadline(time.Now().Add(d))
	buf := make([]byte, 65536)
	n, _, err := u.ReadFrom(buf)
	if err != nil {
		return nil
	}
	return buf[:n]
}

// A title that runs a rollback session through the relay sends small datagrams, many per second, in both directions at
// once. Two players do that here for one second at 60 per second each, with the payload sizes such a title uses (32 to
// 100 bytes): every datagram arrives once, in order, intact, from the server, and the delay each one saw is reported.
func TestUDPRelayRollbackTraffic(t *testing.T) {
	_, addr := startRelay(t, true)
	srv, _ := net.ResolveUDPAddr("udp", addr)
	a, b := join(t, addr, "alice"), join(t, addr, "bob")
	meet(t, a, b)
	ua, ub := udpSock(t), udpSock(t)
	probeFrom(t, ua, addr, a)
	probeFrom(t, ub, addr, b)

	const frames = 60
	payload := func(seq int) []byte {
		p := make([]byte, 32+seq%69) // 32 to 100 bytes
		binary.BigEndian.PutUint32(p, uint32(seq))
		binary.BigEndian.PutUint64(p[4:], uint64(time.Now().UnixNano()))
		for i := 12; i < len(p); i++ {
			p[i] = byte(seq + i)
		}
		return p
	}
	check := func(who string, got []byte, from player, seq int) time.Duration {
		if len(got) < 9+12 || string(got[:4]) != udpMagic || got[4] != udpRelay || binary.BigEndian.Uint32(got[5:]) != from.id {
			t.Fatalf("%s: frame %d came as %q", who, seq, got)
		}
		p := got[9:]
		if want := 32 + seq%69; len(p) != want || binary.BigEndian.Uint32(p) != uint32(seq) {
			t.Fatalf("%s: frame %d has length %d and sequence %d", who, seq, len(p), binary.BigEndian.Uint32(p))
		}
		for i := 12; i < len(p); i++ {
			if p[i] != byte(seq+i) {
				t.Fatalf("%s: frame %d changed at byte %d", who, seq, i)
			}
		}
		return time.Since(time.Unix(0, int64(binary.BigEndian.Uint64(p[4:]))))
	}

	var delays []time.Duration
	tick := time.NewTicker(time.Second / 60)
	defer tick.Stop()
	for seq := 0; seq < frames; seq++ {
		<-tick.C
		ua.WriteTo(relayPkt(a, a.token, b.id, string(payload(seq))), srv)
		ub.WriteTo(relayPkt(b, b.token, a.id, string(payload(seq))), srv)
		delays = append(delays, check("alice to bob", recvBig(ub, time.Second), a, seq), check("bob to alice", recvBig(ua, time.Second), b, seq))
	}
	sort.Slice(delays, func(i, j int) bool { return delays[i] < delays[j] })
	t.Logf("relayed %d datagrams in 1 s on loopback: delay min %v, median %v, max %v", len(delays), delays[0], delays[len(delays)/2], delays[len(delays)-1])
	if delays[len(delays)-1] > 250*time.Millisecond {
		t.Fatalf("a datagram took %v on loopback", delays[len(delays)-1])
	}
}

// The relay has no datagram size cap of its own: a payload as large as one UDP packet allows (the read buffer is 65536
// bytes and the relay header is 17 bytes) goes through whole, and a title's MTU-sized and jumbo payloads with it.
func TestUDPRelayPayloadSizes(t *testing.T) {
	_, addr := startRelay(t, true)
	srv, _ := net.ResolveUDPAddr("udp", addr)
	a, b := join(t, addr, "alice"), join(t, addr, "bob")
	meet(t, a, b)
	ua, ub := udpSock(t), udpSock(t)
	probeFrom(t, ub, addr, b)

	for _, n := range []int{1, 1200, 1472, 9000, 60000} {
		p := bytes.Repeat([]byte{byte(n)}, n)
		if _, err := ua.WriteTo(relayPkt(a, a.token, b.id, string(p)), srv); err != nil {
			t.Fatalf("%d bytes: %v", n, err)
		}
		got := recvBig(ub, time.Second)
		if len(got) != 9+n || !bytes.Equal(got[9:], p) {
			t.Fatalf("%d bytes: relayed %d bytes", n, len(got))
		}
	}
}
