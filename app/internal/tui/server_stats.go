package tui

import (
	"encoding/json"
	"time"
)

// ServerStats holds the aggregate server fields of /api/stats. Every field is a pointer: nil means the server did
// not report it (an older server), which is different from a reported zero.
type ServerStats struct {
	Version    *string
	Relay      *bool
	RelayBytes *uint64 // payload the relay carried since start, not interface traffic
	Limits     ServerLimits
	TCP        TCPRefusals
	UDP        UDPDrops
}

// ServerLimits are the limits in force; a reported 0 means no limit.
type ServerLimits struct {
	MaxRooms        *uint64 `json:"max_rooms"`
	MaxPlayerRooms  *uint64 `json:"max_player_rooms"`
	MaxConnsPerAddr *uint64 `json:"max_conns_per_addr"`
	MaxReqRate      *uint64 `json:"max_req_rate"`
	MaxUDPRate      *uint64 `json:"max_udp_rate"`
}

// TCPRefusals counts TCP connections turned away since start.
type TCPRefusals struct {
	ConnsPerAddr *uint64 `json:"conns_per_addr"`
	ReqRate      *uint64 `json:"req_rate"`
}

// UDPDrops counts dropped UDP datagrams by reason since start.
type UDPDrops struct {
	RateLimit       *uint64 `json:"rate_limit"`
	SourceCapacity  *uint64 `json:"source_capacity"`
	Malformed       *uint64 `json:"malformed"`
	Unauthorized    *uint64 `json:"unauthorized"`
	SourceMismatch  *uint64 `json:"source_mismatch"`
	RelayDisabled   *uint64 `json:"relay_disabled"`
	WriteError      *uint64 `json:"write_error"`
	DiscoverRefused *uint64 `json:"discover_refused"`
}

// opt decodes one JSON value into a new T, or returns nil when it is absent, null or of the wrong type.
func opt[T any](raw json.RawMessage) *T {
	if len(raw) == 0 || string(raw) == "null" {
		return nil
	}
	var v T
	if json.Unmarshal(raw, &v) != nil {
		return nil
	}
	return &v
}

// group splits a JSON object into its members; anything else gives none.
func group(raw json.RawMessage) map[string]json.RawMessage {
	var m map[string]json.RawMessage
	if json.Unmarshal(raw, &m) != nil {
		return nil
	}
	return m
}

// decodeServerStats reads the optional fields. b is already known to be valid JSON. A field that is absent or of
// the wrong type is left nil, so one odd value never hides the rest.
func decodeServerStats(b []byte) ServerStats {
	top := group(b)
	s := ServerStats{Version: opt[string](top["version"]), Relay: opt[bool](top["relay"]), RelayBytes: opt[uint64](top["relay_bytes"])}
	if s.Version != nil && len(*s.Version) > 64 {
		s.Version = nil
	}
	l := group(top["limits"])
	s.Limits = ServerLimits{opt[uint64](l["max_rooms"]), opt[uint64](l["max_player_rooms"]), opt[uint64](l["max_conns_per_addr"]),
		opt[uint64](l["max_req_rate"]), opt[uint64](l["max_udp_rate"])}
	t := group(top["tcp_refusals"])
	s.TCP = TCPRefusals{opt[uint64](t["conns_per_addr"]), opt[uint64](t["req_rate"])}
	u := group(top["udp_drops"])
	s.UDP = UDPDrops{opt[uint64](u["rate_limit"]), opt[uint64](u["source_capacity"]), opt[uint64](u["malformed"]),
		opt[uint64](u["unauthorized"]), opt[uint64](u["source_mismatch"]), opt[uint64](u["relay_disabled"]),
		opt[uint64](u["write_error"]), opt[uint64](u["discover_refused"])}
	return s
}

// Reported says whether the server sent any of the aggregate fields.
func (s ServerStats) Reported() bool {
	return s.Version != nil || s.Relay != nil || s.RelayBytes != nil || len(s.counters()) > 0 || s.Limits != (ServerLimits{})
}

// counters lists every since-start counter the server reported, by name.
func (s ServerStats) counters() map[string]uint64 {
	m := map[string]uint64{}
	for k, p := range map[string]*uint64{
		"relay_bytes": s.RelayBytes, "tcp.conns_per_addr": s.TCP.ConnsPerAddr, "tcp.req_rate": s.TCP.ReqRate,
		"udp.rate_limit": s.UDP.RateLimit, "udp.source_capacity": s.UDP.SourceCapacity, "udp.malformed": s.UDP.Malformed,
		"udp.unauthorized": s.UDP.Unauthorized, "udp.source_mismatch": s.UDP.SourceMismatch,
		"udp.relay_disabled": s.UDP.RelayDisabled, "udp.write_error": s.UDP.WriteError,
		"udp.discover_refused": s.UDP.DiscoverRefused,
	} {
		if p != nil {
			m[k] = *p
		}
	}
	return m
}

// serverTrack remembers the previous refresh so the app can show a rate and notice a restart.
type serverTrack struct {
	have      bool
	at        time.Time
	counters  map[string]uint64
	relay     *uint64
	rate      float64 // relay bytes per second between the last two refreshes
	hasRate   bool
	restarted bool // some counter went down at the last refresh
}

// next returns the state after one more refresh at time at. A counter that went down means the server restarted:
// the rate is dropped and the baseline starts again from this sample.
func (t serverTrack) next(s ServerStats, at time.Time) serverTrack {
	n := serverTrack{have: true, at: at, counters: s.counters()}
	if s.RelayBytes != nil {
		v := *s.RelayBytes
		n.relay = &v
	}
	if !t.have {
		return n
	}
	for k, v := range n.counters {
		if old, ok := t.counters[k]; ok && v < old {
			n.restarted = true
		}
	}
	if n.restarted {
		return n
	}
	if n.relay != nil && t.relay != nil {
		if dt := at.Sub(t.at).Seconds(); dt > 0 {
			n.rate, n.hasRate = float64(*n.relay-*t.relay)/dt, true
		}
	}
	return n
}
