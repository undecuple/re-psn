package framework

import (
	"bytes"
	"crypto/rand"
	"crypto/subtle"
	"encoding/binary"
	"errors"
	"io"
	"net"
	"sync"
	"time"
)

type Grant struct {
	Token     [32]byte `json:"token"`
	TitleID   string   `json:"title_id"`
	ExpiresAt int64    `json:"expires_at"`
}
type brokerGrant struct {
	Grant
	window time.Time
	calls  int
}
type Broker struct {
	mu        sync.Mutex
	store     *Store
	grants    []brokerGrant
	listener  net.Listener
	conns     map[net.Conn]bool
	closed    bool
	closeDone chan struct{}
	wg        sync.WaitGroup
}

func NewBroker(store *Store) *Broker {
	return &Broker{store: store, conns: map[net.Conn]bool{}, closeDone: make(chan struct{})}
}
func (b *Broker) Grant(title string) (Grant, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed {
		return Grant{}, ErrClosed
	}
	found := false
	for _, t := range b.store.View().Titles {
		if t.Catalog.TitleID == title {
			found = true
		}
	}
	if !found {
		return Grant{}, ErrNotFound
	}
	now := time.Now()
	active := b.grants[:0]
	for _, g := range b.grants {
		if g.ExpiresAt > now.UnixMilli() {
			active = append(active, g)
		}
	}
	b.grants = active
	if len(b.grants) >= 128 {
		return Grant{}, ErrBusy
	}
	g := Grant{TitleID: title, ExpiresAt: now.Add(15 * time.Minute).UnixMilli()}
	if _, err := rand.Read(g.Token[:]); err != nil {
		return Grant{}, err
	}
	b.grants = append(b.grants, brokerGrant{Grant: g})
	return g, nil
}
func (b *Broker) Revoke(token [32]byte) {
	b.mu.Lock()
	defer b.mu.Unlock()
	for i := range b.grants {
		if subtle.ConstantTimeCompare(token[:], b.grants[i].Token[:]) == 1 {
			b.grants = append(b.grants[:i], b.grants[i+1:]...)
			return
		}
	}
}

// Renew is host-only: it extends a live grant, never an expired/revoked one.
// It does not reset rate counters or change the title/profile capability.
func (b *Broker) Renew(token [32]byte) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed {
		return ErrClosed
	}
	now := time.Now()
	for i := range b.grants {
		g := &b.grants[i]
		if subtle.ConstantTimeCompare(token[:], g.Token[:]) == 1 {
			if g.ExpiresAt <= now.UnixMilli() {
				return ErrUnauthorized
			}
			g.ExpiresAt = now.Add(15 * time.Minute).UnixMilli()
			return nil
		}
	}
	return ErrUnauthorized
}
func loopbackListener(l net.Listener) bool {
	a, ok := l.Addr().(*net.TCPAddr)
	return ok && a.IP.Equal(net.IPv4(127, 0, 0, 1))
}
func (b *Broker) Serve(l net.Listener) error {
	if l == nil {
		return ErrInvalid
	}
	// A Serve call owns its supplied listener, including rejected/closed starts.
	defer l.Close()
	if !loopbackListener(l) {
		return ErrInvalid
	}
	b.mu.Lock()
	if b.closed || b.listener != nil {
		b.mu.Unlock()
		return ErrBusy
	}
	b.listener = l
	b.mu.Unlock()
	for {
		c, err := l.Accept()
		if err != nil {
			b.mu.Lock()
			closed := b.closed
			b.mu.Unlock()
			if closed {
				return nil
			}
			return err
		}
		b.mu.Lock()
		if b.closed || len(b.conns) >= 16 {
			b.mu.Unlock()
			c.Close()
			continue
		}
		b.conns[c] = true
		b.wg.Add(1)
		b.mu.Unlock()
		go b.handle(c)
	}
}
func (b *Broker) Close() error {
	b.mu.Lock()
	if b.closed {
		b.mu.Unlock()
		<-b.closeDone
		return nil
	}
	b.closed = true
	b.grants = nil
	l := b.listener
	conns := make([]net.Conn, 0, len(b.conns))
	for c := range b.conns {
		conns = append(conns, c)
	}
	b.mu.Unlock()
	if l != nil {
		l.Close()
	}
	for _, c := range conns {
		c.Close()
	}
	b.wg.Wait()
	close(b.closeDone)
	return nil
}
func (b *Broker) handle(c net.Conn) {
	defer func() { c.Close(); b.mu.Lock(); delete(b.conns, c); b.mu.Unlock(); b.wg.Done() }()
	_ = c.SetDeadline(time.Now().Add(2 * time.Second))
	var h [12]byte
	if _, err := io.ReadFull(c, h[:]); err != nil {
		return
	}
	if string(h[:4]) != "RPT1" || h[5] != 0 || h[6] != 0 || h[7] != 0 || binary.BigEndian.Uint32(h[8:]) != 36 || (h[4] != 1 && h[4] != 2) {
		b.reply(c, 4, Unlock{})
		return
	}
	var body [36]byte
	if _, err := io.ReadFull(c, body[:]); err != nil {
		return
	}
	var trailing [1]byte
	n, err := c.Read(trailing[:])
	if n != 0 || err != io.EOF {
		b.reply(c, 4, Unlock{})
		return
	}
	// Hold the grant lock through the mutation so Revoke has an exact boundary.
	b.mu.Lock()
	code := byte(1)
	var result Unlock
	if !b.closed {
		for i := range b.grants {
			g := &b.grants[i]
			if subtle.ConstantTimeCompare(g.Token[:], body[:32]) != 1 {
				continue
			}
			now := time.Now()
			if g.ExpiresAt <= now.UnixMilli() {
				break
			}
			if now.Sub(g.window) >= time.Second {
				g.window = now
				g.calls = 0
			}
			if g.calls >= 100 {
				code = 6
				break
			}
			g.calls++
			id := binary.BigEndian.Uint32(body[32:])
			if h[4] == 1 {
				result, err = b.store.Unlock(g.TitleID, id)
			} else {
				result, err = b.store.Status(g.TitleID, id)
			}
			code = 0
			if errors.Is(err, ErrNotFound) {
				code = 2
			} else if err != nil {
				code = 5
			}
			break
		}
	}
	b.mu.Unlock()
	b.reply(c, code, result)
}
func (b *Broker) reply(c net.Conn, code byte, u Unlock) {
	var out [21]byte
	copy(out[:4], "RPR1")
	out[4] = code
	n := 12
	if code == 0 {
		binary.BigEndian.PutUint32(out[8:12], 9)
		if u.Unlocked {
			out[12] = 1
		}
		binary.BigEndian.PutUint64(out[13:], uint64(u.UnlockedAt))
		n = 21
	}
	_, _ = io.Copy(c, bytes.NewReader(out[:n]))
}
