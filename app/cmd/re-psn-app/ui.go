package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/undecuple/re-psn/app/internal/appinstance"
	"github.com/undecuple/re-psn/app/internal/config"
	"github.com/undecuple/re-psn/app/internal/desktop"
	"github.com/undecuple/re-psn/app/internal/host"
	"github.com/undecuple/re-psn/app/internal/webui"
)

var startUI = runUI

// uiStdin is what --exit-with-parent watches; tests replace it.
var uiStdin io.Reader = os.Stdin

// cancelWhenClosed calls cancel once r reports end of input or an error. A
// parent that owns the other end of the pipe closes it to ask for a clean stop,
// and the system closes it if that parent dies, so no signal is needed (Windows
// has none that stops a console-less process cleanly).
func cancelWhenClosed(r io.Reader, cancel func()) {
	go func() {
		_, _ = io.Copy(io.Discard, r)
		cancel()
	}()
}

func runUI(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("ui", flag.ContinueOnError)
	fs.SetOutput(stderr)
	configPath := fs.String("config", "", "per-user app settings file")
	profile := fs.String("profile-dir", "", "private local profile directory")
	binary := fs.String("server-binary", "", "trusted local server component (default beside app)")
	noOpen := fs.Bool("no-open", false, "serve without opening a browser; requires --url-file")
	exitWithParent := fs.Bool("exit-with-parent", false, "stop cleanly when standard input closes; for a window shell that owns this process")
	urlFile := fs.String("url-file", "", "new private file for the one-use launch link")
	if fs.Parse(args) != nil || fs.NArg() != 0 {
		return 2
	}
	if *noOpen && *urlFile == "" {
		fmt.Fprintln(stderr, "re-psn-app: --no-open requires a private --url-file")
		return 2
	}
	var err error
	if *configPath == "" {
		*configPath, err = config.Path()
		if err != nil {
			fmt.Fprintln(stderr, "re-psn-app: settings location unavailable")
			return 1
		}
	}
	if *profile == "" {
		*profile = filepath.Join(filepath.Dir(*configPath), "profile")
	}
	for _, path := range []*string{configPath, profile, binary} {
		if *path != "" {
			*path, err = filepath.Abs(*path)
			if err != nil {
				fmt.Fprintln(stderr, "re-psn-app: invalid local path")
				return 2
			}
			*path = appinstance.Canonical(*path)
		}
	}
	instance, owner, err := appinstance.Acquire(*configPath)
	if err != nil {
		fmt.Fprintln(stderr, "re-psn-app:", err)
		return 1
	}
	defer instance.Close()
	if !owner {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		link, err := instance.Reopen(ctx, *profile, *binary)
		if err == nil && *urlFile != "" {
			_, err = createPrivate(*urlFile, func(w io.Writer) error { _, e := io.WriteString(w, link+"\n"); return e })
		}
		if err == nil && !*noOpen {
			err = desktop.OpenBrowser(link)
		}
		if err != nil {
			fmt.Fprintln(stderr, "re-psn-app:", err)
			return 1
		}
		fmt.Fprintln(stdout, "Opened the existing app; its owned server keeps running")
		return 0
	}
	s, err := webui.New(webui.Options{ConfigPath: *configPath, ProfileDir: *profile, ServerBinary: *binary, Version: version})
	if err != nil {
		fmt.Fprintln(stderr, "re-psn-app:", err)
		return 1
	}
	defer s.Close()
	ln, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		fmt.Fprintln(stderr, "re-psn-app: private UI port unavailable")
		return 1
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	if *exitWithParent {
		cancelWhenClosed(uiStdin, cancel)
	}
	cleanup := func() {}
	defer func() { cleanup() }()
	err = s.Serve(ctx, ln, func(link string) error {
		if err := instance.Publish(s.Locator()); err != nil {
			return errors.New("private app reopen information could not be saved")
		}
		if *urlFile != "" {
			var e error
			remove, e := createPrivate(*urlFile, func(w io.Writer) error { _, e := io.WriteString(w, link+"\n"); return e })
			if e != nil {
				return e
			}
			cleanup = remove
		}
		fmt.Fprintln(stdout, "re-psn app UI ready at http://"+ln.Addr().String()+" (private launch link)")
		if !*noOpen {
			return desktop.OpenBrowser(link)
		}
		return nil
	})
	if err != nil {
		fmt.Fprintln(stderr, "re-psn-app:", err)
		return 1
	}
	return 0
}
func runHost(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("host", flag.ContinueOnError)
	fs.SetOutput(stderr)
	address := fs.String("listen", "127.0.0.1:36100", "literal game TCP/UDP bind address")
	relay := fs.Bool("relay", false, "enable game relay for the owned server")
	binary := fs.String("server-binary", "", "trusted local server component")
	if len(args) > 0 && args[0] == "serve" {
		args = args[1:]
	}
	if fs.Parse(args) != nil || fs.NArg() != 0 {
		return 2
	}
	m := host.New(*binary)
	defer m.Close()
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	if err := m.Start(ctx, *address, *relay); err != nil {
		fmt.Fprintln(stderr, "re-psn-app:", err)
		return 1
	}
	fmt.Fprintln(stdout, "Hosting game TCP/UDP on", m.View().Address, "until this app stops")
	<-ctx.Done()
	if err := m.Close(); err != nil && !errors.Is(err, context.Canceled) {
		fmt.Fprintln(stderr, "re-psn-app: host shutdown failed")
		return 1
	}
	return 0
}
