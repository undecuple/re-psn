package client

import (
	"sync"
	"time"
)

// The round trip a player reports to the server. The connection times every call, from the send to the reply (the
// server's own work is included, so it is an upper bound on the network round trip), and keeps the smallest of the
// last rttSamples. Heartbeat carries it when ReportRTT(true) has been called. The server only displays it, and
// nobody checks it.
const (
	rttSamples = 8
	maxRTT     = 60000 * time.Millisecond // the server ignores more; so does the client
)

type rttTracker struct {
	mu   sync.Mutex
	ms   [rttSamples]uint16
	n    int
	next int
}

func (r *rttTracker) note(d time.Duration) {
	if d < 0 || d > maxRTT {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.ms[r.next] = uint16(d / time.Millisecond)
	r.next = (r.next + 1) % rttSamples
	if r.n < rttSamples {
		r.n++
	}
}

func (r *rttTracker) min() (uint16, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.n == 0 {
		return 0, false
	}
	best := r.ms[0]
	for _, v := range r.ms[1:r.n] {
		if v < best {
			best = v
		}
	}
	return best, true
}

// RTT is the smallest round trip among the last eight calls, or false before the first reply. Dial only opens the
// connection; the first sample is the caller's Hello. Until then RTT reports false and Heartbeat sends no body.
func (c *Conn) RTT() (time.Duration, bool) {
	ms, ok := c.rtt.min()
	return time.Duration(ms) * time.Millisecond, ok
}

// ReportRTT chooses whether Heartbeat carries RTT, which the server shows next to the player's name as reported by
// the player. It is off until this is called with true, which is recommended.
func (c *Conn) ReportRTT(on bool) { c.reportRTT.Store(on) }
