package client

// Typed messages and their payload layouts (docs/api.md). Request encoders
// write the fields after the u32 request id; reply decoders start after it.

import (
	"fmt"
	"net/netip"
)

// Hello is the HELLO request: who this player is and where it takes peer traffic.
type Hello struct {
	CommID   string // the title's NP communication ID, e.g. NPWR00860_00; 1-12 bytes
	OnlineID string // the player's name; 1-16 bytes
	P2PPort  uint16 // UDP port the title receives peer traffic on, or 0
}

func (h Hello) validate() error {
	switch {
	case h.CommID == "" || len(h.CommID) > commIDLen:
		return fmt.Errorf("comm id %q: want 1-%d bytes", h.CommID, commIDLen)
	case h.OnlineID == "" || len(h.OnlineID) > onlineIDLen:
		return fmt.Errorf("online id %q: want 1-%d bytes", h.OnlineID, onlineIDLen)
	}
	return nil
}

func (h Hello) encode(e *encoder) {
	e.str(h.CommID, commIDLen)
	e.str(h.OnlineID, onlineIDLen)
	e.u16(h.P2PPort)
}

// HELLO_ACK flag bits.
const (
	FlagRelay     = 1 << 0 // the server relays traffic (-relay)
	FlagRoomCodes = 1 << 1 // the server has invite-only rooms
	FlagPass      = 1 << 3 // the server wants AUTH before it treats this connection as a player (a hint only)
)

// HelloAck is the server's answer to HELLO.
type HelloAck struct {
	UserID   uint32
	PublicIP netip.Addr // the address the server sees this connection come from
	Token    uint32     // carried by UDP probes
	Flags    uint8
}

// Relay reports whether the server carries traffic for players who cannot reach each other.
func (a HelloAck) Relay() bool { return a.Flags&FlagRelay != 0 }

// RoomCodes reports whether the server has invite-only rooms.
func (a HelloAck) RoomCodes() bool { return a.Flags&FlagRoomCodes != 0 }

// PassRequired reports HELLO_ACK flag bit 3: the server wants a pass to connect (-require-pass connect). It is a hint
// from the server, not proof: a forged or older server can leave it clear or set it. See docs/api.md, Sign-in passes.
func (a HelloAck) PassRequired() bool { return a.Flags&FlagPass != 0 }

func decodeHelloAck(d *decoder) HelloAck {
	return HelloAck{UserID: d.u32(), PublicIP: netip.AddrFrom4(d.ip()), Token: d.u32(), Flags: d.u8()}
}

// decodeAuthReply reads the token only if exactly 4 bytes are there (a reply to AUTH outside connect mode has none).
func decodeAuthReply(d *decoder) AuthReply {
	if len(d.b) == 4 {
		return AuthReply{Token: d.u32(), HasToken: true}
	}
	return AuthReply{}
}

// Member is one room member entry. Addr is where this client should send the member's P2P traffic.
type Member struct {
	ID       uint16
	UserID   uint32
	OnlineID string
	Addr     netip.AddrPort
	Owner    bool
	Data     []byte
}

func decodeMember(d *decoder) Member {
	m := Member{ID: d.u16(), UserID: d.u32(), OnlineID: d.str(onlineIDLen)}
	ip, port := d.ip(), d.u16()
	m.Addr = netip.AddrPortFrom(netip.AddrFrom4(ip), port)
	m.Owner = d.u8() == 1
	m.Data = d.blob()
	return m
}

// CreateRoom is the CREATE_ROOM request.
type CreateRoom struct {
	Max        uint8  // member slots, at least 1
	Flags      uint32 // opaque to the server, shown to searchers
	External   []byte // shown to searchers
	Internal   []byte // shown to members only
	MemberData []byte // this player's own member data
}

func (r CreateRoom) validate() error {
	if r.Max == 0 {
		return fmt.Errorf("max members is 0")
	}
	return blobsFit(r.External, r.Internal, r.MemberData)
}

func (r CreateRoom) encode(e *encoder) {
	e.u8(r.Max)
	e.u32(r.Flags)
	e.blob(r.External)
	e.blob(r.Internal)
	e.blob(r.MemberData)
}

func blobsFit(blobs ...[]byte) error {
	for _, b := range blobs {
		if len(b) > maxRoomBlob {
			return fmt.Errorf("room data of %d bytes is over %d", len(b), maxRoomBlob)
		}
	}
	return nil
}

// Room is the ROOM_JOINED reply: the room as this client sees it after creating or joining.
type Room struct {
	ID       uint64
	MyMember uint16
	Owner    uint16
	Max      uint8
	Flags    uint32
	External []byte
	Internal []byte
	Members  []Member
	Routes   map[uint32]Route // by user id, from ROUTE pushes (only with -relay); see Apply
}

func decodeRoom(d *decoder) Room {
	r := Room{ID: d.u64(), MyMember: d.u16(), Owner: d.u16(), Max: d.u8(), Flags: d.u32()}
	r.External, r.Internal = d.blob(), d.blob()
	n := int(d.u8())
	for i := 0; i < n && d.err == nil; i++ {
		r.Members = append(r.Members, decodeMember(d))
	}
	return r
}

// RoomList is the ROOM_LIST reply to SEARCH_ROOMS.
type RoomList struct {
	Total int // open rooms of this title, all pages
	Rooms []RoomInfo
}

// RoomInfo is one ROOM_LIST entry.
type RoomInfo struct {
	ID       uint64
	Owner    string // the owner's online ID
	Cur, Max uint8
	Flags    uint32
	External []byte
}

func decodeRoomList(d *decoder) RoomList {
	l := RoomList{Total: int(d.u16())}
	n := int(d.u16())
	for i := 0; i < n && d.err == nil; i++ {
		ri := RoomInfo{ID: d.u64(), Owner: d.str(onlineIDLen), Cur: d.u8(), Max: d.u8(), Flags: d.u32()}
		ri.External = d.blob()
		l.Rooms = append(l.Rooms, ri)
	}
	return l
}

// Score order: which way a board ranks. The first RECORD_SCORE on a board sets it.
type Order uint8

const (
	HigherIsBetter Order = 0
	LowerIsBetter  Order = 1
)

// Score is the RECORD_SCORE request.
type Score struct {
	Board   uint32
	Order   Order
	Value   int64
	Comment []byte // at most 64 bytes
}

func (s Score) encode(e *encoder) {
	e.u32(s.Board)
	e.u8(uint8(s.Order))
	e.u64(uint64(s.Value))
	e.blob(s.Comment)
}

// Ranking is the RANKING reply.
type Ranking struct {
	Total int // players on the board
	Rows  []RankRow
}

// RankRow is one RANKING entry. Rank 0 means the player has no score (GET_RANKING_BY_ID only).
type RankRow struct {
	Rank     uint32
	OnlineID string
	Score    int64
	Comment  []byte
	Recorded int64 // unix seconds
}

func decodeRanking(d *decoder) Ranking {
	k := Ranking{Total: int(d.u32())}
	n := int(d.u16())
	for i := 0; i < n && d.err == nil; i++ {
		row := RankRow{Rank: d.u32(), OnlineID: d.str(onlineIDLen), Score: int64(d.u64())}
		row.Comment = d.blob()
		row.Recorded = int64(d.u64())
		k.Rows = append(k.Rows, row)
	}
	return k
}

// Push is a server-initiated event. It is one of *MemberJoined, *MemberLeft,
// *Route, or *RawPush for the push types this package does not decode.
type Push interface{ push() }

// MemberJoined (0xA1): a member joined a room this client is in.
type MemberJoined struct {
	Room   uint64
	Member Member
}

// MemberLeft (0xA2): a member left; Owner is the owner after the leave.
type MemberLeft struct {
	Room   uint64
	Member uint16
	Owner  uint16
}

// Route (0xA7, only with -relay): how to reach a member. Bit 0 of Flags set
// means streams to that player go through the relay.
type Route struct {
	Room   uint64
	UserID uint32
	Flags  uint8
}

// StreamRelay reports whether streams to the member go through the relay.
func (r Route) StreamRelay() bool { return r.Flags&1 != 0 }

// RawPush is a push this package does not decode (ROOM_DATA, ROOM_MSG, KICKED, STREAM_OFFER).
type RawPush struct {
	Type    byte
	Payload []byte
}

func (*MemberJoined) push() {}
func (*MemberLeft) push()   {}
func (*Route) push()        {}
func (*RawPush) push()      {}

func decodePush(t byte, p []byte) (Push, error) {
	d := &decoder{b: p}
	var out Push
	switch t {
	case typeMemberJoined:
		out = &MemberJoined{Room: d.u64(), Member: decodeMember(d)}
	case typeMemberLeft:
		out = &MemberLeft{Room: d.u64(), Member: d.u16(), Owner: d.u16()}
	case typeRoute:
		out = &Route{Room: d.u64(), UserID: d.u32(), Flags: d.u8()}
	default:
		return &RawPush{Type: t, Payload: append([]byte(nil), p...)}, nil
	}
	if d.err != nil {
		return nil, fmt.Errorf("push type 0x%02X: %w", t, d.err)
	}
	return out, nil
}

// Apply updates r with a push about it: a member joining or leaving, or a
// route. It reports whether the push was about r.
func (r *Room) Apply(p Push) bool {
	switch ev := p.(type) {
	case *MemberJoined:
		if ev.Room != r.ID {
			return false
		}
		r.Members = append(r.Members, ev.Member)
	case *MemberLeft:
		if ev.Room != r.ID {
			return false
		}
		r.Owner = ev.Owner
		kept := r.Members[:0]
		for _, m := range r.Members {
			if m.ID != ev.Member {
				m.Owner = m.ID == ev.Owner
				kept = append(kept, m)
			}
		}
		r.Members = kept
	case *Route:
		if ev.Room != r.ID {
			return false
		}
		if r.Routes == nil {
			r.Routes = map[uint32]Route{}
		}
		r.Routes[ev.UserID] = *ev
	default:
		return false
	}
	return true
}
