package main

import (
	"net"
	"testing"
	"time"
)

// A player's name is taken over by a new connection from the same address (a title that restarted): the takeover clears the old
// connection's commID under s.mu while that connection's own goroutine may be handling a request. Run with -race, which is how
// CI runs the server tests: an unlocked read of commID in handle is reported.
func TestTakeoverRacesWithRequestsOnOldConnection(t *testing.T) {
	_, addr := startServer(t)
	for i := 0; i < 400; i++ {
		a := dial(t, addr, "NPWR00001_00", "alice", 3658)
		stop := make(chan struct{})
		done := make(chan struct{})
		go func() {
			defer close(done)
			for {
				select {
				case <-stop:
					return
				default:
				}
				w := &writer{}
				w.u32(1)
				writePacket(a.conn, msgSearchRooms, w.b)
				time.Sleep(50 * time.Microsecond)
			}
		}()
		time.Sleep(2 * time.Millisecond)
		b, err := net.Dial("tcp", addr)
		if err != nil {
			t.Fatal(err)
		}
		helloConn(t, b, "NPWR00001_00", "alice", 3658)
		close(stop)
		<-done
	}
}
