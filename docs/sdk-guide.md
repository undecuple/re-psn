# re-psn developer guide

A developer-first guide to the re-psn client SDK: the C client (`client/psnr.h`, `client/psnr.c`), the local trophy SDK
(`client/psnr_app.h`, `client/psnr_app.c`), the single-file Python client (`client/python/psnr_client.py`), and how they
talk to the server.

## Introduction

**Who it is for.** A developer porting a statically recompiled PS3 game who has to decide what to call, when, from which
thread, and what to do when it fails.

**What you need.** A checkout of this repository, a C compiler and make (the repository's `nix develop` shell supplies Go, a
C compiler and make), and for the Python chapters Python 3. Every program in the guide talks to a server you start yourself on
127.0.0.1.

**Scope and status.** re-psn is unofficial fan work. There is no public instance and the server has no account of its own: you run your own
server, and while you develop it listens on loopback. This guide teaches the SDK and the way it uses the wire. It does not
claim that any particular game works, and it is not a substitute for the wire references, which are listed under "Where to go
next".

**How to read it.** Teach-by-code: each chapter gives the concepts, then working recipes, then an API reference table and a
list of pitfalls. Read Part I once, in order. Use Parts II and III as recipes: later recipes build on the header
`psnr_util.h` that chapters 6 to 8 grow part by part, so keep it as you go.

**The three parts.**

- Part I, Foundations: what the pieces are, a first program, the connection lifecycle, blocking and threads, and version
  tolerance.
- Part II, Recipes: rooms, pushes, leaderboards, mapping matching-style calls to psnr calls, and the Python client.
- Part III, Networking, trophies, testing: NAT traversal, the relay, LAN discovery, sign-in passes, the local trophy SDK,
  testing, and a pre-ship checklist.

## Contents

**Part I. Foundations (chapters 1 to 5)**

1. [How the pieces fit together](#1-how-the-pieces-fit-together)
2. [Build, run, first program](#2-build-run-first-program)
3. [The connection lifecycle](#3-the-connection-lifecycle)
4. [Blocking, threads and errors](#4-blocking-threads-and-errors)
5. [Capabilities and version tolerance](#5-capabilities-and-version-tolerance)

**Part II. Recipes: rooms, pushes, scores, Python (chapters 6 to 10)**

6. [Rooms end to end](#6-rooms-end-to-end)
7. [Server pushes and how a client reads them](#7-server-pushes-and-how-a-client-reads-them)
8. [Leaderboards](#8-leaderboards)
9. [From sceNpMatching2-style calls to psnr calls](#9-from-scenpmatching2-style-calls-to-psnr-calls)
10. [The Python client, side by side with C](#10-the-python-client-side-by-side-with-c)

**Part III. Networking, trophies, testing (chapters 11 to 17)**

11. [NAT traversal from the developer side](#11-nat-traversal-from-the-developer-side)
12. [The relay](#12-the-relay)
13. [LAN discovery from the client](#13-lan-discovery-from-the-client)
14. [Sign-in passes and AUTH from the client](#14-sign-in-passes-and-auth-from-the-client)
15. [The local trophy SDK](#15-the-local-trophy-sdk)
16. [Testing your integration](#16-testing-your-integration)
17. [Troubleshooting and the checklist before you ship](#17-troubleshooting-and-the-checklist-before-you-ship)
18. [Glossary](#18-glossary)
19. [Where to go next](#19-where-to-go-next)

## 1. How the pieces fit together

**What this chapter is for.** Before you write a line of integration code you need a picture of what a re-psn server is,
what it is not, and which bytes go where. Most integration bugs come from guessing wrong about one of these.

re-psn is unofficial fan work. There is no public instance and the server has no account of its own: you run your own server, and while
you develop it listens on loopback. Nothing in this guide is a Sony protocol, and the protocol is not related to any
title's own online services.

### The model in one paragraph

A player's game opens **one TCP connection** to the server. Over it the game sends requests (create a room, search rooms,
record a score) and the server sends back replies and, unprompted, pushes (a member joined, a room message arrived). The
server keeps the **rooms** and the **leaderboards** and hands out the network addresses of the other players in a room.
The game's actual traffic (the match itself) goes **directly from player to player** over the title's own sockets. The
server carries that traffic only if you start it with `-relay`. Rooms and scores live in server memory: a server restart
empties them, and when a client disconnects, the server removes it from its rooms and the library never puts it back.

### Who talks to whom

There are four kinds of traffic. Keep them apart in your head, because the C library handles each differently.

| Kind | Between | Carried by | Who owns the socket |
|---|---|---|---|
| Control connection | your client and the server | one TCP connection; every frame is `u8 type`, `u16 length`, payload | the library (`psnr_client`) |
| UDP control packets | the title's P2P socket and the server's UDP port | datagrams that start with `PSNR` and a type byte (probe, probe reply, punch, relay, discovery) | the title; the library only builds and reads the bytes |
| Direct player traffic | player and player | whatever the title uses, to the `ip` and `p2p_port` in a room member entry | the title |
| Relay | player and player through the server | UDP relay datagrams, and relay streams that are fresh TCP connections | datagrams: the title; streams: the caller gets the socket back from `psnr_stream_connect` or `psnr_stream_accept` and closes it |

The server's UDP port has the same number as its TCP port unless you configure otherwise (the default is 36100 for both).
The library assumes that equality: `psnr_server_addr` returns the TCP address and port it connected to, and that is where
UDP packets go. A server run with a separate `-udp-addr` needs support in your adapter.

The library creates no threads and installs no signal handlers or timers. It never opens, reads, writes or closes a
socket that belongs to the title. The only sockets it makes are the control connection, the relay stream legs it hands
back to you, and one temporary UDP socket inside `psnr_discover`, which it closes before returning. If your title never
calls `psnr_connect`, the library sends nothing.

The wire is documented in [api.md](api.md#framing) (explained) and [protocol.md](protocol.md#5-connection-state-machine)
(exact). This guide teaches you to use the C client, the local trophy SDK and the Python client on top of it.

### The three names that matter

**Title (communication ID).** `comm_id` is the title's NP communication ID, for example `NPWR00860_00`. A disc or product
code is not a substitute. It is at most 12 bytes. It scopes everything: two titles never see each other's rooms or
leaderboards, and the relay permission is per title. The server requires it to be non-empty (otherwise `ERROR 1`) and checks
no character set. The examples in this guide use the made-up title `RE-PSN-DEMO`, which is scoped separately from any real
title.

**Online ID.** `online_id` is the player's name, at most 16 bytes. The server accepts an empty one and the C client lets it
through. The name is a claim, not an identity: see "What is trusted" below. Score boards key on the exact `online_id`
string, with no case folding, but the server's one-player-per-name check on connect is case-insensitive (chapter
3).

**User ID.** The server assigns a `user_id` in HELLO_ACK. It comes from a per-process counter that advances for every
accepted connection, including failed HELLOs, relay stream legs and failed attempts. User IDs are unique and nonzero but not
dense: do not use one as an array index and do not expect the first connection to get 1.

### What is trusted (and not)

- The control connection is plain TCP and the UDP packets are plain UDP. There is no encryption and the client does not
  authenticate the server.
- A name is not an identity. The server keeps no accounts, and a plain connection gives no proof of who is connecting; HELLO grants no credential of any kind.
- The token in HELLO_ACK (a random `u32` with the low bit set, so never 0 outside connect mode) is the secret that UDP
  probes, relay datagrams and stream legs present. It travels in the clear.
- Capability bits, HELLO_ACK flags and an `ERROR 1` answer to GET_CAPS can be forged by anything on the network path. Use
  them to decide what to try, never as proof of security.
- Every member of a room learns the other members' endpoints (`ip` and `p2p_port`). Any room member can also send a
  relay-shaped UDP datagram straight to your game's port, so a title that uses the relay must check the datagram's source
  with `psnr_relay_unwrap_from` (see [Relay](api.md#relay)).
- A client that sees an `ERROR` code it does not know treats it as a general failure.

See [protocol.md section 14](protocol.md#14-security-considerations) for the full list, including what is not protected.

### What the library touches

| Effect | Detail |
|---|---|
| Threads, signal handlers, timers | none |
| Title sockets | never touched |
| Process-global state | the connect policy (`psnr_set_connect_policy`) and the last HELLO refusal (`psnr_connect_error`); neither is locked |
| Windows | each `psnr_connect` and `psnr_discover` calls `WSAStartup` (version 2.2); `WSACleanup` is never called |
| POSIX signals | sends use plain `send()`, so a write to a connection the server has closed can raise `SIGPIPE` unless your title ignores it |
| Address families | IPv4 only: name resolution, connect, discovery, probe and relay helpers |
| Clocks | deadlines use a monotonic clock; the heartbeat timer uses wall-clock `time()` |

### Pitfalls

- Do not assume the server carries game traffic. Without `-relay` it does not.
- Do not assume state survives. Room membership, member IDs and request IDs all die with the connection.
- Do not treat a name or a capability bit as proof of anything.

---

## 2. Build, run, first program

**What this chapter is for.** Get a server running on loopback, get the client into your build, and run a program that
connects, asks what the server offers, lists rooms and closes.

All commands run from the repository root. The repository's `nix develop` shell supplies Go, a C compiler and make; it does
not supply CMake or pkg-config, so the routes that need them need those tools from your system.

### Start a loopback server

```sh
go build -o /tmp/re-psn-server ./server
/tmp/re-psn-server -addr 127.0.0.1:36100 -http "" -db off
```

`-addr` sets TCP and UDP together (default `:36100`); `-tcp-addr` and `-udp-addr` override each. `-http ""` leaves the status
page off, and `-db off` keeps scores in memory (`-db memory` does the same; no database backend is built). `-relay` and
`-discover` turn those features on. `-log-level debug` prints room events, which helps while you learn. Any free port works:
use a random one above 40000 if 36100 is taken, and give every example below that port.

Leave the server running in a second terminal. Stop it with Ctrl-C. Starting it fresh also empties every room and score.

### Get the client into your build

The client core is two files, `client/psnr.c` and `client/psnr.h`. `psnr.c` includes only `psnr.h`. The local trophy SDK
(`psnr_app.c` and `psnr_app.h`) is a separate, independent pair that does not include `psnr.h`; take it only if you need it.

**Compile `psnr.c` as `-std=gnu11`.** Strict `-std=c11` fails on `psnr.c`: it uses POSIX clock and network-interface
declarations, and `-D_POSIX_C_SOURCE=200809L` still fails on `IFF_BROADCAST` and `IFF_UP`. Your own files that only include
`psnr.h` are fine under `-std=c11 -Wall -Wextra -pedantic`: the header needs nothing beyond the standard headers. The header
has `extern "C"` guards for C++.

**Route 1: vendor the two files.** This is the simplest route. Copy `psnr.c` and `psnr.h` from the **same reviewed commit**
into your tree, keep the repository's `LICENSE` and the credits in `NOTICE.md`, and compile them with your game:

```sh
mkdir -p vendor
cp client/psnr.c client/psnr.h vendor/
cc -std=gnu11 -Wall -Wextra -O2 -c vendor/psnr.c -o vendor/psnr.o
cc -std=c11 -Wall -Wextra -pedantic -Ivendor first.c vendor/psnr.o -o first
```

On Windows link `ws2_32`. `psnr.c` carries a `#pragma comment(lib, ...)` for MSVC. Continuous integration compiles the
client with MSVC but does not run it, so treat Windows as compiled, not exercised:

```text
cl /W4 /std:c11 /Iclient client\psnr.c client\examples\minimal.c ws2_32.lib
```

**Route 2: the vendored files in CMake.** Adapt the paths and your existing target:

```cmake
add_library(re_psn_client STATIC vendor/re-psn/psnr.c)
target_include_directories(re_psn_client PUBLIC vendor/re-psn)
target_compile_features(re_psn_client PUBLIC c_std_11)
if(WIN32)
  target_link_libraries(re_psn_client PUBLIC ws2_32)
endif()
target_link_libraries(your_game_target PRIVATE re_psn_client)
```

**Route 3: `make install`.** This builds `libpsnr.a` (from `psnr.o` and `psnr_app.o`) and `psnr.pc`, and installs
`lib/libpsnr.a`, `include/psnr.h`, `include/psnr_app.h` and `lib/pkgconfig/psnr.pc`:

```sh
make -C client PREFIX=/tmp/psnr-prefix install
```

`DESTDIR` is honored; a `PREFIX` containing a space is not. The version in `psnr.pc` is the repository's `VERSION` file,
which names the server release: the client has no version number of its own. Be aware that `make` writes `psnr.o`,
`psnr_app.o`, `libpsnr.a` and `psnr.pc` into `client/` itself, so build in a copy if you want to keep the checkout clean.

**Route 4: CMake package.** `client/CMakeLists.txt` (CMake 3.13 or newer) builds a static target `psnr`, with the alias
`psnr::psnr`, from `psnr.c` and `psnr_app.c`. It keeps compiler extensions on (`C_STANDARD 11`, `C_EXTENSIONS ON`), exports a
public include directory, and links `ws2_32` on Windows:

```sh
cmake -S client -B /tmp/psnr-build -DCMAKE_INSTALL_PREFIX=/tmp/psnr-cmake-prefix
cmake --build /tmp/psnr-build
cmake --install /tmp/psnr-build
```

```cmake
find_package(psnr REQUIRED)
target_link_libraries(mygame PRIVATE psnr::psnr)
```

Pass `-DCMAKE_PREFIX_PATH=/tmp/psnr-cmake-prefix` when you configure your project. There is no package version file, so
`find_package(psnr 1.2 ...)` with a version is not supported. Routes 2 to 4 were not run while writing this guide (the
development shell has no CMake); route 1 and the `make` route were.

### The first program

The program below does what every integration does first, in the order it does it: connect (with a time limit), read what
the server says about itself, send one request, and close. It is the smallest useful program, and it is a real file that
compiles under strict `-std=c11 -pedantic` against `psnr.h`.

```c
/* first.c - connect, ask what the server offers, list rooms, close.
 * Build: cc -std=c11 -Wall -Wextra -pedantic -Ivendor first.c vendor/psnr.o -o first
 * Run:   ./first 127.0.0.1 PORT alice */
#include "psnr.h"

#include <inttypes.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>

int main(int argc, char** argv)
{
    if (argc != 4) {
        fprintf(stderr, "usage: %s HOST PORT NAME\n", argv[0]);
        return 2;
    }
    uint32_t user_id = 0;
    uint8_t public_ip[4] = {0};

    /* "RE-PSN-DEMO" is a made-up title; a real adapter passes the title's own ID. */
    psnr_client* c = psnr_connect_timeout(argv[1], (uint16_t)atoi(argv[2]), "RE-PSN-DEMO", argv[3],
                                          0, &user_id, public_ip, 3000);
    if (!c) {
        /* 0 here means "no HELLO refusal was recorded", not "no error". */
        fprintf(stderr, "connect failed (HELLO error code %d)\n", psnr_connect_error());
        return 1;
    }
    printf("user_id=%" PRIu32 " public_ip=%u.%u.%u.%u relay_flag=%d\n", user_id, public_ip[0], public_ip[1],
           public_ip[2], public_ip[3], psnr_relay_available(c));

    psnr_msg reply;
    int rc = psnr_call(c, PSNR_GET_CAPS, NULL, 0, &reply, 2000);
    if (rc == 1) {
        psnr_caps caps;
        if (reply.type == PSNR_CAPS && psnr_caps_parse(&reply, &caps))
            printf("caps=0x%x version=%.*s\n", (unsigned)caps.bits, (int)caps.version_len, (const char*)caps.version);
        else if (reply.type == PSNR_ERROR && reply.len == 4)
            printf("GET_CAPS refused with ERROR %" PRIu32 "\n", psnr_get32(reply.data));
        psnr_msg_free(&reply); /* ERROR replies are freed too */
    } else {
        printf("GET_CAPS: %s\n", rc == 0 ? "timed out" : "connection lost");
    }

    uint8_t body[4];
    psnr_put16(body, 0);      /* start: 0-based index into the list */
    psnr_put16(body + 2, 10); /* at most ten rooms */
    rc = psnr_call(c, PSNR_SEARCH_ROOMS, body, sizeof body, &reply, 2000);
    if (rc == 1 && reply.type == PSNR_ROOM_LIST) {
        psnr_cursor cur;
        uint16_t total;
        psnr_room room;
        if (psnr_room_list_parse(&reply, &cur, &total)) {
            printf("%u room(s) in total\n", (unsigned)total);
            while (psnr_room_next(&cur, &room))
                printf("  room %" PRIu64 " owner=%s %u/%u\n", room.room_id, room.owner, room.cur, room.max);
        }
    }
    if (rc == 1) psnr_msg_free(&reply);

    psnr_close(c);
    return 0;
}
```

Run it against your server:

```sh
./first 127.0.0.1 36100 alice
```

Against a fresh 0.9.0 server it prints this (user ids and port will differ):

```text
user_id=1 public_ip=127.0.0.1 relay_flag=0
caps=0x1f version=0.9.0
0 room(s) in total
```

Read it line by line:

- `psnr_connect_timeout` opens the TCP connection, sends HELLO and waits for HELLO_ACK, all within 3000 ms (details in the
  next chapter). It returns `NULL` on failure.
- `public_ip` is the address the server sees you at: `127.0.0.1` on loopback. For an IPv6 client the server reports
  `0.0.0.0`.
- `relay_flag=0` is bit 0 of HELLO_ACK's flags, the server's "I relay" flag. This server was started without `-relay`.
- `caps=0x1f` is bits 0 to 4: rooms, scores, room codes, peer keys and probe. Relay (bit 5) and discover (bit 6) are off
  because the server was started without those flags.
- `psnr_call` with `PSNR_GET_CAPS` and no body sends the request and waits for the reply. The reply is freed with
  `psnr_msg_free` even when it is an `ERROR`, and the code of an `ERROR` is `psnr_get32(reply.data)`.
- `SEARCH_ROOMS` has a body of `start u16` (0-based) and `max u16`. An empty list on a fresh server is normal.
- `psnr_close` sends nothing; the server notices the closed connection.

The repository also ships `client/examples/minimal.c`, which does the connect and the room search with hand-written
validation instead of `psnr_room_list_parse`.

### The Python client in thirty seconds

`client/python/psnr_client.py` is one file, standard library only. It covers HELLO, heartbeat, GET_CAPS, rooms, room data and
messages, and scores. It does not cover the relay, UDP probes, sign-in passes, peer keys, room codes or `GET_RANKING_BY_ID`.

```python
"""Connect, read what HELLO_ACK and GET_CAPS say, send one heartbeat, close.
Run from the repository root:  python3 py_basic.py 127.0.0.1 PORT alice
(psnr_client.py lives in client/python; here it is found through PYTHONPATH=client/python)."""
import sys
import psnr_client as psnr

host, port, name = sys.argv[1], int(sys.argv[2]), sys.argv[3]
try:
    # The constructor connects and sends HELLO; it raises if either fails.
    with psnr.Client(host, port, "RE-PSN-DEMO", name, p2p_port=0, timeout=5.0) as c:
        print("user_id", c.user_id, "public_ip", c.public_ip, "token", hex(c.token), "flags", c.flags)
        bits, version = c.caps()
        print("caps", hex(bits), "server version", version)
        c.heartbeat(rtt_ms=3)       # no reply to wait for
        print("pushes waiting:", c.pop_pushes())
except psnr.ServerError as e:       # the server answered ERROR
    print("refused:", e, "code", e.code)
except ConnectionError as e:        # refused, reset, or closed by the server
    print("connection lost:", e)
except TimeoutError as e:           # socket.timeout is TimeoutError on current Python
    print("no answer in time:", e)
```

With `PYTHONPATH=client/python` set, it prints the user id, the public address as a dotted string, the token, the HELLO_ACK
flags (6 on a plain 0.9.0 server) and the capabilities. `Client(...)` is also a context manager that closes the socket.

### Reading the server's side

Start the server with `-log-level debug`: it logs room events, so you can watch your client's actions land. The two
loopback tools in the repository exercise the same code you are about to write against: `python3 tools/loopback.py` runs the C
client plus the Go app client, and `python3 client/python/test_psnr_client.py` builds and starts its own loopback server.

### API reference for this chapter

| Function or macro | Returns | Fails with | Blocking |
|---|---|---|---|
| `psnr_connect_timeout(host, port, comm_id, online_id, p2p_port, &user_id, public_ip, timeout_ms)` | a `psnr_client*` | `NULL` | yes, up to `timeout_ms`; DNS is not covered |
| `psnr_call(c, type, body, len, &reply, timeout_ms)` | 1 reply in `*reply`, 0 timeout, -1 failure | see chapter 4 | yes |
| `psnr_msg_free(&m)` | nothing | never | no |
| `psnr_caps_parse(&m, &caps)` | 1 ok, 0 bad | 0 | no |
| `psnr_room_list_parse(&m, &cursor, &total)` | 1 ok, 0 bad | 0 | no |
| `psnr_room_next(&cursor, &room)` | 1 per entry, then 0 | 0 | no |
| `psnr_put16`, `psnr_put32`, `psnr_get16`, `psnr_get32` | big-endian write or read helpers | none | no |
| `psnr_close(c)` | nothing | `NULL` is allowed | no |

`out_user_id` and `out_public_ip` may be `NULL`; they are left untouched on failure.

### Pitfalls

- **Strict `-std=c11` on `psnr.c`** fails to compile. Use `-std=gnu11` for that file.
- **`make` dirties `client/`.** Build in a copy, or add `psnr.o`, `psnr_app.o`, `libpsnr.a` and `psnr.pc` to your
  ignore list.
- **Vendoring two commits' worth of files.** `psnr.c` and `psnr.h` move together; copy both from one commit.
- **`NULL` from `psnr_connect*`** is the only signal. The next chapter tells you what the process-global
  `psnr_connect_error` can and cannot tell you.

---

## 3. The connection lifecycle

**What this chapter is for.** From the first `psnr_connect_timeout` to the last `psnr_close`: what each step puts on the wire,
what can go wrong, what the server does behind your back, and how to recover.

### The model

A session has four phases. **Connect:** TCP, then HELLO (the first request, always). **Registered:** the server has recorded
your name and endpoint, answered with HELLO_ACK, and now answers every request. **Keep-alive:** the server drops a
connection that sends no frame for 90 seconds, so something must send. **Closed:** you closed it, the server closed it, or the
network did. The library never reconnects by itself. After a loss you start a new session with nothing carried over.

The exact state machine is in [protocol.md section 5](protocol.md#5-connection-state-machine). This chapter is the version
you code against.

### Connecting: `psnr_connect` and `psnr_connect_timeout`

```c
psnr_client* psnr_connect(const char* host, uint16_t port, const char* comm_id, const char* online_id,
                          uint16_t p2p_port, uint32_t* out_user_id, uint8_t out_public_ip[4]);
psnr_client* psnr_connect_timeout(/* the same, then */ int timeout_ms);
```

The arguments:

- `host` is resolved with IPv4 only, so an IPv6-only name fails. `localhost` resolves to `127.0.0.1`. Every address
  `getaddrinfo` returns is tried in order.
- `comm_id` is at most 12 bytes and `online_id` at most 16, by `strlen`. A longer one is **refused** (the function returns
  `NULL`), not truncated, and nothing is sent. A `NULL` pointer for host, `comm_id` or `online_id` also gives `NULL`. Names of
  exactly 12 and 16 bytes work.
- `p2p_port` is the UDP port your title takes peer traffic on, or 0 if it has none. The server gives it to the other
  members of a room in your member entry. Use your real bound port. Two instances on one machine need two ports.
- `out_user_id` and `out_public_ip` may be `NULL`.

Both functions set `TCP_NODELAY` and then switch the socket to non-blocking. What each one bounds:

| | `psnr_connect` | `psnr_connect_timeout` |
|---|---|---|
| DNS (`getaddrinfo`) | blocks, not bounded | blocks, **not bounded by `timeout_ms`** |
| TCP connect | blocking `connect()`, the operating system's own timeout applies | non-blocking, bounded by `timeout_ms` |
| Wait for HELLO_ACK | a fixed 5000 ms | the remainder of **one shared budget** started at entry |
| `timeout_ms <= 0` | not applicable | returns `NULL` at once |

Rule of thumb: always use `psnr_connect_timeout`, give it a numeric IPv4 host if you can (that avoids DNS), and never call
either from a frame or UI thread. A silent server (accepts TCP, never answers) makes `psnr_connect` return `NULL` after
5 seconds; `psnr_connect_timeout(..., 700)` returns after 0.7 seconds. A refused local port returns almost at once.

#### Let the host refuse a destination: the connect policy

A host that wants to control where the title may connect installs a policy before the first connect:

```c
typedef int (*psnr_connect_policy_fn)(void* user, int purpose, const char* host, const uint8_t ip[4], uint16_t port);
void psnr_set_connect_policy(psnr_connect_policy_fn fn, void* user);
```

The callback returns nonzero to allow and 0 to refuse. `purpose` is `PSNR_PURPOSE_SERVER` (1), asked once per resolved IPv4
address before the socket is made (a refused address is skipped and the next is tried; if every address is refused, connect
returns `NULL` and `psnr_connect_error()` returns `PSNR_E_POLICY`, 1000), or `PSNR_PURPOSE_STREAM` (2), asked for each relay
leg (`host` is `NULL`; a refusal returns -1 and opens nothing). A `NULL` function allows everything. It is process-wide and
unlocked: set it before the first connect, from the thread that connects, and do not call back into the library from the
callback. It does not cover `psnr_discover` or your own peer socket.

### A session helper

The complete file for this chapter is `lifecycle.c`. The fragments below are consecutive parts of it; the includes and
a `sleep_ms` helper come first. It compiles under `-std=gnu11 -Wall -Wextra -pedantic` (it uses `nanosleep`).

```c
#include "psnr.h"

#include <inttypes.h>
#include <signal.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <time.h>

static void sleep_ms(long ms)
{
    struct timespec ts = { ms / 1000, (ms % 1000) * 1000000L };
    nanosleep(&ts, NULL);
}
```

The next helper turns the dead-reason enum into text, and a small struct groups the arguments of a session:

```c
static const char* dead_text(int why)
{
    switch (why) {
    case PSNR_DEAD_NONE:        return "still up";
    case PSNR_DEAD_PEER_CLOSED: return "the server closed the connection";
    case PSNR_DEAD_IO_ERROR:    return "a read or write failed";
    case PSNR_DEAD_NOMEM:       return "out of memory";
    case PSNR_DEAD_SEND_TIMEOUT:return "a write stalled";
    default:                    return "unknown reason";
    }
}

typedef struct {
    const char* host;
    uint16_t    port;
    const char* comm_id;   /* the title */
    const char* name;      /* the online ID */
    uint16_t    p2p_port;  /* the title's UDP port, 0 if none */
} session_cfg;
```

The policy callback (here it allows loopback only) and the function that opens a session:

```c
/* The host decides where the client may connect. Only loopback is allowed here. */
static int allow_loopback(void* user, int purpose, const char* host, const uint8_t ip[4], uint16_t port)
{
    (void)user; (void)host; (void)port;
    return purpose != PSNR_PURPOSE_SERVER || ip[0] == 127;
}

/* Returns a connected client or NULL. last_error gets the HELLO refusal code, or 0. */
static psnr_client* open_session(const session_cfg* cfg, uint32_t* user_id, int* last_error)
{
    uint8_t ip[4];
    psnr_client* c = psnr_connect_timeout(cfg->host, cfg->port, cfg->comm_id, cfg->name,
                                          cfg->p2p_port, user_id, ip, 3000);
    *last_error = c ? 0 : psnr_connect_error();
    if (c) psnr_report_rtt(c, 1); /* heartbeats now carry the smallest recent round trip */
    return c;
}
```

`open_session` also turns on RTT reporting, covered below. The network half of a frame loop, `pump`, is shown with the
heartbeat. The part of `main` that produces the takeover output below:

```c
    session_cfg cfg = { argv[1], (uint16_t)atoi(argv[2]), "RE-PSN-DEMO", "Alice", 0 };
    uint32_t uid;
    int err;
    psnr_client* a = open_session(&cfg, &uid, &err);
    if (!a) { printf("first connect failed, HELLO error %d\n", err); return 1; }
    printf("A connected, user_id=%" PRIu32 " rtt=%d ms\n", uid, psnr_rtt_ms(a));

    /* The same name from the same address is a reconnect: the server retires A. */
    cfg.name = "aLICE";
    psnr_client* b = open_session(&cfg, &uid, &err);
    if (!b) { printf("second connect failed, HELLO error %d\n", err); psnr_close(a); return 1; }
    printf("B connected with the same name in other letters, user_id=%" PRIu32 "\n", uid);

    int n = 0;
    for (int i = 0; i < 100 && (n = pump(a)) >= 0; i++) sleep_ms(10);
    if (n < 0)
        printf("A is gone: %s (os error %d)\n", dead_text(psnr_dead_reason(a)), psnr_last_os_error(a));
    printf("send on A returns %" PRIu32 "\n", psnr_send(a, PSNR_GET_CAPS, NULL, 0));
    psnr_close(a);

    /* A heartbeat has no reply; it only resets the server's idle timer. */
    printf("heartbeat id=%" PRIu32 "\n", psnr_heartbeat(b));
    psnr_close(b);

    /* After a loss you start over: nothing from the old session survives. */
    cfg.name = "Alice2";
    psnr_client* c = open_session(&cfg, &uid, &err);
    printf("reconnected: %s\n", c ? "yes" : "no");
    psnr_close(c); /* NULL-safe */
```

### What HELLO and HELLO_ACK carry

HELLO is the first frame. Its body after the request id is 30 bytes: `comm_id [12]`, `online_id [16]`, `p2p_port u16`, the
names NUL-padded. HELLO_ACK's reply payload after the request id is 13 bytes: `user_id u32`, `public_ip [4]`, `token u32`,
`flags u8`. The library reads `user_id` and `public_ip` from any reply of at least 8 bytes, the token only if there are 12,
and the flags only if there are 13, so a server that sends a short ack leaves your token at 0.

The flags the C client exposes:

| Bit | Meaning | Accessor |
|---|---|---|
| 0 | the server relays | `psnr_relay_available(c)` |
| 1 | invite-only rooms exist | none |
| 2 | the server issues peer keys | none |
| 3 | a pass is needed to connect (`-require-pass connect`) | `psnr_pass_hint(c)` |

A plain 0.9.0 server sends `0x06`, or `0x07` with `-relay`. Bits 4 to 7 are reserved: ignore them. See
[protocol.md 6.1](protocol.md#61-hello-0x01) for the exact fields.

HELLO rules you can rely on:

- HELLO must be the first request. Any other request before it, including `HEARTBEAT`, `GET_CAPS`, `AUTH` and an unknown
  type, gets `ERROR 7` and the connection stays open.
- A second HELLO on the same connection gets `ERROR 1` and changes nothing.
- An empty `comm_id`, or a body that is too short, gets `ERROR 1`; the server leaves the connection open, so a HELLO may be
  retried.
- Bytes after the last field of any request are ignored (except `AUTH`). A request payload shorter than 4 bytes (no room for
  the request id) is dropped silently with no reply, though it still counts toward the request rate limit.

### Names, `ERROR 8` and takeover

The server permits **one player per `online_id` on the whole server**. It compares names case-insensitively, and the check
**ignores the title**: a connection for title B named `ALICE` collides with title A's `aLICE`. What happens next depends on
where the second connection comes from:

| Second HELLO with the same name | Result |
|---|---|
| from a different address | `ERROR 8` (name taken); the new connection is refused and nothing else changes |
| from the same address (IPv4 exact, IPv6 exact) | taken as the same player reconnecting: the server **closes the old connection** and accepts the new one |

In C, `psnr_connect*` returns `NULL` for `ERROR 8`, closes the failed connection itself, and `psnr_connect_error()` returns 8.
The old connection that was taken over sees `psnr_poll` return -1 with `PSNR_DEAD_PEER_CLOSED` and os error 0; the output of
`lifecycle.c` shows it:

```text
A connected, user_id=2 rtt=0 ms
B connected with the same name in other letters, user_id=3
A is gone: the server closed the connection (os error 0)
send on A returns 0
heartbeat id=2
reconnected: yes
```

The practical consequence: **two instances of a game on one machine, or behind one NAT address, that use the same name kick
each other**, silently and in a loop if both reconnect. Give each local instance its own name and its own `p2p_port`. On a
real `ERROR 8` do not loop on the same name: ask the player for another, or wait. A real `ERROR 8` needs a second source
address, which you can produce on one Linux machine with a raw socket bound to a second loopback address; `psnr_connect` cannot choose
its source address. The Python snippet below does exactly that.

The server does not check the character set of names. Send printable ASCII. The C decoders replace any byte outside
`0x20` to `0x7E` with `?` when they copy a name into a struct, while the Python client decodes UTF-8, so a name with high bytes
reads differently in the two. Names in decoded structs are `char[PSNR_ONLINE_ID_MAX + 1]` and always NUL-terminated; raw
name fields on the wire are not necessarily terminated. (If you want a rule for a player-facing name, the desktop app uses:
3 to 16 letters, digits, `-` and `_`. That is the app's rule, not the server's.)

### Why `psnr_connect_error()` is only a hint

```c
int psnr_connect_error(void);
```

It returns the last HELLO refusal: 0 if none, otherwise the `ERROR` code the server sent, or `PSNR_E_POLICY` when your
connect policy refused every address. It is a **process-global** that the library resets only after name resolution has
succeeded, and again just before HELLO is sent. A failure at an earlier stage therefore keeps the old value. Observed
sequence: a connect with an empty `comm_id` sets it to 1; a following connect that fails in DNS still reads 1; so does a
following connect with a 17-byte name, and `psnr_connect_timeout` with `timeout_ms <= 0` does not touch it either. A refused
TCP port and a silent server leave it at 0 (it was reset after the lookup), so it does not distinguish "never reached the
server" from "no refusal yet".

How to use it safely: read it only straight after a `NULL` return, treat 0 as "no HELLO refusal known", treat a nonzero value
as a hint for the log and the UI, and never branch a retry loop on it alone. It is not thread-safe: do not connect from two
threads at once. `psnr_discover` does not touch it.

### Heartbeat, idle time and the RTT report

The server drops a connection that sends no frame for **90 seconds**; the clock starts at accept, so it applies before HELLO
too. Any frame resets it, and a HELLO is a frame. The C client sends a HEARTBEAT from inside `psnr_poll` when 20 seconds
have passed since any successful send (any send, including your own requests, resets that timer). Three things follow:

1. The only automatic heartbeat is inside `psnr_poll`. There is no timer and no thread. A title that stops polling for 90
   seconds is dropped, and a title that only ever blocks in `psnr_call` sends no heartbeats at all.
2. `psnr_heartbeat(c)` sends one now and returns its request id, or 0 on failure. It has **no reply**. After HELLO the server
   answers nothing, so `psnr_call(c, PSNR_HEARTBEAT, ...)` always times out (300 ms for a 300 ms budget); use
   `psnr_heartbeat` or `psnr_send`. Before HELLO a heartbeat gets `ERROR 7`.
3. The server also drops a connection that sends more than 200 frames per second (`-max-req-rate`). Do not spin on
   requests.

The optional body of a heartbeat is `rtt_ms u16`, the client's last measured round trip. The client keeps it like this:
`psnr_call` times each call, from send to reply (so the server's own time is included), into a ring of eight samples,
ignoring samples above 60000 ms; `psnr_rtt_ms(c)` returns the smallest of the last eight, or -1 with no sample. The header
says "-1 before the first reply", but `psnr_connect`'s own HELLO is a timed call, so after a successful connect there is
already a sample (0 ms on loopback, as the output above shows). Replies you pick up with `psnr_poll` are not timed.

Reporting is **off by default**. `psnr_report_rtt(c, 1)` turns it on, which is recommended: then each heartbeat carries the
number, the server shows it on its status page and in `/api/stats` (`rtt_ms`, `null` when none or older than 3 minutes), and
uses it for nothing else. It is not checked and not authenticated. `psnr_report_rtt(c, 0)` stops it. A heartbeat with a
one-byte body is ignored; `0xFFFF` or any value above 60000 clears the report. Servers that do not know the field ignore the
body. See [protocol.md 6.2](protocol.md#62-heartbeat-0x02).

The pump that sends those heartbeats, from `lifecycle.c`:

```c
/* One tick of the frame loop's network part: drain everything that has arrived.
 * Returns the number of messages handled, or -1 once the connection is lost and drained. */
static int pump(psnr_client* c)
{
    psnr_msg m;
    int handled = 0, r;
    while ((r = psnr_poll(c, &m)) == 1) {
        printf("  message type=0x%02x req=%" PRIu32 " len=%" PRIu32 "\n", m.type, m.req, m.len);
        psnr_msg_free(&m);
        handled++;
    }
    return r < 0 ? -1 : handled;
}
```

### Timeouts at a glance

| What | Value | Where it applies |
|---|---|---|
| `psnr_connect` wait for HELLO_ACK | 5000 ms, fixed | after the TCP connect |
| `psnr_connect_timeout` | `timeout_ms`, one shared budget | connect and HELLO together; not DNS |
| Automatic heartbeat | 20 s of no sends | inside `psnr_poll` only |
| One write of a request (`PSNR_SEND_TIMEOUT_MS`) | 5000 ms | per write (header, then body); then `PSNR_DEAD_SEND_TIMEOUT` |
| Server idle limit | 90 s with no frame | from accept, including before HELLO |
| Server request rate | 200 frames per second | per connection, `-max-req-rate` |
| Server write of one frame to you | 10 s | a stalled reader is closed |
| Relay stream accept wait | 10 s | server side |
| Relay stream idle | 600 s | `-relay-idle` |
| Sign-in time in connect mode | 30 s from accept | `-require-pass connect` only |
| Python client | 5.0 s per socket operation | constructor argument `timeout`; no heartbeat thread |

If you override `PSNR_SEND_TIMEOUT_MS`, define it for **both** `psnr.h` and `psnr.c` (for example
`-DPSNR_SEND_TIMEOUT_MS=2000` on every compile), because the library source reads the same macro.

### Losing the connection and reconnecting

`psnr_poll` returns -1 only after the connection is lost **and** every queued message has been handed out. When it does, ask
`psnr_dead_reason(c)`:

| Reason | Meaning |
|---|---|
| `PSNR_DEAD_NONE` (0) | still up |
| `PSNR_DEAD_PEER_CLOSED` (1) | a read returned end of stream: the server restarted, closed an idle or over-rate connection, enforced a limit, or took the connection over |
| `PSNR_DEAD_IO_ERROR` (2) | a read or write failed; `psnr_last_os_error(c)` is `errno` (POSIX) or `WSAGetLastError()` (Windows) |
| `PSNR_DEAD_NOMEM` (3) | an allocation failed |
| `PSNR_DEAD_SEND_TIMEOUT` (4) | one write waited `PSNR_SEND_TIMEOUT_MS` for the socket to accept data |

The first failure sticks; later ones are ignored. `psnr_last_os_error` is 0 for a peer close and for timeouts. The server closes
**without** sending an `ERROR` in these cases (a third refused `AUTH` in a row also closes it, with the `ERROR 10` for that AUTH queued first but not guaranteed to arrive): 90 seconds idle, more than 200 frames per second, an outbound queue over 2048
frames or 128 KiB, an over-long frame before sign-in, no sign-in in time, an expired pass, too many wrong room codes, and the connection caps at accept (`-max-conns` 4096, `-max-conns-per-addr` 32, IPv6 counted as
its /64). See [protocol.md 11.1](protocol.md#111-silence-and-closes). So an unexplained `PSNR_DEAD_PEER_CLOSED` usually
means one of these or a takeover.

The recovery recipe:

1. Treat every pending request id as **unanswered, outcome unknown**: the server may have applied it before the drop.
2. Read `psnr_dead_reason` and `psnr_last_os_error` for your log.
3. `psnr_close(c)`.
4. Connect again with `psnr_connect_timeout`, with the same name or a new one, with a back-off that grows; on `ERROR 8` do
   not loop with the same name.
5. Rebuild session state from the server's replies: the new connection is in no room, and old room membership, member ids and
   request ids are gone.

Do not blindly retry `CREATE_ROOM`, `JOIN_ROOM`, `SET_ROOM_DATA`, `ROOM_MESSAGE` or `RECORD_SCORE`: check the state first. A
server restart loses all rooms and scores. A `psnr_send` that returns 0 may mean the connection is dead, or only that the
request was too long: `psnr_dead_reason` tells you which (chapter 4).

A slow client that exceeds the server's outbound limits (2048 frames or 128 KiB queued, or a 10 second write stall) is
closed, and must reconnect and read the current room state.

### Closing

```c
void psnr_close(psnr_client* c);
```

`NULL` is allowed. It closes the socket and frees the queued messages, the buffers and the client. It sends nothing, no
goodbye: the server sees the TCP close, removes you from every room and pushes `MEMBER_LEFT` to the others. Messages you
already took out of `psnr_poll` or `psnr_call` stay valid after `psnr_close`, because they are copies; free them yourself
with `psnr_msg_free`. Anything the library handed you as a socket (relay streams) and your own peer sockets are yours to
close separately. After `psnr_close` the pointer is dangling; set it to `NULL`.

### Python: the same lifecycle, with its own rules

`Client(...)` connects and sends HELLO in the constructor. A HELLO `ERROR` raises `ServerError` from the constructor, and
because `__init__` raised there is no `with` block to close the socket: **the socket stays open** until you close it or it is
collected. The failures are:

| Exception | When |
|---|---|
| `ValueError` | a name longer than 12 or 16 bytes (encoded as UTF-8), a blob over 4096 bytes, a request over 65535 bytes |
| `ServerError` (`.code`) | the server answered `ERROR`; message `server error N (name)` |
| `ConnectionError` | the connection was lost, reset or closed by the server |
| `TimeoutError` (`socket.timeout`) | the socket's own timeout elapsed |

The timeout is **per socket operation**, so a stream of pushes can keep `call()` going past it. After a timeout in the middle of
a frame the partial bytes are dropped and the stream is desynchronized: close and reconnect. `call()` silently discards
replies whose request id it is not waiting for (late replies), and files pushes (types `0xA0` to `0xAF`) in `c.pushes`.
There is **no automatic heartbeat**: call `heartbeat()` at least every 90 seconds. The client is not thread-safe.

```python
"""A client that never goes quiet for the server's 90 second idle limit.
Run:  python3 py_keepalive.py PORT SECONDS   (SECONDS is how long to stay connected)"""
import sys
import time
import psnr_client as psnr

port, run_for = int(sys.argv[1]), float(sys.argv[2])
HEARTBEAT_EVERY = 20.0          # the C client's value; anything under 90 s works

with psnr.Client("127.0.0.1", port, "RE-PSN-DEMO", "pykeep", timeout=5.0) as c:
    last = time.monotonic()
    end = last + run_for
    while time.monotonic() < end:
        now = time.monotonic()
        if now - last >= HEARTBEAT_EVERY:
            c.heartbeat()
            last = now
        # ... your own work here. Pushes only arrive while you read: call() collects them,
        # so a program that only sends heartbeats does not see them until its next call().
        time.sleep(0.5)
    print("still connected, user_id", c.user_id)
```

This script is the whole keep-alive story for Python. Below, every failure above produced on purpose, including a real
`ERROR 8` from a raw socket bound to a second loopback address:

```python
"""The failures a Python caller can see, each produced on purpose.
Run:  python3 py_errors.py PORT   (needs a loopback server on 127.0.0.1:PORT)"""
import socket
import struct
import sys
import psnr_client as psnr

port = int(sys.argv[1])

# 1. A name that does not fit is refused before anything is sent.
try:
    psnr.Client("127.0.0.1", port, "RE-PSN-DEMO", "x" * 17)
except ValueError as e:
    print("ValueError:", e)

# 2. An empty title is refused by the server with ERROR 1.
try:
    psnr.Client("127.0.0.1", port, "", "pyerr_a")
except psnr.ServerError as e:
    print("ServerError:", e.code, e)

# 3. ERROR 8 needs a second source address. 127.0.0.2 is also loopback on Linux.
first = psnr.Client("127.0.0.1", port, "RE-PSN-DEMO", "PyErrName")
raw = socket.create_connection(("127.0.0.1", port), 5.0, source_address=("127.0.0.2", 0))
hello = b"RE-PSN-DEMO".ljust(12, b"\0") + b"pyerrname".ljust(16, b"\0") + struct.pack(">H", 0)
payload = struct.pack(">I", 1) + hello
raw.sendall(struct.pack(">BH", psnr.HELLO, len(payload)) + payload)
t, n = struct.unpack(">BH", raw.recv(3, socket.MSG_WAITALL))
body = raw.recv(n, socket.MSG_WAITALL)
print("raw reply type 0x%02x, code %d" % (t, struct.unpack_from(">I", body, 4)[0]))
raw.close()

# 4. A server that accepts but never answers: the socket's own timeout fires.
silent = socket.socket()
silent.bind(("127.0.0.1", 0))
silent.listen(1)
try:
    psnr.Client("127.0.0.1", silent.getsockname()[1], "RE-PSN-DEMO", "pyerr_b", timeout=0.5)
except TimeoutError as e:
    print("TimeoutError:", e)
silent.close()

# 5. The server goes away under an open client.
c = psnr.Client("127.0.0.1", port, "RE-PSN-DEMO", "pyerr_c")
c.sock.shutdown(socket.SHUT_RDWR)   # stands in for a server that closed the connection
try:
    c.caps()
except ConnectionError as e:
    print("ConnectionError:", e)
c.close()
first.close()
```

Its output against a loopback server:

```text
ValueError: 'xxxxxxxxxxxxxxxxx' is longer than 16 bytes
ServerError: 1 server error 1 (bad request)
raw reply type 0x8f, code 8
TimeoutError: timed out
ConnectionError: connection lost: [Errno 32] Broken pipe
```

### API reference for this chapter

| Function | Returns | Fails with | Blocking |
|---|---|---|---|
| `psnr_connect(host, port, comm_id, online_id, p2p_port, &uid, ip)` | client | `NULL`; hint in `psnr_connect_error()` | yes: DNS, OS connect timeout, then 5 s HELLO wait |
| `psnr_connect_timeout(..., timeout_ms)` | client | `NULL`; `timeout_ms <= 0` is an immediate `NULL` | yes: DNS unbounded, rest bounded |
| `psnr_connect_error()` | 0, an `ERROR` code, or `PSNR_E_POLICY` | none | no |
| `psnr_set_connect_policy(fn, user)` | nothing | none | no |
| `psnr_heartbeat(c)` | request id | 0 on a failed write | may wait up to 5 s on a stalled write |
| `psnr_report_rtt(c, on)` | nothing | none | no |
| `psnr_rtt_ms(c)` | ms, or -1 with no sample | none | no |
| `psnr_dead_reason(c)` | `PSNR_DEAD_*` | none | no |
| `psnr_last_os_error(c)` | OS error or 0 | none | no |
| `psnr_relay_available(c)` | HELLO_ACK bit 0 | none | no |
| `psnr_pass_hint(c)` | HELLO_ACK bit 3 | none | no |
| `psnr_server_addr(c, ip, &port)` | nothing (fills the TCP peer address) | none | no |
| `psnr_discover(port, timeout_ms, out, max)` | servers found | -1 | yes, up to `timeout_ms` (it returns early once `max` servers answered) |
| `psnr_close(c)` | nothing | `NULL` allowed | no |

`psnr_discover` is a setup helper: it sends one 64-byte UDP broadcast to the limited broadcast address on `port` (and, on POSIX, each IPv4
interface's directed broadcast) and collects answers for up to `timeout_ms`. Port 0 means 36100. Only servers started with
`-discover` answer, only from private, carrier-grade NAT, link-local or loopback sources, and it does not cross subnets or
client-isolated Wi-Fi. Keep a manual `host:port` path next to it.

### Pitfalls

- **Reading `psnr_connect_error()` as the cause of every `NULL`.** It is sticky (see above).
- **Same name, same machine.** Two instances kick each other; one `ERROR 8` shows up only from another address.
- **A title that blocks in `psnr_call` for long stretches** sends no heartbeat and gets dropped at 90 seconds.
- **`SIGPIPE`.** Call `signal(SIGPIPE, SIG_IGN)` once on POSIX (`<signal.h>`), or a write after the server
  closed can end the process.
- **`WSAStartup` per connect on Windows**, with no `WSACleanup`. If your host runtime owns Winsock, coordinate.
- **Treating a reconnect as a resume.** It is a new session.

---

## 4. Blocking, threads and errors

**What this chapter is for.** Decide which thread owns the client, which calls may block it, how long, and how each function
tells you it failed. These are the rules that keep a title's frame loop smooth and its network code honest.

### The model

The library has no threads and no internal locks. It is **not thread-safe: one thread per `psnr_client` at a time.** Either one
thread owns the client for its whole life, or you serialize access with your own lock. The recommended structure for a game is
a single networking owner thread per client, with queues to and from the thread that runs guest code: keep potentially
blocking transport work away from timing-sensitive guest execution. Inside the owner, use the asynchronous pair
`psnr_send` and `psnr_poll`; reserve `psnr_call` for setup, tests and tools.

### Which calls block

| Call | Blocks for | Notes |
|---|---|---|
| `psnr_connect` | DNS, the OS's TCP connect timeout, then 5 s | never on a frame thread |
| `psnr_connect_timeout` | the budget, except DNS | |
| `psnr_call` | the send (up to 5 s per write) plus `timeout_ms` | `timeout_ms` is **not** a whole-operation deadline |
| `psnr_auth` | up to `timeout_ms` | covered with [sign-in passes](api.md#sign-in-passes) |
| `psnr_discover` | all of `timeout_ms` | even when it has found fewer than `max` |
| `psnr_stream_connect`, `psnr_stream_accept` | `timeout_ms` | a negative value means no deadline |
| `psnr_send`, `psnr_heartbeat` | up to 5 s per write, only if the socket will not take data | |
| `psnr_poll` | nothing on the receive path | its automatic heartbeat is a write and can wait like `psnr_send` |
| `psnr_close` | does not wait | |
| decoders, `psnr_put*`, `psnr_get*`, `psnr_probe_packet`, `psnr_punch_packet`, `psnr_relay_wrap`, `psnr_relay_unwrap`, `psnr_is_control`, `psnr_probe_reply`, `psnr_rtt_ms`, `psnr_dead_reason`, `psnr_server_addr`, `psnr_relay_available`, `psnr_pass_hint` | never | pure functions on data you already hold |

This is not a hard real-time API. There is no timer, so an idle title must poll often enough for its heartbeat (every frame,
or at least every few seconds).

### `psnr_send` and `psnr_poll`: the asynchronous pair

```c
uint32_t psnr_send(psnr_client* c, uint8_t type, const void* body, uint32_t len);
int      psnr_poll(psnr_client* c, psnr_msg* out);
```

`psnr_send` frames a request (a 7-byte header of `type`, `u16 length` and the `u32` request id, then the body), writes it and
returns the **request id**: never 0, starting at 1 and wrapping past 0. It returns 0 if the connection is already dead, if
`len` is over 65531 (`0xFFFF - 4`), or if a write fails. A too-long request returns 0 **without** marking the connection
dead (65531 bytes works, 65532 gives 0); a failed write marks it dead (`PSNR_DEAD_SEND_TIMEOUT` if one write waited 5 seconds,
else `PSNR_DEAD_IO_ERROR`) and from then on the connection is unusable, because a partial request write can desync the
stream. So after a 0, call `psnr_dead_reason(c)`: `PSNR_DEAD_NONE` means a bad argument, anything else means the connection is
gone. `body` may be `NULL` only when `len` is 0. There is no `NULL` check on `c`. Request ids are per client; the server echoes
them and does not check that they are unique. Blobs a client puts in rooms are limited to 4096 bytes by the server.

`psnr_poll` returns:

| Value | Meaning |
|---|---|
| 1 | `*out` holds the next message, any message, in arrival order: a reply, a push, or the late reply of a call that timed out |
| 0 | nothing yet |
| -1 | disconnected, and the queue is drained (`psnr_dead_reason` says why) |

It first sends a heartbeat if one is due, then does a non-blocking read and frames complete messages into a first-in
first-out queue. If the peer closes mid-read, complete frames read in the same pass are still delivered before the -1. A
message gets one queue node with no cap, so **a title that never polls grows memory without bound**. Replies that are too
short to hold a request id (outside the push types `0xA0` to `0xAF`) are silently dropped. A push has `req == 0`; everything
else carries the id of the request it answers. Room replies and pushes for one connection arrive in the order the server
applied the operations.

The pattern is: save the id from `psnr_send` in a pending table, pump `psnr_poll` until it returns 0, and for each message look
at `m.req`: 0 is a push, otherwise look the id up. Always `psnr_msg_free` the message. Late replies for requests you have given
up on will turn up; drop ids you no longer track. Several requests may be in flight at once, matched by id.

```c
/* ---- the send-and-poll pattern ---- */
typedef struct { uint32_t req; uint8_t type; } pending;
#define MAX_PENDING 16
static pending g_pending[MAX_PENDING];

static int track(uint32_t req, uint8_t type)
{
    if (!req) return 0; /* psnr_send failed; nothing to wait for */
    for (int i = 0; i < MAX_PENDING; i++)
        if (!g_pending[i].req) { g_pending[i].req = req; g_pending[i].type = type; return 1; }
    return 0;           /* table full: the caller must not send */
}

static pending* find_pending(uint32_t req)
{
    for (int i = 0; i < MAX_PENDING; i++)
        if (g_pending[i].req == req) return &g_pending[i];
    return NULL;
}

/* Returns the number of messages handled, or -1 when the connection is lost. */
static int drain(psnr_client* c)
{
    psnr_msg m;
    int handled = 0, r;
    while ((r = psnr_poll(c, &m)) == 1) {
        handled++;
        if (m.req == 0) {
            printf("  push 0x%02x (%" PRIu32 " bytes)\n", m.type, m.len);   /* unknown pushes: ignore */
        } else {
            pending* p = find_pending(m.req);
            if (!p) {
                printf("  late or unknown reply 0x%02x for req %" PRIu32 ", dropped\n", m.type, m.req);
            } else if (m.type == PSNR_ERROR && m.len == 4) {
                printf("  request 0x%02x failed: %s\n", p->type, error_text((int)psnr_get32(m.data)));
                p->req = 0;
            } else {
                printf("  request 0x%02x answered with 0x%02x\n", p->type, m.type);
                p->req = 0;
            }
        }
        psnr_msg_free(&m);
    }
    return r < 0 ? -1 : handled;
}
```

`calls.c` is the whole file for this chapter. Its `main` runs three one-off experiments and then two requests through this
table, with a third request whose id it deliberately does not track:

```c
    /* 1. A server ERROR is a reply: psnr_call returns 1 and request() turns it into a code. */
    psnr_msg m;
    uint8_t join[12] = {0};                    /* room id 0 does not exist */
    int r = request(c, PSNR_JOIN_ROOM, join, sizeof join, PSNR_ROOM_JOINED, &m, 2000);
    printf("JOIN_ROOM 0 -> %d (%s)\n", r, error_text(r));

    /* 2. A request the server never answers: the call times out, the connection stays up. */
    uint8_t rtt[2] = {0, 20};
    r = request(c, PSNR_HEARTBEAT, rtt, sizeof rtt, PSNR_OK, &m, 300);
    printf("HEARTBEAT via psnr_call -> %d (timeout is %d)\n", r, REQ_TIMEOUT);

    /* 3. Too long for one frame: 0 comes back, the connection is not marked dead. */
    static uint8_t big[65532];
    printf("psnr_send(65532 bytes) -> %" PRIu32 ", dead_reason=%d\n", psnr_send(c, PSNR_ROOM_MESSAGE, big, sizeof big),
           psnr_dead_reason(c));

    /* 4. Send, track, poll. Two requests in flight; one id is deliberately not tracked. */
    uint8_t search[4];
    psnr_put16(search, 0); psnr_put16(search + 2, 10);
    track(psnr_send(c, PSNR_SEARCH_ROOMS, search, sizeof search), PSNR_SEARCH_ROOMS);
    track(psnr_send(c, PSNR_JOIN_ROOM, join, sizeof join), PSNR_JOIN_ROOM);
    (void)psnr_send(c, PSNR_GET_CAPS, NULL, 0);  /* its reply will look like a late reply */
    for (int i = 0, got = 0; i < 100 && got < 3; i++) {
        int n = drain(c);
        if (n < 0) break;
        got += n;
        sleep_ms(10);
    }
    psnr_close(c);
```

Run against a loopback server, it prints:

```text
JOIN_ROOM 0 -> 2 (not found)
HEARTBEAT via psnr_call -> -1 (timeout is -1)
psnr_send(65532 bytes) -> 0, dead_reason=0
  request 0x11 answered with 0x91
  request 0x12 failed: not found
  late or unknown reply 0x94 for req 6, dropped
```

### `psnr_call`: the blocking form

```c
int psnr_call(psnr_client* c, uint8_t type, const void* body, uint32_t len, psnr_msg* reply, int timeout_ms);
```

It sends, then loops (read what has arrived, wait in 10 ms slices) until the reply with its request id is taken. It returns
**1** with the reply in `*reply` (free it), **0** on timeout, **-1** if the connection is dead, the send failed or `len` is
too long. Read these carefully:

- **A server `ERROR` is a normal reply.** `psnr_call` returns 1, `reply->type == PSNR_ERROR`, and the code is
  `psnr_get32(reply->data)` (the data excludes the request id, so the payload is 4 bytes). Only `psnr_auth` and `psnr_connect*`
  decode errors for you. Free the reply either way.
- **`timeout_ms` bounds the wait after the send completes**, not the whole call: the send itself can wait up to 5 s.
- **A timeout does not cancel the request.** A timed-out `CREATE_ROOM` may still have happened, and its reply will come out of
  `psnr_poll` later, with a nonzero request id you may no longer track.
- **Negative timeouts are not "forever".** `timeout_ms <= 0` is "one pass of reading, then give up"; a negative value wraps in the
  library's unsigned arithmetic and returns 0 at once. If you want to wait a long time, pass a long positive value.
- Other messages (pushes, replies to other requests) stay queued for `psnr_poll`.
- A reply that was already queued is returned even if the connection died afterwards.
- Requests the server never answers, such as `HEARTBEAT`, time out.
- Only `psnr_call` replies feed the round trip ring.

The helper that makes `psnr_call` safe to use. It turns the three outcomes and the `ERROR` reply into one integer, and frees the
reply on every failure path so the caller frees it only on success:

```c
/* ---- a blocking request helper for setup code and tools ---- */
enum { REQ_TIMEOUT = -1, REQ_DEAD = -2, REQ_BAD = -3 };

/* 0: a reply of type `want` is in *reply (free it). >0: the server's ERROR code.
 * REQ_TIMEOUT, REQ_DEAD, REQ_BAD: failures; *reply holds nothing. */
static int request(psnr_client* c, uint8_t type, const void* body, uint32_t len,
                   uint8_t want, psnr_msg* reply, int timeout_ms)
{
    int r = psnr_call(c, type, body, len, reply, timeout_ms);
    if (r == 0) return REQ_TIMEOUT;
    if (r < 0) return REQ_DEAD;
    if (reply->type == PSNR_ERROR) {
        int code = reply->len >= 4 ? (int)psnr_get32(reply->data) : REQ_BAD;
        psnr_msg_free(reply);
        return code;
    }
    if (reply->type != want) { psnr_msg_free(reply); return REQ_BAD; }
    return 0;
}
```

The same file has a table that turns codes into text (your own game will want its own wording):

```c
static const char* error_text(int code)
{
    switch (code) {
    case PSNR_E_BAD_REQUEST: return "bad request";
    case PSNR_E_NOT_FOUND:   return "not found";
    case PSNR_E_ROOM_FULL:   return "room full";
    case PSNR_E_NOT_OWNER:   return "not the owner";
    case PSNR_E_NOT_IN_ROOM: return "not in that room, or no such room";
    case PSNR_E_LIMIT:       return "a limit was reached";
    case PSNR_E_NO_HELLO:    return "no HELLO yet";
    case PSNR_E_NAME_TAKEN:  return "name taken";
    case PSNR_E_NO_RELAY:    return "no relay";
    case PSNR_E_PASS:        return "pass needed or refused";
    case PSNR_E_POLICY:      return "refused by this host's connect policy";
    default:                 return "unknown code";
    }
}
```

### How each function reports failure

| Function | Success | Failure | Where the detail is |
|---|---|---|---|
| `psnr_connect`, `psnr_connect_timeout` | client pointer | `NULL` | `psnr_connect_error()` (hint) |
| `psnr_send`, `psnr_heartbeat` | request id (nonzero) | 0 | `psnr_dead_reason` (NONE means a bad argument) |
| `psnr_call` | 1 | 0 timeout, -1 dead, bad send or too long | `ERROR` is a successful 1 with `type == PSNR_ERROR` |
| `psnr_poll` | 1 message | 0 nothing, -1 lost | `psnr_dead_reason`, `psnr_last_os_error` |
| `psnr_auth` | 0 | a positive server `ERROR` code, or negative `PSNR_AUTH_E_*` (ARG -1, IO -2, TIMEOUT -3, REPLY -4, NOMEM -5) | -3 may still have been accepted: call again |
| `psnr_stream_connect`, `psnr_stream_accept` | a socket as `int64_t` | -1 for any failure, no code | the connect policy and the server's `ERROR` are not distinguished |
| `psnr_discover` | servers found (0 or more) | -1 (also for `out == NULL`, `max <= 0`, or no send worked) | |
| `psnr_*_parse` | 1 | 0 (short, damaged, over-long) | check the reply type yourself |
| `psnr_relay_wrap` | bytes written | 0 if it does not fit | |
| `psnr_relay_unwrap`, `psnr_relay_unwrap_from` | 1 | 0 if not a relay datagram (or wrong source) | |

There is no `psnr_error_string` function; write your own table, like the one above.

### The `ERROR` codes

The wire code is the first four bytes of the `ERROR` payload after the request id. Code 0 and codes 11 and up are unassigned:
treat any unknown code as a general failure.

| Constant | Code | Where you will meet it |
|---|---|---|
| `PSNR_E_BAD_REQUEST` | 1 | malformed, short or out of range; unknown request type; a second HELLO; an empty `comm_id`; a blob over 4096 bytes; `AUTH` on a server with no pass settings |
| `PSNR_E_NOT_FOUND` | 2 | `JOIN_ROOM` with an unknown id (also another title's room, or an invite-only room you are not in); `JOIN_BY_CODE` with an unknown code; `KICK_MEMBER` for an absent member |
| `PSNR_E_ROOM_FULL` | 3 | the room is at capacity |
| `PSNR_E_NOT_OWNER` | 4 | an owner-only request from a non-owner |
| `PSNR_E_NOT_IN_ROOM` | 5 | every other room request that names an unknown room, or a room you are not in |
| `PSNR_E_LIMIT` | 6 | the server's room limit (`-max-rooms`, default 1000) or the per-player limit (`-max-player-rooms`, default 8); a room state that would not fit one `ROOM_JOINED`; a score store limit or failure |
| `PSNR_E_NO_HELLO` | 7 | any request before HELLO |
| `PSNR_E_NAME_TAKEN` | 8 | HELLO with a name held from another address |
| `PSNR_E_NO_RELAY` | 9 | a relay request to a server without `-relay` |
| `PSNR_E_PASS` | 10 | a sign-in pass is needed or was refused |
| `PSNR_E_POLICY` | 1000 | **local only**, never on the wire; appears only from `psnr_connect_error()` |

Note the asymmetry for unknown rooms: `JOIN_ROOM` answers 2, every other room request answers 5. See
[protocol.md section 11](protocol.md#11-errors) and [api.md](api.md#session) for the full table.

### Decoders are strict, and do not check the type

`psnr_room_joined_parse`, `psnr_room_list_parse`, `psnr_ranking_parse` and `psnr_caps_parse` return 0 for a short, damaged or
over-long payload, and read nothing past the end. Specifically, trailing bytes make them fail; that is why a server may not
grow those replies without a capability bit (chapter 5). Only `psnr_caps_parse` checks
`m->type`; the room, list and ranking parsers do **not**, so test `reply.type` first. `psnr_member_parse` decodes one member
(29 bytes plus a blob), for `MEMBER_JOINED` after its room id. Pointers to blobs inside a decoded struct point into the
message's `data` and die at `psnr_msg_free`: copy anything you keep.

### Message ownership

```c
typedef struct { uint8_t type; uint32_t req; uint8_t* data; uint32_t len; } psnr_msg;
```

`data` excludes the request id and is allocated with `malloc` (at least one byte, even when `len` is 0). `psnr_msg_free(&m)`
frees it and sets the pointer to `NULL`, so freeing twice is safe, but it does not reset `len`. **Free every message you
receive**: replies, `ERROR` replies, pushes and late replies. Messages stay valid after `psnr_close`.

### Reproducible experiments

Each of these was run against a loopback server. The first three, the late-reply row and the takeover row are in `calls.c`
and `lifecycle.c`; the others were run with a throwaway file built from the same two calls:

| You do | You see |
|---|---|
| `psnr_call(PSNR_JOIN_ROOM, room id 0)` | return 1, `ERROR 2` |
| `psnr_call(PSNR_HEARTBEAT, ..., 300)` | return 0 after 0.3 s, connection still up |
| `psnr_send` with 65532 bytes | 0, `psnr_dead_reason` still `PSNR_DEAD_NONE` |
| `psnr_send` with 65531 bytes | a request id |
| `psnr_send(PSNR_GET_CAPS)` and ignore the id | the reply later comes out of `psnr_poll` as an untracked reply |
| `psnr_call` with `timeout_ms` 0 | 0 immediately; the reply later comes out of `psnr_poll` |
| second HELLO via `psnr_call(PSNR_HELLO, 30 bytes)` | `ERROR 1` |
| `psnr_auth` against a server with no pass settings | positive code 1 |
| after a takeover, once `psnr_poll` has noticed the closed connection: `psnr_send` and `psnr_call` on the old client | 0 and -1 |

### Pitfalls

- **Treating `psnr_call` returning 1 as success.** It may be an `ERROR`.
- **Forgetting to free an `ERROR` reply.** It is a heap allocation like any other.
- **Using `psnr_call` in a frame loop.** It can hold the thread for the full `timeout_ms` plus a 5 s send.
- **Retrying after a timeout.** The first request may still be applied.
- **Mixing `psnr_call` and `psnr_poll`.** You will see the replies of requests you gave up on; track ids.
- **Decoding before checking `reply.type`.** Only the caps decoder checks it.
- **Sending `HEARTBEAT` with `psnr_call`.** It never has a reply.
- **Never polling.** The queue grows and the connection times out.
- **Holding blob pointers past `psnr_msg_free`.**

---

## 5. Capabilities and version tolerance

**What this chapter is for.** A server you did not compile may be older or newer than your client. Capabilities (the GET_CAPS
bits) tell you what this server offers this player, so you can switch features on and off instead of failing in the middle of
a match.

### The model

There is **no protocol version number on the wire**. HELLO carries none; the only version byte in the protocol is `AUTH`'s,
and it versions the sign-in pass format. The server's software version string comes back in `CAPS`, in the discovery reply
(cut to 12 bytes) and on `/api/stats`. The feature handshake is the capability word: after HELLO you ask `GET_CAPS`, and
the server answers `CAPS`, a `u32` of bits and a blob with its version text. The release marks in api.md ("0.3 on", "after
0.5.0") name server releases, not protocol numbers.

### The bits

| Constant | Bit | Set when |
|---|---|---|
| `PSNR_CAP_ROOMS` | 0 | always: create, search, join and leave rooms |
| `PSNR_CAP_SCORES` | 1 | always: record and read scores |
| `PSNR_CAP_ROOM_CODES` | 2 | always: invite-only rooms and join by code |
| `PSNR_CAP_PEER_KEYS` | 3 | always: `PEER_AUTH` and `PEER_KEY` pushes |
| `PSNR_CAP_PROBE` | 4 | the server answers a UDP probe from this player's P2P socket; clear for a title in `-no-peer-titles` |
| `PSNR_CAP_RELAY` | 5 | `-relay` is on and the title is not in `-no-peer-titles`; with `-require-pass relay`, only after a pass with relay scope is accepted |
| `PSNR_CAP_DISCOVER` | 6 | the server answers LAN discovery (`-discover`) |
| `PSNR_CAP_PASS_RELAY` | 7 | `-relay` plus `-require-pass relay`, and the title is not in `-no-peer-titles`: the relay needs a pass; a hint |

Bits 8 and up are reserved. A clear bit means "not offered to this player". `GET_CAPS` before HELLO gets `ERROR 7`. Under
`-require-pass connect`, before `AUTH` is accepted `GET_CAPS` shows only bit 7 (or 0) and an empty version. A plain 0.9.0 server
answered `0x1f` with the version text `0.9.0`; the Python client's `caps()` returns `(31, '0.9.0')` against it. See
[protocol.md 6.3](protocol.md#63-get_caps-0x04) and [api.md](api.md#session).

### `psnr_relay_available` is not the relay capability

`psnr_relay_available(c)` reflects HELLO_ACK bit 0, which is a per-title flag ("this server relays this title": set when the server runs `-relay` and the title is not in
`-no-peer-titles`). It is not whether **this player** may use the relay: with `-require-pass relay` bit 0 can be set while `PSNR_CAP_RELAY` is clear. Decide with the capability
bit and treat `PSNR_CAP_PASS_RELAY` as a hint that a pass is needed. Use `psnr_relay_available` only as a fallback for a server
that does not answer `GET_CAPS`.

### Writing the client

The file for this chapter is `caps.c`. It asks, remembers, ignores the bits it does not know, and has a fallback for every
feature:

```c
#define KNOWN_CAPS (PSNR_CAP_ROOMS | PSNR_CAP_SCORES | PSNR_CAP_ROOM_CODES | PSNR_CAP_PEER_KEYS | \
                    PSNR_CAP_PROBE | PSNR_CAP_RELAY | PSNR_CAP_DISCOVER | PSNR_CAP_PASS_RELAY)

typedef struct {
    int      reported;     /* 1 if the server answered GET_CAPS */
    uint32_t bits;         /* only the bits this client knows about */
    char     version[16];  /* the server's version text, may be empty */
} server_info;

/* Fills *info. Never fails: an old or silent server just leaves reported == 0.
 * Returns 0 when the connection itself is unusable. */
static int query_caps(psnr_client* c, server_info* info)
{
    psnr_msg m;
    memset(info, 0, sizeof *info);
    int r = psnr_call(c, PSNR_GET_CAPS, NULL, 0, &m, 2000);
    if (r < 0) return 0;
    if (r == 0) return 1;                       /* no answer: treat as "nothing reported" */
    if (m.type == PSNR_CAPS) {
        psnr_caps cp;
        if (psnr_caps_parse(&m, &cp)) {
            info->reported = 1;
            info->bits = cp.bits & KNOWN_CAPS;  /* ignore bits we do not know */
            size_t n = cp.version_len < sizeof info->version - 1 ? cp.version_len : sizeof info->version - 1;
            memcpy(info->version, cp.version, n);
            info->version[n] = 0;
        }
    }
    /* PSNR_ERROR with code 1 is a server that predates GET_CAPS: also "nothing reported". */
    psnr_msg_free(&m);
    return 1;
}
```

Pay attention to what the code refuses to do. It masks the bits with `KNOWN_CAPS`, so a newer server's new bits cannot
confuse a table indexed by your own bits. It treats a timeout and an `ERROR` (code 1, from a server that predates
`GET_CAPS`) alike: the server reported nothing, so `reported` stays 0. It copies the version text, which is **not**
NUL-terminated in the message, into a buffer it terminates itself.

The decisions that use it:

```c
/* What to do for one feature. `fallback` is used only when the server reported nothing. */
static int offered(const server_info* info, uint32_t cap, int fallback)
{
    return info->reported ? (info->bits & cap) != 0 : fallback;
}

    server_info info;
    if (!query_caps(c, &info)) { psnr_close(c); return 1; }
    printf("reported=%d bits=0x%02x version=\"%s\"\n", info.reported, (unsigned)info.bits, info.version);

    int use_rooms = offered(&info, PSNR_CAP_ROOMS, 1);
    int use_probe = offered(&info, PSNR_CAP_PROBE, 0);
    /* With no report, fall back to the HELLO_ACK flag for the relay (server-level only). */
    int use_relay = offered(&info, PSNR_CAP_RELAY, psnr_relay_available(c));
    printf("rooms=%d probe=%d relay=%d\n", use_rooms, use_probe, use_relay);
```

The last lines of `caps.c` send a request type this server does not know:

```c
    psnr_msg m;
    int r = psnr_call(c, 0x55, NULL, 0, &m, 2000);
    if (r == 1) {
        printf("type 0x55 -> 0x%02x code %u\n", m.type, (unsigned)psnr_get32(m.data));
        psnr_msg_free(&m);
    }
    psnr_close(c);
```

Here is the program's output on a plain 0.9.0 server:

```text
reported=1 bits=0x1f version="0.9.0"
rooms=1 probe=1 relay=0
type 0x55 -> 0x8f code 1
```

### Fallbacks for a server that reports nothing

If `reported` is 0, you know less. Here is a conservative way to think about each feature:

| Feature | When the server reports nothing |
|---|---|
| rooms and scores | assume present: they are the base of the protocol |
| relay | use `psnr_relay_available(c)` (HELLO_ACK flag, from 0.3), and be ready for `ERROR 9` or `ERROR 10` |
| UDP probe | try it; an unanswered probe is harmless, and your title must work without one |
| room codes, peer keys | try the request and treat `ERROR 1` as "not here" |
| discovery | not a connected feature; `psnr_discover` is a setup helper |

A server from 0.5.0 or earlier answers `GET_CAPS` with `ERROR 1`; the code above handles that. This path was not run for this
guide, because no such old server was available: it follows the specification.

### Rules for working across versions

These come from [protocol.md section 13](protocol.md#13-versioning-and-compatibility):

1. Send `GET_CAPS` after HELLO. Use the bits you know and ignore the rest.
2. A client **may** send a request type that an old server does not know and read `ERROR 1` as "not supported": the server
   never closes the connection for an unknown type. The last lines of `caps.c` show it: type `0x55` after HELLO gets `ERROR 1`.
   Remember that `ERROR 1` also means "malformed", so a request that you built wrongly looks the same.
3. A client **must** ignore push types in `0xA0` to `0xAF` that it does not know. The C client hands you every message; the
   `switch` in your dispatcher needs a harmless `default:`.
4. A client must ignore unknown bits in HELLO_ACK flags (bits 4 to 7 reserved), in ROUTE flags (bits 1 to 7 reserved) and in
   CAPS (8 and up).
5. Requests can grow at the end: the server ignores trailing bytes on every request except `AUTH`, so a newer client's extra
   field is harmless to an older server. Replies must **not** grow without a capability bit, because the C decoders reject
   trailing bytes. If you write a server or a test double, remember this; if you write a client, it is why your decoders
   will break on a reply that carries an extra field.
6. A heartbeat's optional round-trip body is ignored by servers that predate it, so reporting it is always safe.

### Forgery

Capability bits, the HELLO_ACK flags and an `ERROR 1` to `GET_CAPS` travel on plain TCP and can be forged by anything on the
path. They tell you what to try. They are never proof that a feature is secure, that a relay is real or that the server is
the one you meant. See [protocol.md 14.7](protocol.md#147-what-is-not-protected).

### API reference for this chapter

| Item | What it is | Blocking |
|---|---|---|
| `psnr_call(c, PSNR_GET_CAPS, NULL, 0, &m, ms)` | request; reply type `PSNR_CAPS`, or `PSNR_ERROR` | yes |
| `psnr_caps_parse(&m, &caps)` | returns 1, or 0 when short, over-long, or `m->type` is not `PSNR_CAPS`; fills `bits`, `version` and `version_len` (pointing into `m`) | no |
| `PSNR_CAP_*` | the eight bits above | |
| `psnr_relay_available(c)` | HELLO_ACK bit 0, a per-title flag | no |
| `psnr_pass_hint(c)` | HELLO_ACK bit 3: the server wants a pass to connect | no |

Python: `Client.caps()` returns `(bits, version_text)` and raises `ServerError(1)` against a server that predates the request.

### Pitfalls

- **Reading the version text as a C string.** It is not terminated; use `version_len`.
- **Treating `ERROR 1` as proof of an old server.** It can be forged, and it also means "malformed".
- **Gating on `psnr_relay_available` instead of bit 5.** Under `-require-pass relay` they differ.
- **Failing on unknown bits or pushes.** Ignore them.
- **Caching capabilities across reconnects.** A restarted server may have different flags; ask again after every HELLO.
- **Treating a clear bit as an error.** It is how the server tells you "not for this player".

## 6. Rooms end to end

### What rooms are for

A room is a small group of players of one title, kept in the server's memory, that the server tells about each
other. You use rooms for a lobby (find a game, see who is in it, chat, start), and for handing each player the
address of every other member so your game can open its own peer traffic. The server does not carry game traffic
for you in a room; room messages are for lobby chat and control only.

### The model

- A room belongs to the title of its creator (the `comm_id` sent in HELLO). Players of other titles never see it.
- The creator is member 1 and the owner. Member ids count up from 1 in join order and are not reused in that room.
- A player has no "current room" on the server. Every room request carries the room id, and one player can be in
  several rooms at once (8 by default, set by the server option `-max-player-rooms`).
- A room has three pieces of state you control: external data (shown to searchers), internal data (members only) and
  a 32-bit flags word (shown to searchers, opaque to the server). Each member also has a data blob that is set
  when the member joins.
- The server tells the members about changes with pushes, which you read with `psnr_poll` (chapter 7). The player who
  causes a change never gets a push about it, only the reply.
- Rooms live in server memory. A server restart loses all of them.

The wire layout of every message in this chapter is in [api.md](api.md#rooms) and
[protocol.md](protocol.md#7-rooms).

### There are no request builders in the C client

`psnr.h` has decoders for replies (`psnr_room_joined_parse`, `psnr_room_list_parse`, `psnr_member_parse`) but no
function that builds a CREATE_ROOM or JOIN_ROOM request. You write each body by hand with `psnr_put16`,
`psnr_put32`, `psnr_put64` and `memcpy`, and send it with `psnr_call` (blocks until the reply) or `psnr_send`
(returns a request id, you read the reply from `psnr_poll`). A blob on the wire is a `u16` length followed by the
bytes, and every number is big-endian.

Do this once, in a small header of your own, and the rest of your code stays short. Save this as `psnr_util.h`.
The recipes in this chapter and the next two are further parts of the same header; each block says
`psnr_util.h, part N`, and you paste them in order, then end the file with `#endif`.

```c
/* psnr_util.h, part 1: blobs and one checked call */
#ifndef PSNR_UTIL_H
#define PSNR_UTIL_H
#include "psnr.h"
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <time.h>

typedef struct { const void* p; uint16_t n; } blob_t;
#define STR(s)   ((blob_t){ (s), (uint16_t)strlen(s) })
#define NOBLOB   ((blob_t){ NULL, 0 })
#define BLOB_MAX 4096                    /* the server's limit for every blob in a room request */

static inline uint8_t* put_blob(uint8_t* p, blob_t b)
{
    p = psnr_put16(p, b.n);
    if (b.n) memcpy(p, b.p, b.n);
    return p + b.n;
}

/* Results of call_ok. Anything but CALL_OK leaves nothing for you to free. */
enum {
    CALL_OK      = 1,    /* the reply has type `want`; you own it, free it with psnr_msg_free */
    CALL_REFUSED = 0,    /* the server answered ERROR; its code is in *err */
    CALL_TIMEOUT = -1,   /* no reply in time. The request may still take effect on the server. */
    CALL_DEAD    = -2,   /* the connection is lost or the send failed */
    CALL_ODD     = -3    /* a reply of a type you did not expect, or a bad request built locally */
};

static inline int call_ok(psnr_client* c, uint8_t type, const void* body, uint32_t len,
                          uint8_t want, psnr_msg* r, uint32_t* err)
{
    int rc = psnr_call(c, type, body, len, r, 3000);
    *err = 0;
    if (rc == 0) return CALL_TIMEOUT;
    if (rc < 0) return CALL_DEAD;
    if (r->type == PSNR_ERROR) {           /* an ERROR is a normal reply: psnr_call returned 1 */
        *err = r->len >= 4 ? psnr_get32(r->data) : 0;
        psnr_msg_free(r);
        return CALL_REFUSED;
    }
    if (r->type != want) { psnr_msg_free(r); return CALL_ODD; }
    return CALL_OK;
}

/* The same for requests answered by a bare OK: the reply is freed for you. */
static inline int call_done(psnr_client* c, uint8_t type, const void* body, uint32_t len, uint32_t* err)
{
    psnr_msg r;
    int rc = call_ok(c, type, body, len, PSNR_OK, &r, err);
    if (rc == CALL_OK) psnr_msg_free(&r);
    return rc;
}

static inline void nap_ms(long ms)
{
    struct timespec ts = { ms / 1000, (ms % 1000) * 1000000L };
    nanosleep(&ts, NULL);
}

/* the header continues in the next parts; the last line of the file is: #endif */
```

Two details in `call_ok` matter. `psnr_call` returns 1 for a server `ERROR` too, so you must test `r->type`
before you parse anything; the parsers check lengths only, not the message type. And a timeout does not cancel the
request (see the pitfalls at the end of this chapter).

The examples use `nanosleep`, so they build on Linux and macOS. Compile them with `-std=gnu11`; strict `c11`
does not build `psnr.c`.

### Create a room

CREATE_ROOM body: `max u8`, `flags u32`, `external blob`, `internal blob`, `member_data blob`. The reply is
ROOM_JOINED, which already describes the room with you in it.

```c
/* psnr_util.h, part 2: create, join, leave */
static inline int room_create(psnr_client* c, uint8_t max, uint32_t flags,
                              blob_t ext, blob_t intl, blob_t mem, psnr_msg* r, uint32_t* err)
{
    static uint8_t body[5 + 3 * (2 + BLOB_MAX)];
    uint8_t* p = body;
    if (max == 0 || ext.n > BLOB_MAX || intl.n > BLOB_MAX || mem.n > BLOB_MAX) { *err = PSNR_E_BAD_REQUEST; return CALL_ODD; }
    *p++ = max;                               /* a bare u8 */
    p = psnr_put32(p, flags);
    p = put_blob(p, ext);
    p = put_blob(p, intl);
    p = put_blob(p, mem);
    return call_ok(c, PSNR_CREATE_ROOM, body, (uint32_t)(p - body), PSNR_ROOM_JOINED, r, err);
}

static inline int room_join(psnr_client* c, uint64_t room, blob_t mem, psnr_msg* r, uint32_t* err)
{
    static uint8_t body[8 + 2 + BLOB_MAX];
    uint8_t* p = psnr_put64(body, room);
    if (mem.n > BLOB_MAX) { *err = PSNR_E_BAD_REQUEST; return CALL_ODD; }
    p = put_blob(p, mem);
    return call_ok(c, PSNR_JOIN_ROOM, body, (uint32_t)(p - body), PSNR_ROOM_JOINED, r, err);
}

static inline int room_leave(psnr_client* c, uint64_t room, uint32_t* err)
{
    uint8_t body[8];
    psnr_put64(body, room);
    return call_done(c, PSNR_LEAVE_ROOM, body, sizeof body, err);
}
```

The `static` body buffers make these helpers non-reentrant, which matches the client itself: a `psnr_client` is
not thread-safe, so one thread owns it.

What you get back is a `psnr_msg`
holding the ROOM_JOINED payload. Decode it with `psnr_room_joined_parse` and read the members with
`psnr_member_next`:

```c
/* psnr_util.h, part 3: show a ROOM_JOINED reply */
static inline int show_room(const psnr_msg* r)
{
    psnr_room_joined rj;
    psnr_member m;
    psnr_cursor cur;
    if (r->type != PSNR_ROOM_JOINED || !psnr_room_joined_parse(r, &rj)) return 0;   /* 0: not a room reply, or damaged */
    printf("room %llu: you are member %u, the owner is member %u, %u of %u places used, flags 0x%08X\n",
           (unsigned long long)rj.room_id, rj.my_member, rj.owner, rj.members.count, rj.max, rj.flags);
    printf("  external data: %.*s\n", (int)rj.external_len, (const char*)rj.external);
    cur = rj.members;                      /* a copy, so rj.members is still unread afterwards */
    while (psnr_member_next(&cur, &m))
        printf("  member %u  %-16s%s  data=%.*s\n", m.member_id, m.online_id, m.owner ? " (owner)" : "",
               (int)m.data_len, (const char*)m.data);
    return 1;
}
```

Facts about the reply and its decoder:

- `psnr_room_joined_parse` checks every member and the trailing bytes up front. A 0 means a damaged reply, not
  "no members".
- It does not check `m->type`. An ERROR payload is 4 bytes and only fails the length check, so test the type first.
- Every pointer in `psnr_room_joined` and `psnr_member` (`external`, `internal`, `data`) points into the `psnr_msg`.
  They are valid until you call `psnr_msg_free`. Copy what you keep.
- `online_id` comes out NUL-terminated, and any byte outside `0x20`-`0x7E` is replaced by `?`.
- `ip` and `p2p_port` in a member are where you should send that member's peer traffic. The server can give two
  players different values for the same member (a public address after a UDP probe, a LAN address, or the TCP
  address plus the HELLO port as a fallback). See [protocol.md](protocol.md#74-member-endpoint).

A new room is public. Creating a room does not leave rooms you are already in: after 8 rooms (the default)
CREATE_ROOM answers ERROR 6. Always `room_leave` when a match is over.

### Search for rooms

SEARCH_ROOMS body: `start u16` (a 0-based offset into the open list) and `max u16` (the most entries you want).
The reply lists only rooms of your title that are not full and not invite-only, oldest first. There is no filter and
no sorting: you filter on external data and flags yourself.

`total` is the length of the whole open list, not of the page. A reply stops adding rooms when the next one would
pass 65535 bytes, so a page can hold fewer than `max` rooms even when more remain. Continue at `start + count`,
where `count` is what you read, not at `start + max`. A room whose entry alone does not fit is sent with an empty
external blob so the list always makes progress. Rooms can fill or close between pages, so pages are not a
snapshot.

```c
/* psnr_util.h, part 4: find an open room that is not marked started (flags bit 0) */
/* returns 1 and sets *out, 0 if there is none, -1 if the call failed (*err holds an ERROR code, or 0) */
static inline int find_room(psnr_client* c, uint64_t* out, uint32_t* err)
{
    uint16_t start = 0, total = 0;
    do {
        uint8_t body[4];
        psnr_msg r;
        psnr_cursor cur;
        psnr_room room;
        uint16_t got = 0;
        psnr_put16(body, start);
        psnr_put16(body + 2, 10);
        if (call_ok(c, PSNR_SEARCH_ROOMS, body, sizeof body, PSNR_ROOM_LIST, &r, err) != CALL_OK) return -1;
        if (!psnr_room_list_parse(&r, &cur, &total)) { psnr_msg_free(&r); return -1; }
        while (psnr_room_next(&cur, &room)) {
            got++;
            if (!(room.flags & 1u)) {            /* your own convention: bit 0 set means "started" */
                *out = room.room_id;
                psnr_msg_free(&r);
                return 1;
            }
        }
        psnr_msg_free(&r);
        if (got == 0) break;                      /* an empty page: stop, do not loop forever */
        start = (uint16_t)(start + got);
    } while (start < total);
    return 0;
}
```

`psnr_room` has `room_id`, `owner` (the owner's name, NUL-terminated), `cur`, `max`, `flags`, `external` and
`external_len`. `psnr_room_list_parse` rejects trailing bytes, and `total` may be passed as `NULL` if you do not want
it.

A room can be full or gone by the time you join it, so search and join are two steps that can disagree. Join with a
retry that searches again on ERROR 2 (gone) or ERROR 3 (full), and stops on anything else:

```c
/* psnr_util.h, part 5: search, then join, with a short retry */
/* returns 1 joined (reply in *r), 0 nothing open, -1 other failure (*err set when the server refused) */
static inline int join_any(psnr_client* c, blob_t mem, psnr_msg* r, uint32_t* err)
{
    int attempt;
    for (attempt = 0; attempt < 3; attempt++) {
        uint64_t room;
        int f = find_room(c, &room, err);
        if (f <= 0) return f;
        switch (room_join(c, room, mem, r, err)) {
        case CALL_OK: return 1;
        case CALL_REFUSED:
            if (*err == PSNR_E_NOT_FOUND || *err == PSNR_E_ROOM_FULL) continue;   /* someone was quicker: search again */
            return -1;                                                            /* e.g. 6, the limit: tell the player */
        default: return -1;
        }
    }
    return 0;
}
```

### Join and leave

JOIN_ROOM body: `room_id u64`, `member_data blob`. The reply is ROOM_JOINED with every current member, yourself
included. Everyone else gets MEMBER_JOINED.

The errors, in the order the server checks them:

| Code | When |
|---|---|
| 1 | the body is short, or the member data is over 4096 bytes |
| 2 | no such room, a room of another title, or an invite-only room you are not in (the answer never reveals that it exists) |
| (reply) | you are already a member: you get the room again, with your old member data, and nobody is told |
| 3 | the room is full |
| 6 | you are in the maximum number of rooms, or the reply would not fit 65535 bytes (you are not added; with 4096-byte member data a room holds 15 members) |

Joining a room you are already in is not an error. It is also the only way to read a room's current state again
without a push.

LEAVE_ROOM body: `room_id u64`. The reply is a bare OK. ERROR 5 means the room does not exist or you are not in it.
You get no push about your own leave. The remaining members get MEMBER_LEFT. When the last member leaves the room is
deleted and nobody is told. A lost connection, or a second HELLO with the same name from the same address that
replaces yours, runs the same leave for every room you are in, so the others get MEMBER_LEFT then too.

### Room data and flags

SET_ROOM_DATA body: `room_id u64`, `which u8`, `data blob`. Only the owner may call it.

| `which` | What it sets | Who sees it |
|---|---|---|
| 0 | external data | searchers (in ROOM_LIST), and members (in ROOM_JOINED) |
| 1 | internal data | members only (in ROOM_JOINED) |
| 2 | flags: `data` must be exactly 4 bytes, a big-endian `u32` | searchers, and members |

Every member except the caller gets a ROOM_DATA push (`room_id u64`, `which u8`, `data blob`; for `which` 2 the blob
is the 4 flag bytes). Checks run in this order: body (ERROR 1), you are in the room (ERROR 5), you are the owner
(ERROR 4), the room's reply would still fit (ERROR 6, room unchanged). A `which` above 2, a flags blob that is not 4
bytes, or a blob over 4096 bytes is ERROR 1.

Flags are yours to define. The server never reads them. A common use is bit 0 for "started" or "closed". Setting
a bit does not hide a room from `SEARCH_ROOMS`: a started room that is not full is still listed, so your search code
must filter it (as `find_room` does above), or you make the room invite-only.

```c
/* psnr_util.h, part 6: owner-only requests */
static inline int room_set(psnr_client* c, uint64_t room, uint8_t which, blob_t data, uint32_t* err)
{
    static uint8_t body[8 + 1 + 2 + BLOB_MAX];
    uint8_t* p = psnr_put64(body, room);
    if (data.n > BLOB_MAX) { *err = PSNR_E_BAD_REQUEST; return CALL_ODD; }
    *p++ = which;
    p = put_blob(p, data);
    return call_done(c, PSNR_SET_ROOM_DATA, body, (uint32_t)(p - body), err);
}

static inline int room_set_flags(psnr_client* c, uint64_t room, uint32_t flags, uint32_t* err)
{
    uint8_t f[4];
    psnr_put32(f, flags);                      /* big-endian, the way the server reads it */
    return room_set(c, room, 2, (blob_t){ f, 4 }, err);
}

static inline int room_kick(psnr_client* c, uint64_t room, uint16_t member, uint32_t* err)
{
    uint8_t body[10];
    psnr_put16(psnr_put64(body, room), member);
    return call_done(c, PSNR_KICK_MEMBER, body, sizeof body, err);
}
```

### Member data

Member data is set only when you join (CREATE_ROOM, JOIN_ROOM or JOIN_BY_CODE). The protocol has no request to
change it afterwards. If a player's state changes (ready, a loadout), send it in a ROOM_MESSAGE, or let the owner
publish it in the internal room data, or leave and rejoin (which announces the player again). Putting the changing
parts somewhere else and keeping in member data only what is fixed for the visit (a display name, a team) is the
simplest design.

### Room messages

ROOM_MESSAGE body: `room_id u64`, `to u16`, `data blob` (at most 4096 bytes). Any member may send. With `to` 0 every
member except the sender receives it; with a member id only that member does. A `to` that is not in the room, or is
yourself, still gets OK and delivers nothing. The sender never receives its own message. ERROR 5 if you are not in the
room. Recipients get ROOM_MSG: `room_id u64`, `from u16`, `to u16` (as sent, 0 for everyone), `data blob`.

Messages travel through the server's TCP connection and share the budget of 200 requests per second per connection.
They are for lobby chat and control, not gameplay traffic.

```c
/* psnr_util.h, part 7: messages and invite-only rooms */
static inline int room_say(psnr_client* c, uint64_t room, uint16_t to, blob_t data, uint32_t* err)
{
    static uint8_t body[8 + 2 + 2 + BLOB_MAX];
    uint8_t* p = psnr_put16(psnr_put64(body, room), to);
    if (data.n > BLOB_MAX) { *err = PSNR_E_BAD_REQUEST; return CALL_ODD; }
    p = put_blob(p, data);
    return call_done(c, PSNR_ROOM_MESSAGE, body, (uint32_t)(p - body), err);
}

/* mode 0 public, 1 invite-only. code receives the 8-character code (all NUL when public). */
static inline int room_set_access(psnr_client* c, uint64_t room, uint8_t mode, char code[9], uint32_t* err)
{
    uint8_t body[9];
    psnr_msg r;
    int rc;
    *psnr_put64(body, room) = mode;
    rc = call_ok(c, PSNR_SET_ROOM_ACCESS, body, sizeof body, PSNR_ROOM_ACCESS, &r, err);
    if (rc != CALL_OK) return rc;
    if (r.len != 9) { psnr_msg_free(&r); return CALL_ODD; }
    memcpy(code, r.data + 1, 8);               /* data[0] is the mode, then code[8] */
    code[8] = '\0';
    psnr_msg_free(&r);
    return CALL_OK;
}

/* any member may read the code, so anyone in the room can invite */
static inline int room_get_access(psnr_client* c, uint64_t room, char code[9], uint32_t* err)
{
    uint8_t body[8];
    psnr_msg r;
    int rc;
    psnr_put64(body, room);
    rc = call_ok(c, PSNR_GET_ROOM_ACCESS, body, sizeof body, PSNR_ROOM_ACCESS, &r, err);
    if (rc != CALL_OK) return rc;
    if (r.len != 9) { psnr_msg_free(&r); return CALL_ODD; }
    memcpy(code, r.data + 1, 8);
    code[8] = '\0';
    psnr_msg_free(&r);
    return CALL_OK;
}

static inline int room_join_code(psnr_client* c, const char* code, blob_t mem, psnr_msg* r, uint32_t* err)
{
    static uint8_t body[8 + 2 + BLOB_MAX];
    uint8_t* p = body;
    if (strlen(code) != 8 || mem.n > BLOB_MAX) { *err = PSNR_E_BAD_REQUEST; return CALL_ODD; }   /* never send a bad code */
    memcpy(p, code, 8);
    p += 8;
    p = put_blob(p, mem);
    return call_ok(c, PSNR_JOIN_BY_CODE, body, (uint32_t)(p - body), PSNR_ROOM_JOINED, r, err);
}
```

### Kick

KICK_MEMBER body: `room_id u64`, `member_id u16`. Owner only. The target gets KICKED (`room_id u64` only), sent
before the MEMBER_LEFT pushes. The target is removed from the room first, so it gets no MEMBER_LEFT. Every remaining
member, the kicker included, gets MEMBER_LEFT with the owner after the change. Errors: 4 if you are not the owner,
5 if the room is unknown or you are not in it, 2 if `member_id` is not in the room or is the owner naming itself.

Kicking is not a ban. The kicked player can send JOIN_ROOM again at once.

### Owner handoff

When the owner leaves or is disconnected, ownership passes
to the first remaining member in join order, that is the longest-standing one. Nobody is elected. The protocol has no
OWNER_CHANGED push and no request to give ownership to a chosen member. The new owner is the `owner` field of
MEMBER_LEFT, and you must update your cached owner on every MEMBER_LEFT, including the ones caused by a kick or a
disconnect (an owner cannot be kicked, but another member's kick still produces a MEMBER_LEFT).
On a fresh join you read the owner from `ROOM_JOINED.owner`, and from the `owner` flag of each member.

### Invite-only rooms and codes

SET_ROOM_ACCESS body: `room_id u64`, `mode u8` (0 public, 1 invite-only; above 1 is ERROR 1). Owner only (ERROR 4;
ERROR 5 if you are not in the room). The reply is ROOM_ACCESS: `mode u8`, `code[8]` (NUL bytes unless the mode is 1).

- Mode 1 gives the room an 8-character code and takes the room out of `SEARCH_ROOMS` and the status page. Asking
  again for mode 1 returns the same code. Mode 0 removes the code and lists the room again. The code ends with the
  room.
- The code uses the symbols `0123456789ABCDEFGHJKMNPQRSTVWXYZ` (no I, L, O or U). The server reads it
  case-insensitively and takes `O` as 0 and `I` or `L` as 1.
- No push goes to the other members when access changes; they find out only by asking. The code is not in ROOM_JOINED.
  A member reads it with GET_ROOM_ACCESS (body `room_id u64`).
- JOIN_BY_CODE body: `code[8]`, `member_data blob`. The reply is ROOM_JOINED. A malformed code (not 8 valid symbols)
  is ERROR 1; a wrong, expired, or other-title code is ERROR 2; a full room is ERROR 3; the room limit or a too big
  reply is ERROR 6.
- Joining an invite-only room by its room id is ERROR 2 unless you are already in it.

Wrong codes close connections. Each ERROR 1 (bad symbol) or ERROR 2 from JOIN_BY_CODE counts as a miss: 5 misses on
one connection, or 20 from one address (an IPv6 /64 counts as one address) within 60 seconds, close the connection. A
body too short to hold a code is not counted. A code that names a room of your title resets the connection's count,
even if the join is then refused. So a code-entry box in a title must not let the player try codes quickly, and
`room_join_code` above refuses to send a code that is not 8 characters.

### The complete lobby program

One program, two modes. `host` creates a room, waits for a guest, sets the "started" flag, makes the room
invite-only, kicks the guest and leaves. `join` finds an open room (or takes a room id), joins, sends a message and
waits until it is kicked. Save the header from this chapter as `psnr_util.h` (parts 1 to 7 in order, then `#endif`;
chapters 7 and 8 add parts 8 to 12 before the `#endif`), save the program as `lobby.c`, and build it from the
repository root:

```sh
cc -std=gnu11 -Wall -Wextra -Iclient lobby.c client/psnr.c -o lobby
```

```c
/* lobby.c */
#include "psnr_util.h"

#define COMM "RE-PSN-DEMO"

/* Print one push. m->data starts at room_id; there is no request id in a push. */
static void print_push(const psnr_msg* m)
{
    switch (m->type) {
    case PSNR_MEMBER_JOINED: {
        psnr_member mb;
        if (m->len >= 8 && psnr_member_parse(m->data + 8, m->len - 8, &mb, NULL))
            printf("joined: member %u \"%s\" owner=%u\n", mb.member_id, mb.online_id, mb.owner);
        break; }
    case PSNR_MEMBER_LEFT:
        if (m->len == 12)
            printf("left: member %u, owner now %u\n", psnr_get16(m->data + 8), psnr_get16(m->data + 10));
        break;
    case PSNR_ROOM_DATA:
        if (m->len >= 11) printf("room data which=%u, %u bytes\n", m->data[8], psnr_get16(m->data + 9));
        break;
    case PSNR_ROOM_MSG:
        if (m->len >= 14)
            printf("message from %u to %u: %.*s\n", psnr_get16(m->data + 8), psnr_get16(m->data + 10),
                   (int)psnr_get16(m->data + 12), (const char*)m->data + 14);
        break;
    case PSNR_KICKED: printf("kicked\n"); break;
    default: printf("push 0x%02X\n", m->type);
    }
}

/* Read for up to `secs` seconds, or until `want` pushes were seen (0 = never stop early). -1: connection lost. */
static int pump(psnr_client* c, int secs, int want)
{
    time_t end = time(NULL) + secs;
    int seen = 0;
    while (time(NULL) < end && (!want || seen < want)) {
        psnr_msg m;
        int r = psnr_poll(c, &m);
        if (r < 0) { printf("disconnected (reason %d)\n", psnr_dead_reason(c)); return -1; }
        if (r == 0) { nap_ms(20); continue; }
        if (m.req == 0) { print_push(&m); seen++; }   /* a push */
        psnr_msg_free(&m);                              /* free everything psnr_poll hands you */
    }
    return seen;
}

int main(int argc, char** argv)
{
    psnr_client* c;
    psnr_msg r;
    psnr_room_joined rj;
    uint32_t err;
    int ok;

    if (argc < 4) { fprintf(stderr, "usage: lobby PORT NAME host | lobby PORT NAME join [ROOM_ID]\n"); return 2; }
    c = psnr_connect_timeout("127.0.0.1", (uint16_t)atoi(argv[1]), COMM, argv[2], 0, NULL, NULL, 3000);
    if (!c) { fprintf(stderr, "connect failed (error %d)\n", psnr_connect_error()); return 1; }

    if (!strcmp(argv[3], "host")) {
        char code[9];
        if (room_create(c, 4, 0, STR("mode=1"), NOBLOB, STR("host"), &r, &err) != CALL_OK) {
            fprintf(stderr, "create failed (%u)\n", err);
            return 1;
        }
        if (!psnr_room_joined_parse(&r, &rj)) return 1;
        printf("ROOM %llu\n", (unsigned long long)rj.room_id);   /* the guest reads this */
        fflush(stdout);
        show_room(&r);
        psnr_msg_free(&r);

        pump(c, 10, 2);                                          /* wait for the join and the message */
        room_set_flags(c, rj.room_id, 1, &err);                   /* mark the room as started */
        if (room_set_access(c, rj.room_id, 1, code, &err) == CALL_OK) printf("invite code %s\n", code);
        room_kick(c, rj.room_id, 2, &err);
        pump(c, 1, 0);
        room_leave(c, rj.room_id, &err);
    } else {
        uint64_t room = argc > 4 ? strtoull(argv[4], NULL, 10) : 0;
        if (room)  ok = room_join(c, room, STR("guest"), &r, &err) == CALL_OK;
        else       ok = join_any(c, STR("guest"), &r, &err) == 1;
        if (!ok) { fprintf(stderr, "no room joined (error %u)\n", err); return 1; }
        if (!psnr_room_joined_parse(&r, &rj)) return 1;
        show_room(&r);
        psnr_msg_free(&r);
        room_say(c, rj.room_id, 0, STR("hi"), &err);
        pump(c, 5, 2);                                           /* the flags push, then KICKED */
    }
    psnr_close(c);
    return 0;
}
```

Run it against a loopback server (replace `PORT` with a free port):

```sh
go build -o re-psn-server ./server
./re-psn-server -addr 127.0.0.1:PORT -http "" -db off &
./lobby PORT hostA host &          # prints "ROOM <id>" first
./lobby PORT guestB join           # searches for the room, joins, says hi
```

The two names must differ. A second HELLO with a name that is already held, from the same address, replaces the
first connection (its `psnr_poll` then returns -1 with `PSNR_DEAD_PEER_CLOSED`).

### Reference: rooms

All requests take a `psnr_client*` from `psnr_connect` or `psnr_connect_timeout`. "Blocking" means the call waits
for the reply, at most `timeout_ms`. "Returns" is for `psnr_call`, which returns 1 (a reply, possibly an ERROR),
0 (timeout) or -1 (lost). Replies use the decoders from `psnr.h`.

| Request (`PSNR_*`) | Body after the request id | Reply | Errors | Blocking |
|---|---|---|---|---|
| `CREATE_ROOM` 0x10 | `max u8` (1-255), `flags u32`, 3 blobs | `ROOM_JOINED` (`psnr_room_joined_parse`) | 1 (max 0, blob over 4096), 6 (room limit) | `psnr_call` yes, `psnr_send` no |
| `SEARCH_ROOMS` 0x11 | `start u16`, `max u16` | `ROOM_LIST` (`psnr_room_list_parse`, `psnr_room_next`) | 1 (short body) | same |
| `JOIN_ROOM` 0x12 | `room_id u64`, `member_data blob` | `ROOM_JOINED` | 1, 2, 3, 6 | same |
| `LEAVE_ROOM` 0x13 | `room_id u64` | `OK` | 5 | same |
| `SET_ROOM_DATA` 0x14 | `room_id u64`, `which u8`, `data blob` | `OK` | 1, 5, 4, 6 | same |
| `ROOM_MESSAGE` 0x15 | `room_id u64`, `to u16`, `data blob` | `OK` | 1, 5 | same |
| `KICK_MEMBER` 0x16 | `room_id u64`, `member_id u16` | `OK` | 4, 5, 2 | same |
| `SET_ROOM_ACCESS` 0x17 | `room_id u64`, `mode u8` | `ROOM_ACCESS` (`mode u8`, `code[8]`; read by hand) | 1, 5, 4, 6 | same |
| `JOIN_BY_CODE` 0x18 | `code[8]`, `member_data blob` | `ROOM_JOINED` | 1, 2, 3, 6 | same |
| `GET_ROOM_ACCESS` 0x19 | `room_id u64` | `ROOM_ACCESS` | 1, 5 | same |

Which error a request can answer, and in which order the server checks, is in [api.md](api.md#session) (the error
table) and [protocol.md](protocol.md#11-errors). The same fault gives different codes by request: an unknown room is
ERROR 2 for JOIN_ROOM and JOIN_BY_CODE but ERROR 5 for every other room request, and ERROR 5 also means that you are
not a member, for example after you were kicked.

Decoders and helpers:

| Function | Returns | Notes |
|---|---|---|
| `psnr_room_joined_parse(msg, &rj)` | 1 ok, 0 damaged | does not check the type; pointers valid until `psnr_msg_free` |
| `psnr_member_next(&cursor, &member)` | 1 per member, then 0 | the cursor is `rj.members` |
| `psnr_member_parse(p, len, &member, &used)` | 1 ok, 0 damaged | one member entry; `used` may be `NULL` |
| `psnr_room_list_parse(msg, &cursor, &total)` | 1 ok, 0 damaged | `total` may be `NULL` |
| `psnr_room_next(&cursor, &room)` | 1 per room, then 0 | |
| `psnr_get16`, `psnr_get32`, `psnr_get64` | the value | for the parts that have no decoder |
| `psnr_put16`, `psnr_put32`, `psnr_put64` | pointer after the value | for building bodies |

Limits, in one place (values are server defaults):

| Limit | Value |
|---|---|
| each blob (external, internal, member data, message, set-data) | 4096 bytes |
| room capacity (`max`) | 1 to 255 |
| rooms on the server (`-max-rooms`) | 1000 |
| rooms per player (`-max-player-rooms`, 0 = no limit) | 8 |
| ROOM_JOINED reply | at most 65535 bytes: 26 + external + internal + (31 + data) per member |
| requests per connection | 200 per second, over that the connection is closed |
| idle connection | dropped after 90 seconds without anything sent |
| wrong invite codes | 5 per connection or 20 per address in 60 seconds, then the connection is closed |

Member ids and room ids are not user ids (a `u32` per connection) and not request ids. Room ids are `u64`: print
them with `%llu` and a cast, or `PRIu64`. Both reset when the server restarts.

### Pitfalls: rooms

- **An ERROR is a normal reply.** `psnr_call` returns 1 for it. Test `reply.type == PSNR_ERROR` first, read the code
  with `psnr_get32(reply.data)`, and free it.
- **A timeout does not cancel the request.** `psnr_call` returning 0 means "no reply yet". The room may already exist
  or you may already be in it, and the late reply comes out of `psnr_poll` later with a nonzero `req`. Do not
  blindly retry CREATE_ROOM or JOIN_ROOM: search first, or leave the room you may have made.
- **Free every message.** Every `psnr_msg` from `psnr_call` or `psnr_poll` needs `psnr_msg_free`, including errors,
  pushes and late replies.
- **Forgetting LEAVE_ROOM.** CREATE_ROOM never leaves earlier rooms. A title that creates a room per match and
  never leaves gets ERROR 6 after 8 rooms.
- **ERROR 2 versus ERROR 5.** An unknown room is 2 for joins and 5 for everything else.
- **Invite-only looks like "not found".** Joining one by id is ERROR 2, and the code is not in ROOM_JOINED or any
  push. Members must ask with GET_ROOM_ACCESS. Changing access sends no push.
- **Search does not reserve anything.** A room can fill (ERROR 3) or close (ERROR 2) between search and join.
- **Search can return fewer rooms than `max`.** Continue at `start + count`.
- **Big external data in a search.** If a room's external blob is too large for even a single-entry page, the search
  entry carries an empty blob while ROOM_JOINED still has the real data.
- **Names.** `psnr_connect` returns NULL for a `comm_id` over 12 bytes or an `online_id` over 16 bytes; nothing is
  cut. HELLO compares names case-insensitively, but score boards match the exact bytes (chapter 8).
- **SIGPIPE.** The client sends with plain `send()`. On POSIX, a write to a connection the server has closed can raise
  SIGPIPE unless your title ignores that signal. A write that stays blocked for `PSNR_SEND_TIMEOUT_MS` (5000)
  marks the connection dead with `PSNR_DEAD_SEND_TIMEOUT`.
- **The room is gone after a server restart.** Treat `psnr_poll` returning -1 as "no rooms any more" and rebuild
  (chapter 7).

## 7. Server pushes and how a client reads them

### What pushes are for

A push is a message the server sends without being asked: someone joined, someone left, the room changed, a message
arrived, you were kicked. Your lobby screen is built from the ROOM_JOINED reply (the first picture) plus the pushes
(every change after it). If you read the pushes, your picture of the room matches the server's. If you stop
reading them, it silently goes stale.

### The model

- A push is a frame of a type from 0xA0 to 0xAF with no request id. In the C client a push has `msg.req == 0`.
- You do not request pushes. The server sends them for every room you are in.
- The player who acts gets only the reply, never a push about their own action.
- The C client queues pushes and replies in arrival order. `psnr_call` takes only the reply it waits for and leaves
  pushes and other replies queued. `psnr_poll` hands them out one by one.
- Pushes only arrive while you read. A client that does not call `psnr_poll` also stops sending the automatic
  heartbeat, and the server drops a connection that sent nothing for 90 seconds.
- Ordering holds per connection: replies and pushes for one connection come in the order the server applied the
  operations. There is no ordering guarantee between two different players' connections.

### Which pushes go to whom

| Type | Name | Payload (starts at `room_id`) | Sent to |
|---|---|---|---|
| 0xA1 | `PSNR_MEMBER_JOINED` | `room_id u64`, then a member entry (29 bytes plus the data blob) | every member except the joiner |
| 0xA2 | `PSNR_MEMBER_LEFT` | `room_id u64`, `member_id u16`, `owner u16` (12 bytes; the owner after the leave) | every remaining member, for a leave, a kick (kicker included) and a disconnect; nobody when the last member leaves |
| 0xA3 | `PSNR_ROOM_DATA` | `room_id u64`, `which u8`, `data blob` | every member except the owner who set it |
| 0xA4 | `PSNR_ROOM_MSG` | `room_id u64`, `from u16`, `to u16`, `data blob` | every member except the sender, or only member `to` |
| 0xA5 | `PSNR_KICKED` | `room_id u64` (8 bytes) | the kicked member only, before the MEMBER_LEFT pushes |

Other push types exist and are not about rooms: STREAM_OFFER 0xA6 and ROUTE 0xA7 (relay) and PEER_KEY 0xA8 (peer keys).
A client should treat any type from 0xA0 to 0xAF as a push with no request id, and ignore the ones it does not know.
Your loop must not choke on them. The push table in [api.md](api.md#rooms) is the reference.

Order around a join, as the other members see it: MEMBER_JOINED first; then, only when the server runs with `-relay`,
ROUTE pushes; then PEER_KEY pushes for pairs that both sent PEER_AUTH. See
[protocol.md](protocol.md#73-order-of-messages-around-a-join). The joiner's own ROOM_JOINED reply is queued first.

### Decoding each push

Offsets are relative to `msg.data`, which excludes the request id. Check the length before every read.

| Push | Check | Fields |
|---|---|---|
| MEMBER_JOINED | `len >= 8` and `psnr_member_parse(data + 8, len - 8, &m, NULL)` | `room = psnr_get64(data)`, the member in `m` |
| MEMBER_LEFT | `len == 12` | `room = get64(data)`, `member = get16(data + 8)`, `owner = get16(data + 10)` |
| ROOM_DATA | `len >= 11` and `11 + get16(data + 9) <= len` | `which = data[8]`, length `get16(data + 9)`, bytes at `data + 11` |
| ROOM_MSG | `len >= 14` and `14 + get16(data + 12) <= len` | `from = get16(data + 8)`, `to = get16(data + 10)`, length `get16(data + 12)`, bytes at `data + 14` |
| KICKED | `len >= 8` | `room = get64(data)` |

### A state struct that stays right

The one rule: keep a small struct for each room you are in, and update it from the ROOM_JOINED reply and every push.
Because there is no owner-changed push, `owner` is updated by every MEMBER_LEFT.

```c
/* psnr_util.h, part 8: room state that follows the pushes */
#define LOBBY_MAX_MEMBERS 32

typedef struct {
    uint64_t room;
    uint16_t me, owner;
    uint32_t flags;
    int      in_room;
    int      n;
    struct { uint16_t id; char name[PSNR_ONLINE_ID_MAX + 1]; } who[LOBBY_MAX_MEMBERS];
} lobby;

static inline void lobby_from_joined(lobby* L, const psnr_room_joined* rj)
{
    psnr_cursor cur = rj->members;
    psnr_member m;
    memset(L, 0, sizeof *L);
    L->room = rj->room_id; L->me = rj->my_member; L->owner = rj->owner; L->flags = rj->flags; L->in_room = 1;
    while (psnr_member_next(&cur, &m) && L->n < LOBBY_MAX_MEMBERS) {
        L->who[L->n].id = m.member_id;
        memcpy(L->who[L->n].name, m.online_id, sizeof L->who[L->n].name);
        L->n++;
    }
}

static inline void lobby_drop(lobby* L, uint16_t id)
{
    int i;
    for (i = 0; i < L->n; i++)
        if (L->who[i].id == id) { L->who[i] = L->who[--L->n]; return; }
}

/* Apply one push (m->req == 0). Returns 1 if it changed the lobby, 0 if it was not for this room or not understood. */
static inline int lobby_on_push(lobby* L, const psnr_msg* m)
{
    if (!L->in_room || m->len < 8 || psnr_get64(m->data) != L->room) return 0;   /* another room, or too short */
    switch (m->type) {
    case PSNR_MEMBER_JOINED: {
        psnr_member mb;
        if (!psnr_member_parse(m->data + 8, m->len - 8, &mb, NULL) || L->n >= LOBBY_MAX_MEMBERS) return 0;
        L->who[L->n].id = mb.member_id;
        memcpy(L->who[L->n].name, mb.online_id, sizeof L->who[L->n].name);
        L->n++;
        return 1; }
    case PSNR_MEMBER_LEFT:
        if (m->len != 12) return 0;
        lobby_drop(L, psnr_get16(m->data + 8));
        L->owner = psnr_get16(m->data + 10);          /* the only place an owner change is announced */
        return 1;
    case PSNR_ROOM_DATA:
        if (m->len >= 11 && m->data[8] == 2 && psnr_get16(m->data + 9) == 4 && m->len >= 15) {
            L->flags = psnr_get32(m->data + 11);       /* which 2: the blob is the 4 flag bytes */
            return 1;
        }
        return 0;
    case PSNR_KICKED:
        L->in_room = 0;                                 /* you are already out; no MEMBER_LEFT will follow for you */
        return 1;
    default: return 0;                                  /* ROOM_MSG: the title handles it itself */
    }
}
```

The `PSNR_MEMBER_JOINED` case relies on `psnr_member_parse`, which validates the entry against the buffer length.
A ROOM_MSG does not change the state of the lobby, so it is left to the caller.

### The event loop

Own the client in one place, call `psnr_poll` in a loop until it returns 0 on every frame (or every tick of your
adapter), and free every message. The loop must run often enough to keep the connection alive: `psnr_poll` sends a
HEARTBEAT itself when 20 seconds have passed since the last send, and that write is the only place it can block
(up to `PSNR_SEND_TIMEOUT_MS`).

```c
/* psnr_util.h, part 9: one tick of the event loop */
/* Drains everything that is ready. Returns the number of messages handled, or -1 if the connection is lost
 * (after the queue was drained; psnr_dead_reason says why). */
static inline int lobby_tick(psnr_client* c, lobby* L,
                             void (*on_message)(const psnr_msg*, void*), void* user)
{
    int handled = 0;
    for (;;) {
        psnr_msg m;
        int r = psnr_poll(c, &m);
        if (r < 0) return -1;
        if (r == 0) return handled;
        handled++;
        if (m.req == 0) {
            lobby_on_push(L, &m);
            if (on_message && m.type == PSNR_ROOM_MSG) on_message(&m, user);
        } else {
            /* A reply to a psnr_send request, or a late reply of a psnr_call that timed out. Look it up
             * in your table of pending ids; if you have none for it, drop it. */
            fprintf(stderr, "late or unmatched reply 0x%02X for request %u\n", m.type, m.req);
        }
        psnr_msg_free(&m);
    }
}
```

`psnr_poll` is not push-only: it also returns replies to requests you sent with `psnr_send`, and late replies to
`psnr_call` requests that timed out. Always separate them by `m.req`.

### Non-blocking requests with `psnr_send`

`psnr_call` waits, which is fine for a menu and wrong for a frame loop. For a non-blocking request send it with
`psnr_send`, remember the id, and keep ticking until a message with that `req` arrives. `psnr_send` returns 0 on
failure (the connection is dead, or the body is over 65531 bytes), and it skips id 0 on wraparound. The send itself can wait
for writability, for at most `PSNR_SEND_TIMEOUT_MS` per write.

```c
/* join_async.c: join a room without psnr_call */
#include "psnr_util.h"

int main(int argc, char** argv)
{
    psnr_client* c;
    lobby L;
    uint8_t body[8 + 2];
    uint8_t* p;
    uint32_t id;
    int done = 0;
    time_t deadline;

    if (argc < 4) { fprintf(stderr, "usage: join_async PORT NAME ROOM_ID\n"); return 2; }
    c = psnr_connect_timeout("127.0.0.1", (uint16_t)atoi(argv[1]), "RE-PSN-DEMO", argv[2], 0, NULL, NULL, 3000);
    if (!c) return 1;
    memset(&L, 0, sizeof L);

    p = psnr_put64(body, strtoull(argv[3], NULL, 10));
    p = psnr_put16(p, 0);                                  /* empty member data */
    id = psnr_send(c, PSNR_JOIN_ROOM, body, (uint32_t)(p - body));
    if (!id) { fprintf(stderr, "send failed\n"); return 1; }

    deadline = time(NULL) + 5;
    while (!done && time(NULL) < deadline) {
        psnr_msg m;
        int r = psnr_poll(c, &m);
        if (r < 0) { fprintf(stderr, "lost: %d\n", psnr_dead_reason(c)); break; }
        if (r == 0) { nap_ms(20); continue; }            /* a frame loop would draw and come back */
        if (m.req == id) {                                /* our reply */
            psnr_room_joined rj;
            if (m.type == PSNR_ERROR && m.len >= 4) printf("refused: %u\n", psnr_get32(m.data));
            else if (m.type == PSNR_ROOM_JOINED && psnr_room_joined_parse(&m, &rj)) {
                lobby_from_joined(&L, &rj);
                printf("in room %llu with %d members, owner %u\n", (unsigned long long)L.room, L.n, L.owner);
            }
            done = 1;
        } else if (m.req == 0) {
            lobby_on_push(&L, &m);                        /* a push that raced the reply */
        }
        psnr_msg_free(&m);
    }
    psnr_close(c);
    return done ? 0 : 1;
}
```

### A worked example: following a lobby

This program joins a room and prints the lobby after every change, using `lobby_tick`. Start `./lobby PORT hostA host`
from chapter 6 first and run this with the printed room id; it joins as member 2. The host waits for two pushes, so
then run `./lobby PORT guestB join ROOM_ID` as well. You see the member list grow, the chat message, and the flags
change to 0x1. Then the host kicks member 2, which is this program, so it prints "no longer in the room".

```c
/* follow.c */
#include "psnr_util.h"

static void show(const lobby* L)
{
    int i;
    printf("room %llu, owner %u, me %u, flags 0x%X:", (unsigned long long)L->room, L->owner, L->me, L->flags);
    for (i = 0; i < L->n; i++) printf(" %u=%s", L->who[i].id, L->who[i].name);
    printf("\n");
}

static void on_message(const psnr_msg* m, void* user)
{
    (void)user;
    if (m->len >= 14 && (uint32_t)14 + psnr_get16(m->data + 12) <= m->len)
        printf("chat from %u: %.*s\n", psnr_get16(m->data + 8), (int)psnr_get16(m->data + 12), (const char*)m->data + 14);
}

int main(int argc, char** argv)
{
    psnr_client* c;
    psnr_msg r;
    psnr_room_joined rj;
    uint32_t err;
    lobby L;
    time_t end;

    if (argc < 4) { fprintf(stderr, "usage: follow PORT NAME ROOM_ID\n"); return 2; }
    c = psnr_connect_timeout("127.0.0.1", (uint16_t)atoi(argv[1]), "RE-PSN-DEMO", argv[2], 0, NULL, NULL, 3000);
    if (!c) return 1;
    if (room_join(c, strtoull(argv[3], NULL, 10), NOBLOB, &r, &err) != CALL_OK) { fprintf(stderr, "join: %u\n", err); return 1; }
    if (!psnr_room_joined_parse(&r, &rj)) return 1;
    lobby_from_joined(&L, &rj);
    psnr_msg_free(&r);
    show(&L);

    end = time(NULL) + 10;
    while (time(NULL) < end && L.in_room) {
        int n = lobby_tick(c, &L, on_message, NULL);
        if (n < 0) { fprintf(stderr, "connection lost (%d): reconnect and search again\n", psnr_dead_reason(c)); break; }
        if (n > 0) show(&L);
        nap_ms(50);
    }
    if (!L.in_room) printf("no longer in the room\n");
    psnr_close(c);
    return 0;
}
```

### When the connection is lost

`psnr_poll` returns -1 once the connection is dead and the queue is drained, and `psnr_dead_reason(c)` says why
(`PSNR_DEAD_PEER_CLOSED`, `PSNR_DEAD_IO_ERROR`, `PSNR_DEAD_NOMEM`, `PSNR_DEAD_SEND_TIMEOUT`). A reconnect is a new
connection, and the new connection is in no room: all rooms of the old one were left when the server noticed it.
Rebuild your state by connecting again with `psnr_connect_timeout` and searching or joining again. Nothing
reconnects by itself.

The server also closes a connection whose outbound queue grows past 2048 frames or 128 KiB, which happens to a client
that stops reading. After such a drop you must rebuild room state the same way.

```c
/* reconnect.c, the shape of the recovery in your adapter (compile-checked only) */
#include "psnr_util.h"

psnr_client* recover(psnr_client* dead, const char* comm, const char* name, uint16_t port, lobby* L)
{
    psnr_client* c;
    psnr_close(dead);                                  /* always close a dead client */
    memset(L, 0, sizeof *L);                           /* every room of the old connection is gone */
    c = psnr_connect_timeout("127.0.0.1", port, comm, name, 0, NULL, NULL, 3000);
    if (!c) fprintf(stderr, "reconnect failed (error %d)\n", psnr_connect_error());   /* 8: name still held */
    return c;                                          /* then search or join again */
}
```

### Reference: reading pushes

| Function | Returns | Blocks |
|---|---|---|
| `psnr_poll(c, &msg)` | 1 a message (reply or push) in arrival order, 0 nothing ready, -1 the connection is lost and the queue is drained | never waits for data; may block for up to `PSNR_SEND_TIMEOUT_MS` while it sends the automatic HEARTBEAT |
| `psnr_msg_free(&msg)` | nothing | no |
| `psnr_send(c, type, body, len)` | the request id, or 0 on failure | up to `PSNR_SEND_TIMEOUT_MS` per write |
| `psnr_call(c, type, body, len, &reply, ms)` | 1 reply (possibly an ERROR), 0 timeout, -1 lost | up to `ms`, polling the socket in 10 ms slices |
| `psnr_dead_reason(c)` | `PSNR_DEAD_*` | no |
| `psnr_heartbeat(c)` | request id, or 0 on failure | up to `PSNR_SEND_TIMEOUT_MS` |

### Pitfalls: pushes

- **Read often.** A title that polls only at menus gets all pushes at once and may have been dropped for silence.
- **Do not wait for a push inside `psnr_call`.** `psnr_call` leaves pushes queued; only `psnr_poll` returns them.
- **There is no owner-changed push.** Read the owner from every MEMBER_LEFT. The leaving player gets no push at all.
- **The kicker gets MEMBER_LEFT, not KICKED.** KICKED goes only to the kicked player and arrives before the others'
  MEMBER_LEFT.
- **You are in many rooms.** Each push names its `room_id`. Compare it with the room you show.
- **Access changes are silent.** A member who needs the invite code asks with GET_ROOM_ACCESS.
- **Reply order is not push order across players.** Do not assume that a push from another player has arrived
  because your own reply has.
- **Short frames.** A push shorter than its layout is a protocol fault, not a crash: check `len` as the table above does.
- **Never block the thread that polls.** Keep potentially blocking transport work away from timing-sensitive code;
  `psnr.h` says the library starts no threads and expects one thread per client.

## 8. Leaderboards

### What they are for

A leaderboard keeps each player's best score for a game mode and ranks players. The server gives you three
operations: record a score, read a page of ranks, and read the standing of named players (a friends list your
title supplies).

### The model

- A board is identified by `(title comm_id, board u32)`. Your title decides what each number means.
- Each player (matched by the exact `online_id`, capital letters included) keeps one entry: the best score so far.
- The first RECORD_SCORE on a board fixes its order for good. `order` 1 means lower is better (lap times); any other
  value means higher is better. Later calls' `order` bytes are ignored, with no error. On PSN the order is
  configuration that lives on the server and the title does not send it; here the title must pass the order it knows
  on every call, the same every time, because whoever records first decides.
- Only a strictly better score replaces a player's entry (with a new timestamp and comment). A worse score changes
  nothing and its comment is thrown away, but you still get the standing rank back. An equal score keeps the old entry
  and its timestamp, so ties go to whoever set the score first.
- Scores are signed 64-bit integers. The comment is at most 64 bytes, a limit far below the 4096 of rooms.
- Boards live in server memory. A restart empties them.

Wire layout: [api.md](api.md#leaderboards), [protocol.md](protocol.md#8-scores).

### Record a score

RECORD_SCORE body: `board u32`, `order u8`, `score s64` (two's complement; cast to `uint64_t` for `psnr_put64`),
`comment blob`. The reply is SCORE_RECORDED with one field, `rank u32` (1 is best): your rank after recording. If your
new score was worse, the old best stands and the rank is the old one. There is no C decoder for it; read
`psnr_get32(reply.data)`.

```c
/* psnr_util.h, part 10: scores */
enum { ORDER_HIGHER_IS_BETTER = 0, ORDER_LOWER_IS_BETTER = 1 };

/* comment: at most 64 bytes. On CALL_OK, *rank is your rank on the board (1 = best). */
static inline int score_record(psnr_client* c, uint32_t board, uint8_t order, int64_t score,
                               blob_t comment, uint32_t* rank, uint32_t* err)
{
    uint8_t body[4 + 1 + 8 + 2 + 64];
    uint8_t* p = psnr_put32(body, board);
    psnr_msg r;
    int rc;
    if (comment.n > 64) { *err = PSNR_E_BAD_REQUEST; return CALL_ODD; }
    *p++ = order;
    p = psnr_put64(p, (uint64_t)score);
    p = put_blob(p, comment);
    rc = call_ok(c, PSNR_RECORD_SCORE, body, (uint32_t)(p - body), PSNR_SCORE_RECORDED, &r, err);
    if (rc != CALL_OK) return rc;
    if (r.len != 4) { psnr_msg_free(&r); return CALL_ODD; }
    *rank = psnr_get32(r.data);
    psnr_msg_free(&r);
    return CALL_OK;
}
```

An ERROR 1 means the comment is over 64 bytes. ERROR 6 means a limit would be passed (1000 titles, 1000 boards per
title, 10000 players per board), or the score store failed or timed out; a player who is already on a full board can
still improve. The server code is the same for a limit and a failure, so do not retry a limit blindly.

### Read a page of ranks

GET_RANKING body: `board u32`, `start u32` (a 1-based rank, not an offset; 0 is ERROR 1), `count u16` (at most 100;
more is ERROR 1, it is not clamped). The reply is RANKING: `total u32` (the number of players on the board), `count u16`,
then rows of `rank u32`, `online_id[16]`, `score s64`, `comment blob`, `recorded_unix u64`. A `start` past the end, a
`count` of 0 or an unknown board gives a RANKING with zero rows (and total 0 for an unknown board), never an error.

Rank numbers are `start` plus the row index, so tied scores get consecutive ranks and not a shared rank. Page by
asking again with `start` advanced by the rows you read.

```c
/* psnr_util.h, part 11: read rankings */
/* Prints up to `count` rows from rank `start`. Returns the number of rows, or -1 on failure. */
static inline int score_top(psnr_client* c, uint32_t board, uint32_t start, uint16_t count, uint32_t* err)
{
    uint8_t body[10];
    psnr_msg r;
    psnr_cursor cur;
    psnr_rank row;
    uint32_t total = 0;
    int rows = 0;
    psnr_put16(psnr_put32(psnr_put32(body, board), start), count);
    if (call_ok(c, PSNR_GET_RANKING, body, sizeof body, PSNR_RANKING, &r, err) != CALL_OK) return -1;
    if (!psnr_ranking_parse(&r, &cur, &total)) { psnr_msg_free(&r); return -1; }
    while (psnr_rank_next(&cur, &row)) {
        printf("%3u  %-16s %12lld  %.*s  (recorded at unix time %llu)\n", row.rank, row.online_id, (long long)row.score,
               (int)row.comment_len, (const char*)row.comment, (unsigned long long)row.recorded_unix);
        rows++;
    }
    printf("(%u players on the board)\n", total);
    psnr_msg_free(&r);
    return rows;
}

/* "Around me": the rank from score_record is 1-based, so start two ranks above it (never below 1). */
static inline int score_around(psnr_client* c, uint32_t board, uint32_t my_rank, uint32_t* err)
{
    return score_top(c, board, my_rank > 2 ? my_rank - 2 : 1, 5, err);
}
```

The protocol has no "around me" request; this is two calls (the rank from RECORD_SCORE, then a range).
`psnr_ranking_parse` validates every row and the trailing bytes, so a 0 means damage. Pointers in a `psnr_rank`
(`comment`) point into the message until `psnr_msg_free`.

### Read the standing of named players

GET_RANKING_BY_ID body: `board u32`, `n u16` (at most 100; more is ERROR 1), then `n` names of 16 bytes each, NUL-padded
and matched in exact case. Fewer than `n` names in the body is ERROR 1. The reply is the same RANKING layout, with one
row per requested name in request order (not rank order), `count` equal to `n`, and `total` the number of players on
the board. A name with no score comes back with rank 0, score 0, an empty comment and time 0. Test `rank == 0` for
"no score": a real score of 0 has rank 1 or more.

```c
/* psnr_util.h, part 12: ranks of a list of names */
static inline int score_by_names(psnr_client* c, uint32_t board, const char* const* names, uint16_t n, uint32_t* err)
{
    static uint8_t body[4 + 2 + 100 * 16];
    uint8_t* p;
    psnr_msg r;
    psnr_cursor cur;
    psnr_rank row;
    uint16_t i;
    if (n > 100) { *err = PSNR_E_BAD_REQUEST; return CALL_ODD; }
    p = psnr_put16(psnr_put32(body, board), n);
    for (i = 0; i < n; i++) {
        size_t len = strlen(names[i]);
        if (len > PSNR_ONLINE_ID_MAX) { *err = PSNR_E_BAD_REQUEST; return CALL_ODD; }
        memset(p, 0, 16);                          /* NUL padding */
        memcpy(p, names[i], len);
        p += 16;
    }
    if (call_ok(c, PSNR_GET_RANKING_BY_ID, body, (uint32_t)(p - body), PSNR_RANKING, &r, err) != CALL_OK) return CALL_REFUSED;
    if (!psnr_ranking_parse(&r, &cur, NULL)) { psnr_msg_free(&r); return CALL_ODD; }
    while (psnr_rank_next(&cur, &row)) {
        if (row.rank == 0) printf("%-16s no score\n", row.online_id);
        else               printf("%-16s rank %u, score %lld\n", row.online_id, row.rank, (long long)row.score);
    }
    psnr_msg_free(&r);
    return CALL_OK;
}
```

(A `NULL` `total` is accepted by `psnr_ranking_parse`.)

### A complete example: the first score fixes the order

This program records scores for two players on a board and shows the order rule and the tie rule. Build it as
`scores.c` the way `lobby.c` was built (`cc -std=gnu11 -Wall -Wextra -Iclient scores.c client/psnr.c -o scores`).

```c
/* scores.c */
#include "psnr_util.h"

int main(int argc, char** argv)
{
    const uint32_t board = 7;
    psnr_client *a, *b;
    uint32_t rank, err;
    const char* names[3] = { "ada", "bob", "nobody" };

    if (argc < 2) { fprintf(stderr, "usage: scores PORT\n"); return 2; }
    a = psnr_connect_timeout("127.0.0.1", (uint16_t)atoi(argv[1]), "RE-PSN-DEMO", "ada", 0, NULL, NULL, 3000);
    b = psnr_connect_timeout("127.0.0.1", (uint16_t)atoi(argv[1]), "RE-PSN-DEMO", "bob", 0, NULL, NULL, 3000);
    if (!a || !b) return 1;

    /* the first record on the board says "lower is better" (a lap time in ms), and that stays */
    if (score_record(a, board, ORDER_LOWER_IS_BETTER, 100, STR("lap 1"), &rank, &err) == CALL_OK) printf("ada 100: rank %u\n", rank);
    if (score_record(a, board, ORDER_LOWER_IS_BETTER, 120, STR("worse"), &rank, &err) == CALL_OK) printf("ada 120: rank %u (old best stands)\n", rank);
    /* bob passes the other order: it is ignored, the board stays "lower is better" */
    if (score_record(b, board, ORDER_HIGHER_IS_BETTER, 90, STR("bob"), &rank, &err) == CALL_OK) printf("bob 90: rank %u\n", rank);
    if (score_record(a, board, ORDER_LOWER_IS_BETTER, 90, STR("tie"), &rank, &err) == CALL_OK) printf("ada 90: rank %u (ties go to the earlier)\n", rank);

    score_top(a, board, 1, 10, &err);
    score_by_names(a, board, names, 3, &err);
    psnr_close(a);
    psnr_close(b);
    return 0;
}
```

### Reference: scores

| Request | Body after the request id | Reply | Errors | Notes |
|---|---|---|---|---|
| `PSNR_RECORD_SCORE` 0x20 | `board u32`, `order u8`, `score s64`, `comment blob` | `PSNR_SCORE_RECORDED`: `rank u32` (read with `psnr_get32`) | 1 (comment over 64 bytes), 6 | the first call on a board fixes its order |
| `PSNR_GET_RANKING` 0x21 | `board u32`, `start u32` (1-based rank), `count u16` (at most 100) | `PSNR_RANKING` (`psnr_ranking_parse`, `psnr_rank_next`) | 1 (start 0, count over 100) | out of range gives zero rows |
| `PSNR_GET_RANKING_BY_ID` 0x22 | `board u32`, `n u16`, `n` x `online_id[16]` | `PSNR_RANKING`, `n` rows in request order | 1 (n over 100, or too few names) | rank 0 means no score |

All three go through `psnr_call` (blocking) or `psnr_send` (the reply then comes out of `psnr_poll`). They share the
200 requests per second per connection.

| Decoder | Returns |
|---|---|
| `psnr_ranking_parse(msg, &cursor, &total)` | 1 ok, 0 damaged; `total` may be `NULL` |
| `psnr_rank_next(&cursor, &row)` | 1 per row, then 0; `row` has `rank`, `online_id[17]`, `score` (`int64_t`), `comment`, `comment_len`, `recorded_unix` (`uint64_t`) |

### Pitfalls: scores

- **The first score fixes the order.** A different `order` later is not an error; it is silently ignored. Keep one
  table of boards and orders in your title and always pass from it.
- **`start` is a rank, not an offset.** `GET_RANKING` with `start` 0 is ERROR 1. (`SEARCH_ROOMS` `start` is a 0-based offset.)
- **`count` over 100 is ERROR 1.** The server does not clamp.
- **Names are exact.** HELLO treats `Alice` and `alice` as the same name, but the board does not. Send the same
  capitalization every time.
- **A worse score returns the old rank**, and its comment is dropped.
- **Ties are consecutive ranks**, not shared. The earlier score comes first.
- **A missing player is rank 0**, not score 0.
- **ERROR 6 is overloaded** (a limit, or a failed store). Scores and rooms are in memory only.
- **No deletion, no per-title board configuration, no large attached data.** The server has none of these.
- **Check the type before parsing.** `psnr_ranking_parse` does not check `m->type`.

## 9. From sceNpMatching2-style calls to psnr calls

The repository states this mapping at the level of API families only. No NP function name was confirmed against this
repository or a public SDK reference, and every row is suggested, not tested in a title. Take exact `sce*` names and
`SCE_NP_*` constants from your title's own headers and your port's bridge code, and treat the right-hand column as a
starting point. The table matches [integrating.md](integrating.md#mapping-the-np-calls); that page is the reference.

| NP call family | psnr call | What to know |
|---|---|---|
| Matching2 context start; Score title-context creation | `psnr_connect` or `psnr_connect_timeout` (HELLO, HELLO_ACK) | `comm_id` is the title's NP communication ID (12 bytes, for example `NPWR00860_00`); a name already held by another address is ERROR 8 (`psnr_connect_error()` returns 8) |
| Matching2 create room | `PSNR_CREATE_ROOM` -> ROOM_JOINED | creator is the owner; blobs of at most 4096 bytes; `max` 1-255 |
| Matching2 search room | `PSNR_SEARCH_ROOMS` -> ROOM_LIST | open rooms only (not full, not invite-only), oldest first, no filter; you filter on external data and flags yourself |
| Matching2 join room | `PSNR_JOIN_ROOM` -> ROOM_JOINED | an invite-only room cannot be joined by id (ERROR 2) |
| Matching2 leave room | `PSNR_LEAVE_ROOM` -> OK | the room is deleted when the last member leaves |
| Matching2 set room data (external, internal) | `PSNR_SET_ROOM_DATA` `which` 0 or 1 | owner only |
| Matching2 room flags | `PSNR_SET_ROOM_DATA` `which` 2 | owner only; a 4-byte big-endian value, opaque to the server |
| Matching2 send room message | `PSNR_ROOM_MESSAGE` | `to` 0 is everyone but the sender |
| Matching2 kick member | `PSNR_KICK_MEMBER` | owner only; not a ban |
| Matching2 room event callbacks | pushes 0xA1 to 0xA5, read with `psnr_poll` | an adapter queues them for the guest callback thread |
| Score record score | `PSNR_RECORD_SCORE` -> SCORE_RECORDED | the title sends the order; the first call on a board wins; comment at most 64 bytes |
| Score ranking by range | `PSNR_GET_RANKING` | `start` is a 1-based rank; `count` at most 100 |
| Score ranking by online id | `PSNR_GET_RANKING_BY_ID` | rows in request order; rank 0 means no score |

Details that matter when you translate:

- **Opaque blobs.** Matching2 "bin attributes" (external, internal, member) travel as opaque blobs; the server never
  needs an SDK header. You choose the byte layout, and your bridge must keep it identical between all clients.
- **Error numbers.** The server's codes (1 to 10) are its own. Your bridge maps them to the title's errors.
  [integrating.md](integrating.md#error-codes-for-a-bridge) lists a suggested category for each code. Those are
  suggestions that were not tested in a title. An ERROR 6 for scores can mean a full store as well as a store failure;
  use the request type to choose.
- **Callbacks.** psnr has pushes, not callbacks. Your adapter polls, then delivers each push to the thread and queue
  where the title expects its callback.
- **One connection per title context.** Rooms and scores share one TCP connection and its 200 requests per second.

Where there is no equivalent (do not claim these in a bridge):

- Changing your own member data after you joined. It is set only when joining. Leave and rejoin, or use a room
  message.
- An owner-change push, or a request to give the room to a chosen member. Ownership moves only when the owner
  leaves, and you read it from MEMBER_LEFT.
- Search filters or sorting on attributes. SEARCH_ROOMS has only `start` and `max`.
- Room passwords, private slots and join-by-id as a private room. These are planned. The invite code mechanism is
  separate, and [integrating.md](integrating.md#not-covered) says that NP room flags or passwords do not become invite codes.
- Lobbies, worlds and world lists (the Medius family): not offered.
- Friends, presence, invites to a friend, join-a-friend and block lists (the sceNpBasic family): not in the server.
- Reading a room's state without joining, and listing members in a search result. The member list comes only in ROOM_JOINED.
- Durable rooms or scores across a server restart. Both are in memory.
- Deleting a score, large attached data, per-title board configuration, or an "around me" query (build it from two
  calls, as shown above).
- PSN authentication and a ban list. A kicked player can rejoin at once.

## 10. The Python client, side by side with C

### What it is for

`client/python/psnr_client.py` is one file, standard library only. Use it for tools, bots, test scripts and quick
checks of a server. It covers HELLO, heartbeat, GET_CAPS, rooms (create, search, join, leave, room data, messages,
kick) and scores (record, ranking by range). It does not cover invite-only rooms, ranking by name, the relay, probes,
sign-in passes or peer keys; for the first two you send the request yourself with `Client.call`, shown below.

### The model

- `Client(host, port, comm_id, online_id, p2p_port=0, timeout=5.0)` connects and sends HELLO. `.user_id`,
  `.public_ip` (a dotted string), `.token` and `.flags` come from HELLO_ACK. Use it as a context manager (`with`) so
  the socket closes.
- Every method is blocking. It sends a request, reads frames until the one with its own request id arrives, and
  returns the decoded result. A lost connection is `ConnectionError`; the client's own timeout is `socket.timeout`;
  a server ERROR is `ServerError` with `.code`.
- Pushes are not delivered to you as events. Any frame of type 0xA0 to 0xAF read while a call waits is appended to
  `c.pushes` as `(type, raw_payload)`, and `c.pop_pushes()` returns and clears the list. There is no wait-for-push
  call: an idle client sees no pushes until it makes some call.
- It is not thread-safe: use one thread per `Client`.

### C and Python, step by step

| Step | C | Python |
|---|---|---|
| connect | `psnr_connect_timeout(host, port, comm, name, p2p, &uid, ip, ms)` returns `psnr_client*` or NULL | `Client(host, port, comm, name)` raises on failure |
| create | hand-built body, `psnr_call(PSNR_CREATE_ROOM)`, `psnr_room_joined_parse` | `c.create_room(4, external=b"mode=1", member_data=b"A")` returns a dict |
| search | `put16(start)`, `put16(max)`, `psnr_room_list_parse`, `psnr_room_next` | `total, rooms = c.search_rooms(start, count)` |
| join | `put64(room)` and a blob | `c.join_room(rid, b"B")` returns a dict like create |
| flags | `which` 2 and 4 bytes via `psnr_put32` | `c.set_room_data(rid, 2, struct.pack(">I", v))` |
| message | `to` 0 for broadcast | `c.room_message(rid, b"hi", to=0)` |
| kick | `room_id`, `member_id` | `c.kick(rid, member_id)` |
| record | order byte 0 or 1 | `c.record_score(board, score, higher_is_better=True, comment=b"")` returns the rank |
| ranking | `psnr_ranking_parse`, `psnr_rank_next` | `total, rows = c.get_ranking(board, start=1, count=10)` |
| error | `reply.type == PSNR_ERROR`, `psnr_get32(data)` | `except ServerError as e: e.code` |
| pushes | `psnr_poll`, `m.req == 0` | `c.pop_pushes()` after any call |
| names | copied and sanitized (`?`) | UTF-8 decoded with replacement, trailing NULs stripped |

Python's `higher_is_better=True` sends order 0 and `False` sends order 1. As in C, the first score on a board fixes
the order. In both, `start` for rankings is a 1-based rank (Python's default is 1; 0 raises `ServerError` with code 1).
Python's `search_rooms` `start` is the 0-based offset as in C.

A room reply is a dict: `room_id`, `my_member`, `owner`, `max`, `flags`, `external`, `internal` (bytes) and `members`,
a list of dicts with `member_id`, `user_id`, `online_id`, `ip` (dotted string), `p2p_port`, `owner` (a bool) and
`data`. A search row has `room_id`, `owner`, `current`, `max`, `flags`, `external`. A ranking row has `rank`,
`online_id`, `score`, `comment`, `recorded_unix`. All blobs are `bytes`; blobs over 4096 bytes raise `ValueError`
before they are sent, including score comments, whose real limit is 64 (the server answers ERROR 1 for those).

### A runnable lobby

Save this as `lobby.py` and run it against a loopback server from the repository root. It uses two clients in one
script, so it shows every room operation and the pushes each side sees. The `drain` helper makes a cheap call
(`caps()`) so that the pushes the server has already sent are read into `c.pushes`.

```python
# lobby.py: python3 -I lobby.py PORT
import struct
import sys
import time

sys.path.insert(0, "client/python")
from psnr_client import Client, ServerError

COMM = "RE-PSN-DEMO"


def decode_push(t, p):
    """The payloads are raw bytes. Offsets are the same as in C (data starts at room_id)."""
    if t == 0xA1:                      # MEMBER_JOINED: room_id, then a member entry
        from psnr_client import parse_member
        return "joined", parse_member(p, 8)[0]["online_id"]
    if t == 0xA2:                      # MEMBER_LEFT: room_id, member_id, owner after the leave
        room, member, owner = struct.unpack(">QHH", p)
        return "left", member, "owner now", owner
    if t == 0xA3:                      # ROOM_DATA: room_id, which, blob
        room, which, n = struct.unpack_from(">QBH", p)
        return "room data", which, p[11:11 + n]
    if t == 0xA4:                      # ROOM_MSG: room_id, from, to, blob
        room, frm, to, n = struct.unpack_from(">QHHH", p)
        return "message from", frm, "to", to, p[14:14 + n]
    if t == 0xA5:                      # KICKED: room_id
        return ("kicked",)
    return ("push", hex(t))


def drain(c, label):
    """Read what the server has sent. Any call reads waiting frames; caps() is the cheapest."""
    time.sleep(0.2)
    c.caps()
    for t, p in c.pop_pushes():
        print(label, *decode_push(t, p))


port = int(sys.argv[1])
with Client("127.0.0.1", port, COMM, "pyA") as a, Client("127.0.0.1", port, COMM, "pyB") as b:
    room = a.create_room(4, external=b"mode=1", member_data=b"A")
    rid = room["room_id"]
    print("A created room", rid, "as member", room["my_member"], "owner", room["owner"])

    total, rooms = b.search_rooms(0, 10)
    print("B sees", total, "room(s):", [(r["room_id"], r["owner"], r["current"], r["max"], r["external"]) for r in rooms])
    joined = b.join_room(rid, b"B")
    print("B joined as member", joined["my_member"], "of", [m["online_id"] for m in joined["members"]])
    drain(a, "A:")

    a.set_room_data(rid, 2, struct.pack(">I", 1))        # flags: bit 0 = started
    b.room_message(rid, b"hello", to=0)
    drain(a, "A:")
    drain(b, "B:")

    a.kick(rid, joined["my_member"])
    drain(b, "B:")
    drain(a, "A:")
    try:
        b.set_room_data(rid, 0, b"x")
    except ServerError as e:
        print("B after the kick:", e.code, e)            # 5: not in room (checked before owner)

    a.leave_room(rid)

    print("A rank", a.record_score(8, 100, higher_is_better=False, comment=b"lap"))
    print("B rank", b.record_score(8, 90))               # order is ignored: the board is 'lower is better'
    print(a.get_ranking(8, 1, 10))
```

```sh
go build -o re-psn-server ./server
./re-psn-server -addr 127.0.0.1:PORT -http "" -db off &
python3 -I lobby.py PORT
```

### What the class does not offer: raw calls

`Client.call(msg_type, body)` sends any request and returns the reply payload after the request id (or raises
`ServerError`). Build the body with `struct`. The module has `blob()` (a length-prefixed field) and
`parse_room_joined()`; `parse_member()` decodes one member entry.

```python
# raw.py: python3 -I raw.py PORT   (invite-only room and ranking by name, which Client has no method for)
import struct
import sys

sys.path.insert(0, "client/python")
from psnr_client import Client, ServerError, blob, parse_room_joined

SET_ROOM_ACCESS, JOIN_BY_CODE, GET_ROOM_ACCESS, GET_RANKING_BY_ID = 0x17, 0x18, 0x19, 0x22


def name16(s):
    return s.encode().ljust(16, b"\0")


port = int(sys.argv[1])
with Client("127.0.0.1", port, "RE-PSN-DEMO", "pyHost") as host, Client("127.0.0.1", port, "RE-PSN-DEMO", "pyGuest") as guest:
    rid = host.create_room(4, member_data=b"H")["room_id"]

    r = host.call(SET_ROOM_ACCESS, struct.pack(">QB", rid, 1))     # ROOM_ACCESS: mode u8, code[8]
    mode, code = r[0], r[1:9].decode()
    print("mode", mode, "code", code)
    try:
        guest.join_room(rid)                                       # by id: refused, as if it did not exist
    except ServerError as e:
        print("by id:", e.code)                                    # 2
    room = parse_room_joined(guest.call(JOIN_BY_CODE, code.lower().encode() + blob(b"G")))
    print("by code (lower case works): member", room["my_member"], "of", len(room["members"]))
    print("member reads the code:", guest.call(GET_ROOM_ACCESS, struct.pack(">Q", rid))[1:9].decode())

    host.record_score(5, 300)
    p = host.call(GET_RANKING_BY_ID, struct.pack(">IH", 5, 2) + name16("pyHost") + name16("nobody"))
    total, n = struct.unpack_from(">IH", p)
    off = 6
    for _ in range(n):
        rank, oid, score = struct.unpack_from(">I16sq", p, off)
        clen, = struct.unpack_from(">H", p, off + 28)
        off += 30 + clen + 8
        print(oid.rstrip(b"\0").decode(), "rank", rank, "score", score)     # rank 0 means no score
```

### Reference: Python

| Call | Returns | Raises, notes |
|---|---|---|
| `Client(host, port, comm_id, online_id, p2p_port=0, timeout=5.0)` | the client | `ValueError` for names over 12 or 16 bytes; `ServerError` code 8 for a name that is taken |
| `c.caps()` | `(bits, version text)` | cheap; any call also drains pushes |
| `c.heartbeat(rtt_ms=None)` | nothing; no reply | send one at least every 90 seconds when idle |
| `c.create_room(max_members, flags=0, external=b"", internal=b"", member_data=b"")` | room dict | 1, 6 |
| `c.join_room(room_id, member_data=b"")` | room dict | 1, 2, 3, 6 |
| `c.leave_room(room_id)` | nothing | 5 |
| `c.search_rooms(start=0, count=10)` | `(total, [room rows])`; may hold fewer than `count` | 1 |
| `c.set_room_data(room_id, which, data)` | nothing | 1, 5, 4, 6 |
| `c.room_message(room_id, data, to=0)` | nothing | 1, 5 |
| `c.kick(room_id, member_id)` | nothing | 4, 5, 2 |
| `c.record_score(board, score, higher_is_better=True, comment=b"")` | rank (int) | 1, 6 |
| `c.get_ranking(board, start=1, count=10)` | `(total, [rows])` | 1 |
| `c.call(msg_type, body=b"")` | reply payload after the request id | `ServerError`, `ConnectionError`, `socket.timeout` |
| `c.pop_pushes()` | list of `(type, payload)`, then clears it | |

Errors: `ServerError(.code)` for any ERROR reply (constants `E_BAD_REQUEST` ... `E_PASS`, texts in `ERROR_NAMES`),
`ConnectionError` for a lost connection, `socket.timeout` for the client's own timeout.

### Pitfalls: Python

- **No waiting for pushes.** Pushes are read only while a call waits for its reply. Use a cheap call (`caps()`) to
  collect them (a server from 0.5.0 or before answers GET_CAPS with ERROR 1, which still reads the waiting pushes but
  raises `ServerError`). Do not call the private `_frame()` with a short socket timeout to wait: an internal read keeps partial
  frames in a local buffer, so a timeout in the middle of a frame loses those bytes and desynchronises the stream. Treat
  the client as unusable after a timeout from such a wait, and reconnect.
- **A call after `socket.timeout` may be out of step.** The request may have taken effect, and its late reply is
  skipped silently by the next call (a reply with another request id is ignored). Check the room state before you
  retry.
- **`parse_room_joined` does not check the type**, and `call()` ignores frames shorter than 4 bytes.
- **Names are exact for scores**, as in C.
- **A kicked client** gets ERROR 5 on its next room call; it is no longer a member.
- **Closing the socket leaves every room.** The server runs the leave for each room you were in, and the others get MEMBER_LEFT.
- **Python raises `ValueError` for any blob over 4096 bytes**, including a score comment (whose limit is 64).

## 11. NAT traversal from the developer side

**What it is for.** Your game's players talk to each other over UDP, and most of them sit behind a home router. The server
helps them find each other and, when that fails, carries their traffic. This chapter is the part you write: the UDP
socket, the probe, the punch, and the receive loop that sorts what arrives.

**The model.**

- The library never opens, reads or writes your game's UDP peer socket. It only builds and parses packets
  (`client/psnr.h`, "What it touches"). You own the socket, the timing and the decisions.
- Every control packet starts with the ASCII bytes `PSNR` and a type byte. Integers are big-endian.
- The server's UDP port is the same address and port as the TCP connection you made with `psnr_connect`. The library has no
  separate setting for it (`psnr_server_addr` returns the peer address of the TCP connection).
- You tell the server your peer port once, in HELLO. You then send a **PROBE** from that same socket. The server records
  the address and port it saw and replies with them (**PROBE_REPLY**). That is your router's mapping, as the server sees it.
- When another player joins your room, the server hands each of you the other's endpoint (the **member entry**). You send
  a **PUNCH** to it, and so do they, so that each router opens a hole for the other.
- If no direct packet ever arrives, you choose to send through the server's **relay** (chapter 12). The server never
  reroutes a datagram by itself.

### 11.1 Bind first, then connect

HELLO carries `p2p_port` once, at connect time. A second HELLO on the same connection gets ERROR 1, so you cannot change the
port later. Bind your UDP socket first, read the port the system gave you, and pass it to `psnr_connect_timeout`. Passing
`0` means "I have no peer socket". The code below is the first block of a small library that the next sections extend; it
is POSIX, and it builds with `cc -std=gnu11 -Wall -Wextra` together with `client/psnr.c`.

```c
/* unit: net */
#include "psnr.h"

#include <arpa/inet.h>
#include <netinet/in.h>
#include <poll.h>
#include <pthread.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <sys/socket.h>
#include <time.h>
#include <unistd.h>

typedef struct {
    psnr_client* c;        /* the control connection */
    int udp;               /* the title's own peer socket */
    uint16_t udp_port;     /* the port announced in HELLO */
    uint32_t id;           /* our user id, from HELLO_ACK */
} player;

/* Bind a UDP socket on an address the system picks and report its port. */
int open_p2p(uint32_t bind_addr_host_order, uint16_t* port)
{
    struct sockaddr_in a;
    socklen_t n = sizeof(a);
    int s = socket(AF_INET, SOCK_DGRAM, 0);
    if (s < 0) return -1;
    memset(&a, 0, sizeof(a));
    a.sin_family = AF_INET;
    a.sin_addr.s_addr = htonl(bind_addr_host_order);   /* INADDR_ANY for a real game */
    if (bind(s, (struct sockaddr*)&a, sizeof(a)) || getsockname(s, (struct sockaddr*)&a, &n)) {
        close(s);
        return -1;
    }
    *port = ntohs(a.sin_port);
    return s;
}

/* Socket first, then HELLO with the socket's port. 0 on success. */
int player_open(player* p, const char* host, uint16_t port, const char* comm_id, const char* name)
{
    p->udp = open_p2p(INADDR_LOOPBACK, &p->udp_port);
    if (p->udp < 0) return -1;
    p->c = psnr_connect_timeout(host, port, comm_id, name, p->udp_port, &p->id, NULL, 3000);
    if (!p->c) {
        close(p->udp);
        return -1;
    }
    return 0;
}

/* The server's UDP endpoint, as a sockaddr you can sendto(). */
void server_sockaddr(const player* p, struct sockaddr_in* a)
{
    uint8_t ip[4];
    uint16_t port;
    psnr_server_addr(p->c, ip, &port);
    memset(a, 0, sizeof(*a));
    a->sin_family = AF_INET;
    memcpy(&a->sin_addr, ip, 4);
    a->sin_port = htons(port);
}
```

The example binds `127.0.0.1` so that it also runs on a test machine; a game binds `INADDR_ANY` (or the interface it wants
to use). The library speaks IPv4 only.

### 11.2 Probe, and keep probing

`psnr_probe_packet` fills 17 bytes from state the client already holds; `psnr_probe_reply` reads the 11-byte answer.

| Bytes | PROBE (you to the server) | PROBE_REPLY (server to you) |
|---|---|---|
| 0-3 | `PSNR` | `PSNR` |
| 4 | `0x01` | `0x81` |
| 5-8 | `user_id` u32 | `public_ip` (4 bytes) |
| 9-12 | `token` u32 | bytes 9-10 of the reply: `public_port` u16 (the reply is 11 bytes) |
| 13-16 | `local_ip` (4 bytes) | none |

Two details matter. The `user_id` and `token` come from HELLO_ACK and are kept inside the client. The `local_ip` is the
local address of the **TCP** connection, not of your UDP socket. `psnr_probe_reply` accepts a datagram of at least 11
bytes and does not look at who sent it.

```c
/* unit: net (continued) */
uint64_t now_ms(void)
{
    struct timespec ts;
    clock_gettime(CLOCK_MONOTONIC, &ts);
    return (uint64_t)ts.tv_sec * 1000u + (uint64_t)ts.tv_nsec / 1000000u;
}

/* One probe from the peer socket, no waiting. Rebuild the packet each time: it reads the client's current token. */
int send_probe(const player* p)
{
    struct sockaddr_in srv;
    uint8_t pkt[PSNR_UDP_PROBE_LEN];
    server_sockaddr(p, &srv);
    psnr_probe_packet(p->c, pkt);
    return sendto(p->udp, pkt, sizeof(pkt), 0, (struct sockaddr*)&srv, sizeof(srv)) == (ssize_t)sizeof(pkt) ? 0 : -1;
}

/* Right after HELLO: probe every 200 ms until the reply comes, at most `tries` times. 1 and the endpoint, or 0. */
int probe_until_reply(const player* p, uint8_t pub_ip[4], uint16_t* pub_port, int tries)
{
    uint8_t buf[64];
    int i;
    for (i = 0; i < tries; i++) {
        struct pollfd pf = { p->udp, POLLIN, 0 };
        if (send_probe(p)) return 0;
        if (poll(&pf, 1, 200) > 0) {
            ssize_t n = recv(p->udp, buf, sizeof(buf), 0);
            if (n > 0 && psnr_probe_reply(buf, (size_t)n, pub_ip, pub_port)) return 1;
        }
    }
    return 0;
}

/* Once online: call this every frame; it sends a keepalive probe about every 15 s. */
#define PROBE_EVERY_MS 15000u
void probe_keepalive(const player* p, uint64_t* last_ms)
{
    uint64_t t = now_ms();
    if (t - *last_ms >= PROBE_EVERY_MS) {
        send_probe(p);
        *last_ms = t;
    }
}
```

**Cadence.** Send a probe after HELLO and repeat it until a reply arrives. Once online, send one about every 15 seconds.
The 15 seconds is guidance, not a protocol rule: some home gateways drop an idle UDP mapping after about 30 seconds.
The probe also shares the server's per-source UDP budget (`-max-udp-rate`, 1000 packets per second with the same burst, by
default), which is shared by every player behind one public address, so do not probe in a busy loop.

**Why a probe gets no reply.** The server drops the packet silently unless all of these hold: it is at least 17 bytes;
the user is registered; the token matches; the title is not in `-no-peer-titles`; and the packet's IPv4 source address
equals the address of the player's TCP connection (unless the operator started the server with `-udp-any-source`). In
connect mode (chapter 14) a probe before AUTH is also dropped. A refused probe is counted, not answered: see the drop
counters in section 16.5. Bytes after the 17th are ignored. A probe from a client connected over IPv6 is counted as malformed.

### 11.3 Route and endpoint

The member entries in `ROOM_JOINED` and `MEMBER_JOINED` carry `ip[4]` and `p2p_port`: the address that **you**, the viewer,
should send to. Two viewers can receive different addresses for the same member. The server chooses, in order:

1. the operator's `-lan-public-addr` case (an address the operator configured for same-LAN players);
2. if the member has probed and you are not on the member's public address: the source address and port of the member's
   probe, which is the router mapping;
3. if the member's probe gave a usable `local_ip`: that address with the port the member sent in HELLO. This is for players
   on the same network;
4. otherwise the member's TCP address with the port from HELLO.

"Same network" means the same IPv4 TCP address. An IPv6 client is never on the same network as anyone, and receives
the all-zero address. A member who has not probed yet shows the HELLO port, not a mapped one; if you read the entry
before the member's first probe, expect to see the wrong port (on a test machine you will see `127.0.0.1` and the HELLO
port). The full rule is in [protocol.md section 7.4](protocol.md#74-member-endpoint).

`psnr_member` has `ip[4]`, `p2p_port`, `owner`, `member_id`, `user_id`, `online_id` and the member's own `data` blob.
`psnr_member_parse` reads one entry (use it for `MEMBER_JOINED`, after the 8-byte room id), and `psnr_member_next` walks
the members of a `ROOM_JOINED`:

```c
/* unit: net (continued) */
void print_members(const psnr_msg* joined)
{
    psnr_room_joined rj;
    psnr_member mb;
    if (!psnr_room_joined_parse(joined, &rj)) return;
    while (psnr_member_next(&rj.members, &mb))
        printf("member %s id=%u user=%u endpoint %u.%u.%u.%u:%u%s\n", mb.online_id, mb.member_id, mb.user_id,
               mb.ip[0], mb.ip[1], mb.ip[2], mb.ip[3], mb.p2p_port, mb.owner ? " (owner)" : "");
}

/* A MEMBER_JOINED push is: room_id u64, then one member entry. */
int member_joined_parse(const psnr_msg* m, psnr_member* out)
{
    if (m->type != PSNR_MEMBER_JOINED || m->len < 8) return 0;
    return psnr_member_parse(m->data + 8, m->len - 8, out, NULL);
}
```

### 11.4 Hole punching

A PUNCH is `PSNR` `0x02` and your `user_id` (9 bytes). You send it from your peer socket to the endpoint in the member entry,
a few times when the peer appears. The other side does the same, so that each router sees outgoing traffic to the other
and lets the reply in. The server never sends a PUNCH; a PUNCH sent to the server is counted as malformed.

```c
/* unit: net (continued) */
/* Send `count` punches to a peer's endpoint, spaced by the caller (once per frame, or per 50 ms). */
int punch_once(const player* p, const uint8_t ip[4], uint16_t port)
{
    struct sockaddr_in to;
    uint8_t pkt[PSNR_UDP_PUNCH_LEN];
    memset(&to, 0, sizeof(to));
    to.sin_family = AF_INET;
    memcpy(&to.sin_addr, ip, 4);
    to.sin_port = htons(port);
    psnr_punch_packet(p->c, pkt);
    return sendto(p->udp, pkt, sizeof(pkt), 0, (struct sockaddr*)&to, sizeof(to)) == (ssize_t)sizeof(pkt) ? 0 : -1;
}
```

A player whose router picks a new public port for each destination (a "symmetric NAT") cannot be punched, because the port
the server saw is not the port the other player would reach. Probing and punching do not help there; the relay does.
The repository's Docker NAT lab (`lab/run.sh`, scenarios cone, symmetric, symmetric-norelay and samelan) reproduces these
cases with the real C client; see `lab/README.md`.

### 11.5 What to do with every datagram you receive

Your peer socket now carries three kinds of traffic: your game's own packets, the server's control replies, and relayed game
data. `psnr_is_control(buf, len)` returns 1 when `len >= 5` and the first four bytes are `PSNR`. A relayed datagram also
matches it (its type byte is `0x03`), so the order of tests matters. If you drop every control packet first, you throw
away relayed game data.

1. Not control: it is game data.
2. `psnr_probe_reply` succeeds: an endpoint update.
3. `psnr_relay_unwrap_from(c, src_ip, src_port, ...)` succeeds: relayed game data from user N.
4. Anything else with the `PSNR` prefix: drop it (a PUNCH, or a forged relay datagram).

This also means that no game datagram of 5 bytes or more may begin with `PSNR`.

```c
/* unit: net (continued) */
enum { D_GAME, D_PROBE_REPLY, D_RELAYED, D_DROP };

int classify(const psnr_client* c, const struct sockaddr_in* from, const uint8_t* buf, size_t n,
             uint32_t* from_user, const uint8_t** payload, size_t* plen, uint8_t pub_ip[4], uint16_t* pub_port)
{
    uint8_t src[4];
    if (!psnr_is_control(buf, n)) return D_GAME;
    if (psnr_probe_reply(buf, n, pub_ip, pub_port)) return D_PROBE_REPLY;
    memcpy(src, &from->sin_addr, 4);
    if (psnr_relay_unwrap_from(c, src, ntohs(from->sin_port), buf, n, from_user, payload, plen)) return D_RELAYED;
    return D_DROP;
}

/* Drain the socket without blocking and say what arrived. Returns the number of datagrams read. */
int pump_udp(const player* p)
{
    uint8_t buf[2048], pub[4];
    int count = 0;
    for (;;) {
        struct sockaddr_in from;
        socklen_t fl = sizeof(from);
        uint32_t from_user = 0;
        const uint8_t* payload = NULL;
        size_t plen = 0;
        uint16_t pub_port = 0;
        ssize_t n = recvfrom(p->udp, buf, sizeof(buf), MSG_DONTWAIT, (struct sockaddr*)&from, &fl);
        if (n < 0) break;
        count++;
        switch (classify(p->c, &from, buf, (size_t)n, &from_user, &payload, &plen, pub, &pub_port)) {
        case D_GAME:        printf("game data, %zd bytes\n", n); break;
        case D_PROBE_REPLY: printf("public endpoint %u.%u.%u.%u:%u\n", pub[0], pub[1], pub[2], pub[3], pub_port); break;
        case D_RELAYED:     printf("relayed from user %u: %.*s\n", from_user, (int)plen, (const char*)payload); break;
        default:            printf("control packet dropped\n"); break;
        }
    }
    return count;
}
```

### 11.6 Choosing the relay

You decide. The server only advises, with the **ROUTE** push.

- **Datagrams.** Use the relay for a peer you have punched, repeatedly, and never heard from directly.
- **Streams.** When a ROUTE push for that peer has bit 0 (`PSNR_ROUTE_STREAM_RELAY`) set, the peer cannot take an incoming
  TCP connection, so open the stream through the relay.
- A title that must not relay simply never wraps a datagram and never opens a relay stream.

The server sends ROUTE only if it runs with `-relay` (and neither title is in `-no-peer-titles`). It arrives through
`psnr_poll` as type `0xA7`, request id 0, 13 bytes: `room_id` u64, `user` u32, `flags` u8. `user` is the **other**
member the route is about, and the flags are for your view of that member. Ignore flag bits you do not know.

```c
/* unit: net (continued) */
/* Return 1 and fill *user if m is a ROUTE push; *stream_relay is bit 0 of its flags. */
int route_parse(const psnr_msg* m, uint32_t* user, int* stream_relay)
{
    if (m->type != PSNR_ROUTE || m->len != 13) return 0;
    *user = psnr_get32(m->data + 8);
    *stream_relay = (m->data[12] & PSNR_ROUTE_STREAM_RELAY) != 0;
    return 1;
}

/* GET_CAPS: the per-player answer to "may I probe and relay now". 1 and the bits, or 0. */
int caps_bits(psnr_client* c, uint32_t* bits)
{
    psnr_msg m;
    psnr_caps caps;
    int ok = 0;
    if (psnr_call(c, PSNR_GET_CAPS, NULL, 0, &m, 2000) != 1) return 0;
    if (m.type == PSNR_CAPS && psnr_caps_parse(&m, &caps)) {
        *bits = caps.bits;
        ok = 1;
    }
    psnr_msg_free(&m);
    return ok;
}
```

What a ROUTE result and the surrounding observations mean:

| You observe | It means |
|---|---|
| ROUTE flags `0` | Streams to that user can go direct. Computed once at join. It does not promise that a stream will work, and a stream through the relay is still served if you ask for one. |
| ROUTE flags `1` | The target has probed, its probe came from an address different from the `local_ip` it reported (a missing `local_ip` counts as different), and the two of you are not on one public address; or the server runs `-relay-force` (testing only). |
| ROUTE flags `0` right after a join, but the peer is behind a strict NAT | The route is computed **once**, at join. A member who had not probed yet gets bit 0 clear and nothing corrects it later. A title that needs a current answer must decide itself. |
| No ROUTE push at all | The server runs without `-relay`, or one of the titles is in `-no-peer-titles`. |
| `psnr_relay_available` is 1 | The server has a relay. It says nothing about **you**. |
| `PSNR_CAP_RELAY` (GET_CAPS bit 5) set | This player may use the relay now. With `-require-pass relay` it stays clear until a pass with the relay scope is accepted (chapter 14). |
| Probe reply endpoint equals your HELLO port | Your router did not rewrite the port (or you are on loopback). |
| Probe reply port differs from your HELLO port | A router mapped you. Peers outside your network use the mapped endpoint. |

A policy sketch. It decides per peer, once a frame or once a second, and it logs the answer (log the path you chose for each
peer: it is the first thing you will want when a player reports a problem):

```c
/* unit: net (continued) */
typedef struct {
    uint64_t last_direct_ms;   /* when a datagram from this peer last arrived directly; 0 if never */
    uint64_t first_punch_ms;   /* when you started punching; 0 if not yet */
} peer_state;

enum path { PATH_DIRECT, PATH_RELAY, PATH_NONE, PATH_WAIT };

/* relay_ok: PSNR_CAP_RELAY was set in GET_CAPS. grace_ms: how long to punch before giving up on a direct path. */
enum path choose_path(const peer_state* s, uint64_t now, int relay_ok, uint64_t grace_ms)
{
    if (s->last_direct_ms && now - s->last_direct_ms < 5000) return PATH_DIRECT;
    if (!s->first_punch_ms || now - s->first_punch_ms < grace_ms) return PATH_WAIT;
    return relay_ok ? PATH_RELAY : PATH_NONE;
}
```

### 11.7 API reference for this chapter

| Signature | Returns | Errors and notes | Blocks |
|---|---|---|---|
| `psnr_connect_timeout(host, port, comm_id, online_id, p2p_port, &user_id, public_ip, timeout_ms)` | client, or `NULL` | `timeout_ms <= 0` returns `NULL`; one budget for the connect and the HELLO wait; use a numeric IPv4 host (DNS is not covered by the budget) | up to `timeout_ms` |
| `psnr_server_addr(c, ip[4], &port)` | void | the TCP peer address; assumes the UDP server uses the same address and port | no |
| `psnr_probe_packet(c, out[17])` | void | reads the current token at call time | no |
| `psnr_punch_packet(c, out[9])` | void | | no |
| `psnr_is_control(buf, len)` | 1 if `len >= 5` and the prefix is `PSNR` | also true for relayed datagrams | no |
| `psnr_probe_reply(buf, len, ip[4], &port)` | 1 and the endpoint, else 0 | needs `len >= 11`; does **not** check the sender | no |
| `psnr_relay_available(c)` | 1 if the server announced a relay in HELLO_ACK | per title, not per player | no |

### 11.8 Pitfalls

- **Wrong socket.** A probe from any socket other than the one whose port you announced records a different mapping, and
  there is no call to change the announced port.
- **Probing before AUTH in connect mode.** The token is 0 until `psnr_auth` returns; a packet built before that is dropped
  as unauthorized. Rebuild after AUTH.
- **Loopback endpoints.** A player who connects over `127.0.0.1` is handed to others as `127.0.0.1`. On one machine that
  works; it also hides every NAT problem.
- **Never trust the probe reply's sender.** If you care who answered, compare the source with `psnr_server_addr` yourself.
- **Never stop probing.** The record on the server is not expired by the server, but your router's mapping is.
- **A hard-coded 15 seconds.** Treat it as a default you can tune from real routers, not as a rule.

Wire reference: [api.md, UDP](api.md#udp) and [protocol.md section 9](protocol.md#9-udp).

## 12. The relay

**What it is for.** When two players cannot reach each other directly, the server can carry their traffic. There are two
forms. **Datagrams** go through the server's UDP port, wrapped in a 17-byte header. **Streams** are TCP connections that the
server joins together and then copies bytes between. Both are for the title to choose; the server never switches by itself.

**The model.**

- The relay needs the server's `-relay` flag. Without it a stream gets ERROR 9, and a wrapped datagram is dropped.
- Both players must be in a **shared room** and must hold a valid token (the sender's token must match).
- A datagram is delivered only to a target that **has probed**. Without a probe the server has no endpoint for it.
- A stream is two **new** TCP connections to the server, not your control connection. The control connection stays untouched.
- Closing one side of a stream closes both.

### 12.1 Datagrams: wrap and unwrap

`psnr_relay_wrap(c, to_user, payload, len, out, cap)` writes `PSNR` `0x03` | your `user_id` u32 | your token u32 |
`to_user` u32 | the payload, and returns `17 + len`; it returns 0, and writes nothing, if `cap < 17 + len`. Send the result to
the server's UDP address **from your peer socket**. The server forwards it from its UDP port to the target's probed
endpoint, as `PSNR` `0x03` | `from_user` u32 | payload (9 bytes plus the payload). An empty payload is forwarded as 9 bytes.

The forward happens only when the sender and the target are both registered, the token matches, they share a room, neither
title is in `-no-peer-titles`, the passes allow it (with `-require-pass relay`), the target has probed, and the sender's
IPv4 source equals its TCP address (unless `-udp-any-source`). Otherwise the datagram is **dropped without a reply**. The
only evidence is a counter in `/api/stats` (section 16.5).

```c
/* unit: net (continued) */
/* Send one game datagram to `to_user` through the server. 0 on success, -1 on a send error or a payload that does not fit. */
int relay_send(const player* p, uint32_t to_user, const void* data, size_t len)
{
    uint8_t out[1500];   /* keep datagrams well below the path MTU */
    struct sockaddr_in srv;
    size_t n = psnr_relay_wrap(p->c, to_user, data, len, out, sizeof(out));
    if (n == 0) return -1;
    server_sockaddr(p, &srv);
    return sendto(p->udp, out, n, 0, (struct sockaddr*)&srv, sizeof(srv)) == (ssize_t)n ? 0 : -1;
}
```

**Receiving.** The server-to-client form has no token. That makes the sender's identity (`from_user`) a claim by whoever
put the packet on the wire. Any room member knows every other member's endpoint, so any member can send a datagram shaped
like a relayed one straight to your UDP port and name any `from_user`. The server's UDP address is the only voucher. Use
`psnr_relay_unwrap_from(c, src_ip, src_port, ...)`, and give it the address your `recvfrom` reported. It returns 0 unless
both the address and the port equal the server's (`psnr_server_addr`), and it also returns 0 when `c` or `src_ip` is `NULL`.
The older `psnr_relay_unwrap` does no source check; it remains for compatibility. The `classify` function in section 11.5
is the receive side of the relay.

The payload pointer returned by unwrap points **into** your buffer, `len - 9` bytes long. Copy it out before you reuse
the buffer.

Sizes and rates: the largest relayed payload on IPv4 is 65490 bytes (65507, the UDP maximum, minus the 17-byte header).
`psnr_relay_wrap` checks only your buffer, not 65490. The server adds no size limit of its own below that, and it does not
queue, resend or reorder. Probes and relay datagrams share one budget of `-max-udp-rate` per source IP (default 1000 per
second, burst 1000), and a large datagram costs one token. Relayed payload bytes are counted in `relay_bytes` in
`/api/stats`.

### 12.2 Streams: connect, offer, accept

A stream is a rendezvous of two blocking calls on two players.

1. Player A calls `psnr_stream_connect(c, to_user, vport, timeout_ms)`. The library opens a new TCP connection to the
   server and sends `STREAM_CONNECT` (`0x30`).
2. The server pushes `STREAM_OFFER` (`0xA6`) to B's **control** connection: `stream_id` u32 | `from_user` u32 | `vport` u16
   (10 bytes, no request id). B sees it only by calling `psnr_poll`.
3. B calls `psnr_stream_accept(c, stream_id, timeout_ms)`: another new TCP connection and `STREAM_ACCEPT` (`0x31`).
4. The server answers both legs with `STREAM_READY` (`0x96`) and then copies bytes in both directions, without framing,
   until one side closes. Closing one closes both.

`vport` is a 16-bit number that the two sides agree on to say what the stream is for. The server does not interpret it.
Both calls return a connected **blocking** TCP socket (a `SOCKET` on Windows, a file descriptor elsewhere) as an `int64_t`,
or `-1`. You own the socket and close it (`closesocket` on Windows; keep the 64-bit width). The socket carries raw bytes:
no length prefix, no message boundary. Frame your own messages.

Because `psnr_stream_connect` blocks until the peer accepts, run it on a worker thread, while the accepting side keeps
calling `psnr_poll` on **its own client** to see the offer. The offer must be accepted within 10 seconds of the server
sending it; after that the connecting side gets an ERROR and its call returns `-1`.

```c
/* unit: net (continued) */
typedef struct {
    psnr_client* c;
    uint32_t to;
    uint16_t vport;
    int timeout_ms;
    int64_t sock;      /* result: -1 until the call returns, then the socket or -1 */
} stream_job;

void* stream_connect_thread(void* arg)
{
    stream_job* j = (stream_job*)arg;
    j->sock = psnr_stream_connect(j->c, j->to, j->vport, j->timeout_ms);
    return NULL;
}

/* On the accepting side: if m is a STREAM_OFFER, accept it. Returns the socket, or -1. */
int64_t accept_offer(psnr_client* c, const psnr_msg* m, int timeout_ms, uint32_t* from_user, uint16_t* vport)
{
    if (m->type != PSNR_STREAM_OFFER || m->len != 10) return -1;
    *from_user = psnr_get32(m->data + 4);
    *vport = psnr_get16(m->data + 8);
    return psnr_stream_accept(c, psnr_get32(m->data), timeout_ms);
}

/* After setup the stream is a plain blocking socket: its deadlines are yours. */
int stream_set_timeout(int64_t s, int ms)
{
    struct timeval tv;
    tv.tv_sec = ms / 1000;
    tv.tv_usec = (ms % 1000) * 1000;
    return setsockopt((int)s, SOL_SOCKET, SO_RCVTIMEO, &tv, sizeof(tv));
}

int stream_send_all(int64_t s, const void* data, size_t len)
{
    const uint8_t* p = (const uint8_t*)data;
    while (len) {
        ssize_t n = send((int)s, p, len, MSG_NOSIGNAL);
        if (n <= 0) return -1;
        p += n;
        len -= (size_t)n;
    }
    return 0;
}
```

A complete usage, on one machine, looks like this (two clients `a` and `b` that already share a room; `b_user` is B's user
id). The connecting thread starts first; the main thread plays B and waits for the offer in `psnr_poll`:

```c
/* unit: net (continued) */
int stream_demo(psnr_client* a, psnr_client* b, uint32_t b_user)
{
    stream_job job = { a, b_user, 7, 5000, -1 };
    pthread_t th;
    psnr_msg m;
    uint64_t end = now_ms() + 3000;
    int64_t sb = -1;
    char buf[16] = {0};
    if (pthread_create(&th, NULL, stream_connect_thread, &job)) return -1;
    while (sb < 0 && now_ms() < end) {
        int r = psnr_poll(b, &m);
        if (r < 0) break;
        if (r == 0) { usleep(2000); continue; }
        if (m.type == PSNR_STREAM_OFFER) {
            uint32_t from; uint16_t vport;
            sb = accept_offer(b, &m, 5000, &from, &vport);
        }
        psnr_msg_free(&m);
    }
    pthread_join(th, NULL);
    if (job.sock < 0 || sb < 0) { if (job.sock >= 0) close((int)job.sock); if (sb >= 0) close((int)sb); return -1; }
    stream_send_all(job.sock, "ping", 4);
    stream_set_timeout(sb, 2000);
    if (recv((int)sb, buf, sizeof(buf) - 1, 0) > 0) printf("B read: %s\n", buf);
    close((int)job.sock);
    printf("after A closes, B recv = %zd\n", recv((int)sb, buf, 1, 0));   /* 0: end of stream */
    close((int)sb);
    return 0;
}
```

### 12.3 Deadlines and errors

`timeout_ms >= 0` is **one absolute budget** for the TCP connect, the setup write and the whole `STREAM_READY` reply. It is
shared across fragments and across retries after an interrupted system call. `timeout_ms < 0` means no deadline anywhere.
`timeout_ms == 0` is **not** "poll once": it fails at once with `-1`. The connect-policy callback (section 12.4) runs
before the budget starts. The server gives the target 10 seconds to accept, so even a negative timeout ends after about
10 seconds with `-1`.

The call reports only `-1`. The server's ERROR code on that leg is read and discarded, so you cannot tell the causes apart
from the return value:

| Server ERROR on the leg | Cause |
|---|---|
| 1 | The first frame was too short. |
| 2 | Anything else: bad token, not a roommate, an unknown or already taken stream id, the target did not accept in 10 s, a connection that is not signed in (connect mode), or the target is not found. |
| 9 | The server runs without `-relay`, or either title is in `-no-peer-titles`. |
| 10 | A pass is needed under `-require-pass relay` and is missing or lapsed (or revoked, or the key was removed). |

To find the cause, look at `GET_CAPS` (bit 5 and bit 7), then the server log. A stream between two players who share no room
returns `-1` almost at once. A stream that is open ends when either player's pass ends (about 60 seconds after the earlier
`exp`, unless refreshed), with both legs closed and **no** ERROR.

**Limits.** `-max-conns` (default 4096) counts relay legs too, and one stream holds two server-side sockets plus each
player's control connection, so a busy server needs a high file-descriptor limit. A server closes a stream after
`-relay-idle` seconds without bytes in either direction (default 600, 0 means never; traffic one way keeps it alive) and
after a write that stalls for 30 seconds. After setup, deadlines on the raw socket are yours.

### 12.4 The connect policy covers relay legs

A host program can refuse the connections the library makes with `psnr_set_connect_policy(fn, user)`. It is process-wide:
set it before the first connect, from the connecting thread; there is no lock, and the callback must not call the library.

- `PSNR_PURPOSE_SERVER` (1): called once per resolved address, with the host text as typed. A refused address is skipped. If
  all are refused, `psnr_connect` returns `NULL` and `psnr_connect_error()` is `PSNR_E_POLICY` (1000, a local code that
  never goes on the wire).
- `PSNR_PURPOSE_STREAM` (2): `host` is `NULL` and `ip` is the server; refusal makes the stream call return `-1` and open
  nothing.

`psnr_discover` and your own sockets are not covered.

### 12.5 API reference for this chapter

| Signature | Returns | Errors and notes | Blocks |
|---|---|---|---|
| `psnr_relay_wrap(c, to_user, payload, len, out, cap)` | `17 + len`, or 0 | 0 if `cap < 17 + len` (nothing written); no check against 65490 | no |
| `psnr_relay_unwrap(buf, len, &from, &payload, &plen)` | 1 or 0 | 1 if `len >= 9`, control, type `0x03`; **no source check** | no |
| `psnr_relay_unwrap_from(c, src_ip, src_port, buf, len, ...)` | 1 or 0 | 0 unless source ip **and** port equal the server's; use this one | no |
| `psnr_stream_connect(c, to_user, vport, timeout_ms)` | socket (`int64_t`) or -1 | -1 for every failure; 0 fails at once | until the peer accepts, up to the budget |
| `psnr_stream_accept(c, stream_id, timeout_ms)` | socket or -1 | the offer must be accepted within 10 s | up to the budget |
| `psnr_set_connect_policy(fn, user)` | void | process-wide; callback must not call the library | no |

### 12.6 Pitfalls

- **Wrapping for a peer that never probed.** Dropped, silently, and counted as `unauthorized`. Probe first, and make sure a
  PROBE_REPLY has arrived for the target before you fall back to the relay.
- **`unwrap_from` and a different UDP port.** The library assumes the server's UDP address is its TCP address and port.
  If the operator uses another UDP address, `unwrap_from` rejects everything, and so does probing.
- **Blocking the frame loop.** `psnr_stream_connect` can block for the whole timeout. Run it on a worker.
- **The offer waits in the queue.** The accepting side must keep calling `psnr_poll`. If it does not drain pushes for 10 s,
  the offer expires.
- **Leaking messages.** Free every `psnr_msg` you receive with `psnr_msg_free`.
- **Treating ROUTE as enforcement.** With plain `-relay` (ROUTE flags 0) a stream still works if you ask for it.
- **Assuming unwrap is enough.** In a test where one machine sends the same bytes straight to another's UDP port,
  `psnr_relay_unwrap` returns 1 and `psnr_relay_unwrap_from` returns 0.

Wire reference: [protocol.md sections 10.1 to 10.3](protocol.md#101-route) and [api.md, Relay](api.md#relay).

## 13. LAN discovery from the client

**What it is for.** A player on the same local network as a server can find it without typing an address. You offer this as
a convenience next to a manual host and port; it is not a replacement for one.

**The model.** `psnr_discover` opens its own temporary UDP socket, the only UDP socket the library ever opens. It sets
`SO_BROADCAST`, sends one 64-byte DISCOVER packet (`PSNR` `0x04`, a nonce, zero padding) to the limited broadcast address on `port` and, on
POSIX, to the directed broadcast address of each IPv4 interface that is up (at most 32 destinations, without duplicates; on
Windows only the limited broadcast address). It collects replies until the timeout passes or `max` servers have answered, and closes the
socket. A reply is exactly 52 bytes, type `0x84`, and echoes the nonce; duplicates with the same address and TCP port are
collapsed. Port 0 means 36100.

```c
/* unit: disc */
#include "psnr.h"

#include <stdio.h>
#include <stdlib.h>
#include <string.h>

/* Find a server on the LAN, or fall back to a typed host:port. Returns a connected client or NULL. */
psnr_client* connect_lan_or_manual(const char* manual_host, uint16_t manual_port, const char* comm_id,
                                   const char* name, uint16_t p2p_port, uint32_t* user_id)
{
    psnr_found found[8];
    int n = psnr_discover(0, 1000, found, 8);   /* blocks up to 1000 ms: call it from the networking owner */
    int i;
    for (i = 0; i < n; i++) {
        char host[16];
        snprintf(host, sizeof(host), "%u.%u.%u.%u", found[i].ip[0], found[i].ip[1], found[i].ip[2], found[i].ip[3]);
        printf("found \"%s\" %s at %s tcp %u relay %u players %u\n", found[i].name, found[i].version, host,
               found[i].tcp_port, found[i].relay, found[i].players);
    }
    if (n > 0) {
        char host[16];
        snprintf(host, sizeof(host), "%u.%u.%u.%u", found[0].ip[0], found[0].ip[1], found[0].ip[2], found[0].ip[3]);
        return psnr_connect_timeout(host, found[0].tcp_port, comm_id, name, p2p_port, user_id, NULL, 3000);
    }
    if (n < 0) fprintf(stderr, "discovery could not send; use the typed address\n");
    if (manual_host) return psnr_connect_timeout(manual_host, manual_port, comm_id, name, p2p_port, user_id, NULL, 3000);
    return NULL;
}
```

**What you get.** `psnr_found` has `ip[4]` (the address the reply came from), `tcp_port` (the port to hand to
`psnr_connect`), `udp_port`, `relay` (reply flag bit 0), `players`, and NUL-terminated `name` (up to 24 characters) and
`version` (up to 12) in printable ASCII, with other bytes replaced by `?`. The ports are what the server is bound to; behind
a container port mapping or a router forward they can differ from what players use to reach it.

**What makes it find nothing.** Discovery is deliberately narrow, so a silent result is normal:

| Cause | What you see |
|---|---|
| The server was not started with `-discover` | 0 found, after the whole timeout |
| The server listens only on `127.0.0.1` (even with `-discover`) | 0 found; it needs a wildcard or interface address |
| The server's source check: it answers private, CGNAT (carrier-grade NAT range), link-local or loopback senders, never a public one | 0 found from outside the LAN |
| Broadcasts do not cross subnets, and Wi-Fi client isolation drops them | 0 found |
| No broadcast-capable interface or route | `-1` |
| DISCOVER requests share the server's UDP rate limit | 0 found under load |
| A reply that is not exactly 52 bytes, or does not echo the nonce | ignored |

The server's name comes from its `-name` flag. A server behind Docker's userland proxy or a source-rewriting NAT may see
a private source address for requests from outside; operators should keep `-discover` off there. `GET_CAPS` bit 6
(`PSNR_CAP_DISCOVER`) tells a connected client that discovery is on. See also
[connecting.md](connecting.md#finding-a-server-on-the-lan).

**Testing without touching your network.** On Linux you can use an isolated network namespace and a dummy interface, so that
no packet leaves the machine (this needs unprivileged user namespaces; run it as your normal user):

```sh
unshare -rn bash -c '
  ip link set lo up; ip link add dummy0 type dummy
  ip addr add ADDRESS/24 brd + dev dummy0; ip link set dummy0 up
  ./re-psn-server -addr :36100 -discover -name "Demo host" -http "" -db off -relay 2>/dev/null &
  SP=$!; sleep 0.7; ./disc; kill $SP'
```

Replace `ADDRESS` with any private IPv4 address that is not in use on your machine, and build `./disc` from the code
above with a `main` that calls `psnr_discover(0, 1000, found, 8)` and prints each entry. In a check of this kind the program
printed the server's name, its version, the dummy interface's address, `tcp 36100`, `udp 36100`, `relay 1` and `players 0`,
and `connect_lan_or_manual` connected to it.

### 13.1 API reference for this chapter

| Signature | Returns | Errors and notes | Blocks |
|---|---|---|---|
| `psnr_discover(port, timeout_ms, out, max)` | number found (0 or more), or -1 | -1 for `out == NULL`, `max <= 0`, a socket failure, or nothing could be sent; negative `timeout_ms` counts as 0; does not touch `psnr_connect_error` | up to `timeout_ms` (the whole time when nobody answers) |
| `psnr_connect_error()` | last HELLO ERROR code, or 0 | process-global, can be stale | no |

### 13.2 Pitfalls

- An unanswered search blocks for the whole timeout. Never call it from the frame loop.
- On Windows only the limited broadcast address is used; the per-interface directed broadcasts are POSIX only.
- Never connect to the first server you find without showing the player its name and address.
- Always keep a manual host and port path.

Wire reference: [protocol.md section 9.3](protocol.md#93-discover) and [api.md, UDP](api.md#udp).

## 14. Sign-in passes and AUTH from the client

**What it is for.** A host can run the server so that a player must present a **sign-in pass** before using the relay
(`-require-pass relay`), or before doing anything at all (`-require-pass connect`, or `connect,relay`). As a client
developer you need to know what to send, when, and what the answers mean.

**No login service exists yet.** A pass comes from a login service the host runs itself. That service is not part of this
server or of the supported pieces of this repository, and the project has no login service of its own. Until a host has such a
service, you will not hold a real pass, and everything in this chapter is exercised only against test servers with
throwaway keys. Do not describe to players where passes come from.

**The model.**

- The client never fetches, stores, parses, verifies or logs a pass. Your program holds opaque bytes (1 to
  `PSNR_PASS_MAX`, 2048). `psnr_auth` sends them and reports the answer; the library's temporary copy is wiped before it
  returns. Nothing the library prints or returns contains the pass.
- The player's own settings decide whether a pass is sent and to which server. Do not send a pass to a server just because
  it asked: `ERROR 1` to AUTH, HELLO_ACK flag bit 3 (`psnr_pass_hint`) and CAPS bit 7 are hints that a forged plain-TCP
  server can fake, and a pass is a bearer secret on a plain TCP connection.
- Version 2 (key-bound) passes are refused.

### 14.1 The two modes

| | Relay mode (`-require-pass relay`) | Connect mode (`-require-pass connect`, or `connect,relay`) |
|---|---|---|
| HELLO | normal; you get a real token | parked: the token is 0, `psnr_pass_hint(c)` is 1, no name, no listing, no UDP or relay lookup |
| What needs a pass | streams and relayed datagrams, at **both** ends, with the relay scope | everything except HELLO, AUTH, HEARTBEAT and GET_CAPS |
| GET_CAPS | bit 7 (`PSNR_CAP_PASS_RELAY`) set; bit 5 (`PSNR_CAP_RELAY`) clear until a relay-scope pass is accepted | only bit 7, or 0, and an empty version |
| AUTH reply | OK with no body; your HELLO token stays | the first AUTH registers you (name check, account limit); its OK carries the UDP token (4 bytes), which `psnr_auth` stores |
| Without a pass | datagrams are dropped and counted `relay_no_pass`; streams get ERROR 10 | any other request gets ERROR 10; a second HELLO gets ERROR 1; no AUTH within 30 s of accepting the connection closes it (HEARTBEAT does not extend this) |

In connect mode the token your probe and relay packets carry changes when `psnr_auth` returns, and a refresh returns the
same token. `psnr_probe_packet` and `psnr_relay_wrap` read the token at call time, so a packet built (or cached) before
AUTH carries 0 and is dropped. Rebuild after AUTH. `psnr_relay_available` mirrors the HELLO_ACK flag, which describes
the server and title; with `-require-pass relay` it stays 1 for a player who cannot use the relay, and AUTH does not refresh it.
`GET_CAPS` bit 5 is the per-player answer.

### 14.2 Sending a pass

Send AUTH after HELLO (any request before HELLO gets ERROR 7, AUTH included). A second AUTH on the same connection is a
**refresh**: it must be for the same account (otherwise ERROR 10), it replaces scopes and expiry, and in connect mode the
new pass must still carry the `connect` scope. Do it before the current pass runs out. A pass lives at most 3600 seconds,
with 60 seconds of clock skew allowed, so a pass can serve a connection for about 62 minutes; in connect mode the
connection closes 60 seconds after `exp` unless refreshed. A refused AUTH changes nothing.

```c
/* unit: auth */
#include "psnr.h"

#include <stdio.h>

/* Text for each psnr_auth result. Never include the pass in any message. */
const char* auth_message(int rc)
{
    switch (rc) {
    case PSNR_AUTH_OK:           return "signed in";
    case PSNR_E_PASS:            return "pass refused or needed (see the server log for the reason)";
    case PSNR_E_NAME_TAKEN:      return "name already in use on this server";
    case PSNR_E_BAD_REQUEST:     return "server has no pass settings, or is too old for AUTH";
    case PSNR_AUTH_E_ARG:        return "bad argument; nothing was sent";
    case PSNR_AUTH_E_IO:         return "connection lost";
    case PSNR_AUTH_E_TIMEOUT:    return "no reply in time; the pass may have been accepted";
    case PSNR_AUTH_E_REPLY:      return "malformed reply";
    case PSNR_AUTH_E_NOMEM:      return "out of memory";
    default:                     return rc > 0 ? "server refused (see the server log)" : "unknown local error";
    }
}

/* Sign in once; if the reply merely timed out, the pass may already be accepted, so ask again once. */
int sign_in(psnr_client* c, const void* pass, size_t len)
{
    int rc = psnr_auth(c, pass, len, 5000);
    if (rc == PSNR_AUTH_E_TIMEOUT) rc = psnr_auth(c, pass, len, 5000);
    fprintf(stderr, "auth: %d (%s)\n", rc, auth_message(rc));   /* log the code, never the pass */
    return rc;
}

/* After AUTH in connect mode, build the probe again: bytes 9..12 hold the token. */
int probe_token_is_set(const psnr_client* c)
{
    uint8_t pkt[PSNR_UDP_PROBE_LEN];
    psnr_probe_packet(c, pkt);
    return psnr_get32(pkt + 9) != 0;
}
```

**Refusals are counted.** Three refused AUTH requests in a row (a refused name counts, and so does ERROR 8) make the server
close the connection, and the reply to the third may never arrive, so your client sees the connection closed
(`PSNR_DEAD_PEER_CLOSED`). A good AUTH resets the count. Do not loop on a refusal: retrying the same pass fails again.
Bytes after the pass blob also refuse it.

### 14.3 ERROR 10

`ERROR 10` (`PSNR_E_PASS`) means a pass was refused (answering AUTH), or a pass is needed: a `STREAM_*` leg under
`-require-pass relay`, or any request other than HELLO, AUTH, HEARTBEAT and GET_CAPS before sign-in under
`-require-pass connect`. The player gets the code only. The reason (a bad signature, expired, not yet valid, wrong audience,
missing scope, revoked, too many connections for one account, and so on) is written to the **server** log at WARN as
`pass refused reason=...`, at most once per reason per second. A pass, a key or an account number is never logged; accounts
appear as a short tag that changes at each server start. A stream leg's ERROR 10 is invisible to the stream calls: they
return -1.

### 14.4 API reference for this chapter

| Signature | Returns | Errors and notes | Blocks |
|---|---|---|---|
| `psnr_auth(c, pass, len, timeout_ms)` | `PSNR_AUTH_OK` (0), a **positive** server ERROR code, or a **negative** local failure | `-1` bad argument (`c` or `pass` NULL, `len` 0 or over 2048, `timeout_ms <= 0`; nothing is sent); `-2` dead connection or failed write; `-3` timeout (may still be accepted); `-4` malformed reply; `-5` out of memory. Positive: 10 refused or needed, 8 name taken (connect mode), 1 no pass settings or a server older than AUTH | up to `timeout_ms` |
| `psnr_pass_hint(c)` | 1 if HELLO_ACK flag bit 3 was set | a hint a forged server can fake | no |
| `psnr_call(c, PSNR_GET_CAPS, ...)` and `psnr_caps_parse` | bits and the version text | bit 5 relay for this player; bit 7 relay needs a pass | up to the timeout |

### 14.5 Pitfalls

- **Logging the pass**, or the grant, token or UDP token. They are bearer secrets. Log the numeric result only.
- **Treating `-3` as a refusal.** The pass may have been accepted; call again.
- **Calling AUTH from the frame loop.** `psnr_auth` blocks like `psnr_call`.
- **Expecting a client that never sends AUTH to work under connect mode.** It is refused on purpose, within 30 seconds.
- **Caching packets across AUTH or a refresh.** Rebuild them.
- **Sending a pass because the server hinted at one** (see the model above).

Wire reference: [api.md, Sign-in passes](api.md#sign-in-passes), [Connect mode](api.md#connect-mode),
[Sending a pass from a client](api.md#sending-a-pass-from-a-client) and
[protocol.md sections 14.3 and 14.4](protocol.md#143-the-sign-in-pass).

## 15. The local trophy SDK

**What it is for.** `psnr_app` lets a game report trophy unlocks to a **local** desktop app (or the standalone service
`re-psn-app profile serve`) on the same machine. It is separate from the network client: it has no server connection, no
accounts and no relay. It is not the game protocol, and it does not change the game protocol version.

**The model.**

- `client/psnr_app.h` and `client/psnr_app.c` are dependency-free C99, with no threads. You do not need `psnr.c`. On Windows
  link `ws2_32`.
- The app approves a **catalog** (title id, name, revision, and 1 to 1024 trophies with unique ids) and enables a **grant**
  for one title. The game never approves a catalog itself.
- The grant is a small text file the app writes, holding a port and a 32-byte capability (the "token"). The SDK reads that
  file and talks to `127.0.0.1` on that port. It never connects to a host a caller chose.
- Each call is one new TCP connection with one request and one response (wire v1, section 15.3).
- The SDK does not start the app, does not implement any console trophy or save format, does not award a platinum trophy
  by itself, and does not check that the player earned anything.

### 15.1 The grant descriptor

A grant is an ASCII text file of at most 512 bytes with exactly these three lines (a real one is about 100 bytes):

```text
re-psn-app-grant-v1
port=<decimal 1..65535>
token=<64 hex digits>
```

The loader accepts either letter case for the hex digits; the app writes lowercase. Treat the file as a **credential**:

- Keep it private (the app writes it with mode 0600) and never log its contents, put the token in command arguments, or
  send it in telemetry. Anyone on the same user account who can read it can report that title's trophies.
- It is valid for 15 minutes after it is issued. The desktop app and `profile serve` renew it every 5 minutes while they
  own it, but the SDK cannot renew it. It is invalid after the broker restarts, and the file is removed when the app or
  service stops. After a long sleep, or an app restart, a fresh grant is needed.
- The loader opens the file without following symbolic links on POSIX (`O_NOFOLLOW`) and without reparse points on Windows,
  needs a regular file of at most 512 bytes with no NUL bytes, and checks the three lines.

### 15.2 Calls and results

All functions return 0 on success, a status from the app (1 to 6), or a local code (100 to 103).

| Code | Name | Meaning |
|---|---|---|
| 0 | `PSNR_APP_OK` | success |
| 1 | `PSNR_APP_UNAUTHORIZED` | the app does not accept this token: expired, revoked, unknown, or wrong |
| 2 | `PSNR_APP_UNKNOWN_TROPHY` | the trophy id is not in the approved catalog |
| 3 | `PSNR_APP_BUSY` | defined by the SDK; the current app does not send it |
| 4 | `PSNR_APP_MALFORMED` | the app could not parse the request |
| 5 | `PSNR_APP_STORAGE_ERROR` | the app could not save |
| 6 | `PSNR_APP_RATE_LIMITED` | too many calls; the app allows 100 per second per grant |
| 100 | `PSNR_APP_UNAVAILABLE` | not reached: the grant file is missing or unreadable, or the app is not listening |
| 101 | `PSNR_APP_INVALID_ARGUMENT` | a `NULL` argument, a `timeout_ms` outside 1 to 60000, or a bad, oversize or non-regular grant |
| 102 | `PSNR_APP_INVALID_RESPONSE` | the reply was not valid, or the connection closed early (the app limits itself to 16 simultaneous connections) |
| 103 | `PSNR_APP_TIMEOUT` | the deadline passed |

`psnr_app_load_grant(client, path)` fills a `psnr_app_client` (`port`, `capability[32]` and `timeout_ms`, which it sets to
2000) and zeroes it again on any failure after the argument check. You may change `timeout_ms` to a value from 1 to 60000
afterwards. `psnr_app_unlock` and `psnr_app_status` fill a `psnr_app_trophy` with `unlocked` and `unlocked_at`, which is
Unix **milliseconds** of the app's first receipt (0 while locked). The result is zeroed before anything else happens.
Unlocking is monotonic and idempotent: a repeated unlock returns the **first** receipt time.

### 15.3 Wire v1

You only need this to write a test double or to understand a failure. One request per TCP connection to `127.0.0.1:port`:

| Bytes | Request (48 bytes) |
|---|---|
| 0-3 | `RPT1` |
| 4 | operation: 1 unlock, 2 status |
| 5-7 | zero |
| 8-11 | body length, 36 (u32 big-endian) |
| 12-43 | the 32-byte capability |
| 44-47 | trophy id (u32 big-endian) |

After the request the SDK half-closes its write side; the app requires end-of-stream after exactly 48 bytes before it
changes anything (extra or short bytes get status 4). The response starts with `RPR1`, a status byte, three zero bytes and a
u32 length. Status 0 has length 9: an `unlocked` byte (0 or 1), then a u64 of Unix milliseconds. Every other status has
length 0. The SDK checks each byte of the reply, requires an `unlocked` of 0 or 1, requires a nonzero time for an unlocked
trophy and zero for a locked one, requires an unlock call to return `unlocked == 1`, and then requires the connection to end
before it reports success. The three phases (connect, send, receive) share one absolute deadline that starts before the
connect.

```c
/* unit: app */
#include "psnr_app.h"

#include <string.h>

/* Build the 48-byte wire v1 request by hand (for a test double). op: 1 unlock, 2 status. */
void app_build_request(uint8_t op, const uint8_t capability[32], uint32_t trophy_id, uint8_t out[48])
{
    memset(out, 0, 48);
    memcpy(out, "RPT1", 4);
    out[4] = op;
    out[11] = 36;                      /* body length, big-endian: bytes 8..11 */
    memcpy(out + 12, capability, 32);
    out[44] = (uint8_t)(trophy_id >> 24);
    out[45] = (uint8_t)(trophy_id >> 16);
    out[46] = (uint8_t)(trophy_id >> 8);
    out[47] = (uint8_t)trophy_id;
}

/* Parse the 12-byte response header. Returns the status (0..6), or -1 if the header is not valid. */
int app_parse_header(const uint8_t b[12], uint32_t* body_len)
{
    if (memcmp(b, "RPR1", 4) || b[5] || b[6] || b[7] || b[4] > 6) return -1;
    *body_len = (uint32_t)b[8] << 24 | (uint32_t)b[9] << 16 | (uint32_t)b[10] << 8 | b[11];
    return b[4];
}
```

### 15.4 An adapter with an offline fallback

Calls are synchronous and block for up to `timeout_ms`. Run them on a worker queue, outside your engine's locks and frame
loop, and deliver the result through the runtime's normal callback queue. Never report an unlock the app did not confirm.
The adapter below keeps the unlocks it could not deliver, and tries them again when a fresh grant is available. Only
`PSNR_APP_OK` marks a trophy delivered; an unknown trophy (2) is dropped from the queue because retrying cannot fix it.

```c
/* unit: app (continued) */
#include <stdio.h>

#define PENDING_MAX 64

typedef struct {
    psnr_app_client app;
    int loaded;
    uint32_t pending[PENDING_MAX];   /* unlocks the game earned but the app has not confirmed */
    int npending;
} trophy_sink;

const char* trophy_result_text(int rc)
{
    switch (rc) {
    case PSNR_APP_OK:             return "ok";
    case PSNR_APP_UNAUTHORIZED:   return "grant expired or revoked: get a new one";
    case PSNR_APP_UNKNOWN_TROPHY: return "trophy is not in the approved catalog";
    case PSNR_APP_RATE_LIMITED:   return "rate limited";
    case PSNR_APP_UNAVAILABLE:    return "app not reachable or grant file missing";
    case PSNR_APP_INVALID_ARGUMENT: return "bad grant file or argument";
    case PSNR_APP_INVALID_RESPONSE: return "invalid or incomplete reply";
    case PSNR_APP_TIMEOUT:        return "timed out";
    default:                      return "app error";
    }
}

/* Load (or reload) the grant. The path comes from your settings; never print the file's contents. */
int trophy_load(trophy_sink* t, const char* grant_path)
{
    int rc = psnr_app_load_grant(&t->app, grant_path);
    t->loaded = rc == PSNR_APP_OK;
    if (rc) fprintf(stderr, "trophy grant: %d (%s)\n", rc, trophy_result_text(rc));
    return rc;
}

static void trophy_queue(trophy_sink* t, uint32_t id)
{
    int i;
    for (i = 0; i < t->npending; i++)
        if (t->pending[i] == id) return;
    if (t->npending < PENDING_MAX) t->pending[t->npending++] = id;
}

/* Report one unlock. On a local failure the id is kept for later; the return value is the SDK code. */
int trophy_report(trophy_sink* t, uint32_t id, uint64_t* unlocked_at_ms)
{
    psnr_app_trophy r;
    int rc = t->loaded ? psnr_app_unlock(&t->app, id, &r) : PSNR_APP_UNAVAILABLE;
    if (rc == PSNR_APP_OK) {
        if (unlocked_at_ms) *unlocked_at_ms = r.unlocked_at;
        return rc;
    }
    if (rc != PSNR_APP_UNKNOWN_TROPHY) trophy_queue(t, id);   /* keep it; a failure is never an unlock */
    fprintf(stderr, "trophy %u: %d (%s)\n", (unsigned)id, rc, trophy_result_text(rc));
    return rc;
}

/* Try the queued unlocks again, after a fresh grant was loaded. Returns how many are still pending. */
int trophy_flush(trophy_sink* t)
{
    uint32_t todo[PENDING_MAX];
    int n = t->npending, i;
    memcpy(todo, t->pending, sizeof(uint32_t) * (size_t)n);
    t->npending = 0;
    for (i = 0; i < n; i++) trophy_report(t, todo[i], NULL);
    return t->npending;
}
```

### 15.5 A worked example you can run

The repository has a synthetic catalog (`client/examples/app_trophies.json`, title `RE-PSN-SDK-DEMO`, trophies 1 bronze,
2 gold, 3 platinum) and a command-line program (`client/examples/app_trophy.c`). Build both halves:

```sh
cc -std=c99 -Wall -Wextra -Werror -Iclient client/psnr_app.c client/examples/app_trophy.c -o app-trophy
(cd app && go build -o re-psn-app ./cmd/re-psn-app)
```

Create a private profile directory, install the catalog, and start the service. Keep it running; it prints
`Local trophy service ready; game grant file written. No relay connection was opened.`

```sh
DIR=$(mktemp -d); chmod 700 "$DIR"
./app/re-psn-app profile create --dir "$DIR/profile" --name 'Demo player'
./app/re-psn-app trophies install --dir "$DIR/profile" --input client/examples/app_trophies.json
./app/re-psn-app profile serve --dir "$DIR/profile" --title RE-PSN-SDK-DEMO --grant-file "$DIR/grant.txt"
```

In a second terminal (`app-trophy GRANT unlock|status ID` prints `<id> <unlocked> <unlocked_at>`):

```sh
./app-trophy "$DIR/grant.txt" status 1     # 1 0 0
./app-trophy "$DIR/grant.txt" unlock 1     # 1 1 <unix milliseconds>
./app-trophy "$DIR/grant.txt" unlock 1     # the same milliseconds again: the first receipt time
./app-trophy "$DIR/grant.txt" status 99    # app request failed (2)
```

What the failures look like (the program exits 1, or 2 for bad usage):

| Situation | Output |
|---|---|
| grant file does not exist, or the service has stopped and removed it | `grant unavailable (100)` |
| a file of 100 zero bytes as the grant | `grant unavailable (101)` |
| a well-formed grant with the wrong token | `app request failed (1)` |
| trophy id not in the catalog | `app request failed (2)` |

The profile directory must be private (mode 700 on Unix), and the standalone service holds the profile's writer lock, so
stop it before other commands that change the profile. See [app-framework.md](app-framework.md) for the app and
[app-framework-api.md](app-framework-api.md) for the descriptor and local protocol reference.

### 15.6 API reference for this chapter

| Signature | Returns | Errors and notes | Blocks |
|---|---|---|---|
| `psnr_app_load_grant(client, path)` | 0 or 100/101 | 100 missing or unreadable file; 101 oversize, not a regular file, malformed or unknown prefix, or a `NULL` argument; `client` is zeroed on failure | local file read |
| `psnr_app_unlock(client, id, result)` | 0 or 1-6, 100-103 | `result` zeroed first; 101 if `timeout_ms` is outside 1 to 60000 | up to `timeout_ms` |
| `psnr_app_status(client, id, result)` | same | same | up to `timeout_ms` |

### 15.7 Pitfalls

- **Calling from the frame loop** or under an engine lock: every call blocks up to `timeout_ms`.
- **Treating a failure as an unlock**, or fabricating a success when the app is unavailable.
- **Not reconciling.** After a long sleep or an app restart a new grant is needed; resend what is pending.
- **Reading `unlocked_at` as game time.** It is the app's receipt time in milliseconds.
- **Mixing up 100 and 102.** 100 covers a missing grant file and a refused port (the app is not running); 102 also covers a
  connection the app closed early, for example when it is at its connection limit.
- **Logging the grant.** Do not log its contents or put the token in arguments.
- **Windows.** `psnr_app` calls `WSAStartup` and `WSACleanup` in balance for each request; `psnr.c` never calls
  `WSACleanup`.

## 16. Testing your integration

**What it is for.** Most client bugs are found before a game is involved. This chapter shows how to run a throwaway
server, how the repository tests its own client, how to make your tests repeatable, and how to read what the server says
about you.

### 16.1 A loopback server

Build the server and run it on loopback only. Pick a random port above 40000 so that parallel runs do not collide. The
status page needs its own port.

```sh
go build -o ./re-psn-server ./server
P=$((40000 + RANDOM % 20000)); H=$((P + 1))
./re-psn-server -addr 127.0.0.1:$P -http 127.0.0.1:$H -db memory -relay -relay-force -log-level debug 2>server.log &
SERVER=$!
# ... run your test against 127.0.0.1:$P ...
kill $SERVER
```

- `-db memory` keeps scores in memory, so a run leaves nothing behind.
- `-relay` turns the relay on. `-relay-force` makes ROUTE bit 0 set and makes streams use the relay from one machine. It is
  for testing, warns at start, and does **not** force datagrams.
- `-log-level debug` (or `-v`) adds room, relay and P2P-socket events; the default level shows warnings.
- `-http` serves the status page, `/api/stats` and `/healthz`, read-only.
- Use `-name` and `-discover` only for discovery tests (section 13).
- The default game port is 36100 for TCP and UDP, and the C library expects the UDP server at the TCP address and port.

On loopback, probe results and endpoints are `127.0.0.1` with the ports your sockets bound. That tests your code, not your
router. Give every local instance its own name and peer port: the same name from the same address replaces the older
connection (the old client's next `psnr_poll` returns -1 with `PSNR_DEAD_PEER_CLOSED`).

### 16.2 The repository's test layout

| Test | What it covers |
|---|---|
| `client/test_psnr.c`, run by `TestCClient` (`server/client_test.go`) | HELLO, a room with a second client, pushes, messages, scores, a probe round trip, discovery with a fake responder, the relay source check, decoders, name limits, a connect timeout against a silent listener, the connect policy. Also builds `client/examples/minimal.c` and checks its argument errors |
| `client/test_auth.c`, `TestCClientAuth` | AUTH against servers with throwaway keys and passes (modes `connect`, `taken`, `relay`, `old`) |
| `client/test_stream.c`, `TestCClientStreamDeadline` | stream deadlines against synthetic responders: fragmented header, stalled body, success, negative timeout, refusal |
| `client/test_connect_wait.c`, `TestCClientConnectWait` | Linux only. Wraps `connect`, `poll` and `clock_gettime` at link time to inject interrupted calls and a fake clock |
| `client/test_relay_pair.c`, `TestCClientRelayPair` | POSIX only. Two native clients move 65553 bytes each way through the real relay |
| `TestCClientReportsRTT` (`server/rtt_client_test.go`) | the round trip a client reports reaches the server |
| `client/python/test_psnr_client.py` | the Python client, against `./server` or the binary named in `PSNR_SERVER`; skips if neither |
| `server/relay_test.go` and the other `relay_*_test.go`, `server/udp_test.go` | server-side relay and UDP behaviour |
| `lab/run.sh` | NAT scenarios in Docker: cone, symmetric, symmetric-norelay, samelan |

Run them with `go test ./server`, with `python3 tools/loopback.py [--race]` (it requires the named tests to run, with no
skips, and strips `RE_PSN_*` from the environment), and with `python3 client/python/test_psnr_client.py`. Read
[testing.md](testing.md) for what the runner checks and what it does not: it does not run a game, prove Internet
reachability or exercise a router's NAT.

### 16.3 Make tests deterministic

Prefer a test that decides its own outcome over one that sleeps and hopes. The repository uses these techniques, and you can
borrow them:

- **Synthetic responders** that fragment, stall or refuse the server's replies, so a deadline test is exact
  (`client/test_stream.c`, `server/client_stream_test.go`).
- **Link-time wrapping** of `connect`, `poll` and `clock_gettime` with a fake clock, so interruption and budget tests need
  no sleeps (`client/test_connect_wait.c`, linked with `-Wl,--wrap=...`).
- **A silent listener** that accepts into its backlog and never answers, to test `psnr_connect_timeout`.
- **A fake DISCOVER responder** on loopback, so discovery is tested without broadcasts.
- **Hand-built payloads**: feed bytes you wrote to the decoders, and check that every cut-short copy is refused.
- **Throwaway signing keys** for pass tests.
- **Asserts on byte layouts and reply types**, not on log text.
- Bind `127.0.0.1` and free ports only.

Here is a decoder test with no server at all: it builds a member entry by hand, parses it, and checks that every shorter
copy is refused.

```c
/* unit: unit_test */
#include "psnr.h"

#include <assert.h>
#include <stdio.h>
#include <string.h>

int main(void)
{
    /* member: member_id u16, user_id u32, online_id[16], ip[4], p2p_port u16, owner u8, data blob (u16 + bytes) */
    uint8_t e[2 + 4 + 16 + 4 + 2 + 1 + 2 + 3], *p = e;
    psnr_member m;
    size_t used = 0, cut;
    memset(e, 0, sizeof(e));
    p = psnr_put16(p, 7);
    p = psnr_put32(p, 1234);
    memcpy(p, "alice", 5); p += 16;
    p[0] = 10; p[1] = 0; p[2] = 0; p[3] = 5; p += 4;
    p = psnr_put16(p, 40001);
    *p++ = 1;
    p = psnr_put16(p, 3);
    memcpy(p, "abc", 3);

    assert(psnr_member_parse(e, sizeof(e), &m, &used) == 1);
    assert(used == sizeof(e) && m.member_id == 7 && m.user_id == 1234 && m.p2p_port == 40001);
    assert(!strcmp(m.online_id, "alice") && m.ip[0] == 10 && m.ip[3] == 5 && m.owner == 1);
    assert(m.data_len == 3 && !memcmp(m.data, "abc", 3));
    for (cut = 0; cut < sizeof(e); cut++) assert(psnr_member_parse(e, cut, &m, NULL) == 0);
    puts("decoder ok");
    return 0;
}
```

### 16.4 What to log

The library has no logging and no function that turns a code into text. You log, per connection:

- the `psnr_connect` result, and `psnr_connect_error()` when it is `NULL` (only the last HELLO diagnostic; it is
  process-global and can be stale after a DNS or TCP failure);
- `user_id` and the public address the server saw (the `out_public_ip` of connect);
- `psnr_relay_available`, the GET_CAPS bits and version, and `psnr_pass_hint`;
- when `psnr_poll` returns -1: `psnr_dead_reason` and `psnr_last_os_error`;
- every `PSNR_ERROR` reply: the request type and the code (the first 4 bytes of `data`);
- ROUTE flags per peer, the probe reply endpoint against your HELLO port, and which path each peer uses (direct, relay
  datagram, relay stream);
- the round trip, `psnr_rtt_ms`.

Never log a sign-in pass, a grant file's contents, or the user token or UDP token. They are bearer secrets (the server does
not log the probe token either).

```c
/* unit: diag */
#include "psnr.h"

#include <stdio.h>

/* One block of connection facts for a debug overlay or a log line. Contains no secrets. */
void diagnose(psnr_client* c, uint32_t user_id, const uint8_t public_ip[4])
{
    uint32_t bits = 0;
    psnr_msg m;
    psnr_caps caps;
    printf("user %u, server sees us as %u.%u.%u.%u\n", user_id, public_ip[0], public_ip[1], public_ip[2], public_ip[3]);
    printf("relay announced: %d, pass hint: %d, rtt: %d ms\n", psnr_relay_available(c), psnr_pass_hint(c), psnr_rtt_ms(c));
    if (psnr_call(c, PSNR_GET_CAPS, NULL, 0, &m, 2000) == 1) {
        if (m.type == PSNR_CAPS && psnr_caps_parse(&m, &caps)) {
            bits = caps.bits;
            printf("server %.*s caps 0x%02x:%s%s%s%s%s%s%s%s\n", (int)caps.version_len, (const char*)caps.version, bits,
                   bits & PSNR_CAP_ROOMS ? " rooms" : "", bits & PSNR_CAP_SCORES ? " scores" : "",
                   bits & PSNR_CAP_ROOM_CODES ? " codes" : "", bits & PSNR_CAP_PEER_KEYS ? " peer-keys" : "",
                   bits & PSNR_CAP_PROBE ? " probe" : "", bits & PSNR_CAP_RELAY ? " relay" : "",
                   bits & PSNR_CAP_DISCOVER ? " discover" : "", bits & PSNR_CAP_PASS_RELAY ? " pass-relay" : "");
        }
        psnr_msg_free(&m);
    }
}
```

### 16.5 Reading the server log and the status page

The server writes structured text (`-log-format json` is available; `-log-file` appends with mode 0600). What to look for:

| Level | Line | Tells you |
|---|---|---|
| INFO | `hello client=N comm_id=... online_id=... addr=... p2p_port=...` | the title id, name and peer port the server saw; check they are what you meant |
| INFO | `reconnected client=N replaces=M online_id=...` | a same-name takeover: a second instance used the same name from the same address |
| INFO | `disconnected client=N`, `read failed client=N err=...` | the connection ended, and why |
| WARN | `UDP probe refused ... reason="source address differs from the TCP connection's" udp=... tcp=...` | your UDP leaves from a different address than your TCP (once per player) |
| WARN | `request refused reason=...`, `unknown request type` | a bad request |
| WARN | `connection closed reason="request rate"`, `"no sign-in in time"`, `"pass expired"`, `"wrong room codes"` | why the server closed you |
| WARN | `pass refused reason=...`, `hello refused reason="name taken"` | the reason behind ERROR 10 or ERROR 8 |
| WARN | `relay stream closed stream=N reason="pass expired"` (or `pass key removed`, `pass revoked`) | a stream ended with its pass |
| DEBUG | `P2P socket seen client=N addr=ip:port local=ip` | the server **accepted** a probe |
| DEBUG | `relay stream stream=N from=A to=B vport=V` | a stream was set up |
| DEBUG | `UDP probe ignored: the title is in -no-peer-titles` | the server will not help this title with peer traffic |

UDP drops have **no per-packet log line**; read the counters instead. Some warnings are limited to one a second and then
carry `skipped_since_last_line`. Full list: [running.md, Logging](running.md#logging).

The status page is at `/`, refreshes every 5 seconds, and shows rooms and leaderboards. `/api/stats` is the same data as
JSON (built at most once a second), and `/healthz` answers 200 `ok` or 503. Keys that matter for a client problem:

| Key | Use |
|---|---|
| `rooms[]` (`id`, `title`, `label`, `members[]`, `max`, `age`, `verified`) | is your room listed under the right title; does it have the members you expect |
| `rooms[].verified` | members whose probe was accepted; lower than the member count means a probe never arrived or was refused |
| `rooms[].rtt_ms[]` | `null` when a client never called `psnr_report_rtt(c, 1)` or the value is older than 3 minutes |
| `verified_players` | the same count across the server |
| `udp_drops` | counters: `rate_limit`, `source_capacity`, `malformed`, `unauthorized`, `source_mismatch`, `relay_disabled`, `write_error`, `discover_refused`, `relay_no_pass` |
| `relay`, `relay_bytes` | whether a relay is on; relayed payload bytes (flat while you expect relay traffic means the data is direct or never wrapped) |
| `tcp_refusals` | connection caps (`conns`, `conns_per_addr`, `req_rate`) |
| `limits`, `version`, `uptime`, `clients`, `connections_total` | context |

Counters are cumulative since the server started and reset on restart. No addresses or tokens appear. Reading the counters:

```sh
curl -s http://127.0.0.1:$H/api/stats | grep -o '"udp_drops":{[^}]*}'
```

| `udp_drops` field rising | Look at |
|---|---|
| `unauthorized` | a bad token, a target that never probed, no shared room, or not signed in (connect mode); also a probe before AUTH |
| `source_mismatch` | your UDP leaves from a different public address than your TCP |
| `rate_limit` | the per-source UDP budget; players behind one NAT share it |
| `malformed` | wrong magic, a short packet, an IPv6 source, a PUNCH sent to the server |
| `relay_disabled` | a wrapped datagram sent to a server without `-relay` |
| `relay_no_pass` | `-require-pass relay` and no relay-scope pass at one end |
| `discover_refused` | a discovery request from a source the server will not answer |

### 16.6 A harness in Python

The single-file Python client (`client/python/psnr_client.py`, standard library only, not thread-safe) does the TCP side:
`Client(host, port, comm_id, online_id, p2p_port=0, timeout=5.0)` performs HELLO and exposes `user_id`, `public_ip`, `token`
and `flags`. `call(type, body)` returns the payload after the request id and raises `ServerError` (with `.code`) on an
ERROR; pushes (`0xA0` to `0xAF`) are collected and read with `pop_pushes()`. It has `heartbeat`, `caps`, `create_room`,
`join_room`, `leave_room`, `search_rooms`, `set_room_data`, `room_message`, `kick`, `record_score` and `get_ranking`. It
does **not** cover UDP, relay streams, sign-in passes, peer keys, room access or ranking by id, and it never sends
heartbeats on its own (the server drops a silent connection after 90 seconds). A lost connection raises `ConnectionError`.

UDP is a few lines of `struct` and a socket. This script probes two players, relays one datagram, checks the sender the
right way, and reads `/api/stats`. Start a server with `-relay` (section 16.1) and run
`python3 harness.py REPO_DIR PORT HTTP_PORT`.

```python
# unit: py_harness
import json, socket, struct, sys, urllib.request

sys.path.insert(0, sys.argv[1] + "/client/python")
from psnr_client import Client

port, http = int(sys.argv[2]), int(sys.argv[3])


def p2p_socket():
    s = socket.socket(socket.AF_INET, socket.SOCK_DGRAM)
    s.bind(("127.0.0.1", 0))     # bind first: HELLO carries this port
    s.settimeout(1.0)
    return s


def probe(c, sock):
    # "PSNR" 01 | user_id u32 | token u32 | local_ip [4]
    pkt = b"PSNR\x01" + struct.pack(">II", c.user_id, c.token) + socket.inet_aton(sock.getsockname()[0])
    sock.sendto(pkt, ("127.0.0.1", port))
    reply, _ = sock.recvfrom(64)
    assert reply[:5] == b"PSNR\x81" and len(reply) >= 11
    return socket.inet_ntoa(reply[5:9]), struct.unpack(">H", reply[9:11])[0]


def wrap(c, to_user, payload):
    # "PSNR" 03 | from u32 | token u32 | to u32 | payload
    return b"PSNR\x03" + struct.pack(">III", c.user_id, c.token, to_user) + payload


ua, ub = p2p_socket(), p2p_socket()
a = Client("127.0.0.1", port, "RE-PSN-DEMO", "py-alice", ua.getsockname()[1])
b = Client("127.0.0.1", port, "RE-PSN-DEMO", "py-bob", ub.getsockname()[1])
print("relay announced:", a.flags & 1, "caps:", a.caps())
print("alice endpoint", probe(a, ua), "bob endpoint", probe(b, ub))
room = a.create_room(4)
b.join_room(room["room_id"])
ua.sendto(wrap(a, b.user_id, b"hello from python"), ("127.0.0.1", port))
data, src = ub.recvfrom(2048)
assert src == ("127.0.0.1", port), src      # the sender check: only the server may vouch for from_user
assert data[:5] == b"PSNR\x03"
from_user, = struct.unpack(">I", data[5:9])
print("bob got", data[9:], "from", from_user, "(alice is", a.user_id, ")")
stats = json.load(urllib.request.urlopen("http://127.0.0.1:%d/api/stats" % http))
print({k: stats[k] for k in ("version", "relay", "relay_bytes", "verified_players")})
b.close()
a.close()
```

### 16.7 API reference for this chapter

| Signature | Returns | Errors and notes | Blocks |
|---|---|---|---|
| `psnr_poll(c, &msg)` | 1 message, 0 none, -1 dead | -1 only after queued messages drain; sends HEARTBEAT every 20 s when idle, but **only when you call it** | no |
| `psnr_call(c, type, body, len, &reply, timeout_ms)` | 1 reply, 0 timeout, -1 dead | a server ERROR is a normal reply (return 1, type `PSNR_ERROR`); a late reply may still arrive | up to `timeout_ms` |
| `psnr_dead_reason(c)` | `PSNR_DEAD_*` | 1 peer closed, 2 I/O error, 3 out of memory, 4 send timeout | no |
| `psnr_last_os_error(c)` | OS error code | | no |
| `psnr_rtt_ms(c)`, `psnr_report_rtt(c, on)` | round trip; opt-in to report it in HEARTBEAT | the status page shows `rtt_ms` only if you report | no |
| `psnr_msg_free(&msg)` | void | free every message you receive | no |

### 16.8 Pitfalls

- **A silent test.** A UDP drop prints nothing; always read `udp_drops` after a test that expected delivery.
- **Sleeping instead of waiting for an event.** Poll until a ROUTE, OFFER or reply arrives, with a deadline.
- **Two instances, one name.** The second kills the first.
- **Reusing a port or a name across parallel runs.**
- **Timing a call inside a `printf` argument list.** C does not define the order in which arguments are evaluated; run the
  call first, then print.
- **Testing only on loopback.** It proves your code, not your NAT behaviour; use the lab or two real networks too.

## 17. Troubleshooting and the checklist before you ship

### 17.1 Troubleshooting table

This extends the short table in [integrating.md](integrating.md#troubleshooting); it does not repeat it.

| Symptom | Likely cause | Check |
|---|---|---|
| `psnr_connect` returns `NULL` | a comm_id over 12 bytes or an online_id over 16 (not cut, refused); the connect policy refused (`psnr_connect_error()` is 1000); the name is taken (8); DNS or TCP failure; no HELLO_ACK in the time allowed | `psnr_connect_error()`, then the server log for `hello refused`; use a numeric IPv4 host |
| Connected, probe never answered | probe sent from another socket than the announced one; wrong or zero token (connect mode before AUTH); the UDP source differs from the TCP address; the title is in `-no-peer-titles`; UDP blocked; rate limit | `udp_drops`, the WARN `UDP probe refused`, `verified` on the room, DEBUG `P2P socket seen` |
| Peers see `127.0.0.1` or a wrong address | a loopback connection; the member had not probed when you read its entry; an IPv6 client (address `0.0.0.0`) | the member-endpoint rule in section 11.3; read the entry again after the peer's probe |
| A direct packet never arrives | the peer did not punch; a symmetric NAT; the filter dropped it before the game saw it | the receive order in section 11.5; try the relay |
| A relayed datagram never arrives | the target never probed; no shared room; no `-relay`; a missing pass; a wrong or stale token; source mismatch | `udp_drops` (`unauthorized`, `relay_disabled`, `relay_no_pass`, `source_mismatch`); `relay_bytes` flat |
| Relayed data arrives but `from_user` is unexpected | you used `psnr_relay_unwrap` without a source check | switch to `psnr_relay_unwrap_from` with the `recvfrom` address |
| `unwrap_from` rejects every relayed datagram | the server's UDP port differs from its TCP port, or you passed the wrong source | `psnr_server_addr` against the datagram's source |
| `psnr_stream_connect` returns -1 at once | timeout 0; no shared room; the server has no relay; a policy refusal | `GET_CAPS` bit 5, the server log, `psnr_connect_error()` |
| `psnr_stream_connect` returns -1 after seconds | the peer did not call `psnr_poll` and accept within 10 s; your timeout was shorter | does the other side drain pushes; DEBUG `relay stream` |
| A stream closes by itself | idle limit (`-relay-idle`, default 600 s); a pass ended; the peer closed | WARN `relay stream closed ... reason=` |
| Disconnect after about 90 seconds | nothing was sent: no `psnr_poll` or `psnr_heartbeat` during a long load | poll or send a heartbeat from a pump while loading |
| Your first instance dies when the second starts | same name from the same address | `reconnected ... replaces=` in the log |
| `psnr_poll` returns -1 | the server closed or restarted; an I/O error; a send timeout of 5 s | `psnr_dead_reason`, `psnr_last_os_error`; nothing reconnects by itself |
| `psnr_auth` returns 10 | pass refused or needed | the server log `pass refused reason=...` |
| `psnr_auth` returns 8 | the name is taken (connect mode) | the log `hello refused` |
| `psnr_auth` returns 1 | the server has no pass settings, or is older than AUTH | `GET_CAPS` bit 7; the server's version |
| `psnr_auth` returns -3 | no reply in time; may have been accepted | call again once |
| The connection closes after repeated AUTH | three refused AUTH in a row | stop retrying a refused pass |
| `psnr_discover` returns 0 | no `-discover`; the server listens only on loopback; another subnet; Wi-Fi isolation | section 13 |
| `psnr_discover` returns -1 | no broadcast-capable interface, a `NULL` buffer, or `max <= 0` | section 13 |
| `psnr_app_*` returns 100 | the grant file is missing or the app is not running | is the service running; does the file exist |
| 101 | the grant file is malformed, oversize or not regular; or a bad `timeout_ms` | the file's three lines; the 512-byte limit |
| 102 | an invalid reply; the app closed the connection early | the app's log; its 16-connection limit |
| 103 | the deadline passed | the app responding; `timeout_ms` |
| `psnr_app_*` returns 1, 2 or 6 | grant expired or revoked; trophy not in the catalog; rate limit | a fresh grant; the approved catalog; slow down |
| Windows: a stream socket compares badly | the handle was cast to a 32-bit `int` | keep the `int64_t`; close with `closesocket` |

### 17.2 Checklist before you ship

Identity and sockets

- [ ] Every local instance has a unique name and a unique peer port.
- [ ] The UDP peer socket is bound **before** `psnr_connect`, and its port is passed to it.
- [ ] You probe from that socket, again until the first reply, and about every 15 seconds afterwards.
- [ ] On POSIX your process ignores `SIGPIPE` (or the relevant writes are made with `MSG_NOSIGNAL`); the library writes with
  plain `send()`.

Receiving

- [ ] The receive path tests, in this order: not control, probe reply, `psnr_relay_unwrap_from`, then drops the rest.
- [ ] You use `psnr_relay_unwrap_from` everywhere, with the address from `recvfrom`.
- [ ] No game datagram of 5 bytes or more starts with `PSNR`.
- [ ] You copy a relayed payload out of the receive buffer before reusing it.

Connection and threads

- [ ] One thread owns each `psnr_client`. Every `psnr_msg` is freed.
- [ ] The client is polled, or `psnr_heartbeat` is sent, during long loads (the server drops silence after 90 seconds).
- [ ] Anything that blocks (`psnr_connect`, `psnr_call`, `psnr_auth`, `psnr_discover`, `psnr_stream_*`, `psnr_app_*`) runs off
  the frame loop with a deadline.
- [ ] You decide what happens after a drop: nothing reconnects by itself.
- [ ] If the host restricts targets, `psnr_set_connect_policy` is set before the first connect.

Relay and passes

- [ ] You check `GET_CAPS` before relying on probe or relay.
- [ ] A stream is requested only after a shared room exists, with a timeout above 0, and the other side drains pushes.
- [ ] After AUTH or a refresh, probe and wrap packets are rebuilt.
- [ ] A refused pass is not retried in a loop.

Secrets and fallbacks

- [ ] Passes, grant contents and tokens are never logged, printed or put in arguments.
- [ ] Trophy unlocks that fail are kept and reconciled; a failure is never reported as an unlock.
- [ ] There is a typed host and port next to LAN discovery.

Limits to design for

| Item | Limit |
|---|---|
| Frame payload / `psnr_send` body | 65535 / 65531 bytes |
| comm_id / online_id | 12 / 16 bytes (names compare case-insensitively on the server) |
| Room or member blob | 4096 bytes |
| Pass | 1 to 2048 bytes |
| Relayed datagram payload | 65490 bytes (keep far below the MTU) |
| UDP probe / punch / relay header up / down | 17 / 9 / 17 / 9 bytes |
| Idle timeout / C heartbeat / C send stall | 90 s / 20 s / 5 s |
| Request rate | 200 per second per connection (`-max-req-rate`) |
| Connections | 4096 total, 32 per address |
| Rooms | 1000 total, 8 per player |
| Refused AUTH in a row | 3 |
| Connect-mode sign-in window | 30 s |
| Stream accept wait / idle / write stall | 10 s / 600 s / 30 s |
| UDP rate | 1000 per second per source address |
| Default port | 36100, TCP and UDP |

Tested

- [ ] On loopback with two instances, and on two machines.
- [ ] A direct path, a relay path, and the relay switched off (the failure is handled).
- [ ] The server restarted during play; the server refusing the pass; the server unreachable at start.
- [ ] You read `udp_drops` and the server log after each test and they say what you expect.

The full limits are in [protocol.md section 12](protocol.md#12-limits). The checks the project itself asks before a title is
called working are in [integrating.md](integrating.md#acceptance-checks-before-claiming-a-title-works); passing them is
evidence about your build, not a claim about any game.

## 18. Glossary

- **Title (communication ID).** The identifier `comm_id` that scopes rooms, leaderboards and relay permission to one title (chapter 1).
- **Online ID.** The player's name, `online_id`, at most 16 bytes. A claim, not an identity (chapter 1).
- **User ID.** The nonzero `user_id` the server assigns in HELLO_ACK. Unique, not dense (chapter 1).
- **Request, reply, push.** A request carries a request id and gets a reply. A push is a message the server sends without being asked, with a type from 0xA0 to 0xAF and no request id (chapter 7).
- **Capabilities.** The bit set from GET_CAPS that tells a client what the server supports (chapter 5).
- **Room.** A small group of players of one title, kept in the server's memory (chapter 6).
- **Member entry.** The endpoint information the server hands each room member about the others (chapter 11).
- **PROBE, PROBE_REPLY.** The UDP packets by which a client learns its router's mapping as the server sees it (chapter 11).
- **PUNCH.** The UDP packet two peers send each other so that each router opens a hole (chapter 11).
- **Relay.** The server's carriage of datagrams and streams when no direct path works; you choose to use it (chapter 12).
- **Pass.** A sign-in pass sent with AUTH when the server requires one (chapter 14).
- **Loopback.** The 127.0.0.1 address on which you run a development server.

## 19. Where to go next

- [api.md](api.md): how game clients talk to the server over TCP, message by message, with advice.
- [protocol.md](protocol.md): the reference for byte layouts, state machine, error codes and limits.
- [integrating.md](integrating.md): choosing an integration path for your port.
- [sdk.md](sdk.md): the client quick start (build with make or CMake, or use the Python client).
- [app-framework-api.md](app-framework-api.md): the app framework API.
- `client/examples/`: `minimal.c`, `app_trophy.c` and `app_trophies.json`, small programs to read next to the chapters.
