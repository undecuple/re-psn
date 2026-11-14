package main

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"io"
	"net"
	"runtime"
	"sync"
	"testing"
	"time"
)

type outputTestConn struct {
	net.Conn
	writes      chan []byte
	blocked     chan struct{}
	release     chan struct{}
	closed      chan struct{}
	once        sync.Once
	entered     sync.Once
	writeErr    error
	deadlineErr error
	short       bool
}

func newOutputTestConn() *outputTestConn {
	return &outputTestConn{writes: make(chan []byte, maxOutputFrames), blocked: make(chan struct{}), release: make(chan struct{}), closed: make(chan struct{})}
}
func (c *outputTestConn) Write(p []byte) (int, error) {
	c.entered.Do(func() { close(c.blocked) })
	select {
	case <-c.release:
	case <-c.closed:
		return 0, net.ErrClosed
	}
	if c.writeErr != nil {
		return 0, c.writeErr
	}
	if c.short {
		return len(p) - 1, nil
	}
	c.writes <- append([]byte(nil), p...)
	return len(p), nil
}
func (c *outputTestConn) SetWriteDeadline(time.Time) error { return c.deadlineErr }
func (c *outputTestConn) Close() error                     { c.once.Do(func() { close(c.closed) }); return nil }
func outputTestClient(t *testing.T) (*client, *outputTestConn) {
	t.Helper()
	conn := newOutputTestConn()
	c := &client{conn: conn, rooms: map[uint64]*room{}}
	t.Cleanup(func() { c.stopOutput(); c.waitOutput() })
	return c, conn
}
func outputFrame(t *testing.T, c *outputTestConn) []byte {
	t.Helper()
	select {
	case p := <-c.writes:
		return p
	case <-time.After(2 * time.Second):
		t.Fatal("missing output frame")
		return nil
	}
}

func TestOutputFrameAndByteLimits(t *testing.T) {
	for _, limit := range []string{"frames", "bytes"} {
		t.Run(limit, func(t *testing.T) {
			c, conn := outputTestClient(t)
			if limit == "frames" {
				c.send(msgOK, nil)
				waitSignal(t, conn.blocked, "in-flight frame")
				for i := 1; i < maxOutputFrames; i++ {
					c.send(msgOK, nil)
				}
			} else {
				c.send(msgOK, make([]byte, maxPayload))
				waitSignal(t, conn.blocked, "maximum legal in-flight frame")
				c.send(msgOK, make([]byte, maxOutputBytes-(maxPayload+3)-3))
			}
			select {
			case <-conn.closed:
				t.Fatal("closed at the exact allowed boundary")
			default:
			}
			c.send(msgOK, nil) // one frame, including its header, crosses the limit
			waitSignal(t, conn.closed, "overflow disconnect")
			c.waitOutput()
			c.send(msgOK, nil)
			select {
			case <-conn.writes:
				t.Fatal("frame written after overflow")
			default:
			}
			c.output.mu.Lock()
			defer c.output.mu.Unlock()
			if c.output.frames != nil || c.output.queued != 0 {
				t.Fatal("closed queue retained payload references")
			}
		})
	}
}

func TestOutputOwnsFrameAndReusesBudget(t *testing.T) {
	c, conn := outputTestClient(t)
	payload := make([]byte, maxPayload, maxPayload*4)
	for i := range payload {
		payload[i] = byte(i)
	}
	want := append([]byte(nil), payload...)
	c.send(msgOK, payload)
	waitSignal(t, conn.blocked, "copied frame")
	clear(payload)
	close(conn.release)
	got := outputFrame(t, conn)
	if got[0] != msgOK || int(binary.BigEndian.Uint16(got[1:])) != maxPayload || !bytes.Equal(got[3:], want) {
		t.Fatal("queued frame changed with caller buffer")
	}
	// Reading a second full-size frame proves the successful first write
	// released its budget; two simultaneous maximum frames exceed the byte cap.
	for deadline := time.Now().Add(2 * time.Second); ; {
		c.output.mu.Lock()
		retained := c.output.retained
		c.output.mu.Unlock()
		if retained == 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("write did not release budget")
		}
		runtime.Gosched()
	}
	c.send(msgRoomList, want)
	got = outputFrame(t, conn)
	if len(got) != maxPayload+3 || got[0] != msgRoomList {
		t.Fatal("maximum legal frame rejected after budget release")
	}
}

func TestOutputFailureClosesConnection(t *testing.T) {
	for _, failure := range []string{"write", "short-write", "deadline"} {
		t.Run(failure, func(t *testing.T) {
			c, conn := outputTestClient(t)
			switch failure {
			case "write":
				conn.writeErr = io.ErrClosedPipe
			case "short-write":
				conn.short = true
			case "deadline":
				conn.deadlineErr = errors.New("deadline failed")
			}
			close(conn.release)
			c.send(msgOK, []byte{1})
			waitSignal(t, conn.closed, "failed output closes connection")
			c.waitOutput()
		})
	}
}

type shortenedOutputDeadline struct{ net.Conn }

func (c shortenedOutputDeadline) SetWriteDeadline(deadline time.Time) error {
	if !deadline.After(time.Now()) {
		return errors.New("expected future output deadline")
	}
	return c.Conn.SetWriteDeadline(time.Now().Add(30 * time.Millisecond))
}

func TestOutputWriteTimeout(t *testing.T) {
	a, b := net.Pipe()
	c := &client{conn: shortenedOutputDeadline{a}}
	t.Cleanup(func() { b.Close(); c.stopOutput(); c.waitOutput() })
	c.send(msgOK, []byte{1})
	done := make(chan struct{})
	go func() { c.waitOutput(); close(done) }()
	waitSignal(t, done, "stalled write deadline and writer exit")
	if _, _, err := readPacket(b); err == nil {
		t.Fatal("timed-out frame reached reader")
	}
}

func TestOutputShutdownWithPendingFrames(t *testing.T) {
	s, addr := startServer(t)
	a := dial(t, addr, "NPWR00001_00", "alice", 3658)
	s.mu.Lock()
	var player *client
	for _, c := range s.clients {
		player = c
	}
	player.send(msgOK, make([]byte, maxPayload))
	player.send(msgOK, make([]byte, 60000))
	s.mu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := s.Shutdown(ctx); err != nil {
		t.Fatal(err)
	}
	player.output.mu.Lock()
	done, closed := player.output.done, player.output.closed
	player.output.mu.Unlock()
	if !closed {
		t.Fatal("shutdown left output accepting frames")
	}
	waitSignal(t, done, "writer joined before shutdown returned")
	s.mu.Lock()
	clients, rooms, open := len(s.clients), len(s.rooms), s.openConns
	s.mu.Unlock()
	if clients != 0 || rooms != 0 || open != 0 {
		t.Fatalf("shutdown state: clients=%d rooms=%d open=%d", clients, rooms, open)
	}
	a.conn.Close()
}

func TestOutputDefaultPeerAuthBurst(t *testing.T) {
	old := runtime.GOMAXPROCS(1)
	defer runtime.GOMAXPROCS(old)
	s := NewServer(10, false)
	c, conn := outputTestClient(t)
	c.id = 1
	c.commID = "NPWR00001_00"
	s.clients[c.id] = c
	peers := make([]*client, 254)
	for i := range peers {
		peer, pc := outputTestClient(t)
		peer.id = uint32(i + 2)
		peer.commID = c.commID
		peer.peerAuth = true
		close(pc.release)
		peers[i] = peer
	}
	for i := 0; i < 8; i++ {
		r := &room{id: uint64(i + 1), commID: c.commID, max: 255}
		s.rooms[r.id] = r
		r.owner = s.addMember(r, c, nil).id
		for _, peer := range peers {
			s.addMember(r, peer, nil)
		}
	}
	request := &writer{}
	request.u32(1)
	s.handle(c, msgPeerAuth, request.b)
	// The first writer is gated, so all 2033 frames remain charged together.
	select {
	case <-conn.closed:
		t.Fatal("default legal PEER_AUTH burst overflowed")
	default:
	}
	close(conn.release)
	first := outputFrame(t, conn)
	if first[0] != msgOK {
		t.Fatalf("first frame %x, want OK", first[0])
	}
	seen := map[[2]uint64]bool{}
	for i := 0; i < 8*254; i++ {
		frame := outputFrame(t, conn)
		if frame[0] != msgPeerKey || len(frame) != 31 {
			t.Fatalf("key frame type=%x len=%d", frame[0], len(frame))
		}
		r := &reader{b: frame[3:]}
		key := [2]uint64{r.u64(), uint64(r.u32())}
		if seen[key] {
			t.Fatalf("duplicate room/user key %v", key)
		}
		seen[key] = true
	}
	if len(seen) != 2032 {
		t.Fatalf("keys=%d", len(seen))
	}
}

func TestOutputMaximumJoinBurst(t *testing.T) {
	s := NewServer(10, false)
	s.relay = true
	c, conn := outputTestClient(t)
	c.id = 1
	c.commID = "NPWR00001_00"
	c.peerAuth = true
	s.clients[c.id] = c
	r := &room{id: 1, commID: c.commID, max: 255}
	s.rooms[r.id] = r
	for i := 0; i < 254; i++ {
		peer, pc := outputTestClient(t)
		peer.id = uint32(i + 2)
		peer.commID = c.commID
		peer.peerAuth = true
		close(pc.release)
		s.addMember(r, peer, nil)
	}
	r.owner = r.members[0].id
	// Fill legal per-member blobs until adding one more member yields exactly
	// the largest legal ROOM_JOINED frame, then also require all route/key pushes.
	for _, m := range r.members {
		remaining := maxPayload - joinedSize(r, 0)
		if remaining == 0 {
			break
		}
		m.data = make([]byte, min(4096, remaining))
	}
	if size := joinedSize(r, 0); size != maxPayload {
		t.Fatalf("fixture join size %d", size)
	}
	w := &writer{}
	w.u32(1)
	w.u64(r.id)
	w.blob(nil)
	s.handle(c, msgJoinRoom, w.b)
	select {
	case <-conn.closed:
		t.Fatal("maximum legal join burst overflowed")
	default:
	}
	close(conn.release)
	first := outputFrame(t, conn)
	if first[0] != msgRoomJoined || len(first) != maxPayload+3 {
		t.Fatalf("join frame type=%x len=%d", first[0], len(first))
	}
	for i := 0; i < 254; i++ {
		if frame := outputFrame(t, conn); frame[0] != msgRoute {
			t.Fatalf("route frame %x", frame[0])
		}
	}
	for i := 0; i < 254; i++ {
		if frame := outputFrame(t, conn); frame[0] != msgPeerKey {
			t.Fatalf("key frame %x", frame[0])
		}
	}
}
