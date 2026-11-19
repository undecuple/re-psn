package host

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/undecuple/re-psn/app/internal/client"
)

var serverBuild struct {
	once              sync.Once
	path, dir, output string
	err               error
}

func TestMain(m *testing.M) {
	code := m.Run()
	if serverBuild.dir != "" {
		_ = os.RemoveAll(serverBuild.dir)
	}
	os.Exit(code)
}

// Every required lifecycle test uses the actual standalone server. A build
// failure is a failure, never an optional-tool skip or a successful mock host.
func realServer(t *testing.T) string {
	t.Helper()
	serverBuild.once.Do(func() {
		serverBuild.dir, serverBuild.err = os.MkdirTemp("", "re-psn-host-test-build-")
		if serverBuild.err != nil {
			return
		}
		name := "re-psn"
		if runtime.GOOS == "windows" {
			name += ".exe"
		}
		serverBuild.path = filepath.Join(serverBuild.dir, name)
		// Go test starts in the package source directory. Walk upward rather
		// than relying on runtime.Caller paths, which -trimpath can rewrite.
		root, err := os.Getwd()
		if err != nil {
			serverBuild.err = err
			return
		}
		for {
			_, serverErr := os.Stat(filepath.Join(root, "server", "main.go"))
			_, appErr := os.Stat(filepath.Join(root, "app", "go.mod"))
			if serverErr == nil && appErr == nil {
				break
			}
			parent := filepath.Dir(root)
			if parent == root {
				serverBuild.err = fmt.Errorf("cannot locate required server source")
				return
			}
			root = parent
		}
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		cmd := exec.CommandContext(ctx, "go", "build", "-buildvcs=false", "-o", serverBuild.path, "./server")
		cmd.Dir = root
		out, err := cmd.CombinedOutput()
		serverBuild.output = string(out)
		serverBuild.err = err
	})
	if serverBuild.err != nil {
		t.Fatalf("required real server build failed: %v\n%s", serverBuild.err, serverBuild.output)
	}
	return serverBuild.path
}

func freeGameAddress(t *testing.T) string {
	t.Helper()
	for attempt := 0; attempt < 16; attempt++ {
		ln, err := net.Listen("tcp4", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		address := ln.Addr().String()
		udpAddress, err := net.ResolveUDPAddr("udp4", address)
		if err != nil {
			ln.Close()
			t.Fatal(err)
		}
		udp, err := net.ListenUDP("udp4", udpAddress)
		ln.Close()
		if err == nil {
			udp.Close()
			return address
		}
	}
	t.Fatal("could not reserve a free TCP/UDP game address")
	return ""
}
func startManager(t *testing.T, m *Manager, address string, relay bool) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	if err := m.Start(ctx, address, relay); err != nil {
		t.Fatal(err)
	}
}
func hello(t *testing.T, address string) client.HelloAck {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	c, err := client.Dial(ctx, address)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	ack, err := c.Hello(ctx, client.Hello{CommID: "SYNTHETIC", OnlineID: "host_fixture"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.SearchRooms(ctx, 0, 10); err != nil {
		t.Fatal(err)
	}
	return ack
}
func requireReleased(t *testing.T, address string) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second) // a killed child's sockets can take a few seconds to free on Windows
	a, err := net.ResolveUDPAddr("udp4", address)
	if err != nil {
		t.Fatal(err)
	}
	for {
		c, err := net.DialTimeout("tcp4", address, 100*time.Millisecond)
		if err == nil {
			c.Close()
		} else if udp, e := net.ListenUDP("udp4", a); e == nil {
			udp.Close()
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("owned TCP/UDP listeners survived stop")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func statusRequest(t *testing.T, origin, token string) (int, []byte) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, origin+"/api/stats", nil)
	if err != nil {
		t.Fatal(err)
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	transport := &http.Transport{Proxy: nil}
	defer transport.CloseIdleConnections()
	res, err := (&http.Client{Transport: transport, Timeout: time.Second}).Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	b, err := io.ReadAll(io.LimitReader(res.Body, 1<<20))
	if err != nil {
		t.Fatal(err)
	}
	return res.StatusCode, b
}

func TestManagerRealLifecycleAuthenticatedStatusAndRestart(t *testing.T) {
	binary := realServer(t)
	// A managed host must not inherit unrelated server controls or a shared token.
	t.Setenv("RE_PSN_ADDR", "192.0.2.1:1")
	t.Setenv("RE_PSN_RELAY", "true")
	t.Setenv("PSNR_HTTP", "192.0.2.1:1")
	t.Setenv("RE_PSN_HTTP_TOKEN", "synthetic-inherited-token")
	t.Setenv("RE_PSN_ADMIN", "192.0.2.1:1")
	t.Setenv("RE_PSN_REPLACE", "true")
	m := New(binary)
	defer m.Close()
	if v := m.View(); !v.Available || v.Running {
		t.Fatal(v)
	}
	address := freeGameAddress(t)
	before := time.Now().UnixMilli()
	startManager(t, m, address, false)
	v := m.View()
	if !v.Running || v.Address != address || v.StartedAt < before {
		t.Fatal("false readiness", v)
	}
	origin, tokenPath := m.StatusSource()
	if !strings.HasPrefix(origin, "http://127.0.0.1:") || tokenPath == "" {
		t.Fatal("missing private status source")
	}
	token, err := os.ReadFile(tokenPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(token) != 64 || string(token) == "synthetic-inherited-token" {
		t.Fatal("status token not fresh")
	}
	if st, err := os.Stat(tokenPath); err != nil || (runtime.GOOS != "windows" && st.Mode().Perm()&0077 != 0) {
		t.Fatal("status token permissions", err)
	}
	if code, _ := statusRequest(t, origin, ""); code != 401 {
		t.Fatal("status did not require private credential", code)
	}
	if code, _ := statusRequest(t, origin, "wrong-synthetic-token"); code != 401 {
		t.Fatal("wrong token admitted", code)
	}
	code, body := statusRequest(t, origin, string(token))
	var stats struct {
		Version string `json:"version"`
		Relay   bool   `json:"relay"`
	}
	if code != 200 || json.Unmarshal(body, &stats) != nil || stats.Version == "" || stats.Relay {
		t.Fatal("authenticated source/overrides failed", code)
	}
	if hello(t, address).Relay() {
		t.Fatal("inherited relay option escaped sanitization")
	}
	// Stop waits up to 3 s for the child to exit, then kills it; a loaded Windows runner needs room beyond that, so a slow stop is
	// not a failure and only a real hang is.
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if err := m.Stop(ctx); err != nil {
		t.Fatal(err)
	}
	if m.View().Running {
		t.Fatal("stop left running state")
	}
	if a, b := m.StatusSource(); a != "" || b != "" {
		t.Fatal("stopped source remained exposed")
	}
	if _, err := os.Stat(tokenPath); !os.IsNotExist(err) {
		t.Fatal("private token survived stop", err)
	}
	requireReleased(t, address)
	startManager(t, m, address, true)
	if !hello(t, address).Relay() {
		t.Fatal("explicit relay restart not applied")
	}
	_, newPath := m.StatusSource()
	fresh, err := os.ReadFile(newPath)
	if err != nil || bytes.Equal(fresh, token) {
		t.Fatal("restart reused status authority", err)
	}
	if err := m.Close(); err != nil {
		t.Fatal(err)
	}
	requireReleased(t, address)
	if err := m.Start(ctx, address, false); err == nil {
		t.Fatal("closed manager started a new process")
	}
}

func TestManagerStopsOnlyItsOwnedServer(t *testing.T) {
	binary := realServer(t)
	owned, other := New(binary), New(binary)
	defer owned.Close()
	defer other.Close()
	one := freeGameAddress(t)
	startManager(t, owned, one, false)
	two := freeGameAddress(t)
	startManager(t, other, two, false)
	hello(t, one)
	hello(t, two)
	if err := owned.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}
	requireReleased(t, one)
	if !other.View().Running {
		t.Fatal("another manager's host was stopped")
	}
	hello(t, two)
}

func TestManagerOccupiedPortsFailWithoutReplacingOwners(t *testing.T) {
	binary := realServer(t)
	t.Run("TCP", func(t *testing.T) {
		address := freeGameAddress(t)
		original := New(binary)
		defer original.Close()
		startManager(t, original, address, false)
		m := New(binary)
		defer m.Close()
		ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
		defer cancel()
		if err := m.Start(ctx, address, false); err == nil || m.View().Running {
			t.Fatal("occupied TCP port reported ready")
		}
		if !original.View().Running {
			t.Fatal("same-name server was replaced")
		}
		hello(t, address)
	})
	t.Run("UDP", func(t *testing.T) {
		address := freeGameAddress(t)
		a, err := net.ResolveUDPAddr("udp4", address)
		if err != nil {
			t.Fatal(err)
		}
		listener, err := net.ListenUDP("udp4", a)
		if err != nil {
			t.Fatal(err)
		}
		defer listener.Close()
		m := New(binary)
		defer m.Close()
		ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
		defer cancel()
		if err := m.Start(ctx, address, false); err == nil || m.View().Running {
			t.Fatal("occupied UDP port reported ready")
		}
		sender, err := net.DialUDP("udp4", nil, a)
		if err != nil {
			t.Fatal(err)
		}
		defer sender.Close()
		if _, err := sender.Write([]byte("synthetic-owner")); err != nil {
			t.Fatal(err)
		}
		listener.SetReadDeadline(time.Now().Add(time.Second))
		var b [64]byte
		n, _, err := listener.ReadFromUDP(b[:])
		if err != nil || string(b[:n]) != "synthetic-owner" {
			t.Fatal("existing UDP owner was disturbed", err)
		}
		if c, err := net.DialTimeout("tcp4", address, 100*time.Millisecond); err == nil {
			c.Close()
			t.Fatal("failed child left game TCP listening")
		}
	})
}

func TestManagerUnexpectedExitIsNotAnIntentionalStop(t *testing.T) {
	source := realServer(t)
	data, err := os.ReadFile(source)
	if err != nil {
		t.Fatal(err)
	}
	name := "synthetic-server-copy"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	copyPath := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(copyPath, data, 0700); err != nil {
		t.Fatal(err)
	}
	m := New(copyPath)
	defer m.Close()
	address := freeGameAddress(t)
	startManager(t, m, address, false)
	_, tokenPath := m.StatusSource()
	m.mu.Lock()
	process, done := m.cmd.Process, m.done
	m.mu.Unlock()
	if err := process.Kill(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("unexpected exit was not observed")
	}
	view := m.View()
	if view.Running || !strings.Contains(strings.ToLower(view.Message), "exited") || strings.Contains(strings.ToLower(view.Message), "stopped") {
		t.Fatal("crash mislabeled", view)
	}
	if a, b := m.StatusSource(); a != "" || b != "" {
		t.Fatal("crashed source still published")
	}
	if _, err := os.Stat(tokenPath); !os.IsNotExist(err) {
		t.Fatal("crash did not clean private state", err)
	}
	requireReleased(t, address)
	startManager(t, m, address, false)
	hello(t, address)
}

func TestManagerParentDeathClosesLinuxChild(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("Linux parent-death signal control; Windows ownership is exercised by the lifecycle tests")
	}
	if os.Getenv("RE_PSN_HOST_TEST_PARENT_HELPER") == "1" {
		// Only this owned helper reuses the binary built by the parent test.
		binary := os.Getenv("RE_PSN_HOST_TEST_SERVER")
		if binary == "" {
			t.Fatal("parent helper has no owned server binary")
		}
		m := New(binary)
		startManager(t, m, os.Getenv("RE_PSN_HOST_TEST_ADDRESS"), false)
		_, tokenPath := m.StatusSource()
		if err := os.WriteFile(os.Getenv("RE_PSN_HOST_TEST_READY"), []byte(tokenPath), 0600); err != nil {
			t.Fatal(err)
		}
		select {}
	}
	binary := realServer(t)
	address := freeGameAddress(t)
	ready := filepath.Join(t.TempDir(), "ready")
	cmd := exec.Command(os.Args[0], "-test.run=^TestManagerParentDeathClosesLinuxChild$")
	cmd.Env = append(os.Environ(), "RE_PSN_HOST_TEST_PARENT_HELPER=1", "RE_PSN_HOST_TEST_SERVER="+binary, "RE_PSN_HOST_TEST_ADDRESS="+address, "RE_PSN_HOST_TEST_READY="+ready)
	var output bytes.Buffer
	cmd.Stdout = &output
	cmd.Stderr = &output
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	var waitErr error
	go func() { waitErr = cmd.Wait(); close(done) }()
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
		}
	})
	deadline := time.Now().Add(8 * time.Second)
	var tokenPath []byte
	for {
		var err error
		tokenPath, err = os.ReadFile(ready)
		if err == nil {
			break
		}
		select {
		case <-done:
			t.Fatalf("helper exited before readiness: %v\n%s", waitErr, output.String())
		default:
		}
		if time.Now().After(deadline) {
			t.Fatal("parent helper did not start")
		}
		time.Sleep(10 * time.Millisecond)
	}
	privateDir := filepath.Dir(string(tokenPath))
	if !strings.HasPrefix(filepath.Base(privateDir), "re-psn-host-") {
		t.Fatal("unexpected helper state location")
	}
	t.Cleanup(func() { _ = os.RemoveAll(privateDir) })
	hello(t, address)
	if err := cmd.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("helper did not die")
	}
	requireReleased(t, address)
}
