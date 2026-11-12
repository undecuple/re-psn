package main

import (
	"fmt"
	"strings"
)

// titlePeerOff is the -no-peer-titles setting: titles (by communication ID) that never use peer traffic. For a player
// of such a title the server answers no UDP probe, so it never records a UDP endpoint for it, relays none of its UDP
// packets or streams, tells nobody to use the relay for it, and clears the relay flag in its HELLO_ACK. Rooms,
// scores and everything else work as usual. Other titles on the same server are not affected.

// parseTitleList reads a comma-separated list of communication IDs: each 1 to 12 printable ASCII bytes (the HELLO field).
func parseTitleList(list string) (map[string]bool, error) {
	if strings.TrimSpace(list) == "" {
		return nil, nil
	}
	out := map[string]bool{}
	for _, id := range strings.Split(list, ",") {
		id = strings.TrimSpace(id)
		if id == "" || len(id) > 12 {
			return nil, fmt.Errorf("-no-peer-titles: %q is not a communication ID (1 to 12 bytes)", id)
		}
		for i := 0; i < len(id); i++ {
			if id[i] < 0x21 || id[i] > 0x7e {
				return nil, fmt.Errorf("-no-peer-titles: %q holds a byte that is not printable ASCII", id)
			}
		}
		out[id] = true
	}
	return out, nil
}
