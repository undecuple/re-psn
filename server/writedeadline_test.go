package main

import (
	"context"
	"net"
	"testing"
	"time"
)

// A member who joins a room and stops reading must not hold up the others.
// Its receive buffer is shrunk so a few kilobytes of room messages fill it;
// the owner keeps sending to it until the server's write to it times out.
func TestStuckMemberDoesNotBlockOthers(t *testing.T) {
	old := writeTimeout
	writeTimeout = 300 * time.Millisecond
	t.Cleanup(func() { writeTimeout = old })

	s, addr := startServer(t)
	// Cleanups run last in first-registered order, so this waits for the
	// server's handlers (including their final pushes, which read writeTimeout)
	// before the restore above runs.
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

	// alice sends bob room messages until bob's socket stalls the server
	payload := make([]byte, 4000)
	stalled := false
	for i := 0; i < 2000 && !stalled; i++ {
		w := &writer{}
		w.u64(roomID)
		w.u16(bob)
		w.blob(payload)
		begin := time.Now()
		if rt, _ := a.call(msgRoomMessage, w.b); rt != msgOK {
			t.Fatalf("message %d: 0x%02X", i, rt)
		}
		// A reply is sent before the pushes for its request, so a long
		// reply means the previous push was stuck for most of writeTimeout.
		stalled = time.Since(begin) >= writeTimeout/2
		time.Sleep(6 * time.Millisecond) // under the 200 requests per second limit
	}
	if !stalled {
		t.Fatal("bob's socket never stalled the server; the test does not exercise the path")
	}

	// carol still gets replies while bob is stuck
	search := func(when string) {
		t.Helper()
		w := &writer{}
		w.u16(0)
		w.u16(10)
		if rt, _ := c.call(msgSearchRooms, w.b); rt != msgRoomList {
			t.Fatalf("search %s: 0x%02X", when, rt)
		}
	}
	search("while bob is stuck")

	// the server drops bob: its write timed out, it closed the connection,
	// and the reader's cleanup removed bob from the server's clients. Bob's
	// kernel never gets an EOF (bob's window stays shut without reads), so
	// the server's state is what is checked.
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
	for deadline := time.Now().Add(5 * time.Second); !bobGone(); {
		if time.Now().After(deadline) {
			t.Fatal("server did not drop bob within 5s")
		}
		time.Sleep(20 * time.Millisecond)
	}

	search("after bob was dropped")
}
