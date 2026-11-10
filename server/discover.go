package main

// LAN discovery (opt-in, -discover). A client broadcasts a DISCOVER to the
// server's UDP port and every server that has it on answers the sender with
// its ports, relay flag, player count, name and version. See docs/api.md.
//
//   DISCOVER        client -> server  nonce u32 | zero padding, 64 bytes in all
//   DISCOVER_REPLY  server -> client  nonce u32 | tcp_port u16 | udp_port u16 | flags u8 | players u16 | name [24] | version [12]
//
// The reply (52 bytes) is smaller than the request, so it cannot amplify
// traffic, and it goes only to private, carrier-grade NAT, link-local or
// loopback sources.

import (
	"encoding/binary"
	"fmt"
	"net"
	"net/netip"
)

const (
	udpDiscover      = 0x04
	udpDiscoverReply = 0x84

	discoverRequestLen = 64
	discoverReplyLen   = 52
	maxServerName      = 24
	discoverVersionLen = 12
)

// validServerName checks a -name value: at most 24 bytes of printable ASCII.
func validServerName(name string) error {
	if len(name) > maxServerName {
		return fmt.Errorf("-name: %d bytes, at most %d", len(name), maxServerName)
	}
	for i := 0; i < len(name); i++ {
		if name[i] < 0x20 || name[i] > 0x7e {
			return fmt.Errorf("-name: %q holds a control or non-ASCII character", name)
		}
	}
	return nil
}

var cgnat = netip.MustParsePrefix("100.64.0.0/10")

// discoverSource reports whether a DISCOVER from a may be answered: private
// (10/8, 172.16/12, 192.168/16), carrier-grade NAT (100.64/10), link-local
// (169.254/16) or loopback. Never a public address.
func discoverSource(a netip.Addr) bool {
	a = a.Unmap()
	return a.IsLoopback() || a.IsPrivate() || a.IsLinkLocalUnicast() || cgnat.Contains(a)
}

// isDiscover is whether pkt is a DISCOVER: the magic, the type and exactly 64 bytes.
func isDiscover(pkt []byte) bool {
	return len(pkt) == discoverRequestLen && string(pkt[:4]) == udpMagic && pkt[4] == udpDiscover
}

func (s *Server) discoverUDP(pc net.PacketConn, pkt []byte, addr *net.UDPAddr, source netip.Addr) {
	if !discoverSource(source) {
		s.udpDrops.discoverRefused.Add(1)
		return
	}
	s.mu.Lock()
	players := len(s.clients)
	s.mu.Unlock()
	if players > 0xffff {
		players = 0xffff
	}
	var flags byte
	if s.relay {
		flags |= 1
	}
	var udpPort uint16
	if ua, ok := pc.LocalAddr().(*net.UDPAddr); ok {
		udpPort = uint16(ua.Port)
	}
	reply := make([]byte, discoverReplyLen)
	copy(reply, udpMagic)
	reply[4] = udpDiscoverReply
	copy(reply[5:9], pkt[5:9])
	binary.BigEndian.PutUint16(reply[9:], s.tcpPort)
	binary.BigEndian.PutUint16(reply[11:], udpPort)
	reply[13] = flags
	binary.BigEndian.PutUint16(reply[14:], uint16(players))
	copy(reply[16:16+maxServerName], s.name)
	copy(reply[16+maxServerName:], version[:min(len(version), discoverVersionLen)])
	s.log.Debug("discover answered", "to", addr.String())
	if n, err := pc.WriteTo(reply, addr); err != nil || n != len(reply) {
		s.udpDrops.writeError.Add(1)
	}
}
