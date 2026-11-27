#!/usr/bin/env python3
"""Single-file Python client for the re-psn TCP protocol (docs/api.md). Standard library only.

Covers HELLO, heartbeat, rooms, room data and messages, and scores. It does not cover the
relay, UDP probes, sign-in passes or peer keys. Unofficial fan work; it talks only to the
host you give it. Not thread-safe: use one thread per Client.

    c = Client("127.0.0.1", 36100, "RE-PSN-DEMO", "alice")
    room = c.create_room(max_members=4, external=b"hi")
    print(c.search_rooms())
"""
import itertools
import socket
import struct

HELLO, HEARTBEAT, GET_CAPS = 0x01, 0x02, 0x04
CREATE_ROOM, SEARCH_ROOMS, JOIN_ROOM, LEAVE_ROOM = 0x10, 0x11, 0x12, 0x13
SET_ROOM_DATA, ROOM_MESSAGE, KICK_MEMBER = 0x14, 0x15, 0x16
RECORD_SCORE, GET_RANKING = 0x20, 0x21
ERROR = 0x8F

E_BAD_REQUEST, E_NOT_FOUND, E_ROOM_FULL, E_NOT_OWNER, E_NOT_IN_ROOM = 1, 2, 3, 4, 5
E_LIMIT, E_NO_HELLO, E_NAME_TAKEN, E_NO_RELAY, E_PASS = 6, 7, 8, 9, 10
ERROR_NAMES = {1: "bad request", 2: "not found", 3: "room full", 4: "not owner", 5: "not in room",
               6: "limit", 7: "no hello", 8: "name taken", 9: "no relay", 10: "pass refused or needed"}


class ServerError(Exception):
    """The server answered ERROR; .code is the number from docs/api.md."""

    def __init__(self, code):
        super().__init__("server error %d (%s)" % (code, ERROR_NAMES.get(code, "unknown")))
        self.code = code


def _own_timeout(e):
    return isinstance(e, socket.timeout) and e.errno is None


def blob(data):
    if len(data) > 4096:
        raise ValueError("blobs are at most 4096 bytes")
    return struct.pack(">H", len(data)) + data


def _name(text, width):
    raw = text.encode("utf-8")
    if len(raw) > width:
        raise ValueError("%r is longer than %d bytes" % (text, width))
    return raw.ljust(width, b"\0")


def _text(raw):
    return raw.rstrip(b"\0").decode("utf-8", "replace")


def _blob_at(p, off):
    n, = struct.unpack_from(">H", p, off)
    if off + 2 + n > len(p):
        raise ValueError("short reply")
    return p[off + 2:off + 2 + n], off + 2 + n


def parse_member(p, off):
    mid, uid, oid, ip, port, owner = struct.unpack_from(">HI16s4sHB", p, off)
    data, off = _blob_at(p, off + 29)
    return {"member_id": mid, "user_id": uid, "online_id": _text(oid),
            "ip": ".".join(map(str, ip)), "p2p_port": port, "owner": bool(owner), "data": data}, off


def parse_room_joined(p):
    room_id, me, owner, mx, flags = struct.unpack_from(">QHHBI", p, 0)
    external, off = _blob_at(p, 17)
    internal, off = _blob_at(p, off)
    count = p[off]
    off += 1
    members = []
    for _ in range(count):
        m, off = parse_member(p, off)
        members.append(m)
    return {"room_id": room_id, "my_member": me, "owner": owner, "max": mx, "flags": flags,
            "external": external, "internal": internal, "members": members}


class Client:
    """One TCP connection. Replies are matched by request id; pushes (types 0xA0-0xAF) are
    kept in .pushes as (type, payload) until you take them with pop_pushes().
    A socket read or write timeout raises TimeoutError and closes the connection."""

    def __init__(self, host, port, comm_id, online_id, p2p_port=0, timeout=5.0):
        self.sock = socket.create_connection((host, port), timeout)
        self.sock.settimeout(timeout)
        self._req = itertools.count(1)
        self.pushes = []
        r = self.call(HELLO, _name(comm_id, 12) + _name(online_id, 16) + struct.pack(">H", p2p_port))
        self.user_id, = struct.unpack_from(">I", r, 0)
        self.public_ip = ".".join(map(str, r[4:8]))
        self.token, self.flags = struct.unpack_from(">IB", r, 8)

    def close(self):
        self.sock.close()

    def __enter__(self):
        return self

    def __exit__(self, *exc):
        self.close()

    def _send(self, data):
        # A dead connection is always ConnectionError, whether it shows on send or on receive.
        # The socket's own timeout stays a TimeoutError. A system ETIMEDOUT is a lost connection.
        try:
            self.sock.sendall(data)
        except OSError as e:
            if _own_timeout(e):
                # sendall cannot tell us how much of the frame was sent.
                self.close()
                raise
            raise ConnectionError("connection lost: %s" % e) from e

    def _read(self, n):
        buf = b""
        while len(buf) < n:
            try:
                chunk = self.sock.recv(n - len(buf))
            except OSError as e:
                if _own_timeout(e):
                    # The next recv could start in the middle of a frame.
                    self.close()
                    raise
                raise ConnectionError("connection lost: %s" % e) from e
            if not chunk:
                raise ConnectionError("connection closed by the server")
            buf += chunk
        return buf

    def _frame(self):
        t, n = struct.unpack(">BH", self._read(3))
        return t, self._read(n)

    def call(self, msg_type, body=b""):
        """Send one request and return the reply payload after the request id."""
        req = next(self._req)
        payload = struct.pack(">I", req) + body
        if len(payload) > 65535:
            raise ValueError("request too large")
        self._send(struct.pack(">BH", msg_type, len(payload)) + payload)
        while True:
            t, p = self._frame()
            if 0xA0 <= t <= 0xAF:       # pushes carry no request id
                self.pushes.append((t, p))
                continue
            if len(p) < 4 or struct.unpack_from(">I", p, 0)[0] != req:
                continue                # a late reply to something else
            if t == ERROR:
                raise ServerError(struct.unpack_from(">I", p, 4)[0])
            return p[4:]

    def pop_pushes(self):
        out, self.pushes = self.pushes, []
        return out

    def heartbeat(self, rtt_ms=None):
        """HEARTBEAT has no reply; send one at least every 90 seconds (the C client does every 20)."""
        body = struct.pack(">I", 0) + (b"" if rtt_ms is None else struct.pack(">H", rtt_ms))
        self._send(struct.pack(">BH", HEARTBEAT, len(body)) + body)

    def caps(self):
        """GET_CAPS: (bits, server version text). Servers up to 0.5.0 answer ERROR 1."""
        r = self.call(GET_CAPS)
        bits, = struct.unpack_from(">I", r, 0)
        return bits, _blob_at(r, 4)[0].decode("utf-8", "replace")

    def create_room(self, max_members, flags=0, external=b"", internal=b"", member_data=b""):
        return parse_room_joined(self.call(
            CREATE_ROOM, struct.pack(">BI", max_members, flags) + blob(external) + blob(internal) + blob(member_data)))

    def join_room(self, room_id, member_data=b""):
        return parse_room_joined(self.call(JOIN_ROOM, struct.pack(">Q", room_id) + blob(member_data)))

    def leave_room(self, room_id):
        self.call(LEAVE_ROOM, struct.pack(">Q", room_id))

    def search_rooms(self, start=0, count=10):
        """Returns (total, rooms). The server may return fewer rooms than total."""
        p = self.call(SEARCH_ROOMS, struct.pack(">HH", start, count))
        total, n = struct.unpack_from(">HH", p, 0)
        off, rooms = 4, []
        for _ in range(n):
            rid, owner, cur, mx, flags = struct.unpack_from(">Q16sBBI", p, off)
            external, off = _blob_at(p, off + 30)
            rooms.append({"room_id": rid, "owner": _text(owner), "current": cur, "max": mx,
                          "flags": flags, "external": external})
        return total, rooms

    def set_room_data(self, room_id, which, data):
        """which: 0 external, 1 internal, 2 flags (data is then a 4-byte big-endian u32). Owner only."""
        self.call(SET_ROOM_DATA, struct.pack(">QB", room_id, which) + blob(data))

    def room_message(self, room_id, data, to=0):
        """to is a member id, or 0 for everyone else in the room."""
        self.call(ROOM_MESSAGE, struct.pack(">QH", room_id, to) + blob(data))

    def kick(self, room_id, member_id):
        self.call(KICK_MEMBER, struct.pack(">QH", room_id, member_id))

    def record_score(self, board, score, higher_is_better=True, comment=b""):
        """Returns the player's rank. order byte 1 means lower is better; anything else, higher."""
        r = self.call(RECORD_SCORE, struct.pack(">IBq", board, 0 if higher_is_better else 1, score) + blob(comment))
        return struct.unpack_from(">I", r, 0)[0]

    def get_ranking(self, board, start=1, count=10):
        """start is a 1-based rank. Returns (total, entries) with rank, online_id, score, comment, recorded_unix."""
        p = self.call(GET_RANKING, struct.pack(">IIH", board, start, count))
        total, n = struct.unpack_from(">IH", p, 0)
        off, out = 6, []
        for _ in range(n):
            rank, oid, score = struct.unpack_from(">I16sq", p, off)
            comment, off = _blob_at(p, off + 28)
            when, = struct.unpack_from(">Q", p, off)
            off += 8
            out.append({"rank": rank, "online_id": _text(oid), "score": score,
                        "comment": comment, "recorded_unix": when})
        return total, out


if __name__ == "__main__":
    import sys
    if len(sys.argv) != 4:
        sys.exit("usage: psnr_client.py HOST PORT NAME")
    with Client(sys.argv[1], int(sys.argv[2]), "RE-PSN-DEMO", sys.argv[3]) as c:
        print("user", c.user_id, "ip", c.public_ip, "flags", c.flags)
        print("rooms", c.search_rooms())
