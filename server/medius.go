package main

import (
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/netip"
	"strconv"
	"sync/atomic"
)

// The Medius compatibility switch (-medius). No Medius protocol, profile or
// message is implemented (docs/medius-scope.md): the listener this opens
// accepts a TCP connection and closes it at once, without reading or writing a
// byte. It exists so a title's client can be pointed at a private re-psn and
// the setting, the port and the address rules are in place before a profile is.

const (
	defaultMediusPort = 10071
	defaultMediusAddr = "127.0.0.1"
)

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

// mediusListener accepts and immediately closes every connection.
type mediusListener struct {
	ln     net.Listener
	log    *slog.Logger
	closed atomic.Int64
}

func newMediusListener(addr string, log *slog.Logger) (*mediusListener, error) {
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return nil, fmt.Errorf("medius listener: %w", err)
	}
	return &mediusListener{ln: ln, log: log}, nil
}

func (m *mediusListener) Addr() string { return m.ln.Addr().String() }

// Closed is how many connections have been accepted and closed.
func (m *mediusListener) Closed() int64 { return m.closed.Load() }

// Serve runs until Shutdown. The peer's address is not logged.
func (m *mediusListener) Serve() error {
	for {
		c, err := m.ln.Accept()
		if err != nil {
			if errors.Is(err, net.ErrClosed) {
				return nil
			}
			return err
		}
		c.Close()
		n := m.closed.Add(1)
		m.log.Debug("medius connection closed: no Medius protocol is implemented", "total", n)
	}
}

// Shutdown closes the listener; Serve then returns nil.
func (m *mediusListener) Shutdown() { m.ln.Close() }
