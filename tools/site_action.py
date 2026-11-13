"""The script of the "see it in action" page (site/action.html), kept apart from tools/site.py so the page's content is one reviewable file.

It is a simulation drawn from a fixed script, not live traffic: the addresses are the ones reserved for documentation (RFC 5737), and every
message name comes from docs/api.md (tools/test_site.py checks that each one is named there). Nothing here is measured.

A scenario has: an id and a title, a summary line, which edge of the diagram is drawn how (`edges`), and the steps. A step is
(from, to, message, caption); `message` is a protocol message name from docs/api.md, or "" for game traffic that is not a protocol message.
"""

SERVER = "192.0.2.10"
PLAYER_A = "198.51.100.7"
PLAYER_B = "203.0.113.9"

# Where each name sits in the diagram: id -> (label for the steps list, short name)
WHO = {"A": "player A", "B": "player B", "S": "server"}

# Edge kinds: tcp (control connection), udp (probes and punches), direct (game traffic between the players), blocked (a direct path
# that fails), relay (game traffic carried by the server).
SCENARIOS = (
    {
        "id": "direct",
        "title": "Direct: players on one LAN or VPN",
        "summary": "Both players can reach each other, so the server only introduces them. Game traffic never touches it.",
        "edges": {"AS": ("tcp", "TCP 36100"), "BS": ("tcp", "TCP 36100"), "AB": ("direct", "game traffic, direct")},
        "steps": (
            ("A", "S", "HELLO", "Player A says who it is and which UDP port its game listens on."),
            ("S", "A", "HELLO_ACK", "The server answers with a user id, the address it sees, a token and a flags byte."),
            ("A", "S", "CREATE_ROOM", "Player A opens a room."),
            ("S", "A", "ROOM_JOINED", "The room exists, and A is its first member."),
            ("B", "S", "HELLO", "Player B connects the same way."),
            ("S", "B", "HELLO_ACK", "The server answers B."),
            ("B", "S", "SEARCH_ROOMS", "B asks for rooms of its title that are not full."),
            ("S", "B", "ROOM_LIST", "A's room is in the list."),
            ("B", "S", "JOIN_ROOM", "B joins it."),
            ("S", "B", "ROOM_JOINED", "The reply carries every member's address and game port, so B knows where A is."),
            ("B", "A", "", "B sends game datagrams straight to A. The server is not on the path."),
        ),
    },
    {
        "id": "punch",
        "title": "Hole punch: two ordinary home routers",
        "summary": "Each player is behind a router. The server learns each router's public address from a probe, the players punch "
                   "through, and then talk directly.",
        "edges": {"AS": ("tcp", "TCP 36100"), "BS": ("tcp", "TCP 36100"), "AB": ("direct", "direct after the punch")},
        "steps": (
            ("A", "S", "HELLO", "Player A connects and gives its game port."),
            ("S", "A", "HELLO_ACK", "The answer includes the token that A's probes will carry."),
            ("A", "S", "CREATE_ROOM", "A opens a room."),
            ("S", "A", "ROOM_JOINED", "A is its first member."),
            ("A", "S", "PROBE", "From its game socket, A sends a UDP probe, so the server sees A's router's public address and port."),
            ("S", "A", "PROBE_REPLY", "The server tells A what it saw."),
            ("B", "S", "HELLO", "Player B connects."),
            ("S", "B", "HELLO_ACK", "The server answers B."),
            ("B", "S", "PROBE", "B probes from its game socket too."),
            ("S", "B", "PROBE_REPLY", "The server tells B what it saw."),
            ("B", "S", "JOIN_ROOM", "B joins A's room."),
            ("S", "B", "ROOM_JOINED", "The member entry for A carries the router address and port from A's probe."),
            ("A", "B", "PUNCH", "Both players send PUNCH packets to each other's router address, which opens each router for the other."),
            ("B", "A", "", "Game datagrams now go directly between the players."),
        ),
    },
    {
        "id": "relayed",
        "title": "Relayed: one player behind a symmetric NAT",
        "summary": "B's router picks a new port for every destination, so the punch cannot work. A server run with -relay carries "
                   "the game traffic instead. The room steps are as in the scenarios above and are left out here; the server sends ROUTE to both "
                   "players, and only A's copy is shown.",
        "edges": {"AS": ("tcp", "TCP 36100"), "BS": ("tcp", "TCP 36100"), "AB": ("blocked", "direct path: blocked"),
                  "AR": ("relay", "relayed"), "BR": ("relay", "relayed")},
        "steps": (
            ("A", "S", "HELLO", "Player A connects."),
            ("S", "A", "HELLO_ACK", "The flags byte says the server relays."),
            ("B", "S", "HELLO", "Player B connects."),
            ("S", "B", "HELLO_ACK", "The server answers B."),
            ("A", "S", "PROBE", "A probes; its router keeps one port, so the server sees a usable address."),
            ("B", "S", "PROBE", "B probes; its router maps a new port for each destination."),
            ("A", "B", "PUNCH", "A punches towards the address the server saw for B. It does not reach B."),
            ("S", "A", "ROUTE", "The server tells A that streams to B go through the relay."),
            ("A", "S", "STREAM_CONNECT", "A opens a new TCP connection to the server and asks for a stream to B."),
            ("S", "B", "STREAM_OFFER", "The server offers B the stream, on B's control connection."),
            ("B", "S", "STREAM_ACCEPT", "B opens its own new TCP connection to the server and accepts, within 10 seconds."),
            ("S", "A", "STREAM_READY", "The stream is ready on A's side."),
            ("S", "B", "STREAM_READY", "And on B's side."),
            ("A", "B", "", "From here the server copies the stream's bytes between the two connections. UDP game datagrams go through the relay as well."),
        ),
    },
    {
        "id": "blocked",
        "title": "Blocked: the relay is off",
        "summary": "The same failed punch as above, but the server was not started with -relay, so it carries nothing. The room steps "
                   "are as in the scenarios above and are left out here.",
        "edges": {"AS": ("tcp", "TCP 36100"), "BS": ("tcp", "TCP 36100"), "AB": ("blocked", "direct path: blocked")},
        "steps": (
            ("A", "S", "HELLO", "Player A connects."),
            ("S", "A", "HELLO_ACK", "The flags byte says the server does not relay."),
            ("B", "S", "HELLO", "Player B connects."),
            ("S", "B", "HELLO_ACK", "The server answers B."),
            ("A", "S", "PROBE", "A probes."),
            ("B", "S", "PROBE", "B probes; its router maps a new port for each destination."),
            ("A", "B", "PUNCH", "The punch towards B's seen address does not reach B."),
            ("S", "A", "", "There is no ROUTE and no stream: the server has nothing to offer. The players need a port forward, or a shared VPN such as Tailscale."),
        ),
    },
)

# Node boxes: key -> (x, y, width, height, lines)
NODES = {
    "S": (240, 24, 180, 56, ("re-psn server", SERVER)),
    "A": (24, 176, 180, 60, ("player A", PLAYER_A)),
    "B": (456, 176, 180, 60, ("player B", PLAYER_B)),
}
# Edge lines: key -> (x1, y1, x2, y2, label position)
EDGES = {
    "AS": (114, 176, 270, 80, (150, 118)),
    "BS": (546, 176, 390, 80, (510, 118)),
    "AB": (204, 206, 456, 206, (330, 198)),
    "AR": (150, 176, 300, 80, (262, 152)),
    "BR": (510, 176, 360, 80, (398, 152)),
}


def diagram_svg(scenario, esc):
    """One static diagram for a scenario; classes only (the page allows no inline style)."""
    parts = ['<svg class="action-diagram" viewBox="0 0 660 260" role="img" aria-label="'
             + esc(f"Diagram: {scenario['title']}. The steps below describe it.")
             + '" xmlns="http://www.w3.org/2000/svg">']
    for key, (kind, label) in scenario["edges"].items():
        x1, y1, x2, y2, (lx, ly) = EDGES[key]
        parts.append(f'<line class="edge e-{kind}" x1="{x1}" y1="{y1}" x2="{x2}" y2="{y2}"/>')
        parts.append(f'<text class="edge-label" x="{lx}" y="{ly}" text-anchor="middle">{esc(label)}</text>')
    for key, (x, y, w, h, lines) in NODES.items():
        parts.append(f'<rect class="node" x="{x}" y="{y}" width="{w}" height="{h}"/>')
        for i, line in enumerate(lines):
            cls = "node-name" if i == 0 else "node-addr"
            parts.append(f'<text class="{cls}" x="{x + w // 2}" y="{y + 24 + i * 20}" text-anchor="middle">{esc(line)}</text>')
    parts.append("</svg>\n")
    return "".join(parts)
