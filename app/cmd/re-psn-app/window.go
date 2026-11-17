package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"time"
)

// windowProgram returns the path of re-psn-window beside this program, or "". Tests replace it.
var windowProgram = findWindowProgram

// windowStartGrace is how long a window program that exits with an error counts as one that
// could not show a window (no web view installed, no display), after which the app falls back to the browser.
const windowStartGrace = 5 * time.Second

func findWindowProgram() string {
	exe, err := os.Executable()
	if err != nil {
		return ""
	}
	name := "re-psn-window"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	path := filepath.Join(filepath.Dir(exe), name)
	if st, err := os.Lstat(path); err != nil || !st.Mode().IsRegular() {
		return ""
	}
	return path
}

// delegateToWindow hands the launch over to re-psn-window when it is installed beside this program and the
// caller did not ask for a browser tab or a headless start. The window program starts this program again
// with --no-open, so it does not come back here. handled is false when the caller should open the browser as
// before: no window program, an explicit browser/headless request, or a window that failed at once.
func delegateToWindow(args []string, browser, noOpen bool, urlFile string, stdout, stderr io.Writer) (handled bool, code int) {
	if browser || noOpen || urlFile != "" {
		return false, 0
	}
	program := windowProgram()
	if program == "" {
		return false, 0
	}
	cmd := exec.Command(program, args...)
	cmd.Stdout, cmd.Stderr = stdout, stderr
	started := time.Now()
	err := cmd.Run()
	if err == nil {
		return true, 0
	}
	var exit *exec.ExitError
	if errors.As(err, &exit) && time.Since(started) >= windowStartGrace {
		return true, exit.ExitCode()
	}
	fmt.Fprintln(stderr, "re-psn-app: the window did not start; opening your browser instead")
	return false, 0
}
