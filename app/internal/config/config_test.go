package config

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

const sample = `# re-psn settings
[app]
mode = both
theme = dark

[player]
; the online ID
name = alice_01

[server]
server = play.example.org:36100
status_url = https://play.example.org/status
status_token_file = /etc/re-psn/status-token
retries = 3

[later]
anything = goes here
`

func TestParse(t *testing.T) {
	f, err := Parse(strings.NewReader(sample))
	if err != nil {
		t.Fatal(err)
	}
	want := Settings{
		Mode: ModeBoth, Theme: ThemeDark, Name: "alice_01", Server: "play.example.org:36100",
		StatusURL: "https://play.example.org/status", StatusTokenFile: "/etc/re-psn/status-token",
	}
	if f.Settings != want {
		t.Fatalf("got %+v\nwant %+v", f.Settings, want)
	}
}

func TestRoundTripKeepsUnknownLines(t *testing.T) {
	f, err := Parse(strings.NewReader(sample))
	if err != nil {
		t.Fatal(err)
	}
	b, err := f.Bytes()
	if err != nil {
		t.Fatal(err)
	}
	if string(b) != sample {
		t.Fatalf("round trip changed the file:\n%s", b)
	}

	f.Name, f.Mode = "bob", ModeHost
	b, err = f.Bytes()
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"mode = host\ntheme = dark\n", "; the online ID\nname = bob\n", "retries = 3\n", "[later]\nanything = goes here\n"} {
		if !strings.Contains(string(b), want) {
			t.Errorf("written file lacks %q:\n%s", want, b)
		}
	}
}

func TestBytesAddsMissingKeys(t *testing.T) {
	cases := []struct {
		name, in string
		set      Settings
		want     string
	}{
		{"empty file", "", Settings{Mode: ModePlay, Server: "h:1"}, "[app]\nmode = play\n\n[server]\nserver = h:1\n"},
		{"into an existing section", "[server]\n# note\n\n[app]\n", Settings{Server: "h:1"}, "[server]\n# note\n\nserver = h:1\n[app]\n"},
		{"unset key that was in the file", "[player]\nname = carol\n", Settings{}, "[player]\nname =\n"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f, err := Parse(strings.NewReader(c.in))
			if err != nil {
				t.Fatal(err)
			}
			f.Settings = c.set
			b, err := f.Bytes()
			if err != nil {
				t.Fatal(err)
			}
			if string(b) != c.want {
				t.Fatalf("got\n%q\nwant\n%q", b, c.want)
			}
		})
	}
}

func TestParseErrors(t *testing.T) {
	cases := []struct{ name, in, want string }{
		{"bad mode", "[app]\nmode = spectate\n", "[app] mode"},
		{"bad theme", "[app]\ntheme = blue\n", "[app] theme"},
		{"bad setup_done", "[app]\nsetup_done = yes\n", "[app] setup_done"},
		{"short name", "[player]\nname = ab\n", "[player] name"},
		{"name with a space", "[player]\nname = a b c\n", "[player] name"},
		{"server without port", "[server]\nserver = example.org\n", "[server] server"},
		{"server port 0", "[server]\nserver = example.org:0\n", "[server] server"},
		{"status url scheme", "[server]\nstatus_url = ftp://x/\n", "[server] status_url"},
		{"set twice", "[app]\nmode = play\nmode = host\n", "line 3: [app] mode is set twice"},
		{"key outside a section", "mode = play\n", "line 1: key \"mode\" is outside a section"},
		{"no equals sign", "[app]\nmode play\n", "line 2:"},
		{"broken header", "[app\n", "line 1: section header"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := Parse(strings.NewReader(c.in))
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("got %v, want an error containing %q", err, c.want)
			}
		})
	}
}

func TestParseAcceptsCRLFAndSpacing(t *testing.T) {
	f, err := Parse(strings.NewReader("[app]\r\n  mode=host  \r\n[ player ]\r\nname = dave\r\n"))
	if err != nil {
		t.Fatal(err)
	}
	if f.Mode != ModeHost || f.Name != "dave" {
		t.Fatalf("got %+v", f.Settings)
	}
}

func TestPathFor(t *testing.T) {
	env := func(m map[string]string) func(string) string { return func(k string) string { return m[k] } }
	cases := []struct {
		name, goos string
		env        map[string]string
		home, want string
	}{
		{"xdg", "linux", map[string]string{"XDG_CONFIG_HOME": "/x"}, "/h", filepath.Join("/x", "re-psn", FileName)},
		{"relative xdg is ignored", "linux", map[string]string{"XDG_CONFIG_HOME": "rel"}, "/h", filepath.Join("/h", ".config", "re-psn", FileName)},
		{"home", "darwin", nil, "/h", filepath.Join("/h", ".config", "re-psn", FileName)},
		{"windows", "windows", map[string]string{"AppData": `C:\Users\a\AppData\Roaming`, "XDG_CONFIG_HOME": "/x"}, "", filepath.Join(`C:\Users\a\AppData\Roaming`, "re-psn", FileName)},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := pathFor(c.goos, env(c.env), c.home)
			if err != nil || got != c.want {
				t.Fatalf("got %q, %v; want %q", got, err, c.want)
			}
		})
	}
	if _, err := pathFor("linux", env(nil), ""); err == nil {
		t.Fatal("no home and no XDG_CONFIG_HOME: want an error")
	}
	if _, err := pathFor("windows", env(nil), "/h"); err == nil {
		t.Fatal("windows without AppData: want an error")
	}
}

func TestSaveAndLoad(t *testing.T) {
	path := filepath.Join(t.TempDir(), "re-psn", FileName)
	if _, err := Load(path); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("missing file: %v, want ErrNotExist", err)
	}
	f, err := Parse(strings.NewReader(sample))
	if err != nil {
		t.Fatal(err)
	}
	f.Server = "127.0.0.1:36100"
	if err := Save(path, f); err != nil {
		t.Fatal(err)
	}
	if err := Save(path, f); err != nil { // over an existing file
		t.Fatal(err)
	}
	got, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if got.Settings != f.Settings {
		t.Fatalf("loaded %+v, saved %+v", got.Settings, f.Settings)
	}
	st, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" && st.Mode().Perm() != 0o600 {
		t.Fatalf("mode %v, want 0600", st.Mode().Perm())
	}
	if left, _ := filepath.Glob(filepath.Join(filepath.Dir(path), ".*")); len(left) != 0 {
		t.Fatalf("temporary files left: %v", left)
	}
}

func TestSaveRefusesInvalid(t *testing.T) {
	path := filepath.Join(t.TempDir(), FileName)
	err := Save(path, &File{Settings: Settings{Mode: "watch"}})
	if err == nil || !strings.Contains(err.Error(), "[app] mode") {
		t.Fatalf("got %v, want an error naming [app] mode", err)
	}
	if _, err := os.Stat(path); !errors.Is(err, fs.ErrNotExist) {
		t.Fatal("an invalid save wrote a file")
	}
}

func TestSetupDoneKeepsUnknownLines(t *testing.T) {
	in := "# mine\n[app]\nmode = play\nsetup_done = false\nfuture = 1\n\n[later]\nanything = goes here\n"
	f, err := Parse(strings.NewReader(in))
	if err != nil {
		t.Fatal(err)
	}
	if f.SetupDone != "false" {
		t.Fatalf("SetupDone = %q, want false", f.SetupDone)
	}
	f.SetupDone = "true"
	b, err := f.Bytes()
	if err != nil {
		t.Fatal(err)
	}
	want := "# mine\n[app]\nmode = play\nsetup_done = true\nfuture = 1\n\n[later]\nanything = goes here\n"
	if string(b) != want {
		t.Fatalf("got:\n%s\nwant:\n%s", b, want)
	}
}
