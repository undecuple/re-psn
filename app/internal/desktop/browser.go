package desktop

import (
	"encoding/hex"
	"errors"
	"html"
	"io"
	"net"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// OpenBrowser gives the OS dispatcher a private local HTML file. Neither the
// one-use bootstrap nor the persistent UI capability is exposed in process args.
func OpenBrowser(target string) error {
	u, err := url.Parse(target)
	if err != nil || u.Scheme != "http" || u.Hostname() != "127.0.0.1" || u.User != nil || u.RawQuery != "" || u.Path != "/" {
		return errors.New("invalid local app link")
	}
	if _, _, err = net.SplitHostPort(u.Host); err != nil {
		return errors.New("invalid local app authority")
	}
	bootstrap := strings.TrimPrefix(u.Fragment, "token=")
	if decoded, e := hex.DecodeString(bootstrap); e != nil || len(decoded) != 32 || u.Fragment != "token="+bootstrap {
		return errors.New("invalid local app bootstrap")
	}
	target, cleanup, err := browserLaunchFile(target)
	if err != nil {
		return err
	}
	// Reopen launchers may exit immediately; the file contains only a short-lived,
	// one-use bootstrap and stays protected by the user's temporary directory.
	time.AfterFunc(90*time.Second, cleanup)
	var command string
	var args []string
	switch runtime.GOOS {
	case "linux":
		command = "xdg-open"
		args = []string{target}
	case "windows":
		command = "rundll32.exe"
		args = []string{"url.dll,FileProtocolHandler", target}
	case "darwin":
		command = "open"
		args = []string{target}
	default:
		return errors.New("open the app with a supported desktop browser")
	}
	cmd := exec.Command(command, args...)
	cmd.Stdout, cmd.Stderr = io.Discard, io.Discard
	if err = cmd.Start(); err != nil {
		cleanup()
		return errors.New("no desktop browser launcher is available")
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case err = <-done:
		if err != nil {
			return errors.New("desktop browser could not be opened")
		}
	case <-time.After(time.Second):
	}
	return nil
}

// browserLaunchFile keeps the bootstrap out of process listings. A killed launcher
// can leave this private file behind; the controller expires the bootstrap itself.
func browserLaunchFile(target string) (string, func(), error) {
	dir, err := os.MkdirTemp("", "re-psn-launch-")
	if err != nil {
		return "", nil, errors.New("private browser launch file unavailable")
	}
	cleanup := func() { _ = os.RemoveAll(dir) }
	path := filepath.Join(dir, "launch.html")
	body := "<!doctype html><meta charset=\"utf-8\"><meta name=\"referrer\" content=\"no-referrer\"><meta http-equiv=\"refresh\" content=\"0;url=" + html.EscapeString(target) + "\"><title>Open re-psn</title><a href=\"" + html.EscapeString(target) + "\">Open re-psn</a>"
	if err = os.WriteFile(path, []byte(body), 0600); err != nil {
		cleanup()
		return "", nil, errors.New("private browser launch file unavailable")
	}
	filePath := filepath.ToSlash(path)
	if runtime.GOOS == "windows" {
		filePath = "/" + filePath
	}
	return (&url.URL{Scheme: "file", Path: filePath}).String(), cleanup, nil
}
