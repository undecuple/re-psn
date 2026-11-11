package client

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"errors"
	"net"
	"net/netip"
	"os"
	"time"
)

// LAN discovery. The client broadcasts one 64-byte UDP datagram and collects the replies of servers that were
// started with discovery on. All integers are big-endian.
//
//	request: "PSNR" | 0x04 | nonce u32 | zero padding to 64 bytes
//	reply:   "PSNR" | 0x84 | nonce u32 | tcp_port u16 | udp_port u16 | flags u8 (bit 0: relay on) | players u16 |
//	         name [24] | version [12]   (52 bytes; text is ASCII, padded with NUL)
const (
	typeDiscover      = 0x04
	typeDiscoverReply = 0x84

	discoverRequestLen = 64
	discoverReplyLen   = 52
	discoverNameLen    = 24
	discoverVersionLen = 12

	// DefaultPort is the port servers listen on when none is configured.
	DefaultPort = 36100
	// DefaultDiscoverWait is how long a caller usually listens for replies.
	DefaultDiscoverWait = 1500 * time.Millisecond
)

// Found is one server that answered a discovery request.
type Found struct {
	IP      netip.Addr
	TCPPort uint16
	UDPPort uint16
	Relay   bool
	Players uint16
	Name    string
	Version string
}

// Addr is the host:port to connect to over TCP.
func (f Found) Addr() string { return netip.AddrPortFrom(f.IP, f.TCPPort).String() }

// Discover broadcasts a discovery request to 255.255.255.255:port (0 means 36100) and to the directed broadcast
// address of every IPv4 interface that is up, then listens for wait and returns the servers that answered, in the
// order they did, one per address and TCP port. Finding none is not an error. If ctx ends first, the servers found
// so far are returned with ctx's error, unless that error is the deadline, which only ends the wait. The socket is
// a temporary one and is closed before Discover returns.
func Discover(ctx context.Context, wait time.Duration, port uint16) ([]Found, error) {
	if port == 0 {
		port = DefaultPort
	}
	return discover(ctx, wait, broadcastTargets(port))
}

// broadcastTargets lists the limited broadcast and the directed broadcast of every IPv4 interface that is up.
func broadcastTargets(port uint16) []netip.AddrPort {
	out := []netip.AddrPort{netip.AddrPortFrom(netip.AddrFrom4([4]byte{255, 255, 255, 255}), port)}
	ifs, err := net.Interfaces()
	if err != nil {
		return out
	}
	for _, ifc := range ifs {
		if ifc.Flags&net.FlagUp == 0 || ifc.Flags&net.FlagLoopback != 0 {
			continue
		}
		addrs, err := ifc.Addrs()
		if err != nil {
			continue
		}
		for _, a := range addrs {
			n, ok := a.(*net.IPNet)
			if !ok {
				continue
			}
			ip, m := n.IP.To4(), n.Mask
			if ip == nil || len(m) != 4 {
				continue
			}
			var b [4]byte
			for i := range b {
				b[i] = ip[i] | ^m[i]
			}
			t := netip.AddrPortFrom(netip.AddrFrom4(b), port)
			dup := false
			for _, o := range out {
				dup = dup || o == t
			}
			if !dup {
				out = append(out, t)
			}
		}
	}
	return out
}

func discover(ctx context.Context, wait time.Duration, targets []netip.AddrPort) ([]Found, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	conn, err := net.ListenUDP("udp4", &net.UDPAddr{})
	if err != nil {
		return nil, err
	}
	defer conn.Close()

	var nonce [4]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return nil, err
	}
	n := binary.BigEndian.Uint32(nonce[:])
	req := make([]byte, discoverRequestLen)
	copy(req, "PSNR")
	req[4] = typeDiscover
	binary.BigEndian.PutUint32(req[5:], n)

	sent := 0
	var sendErr error
	for _, t := range targets {
		if _, err := conn.WriteToUDPAddrPort(req, t); err != nil {
			sendErr = err
		} else {
			sent++
		}
	}
	if sent == 0 && sendErr != nil {
		return nil, sendErr
	}

	var found []Found
	seen := map[netip.AddrPort]bool{}
	end := time.Now().Add(wait)
	buf := make([]byte, 512)
	for {
		if err := ctx.Err(); err != nil {
			if errors.Is(err, context.DeadlineExceeded) {
				err = nil
			}
			return found, err
		}
		now := time.Now()
		if !now.Before(end) {
			return found, nil
		}
		// Wake up often enough to notice a cancelled context without a helper goroutine.
		step := now.Add(50 * time.Millisecond)
		if step.After(end) {
			step = end
		}
		conn.SetReadDeadline(step)
		sz, from, err := conn.ReadFromUDPAddrPort(buf)
		if err != nil {
			if errors.Is(err, os.ErrDeadlineExceeded) {
				continue
			}
			return found, err
		}
		f, ok := parseReply(buf[:sz], n, from.Addr())
		if !ok {
			continue
		}
		key := netip.AddrPortFrom(f.IP, f.TCPPort)
		if !seen[key] {
			seen[key] = true
			found = append(found, f)
		}
	}
}

// parseReply accepts only a well-formed reply that echoes nonce, from an IPv4 sender.
func parseReply(p []byte, nonce uint32, from netip.Addr) (Found, bool) {
	from = from.Unmap()
	if len(p) != discoverReplyLen || string(p[:4]) != "PSNR" || p[4] != typeDiscoverReply ||
		binary.BigEndian.Uint32(p[5:]) != nonce || !from.Is4() {
		return Found{}, false
	}
	return Found{
		IP:      from,
		TCPPort: binary.BigEndian.Uint16(p[9:]),
		UDPPort: binary.BigEndian.Uint16(p[11:]),
		Relay:   p[13]&1 != 0,
		Players: binary.BigEndian.Uint16(p[14:]),
		Name:    asciiText(p[16 : 16+discoverNameLen]),
		Version: asciiText(p[16+discoverNameLen : 16+discoverNameLen+discoverVersionLen]),
	}, true
}

// asciiText reads a NUL-padded field: it stops at the first NUL and drops everything that is not printable ASCII.
func asciiText(b []byte) string {
	out := make([]byte, 0, len(b))
	for _, c := range b {
		if c == 0 {
			break
		}
		if c >= 0x20 && c < 0x7f {
			out = append(out, c)
		}
	}
	return string(out)
}
