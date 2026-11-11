package client

import (
	"context"
	"encoding/binary"
	"errors"
	"net"
	"net/netip"
	"runtime"
	"testing"
	"time"
)

func discoverReply(nonce uint32, tcp, udp uint16, flags byte, players uint16, name, version string) []byte {
	p := make([]byte, discoverReplyLen)
	copy(p, "PSNR")
	p[4] = typeDiscoverReply
	binary.BigEndian.PutUint32(p[5:], nonce)
	binary.BigEndian.PutUint16(p[9:], tcp)
	binary.BigEndian.PutUint16(p[11:], udp)
	p[13] = flags
	binary.BigEndian.PutUint16(p[14:], players)
	copy(p[16:16+discoverNameLen], name)
	copy(p[16+discoverNameLen:], version)
	return p
}

// responder listens on loopback and answers each request with the datagrams reply returns (given the request's nonce).
func responder(t *testing.T, reply func(nonce uint32) [][]byte) netip.AddrPort {
	t.Helper()
	c, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		buf := make([]byte, 128)
		for {
			n, from, err := c.ReadFromUDP(buf)
			if err != nil {
				return
			}
			if n != discoverRequestLen || string(buf[:4]) != "PSNR" || buf[4] != typeDiscover {
				continue
			}
			for _, d := range reply(binary.BigEndian.Uint32(buf[5:])) {
				c.WriteToUDP(d, from)
			}
		}
	}()
	t.Cleanup(func() { c.Close(); <-done })
	return c.LocalAddr().(*net.UDPAddr).AddrPort()
}

func TestDiscoverValidReply(t *testing.T) {
	to := responder(t, func(n uint32) [][]byte {
		return [][]byte{discoverReply(n, 36100, 36101, 1, 7, "lan box", "0.5.0")}
	})
	got, err := discover(context.Background(), 300*time.Millisecond, []netip.AddrPort{to})
	if err != nil || len(got) != 1 {
		t.Fatalf("got %v, %v", got, err)
	}
	want := Found{IP: netip.MustParseAddr("127.0.0.1"), TCPPort: 36100, UDPPort: 36101, Relay: true, Players: 7, Name: "lan box", Version: "0.5.0"}
	if got[0] != want {
		t.Fatalf("got %+v, want %+v", got[0], want)
	}
	if got[0].Addr() != "127.0.0.1:36100" {
		t.Fatalf("addr %q", got[0].Addr())
	}
}

func TestDiscoverIgnoresBadDatagrams(t *testing.T) {
	to := responder(t, func(n uint32) [][]byte {
		good := discoverReply(n, 1, 2, 0, 0, "ok", "v")
		wrongNonce := discoverReply(n+1, 3, 4, 0, 0, "nonce", "v")
		wrongType := discoverReply(n, 5, 6, 0, 0, "type", "v")
		wrongType[4] = 0x83
		wrongMagic := discoverReply(n, 7, 8, 0, 0, "magic", "v")
		wrongMagic[0] = 'X'
		long := append(discoverReply(n, 9, 10, 0, 0, "long", "v"), 0)
		short := discoverReply(n, 11, 12, 0, 0, "short", "v")[:51]
		return [][]byte{wrongNonce, wrongType, wrongMagic, long, short, good}
	})
	got, err := discover(context.Background(), 300*time.Millisecond, []netip.AddrPort{to})
	if err != nil || len(got) != 1 || got[0].Name != "ok" {
		t.Fatalf("got %v, %v", got, err)
	}
}

func TestDiscoverDeduplicates(t *testing.T) {
	to := responder(t, func(n uint32) [][]byte {
		a := discoverReply(n, 36100, 36101, 0, 1, "a", "v")
		return [][]byte{a, a, discoverReply(n, 36200, 36201, 0, 2, "b", "v"), a}
	})
	// Two targets that reach the same responder: its answers arrive twice over.
	got, err := discover(context.Background(), 300*time.Millisecond, []netip.AddrPort{to, to})
	if err != nil || len(got) != 2 || got[0].TCPPort != 36100 || got[1].TCPPort != 36200 {
		t.Fatalf("got %v, %v", got, err)
	}
}

func TestDiscoverTimeoutIsEmptyNotError(t *testing.T) {
	to := responder(t, func(uint32) [][]byte { return nil })
	start := time.Now()
	got, err := discover(context.Background(), 150*time.Millisecond, []netip.AddrPort{to})
	if err != nil || len(got) != 0 {
		t.Fatalf("got %v, %v", got, err)
	}
	if d := time.Since(start); d < 140*time.Millisecond || d > 2*time.Second {
		t.Fatalf("waited %v", d)
	}
}

func TestDiscoverSanitisesText(t *testing.T) {
	to := responder(t, func(n uint32) [][]byte {
		p := discoverReply(n, 1, 2, 0, 0, "", "")
		copy(p[16:], "a\x01b\x1b[31m\xffc\x7fd\x00hidden")
		copy(p[16+discoverNameLen:], "1.\x02"+"2")
		return [][]byte{p}
	})
	got, err := discover(context.Background(), 300*time.Millisecond, []netip.AddrPort{to})
	if err != nil || len(got) != 1 {
		t.Fatalf("got %v, %v", got, err)
	}
	if got[0].Name != "ab[31mcd" || got[0].Version != "1.2" {
		t.Fatalf("name %q version %q", got[0].Name, got[0].Version)
	}
}

func TestDiscoverCancel(t *testing.T) {
	to := responder(t, func(n uint32) [][]byte { return [][]byte{discoverReply(n, 1, 2, 0, 0, "x", "v")} })
	before := runtime.NumGoroutine()
	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(150*time.Millisecond, cancel)
	start := time.Now()
	got, err := discover(ctx, 10*time.Second, []netip.AddrPort{to})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("found so far: %v", got)
	}
	if time.Since(start) > 2*time.Second {
		t.Fatal("cancel was not prompt")
	}
	if _, err := discover(ctx, time.Second, []netip.AddrPort{to}); !errors.Is(err, context.Canceled) {
		t.Fatalf("already cancelled: %v", err)
	}
	time.Sleep(50 * time.Millisecond)
	if after := runtime.NumGoroutine(); after > before {
		t.Fatalf("goroutines %d before, %d after", before, after)
	}
}

func TestBroadcastTargetsStartWithLimited(t *testing.T) {
	ts := broadcastTargets(0x1234)
	if len(ts) == 0 || ts[0] != netip.MustParseAddrPort("255.255.255.255:4660") {
		t.Fatalf("targets %v", ts)
	}
	for _, a := range ts {
		if !a.Addr().Is4() || a.Port() != 0x1234 {
			t.Fatalf("target %v", a)
		}
	}
}
