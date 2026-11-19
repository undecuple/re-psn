package main

import (
	"crypto/ed25519"
	crand "crypto/rand"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

// A relay stream that is already open ends with the pass of either player: when the account is revoked, when the key that
// signed the pass leaves the key file, and when the pass has run out plus the skew.

type cutEnv struct {
	t       *testing.T
	s       *Server
	addr    string
	k1, k2  *passTool
	keyFile string
	revFile string
	log     *syncBuf
}

func newCutEnv(t *testing.T) *cutEnv {
	t.Helper()
	p := newPassTool(t)
	pub2, priv2, _ := ed25519.GenerateKey(crand.Reader)
	e := &cutEnv{t: t, k1: p, k2: &passTool{t: t, priv: priv2, pub: pub2, cfg: p.cfg}, log: &syncBuf{}}
	dir := t.TempDir()
	e.keyFile, e.revFile = filepath.Join(dir, "keys"), filepath.Join(dir, "revoked")
	e.writeKeys(true, true)
	e.writeRevoked()
	e.s, e.addr = startPassServer(t, p, true, func(s *Server) { s.log = newLogger(e.log, levelFor(false), "text") })
	e.reload()
	return e
}

func (e *cutEnv) writeKeys(k1, k2 bool) {
	var b strings.Builder
	if k1 {
		b.WriteString("k1 " + b64.EncodeToString(e.k1.pub) + "\n")
	}
	if k2 {
		b.WriteString("k2 " + b64.EncodeToString(e.k2.pub) + "\n")
	}
	if err := os.WriteFile(e.keyFile, []byte(b.String()), 0o600); err != nil {
		e.t.Fatal(err)
	}
}

func (e *cutEnv) writeRevoked(subs ...string) {
	if err := os.WriteFile(e.revFile, []byte(strings.Join(subs, "\n")+"\n"), 0o600); err != nil {
		e.t.Fatal(err)
	}
}

func (e *cutEnv) reload() {
	e.t.Helper()
	if err := e.s.reloadPass(e.keyFile, e.revFile); err != nil {
		e.t.Fatal(err)
	}
}

// pass is a relay pass for sub signed with kid, good for life from now.
func (e *cutEnv) pass(kid, sub string, life time.Duration) string {
	tool := e.k1
	if kid == "k2" {
		tool = e.k2
	}
	now := time.Now()
	c := claims(now)
	c["kid"], c["sub"] = kid, sub
	c["iat"], c["exp"] = now.Unix(), now.Add(life).Unix()
	return tool.pass(c)
}

// player joins and signs in.
func (e *cutEnv) player(name, kid, sub string) player {
	e.t.Helper()
	pl := join(e.t, e.addr, name)
	if rt, _ := pl.c.auth(e.pass(kid, sub, time.Hour)); rt != msgOK {
		e.t.Fatalf("AUTH for %s refused", name)
	}
	return pl
}

// stream opens a relay stream between two new players, one signed by akid for asub, one by bkid for bsub.
func (e *cutEnv) stream(name, akid, asub, bkid, bsub string) (a, b player, legA, legB *tc) {
	e.t.Helper()
	a, b = e.player(name+"a", akid, asub), e.player(name+"b", bkid, bsub)
	meet(e.t, a, b)
	legA, legB = openStream(e.t, e.addr, a, b)
	return
}

// carries reports whether a byte sent on from arrives on to.
func carries(from, to *tc) bool {
	from.conn.SetWriteDeadline(time.Now().Add(time.Second))
	if _, err := from.conn.Write([]byte{1}); err != nil {
		return false
	}
	to.conn.SetReadDeadline(time.Now().Add(time.Second))
	_, err := to.conn.Read(make([]byte, 1))
	return err == nil
}

// ended reports whether both legs are closed within d.
func ended(d time.Duration, legs ...*tc) bool {
	for _, l := range legs {
		if !closedWithin(l.conn, d) {
			return false
		}
	}
	return true
}

func (e *cutEnv) live() int {
	e.s.mu.Lock()
	defer e.s.mu.Unlock()
	return len(e.s.live)
}

func TestRelayStreamEndsWhenAccountRevoked(t *testing.T) {
	e := newCutEnv(t)
	_, _, ra, rb := e.stream("r", "k1", "github:1", "k1", "github:2")
	_, _, oa, ob := e.stream("o", "k1", "github:3", "k1", "github:4")
	if !carries(ra, rb) || !carries(ob, oa) {
		t.Fatal("control: the streams carry nothing")
	}
	e.writeRevoked("github:2") // the accepting end
	e.reload()
	if !ended(3*time.Second, ra, rb) {
		t.Fatal("a stream outlived the revocation of one of its accounts")
	}
	if !carries(oa, ob) {
		t.Fatal("a stream of other accounts was cut")
	}
	if !strings.Contains(e.log.String(), "relay stream closed") || !strings.Contains(e.log.String(), "pass revoked") {
		t.Fatalf("no reason logged:\n%s", e.log.String())
	}
	// the connecting end too
	_, _, ca, cb := e.stream("c", "k1", "github:5", "k1", "github:6")
	e.writeRevoked("github:2", "github:5")
	e.reload()
	if !ended(3*time.Second, ca, cb) {
		t.Fatal("a stream outlived the revocation of its connecting account")
	}
}

func TestRelayStreamEndsWhenKeyRemoved(t *testing.T) {
	e := newCutEnv(t)
	_, _, xa, xb := e.stream("x", "k2", "github:1", "k1", "github:2") // one end signed by k1
	_, _, ya, yb := e.stream("y", "k2", "github:3", "k2", "github:4")
	e.writeKeys(false, true)
	e.reload()
	if !ended(3*time.Second, xa, xb) {
		t.Fatal("a stream outlived the removal of the key that signed one end's pass")
	}
	if !carries(ya, yb) {
		t.Fatal("a stream signed by a key still listed was cut")
	}
}

func TestRelayStreamEndsWhenPassExpires(t *testing.T) {
	e := newCutEnv(t)
	a, b := e.player("ea", "k1", "github:1"), e.player("eb", "k1", "github:2")
	c, d := e.player("ec", "k1", "github:3"), e.player("ed", "k1", "github:4")
	meet(t, a, b)
	meet(t, c, d)
	la, lb := openStream(t, e.addr, a, b)
	lc, ld := openStream(t, e.addr, c, d)
	// 58 s past exp is inside the skew, so b's AUTH is accepted, and the pass has 2 s left
	now := time.Now()
	short := claims(now)
	short["sub"], short["iat"], short["exp"] = "github:2", now.Unix()-100, now.Unix()-58
	if rt, _ := b.c.auth(e.k1.pass(short)); rt != msgOK {
		t.Fatal("a pass inside the skew was refused")
	}
	// d does the same, then refreshes with a good pass before the end
	short["sub"] = "github:4"
	if rt, _ := d.c.auth(e.k1.pass(short)); rt != msgOK {
		t.Fatal("a pass inside the skew was refused")
	}
	if rt, _ := d.c.auth(e.pass("k1", "github:4", time.Hour)); rt != msgOK {
		t.Fatal("refresh refused")
	}
	start := time.Now()
	if !ended(6*time.Second, la, lb) {
		t.Fatal("a stream outlived its pass")
	}
	if took := time.Since(start); took > 4*time.Second {
		t.Fatalf("closed after %v", took)
	}
	if !strings.Contains(e.log.String(), "pass expired") {
		t.Fatalf("no reason logged:\n%s", e.log.String())
	}
	time.Sleep(500 * time.Millisecond)
	if !carries(lc, ld) {
		t.Fatal("a refreshed pass did not keep its stream")
	}
}

// Without a pass requirement a pass is optional, and an end of a stream expiring cuts nothing.
func TestRelayStreamKeepsRunningPastExpiryWithoutRequirement(t *testing.T) {
	p := newPassTool(t)
	s, addr := startPassServer(t, p, false)
	a, b := join(t, addr, "a"), join(t, addr, "b")
	now := time.Now()
	short := claims(now)
	short["iat"], short["exp"] = now.Unix()-100, now.Unix()-58
	a.c.auth(p.pass(short))
	meet(t, a, b)
	la, lb := openStream(t, addr, a, b)
	s.mu.Lock()
	armed := false
	for ls := range s.live {
		armed = ls.timer != nil
	}
	s.mu.Unlock()
	if armed {
		t.Fatal("a timer for a pass nobody requires")
	}
	time.Sleep(2500 * time.Millisecond)
	if !carries(la, lb) {
		t.Fatal("the stream was cut with no pass required")
	}
}

// A reload that lands while a stream is being set up either finds the stream and closes it, or the stream is refused: the
// connecting leg never sits waiting for an accept that an account without a key could still give.
func TestRelayReloadRacingStreamSetup(t *testing.T) {
	e := newCutEnv(t)
	a, b := e.player("ra", "k1", "github:1"), e.player("rb", "k2", "github:2")
	meet(t, a, b)
	const rounds = 150
	var sawCut, sawRefused int
	for i := 0; i < rounds; i++ {
		e.writeKeys(true, true)
		e.reload()
		if rt, _ := a.c.auth(e.pass("k1", "github:1", time.Hour)); rt != msgOK { // the removed key took a's relay: sign in again
			t.Fatalf("round %d: AUTH refused", i)
		}
		w := &writer{}
		w.u32(a.id)
		w.u32(a.token)
		w.u32(b.id)
		w.u16(3658)
		var leg *tc
		var wg, start sync.WaitGroup
		start.Add(1)
		wg.Add(2)
		go func() {
			defer wg.Done()
			start.Wait()
			time.Sleep(time.Duration(i%7) * 40 * time.Microsecond)
			leg = streamFirst(t, e.addr, msgStreamConnect, w.b)
		}()
		go func() {
			defer wg.Done()
			start.Wait()
			e.writeKeys(false, true)
			e.reload()
		}()
		start.Done()
		wg.Wait()
		leg.conn.SetReadDeadline(time.Now().Add(2 * time.Second))
		rt, _, err := readPacket(leg.conn)
		if ne, ok := err.(net.Error); ok && ne.Timeout() {
			t.Fatalf("round %d: the stream was left waiting after its key was removed", i)
		}
		if err == nil && rt == msgError {
			sawRefused++
		} else {
			sawCut++
		}
		leg.conn.Close()
		drain(b.c)
	}
	t.Logf("refused at setup %d, cut after setup %d", sawRefused, sawCut)
}

// drain throws away the pushes waiting on c, such as a stream offer.
func drain(c *tc) {
	for {
		c.conn.SetReadDeadline(time.Now().Add(5 * time.Millisecond))
		if _, _, err := readPacket(c.conn); err != nil {
			return
		}
	}
}

// A stream that ends normally leaves nothing behind: no entry in the table, no timer, no goroutine.
func TestRelayStreamLeavesNothingBehind(t *testing.T) {
	e := newCutEnv(t)
	a, b := e.player("na", "k1", "github:1"), e.player("nb", "k2", "github:2")
	meet(t, a, b)
	settle := func() int {
		var n int
		for i := 0; i < 50; i++ {
			time.Sleep(50 * time.Millisecond)
			if n2 := runtime.NumGoroutine(); n2 == n && i > 2 {
				break
			} else {
				n = n2
			}
		}
		return n
	}
	before := settle()
	for i := 0; i < 20; i++ {
		la, lb := openStream(t, e.addr, a, b)
		if !carries(la, lb) {
			t.Fatal("control: the stream carries nothing")
		}
		la.conn.Close()
		lb.conn.Close()
	}
	for i := 0; i < 40 && e.live() != 0; i++ {
		time.Sleep(50 * time.Millisecond)
	}
	if n := e.live(); n != 0 {
		t.Fatalf("%d streams still tracked", n)
	}
	e.s.mu.Lock()
	n := len(e.s.streams)
	e.s.mu.Unlock()
	if n != 0 {
		t.Fatalf("%d streams still waiting for an accept", n)
	}
	if after := settle(); after > before+2 {
		t.Fatalf("goroutines: %d before, %d after", before, after)
	}
}

// A stream cut while it waits for its accept does not hold its goroutine for the rest of the wait.
func TestRelayCutWhileWaitingReleasesAtOnce(t *testing.T) {
	e := newCutEnv(t)
	a, b := e.player("wa", "k1", "github:1"), e.player("wb", "k2", "github:2")
	meet(t, a, b)
	w := &writer{}
	w.u32(a.id)
	w.u32(a.token)
	w.u32(b.id)
	w.u16(3658)
	leg := streamFirst(t, e.addr, msgStreamConnect, w.b)
	for i := 0; i < 100 && e.live() == 0; i++ {
		time.Sleep(10 * time.Millisecond)
	}
	e.writeRevoked("github:2")
	e.reload()
	if !closedWithin(leg.conn, 2*time.Second) {
		t.Fatal("the connecting leg stayed open")
	}
	for i := 0; i < 40 && e.live() != 0; i++ {
		time.Sleep(50 * time.Millisecond)
	}
	if n := e.live(); n != 0 {
		t.Fatalf("%d streams still tracked well before the accept wait ends", n)
	}
}

// A stream that ends normally stops its timer: it would otherwise keep the stream alive until its pass lapses.
func TestRelayEndedStreamStopsItsTimer(t *testing.T) {
	e := newCutEnv(t) // passes last an hour, so every stream has a timer armed
	a, b := e.player("ta", "k1", "github:1"), e.player("tb", "k2", "github:2")
	meet(t, a, b)
	la, lb := openStream(t, e.addr, a, b)
	e.s.mu.Lock()
	var timers []*time.Timer
	for ls := range e.s.live {
		if ls.timer == nil {
			e.s.mu.Unlock()
			t.Fatal("control: a stream under a pass has no timer")
		}
		timers = append(timers, ls.timer)
	}
	e.s.mu.Unlock()
	if len(timers) != 1 {
		t.Fatalf("control: %d tracked streams, want 1", len(timers))
	}
	la.conn.Close()
	lb.conn.Close()
	for i := 0; i < 100 && e.live() != 0; i++ {
		time.Sleep(50 * time.Millisecond)
	}
	if e.live() != 0 {
		t.Fatal("the stream is still tracked")
	}
	// Stop reports whether it stopped a pending timer: false means the end of the stream had already stopped it.
	if timers[0].Stop() {
		t.Fatal("the timer of an ended stream was still pending")
	}
}

// A leg that arrives after the stream was cut is closed too: the accepting leg has taken the stream from the table, the
// stream is cut, and only then does the leg send itself on. The check in relayStream (cut) is belt and braces: the cut has
// closed the connecting leg, so without it the splice would end at once and close this leg as well; the test pins that outcome.
func TestRelayLegArrivingAfterCutIsClosed(t *testing.T) {
	e := newCutEnv(t)
	reached, release := make(chan struct{}), make(chan struct{})
	e.s.acceptHook = func() {
		close(reached)
		<-release
	}
	a, b := e.player("la", "k1", "github:1"), e.player("lb", "k2", "github:2")
	meet(t, a, b)
	w := &writer{}
	w.u32(a.id)
	w.u32(a.token)
	w.u32(b.id)
	w.u16(3658)
	legA := streamFirst(t, e.addr, msgStreamConnect, w.b)
	var offer []byte
	for offer == nil {
		if rt, body := b.c.read(); rt == msgStreamOffer {
			offer = body
		}
	}
	aw := &writer{}
	aw.u32(b.id)
	aw.u32(b.token)
	aw.u32((&reader{b: offer}).u32())
	legB := streamFirst(t, e.addr, msgStreamAccept, aw.b)
	select {
	case <-reached:
	case <-time.After(3 * time.Second):
		close(release)
		t.Fatal("the accepting leg never reached the hook")
	}
	e.writeRevoked("github:1")
	e.reload() // cuts the stream while the accepting leg holds it outside the table
	if !closedWithin(legA.conn, 2*time.Second) {
		close(release)
		t.Fatal("the connecting leg stayed open")
	}
	close(release)
	// the accepting leg has been answered READY already; it must then be closed, not spliced
	legB.conn.SetReadDeadline(time.Now().Add(3 * time.Second))
	for {
		if _, err := legB.conn.Read(make([]byte, 64)); err != nil {
			if ne, ok := err.(net.Error); ok && ne.Timeout() {
				t.Fatal("the leg that arrived after the cut stayed open")
			}
			break
		}
	}
	for i := 0; i < 40 && e.live() != 0; i++ {
		time.Sleep(50 * time.Millisecond)
	}
	if n := e.live(); n != 0 {
		t.Fatalf("%d streams still tracked", n)
	}
}
