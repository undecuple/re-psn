package main

// One goroutine per client connection, one mutex over all room state (the
// score store has its own lock; scores.go). Room handlers run under the lock
// and return the pushes they owe other clients; those are written after the
// lock drops, so a slow peer socket never stalls anyone else's request.

import (
	crand "crypto/rand"
	"encoding/binary"
	"io"
	"log/slog"
	"net"
	"net/netip"
	"os"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/sp00nznet/psnr/server/store"
)

const clientTimeout = 90 * time.Second // HELLO/HEARTBEAT keep a client alive

// A reported round trip is shown while it is fresh, and values above maxReportedRTT (including 0xFFFF, "unknown")
// clear it.
const (
	maxReportedRTT = 60000 // milliseconds
	rttFresh       = 3 * time.Minute
)

type Server struct {
	log        *slog.Logger
	relay      bool // -relay: carry traffic for players who can't reach each other (relay.go)
	relayForce bool // -relay-force: with relay, send every stream through it (a test aid)

	httpToken string // -http-token: required by the status page when set (api.go)

	lifeMu   sync.Mutex
	tcp      net.Listener
	udp      net.PacketConn
	tcpReady bool
	udpReady bool
	stopping bool
	stopped  chan struct{}
	owned    map[*ownedConn]struct{}
	handlers sync.WaitGroup

	// ponytail: one lock for everything; per-title locks if one server ever
	// carries enough titles for it to show.
	mu       sync.Mutex
	clients  map[uint32]*client
	rooms    map[uint64]*room
	byCode   map[string]*room // invite-only rooms by their code (rooms.go)
	nextID   uint32
	nextRoom uint64
	maxRooms int
	started  time.Time
	conns    uint64

	scores store.Store // leaderboards (scores.go); memory unless -db says otherwise

	maxPlayerRooms int            // rooms one player may be in at once
	maxPerAddr     int            // open connections per remote address
	maxReqRate     int            // requests per second per connection
	addrConns      map[string]int // open connections by remote host

	udpAnySource   bool // accept UDP probes and relay packets from an address other than the player's TCP address (udp.go)
	maxUDPRate     int  // datagrams per second per source IP; zero disables admission limiting
	udpDrops       udpDropCounters
	trustedProxies []netip.Prefix // peers allowed to name the real client (proxy.go)
	proxyProto     bool           // require a PROXY header from a trusted peer

	streams    map[uint32]*stream // relay streams waiting for their target to accept
	nextStream uint32
	relayed    uint64 // bytes carried by the relay (atomic)

	refusedConns   uint64 // connections refused for too many from one address (atomic)
	refusedReqRate uint64 // connections closed for sending requests too fast (atomic)
}

type client struct {
	id       uint32
	conn     net.Conn
	commID   string // NP communication ID, e.g. NPWR00860_00: the title
	onlineID string
	ip       [4]byte // as the server sees it
	p2pPort  uint16  // UDP port the title receives peer traffic on

	codeMisses int       // wrong or malformed room codes in the current window
	codeWindow time.Time // start of that window
	token      uint32    // from HELLO_ACK; UDP probes must carry it
	udpIP      [4]byte   // P2P socket's public endpoint, from its UDP probe
	udpWarned  bool      // a probe from another address was already logged for this player
	udpPort    uint16
	localIP    [4]byte   // the client's own address, from its UDP probe
	peerAuth   bool      // asked for PEER_KEY pushes (peerkey.go)
	rttMs      uint16    // the player's own last round trip in milliseconds, from an optional HEARTBEAT body (display only)
	rttAt      time.Time // when it was reported; the zero time means none or unknown
	rooms      map[uint64]*room
	sendMu     sync.Mutex
}

type push struct {
	to *client
	t  byte
	p  []byte
}

func NewServer(maxRooms int, verbose bool) *Server {
	return &Server{
		log:      newLogger(os.Stderr, levelFor(verbose), "text"),
		clients:  map[uint32]*client{},
		rooms:    map[uint64]*room{},
		byCode:   map[string]*room{},
		scores:   store.NewMemory(),
		streams:  map[uint32]*stream{},
		maxRooms: maxRooms,
		started:  time.Now(),

		maxPlayerRooms: 8,
		maxPerAddr:     32,
		maxReqRate:     200,
		maxUDPRate:     defaultUDPRate,
		addrConns:      map[string]int{},
		stopped:        make(chan struct{}),
		owned:          map[*ownedConn]struct{}{},
	}
}

func (s *Server) Serve(ln net.Listener) error {
	s.lifeMu.Lock()
	if s.stopping || s.tcp != nil {
		s.lifeMu.Unlock()
		ln.Close()
		return net.ErrClosed
	}
	s.tcp, s.tcpReady = ln, true
	s.handlers.Add(1)
	s.lifeMu.Unlock()
	defer func() {
		s.lifeMu.Lock()
		s.tcpReady = false
		s.lifeMu.Unlock()
		s.handlers.Done()
	}()
	for {
		conn, err := ln.Accept()
		if err != nil {
			return err
		}
		s.lifeMu.Lock()
		if s.stopping {
			s.lifeMu.Unlock()
			conn.Close()
			return net.ErrClosed
		}
		owned := &ownedConn{Conn: conn, server: s}
		s.owned[owned] = struct{}{}
		s.handlers.Add(1)
		s.lifeMu.Unlock()
		go func() {
			defer s.handlers.Done()
			s.admit(owned)
		}()
	}
}

// admit works out who the client is, applies the per-address limit and runs
// the connection. It is a goroutine of its own because a PROXY header has to
// be read first, and a slow proxy must not stall Accept.
func (s *Server) admit(conn net.Conn) {
	peer := connAddr(conn)
	real := peer
	if s.proxyProto && s.trusted(peer) {
		conn.SetReadDeadline(time.Now().Add(proxyHeaderTimeout))
		a, ok, err := readProxyHeader(conn)
		if err != nil {
			// a trusted peer that sends none is a misconfigured proxy: close, never fall back to its address
			s.log.Warn("connection refused", "addr", peer.String(), "reason", "no valid PROXY header from a trusted proxy")
			conn.Close()
			return
		}
		if ok {
			real = a
		} // else a valid header with no client (health check): this connection stays under the proxy's address
	}
	host := real.String()
	s.mu.Lock()
	if s.maxPerAddr > 0 && s.addrConns[host] >= s.maxPerAddr {
		s.mu.Unlock()
		atomic.AddUint64(&s.refusedConns, 1)
		s.log.Warn("connection refused", "addr", host, "reason", "too many connections", "limit", s.maxPerAddr)
		conn.Close()
		return
	}
	s.addrConns[host]++
	s.nextID++
	s.conns++
	c := &client{id: s.nextID, conn: conn, rooms: map[uint64]*room{}}
	s.log.Debug("connected", "client", c.id, "addr", host)
	if real.Is4() {
		c.ip = real.As4()
	}
	s.clients[c.id] = c
	s.mu.Unlock()
	s.run(c)
	s.mu.Lock()
	if s.addrConns[host]--; s.addrConns[host] <= 0 {
		delete(s.addrConns, host)
	}
	s.mu.Unlock()
}

func (s *Server) run(c *client) {
	relayLeg := false // a relay stream connection, not a player
	defer func() {
		if relayLeg {
			return
		}
		c.conn.Close()
		s.mu.Lock()
		var out []push
		for _, r := range c.rooms {
			out = append(out, s.leave(r, c)...)
		}
		delete(s.clients, c.id)
		s.mu.Unlock()
		s.flush(out)
		s.log.Info("disconnected", "client", c.id, "online_id", c.onlineID)
	}()

	windowStart, inWindow := time.Now(), 0
	for first := true; ; first = false {
		c.conn.SetReadDeadline(time.Now().Add(clientTimeout))
		t, p, err := readPacket(c.conn)
		if err != nil {
			if err != io.EOF {
				s.log.Info("read failed", "client", c.id, "err", err)
			}
			return
		}
		if first && (t == msgStreamConnect || t == msgStreamAccept) {
			relayLeg = true
			s.mu.Lock()
			delete(s.clients, c.id)
			s.mu.Unlock()
			if !s.streamLeg(c.conn, t, p) {
				c.conn.Close()
			}
			return
		}
		if now := time.Now(); now.Sub(windowStart) >= time.Second {
			windowStart, inWindow = now, 0
		}
		if inWindow++; s.maxReqRate > 0 && inWindow > s.maxReqRate {
			atomic.AddUint64(&s.refusedReqRate, 1)
			s.log.Warn("connection closed", "client", c.id, "online_id", c.onlineID, "reason", "request rate", "limit", s.maxReqRate)
			return
		}
		s.handle(c, t, p)
	}
}

func (s *Server) handle(c *client, t byte, p []byte) {
	r := &reader{b: p}
	req := r.u32()
	if r.err != nil {
		return
	}
	if t != msgHello && c.commID == "" {
		s.log.Debug("request before hello", "client", c.id, "type", t)
		c.send(msgError, errReply(req, errNoHello))
		return
	}

	switch t {
	case msgRecordScore, msgGetRanking, msgGetRankingID:
		s.mu.Lock()
		who := scorer{c.commID, c.onlineID}
		s.mu.Unlock()
		c.send(s.scoreRequest(c, who, t, req, r))
		return
	}

	s.mu.Lock()
	var reply []byte
	var rt byte
	var out []push
	switch t {
	case msgHello:
		rt, reply = s.hello(c, req, r)
	case msgPeerAuth:
		rt, reply, out = s.peerAuth(c, req)
	case msgHeartbeat:
		// An optional body is the player's own last round trip: rtt_ms u16, 0xFFFF for unknown. It is reported by the
		// player, not measured here, and is only displayed. Servers before this ignored any body; a HEARTBEAT without
		// one changes nothing. Bytes after the first two are ignored.
		if len(r.b) >= 2 {
			if ms := r.u16(); ms <= maxReportedRTT {
				c.rttMs, c.rttAt = ms, time.Now()
			} else {
				c.rttAt = time.Time{}
			}
		}
		s.mu.Unlock()
		return
	case msgCreateRoom:
		rt, reply = s.createRoom(c, req, r)
	case msgSearchRooms:
		rt, reply = s.searchRooms(c, req, r)
	case msgJoinRoom:
		rt, reply, out = s.joinRoom(c, req, r)
	case msgLeaveRoom:
		rt, reply, out = s.leaveRoom(c, req, r)
	case msgSetRoomData:
		rt, reply, out = s.setRoomData(c, req, r)
	case msgRoomMessage:
		rt, reply, out = s.roomMessage(c, req, r)
	case msgKickMember:
		rt, reply, out = s.kick(c, req, r)
	case msgSetRoomAccess:
		rt, reply, out = s.setRoomAccess(c, req, r)
	case msgGetRoomAccess:
		rt, reply, out = s.getRoomAccess(c, req, r)
	case msgJoinByCode:
		rt, reply, out = s.joinByCode(c, req, r)
	default:
		s.log.Warn("unknown request type", "client", c.id, "online_id", c.onlineID, "type", t)
		rt, reply = msgError, errReply(req, errBadRequest)
	}
	s.mu.Unlock()

	c.send(rt, reply)
	s.flush(out)
}

func (s *Server) hello(c *client, req uint32, r *reader) (byte, []byte) {
	commID, onlineID, port := r.str(12), r.str(16), r.u16()
	if r.err != nil || commID == "" {
		return msgError, errReply(req, errBadRequest)
	}
	// One player per name, as PSN IDs are unique, compared case-insensitively
	// like them. The same name from the same address is the same player
	// reconnecting -- a title that crashed and restarted -- whose old
	// connection can linger until clientTimeout, so it gives way.
	for _, o := range s.clients {
		if o == c || o.commID == "" || !strings.EqualFold(o.onlineID, onlineID) {
			continue
		}
		if o.ip != c.ip {
			s.log.Warn("hello refused", "client", c.id, "reason", "name taken", "online_id", onlineID, "addr", ipString(c.ip))
			return msgError, errReply(req, errNameTaken)
		}
		s.log.Info("reconnected", "client", c.id, "replaces", o.id, "online_id", onlineID)
		o.commID = "" // no longer counts; its run loop cleans up once Close lands
		o.conn.Close()
	}
	c.commID, c.onlineID, c.p2pPort = commID, onlineID, port
	var tok [4]byte
	crand.Read(tok[:])
	c.token = binary.BigEndian.Uint32(tok[:]) | 1 // never 0
	s.log.Info("hello", "client", c.id, "comm_id", commID, "online_id", onlineID, "addr", ipString(c.ip), "p2p_port", port)
	w := &writer{}
	w.u32(req)
	w.u32(c.id)
	w.bytes(c.ip[:])
	w.u32(c.token)
	var flags uint8
	if s.relay {
		flags |= 1 // the relay is available
	}
	flags |= 2 // invite-only rooms (room codes)
	flags |= helloPeerKeys
	w.u8(flags)
	return msgHelloAck, w.b
}

func (s *Server) flush(out []push) {
	for _, m := range out {
		m.to.send(m.t, m.p)
	}
}

func (c *client) send(t byte, p []byte) {
	c.sendMu.Lock()
	defer c.sendMu.Unlock()
	if err := writePacket(c.conn, t, p); err != nil {
		c.conn.Close() // the reader goroutine sees it and cleans up
	}
}

func errReply(req uint32, code uint32) []byte {
	w := &writer{}
	w.u32(req)
	w.u32(code)
	return w.b
}

func okReply(req uint32) []byte {
	w := &writer{}
	w.u32(req)
	return w.b
}

func ipString(ip [4]byte) string { return net.IP(ip[:]).String() }

// refused logs a request the server turned down for a limit.
func (s *Server) refused(c *client, what, reason string) {
	s.log.Warn("request refused", "client", c.id, "online_id", c.onlineID, "request", what, "reason", reason)
}

// sortedRooms returns a title's rooms oldest first, so search pages are stable.
func (s *Server) sortedRooms(commID string) []*room {
	var out []*room
	for _, r := range s.rooms {
		if r.commID == commID {
			out = append(out, r)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].id < out[j].id })
	return out
}
