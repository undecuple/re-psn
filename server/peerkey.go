package main

// Peer keys, for players who send each other game datagrams directly. The
// server never sees that traffic, so it cannot vouch for a sender the way it
// does for a relayed datagram. What it can do is give each pair of players
// one secret only those two (and the server) hold, which a client can use to
// tag its packets to that peer and check the tags on the ones it gets.
//
// A client asks with PEER_AUTH after HELLO. From then on, whenever it and
// another asking member are in a room together, both get the same PEER_KEY
// push for each other. A member who did not ask gets no key and nobody gets a
// key about them. The key is 16 random bytes, new for every join, so it never
// outlives the pair's time in that room. The keys travel over the plain TCP
// connection like the token does: this stops other members forging a peer,
// and off-path senders, not someone who can read a player's network traffic.
// The server uses the keys for nothing else and does not keep them.

import (
	crand "crypto/rand"
)

const (
	msgPeerAuth = 0x03 // request: no body; OK. Opts the client in to PEER_KEY pushes
	msgPeerKey  = 0xA8 // push: room u64 | user u32 | key [16]

	helloPeerKeys = 4 // HELLO_ACK flags bit 2: the server issues peer keys
	peerKeyLen    = 16
)

// peerAuth turns PEER_AUTH on for c and sends the keys for the rooms it is
// already in, so asking after a join works the same as asking before.
func (s *Server) peerAuth(c *client, req uint32) (byte, []byte, []push) {
	if c.peerAuth {
		return msgOK, okReply(req), nil
	}
	c.peerAuth = true
	var out []push
	for _, r := range c.rooms {
		for _, m := range r.members {
			if m.c != c {
				out = append(out, s.peerKeyPushes(r, c, m.c)...)
			}
		}
	}
	return msgOK, okReply(req), out
}

// peerKeyPushes is the pair of PEER_KEY pushes for a and b in r, the same new
// key to each, or nothing if either has not asked.
func (s *Server) peerKeyPushes(r *room, a, b *client) []push {
	if !a.peerAuth || !b.peerAuth {
		return nil
	}
	var key [peerKeyLen]byte
	if _, err := crand.Read(key[:]); err != nil {
		s.log.Error("peer key", "err", err)
		return nil
	}
	return []push{peerKeyPush(r, b, a, key), peerKeyPush(r, a, b, key)}
}

// peerKeyPush tells to the key it shares with about.
func peerKeyPush(r *room, about, to *client, key [peerKeyLen]byte) push {
	w := &writer{}
	w.u64(r.id)
	w.u32(about.id)
	w.bytes(key[:])
	return push{to, msgPeerKey, w.b}
}
