package main

// The relay, for players who can't reach each other directly. Off unless the
// server runs with -relay: it costs the host's bandwidth.
//
// Streams (TCP). A player behind a router can't take an incoming connection,
// so a peer that wants a stream with one opens a second TCP connection to the
// server that starts with STREAM_CONNECT instead of HELLO. The server offers
// the stream to the target over its own connection (STREAM_OFFER); the
// target opens a connection starting with STREAM_ACCEPT; the server answers
// both with STREAM_READY and from then on copies bytes between them. Which
// peers need this is the server's call (ROUTE pushes, below).
//
// Datagrams (UDP). A RELAY packet to the server's UDP port carries the target
// player; the server sends the payload on from that same port, where the
// target's router already has a mapping (its probes go there), with the
// sender's id in front. Clients switch to it for a peer they punched but
// never heard from directly: a symmetric NAT on either side.

import (
	"encoding/binary"
	"errors"
	"net"
	"sync/atomic"
	"time"
)

const (
	msgStreamConnect = 0x30 // first message on a relay stream connection
	msgStreamAccept  = 0x31
	msgStreamReady   = 0x96 // server -> both ends: raw bytes follow
	msgStreamOffer   = 0xA6 // push: stream_id u32 | from_user u32 | vport u16
	msgRoute         = 0xA7 // push: room u64 | user u32 | flags u8

	routeStreamRelay = 1 // flags bit 0: streams to that player go through the relay

	udpRelay = 0x03

	streamAcceptTimeout = 10 * time.Second
)

type stream struct {
	id     uint32
	from   *client
	to     *client
	accept chan net.Conn // the target's leg, once it accepts
}

// relayStream is how a player needing the relay for streams is told.
// The target can't take a direct connection when it's behind a router (its
// probe came from a public address that isn't its own) and the viewer isn't
// on its network. Without a probe, direct is assumed. With -relay-force every
// stream goes through the relay, whoever the two are, so a test can use it
// from one network.
func (s *Server) relayStream(target, viewer *client) bool {
	if s.relay && s.relayForce {
		return true
	}
	return s.relay && target.udpPort != 0 && target.udpIP != target.localIP && !sameNetwork(viewer, target)
}

func (s *Server) routePush(r *room, about, to *client) push {
	w := &writer{}
	w.u64(r.id)
	w.u32(about.id)
	var flags uint8
	if s.relayStream(about, to) {
		flags |= routeStreamRelay
	}
	w.u8(flags)
	return push{to, msgRoute, w.b}
}

func sharesRoom(a, b *client) bool {
	for id := range a.rooms {
		if b.rooms[id] != nil {
			return true
		}
	}
	return false
}

// streamLeg handles a connection whose first message is STREAM_CONNECT or
// STREAM_ACCEPT. It reports whether it handed the connection to another
// goroutine (which then owns it).
func (s *Server) streamLeg(conn net.Conn, t byte, p []byte) (handedOff bool) {
	r := &reader{b: p}
	req, user, token := r.u32(), r.u32(), r.u32()
	fail := func(code uint32) bool {
		writePacket(conn, msgError, errReply(req, code))
		return false
	}
	if r.err != nil {
		return fail(errBadRequest)
	}
	if !s.relay {
		return fail(errNoRelay)
	}
	ready := func() error {
		w := &writer{}
		w.u32(req)
		return writePacket(conn, msgStreamReady, w.b)
	}

	if t == msgStreamAccept {
		id := r.u32()
		s.mu.Lock()
		st := s.streams[id]
		ok := r.err == nil && st != nil && st.to.id == user && st.to.token == token
		if ok {
			delete(s.streams, id)
		}
		s.mu.Unlock()
		if !ok {
			return fail(errNotFound)
		}
		if ready() != nil {
			return false
		}
		st.accept <- conn
		return true
	}

	toUser, vport := r.u32(), r.u16()
	s.mu.Lock()
	from, to := s.clients[user], s.clients[toUser]
	ok := r.err == nil && from != nil && from.token == token && to != nil && to.commID != "" && sharesRoom(from, to)
	var st *stream
	if ok {
		s.nextStream++
		st = &stream{id: s.nextStream, from: from, to: to, accept: make(chan net.Conn, 1)}
		s.streams[st.id] = st
	}
	s.mu.Unlock()
	if !ok {
		return fail(errNotFound)
	}
	w := &writer{}
	w.u32(st.id)
	w.u32(from.id)
	w.u16(vport)
	s.flush([]push{{to, msgStreamOffer, w.b}})
	s.log.Debug("relay stream", "stream", st.id, "from", from.onlineID, "to", to.onlineID, "vport", vport)

	var other net.Conn
	select {
	case other = <-st.accept:
	case <-s.stopped:
		s.mu.Lock()
		delete(s.streams, st.id)
		s.mu.Unlock()
		return false
	case <-time.After(streamAcceptTimeout):
		s.mu.Lock()
		delete(s.streams, st.id)
		s.mu.Unlock()
		return fail(errNotFound)
	}
	if ready() != nil {
		other.Close()
		return false
	}
	conn.SetDeadline(time.Time{})
	other.SetDeadline(time.Time{})
	s.splice(conn, other)
	return false
}

// relayWriteTimeout bounds one write of a relayed chunk to a leg: a peer that stops reading cannot hold the leg open.
const relayWriteTimeout = 30 * time.Second

// splice copies both ways between the two legs of a relay stream until either end closes, the stream has been idle for
// s.relayIdle, or a write stalls; it then closes both. Idle means no byte in either direction: traffic one way keeps the
// stream alive. s.relayIdle 0 leaves the stream without an idle limit.
func (s *Server) splice(a, b net.Conn) {
	var last atomic.Int64
	last.Store(time.Now().UnixNano())
	idle := s.relayIdle
	copyOne := func(dst, src net.Conn) {
		defer func() { dst.Close(); src.Close() }()
		buf := make([]byte, 32*1024)
		for {
			if idle > 0 {
				src.SetReadDeadline(time.Now().Add(idle))
			}
			n, err := src.Read(buf)
			if n > 0 {
				last.Store(time.Now().UnixNano())
				dst.SetWriteDeadline(time.Now().Add(relayWriteTimeout))
				w, werr := dst.Write(buf[:n])
				atomic.AddUint64(&s.relayed, uint64(w))
				if werr != nil {
					return
				}
			}
			if err != nil {
				var ne net.Error
				if idle > 0 && errors.As(err, &ne) && ne.Timeout() && time.Since(time.Unix(0, last.Load())) < idle {
					continue // quiet this way, but the other way moved bytes within the idle time
				}
				return
			}
		}
	}
	done := make(chan struct{})
	go func() { copyOne(a, b); close(done) }()
	copyOne(b, a)
	<-done
}

// relayUDP forwards a RELAY packet: "PSNR" 03 | from u32 | token u32 | to u32 |
// payload, sent from sender, becomes "PSNR" 03 | from u32 | payload, sent to the target's probed
// endpoint from the server's UDP port.
func (s *Server) relayUDP(pc net.PacketConn, pkt []byte, sender *net.UDPAddr) {
	if len(pkt) < 17 {
		s.udpDrops.malformed.Add(1)
		return
	}
	if !s.relay {
		s.udpDrops.relayDisabled.Add(1)
		return
	}
	from, token, toID := binary.BigEndian.Uint32(pkt[5:]), binary.BigEndian.Uint32(pkt[9:]), binary.BigEndian.Uint32(pkt[13:])
	s.mu.Lock()
	src, dst := s.clients[from], s.clients[toID]
	ok := src != nil && src.token == token && dst != nil && dst.udpPort != 0 && sharesRoom(src, dst)
	mismatch := false
	if ok && !s.udpAnySource {
		ip := sender.IP.To4()
		mismatch = ip == nil || src.ip != [4]byte(ip) // the sender's own address, as for a probe
	}
	var to *net.UDPAddr
	if ok && !mismatch {
		to = &net.UDPAddr{IP: net.IP(append([]byte(nil), dst.udpIP[:]...)), Port: int(dst.udpPort)}
	}
	s.mu.Unlock()
	if !ok {
		s.udpDrops.unauthorized.Add(1)
		return
	}
	if mismatch {
		s.udpDrops.sourceMismatch.Add(1)
		return
	}
	out := make([]byte, 0, len(pkt)-8)
	out = append(out, udpMagic...)
	out = append(out, udpRelay)
	out = binary.BigEndian.AppendUint32(out, from)
	out = append(out, pkt[17:]...)
	if n, err := pc.WriteTo(out, to); err != nil || n != len(out) {
		s.udpDrops.writeError.Add(1)
		return
	}
	atomic.AddUint64(&s.relayed, uint64(len(pkt)-17))
}
