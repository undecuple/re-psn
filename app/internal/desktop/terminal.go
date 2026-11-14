// Package desktop opens the terminal UI when a Linux desktop starts the app
// without a terminal. It neither starts a server nor opens a browser.
package desktop

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

// reexecGuard is internal process state, not a user setting. The second app
// refuses another relaunch if its terminal still has not supplied a TTY.
const reexecGuard = "RE_PSN_APP_TERMINAL_REEXEC"

type system struct {
	goos       string
	environ    []string
	executable func() (string, error)
	lookPath   func(string) (string, error)
	run        func(string, []string, []string) error
}

// Launch opens an installed terminal and waits for it. Windows console builds
// open a console through the OS; other platforms must already have a terminal.
// Command-line subcommands never call this function.
func Launch() error {
	return launch(system{runtime.GOOS, os.Environ(), os.Executable, exec.LookPath, runTerminal})
}

func launch(s system) error {
	if s.goos != "linux" {
		return errors.New("run re-psn-app in a terminal, or use re-psn-app check HOST:PORT")
	}
	if env(s.environ, reexecGuard) != "" {
		return errors.New("the opened terminal did not provide a terminal; run re-psn-app in a terminal")
	}
	if env(s.environ, "DISPLAY") == "" && env(s.environ, "WAYLAND_DISPLAY") == "" {
		return errors.New("no desktop display is available; run re-psn-app in a terminal, or use re-psn-app check HOST:PORT")
	}
	self, err := s.executable()
	if err != nil || !filepath.IsAbs(self) {
		return errors.New("cannot find this application executable; run re-psn-app in a terminal")
	}
	choices := []string{"x-terminal-emulator", "kitty", "xterm"}
	if preferred := env(s.environ, "TERMINAL"); preferred != "" {
		// One executable, including a path containing spaces. No splitting,
		// expansion or shell command is performed.
		choices = append([]string{preferred}, choices...)
	}
	seen := map[string]bool{}
	for _, name := range choices {
		if seen[name] {
			continue
		}
		seen[name] = true
		terminal, err := s.lookPath(name)
		if err != nil {
			continue
		}
		// kitty takes the program after its options; the other terminals use
		// the xterm-compatible -e convention. The explicit tui argument also
		// avoids xterm's single-argument shell fallback.
		args := []string{"-e", self, "tui"}
		if filepath.Base(name) == "kitty" {
			// A configured startup session would otherwise ignore the app.
			args = []string{"--session=none", self, "tui"}
		}
		childEnv := append([]string(nil), s.environ...)
		childEnv = append(childEnv, reexecGuard+"=1")
		if err := s.run(terminal, args, childEnv); err != nil {
			// Do not start another terminal after an emulator has run: an
			// error may come from its child, and another would duplicate it.
			return errors.New("the terminal could not run the application; run re-psn-app in a terminal")
		}
		return nil
	}
	return errors.New("no supported terminal was found; install xterm or kitty, or set TERMINAL to an executable")
}

func env(environ []string, name string) string {
	for i := len(environ) - 1; i >= 0; i-- {
		if value, ok := strings.CutPrefix(environ[i], name+"="); ok {
			return value
		}
	}
	return ""
}

func runTerminal(path string, args, environ []string) error {
	cmd := exec.Command(path, args...)
	cmd.Env = environ
	return cmd.Run()
}
