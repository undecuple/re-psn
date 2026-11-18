package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// A fake clock the cache reads, so no test sleeps.
type fakeClock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *fakeClock) now() time.Time { c.mu.Lock(); defer c.mu.Unlock(); return c.t }
func (c *fakeClock) add(d time.Duration) {
	c.mu.Lock()
	c.t = c.t.Add(d)
	c.mu.Unlock()
}

// Within the ttl every caller gets the one snapshot; after it the next caller builds a new one.
func TestSnapshotCacheReusesWithinTTL(t *testing.T) {
	clk := &fakeClock{t: time.Unix(1000, 0)}
	c := &snapshotCache{ttl: time.Second, now: clk.now}
	var builds atomic.Int32
	build := func() statsJSON { return statsJSON{Clients: int(builds.Add(1))} }

	if got := c.get(build).Clients; got != 1 {
		t.Fatalf("first call built %d", got)
	}
	clk.add(999 * time.Millisecond)
	if got := c.get(build).Clients; got != 1 || builds.Load() != 1 {
		t.Fatalf("within the ttl: clients %d after %d builds, want the first snapshot", got, builds.Load())
	}
	clk.add(time.Millisecond) // exactly ttl old: stale
	if got := c.get(build).Clients; got != 2 || builds.Load() != 2 {
		t.Fatalf("at the ttl: clients %d after %d builds, want a new snapshot", got, builds.Load())
	}
}

// Many callers at once cost one build: the ones that arrive during it wait for it instead of starting their own.
func TestSnapshotCacheBuildsOnceForConcurrentCallers(t *testing.T) {
	c := &snapshotCache{ttl: time.Hour}
	var builds atomic.Int32
	release := make(chan struct{})
	build := func() statsJSON {
		builds.Add(1)
		<-release
		return statsJSON{Clients: 7}
	}
	var wg sync.WaitGroup
	got := make([]int, 50)
	for i := range got {
		wg.Add(1)
		go func(i int) { defer wg.Done(); got[i] = c.get(build).Clients }(i)
	}
	for builds.Load() == 0 {
		time.Sleep(time.Millisecond)
	}
	time.Sleep(20 * time.Millisecond) // let the others queue behind the build
	close(release)
	wg.Wait()
	if builds.Load() != 1 {
		t.Fatalf("%d builds for 50 concurrent callers, want 1", builds.Load())
	}
	for i, g := range got {
		if g != 7 {
			t.Fatalf("caller %d got %d", i, g)
		}
	}
}

// While a snapshot is being built (it holds s.mu), callers waiting for it hold nothing the game needs, and a build that has
// finished does not run again for them.
func TestSnapshotCacheWaitersDoNotTakeServerLock(t *testing.T) {
	s := NewServer(10, false)
	s.statsCache = &snapshotCache{ttl: time.Hour}
	s.statsView() // built once
	s.mu.Lock()   // the game path holds the server lock
	done := make(chan struct{})
	go func() { s.statsView(); close(done) }()
	select {
	case <-done: // served from the cache without s.mu
	case <-time.After(2 * time.Second):
		t.Fatal("a cached status request waited for s.mu")
	}
	s.mu.Unlock()
}

func statsClients(t *testing.T, h http.Handler) int {
	t.Helper()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/stats", nil))
	if rec.Code != 200 {
		t.Fatalf("/api/stats: %d", rec.Code)
	}
	var st statsJSON
	if err := json.Unmarshal(rec.Body.Bytes(), &st); err != nil {
		t.Fatal(err)
	}
	return st.Clients
}

// Without the cache (the default for tests and embedders) /api/stats is current; with it, up to the ttl old, then current.
func TestStatusUsesTheSnapshotCacheOnlyWhenOn(t *testing.T) {
	s, addr := startServer(t)
	h := s.HTTP()
	if n := statsClients(t, h); n != 0 {
		t.Fatalf("clients %d, want 0", n)
	}
	dial(t, addr, "NPWR00001_00", "alice", 3658)
	if n := statsClients(t, h); n != 1 {
		t.Fatalf("uncached: clients %d, want 1 at once", n)
	}

	clk := &fakeClock{t: time.Unix(2000, 0)}
	s.statsCache = &snapshotCache{ttl: statusSnapshotTTL, now: clk.now}
	if n := statsClients(t, h); n != 1 {
		t.Fatalf("cached, first: clients %d, want 1", n)
	}
	dial(t, addr, "NPWR00001_00", "bob", 3659)
	clk.add(statusSnapshotTTL / 2)
	if n := statsClients(t, h); n != 1 {
		t.Fatalf("cached, within the ttl: clients %d, want the earlier 1", n)
	}
	clk.add(statusSnapshotTTL)
	if n := statsClients(t, h); n != 2 {
		t.Fatalf("cached, after the ttl: clients %d, want 2", n)
	}
}

func TestStatusSnapshotTTLIsOneSecond(t *testing.T) {
	if statusSnapshotTTL != time.Second {
		t.Fatalf("statusSnapshotTTL %v: the docs say the status numbers are at most one second old", statusSnapshotTTL)
	}
}
