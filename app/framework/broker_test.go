package framework

import (
	"encoding/binary"
	"io"
	"net"
	"testing"
	"time"
)

func startBroker(t *testing.T, s *Store) (*Broker, string) {
	t.Helper()
	l, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	b := NewBroker(s)
	done := make(chan error, 1)
	go func() { done <- b.Serve(l) }()
	t.Cleanup(func() {
		b.Close()
		if err := <-done; err != nil {
			t.Error(err)
		}
	})
	return b, l.Addr().String()
}
func brokerCall(t *testing.T, addr string, token [32]byte, id uint32, op byte, extra bool) (byte, Unlock) {
	t.Helper()
	c, err := net.DialTimeout("tcp4", addr, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	c.SetDeadline(time.Now().Add(3 * time.Second))
	var req [48]byte
	copy(req[:4], "RPT1")
	req[4] = op
	binary.BigEndian.PutUint32(req[8:], 36)
	copy(req[12:44], token[:])
	binary.BigEndian.PutUint32(req[44:], id)
	if _, err := c.Write(req[:]); err != nil {
		t.Fatal(err)
	}
	if extra {
		c.Write([]byte{1})
	}
	if err := c.(*net.TCPConn).CloseWrite(); err != nil {
		t.Fatal(err)
	}
	var h [12]byte
	if _, err := io.ReadFull(c, h[:]); err != nil {
		t.Fatal(err)
	}
	if string(h[:4]) != "RPR1" {
		t.Fatal(h)
	}
	if h[4] != 0 {
		if binary.BigEndian.Uint32(h[8:]) != 0 {
			t.Fatal(h)
		}
		return h[4], Unlock{}
	}
	if binary.BigEndian.Uint32(h[8:]) != 9 {
		t.Fatal(h)
	}
	var result [9]byte
	if _, err := io.ReadFull(c, result[:]); err != nil {
		t.Fatal(err)
	}
	return 0, Unlock{TrophyID: id, Unlocked: result[0] == 1, UnlockedAt: int64(binary.BigEndian.Uint64(result[1:]))}
}
func TestBrokerScopedAndRevoked(t *testing.T) {
	s := fixtureStore(t, "Synthetic broker player")
	for _, title := range []string{"SYNTHETIC-A", "SYNTHETIC-B"} {
		if err := s.InstallCatalog(fixtureCatalog(title)); err != nil {
			t.Fatal(err)
		}
	}
	b, addr := startBroker(t, s)
	g, err := b.Grant("SYNTHETIC-A")
	if err != nil {
		t.Fatal(err)
	}
	wrong := g.Token
	wrong[0] ^= 1
	if code, _ := brokerCall(t, addr, wrong, 7, 1, false); code != 1 {
		t.Fatal(code)
	}
	if code, _ := brokerCall(t, addr, g.Token, 8, 1, false); code != 2 {
		t.Fatal(code)
	}
	if code, _ := brokerCall(t, addr, g.Token, 7, 1, true); code != 4 {
		t.Fatal(code)
	}
	if u, _ := s.Status("SYNTHETIC-A", 7); u.Unlocked {
		t.Fatal("trailing bytes mutated state")
	}
	code, u := brokerCall(t, addr, g.Token, 7, 1, false)
	if code != 0 || !u.Unlocked || u.UnlockedAt < time.Now().Add(-time.Minute).UnixMilli() {
		t.Fatal(code, u)
	}
	if other, _ := s.Status("SYNTHETIC-B", 7); other.Unlocked {
		t.Fatal("cross-title unlock")
	}
	if code, again := brokerCall(t, addr, g.Token, 7, 2, false); code != 0 || again != u {
		t.Fatal(code, again)
	}
	b.Revoke(g.Token)
	if code, _ := brokerCall(t, addr, g.Token, 9, 1, false); code != 1 {
		t.Fatal(code)
	}
	g, err = b.Grant("SYNTHETIC-B")
	if err != nil {
		t.Fatal(err)
	}
	b.mu.Lock()
	for i := range b.grants {
		b.grants[i].ExpiresAt = time.Now().Add(-time.Second).UnixMilli()
	}
	b.mu.Unlock()
	if code, _ := brokerCall(t, addr, g.Token, 7, 1, false); code != 1 {
		t.Fatal("expired", code)
	}
}
func TestBrokerRejectsWithoutEOF(t *testing.T) {
	s := fixtureStore(t, "Synthetic incomplete player")
	if err := s.InstallCatalog(fixtureCatalog("SYNTHETIC")); err != nil {
		t.Fatal(err)
	}
	b, addr := startBroker(t, s)
	g, err := b.Grant("SYNTHETIC")
	if err != nil {
		t.Fatal(err)
	}
	c, err := net.Dial("tcp4", addr)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	var req [48]byte
	copy(req[:4], "RPT1")
	req[4] = 1
	binary.BigEndian.PutUint32(req[8:], 36)
	copy(req[12:44], g.Token[:])
	binary.BigEndian.PutUint32(req[44:], 7)
	c.Write(req[:])
	time.Sleep(30 * time.Millisecond)
	if u, _ := s.Status("SYNTHETIC", 7); u.Unlocked {
		t.Fatal("mutation before EOF")
	}
	b.Revoke(g.Token)
	c.(*net.TCPConn).CloseWrite()
	var resp [12]byte
	c.SetReadDeadline(time.Now().Add(time.Second))
	if _, err := io.ReadFull(c, resp[:]); err != nil {
		t.Fatal(err)
	}
	if resp[4] != 1 {
		t.Fatal("queued request bypassed revoke", resp)
	}
}

type blockedCloseConn struct {
	net.Conn
	entered, release chan struct{}
}

func (c *blockedCloseConn) Close() error { close(c.entered); <-c.release; return c.Conn.Close() }
func TestBrokerCloseDoesNotHoldGrantLock(t *testing.T) {
	s := fixtureStore(t, "Synthetic close player")
	b := NewBroker(s)
	left, right := net.Pipe()
	defer right.Close()
	c := &blockedCloseConn{Conn: left, entered: make(chan struct{}), release: make(chan struct{})}
	b.conns[c] = true
	done1, done2 := make(chan struct{}), make(chan struct{})
	go func() { b.Close(); close(done1) }()
	<-c.entered
	go func() { b.Close(); close(done2) }()
	revoked := make(chan struct{})
	go func() { b.Revoke([32]byte{}); close(revoked) }()
	select {
	case <-revoked:
	case <-time.After(time.Second):
		close(c.release)
		t.Fatal("connection Close held grant mutex")
	}
	select {
	case <-done2:
		close(c.release)
		t.Fatal("concurrent Close returned before completion")
	default:
	}
	close(c.release)
	<-done1
	<-done2
}

func TestBrokerRenewNeverRevivesExpiredOrRevoked(t *testing.T) {
	s := fixtureStore(t, "Synthetic renewal player")
	if err := s.InstallCatalog(fixtureCatalog("SYNTHETIC")); err != nil {
		t.Fatal(err)
	}
	b := NewBroker(s)
	defer b.Close()
	g, err := b.Grant("SYNTHETIC")
	if err != nil {
		t.Fatal(err)
	}
	b.mu.Lock()
	b.grants[0].ExpiresAt = time.Now().Add(time.Minute).UnixMilli()
	before := b.grants[0].ExpiresAt
	b.grants[0].calls = 9
	b.mu.Unlock()
	if err := b.Renew(g.Token); err != nil {
		t.Fatal(err)
	}
	b.mu.Lock()
	after, calls := b.grants[0].ExpiresAt, b.grants[0].calls
	b.mu.Unlock()
	if after <= before || calls != 9 {
		t.Fatal("renew failed or reset quota", after, before, calls)
	}
	b.Revoke(g.Token)
	if err := b.Renew(g.Token); err == nil {
		t.Fatal("renew revived revoked grant")
	}
	g, err = b.Grant("SYNTHETIC")
	if err != nil {
		t.Fatal(err)
	}
	b.mu.Lock()
	b.grants[0].ExpiresAt = time.Now().Add(-time.Second).UnixMilli()
	b.mu.Unlock()
	if err := b.Renew(g.Token); err == nil {
		t.Fatal("renew revived expired grant")
	}
}
