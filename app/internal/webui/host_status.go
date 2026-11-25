package webui

import (
	"context"
	"time"

	"github.com/undecuple/re-psn/app/internal/tui"
)

// statsReader reads the owned host's status page. It is a field on Server so tests can inject an answer; the real one
// is the reader the Status screen uses, and it is given the status origin and token file, never a token.
type statsReader func(ctx context.Context, origin, tokenFile string) (*tui.HTTPStats, error)

// HostLimits are the limits in force on the host. A reported 0 means no limit; a nil field was not reported.
type HostLimits struct {
	MaxRooms        *uint64 `json:"max_rooms"`
	MaxPlayerRooms  *uint64 `json:"max_player_rooms"`
	MaxConnsPerAddr *uint64 `json:"max_conns_per_addr"`
	MaxReqRate      *uint64 `json:"max_req_rate"`
	MaxUDPRate      *uint64 `json:"max_udp_rate"`
}

// HostRefusals counts what the host turned away since it started. A nil field was not reported.
type HostRefusals struct {
	TCPConnsPerAddr *uint64 `json:"tcp_conns_per_addr"`
	TCPReqRate      *uint64 `json:"tcp_req_rate"`
	// UDPDropped is the sum of the UDP drop counters the host reported; nil when it reported none.
	UDPDropped *uint64 `json:"udp_dropped"`
}

// HostStatus is what the Host page shows after Refresh status. Pointer fields that are nil were not reported, and the
// page says "not reported" for them. When Error is set the read failed and only ReadAt (and the uptime) are meaningful.
type HostStatus struct {
	Clients       *int          `json:"clients"`
	Rooms         *int          `json:"rooms"`
	Unlisted      *int          `json:"unlisted"`
	Limits        *HostLimits   `json:"limits"`
	Refusals      *HostRefusals `json:"refusals"`
	ReadAt        int64         `json:"read_at"`
	UptimeSeconds *int64        `json:"uptime_seconds,omitempty"`
	Error         string        `json:"error,omitempty"`
}

// hostStatusError is the only text a failed read produces. It carries nothing from the failure itself, so a path or a
// token cannot appear in it.
const hostStatusError = "The host did not answer its status request. It may still be starting or may have stopped. Press Refresh status to try again."

// hostUptime is the whole seconds since startedMS (unix milliseconds), or nil when the start time is unknown or in the future.
func hostUptime(startedMS int64, now time.Time) *int64 {
	if startedMS <= 0 {
		return nil
	}
	d := now.UnixMilli() - startedMS
	if d < 0 {
		return nil
	}
	secs := d / 1000
	return &secs
}

// buildHostStatus turns one read into the page's answer. A failed read gives a plain message; the uptime stays in the answer, but the page shows only the message.
func buildHostStatus(stats *tui.HTTPStats, err error, startedMS int64, now time.Time) HostStatus {
	out := HostStatus{ReadAt: now.UnixMilli(), UptimeSeconds: hostUptime(startedMS, now)}
	if err != nil || stats == nil {
		out.Error = hostStatusError
		return out
	}
	clients, rooms, unlisted := stats.Clients, len(stats.Rooms), stats.Unlisted
	out.Clients, out.Rooms, out.Unlisted = &clients, &rooms, &unlisted
	l := stats.Server.Limits
	if limits := (HostLimits{l.MaxRooms, l.MaxPlayerRooms, l.MaxConnsPerAddr, l.MaxReqRate, l.MaxUDPRate}); limits != (HostLimits{}) {
		out.Limits = &limits
	}
	t, u := stats.Server.TCP, stats.Server.UDP
	r := HostRefusals{TCPConnsPerAddr: t.ConnsPerAddr, TCPReqRate: t.ReqRate}
	var sum uint64
	reported := false
	for _, p := range []*uint64{u.RateLimit, u.SourceCapacity, u.Malformed, u.Unauthorized, u.SourceMismatch, u.RelayDisabled, u.WriteError, u.DiscoverRefused} {
		if p != nil {
			sum += *p
			reported = true
		}
	}
	if reported {
		r.UDPDropped = &sum
	}
	if r != (HostRefusals{}) {
		out.Refusals = &r
	}
	return out
}
