package main

// GET_CAPS asks which features the server offers this player; CAPS answers. It exists so a title that needs only some
// features can see, before it relies on one, whether the server has it. The reply is the request id, a bit set and the
// server version:
//
//	GET_CAPS  req u32                                  (after HELLO)
//	CAPS      req u32 | bits u32 | version blob
//
// A client ignores bits it does not know, and treats a bit as "not offered" when it is clear. A server that predates
// GET_CAPS answers the request with ERROR bad request (1).
const (
	msgGetCaps = 0x04
	msgCaps    = 0x94

	capRooms     = 1 << 0 // create, search, join and leave rooms
	capScores    = 1 << 1 // record and read scores
	capRoomCodes = 1 << 2 // invite-only rooms and join by code
	capPeerKeys  = 1 << 3 // PEER_AUTH and PEER_KEY pushes
	capProbe     = 1 << 4 // the UDP probe: the server answers a PROBE from this player's P2P socket
	capRelay     = 1 << 5 // relay streams and relay datagrams for this player
	capDiscover  = 1 << 6 // the server answers LAN discovery (-discover)
)

// capsFor is the bit set for c's title. The probe and the relay depend on -no-peer-titles and -relay; the rest are
// always on. The caller holds s.mu.
func (s *Server) capsFor(c *client) uint32 {
	bits := uint32(capRooms | capScores | capRoomCodes | capPeerKeys)
	if !s.noPeer[c.commID] {
		bits |= capProbe
		if s.relay {
			bits |= capRelay
		}
	}
	if s.discover {
		bits |= capDiscover
	}
	return bits
}

func (s *Server) getCaps(c *client, req uint32) (byte, []byte) {
	w := &writer{}
	w.u32(req)
	w.u32(s.capsFor(c))
	w.blob([]byte(version))
	return msgCaps, w.b
}
