# re-psn desktop app

The start of the desktop app for players and hosts of a re-psn server. It is a Go module of its own,
`github.com/undecuple/re-psn/app`, next to the server; the server's module is not changed by it and stays standard-library only.
The app has a terminal UI built on [Bubble Tea](https://github.com/charmbracelet/bubbletea); its dependencies are listed under
[Dependencies](#dependencies).

## Contents

- [What is here](#what-is-here)
- [Build and test](#build-and-test)
- [The terminal UI](#the-terminal-ui)
- [Optional native HTTP status](#optional-native-http-status)
- [Starting from the desktop](#starting-from-the-desktop)
- [The check command](#the-check-command)
- [Settings file](#settings-file)
- [Dependencies](#dependencies)
- [What the client does not do yet](#what-the-client-does-not-do-yet)

## What is here

| Path | What is there |
|---|---|
| `internal/client/` | A Go client for the server's TCP protocol ([docs/api.md](../docs/api.md)), with no UI |
| `internal/client/conformance_test.go` | Builds the real server from `../server`, starts it on free loopback ports and drives the client against it |
| `internal/config/` | Reads and writes the per-user settings file, `re-psn.ini` |
| `internal/tui/` | The terminal UI: a Bubble Tea model whose Update and View are tested with messages and a fake server, no terminal |
| `cmd/re-psn-app/` | The program: the terminal UI with no arguments, `check` and `version` |

The client can use the game protocol alone. An explicitly configured status URL adds native HTTP status data without
opening a browser; see [Optional native HTTP status](#optional-native-http-status).

## Build and test

Go 1.26 or newer:

```sh
cd app
go vet ./...
go test -race ./...
go build ./cmd/re-psn-app
```

The conformance test needs the `go` command to build the server; it skips, saying why, when the server cannot be built. It runs
the server with `-http ""`, so no status page is started.

## The terminal UI

```sh
re-psn-app            # start the terminal UI
re-psn-app --no-tui   # do not: print a note and exit 2
```

The UI needs a terminal on stdin and stdout. On a Linux desktop it can open an installed terminal when started without
one, for example by a file manager. See [Starting from the desktop](#starting-from-the-desktop).

**First start.** With no settings file yet, the app asks what it is for on this machine: **Play** (join servers), **Host** (run a
server; the Host screen comes later, Status works now) or **Both**. Then the online ID (not for Host) and the server as
`host:port`, each checked with the settings file's own rules. Enter saves `re-psn.ini`; Esc quits without saving, as does `q` on
the first question; backspace in an empty field goes back one step. While typing the server, Ctrl+F opens **Find on LAN** (below).

**Home.** A menu (Overview, Rooms, Leaderboards, Settings, Help), the chosen item's content beside it, and a line of keys at the
bottom. Under 70 columns the menu and the content are shown one at a time.

- **Overview:** mode, name, server and the connection in words: connected or not, the HELLO round trip, relay and invite-only
  rooms on or off, the address the server sees.
- **Rooms** and **Leaderboards** are the two tabs of the Status screen. Rooms belong to one title, so the screen asks for the
  title's communication ID (such as `NPWR00860_00`) first. Rooms shows ID, owner, members as current/max and the room flags.
  Leaderboards shows one board, whose id you type: rank, player, score and when it was recorded. With a configured HTTP
  status source, these screens instead show public room/member/age data and board leader summaries across titles.
- **Settings:** mode, name, server, status URL and theme. Enter changes the selected row; `s` saves to `re-psn.ini` (mode 0600).
  A refused value says which rule it broke. The optional status URL takes effect on refresh. Its token-file path is
  set in `re-psn.ini`; the UI does not display or edit the token itself.
  **Find on LAN:** `f` here (or Ctrl+F while typing the server, also on the first start) broadcasts a discovery request and lists
  the servers that answer, each with name, address:port, players, relay on or off and version. Up/down and Enter put the chosen
  address in the server field (in Settings it is then an unsaved change; `s` saves it); `r` searches again, Esc closes. The search
  runs in the background for about 1.5 s and never blocks the screen. With no answer the screen says: none found (the server must
  run with discovery on; it does not cross subnets). Typing `host:port` works as before. If the server field already has a port,
  the request goes to that UDP port, otherwise to 36100. The request is sent from a temporary UDP socket to 255.255.255.255 and to
  the broadcast address of each IPv4 interface that is up; only servers started with discovery on answer, and only senders on a
  private network. `internal/client` has `Discover` for it. `server = auto` in `re-psn.ini` is not supported.
- **Help:** the keys and what the screens can show.

| Key | What it does |
|---|---|
| ↑ / ↓, `k` / `j`, `1`-`5` | move in the menu or a list; the digits jump to a menu item |
| Enter | open the item; change a setting; finish typing |
| Esc | back to the menu; cancel typing |
| Tab | switch between Rooms and Leaderboards |
| `e`, `b` | type the title, the board id |
| `r` | ask the server again |
| `f`, Ctrl+F | find servers on the LAN (Settings; while typing the server) |
| `s` | save the settings |
| `t` | dark or light theme (saved with the settings) |
| `q`, Ctrl+C | quit (Ctrl+C also while typing) |

**Where the data comes from without a status URL.** The game protocol, through `internal/client`: each refresh connects to the configured server,
says HELLO under a throwaway name (`view-` and 10 random hex digits) for the title on screen, sends SEARCH_ROOMS (up to 50 rooms)
and, with a board id, GET_RANKING (the top 20), and closes the connection. Every refresh has a 5-second timeout. A failure is
shown as text on the screen, with the error, and the app keeps running. No HTTP request is made while the status URL is unset.

**What the protocol cannot show.** The server's status page shows some things the game protocol has no message for. The UI says
"not available over the protocol" in those places:

- the server's version, uptime and connection counts (Overview and the Status header);
- a room's age (the Age column of Rooms);
- which boards exist, for one title or across titles (so you type the title and a board id);
- full rooms and invite-only rooms, which SEARCH_ROOMS leaves out.

**Theme.** Dark by default, light with `t`; the colours are the gruvbox-material values the website uses
([docs/art/palette.json](../docs/art/palette.json)). In light mode the accents that are too faint as text are replaced by the
strong text colour in bold. No state is shown by colour alone. With `NO_COLOR` set the UI uses no colour, only bold and reverse.

## Optional native HTTP status

On Windows and Linux, the same terminal app can display the server's existing `/api/stats` directly. The server must
already have its opt-in HTTP listener enabled; setting the app's URL does not start a server, enable that listener or
open a browser. In `re-psn.ini`, for example:

```ini
[server]
server = 127.0.0.1:36100
status_url = http://127.0.0.1:36101
status_token_file = /path/to/status-token
```

The token-file key is optional when that server requires no HTTP token. Use an absolute local path where practical;
paths are otherwise used exactly as written, without `~` expansion. The file is read on each refresh, up to 4096 bytes,
with surrounding whitespace removed. Empty, oversized, non-regular or control-containing token files are refused.
The token is sent only as an `Authorization: Bearer` header, and never displayed in an error. Use HTTPS for remote
token-bearing connections. The existing local HTTP example remains useful for loopback or a host-managed private path.

Give the status page's base URL: `/api/stats` is appended, including after a reverse proxy's prefix, so
`https://relay.example.org/status` becomes `https://relay.example.org/status/api/stats`. An explicit URL ending in
`/api/stats` is also accepted. URLs with user credentials, a query string or fragment are refused. Requests go directly
to the configured host; redirects and environment HTTP proxies are not used.

An HTTP refresh has a two-second request deadline inside the existing five-second refresh budget. A successful
response opens no game-protocol session. If it fails, the app attempts the existing game-protocol view within the
remaining budget and shows a generic HTTP-unavailable note. A failed source does not terminate the app. Press `r`
after changing the URL or token file; clearing the URL returns to protocol-only behavior.

| Screen | HTTP data |
|---|---|
| Overview | Uptime, current client count and total connections; the server version remains unshared. |
| Rooms | Public-room IDs, titles, current/max members, names and age, including full rooms; invite-only rooms are counted without names. |
| Leaderboards | Existing boards, entry counts, leader and best score. These are board summaries; HTTP does not provide full rankings. |

Leave title/board filters empty to show all shared entries, or use the existing `e`/`b` controls to filter. Up to 100
matching rows are displayed with a count. Room IDs, connection totals and scores retain their full integer precision.
Remote text is stripped of terminal controls and bounded for display. A response is limited to 4 MiB, 32 KiB of
headers and 5000 rooms/boards; over-limit or malformed data uses the fallback. Additional JSON fields are ignored for
compatibility, including the UDP drop counters. No account verification or administrative writes are added.

## Starting from the desktop

The same Go terminal app builds for Windows and Linux. Windows uses the normal console-subsystem executable: it can
open a console when double-clicked, following the Windows console configuration. A physical Windows terminal has not
been tested locally; CI tests and cross-builds provide separate evidence.

On Linux, a start with no terminal and with `DISPLAY` or `WAYLAND_DISPLAY` set opens a supported terminal. It tries
`TERMINAL` first, then `x-terminal-emulator`, `kitty` and `xterm`. `TERMINAL` is one executable name or path, including a
path containing spaces; command strings, shell expansion and extra options are not parsed. A custom terminal must
support the xterm-style `-e PROGRAM ARG...` convention. Kitty is invoked with its own command syntax and no startup session.

Arguments and the environment are passed directly to the terminal. An internal recursion guard stops a second relaunch
if that terminal fails to provide a TTY. The app waits for the selected terminal and does not launch another after a
runtime error. Without a display or supported terminal, or on an error, it prints one line and exits 2. Starting
`check`, `version`, `--help` or `--no-tui` never opens a terminal. No server or browser is started by this behavior.

The repository provides a Linux desktop entry at `share/applications/re-psn.desktop` with `Terminal=true`, plus
`share/icons/hicolor/scalable/apps/re-psn.svg`, copied from the site's favicon artwork. These are installation inputs;
they do not install the executable, register a desktop entry automatically or add release assets. Install the app on
the graphical session's PATH before installing the entry, and install the SVG under the matching icon name `re-psn`.

Tests exercise selection, literal arguments, failure/recursion paths and a synthetic terminal/child process. These are
not a physical desktop, Steam Deck game-mode or emulator compatibility run. macOS still requires an existing terminal.

## The check command

```sh
re-psn-app check [-timeout 5s] HOST:PORT
re-psn-app version
```

`check` connects, says HELLO under a throwaway name (`check-` and 10 random hex digits) for a made-up title, `RE-PSN-CHECK`, so it
never sees a game's rooms, and times one more request. It prints what the server answered and `OK`, or `FAIL` and what failed, and
exits 0 or 1:

```text
connect   127.0.0.1:47391 in 0.3 ms
hello     accepted as check-b9656030ba in 0.7 ms; the server sees this machine as 127.0.0.1
features  relay on, invite-only rooms on (flags 0x03)
version   not reported: the protocol has no version message
rtt       0.2 ms (one SEARCH_ROOMS request)
OK
```

## Settings file

`re-psn.ini` lives in `$XDG_CONFIG_HOME/re-psn/` when that variable holds an absolute path, else in `~/.config/re-psn/`; on Windows
in `%AppData%\re-psn\`. It is written atomically (a temporary file, then a rename) with mode 0600.

```ini
# Comments are whole lines starting with # or ;
[app]
mode = play
theme = dark

[player]
name = alice_01

[server]
server = play.example.org:36100
status_url = https://play.example.org/status
status_token_file = /etc/re-psn/status-token
```

| Section | Key | Value |
|---|---|---|
| `[app]` | `mode` | `play`, `host` or `both` |
| `[app]` | `theme` | `dark` or `light`; unset means dark |
| `[player]` | `name` | the online ID: 3 to 16 letters, digits, `-` or `_` |
| `[server]` | `server` | `host:port`, port 1 to 65535 |
| `[server]` | `status_url` | optional HTTP(S) status base or `/api/stats` URL, without credentials, query or fragment |
| `[server]` | `status_token_file` | optional local Bearer-token file path, used exactly as written |

The rules:

- `key = value` lines go under a `[section]` line; spaces around the key and the value are dropped.
- A `#` or `;` after a value is part of the value, not a comment.
- An empty value means the key is unset. A known key set twice is an error.
- There are no includes, quotes, escapes or variables (`~` and `$HOME` are not expanded).
- Other keys, other sections and comment lines are kept and written back as they were.
- An invalid value is an error that names the section and key, such as `re-psn.ini: [app] mode: "watch": want play, host or both`.

## Dependencies

The server module has none. The app module has these, pinned in `go.mod` and `go.sum` (`go mod verify` checks them, CI builds
with `-mod=readonly`, and `govulncheck` runs on the module):

| Module | Version | Why | Licence |
|---|---|---|---|
| [charm.land/bubbletea/v2](https://github.com/charmbracelet/bubbletea) | v2.1.1 | the terminal UI's event loop and renderer | MIT |
| [charm.land/lipgloss/v2](https://github.com/charmbracelet/lipgloss) | v2.0.6 | colours, frames and layout | MIT |
| [github.com/charmbracelet/x/term](https://github.com/charmbracelet/x) | v0.2.2 | the "is this a terminal" check (already needed by the two above) | MIT |

The program also links the modules those three need: `github.com/charmbracelet/colorprofile`, `ultraviolet`, `x/ansi`, `x/termios`
and `x/windows` (MIT), `github.com/clipperhouse/displaywidth` and `uax29/v2` (MIT), `github.com/lucasb-eyer/go-colorful` (MIT),
`github.com/mattn/go-runewidth` (MIT), `github.com/muesli/cancelreader` (MIT), `github.com/rivo/uniseg` (MIT),
`github.com/xo/terminfo` (MIT), and `golang.org/x/sync` and `golang.org/x/sys` (BSD-3-Clause). The module graph (`go list -m all`)
also has modules that only their tests or their own dependencies' tests use (`aymanbagabas/go-udiff`, `bits-and-blooms/bitset`,
`charmbracelet/x/exp/golden`, `clipperhouse/stringish`, `golang.org/x/exp`); they are not built into the program. None needs cgo. Bubbles is not used.

## What the client does not do yet

The client implements HELLO, HEARTBEAT, CREATE_ROOM, SEARCH_ROOMS, JOIN_ROOM, LEAVE_ROOM, RECORD_SCORE, GET_RANKING and
GET_RANKING_BY_ID, and decodes the MEMBER_JOINED, MEMBER_LEFT and ROUTE pushes; other pushes arrive undecoded. Room data and
messages, kicks, invite-only rooms, relay streams, UDP probes and the relay's datagrams are not in it yet. The protocol has no
message for the server's version, uptime or connection counts. Configured HTTP supplies uptime and counts; neither
source reports the server version. Host mode, room joining from the UI and launch profiles remain unfinished.
