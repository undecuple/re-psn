# Changelog

All notable changes to this project are documented here. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and the project uses
[Semantic Versioning](https://semver.org/).

## [Unreleased]

### Added
- **Docs and tooling:** website: in the terminal view the name in the header of an answer now ends in `.md` (`about.md`, `features/rooms-basic.md`, `settings/addr.md`), and the newest answer fills the pane, with the line-number gutter's rule running to the bottom, like a file open in a viewer. Styles and file names only; no new command and no change to what an answer says.
- **Docs and tooling:** website: a discussion page (`site/discuss.html`, "Discuss" in the menu): one GitHub Discussions thread (repository `undecuple/me`, category Announcements, term `re-psn`) shown with giscus. Nothing is requested from giscus.app or GitHub until the reader presses "Load the discussion"; `site/discuss.js` then adds the giscus script. This is the only page whose Content-Security-Policy allows another site (`https://giscus.app` for scripts, styles and frames); the other pages keep the strict policy, and a test checks both. The settings of the thread are in `tools/site.py`; no setting of the repository was changed by this.
- **Docs and tooling:** website: the terminal view has an `action` command and an "In action" menu entry. `action` lists the four simulated scenarios; `action direct`, `action punch`, `action relayed` or `action blocked` prints that scenario's numbered steps with the message names, and every answer links to the animated page. The text comes from the same data as `site/action.html`, so nothing is added to the page's data.
- **Docs and tooling:** website: a "see it in action" page (`site/action.html`, linked from the page's menu as "In action"): four simulated scenarios (direct, hole punch, relayed, relay off), each a diagram and numbered steps that name the real messages (`HELLO`, `PROBE`, `PUNCH`, `ROUTE`, `STREAM_CONNECT` and so on). The script is `tools/site_action.py`: documentation addresses only, no live traffic, no script on the page, and a test checks every message name against `docs/api.md`. With script (`site/action.js`, same origin, no library) one scenario shows at a time and a packet moves along the diagram: Play/Pause, Back, Step, Restart, a speed choice, a clickable step list and the keys Space, Right, Left and R; with a reduced-motion setting nothing moves by itself and Step and Back show each step at once.
- **Client:** `psnr_set_connect_policy(fn, user)` (`client/psnr.h`): the host registers one callback that the C client asks before it opens a TCP connection, so a runtime that restricts where a game may connect can refuse it. It is asked for the server connection (once per resolved address, with the host as typed) and for each relay leg; it returns nonzero to allow and 0 to refuse. If every address is refused, `psnr_connect` returns NULL and `psnr_connect_error()` is the new `PSNR_E_POLICY` (1000, set by the client, not sent by a server); a refused relay leg returns -1. No callback, the default, changes nothing. `psnr_discover` and the title's own P2P socket are not covered.
- **Server:** a capability report (#77, item 3): `GET_CAPS` (0x04, no body, after HELLO) is answered with `CAPS` (0x94): `bits u32` and the server version as a blob. The bits say which features the server offers this player: rooms, scores, room codes, peer keys, the UDP probe, the relay and LAN discovery; the probe and relay bits are per title, so a title in `-no-peer-titles` sees them clear. A server before this answers `GET_CAPS` with error 1. The C client has `PSNR_GET_CAPS`, `PSNR_CAPS`, the `PSNR_CAP_*` bits and `psnr_caps_parse`. See `docs/api.md`, "Capabilities".
- **Deploy:** `docker/docker-compose.yml` and `docker/docker-compose.host.yml` now run the container as user 10001 with all capabilities dropped, `no-new-privileges` and a read-only root filesystem with a 16 MB tmpfs on `/tmp`, the lines `docker-compose.test.yml` already had. A log file (`RE_PSN_LOG_FILE`) inside the container needs a volume, and a listen port below 1024 cannot be bound. Memory, CPU and process limits stay in the test file only.
- **Deploy:** `deploy/re-psn.service` sets `LimitNOFILE=16384`, so a server at the default `-max-conns` (4096, a relayed stream counting two sockets) stays under the file-descriptor limit. `scripts/check-deploy.sh` checks the line. An install that copied the unit earlier keeps its old limit until the file is copied again.
- **Network:** `-relay-idle` (`RE_PSN_RELAY_IDLE`, default 600 seconds; 0 for never) closes a relay TCP stream that has carried no bytes in either direction for that long, and a write to a stream leg that the peer does not read is dropped after 30 seconds. Before, a relay stream cleared its deadlines and stayed open as long as both legs did, so idle or stalled streams held connections (now counted by `-max-conns`) indefinitely. A stream that keeps moving bytes is not limited in time or size.
- **Docs and tooling:** `docs/integrating.md` has a "Mapping the NP calls" section: suggested NP call families to re-psn message tables, a list of what the protocol does not cover (world lists included), and a recommended NP error category for each protocol error code. It is suggested, not tested in a title, and cites sections instead of line numbers.
- **Server:** `-max-conns` (`RE_PSN_MAX_CONNS`, default 4096; 0 for no limit) caps the open TCP connections in total, players and relay legs together. Until now only `-max-conns-per-addr` (32) bounded them, so many addresses could open connections until the process ran out of file descriptors. A connection over the cap is closed without an answer, counted in `/api/stats` (`limits.max_conns`, `tcp_refusals.conns`) and in the admin state, and logged at warn level at most once a second. A server that really holds more than 4096 players at once needs a higher value (and a file-descriptor limit above it).
- **Server:** `-no-peer-titles` (`RE_PSN_NO_PEER_TITLES`, off by default): a comma-separated list of communication IDs (1 to 12 printable ASCII bytes each; anything else stops startup) of titles that use no peer traffic. For a player of a listed title the server answers no UDP probe and records no UDP endpoint, drops relay datagrams from or to the player (counted under `relay_disabled`), answers a relay stream with the "no relay" error (9), pushes no relay route even with `-relay-force`, and clears the relay flag (bit 0) in HELLO_ACK. Rooms, scores and every other title on the same server are unaffected. One list turns off the probe and the relay together; LAN discovery is not covered. See `docs/running.md` and `docs/side-service.md`.
- **Server:** Experimental, read-only admin API, off by default: `-admin ADDR` (`RE_PSN_ADMIN`) opens a second listener with one route, `GET /admin/v1/state`, for a read token kept in a file (`-admin-read-token-file`; at least 32 characters, mode 0600, read again on SIGHUP). An address that is not loopback needs `-admin-tls-cert` and `-admin-tls-key` (TLS 1.2 or newer). The state is aggregate only (version, uptime, relay, limits, refusal counts, the ban count, which is 0 for now): no address, player or token. A wrong or missing token gets one identical 401 after a growing, capped delay and one log line that carries only the socket's remote address, at most 8 of the 32 connections go to one source, an `Origin` header, or a name in `Host` over plain HTTP, is refused. There are no actions yet, and the status page and `/api/stats` are unchanged. See docs/admin-api.md.
- **Server:** Medius compatibility switch, off by default: `-medius` (`RE_PSN_MEDIUS`), `-medius-port` (default 10071) and `-medius-addr` (default `127.0.0.1`; a loopback, private or link-local address, or `0.0.0.0`/`::` in a container). **No Medius protocol is implemented**: with the switch on, the server opens that port and closes every connection to it without reading or writing, and says so in a start-up warning. A port or address other than the defaults is refused without the switch. Commented examples in both Docker compose files, `deploy/re-psn.env.example` and `docs/running.md`.
- **Client:** constants for `PEER_AUTH`, `SET_ROOM_ACCESS`, `JOIN_BY_CODE`, `GET_ROOM_ACCESS`, `ROOM_ACCESS` and `PEER_KEY`, and
  bounds-checked decoders for ROOM_JOINED, member entries, ROOM_LIST and RANKING (`psnr_room_joined_parse` and friends).
- **Client:** `psnr_connect_timeout(...)` (`client/psnr.h`): `psnr_connect` with a time limit. The TCP connect is non-blocking and the wait for HELLO_ACK is bounded, both by one `timeout_ms` budget; a stalled server gives NULL instead of blocking the caller. DNS is still blocking and not covered. `psnr_connect` is unchanged.
- **Client:** `psnr_dead_reason(c)` and `psnr_last_os_error(c)`: why the C client's connection was lost (peer closed, I/O error, out of memory, send timeout) and the OS error at that point. The first failure is kept.
- **Client:** `PSNR_SEND_TIMEOUT_MS` (5000, overridable before including `psnr.h`): the most time one write may wait for the socket to accept data, in total.
- **Docs and tooling:** CI compiles the C client (`client/psnr.c`, `client/examples/minimal.c`) with MSVC on Windows and does not run it there; the build instructions in `docs/integrating.md` give the MSVC command.

### Fixed
- **Server:** the in-memory score store now keeps boards for at most 1000 titles, next to its existing 1000 boards per title and 10000 players per board. A RECORD_SCORE that would start a board for a 1001st title gets ERROR 6 and is not kept; titles that already have boards, and players already on a board, are unaffected. Before, the number of titles was not limited. The limit is fixed, not a flag. See `docs/api.md`, Leaderboards.
- **Server:** `-max-conns-per-addr` now counts an IPv6 address as its /64 (an IPv4-mapped address as the IPv4 address), as the UDP limit and the admin failure delay already did. Before, it counted each exact IPv6 address, so connections from different addresses of one network each had a full allowance. The address a player is known by (rooms, peers, logs) is unchanged. Players behind one IPv6 /64, such as one LAN, now share the limit, as players behind one IPv4 address always did; raise `-max-conns-per-addr` if that is too tight.
- **Server:** the `-http` status page now closes a connection that does not finish its request header within 5 seconds, its request within 10 seconds, does not read its answer within 10 seconds, or sits idle for 30 seconds. Before, the status server had no such limits.
- **Server:** `SET_ROOM_DATA` no longer lets the owner make the room's own join reply too big for one packet: a write to the external or internal blob that would is refused with error 6 and changes nothing, so a member who asks for the room again always gets an answer. Before, such a write was accepted and later joins and re-joins were refused.
- **Server:** A search no longer disconnects the player when the rooms' data is large: the reply lists only as many rooms as fit in one packet, and the player pages on from there.
- **Server:** A player whose connection stops reading no longer freezes the other players in its room: the server drops it after 10 seconds of a stuck write (`writeTimeout`), so its room mates' requests get their replies and the stuck player is removed from the room.
- **Server:** A second HELLO on one connection gets error 1 and changes nothing, and a name is held by the player's full IPv6 address: before, all IPv6 players shared one address, so any of them could take another's name.
- **Server:** A room whose members' data, with the room's blobs, would make its join reply too big for one packet no longer breaks the joiner: JOIN_ROOM and JOIN_BY_CODE answer error 6 and the joiner is not added, instead of the connection being closed after the other members were told of it.
- **Server:** a player with no IPv4 address (an IPv6 connection) is no longer treated as being on the same network as every other such player. Before, two IPv6 players both had the address 0.0.0.0, so each was handed the other's TCP-derived address instead of its probed UDP endpoint, and a relay stream between them was never chosen. Now they are never on one network, so the probed endpoint is announced and, with `-relay`, the relay can be chosen for them. This only shows when such a player has a recorded UDP probe, which needs an IPv4 UDP source and `-udp-any-source`, since a probe from an address other than the TCP one is otherwise ignored.
- **Server:** A score server no longer slows down or grows without bound under many boards: the memory store keeps at most 1000 boards per title and 10000 players per board, and a score that would pass either gets ERROR 6 (an existing player can still improve their score). Ranks are read from a sorted list instead of sorting the board on every request, and a GET_RANKING_BY_ID with more than 100 ids is refused before its ids are read.
- **Client:** The C client has `psnr_relay_unwrap_from`, which accepts a relayed datagram only from the server's UDP address. A title that
  reads relayed datagrams with `psnr_relay_unwrap` can be sent a forged sender by any room member; `psnr_relay_unwrap` is kept
  unchanged for compatibility and documented as not checking the source.
- **Docs and tooling:** a comment in `server/rooms.go` that pointed at the missing `docs/room-codes.md` now names the section of `docs/api.md` that holds the content; SECURITY.md states that players behind one address can take over each other's names, and the docs say that `-max-req-rate` does not bound the in-memory score store (which has its own fixed limits per title and board), and that peer keys give no replay protection on their own.
- **Client:** the receive path checks its allocations (a failure now ends the connection instead of dereferencing NULL), a reply
  frame too short to hold its request id is dropped instead of being delivered as a push, and `psnr_connect` refuses a
  `comm_id` over 12 or an `online_id` over 16 bytes instead of cutting it.
- **Client:** in the desktop app, `r` (refresh) on Rooms, Leaderboards and Server does nothing while a fetch is running and for one second after its answer. Every fetch opens a new game connection to the server and says HELLO under a throwaway name, so holding or repeating `r` used to open one connection per press and inflate the server's connection counts. A second refresh right after an answer needs a second press.

### Changed
- **Docs and tooling:** the NAT lab's player (`lab/natcheck.c`) unwraps relayed datagrams with `psnr_relay_unwrap_from` and the source address `recvfrom` reported, instead of `psnr_relay_unwrap`, which does not check the source and which `client/psnr.h` says titles should not use on the P2P socket. The lab's `symmetric` scenario is the one that sends pings through the relay.
- **Docs and tooling:** `docs/releasing.md` no longer says the ten-asset release and the first run of the app-archive upload and `release-check.yml` are still to come: v0.5.0 is that release, and both ran on it and passed. The Windows and arm64 app binaries are still listed as not run.
- **Docs and tooling:** `scripts/bump-version.sh` now also sets the `var version` default of the desktop app (`app/cmd/re-psn-app/main.go`), and `TestVersionAgrees` checks it against `VERSION`; before, a bump left the app's source default at the old number. Release builds set the app version with `-ldflags` and were not affected.
- **Docs and tooling:** the website, the README images, the favicon, the README badges, the desktop app's dark colours and the status page's dark colours now follow the colours of https://undecuple.github.io/me/ (`docs/art/palette.json`); the website has one dark theme (the System/Light/Dark switch and the `theme` command are gone) and a pixel-letter banner in a double frame above the title bar. The light colours of the app and the status page are unchanged. The colours credit guts.nvim through that page; guts.nvim has no licence file, so NOTICE.md names it as the source and states no licence. `docs/img/social.png` must be uploaded to the repository settings by hand to change the social preview. The banner reads "re:psn" in pixel letters and sits at the top of the page, above the terminal view; on screens at least 1400 px wide a tall copy of it stands at the left edge instead.
- **Docs and tooling:** the features list says `since` 0.5.0, not `main`, for the twelve rows that shipped in v0.5.0 (LAN discovery in the server, the C client and the app, game names, verified player counts, server state and reported latency in the status page, `-relay-force`, peer keys, `-lan-public-addr`, the app Server tab and the app archives), and `docs/releasing.md` has the step that sets this column at each release.
- **Docs and tooling:** the README settings table now lists all 35 flags (it lacked `-admin`, `-admin-read-token-file`, `-admin-tls-cert`, `-admin-tls-key`, `-discover`, `-name`, `-lan-public-addr`, `-medius`, `-medius-port` and `-medius-addr`, and said 21), and the Flags table in `docs/running.md` lists `-max-player-rooms`, `-max-conns`, `-max-conns-per-addr` and `-max-req-rate`.
- **Docs and tooling:** `docs/side-service.md` says re-psn offers none of the platform social services (sign-in ticket, friends, presence, block list, recent players, messages, lookup by online ID, account region and age) and that a title with its own server answers them locally; the planned friends, presence and invites feature is not built.
- **Docs and tooling:** README no longer says the desktop app has no release binary: from v0.5.0 a release carries the app archives (the "Release archives" section already said so).
- **Docs and tooling:** README and ROADMAP no longer call ps3recomp#200 a draft: GitHub shows it merged into ps3recomp's `master` on 2026-10-07.
- **Client:** The C client's writes give up after `PSNR_SEND_TIMEOUT_MS` in total instead of waiting without a limit. The connection is then dead with `PSNR_DEAD_SEND_TIMEOUT`, as a failed write already was.

## [0.5.0] - 2026-11-11

### Added
- The desktop app's Status screen has a read-only Server tab when a status URL is set: version, uptime, relay on or off, the limits in
  force (0 shown as none), TCP refusals and UDP drop counters, and the payload the relay has carried with a bytes-per-second rate
  between two refreshes (not network bandwidth). A counter going down is shown as a server restart. A server that omits the fields
  shows each as not reported, never as 0. It changes nothing on the server.
- The release build also makes three archives of the desktop app, `re-psn-app-vX.Y.Z-linux-amd64.tar.gz`, `-linux-arm64.tar.gz` and
  `-windows-amd64.zip`, each with the static `re-psn-app` binary (no cgo, version set from the tag), `LICENSE`, `NOTICE.md` and the
  app's README, and listed in `SHA256SUMS`. A release now has ten assets; `scripts/check-release.sh --assets`, both release workflows
  and their tests expect them (v0.4.0, with seven, is still accepted by the release check). The install scripts do not install the app unless asked (see below).
- `install.sh --app` and `install.ps1 -App` install the desktop app (`re-psn-app`) from a release instead of the server, with the same checksum check, no sudo and no profile edits. They need a release that has the app archives (after v0.4.0) and say so when the download is missing. Without the flag the scripts behave as before.
- `-lan-public-addr IP` (`RE_PSN_LAN_PUBLIC_ADDR`, off by default, IPv4): when a player connected from a private, carrier-grade NAT or
  link-local address, players on public addresses are given IP and that player's P2P port instead of the unreachable LAN address
  (the host must forward that port). Players on private addresses and players with public addresses are unaffected.
- LAN discovery on the server, opt-in: `-discover` (`RE_PSN_DISCOVER`, off by default) answers a 64-byte DISCOVER sent to the game UDP
  port with a 52-byte DISCOVER_REPLY (TCP and UDP ports, relay flag, player count, name, version), only to private, carrier-grade NAT,
  link-local or loopback senders, under the existing UDP rate limit. `-name` (`RE_PSN_NAME`, at most 24 printable ASCII bytes) sets the
  name shown. `/api/stats` `udp_drops` gains `discover_refused`. With the flag off the server behaves as before. No client helper yet.
- The C client can find servers on the local network: `psnr_discover(port, timeout_ms, out, max)` (`client/psnr.h`) broadcasts one UDP request from its own temporary socket (never a title socket) and returns each server that answers. Only servers started with discovery on reply; it does not cross subnets or client-isolated Wi-Fi, so keep a manual host:port path. On Windows it broadcasts to 255.255.255.255 only; elsewhere it also tries each IPv4 interface's directed broadcast.
- `/api/stats` reports the server's version, whether the relay is on, the payload the relay has carried, the limits in force and
  how many TCP connections were refused (too many from one address, request rate). Aggregate only; same access rules as the rest
  of `/api/stats`. Older servers omit the fields.
- HEARTBEAT may carry the player's own last round trip (`rtt_ms`, 2 bytes). The server shows it next to each member on the status
  page and in `/api/stats` as reported by the player, unverified and for display only; a HEARTBEAT without it, and older servers,
  behave as before. The C client sends it once a title opts in with `psnr_report_rtt(c, 1)` (recommended; off by default), from the
  round trips of its own calls (`psnr_rtt_ms`, `psnr_heartbeat`). The app's Go client does the same with `Conn.ReportRTT(true)`.
- `/api/stats` and the status page count verified players: each public room has `verified` (members whose UDP probe the server
  has accepted on this connection) and the top level has `verified_players`. The page shows "(N verified)" after a room's
  member count when N is above zero. The protocol is unchanged.
- `client/psnr.h` now says what the C client touches: only the sockets it makes itself (the server connection and relay stream connections, which the caller owns), never the title's; connecting and disconnecting are explicit and nothing reconnects by itself; the process-wide effects (`WSAStartup` on Windows, one global connect-error value, plain `send()` so a closed connection can raise `SIGPIPE`, IPv4 only). A comment only; no code changes.
- A test checks that no secret reaches the log: it drives a session at debug level, in both log formats, and looks for each player's UDP token, a room's invite code (right and wrong), a peer key and the status token (right and wrong). The server's behaviour is unchanged.
- Peer keys, for players who connect directly: a client may send PEER_AUTH (0x03, after HELLO, when HELLO_ACK's flag bit 2 says the server issues keys), and then each pair of members who both asked gets one new random 16-byte key from the server in a PEER_KEY push (0xA8) when they share a room. It is the groundwork for tagging packets between peers; no client uses it yet. A client that does not ask is unaffected, and the keys are not confidential on the wire (they travel over the plain TCP connection). HELLO_ACK's flags gain bit 2.
- `-relay-force` (`RE_PSN_RELAY_FORCE`, off by default, needs `-relay`): every stream goes through the relay, even between players
  who could connect directly, so the relay can be tested from one network. The server logs a warning at start and refuses to
  start with it and without `-relay`. It sets the existing ROUTE flag, so clients need no change; datagrams (UDP RELAY) are not
  forced. For testing only: it uses the host's bandwidth.
- The desktop app can find servers on the local network: Find on LAN (`f` in Settings, Ctrl+F while typing the server, also on the
  first start) broadcasts one 64-byte UDP datagram (`PSNR`, type 0x04, a random nonce) from a temporary socket, lists the replies
  (name, address:port, players, relay, version) and puts the chosen address in the server field; typing `host:port` still works.
  The Go client package has `Discover` for it. A server answers only when it was started with `-discover`, and only inside its own subnet; otherwise the list says none found.

### Added
- The status page and `/api/stats` show known games by name: rooms and leaderboards carry a `label` such as
  `[Killzone 3 (BCUS98234, BCES01007)]`, with `game` and `serials` fields, and are sorted by it. A game the table does not
  know keeps its communication ID. The table is embedded in the server (four games so far), never fetched, and the
  `title` field is unchanged. The protocol is unchanged.

### Fixed
- `deploy/re-psn.env.example` now lists `RE_PSN_DISCOVER`, `RE_PSN_NAME` and `RE_PSN_LAN_PUBLIC_ADDR`, and the old names of those and of `-relay-force`, so `scripts/check-deploy.sh` (run by the release build) passes again. A test now runs that check, so a flag added without its example line fails in CI.
- The start log line for LAN discovery ("lan discovery on") no longer shows the server name quoted twice; the name is logged as a plain
  attribute, quoted once by the log format. A test checks it in text and JSON.
- The advice for the UDP PROBE (`client/psnr.h`, `docs/api.md`) is now concrete: send one about every 15 seconds while the title is
  online, from its own P2P socket, because some home gateways drop an idle UDP mapping after 30 seconds. Comments and docs only.
- `re-psn -h` no longer prints the status token. The `-http-token` default was read from `RE_PSN_HTTP_TOKEN` (or the old
  `PSNR_HTTP_TOKEN`), so the usage text showed it, and a pasted `-h` or a CI log could leak it. The flag's own default is now
  empty, as for `-db`, and the variable is read after the command line is parsed. The token is still taken from either
  variable or the flag, the flag still wins, and nothing else about the status page changes. If you ever ran `-h` with the
  token set and kept the output, rotate the token.

## [0.4.0] - 2026-11-09

### Added
- Optional fail2ban instructions and a disabled journal jail/filter for refused HTTP status tokens, with offline
  synthetic log checks. Live host bans are unverified; the example leaves game TCP/UDP outside its scope.
- A recomp developer integration guide with pinned runtime sources, callback/lifecycle and peer-transport guidance.
  A minimal C HELLO/search example runs in the required native-client check; C API comments now clarify blocking
  writes, diagnostic state and reply-wait timeout limits without changing implementation or the wire protocol.
- Native optional HTTP status in the shared Windows/Linux terminal app: server totals, public-room members/age and
  leaderboard summaries, using a local Bearer-token file. Requests and displayed data are bounded, redirects/proxies
  are disabled and failures fall back to the game protocol. Initial refresh and changed-source sequencing are corrected.
- Linux desktop startup for the shared terminal app: open an installed terminal when no TTY is attached, with literal
  arguments and a recursion guard. A desktop entry and the site's favicon artwork are provided as installation inputs;
  no release assets are added, and the Windows build retains its normal console behavior.
- Bounded per-source UDP admission (per IPv4 address, per IPv6 /64) with `-max-udp-rate` / `RE_PSN_MAX_UDP_RATE` (default 1000 packets/second,
  same-size burst; 0 disables it), shared by probes and relay datagrams. Aggregate `udp_drops` counters in the
  opt-in `/api/stats` identify refusal reasons without per-packet logging or source labels. Relay byte/stream budgets
  remain planned; the packet format and token checks are unchanged.
- `/healthz` on the opt-in status listener, protected by the existing HTTP token. It reports game TCP/UDP readiness
  without player data. Shutdown marks the server unready and closes accepted client/relay sockets with bounded cleanup;
  restart behavior and supervisor settings are documented in `docs/restarts.md`.
- A stock Caddy 2 example for the HTTP status page, included with the deployment examples. It defaults to loopback;
  game TCP and UDP stay direct. The example has not been run or validated with Caddy.
- The opt-in server status page now matches the website and terminal app's gruvbox-material palette and monospace
  framing, with Overview/Rooms/Leaderboards tabs, System/Light/Dark, text size, spacing and contrast-checked custom colours.
  Appearance is local to each browser. Live refresh can be paused and retains the last snapshot on failure; without
  JavaScript both tables remain visible and reload as before. The status token is unchanged; the fields of `/api/stats`
  are unchanged by this page (the `udp_drops` counters are a separate addition).
  Embedded assets normalize line endings before rendering and CSP hashing. Polling preserves full 64-bit score and
  identifier values without changing their JSON types on the server.
- An offline repository check for tracked root/top-level layout, local Markdown links and anchors, and static
  workflow/Dependabot input paths. Run `python3 tools/repo_check.py`; CI runs it with the documentation checks.
- A focused loopback check: `python3 tools/loopback.py` runs the existing native C-client and Go app-client conformance
  tests against temporary loopback servers. Required skips and timeouts fail; `--race` is optional. See `docs/testing.md`.
- Numbered file-style output in the website's terminal view: a header, frame, line-number gutter, syntax-style colours
  and wrapped continuation rows with an empty gutter. Headers and numbers are excluded from text selection. See
  `docs/website.md` for controls and browser-test instructions.
- Optional install scripts: `scripts/install.sh` (POSIX sh; Linux amd64 and arm64) and `scripts/install.ps1` (Windows amd64;
  written for PowerShell 5.1 and 7, tested on 7 only) install the server for the current user from a GitHub release, the newest one (pre-releases included)
  or a pinned `--version` / `-Version`. They check the download against `SHA256SUMS` and stop before installing anything on a
  mismatch or a missing line, need no sudo or administrator rights, edit no shell profile, and change the user PATH only with
  `-AddToPath` on Windows. macOS is refused (there are no macOS builds). `--dry-run` / `-DryRun` downloads nothing. Tested
  with `scripts/test-install.sh` and `scripts/test-install.ps1` against a fake release. See `docs/installing.md`.
- `.github/FUNDING.yml`, which gives the repository a sponsor link (Ko-fi).
- Tests for room kick behavior, contributed by Tzeentchnet.

### Changed
- The website's pager trial is removed: the `pager` command, its keys, buttons and status bar, and the saved pager
  setting are gone, and long output is shown in full in the scrolling output pane. Numbered file output is unchanged.
- The README install section recommends the release archives and shows an optional quick install: the installer is
  fetched from a pinned revision of the repository, its hash is checked, and it is read before it is run.
- The release carries seven assets: the three archives, the AppImage, `SHA256SUMS`, and the two install scripts as
  `install.sh` and `install.ps1`, byte for byte the tagged `scripts/` files, with their checksums in `SHA256SUMS`. Check one
  with `sha256sum -c SHA256SUMS --ignore-missing` before reading and running it. `scripts/check-release.sh --assets` checks a
  build holds exactly the seven, and `release-check` compares the published scripts with the tag.

## [0.3.0] - 2026-11-06

### Changed
- The website starts in the plain view on a phone (narrow screens); the `terminal view` button switches the interface on, and nothing is stored.
- The website's interface on a phone: the version no longer wraps one letter per line in the title bar, and the menu is a row of tabs that scrolls sideways with the page content shown under it, instead of a full-screen list that hid the content.
- Credits: README, NOTICE.md and the website name undecuple as the maintainer of re-psn and author of the changes since the fork, and LICENSE
  keeps upstream's copyright line with a second line for the fork's changes.
- The status page and `/api/stats` are off unless `-http` / `RE_PSN_HTTP` names an address; they used to listen on
  `127.0.0.1:36101` by default. To keep the old behaviour, set `RE_PSN_HTTP=127.0.0.1:36101` (or `-http 127.0.0.1:36101`).
  `-http-token` and the warning for a page reachable beyond localhost without a token are unchanged.
- The release carries one `SHA256SUMS` instead of a `.sha256` file per download: five assets instead of nine.
  Check a download with `sha256sum -c SHA256SUMS --ignore-missing`.
- A shorter README: its long reference parts fold into dropdowns, and a short list linking to `docs/features.md` replaces the features table.
- The container image moves from Alpine 3.20 (end of life) to Alpine 3.24 and Go 1.26, both bases pinned by digest, and the server
  runs as the non-root user 10001. Tested with Podman and in the NAT lab on CI.

### Added
- `re-psn-app` starts a terminal UI when run with no arguments, built on Bubble Tea v2.1.1 and Lip Gloss v2.0.6 (MIT; the app module's
  first dependencies, pinned in `app/go.mod` and `app/go.sum`; the server module still has none). On first start it asks whether
  this machine plays, hosts or both, then for the online ID and the server, and saves `re-psn.ini`. The home screen has a menu
  (Overview, Rooms, Leaderboards, Settings, Help) beside the content, or one at a time under 70 columns. Rooms and Leaderboards
  are the tabs of a Status screen filled over the game protocol only (HELLO, SEARCH_ROOMS, GET_RANKING for a typed board id);
  the server's version, uptime and connection counts read "not available over the protocol". Settings edits mode, name, server,
  status URL and theme (`[app] theme`, new) and saves with mode 0600. Dark by default, `t` for light, in the website's colours;
  `NO_COLOR` turns colour off. Without a terminal, or with `--no-tui`, it prints one line and exits 2. See `app/README.md`.
- Website: autocomplete in the terminal interface. While you type, the command line shows the rest of the best match dimmed and a list of up to eight matches (command names, `--status` and its values, feature ids, setting names, `theme` values) with a one-line description each. Tab or Right arrow takes the match, Up and Down choose from the list, Enter takes a chosen item, Esc closes the list, and a click or tap takes an item. The input is an ARIA combobox and the list a listbox, with a polite count of the suggestions. Nothing new is requested or stored.
- Website: a visible System / Light / Dark switch in the page header and in the terminal interface (without script it is hidden and the page follows the system), kept in step with the `theme` command and remembered in the browser; and colour from the whole gruvbox-material palette for headings, status tags, command names, the prompt, flags, ids, links and the bars. Every text colour is checked at 4.5:1 or more against its background in both modes; where an accent is too faint as text on the light page it is a border or an underline and the text keeps a passing colour.
- `site/`, the project website: `tools/site.py` generates `site/index.html` and `site/data.json` from `docs/features.tsv`, `VERSION`, the README settings table
  and this file (`--check` compares the committed files and the colour tokens of `site/style.css` with the sources), and `site/style.css` is hand-written in the
  gruvbox-material palette, dark by default and light with the system. No script and no third-party loads (a CSP says so). `.github/workflows/pages.yml`
  deploys `site/` to GitHub Pages on a push to main that touches it, or by hand; the deploy job runs only when the repository variable `PAGES_ENABLED` is `true`.
- `site/terminal.js`, the terminal-style interface of the project website: a menu, a content pane and a command line (`features [--status S] [id]`, `roadmap`, `settings [name]`,
  `install`, `connect`, `version`, `changelog`, `about`, `credits`, `licence`, `docs`, `links`, `theme`, `clear`, `history`, `--help`), Tab completion, history, a dark and a light theme,
  and, on a phone, a row of scrolling tabs over the content with tappable command chips. The page still works without script and a `plain view` link switches the interface off. The only request is for `data.json`
  on the same site (the content security policy allows scripts and requests from this site only). The section bar now stays in view while scrolling, there is a Top link, a real
  page heading and a favicon. Tested with `node --test tools/terminal.test.js`.
- `app/`, the start of the desktop app, as a Go module of its own: a client
  for the TCP protocol (HELLO, heartbeat, rooms, leaderboards, and the MEMBER_JOINED, MEMBER_LEFT and ROUTE pushes) with a
  conformance test against the real server, a reader and writer for the per-user settings file `re-psn.ini`, and
  `re-psn-app check HOST:PORT` and `re-psn-app version`, next to the terminal UI described above. See `app/README.md`.
- `docker/docker-compose.test.yml`: an opt-in hardened host-network compose file for a shared test host (non-root, no capabilities,
  read-only filesystem, resource limits). It refuses to start without an explicit `RE_PSN_BIND` address.
- `.github/workflows/container.yml`: builds the image for linux/amd64 and linux/arm64 and, when a release is published, pushes it to
  `ghcr.io/undecuple/re-psn` (tags `X.Y.Z`, plus `X.Y` and `latest` for a release that is not a pre-release). A manual run builds and
  pushes nothing unless asked. The first image, `0.2.0`, was pushed by a manual run (amd64 and arm64, unsigned); the arm64 image was not run.
- `-replace` (`RE_PSN_REPLACE`): when the game port is held by an earlier re-psn of the same user, the server sends it SIGTERM,
  waits up to 10 s for the port and starts in its place (Linux only). It finds the earlier instance through a pidfile in
  `$XDG_RUNTIME_DIR/re-psn/` and signals it only if the pid, owner, start time and program name all check out. The server now
  stops cleanly on SIGINT or SIGTERM, and without `-replace` a busy port names the earlier re-psn holding it. See `docs/running.md`.

## [0.2.0] - 2026-11-06

### Added
- A Linux x86_64 AppImage of the server in the release, `re-psn-vX.Y.Z-x86_64.AppImage`, beside the archives: the same binary and
  examples as one runnable file, built by `scripts/build-appimage.sh` with appimagetool 1.9.1 and the type-2 runtime 20251108
  (both downloaded by the release workflow and checked against pinned SHA-256 hashes), smoke-tested in the workflow, listed in
  `SHA256SUMS`. Unsigned; needs FUSE or `--appimage-extract-and-run`. See `docs/releasing.md`.
- `docs/img/social.png` (1280x640), drawn by the art generator for the repository's social preview. It is uploaded by hand in the
  repository settings and no page uses it.
- `tools/art/make_art.py` draws the README banner and terminal images (`docs/img/`) in the gruvbox-material palette
  (`docs/art/palette.json`) with JetBrains Mono pinned by SHA-256. Terminal images come only from transcripts of real runs
  (`docs/art/transcripts/`, recorded by `tools/art/capture.sh`). CI renders them again and fails on any byte that differs.
- `AGENTS.md`: the commands, layout and rules for AI coding agents working in the repository.
- A `VERSION` file with a test that the code, this file and the README agree on it, `scripts/bump-version.sh` (one bump, every place,
  `--dry-run` shows the diff), `scripts/release-notes.sh` (notes from this file) and
  `scripts/check-release.sh`, run by `scripts/release-build.sh`. See `docs/releasing.md`.
- Scores go through a storage interface, and `-db` / `-db-file` (`RE_PSN_DB`, `RE_PSN_DB_FILE`) pick the backend. Only `off` (the default)
  and `memory` exist, so scores are still kept in memory; database settings stop the server at start, and the setting is never printed.
- `docs/connecting.md`: the ways players reach a server (LAN, port forwarding, a public server with `-relay`, Tailscale, ZeroTier, a
  reverse proxy), with diagrams and links to the overlays' documentation. re-psn has not been run over Tailscale or ZeroTier.
- A features list: `docs/features.tsv` holds one row per feature with its status and evidence, and `tools/features.py` generates
  `docs/features.md` from it. CI runs `python3 tools/features.py --check`, which fails on a stale page, a malformed row, or
  evidence that does not exist.
- Release archives include an `examples/` folder: `re-psn.env.example` with every setting, a systemd unit, and Traefik, nginx and HAProxy
  configs that pass the real client address. `scripts/check-deploy.sh` checks the env file against the server's flags and runs in
  `scripts/release-build.sh`. Only the Traefik config has been run.
- Reverse proxy support: `-trusted-proxies` and `-proxy-protocol` (PROXY protocol v1/v2 on the game TCP port), and forwarded
  headers for the status page log, both off by default. The real client address then counts for `-max-conns-per-addr`, the
  name take-over rule, `public_ip` and the log. UDP is not proxied. See `docs/running.md`.
- Every flag can be set as a `PSNR_<FLAG>` environment variable (a flag wins), and `-tcp-addr` / `-udp-addr` give the two
  game sockets their own addresses or ports. A bad value stops the server at start. See `docs/running.md`.
- Invite-only rooms (protocol 0.4): `SET_ROOM_ACCESS`, `JOIN_BY_CODE` and `GET_ROOM_ACCESS` give a room an 8-character code and take it
  out of search and the status page (which counts "unlisted rooms"). Wrong codes are rate-limited. HELLO_ACK flags bit 1 says the server
  has them. See `docs/api.md`.
- Logging with levels: `-log-level` (debug, info, warn, error), `-log-format` (text or json) and `-log-file`. Every refusal
  (connection or request limit, name taken, status page token) is now logged at warn level; `-v` is the same as
  `-log-level debug`. Tokens and query strings are never logged. See `docs/running.md`.
- One player per name on a server, compared case-insensitively. A HELLO with a
  name someone else holds gets ERROR 8 (name taken); from the same IP address
  it is the same player reconnecting, and the old connection is closed. The C
  client reports the refusal through `psnr_connect_error()`.
- Firewall: `Setup.cmd` offers to add a Windows Firewall rule for the server
  (TCP 36100, one UAC prompt); `setup.sh` prints the ufw/firewalld command when
  a firewall is on. `docs/running.md` covers the server's and players' rules.

- NAT traversal. The server listens on UDP 36100 too. A player's P2P socket
  sends it a PROBE (with a token from HELLO_ACK), and the server records the
  public endpoint the probe came from. Member entries now give each viewer the
  address that works from where they are: that public endpoint from another
  network, the member's LAN address from the same one. Clients built against
  0.2 still work; without a probe nothing changes. The C client builds and
  reads the packets (`psnr_probe_packet`, `psnr_punch_packet`,
  `psnr_probe_reply`, `psnr_is_control`); the caller sends them from its own
  P2P socket. Protocol in `docs/api.md`, "UDP".
- The relay (`-relay`, off by default), for players who can't reach each other
  directly. Streams into a player behind a router go through the server
  (STREAM_CONNECT / STREAM_OFFER / STREAM_ACCEPT / STREAM_READY), and ROUTE
  pushes say which members need it. Datagrams go through the server's UDP
  port (RELAY) when punching doesn't get through, as with a symmetric NAT.
  HELLO_ACK gains a flags byte saying whether the relay is on. The C client
  wraps and unwraps relayed datagrams and opens and accepts relayed streams.
  In the lab, the symmetric-NAT scenario now passes, and every scenario also
  runs a host-to-joiner stream.
- `lab/`: a NAT lab in Docker. A psnr server on a pretend internet, headless
  players behind NATing routers, and three scenarios: two homes (passes), a
  symmetric NAT (fails until psnr has a relay), and two players in one home
  (passes, over LAN addresses). CI runs it.

### Fixed
- `docs/running.md` said the P2P port is UDP only; titles use TCP on it too.

### Changed
- The release workflow builds its notes with `scripts/release-notes.sh` from the CHANGELOG.md entries for the version in `VERSION`
  (ending "Next release: when ready"), and on a tag fails before building unless the tag is `v` + `VERSION` and
  `scripts/check-release.sh --tag` passes. A manual run still builds everything and publishes nothing.
- `README.md` is rewritten: a fork notice crediting sp00nz, an overview with a session diagram, an example from real local runs,
  the available features, install, a settings table checked against the flags, and the `PSNR_*` migration note.
- `ROADMAP.md` is rewritten around the feature ids (Done, Now, Next, Later, Not planned), keeping upstream's scope section; the
  features list gains planned and later rows for storage, high availability, metrics, the apps' themes and the docs, and
  `tools/features.py --check` also fails when a roadmap item sits under a section that does not match its status.
- `deploy/README.txt` is now `deploy/README.md` (`examples/README.md` in the release archives); the docs share one layout, and the deploy and Docker READMEs gain Mermaid diagrams.
- The docs use the fork's name, re-psn, and credit sp00nz as the author of the original psnr. `client/psnr.c` keeps its name.
- Settings are read from `RE_PSN_<FLAG>` variables. The old `PSNR_<FLAG>` names still work as aliases: a flag wins, then
  `RE_PSN_<FLAG>`, then `PSNR_<FLAG>`. An old name logs a warning at start naming the old and new variable (never the value), and
  if both are set and differ the new one is used, with a warning. `deploy/psnr.env.example` is now `deploy/re-psn.env.example`,
  with the old names in a comment block; `scripts/check-deploy.sh` checks both lists and refuses a live `deploy/re-psn.env` or
  `deploy/psnr.env`. The compose file, Docker README and proxy examples use the new names.
- The server binary is `re-psn` (`re-psn.exe` on Windows): release archives are `re-psn-vX.Y.Z-<os>-<arch>` (.tar.gz, .zip) with
  `re-psn-*.sha256` files and SHA256SUMS, and the release workflows look for those names. No `psnr` copy is shipped. The image, the
  compose service and the `Setup.cmd` / `setup.sh` launchers (`Start re-psn.cmd`, `start-re-psn.sh`) and Windows firewall rule
  (`re-psn`; the old `psnr` rule is removed) use the new name. `deploy/psnr.service` is now `deploy/re-psn.service`: it starts
  `/usr/local/bin/re-psn` and reads `/etc/psnr/psnr.env`, then `/etc/re-psn/re-psn.env` (both optional, the later one wins); the
  state directory stays `/var/lib/psnr`. `scripts/check-deploy.sh` checks the unit's binary and both env paths. The Go module path,
  the C API, the UDP magic and the wire format are unchanged.
- The architecture and NAT lab diagrams are Mermaid (GitHub renders them), and `docs/architecture.md` gains a diagram of the optional
  reverse proxy path and diagrams of a relayed connection (a VPS example) and of how a relayed stream is set up. The text there that said
  there is no hole punching and no relay is corrected.
- PROXY protocol v2 headers are now checked for family and transport: unassigned values, and a datagram or unspecified transport on the
  TCP game port, close the connection (a LOCAL header still carries no client and is consumed to its declared length). The nginx example
  is labelled v1 (`proxy_protocol on;` sends v1), and `docs/running.md` says Cloudflare Spectrum's PROXY protocol is an Enterprise feature.
- The Dockerfile, compose file and `Dockerfile.dockerignore` moved into `docker/`, with a README. Build from the repository root with
  `docker build -f docker/Dockerfile .`, or run `docker compose -f docker/docker-compose.yml up -d`. `lab/run.sh` and Dependabot follow.
- The Docker image no longer forces `-http 0.0.0.0:36101` without a token: the status page stays on localhost inside the
  container until `PSNR_HTTP` and `PSNR_HTTP_TOKEN` are set (the compose file shows how).
- Protocol additions that Simpsons Arcade's Matching2 use needs. Clients
  built against 0.1.0 no longer match.
  - CREATE_ROOM and JOIN_ROOM carry the member's own data, and every member
    entry returns it.
  - SET_ROOM_DATA `which` 2 sets the room's flags, which searchers see.
  - ROOM_MSG pushes say which member the message was sent to.
- `docker/docker-compose.host.yml`: an opt-in compose file for host networking on Linux, for runtimes that rewrite source addresses. `docker/docker-compose.yml` keeps its ports and defaults and gains a commented `RE_PSN_RELAY` example; `docker/README.md` says what relay is and when to use host networking.
- The README status line is now a row of badges in the gruvbox-material colours, and `docs/status.png` is a new screenshot of the real
  status page (`tools/art/capture-status.sh`; it is not part of `tools/art/make_art.py --check`, a browser screenshot is not reproducible).

### Security
- UDP PROBE and RELAY packets are accepted only from the IP address of the player's TCP connection, so a stolen or guessed
  token, or a forged source address, can no longer point a player's endpoint at a third party. A probe's `local_ip` is kept
  only if it is private, carrier-grade NAT, link-local or the probe's own source. Players whose UDP leaves from another address
  (some multi-WAN or carrier NAT setups) are refused; `-udp-any-source` (`PSNR_UDP_ANY_SOURCE`) turns the check off. See
  `docs/running.md`.

## [0.1.0] - 2026-09-29

### Added
- Server (`server/`), speaking a big-endian TCP protocol with request ids so
  several asynchronous NP calls can be in flight at once.
- Rooms modelled on sceNpMatching2:
  - create, search, join, leave and kick;
  - external and internal room data, and room messages;
  - owner handoff when the owner leaves;
  - member pushes that carry each peer's address and P2P port.
- Leaderboards modelled on sceNpScore:
  - each player's best score kept, ties to whoever scored first;
  - higher-is-better and lower-is-better boards;
  - ranking by range and by online ID.
- A read-only status page and `/api/stats` JSON on a separate localhost
  listener.
- C client (`client/`) with no threads: `psnr_send` returns a request id, and
  `psnr_poll` hands out replies and pushes without blocking.
- Tests:
  - the Go end-to-end tests run over real sockets;
  - `TestCClient` builds the C client test and runs it against the server.
- Docker image and compose file. `Setup.cmd` / `setup.sh` quick start.
