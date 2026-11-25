package webui

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/undecuple/re-psn/app/framework"
	"github.com/undecuple/re-psn/app/internal/config"
	"github.com/undecuple/re-psn/app/internal/tui"
)

func (s *Server) role(host bool) error {
	s.mu.Lock()
	mode := s.file.Mode
	s.mu.Unlock()
	if mode == config.ModeBoth || host && mode == config.ModeHost || !host && mode == config.ModePlay {
		return nil
	}
	return framework.ErrUnauthorized
}
func (s *Server) actionRequest(w http.ResponseWriter, r *http.Request) error {
	switch r.URL.Path {
	case "/api/settings":
		var q struct {
			Settings
			StopHost bool `json:"stop_host"`
		}
		if err := decode(r, &q); err != nil {
			return err
		}
		s.mu.Lock()
		next := *s.file
		old := s.file.Settings
		s.mu.Unlock()
		next.Settings = config.Settings{Mode: config.Mode(q.Mode), Theme: config.Theme(q.Theme), Name: q.Name, Server: q.Server, StatusURL: q.StatusURL, StatusTokenFile: old.StatusTokenFile, HostAddress: q.HostAddress, HostRelay: strconv.FormatBool(q.HostRelay), SocialOrigin: q.SocialOrigin, SocialCAFile: q.SocialCAFile, SetupDone: strconv.FormatBool(q.SetupDone)}
		// Reader trust must not silently follow a newly selected endpoint.
		if old.StatusURL != next.StatusURL {
			next.StatusTokenFile = ""
		}
		if next.Mode == "" {
			return errors.New("Choose Play, Host or Both")
		}
		if err := next.Validate(); err != nil {
			return err
		}
		hosted := s.host.View()
		nextHost := next.HostAddress
		if nextHost == "" {
			nextHost = "127.0.0.1:36100"
		}
		changingHost := next.Mode == config.ModePlay || hosted.Address != nextHost || (old.HostRelay == "true") != (next.HostRelay == "true")
		if hosted.Running && changingHost {
			if !q.StopHost {
				return errors.New("Stop the owned server before changing its role or bind settings")
			}
			if err := s.host.Stop(r.Context()); err != nil {
				return err
			}
		}
		if err := config.Save(s.opts.ConfigPath, &next); err != nil {
			return errors.New("App settings could not be saved")
		}
		if next.Mode == config.ModeHost {
			if err := s.profile.Revoke(); err != nil {
				return err
			}
		}
		s.mu.Lock()
		s.file = &next
		s.lan = []LANServer{}
		if old.Server != next.Server || old.StatusURL != next.StatusURL {
			s.play = Play{Status: "unchecked", Rooms: []Room{}, Boards: []Board{}}
		}
		s.mu.Unlock()
	case "/api/play":
		if err := s.role(false); err != nil {
			return err
		}
		var q struct {
			Title  string `json:"title"`
			Board  uint32 `json:"board"`
			Boards bool   `json:"boards"`
		}
		if err := decode(r, &q); err != nil {
			return err
		}
		s.mu.Lock()
		v := s.file.Settings
		s.mu.Unlock()
		if v.Server == "" && v.StatusURL == "" {
			return errors.New("Set the remote player server first")
		}
		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()
		snapshot, err := s.fetch.Fetch(ctx, tui.Query{Server: v.Server, StatusURL: v.StatusURL, StatusTokenFile: v.StatusTokenFile, Title: q.Title, Board: q.Board, Boards: q.Boards})
		play := Play{Status: "reachable", CheckedAt: time.Now().UnixMilli(), Message: "Reachable at last check; the game connects independently", RoundTripMS: float64(snapshot.RTT) / float64(time.Millisecond), Rooms: []Room{}, Boards: []Board{}, Address: v.Server, Titled: q.Title != "", UsedStatusURL: v.StatusURL != "", Source: "game_connection"}
		if err != nil {
			play.Status = "unavailable"
			play.Message = "Remote server unavailable; check its address and whether it is running"
			play.Kind = failureKind(err, v.Server)
		} else if snapshot.HTTP != nil {
			play.Source = "status_page"
			play.Unlisted = snapshot.HTTP.Unlisted
			for _, room := range snapshot.HTTP.Rooms {
				if q.Title != "" && room.Title != q.Title {
					continue
				}
				play.Rooms = append(play.Rooms, Room{strconv.FormatUint(room.ID, 10), room.Title, len(room.Members), room.Max, room.Members})
				if len(play.Rooms) >= 200 {
					break
				}
			}
			for _, board := range snapshot.HTTP.Boards {
				if q.Title != "" && board.Title != q.Title {
					continue
				}
				play.Boards = append(play.Boards, Board{board.Board, board.Title, board.Leader, strconv.FormatInt(board.Best, 10), board.Entries})
				if len(play.Boards) >= 200 {
					break
				}
			}
		} else {
			play.StatusFallback = snapshot.HTTPUnavailable
			for _, room := range snapshot.Rooms.Rooms {
				play.Rooms = append(play.Rooms, Room{strconv.FormatUint(room.ID, 10), q.Title, int(room.Cur), int(room.Max), []string{room.Owner}})
			}
			for _, row := range snapshot.Ranking.Rows {
				play.Boards = append(play.Boards, Board{q.Board, q.Title, row.OnlineID, strconv.FormatInt(row.Score, 10), int(snapshot.Ranking.Total)})
			}
		}
		s.mu.Lock()
		s.play = play
		s.mu.Unlock()
	case "/api/host/start":
		if err := s.role(true); err != nil {
			return err
		}
		var q struct {
			ConfirmPublic bool `json:"confirm_public"`
		}
		if err := decode(r, &q); err != nil {
			return err
		}
		s.mu.Lock()
		v := s.settings()
		s.mu.Unlock()
		bind, _, _ := net.SplitHostPort(v.HostAddress)
		if ip := net.ParseIP(bind); ip == nil || !ip.IsLoopback() {
			if !q.ConfirmPublic {
				return errors.New("Confirm the game server can be reached on the selected network interface")
			}
		}
		if err := s.host.Start(r.Context(), v.HostAddress, v.HostRelay); err != nil {
			return err
		}
	case "/api/host/stop":
		if err := s.role(true); err != nil {
			return err
		}
		if err := decode(r, &struct{}{}); err != nil {
			return err
		}
		if err := s.host.Stop(r.Context()); err != nil {
			return err
		}
	case "/api/host/invite":
		// Read-only: lists this computer's addresses for the running owned host. Never polled; the page asks when the
		// Host page is shown with a running host, and when the person presses Refresh addresses.
		if err := s.role(true); err != nil {
			return err
		}
		if err := decode(r, &struct{}{}); err != nil {
			return err
		}
		v := s.host.View()
		if !v.Running {
			return errors.New("Start the owned host before listing addresses for friends")
		}
		invite, err := buildInvite(v.Address, s.addrs)
		if err != nil {
			return err
		}
		writeJSON(w, invite)
		return nil
	case "/api/host/status":
		// Read-only: one read of the owned host's own status page, only when the person presses Refresh status. The
		// token file is read here, on the server side, and the token is never part of any answer.
		if err := s.role(true); err != nil {
			return err
		}
		if err := decode(r, &struct{}{}); err != nil {
			return err
		}
		v, origin, tokenFile := s.hostSnapshot()
		if !v.Running || origin == "" {
			return errors.New("Start the owned host before reading its status")
		}
		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()
		stats, err := s.stats(ctx, origin, tokenFile)
		writeJSON(w, buildHostStatus(stats, err, v.StartedAt, time.Now()))
		return nil
	case "/api/lan/find":
		// One broadcast to this computer's subnets, only when the person presses the button. The answer is kept in memory
		// and shown in the state; nothing is saved or checked.
		if err := s.role(false); err != nil {
			return err
		}
		var q struct {
			Port *int `json:"port"`
		}
		if err := decode(r, &q); err != nil {
			return err
		}
		if err := s.findLAN(r.Context(), q.Port); err != nil {
			return err
		}
	case "/api/shutdown":
		if err := decode(r, &struct{}{}); err != nil {
			return err
		}
		if err := s.host.Stop(r.Context()); err != nil {
			return err
		}
		writeJSON(w, s.state())
		s.cancel()
		return nil
	case "/api/servers/add", "/api/servers/remove", "/api/servers/check", "/api/servers/check-all", "/api/servers/use", "/api/servers/export", "/api/servers/preview", "/api/servers/import":
		if err := s.serverAction(w, r); err != nil {
			if errors.Is(err, errNoState) {
				return nil
			}
			return err
		}
	default:
		if err := s.role(false); err != nil {
			return err
		}
		if err := s.personalAction(w, r); err != nil {
			return err
		}
		if r.URL.Path == "/api/profile/export" {
			return nil
		}
	}
	writeJSON(w, s.state())
	return nil
}
func (s *Server) personalAction(w http.ResponseWriter, r *http.Request) error {
	switch r.URL.Path {
	case "/api/profile/create":
		var q struct {
			Label string `json:"label"`
		}
		if err := decode(r, &q); err != nil {
			return err
		}
		return s.profile.Create(q.Label)
	case "/api/catalog":
		var q struct {
			Catalog string `json:"catalog"`
		}
		if err := decode(r, &q); err != nil {
			return err
		}
		c, err := framework.DecodeCatalog(strings.NewReader(q.Catalog))
		if err != nil {
			return errors.New("Invalid catalog; duplicate fields and incompatible definitions are refused")
		}
		return s.profile.InstallCatalog(c)
	case "/api/grant":
		var q struct {
			Title string `json:"title"`
		}
		if err := decode(r, &q); err != nil {
			return err
		}
		_, err := s.profile.Grant(q.Title)
		return err
	case "/api/revoke":
		if err := decode(r, &struct{}{}); err != nil {
			return err
		}
		return s.profile.Revoke()
	case "/api/friends":
		var q struct {
			Action string `json:"action"`
			ID     string `json:"id"`
		}
		if err := decode(r, &q); err != nil {
			return err
		}
		s.mu.Lock()
		v := s.file.Settings
		s.mu.Unlock()
		if v.SocialOrigin == "" {
			return errors.New("Choose an HTTPS friend service explicitly in Settings")
		}
		return s.profile.SocialContext(r.Context(), v.SocialOrigin, v.SocialCAFile, q.Action, q.ID)
	case "/api/profile/export":
		var q struct {
			Passphrase string `json:"passphrase"`
		}
		if err := decode(r, &q); err != nil {
			return err
		}
		pass := []byte(q.Passphrase)
		defer clear(pass)
		var b bytes.Buffer
		if err := s.profile.Export(&b, pass); err != nil {
			return err
		}
		w.Header().Set("Content-Type", "application/octet-stream")
		w.Header().Set("Content-Disposition", `attachment; filename="re-psn-profile.backup"`)
		w.Write(b.Bytes())
		return nil
	case "/api/profile/restore":
		var q struct {
			Archive    string `json:"archive"`
			Passphrase string `json:"passphrase"`
		}
		if err := decode(r, &q); err != nil {
			return err
		}
		if len(q.Archive) > base64.StdEncoding.EncodedLen(framework.MaxStateBytes+52) {
			return errors.New("Backup exceeds its limit")
		}
		archive, err := base64.StdEncoding.Strict().DecodeString(q.Archive)
		if err != nil {
			return errors.New("Invalid encrypted backup")
		}
		pass := []byte(q.Passphrase)
		defer clear(pass)
		return s.profile.Restore(bytes.NewReader(archive), pass)
	default:
		return errors.New("Unknown app action")
	}
}
