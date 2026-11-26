package main

import (
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/netip"
	"strconv"
	"sync"
	"sync/atomic"
	"time"
)

// The Medius compatibility switch (-medius). No Medius protocol, profile or
// message is implemented (docs/medius-scope.md): the listener this opens
// accepts a TCP connection and closes it at once, without reading or writing a
// byte. It exists so a title's client can be pointed at a private re-psn and
// the setting, the port and the address rules are in place before a profile is.

const (
	defaultMediusPort = 10071
	defaultMediusAddr = "127.0.0.1"

	// A failed Accept (out of descriptors, a reset before accept) is retried
	// after a pause that doubles from the first to the second value.
	acceptBackoffMin = 5 * time.Millisecond
	acceptBackoffMax = time.Second
)

// nextAcceptBackoff is the pause after a failed Accept: the minimum for the
// first failure in a row, then double, never above the maximum.
func nextAcceptBackoff(prev time.Duration) time.Duration {
	if prev < acceptBackoffMin {
		return acceptBackoffMin
	}
	if prev >= acceptBackoffMax/2 {
		return acceptBackoffMax
	}
	return prev * 2
}

// parseMediusAddr checks -medius-addr and -medius-port and returns the
// listen address, and whether it is unspecified (0.0.0.0 or ::, also written
// as an IPv4-mapped address), which a container needs and the caller warns
// about. The address must be an IP literal that is loopback, private,
// link-local or unspecified; a public address or a host name is refused.
func parseMediusAddr(addr string, port int) (listen string, wide bool, err error) {
	if port < 1 || port > 65535 {
		return "", false, fmt.Errorf("-medius-port %d is not a port number (1-65535)", port)
	}
	ip, err := netip.ParseAddr(addr)
	if err != nil {
		return "", false, fmt.Errorf("-medius-addr %q is not an IP address", addr)
	}
	ip = ip.Unmap()
	if !(ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsUnspecified()) {
		return "", false, fmt.Errorf("-medius-addr %s is not a loopback, private or link-local address; Medius compatibility is for private self-hosted use only", ip)
	}
	return net.JoinHostPort(ip.String(), strconv.Itoa(port)), ip.IsUnspecified(), nil
}

// setupMedius applies the -medius flags: with the switch off it returns no
// listener, and refuses a port or address other than the defaults; with it on
// it opens the listener and logs the two warnings. The warnings carry only the
// listen address the operator chose.
func setupMedius(on bool, addr string, port int, logger *slog.Logger) (*mediusListener, error) {
	if !on {
		if port != defaultMediusPort || addr != defaultMediusAddr {
			return nil, errors.New("a -medius-port or -medius-addr other than the default does nothing without -medius")
		}
		return nil, nil
	}
	listen, wide, err := parseMediusAddr(addr, port)
	if err != nil {
		return nil, err
	}
	med, err := newMediusListener(listen, logger)
	if err != nil {
		return nil, err
	}
	mediusWarnings(logger, med.Addr(), wide)
	return med, nil
}

// mediusWarnings logs the start-up warnings: always that no protocol exists,
// and, for a listener on every interface, that its port is for a private network.
func mediusWarnings(logger *slog.Logger, addr string, wide bool) {
	logger.Warn("Medius compatibility is on, but no Medius protocol is implemented: every connection to the Medius port is closed", "addr", addr)
	if wide {
		logger.Warn("the Medius listener accepts connections on every interface; publish its port only to a private network")
	}
}

// mediusListener accepts and immediately closes every connection.
type mediusListener struct {
	ln     net.Listener
	log    *slog.Logger
	closed atomic.Int64
	done   chan struct{}
	once   sync.Once
	// wait pauses for d, or returns false once Shutdown has been called.
	wait func(d time.Duration) bool
}

func newMediusListener(addr string, log *slog.Logger) (*mediusListener, error) {
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return nil, fmt.Errorf("medius listener: %w", err)
	}
	return newMediusListenerOn(ln, log), nil
}

func newMediusListenerOn(ln net.Listener, log *slog.Logger) *mediusListener {
	m := &mediusListener{ln: ln, log: log, done: make(chan struct{})}
	m.wait = func(d time.Duration) bool {
		t := time.NewTimer(d)
		defer t.Stop()
		select {
		case <-t.C:
			return true
		case <-m.done:
			return false
		}
	}
	return m
}

func (m *mediusListener) Addr() string { return m.ln.Addr().String() }

// Closed is how many connections have been accepted and closed.
func (m *mediusListener) Closed() int64 { return m.closed.Load() }

// Serve runs until Shutdown. The peer's address is not logged. A failed Accept
// is retried after a growing pause, so one error here does not stop the server;
// the error text is not logged, as it may carry a peer address.
func (m *mediusListener) Serve() error {
	var pause time.Duration
	for {
		c, err := m.ln.Accept()
		if err != nil {
			if errors.Is(err, net.ErrClosed) {
				return nil
			}
			pause = nextAcceptBackoff(pause)
			m.log.Warn("medius accept failed; retrying", "pause", pause)
			if !m.wait(pause) {
				return nil
			}
			continue
		}
		pause = 0
		c.Close()
		n := m.closed.Add(1)
		m.log.Debug("medius connection closed: no Medius protocol is implemented", "total", n)
	}
}

// Shutdown closes the listener; Serve then returns nil.
func (m *mediusListener) Shutdown() {
	m.once.Do(func() { close(m.done) })
	m.ln.Close()
}
