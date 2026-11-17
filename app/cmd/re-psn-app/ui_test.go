package main

import (
	"bytes"
	"github.com/undecuple/re-psn/app/internal/appinstance"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestExistingLaunchFileFailsCleanlyAndReleasesInstance(t *testing.T) {
	root := t.TempDir()
	config := filepath.Join(root, "private", "re-psn.ini")
	link := filepath.Join(root, "existing.url")
	const sentinel = "existing caller data"
	if err := os.WriteFile(link, []byte(sentinel), 0600); err != nil {
		t.Fatal(err)
	}
	var out, errs bytes.Buffer
	if code := runUI([]string{"--config", config, "--no-open", "--url-file", link}, &out, &errs); code != 1 {
		t.Fatal("expected controlled launch failure", code)
	}
	b, err := os.ReadFile(link)
	if err != nil || string(b) != sentinel {
		t.Fatal("failed launch removed or changed caller file", err)
	}
	lock, owner, err := appinstance.Acquire(config)
	if err != nil || !owner {
		t.Fatal("failed launch leaked instance ownership", err)
	}
	lock.Close()
}
func TestHeadlessUIDoesNotPrintLaunchSecret(t *testing.T) {
	var out, errs bytes.Buffer
	if code := runUI([]string{"--no-open"}, &out, &errs); code != 2 || out.Len() != 0 {
		t.Fatal("headless UI needs explicit private file", code, out.Len())
	}
}

func TestCancelWhenClosedWaitsForEndOfInput(t *testing.T) {
	pr, pw := io.Pipe()
	stopped := make(chan struct{})
	cancelWhenClosed(pr, func() { close(stopped) })
	if _, err := pw.Write([]byte("ignored input")); err != nil {
		t.Fatal(err)
	}
	select {
	case <-stopped:
		t.Fatal("stopped while the parent was still connected")
	case <-time.After(200 * time.Millisecond):
	}
	pw.Close()
	select {
	case <-stopped:
	case <-time.After(3 * time.Second):
		t.Fatal("did not stop when the parent pipe closed")
	}
}

func TestUIExitsCleanlyWhenParentPipeCloses(t *testing.T) {
	old := uiStdin
	defer func() { uiStdin = old }()
	pr, pw := io.Pipe()
	uiStdin = pr
	root := t.TempDir()
	cfg := filepath.Join(root, "private", "re-psn.ini")
	if err := os.MkdirAll(filepath.Dir(cfg), 0700); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "link.txt")
	var out, errs bytes.Buffer
	done := make(chan int, 1)
	go func() {
		done <- runUI([]string{"--config", cfg, "--no-open", "--exit-with-parent", "--url-file", link}, &out, &errs)
	}()
	deadline := time.Now().Add(5 * time.Second)
	for {
		if b, err := os.ReadFile(link); err == nil && strings.HasSuffix(string(b), "\n") {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("app did not publish its launch link", errs.String())
		}
		time.Sleep(20 * time.Millisecond)
	}
	select {
	case code := <-done:
		t.Fatal("exited before the parent closed", code)
	case <-time.After(200 * time.Millisecond):
	}
	pw.Close()
	select {
	case code := <-done:
		if code != 0 {
			t.Fatal("unclean exit", code, errs.String())
		}
	case <-time.After(10 * time.Second):
		t.Fatal("app kept running after the parent pipe closed")
	}
	if _, err := os.Stat(link); !os.IsNotExist(err) {
		t.Fatal("link file left behind", err)
	}
}

func fakeWindow(t *testing.T, body string) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("shell script stand-in for the window program")
	}
	path := filepath.Join(t.TempDir(), "re-psn-window")
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"+body+"\n"), 0700); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestDelegateToWindowPassesFlagsAndExitCode(t *testing.T) {
	old := windowProgram
	defer func() { windowProgram = old }()
	rec := filepath.Join(t.TempDir(), "args.txt")
	prog := fakeWindow(t, `printf '%s\n' "$@" > '`+rec+`'; echo shown; exit 0`)
	windowProgram = func() string { return prog }
	var out, errs bytes.Buffer
	handled, code := delegateToWindow([]string{"--config", "/x/re-psn.ini", "--profile-dir", "/x/p"}, false, false, "", &out, &errs)
	if !handled || code != 0 || !strings.Contains(out.String(), "shown") {
		t.Fatal("window not used", handled, code, out.String(), errs.String())
	}
	b, err := os.ReadFile(rec)
	if err != nil || string(b) != "--config\n/x/re-psn.ini\n--profile-dir\n/x/p\n" {
		t.Fatal("flags not passed through", string(b), err)
	}
}

func TestDelegateToWindowSkippedForBrowserHeadlessOrMissing(t *testing.T) {
	old := windowProgram
	defer func() { windowProgram = old }()
	windowProgram = func() string {
		t.Fatal("looked for a window program although it was not wanted")
		return ""
	}
	for _, c := range []struct {
		browser, noOpen bool
		urlFile         string
	}{{true, false, ""}, {false, true, ""}, {false, false, "/x/link"}} {
		if handled, _ := delegateToWindow(nil, c.browser, c.noOpen, c.urlFile, io.Discard, io.Discard); handled {
			t.Fatal("delegated although the caller asked otherwise", c)
		}
	}
	windowProgram = func() string { return "" }
	if handled, _ := delegateToWindow(nil, false, false, "", io.Discard, io.Discard); handled {
		t.Fatal("delegated with no window program installed")
	}
}

func TestDelegateToWindowFallsBackWhenItFailsAtOnce(t *testing.T) {
	old := windowProgram
	defer func() { windowProgram = old }()
	windowProgram = func() string { return fakeWindow(t, `echo "no web view" >&2; exit 127`) }
	var errs bytes.Buffer
	handled, _ := delegateToWindow(nil, false, false, "", io.Discard, &errs)
	if handled || !strings.Contains(errs.String(), "opening your browser instead") {
		t.Fatal("a window that fails at once must fall back to the browser", handled, errs.String())
	}
}

func TestFindWindowProgramRequiresARegularFileBesideTheProgram(t *testing.T) {
	// The test program's folder holds no re-psn-window.
	if p := findWindowProgram(); p != "" {
		t.Fatal("found a window program in the test folder", p)
	}
}
