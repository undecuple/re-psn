package main

import (
	"net"
	"testing"
)

// searchBody is SEARCH_ROOMS' request: start u16 | max u16.
func searchBody(start, max uint16) []byte {
	w := &writer{}
	w.u16(start)
	w.u16(max)
	return w.b
}

// readRoomList parses a ROOM_LIST reply body (after the request id) and returns its total, count and the
// external blob lengths of its entries. It fails the test if the entries do not add up to the header.
func readRoomList(t *testing.T, r *reader) (total, count int, extLens []int) {
	t.Helper()
	total = int(r.u16())
	count = int(r.u16())
	for i := 0; i < count; i++ {
		r.u64()   // room id
		r.str(16) // owner
		r.u8()    // cur
		r.u8()    // max
		r.u32()   // flags
		extLens = append(extLens, len(r.blob()))
	}
	if r.err != nil || len(r.b) != 0 {
		t.Fatalf("ROOM_LIST entries do not match the header: err %v, %d bytes left", r.err, len(r.b))
	}
	return total, count, extLens
}

// TestSearchReplyFitsPacket fills a title with rooms whose external data is as large as a room accepts, then
// searches. The reply must fit one packet, the connection must stay open, and paging from count must return the rest.
func TestSearchReplyFitsPacket(t *testing.T) {
	s := NewServer(100, false)
	s.maxPlayerRooms = 0 // one owner makes every room
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	go s.Serve(ln)
	addr := ln.Addr().String()

	owner := dial(t, addr, "NPWR00001_00", "alice", 3658)
	searcher := dial(t, addr, "NPWR00001_00", "bob", 3659)

	// maxRoomBlob is the largest external blob a room accepts. Entries are 32 bytes plus the blob, so
	// about fifteen of them fill a packet and a sixteenth would overflow it.
	const rooms = 20
	ext := make([]byte, maxRoomBlob)
	for i := 0; i < rooms; i++ {
		if rt, _ := owner.call(msgCreateRoom, createBody(ext)); rt != msgRoomJoined {
			t.Fatalf("create room %d: got 0x%02X", i, rt)
		}
	}

	rt, r := searcher.call(msgSearchRooms, searchBody(0, 100))
	if rt != msgRoomList {
		t.Fatalf("search: got 0x%02X, want ROOM_LIST", rt)
	}
	if size := 4 + len(r.b); size > maxPayload { // r.b is what follows the request id
		t.Fatalf("reply is %d bytes, over maxPayload %d", size, maxPayload)
	}
	total, count, lens := readRoomList(t, r)
	if total != rooms {
		t.Fatalf("total %d, want %d open rooms", total, rooms)
	}
	if count <= 0 || count >= total {
		t.Fatalf("count %d of total %d: want a partial page", count, total)
	}
	for i, n := range lens {
		if n != maxRoomBlob {
			t.Fatalf("entry %d: external blob %d bytes, want %d", i, n, maxRoomBlob)
		}
	}

	// The connection is still open, and the rest comes from start = count.
	rt, r = searcher.call(msgSearchRooms, searchBody(uint16(count), 100))
	if rt != msgRoomList {
		t.Fatalf("second search: got 0x%02X, want ROOM_LIST", rt)
	}
	total2, count2, _ := readRoomList(t, r)
	if total2 != rooms {
		t.Fatalf("second search total %d, want %d", total2, rooms)
	}
	if count+count2 != total {
		t.Fatalf("pages cover %d rooms, want %d", count+count2, total)
	}
}

// TestSearchFirstEntryTooBig covers the fallback: a room whose external blob cannot fit in a packet even alone
// is listed with an empty blob, so paging still moves on. The protocol refuses such a blob, so the test sets it
// on the room directly.
func TestSearchFirstEntryTooBig(t *testing.T) {
	s := NewServer(100, false)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	go s.Serve(ln)
	addr := ln.Addr().String()

	owner := dial(t, addr, "NPWR00001_00", "alice", 3658)
	searcher := dial(t, addr, "NPWR00001_00", "bob", 3659)
	for i := 0; i < 2; i++ {
		if rt, _ := owner.call(msgCreateRoom, createBody(nil)); rt != msgRoomJoined {
			t.Fatalf("create room %d: got 0x%02X", i, rt)
		}
	}
	s.mu.Lock()
	s.rooms[1].external = make([]byte, 0xFFFF) // room 1 is the oldest; no entry with this blob fits a reply
	s.mu.Unlock()

	rt, r := searcher.call(msgSearchRooms, searchBody(0, 100))
	if rt != msgRoomList {
		t.Fatalf("search: got 0x%02X, want ROOM_LIST", rt)
	}
	total, count, lens := readRoomList(t, r)
	if total != 2 || count != 2 {
		t.Fatalf("total %d count %d: want both rooms listed", total, count)
	}
	if lens[0] != 0 {
		t.Fatalf("oversize room listed with a %d byte blob, want empty", lens[0])
	}
}
