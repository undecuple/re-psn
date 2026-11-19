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

// streamEnd is one player of a relay stream as its pass stood: the account, key and expiry are kept after the player's
// connection is gone, so the stream still ends with its pass.
type streamEnd struct {
	c   *client
	sub string
	kid string
	exp int64
}

// liveStream is a relay stream from its offer to the end of its splice, kept so a reload, or the end of a pass, can close it.
// All fields are under s.mu.
type liveStream struct {
	id    uint32
	st    *stream
	ends  [2]streamEnd
	a, b  net.Conn // the connecting leg, and the accepting leg once it has arrived
	timer *time.Timer
	cut   bool // closed by cutStream: a leg that arrives later is closed too
	done  bool // the splice ended
}

// streamCutReason says why ls must end under p at now, or "" if it may go on; next is when the first pass at an end lapses
// (zero if none can). Where a pass is needed (-require-pass relay or connect) a stream ends with its pass as the control
// connection does: 60 s after exp, when its key leaves the key file, or when its account is revoked; with no pass needed
// only the revocation counts. The caller holds s.mu.
func (s *Server) streamCutReason(ls *liveStream, p *passConfig, now time.Time) (reason string, next time.Time) {
	if p == nil {
		return "", time.Time{}
	}
	gated := s.requireRelayPass || s.requireConnectPass
	for i := range ls.ends {
		e := &ls.ends[i]
		if e.sub == "" {
			continue
		}
		if e.c != nil && e.c.sub == e.sub { // a refresh moved the expiry; once the connection is gone nothing can
			e.kid, e.exp = e.c.passKid, e.c.passExp
		}
		if p.revoked[e.sub] {
			return "pass revoked", time.Time{}
		}
		if !gated {
			continue
		}
		if p.keys[e.kid] == nil {
			return "pass key removed", time.Time{}
		}
		end := time.Unix(e.exp+passSkew, 0)
		if now.Unix() >= e.exp+passSkew {
			return "pass expired", time.Time{}
		}
		if next.IsZero() || end.Before(next) {
			next = end
		}
	}
	return "", next
}

// armStream makes ls's timer fire at next, or leaves it unarmed when next is zero. The caller holds s.mu.
func (s *Server) armStream(ls *liveStream, next time.Time) {
	if next.IsZero() || ls.done || ls.cut {
		return
	}
	if ls.timer == nil {
		ls.timer = time.AfterFunc(time.Until(next), func() { s.streamTimer(ls) })
	} else {
		ls.timer.Reset(time.Until(next))
	}
}

// rearmStreams moves the timers of c's streams after its AUTH changed its pass: a refresh can end sooner as well as later.
// The caller holds s.mu.
func (s *Server) rearmStreams(c *client) {
	for ls := range s.live {
		if ls.ends[0].c == c || ls.ends[1].c == c {
			_, next := s.streamCutReason(ls, s.pass.Load(), time.Now())
			s.armStream(ls, next)
		}
	}
}

// cutStream marks ls cut and returns the legs to close. The caller holds s.mu, closes them after releasing it and logs.
func (s *Server) cutStream(ls *liveStream) []net.Conn {
	if ls.cut {
		return nil
	}
	ls.cut = true
	if _, waiting := s.streams[ls.id]; waiting { // no target can accept it any more, and the wait for one is over
		delete(s.streams, ls.id)
		ls.st.accept <- nil // nobody else has sent on it: the leg that takes a stream out of the table is the one that sends
	}
	if ls.timer != nil {
		ls.timer.Stop()
	}
	conns := []net.Conn{ls.a}
	if ls.b != nil {
		conns = append(conns, ls.b)
	}
	return conns
}

// streamTimer runs when the first pass of a stream may have lapsed: it ends the stream, or waits for the new end if a refresh
// moved it.
func (s *Server) streamTimer(ls *liveStream) {
	s.mu.Lock()
	if ls.done || ls.cut {
		s.mu.Unlock()
		return
	}
	reason, next := s.streamCutReason(ls, s.pass.Load(), time.Now())
	var conns []net.Conn
	if reason != "" {
		conns = s.cutStream(ls)
	} else {
		s.armStream(ls, next)
	}
	s.mu.Unlock()
	s.closeStream(ls, conns, reason)
}

func (s *Server) closeStream(ls *liveStream, conns []net.Conn, reason string) {
	if len(conns) == 0 {
		return
	}
	s.log.Warn("relay stream closed", "stream", ls.id, "reason", reason)
	for _, c := range conns {
		c.Close()
	}
}

// endStream forgets ls when its splice is over or it never started: the timer is stopped and nothing keeps the stream.
func (s *Server) endStream(ls *liveStream) {
	s.mu.Lock()
	ls.done = true
	if ls.timer != nil {
		ls.timer.Stop()
	}
	delete(s.live, ls)
	s.mu.Unlock()
}

// relayStream is how a player needing the relay for streams is told.
// The target can't take a direct connection when it's behind a router (its
// probe came from a public address that isn't its own) and the viewer isn't
// on its network. Without a probe, direct is assumed. With -relay-force every
// stream goes through the relay, whoever the two are, so a test can use it
// from one network.
func (s *Server) relayStream(target, viewer *client) bool {
	if s.noPeer[target.commID] || s.noPeer[viewer.commID] {
		return false
	}
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
		noPass := ok && s.requireRelayPass && !(st.to.passAllows(scopeRelay, time.Now()) && st.from.passAllows(scopeRelay, time.Now()))
		ok = ok && !noPass
		if ok {
			delete(s.streams, id)
		}
		s.mu.Unlock()
		if noPass {
			return fail(errPass)
		}
		if !ok {
			return fail(errNotFound)
		}
		if s.acceptHook != nil {
			s.acceptHook()
		}
		if ready() != nil {
			st.accept <- nil
			return false
		}
		st.accept <- conn
		return true
	}

	toUser, vport := r.u32(), r.u16()
	s.mu.Lock()
	from, to := s.clients[user], s.clients[toUser] // not listed before AUTH under -require-pass connect: a stream from a connection not signed in is not found
	ok := r.err == nil && from != nil && from.token == token && to != nil && to.commID != "" && sharesRoom(from, to)
	noPass := ok && s.requireRelayPass && !(from.passAllows(scopeRelay, time.Now()) && to.passAllows(scopeRelay, time.Now()))
	off := ok && (s.noPeer[from.commID] || s.noPeer[to.commID])
	var st *stream
	var ls *liveStream
	if ok && !off && !noPass {
		// registered here, under the lock that offers it: a reload either sees it or has already changed what this checks
		ls = &liveStream{ends: [2]streamEnd{{c: from, sub: from.sub, kid: from.passKid, exp: from.passExp}, {c: to, sub: to.sub, kid: to.passKid, exp: to.passExp}}, a: conn}
		var next time.Time
		if reason, n := s.streamCutReason(ls, s.pass.Load(), time.Now()); reason != "" {
			noPass, ls = true, nil
		} else {
			next = n
		}
		if ls != nil {
			s.nextStream++
			st = &stream{id: s.nextStream, from: from, to: to, accept: make(chan net.Conn, 1)}
			ls.id, ls.st = st.id, st
			s.live[ls] = struct{}{}
			s.armStream(ls, next)
		}
	}
	if st != nil {
		s.streams[st.id] = st
		w := &writer{}
		w.u32(st.id)
		w.u32(from.id)
		w.u16(vport)
		s.flush([]push{{to, msgStreamOffer, w.b}})
	}
	s.mu.Unlock()
	if off {
		return fail(errNoRelay)
	}
	if noPass {
		return fail(errPass)
	}
	if !ok {
		return fail(errNotFound)
	}
	s.log.Debug("relay stream", "stream", st.id, "from", from.onlineID, "to", to.onlineID, "vport", vport)

	defer s.endStream(ls)
	other := s.awaitAccept(st, streamAcceptTimeout)
	s.mu.Lock()
	cut := ls.cut
	if other != nil {
		ls.b = other
	}
	s.mu.Unlock()
	if cut { // closed while waiting: the legs are closed by whoever cut it, and by us for one that came later
		if other != nil {
			other.Close()
		}
		conn.Close()
		return false
	}
	if other == nil {
		select {
		case <-s.stopped:
			return false
		default:
		}
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

// awaitAccept waits up to d for the target's leg of st. It returns nil if there is none: the server is stopping, the wait ran out,
// or the accepting side failed. An accepting side that had already taken the stream out of s.streams when the wait ran out is
// waited for (it always sends a leg or nil), so its connection is used and not left behind.
func (s *Server) awaitAccept(st *stream, d time.Duration) net.Conn {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case other := <-st.accept:
		return other
	case <-s.stopped:
	case <-timer.C:
	}
	s.mu.Lock()
	_, waiting := s.streams[st.id]
	delete(s.streams, st.id)
	s.mu.Unlock()
	if waiting {
		return nil
	}
	return <-st.accept
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
	src, dst := s.clients[from], s.clients[toID] // as above: a connection not signed in is not listed, so its datagrams are dropped as unauthorized
	valid := src != nil && src.token == token && dst != nil && sharesRoom(src, dst)
	off := valid && (s.noPeer[src.commID] || s.noPeer[dst.commID]) // checked before the endpoint: a listed title never has one
	noPass := valid && !off && s.requireRelayPass && !(src.passAllows(scopeRelay, time.Now()) && dst.passAllows(scopeRelay, time.Now()))
	ok := valid && !off && !noPass && dst.udpPort != 0
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
	if off {
		s.udpDrops.relayDisabled.Add(1)
		return
	}
	if noPass {
		s.udpDrops.relayNoPass.Add(1)
		return
	}
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
