package webui

import (
	"context"
	"errors"
	"net"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/undecuple/re-psn/app/internal/client"
)

// maxLANServers bounds the list kept and shown; a busy network could otherwise bury the few servers that matter.
const maxLANServers = 16

// lanWait is how long a search listens for replies.
const lanWait = 1500 * time.Millisecond

// lanSearchError is the only text a failed search produces; it carries nothing from the failure itself.
const lanSearchError = "The search on this network failed. Check that this computer is connected to a network, then try again."

// discoverer broadcasts one discovery request and returns the servers that answered. It is a field on Server so tests
// can inject an answer; the real one is client.Discover.
type discoverer func(ctx context.Context, wait time.Duration, port uint16) ([]client.Found, error)

// LANServer is one server that answered a search. Every field came from the network, so the text is cleaned and
// bounded, and the page shows it as text only. Nothing here is saved or checked.
type LANServer struct {
	Address string `json:"address"`
	Name    string `json:"name"`
	Players int    `json:"players"`
	Version string `json:"version"`
}

// cleanLAN drops control, format and direction-override characters and keeps at most limit characters.
func cleanLAN(s string, limit int) string {
	var b strings.Builder
	n := 0
	for _, r := range s {
		if r == utf8.RuneError || unicode.IsControl(r) || unicode.Is(unicode.Cf, r) || !unicode.IsPrint(r) {
			continue
		}
		if n >= limit {
			break
		}
		b.WriteRune(r)
		n++
	}
	return strings.TrimSpace(b.String())
}

// buildLAN turns what Discover returned into the list the page shows. It lists only what Discover returned, at most
// maxLANServers, in the order they answered.
func buildLAN(found []client.Found) []LANServer {
	out := []LANServer{}
	for _, f := range found {
		if len(out) >= maxLANServers {
			break
		}
		if !f.IP.IsValid() || f.TCPPort == 0 {
			continue
		}
		out = append(out, LANServer{Address: f.Addr(), Name: cleanLAN(f.Name, 24), Players: int(f.Players), Version: cleanLAN(f.Version, 12)})
	}
	return out
}

// lanPort picks the port to search. An explicit port must be 1 to 65535. Otherwise it is the port in the saved server
// address when that has one, else the default.
func lanPort(requested *int, server string) (uint16, error) {
	if requested != nil {
		if *requested < 1 || *requested > 65535 {
			return 0, errors.New("The port must be a number from 1 to 65535")
		}
		return uint16(*requested), nil
	}
	if _, p, err := net.SplitHostPort(server); err == nil {
		if n, err := strconv.Atoi(p); err == nil && n >= 1 && n <= 65535 {
			return uint16(n), nil
		}
	}
	return client.DefaultPort, nil
}

// findLAN runs one search and keeps the result in memory. A failed search clears the list and gives a plain message.
func (s *Server) findLAN(ctx context.Context, requested *int) error {
	s.mu.Lock()
	server := s.file.Settings.Server
	s.mu.Unlock()
	port, err := lanPort(requested, server)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, lanWait+2*time.Second)
	defer cancel()
	found, err := s.discover(ctx, lanWait, port)
	s.mu.Lock()
	defer s.mu.Unlock()
	if err != nil {
		s.lan = []LANServer{}
		return errors.New(lanSearchError)
	}
	s.lan = buildLAN(found)
	return nil
}
