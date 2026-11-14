package main

import (
	"context"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

// TestCClientConnectWait uses linker wrappers to inject EINTR and advance a
// monotonic clock by exact amounts. The signal cases also exercise a real poll
// interrupted by SIGUSR1 while a full loopback listen backlog delays connect.
func TestCClientConnectWait(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("connect-wait fixture uses Linux backlog behavior and GNU linker wrappers")
	}
	cc := ""
	for _, name := range []string{"cc", "gcc", "clang"} {
		if p, err := exec.LookPath(name); err == nil {
			cc = p
			break
		}
	}
	if cc == "" {
		t.Skip("no C compiler on PATH; client/test_connect_wait.c not run")
	}
	exe := filepath.Join(t.TempDir(), "test_connect_wait")
	args := []string{"-std=gnu11", "-Wall", "-Wextra", "-Werror", "-I../client", "../client/test_connect_wait.c",
		"-Wl,--wrap=connect,--wrap=poll,--wrap=clock_gettime", "-o", exe}
	if out, err := exec.Command(cc, args...).CombinedOutput(); err != nil {
		t.Fatalf("build connect-wait client: %v\n%s", err, out)
	}
	for _, operation := range []string{"connect", "accept", "control"} {
		modes := []string{"success", "repeated", "expires"}
		if operation != "control" {
			modes = append(modes, "negative", "signal", "signal-negative")
		}
		for _, mode := range modes {
			t.Run(operation+"/"+mode, func(t *testing.T) {
				ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
				defer cancel()
				if out, err := exec.CommandContext(ctx, exe, operation, mode).CombinedOutput(); err != nil {
					t.Fatalf("connect-wait client: %v\n%s", err, out)
				} else {
					t.Logf("%s", out)
				}
			})
		}
	}
}
