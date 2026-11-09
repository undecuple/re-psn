package main

import (
	"context"
	"net"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// TestCClient builds the C conformance client and minimal example and runs
// them against a live server, so the client and server can't drift apart. Skips
// when there's no `cc`, `gcc` or `clang` on PATH.
func TestCClient(t *testing.T) {
	cc := ""
	for _, name := range []string{"cc", "gcc", "clang"} {
		if p, err := exec.LookPath(name); err == nil {
			cc = p
			break
		}
	}
	if cc == "" {
		t.Skip("no C compiler on PATH; client/test_psnr.c not run")
	}

	exe := filepath.Join(t.TempDir(), "test_psnr")
	args := []string{"-std=gnu11", "-Wall", "-o", exe, "../client/psnr.c", "../client/test_psnr.c"}
	if runtime.GOOS == "windows" {
		args = append(args, "-lws2_32")
	}
	if out, err := exec.Command(cc, args...).CombinedOutput(); err != nil {
		t.Fatalf("%s: %v\n%s", cc, err, out)
	}

	_, addr := startTCPUDP(t)
	_, port, _ := net.SplitHostPort(addr)
	out, err := exec.Command(exe, "127.0.0.1", port).CombinedOutput()
	if err != nil || !strings.Contains(string(out), "all passed") {
		t.Fatalf("test_psnr: %v\n%s", err, out)
	}

	example := filepath.Join(t.TempDir(), "minimal")
	args = []string{"-std=gnu11", "-Wall", "-Wextra", "-I../client", "../client/psnr.c", "../client/examples/minimal.c", "-o", example}
	if runtime.GOOS == "windows" {
		args = append(args, "-lws2_32")
	}
	if out, err := exec.Command(cc, args...).CombinedOutput(); err != nil {
		t.Fatalf("build minimal example: %v\n%s", err, out)
	}
	runExample := func(argv ...string) ([]byte, error) {
		t.Helper()
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		out, err := exec.CommandContext(ctx, example, argv...).CombinedOutput()
		if ctx.Err() != nil {
			t.Fatalf("minimal example exceeded deadline: %v\n%s", ctx.Err(), out)
		}
		return out, err
	}
	for _, argv := range [][]string{nil, {"127.0.0.1", port}, {"127.0.0.1", port, "demo", "extra"}} {
		out, err := runExample(argv...)
		if exit, ok := err.(*exec.ExitError); !ok || exit.ExitCode() != 2 || !strings.Contains(string(out), "Usage:") {
			t.Fatalf("minimal invalid arguments %q: %v\n%s", argv, err, out)
		}
	}
	for _, badPort := range []string{"", "0", "65536", "999999999999999999999999999", "-1", "+1", " 1", "1 ", "1x"} {
		out, err := runExample("127.0.0.1", badPort, "demo")
		if exit, ok := err.(*exec.ExitError); !ok || exit.ExitCode() != 2 || !strings.Contains(string(out), "PORT must") {
			t.Fatalf("minimal invalid port %q: %v\n%s", badPort, err, out)
		}
	}
	for _, name := range []string{"", "abcdefghijklmnopq"} {
		out, err := runExample("127.0.0.1", port, name)
		if exit, ok := err.(*exec.ExitError); !ok || exit.ExitCode() != 2 || !strings.Contains(string(out), "NAME must") {
			t.Fatalf("minimal invalid name %q: %v\n%s", name, err, out)
		}
	}
	out, err = runExample("127.0.0.1", port, "demo")
	if err != nil || !strings.Contains(string(out), "HELLO user=") || !strings.Contains(string(out), "ROOM_LIST total=0 count=0") {
		t.Fatalf("minimal loopback example: %v\n%s", err, out)
	}

	// Exercise the example's entry/blob length checks with a populated list.
	host := dial(t, addr, "RE-PSN-DEMO", "demo-host", 0)
	w := &writer{}
	w.u8(2)
	w.u32(0)
	w.blob([]byte("synthetic room"))
	w.blob(nil)
	w.blob(nil)
	if rt, _ := host.call(msgCreateRoom, w.b); rt != msgRoomJoined {
		t.Fatalf("create example room: got 0x%02X", rt)
	}
	out, err = runExample("127.0.0.1", port, "demo")
	if err != nil || !strings.Contains(string(out), "ROOM_LIST total=1 count=1") {
		t.Fatalf("minimal populated room list: %v\n%s", err, out)
	}
}
