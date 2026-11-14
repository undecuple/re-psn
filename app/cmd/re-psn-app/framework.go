package main

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"time"

	"github.com/undecuple/re-psn/app/framework"
	"github.com/undecuple/re-psn/app/internal/config"
	"github.com/undecuple/re-psn/app/internal/tui"

	tea "charm.land/bubbletea/v2"
)

func defaultProfileDir() (string, error) {
	p, err := config.Path()
	return filepath.Join(filepath.Dir(p), "profile"), err
}

// readRegular bounds input before parsing or key derivation. A passphrase file
// is an explicit local input, never a command-line secret or a shared INI key.
func readRegular(path string, limit int64, secret bool) ([]byte, error) {
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Size() > limit {
		return nil, errors.New("input must be a bounded regular file")
	}
	if secret && runtime.GOOS != "windows" && info.Mode().Perm()&0077 != 0 {
		return nil, errors.New("secret input must be private to its owner")
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, errors.New("input unavailable")
	}
	defer f.Close()
	b, err := io.ReadAll(io.LimitReader(f, limit+1))
	if err != nil || int64(len(b)) > limit {
		return nil, errors.New("input exceeds its limit")
	}
	return b, nil
}

func readPassphrase(path string) ([]byte, error) {
	b, err := readRegular(path, 1026, true)
	if err != nil {
		return nil, err
	}
	b = bytes.TrimSuffix(b, []byte("\n"))
	b = bytes.TrimSuffix(b, []byte("\r"))
	if len(b) < 12 || len(b) > 1024 {
		clear(b)
		return nil, errors.New("passphrase must contain 12 to 1024 bytes")
	}
	return b, nil
}

// createPrivate never replaces an existing user file. The descriptor cleanup
// checks file identity so it does not remove another broker's later file.
func createPrivate(path string, write func(io.Writer) error) (func(), error) {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return nil, errors.New("output exists or cannot be created")
	}
	info, statErr := f.Stat()
	cleanup := func() {
		if current, e := os.Lstat(path); e == nil && info != nil && os.SameFile(info, current) {
			_ = os.Remove(path)
		}
	}
	if statErr == nil {
		err = write(f)
	} else {
		err = statErr
	}
	if err == nil {
		err = f.Sync()
	}
	if e := f.Close(); err == nil {
		err = e
	}
	if err != nil {
		cleanup()
		return nil, err
	}
	return cleanup, nil
}

func runFramework(args []string, stdout, stderr io.Writer) int {
	if len(args) < 2 {
		fmt.Fprintln(stderr, "profile/trophies/friends/account-relay: an action is required; see help")
		return 2
	}
	group, action := args[0], args[1]
	fs := flag.NewFlagSet(group+" "+action, flag.ContinueOnError)
	fs.SetOutput(stderr)
	dir := fs.String("dir", "", "private profile directory")
	name := fs.String("name", "", "profile display label")
	in := fs.String("input", "", "catalog or encrypted backup file")
	out := fs.String("output", "", "new encrypted backup file")
	passwordFile := fs.String("passphrase-file", "", "private passphrase file, not the passphrase")
	title := fs.String("title", "", "approved trophy catalog title ID")
	grantFile := fs.String("grant-file", "", "new private game grant descriptor")
	relay := fs.String("relay", "", "explicit HTTPS social companion origin")
	caFile := fs.String("ca-file", "", "optional explicit PEM CA file")
	peer := fs.String("id", "", "peer or revoked account ID")
	stateDir := fs.String("state", "", "private companion state directory")
	allow := fs.String("allow", "", "comma-separated allowed public account IDs")
	listen := fs.String("listen", "127.0.0.1:0", "social companion listen address")
	certFile := fs.String("tls-cert", "", "server certificate PEM file")
	keyFile := fs.String("tls-key", "", "server private-key PEM file")
	if err := fs.Parse(args[2:]); err != nil || fs.NArg() != 0 {
		return 2
	}
	fail := func(err error) int { fmt.Fprintln(stderr, "re-psn-app:", err); return 1 }
	printJSON := func(v any) int {
		if err := json.NewEncoder(stdout).Encode(v); err != nil {
			return fail(err)
		}
		return 0
	}
	if group == "account-relay" {
		if *stateDir == "" || (action != "serve" && action != "revoke") {
			return fail(errors.New("account-relay requires serve/revoke and --state"))
		}
		var ids []string
		if *allow != "" {
			ids = strings.Split(*allow, ",")
		}
		s, err := framework.OpenSocialService(framework.SocialConfig{StateDir: *stateDir, AllowedIDs: ids})
		if err != nil {
			return fail(err)
		}
		defer s.Close()
		if action == "revoke" {
			if err := s.RevokeAccount(*peer); err != nil {
				return fail(err)
			}
			fmt.Fprintln(stdout, "Account revoked; friendships and pending requests removed.")
			return 0
		}
		certificate, err := tls.LoadX509KeyPair(*certFile, *keyFile)
		if err != nil {
			return fail(errors.New("server TLS certificate/key unavailable"))
		}
		ln, err := net.Listen("tcp", *listen)
		if err != nil {
			return fail(errors.New("social listener unavailable"))
		}
		defer ln.Close()
		ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		defer cancel()
		done := make(chan error, 1)
		go func() { done <- s.ServeTLS(ln, certificate) }()
		fmt.Fprintln(stdout, "Authenticated social companion ready on", ln.Addr())
		select {
		case <-ctx.Done():
			return failIfClose(s.Close(), stderr)
		case err := <-done:
			if err != nil {
				return fail(err)
			}
			return 0
		}
	}
	if *dir == "" {
		var err error
		*dir, err = defaultProfileDir()
		if err != nil {
			return fail(err)
		}
	}
	if group == "profile" && action == "show" {
		v, err := framework.Read(*dir)
		if err != nil {
			return fail(err)
		}
		return printJSON(v)
	}
	if group == "profile" && action == "create" {
		s, err := framework.Create(*dir, *name)
		if err != nil {
			return fail(err)
		}
		defer s.Close()
		return printJSON(s.View())
	}
	if group == "profile" && action == "restore" {
		password, err := readPassphrase(*passwordFile)
		if err != nil {
			return fail(err)
		}
		defer clear(password)
		b, err := readRegular(*in, framework.MaxStateBytes+1024, false)
		if err != nil {
			return fail(err)
		}
		s, err := framework.Restore(*dir, bytes.NewReader(b), password)
		if err != nil {
			return fail(err)
		}
		defer s.Close()
		return printJSON(s.View())
	}
	if group == "trophies" && action == "show" {
		v, err := framework.Read(*dir)
		if err != nil {
			return fail(err)
		}
		return printJSON(v.Titles)
	}
	s, err := framework.Open(*dir)
	if err != nil {
		return fail(err)
	}
	defer s.Close()
	switch group {
	case "profile":
		switch action {
		case "export":
			password, err := readPassphrase(*passwordFile)
			if err != nil {
				return fail(err)
			}
			defer clear(password)
			_, err = createPrivate(*out, func(w io.Writer) error { return s.Export(w, password) })
			if err != nil {
				return fail(err)
			}
			fmt.Fprintln(stdout, "Encrypted profile backup saved. Keep its passphrase separately.")
			return 0
		case "serve":
			b := framework.NewBroker(s)
			defer b.Close()
			grant, err := b.Grant(*title)
			if err != nil {
				return fail(err)
			}
			ln, err := net.Listen("tcp4", "127.0.0.1:0")
			if err != nil {
				return fail(err)
			}
			defer ln.Close()
			cleanup, err := createPrivate(*grantFile, func(w io.Writer) error {
				_, err := fmt.Fprintf(w, "re-psn-app-grant-v1\nport=%d\ntoken=%s\n", ln.Addr().(*net.TCPAddr).Port, hex.EncodeToString(grant.Token[:]))
				return err
			})
			if err != nil {
				return fail(err)
			}
			defer cleanup()
			ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
			defer cancel()
			done := make(chan error, 1)
			go func() { done <- b.Serve(ln) }()
			fmt.Fprintln(stdout, "Local trophy service ready; game grant file written. No relay connection was opened.")
			ticker := time.NewTicker(5 * time.Minute)
			defer ticker.Stop()
			for {
				select {
				case <-ctx.Done():
					return failIfClose(b.Close(), stderr)
				case err := <-done:
					if err != nil {
						return fail(err)
					}
					return 0
				case <-ticker.C:
					if err := b.Renew(grant.Token); err != nil {
						return fail(errors.New("game grant expired; restart the service and reconcile trophy state"))
					}
				}
			}
		}
	case "trophies":
		if action == "install" {
			b, err := readRegular(*in, framework.MaxStateBytes, false)
			if err != nil {
				return fail(err)
			}
			catalog, err := framework.DecodeCatalog(bytes.NewReader(b))
			if err != nil {
				return fail(err)
			}
			if err := s.InstallCatalog(catalog); err != nil {
				return fail(err)
			}
			fmt.Fprintln(stdout, "Local trophy catalog approved.")
			return 0
		}
	case "friends":
		var roots *x509.CertPool
		if *caFile != "" {
			b, err := readRegular(*caFile, 1<<20, false)
			if err != nil {
				return fail(err)
			}
			roots = x509.NewCertPool()
			if !roots.AppendCertsFromPEM(b) {
				return fail(errors.New("invalid CA file"))
			}
		}
		c, err := framework.NewSocialClient(s, framework.SocialClientConfig{Origin: *relay, Roots: roots})
		if err != nil {
			return fail(err)
		}
		defer c.Close()
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		var v framework.SocialState
		switch action {
		case "register":
			v, err = c.Register(ctx, s.View().Label)
		case "list":
			v, err = c.State(ctx)
		case "request":
			v, err = c.Request(ctx, *peer)
		case "accept":
			v, err = c.Accept(ctx, *peer)
		case "decline":
			v, err = c.Decline(ctx, *peer)
		case "cancel":
			v, err = c.Cancel(ctx, *peer)
		case "remove":
			v, err = c.Remove(ctx, *peer)
		case "block":
			v, err = c.Block(ctx, *peer)
		case "unblock":
			v, err = c.Unblock(ctx, *peer)
		default:
			return fail(errors.New("unknown friends action"))
		}
		if err != nil {
			return fail(err)
		}
		return printJSON(v)
	}
	return fail(errors.New("unknown framework action"))
}

func failIfClose(err error, stderr io.Writer) int {
	if err != nil {
		fmt.Fprintln(stderr, "re-psn-app:", err)
		return 1
	}
	return 0
}

func runProfileTUI(args []string, stderr io.Writer) int {
	fs := flag.NewFlagSet("--profile-dir", flag.ContinueOnError)
	fs.SetOutput(stderr)
	dir := fs.String("profile-dir", "", "private profile directory for the desktop screens")
	origin := fs.String("social-relay", "", "explicit HTTPS social companion")
	ca := fs.String("social-ca-file", "", "optional explicit CA file")
	if fs.Parse(args) != nil || *dir == "" || fs.NArg() != 0 {
		return 2
	}
	if !isTerminal() {
		fmt.Fprintln(stderr, "re-psn-app: use a terminal for profile screens")
		return 2
	}
	path, err := config.Path()
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	f, err := config.Load(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		fmt.Fprintln(stderr, err)
		return 1
	}
	_, noColor := os.LookupEnv("NO_COLOR")
	session := tui.NewProfileSession(*dir)
	defer session.Close()
	m := tui.New(tui.Options{Path: path, File: f, NoColor: noColor, ProfileDir: *dir, ProfileSession: session, SocialOrigin: *origin, SocialCAFile: *ca, StartProfile: true})
	if _, err := tea.NewProgram(m).Run(); err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	return 0
}
