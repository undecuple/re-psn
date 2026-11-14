package main

import (
	"encoding/binary"
	"io"
	"sync"
	"time"
)

// Both limits include the frame currently being written, including its three
// wire-header bytes. A maximum-size legal frame (65,538 bytes) fits. With the
// default 4096-connection limit, encoded outbound frame storage is at most
// 512 MiB, plus lazily grown rings (192 MiB resident descriptors on 64-bit
// systems, temporarily 288 MiB during growth),
// goroutine stacks and other server state. The frame cap accommodates PEER_AUTH
// across eight default-limit rooms of 255 members: 2032 keys plus one reply. Disabling
// that connection limit removes the aggregate bound, not these per-client caps.
const (
	maxOutputFrames = 2048
	maxOutputBytes  = 128 * 1024
)

type clientOutput struct {
	mu        sync.Mutex
	frames    [][]byte
	head      int
	queued    int
	retained  int // queued plus in-flight frames
	bytes     int // encoded bytes, queued plus in-flight
	closed    bool
	wake      chan struct{}
	done      chan struct{}
	closeDone chan struct{}
}

// send is nonblocking with respect to socket I/O. Its private, exact-size frame
// copy makes the caller's payload reusable immediately after enqueue. Room
// handlers call it under s.mu so a recipient's FIFO follows state transitions.
func (c *client) send(t byte, p []byte) {
	o := &c.output
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.closed {
		return
	}
	n := 3 + len(p)
	if len(p) > maxPayload || o.retained == maxOutputFrames || n > maxOutputBytes-o.bytes {
		c.stopOutputLocked()
		return
	}
	if o.done == nil {
		o.wake, o.done = make(chan struct{}, 1), make(chan struct{})
		go c.writeOutput()
	}
	if o.queued == len(o.frames) {
		frames := make([][]byte, min(maxOutputFrames, max(16, 2*len(o.frames))))
		for i := 0; i < o.queued; i++ {
			frames[i] = o.frames[(o.head+i)%len(o.frames)]
		}
		o.frames, o.head = frames, 0
	}
	frame := make([]byte, n)
	frame[0] = t
	binary.BigEndian.PutUint16(frame[1:], uint16(len(p)))
	copy(frame[3:], p)
	o.frames[(o.head+o.queued)%len(o.frames)] = frame
	o.queued++
	o.retained++
	o.bytes += n
	select {
	case o.wake <- struct{}{}:
	default:
	}
}

func (c *client) outputClosed() bool {
	c.output.mu.Lock()
	defer c.output.mu.Unlock()
	return c.output.closed
}

func (c *client) stopOutput() {
	c.output.mu.Lock()
	defer c.output.mu.Unlock()
	c.stopOutputLocked()
}

// Closing a net.Conn may run arbitrary wrapper code. Do it once in an owned
// goroutine, outside both locks, so overflow can interrupt an in-flight writer
// without blocking a room transition. The reader joins both on exit.
func (c *client) stopOutputLocked() {
	o := &c.output
	if o.closed {
		return
	}
	o.closed = true
	o.frames = nil
	o.queued, o.retained, o.bytes = 0, 0, 0
	o.closeDone = make(chan struct{})
	go func() {
		defer close(o.closeDone)
		c.conn.Close()
	}()
	if o.wake != nil {
		select {
		case o.wake <- struct{}{}:
		default:
		}
	}
}

func (c *client) waitOutput() {
	o := &c.output
	o.mu.Lock()
	done, closed := o.done, o.closeDone
	o.mu.Unlock()
	if done != nil {
		<-done
	}
	if closed != nil {
		<-closed
	}
}

func (c *client) writeOutput() {
	o := &c.output
	defer close(o.done)
	for {
		o.mu.Lock()
		if o.closed {
			o.mu.Unlock()
			return
		}
		if o.queued == 0 {
			o.mu.Unlock()
			<-o.wake
			continue
		}
		frame := o.frames[o.head]
		o.frames[o.head] = nil
		o.head = (o.head + 1) % len(o.frames)
		o.queued--
		o.mu.Unlock()

		err := c.conn.SetWriteDeadline(time.Now().Add(writeTimeout))
		if err == nil {
			var n int
			n, err = c.conn.Write(frame)
			if err == nil && n != len(frame) {
				err = io.ErrShortWrite
			}
		}
		n := len(frame)
		frame = nil
		o.mu.Lock()
		if !o.closed {
			o.retained--
			o.bytes -= n
		}
		if err != nil {
			c.stopOutputLocked()
		}
		o.mu.Unlock()
	}
}
