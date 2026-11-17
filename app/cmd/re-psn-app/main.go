// re-psn-app: the desktop app for players and hosts of a re-psn server. With
// no arguments it opens a private local browser interface. The advanced terminal
// interface is available through tui; check and version also remain available.
package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/term"

	"github.com/undecuple/re-psn/app/internal/client"
	"github.com/undecuple/re-psn/app/internal/config"
	"github.com/undecuple/re-psn/app/internal/desktop"
	"github.com/undecuple/re-psn/app/internal/host"
	"github.com/undecuple/re-psn/app/internal/tui"
)

var version = "0.7.0"

// checkTitle is the communication ID check uses: no real title has it, so the
// check never sees or touches a game's rooms.
const checkTitle = "RE-PSN-CHECK"

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

// isTerminal reports whether stdin and stdout are both a terminal; tests replace it.
var isTerminal = func() bool { return term.IsTerminal(os.Stdin.Fd()) && term.IsTerminal(os.Stdout.Fd()) }

// startTUI runs the terminal UI; tests replace it.
var startTUI = runTUI

// launchDesktop opens a Linux desktop terminal; tests replace it.
var launchDesktop = desktop.Launch

func run(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 || len(args) == 1 && args[0] == "--" {
		return startUI(nil, stdout, stderr)
	}
	if len(args) == 1 && args[0] == "tui" {
		if !isTerminal() {
			if err := launchDesktop(); err != nil {
				fmt.Fprintln(stderr, "re-psn-app: not a terminal:", err)
				return 2
			}
			return 0
		}
		return startTUI(stderr)
	}
	switch args[0] {
	case "ui":
		return startUI(args[1:], stdout, stderr)
	case "host":
		return runHost(args[1:], stdout, stderr)
	case "profile", "trophies", "friends", "account-relay":
		return runFramework(args, stdout, stderr)
	case "--profile-dir":
		return runProfileTUI(args, stderr)
	case "--no-tui", "-no-tui":
		fmt.Fprintln(stderr, "re-psn-app: the terminal UI is off (--no-tui); use re-psn-app check HOST:PORT or re-psn-app version")
		return 2
	case "version":
		fmt.Fprintln(stdout, "re-psn-app", version)
		return 0
	case "check":
		return check(args[1:], stdout, stderr)
	case "help", "-h", "-help", "--help":
		usage(stdout)
		return 0
	}
	fmt.Fprintf(stderr, "re-psn-app: unknown command %q\n", args[0])
	usage(stderr)
	return 2
}

func usage(w io.Writer) {
	fmt.Fprint(w, `usage:
  re-psn-app [ui] [--browser]                open the app: in re-psn-window when it is beside this program, else in your browser
  re-psn-app tui                             open the advanced terminal interface
  re-psn-app ui --no-open --url-file FILE     write a private one-use launch link
  re-psn-app host serve [--listen IP:PORT]     explicitly run the owned server component
  re-psn-app --no-tui                        do not start it: print a note and exit 2
  re-psn-app check [-timeout 5s] HOST:PORT   connect, say HELLO and report what the server answers
  re-psn-app version                         print the version
  re-psn-app --profile-dir DIR               open local Profile, Trophies and Friends screens
  re-psn-app profile create|show|export|restore|serve --dir DIR [OPTIONS]
  re-psn-app trophies install|show --dir DIR [--input CATALOG]
  re-psn-app friends register|list|request|accept|decline|cancel|remove|block|unblock --dir DIR --relay HTTPS_ORIGIN [--ca-file FILE] [--id PUBLIC_ID]
  re-psn-app account-relay serve --state DIR --allow PUBLIC_IDS --tls-cert FILE --tls-key FILE [--listen HOST:PORT]
  re-psn-app account-relay revoke --state DIR --id PUBLIC_ID  (stop the companion first)
`)
}

// runTUI loads the settings file (none yet means the first-run chooser) and runs the UI until it quits.
func runTUI(stderr io.Writer) int {
	path, err := config.Path()
	if err != nil {
		fmt.Fprintln(stderr, "re-psn-app:", err)
		return 1
	}
	f, err := config.Load(path)
	first := errors.Is(err, os.ErrNotExist)
	if err != nil && !first {
		fmt.Fprintln(stderr, "re-psn-app:", err)
		return 1
	}
	_, noColor := os.LookupEnv("NO_COLOR")
	profileDir, _ := defaultProfileDir()
	session := tui.NewProfileSession(profileDir)
	defer session.Close()
	manager := host.New("")
	defer manager.Close()
	m := tui.New(tui.Options{Path: path, File: f, FirstRun: first, NoColor: noColor, ProfileDir: profileDir, ProfileSession: session, Host: manager, SocialOrigin: f.SocialOrigin, SocialCAFile: f.SocialCAFile})
	if _, err := tea.NewProgram(m).Run(); err != nil {
		fmt.Fprintln(stderr, "re-psn-app:", err)
		return 1
	}
	return 0
}

func check(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("check", flag.ContinueOnError)
	fs.SetOutput(stderr)
	timeout := fs.Duration("timeout", 5*time.Second, "give up after this long")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() != 1 {
		fmt.Fprintln(stderr, "re-psn-app check: want one HOST:PORT")
		return 2
	}
	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()
	if err := checkServer(ctx, fs.Arg(0), stdout); err != nil {
		fmt.Fprintln(stdout, "FAIL", err)
		return 1
	}
	fmt.Fprintln(stdout, "OK")
	return 0
}

// checkServer dials addr, says HELLO under a throwaway name and times one
// more request. It uses the game protocol only, never the HTTP status page.
func checkServer(ctx context.Context, addr string, out io.Writer) error {
	start := time.Now()
	c, err := client.Dial(ctx, addr)
	if err != nil {
		return err
	}
	defer c.Close()
	fmt.Fprintf(out, "connect   %s in %s\n", addr, ms(time.Since(start)))

	name, err := throwawayName()
	if err != nil {
		return err
	}
	start = time.Now()
	ack, err := c.Hello(ctx, client.Hello{CommID: checkTitle, OnlineID: name})
	if err != nil {
		return err
	}
	fmt.Fprintf(out, "hello     accepted as %s in %s; the server sees this machine as %s\n", name, ms(time.Since(start)), ack.PublicIP)
	fmt.Fprintf(out, "features  relay %s, invite-only rooms %s (flags 0x%02X)\n", onOff(ack.Relay()), onOff(ack.RoomCodes()), ack.Flags)
	fmt.Fprintln(out, "version   not reported: the protocol has no version message")

	start = time.Now()
	if _, err := c.SearchRooms(ctx, 0, 1); err != nil {
		return err
	}
	fmt.Fprintf(out, "rtt       %s (one SEARCH_ROOMS request)\n", ms(time.Since(start)))
	if err := c.Close(); err != nil && !errors.Is(err, client.ErrClosed) {
		return fmt.Errorf("close: %w", err)
	}
	return nil
}

// throwawayName is "check-" and 10 random hex digits: 16 bytes, the online ID limit.
func throwawayName() (string, error) {
	var b [5]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("random name: %w", err)
	}
	return "check-" + hex.EncodeToString(b[:]), nil
}

func ms(d time.Duration) string { return fmt.Sprintf("%.1f ms", float64(d.Microseconds())/1000) }

func onOff(b bool) string {
	if b {
		return "on"
	}
	return "off"
}
