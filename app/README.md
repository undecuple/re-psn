# re-psn desktop app

The optional Windows/Linux client opens a private local browser interface for Play, Host or Both, local trophy
management and authenticated friends. Games connect directly to the standalone server without this app, a local
profile or a relay account. Local trophies need no relay registration.

Run `re-psn-app` or `re-psn-app ui`. The interface uses the dark palette, local monospace fonts and double framing
of [undecuple/me](https://undecuple.github.io/me/), with accessible controls and Light/System appearance options.
The advanced terminal interface remains available as `re-psn-app tui`.

This page describes the current source. Previously published app archives can predate these screens and the paired
host component. See the [client guide](../docs/client-ui.md) for normal operation and platform qualifications.

## Contents

- [What is here](#what-is-here)
- [Play, Host and Both](#play-host-and-both)
- [Profiles, trophies and friends](#profiles-trophies-and-friends)
- [Build and test](#build-and-test)
- [The terminal UI](#the-terminal-ui)
- [Optional native HTTP status](#optional-native-http-status)
- [Starting from the desktop](#starting-from-the-desktop)
- [The check command](#the-check-command)
- [Settings file](#settings-file)
- [Dependencies](#dependencies)
- [What the client does not do yet](#what-the-client-does-not-do-yet)

## What is here

The app is its own Go 1.26 module, `github.com/undecuple/re-psn/app`. The game server's module stays standard-library only.

| Path | Purpose |
|---|---|
| `internal/webui/` | Embedded HTML/CSS/JavaScript and authenticated local actions; no frontend build or dependency |
| `internal/desktop/` | Browser dispatch through a private temporary HTML launch file; advanced Linux terminal launch |
| `internal/host/` | Explicit lifecycle control of this app's own server child |
| `internal/appinstance/` | Per-settings ownership and authenticated reopening of a running UI |
| `internal/client/` | The game protocol client and real-server conformance tests |
| `internal/config/` | Per-user INI settings that preserve unknown keys/comments |
| `internal/tui/` | Bubble Tea terminal views and shared profile session |
| `framework/` | Exported profile/trophy store, scoped local game broker, encrypted backups and mTLS friend service/client |
| `integration/` | Generated-data native SDK and two-host authenticated friendship scenarios |
| `cmd/re-psn-app/` | Browser app, terminal interface and CLI commands |

## Play, Host and Both

| Workspace | Working actions |
|---|---|
| **Play / connect** | Save a remote game target, explicitly check it, view public snapshots and use optional Profile/Trophies/Friends. A check is not a live game session or game configuration adapter. |
| **Host** | Configure a local literal IP/port and explicitly start/stop the owned server component. No player name or profile is required. |
| **Both** | Keep the remote Play target and local host independent. An explicit button can copy the local host address into the Play target. |

Choosing a role or saving settings starts no server, broker or registration. Closing a browser tab leaves the controller
and its owned host running; launch the app again to reopen it. Stop owned host affects only that child. Stop app or
process termination revokes its game grants and stops its owned host. Independently started servers remain untouched.
Switching from active hosting to Play requires confirmation to stop the owned host.

App archives built from this source include `re-psn-host` (`.exe` on Windows) beside the app. Keep them together.
App installation preserves a separately installed `re-psn` server. New app installers refuse incomplete/older app
archives without the host member. Stop the app and host before updating: two executable replacements are not
crash-atomic, although reported replacement failures trigger best-effort rollback. See [client updates](../docs/client-ui.md#install-and-update).

## Profiles, trophies and friends

Use Profile & backup to explicitly create a local identity or restore into an empty profile. In Trophies, choose a
developer catalog file, review/approve it and enable game reporting for its title. Give the resulting private descriptor
path to the recomp's SDK adapter; never share the descriptor contents. Revoke reporting explicitly or stop the app.
Reports and first local receipt times persist; they do not verify achievements and are not uploaded.

In Settings, select a friend companion's HTTPS origin and optional trusted CA file. Saving these settings contacts
nothing. Friends offers explicit registration, refresh and request/accept/decline/cancel/remove/block/unblock.
Snapshots are labeled cached with a timestamp; online presence is not provided. Each host independently owns
registration and friendship consent. Reusing a public identity at multiple hosts makes those registrations linkable.

The browser also downloads encrypted backups and restores an encrypted backup only when no profile exists at the
selected location. CLI commands remain available for automation and a different private profile directory:

```sh
re-psn-app profile create --dir /private/player-profile --name 'Player'
re-psn-app trophies install --dir /private/player-profile --input ../client/examples/app_trophies.json
re-psn-app ui --profile-dir /private/player-profile
```

An open writer is exclusive: use the running app's actions, or stop that session before a separate mutating CLI
command. Read-only snapshots remain available. The [framework guide](../docs/app-framework.md) preserves the full
C/C++ SDK, broker protocol, TLS companion/allowlist and recovery instructions.

## Build and test

Go 1.26 or newer for the app; the server retains its own minimum in the root `go.mod`. From the repository root:

```sh
go build -o re-psn-host ./server
(cd app && go build -mod=readonly -o ../re-psn-app ./cmd/re-psn-app)
./re-psn-app
```

For a Windows browser build, use `re-psn-host.exe` beside `re-psn-app.exe`; the release builder applies
`-ldflags "-H=windowsgui"` to the app so normal launch does not open a console. A console build is useful for
interactive terminal development. `ui --server-binary PATH` can explicitly select a trusted compatible component;
the browser cannot choose an executable, shell command or arbitrary child arguments.

```sh
cd app
go vet ./...
go test -race ./...
```

The conformance tests build and drive the real standalone server. Required Host lifecycle tests fail if their owned
server build fails. The older protocol conformance tests retain their documented optional-tool skip behavior.
No browser automation or actual game is implied by the package test command.

## The terminal UI

```sh
re-psn-app tui
re-psn-app --profile-dir /private/player-profile  # advanced profile terminal screens
```

The TUI needs a usable terminal on stdin/stdout; Linux can open a supported terminal when needed. Packaged Windows
uses a GUI subsystem for normal browser launch; interactive terminal use may need a console build and is not native
Windows desktop evidence.

The chooser distinguishes Play, Host and Both. Host skips the player name/remote target and asks for a local bind;
Both stores that bind separately. Menus include Overview, Rooms, Leaderboards, Settings, Help, Profile, Trophies,
Friends and Host, with role-appropriate entries. Below 70 columns, menu and content are shown separately.

Use arrows or `j`/`k`, Enter and Esc; `r` refreshes, `s` saves Settings, `t` selects dark/light, `q` or Ctrl+C quits.
Host uses `s`, then `y` to confirm start, and `x` to stop. Quitting this terminal controller stops its owned host.
Trophies uses `g` to grant and `v` to revoke; Friends uses `e` to register, `f` to request, `a` to accept,
`d` to decline/cancel, `u` to remove and `b`/`x` to block/unblock. Friends and catalog/backup automation are also in the CLI.

The Rooms/Leaderboards protocol view uses a throwaway name and closes after each fetch. It does not join a game.
`f` in Settings, or Ctrl+F while entering a server, performs an explicit bounded LAN search; it does not cross subnets.
No server is shipped as a preset. The terminal respects `NO_COLOR`; the browser supports Dark, Light and System.

## Optional native HTTP status

The app backend and terminal interface can fetch a server's existing `/api/stats` directly. The listener must already
be enabled; selecting its URL starts no remote service. These reader credentials grant no hosting authority.

```ini
[server]
server = 127.0.0.1:36100
status_url = http://127.0.0.1:36101
status_token_file = /path/to/status-token
```

A token file is optional when the server needs none. It is a bounded regular file, up to 4096 bytes, read on refresh;
empty, oversized or control-containing values are refused. The backend sends the token only in an Authorization
header. Use HTTPS for remote token-bearing requests. URLs cannot contain credentials/query/fragment; redirects
and environment HTTP proxies are disabled. A base path is supported: `/status` becomes `/status/api/stats`.

HTTP has a two-second deadline within the five-second refresh budget. On failure the backend can use the game
protocol within the remaining budget and labels the fallback. A successful HTTP refresh opens no game connection.
Public room/member/age and board leader summaries are read-only; invite-only rooms remain counted without names.

### Server tab

The advanced terminal Server tab shows version, uptime, relay state, limits, refusals/drop counters and relay payload
rate from successive snapshots. Missing fields are labeled unavailable rather than zero; restart detection resets
rate comparisons. Status responses are bounded to 4 MiB, 32 KiB headers and 5000 rooms/boards; text is displayed safely
and large integers preserve precision. Browser Host control remains a separate local capability, not a status page action.

## Starting from the desktop

Normal launch opens an app-owned interface on literal 127.0.0.1 at an ephemeral port. The OS dispatcher receives a
private temporary HTML file, not an authentication secret in process arguments. It redirects to a one-use bootstrap
link valid for one minute; frontend code removes the fragment and exchanges it for a separate in-memory/sessionStorage
API capability. Launch-document cleanup is scheduled after 90 seconds. An interrupted/short-lived launcher can leave
an expired private document; it cannot reuse an already spent/expired bootstrap. Unix creation modes do not prove a
Windows private ACL: use a private per-user location.

Starting the app again with the same settings reopens the existing controller through an authenticated local
exchange; it does not duplicate or stop the owned host. Explicit conflicting profile/server selections are refused.
The browser interface loads no remote fonts/scripts/assets. Its appearance follows [undecuple/me](https://undecuple.github.io/me/).

For trusted local automation without opening the browser:

```sh
re-psn-app ui --no-open --url-file /private/new-launch-link.txt
```

The output file must be new and contains a short-lived private launch link. Protect it; ordinary diagnostics print
only the token-free loopback base. [Client UI](../docs/client-ui.md) covers reopen, shutdown and troubleshooting.

## The check command

```sh
re-psn-app check [-timeout 5s] HOST:PORT
re-psn-app version
```

`check` uses a throwaway name/title, sends HELLO and SEARCH_ROOMS, prints reachability/round-trip results and closes.
It returns 0 for success, 1 for failure and 2 for malformed command arguments. It does not configure or connect your game.
Canceling a completed/partially sent request does not undo a server-side action; interrupted protocol writes close
the connection because a partial frame cannot safely be reused.

## Settings file

Settings live in `$XDG_CONFIG_HOME/re-psn/re-psn.ini` when that variable is absolute, else
`~/.config/re-psn/re-psn.ini`; Windows uses `%APPDATA%\re-psn\re-psn.ini`. The profile defaults to its `profile/`
subdirectory. `ui --config FILE --profile-dir DIR` selects separate locations; directories containing private state
must be private. INI writes preserve unknown keys/comments and use temporary replacement.

| Section | Key | Meaning |
|---|---|---|
| `[app]` | `mode` | `play`, `host` or `both`; selecting it starts nothing |
| `[app]` | `theme` | `dark`, `light` or `system`; browser defaults to dark |
| `[player]` | `name` | Optional player display name; Host needs none |
| `[server]` | `server` | Remote game target, `HOST:PORT` |
| `[server]` | `status_url` | Explicit HTTP(S) read-only status base |
| `[server]` | `status_token_file` | Optional local reader-token path; never shown in the frontend |
| `[host]` | `address` | Independent literal game IP:port; defaults to 127.0.0.1:36100 |
| `[host]` | `relay` | `true` or `false`; applied on explicit host start |
| `[social]` | `origin` | Explicit HTTPS friend companion origin |
| `[social]` | `ca_file` | Optional locally trusted CA path; otherwise system trust |

There are no includes, execution, interpolation or `~` expansion. Unknown lines are retained; duplicate known keys
and invalid known values are refused. Credentials are not stored in the shared game networking INI.

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
`github.com/xo/terminfo` (MIT), and `golang.org/x/sync` and `golang.org/x/sys` (BSD-3-Clause). The framework also uses the
already pinned `golang.org/x/sys` directly for OS writer locks and Windows atomic replacement. No dependency version
is added or changed by the framework. The module graph (`go list -m all`)
also has modules that only their tests or their own dependencies' tests use (`aymanbagabas/go-udiff`, `bits-and-blooms/bitset`,
`charmbracelet/x/exp/golden`, `clipperhouse/stringish`, `golang.org/x/exp`); they are not built into the program. None needs cgo. Bubbles is not used.

## What the client does not do yet

The app does not launch/join a recomp game, implement its trophy/HLE semantics, verify earned trophies, award platinum,
upload trophies or provide live friend presence/invitations. It does not authenticate legacy gameplay names or create
GitHub/Discord verification badges. Extra theme presets and desktop custom-color editing remain separate features.

Windows/Linux binaries are built. Linux synthetic browser/host/SDK checks and Windows PE controller/profile/SDK
checks under Wine provide separate evidence; Wine is not native Windows. The Windows server's UDP setup is unsupported
by the tested Wine environment (`WSAIoctl`, error 10045), so native Windows hosting is not established by those runs.
No physical Windows desktop, native Windows game runtime, real title or hosted deployment is certified.
