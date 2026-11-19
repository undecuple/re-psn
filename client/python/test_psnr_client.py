"""Tests for psnr_client.py against a real server on loopback.

Set PSNR_SERVER to a built server binary, or run from a checkout with Go installed and the
test builds ./server into a temporary directory. Without either, the tests are skipped.
    python3 client/python/test_psnr_client.py
"""
import errno
import os
import shutil
import socket
import struct
import subprocess
import sys
import tempfile
import time
import unittest

HERE = os.path.dirname(os.path.abspath(__file__))
sys.path.insert(0, HERE)
import psnr_client as pc  # noqa: E402

ROOT = os.path.dirname(os.path.dirname(HERE))
COMM = "RE-PSN-TEST"


def free_port():
    with socket.socket() as s:
        s.bind(("127.0.0.1", 0))
        return s.getsockname()[1]


class ServerCase(unittest.TestCase):
    proc = None
    tmp = None

    @classmethod
    def setUpClass(cls):
        binary = os.environ.get("PSNR_SERVER")
        if not binary and shutil.which("go"):
            cls.tmp = tempfile.mkdtemp()
            binary = os.path.join(cls.tmp, "server")
            built = subprocess.run(["go", "build", "-buildvcs=false", "-o", binary, "./server"],
                                   cwd=ROOT, capture_output=True, text=True)
            if built.returncode:
                raise unittest.SkipTest("server build failed: " + built.stderr[-300:])
        if not binary:
            raise unittest.SkipTest("no PSNR_SERVER and no go on PATH")
        cls.port = free_port()
        cls.proc = subprocess.Popen([binary, "-addr", "127.0.0.1:%d" % cls.port, "-http", "", "-db", "memory"],
                                    stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
        for _ in range(100):
            try:
                socket.create_connection(("127.0.0.1", cls.port), 0.2).close()
                return
            except OSError:
                time.sleep(0.05)
        cls.tearDownClass()
        raise RuntimeError("server did not start")

    @classmethod
    def tearDownClass(cls):
        if cls.proc:
            cls.proc.terminate()
            cls.proc.wait(5)
        if cls.tmp:
            shutil.rmtree(cls.tmp, ignore_errors=True)

    def client(self, name):
        c = pc.Client("127.0.0.1", self.port, COMM, name)
        self.addCleanup(c.close)
        return c


class ClientTest(ServerCase):
    def test_hello_and_caps(self):
        c = self.client("hello_a")
        self.assertGreater(c.user_id, 0)
        self.assertEqual(c.public_ip, "127.0.0.1")
        bits, version = c.caps()
        self.assertEqual(bits & 0xF, 0xF)   # rooms, scores, room codes, peer keys are always on
        self.assertTrue(version)

    def test_same_address_takes_over_name_and_limits(self):
        # The same address with the same name (any case) is a reconnect: the old connection is closed.
        old = self.client("taken_a")
        new = self.client("TAKEN_A")
        self.assertNotEqual(old.user_id, 0)
        self.assertGreater(new.user_id, 0)
        with self.assertRaises(ConnectionError):
            old.call(pc.GET_CAPS)
        with self.assertRaises(ConnectionError):    # a second call on the dead connection, send side
            for _ in range(50):
                old.heartbeat()
                time.sleep(0.01)
        with self.assertRaises(ValueError):
            pc._name("x" * 17, 16)
        with self.assertRaises(ValueError):
            pc.blob(b"x" * 4097)

    def test_rooms_pushes_and_messages(self):
        a, b = self.client("room_a"), self.client("room_b")
        room = a.create_room(4, external=b"mode=1", internal=b"secret", member_data=b"A")
        self.assertEqual(room["my_member"], 1)
        self.assertEqual(room["owner"], 1)
        self.assertEqual([m["online_id"] for m in room["members"]], ["room_a"])
        total, rooms = b.search_rooms()
        mine = [r for r in rooms if r["room_id"] == room["room_id"]]
        self.assertEqual(len(mine), 1)
        self.assertEqual(mine[0]["external"], b"mode=1")
        self.assertGreaterEqual(total, 1)

        joined = b.join_room(room["room_id"], b"B")
        self.assertEqual(joined["internal"], b"secret")
        self.assertEqual([m["data"] for m in joined["members"]], [b"A", b"B"])
        a.heartbeat(25)
        a.set_room_data(room["room_id"], 0, b"mode=2")     # also makes a read its pushes
        a.room_message(room["room_id"], b"ping")
        a.set_room_data(room["room_id"], 2, struct.pack(">I", 7))
        b.heartbeat()
        b.caps()                      # any call drains pushes into b.pushes
        kinds = [t for t, _ in b.pop_pushes()]
        self.assertEqual(kinds, [0xA3, 0xA4, 0xA3])
        with self.assertRaises(pc.ServerError) as e:
            b.set_room_data(room["room_id"], 0, b"nope")
        self.assertEqual(e.exception.code, pc.E_NOT_OWNER)

        a.kick(room["room_id"], joined["my_member"])
        b.caps()
        self.assertIn(0xA5, [t for t, _ in b.pop_pushes()])
        a.leave_room(room["room_id"])

    def test_missing_room_is_not_found(self):
        c = self.client("missing_a")
        with self.assertRaises(pc.ServerError) as e:
            c.join_room(123456789)
        self.assertEqual(e.exception.code, pc.E_NOT_FOUND)

    def test_scores(self):
        a, b = self.client("score_a"), self.client("score_b")
        self.assertEqual(a.record_score(9, 100, comment=b"first"), 1)
        self.assertEqual(b.record_score(9, 50), 2)
        total, rows = b.get_ranking(9)
        self.assertEqual(total, 2)
        self.assertEqual([(r["rank"], r["online_id"], r["score"]) for r in rows],
                         [(1, "score_a", 100), (2, "score_b", 50)])
        self.assertEqual(rows[0]["comment"], b"first")
        # lower is better on a different board
        self.assertEqual(a.record_score(10, 30, higher_is_better=False), 1)
        self.assertEqual(b.record_score(10, 20, higher_is_better=False), 1)


class FakeSocket:
    def __init__(self, send_exc=None, recv_exc=None):
        self.send_exc, self.recv_exc, self.sent = send_exc, recv_exc, []

    def sendall(self, data):
        if self.send_exc:
            raise self.send_exc
        self.sent.append(data)

    def recv(self, n):
        raise self.recv_exc

    def close(self):
        pass


def offline_client(sock):
    c = pc.Client.__new__(pc.Client)
    c.sock, c._req, c.pushes = sock, iter(range(1, 100)), []
    return c


class OfflineTest(unittest.TestCase):
    def test_system_timeout_is_a_lost_connection(self):
        c = offline_client(FakeSocket(send_exc=OSError(errno.ETIMEDOUT, "timed out")))
        with self.assertRaises(ConnectionError):
            c.heartbeat()
        c = offline_client(FakeSocket(recv_exc=OSError(errno.ETIMEDOUT, "timed out")))
        with self.assertRaises(ConnectionError):
            c.caps()

    def test_own_timeout_stays_a_timeout(self):
        c = offline_client(FakeSocket(send_exc=socket.timeout("timed out")))
        with self.assertRaises(socket.timeout):
            c.heartbeat()
        c = offline_client(FakeSocket(recv_exc=socket.timeout("timed out")))
        with self.assertRaises(socket.timeout):
            c.caps()

    def test_heartbeat_frame_bytes(self):
        s = FakeSocket()
        c = offline_client(s)
        c.heartbeat()
        c.heartbeat(25)
        self.assertEqual(s.sent, [b"\x02\x00\x04\x00\x00\x00\x00",
                                  b"\x02\x00\x06\x00\x00\x00\x00\x00\x19"])


if __name__ == "__main__":
    unittest.main(verbosity=2)
