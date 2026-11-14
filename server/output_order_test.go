package main

import (
	"context"
	"net"
	"sync"
	"testing"
	"time"
)

// gateWriteConn makes the next write wait before it reaches the socket. The
// state transition has already happened when blocked closes; Close releases it.
type gateWriteConn struct {
	net.Conn
	mu      sync.Mutex
	gate    chan struct{}
	blocked chan struct{}
	closed  chan struct{}
	once    sync.Once
}

func (c *gateWriteConn) hold() (<-chan struct{}, func()) {
	c.mu.Lock()
	defer c.mu.Unlock()
	gate, blocked := make(chan struct{}), make(chan struct{})
	c.gate, c.blocked = gate, blocked
	var once sync.Once
	return blocked, func() { once.Do(func() { close(gate) }) }
}
func (c *gateWriteConn) Write(p []byte) (int, error) {
	c.mu.Lock()
	gate, blocked := c.gate, c.blocked
	c.gate, c.blocked = nil, nil
	c.mu.Unlock()
	if gate != nil {
		close(blocked)
		select {
		case <-gate:
		case <-c.closed:
			return 0, net.ErrClosed
		}
	}
	return c.Conn.Write(p)
}
func (c *gateWriteConn) Close() error { c.once.Do(func() { close(c.closed) }); return c.Conn.Close() }

func waitSignal(t *testing.T, ch <-chan struct{}, what string) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(2 * time.Second):
		t.Fatalf("timed out: %s", what)
	}
}

// orderPipeListener exercises Serve admission and connection ownership over
// local pipes. Its one-slot handoff bounds pending connections.
type orderPipeListener struct {
	conns  chan net.Conn
	closed chan struct{}
	once   sync.Once
}

func (ln *orderPipeListener) Accept() (net.Conn, error) {
	select {
	case c := <-ln.conns:
		return c, nil
	case <-ln.closed:
		return nil, net.ErrClosed
	}
}
func (ln *orderPipeListener) Close() error { ln.once.Do(func() { close(ln.closed) }); return nil }
func (ln *orderPipeListener) Addr() net.Addr {
	return &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 1}
}

type orderPipes struct {
	t      *testing.T
	s      *Server
	ln     *orderPipeListener
	conns  []*gateWriteConn
	peers  []net.Conn
	served chan struct{}
}

func newOrderPipes(t *testing.T) *orderPipes {
	f := &orderPipes{t: t, s: NewServer(10, false), ln: &orderPipeListener{conns: make(chan net.Conn, 1), closed: make(chan struct{})}, served: make(chan struct{})}
	go func() { defer close(f.served); f.s.Serve(f.ln) }()
	t.Cleanup(func() {
		for _, c := range f.conns {
			c.Close()
		}
		for _, c := range f.peers {
			c.Close()
		}
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		if err := f.s.Shutdown(ctx); err != nil {
			t.Errorf("pipe server shutdown: %v", err)
		}
		f.ln.Close()
		waitSignal(t, f.served, "Serve and owned-handler cleanup")
	})
	return f
}
func (f *orderPipes) dial(name string) (*tc, *gateWriteConn) {
	a, b := net.Pipe()
	c := &gateWriteConn{Conn: a, closed: make(chan struct{})}
	f.conns = append(f.conns, c)
	f.peers = append(f.peers, b)
	select {
	case f.ln.conns <- c:
	case <-f.ln.closed:
		f.t.Fatal("pipe listener closed")
	case <-time.After(2 * time.Second):
		f.t.Fatal("pipe listener did not accept")
	}
	return helloConn(f.t, b, "NPWR00001_00", name, 3658), c
}
func orderRoom(t *testing.T, a *tc) uint64 {
	t.Helper()
	w := &writer{}
	w.u8(4)
	w.u32(0)
	w.blob(nil)
	w.blob(nil)
	w.blob(nil)
	typ, r := a.call(msgCreateRoom, w.b)
	if typ != msgRoomJoined {
		t.Fatalf("create: %x", typ)
	}
	return r.u64()
}
func orderJoin(t *testing.T, c *tc, room uint64) uint16 {
	t.Helper()
	w := &writer{}
	w.u64(room)
	w.blob(nil)
	typ, r := c.call(msgJoinRoom, w.b)
	if typ != msgRoomJoined {
		t.Fatalf("join: %x", typ)
	}
	r.u64()
	return r.u16()
}

// This regression runs against the old server unchanged: there the first
// observed event is B's transition, and A's stale owner assignment arrives last.
func TestRoomEventsFollowTransitions(t *testing.T) {
	for _, next := range []string{"leave", "disconnect", "join", "kick"} {
		t.Run(next, func(t *testing.T) {
			f := newOrderPipes(t)
			a, ac := f.dial("alice")
			b, _ := f.dial("bob")
			c, _ := f.dial("carol")
			room := orderRoom(t, a)
			bm := orderJoin(t, b, room)
			a.next(msgMemberJoined)
			cm := orderJoin(t, c, room)
			a.next(msgMemberJoined)
			b.next(msgMemberJoined)
			var d *tc
			var dm uint16
			if next == "kick" {
				d, _ = f.dial("dave")
				dm = orderJoin(t, d, room)
				a.next(msgMemberJoined)
				b.next(msgMemberJoined)
				c.next(msgMemberJoined)
			}
			f.s.mu.Lock()
			am := f.s.rooms[room].members[0].id
			f.s.mu.Unlock()
			blocked, release := ac.hold()
			defer release()
			a.req++
			w := &writer{}
			w.u32(a.req)
			w.u64(room)
			if err := writePacket(a.conn, msgLeaveRoom, w.b); err != nil {
				t.Fatal(err)
			}
			waitSignal(t, blocked, "owner leave reply blocked")
			switch next {
			case "leave":
				w = &writer{}
				w.u64(room)
				if typ, _ := b.call(msgLeaveRoom, w.b); typ != msgOK {
					t.Fatalf("leave: %x", typ)
				}
			case "disconnect":
				b.conn.Close()
			case "join":
				d, _ = f.dial("dave")
				dm = orderJoin(t, d, room)
			case "kick":
				w = &writer{}
				w.u64(room)
				w.u16(dm)
				if typ, _ := b.call(msgKickMember, w.b); typ != msgOK {
					t.Fatalf("kick: %x", typ)
				}
			}
			first := c.next(msgMemberLeft)
			if got, gone, owner := first.u64(), first.u16(), first.u16(); got != room || gone != am || owner != bm {
				t.Fatalf("first transition room=%d gone=%d owner=%d, want room=%d gone=%d owner=%d", got, gone, owner, room, am, bm)
			}
			owner := bm
			switch next {
			case "leave", "disconnect":
				r := c.next(msgMemberLeft)
				if got, gone, newOwner := r.u64(), r.u16(), r.u16(); got != room || gone != bm || newOwner != cm {
					t.Fatalf("second leave room=%d gone=%d owner=%d", got, gone, newOwner)
				}
				owner = cm
			case "join":
				r := c.next(msgMemberJoined)
				if got, member := r.u64(), r.u16(); got != room || member != dm {
					t.Fatalf("join room=%d member=%d", got, member)
				}
			case "kick":
				r := c.next(msgMemberLeft)
				if got, gone, newOwner := r.u64(), r.u16(), r.u16(); got != room || gone != dm || newOwner != bm {
					t.Fatalf("kick room=%d gone=%d owner=%d", got, gone, newOwner)
				}
			}
			f.s.mu.Lock()
			actual := f.s.rooms[room].owner
			f.s.mu.Unlock()
			if actual != owner {
				t.Fatalf("authoritative owner %d, latest delivered owner %d", actual, owner)
			}
			release()
		})
	}
}
