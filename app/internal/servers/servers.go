// Package servers keeps the user's own list of game servers: a name, a host:port and an optional status URL for each.
// The list is a small JSON file beside the settings file. Nothing in this package touches the network, and nothing is
// fetched from anywhere: the list holds only what the user typed or imported from a file they chose.
package servers

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/undecuple/re-psn/app/internal/config"
)

// FileName is the list's name, in the same folder as the settings file.
const FileName = "servers.json"

const (
	// Version is the file format version.
	Version = 1
	// MaxEntries is the most servers a list may hold.
	MaxEntries = 64
	// MaxFileBytes is the largest list file the app reads or imports.
	MaxFileBytes = 64 << 10
	// MaxNameRunes is the longest server name.
	MaxNameRunes = 64
)

// Entry is one server. StatusURL may be empty.
type Entry struct {
	Name      string `json:"name"`
	Server    string `json:"server"`
	StatusURL string `json:"status_url,omitempty"`
}

// List is the user's servers, in the order they were added.
type List struct {
	Entries []Entry
}

type fileFormat struct {
	Version int     `json:"version"`
	Servers []Entry `json:"servers"`
}

// Key identifies an entry: its game server and its status URL, both in the form Normalize gives. Two entries with one key
// are duplicates.
func Key(e Entry) string { return e.Server + "\n" + e.StatusURL }

// Normalize puts an entry in its one canonical form: spaces trimmed; in the game server and the status URL the host in lower
// case without a trailing dot; in the status URL the scheme in lower case and the default port (80 for http, 443 for https)
// dropped. A field it cannot parse is only trimmed, and Validate then rejects it.
func Normalize(e Entry) Entry {
	e = Entry{Name: strings.TrimSpace(e.Name), Server: strings.TrimSpace(e.Server), StatusURL: strings.TrimSpace(e.StatusURL)}
	if host, port, err := net.SplitHostPort(e.Server); err == nil {
		e.Server = net.JoinHostPort(strings.TrimSuffix(strings.ToLower(host), "."), canonPort(port))
	}
	if u, err := url.Parse(e.StatusURL); err == nil && e.StatusURL != "" && u.Host != "" {
		u.Scheme = strings.ToLower(u.Scheme)
		host := strings.TrimSuffix(strings.ToLower(u.Hostname()), ".")
		port := canonPort(u.Port())
		if port == "80" && u.Scheme == "http" || port == "443" && u.Scheme == "https" {
			port = ""
		}
		if strings.Contains(host, ":") {
			host = "[" + host + "]"
		}
		if port != "" {
			host += ":" + port
		}
		u.Host = host
		e.StatusURL = u.String()
	}
	return e
}

// canonPort writes a numeric port the one way, so "080" and "+80" are "80". Anything else is left for Validate to refuse.
func canonPort(p string) string {
	if n, err := strconv.Atoi(p); err == nil && n >= 1 && n <= 65535 {
		return strconv.Itoa(n)
	}
	return p
}

// Validate reports whether e is acceptable as it is; Normalize it first.
func Validate(e Entry) error {
	if e != Normalize(e) {
		return errors.New("the server entry is not in its normal form")
	}
	if n := utf8.RuneCountInString(e.Name); n < 1 || n > MaxNameRunes {
		return fmt.Errorf("the server name must be 1 to %d characters", MaxNameRunes)
	}
	if !utf8.ValidString(e.Name) {
		return errors.New("the server name is not valid text")
	}
	for _, r := range e.Name {
		if unicode.IsControl(r) {
			return errors.New("the server name has a control character")
		}
	}
	if err := config.ValidateServer(e.Server); err != nil {
		return err
	}
	if e.StatusURL != "" {
		if err := config.ValidateURL(e.StatusURL); err != nil {
			return err
		}
		if u, err := url.Parse(e.StatusURL); err != nil || u.User != nil {
			return errors.New("the status URL must not contain a user name or password")
		}
	}
	return nil
}

// Add appends e after normalizing and validating it. A duplicate or a full list is an error.
func (l *List) Add(e Entry) error {
	e = Normalize(e)
	if err := Validate(e); err != nil {
		return err
	}
	if len(l.Entries) >= MaxEntries {
		return fmt.Errorf("the list already holds %d servers", MaxEntries)
	}
	if l.Index(Key(e)) >= 0 {
		return errors.New("that server is already in the list")
	}
	l.Entries = append(l.Entries, e)
	return nil
}

// Index returns the position of the entry with this key, or -1.
func (l *List) Index(key string) int {
	for i, e := range l.Entries {
		if Key(e) == key {
			return i
		}
	}
	return -1
}

// Remove deletes the entry with this key and reports whether there was one.
func (l *List) Remove(key string) bool {
	i := l.Index(key)
	if i < 0 {
		return false
	}
	l.Entries = append(l.Entries[:i:i], l.Entries[i+1:]...)
	return true
}

// Merge adds the entries of other that are not already in the list, up to MaxEntries. It returns how many it added and how
// many it skipped (duplicates, invalid entries, or no room left). The entries are normalized first.
func (l *List) Merge(other []Entry) (added, skipped int) {
	for _, e := range other {
		e = Normalize(e)
		if Validate(e) != nil || len(l.Entries) >= MaxEntries || l.Index(Key(e)) >= 0 {
			skipped++
			continue
		}
		l.Entries = append(l.Entries, e)
		added++
	}
	return added, skipped
}

// LocalNetwork reports whether the entry's game server or status URL names a loopback, private, link-local or unspecified
// address, or localhost. Only literal addresses and "localhost" are recognised: other names are not resolved here.
func LocalNetwork(e Entry) bool {
	if host, _, err := net.SplitHostPort(e.Server); err == nil && localHost(host) {
		return true
	}
	if e.StatusURL != "" {
		if u, err := url.Parse(e.StatusURL); err == nil && localHost(u.Hostname()) {
			return true
		}
	}
	return false
}

func localHost(h string) bool {
	h = strings.TrimSuffix(strings.ToLower(h), ".")
	if h == "localhost" || strings.HasSuffix(h, ".localhost") {
		return true
	}
	ip := net.ParseIP(strings.SplitN(h, "%", 2)[0])
	return ip != nil && (ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsUnspecified())
}

// Parse reads a list from r. It accepts only this package's own format: the right version, known fields only, valid
// entries, no duplicates, at most MaxEntries entries and MaxFileBytes bytes, and nothing after the JSON value.
func Parse(r io.Reader) (*List, error) {
	data, err := io.ReadAll(io.LimitReader(r, MaxFileBytes+1))
	if err != nil {
		return nil, err
	}
	if len(data) > MaxFileBytes {
		return nil, fmt.Errorf("the server list is larger than %d KiB", MaxFileBytes>>10)
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	var f fileFormat
	if err := dec.Decode(&f); err != nil {
		return nil, fmt.Errorf("the server list is not valid: %w", err)
	}
	if dec.More() {
		return nil, errors.New("the server list has data after its end")
	}
	if _, err := dec.Token(); err != io.EOF {
		return nil, errors.New("the server list has data after its end")
	}
	if f.Version != Version {
		return nil, fmt.Errorf("the server list has version %d; this app reads version %d", f.Version, Version)
	}
	if len(f.Servers) > MaxEntries {
		return nil, fmt.Errorf("the server list holds more than %d servers", MaxEntries)
	}
	l := &List{}
	for i, e := range f.Servers {
		e = Normalize(e)
		if err := Validate(e); err != nil {
			return nil, fmt.Errorf("server %d: %w", i+1, err)
		}
		if l.Index(Key(e)) >= 0 {
			return nil, fmt.Errorf("server %d is a duplicate", i+1)
		}
		l.Entries = append(l.Entries, e)
	}
	return l, nil
}

// Marshal renders the list in the file format, with a final newline.
func (l *List) Marshal() ([]byte, error) {
	servers := l.Entries
	if servers == nil {
		servers = []Entry{}
	}
	b, err := json.MarshalIndent(fileFormat{Version: Version, Servers: servers}, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(b, '\n'), nil
}

// PathFor returns where the list lives for a settings file path: beside it.
func PathFor(settingsPath string) string { return filepath.Join(filepath.Dir(settingsPath), FileName) }

// Load reads the list at path. A missing file is an empty list.
func Load(path string) (*List, error) {
	fh, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return &List{}, nil
	}
	if err != nil {
		return nil, err
	}
	defer fh.Close()
	l, err := Parse(fh)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return l, nil
}

// Save writes the list to path atomically: a temporary file in the same folder, mode 0600, then a rename over the old file.
// It creates the folder (0700).
func Save(path string, l *List) error {
	b, err := l.Marshal()
	if err != nil {
		return err
	}
	if len(b) > MaxFileBytes {
		return fmt.Errorf("the server list is larger than %d KiB", MaxFileBytes>>10)
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("server list: %w", err)
	}
	tmp, err := os.CreateTemp(dir, "."+FileName+".*")
	if err != nil {
		return fmt.Errorf("server list: %w", err)
	}
	defer os.Remove(tmp.Name()) // fails harmlessly after the rename
	err = tmp.Chmod(0o600)
	if err == nil {
		_, err = tmp.Write(b)
	}
	if err == nil {
		err = tmp.Sync()
	}
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return fmt.Errorf("server list %s: %w", tmp.Name(), err)
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		return fmt.Errorf("server list: %w", err)
	}
	return nil
}
