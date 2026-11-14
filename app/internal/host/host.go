// Package host owns an explicitly started server child, independently of the
// remote server a player checks. It never attaches process control to a URL/PID.
package host

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"
)

type View struct {
	Available bool   `json:"available"`
	Running   bool   `json:"running"`
	Address   string `json:"address"`
	Message   string `json:"message"`
	StartedAt int64  `json:"started_at"`
}
type Manager struct {
	mu                   sync.Mutex
	gate                 chan struct{}
	binary               string
	cmd                  *exec.Cmd
	done                 chan struct{}
	cleanup              func()
	statusURL, tokenFile string
	view                 View
	closed               bool
	expectedStop         bool
}

func New(binary string) *Manager {
	if binary == "" {
		if self, err := os.Executable(); err == nil {
			name := "re-psn-host"
			if runtime.GOOS == "windows" {
				name += ".exe"
			}
			binary = filepath.Join(filepath.Dir(self), name)
			if _, err := os.Stat(binary); os.IsNotExist(err) {
				legacy := "re-psn"
				if runtime.GOOS == "windows" {
					legacy += ".exe"
				}
				binary = filepath.Join(filepath.Dir(self), legacy)
			}
		}
	}
	if binary != "" {
		if abs, err := filepath.Abs(binary); err == nil {
			binary = abs
		}
	}
	st, err := os.Stat(binary)
	available := err == nil && st.Mode().IsRegular() && (runtime.GOOS == "windows" || st.Mode().Perm()&0111 != 0)
	return &Manager{gate: make(chan struct{}, 1), binary: binary, view: View{Available: available}}
}
func (m *Manager) View() View { m.mu.Lock(); defer m.mu.Unlock(); return m.view }
func (m *Manager) StatusSource() (string, string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.statusURL, m.tokenFile
}
func (m *Manager) acquire(ctx context.Context) error {
	select {
	case m.gate <- struct{}{}:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
func (m *Manager) Start(ctx context.Context, address string, relay bool) error {
	if err := m.acquire(ctx); err != nil {
		return err
	}
	defer func() { <-m.gate }()
	m.mu.Lock()
	closed, running, available := m.closed, m.cmd != nil, m.view.Available
	m.mu.Unlock()
	if closed || running || !available {
		return errors.New("host unavailable, already running or closed")
	}
	h, p, err := net.SplitHostPort(address)
	port, e := strconv.Atoi(p)
	if err != nil || e != nil || port < 1 || port > 65535 || net.ParseIP(h) == nil {
		return errors.New("host address must be a literal IP and port")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	temp, err := os.MkdirTemp("", "re-psn-host-")
	if err != nil {
		return errors.New("host private state unavailable")
	}
	var secret [32]byte
	if _, err = rand.Read(secret[:]); err != nil {
		os.RemoveAll(temp)
		return err
	}
	token := hex.EncodeToString(secret[:])
	tokenFile := filepath.Join(temp, "status-token")
	if err = os.WriteFile(tokenFile, []byte(token), 0600); err != nil {
		os.RemoveAll(temp)
		return errors.New("host private state unavailable")
	}
	ln, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		os.RemoveAll(temp)
		return errors.New("host status port unavailable")
	}
	statusAddress := ln.Addr().String()
	ln.Close()
	cmd := exec.Command(m.binary, "-addr", address, "-http", statusAddress, "-relay="+strconv.FormatBool(relay), "-db", "off", "-admin", "", "-medius=false", "-discover=false", "-replace=false", "-proxy-protocol=false", "-udp-any-source=false", "-log-file", "", "-log-level", "error")
	for _, entry := range os.Environ() {
		name, _, _ := strings.Cut(entry, "=")
		upper := strings.ToUpper(name)
		if !strings.HasPrefix(upper, "RE_PSN_") && !strings.HasPrefix(upper, "PSNR_") {
			cmd.Env = append(cmd.Env, entry)
		}
	}
	cmd.Env = append(cmd.Env, "RE_PSN_HTTP_TOKEN="+token)
	cmd.Stdout, cmd.Stderr = io.Discard, io.Discard
	configureChild(cmd)
	if err = cmd.Start(); err != nil {
		os.RemoveAll(temp)
		return errors.New("server component could not start")
	}
	release, err := ownChild(cmd)
	if err != nil {
		cmd.Process.Kill()
		cmd.Wait()
		os.RemoveAll(temp)
		return errors.New("server process ownership unavailable")
	}
	done := make(chan struct{})
	cleanup := func() { release(); _ = os.RemoveAll(temp) }
	m.mu.Lock()
	m.cmd, m.done, m.cleanup = cmd, done, cleanup
	m.expectedStop = false
	m.statusURL, m.tokenFile = "http://"+statusAddress, tokenFile
	m.view.Address = address
	m.view.Message = "Starting server"
	m.mu.Unlock()
	go func() {
		exitErr := cmd.Wait()
		m.mu.Lock()
		if m.cmd == cmd {
			m.cmd = nil
			m.view.Running = false
			m.view.Message = "Server exited"
			if exitErr != nil {
				m.view.Message = "Server exited with an error"
			}
			if m.expectedStop {
				m.view.Message = "Server stopped"
			}
			m.statusURL, m.tokenFile = "", ""
		}
		m.mu.Unlock()
		cleanup()
		close(done)
	}()
	startCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	transport := &http.Transport{Proxy: nil}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: 300 * time.Millisecond, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	tick := time.NewTicker(50 * time.Millisecond)
	defer tick.Stop()
	for {
		req, _ := http.NewRequestWithContext(startCtx, http.MethodGet, "http://"+statusAddress+"/api/stats", nil)
		req.Header.Set("Authorization", "Bearer "+token)
		if res, e := client.Do(req); e == nil {
			var stats struct {
				Version string `json:"version"`
				Clients *int   `json:"clients"`
				Relay   *bool  `json:"relay"`
			}
			parseErr := json.NewDecoder(io.LimitReader(res.Body, 4096)).Decode(&stats)
			res.Body.Close()
			negative, _ := http.NewRequestWithContext(startCtx, http.MethodGet, "http://"+statusAddress+"/api/stats", nil)
			denied := false
			if noAuth, e := client.Do(negative); e == nil {
				denied = noAuth.StatusCode == http.StatusUnauthorized
				noAuth.Body.Close()
			}
			if res.StatusCode == 200 && parseErr == nil && stats.Version != "" && len(stats.Version) <= 32 && stats.Clients != nil && stats.Relay != nil && denied {
				m.mu.Lock()
				if m.cmd == cmd {
					m.view.Running = true
					m.view.StartedAt = time.Now().UnixMilli()
					m.view.Message = "Hosting · game TCP and UDP"
					m.mu.Unlock()
					return nil
				}
				m.mu.Unlock()
			}
		}
		select {
		case <-done:
			return errors.New("server could not bind its game/status ports")
		case <-startCtx.Done():
			m.stopOwned()
			return errors.New("server did not become ready")
		case <-tick.C:
		}
	}
}
func (m *Manager) stopOwned() {
	m.mu.Lock()
	cmd, done := m.cmd, m.done
	if cmd != nil {
		m.expectedStop = true
	}
	m.mu.Unlock()
	if cmd == nil {
		if done != nil {
			<-done
		}
		return
	}
	_ = stopChild(cmd)
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		_ = cmd.Process.Kill()
		<-done
	}
}
func (m *Manager) Stop(ctx context.Context) error {
	if err := m.acquire(ctx); err != nil {
		return err
	}
	defer func() { <-m.gate }()
	m.stopOwned()
	return nil
}
func (m *Manager) Close() error {
	m.gate <- struct{}{}
	defer func() { <-m.gate }()
	m.mu.Lock()
	m.closed = true
	m.mu.Unlock()
	m.stopOwned()
	return nil
}
