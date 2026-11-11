package tui

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
	"unicode"
)

const (
	maxStatusBody  = 4 << 20
	maxStatusRows  = 5000
	maxStatusToken = 4096
	statusTimeout  = 2 * time.Second
)

// HTTPRoom and HTTPBoard are the data shared by the optional status listener.
// Integers retain their full wire precision; display code treats strings as untrusted.
type HTTPRoom struct {
	ID      uint64   `json:"id"`
	Title   string   `json:"title"`
	Members []string `json:"members"`
	Max     int      `json:"max"`
	Age     string   `json:"age"`
}
type HTTPBoard struct {
	Title   string `json:"title"`
	Board   uint32 `json:"board"`
	Entries int    `json:"entries"`
	Leader  string `json:"leader"`
	Best    int64  `json:"best"`
}
type HTTPStats struct {
	Uptime      string
	Clients     int
	Connections uint64
	Unlisted    int
	Rooms       []HTTPRoom
	Boards      []HTTPBoard
	Server      ServerStats
}

var errHTTPStatus = errors.New("HTTP status unavailable")

// NativeStatus uses HTTP only when explicitly configured. A failed HTTP request
// leaves time within the shared refresh deadline for the existing protocol view.
type NativeStatus struct{}

func (NativeStatus) Fetch(ctx context.Context, q Query) (Snapshot, error) {
	if q.StatusURL == "" {
		return (Protocol{}).Fetch(ctx, q)
	}
	if s, err := fetchHTTPStatus(ctx, q.StatusURL, q.StatusTokenFile); err == nil {
		return Snapshot{HTTP: s}, nil
	}
	s, err := (Protocol{}).Fetch(ctx, q)
	s.HTTPUnavailable = true
	return s, err
}

func statusEndpoint(address string) (string, error) {
	u, err := url.Parse(address)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || u.Opaque != "" {
		return "", errHTTPStatus
	}
	u.Path = strings.TrimRight(u.Path, "/")
	// Clearing RawPath lets net/url encode the normalized decoded path once.
	u.RawPath = ""
	if !strings.HasSuffix(u.Path, "/api/stats") {
		u.Path += "/api/stats"
	}
	return u.String(), nil
}

func statusToken(path string) (string, error) {
	if path == "" {
		return "", nil
	}
	// Refuse ordinary FIFOs/devices before opening them, and bound the regular
	// file's size both before and after opening. This is a user-selected local file.
	st, err := os.Stat(path)
	if err != nil || !st.Mode().IsRegular() || st.Size() > maxStatusToken {
		return "", errHTTPStatus
	}
	f, err := os.Open(path)
	if err != nil {
		return "", errHTTPStatus
	}
	defer f.Close()
	st, err = f.Stat()
	if err != nil || !st.Mode().IsRegular() || st.Size() > maxStatusToken {
		return "", errHTTPStatus
	}
	b, err := io.ReadAll(io.LimitReader(f, maxStatusToken+1))
	if err != nil || len(b) > maxStatusToken {
		return "", errHTTPStatus
	}
	token := strings.TrimSpace(string(b))
	if token == "" || strings.IndexFunc(token, unicode.IsControl) >= 0 {
		return "", errHTTPStatus
	}
	return token, nil
}

func fetchHTTPStatus(ctx context.Context, address, tokenFile string) (*HTTPStats, error) {
	endpoint, err := statusEndpoint(address)
	if err != nil {
		return nil, errHTTPStatus
	}
	token, err := statusToken(tokenFile)
	if err != nil {
		return nil, errHTTPStatus
	}
	ctx, cancel := context.WithTimeout(ctx, statusTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, errHTTPStatus
	}
	req.Header.Set("Accept", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	transport := &http.Transport{Proxy: nil, DialContext: (&net.Dialer{Timeout: statusTimeout}).DialContext,
		TLSHandshakeTimeout: statusTimeout, ResponseHeaderTimeout: statusTimeout,
		MaxResponseHeaderBytes: 32 << 10, DisableKeepAlives: true}
	defer transport.CloseIdleConnections()
	c := &http.Client{Transport: transport, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	res, err := c.Do(req)
	if err != nil {
		return nil, errHTTPStatus
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return nil, errHTTPStatus
	}
	b, err := io.ReadAll(io.LimitReader(res.Body, maxStatusBody+1))
	if err != nil || len(b) > maxStatusBody {
		return nil, errHTTPStatus
	}
	return decodeHTTPStatus(b)
}

func decodeHTTPStatus(b []byte) (*HTTPStats, error) {
	var w struct {
		Uptime      *string      `json:"uptime"`
		Clients     *int         `json:"clients"`
		Connections *uint64      `json:"connections_total"`
		Unlisted    *int         `json:"unlisted_rooms"`
		Rooms       *[]HTTPRoom  `json:"rooms"`
		Boards      *[]HTTPBoard `json:"boards"`
	}
	if json.Unmarshal(b, &w) != nil || w.Uptime == nil || w.Clients == nil || w.Connections == nil || w.Unlisted == nil || w.Rooms == nil || w.Boards == nil {
		return nil, errHTTPStatus
	}
	if *w.Clients < 0 || *w.Clients > 1000000 || *w.Unlisted < 0 || *w.Unlisted > 1000000 || len(*w.Rooms) > maxStatusRows || len(*w.Boards) > maxStatusRows || !statusText(*w.Uptime, 64) || *w.Uptime == "" {
		return nil, errHTTPStatus
	}
	for _, r := range *w.Rooms {
		if r.Max < 0 || r.Max > 255 || len(r.Members) > r.Max || !statusText(r.Title, 256) || !statusText(r.Age, 64) {
			return nil, errHTTPStatus
		}
		for _, name := range r.Members {
			if !statusText(name, 256) {
				return nil, errHTTPStatus
			}
		}
	}
	for _, r := range *w.Boards {
		if r.Entries < 0 || r.Entries > 1000000 || !statusText(r.Title, 256) || !statusText(r.Leader, 256) {
			return nil, errHTTPStatus
		}
	}
	return &HTTPStats{Uptime: *w.Uptime, Clients: *w.Clients, Connections: *w.Connections, Unlisted: *w.Unlisted, Rooms: *w.Rooms, Boards: *w.Boards, Server: decodeServerStats(b)}, nil
}
func statusText(s string, limit int) bool { return len(s) <= limit }
