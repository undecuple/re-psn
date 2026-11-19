package main

// One reader and one lazy writer per player connection, one mutex over room
// state (the score store has its own lock; scores.go). Replies and pushes are
// queued under the state lock; only the writer writes control frames, outside it.

import (
	crand "crypto/rand"
	"encoding/binary"
	"errors"
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

// writeTimeout bounds one queued frame write to a client.
const writeTimeout = 10 * time.Second

// defaultMaxConns is the default for -max-conns: a bound on file descriptors, high enough that a normal server never meets it.
const defaultMaxConns = 4096

// Defaults for -pass-max-unauth-conns and -pass-max-unauth-per-addr; read only under -require-pass connect.
const (
	defaultMaxUnauth        = 64
	defaultMaxUnauthPerAddr = 4
)

// defaultRelayIdle is the default for -relay-idle, in seconds.
const defaultRelayIdle = 10 * time.Minute

// A reported round trip is shown while it is fresh, and values above maxReportedRTT (including 0xFFFF, "unknown")
// clear it.
const (
	maxReportedRTT = 60000 // milliseconds
	rttFresh       = 3 * time.Minute
)

type Server struct {
	log                *slog.Logger
	relay              bool                       // -relay: carry traffic for players who can't reach each other (relay.go)
	relayForce         bool                       // -relay-force: with relay, send every stream through it (a test aid)
	pass               atomic.Pointer[passConfig] // sign-in pass settings; nil when no -pass-key-file is set (pass.go)
	requireRelayPass   bool                       // -require-pass relay: the relay carries traffic only for players with a relay pass
	requireConnectPass bool                       // -require-pass connect: a connection is a player only after AUTH (pass.go)
	maxUnauth          int                        // -pass-max-unauth-conns: connections not yet signed in, in total (0: no limit)
	maxUnauthPerAddr   int                        // -pass-max-unauth-per-addr: the same per remote address (0: no limit)
	acceptHook         func()                     // a test aid: runs when an accepting leg has taken its stream from the table, before it answers (relay.go)
	authTimeout        time.Duration              // how long after accept a connection has to sign in
	maxPerSub          int                        // -pass-max-conns-per-sub: connections one account may hold (0 for no limit)
	subConns           map[string]int             // connections per account, under mu
	noPeer             map[string]bool            // -no-peer-titles: communication IDs of titles that use no peer traffic (title_peer.go)
	relayIdle          time.Duration              // -relay-idle: close a relay stream with no bytes either way for this long (0: never)
	lanPublic          [4]byte                    // -lan-public-addr: address announced for LAN members to viewers outside the LAN (zero: off)

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

	statsCache *snapshotCache // the status page and /api/stats share one snapshot per second (api.go); nil: every request builds its own

	maxPlayerRooms int                    // rooms one player may be in at once
	maxConns       int                    // open connections in total, players and relay legs (0: no limit)
	maxPerAddr     int                    // open connections per remote address
	maxReqRate     int                    // requests per second per connection
	addrConns      map[string]int         // open connections by remote host
	addrMisses     map[string]*addrMisses // wrong room codes by remote host and window
	openConns      int                    // open connections in total
	lastConnsLog   time.Time              // when a refusal for -max-conns was last logged
	connsLogSkip   int                    // refusals for -max-conns not logged since then
	unauthConns    int                    // connections not yet signed in, in total (connect mode)
	unauthByAddr   map[string]int         // the same by remote address, keyed as addrConns
	lastUnauthLog  time.Time              // when a refusal for the sign-in limits was last logged
	unauthLogSkip  int                    // such refusals not logged since then

	discover bool   // -discover: answer LAN DISCOVER packets (discover.go)
	name     string // -name: shown in discovery, at most 24 bytes
	tcpPort  uint16 // the game TCP port, for discovery replies

	udpAnySource   bool // accept UDP probes and relay packets from an address other than the player's TCP address (udp.go)
	maxUDPRate     int  // datagrams per second per source IP; zero disables admission limiting
	udpDrops       udpDropCounters
	trustedProxies []netip.Prefix // peers allowed to name the real client (proxy.go)
	proxyProto     bool           // require a PROXY header from a trusted peer

	streams    map[uint32]*stream       // relay streams waiting for their target to accept
	live       map[*liveStream]struct{} // relay streams from their offer to the end of their splice (relay.go)
	nextStream uint32
	relayed    uint64 // bytes carried by the relay (atomic)

	refusedConns   uint64 // connections refused for too many from one address (atomic)
	refusedTotal   uint64 // connections refused because -max-conns were open (atomic)
	refusedReqRate uint64 // connections closed for sending requests too fast (atomic)
}

type client struct {
	id       uint32
	conn     net.Conn
	commID   string // NP communication ID, e.g. NPWR00860_00: the title
	onlineID string
	ip       [4]byte    // as the server sees it; all zero unless IPv4 (HELLO_ACK, UDP)
	addr     netip.Addr // the real remote address, IPv4 or IPv6, unmapped, zone stripped: what a name is held against
	p2pPort  uint16     // UDP port the title receives peer traffic on

	codeMisses int       // wrong or malformed room codes in the current window
	codeWindow time.Time // start of that window
	token      uint32    // from HELLO_ACK; UDP probes must carry it
	udpIP      [4]byte   // P2P socket's public endpoint, from its UDP probe
	udpWarned  bool      // a probe from another address was already logged for this player
	udpPort    uint16
	localIP    [4]byte   // the client's own address, from its UDP probe
	peerAuth   bool      // asked for PEER_KEY pushes (peerkey.go)
	sub        string    // the account of an accepted pass, counted in subConns (pass.go)
	passScopes uint8     // scopes of the accepted pass
	passExp    int64     // its expiry, Unix seconds
	passKid    string    // the key that signed it
	authFails  int       // refused AUTH requests on this connection; the connection is closed after maxAuthFails
	rttMs      uint16    // the player's own last round trip in milliseconds, from an optional HEARTBEAT body (display only)
	rttAt      time.Time // when it was reported; the zero time means none or unknown

	// Connect mode (-require-pass connect). A HELLO is parked in pend* until AUTH registers it, so commID stays empty and
	// every "registered" check keeps meaning what it meant. helloSeen and authed are under s.mu. The rest is only touched
	// by the connection's own goroutine (admit, run, handle, handleAuth), so it needs no lock.
	helloSeen  bool
	authed     bool
	pendComm   string
	pendOnline string
	pendPort   uint16
	unauth     bool      // counted in unauthConns until it signs in or goes away; written under s.mu
	unauthKey  string    // its key in unauthByAddr
	authBy     time.Time // sign in before this, counted from accept
	gateUntil  time.Time // close the connection at this time unless a refresh AUTH moved it; zero: never
	rooms      map[uint64]*room
	output     clientOutput
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
		live:     map[*liveStream]struct{}{},
		subConns: map[string]int{},
		maxRooms: maxRooms,

		authTimeout:      defaultAuthTimeout,
		maxUnauth:        defaultMaxUnauth,
		maxUnauthPerAddr: defaultMaxUnauthPerAddr,
		unauthByAddr:     map[string]int{},
		started:          time.Now(),

		maxPlayerRooms: 8,
		maxConns:       defaultMaxConns,
		relayIdle:      defaultRelayIdle,
		maxPerAddr:     32,
		maxReqRate:     200,
		maxUDPRate:     defaultUDPRate,
		addrConns:      map[string]int{},
		addrMisses:     map[string]*addrMisses{},
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

// addrLimitKey is what the per-address connection limit counts by: an IPv4 address (an IPv4-mapped one counts as that IPv4 address),
// or an IPv6 address cut to its /64, as the UDP guard and the admin failure table already do. One IPv6 network is one source, so
// changing the low 64 bits does not give a fresh allowance. The address the player is known by is not changed.
func addrLimitKey(a netip.Addr) string {
	a = a.Unmap().WithZone("")
	if a.Is6() {
		p, _ := a.Prefix(64)
		return p.String()
	}
	return a.String()
}

// admit works out who the client is, applies the per-address limit and runs
// the connection. It is a goroutine of its own because a PROXY header has to
// be read first, and a slow proxy must not stall Accept.
func (s *Server) admit(conn net.Conn) {
	accepted := time.Now() // the sign-in time (connect mode) counts from here, not from the PROXY header or HELLO
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
	limitKey := addrLimitKey(real)
	s.mu.Lock()
	if s.maxPerAddr > 0 && s.addrConns[limitKey] >= s.maxPerAddr {
		s.mu.Unlock()
		atomic.AddUint64(&s.refusedConns, 1)
		s.log.Warn("connection refused", "addr", host, "reason", "too many connections", "limit", s.maxPerAddr)
		conn.Close()
		return
	}
	if s.maxConns > 0 && s.openConns >= s.maxConns {
		// a flood must not also flood the log: one line a second, with the number left out in between
		var skipped int
		logIt := time.Since(s.lastConnsLog) >= time.Second
		if logIt {
			s.lastConnsLog, skipped, s.connsLogSkip = time.Now(), s.connsLogSkip, 0
		} else {
			s.connsLogSkip++
		}
		s.mu.Unlock()
		atomic.AddUint64(&s.refusedTotal, 1)
		if logIt {
			s.log.Warn("connection refused", "reason", "too many connections in total", "limit", s.maxConns, "refused_since_last_line", skipped)
		}
		conn.Close()
		return
	}
	if s.requireConnectPass && ((s.maxUnauthPerAddr > 0 && s.unauthByAddr[limitKey] >= s.maxUnauthPerAddr) || (s.maxUnauth > 0 && s.unauthConns >= s.maxUnauth)) {
		var skipped int
		logIt := time.Since(s.lastUnauthLog) >= time.Second
		if logIt {
			s.lastUnauthLog, skipped, s.unauthLogSkip = time.Now(), s.unauthLogSkip, 0
		} else {
			s.unauthLogSkip++
		}
		s.mu.Unlock()
		if logIt {
			s.log.Warn("connection refused", "addr", host, "reason", "too many connections not signed in", "refused_since_last_line", skipped)
		}
		conn.Close()
		return
	}
	s.addrConns[limitKey]++
	s.openConns++
	s.nextID++
	s.conns++
	c := &client{id: s.nextID, conn: conn, addr: real.Unmap().WithZone(""), rooms: map[uint64]*room{}}
	if s.requireConnectPass {
		s.unauthByAddr[limitKey]++
		s.unauthConns++
		c.unauth, c.unauthKey, c.authBy = true, limitKey, accepted.Add(s.authTimeout)
	}
	s.log.Debug("connected", "client", c.id, "addr", host)
	if real.Is4() {
		c.ip = real.As4()
	}
	if !s.requireConnectPass { // in connect mode the client is listed when AUTH accepts it: no id lookup finds it before
		s.clients[c.id] = c
	}
	s.mu.Unlock()
	s.run(c)
	s.mu.Lock()
	s.releaseUnauth(c)
	s.openConns--
	if s.addrConns[limitKey]--; s.addrConns[limitKey] <= 0 {
		delete(s.addrConns, limitKey)
	}
	s.mu.Unlock()
}

// releaseUnauth stops counting c as a connection that has not signed in. Safe to call twice. The caller holds s.mu.
func (s *Server) releaseUnauth(c *client) {
	if !c.unauth {
		return
	}
	c.unauth = false
	if s.unauthByAddr[c.unauthKey]--; s.unauthByAddr[c.unauthKey] <= 0 {
		delete(s.unauthByAddr, c.unauthKey)
	}
	s.unauthConns--
}

func (s *Server) run(c *client) {
	relayLeg := false // a relay stream connection, not a player
	defer func() {
		if relayLeg {
			return
		}
		c.stopOutput()
		s.mu.Lock()
		var out []push
		for _, r := range c.rooms {
			out = append(out, s.leave(r, c)...)
		}
		delete(s.clients, c.id)
		s.dropSub(c)
		s.flush(out)
		s.mu.Unlock()
		c.waitOutput()
		s.log.Info("disconnected", "client", c.id, "online_id", c.onlineID)
	}()

	windowStart, inWindow := time.Now(), 0
	for first := true; ; first = false {
		dl, why := time.Now().Add(clientTimeout), ""
		if c.unauth && c.authBy.Before(dl) { // absolute: a HEARTBEAT only brings the loop back here, it does not move this
			dl, why = c.authBy, "no sign-in in time"
		} else if !c.gateUntil.IsZero() && c.gateUntil.Before(dl) {
			dl, why = c.gateUntil, "pass expired"
		}
		c.conn.SetReadDeadline(dl)
		limit := maxPayload
		if c.unauth {
			limit = maxUnauthFrame
		}
		t, p, err := readPacketLimit(c.conn, limit)
		if err != nil {
			var ne net.Error
			switch {
			case errors.Is(err, errFrameTooLong):
				s.log.Warn("connection closed", "client", c.id, "reason", "frame too long before sign-in", "limit", limit)
			case why != "" && errors.As(err, &ne) && ne.Timeout():
				s.log.Warn("connection closed", "client", c.id, "online_id", c.onlineID, "reason", why)
			case err != io.EOF:
				s.log.Info("read failed", "client", c.id, "err", err)
			}
			return
		}
		if first && (t == msgStreamConnect || t == msgStreamAccept) {
			relayLeg = true
			s.mu.Lock()
			delete(s.clients, c.id)
			s.releaseUnauth(c) // a leg can splice for hours and is not waiting to sign in
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
	// commID is cleared under s.mu by another client's HELLO (or AUTH, in connect mode) that takes over this player's name, so it is read under s.mu too.
	s.mu.Lock()
	noHello := c.commID == "" && !c.helloSeen
	gated := s.requireConnectPass && !c.authed
	closed := c.outputClosed()
	s.mu.Unlock()
	if closed {
		return
	}
	if t != msgHello && noHello {
		s.log.Debug("request before hello", "client", c.id, "type", t)
		c.send(msgError, errReply(req, errNoHello))
		return
	}
	if gated { // connect mode, not signed in: HELLO (parks its fields), AUTH, HEARTBEAT and the pass bits of GET_CAPS only
		switch t {
		case msgHello:
		case msgAuth:
			s.handleAuth(c, req, r)
			return
		case msgHeartbeat: // keeps nothing alive: the sign-in deadline is absolute
			return
		case msgGetCaps:
			s.mu.Lock()
			if !c.outputClosed() {
				w := &writer{}
				w.u32(req)
				w.u32(s.preAuthCaps(c))
				w.blob(nil) // no version text before sign-in: the pass bits only
				c.send(msgCaps, w.b)
			}
			s.mu.Unlock()
			return
		default: // every other type, defined or not, so a probe cannot tell them apart
			s.log.Debug("request before sign-in", "client", c.id, "type", t)
			c.send(msgError, errReply(req, errPass))
			return
		}
	}

	switch t {
	case msgRecordScore, msgGetRanking, msgGetRankingID:
		s.mu.Lock()
		who := scorer{c.commID, c.onlineID}
		s.mu.Unlock()
		c.send(s.scoreRequest(c, who, t, req, r))
		return
	}

	if t == msgAuth {
		s.handleAuth(c, req, r)
		return
	}

	s.mu.Lock()
	if c.outputClosed() {
		s.mu.Unlock()
		return
	}
	var reply []byte
	var rt byte
	var out []push
	switch t {
	case msgHello:
		rt, reply = s.hello(c, req, r)
	case msgPeerAuth:
		rt, reply, out = s.peerAuth(c, req)
	case msgGetCaps:
		rt, reply = s.getCaps(c, req)
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
	c.send(rt, reply)
	s.flush(out)
	s.mu.Unlock()
}

// hello handles HELLO. Without connect mode it registers the player. With it the fields are only parked on the connection and
// nothing is reserved, listed or issued until AUTH accepts a pass (handleAuth), so an unauthenticated connection cannot hold a
// name.
func (s *Server) hello(c *client, req uint32, r *reader) (byte, []byte) {
	if c.commID != "" || c.helloSeen { // HELLO is sent once per connection; a second one changes nothing
		return msgError, errReply(req, errBadRequest)
	}
	commID, onlineID, port := r.str(12), r.str(16), r.u16()
	if r.err != nil || commID == "" {
		return msgError, errReply(req, errBadRequest)
	}
	if s.requireConnectPass {
		c.helloSeen, c.pendComm, c.pendOnline, c.pendPort = true, commID, onlineID, port
		s.log.Debug("hello parked until AUTH", "client", c.id)
		return s.helloAck(c, req, commID, 0, helloPassConnect)
	}
	return s.register(c, commID, onlineID, port, req)
}

// register makes c the player onlineID for the title commID: the name check, a takeover, the token and the HELLO_ACK.
func (s *Server) register(c *client, commID, onlineID string, port uint16, req uint32) (byte, []byte) {
	victims, taken := s.nameConflict(c, onlineID)
	if taken {
		s.log.Warn("hello refused", "client", c.id, "reason", "name taken", "online_id", onlineID, "addr", c.addr.String())
		return msgError, errReply(req, errNameTaken)
	}
	s.takeOver(victims, c)
	c.commID, c.onlineID, c.p2pPort = commID, onlineID, port
	s.newToken(c)
	s.log.Info("hello", "client", c.id, "comm_id", commID, "online_id", onlineID, "addr", c.addr.String(), "p2p_port", port)
	return s.helloAck(c, req, commID, c.token, 0)
}

// nameConflict looks for other players with the name onlineID. One player per name, as PSN IDs are unique, compared
// case-insensitively like them. The same name from the same address is the same player reconnecting -- a title that crashed
// and restarted -- whose old connection can linger until clientTimeout, so it gives way: those are victims. A name held from
// another address is taken. It changes nothing. The caller holds s.mu.
func (s *Server) nameConflict(c *client, onlineID string) (victims []*client, taken bool) {
	for _, o := range s.clients {
		if o == c || o.commID == "" || !strings.EqualFold(o.onlineID, onlineID) {
			continue
		}
		if !sameAddr(o.addr, c.addr) {
			return nil, true
		}
		victims = append(victims, o)
	}
	return victims, false
}

// takeOver retires the connections c replaces. The caller holds s.mu.
func (s *Server) takeOver(victims []*client, c *client) {
	for _, o := range victims {
		s.log.Info("reconnected", "client", c.id, "replaces", o.id, "online_id", o.onlineID)
		o.commID = "" // no longer counts; its run loop cleans up once Close lands
		o.helloSeen, o.authed = false, false
		s.dropSub(o) // and its account no longer counts against the limit
		o.stopOutput()
	}
}

// newToken gives c its UDP token, never 0.
func (s *Server) newToken(c *client) {
	var tok [4]byte
	crand.Read(tok[:])
	c.token = binary.BigEndian.Uint32(tok[:]) | 1
}

// helloAck is the HELLO_ACK for c; token is 0 while c has not signed in. extra is OR-ed into the flags.
func (s *Server) helloAck(c *client, req uint32, commID string, token uint32, extra uint8) (byte, []byte) {
	w := &writer{}
	w.u32(req)
	w.u32(c.id)
	w.bytes(c.ip[:])
	w.u32(token)
	flags := extra
	if s.relay && !s.noPeer[commID] {
		flags |= 1 // the relay is available
	}
	flags |= 2 // invite-only rooms (room codes)
	flags |= helloPeerKeys
	w.u8(flags)
	return msgHelloAck, w.b
}

// flush queues pushes while s.mu is held, preserving state-transition order.
func (s *Server) flush(out []push) {
	for _, m := range out {
		m.to.send(m.t, m.p)
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

// sameAddr reports whether a and b are the same known address. An invalid
// address is never the same as anything, so a connection whose address is
// unknown can never take over another player's name.
func sameAddr(a, b netip.Addr) bool {
	return a.IsValid() && a.Unmap().WithZone("") == b.Unmap().WithZone("")
}

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
