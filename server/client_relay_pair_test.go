package main

import (
	"context"
	"net"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// TestCClientRelayPair drives two native clients through a real server's forced
// relay route, including raw bidirectional payloads and closure from either leg.
func TestCClientRelayPair(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the native relay-pair fixture uses POSIX threads")
	}
	cc := ""
	for _, name := range []string{"cc", "gcc", "clang"} {
		if path, err := exec.LookPath(name); err == nil {
			cc = path
			break
		}
	}
	if cc == "" {
		t.Skip("no C compiler on PATH; client/test_relay_pair.c not run")
	}
	exe := filepath.Join(t.TempDir(), "test_relay_pair")
	args := []string{"-std=gnu11", "-Wall", "-Wextra", "-pthread", "../client/psnr.c", "../client/test_relay_pair.c", "-o", exe}
	if out, err := exec.Command(cc, args...).CombinedOutput(); err != nil {
		t.Fatalf("build native relay pair: %v\n%s", err, out)
	}
	for _, side := range []string{"connector-close", "acceptor-close"} {
		t.Run(side, func(t *testing.T) {
			s := NewServer(10, false)
			s.relay, s.relayForce = true, true
			ln, err := net.Listen("tcp4", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
				defer cancel()
				if err := s.Shutdown(ctx); err != nil {
					t.Errorf("shutdown native relay server: %v", err)
				}
			})
			go s.Serve(ln)
			_, port, _ := net.SplitHostPort(ln.Addr().String())
			ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
			defer cancel()
			out, err := exec.CommandContext(ctx, exe, port, side).CombinedOutput()
			if err != nil || !strings.Contains(string(out), "native relay pair passed bytes=131106") {
				t.Fatalf("native relay pair: %v (context %v)\n%s", err, ctx.Err(), out)
			}
			deadline := time.Now().Add(3 * time.Second)
			for {
				s.mu.Lock()
				clean := len(s.clients) == 0 && len(s.rooms) == 0 && len(s.streams) == 0 && s.openConns == 0
				s.mu.Unlock()
				s.lifeMu.Lock()
				clean = clean && len(s.owned) == 0
				s.lifeMu.Unlock()
				if clean {
					break
				}
				if time.Now().After(deadline) {
					t.Fatal("native client controls, relay sockets or room state remained after closure")
				}
				time.Sleep(time.Millisecond)
			}
			if got := atomic.LoadUint64(&s.relayed); got != 131106 {
				t.Fatalf("relayed bytes %d, want 131106", got)
			}
		})
	}
}
