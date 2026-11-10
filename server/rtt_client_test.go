package main

import (
	"bufio"
	"net"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// TestCClientReportsRTT runs the C conformance client against a live server and, while its two players are still
// connected in one room, looks at what the server was told: the first player's HEARTBEAT carried the round trip the
// library timed after it opted in, the second left reporting at its default (off) and sent an empty HEARTBEAT, as
// clients did before.
func TestCClientReportsRTT(t *testing.T) {
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

	s, addr := startTCPUDP(t)
	_, port, _ := net.SplitHostPort(addr)
	cmd := exec.Command(exe, "127.0.0.1", port, "--rtt-sync")
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	var out strings.Builder
	lines := make(chan string)
	go func() {
		sc := bufio.NewScanner(stdout)
		for sc.Scan() {
			lines <- sc.Text()
		}
		close(lines)
	}()
	synced := false
	deadline := time.After(20 * time.Second)
loop:
	for {
		select {
		case line, ok := <-lines:
			if !ok {
				break loop
			}
			out.WriteString(line + "\n")
			if line == "RTT_SENT" && !synced {
				synced = true
				st := s.snapshot()
				if len(st.Rooms) != 1 || len(st.Rooms[0].RTT) != 2 {
					t.Errorf("rooms = %+v", st.Rooms)
				} else if r := st.Rooms[0].RTT; r[0] == nil || *r[0] < 0 || *r[0] > 5000 || r[1] != nil {
					t.Errorf("reports = %v %v, want a small number for the first player and none for the second", r[0], r[1])
				}
				stdin.Write([]byte("go\n"))
			}
		case <-deadline:
			cmd.Process.Kill()
			t.Fatalf("test_psnr did not finish:\n%s", out.String())
		}
	}
	if err := cmd.Wait(); err != nil || !synced || !strings.Contains(out.String(), "all passed") {
		t.Fatalf("test_psnr: %v (synced %v)\n%s", err, synced, out.String())
	}
}
