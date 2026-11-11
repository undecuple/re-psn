package main

// Matching2-style rooms. A room belongs to one title (communication ID) and
// holds up to max members. Members are numbered from 1 in join order, the way
// Matching2 room member IDs are, and the owner is a member ID. Every join or
// leave pushes to the members who stay, and each member entry carries the
// peer's address and P2P port: that is what the client's signaling layer
// connects to. The server never carries game traffic.

import (
	crand "crypto/rand"
	"crypto/subtle"
	"encoding/binary"
	"time"
)

// maxRoomBlob caps each blob a client puts in a room: attributes, member data
// and room messages. Matching2 attributes are small; game files do not belong
// on the wire.
const maxRoomBlob = 4096

func tooBig(blobs ...[]byte) bool {
	for _, b := range blobs {
		if len(b) > maxRoomBlob {
			return true
		}
	}
	return false
}

type room struct {
	id         uint64
	commID     string
	max        uint8
	flags      uint32
	external   []byte // visible to searchers (Matching2 "external" bin attrs)
	internal   []byte // visible to members only
	owner      uint16
	code       string // invite-only: the 8-character room code; "" for a public room
	members    []*member
	nextMember uint16
	created    time.Time
	srv        *Server
}

type member struct {
	id   uint16
	c    *client
	data []byte // the member's own data (Matching2 member bin attrs), set at join
}

// Member entry on the wire:
// member_id u16 | user_id u32 | online_id [16] | ip [4] | p2p_port u16 | owner u8 | data blob
// ip and p2p_port are where `viewer` should send m's traffic (see endpoint).
func (r *room) putMember(w *writer, m *member, viewer *client) {
	w.u16(m.id)
	w.u32(m.c.id)
	w.str(m.c.onlineID, 16)
	ip, port := r.srv.endpoint(m.c, viewer)
	w.bytes(ip[:])
	w.u16(port)
	if m.id == r.owner {
		w.u8(1)
	} else {
		w.u8(0)
	}
	w.blob(m.data)
}

func (r *room) memberOf(c *client) *member {
	for _, m := range r.members {
		if m.c == c {
			return m
		}
	}
	return nil
}

// ROOM_JOINED: req | room_id u64 | my_member u16 | owner u16 | max u8 |
// flags u32 | external blob | internal blob | count u8 | members...
func (r *room) joinedReply(req uint32, me *member) []byte {
	w := &writer{}
	w.u32(req)
	w.u64(r.id)
	w.u16(me.id)
	w.u16(r.owner)
	w.u8(r.max)
	w.u32(r.flags)
	w.blob(r.external)
	w.blob(r.internal)
	w.u8(uint8(len(r.members)))
	for _, m := range r.members {
		r.putMember(w, m, me.c)
	}
	return w.b
}

// Room access (docs/room-codes.md). A public room is listed in search; an invite-only room carries a code,
// is listed nowhere (search, the status page) and is joined with JOIN_BY_CODE. The code is a bearer secret:
// it is never logged.
const (
	accessPublic = 0
	accessCode   = 1

	codeLen        = 8
	codeAlphabet   = "0123456789ABCDEFGHJKMNPQRSTVWXYZ" // 32 symbols, no I L O U: 5 bits each, 40 bits a code
	maxCodeMisses  = 5                                  // wrong codes a connection may send per window
	codeMissWindow = time.Minute
)

// newCode returns an unused code from crypto/rand, or an error if the system's random source fails.
func (s *Server) newCode() (string, error) {
	for {
		var b [codeLen]byte
		if _, err := crand.Read(b[:]); err != nil {
			return "", err
		}
		for i := range b {
			b[i] = codeAlphabet[b[i]&31]
		}
		if code := string(b[:]); s.byCode[code] == nil {
			return code, nil
		}
	}
}

// normalizeCode accepts a typed code: case-insensitive, with O read as 0 and I or L as 1.
func normalizeCode(in string) (string, bool) {
	if len(in) != codeLen {
		return "", false
	}
	b := []byte(in)
	for i, c := range b {
		switch {
		case c >= 'a' && c <= 'z':
			c -= 'a' - 'A'
		}
		switch c {
		case 'O':
			c = '0'
		case 'I', 'L':
			c = '1'
		}
		ok := false
		for j := 0; j < len(codeAlphabet); j++ {
			if codeAlphabet[j] == c {
				ok = true
			}
		}
		if !ok {
			return "", false
		}
		b[i] = c
	}
	return string(b), true
}

// roomByCode compares the code with every code in use and does not stop at a match.
func (s *Server) roomByCode(code string) *room {
	var found *room
	for k, r := range s.byCode {
		if subtle.ConstantTimeCompare([]byte(k), []byte(code)) == 1 {
			found = r
		}
	}
	return found
}

// codeMiss counts a wrong or malformed code against the connection; too many in a window close it.
func (s *Server) codeMiss(c *client) {
	if now := time.Now(); now.Sub(c.codeWindow) > codeMissWindow {
		c.codeWindow, c.codeMisses = now, 0
	}
	if c.codeMisses++; c.codeMisses >= maxCodeMisses {
		s.log.Warn("connection closed", "client", c.id, "online_id", c.onlineID, "reason", "wrong room codes", "limit", maxCodeMisses)
		c.conn.Close()
		return
	}
	s.log.Warn("room code refused", "client", c.id, "online_id", c.onlineID)
}

func (s *Server) addMember(r *room, c *client, data []byte) *member {
	r.nextMember++
	m := &member{id: r.nextMember, c: c, data: data}
	r.members = append(r.members, m)
	c.rooms[r.id] = r
	return m
}

func (s *Server) createRoom(c *client, req uint32, rd *reader) (byte, []byte) {
	max, flags, ext, in, mine := rd.u8(), rd.u32(), rd.blob(), rd.blob(), rd.blob()
	if rd.err != nil || max == 0 || tooBig(ext, in, mine) {
		if tooBig(ext, in, mine) {
			s.refused(c, "create room", "blob over limit")
		}
		return msgError, errReply(req, errBadRequest)
	}
	if len(s.rooms) >= s.maxRooms || s.maxPlayerRooms > 0 && len(c.rooms) >= s.maxPlayerRooms {
		s.refused(c, "create room", "room limit")
		return msgError, errReply(req, errLimitReached)
	}
	s.nextRoom++
	r := &room{id: s.nextRoom, commID: c.commID, max: max, flags: flags,
		external: ext, internal: in, created: time.Now(), srv: s}
	s.rooms[r.id] = r
	me := s.addMember(r, c, mine)
	r.owner = me.id
	s.log.Debug("room created", "room", r.id, "online_id", c.onlineID, "comm_id", c.commID, "max", max)
	return msgRoomJoined, r.joinedReply(req, me)
}

// ROOM_LIST: req | total u16 | count u16 | entries...
// entry: room_id u64 | owner online_id [16] | cur u8 | max u8 | flags u32 | external blob
//
// The reply must fit one packet (maxPayload). Entries are added in order while they fit, and count is the number
// written. total is still every open room in the title, so a caller that sees count < total asks again with
// start = count. A room whose entry cannot fit even alone goes out with an empty external blob instead, so
// the list always makes progress; the caller then sees that room's external data as empty.
func (s *Server) searchRooms(c *client, req uint32, rd *reader) (byte, []byte) {
	start, max := rd.u16(), rd.u16()
	if rd.err != nil {
		return msgError, errReply(req, errBadRequest)
	}
	var open []*room
	for _, r := range s.sortedRooms(c.commID) {
		if len(r.members) < int(r.max) && r.code == "" {
			open = append(open, r)
		}
	}
	page := open[min(int(start), len(open)):]
	page = page[:min(int(max), len(page))]

	var entries writer
	size := searchHeaderLen // req | total | count, as the reply is sent
	count := 0
	for _, r := range page {
		ext := r.external
		if size+roomEntryFixedLen+len(ext) > maxPayload {
			if count > 0 {
				break
			}
			ext = nil // alone it does not fit: send the room without its external data
		}
		size += roomEntryFixedLen + len(ext)
		entries.u64(r.id)
		owner := ""
		for _, m := range r.members {
			if m.id == r.owner {
				owner = m.c.onlineID
			}
		}
		entries.str(owner, 16)
		entries.u8(uint8(len(r.members)))
		entries.u8(r.max)
		entries.u32(r.flags)
		entries.blob(ext)
		count++
	}

	w := &writer{}
	w.u32(req)
	w.u16(uint16(len(open)))
	w.u16(uint16(count))
	w.bytes(entries.b)
	return msgRoomList, w.b
}

const (
	searchHeaderLen   = 4 + 2 + 2              // req u32 | total u16 | count u16
	roomEntryFixedLen = 8 + 16 + 1 + 1 + 4 + 2 // room_id | owner [16] | cur | max | flags | external blob length
)

func (s *Server) joinRoom(c *client, req uint32, rd *reader) (byte, []byte, []push) {
	id, mine := rd.u64(), rd.blob()
	r, ok := s.rooms[id]
	if tooBig(mine) {
		s.refused(c, "join room", "blob over limit")
	}
	switch {
	case rd.err != nil || tooBig(mine):
		return msgError, errReply(req, errBadRequest), nil
	case !ok || r.commID != c.commID:
		return msgError, errReply(req, errNotFound), nil
	case r.code != "" && r.memberOf(c) == nil:
		return msgError, errReply(req, errNotFound), nil // invite-only: not joinable by room ID
	}
	return s.joinMember(c, req, r, mine)
}

// joinMember adds c to r, or says why not. The caller has checked the room exists, is c's title and may be joined.
func (s *Server) joinMember(c *client, req uint32, r *room, mine []byte) (byte, []byte, []push) {
	switch {
	case r.memberOf(c) != nil:
		return msgRoomJoined, r.joinedReply(req, r.memberOf(c)), nil
	case len(r.members) >= int(r.max):
		return msgError, errReply(req, errRoomFull), nil
	case s.maxPlayerRooms > 0 && len(c.rooms) >= s.maxPlayerRooms:
		s.refused(c, "join room", "room limit")
		return msgError, errReply(req, errLimitReached), nil
	}
	me := s.addMember(r, c, mine)

	var out []push
	for _, m := range r.members {
		if m != me {
			w := &writer{}
			w.u64(r.id)
			r.putMember(w, me, m.c)
			out = append(out, push{m.c, msgMemberJoined, w.b})
		}
	}
	if s.relay { // which of them each side must reach through the relay
		for _, m := range r.members {
			if m != me {
				out = append(out, s.routePush(r, m.c, c), s.routePush(r, c, m.c))
			}
		}
	}
	for _, m := range r.members {
		if m != me {
			out = append(out, s.peerKeyPushes(r, c, m.c)...)
		}
	}
	s.log.Debug("room joined", "room", r.id, "online_id", c.onlineID, "member", me.id)
	return msgRoomJoined, r.joinedReply(req, me), out
}

func (s *Server) leaveRoom(c *client, req uint32, rd *reader) (byte, []byte, []push) {
	r, ok := s.rooms[rd.u64()]
	if rd.err != nil || !ok || r.memberOf(c) == nil {
		return msgError, errReply(req, errNotInRoom), nil
	}
	return msgOK, okReply(req), s.leave(r, c)
}

// leave removes c from r and returns the MEMBER_LEFT pushes for those who
// stay. The owner leaving hands the room to the longest-standing member;
// the last one out closes it.
func (s *Server) leave(r *room, c *client) []push {
	gone := r.memberOf(c)
	if gone == nil {
		return nil
	}
	delete(c.rooms, r.id)
	for i, m := range r.members {
		if m == gone {
			r.members = append(r.members[:i], r.members[i+1:]...)
			break
		}
	}
	if len(r.members) == 0 {
		delete(s.rooms, r.id)
		if r.code != "" {
			delete(s.byCode, r.code)
		}
		s.log.Debug("room closed", "room", r.id)
		return nil
	}
	if r.owner == gone.id {
		r.owner = r.members[0].id
	}

	// MEMBER_LEFT: room_id u64 | member_id u16 | owner u16
	w := &writer{}
	w.u64(r.id)
	w.u16(gone.id)
	w.u16(r.owner)
	var out []push
	for _, m := range r.members {
		out = append(out, push{m.c, msgMemberLeft, w.b})
	}
	return out
}

// SET_ROOM_DATA: room_id u64 | which u8 | blob. which 0 = external data,
// 1 = internal data, 2 = flags (the blob is a u32; searchers see them in
// ROOM_LIST, so a title can mark its room closed). Only the owner writes;
// members get ROOM_DATA: room_id | which | blob.
func (s *Server) setRoomData(c *client, req uint32, rd *reader) (byte, []byte, []push) {
	r, ok := s.rooms[rd.u64()]
	which, data := rd.u8(), rd.blob()
	if tooBig(data) {
		s.refused(c, "set room data", "blob over limit")
	}
	switch {
	case rd.err != nil || which > 2 || which == 2 && len(data) != 4 || tooBig(data):
		return msgError, errReply(req, errBadRequest), nil
	case !ok || r.memberOf(c) == nil:
		return msgError, errReply(req, errNotInRoom), nil
	case r.memberOf(c).id != r.owner:
		return msgError, errReply(req, errNotOwner), nil
	}
	switch which {
	case 0:
		r.external = data
	case 1:
		r.internal = data
	case 2:
		r.flags = binary.BigEndian.Uint32(data)
	}
	w := &writer{}
	w.u64(r.id)
	w.u8(which)
	w.blob(data)
	var out []push
	for _, m := range r.members {
		if m.c != c {
			out = append(out, push{m.c, msgRoomData, w.b})
		}
	}
	return msgOK, okReply(req), out
}

// ROOM_MESSAGE: room_id u64 | to u16 (0 = everyone else) | blob.
// Delivered as ROOM_MSG: room_id | from u16 | to u16 | blob.
func (s *Server) roomMessage(c *client, req uint32, rd *reader) (byte, []byte, []push) {
	r, ok := s.rooms[rd.u64()]
	to, data := rd.u16(), rd.blob()
	if tooBig(data) {
		s.refused(c, "room message", "blob over limit")
	}
	if rd.err != nil || tooBig(data) {
		return msgError, errReply(req, errBadRequest), nil
	}
	if !ok || r.memberOf(c) == nil {
		return msgError, errReply(req, errNotInRoom), nil
	}
	from := r.memberOf(c)
	w := &writer{}
	w.u64(r.id)
	w.u16(from.id)
	w.u16(to)
	w.blob(data)
	var out []push
	for _, m := range r.members {
		if m != from && (to == 0 || m.id == to) {
			out = append(out, push{m.c, msgRoomMsg, w.b})
		}
	}
	return msgOK, okReply(req), out
}

// KICK_MEMBER: room_id u64 | member_id u16. Owner only; the target gets
// KICKED: room_id, the rest MEMBER_LEFT.
func (s *Server) kick(c *client, req uint32, rd *reader) (byte, []byte, []push) {
	r, ok := s.rooms[rd.u64()]
	target := rd.u16()
	if rd.err != nil {
		return msgError, errReply(req, errBadRequest), nil
	}
	if !ok || r.memberOf(c) == nil {
		return msgError, errReply(req, errNotInRoom), nil
	}
	if r.memberOf(c).id != r.owner {
		return msgError, errReply(req, errNotOwner), nil
	}
	for _, m := range r.members {
		if m.id == target && m.c != c {
			w := &writer{}
			w.u64(r.id)
			out := append([]push{{m.c, msgKicked, w.b}}, s.leave(r, m.c)...)
			return msgOK, okReply(req), out
		}
	}
	return msgError, errReply(req, errNotFound), nil
}

// accessReply is ROOM_ACCESS: req | mode u8 | code [8] (zeros unless invite-only).
func accessReply(req uint32, r *room) []byte {
	w := &writer{}
	w.u32(req)
	if r.code == "" {
		w.u8(accessPublic)
		w.bytes(make([]byte, codeLen))
	} else {
		w.u8(accessCode)
		w.str(r.code, codeLen)
	}
	return w.b
}

// SET_ROOM_ACCESS: room_id u64 | mode u8 (0 public, 1 invite-only). Owner only. Mode 1 gives the room a code (the same one if
// it already has it) and takes it out of search; mode 0 lists it again and the code stops working.
func (s *Server) setRoomAccess(c *client, req uint32, rd *reader) (byte, []byte, []push) {
	r, ok := s.rooms[rd.u64()]
	mode := rd.u8()
	switch {
	case rd.err != nil || mode > accessCode:
		return msgError, errReply(req, errBadRequest), nil
	case !ok || r.memberOf(c) == nil:
		return msgError, errReply(req, errNotInRoom), nil
	case r.memberOf(c).id != r.owner:
		return msgError, errReply(req, errNotOwner), nil
	}
	switch {
	case mode == accessCode && r.code == "":
		code, err := s.newCode()
		if err != nil {
			s.log.Error("room code: random source failed", "err", err)
			return msgError, errReply(req, errLimitReached), nil
		}
		r.code = code
		s.byCode[r.code] = r
	case mode == accessPublic && r.code != "":
		delete(s.byCode, r.code)
		r.code = ""
	}
	s.log.Debug("room access", "room", r.id, "mode", mode)
	return msgRoomAccess, accessReply(req, r), nil
}

// GET_ROOM_ACCESS: room_id u64. Any member may ask, so anyone in the room can invite.
func (s *Server) getRoomAccess(c *client, req uint32, rd *reader) (byte, []byte, []push) {
	r, ok := s.rooms[rd.u64()]
	switch {
	case rd.err != nil:
		return msgError, errReply(req, errBadRequest), nil
	case !ok || r.memberOf(c) == nil:
		return msgError, errReply(req, errNotInRoom), nil
	}
	return msgRoomAccess, accessReply(req, r), nil
}

// JOIN_BY_CODE: code [8] | member_data blob -> ROOM_JOINED. A wrong, malformed, expired or other-title code all
// answer "not found" (malformed: bad request) and count as a miss on the connection.
func (s *Server) joinByCode(c *client, req uint32, rd *reader) (byte, []byte, []push) {
	raw, mine := rd.str(codeLen), rd.blob()
	if rd.err != nil || tooBig(mine) {
		if rd.err == nil {
			s.refused(c, "join by code", "blob over limit")
		}
		return msgError, errReply(req, errBadRequest), nil
	}
	code, ok := normalizeCode(raw)
	if !ok {
		s.codeMiss(c)
		return msgError, errReply(req, errBadRequest), nil
	}
	r := s.roomByCode(code)
	if r == nil || r.commID != c.commID {
		s.codeMiss(c)
		return msgError, errReply(req, errNotFound), nil
	}
	c.codeMisses = 0
	return s.joinMember(c, req, r, mine)
}
