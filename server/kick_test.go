package main

import (
	"bytes"
	"net"
	"testing"
	"time"
)

func kickBody(room uint64, target uint16) []byte {
	w := &writer{}
	w.u64(room)
	w.u16(target)
	return w.b
}

type kickMember struct {
	id    uint16
	name  string
	owner byte
}

func checkKickMembers(t *testing.T, c *tc, room uint64, self, owner uint16, want []kickMember) {
	t.Helper()
	x := joinID(c, room)
	if x.rt != msgRoomJoined {
		t.Fatalf("read room: got 0x%02X, want ROOM_JOINED", x.rt)
	}
	r := x.r
	gotRoom, gotSelf, gotOwner := r.u64(), r.u16(), r.u16()
	r.u8()
	r.u32()
	r.blob()
	r.blob()
	count := int(r.u8())
	got := make([]kickMember, 0, count)
	for i := 0; i < count; i++ {
		id := r.u16()
		r.u32()
		name := r.str(16)
		r.take(4)
		r.u16()
		isOwner := r.u8()
		r.blob()
		got = append(got, kickMember{id, name, isOwner})
	}
	if r.err != nil || len(r.b) != 0 || gotRoom != room || gotSelf != self || gotOwner != owner {
		t.Fatalf("room snapshot: room %d self %d owner %d, remaining %d, err %v",
			gotRoom, gotSelf, gotOwner, len(r.b), r.err)
	}
	if len(got) != len(want) {
		t.Fatalf("room members: got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("room members: got %v, want %v", got, want)
		}
	}
}

func TestRoomKick(t *testing.T) {
	addr := start(t)
	owner := dial(t, addr, "NPWR00001_00", "alice", 3658)
	target := dial(t, addr, "NPWR00001_00", "bob", 3659)
	other := dial(t, addr, "NPWR00001_00", "carol", 3660)
	outsider := dial(t, addr, "NPWR00001_00", "dave", 3661)
	clients := []*tc{owner, target, other, outsider}

	w := &writer{}
	w.u8(3)
	w.u32(0)
	w.blob(nil)
	w.blob(nil)
	w.blob(nil)
	rt, r := owner.call(msgCreateRoom, w.b)
	if rt != msgRoomJoined {
		t.Fatalf("create: got 0x%02X, want ROOM_JOINED", rt)
	}
	room, ownerID := r.u64(), r.u16()
	join := func(c *tc) uint16 {
		t.Helper()
		x := joinID(c, room)
		if x.rt != msgRoomJoined {
			t.Fatalf("join: got 0x%02X, want ROOM_JOINED", x.rt)
		}
		gotRoom, id := x.r.u64(), x.r.u16()
		if x.r.err != nil || gotRoom != room {
			t.Fatalf("join: room %d, err %v", gotRoom, x.r.err)
		}
		return id
	}
	targetID := join(target)
	owner.next(msgMemberJoined)
	otherID := join(other)
	owner.next(msgMemberJoined)
	target.next(msgMemberJoined)

	// Each round trip fences pushes from earlier requests on that connection.
	// Two passes first finish every sender's requests, then drain every client.
	noPushes := func() {
		t.Helper()
		search := &writer{}
		search.u16(0)
		search.u16(10)
		for i := 0; i < 2; i++ {
			for _, c := range clients {
				if rt, _ := c.call(msgSearchRooms, search.b); rt != msgRoomList {
					t.Fatalf("search barrier: got 0x%02X, want ROOM_LIST", rt)
				}
			}
		}
		for i, c := range clients {
			if len(c.pushes) != 0 {
				t.Fatalf("client %d received unexpected pushes: %v", i, c.pushes)
			}
		}
	}

	rejections := []struct {
		name string
		from *tc
		body []byte
		code uint32
	}{
		{"member kicking owner", other, kickBody(room, ownerID), errNotOwner},
		{"member kicking peer", other, kickBody(room, targetID), errNotOwner},
		{"member kicking missing target", other, kickBody(room, 0), errNotOwner},
		{"non-member", outsider, kickBody(room, targetID), errNotInRoom},
		{"missing room", owner, kickBody(room+1, targetID), errNotInRoom},
		{"missing target", owner, kickBody(room, 0), errNotFound},
		{"owner targeting self", owner, kickBody(room, ownerID), errNotFound},
		{"truncated target", owner, kickBody(room, targetID)[:9], errBadRequest},
	}
	for _, test := range rejections {
		rt, r := test.from.call(msgKickMember, test.body)
		if rt != msgError {
			t.Fatalf("%s: got 0x%02X, want ERROR", test.name, rt)
		}
		code := r.u32()
		if code != test.code || r.err != nil || len(r.b) != 0 {
			t.Fatalf("%s: error %d, want %d; remaining %d, err %v",
				test.name, code, test.code, len(r.b), r.err)
		}
	}
	noPushes()
	checkKickMembers(t, owner, room, ownerID, ownerID, []kickMember{
		{ownerID, "alice", 1}, {targetID, "bob", 0}, {otherID, "carol", 0},
	})

	// Unlike tc.call, read the first packet directly: OK must precede the
	// owner's MEMBER_LEFT push for this request.
	owner.req++
	w = &writer{}
	w.u32(owner.req)
	w.bytes(kickBody(room, targetID))
	if err := writePacket(owner.conn, msgKickMember, w.b); err != nil {
		t.Fatal(err)
	}
	rt, payload := owner.read()
	if rt != msgOK {
		t.Fatalf("kick first packet: got 0x%02X, want OK", rt)
	}
	r = &reader{b: payload}
	if req := r.u32(); req != owner.req || r.err != nil || len(r.b) != 0 {
		t.Fatalf("kick reply: request %d, want %d; remaining %d, err %v",
			req, owner.req, len(r.b), r.err)
	}
	kicked := target.next(msgKicked)
	if gotRoom := kicked.u64(); gotRoom != room || kicked.err != nil || len(kicked.b) != 0 {
		t.Fatalf("KICKED: room %d, want %d; remaining %d, err %v",
			gotRoom, room, len(kicked.b), kicked.err)
	}
	left := func(c *tc) {
		t.Helper()
		p := c.next(msgMemberLeft)
		gotRoom, member, gotOwner := p.u64(), p.u16(), p.u16()
		if gotRoom != room || member != targetID || gotOwner != ownerID || p.err != nil || len(p.b) != 0 {
			t.Fatalf("MEMBER_LEFT: room %d member %d owner %d; remaining %d, err %v",
				gotRoom, member, gotOwner, len(p.b), p.err)
		}
	}
	left(owner)
	left(other)
	noPushes()
	checkKickMembers(t, owner, room, ownerID, ownerID, []kickMember{
		{ownerID, "alice", 1}, {otherID, "carol", 0},
	})

	w = &writer{}
	w.u64(room)
	rt, r = target.call(msgLeaveRoom, w.b)
	if rt != msgError {
		t.Fatalf("kicked member leave: got 0x%02X, want ERROR", rt)
	}
	if code := r.u32(); code != errNotInRoom || r.err != nil || len(r.b) != 0 {
		t.Fatalf("kicked member leave: error %d, want %d", code, errNotInRoom)
	}

	// A kick removes the member, but does not ban a later join.
	rejoinedID := join(target)
	if rejoinedID == ownerID || rejoinedID == targetID || rejoinedID == otherID {
		t.Fatalf("rejoin: member id %d was already used", rejoinedID)
	}
	for _, c := range []*tc{owner, other} {
		p := c.next(msgMemberJoined)
		if gotRoom, id := p.u64(), p.u16(); gotRoom != room || id != rejoinedID || p.err != nil {
			t.Fatalf("rejoin push: room %d member %d, err %v", gotRoom, id, p.err)
		}
	}
	noPushes()
	checkKickMembers(t, owner, room, ownerID, ownerID, []kickMember{
		{ownerID, "alice", 1}, {otherID, "carol", 0}, {rejoinedID, "bob", 0},
	})
}

type kickWrite struct {
	to     string
	packet []byte
}

type kickRecordingConn struct {
	net.Conn
	name   string
	writes *[]kickWrite
}

func (c *kickRecordingConn) Write(p []byte) (int, error) {
	*c.writes = append(*c.writes, kickWrite{c.name, append([]byte(nil), p...)})
	return len(p), nil
}

func (c *kickRecordingConn) SetWriteDeadline(time.Time) error { return nil }

func TestRoomKickDispatchOrder(t *testing.T) {
	s := NewServer(10, false)
	var writes []kickWrite
	client := func(id uint32, name string) *client {
		return &client{
			id: id, conn: &kickRecordingConn{name: name, writes: &writes},
			commID: "NPWR00001_00", onlineID: name, rooms: map[uint64]*room{},
		}
	}
	owner, target, other := client(1, "alice"), client(2, "bob"), client(3, "carol")
	r := &room{id: 7, commID: owner.commID, max: 3}
	s.rooms[r.id] = r
	ownerMember := s.addMember(r, owner, nil)
	r.owner = ownerMember.id
	targetMember := s.addMember(r, target, nil)
	otherMember := s.addMember(r, other, nil)
	elsewhere := &room{id: 8, commID: target.commID, max: 2}
	s.rooms[elsewhere.id] = elsewhere
	elsewhere.owner = s.addMember(elsewhere, target, nil).id

	const req uint32 = 1234
	request := &writer{}
	request.u32(req)
	request.bytes(kickBody(r.id, targetMember.id))
	s.handle(owner, msgKickMember, request.b)

	reply := &writer{}
	reply.u32(req)
	kicked := &writer{}
	kicked.u64(r.id)
	left := &writer{}
	left.u64(r.id)
	left.u16(targetMember.id)
	left.u16(ownerMember.id)
	want := []struct {
		to      string
		typ     byte
		payload []byte
	}{
		{"alice", msgOK, reply.b},
		{"bob", msgKicked, kicked.b},
		{"alice", msgMemberLeft, left.b},
		{"carol", msgMemberLeft, left.b},
	}
	if len(writes) != len(want) {
		t.Fatalf("dispatches: got %d, want %d", len(writes), len(want))
	}
	for i, got := range writes {
		typ, payload, err := readPacket(bytes.NewReader(got.packet))
		if err != nil || len(got.packet) != len(payload)+3 ||
			got.to != want[i].to || typ != want[i].typ || !bytes.Equal(payload, want[i].payload) {
			t.Fatalf("dispatch %d: to %s type 0x%02X payload %x, err %v; want to %s type 0x%02X payload %x",
				i, got.to, typ, payload, err, want[i].to, want[i].typ, want[i].payload)
		}
	}
	_, targetStillInRoom := target.rooms[r.id]
	if s.rooms[r.id] != r || r.owner != ownerMember.id || len(r.members) != 2 ||
		r.members[0] != ownerMember || r.members[1] != otherMember ||
		owner.rooms[r.id] != r || other.rooms[r.id] != r || targetStillInRoom {
		t.Fatal("kick did not preserve the room and remaining members or remove the target")
	}
	if s.rooms[elsewhere.id] != elsewhere || len(target.rooms) != 1 ||
		target.rooms[elsewhere.id] != elsewhere || elsewhere.memberOf(target) == nil {
		t.Fatal("kick changed the target's membership in another room")
	}
}
