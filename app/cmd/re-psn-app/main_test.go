package main

import (
	"bytes"
	"errors"
	"io"
	"net"
	"strings"
	"testing"
)

func restoreStartupHooks(t *testing.T) {
	t.Helper()
	tty, tui, desktop, ui := isTerminal, startTUI, launchDesktop, startUI
	t.Cleanup(func() { isTerminal, startTUI, launchDesktop, startUI = tty, tui, desktop, ui })
	launchDesktop = func() error { return errors.New("run in a terminal") }
	startUI = func([]string, io.Writer, io.Writer) int { return 2 }
}
func TestRun(t *testing.T) {
	restoreStartupHooks(t)
	isTerminal = func() bool { return false }
	startTUI = func(io.Writer) int { t.Fatal("CLI unexpectedly opened TUI"); return 0 }
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	closed := ln.Addr().String()
	ln.Close()
	for _, c := range []struct {
		args []string
		code int
		out  string
	}{
		{[]string{"version"}, 0, "re-psn-app " + version + "\n"}, {nil, 2, ""}, {[]string{"--no-tui"}, 2, ""}, {[]string{"unknown"}, 2, ""}, {[]string{"check"}, 2, ""}, {[]string{"check", "-timeout", "2s", closed}, 1, "FAIL dial " + closed},
	} {
		var out, errs bytes.Buffer
		if code := run(c.args, &out, &errs); code != c.code || !strings.HasPrefix(out.String(), c.out) {
			t.Fatalf("run%q code%d stdout%q stderr%q", c.args, code, out.String(), errs.String())
		}
	}
}
func TestExplicitTUIRequiresTerminal(t *testing.T) {
	restoreStartupHooks(t)
	calls := 0
	startTUI = func(io.Writer) int { calls++; return 0 }
	for _, tty := range []bool{false, true} {
		isTerminal = func() bool { return tty }
		var out, errs bytes.Buffer
		code := run([]string{"tui"}, &out, &errs)
		if tty && (code != 0 || calls != 1) || !tty && (code != 2 || calls != 0 || !strings.Contains(errs.String(), "not a terminal")) {
			t.Fatalf("explicit TUI tty%t code%d calls%d %s", tty, code, calls, errs.String())
		}
	}
}
func TestRunDesktopStartup(t *testing.T) {
	restoreStartupHooks(t)
	startTUI = func(io.Writer) int { t.Fatal("desktop default opened terminal"); return 0 }
	launchDesktop = func() error { t.Fatal("desktop default relaunched a terminal"); return nil }
	for _, tty := range []bool{false, true} {
		isTerminal = func() bool { return tty }
		for _, args := range [][]string{nil, {"--"}, {"ui"}} {
			calls := 0
			startUI = func(got []string, _ io.Writer, _ io.Writer) int {
				calls++
				if len(got) != 0 {
					t.Fatal(got)
				}
				return 0
			}
			if run(args, io.Discard, io.Discard) != 0 || calls != 1 {
				t.Fatalf("GUI default did not run once tty%t args%q", tty, args)
			}
		}
	}
	startUI = func([]string, io.Writer, io.Writer) int { t.Fatal("noninteractive command opened UI"); return 0 }
	for _, args := range [][]string{{"version"}, {"--help"}, {"--no-tui"}, {"unknown"}, {"check"}} {
		run(args, io.Discard, io.Discard)
	}
}
