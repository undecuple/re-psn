package main

// Read-only HTTP view of the server: JSON for scripts, one HTML page for
// people. It is opt-in, supports a status token, and lists public-room
// members. Invite-only rooms are counted without exposing their members.

import (
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"sort"
	"strings"
	"sync/atomic"
	"time"
)

type roomJSON struct {
	ID      uint64   `json:"id"`
	Title   string   `json:"title"`
	Label   string   `json:"label"`
	Game    string   `json:"game,omitempty"`
	Serials []string `json:"serials,omitempty"`
	Members []string `json:"members"`
	// RTT is parallel to Members: the round trip in milliseconds the player reported, or null (none, unknown or stale).
	RTT []*int `json:"rtt_ms"`
	// Verified counts members whose UDP probe the server has accepted on this connection (see docs/api.md).
	Verified int    `json:"verified"`
	Max      uint8  `json:"max"`
	Age      string `json:"age"`
}

type boardJSON struct {
	Title   string   `json:"title"`
	Label   string   `json:"label"`
	Game    string   `json:"game,omitempty"`
	Serials []string `json:"serials,omitempty"`
	Board   uint32   `json:"board"`
	Entries int      `json:"entries"`
	Leader  string   `json:"leader"`
	Best    int64    `json:"best"`
}

type statsJSON struct {
	Uptime      string     `json:"uptime"`
	Clients     int        `json:"clients"`
	Connections uint64     `json:"connections_total"`
	Rooms       []roomJSON `json:"rooms"`
	Unlisted    int        `json:"unlisted_rooms"`
	// VerifiedPlayers counts every connected player with an accepted UDP probe, in a room or not.
	VerifiedPlayers int          `json:"verified_players"`
	Boards          []boardJSON  `json:"boards"`
	UDPDrops        udpDropStats `json:"udp_drops"`
	// The fields below are aggregate and carry no address or player. A client that does not know them ignores them.
	Version     string       `json:"version"`
	Relay       bool         `json:"relay"`
	RelayBytes  uint64       `json:"relay_bytes"` // payload carried by the relay since start, not interface traffic
	Limits      limitsJSON   `json:"limits"`
	TCPRefusals refusalsJSON `json:"tcp_refusals"`
}

// limitsJSON is what is in force; zero means no limit.
type limitsJSON struct {
	MaxRooms        int `json:"max_rooms"`
	MaxPlayerRooms  int `json:"max_player_rooms"`
	MaxConns        int `json:"max_conns"`
	MaxConnsPerAddr int `json:"max_conns_per_addr"`
	MaxReqRate      int `json:"max_req_rate"`
	MaxUDPRate      int `json:"max_udp_rate"`
}

// refusalsJSON counts TCP connections turned away since start.
type refusalsJSON struct {
	Conns        uint64 `json:"conns"`
	ConnsPerAddr uint64 `json:"conns_per_addr"`
	ReqRate      uint64 `json:"req_rate"`
}

// boardStats lists the leaderboards, sorted by title and board. A store
// error is logged and gives an empty list, so the page still shows rooms.
func (s *Server) boardStats() []boardJSON {
	ctx, cancel := storeCtx()
	defer cancel()
	out := []boardJSON{}
	boards, err := s.scores.Boards(ctx)
	if err != nil {
		s.log.Warn("status page", "reason", "score store", "err", err)
		return out
	}
	for _, b := range boards {
		bj := boardJSON{Title: b.Key.Title, Board: b.Key.Board, Entries: b.Entries}
		bj.Label, bj.Game, bj.Serials = titleLabel(b.Key.Title)
		if b.Entries > 0 {
			bj.Leader, bj.Best = b.Leader.OnlineID, b.Leader.Score
		}
		out = append(out, bj)
	}
	sort.Slice(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if a.Label != b.Label {
			return gameBefore(a.Game, a.Label, b.Game, b.Label)
		}
		return a.Title < b.Title || a.Title == b.Title && a.Board < b.Board
	})
	return out
}

func (s *Server) snapshot() statsJSON {
	boards := s.boardStats() // outside s.mu: the store has its own lock
	s.mu.Lock()
	defer s.mu.Unlock()
	st := statsJSON{
		Uptime:      time.Since(s.started).Round(time.Second).String(),
		Clients:     len(s.clients),
		Connections: s.conns,
		Rooms:       []roomJSON{},
		Boards:      boards,
		UDPDrops:    s.udpDrops.snapshot(),
		Version:     version,
		Relay:       s.relay,
		RelayBytes:  atomic.LoadUint64(&s.relayed),
		Limits: limitsJSON{MaxRooms: s.maxRooms, MaxPlayerRooms: s.maxPlayerRooms, MaxConns: s.maxConns, MaxConnsPerAddr: s.maxPerAddr,
			MaxReqRate: s.maxReqRate, MaxUDPRate: s.maxUDPRate},
		TCPRefusals: refusalsJSON{Conns: atomic.LoadUint64(&s.refusedTotal), ConnsPerAddr: atomic.LoadUint64(&s.refusedConns), ReqRate: atomic.LoadUint64(&s.refusedReqRate)},
	}
	for _, c := range s.clients {
		if c.commID != "" && c.udpPort != 0 {
			st.VerifiedPlayers++
		}
	}
	for _, r := range s.rooms {
		if r.code != "" { // invite-only rooms are counted, not listed
			st.Unlisted++
			continue
		}
		rj := roomJSON{ID: r.id, Title: r.commID, Max: r.max,
			Age: time.Since(r.created).Round(time.Second).String()}
		rj.Label, rj.Game, rj.Serials = titleLabel(r.commID)
		for _, m := range r.members {
			rj.Members = append(rj.Members, m.c.onlineID)
			rj.RTT = append(rj.RTT, reportedRTT(m.c))
			if m.c.udpPort != 0 {
				rj.Verified++
			}
		}
		st.Rooms = append(st.Rooms, rj)
	}
	sort.Slice(st.Rooms, func(i, j int) bool {
		a, b := st.Rooms[i], st.Rooms[j]
		if a.Label != b.Label {
			return gameBefore(a.Game, a.Label, b.Game, b.Label)
		}
		return a.ID < b.ID
	})
	return st
}

// reportedRTT is the player's reported round trip while it is fresh, otherwise nil. Callers hold s.mu.
func reportedRTT(c *client) *int {
	if c.rttAt.IsZero() || time.Since(c.rttAt) > rttFresh {
		return nil
	}
	ms := int(c.rttMs)
	return &ms
}

// MemberRTT is " (N ms)" for the i-th member when a fresh report exists, for the status page template.
func (r roomJSON) MemberRTT(i int) string {
	if i < len(r.RTT) && r.RTT[i] != nil {
		return fmt.Sprintf(" (%d ms)", *r.RTT[i])
	}
	return ""
}

// gameBefore orders labels so that known games come first, by label, and bare communication IDs after them.
func gameBefore(nameA, labelA, nameB, labelB string) bool {
	if (nameA == "") != (nameB == "") {
		return nameA != ""
	}
	return labelA < labelB
}

// reachable reports whether an -http address listens beyond this machine.
func reachable(addr string) bool {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return true
	}
	if host == "localhost" {
		return false
	}
	ip := net.ParseIP(host)
	return ip == nil || !ip.IsLoopback() // "" and "0.0.0.0" listen everywhere
}

// authorized checks the -http-token: an "Authorization: Bearer" header or a
// ?token= query (which survives the page's meta refresh).
func (s *Server) authorized(r *http.Request) bool {
	if s.httpToken == "" {
		return true
	}
	got := r.URL.Query().Get("token")
	if h, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer "); ok {
		got = h
	}
	return subtle.ConstantTimeCompare([]byte(got), []byte(s.httpToken)) == 1
}

// The status page answers small, quick requests, so a connection that does not send its request, or does not read the answer, in
// these times is closed. Without them one idle or slow connection held a goroutine and a file descriptor for as long as it liked.
const (
	statusReadHeaderTimeout = 5 * time.Second
	statusReadTimeout       = 10 * time.Second
	statusWriteTimeout      = 10 * time.Second
	statusIdleTimeout       = 30 * time.Second
)

// newStatusServer is the -http server: the status handler with the timeouts above.
func newStatusServer(addr string, h http.Handler) *http.Server {
	return &http.Server{
		Addr:              addr,
		Handler:           h,
		ReadHeaderTimeout: statusReadHeaderTimeout,
		ReadTimeout:       statusReadTimeout,
		WriteTimeout:      statusWriteTimeout,
		IdleTimeout:       statusIdleTimeout,
	}
}

func (s *Server) HTTP() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			w.Header().Set("Allow", "GET, HEAD")
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		body := "ok\n"
		if !s.ready() {
			w.WriteHeader(http.StatusServiceUnavailable)
			body = "not ready\n"
		}
		if r.Method != http.MethodHead {
			io.WriteString(w, body)
		}
	})
	mux.HandleFunc("/api/stats", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(s.snapshot())
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Content-Security-Policy", statusPolicy)
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		if err := page.Execute(w, s.snapshot()); err != nil {
			fmt.Fprintln(w, err)
		}
	})
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		if !s.authorized(r) {
			// the path only: the token may be in the query string
			s.log.Warn("status page refused", "addr", s.clientAddr(r), "path", r.URL.Path, "reason", "token missing or wrong")
			http.Error(w, "token required", http.StatusUnauthorized)
			return
		}
		mux.ServeHTTP(w, r)
	})
}
