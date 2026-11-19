package main

import (
	"context"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// TestCClientAuth builds client/test_auth.c and runs it against live servers with sign-in passes on, using passes
// signed by throwaway keys made here. The C program reads each pass from a file and never prints it.
func TestCClientAuth(t *testing.T) {
	cc := ""
	for _, name := range []string{"cc", "gcc", "clang"} {
		if p, err := exec.LookPath(name); err == nil {
			cc = p
			break
		}
	}
	if cc == "" {
		t.Skip("no C compiler on PATH; client/test_auth.c not run")
	}
	exe := filepath.Join(t.TempDir(), "test_auth")
	args := []string{"-std=gnu11", "-Wall", "-Wextra", "-Werror", "-I../client", "../client/psnr.c", "../client/test_auth.c", "-o", exe}
	if runtime.GOOS == "windows" {
		args = append(args, "-lws2_32")
	}
	if out, err := exec.Command(cc, args...).CombinedOutput(); err != nil {
		t.Fatalf("build auth client: %v\n%s", err, out)
	}

	dir := t.TempDir()
	file := func(name, pass string) string {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte(pass), 0o600); err != nil {
			t.Fatal(err)
		}
		return p
	}
	run := func(t *testing.T, addr string, argv ...string) {
		t.Helper()
		_, port, _ := net.SplitHostPort(addr)
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		out, err := exec.CommandContext(ctx, exe, append([]string{port}, argv...)...).CombinedOutput()
		if err != nil || !strings.Contains(string(out), "passed") {
			t.Fatalf("test_auth %s: %v\n%s", argv[0], err, out)
		}
	}
	now := time.Now()

	t.Run("connect mode", func(t *testing.T) {
		p := newPassTool(t)
		other := newPassTool(t)
		_, addr := startConnectServer(t, p, false)
		cl := connectClaims(now, "github:1")
		run(t, addr, "connect",
			file("good", p.pass(cl)),
			file("refresh", p.pass(connectClaims(now.Add(time.Second), "github:1"))),
			file("other", p.pass(connectClaims(now, "github:2"))),
			file("bad", other.pass(cl)))
	})
	t.Run("name taken", func(t *testing.T) {
		p := newPassTool(t)
		_, addr := startConnectServer(t, p, false)
		d := net.Dialer{LocalAddr: &net.TCPAddr{IP: net.IPv4(127, 0, 0, 2)}, Timeout: 5 * time.Second}
		nc, err := d.Dial("tcp", addr)
		if err != nil {
			t.Skip("cannot dial from 127.0.0.2: " + err.Error())
		}
		defer nc.Close()
		holder := &tc{t: t, conn: nc}
		helloAck(t, holder, "taken")
		if rt, _ := holder.call(msgAuth, authBody(passVersion, p.pass(connectClaims(now, "github:3")))); rt != msgOK {
			t.Fatalf("holder AUTH: 0x%02X", rt)
		}
		run(t, addr, "taken", file("taken", p.pass(connectClaims(now, "github:4"))))
	})
	t.Run("relay only", func(t *testing.T) {
		p := newPassTool(t)
		_, addr := startPassServer(t, p, true)
		run(t, addr, "relay", file("relay", p.good(now)))
	})
	t.Run("server without pass settings", func(t *testing.T) {
		_, addr := startTCPUDP(t)
		run(t, addr, "old")
	})
}
