# Admin API (experimental, read-only)

> [!WARNING]
> Experimental. It is off unless you start the server with `-admin`, it can only read, and its format may change without
> notice before it is released. Nothing here can change the server.

The admin API is a second HTTP listener, separate from the [status page](running.md), with one route and one token. It is
meant for a tool such as the desktop app's Server tab to read the server's state from a machine that is not the server.
Actions (closing a room, kicking a player, banning an address) are planned as separate, later changes behind their own
setting; this page describes only what exists.

## Turning it on

```sh
re-psn -admin 127.0.0.1:36102 -admin-read-token-file /etc/re-psn/admin-read.token
```

| Setting | Meaning |
|---|---|
| `-admin ADDR` (`RE_PSN_ADMIN`) | The address to listen on. Off when empty. It must not be the same as `-http`. |
| `-admin-read-token-file FILE` (`RE_PSN_ADMIN_READ_TOKEN_FILE`) | The read token. Required: no token, no admin listener. |
| `-admin-tls-cert`, `-admin-tls-key` | A certificate and key. Required when `ADDR` is not loopback. TLS 1.2 or newer, HTTP/1.1 only. |

The server refuses to start, and says which rule, when: the address is not a loopback IP and there is no certificate (an
empty host, `0.0.0.0` and `::` are not loopback); the address is the status page's; only one of the certificate and key is
given; the token is not acceptable; or the token is the same as `-http-token`. The start log says
`admin API is experimental` with the address and whether TLS is on, and never the token.

## The token

The file holds one line: at least 32 printable characters, no spaces, at least 12 different ones. Make one with
`head -c 32 /dev/urandom | base64 | tr -d '=\n' > admin-read.token`, then `chmod 600` it. The server refuses a file that
group or others can read. That check uses Unix file modes: on Windows it is skipped, and on a container secret mount the
mode may show as 0644, so mount the file with mode 0400 (or the server will refuse it). The token is read from the file
only, never from the command line.

Send it as `Authorization: Bearer <token>`. A token in the query string is ignored. Replace the file's content and send the
server `SIGHUP` to rotate the token without a restart: the old token stops working at once. If the new file is not valid
(for example half written), the old token stays in force and the log says so.

## What you can read

`GET /admin/v1/state` answers JSON, schema 1. The fields are fixed; a later version adds fields and keeps these:

```json
{"schema":1,"version":"0.5.0","uptime":"2h3m0s","relay":false,"relay_bytes":0,
 "limits":{"max_rooms":0,"max_player_rooms":8,"max_conns_per_addr":32,"max_req_rate":200,"max_udp_rate":1000},
 "tcp_refusals":{"conns_per_addr":0,"req_rate":0},"udp_drops":{"...":0},
 "bans":{"count":0,"next_expiry_seconds":null},"actions_enabled":false}
```

It holds aggregates only: no address, no player name, no token. `bans.count` is 0 until bans exist. `actions_enabled`
tells a tool whether this server offers actions; it is `false` today. The public `/api/stats` does not say the admin API
exists.

## How it refuses

- A request without the right token gets `401` with the same body whether the token was missing or wrong, and, so
  that routes are not revealed, the same `401` for any path. The read token on a request that is not `GET` or `HEAD`
  gets `403`.
- A failure costs the client a growing delay (100 ms, doubling, up to 5 s) before the answer, keyed by IPv4 address or IPv6
  `/64`, in a table of fixed size. It never blocks a request that carries the right token.
- Each refusal writes one log line with the socket's remote address and a fixed reason, and nothing the client wrote:

  ```
  time=... level=WARN msg="admin refused" addr=192.0.2.7 reason="token missing or wrong"
  ```

  This is the line to match with [fail2ban](fail2ban.md); the address is never taken from a header.
- A request with an `Origin` header is refused with `403` even with the right token, and over plain HTTP the `Host` header
  must be an IP address or `localhost` (this stops a web page from reaching the API through a rebound name). No CORS header is sent.
- Limits: 32 concurrent connections, 5 s to send headers, 10 s to read or write, 8 KiB of headers, 4 KiB of body.

## Reaching it safely

Keep the admin port off the public interface: bind it to loopback and reach it through an SSH forward or a WireGuard
tunnel, or give it a certificate and put a firewall in front. A token is a password for the server's state; use a
different one from the status page's. Over an SSH forward every request arrives from one address, which is why a failure only
costs a delay and never a block.

## Not there yet

Actions (close a room, kick a member, ban an address), the ban list, the action token, and the desktop app's Admin view.
Each is a separate change and will say so here when it ships.
