// Package client speaks the re-psn server's TCP protocol (docs/api.md): one
// connection per player, requests matched to replies by request id, and
// server pushes delivered on a channel. It has no UI and no global state.
package client

import (
	"context"
	"errors"
	"fmt"
	"net"
	"sync"
	"sync/atomic"
	"time"
)

// DefaultTimeout bounds a dial or a write when the context has no deadline.
const DefaultTimeout = 10 * time.Second

// HeartbeatEvery is how often KeepAlive sends a HEARTBEAT by default; the
// server drops a connection that is silent for 90 seconds.
const HeartbeatEvery = 20 * time.Second

// ErrorCode is the code in an ERROR reply.
type ErrorCode uint32

const (
	ErrBadRequest   ErrorCode = 1
	ErrNotFound     ErrorCode = 2
	ErrRoomFull     ErrorCode = 3
	ErrNotOwner     ErrorCode = 4
	ErrNotInRoom    ErrorCode = 5
	ErrLimitReached ErrorCode = 6
	ErrNoHello      ErrorCode = 7
	ErrNameTaken    ErrorCode = 8
	ErrNoRelay      ErrorCode = 9
)

var codeNames = map[ErrorCode]string{
	ErrBadRequest: "bad request", ErrNotFound: "not found", ErrRoomFull: "room full",
	ErrNotOwner: "not the room owner", ErrNotInRoom: "not in that room", ErrLimitReached: "limit reached",
	ErrNoHello: "no HELLO yet", ErrNameTaken: "name taken", ErrNoRelay: "no relay",
}

func (c ErrorCode) String() string {
	if s, ok := codeNames[c]; ok {
		return s
	}
	return "unknown code"
}

// ServerError is an ERROR reply to a request.
type ServerError struct {
	Op   string // the request, e.g. "join room"
	Code ErrorCode
}

func (e *ServerError) Error() string {
	return fmt.Sprintf("%s: server answered error %d (%s)", e.Op, uint32(e.Code), e.Code)
}

// ErrClosed is returned by requests on a connection that has ended.
var ErrClosed = errors.New("connection closed")

type frame struct {
	t byte
	p []byte
}

// Conn is one connection to a server. Its methods are safe for concurrent use.
type Conn struct {
	nc      net.Conn
	wmu     sync.Mutex
	nextReq atomic.Uint32

	rtt       rttTracker  // round trips of recent calls (rtt.go)
	reportRTT atomic.Bool // Heartbeat carries the smallest of them

	mu      sync.Mutex
	waiting map[uint32]chan frame
	queue   []Push
	err     error // why the connection ended, once it has

	wake   chan struct{} // a push was queued
	done   chan struct{} // the read loop ended
	closed chan struct{} // Close was called
	once   sync.Once
	events chan Push
}

// Dial connects to a server at addr (host:port). Without a deadline on ctx it waits at most DefaultTimeout.
func Dial(ctx context.Context, addr string) (*Conn, error) {
	if _, ok := ctx.Deadline(); !ok {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, DefaultTimeout)
		defer cancel()
	}
	var d net.Dialer
	nc, err := d.DialContext(ctx, "tcp", addr)
	if err != nil {
		return nil, fmt.Errorf("dial %s: %w", addr, err)
	}
	return newConn(nc), nil
}

func newConn(nc net.Conn) *Conn {
	c := &Conn{
		nc:      nc,
		waiting: map[uint32]chan frame{},
		wake:    make(chan struct{}, 1),
		done:    make(chan struct{}),
		closed:  make(chan struct{}),
		events:  make(chan Push),
	}
	go c.readLoop()
	go c.pump()
	return c
}

// Events delivers server pushes in the order they arrived. It is closed once
// the connection has ended and every queued push was received, or at Close.
func (c *Conn) Events() <-chan Push { return c.events }

// Close ends the connection. Requests waiting for a reply return ErrClosed.
func (c *Conn) Close() error {
	var err error
	c.once.Do(func() {
		close(c.closed)
		err = c.nc.Close()
	})
	<-c.done
	return err
}

func (c *Conn) readLoop() {
	defer close(c.done)
	for {
		t, p, err := readFrame(c.nc)
		if err == nil {
			err = c.dispatch(t, p)
		}
		if err != nil {
			c.fail(err)
			return
		}
	}
}

// dispatch hands a reply to its waiting request, or queues a push.
func (c *Conn) dispatch(t byte, p []byte) error {
	if isPush(t) {
		ev, err := decodePush(t, p)
		if err != nil {
			return err
		}
		c.mu.Lock()
		c.queue = append(c.queue, ev)
		c.mu.Unlock()
		select {
		case c.wake <- struct{}{}:
		default:
		}
		return nil
	}
	d := &decoder{b: p}
	req := d.u32()
	if d.err != nil {
		return fmt.Errorf("reply type 0x%02X: no request id", t)
	}
	c.mu.Lock()
	ch := c.waiting[req]
	delete(c.waiting, req)
	c.mu.Unlock()
	if ch != nil { // else the request gave up waiting
		ch <- frame{t, d.b}
	}
	return nil
}

func (c *Conn) fail(err error) {
	select {
	case <-c.closed:
		err = ErrClosed
	default:
		c.nc.Close()
		err = fmt.Errorf("connection ended: %w", err)
	}
	c.mu.Lock()
	c.err = err
	c.mu.Unlock()
}

// pump moves queued pushes to the Events channel, so a slow reader of
// Events never holds up replies.
func (c *Conn) pump() {
	defer close(c.events)
	for {
		c.mu.Lock()
		var ev Push
		if len(c.queue) > 0 {
			ev, c.queue = c.queue[0], c.queue[1:]
		}
		c.mu.Unlock()
		if ev == nil {
			select {
			case <-c.wake:
				continue
			case <-c.done:
				if c.queueEmpty() {
					return
				}
				continue
			case <-c.closed:
				return
			}
		}
		select {
		case c.events <- ev:
		case <-c.closed:
			return
		}
	}
}

func (c *Conn) queueEmpty() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.queue) == 0
}

// endErr says why the connection ended.
func (c *Conn) endErr() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.err == nil {
		return ErrClosed
	}
	return c.err
}

// send writes one request frame: the request id, then body's fields.
func (c *Conn) send(ctx context.Context, t byte, req uint32, body func(*encoder)) error {
	e := &encoder{}
	e.u32(req)
	if body != nil {
		body(e)
	}
	deadline, ok := ctx.Deadline()
	if !ok {
		deadline = time.Now().Add(DefaultTimeout)
	}
	c.wmu.Lock()
	defer c.wmu.Unlock()
	c.nc.SetWriteDeadline(deadline)
	if err := writeFrame(c.nc, t, e.b); err != nil {
		c.nc.Close() // a part-written frame leaves the stream unusable
		return err
	}
	return nil
}

// call sends a request and waits for its reply, which must be of type want.
// It returns a decoder over the reply's fields after the request id.
func (c *Conn) call(ctx context.Context, op string, t, want byte, body func(*encoder)) (*decoder, error) {
	req := c.nextReq.Add(1)
	ch := make(chan frame, 1)
	started := time.Now()
	c.mu.Lock()
	if c.err != nil {
		c.mu.Unlock()
		return nil, fmt.Errorf("%s: %w", op, c.err)
	}
	c.waiting[req] = ch
	c.mu.Unlock()
	defer func() {
		c.mu.Lock()
		delete(c.waiting, req)
		c.mu.Unlock()
	}()

	if err := c.send(ctx, t, req, body); err != nil {
		return nil, fmt.Errorf("%s: send: %w", op, err)
	}
	select {
	case f := <-ch:
		c.rtt.note(time.Since(started))
		return replyFields(op, want, f)
	case <-ctx.Done():
		return nil, fmt.Errorf("%s: waiting for the reply: %w", op, ctx.Err())
	case <-c.done:
		return nil, fmt.Errorf("%s: %w", op, c.endErr())
	}
}

func replyFields(op string, want byte, f frame) (*decoder, error) {
	d := &decoder{b: f.p}
	switch f.t {
	case want:
		return d, nil
	case typeError:
		code := ErrorCode(d.u32())
		if d.err != nil {
			return nil, fmt.Errorf("%s: ERROR reply: %w", op, d.err)
		}
		return nil, &ServerError{Op: op, Code: code}
	}
	return nil, fmt.Errorf("%s: reply type 0x%02X, want 0x%02X", op, f.t, want)
}

// finish checks the decoder after the last field.
func finish[T any](op string, d *decoder, v T) (T, error) {
	if d.err != nil {
		var zero T
		return zero, fmt.Errorf("%s: reply: %w", op, d.err)
	}
	return v, nil
}
