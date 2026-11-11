package main

import (
	"fmt"
	"net"
	"testing"
	"time"
)

// A ROOM_JOINED reply must fit one packet. These tests fill a room with members whose data is capBlob bytes until
// the next join would overflow, then check the refusal: error 6, the joiner still connected and not in the room,
// and no MEMBER_JOINED to the members who stay.

const capBlob = maxRoomBlob

var capData = make([]byte, capBlob)

// capServer starts a server the test can inspect (its rooms are read under s.mu).
func capServer(t *testing.T) (*Server, string) {
	s := NewServer(100, false)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	go s.Serve(ln)
	return s, ln.Addr().String()
}

func capCreate(t *testing.T, c *tc, max uint8, ext, in, mine []byte) uint64 {
	t.Helper()
	w := &writer{}
	w.u8(max)
	w.u32(0)
	w.blob(ext)
	w.blob(in)
	w.blob(mine)
	rt, r := c.call(msgCreateRoom, w.b)
	if rt != msgRoomJoined {
		t.Fatalf("create room: got 0x%02X", rt)
	}
	return r.u64()
}

func capJoinBody(room uint64, data []byte) []byte {
	w := &writer{}
	w.u64(room)
	w.blob(data)
	return w.b
}

// capRaw sends a request and returns the reply payload (request id included) as it came off the wire.
func capRaw(c *tc, t byte, body []byte) (byte, []byte) {
	c.req++
	w := &writer{}
	w.u32(c.req)
	w.bytes(body)
	if err := writePacket(c.conn, t, w.b); err != nil {
		c.t.Fatal(err)
	}
	for {
		rt, p := c.read()
		if rt >= 0xA0 && rt < 0xB0 {
			c.pushes = append(c.pushes, pkt{rt, p})
			continue
		}
		return rt, p
	}
}

// capDrain returns how many MEMBER_JOINED pushes c has waiting, and clears them.
func capDrain(c *tc) int {
	n := 0
	for _, p := range c.pushes {
		if p.t == msgMemberJoined {
			n++
		}
	}
	c.pushes = nil
	c.conn.SetReadDeadline(time.Now().Add(150 * time.Millisecond))
	for {
		t, _, err := readPacket(c.conn)
		if err != nil {
			return n
		}
		if t == msgMemberJoined {
			n++
		}
	}
}

// capFill joins members with capBlob bytes of data until the next such join would not fit one reply. Each join
// is checked against the members already in: each gets exactly one MEMBER_JOINED. It returns owner and the joiners.
func capFill(t *testing.T, s *Server, addr string, owner *tc, room uint64, join func(*tc, []byte) (byte, *reader)) []*tc {
	t.Helper()
	members := []*tc{owner}
	for {
		s.mu.Lock()
		next := joinedSize(s.rooms[room], capBlob)
		s.mu.Unlock()
		if next > maxPayload {
			return members
		}
		c := dial(t, addr, "NPWR00001_00", fmt.Sprintf("p%d", len(members)), uint16(4000+len(members)))
		if rt, _ := join(c, capData); rt != msgRoomJoined {
			t.Fatalf("join %d: got 0x%02X, want ROOM_JOINED", len(members), rt)
		}
		for _, m := range members {
			m.next(msgMemberJoined)
		}
		members = append(members, c)
	}
}

// capRefused asserts the room is still at want members, and the joiner got error 6 and stayed connected.
func capRefused(t *testing.T, s *Server, addr string, room uint64, joiner *tc, rt byte, r *reader, want int, stay []*tc) {
	t.Helper()
	if rt != msgError || r.u32() != errLimitReached {
		t.Fatalf("overflow join: got 0x%02X, want error %d", rt, errLimitReached)
	}
	s.mu.Lock()
	got := len(s.rooms[room].members)
	s.mu.Unlock()
	if got != want {
		t.Fatalf("room has %d members after the refused join, want %d", got, want)
	}
	// still connected: a search on the same connection is answered and shows the same member count
	w := &writer{}
	w.u16(0)
	w.u16(10)
	rt, r = joiner.call(msgSearchRooms, w.b)
	if rt != msgRoomList {
		t.Fatalf("search after refusal: got 0x%02X, want ROOM_LIST", rt)
	}
	// a public room is listed with its member count; an invite-only one is not listed
	if total, count := r.u16(), r.u16(); total == 1 && count == 1 {
		r.u64()
		r.str(16)
		if cur := int(r.u8()); cur != want {
			t.Fatalf("search shows %d members, want %d", cur, want)
		}
	} else if total != 0 || count != 0 {
		t.Fatalf("search after refusal: total %d count %d", total, count)
	}
	for _, m := range stay {
		if n := capDrain(m); n != 0 {
			t.Fatalf("a member got %d MEMBER_JOINED for the refused joiner", n)
		}
	}
}

func TestJoinReplyLimitJoinRoom(t *testing.T) {
	s, addr := capServer(t)
	owner := dial(t, addr, "NPWR00001_00", "alice", 3658)
	room := capCreate(t, owner, 32, nil, nil, capData)
	members := capFill(t, s, addr, owner, room, func(c *tc, data []byte) (byte, *reader) {
		return c.call(msgJoinRoom, capJoinBody(room, data))
	})
	// 26 bytes of fixed fields plus 31 + 4096 per member: 15 fit, a 16th does not
	if len(members) != 15 {
		t.Fatalf("filled %d members, want 15", len(members))
	}

	late := dial(t, addr, "NPWR00001_00", "late", 3700)
	rt, r := late.call(msgJoinRoom, capJoinBody(room, capData))
	capRefused(t, s, addr, room, late, rt, r, 15, members)

	// the refusal is about size, not the connection: a joiner with no data still gets in
	if rt, _ := late.call(msgJoinRoom, capJoinBody(room, nil)); rt != msgRoomJoined {
		t.Fatalf("small joiner after refusal: got 0x%02X, want ROOM_JOINED", rt)
	}
	if n := capDrain(owner); n != 1 {
		t.Fatalf("owner got %d MEMBER_JOINED for the small joiner, want 1", n)
	}
}

func TestJoinReplyLimitByCode(t *testing.T) {
	s, addr := capServer(t)
	owner := dial(t, addr, "NPWR00001_00", "alice", 3658)
	room := capCreate(t, owner, 32, nil, nil, capData)
	w := &writer{}
	w.u64(room)
	w.u8(accessCode)
	rt, r := owner.call(msgSetRoomAccess, w.b)
	if rt != msgRoomAccess {
		t.Fatalf("set access: got 0x%02X", rt)
	}
	r.u8()
	code := r.str(codeLen)

	members := capFill(t, s, addr, owner, room, func(c *tc, data []byte) (byte, *reader) {
		w := &writer{}
		w.str(code, codeLen)
		w.blob(data)
		return c.call(msgJoinByCode, w.b)
	})
	if len(members) != 15 {
		t.Fatalf("filled %d members, want 15", len(members))
	}

	late := dial(t, addr, "NPWR00001_00", "late", 3700)
	w = &writer{}
	w.str(code, codeLen)
	w.blob(capData)
	rt, r = late.call(msgJoinByCode, w.b)
	capRefused(t, s, addr, room, late, rt, r, 15, members)
}

// A member who is already in the room asks for it again. Its reply can outgrow a packet after the room's
// internal blob is set to 4096 bytes: that is error 6 too, and the member stays connected and in the room.
func TestJoinReplyLimitRejoin(t *testing.T) {
	s, addr := capServer(t)
	owner := dial(t, addr, "NPWR00001_00", "alice", 3658)
	room := capCreate(t, owner, 32, nil, nil, capData)
	members := capFill(t, s, addr, owner, room, func(c *tc, data []byte) (byte, *reader) {
		return c.call(msgJoinRoom, capJoinBody(room, data))
	})

	w := &writer{}
	w.u64(room)
	w.u8(1) // internal
	w.blob(capData)
	if rt, _ := owner.call(msgSetRoomData, w.b); rt != msgOK {
		t.Fatalf("set internal data: got 0x%02X", rt)
	}
	joiner := members[1]
	rt, r := joiner.call(msgJoinRoom, capJoinBody(room, nil))
	capRefused(t, s, addr, room, joiner, rt, r, 15, members)
}

// joinedSize must be the length of the reply that goes out: checked against the wire for the joiner and for a
// member asking again, in a room with external and internal data and members of different data sizes.
func TestJoinedSizeMatchesReply(t *testing.T) {
	s, addr := capServer(t)
	owner := dial(t, addr, "NPWR00001_00", "alice", 3658)
	room := capCreate(t, owner, 8, make([]byte, 100), make([]byte, 200), nil)

	size := func(extra int) int {
		s.mu.Lock()
		defer s.mu.Unlock()
		return joinedSize(s.rooms[room], extra)
	}
	if rt, p := capRaw(owner, msgJoinRoom, capJoinBody(room, nil)); rt != msgRoomJoined || len(p) != size(-1) {
		t.Fatalf("owner's reply: %d bytes, joinedSize %d", len(p), size(-1))
	}
	for _, n := range []int{1, capBlob, 77} {
		want := size(n)
		c := dial(t, addr, "NPWR00001_00", fmt.Sprintf("p%d", n), 3700)
		rt, p := capRaw(c, msgJoinRoom, capJoinBody(room, make([]byte, n)))
		if rt != msgRoomJoined || len(p) != want {
			t.Fatalf("joiner with %d data bytes: reply %d bytes (0x%02X), joinedSize %d", n, len(p), rt, want)
		}
		if _, p := capRaw(c, msgJoinRoom, capJoinBody(room, nil)); len(p) != size(-1) {
			t.Fatalf("rejoin after %d data bytes: reply %d bytes, joinedSize %d", n, len(p), size(-1))
		}
	}
}
