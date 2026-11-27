# Running a server that the internet can reach

Recommended settings for a re-psn server that is open to the internet, not only to a LAN, a VPN or friends you know. Read
[SECURITY.md](../SECURITY.md) first: re-psn is written for people you would let onto your own network, and this page does
not change that. It lists what to set, what to leave off and what stays your problem when anyone can connect. The flags
are in [running.md](running.md); this page only recommends values.

re-psn has no public instance, and nothing here says one exists. This is for a host who chooses to expose their own server.

## What you are taking on

A public server is open to everyone who finds the address. These are the limits to accept before you start:

- **There is no authentication.** Anyone who can reach TCP 36100 can connect and claim any online ID. A name can be taken
  over by a connection from the same address. Scores come from whatever the client sends; nothing proves them.
- **Players' addresses go to the other players in a room.** Every member learns every other member's IP address and P2P port,
  because they need it to connect. Say so in whatever rules you publish for your server.
- **The server does not stop a flood.** It limits connections, requests and UDP packets per source, and that is all. Those
  limits control memory and work, and are not DDoS protection ([UDP guardrails](udp-guardrails.md)). A saturated link needs
  protection from your network or provider.
- **There is no TLS and no encryption on the game port.** Tokens and names cross it in the clear.
- **Rooms and scores are in memory.** A restart closes every room and empties every board.
- **There is no moderation tool.** The admin API is read-only. You can restart the server, firewall an address, or set limits;
  you cannot kick or ban a player from the server.
- **You are the operator.** Your logs hold online IDs and addresses. Decide what you keep and for how long, and who you tell.

## The short version

For a typical public host: leave the status page off or on localhost with a token, keep every default limit, leave the relay off
unless you have bandwidth to give, publish only 36100 (TCP and UDP), run as an unprivileged user, and keep the logs.

```sh
# /etc/re-psn/re-psn.env  (systemd; or use --env-file with Docker). Everything not listed keeps its default.
RE_PSN_ADDR=:36100
RE_PSN_HTTP=127.0.0.1:36101
RE_PSN_HTTP_TOKEN=<a long random string>
RE_PSN_LOG_FORMAT=json
RE_PSN_LOG_FILE=/var/lib/psnr/re-psn.log
# RE_PSN_RELAY=true            # only if you accept the bandwidth, see "The relay" below
```

## Settings

| Setting | Recommended | Why |
|---|---|---|
| `-addr` | `:36100` (or an address on one interface) | The only port players need. TCP and UDP both. |
| `-http` | off, or `127.0.0.1:36101` | The status page lists every online ID. Never bind it to a public address. |
| `-http-token` | set it whenever `-http` is on | Even on localhost it keeps another local user out. Set it whenever you bind a non-local address; the server only warns when it is missing, it does not refuse to start. |
| `-admin` | off | Experimental and read-only. If you use it, keep it on loopback with `-admin-read-token-file` (a different token from the status page); a non-loopback address needs TLS. |
| `-max-conns` | keep `4096`, or lower it to what your machine can carry | Counts players and relay legs together. Keep the file-descriptor limit above twice this value (the systemd unit sets 16384). |
| `-max-conns-per-addr` | keep `32` | Households and mobile carriers put many players behind one address; a low value locks them out. Behind a proxy without PROXY protocol every player looks like the proxy and this trips for everyone, so set up the proxy as described below. |
| `-max-req-rate` | keep `200` | A connection that sends more requests per second is closed. |
| `-max-udp-rate` | keep `1000`, never `0` | Per source address, shared by probes and relayed datagrams, with a one-second burst. Raise it only if the drop counters show real players being limited behind one NAT. |
| `-max-rooms`, `-max-player-rooms` | keep `1000` and `8`, or lower `-max-rooms` | Memory grows with rooms. Lower `-max-rooms` if you want a smaller server. |
| `-relay` | off unless you can give the bandwidth | See below. |
| `-relay-idle` | keep `600` | Closes a relay stream with no bytes either way. A stream that keeps moving has no byte cap. |
| `-relay-force` | off | A test setting. |
| `-no-peer-titles` | list titles that use no peer traffic | Those titles get no UDP probe and no relay, so nobody can use your server to carry their traffic. |
| `-udp-any-source` | off | It turns off the check that a UDP probe or relayed datagram comes from the player's own address, and weakens spoof protection. Turn it on only for a known multi-WAN or carrier NAT problem. |
| `-discover` | off | LAN discovery has no use on the internet. |
| `-medius` | off | No Medius protocol is implemented. |
| `-log-level` | `info` | Connects, hellos and disconnects. `warn` loses them; `debug` adds room and relay events and is for troubleshooting. |
| `-log-format` | `json` | One line per event, easy for tools and for fail2ban. |
| `-log-file` | set, and rotate it | The server does not rotate. Use logrotate with `copytruncate`, or restart after moving it. It is created with mode 0600. |
| `-trusted-proxies`, `-proxy-protocol` | only with a proxy you run | List only your own proxies. A list such as `0.0.0.0/0` lets any client claim any address. |

### The relay

`-relay` carries traffic between players who cannot reach each other directly. It is off by default and costs the host's
bandwidth. On a public server, think before you turn it on:

- **No flag limits relay bandwidth.** The packet limit counts datagrams, not bytes, and a TCP stream that keeps moving has no
  cap. Limit it outside the process (your provider's bandwidth cap, or traffic shaping on the host) and watch `relay_bytes`
  in `/api/stats`.
- **Anyone who joins a room can use it** with the other room members.
- **Titles that do not need it** can be excluded with `-no-peer-titles`.
- It carries any payload that fits one UDP datagram; see [Real-time titles such as rollback](api.md#real-time-titles-such-as-rollback).

Without the relay, players who cannot punch through their routers cannot connect to each other. That is a reasonable choice
for a server you cannot afford to carry traffic for.

## Network

- **Publish only TCP and UDP 36100.** The status page, the admin API and the Medius port are not for the internet. Each
  player's P2P port is on the players' machines, not yours.
- **UDP must arrive with the player's own source address.** Publish UDP straight to the host. A proxy or port forwarder that
  rewrites the source makes every probe refused. With Docker, if players all appear to come from the host or gateway, use
  [`docker-compose.host.yml`](../docker/docker-compose.host.yml) (Linux, host networking) so the container sees real addresses.
- **A reverse proxy fronts the TCP port only**, with PROXY protocol, and only if you need one; UDP is not proxied. The
  proxy examples are in [deploy/](../deploy/README.md); only the Traefik one has been run.
- **The status page can be fronted by a proxy** if you want it reachable: keep the token on, and keep it off the public
  game port. Cloudflare's ordinary proxy cannot carry the game port.

## Run it as an unprivileged service

- **systemd:** [`deploy/re-psn.service`](../deploy/re-psn.service) already uses `DynamicUser`, `NoNewPrivileges`,
  `ProtectSystem=strict`, `PrivateTmp` and more, and `LimitNOFILE=16384`. Keep them.
- **Docker or Podman:** the compose files run as user 10001 with `cap_drop: [ALL]`, `no-new-privileges` and a read-only root
  file system. A log file needs a volume. The game port is above 1024, so no capability is needed.
- **Firewall:** allow 36100/tcp and 36100/udp, and nothing else inbound for re-psn. Commands are in
  [running.md](running.md#firewall).
- **Do not run it as root.**

## Watch it

- `/api/stats` (needs `-http` and the token) reports the version, the limits in force, `relay_bytes`, TCP refusals and the
  `udp_drops` counters (`rate_limit`, `source_capacity`, `malformed`, `unauthorized`, `source_mismatch`, `relay_disabled`, `write_error`, `discover_refused`, `relay_no_pass`; see [udp-guardrails.md](udp-guardrails.md#drop-counters)). A steady rise in
  `rate_limit` from real players behind one NAT means the UDP allowance is too low; a spike of `malformed` or `unauthorized`
  from nowhere you know is scanning.
- `GET /healthz` on the status listener, with the token, reports whether both serving loops are active; use it with a
  supervisor ([restarts.md](restarts.md)). It is a liveness check, not a test that games work.
- **Warn lines** in the log include connection refusals, request-rate closes, taken names and UDP probes from a different
  address. Refused passes are logged when pass settings are enabled. [Logging](running.md#logging) lists which warnings
  are limited and which counters exist.

### Handling abuse

- **Game TCP** defaults to 32 open connections per IPv4 address or IPv6 /64 and 4096 in total, including relay legs.
  A game control connection closes when it exceeds 200 requests in a one-second window. In connect-pass mode, unsigned
  connections also have separate caps (four per source, 64 total). Pass authentication defaults to two connections per account.
- **Game UDP** defaults to 1000 datagrams per second per IPv4 address or IPv6 /64, with a burst of 1000. Excess datagrams
  are dropped. Players sharing a source address share its allowance. [UDP guardrails](udp-guardrails.md) has the settings and counters.
- **Invite-code checks** close the offending connection after five wrong codes on it or 20 from its source in a minute. A room
  owner can kick a member, who can rejoin if the normal join checks pass (in an invite-only room, with the code).
- **Admin**, if enabled, accepts up to eight open connections per source and 32 in total. Repeated refused requests from
  a source increase the delay before the error response, up to five seconds.
- **Operator actions** include lowering the [connection and rate limits](#settings) or blocking a verified source at the
  firewall or proxy that sees it (game and UDP bans are not covered by the fail2ban example). An operator can add an account to the configured `-pass-revoked-file` and
  reload the settings. The optional [fail2ban example](fail2ban.md) starts disabled and covers repeated HTTP status-token refusals.

## Docker Compose

[`docker/docker-compose.public.yml`](../docker/docker-compose.public.yml) applies these settings to a container: non-root, no
capabilities, a read-only root, limits, a health check, rotated JSON logs, the status page on localhost with a token, and
the relay, discovery and Medius off. [public-compose.md](public-compose.md) says what each setting is for and what the file
does not do.

## Upgrades and restarts

Rooms and scores live in memory, so every restart disconnects everyone and empties the boards. Pick a quiet time, stop the
server (SIGTERM closes the listeners and exits), start the new version, and players reconnect when their game next goes online.
There is nothing to back up. Check the [changelog](../CHANGELOG.md) for protocol changes before you upgrade.

## What to tell your players

- That the server is unofficial fan work and is not affiliated with Sony, and that they must bring their own game.
- That other players in a room see their IP address.
- That names are not protected: anyone can use any name that is not currently connected.
- What you log and how long you keep it.
- What you do when someone abuses the server: for example, that you block addresses at the firewall.

## Not checked

This page is written from the flags, the protocol, the security notes and the example files. It has not been checked against
a server on the open internet, under attack, or under a real relay load. The default limits are an initial choice, not a
measured capacity ([udp-guardrails.md](udp-guardrails.md)). The Windows server binary is cross-built and has not been run by
us; its tests run on Windows in CI.
