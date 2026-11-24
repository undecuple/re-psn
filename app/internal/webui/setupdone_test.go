package webui

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/undecuple/re-psn/app/internal/config"
)

func TestSetupDone(t *testing.T) {
	cases := []struct {
		name string
		set  config.Settings
		want bool
	}{
		{"nothing saved", config.Settings{}, true},
		{"settings file from before the checklist", config.Settings{Mode: config.ModePlay, Name: "alice"}, true},
		{"role and default address, no key", config.Settings{Mode: config.ModeHost, HostAddress: "127.0.0.1:36100"}, true},
		{"stored true wins", config.Settings{SetupDone: "true"}, true},
		{"stored false wins over a server", config.Settings{SetupDone: "false", Server: "play.example.org:36100"}, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := setupDone(c.set); got != c.want {
				t.Fatalf("setupDone = %v, want %v", got, c.want)
			}
		})
	}
}

func TestUISetupDoneRoundTrip(t *testing.T) {
	f := serveUI(t)
	ini := filepath.Join(f.dir, "re-psn.ini")
	read := func() State {
		var st State
		if err := json.Unmarshal(callUI(t, f, f.token, "/api/state", nil, 200), &st); err != nil {
			t.Fatal(err)
		}
		return st
	}
	if !read().Settings.SetupDone {
		t.Fatal("a settings file with no setup_done key must count as done")
	}
	saveRole(t, f, "host", map[string]any{"setup_done": false})
	if read().Settings.SetupDone {
		t.Fatal("the first workspace choice stores false and starts the checklist")
	}
	saveRole(t, f, "host", map[string]any{"setup_done": true})
	if !read().Settings.SetupDone {
		t.Fatal("setup_done true was not kept")
	}
	b, err := os.ReadFile(ini)
	if err != nil || !strings.Contains(string(b), "setup_done = true") {
		t.Fatalf("setup_done not written: %v %s", err, b)
	}
	saveRole(t, f, "host", map[string]any{"setup_done": false})
	if read().Settings.SetupDone {
		t.Fatal("stored false did not win")
	}
}

func TestSetupDoneDefaultForExistingUser(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "private")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	// An older settings file has no setup_done key: the checklist is not shown, with or without a saved server.
	ini := filepath.Join(dir, "re-psn.ini")
	if err := os.WriteFile(ini, []byte("[app]\nmode = play\n\n[server]\nserver = 127.0.0.1:36300\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	s, err := New(Options{ConfigPath: ini, ProfileDir: filepath.Join(dir, "profile"), ServerBinary: filepath.Join(dir, "missing-server"), Version: "synthetic"})
	if err != nil {
		t.Fatal(err)
	}
	if !s.settings().SetupDone {
		t.Fatal("an existing user is shown the checklist")
	}
}
