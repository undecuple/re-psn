// Package config reads and writes the app's per-user settings file,
// re-psn.ini. The format is fixed and small: [section] lines, key = value
// lines, and whole-line comments starting with # or ;. There are no
// includes, escapes, quotes or variable expansion. Lines the package does not
// know (other keys, other sections, comments) are kept and written back.
package config

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
)

// FileName is the settings file's name.
const FileName = "re-psn.ini"

// Mode is what the app does: play, host or both.
type Mode string

const (
	ModePlay Mode = "play"
	ModeHost Mode = "host"
	ModeBoth Mode = "both"
)

// Theme is the app's colour mode: dark or light. Unset means dark.
type Theme string

const (
	ThemeDark   Theme = "dark"
	ThemeLight  Theme = "light"
	ThemeSystem Theme = "system"
)

// Settings are the keys this package knows. An empty value means unset.
type Settings struct {
	Mode            Mode   // [app] mode
	Theme           Theme  // [app] theme
	Name            string // [player] name: the online ID
	Server          string // [server] server: host:port
	StatusURL       string // [server] status_url
	StatusTokenFile string // [server] status_token_file: a path, used as written
	HostAddress     string // [host] address: independent of the remote player endpoint
	HostRelay       string // [host] relay: true or false
	SocialOrigin    string // [social] origin: explicit HTTPS origin
	SocialCAFile    string // [social] ca_file: optional local CA file
}

// key is where a known setting lives and how to reach its field.
type key struct {
	section, name string
	field         func(*Settings) *string
	check         func(string) error
}

var keys = []key{
	{"app", "mode", func(s *Settings) *string { return (*string)(&s.Mode) }, checkMode},
	{"app", "theme", func(s *Settings) *string { return (*string)(&s.Theme) }, checkTheme},
	{"player", "name", func(s *Settings) *string { return &s.Name }, checkName},
	{"server", "server", func(s *Settings) *string { return &s.Server }, checkServer},
	{"server", "status_url", func(s *Settings) *string { return &s.StatusURL }, checkURL},
	{"server", "status_token_file", func(s *Settings) *string { return &s.StatusTokenFile }, nil},
	{"host", "address", func(s *Settings) *string { return &s.HostAddress }, checkHostAddress},
	{"host", "relay", func(s *Settings) *string { return &s.HostRelay }, checkBool},
	{"social", "origin", func(s *Settings) *string { return &s.SocialOrigin }, checkSocialOrigin},
	{"social", "ca_file", func(s *Settings) *string { return &s.SocialCAFile }, nil},
}

func lookup(section, name string) *key {
	for i := range keys {
		if keys[i].section == section && keys[i].name == name {
			return &keys[i]
		}
	}
	return nil
}

// File is a parsed settings file: the known settings plus every line as read.
type File struct {
	Settings
	lines []line
}

type line struct {
	raw     string // the line as read, written back unless it holds a known key
	section string // the section it is in
	key     string // set for key = value lines
}

// Parse reads a settings file. Errors name the line and, for a bad value, the key.
func Parse(r io.Reader) (*File, error) {
	f := &File{}
	seen := map[string]bool{}
	section := ""
	sc := bufio.NewScanner(r)
	for n := 1; sc.Scan(); n++ {
		raw := strings.TrimSuffix(sc.Text(), "\r")
		l, err := parseLine(raw, &section)
		if err != nil {
			return nil, fmt.Errorf("%s line %d: %w", FileName, n, err)
		}
		if k := lookup(l.section, l.key); k != nil {
			id := k.section + "." + k.name
			if seen[id] {
				return nil, fmt.Errorf("%s line %d: [%s] %s is set twice", FileName, n, k.section, k.name)
			}
			seen[id] = true
			*k.field(&f.Settings) = value(raw)
		}
		f.lines = append(f.lines, l)
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("%s: %w", FileName, err)
	}
	if err := f.Validate(); err != nil {
		return nil, fmt.Errorf("%s: %w", FileName, err)
	}
	return f, nil
}

func parseLine(raw string, section *string) (line, error) {
	t := strings.TrimSpace(raw)
	switch {
	case t == "" || t[0] == '#' || t[0] == ';':
		return line{raw: raw, section: *section}, nil
	case t[0] == '[':
		if !strings.HasSuffix(t, "]") || len(t) < 3 {
			return line{}, fmt.Errorf("section header %q: want [name]", t)
		}
		*section = strings.TrimSpace(t[1 : len(t)-1])
		return line{raw: raw, section: *section}, nil
	}
	k, _, ok := strings.Cut(t, "=")
	k = strings.TrimSpace(k)
	switch {
	case !ok || k == "":
		return line{}, fmt.Errorf("%q: want key = value", t)
	case *section == "":
		return line{}, fmt.Errorf("key %q is outside a section", k)
	}
	return line{raw: raw, section: *section, key: k}, nil
}

func value(raw string) string {
	_, v, _ := strings.Cut(raw, "=")
	return strings.TrimSpace(v)
}

// Validate checks every set value and names the key of the first bad one.
func (s Settings) Validate() error {
	for _, k := range keys {
		v := *k.field(&s)
		if v == "" {
			continue
		}
		if strings.ContainsFunc(v, func(r rune) bool { return r < 0x20 || r == 0x7f }) {
			return fmt.Errorf("[%s] %s: holds a control character", k.section, k.name)
		}
		if v != strings.TrimSpace(v) {
			return fmt.Errorf("[%s] %s: starts or ends with a space", k.section, k.name)
		}
		if k.check != nil {
			if err := k.check(v); err != nil {
				return fmt.Errorf("[%s] %s: %w", k.section, k.name, err)
			}
		}
	}
	return nil
}

func checkMode(v string) error {
	switch Mode(v) {
	case ModePlay, ModeHost, ModeBoth:
		return nil
	}
	return fmt.Errorf("%q: want play, host or both", v)
}

func checkTheme(v string) error {
	switch Theme(v) {
	case ThemeDark, ThemeLight, ThemeSystem:
		return nil
	}
	return fmt.Errorf("%q: want dark, light or system", v)
}

func checkHostAddress(v string) error {
	h, _, err := net.SplitHostPort(v)
	if err != nil || net.ParseIP(h) == nil || checkServer(v) != nil {
		return errors.New("want a literal IP:port, for example 127.0.0.1:36100")
	}
	return nil
}
func checkBool(v string) error {
	if v != "true" && v != "false" {
		return errors.New("want true or false")
	}
	return nil
}
func checkSocialOrigin(v string) error {
	u, err := url.Parse(v)
	if err != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || u.Opaque != "" || u.RawPath != "" || (u.Path != "" && u.Path != "/") {
		return errors.New("want an HTTPS origin without credentials, path, query or fragment")
	}
	return nil
}

// checkName follows PSN online IDs: 3-16 letters, digits, - or _.
func checkName(v string) error {
	if len(v) < 3 || len(v) > 16 {
		return fmt.Errorf("%q: want 3-16 characters", v)
	}
	for _, r := range v {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '_') {
			return fmt.Errorf("%q: only letters, digits, - and _", v)
		}
	}
	return nil
}

func checkServer(v string) error {
	host, port, err := net.SplitHostPort(v)
	if err != nil {
		return fmt.Errorf("%q: want host:port", v)
	}
	if n, err := strconv.Atoi(port); host == "" || err != nil || n < 1 || n > 65535 {
		return fmt.Errorf("%q: want host:port with a port from 1 to 65535", v)
	}
	return nil
}

func checkURL(v string) error {
	u, err := url.Parse(v)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return fmt.Errorf("%q: want an http:// or https:// URL", v)
	}
	return nil
}

// ValidateServer reports whether v is a game server address: host:port with a port from 1 to 65535.
func ValidateServer(v string) error { return checkServer(v) }

// ValidateURL reports whether v is an http:// or https:// status URL.
func ValidateURL(v string) error { return checkURL(v) }

// Bytes renders the file: every line as read, known keys with their current
// values, and known keys that were not in the file added to their section.
func (f *File) Bytes() ([]byte, error) {
	if err := f.Validate(); err != nil {
		return nil, err
	}
	var b bytes.Buffer
	written := map[*key]bool{}
	for i, l := range f.lines {
		if k := lookup(l.section, l.key); k != nil {
			b.WriteString(keyLine(k, &f.Settings))
			written[k] = true
		} else {
			b.WriteString(l.raw + "\n")
		}
		if f.sectionEnds(i) {
			f.addMissing(&b, l.section, written)
		}
	}
	for _, sec := range []string{"app", "player", "server", "host", "social"} {
		if !f.hasSection(sec) && f.anySet(sec) {
			if b.Len() > 0 {
				b.WriteString("\n")
			}
			fmt.Fprintf(&b, "[%s]\n", sec)
			f.addMissing(&b, sec, written)
		}
	}
	return b.Bytes(), nil
}

func keyLine(k *key, s *Settings) string {
	if v := *k.field(s); v != "" {
		return k.name + " = " + v + "\n"
	}
	return k.name + " =\n"
}

func (f *File) sectionEnds(i int) bool {
	return i == len(f.lines)-1 || f.lines[i+1].section != f.lines[i].section
}

func (f *File) hasSection(sec string) bool {
	for _, l := range f.lines {
		if l.section == sec {
			return true
		}
	}
	return false
}

func (f *File) anySet(sec string) bool {
	for _, k := range keys {
		if k.section == sec && *k.field(&f.Settings) != "" {
			return true
		}
	}
	return false
}

// addMissing writes the set keys of sec that the file did not hold.
func (f *File) addMissing(b *bytes.Buffer, sec string, written map[*key]bool) {
	for i := range keys {
		k := &keys[i]
		if k.section == sec && !written[k] && *k.field(&f.Settings) != "" {
			b.WriteString(keyLine(k, &f.Settings))
			written[k] = true
		}
	}
}

// Path returns where the settings file lives for this user.
func Path() (string, error) {
	home, _ := os.UserHomeDir()
	return pathFor(runtime.GOOS, os.Getenv, home)
}

// pathFor: %AppData%\re-psn on Windows; elsewhere $XDG_CONFIG_HOME/re-psn
// (when absolute), else ~/.config/re-psn.
func pathFor(goos string, getenv func(string) string, home string) (string, error) {
	if goos == "windows" {
		dir := getenv("AppData")
		if dir == "" {
			return "", errors.New("settings file: %AppData% is not set")
		}
		return filepath.Join(dir, "re-psn", FileName), nil
	}
	// Not filepath.IsAbs: this branch is for non-Windows systems, where absolute means a leading slash, whatever
	// system the code runs on (the tests run it on Windows too).
	if dir := getenv("XDG_CONFIG_HOME"); strings.HasPrefix(dir, "/") {
		return filepath.Join(dir, "re-psn", FileName), nil
	}
	if home == "" {
		return "", errors.New("settings file: neither $XDG_CONFIG_HOME nor a home directory is set")
	}
	return filepath.Join(home, ".config", "re-psn", FileName), nil
}

// Load reads the settings file at path. A missing file gives an empty File
// and an error that matches os.ErrNotExist.
func Load(path string) (*File, error) {
	fh, err := os.Open(path)
	if err != nil {
		return &File{}, err
	}
	defer fh.Close()
	f, err := Parse(fh)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return f, nil
}

// Save writes f to path atomically: a temporary file in the same directory,
// mode 0600, then a rename over the old one. It creates the directory (0700).
func Save(path string, f *File) error {
	b, err := f.Bytes()
	if err != nil {
		return err
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("settings file: %w", err)
	}
	tmp, err := os.CreateTemp(dir, "."+FileName+".*")
	if err != nil {
		return fmt.Errorf("settings file: %w", err)
	}
	defer os.Remove(tmp.Name()) // fails harmlessly after the rename
	if err := writeAll(tmp, b); err != nil {
		return fmt.Errorf("settings file %s: %w", tmp.Name(), err)
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		return fmt.Errorf("settings file: %w", err)
	}
	return nil
}

func writeAll(fh *os.File, b []byte) error {
	err := fh.Chmod(0o600)
	if err == nil {
		_, err = fh.Write(b)
	}
	if err == nil {
		err = fh.Sync()
	}
	if cerr := fh.Close(); err == nil {
		err = cerr
	}
	return err
}
