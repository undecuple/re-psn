package main

import (
	"net/netip"
	"sync/atomic"
	"time"
)

const (
	defaultUDPRate = 1000
	udpSourceLimit = 4096
	udpSourceTTL   = time.Minute
)

type udpBucket struct {
	tokens float64
	last   time.Time
}

// udpLimiter belongs to the single UDP serving goroutine. All datagrams cost
// one token, before parsing or taking the game-state lock. A full table rejects
// new sources instead of evicting buckets and giving a busy sender a new burst.
type udpLimiter struct {
	rate    int
	sources map[netip.Addr]udpBucket
	swept   time.Time
}

func (l *udpLimiter) allow(source netip.Addr, now time.Time) (rateLimited, capacity bool) {
	if l.rate == 0 {
		return false, false
	}
	if l.sources == nil {
		l.sources = make(map[netip.Addr]udpBucket)
		l.swept = now
	}
	if now.Sub(l.swept) >= time.Second {
		for ip, bucket := range l.sources {
			if now.Sub(bucket.last) >= udpSourceTTL {
				delete(l.sources, ip)
			}
		}
		l.swept = now
	}
	source = source.Unmap()
	if source.Is6() {
		// One IPv6 network (/64) is one source: a single customer holds billions of addresses, and counting each
		// would let one host fill the table or restart its allowance with every new address.
		source = netip.PrefixFrom(source, 64).Masked().Addr()
	}
	b, found := l.sources[source]
	if !found {
		if len(l.sources) >= udpSourceLimit {
			return false, true
		}
		b = udpBucket{tokens: float64(l.rate), last: now}
	}
	if elapsed := now.Sub(b.last).Seconds(); elapsed > 0 {
		b.tokens += elapsed * float64(l.rate)
		if b.tokens > float64(l.rate) {
			b.tokens = float64(l.rate)
		}
		b.last = now
	}
	limited := b.tokens < 1
	if !limited {
		b.tokens--
	}
	l.sources[source] = b
	return limited, false
}

type udpDropCounters struct {
	rateLimit      atomic.Uint64
	sourceCapacity atomic.Uint64
	malformed      atomic.Uint64
	unauthorized   atomic.Uint64
	sourceMismatch atomic.Uint64
	relayDisabled  atomic.Uint64
	writeError     atomic.Uint64
}

type udpDropStats struct {
	RateLimit      uint64 `json:"rate_limit"`
	SourceCapacity uint64 `json:"source_capacity"`
	Malformed      uint64 `json:"malformed"`
	Unauthorized   uint64 `json:"unauthorized"`
	SourceMismatch uint64 `json:"source_mismatch"`
	RelayDisabled  uint64 `json:"relay_disabled"`
	WriteError     uint64 `json:"write_error"`
}

func (c *udpDropCounters) snapshot() udpDropStats {
	return udpDropStats{c.rateLimit.Load(), c.sourceCapacity.Load(), c.malformed.Load(),
		c.unauthorized.Load(), c.sourceMismatch.Load(), c.relayDisabled.Load(), c.writeError.Load()}
}
