package main

import (
	"context"
	"net"
	"testing"
	"time"
)

// A member who joins a room and stops reading must not hold up the others.
// Its receive buffer is shrunk so a few kilobytes of room messages fill it;
// the owner keeps sending until the server disconnects the slow recipient.
func TestStuckMemberDoesNotBlockOthers(t *testing.T) {
	s, addr := startServer(t)
	// Join the server's handlers and their writers before this test returns.
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := s.Shutdown(ctx); err != nil {
			t.Errorf("shutdown: %v", err)
		}
	})
	a := dial(t, addr, "NPWR00001_00", "alice", 3658)
	w := &writer{}
	w.u8(3)
	w.u32(0)
	w.blob([]byte("stage1"))
	w.blob([]byte("secret"))
	w.blob([]byte("homer"))
	rt, r := a.call(msgCreateRoom, w.b)
	if rt != msgRoomJoined {
		t.Fatalf("create: 0x%02X", rt)
	}
	roomID := r.u64()

	join := func(c *tc, name string) uint16 {
		t.Helper()
		w := &writer{}
		w.u64(roomID)
		w.blob([]byte(name))
		rt, r := c.call(msgJoinRoom, w.b)
		if rt != msgRoomJoined {
			t.Fatalf("join %s: 0x%02X", name, rt)
		}
		r.u64()
		return r.u16()
	}

	// bob joins and then never reads again
	conn, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	conn.(*net.TCPConn).SetReadBuffer(4096)
	b := helloConn(t, conn, "NPWR00001_00", "bob", 3659)
	bob := join(b, "bob")

	// carol is a third member; she joins after bob, so her join is pushed to bob too
	c := dial(t, addr, "NPWR00001_00", "carol", 3660)
	join(c, "carol")

	// Alice keeps receiving replies independently of Bob's writer. Detect the
	// slow client's removal, rather than requiring its socket to stall Alice.
	bobGone := func() bool {
		s.mu.Lock()
		defer s.mu.Unlock()
		for _, cl := range s.clients {
			if cl.onlineID == "bob" {
				return false
			}
		}
		return true
	}
	payload := make([]byte, 4000)
	for i := 0; i < 2000 && !bobGone(); i++ {
		w := &writer{}
		w.u64(roomID)
		w.u16(bob)
		w.blob(payload)
		if rt, _ := a.call(msgRoomMessage, w.b); rt != msgOK {
			t.Fatalf("message %d: %x", i, rt)
		}
		time.Sleep(6 * time.Millisecond)
	}
	// Carol remains usable after the send loop; the deterministic gated-writer
	// test separately proves progress while another writer is blocked.
	search := func(when string) {
		t.Helper()
		w := &writer{}
		w.u16(0)
		w.u16(10)
		if rt, _ := c.call(msgSearchRooms, w.b); rt != msgRoomList {
			t.Fatalf("search %s: 0x%02X", when, rt)
		}
	}
	search("after send loop")

	// The server drops Bob on output overflow or write timeout; it closes the connection,
	// and the reader's cleanup removed bob from the server's clients. Bob's
	// kernel never gets an EOF (bob's window stays shut without reads), so
	// the server's state is what is checked.
	for deadline := time.Now().Add(5 * time.Second); !bobGone(); {
		if time.Now().After(deadline) {
			t.Fatal("server did not drop bob within 5s")
		}
		time.Sleep(20 * time.Millisecond)
	}

	search("after bob was dropped")
}
