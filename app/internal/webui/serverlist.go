package webui

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/undecuple/re-psn/app/internal/config"
	"github.com/undecuple/re-psn/app/internal/servers"
	"github.com/undecuple/re-psn/app/internal/tui"
)

const (
	checkTimeout  = 5 * time.Second
	checkParallel = 4
)

// Check is the last result of asking one saved server. It lives in memory only and is never written to disk.
type Check struct {
	Status      string  `json:"status"` // unchecked, reachable or unavailable
	CheckedAt   int64   `json:"checked_at"`
	RoundTripMS float64 `json:"round_trip_ms"`
	Rooms       int     `json:"rooms"`
}

// ServerRow is one saved server as the page sees it.
type ServerRow struct {
	servers.Entry
	Local bool  `json:"local"` // a loopback, private or link-local address, named literally
	Check Check `json:"check"`
}

// ServerList is the saved-servers part of the app state. Error is set when servers.json is unreadable: the list is then
// empty and read-only, and the file is left as it is.
type ServerList struct {
	Entries []ServerRow `json:"entries"`
	Error   string      `json:"error"`
	Max     int         `json:"max"`
}

// serverStore holds the list. Its mutex is separate from Server.mu so a slow save never blocks the state snapshot.
type serverStore struct {
	mu     sync.Mutex
	path   string
	list   *servers.List
	err    error
	checks map[string]Check
}

func newServerStore(configPath string) *serverStore {
	st := &serverStore{path: servers.PathFor(configPath), checks: map[string]Check{}}
	st.list, st.err = servers.Load(st.path)
	if st.list == nil {
		st.list = &servers.List{}
	}
	return st
}

func (st *serverStore) view() ServerList {
	st.mu.Lock()
	defer st.mu.Unlock()
	out := ServerList{Entries: []ServerRow{}, Max: servers.MaxEntries}
	if st.err != nil {
		out.Error = "Saved servers could not be read; the file was left unchanged"
		return out
	}
	for _, e := range st.list.Entries {
		c, ok := st.checks[servers.Key(e)]
		if !ok {
			c = Check{Status: "unchecked"}
		}
		out.Entries = append(out.Entries, ServerRow{e, servers.LocalNetwork(e), c})
	}
	return out
}

// edit applies change to a copy of the list and replaces the file before the live list, so a failed save changes nothing.
func (st *serverStore) edit(change func(*servers.List) error) error {
	st.mu.Lock()
	defer st.mu.Unlock()
	if st.err != nil {
		return errors.New("Saved servers could not be read; fix or remove servers.json first")
	}
	next := &servers.List{Entries: append([]servers.Entry(nil), st.list.Entries...)}
	if err := change(next); err != nil {
		return errors.New(sentence(err.Error()))
	}
	if err := servers.Save(st.path, next); err != nil {
		return errors.New("Saved servers could not be written")
	}
	kept := map[string]Check{}
	for _, e := range next.Entries {
		if c, ok := st.checks[servers.Key(e)]; ok {
			kept[servers.Key(e)] = c
		}
	}
	st.list, st.checks = next, kept
	return nil
}

// sentence capitalizes an error from the servers package for display, like the app's other messages.
func sentence(msg string) string {
	if msg == "" {
		return msg
	}
	r, n := utf8.DecodeRuneInString(msg)
	return string(unicode.ToUpper(r)) + msg[n:]
}

func (st *serverStore) find(key string) (servers.Entry, bool) {
	st.mu.Lock()
	defer st.mu.Unlock()
	if i := st.list.Index(key); i >= 0 {
		return st.list.Entries[i], true
	}
	return servers.Entry{}, false
}

func (st *serverStore) record(key string, c Check) {
	st.mu.Lock()
	defer st.mu.Unlock()
	if st.list.Index(key) >= 0 {
		st.checks[key] = c
	}
}

// probe asks one saved server. It never sends the Play status token: that file belongs to the Play target only. record is
// false when the caller went away first, so a cancelled probe never overwrites an earlier result. A panic in a fetcher is
// an unavailable server, not a crashed app: probes run in goroutines that net/http does not guard.
func (s *Server) probe(parent context.Context, e servers.Entry) (c Check, record bool) {
	ctx, cancel := context.WithTimeout(parent, checkTimeout)
	defer cancel()
	defer func() {
		if recover() != nil {
			c, record = Check{Status: "unavailable", CheckedAt: time.Now().UnixMilli()}, parent.Err() == nil
		}
	}()
	snap, err := s.fetch.Fetch(ctx, tui.Query{Server: e.Server, StatusURL: e.StatusURL})
	if parent.Err() != nil {
		return Check{}, false
	}
	c = Check{Status: "reachable", CheckedAt: time.Now().UnixMilli(), RoundTripMS: float64(snap.RTT) / float64(time.Millisecond)}
	if err != nil {
		return Check{Status: "unavailable", CheckedAt: c.CheckedAt}, true
	}
	if snap.HTTP != nil {
		c.Rooms = len(snap.HTTP.Rooms)
	} else {
		c.Rooms = len(snap.Rooms.Rooms)
	}
	return c, true
}

func (s *Server) probeAndRecord(ctx context.Context, e servers.Entry) {
	if c, ok := s.probe(ctx, e); ok {
		s.servers.record(servers.Key(e), c)
	}
}

// checkAll probes every entry, a few at a time. The caller holds the app action slot, so two runs never overlap.
func (s *Server) checkAll(ctx context.Context, entries []servers.Entry) {
	slots := make(chan struct{}, checkParallel)
	var wg sync.WaitGroup
	for _, e := range entries {
		if ctx.Err() != nil {
			break
		}
		slots <- struct{}{}
		wg.Add(1)
		go func(e servers.Entry) {
			defer func() { <-slots; wg.Done() }()
			s.probeAndRecord(ctx, e)
		}(e)
	}
	wg.Wait()
}

type serverRef struct {
	Server    string `json:"server"`
	StatusURL string `json:"status_url"`
}

func (s *Server) serverAction(w http.ResponseWriter, r *http.Request) error {
	if err := s.role(false); err != nil {
		return err
	}
	switch r.URL.Path {
	case "/api/servers/add":
		var q servers.Entry
		if err := decode(r, &q); err != nil {
			return err
		}
		return s.servers.edit(func(l *servers.List) error { return l.Add(q) })
	case "/api/servers/remove":
		var q serverRef
		if err := decode(r, &q); err != nil {
			return err
		}
		return s.servers.edit(func(l *servers.List) error {
			if !l.Remove(servers.Key(servers.Normalize(servers.Entry{Server: q.Server, StatusURL: q.StatusURL}))) {
				return errors.New("That server is not in the list")
			}
			return nil
		})
	case "/api/servers/check":
		var q serverRef
		if err := decode(r, &q); err != nil {
			return err
		}
		key := servers.Key(servers.Normalize(servers.Entry{Server: q.Server, StatusURL: q.StatusURL}))
		e, ok := s.servers.find(key)
		if !ok {
			return errors.New("That server is not in the list")
		}
		s.probeAndRecord(r.Context(), e)
		return nil
	case "/api/servers/check-all":
		if err := decode(r, &struct{}{}); err != nil {
			return err
		}
		s.servers.mu.Lock()
		entries := append([]servers.Entry(nil), s.servers.list.Entries...)
		s.servers.mu.Unlock()
		// A full batch outlasts the usual write limit: 64 silent servers take 16 rounds of 4 at 5 s each.
		rounds := (len(entries) + checkParallel - 1) / checkParallel
		if err := http.NewResponseController(w).SetWriteDeadline(time.Now().Add(time.Duration(rounds)*checkTimeout + uiWriteTimeout)); err != nil {
			return errors.New("Check all cannot run here")
		}
		s.checkAll(r.Context(), entries)
		return nil
	case "/api/servers/use":
		var q serverRef
		if err := decode(r, &q); err != nil {
			return err
		}
		e, ok := s.servers.find(servers.Key(servers.Normalize(servers.Entry{Server: q.Server, StatusURL: q.StatusURL})))
		if !ok {
			return errors.New("That server is not in the list")
		}
		return s.useServer(e)
	case "/api/servers/preview", "/api/servers/import":
		var q struct {
			File string `json:"file"`
		}
		if err := decode(r, &q); err != nil {
			return err
		}
		other, err := servers.Parse(strings.NewReader(q.File))
		if err != nil {
			return errors.New("That is not a saved servers file: " + err.Error())
		}
		if r.URL.Path == "/api/servers/preview" {
			s.servers.mu.Lock()
			trial := &servers.List{Entries: append([]servers.Entry(nil), s.servers.list.Entries...)}
			bad := s.servers.err
			s.servers.mu.Unlock()
			if bad != nil {
				return errors.New("Saved servers could not be read; fix or remove servers.json first")
			}
			added, skipped := trial.Merge(other.Entries)
			writeJSON(w, map[string]int{"total": len(other.Entries), "add": added, "skip": skipped})
			return errNoState
		}
		// Import adds entries and contacts nothing: they stay "Not checked" until Check is pressed.
		return s.servers.edit(func(l *servers.List) error {
			if added, _ := l.Merge(other.Entries); added == 0 {
				return errors.New("nothing new to import")
			}
			return nil
		})
	case "/api/servers/export":
		if err := decode(r, &struct{}{}); err != nil {
			return err
		}
		s.servers.mu.Lock()
		b, err := s.servers.list.Marshal()
		bad := s.servers.err
		s.servers.mu.Unlock()
		if err != nil || bad != nil {
			return errors.New("Saved servers could not be exported")
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Content-Disposition", `attachment; filename="re-psn-servers.json"`)
		w.Write(b)
		return errNoState
	}
	return errors.New("Unknown app action")
}

// errNoState tells actionRequest the route already wrote its own response.
var errNoState = errors.New("response written")

// useServer makes a saved entry the Play target. Like the Settings route, it forgets the status token file when the
// status URL changes, so a token trusted for one endpoint is never sent to another.
func (s *Server) useServer(e servers.Entry) error {
	s.mu.Lock()
	next := *s.file
	old := s.file.Settings
	s.mu.Unlock()
	next.Server, next.StatusURL = e.Server, e.StatusURL
	if old.StatusURL != next.StatusURL {
		next.StatusTokenFile = ""
	}
	if err := next.Validate(); err != nil {
		return err
	}
	if err := config.Save(s.opts.ConfigPath, &next); err != nil {
		return errors.New("App settings could not be saved")
	}
	s.mu.Lock()
	s.file = &next
	s.lan = []LANServer{}
	if old.Server != next.Server || old.StatusURL != next.StatusURL {
		s.play = Play{Status: "unchecked", Rooms: []Room{}, Boards: []Board{}}
	}
	s.mu.Unlock()
	return nil
}
