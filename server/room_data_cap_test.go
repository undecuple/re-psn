package main

import "testing"

// SET_ROOM_DATA must not let the owner make the room's own ROOM_JOINED reply larger than one packet: the write is
// refused with error 6 and changes nothing. The limit is exact: a blob that leaves the reply at maxPayload is
// accepted, one byte more is not.

func capSetData(owner *tc, room uint64, which byte, data []byte) (byte, *reader) {
	w := &writer{}
	w.u64(room)
	w.u8(which)
	w.blob(data)
	return owner.call(msgSetRoomData, w.b)
}

func capBlobLens(s *Server, room uint64) (ext, in int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.rooms[room].external), len(s.rooms[room].internal)
}

func TestSetRoomDataReplyLimit(t *testing.T) {
	s, addr := capServer(t)
	owner := dial(t, addr, "NPWR00001_00", "alice", 3658)
	room := capCreate(t, owner, 32, nil, nil, capData)
	members := capFill(t, s, addr, owner, room, func(c *tc, data []byte) (byte, *reader) {
		return c.call(msgJoinRoom, capJoinBody(room, data))
	})
	capDrain(members[1])

	s.mu.Lock()
	slack := maxPayload - joinedSize(s.rooms[room], -1)
	s.mu.Unlock()
	if slack <= 0 || slack >= maxRoomBlob {
		t.Fatalf("test needs 0 < slack < %d, got %d", maxRoomBlob, slack)
	}

	// over by one byte, for either blob: error 6, nothing changed, the owner stays connected, nobody is told
	for which := byte(0); which < 2; which++ {
		rt, r := capSetData(owner, room, which, make([]byte, slack+1))
		if rt != msgError || r.u32() != errLimitReached {
			t.Fatalf("which %d, slack+1 bytes: got 0x%02X, want error %d", which, rt, errLimitReached)
		}
	}
	if ext, in := capBlobLens(s, room); ext != 0 || in != 0 {
		t.Fatalf("a refused write changed the room: external %d, internal %d", ext, in)
	}
	if n := capDrain(members[1]); n != 0 {
		t.Fatalf("a member got %d pushes for refused writes", n)
	}

	// exactly the slack fits
	if rt, _ := capSetData(owner, room, 0, make([]byte, slack)); rt != msgOK {
		t.Fatalf("external of %d bytes (fits exactly): got 0x%02X", slack, rt)
	}
	if rt, r := capSetData(owner, room, 1, []byte{1}); rt != msgError || r.u32() != errLimitReached {
		t.Fatalf("internal of 1 byte on a full reply: got 0x%02X, want error %d", rt, errLimitReached)
	}
	// replacing a blob counts the old one out: the same size again is fine, and so is shrinking
	if rt, _ := capSetData(owner, room, 0, make([]byte, slack)); rt != msgOK {
		t.Fatalf("same-size external again: got 0x%02X", rt)
	}
	if rt, _ := capSetData(owner, room, 0, nil); rt != msgOK {
		t.Fatalf("shrinking the external blob: got 0x%02X", rt)
	}
	// flags are four bytes and not part of the sizing
	if rt, _ := capSetData(owner, room, 2, []byte{0, 0, 0, 1}); rt != msgOK {
		t.Fatalf("flags: got 0x%02X", rt)
	}

	// a member asking for the room again at the limit still gets its reply
	if rt, _ := capSetData(owner, room, 1, make([]byte, slack)); rt != msgOK {
		t.Fatalf("internal of %d bytes: got 0x%02X", slack, rt)
	}
	if rt, _ := members[1].call(msgJoinRoom, capJoinBody(room, nil)); rt != msgRoomJoined {
		t.Fatalf("rejoin at the limit: got 0x%02X, want ROOM_JOINED", rt)
	}
}
