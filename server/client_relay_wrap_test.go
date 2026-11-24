package main

import (
	"context"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// TestCClientRelayWrap builds client/test_relay_wrap.c, which checks the size
// bounds of psnr_relay_wrap against guard pages: a payload of 0 bytes, the
// largest one, one more and SIZE_MAX. It needs no server.
func TestCClientRelayWrap(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the relay-wrap fixture uses POSIX guard pages")
	}
	cc := ""
	for _, name := range []string{"cc", "gcc", "clang"} {
		if p, err := exec.LookPath(name); err == nil {
			cc = p
			break
		}
	}
	if cc == "" {
		t.Skip("no C compiler on PATH; client/test_relay_wrap.c not run")
	}
	exe := filepath.Join(t.TempDir(), "test_relay_wrap")
	args := []string{"-std=gnu11", "-Wall", "-Wextra", "-Werror", "-I../client", "../client/test_relay_wrap.c", "-o", exe}
	if out, err := exec.Command(cc, args...).CombinedOutput(); err != nil {
		t.Fatalf("build relay-wrap client: %v\n%s", err, out)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, exe).CombinedOutput()
	if err != nil || !strings.Contains(string(out), "relay wrap bounds passed") {
		t.Fatalf("relay-wrap client: %v\n%s", err, out)
	}
	t.Logf("%s", out)
}
