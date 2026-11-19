package main

import (
	"context"
	"net"
	"sync"
)

// ownedConn remains tracked even when a relay leg is handed to another
// goroutine. Closing the connection releases ownership exactly once.
type ownedConn struct {
	net.Conn
	server *Server
	once   sync.Once
}

func (c *ownedConn) Close() error {
	err := c.Conn.Close()
	c.once.Do(func() {
		c.server.lifeMu.Lock()
		delete(c.server.owned, c)
		c.server.lifeMu.Unlock()
	})
	return err
}

func (s *Server) ready() bool {
	s.lifeMu.Lock()
	defer s.lifeMu.Unlock()
	return !s.stopping && s.tcpReady && s.udpReady
}

// Shutdown stops admission before closing listeners and every accepted socket,
// including peers still reading a PROXY header or waiting for a relay accept.
// Existing handlers can finish their cleanup until ctx expires. The server
// cannot be started again; a supervisor starts a fresh process instead.
func (s *Server) Shutdown(ctx context.Context) error {
	s.lifeMu.Lock()
	if !s.stopping {
		s.stopping = true
		close(s.stopped)
	}
	ln, pc := s.tcp, s.udp
	conns := make([]*ownedConn, 0, len(s.owned))
	for conn := range s.owned {
		conns = append(conns, conn)
	}
	s.lifeMu.Unlock()
	if ln != nil {
		ln.Close()
	}
	if pc != nil {
		pc.Close()
	}
	for _, conn := range conns {
		conn.Close()
	}
	done := make(chan struct{})
	go func() {
		s.handlers.Wait()
		close(done)
	}()
	defer s.flushAllLimited()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
