package client

import (
	"context"
	"errors"
	"net"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type observedWriteConn struct {
	net.Conn
	started chan struct{}
	once    sync.Once
	writes  atomic.Int32
}

func (c *observedWriteConn) Write(p []byte) (int, error) {
	c.writes.Add(1)
	c.once.Do(func() { close(c.started) })
	return c.Conn.Write(p)
}

func pipeClient(t *testing.T) (*Conn, *observedWriteConn, net.Conn) {
	t.Helper()
	nc, peer := net.Pipe()
	observed := &observedWriteConn{Conn: nc, started: make(chan struct{})}
	c := newConn(observed)
	t.Cleanup(func() { c.Close(); peer.Close() })
	return c, observed, peer
}

func awaitResult(t *testing.T, result <-chan error) error {
	t.Helper()
	select {
	case err := <-result:
		return err
	case <-time.After(time.Second):
		t.Fatal("request did not finish promptly")
		return nil
	}
}

func TestAlreadyCanceledCallDoesNotSend(t *testing.T) {
	c, observed, peer := pipeClient(t)
	go func() { readFrame(peer) }() // let an erroneous write finish, rather than hiding it behind a timeout
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := c.CreateRoom(ctx, CreateRoom{Max: 2})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("create room: %v, want context.Canceled", err)
	}
	if n := observed.writes.Load(); n != 0 {
		t.Fatalf("already canceled room creation wrote %d frame(s)", n)
	}
}

func TestCancelInterruptsWriteWithoutDeadline(t *testing.T) {
	c, observed, _ := pipeClient(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	result := make(chan error, 1)
	go func() { result <- c.Heartbeat(ctx) }()
	<-observed.started // the peer does not read: the write is now blocked
	cancel()
	if err := awaitResult(t, result); !errors.Is(err, context.Canceled) {
		t.Fatalf("heartbeat: %v, want context.Canceled", err)
	}
}

func TestCancelQueuedCallPreservesActiveWriter(t *testing.T) {
	c, observed, peer := pipeClient(t)
	first := make(chan error, 1)
	go func() { first <- c.Heartbeat(context.Background()) }()
	<-observed.started

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	encoded := make(chan struct{})
	queued := make(chan error, 1)
	go func() {
		_, err := c.call(ctx, "queued search", typeSearchRooms, typeRoomList, func(e *encoder) {
			e.u16(0)
			e.u16(1)
			close(encoded)
		})
		queued <- err
	}()
	<-encoded // the second request is ready while the first owns the writer
	cancel()
	if err := awaitResult(t, queued); !errors.Is(err, context.Canceled) {
		t.Fatalf("queued search: %v, want context.Canceled", err)
	}
	select {
	case err := <-first:
		t.Fatalf("canceling the queued request interrupted the active writer: %v", err)
	default:
	}
	peer.SetReadDeadline(time.Now().Add(time.Second))
	if typ, _, err := readFrame(peer); err != nil || typ != typeHeartbeat {
		t.Fatalf("active frame: type %#x, error %v", typ, err)
	}
	if err := awaitResult(t, first); err != nil {
		t.Fatalf("active heartbeat: %v", err)
	}

	// A canceled waiter must neither close the connection nor leak a deadline
	// callback into the next writer.
	next := make(chan error, 1)
	go func() { next <- c.Heartbeat(context.Background()) }()
	if typ, _, err := readFrame(peer); err != nil || typ != typeHeartbeat {
		t.Fatalf("next frame: type %#x, error %v", typ, err)
	}
	if err := awaitResult(t, next); err != nil {
		t.Fatalf("next heartbeat: %v", err)
	}
	if n := observed.writes.Load(); n != 2 {
		t.Fatalf("got %d writes, want the two heartbeats only", n)
	}
}
