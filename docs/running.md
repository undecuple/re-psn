# Running the server

How to build and run the re-psn server, its flags and environment variables, the UDP source address check, running it
behind a reverse proxy, logging, ports, firewall rules, and what state it keeps.

## Contents

- [Run it](#run-it)
- [Flags](#flags)
- [Status page appearance](#status-page-appearance)
- [UDP source address](#udp-source-address)
- [UDP rate limits and counters](udp-guardrails.md)
- [Behind a reverse proxy](#behind-a-reverse-proxy)
- [Health checks and restarts](restarts.md)
- [Environment variables](#environment-variables)
- [Logging](#logging)
- [Ports](#ports)
- [Firewall](#firewall)
- [Stopping and replacing](#stopping-and-replacing)
- [State, backup, upgrade](#state-backup-upgrade)

## Run it

```sh
cd server
go build -o re-psn .      # re-psn.exe on Windows
./re-psn
```

```text
time=2026-09-29T02:17:35.000+00:00 level=INFO msg=started version=0.1.0 clients=[::]:36100 udp=[::]:36100 relay=false
```

With Docker (see [docker/README.md](../docker/README.md)):

```sh
docker compose -f docker/docker-compose.yml up -d
```

## Flags

| Flag | Default | What it does |
|---|---|---|
| `-addr` | `:36100` | TCP and UDP address for game clients |
| `-tcp-addr`, `-udp-addr` | `-addr` | Give TCP or UDP its own address (a different port, or one interface). Unset, each takes `-addr`. |
| `-http` | none (off) | Status page and `/api/stats` on this address, for example `127.0.0.1:36101`. Unset or empty, neither is served. |
| `-http-token` | none | Require this token on the status page, as `Authorization: Bearer <token>` or `?token=<token>`. Set it whenever `-http` is not on localhost; the server logs a warning if it is not. |
| `-trusted-proxies` | none | Reverse proxies allowed to name the real client: CIDRs or addresses, comma-separated. See [Behind a reverse proxy](#behind-a-reverse-proxy). |
| `-proxy-protocol` | off | Require a PROXY protocol v1 or v2 header on the game TCP port from a trusted proxy. Needs `-trusted-proxies`. |
| `-max-rooms` | `1000` | Room limit across all titles |
| `-max-udp-rate` | `1000` | UDP packets per second per source (IPv4 address or IPv6 /64), including invalid packets, with the same-size burst allowance. Probes and UDP relay traffic share it; 0 disables the limiter, negative values stop startup. Players behind one NAT share one allowance. See [UDP guardrails](udp-guardrails.md). |
| `-discover` | off | Answer LAN discovery broadcasts on the game UDP port, from private-network senders only. See [Finding a server on the LAN](connecting.md#finding-a-server-on-the-lan). With it off, a discovery packet is treated like any other unknown UDP packet. |
| `-admin` | none (off) | **Experimental.** A read-only admin API on its own address, for example `127.0.0.1:36102`. It needs `-admin-read-token-file`; an address that is not loopback also needs `-admin-tls-cert` and `-admin-tls-key`. It must differ from `-http`. See [admin-api.md](admin-api.md). |
| `-admin-read-token-file` | none | File holding the admin read token: one line of at least 32 printable characters, mode 0600 (refused when group or others can read it, except on Windows). Read again on SIGHUP. Must differ from `-http-token`. |
| `-admin-tls-cert`, `-admin-tls-key` | none | Certificate and key for the admin listener (TLS 1.2 or newer, HTTP/1.1 only). Both or neither. |
| `-name` | none | Server name shown in LAN discovery: at most 24 bytes of printable ASCII; anything else stops startup. It is shown only when `-discover` is on. Discovery trusts the packet's source address; leave `-discover` off where a proxy or NAT hides the real sender. |
| `-udp-any-source` | off | Accept UDP probes and relay packets from an address other than the player's TCP address. By default they must match; turn this on only for players whose UDP leaves from another public address (some multi-WAN or carrier NAT setups). It weakens the check, and the server warns at start. See [UDP source address](#udp-source-address). |
| `-relay` | off | Carry traffic between players who can't reach each other directly: streams into a player behind a router, and datagrams when a router won't let punched traffic through. Uses this server's bandwidth. |
| `-relay-force` | off | With `-relay`, send every stream through the relay, even between players on one network or with a public address, so a test can exercise the relay without two networks. Needs `-relay` (the server refuses to start without it) and logs a warning at start. For testing only; it uses this server's bandwidth. Datagrams are not forced: a client still switches a peer to the relay only after it has punched and heard nothing. |
| `-lan-public-addr` | off | An IPv4 address (`RE_PSN_LAN_PUBLIC_ADDR`). For a server on a LAN with players on the internet: a player who connected from a private, carrier-grade NAT (100.64/10) or link-local address is given to players on public addresses as this address and that player's P2P port, instead of an address they cannot reach. The host must forward each such P2P port to the player. Players on private addresses are unaffected. An invalid value stops the server at start; logs the address at start. |
| `-db` | `off` | Score storage: `off` or `memory`, which both keep scores in memory. See [Storage](#storage). |
| `-db-file` | none | Read the `-db` setting from this file instead. Set `-db` or `-db-file`, not both. |
| `-log-level` | `info` | `debug` (room and relay events), `info` (connects, hellos, disconnects), `warn` (refusals and warnings) or `error`. UDP drops use aggregate counters; see [UDP guardrails](udp-guardrails.md#drop-counters). |
| `-log-format` | `text` | `text` or `json`, one line per event. |
| `-log-file` | none | Also append the log to this file (created with mode 0600). |
| `-v` | off | Same as `-log-level debug`. |
| `-replace` | off | If the game port is already in use by an earlier instance of this server, ask it to stop, wait for the port, then start. Linux only; see [Stopping and replacing](#stopping-and-replacing). |

## Status page appearance

With `-http 127.0.0.1:36101`, open `http://127.0.0.1:36101/`. The read-only status page uses the same
gruvbox-material palette and monospace framing as the project website and terminal app. Rooms and Leaderboards
are tabs; Overview shows both tables together. Use the arrow keys while a tab is focused, or click one.
Invite-only rooms remain counted without names.
`/api/stats` keeps its existing JSON fields and token rules, and adds aggregate [`udp_drops` counters](udp-guardrails.md#drop-counters). It also reports the server state (version, relay, limits in force and TCP refusals); see [Server state](api.md#server-state).

The header offers System, Light and Dark; System follows the browser and responds to changes while the page is open.
Appearance offers 14/16/18 px text, comfortable/compact spacing and eight custom colour slots for each light or dark mode.
Enter six-digit hex colours and choose Apply colours. Text, muted, accent and status colours must have at least 4.5:1
contrast on both backgrounds; invalid changes leave the current colours intact. Reset appearance restores the defaults.
Choices stay in this browser's storage for the server origin; they change no server flags or other visitors' settings.
If storage is unavailable, choices work for the current page and a message explains that they cannot be saved.

With JavaScript, status data refreshes every five seconds while the page is visible. Pause refresh keeps a snapshot;
Refresh now still works while paused. A failed refresh retains the previous snapshot and shows an error. Theme, focus
and tabs stay in place during updates. The browser fetches only this server's `/api/stats`; nothing loads from other sites.
Without JavaScript both tables are visible, the system colour scheme applies, and the page reloads every five seconds.
The existing `-http-token` protects both views: browser links may use `?token=...`, which the refresh request preserves
without saving it in appearance settings. Responses disable caching and referrer forwarding.

Assets are embedded in the binary with `embed` and `html/template`; there is no frontend build or runtime dependency.
The HTML response uses hashes for its embedded CSS and script in its Content Security Policy. `go test ./...` checks
line-ending normalization, so CRLF checkouts produce the same rendered assets and hashes. Polling retains exact decimal
text for integers beyond JavaScript's safe range, including signed 64-bit scores; `/api/stats` still returns JSON numbers.
The same suite checks
escaping, authentication and the actual rendered hashes. `python3 -m unittest tools.test_status` checks palette parity;
`node --test tools/status.test.js` checks custom colour validation and stored settings. The optional
`tools/status_browser_check.mjs` uses the existing Chromium DevTools helper against an owned loopback
`TestStatusBrowserFixture` (set `RE_PSN_STATUS_BROWSER_READY` to a fresh ready-file path). This fixture creates a room
and score over real loopback TCP, writes its ephemeral HTTP URL to that file, and exits when the file is removed or after
two minutes. Its token is the public synthetic string `synthetic-status-token`, never a deployment credential.
The check takes a Chromium debug port, that URL with the synthetic token, and an artifact directory. Do not aim it at
a deployed server: it changes browser preferences and tests deliberate refresh failures in its isolated browser profile.

## UDP source address

A UDP PROBE (and a datagram sent through `-relay`) is accepted only from the same IP address as the player's TCP
connection; the token alone is not enough. Without this, anyone holding a player's token, or able to forge a source
address, could point that player's endpoint at a third party and have other players send their game traffic there.

What it does not cover: a forger who can send packets with the player's own source address, or a player sharing that
address (the same household or carrier NAT). It also refuses players whose UDP leaves from a different public address
than their TCP (multi-WAN routers that balance per connection, some carrier NAT): their probes go unanswered, so other
players on other networks get the address TCP shows instead of the router's mapping. The first refused probe per player is
logged at warn level (`UDP probe refused`, with both addresses and the flag that turns the check off). If you see it for real players, `-udp-any-source` turns the
check off. The same check means UDP must reach re-psn with the player's own address: published straight to the host (see
the next section), not through a proxy or port forwarder that rewrites the source. A TCP proxy without `-trusted-proxies`
makes every player look like the proxy, so every probe is refused.

The `local_ip` a probe carries is kept only if it is private (10/8, 172.16/12, 192.168/16), carrier-grade NAT (100.64/10),
link-local (169.254/16), or the probe's own source address. Anything else is stored as none, since it is handed to players
on the same network, who send UDP to it.

## Behind a reverse proxy

The release archives carry an `examples/` folder (in this repository, [deploy/](../deploy/README.md)): `re-psn.env.example` (every setting, commented, with its
default; copy it to `re-psn.env`), `re-psn.service` (a systemd unit, reading `/etc/psnr/psnr.env` then `/etc/re-psn/re-psn.env`) and configs for Traefik, nginx
and HAProxy. A [Caddy status-page example](../deploy/caddy/README.md) is in the v0.4.0 archives and later;
v0.3.0 and earlier archives do not contain it. Only the Traefik one has been run (see below).

Without a proxy nothing changes. With one, the server must learn the real player address, or every player looks like the proxy
(`-max-conns-per-addr` trips for everyone, and the address other players receive is wrong).

- **Game TCP port:** have the proxy send a PROXY protocol v2 (or v1) header and start re-psn with `-trusted-proxies <proxy address>
  -proxy-protocol`. A peer in the list must send a header: one that sends none is closed, so a misconfigured proxy cannot make
  everyone look like one address. A header from a peer that is not in the list is not believed.
- **UDP:** not proxied. Publish UDP straight to the host (the address probes need the player's own source address). A
  player behind your proxy still needs UDP reachable on its own.
- **Status page:** from a listed proxy, `CF-Connecting-IP`, else `X-Forwarded-For` taken from the right past other listed
  proxies, is the address in the log. The token check does not change.
- **The list is trust.** Put only your own proxies in it. A list that includes `0.0.0.0/0` lets any client claim any address; the
  server logs a warning at start if it sees one. Cloudflare's address ranges change, so they are not built in: copy
  Cloudflare's published list into `RE_PSN_TRUSTED_PROXIES` yourself and keep it current.

Traefik, with a TCP router sending PROXY protocol v2 to re-psn (tested with Traefik 3.1 under Podman: the HELLO reply and the log
showed the client's own address, and a forged `X-Forwarded-For` on the status page was not believed):

```yaml
tcp:
  routers:
    game: {entryPoints: [game], rule: "HostSNI(`*`)", service: psnr-tcp}
  services:
    psnr-tcp:
      loadBalancer:
        proxyProtocol: {version: 2}
        servers: [{address: "127.0.0.1:36100"}]
```

with `RE_PSN_TRUSTED_PROXIES=127.0.0.1` and `RE_PSN_PROXY_PROTOCOL=true`. The example in [deploy/traefik/dynamic.yml](../deploy/traefik/dynamic.yml) has
re-psn listen behind Traefik on `127.0.0.1:36200` instead, so both fit on one host.

nginx (`proxy_protocol on;` in a `stream` block, which sends PROXY protocol **v1**) and HAProxy
(`send-proxy-v2` on the server line; a health check to re-psn needs `check-send-proxy` as well) work the same way but have not been run here. re-psn
accepts v1 and v2. A v2 header must describe a TCP stream: a datagram or unassigned family or transport is refused and the connection closed.

The [Caddy example](../deploy/caddy/README.md) uses stock Caddy 2 for HTTP only and starts on loopback. It keeps game TCP
and UDP direct; leave `-proxy-protocol` off for that setup. It removes the client-supplied `CF-Connecting-IP` header
before forwarding and uses the status page's existing token check. It has not been run or validated with Caddy.

> [!NOTE]
> **Cloudflare:** the ordinary proxy carries only HTTP and HTTPS, so it can front the status page but not the game port. Cloudflare Spectrum can send
> PROXY protocol for TCP and UDP, but its documentation lists it as an Enterprise feature, so below that plan Cloudflare is not a way to get real
> player addresses on the game port (check Cloudflare's current plan page).

## Environment variables

Every flag can be set as `RE_PSN_<FLAG>`, upper case, dashes as underscores: `-max-rooms` is `RE_PSN_MAX_ROOMS`, `-tcp-addr`
is `RE_PSN_TCP_ADDR`, `-v` is `RE_PSN_V`. A variable that is set but empty counts as set, so `RE_PSN_HTTP=` keeps the status
page off even when `PSNR_HTTP` is set. A number or true/false that does not parse stops the server at start with a message naming the variable it read.
Booleans take `true` or `false` (also `1`, `0`).

The old names, `PSNR_<FLAG>`, still work. The order is: the flag on the command line, then `RE_PSN_<FLAG>`, then
`PSNR_<FLAG>`, then the default; set-but-empty counts as set for both names. When the server reads an old name it logs a
warning at start naming the old and the new variable, once per variable. When both are set with different values it uses
`RE_PSN_<FLAG>` and logs a warning saying so. These warnings name variables only, never their values. Rename the old
variables when convenient; nothing else changes.

The Docker image has no fixed flags: it is configured only by these variables. The status page is off, and not reachable from
the host, until you set `RE_PSN_HTTP` (for example `0.0.0.0:36101` inside the container) and `RE_PSN_HTTP_TOKEN`; the compose file
shows the lines. A token set this way is visible to `docker inspect` and to other users on the host (process list, for a
native run), so treat the token as protecting a status page on a trusted machine, not as a secret that survives a shared host.
If you change a port, change it in the compose `ports` list as well, and in your firewall rule.

## Logging

The log goes to standard error, and to `-log-file` if set. It holds online IDs and client addresses, because a refusal
is about them; it never holds the status page token, the UDP probe token, the `-db` setting or a request query string. What is logged:

| Level | Events |
|---|---|
| info | server start (and the storage backend), a stop on SIGINT or SIGTERM, an earlier instance replaced (`-replace`), each HELLO (client, title, online ID, address), a reconnect that replaces a connection, each disconnect, a failed read |
| warn | a connection refused (too many from one address), a UDP probe from another address than the player's TCP connection (once per player), a connection closed for its request rate, a HELLO refused (name taken), a room request refused (room limit, blob over limit), an unknown request type, a status page request refused (path only), a score request or status page that the score store failed |
| debug | connects, requests before HELLO, room created, joined and closed, relay streams, UDP probe results |

Rotate the file with logrotate or your service manager (`copytruncate`, or restart after moving it); the server does not rotate it.

For optional host bans after repeated HTTP status-token refusals, see [fail2ban.md](fail2ban.md). Its disabled example
uses JSON journal logs and a separate firewall action; game/UDP bans and live host enforcement are not covered.

## Ports

| Port | Proto | Open to |
|---|---|---|
| 36100 | TCP and UDP | every player (the only port the server needs reachable). UDP carries the players' address probes. |
| 36101 | TCP | you, and only when `-http` is set. It lists every player's online ID and has no auth unless `-http-token` is set. Keep it on localhost or a LAN, not the internet. |
| each player's P2P port (3658 unless `PS3_NET_P2P_PORT` says otherwise) | UDP and TCP | the other players. The game opens this port itself; titles use both (Simpsons Arcade sends its game setup over TCP). |

Players on the same LAN or tailnet need nothing forwarded. Across the internet,
the server's machine must accept TCP and UDP 36100, and each player must accept the
other players' traffic on their P2P port. The server passes out addresses and
relays nothing unless `-relay` is on.

See also [connecting.md](connecting.md): the ways players can reach the server, with Tailscale and ZeroTier.

## Firewall

**The server (TCP and UDP 36100).** On Windows, `Setup.cmd` offers to add the
rules. By hand, from an admin terminal:

```bat
netsh advfirewall firewall add rule name=re-psn dir=in action=allow protocol=TCP localport=36100 program="C:\path\to\re-psn\server\re-psn.exe"
netsh advfirewall firewall add rule name=re-psn dir=in action=allow protocol=UDP localport=36100 program="C:\path\to\re-psn\server\re-psn.exe"
```

On Linux, `setup.sh` prints the commands if a firewall is on:

```sh
sudo ufw allow 36100                                        # ufw: TCP and UDP
sudo firewall-cmd --permanent --add-port=36100/tcp --add-port=36100/udp && sudo firewall-cmd --reload   # firewalld
```

**Each player (the P2P port, UDP and TCP).** On Windows, the first time the
game goes online Windows asks whether to allow it; allow it on the network
type you play on (private, or public too). Or add the rule ahead of time:

```bat
netsh advfirewall firewall add rule name="re-psn game" dir=in action=allow program="C:\path\to\game.exe"
```

On Linux: `sudo ufw allow 3658` (both protocols).

> [!TIP]
> A tailnet needs neither rule for players on it.

## Storage

Scores are kept by a storage backend that `-db` (or `RE_PSN_DB`) picks. Only `off` (the default) and `memory` exist
today, and they are the same thing: scores live in memory and a restart empties every board. A setting that starts
with `sqlite:`, `postgres://`, `postgresql://` or `mysql://` stops the server at start with a message saying that
backend is not built yet; anything else stops it as a bad setting. Because a connection string can hold a password,
`-db-file` (or `RE_PSN_DB_FILE`) reads the setting from a file instead, and the server never prints the setting or the
file's contents: errors name the variable, the scheme and the file's path only. If the score store fails, a score
request gets ERROR 6 and the failure is logged at warn level.

`-max-req-rate` only limits requests per second on each connection; it does not bound the in-memory score store. The
store has its own fixed limits: at most 1000 boards per title and 10000 players per board. A score past either gets
ERROR 6 (see [docs/api.md](api.md#leaderboards)). Across titles the store can still grow, because the number of titles
is not limited.

## Stopping and replacing

SIGINT (Ctrl-C) or SIGTERM stops the server: it logs `stopping`, closes the game ports and the status page and exits 0.
On Windows only Ctrl-C does.

`/healthz` on the opt-in status listener reports whether both game serving loops are active; it uses the same token
check as the status page. Shutdown first marks the server unready, then closes accepted connections as well as the
listeners within a bounded cleanup period. Rooms and scores are still lost on restart. See [Health checks and restarts](restarts.md)
for probe responses, supervisor settings and the limits of this check.

Once the game ports are bound, the server writes a pidfile, `<port>.pid` (the TCP port), mode 0600, in
`$XDG_RUNTIME_DIR/re-psn/`, or in `re-psn-<uid>/` under the temp directory when that variable is not set. The file holds the
pid, the process start time, the executable and the TCP address; a graceful stop removes it. If the directory is not owned
by you, is open to other users or is a symlink, the server logs a warning and writes no pidfile. A file left by a process
that is gone is replaced.

When the port is busy, the server still exits 1, and if the pidfile names a running re-psn of yours the message says so:
`port 36100 is in use by an earlier re-psn (pid 4242, started ...); stop it, or run with -replace`. With `-replace` it sends
that process SIGTERM, waits up to 10 s for the port, and starts. It signals only a process that passes every check: the
pidfile parses, the pid is running, it is yours, its start time matches the file's (so a reused pid is not signalled), and
its program is named like this one or starts with `re-psn`. Anything else, or a port still busy after 10 s, stops the new
server with a message naming what it found. It never sends SIGKILL. The same goes for the UDP port. `-replace` is Linux
only: on Windows and macOS it exits with a message.

The AppImage takes the same flag. Started twice from a file manager, the first copy keeps running without a terminal and
the second cannot bind; `./re-psn-vX.Y.Z-x86_64.AppImage -replace` stops the first and takes its place.

## State, backup, upgrade

- **State:** everything lives in memory. Restarting the server closes every room
  and empties every leaderboard.
- **Backup:** there is nothing to back up.
- **Upgrade:** stop the server, rebuild it, start it again. Titles reconnect when
  they next go online.
