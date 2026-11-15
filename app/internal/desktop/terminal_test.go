package desktop

import (
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestLaunchSelection(t *testing.T) {
	self := filepath.Join(t.TempDir(), "app with spaces;$(literal)")
	for _, tc := range []struct {
		name, preferred, installed string
		args                       []string
	}{
		{"alternative", "", "x-terminal-emulator", []string{"-e", self, "tui"}},
		{"kitty", "", "kitty", []string{"--session=none", self, "tui"}},
		{"xterm", "", "xterm", []string{"-e", self, "tui"}},
		{"preferred", "my-terminal", "my-terminal", []string{"-e", self, "tui"}},
		{"preferred kitty", "kitty", "kitty", []string{"--session=none", self, "tui"}},
		{"literal path", filepath.Join(t.TempDir(), "terminal with spaces"), "", []string{"-e", self, "tui"}},
		{"not shell words", "missing --option", "xterm", []string{"-e", self, "tui"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			installed := tc.installed
			if installed == "" {
				installed = tc.preferred
			}
			environ := []string{"DISPLAY=:synthetic", "KEEP=unchanged", "TERMINAL=" + tc.preferred}
			before := append([]string(nil), environ...)
			var searched []string
			runs := 0
			err := launch(system{
				goos: "linux", environ: environ,
				executable: func() (string, error) { return self, nil },
				lookPath: func(name string) (string, error) {
					searched = append(searched, name)
					if name == installed {
						return filepath.Join("bin", "selected-terminal"), nil
					}
					return "", exec.ErrNotFound
				},
				run: func(path string, args, childEnv []string) error {
					runs++
					if path != filepath.Join("bin", "selected-terminal") || !reflect.DeepEqual(args, tc.args) {
						t.Fatalf("unexpected command: %q %q", path, args)
					}
					if !reflect.DeepEqual(childEnv[:len(environ)], environ) || env(childEnv, reexecGuard) != "1" {
						t.Fatal("child environment was not preserved with a recursion guard")
					}
					return nil
				},
			})
			if err != nil || runs != 1 || !reflect.DeepEqual(before, environ) {
				t.Fatalf("launch = %v, runs=%d, environment changed=%v", err, runs, !reflect.DeepEqual(before, environ))
			}
			if tc.preferred != "" && searched[0] != tc.preferred {
				t.Fatal("TERMINAL was split, expanded or not preferred")
			}
		})
	}
}

func TestLaunchRefusals(t *testing.T) {
	for _, tc := range []struct {
		name, goos string
		environ    []string
		want       string
	}{
		{"windows", "windows", []string{"DISPLAY=:1"}, "run re-psn-app in a terminal"},
		{"other", "darwin", []string{"DISPLAY=:1"}, "run re-psn-app in a terminal"},
		{"headless", "linux", nil, "no desktop display"},
		{"guard", "linux", []string{"DISPLAY=:1", reexecGuard + "=1"}, "did not provide a terminal"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := launch(system{goos: tc.goos, environ: tc.environ,
				executable: func() (string, error) { t.Fatal("looked up executable after refusal"); return "", nil },
				lookPath:   func(string) (string, error) { t.Fatal("searched after refusal"); return "", nil },
				run:        func(string, []string, []string) error { t.Fatal("started after refusal"); return nil },
			})
			if err == nil || !strings.Contains(err.Error(), tc.want) || strings.Contains(err.Error(), "\n") {
				t.Fatalf("unexpected refusal: %v", err)
			}
		})
	}
}

func TestLaunchFailuresDoNotLeakOrRetry(t *testing.T) {
	for _, mode := range []string{"self error", "relative self", "missing terminal", "terminal error"} {
		t.Run(mode, func(t *testing.T) {
			runs := 0
			err := launch(system{
				goos: "linux", environ: []string{"WAYLAND_DISPLAY=synthetic"},
				executable: func() (string, error) {
					if mode == "self error" {
						return "", errors.New("private-value\nand more")
					}
					if mode == "relative self" {
						return "relative-app", nil
					}
					return filepath.Join(t.TempDir(), "app"), nil
				},
				lookPath: func(name string) (string, error) {
					if mode == "missing terminal" {
						return "", errors.New("private-value")
					}
					return name, nil
				},
				run: func(string, []string, []string) error { runs++; return errors.New("private-value\nmore") },
			})
			if err == nil || strings.Contains(err.Error(), "private-value") || strings.Contains(err.Error(), "\n") {
				t.Fatalf("unsafe or missing error: %v", err)
			}
			want := 0
			if mode == "terminal error" {
				want = 1
			}
			if runs != want {
				t.Fatalf("started %d terminals, want %d", runs, want)
			}
		})
	}
}

// init provides a synthetic emulator and child before Go's test flag parser,
// so the real subprocess test can use exactly the terminal's -e argv contract.
// No real terminal, shell, display, server or browser is started by the test.
func init() {
	switch os.Getenv("RE_PSN_DESKTOP_TEST_HELPER") {
	case "terminal":
		if len(os.Args) != 4 || os.Args[1] != "-e" || os.Args[3] != "tui" {
			os.Exit(91)
		}
		cmd := exec.Command(os.Args[2], os.Args[3:]...)
		cmd.Env = append(os.Environ(), "RE_PSN_DESKTOP_TEST_HELPER=child")
		if err := cmd.Run(); err != nil {
			os.Exit(92)
		}
		os.Exit(0)
	case "child":
		err := launch(system{goos: "linux", environ: os.Environ()})
		if err == nil || !strings.Contains(err.Error(), "did not provide a terminal") {
			os.Exit(93)
		}
		data, _ := json.Marshal(struct {
			Args  []string
			Keep  string
			Guard string
		}{os.Args[1:], os.Getenv("RE_PSN_DESKTOP_TEST_KEEP"), os.Getenv(reexecGuard)})
		if os.WriteFile(os.Getenv("RE_PSN_DESKTOP_TEST_REPORT"), data, 0o600) != nil {
			os.Exit(94)
		}
		os.Exit(0)
	}
}

func TestLaunchSyntheticTerminalProcess(t *testing.T) {
	dir := t.TempDir()
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(self)
	if err != nil {
		t.Fatal(err)
	}
	// Shell punctuation must remain part of this executable's literal name.
	child := filepath.Join(dir, "app ;$(exit 9).exe")
	if err := os.WriteFile(child, data, 0o700); err != nil {
		t.Fatal(err)
	}
	report := filepath.Join(dir, "report.json")
	environ := []string{"DISPLAY=:synthetic", "RE_PSN_DESKTOP_TEST_HELPER=terminal",
		"RE_PSN_DESKTOP_TEST_REPORT=" + report, "RE_PSN_DESKTOP_TEST_KEEP=literal ; $(value)"}
	if err := launch(system{goos: "linux", environ: environ,
		executable: func() (string, error) { return child, nil },
		lookPath:   func(string) (string, error) { return self, nil }, run: runTerminal}); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(report)
	if err != nil {
		t.Fatal(err)
	}
	var got struct {
		Args  []string
		Keep  string
		Guard string
	}
	if json.Unmarshal(raw, &got) != nil || !reflect.DeepEqual(got.Args, []string{"tui"}) ||
		got.Keep != "literal ; $(value)" || got.Guard != "1" {
		t.Fatalf("unexpected child arguments/environment: %s", raw)
	}
}
