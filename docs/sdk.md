# Client quick start

How to build the C client, or use the single-file Python client, against a re-psn server. This is unofficial fan
work. There is no public instance and there are no accounts: run your own server on loopback while you develop.
For what the messages mean see [Protocol](api.md); for wiring a client into a recompiled game see
[Integrating re-psn into a recompiled game](integrating.md).

All commands run from the repository root. The repository's `nix develop` shell supplies Go, a C compiler and make only. The pkg-config and CMake steps below need
`pkg-config` (or `pkgconf`) and `cmake` from your system, or from a shell that adds them.

## Start a server

```sh
go build -o /tmp/re-psn-server ./server
/tmp/re-psn-server -addr 127.0.0.1:36100 -http "" -db off
```

It listens on loopback only, with the status page off and scores kept in memory. Leave it running in a second terminal.

## C client: build and install

The client is two files, `client/psnr.c` and `client/psnr.h` (plus `psnr_app.c`/`psnr_app.h` for the local trophy
SDK). Vendoring them is still the simplest route. For a system or project-local install, `client/Makefile` builds a
static library and a pkg-config file:

```sh
make -C client PREFIX=/tmp/psnr-prefix install
```

This installs `lib/libpsnr.a`, `include/psnr.h`, `include/psnr_app.h` and `lib/pkgconfig/psnr.pc`. `DESTDIR` is
honored. A `PREFIX` containing a space is not supported. The version in `psnr.pc` is the repository's `VERSION` file, which names the server release; the client has
no version number of its own yet.

Build and run the minimal example against the installed library:

```sh
cc -std=gnu11 -Wall -Wextra $(PKG_CONFIG_PATH=/tmp/psnr-prefix/lib/pkgconfig pkg-config --cflags psnr) \
  client/examples/minimal.c $(PKG_CONFIG_PATH=/tmp/psnr-prefix/lib/pkgconfig pkg-config --libs psnr) -o /tmp/re-psn-minimal
/tmp/re-psn-minimal 127.0.0.1 36100 demo_one
```

Success prints `HELLO user=...` and `ROOM_LIST total=... count=...`.

Use `-std=gnu11`. Strict `-std=c11` fails to compile `psnr.c`, because it uses POSIX clock and network-interface
declarations; `-std=c11 -D_POSIX_C_SOURCE=200809L` still fails on the network-interface flags (`IFF_BROADCAST`, `IFF_UP`),
so gnu mode is the supported way.

### CMake

`client/CMakeLists.txt` builds the same static library as the target `psnr::psnr` and installs a package file:

```sh
cmake -S client -B /tmp/psnr-build -DCMAKE_INSTALL_PREFIX=/tmp/psnr-cmake-prefix
cmake --build /tmp/psnr-build
cmake --install /tmp/psnr-build
```

In your project:

```cmake
find_package(psnr REQUIRED)
target_link_libraries(mygame PRIVATE psnr::psnr)
```

Pass `-DCMAKE_PREFIX_PATH=/tmp/psnr-cmake-prefix` when configuring your project. The package has no version file, so
`find_package(psnr 1.2 ...)` with a version is not supported; ask for `psnr` without one. The target keeps compiler extensions
on (gnu11) and links `ws2_32` on Windows; the Windows part is not tested here.

## Python client

`client/python/psnr_client.py` is one file with no dependencies beyond the Python 3 standard library. It covers
HELLO, heartbeat, GET_CAPS, rooms (create, search, join, leave, room data, room messages, kick) and scores. It does
not cover the relay, UDP probes, sign-in passes or peer keys.

```sh
python3 client/python/psnr_client.py 127.0.0.1 36100 demo_py
```

It prints your user id, the address the server sees and the room list. From your own code:

```python
import sys
sys.path.insert(0, "client/python")
from psnr_client import Client, ServerError

with Client("127.0.0.1", 36100, "RE-PSN-DEMO", "alice") as c:
    room = c.create_room(max_members=4, external=b"mode=1")
    print(c.search_rooms())
    print("rank", c.record_score(board=1, score=500))
    print(c.get_ranking(1))
    try:
        c.join_room(123456789)
    except ServerError as e:
        print(e.code, e)          # 2: not found
    c.leave_room(room["room_id"])
```

Pushes (member joined, room data, room message, kicked) arrive between replies; the client keeps them in
`c.pushes`, and `c.pop_pushes()` returns and clears them. Send a heartbeat at least every 90 seconds on an idle
connection, or the server drops it.

The client is not thread-safe, and every call waits up to the connection timeout (5 seconds by default). A lost
connection raises `ConnectionError` on a send or a receive; a timeout raises `socket.timeout` (`TimeoutError`); an `ERROR` reply raises
`ServerError`.

Run its tests (they build and start their own loopback server, and skip if neither `go` nor `PSNR_SERVER` is
available):

```sh
python3 client/python/test_psnr_client.py
```

## What is not here yet

- A Go client other apps can import: the one in `app/internal/client` is internal to the desktop app module.
- A client version number or a `psnr_error_string` function in the C client.
- Python support for the relay, probes and passes.
